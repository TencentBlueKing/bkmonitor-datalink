// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// LiveReserveTightWindow is how far back a fallback names its Query Group as
// tight: an hour, the window the overview's other recent signals use, so a
// group stops being reported an hour after its last fallback.
const LiveReserveTightWindow = time.Hour

// LiveReserveTightGroupsKept bounds the tight Query Groups a runtime reports,
// the same bound as the overdue wakes a replica publishes: it bounds the list a
// reader sees, not what is counted - Fallbacks stays exact.
const LiveReserveTightGroupsKept = 50

// LiveReserveGroupTailsKept is how many of a Query Group's own post-query
// tails its floor is the longest of, as many as the lookback keeps of a
// group's first reads to bound its reread: when tails can be exchanged, the
// next is longer than the longest of sixteen one time in seventeen.
const LiveReserveGroupTailsKept = 16

// LiveReserveGroupTailAge is how long a Query Group's tails count after its
// last one: the day the pooled tail is read over. A group this worker no
// longer runs stops being kept a day after its last Slot.
const LiveReserveGroupTailAge = 24 * time.Hour

// liveReservePruneEvery is how often recording sweeps the kept groups for
// ones that aged out: the sweep visits every group under the lock that each
// query deadline also takes, and recording runs on every Slot's completion, so
// the per-record path stays O(1) and the sweep runs at most once a minute.
const liveReservePruneEvery = time.Minute

// groupTails are a Query Group's last LiveReserveGroupTailsKept post-query
// tails and when the last was recorded.
type groupTails struct {
	values [LiveReserveGroupTailsKept]time.Duration
	count  int
	next   int
	last   time.Time
}

func (tails *groupTails) add(at time.Time, tail time.Duration) {
	tails.values[tails.next] = tail
	tails.next = (tails.next + 1) % LiveReserveGroupTailsKept
	tails.count = min(tails.count+1, LiveReserveGroupTailsKept)
	tails.last = at
}

func (tails *groupTails) longest() time.Duration {
	longest := time.Duration(0)
	for index := 0; index < tails.count; index++ {
		longest = max(longest, tails.values[index])
	}
	return longest
}

// LiveReserve is one worker runtime's downstream execution reserve: the time a
// Slot keeps after its query for everything that follows it - state reads and
// writes, evaluation, output to Kafka.
//
// The control plane freezes one value into every plan
// (DataRequirementConsumer.DownstreamExecutionReserveMilliSec). That value
// stays the plan's validity basis, the Slot's own deadlines and recovery
// window, and the capability verdict's basis, so plans, handover and the
// supported-interval check do not move with load. The worker sizes only the
// deadline its queries run under from what the reserve has to cover in this
// runtime: the measured post-query tail, from a query's return to its Slot's
// return, which the runtime's scheduler shaper publishes here. Until the
// runtime has measured enough Slots for that percentile to exist there is
// none, and the frozen value - today's fixed reserve - is used, as it always
// was.
//
// The pooled tail is every Query Group's, and post-query cost is a group's
// own: its state reads and writes grow with its series, its output with its
// alerts. A heavy group that makes up under one percent of the Slots is above
// the pooled percentile on every Slot of its own. So a group's query runs
// under the longer of the pooled tail and the longest of its own recent tails
// (RecordGroupTail), the way the lookback bounds a group's reread by its own
// longest first read.
//
// The trade the percentile makes, by design: up to one in a hundred Slots
// whose query runs to its deadline now overruns its completion deadline into
// a retry, where the fixed reserve absorbed them. Both directions are
// counted: Fallbacks, a query the live reserve left no time and that ran
// under the frozen one; Overruns, a Slot that returned after its completion
// deadline.
//
// It belongs to the runtime that measures it, not to the process: two runtimes
// in one process (tests, a split deployment) each size from their own tail. A
// nil LiveReserve is a runtime with none, and every method answers with the
// frozen reserve.
type LiveReserve struct {
	reserveMilli atomic.Int64
	// fallbacks counts query deadlines that fell back to the frozen reserve
	// because the live one left the query no time.
	fallbacks atomic.Uint64
	// overruns counts Slots that returned after their completion deadline.
	overruns atomic.Uint64

	mu sync.Mutex
	// tight is when each Query Group last fell back.
	tight map[QueryGroupIdentity]time.Time
	// groups is each Query Group's own recent post-query tails.
	groups map[QueryGroupIdentity]*groupTails
	// prunedGroups and prunedTight are when groups and tight were last swept.
	prunedGroups time.Time
	prunedTight  time.Time
}

// RecordGroupTail keeps one of queryGroup's post-query tails, measured at at.
// Groups whose last tail is older than LiveReserveGroupTailAge are dropped.
func (live *LiveReserve) RecordGroupTail(queryGroup QueryGroupIdentity, at time.Time, tail time.Duration) {
	if live == nil || tail <= 0 {
		return
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.groups == nil {
		live.groups = make(map[QueryGroupIdentity]*groupTails)
	}
	tails := live.groups[queryGroup]
	if tails == nil {
		tails = &groupTails{}
		live.groups[queryGroup] = tails
	}
	tails.add(at, tail)
	if at.Sub(live.prunedGroups) < liveReservePruneEvery {
		return
	}
	live.prunedGroups = at
	for group, kept := range live.groups {
		if at.Sub(kept.last) > LiveReserveGroupTailAge {
			delete(live.groups, group)
		}
	}
}

// GroupTail is the longest of queryGroup's recent post-query tails; zero for
// a group with none.
func (live *LiveReserve) GroupTail(queryGroup QueryGroupIdentity) time.Duration {
	if live == nil {
		return 0
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if tails := live.groups[queryGroup]; tails != nil {
		return tails.longest()
	}
	return 0
}

// reserveFor is the reserve queryGroup's queries run under: the longer of the
// pooled tail and the group's own longest recent one; zero - the frozen
// reserve - until the pooled tail is published, so a group never leaves the
// frozen reserve on a handful of its own Slots.
func (live *LiveReserve) reserveFor(queryGroup QueryGroupIdentity) int64 {
	pooled := live.Reserve()
	if pooled <= 0 {
		return 0
	}
	return max(pooled, live.GroupTail(queryGroup)).Milliseconds()
}

// Overruns is how many Slots returned after their completion deadline.
func (live *LiveReserve) Overruns() uint64 {
	if live == nil {
		return 0
	}
	return live.overruns.Load()
}

// CountOverrun counts a Slot that returned after its completion deadline.
func (live *LiveReserve) CountOverrun() {
	if live != nil {
		live.overruns.Add(1)
	}
}

// Set publishes the runtime's measured post-query tail. Zero or negative
// clears it, so the frozen reserve applies again.
func (live *LiveReserve) Set(reserve time.Duration) {
	if live != nil {
		live.reserveMilli.Store(max(reserve.Milliseconds(), 0))
	}
}

// Reserve is the published post-query tail; zero before any.
func (live *LiveReserve) Reserve() time.Duration {
	if live == nil {
		return 0
	}
	return time.Duration(live.reserveMilli.Load()) * time.Millisecond
}

// Fallbacks is how many query deadlines fell back to the frozen reserve.
func (live *LiveReserve) Fallbacks() uint64 {
	if live == nil {
		return 0
	}
	return live.fallbacks.Load()
}

// CountFallback counts a query of queryGroup that ran under the frozen reserve
// at at because the live one left it no time.
func (live *LiveReserve) CountFallback(queryGroup QueryGroupIdentity, at time.Time) {
	if live == nil {
		return
	}
	live.fallbacks.Add(1)
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.tight == nil {
		live.tight = make(map[QueryGroupIdentity]time.Time)
	}
	live.tight[queryGroup] = at
	if at.Sub(live.prunedTight) < liveReservePruneEvery {
		return
	}
	live.prunedTight = at
	for group, last := range live.tight {
		if at.Sub(last) > LiveReserveTightWindow {
			delete(live.tight, group)
		}
	}
}

// TightQueryGroups is the Query Groups that fell back within
// LiveReserveTightWindow before at, most recent first, at most
// LiveReserveTightGroupsKept of them: the ones whose interval does not hold
// the measured post-query tail, reported as a warning - they keep running.
func (live *LiveReserve) TightQueryGroups(at time.Time) []QueryGroupIdentity {
	if live == nil {
		return nil
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	groups := make([]QueryGroupIdentity, 0, len(live.tight))
	for group, last := range live.tight {
		if at.Sub(last) <= LiveReserveTightWindow {
			groups = append(groups, group)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if left, right := live.tight[groups[i]], live.tight[groups[j]]; !left.Equal(right) {
			return left.After(right)
		}
		return groups[i] < groups[j]
	})
	if len(groups) > LiveReserveTightGroupsKept {
		groups = groups[:LiveReserveTightGroupsKept]
	}
	return groups
}

// QueryDeadlineUnixMilli is the consumer's query deadline for a query of
// queryGroup in this runtime: the consumer deadline less the group's live
// reserve (reserveFor) when one is measured, less the frozen reserve
// otherwise.
//
// notBeforeUnixMilli is the earliest the query can run (its readiness, or now
// when it is about to start). A live reserve that would put the deadline at or
// before it - a slow tail hour on a short interval - would turn every query of
// that Slot into an instant failure. In that case the frozen reserve is used
// for this Slot and fellBack is true; the query paths count it
// (CountFallback). A spike in the tail therefore never stops detection.
func (live *LiveReserve) QueryDeadlineUnixMilli(consumer DataRequirementConsumer, queryGroup QueryGroupIdentity, notBeforeUnixMilli int64) (deadline int64, fellBack bool) {
	frozen := consumer.ConsumerDeadlineUnixMilli - consumer.DownstreamExecutionReserveMilliSec
	reserve := live.reserveFor(queryGroup)
	if reserve <= 0 {
		return frozen, false
	}
	if candidate := consumer.ConsumerDeadlineUnixMilli - reserve; candidate > notBeforeUnixMilli {
		return candidate, false
	}
	return frozen, true
}

// QueryBudgetMilli is the time a recovery query of queryGroup for a consumer
// with this evaluation interval may use: the interval less the group's live
// reserve when one is measured and it leaves a positive budget, less the
// frozen reserve otherwise.
func (live *LiveReserve) QueryBudgetMilli(consumer DataRequirementConsumer, queryGroup QueryGroupIdentity, intervalMillis int64) (budget int64, fellBack bool) {
	frozen := intervalMillis - consumer.DownstreamExecutionReserveMilliSec
	reserve := live.reserveFor(queryGroup)
	if reserve <= 0 {
		return frozen, false
	}
	if candidate := intervalMillis - reserve; candidate > 0 {
		return candidate, false
	}
	return frozen, true
}
