// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"container/heap"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// dueIndexRetiredRecheckSeconds bounds how long a retired Query Group is left
// alone. Retirement is not final: a later publication can revoke it through a
// ReactivatedAfter tombstone, so a retired bound cannot be infinite or the
// revocation would never be acted on. A publication normally drops the bound
// long before this expires; the recheck is what covers the case where it does
// not.
//
// It is a semantic constant rather than a setting. Nobody operating the
// deployment knows better than the program how long to wait before asking
// again, and the only thing the value changes is how quickly a revoked
// retirement resumes.
const dueIndexRetiredRecheckSeconds = 60

// OverdueWake is one Query Group whose wake time has passed, for a reader that
// has to show which objects are stuck and for how long.
//
// The reading is reported as it stands: the wake that was written and the
// evaluation interval it belongs to. How late is too late is not decided here.
// A fixed threshold is blind to cadence - sixty seconds late is nothing to an
// hourly Plan and six missed evaluations to a ten-second one - so the interval
// travels with the wake and the reader scales the judgement by it.
//
// An alias rather than a second declaration of the same three fields. The
// reader this was written for is fleet.OverdueWakeSource, and two structurally
// identical types kept in step by hand is the shape that drifts: a field added
// on one side compiles fine and silently stops travelling. Aliased, the index
// satisfies that interface directly and the compiler keeps them identical.
type OverdueWake = fleet.OverdueWake

// phaseTwoDueIndex holds, per owned Query Group, the second before which asking
// the Slot source cannot produce work.
//
// It is a cache, not a ledger. Redis already holds the authoritative facts -
// the Progress cursor, the schedule timeline, the Assignment - and every entry
// here is derived from a call this replica already made. Nothing is persisted
// and nothing is shared, so clearing it at any moment costs extra calls and
// nothing else: the degraded ceiling is the behaviour of a deployment with no
// index at all.
//
// One invariant carries the whole thing:
//
//	a bound is never later than the second at which the source would first
//	return a due Slot.
//
// An early bound costs one wasted call. A late one holds back a Slot that is
// already due, which is a correctness defect, so every derivation rounds toward
// the past and every event that could pull a Slot forward drops the bound
// rather than adjusting it.
//
// Two things follow structurally and are worth stating because they are what
// makes the index safe to deploy:
//
// Backlog is never held back. A Query Group whose cursor is in the past -
// replay, expired range, gap, a Snapshot that will not load, a restored
// unfinished range - takes the branch in Next that resolves a Slot, so the call
// establishes no future bound at all and the entry stays due now. Recovery work
// is outside the index's reach by construction, not by a rule it has to follow.
//
// An entry's absence has exactly one meaning: nothing has been evaluated for
// that Query Group since this replica took it over. Entries are rewritten when
// a round returns and never removed when one is dispatched, so a Runner that
// was handed out and never came back keeps its old bound and falls behind the
// wall clock, where a scheme that cleared on dispatch would leave nothing to
// see.
type phaseTwoDueIndex struct {
	mu       sync.Mutex
	recorder *metric.Recorder
	entries  map[execution.QueryGroupIdentity]*phaseTwoDueEntry
	pending  dueEntryHeap
	// skips is the count this replica states about itself in its own snapshot,
	// alongside the metric of the same name. The page reads the snapshot rather
	// than the series because the only path it has to a series can answer
	// "absent" for a series that exists, and here that failure would read as
	// "suppression is not running".
	skips *fleet.DispatchSkipTally
	// versionTag is the activation header the bounds are anchored to.
	// versionEpoch counts the times that anchor moved, and it is what a round in
	// flight is stamped with: a publication that lands while a round is running
	// would otherwise let the round write a bound taken under the older
	// publication and have it survive the sweep that was supposed to drop it.
	versionTag   string
	versionSeen  bool
	versionEpoch uint64
	// completions is the last six hours of returned rounds, one bucket per
	// minute, so the census can say how many finished before their next turn
	// fell due. A ring rather than a list: the cost is fixed whatever the
	// deployment's rate, and six hours is the longer of the two windows the
	// page reads.
	completions [dueCompletionBuckets]dueCompletionBucket
	// overdue is the last hour of the census's own overdue count, one sample
	// per minute, so the next census can say whether the backlog grew. The
	// count alone is an instant; "is it growing" needs the same count an
	// hour ago, and nothing else keeps it.
	overdue [dueOverdueSamples]dueOverdueSample
}

// dueCompletionBuckets is six hours of one-minute buckets.
const dueCompletionBuckets = 6 * 60

// dueOverdueSamples is one hour of one-minute samples of the overdue count.
const dueOverdueSamples = 60

// dueOverdueSample is the overdue count the census found in one minute; the
// last census of that minute wins, and a sample that has wrapped from an
// hour ago is recognised by its minute and not read as this hour's.
type dueOverdueSample struct {
	minute  int64
	overdue int
}

// dueCompletionBucket is what one minute of returned rounds adds up to.
// minute is the Unix minute the bucket describes, so a bucket that has wrapped
// around from six hours ago is recognised and reset rather than added to.
type dueCompletionBucket struct {
	minute         int64
	completed      int
	onTime         int
	heldBack       int
	heldBackOnTime int
}

type phaseTwoDueEntry struct {
	queryGroup      execution.QueryGroupIdentity
	lifecycle       *phaseTwoQueryGroupLifecycle
	dueAtUnix       int64
	intervalSeconds int64
	// deferred marks a bound that came from the Runner's own backoff rather than
	// from the schedule. It decides two separate things. A publication can only
	// pull a schedule bound forward, never a backoff one, so only schedule
	// bounds are swept. And a matured backoff is recovery work: it belongs in
	// the delayed queue that alternates with the ready queue, not in the ready
	// queue, or backing off would become a way to jump the recovery rotation.
	deferred      bool
	queryCooldown bool
	// heldBack marks that the dispatcher pushed this object back at least once
	// since its last round returned: no room in the ready queue, or not urgent
	// enough for the recovery queue. Cleared when the next round returns, and
	// read at that moment to say whether being pushed back cost the deadline.
	heldBack bool
	position int
}

func newPhaseTwoDueIndex(recorder *metric.Recorder) *phaseTwoDueIndex {
	return &phaseTwoDueIndex{
		recorder: recorder,
		entries:  make(map[execution.QueryGroupIdentity]*phaseTwoDueEntry),
		skips:    fleet.NewDispatchSkipTally(),
	}
}

// RecordSkip counts one dispatch this index held back, in both places at once:
// the metric a direct scrape reads during acceptance, and the tally the replica
// states in its own snapshot.
//
// It is called from the line that makes the decision rather than reconstructed
// afterwards. A count assembled later is a bit somebody has to remember to set
// on every path that grows into the decision, and the paths that forget it are
// exactly the ones nobody thought about.
func (index *phaseTwoDueIndex) RecordSkip(recorder *metric.Recorder, deferred bool, queryGroup execution.QueryGroupIdentity) {
	if index != nil && !deferred {
		index.mu.Lock()
		entry := index.entries[queryGroup]
		cooldown := entry != nil && entry.queryCooldown
		index.mu.Unlock()
		if cooldown {
			recorder.RecordDispatchSkipped("query_cooldown")
			index.skips.SkippedQueryCooldown()
			return
		}
	}
	reason := "not_due"
	if deferred {
		reason = "backoff"
	}
	recorder.RecordDispatchSkipped(reason)
	if index == nil {
		return
	}
	if deferred {
		index.skips.SkippedOnBackoff()
		return
	}
	index.skips.SkippedNotDue()
}

// SuppressionFacts is what this replica states about dispatch suppression.
//
// It never returns nil. The presence of the field is the answer to "is
// suppression running in this build", so a source that could decline to fill it
// in would be putting that answer back into doubt.
func (index *phaseTwoDueIndex) SuppressionFacts(now time.Time) *fleet.DispatchSuppression {
	facts := &fleet.DispatchSuppression{}
	if index == nil {
		facts.Skipped = (*fleet.DispatchSkipTally)(nil).Counts()
		return facts
	}
	facts.Skipped = index.skips.Counts()
	index.mu.Lock()
	defer index.mu.Unlock()
	nowUnix := now.Unix()
	for _, entry := range index.pending {
		if entry.dueAtUnix > nowUnix {
			facts.Parked++
		}
	}
	return facts
}

// Len reports how many bounds are held.
func (index *phaseTwoDueIndex) Len() int {
	if index == nil {
		return 0
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	return len(index.entries)
}

// Clear drops every bound. It is safe at any moment and from any state, which
// is the point: a replica that has just started, one that has just recovered
// from a panic, one whose invalidation logic is itself suspect, can all take
// this and be no worse off than a replica with no index.
func (index *phaseTwoDueIndex) Clear() {
	if index == nil {
		return
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	index.entries = make(map[execution.QueryGroupIdentity]*phaseTwoDueEntry)
	index.pending = nil
}

// Predict answers whether this Query Group is worth dispatching now, why not
// when it is not, and the version epoch the round is to be stamped with.
//
// No entry, or an entry belonging to a lifecycle this is not, means due: a
// Query Group this replica has just taken over has never been evaluated here,
// and the only honest answer is to go and find out.
// heldFor is how much longer this bound would keep the Query Group back, and it
// is the whole discriminator when the round then finds the object due after all.
//
// The index holding back an object that is already due is the failure its own
// file calls a correctness defect, and the count of those alone cannot separate
// it from the clock simply crossing the boundary between the prediction and the
// verdict -- two things that need opposite responses and produce the same
// tally. How far the bound reaches past the moment it was consulted is what
// separates them: shorter than one walk over the owned set and the boundary was
// crossed in flight; much longer and the bound is late by that much.
//
// Zero when the object is due, because there is nothing being held back.
func (index *phaseTwoDueIndex) Predict(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	now time.Time,
) (due bool, deferred bool, epoch uint64, heldFor time.Duration) {
	if index == nil {
		return true, false, 0, 0
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	entry, ok := index.entries[queryGroup]
	if !ok || entry.lifecycle != lifecycle {
		return true, false, index.versionEpoch, 0
	}
	if entry.dueAtUnix <= now.Unix() {
		return true, entry.deferred, index.versionEpoch, 0
	}
	// Seconds, because that is the resolution the comparison above is made at:
	// the bound is stored as a whole second and compared against a whole second,
	// so anything finer here would be precision this decision does not have.
	return false, entry.deferred, index.versionEpoch,
		time.Duration(entry.dueAtUnix-now.Unix()) * time.Second
}

// Record rewrites the bound for one Query Group from the round that has just
// returned. Every return writes, including the returns that establish no bound
// at all: those write "due now", which is the same answer as no entry and costs
// one call to confirm.
func (index *phaseTwoDueIndex) Record(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	epoch uint64,
	bound scheduler.RunnerDueBound,
	now time.Time,
) {
	if index == nil || queryGroup == "" || lifecycle == nil {
		return
	}
	index.mu.Lock()
	nowUnix := now.Unix()
	existing, ok := index.entries[queryGroup]
	if ok && existing.lifecycle != lifecycle {
		// A round for the new owner returned before the owned set was walked
		// again, so the previous owner's entry is still here. It has to leave the
		// heap as well as the map: dropping only the map reference would leave an
		// entry nothing can reach, which no later pass would ever remove.
		heap.Remove(&index.pending, existing.position)
		ok = false
	}
	dueAt, interval, trigger := bound.NotDueUntilUnix, bound.IntervalSeconds, "attempt"
	if bound.Retired {
		dueAt = nowUnix + dueIndexRetiredRecheckSeconds
	}
	switch {
	case !bound.Deferred && epoch != index.versionEpoch:
		// A publication landed while this round was running, so what it read is
		// already superseded. Dropping the bound is the same action the sweep
		// would have taken had the round finished before it.
		dueAt, trigger = nowUnix, "version_change"
	case !ok:
		trigger = "absent"
	case bound.Deferred:
		trigger = "deferral"
	case bound.Retired:
		trigger = "retired_ttl"
	case bound.Executed:
		trigger = "execute"
	}
	if dueAt < nowUnix {
		dueAt = nowUnix
	}
	if interval == 0 && ok {
		// A round that could not read the schedule keeps the last interval that
		// was read. Zero survives only for a Query Group whose schedule has never
		// been read at all, and that is a real state worth being able to see.
		interval = existing.intervalSeconds
	}
	if ok && bound.Executed {
		// The round that just returned fell due at the bound the previous
		// round wrote, and it is on time if it returned before the next one
		// falls due: one period after. Measured here because this is the one
		// place both moments are in hand. Only executed rounds count -- a
		// return that ran nothing is not a completion -- and only rounds whose
		// period is known can be judged, so the rest are not in the
		// denominator either.
		index.recordCompletionLocked(nowUnix, existing.dueAtUnix, interval, existing.heldBack)
	}
	if ok {
		existing.dueAtUnix, existing.intervalSeconds = dueAt, interval
		existing.deferred = bound.Deferred
		existing.queryCooldown = bound.QueryCooldown
		existing.heldBack = false
		heap.Fix(&index.pending, existing.position)
	} else {
		entry := &phaseTwoDueEntry{
			queryGroup: queryGroup, lifecycle: lifecycle, dueAtUnix: dueAt,
			intervalSeconds: interval, deferred: bound.Deferred, queryCooldown: bound.QueryCooldown,
		}
		index.entries[queryGroup] = entry
		heap.Push(&index.pending, entry)
	}
	recorder, horizon := index.recorder, float64(dueAt-nowUnix)
	index.mu.Unlock()

	recorder.RecordDueIndexRecomputed(trigger, 1)
	recorder.ObserveDueIndexHorizon(horizon)
}

// ObserveControlVersion compares the activation header against the one the
// bounds are anchored to and drops every schedule bound that is no longer
// anchored to it.
//
// A publication is the only event that can make a Query Group due earlier than
// its bound says. Everything else - the cursor moving on, a retirement, a
// readiness deferral, a retry backoff - can only push it later, because the
// Progress cursor only ever moves forward. That is why one global header read
// per tick is enough, and why it is read once for the whole replica rather than
// once per Query Group.
//
// A header that cannot be read fails open: every schedule bound is dropped
// without waiting to find out what changed. The cost is a tick that behaves
// like a deployment with no index, which is a load this deployment is already
// known to carry. The alternative is holding Slots back on bounds that may have
// been superseded, and a late Slot past the replay window becomes a real gap.
func (index *phaseTwoDueIndex) ObserveControlVersion(tag string, known bool, now time.Time) {
	if index == nil {
		return
	}
	index.mu.Lock()
	result, trigger := "unchanged", ""
	switch {
	case !known:
		result, trigger = "unknown", "version_unknown"
	case !index.versionSeen || tag != index.versionTag:
		// The first reading counts as a change. It is one: the bounds written
		// before it were anchored to nothing.
		index.versionTag, index.versionSeen = tag, true
		result, trigger = "changed", "version_change"
	}
	dropped := 0
	if trigger != "" {
		index.versionEpoch++
		dropped = index.expireScheduleBoundsLocked(now.Unix())
	}
	recorder := index.recorder
	index.mu.Unlock()

	recorder.RecordDueIndexVersionCheck(result)
	if trigger != "" {
		recorder.RecordDueIndexRecomputed(trigger, dropped)
	}
}

// expireScheduleBoundsLocked pulls every schedule bound to now. Deferred bounds
// are left alone: they describe this Runner's own backoff, which no publication
// can shorten.
func (index *phaseTwoDueIndex) expireScheduleBoundsLocked(nowUnix int64) int {
	dropped := 0
	for _, entry := range index.pending {
		if entry.deferred || entry.dueAtUnix <= nowUnix {
			continue
		}
		entry.dueAtUnix = nowUnix
		dropped++
	}
	if dropped > 0 {
		heap.Init(&index.pending)
	}
	return dropped
}

// DropLostLifecycles removes the entries whose Query Group is no longer owned
// by the lifecycle they were written for.
//
// The identity test is the dispatcher's own: a Query Group taken over again
// gets a new lifecycle, and an entry keyed on the name alone would let the new
// owner inherit the previous one's bound. Inheriting it would be the one shape
// of staleness the version sweep cannot catch, because the bound may well still
// be anchored to the current publication.
func (index *phaseTwoDueIndex) DropLostLifecycles(
	current func(execution.QueryGroupIdentity, *phaseTwoQueryGroupLifecycle) bool,
) {
	if index == nil || current == nil {
		return
	}
	// The ownership question is answered outside this lock. Answering it inside
	// would mean holding the index while taking the bundle's lock, and the index
	// is also read by whoever publishes this replica's object facts; one ordering
	// hazard is not worth introducing for a cache. An entry that stops being
	// current between the two halves is caught on the next owned-set change,
	// which is the only thing that can make it stale in the first place.
	index.mu.Lock()
	candidates := make([]*phaseTwoDueEntry, len(index.pending))
	copy(candidates, index.pending)
	index.mu.Unlock()

	lost := candidates[:0]
	for _, entry := range candidates {
		if !current(entry.queryGroup, entry.lifecycle) {
			lost = append(lost, entry)
		}
	}
	if len(lost) == 0 {
		return
	}

	index.mu.Lock()
	dropped := 0
	for _, entry := range lost {
		if index.entries[entry.queryGroup] != entry {
			continue
		}
		delete(index.entries, entry.queryGroup)
		heap.Remove(&index.pending, entry.position)
		dropped++
	}
	recorder := index.recorder
	index.mu.Unlock()

	recorder.RecordDueIndexRecomputed("ownership", dropped)
}

// OverdueWakes reports the Query Groups whose wake time has passed, oldest
// first, with the true count before truncation.
//
// In a healthy replica this is short: a wake that arrives is dispatched and
// rewritten. What it exists to surface is the opposite - an object whose wake
// passed and which is not coming back - and a caller that only asked for a
// count could not tell which objects those were or how long they had been that
// way.
func (index *phaseTwoDueIndex) OverdueWakes(now time.Time, limit int) ([]OverdueWake, int) {
	if index == nil || limit <= 0 {
		return nil, 0
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	nowUnix, total := now.Unix(), 0
	oldest := make([]*phaseTwoDueEntry, 0, limit)
	for _, entry := range index.pending {
		if entry.dueAtUnix > nowUnix {
			continue
		}
		total++
		// Bounded insertion rather than sorting the whole overdue set: after a
		// fail-open tick every entry is overdue, and that is precisely the moment
		// this must not become the expensive call.
		if len(oldest) == limit && !dueEntryBefore(entry, oldest[len(oldest)-1]) {
			continue
		}
		position := len(oldest)
		if position == limit {
			position--
		} else {
			oldest = append(oldest, nil)
		}
		for position > 0 && dueEntryBefore(entry, oldest[position-1]) {
			oldest[position] = oldest[position-1]
			position--
		}
		oldest[position] = entry
	}
	wakes := make([]OverdueWake, 0, len(oldest))
	for _, entry := range oldest {
		wakes = append(wakes, OverdueWake{
			QueryGroup:      string(entry.queryGroup),
			WakeAt:          time.Unix(entry.dueAtUnix, 0),
			IntervalSeconds: entry.intervalSeconds,
		})
	}
	return wakes, total
}

// recordCompletionLocked adds one returned round to the minute it returned in.
// Caller holds the lock.
func (index *phaseTwoDueIndex) recordCompletionLocked(nowUnix, dueAtUnix, intervalSeconds int64, heldBack bool) {
	if intervalSeconds <= 0 {
		return
	}
	minute := nowUnix / 60
	bucket := &index.completions[dueBucketSlot(minute)]
	if bucket.minute != minute {
		*bucket = dueCompletionBucket{minute: minute}
	}
	// A publication that pulled the bound to now while the round was running
	// leaves the round returning before it was ever due by the entry's
	// reckoning; that is on time.
	onTime := nowUnix-dueAtUnix <= intervalSeconds
	bucket.completed++
	if onTime {
		bucket.onTime++
	}
	if heldBack {
		bucket.heldBack++
		if onTime {
			bucket.heldBackOnTime++
		}
	}
}

// MarkHeldBack records that the dispatcher pushed this object back rather than
// queueing it. Called from the line that makes the decision, like RecordSkip,
// so no path that grows into the decision can forget it. An object with no
// entry has nothing to mark: nothing has been evaluated for it yet, so there is
// no next return to judge.
func (index *phaseTwoDueIndex) MarkHeldBack(queryGroup execution.QueryGroupIdentity) {
	if index == nil {
		return
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	if entry, ok := index.entries[queryGroup]; ok {
		entry.heldBack = true
	}
}

// dueBucketSlot is the ring position of a Unix minute. Go's % keeps the sign
// of the dividend, and a minute earlier than the ring is long is what a test
// clock starting near zero produces.
func dueBucketSlot(minute int64) int64 {
	return ((minute % dueCompletionBuckets) + dueCompletionBuckets) % dueCompletionBuckets
}

// Census counts the owned objects by where they are in their cycle right now,
// and the returned rounds of the last hour and six hours by whether they were
// on time. owned is how many objects the replica holds, so objects with no
// entry -- nothing evaluated since takeover -- can be counted.
//
// The grace is one period: an object past its due time by less than its own
// period is late, past it by more is overdue and has missed a turn. This is
// the same rule OverdueWakes applies, stated once more here because the census
// is over every entry and that call is over the overdue ones only.
func (index *phaseTwoDueIndex) Census(now time.Time, owned int) fleet.ScheduleCensus {
	census := fleet.ScheduleCensus{}
	if index == nil {
		return census
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	nowUnix := now.Unix()
	// The same pass counts each entry under its period, so the cohort a
	// reader meets on a histogram has its population and its cooling and
	// overdue share from the one source that knows every object's period.
	cohorts := make(map[int64]*fleet.ScheduleCohort)
	cohortOf := func(interval int64) *fleet.ScheduleCohort {
		if interval < 0 {
			interval = 0
		}
		cohort, ok := cohorts[interval]
		if !ok {
			cohort = &fleet.ScheduleCohort{IntervalSeconds: interval}
			cohorts[interval] = cohort
		}
		return cohort
	}
	for _, entry := range index.pending {
		cohort := cohortOf(entry.intervalSeconds)
		cohort.Objects++
		switch {
		case entry.dueAtUnix > nowUnix:
			census.Waiting++
			if entry.queryCooldown {
				census.Cooling++
				cohort.Cooling++
			}
		default:
			late := nowUnix - entry.dueAtUnix
			switch {
			case entry.intervalSeconds <= 0:
				census.Late++
				census.MissingPeriod++
			case late <= entry.intervalSeconds:
				census.Late++
			default:
				census.Overdue++
				cohort.Overdue++
			}
			if float64(late) > census.OldestLateSeconds {
				census.OldestLateSeconds = float64(late)
			}
		}
	}
	for _, cohort := range cohorts {
		census.Cohorts = append(census.Cohorts, *cohort)
	}
	sort.Slice(census.Cohorts, func(i, j int) bool { return census.Cohorts[i].IntervalSeconds < census.Cohorts[j].IntervalSeconds })
	if owned > len(index.entries) {
		census.Never = owned - len(index.entries)
	}
	minute := nowUnix / 60
	for offset := int64(0); offset < dueCompletionBuckets; offset++ {
		bucket := index.completions[dueBucketSlot(minute-offset)]
		if bucket.minute != minute-offset {
			continue
		}
		census.Completed6h += bucket.completed
		census.OnTime6h += bucket.onTime
		if offset < 60 {
			census.Completed1h += bucket.completed
			census.OnTime1h += bucket.onTime
			census.HeldBack1h += bucket.heldBack
			census.HeldBackOnTime1h += bucket.heldBackOnTime
		}
	}
	// The backlog an hour ago, from the oldest sample of the last hour this
	// index still holds -- which is younger than an hour on a process
	// younger than that, and the census says how much younger rather than
	// letting a ten-minute-old sample read as an hour's trend.
	for offset := int64(dueOverdueSamples - 1); offset > 0; offset-- {
		sample := index.overdue[((minute-offset)%dueOverdueSamples+dueOverdueSamples)%dueOverdueSamples]
		if sample.minute != minute-offset {
			continue
		}
		ago := sample.overdue
		census.OverdueAgo = &ago
		census.OverdueAgoSeconds = float64(offset * 60)
		break
	}
	index.overdue[(minute%dueOverdueSamples+dueOverdueSamples)%dueOverdueSamples] = dueOverdueSample{minute: minute, overdue: census.Overdue}
	return census
}

// WakeOf reports what the index holds for one object. Known is false when it
// holds nothing, which means nothing has been evaluated for the object since
// this replica took it over.
func (index *phaseTwoDueIndex) WakeOf(queryGroup string) fleet.WakeFacts {
	if index == nil {
		return fleet.WakeFacts{}
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	entry, ok := index.entries[execution.QueryGroupIdentity(queryGroup)]
	if !ok {
		return fleet.WakeFacts{}
	}
	return fleet.WakeFacts{Known: true, DueAt: time.Unix(entry.dueAtUnix, 0),
		IntervalSeconds: entry.intervalSeconds, Deferred: entry.deferred, Cooling: entry.queryCooldown}
}

// dueEntryHeap orders by wake time, then by name so an ordering is total. The
// heap is a min-heap with a position recorded on each entry, so a bound that
// moves is repaired in place; bounds move far more often than they are read,
// which is what rules out re-sorting the owned set instead.
//
// A second-resolution wall clock loses nothing here: an evaluation time is a
// Unix second everywhere in the contract, and the dispatcher tick is a second.
type dueEntryHeap []*phaseTwoDueEntry

func dueEntryBefore(left, right *phaseTwoDueEntry) bool {
	if left.dueAtUnix != right.dueAtUnix {
		return left.dueAtUnix < right.dueAtUnix
	}
	return left.queryGroup < right.queryGroup
}

func (entries dueEntryHeap) Len() int { return len(entries) }

func (entries dueEntryHeap) Less(left, right int) bool {
	return dueEntryBefore(entries[left], entries[right])
}

func (entries dueEntryHeap) Swap(left, right int) {
	entries[left], entries[right] = entries[right], entries[left]
	entries[left].position, entries[right].position = left, right
}

func (entries *dueEntryHeap) Push(value any) {
	entry := value.(*phaseTwoDueEntry)
	entry.position = len(*entries)
	*entries = append(*entries, entry)
}

func (entries *dueEntryHeap) Pop() any {
	old := *entries
	last := len(old) - 1
	entry := old[last]
	old[last] = nil
	*entries = old[:last]
	return entry
}
