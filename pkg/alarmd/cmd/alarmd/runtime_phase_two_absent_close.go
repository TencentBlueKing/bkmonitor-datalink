package main

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A strategy stops being a candidate when the link's search no longer finds
// an active alert of it: its alerts closed, the next round's discovery
// passes over it. That is this loop's termination condition. Until then the
// next round may run the same close again; it is the same command
// (filterclose), and an alert no longer active is not matched.
//
// absentCloseInterval is how often the leader takes the difference. A
// candidate with no mark has to be seen missing by absentCloseMinReads
// rounds under two observations before it is closed, and the source is read
// again at least every controlplane.SourceFullReadInterval, so a round no
// slower than that re-read is what makes a confirmed absence reach a close
// inside absentCloseBound. Both relations are pinned by a test against the
// source's own bound, so neither number drifts from it.
const absentCloseInterval = 5 * time.Minute

// absentCloseRoundTime is how long one round's absent closes may take: half
// the round, as the edit close's (editCloseRoundTime). A job now runs again
// within its round while it gets somewhere (filterclose.Closer.UntilProgressStops),
// so either could fill the round; each half is its own.
const absentCloseRoundTime = absentCloseInterval / 2

// absentCloseBound is how long a confirmed absence may take to reach a close.
const absentCloseBound = 15 * time.Minute

// absentCloseMinReads is how many deciding rounds must have found a strategy
// with no mark missing from the snapshot before its alerts are closed: the
// first round, and two more absentCloseInterval apart, which is the ten
// minutes the grace used to be, counted in rounds so that a release that
// restarts the leader no longer starts the count over. It is
// this loop's own number and not the catalog's removal grace: that grace
// serves only a writer that makes no statement and goes when that writer is
// fixed (controlplane.AbsenceGracePeriod), while closing alerts takes a
// confirmed absence whatever the writer states. A strategy under the
// writer's mark takes the fast path instead (absentalerts.PathDeparted).
const absentCloseMinReads = 3

// absentStrategyClose closes the unrecovered alerts of strategies that no
// longer exist. See package absentalerts for what the difference is and
// what has to hold before anything is closed.
type absentStrategyClose struct {
	bundle  *phaseTwoWorkerBundle
	control absentCloseControl
	tracker *absentalerts.Tracker
	bounds  absentalerts.Bounds
	// lastDecided is the last strategy a round closed, where the next
	// round's walk over the candidates starts: a close decided and not
	// executed, the round having ended first, is first in line next time.
	lastDecided absentalerts.Key
	// wasLeader is whether the previous round ran as leader. Losing the
	// term clears the candidate clocks: a replica that comes back after an
	// hour must not close on memory it made in another term.
	wasLeader bool
	countsMu  sync.Mutex
	counts    map[string]uint64
	// last is the last round's denominators, which the gauge reports.
	last absentRoundSizes
	// table is the last deciding round, strategy by strategy, for the
	// candidate page; documents is the source's existence check that page
	// reads source_now with, nil where the source has none.
	table     *absentCandidateTable
	documents controlplane.StrategyDocumentPresence
	// edits is the edit close, run after each round over the strategies
	// that round's snapshot holds (lastSnapshot: nil before a round has
	// read one, and after the term ends). Nil where it is not wired.
	edits        *editStrategyClose
	lastSnapshot map[absentalerts.Key]struct{}
	// scopes is the strategy scope close, run after the edit close over
	// the same snapshot. Nil where it is not wired.
	scopes *strategyScopeClose
	// filter is the link's search and close by filter: the discovery of
	// the strategies the link holds alerts for, and every close.
	filter *filterRoute
	// logger writes the writer_unstated line, once per leader term and
	// again after a round with the statement; statementKey is the key the
	// statement is read from, which the line names. Either may be empty.
	logger       *observability.Logger
	statementKey string
	unstatedSaid bool
	// marks reads the writer's departure marks each round and again before
	// a fast-path close; nil where the source keeps none, and every
	// candidate takes the implicit path.
	marks departedMarkReader
	// memory keeps the candidates across terms; nil keeps them in this
	// process only. saved is the candidates the last save wrote, for the
	// removals the next one makes.
	memory absentMemory
	saved  map[absentalerts.Key]struct{}
}

// departedMarkReader is the source's read of the writer's departure marks:
// all of them for a round, one for the re-check just before a close
// (controlplane.LegacyRedisStrategySource.DepartedMarks, DepartedMark).
type departedMarkReader interface {
	DepartedMarks(context.Context) (map[string]controlplane.DepartedMark, int, error)
	DepartedMark(context.Context, string) (controlplane.DepartedMark, bool, error)
}

// absentRoundSizes is what the last round read, beside what it decided on.
type absentRoundSizes struct {
	counts absentalerts.Counts
	// snapshotAge is how old the snapshot was, reported beside its bound
	// so that a stale snapshot reads as the side falling behind.
	snapshotAge int
	// searches is the discovery's requests this round, tenants how many
	// tenants it walked, and complete whether every walk reached its end.
	searches, tenants int
	complete          bool
	identities        int
}

// absentCloseControl is what the loop asks the control plane: what the
// strategy cache says exists, and what it remembers about the strategies it
// let go.
type absentCloseControl interface {
	ObservedSnapshot() (controlplane.ObservedSnapshot, bool)
	DepartedStrategies() ([]controlplane.DepartedStrategy, uint64)
	// CapabilityRefused is the strategies the last publication refused for a
	// capability and runs no Plan of (controlplane.CapabilityRefused).
	CapabilityRefused() map[string]string
}

func newAbsentStrategyClose(bundle *phaseTwoWorkerBundle, control absentCloseControl, filter *filterRoute) *absentStrategyClose {
	return &absentStrategyClose{
		bundle: bundle, control: control, filter: filter,
		tracker: absentalerts.NewTracker(controlplane.MaxDepartedStrategies),
		bounds: absentalerts.Bounds{
			MinReads: absentCloseMinReads,
			// Derived from how often the source is actually read rather than
			// set as a second constant: a bound that does not follow the
			// reader's own cadence refuses every round on a deployment whose
			// source is slower than whatever number was written here. Five
			// reads' worth of slack.
			MaxSnapshotAge:     5 * controlplane.SourceFullReadInterval,
			MaxCloseStrategies: 8,
		},
		counts: make(map[string]uint64),
		table:  newAbsentCandidateTable(controlplane.MaxDepartedStrategies),
	}
}

// Stats is the per-candidate and per-alert outcome counts.
func (loop *absentStrategyClose) Stats() map[string]uint64 {
	loop.countsMu.Lock()
	defer loop.countsMu.Unlock()
	counts := make(map[string]uint64, len(absentalerts.Outcomes))
	for _, outcome := range absentalerts.Outcomes {
		counts[outcome] = loop.counts[outcome]
	}
	return counts
}

// Rounds is how each round ended, by its own word.
//
// A separate family from Stats on purpose: "this round decided nothing" is
// a fact about one round of this service, and "this candidate was decided
// and not closed" is a fact about one strategy's data.
func (loop *absentStrategyClose) Rounds() map[string]uint64 {
	loop.countsMu.Lock()
	defer loop.countsMu.Unlock()
	rounds := make(map[string]uint64, len(absentalerts.Refusals))
	for _, refusal := range absentalerts.Refusals {
		rounds[refusal] = loop.counts[refusal]
	}
	return rounds
}

// Difference is the round's denominators, for the gauge that reports them.
// A zero close count means one thing beside a difference of zero and
// another beside a round that refused, and these are what tell them apart.
func (loop *absentStrategyClose) Difference() map[string]int {
	loop.countsMu.Lock()
	defer loop.countsMu.Unlock()
	last := loop.last
	return map[string]int{
		"found_strategies": last.counts.Roster, "discovery_searches": last.searches, "discovery_tenants": last.tenants,
		"discovery_complete": boolSide(last.complete),
		"candidates":         last.counts.Candidates, "snapshot_strategies": last.counts.SnapshotStrategies,
		"remembered_identities": last.identities,
		"snapshot_age_seconds":  last.snapshotAge, "max_snapshot_age_seconds": int(loop.bounds.MaxSnapshotAge / time.Second),
	}
}

func boolSide(value bool) int {
	if value {
		return 1
	}
	return 0
}

// setTotal takes a running total the loop does not own - the memories keep
// their own - and publishes it as this counter's value.
func (loop *absentStrategyClose) setTotal(outcome string, total uint64) {
	loop.countsMu.Lock()
	loop.counts[outcome] = total
	loop.countsMu.Unlock()
}

func (loop *absentStrategyClose) count(outcome string, n int) {
	if n <= 0 {
		return
	}
	loop.countsMu.Lock()
	loop.counts[outcome] += uint64(n)
	loop.countsMu.Unlock()
}

func (loop *absentStrategyClose) run(ctx context.Context) {
	ticker := time.NewTicker(absentCloseInterval)
	defer ticker.Stop()
	for {
		loop.step(ctx)
		if loop.edits != nil {
			loop.edits.step(ctx, loop.lastSnapshot)
		}
		if loop.scopes != nil {
			loop.scopes.step(ctx, loop.lastSnapshot)
		}
		if loop.leading() {
			loop.filter.retryPending(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step is one round's absent closes. It may run for half the round
// (absentCloseRoundTime), the edit close the other half, and each request is
// bounded by the link's own budget (filterCloseHTTPTimeout). What a round
// does not finish - closes it decided - the next one carries on with.
func (loop *absentStrategyClose) step(ctx context.Context) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, absentCloseRoundTime)
	defer cancel()
	if !loop.leading() {
		if loop.wasLeader {
			loop.endTerm()
		}
		loop.count(absentalerts.OutcomeNotLeader, 1)
		return
	}
	if !loop.wasLeader {
		loop.startTerm(ctx)
	}
	loop.wasLeader = true
	// marksSaid is whether this round has counted marks_unreadable: once a
	// round, whichever read failed.
	marksSaid := false
	now := loop.bundle.dependencies.Now()
	observed, haveSnapshot := loop.control.ObservedSnapshot()
	departed, refusedDepartures := loop.control.DepartedStrategies()
	loop.setTotal(absentalerts.OutcomeMemoryFull, refusedDepartures+loop.tracker.Dropped())
	round := absentalerts.Round{
		Identities:         identitiesByKey(departed),
		SnapshotStrategies: snapshotKeys(observed), SnapshotUsable: haveSnapshot,
		Refused:             capabilityRefusals(loop.control.CapabilityRefused()),
		SnapshotObservation: observed.Observation,
		After:               loop.lastDecided, Now: now,
		Marks: loop.readMarks(ctx, &marksSaid),
	}
	round.WriterHoldsLastGood = haveSnapshot && observed.HoldsLastGood
	sizes := absentRoundSizes{identities: len(round.Identities)}
	if haveSnapshot {
		round.SnapshotAgeSeconds = int64(now.Sub(observed.ReadAt) / time.Second)
		sizes.snapshotAge = int(round.SnapshotAgeSeconds)
	}
	if haveSnapshot {
		loop.lastSnapshot = round.SnapshotStrategies
		sizes.searches, sizes.tenants = loop.discover(ctx, &round, observed, departed)
		sizes.complete = round.RosterComplete
	} else {
		// The discovery excludes the running strategies and cannot run
		// without them: the snapshot is what the round lacks, and the link,
		// not asked, is not named for it.
		round.LinkRead = true
	}
	result := loop.tracker.Round(round, loop.bounds)
	sizes.counts = result.Counts
	if result.Refusal == absentalerts.RefusalNone && !loop.save(ctx) {
		// The lease is gone: another replica holds the term. Nothing this
		// round decided is sent, and the memory is left to that term.
		loop.record(ctx, result, sizes, time.Since(started))
		loop.endTerm()
		return
	}
	loop.record(ctx, result, sizes, time.Since(started))
	loop.sayUnstated(result.Refusal, round.WriterHoldsLastGood)
	loop.table.noteRound(now, result.Refusal)
	if result.Refusal == absentalerts.RefusalNone {
		loop.table.rebuild(now, result, round.Roster, nil)
	}
	for _, absent := range result.Close {
		// A round now runs for minutes: the term is asked again before each
		// strategy, not only once at the start, and a fast-path close reads
		// its strategy's mark and the latest snapshot again right before it
		// is sent, so a strategy enabled again while earlier closes of the
		// round ran is not closed.
		if ctx.Err() != nil || !loop.leading() {
			return
		}
		if absent.Path == absentalerts.PathDeparted && !loop.stillDeparted(ctx, absent, &marksSaid) {
			continue
		}
		loop.table.executed(absent.Key, loop.closeStrategy(ctx, absent, now))
		loop.lastDecided = absent.Key
	}
}

// endTerm forgets what this term held. Every candidate's count restarts in
// this process, so a replica that becomes leader again starts from what the
// term in between kept (startTerm), not from what it observed itself under
// a term it no longer holds.
func (loop *absentStrategyClose) endTerm() {
	loop.tracker.Forget()
	loop.table.forget()
	loop.lastSnapshot = nil
	loop.filter.forget()
	loop.unstatedSaid = false
	loop.saved = nil
	loop.wasLeader = false
}

// startTerm restores the candidates the last term kept. None of them counts
// as read in this term (absentalerts.Tracker.Restore). A memory that cannot
// be read starts from nothing, counted.
func (loop *absentStrategyClose) startTerm(ctx context.Context) {
	if loop.memory == nil {
		return
	}
	kept, invalid, err := loop.memory.Recall(ctx)
	if err != nil {
		loop.observe(ctx, absentalerts.OutcomeMemoryLoadFailed, err, 1)
		return
	}
	if invalid > 0 {
		loop.observe(ctx, absentalerts.OutcomeMemoryInvalid, errors.New("kept candidates that did not decode were skipped"), invalid)
	}
	loop.tracker.Restore(kept)
	loop.saved = make(map[absentalerts.Key]struct{}, len(kept))
	for key := range kept {
		loop.saved[key] = struct{}{}
	}
}

// save writes the round's candidates and removes the ones that stopped
// being candidates since the last save - a strategy the snapshot lists
// again, one the link stopped listing. False when the lease is gone; any
// other failure is counted and the term goes on from its own memory.
func (loop *absentStrategyClose) save(ctx context.Context) bool {
	if loop.memory == nil {
		return true
	}
	current := loop.tracker.Absences()
	var removed []absentalerts.Key
	for key := range loop.saved {
		if _, still := current[key]; !still {
			removed = append(removed, key)
		}
	}
	err := loop.memory.Remember(ctx, current, removed)
	switch {
	case err == nil:
		loop.saved = make(map[absentalerts.Key]struct{}, len(current))
		for key := range current {
			loop.saved[key] = struct{}{}
		}
		return true
	case errors.Is(err, ownership.ErrStaleFence):
		loop.observe(ctx, absentalerts.OutcomeMemoryFenced, err, 1)
		return false
	default:
		loop.observe(ctx, absentalerts.OutcomeMemorySaveFailed, err, 1)
		return true
	}
}

// readMarks is this round's writer's marks by strategy id, nil where the
// source keeps none or the read failed (counted): every candidate then takes
// the implicit path. A mark that did not decode is counted and skipped.
func (loop *absentStrategyClose) readMarks(ctx context.Context, marksSaid *bool) map[string]absentalerts.Mark {
	if loop.marks == nil {
		return nil
	}
	read, invalid, err := loop.marks.DepartedMarks(ctx)
	if err != nil {
		loop.marksUnreadable(ctx, err, marksSaid)
		return nil
	}
	if invalid > 0 {
		loop.observe(ctx, absentalerts.OutcomeMarkInvalid, errors.New("departure marks that did not decode were skipped"), invalid)
	}
	marks := make(map[string]absentalerts.Mark, len(read))
	for id, mark := range read {
		marks[id] = absentalerts.Mark{TenantID: mark.TenantID, State: mark.State, Revision: mark.Revision}
	}
	return marks
}

// stillDeparted is the re-check just before a fast-path close: the latest
// snapshot does not list the strategy, and its mark, read again with one
// HGET, is the one it was decided under - the same tenant, state and
// revision. A strategy enabled again is listed, or its mark is gone or
// moved; either way it is not closed. A re-check that cannot read is not a
// yes.
func (loop *absentStrategyClose) stillDeparted(ctx context.Context, absent absentalerts.Absent, marksSaid *bool) bool {
	if loop.marks == nil {
		loop.marksUnreadable(ctx, errors.New("the fast path's re-check has no marks to read"), marksSaid)
		return false
	}
	mark, found, err := loop.marks.DepartedMark(ctx, absent.Key.StrategyID)
	if err != nil {
		loop.marksUnreadable(ctx, err, marksSaid)
		return false
	}
	// The snapshot after the mark: the latest the reconciler holds once the
	// mark is in hand.
	observed, haveSnapshot := loop.control.ObservedSnapshot()
	if !haveSnapshot {
		loop.marksUnreadable(ctx, errors.New("the fast path's re-check has no snapshot"), marksSaid)
		return false
	}
	for _, entry := range observed.Strategies {
		if entry.StrategyID == absent.Key.StrategyID {
			loop.observe(ctx, absentalerts.OutcomeDepartedReappeared, nil, 1)
			return false
		}
	}
	if !found || mark.State != absent.Mark.State || mark.Revision != absent.Mark.Revision || mark.TenantID != absent.Mark.TenantID {
		loop.observe(ctx, absentalerts.OutcomeDepartedMarkChanged, nil, 1)
		return false
	}
	return true
}

// marksUnreadable counts marks_unreadable once a round and writes a line
// for every failed read.
func (loop *absentStrategyClose) marksUnreadable(ctx context.Context, err error, said *bool) {
	count := 1
	if *said {
		count = 0
	}
	*said = true
	loop.observe(ctx, absentalerts.OutcomeMarksUnreadable, err, count)
}

// sayUnstated writes, the first time a term's round is refused as
// writer_unstated, that absent closes are off until the writer states its
// publication: one line, not one per round (the count is per round). A
// round with the statement re-arms it.
func (loop *absentStrategyClose) sayUnstated(refusal string, stated bool) {
	if stated {
		loop.unstatedSaid = false
		return
	}
	if refusal != absentalerts.RefusalWriterUnstated || loop.unstatedSaid {
		return
	}
	loop.unstatedSaid = true
	loop.logger.WarnEvent("absent_close_writer_unstated", "refused", 0, slog.String("statement_key", loop.statementKey),
		slog.String("detail", "the strategy list's writer made no publication_semantics statement about this list "+
			"(hold_last_good with the list's strategy_ids digest): no strategy's alerts are closed as absent; "+
			"each round is counted writer_unstated"))
}

// leading is whether this replica is the control leader and neither
// draining nor closed, read from memory.
func (loop *absentStrategyClose) leading() bool {
	loop.bundle.mu.RLock()
	defer loop.bundle.mu.RUnlock()
	return loop.bundle.controlLeader && !loop.bundle.draining && !loop.bundle.closed
}

// discover fills the round's roster from the link's search: per tenant the
// snapshot or the catalog's departures name, the strategies of active
// alerts of this source the snapshot does not run
// (filterclose.AbsentStrategies). A strategy the publication refused for a
// capability is not "running": its alerts are found and close as no longer
// detected. A tenant whose walk failed leaves the round unread - the link
// unavailable - and one whose walk stopped at its bound leaves it
// incomplete; both can only delay a close. It returns the searches made
// and the tenants walked.
func (loop *absentStrategyClose) discover(ctx context.Context, round *absentalerts.Round, observed controlplane.ObservedSnapshot,
	departed []controlplane.DepartedStrategy) (int, int) {
	if loop.filter == nil {
		loop.observe(ctx, absentalerts.RefusalLinkUnavailable, errors.New("the link's control plane is not named"), 0)
		return 0, 0
	}
	// A strategy listed without a tenant still exists (absentalerts reads
	// existence by id): it is running under every tenant, and no tenant ""
	// is walked - the link searches within a tenant only.
	running := map[string][]string{}
	var tenantless []string
	for _, entry := range observed.Strategies {
		if _, refused := round.Refused[entry.StrategyID]; refused {
			if _, ok := running[entry.TenantID]; !ok && entry.TenantID != "" {
				running[entry.TenantID] = nil
			}
			continue
		}
		if entry.TenantID == "" {
			tenantless = append(tenantless, entry.StrategyID)
			continue
		}
		running[entry.TenantID] = append(running[entry.TenantID], entry.StrategyID)
	}
	for _, entry := range departed {
		if _, ok := running[entry.TenantID]; !ok && entry.TenantID != "" {
			running[entry.TenantID] = nil
		}
	}
	tenants := make([]string, 0, len(running))
	for tenant := range running {
		running[tenant] = append(running[tenant], tenantless...)
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	round.Roster = map[absentalerts.Key]int{}
	round.LinkRead, round.RosterComplete = true, true
	searches := 0
	for _, tenant := range tenants {
		found, word := loop.filter.closer.AbsentStrategies(ctx, tenant, loop.filter.source, running[tenant])
		searches += len(found) + 1
		for _, strategy := range found {
			round.Roster[absentalerts.Key{TenantID: tenant, StrategyID: strategy.StrategyID}] = 1
		}
		switch {
		case word != "":
			round.LinkRead, round.RosterComplete = false, false
			loop.observe(ctx, absentalerts.RefusalLinkUnavailable, errors.New("the link's discovery answered "+word), 0)
			return searches, len(tenants)
		case len(found) == filterclose.MaxAbsentPerRound:
			round.RosterComplete = false
		}
	}
	return searches, len(tenants)
}

// closeStrategy closes the strategy's active alerts of this source through
// the link's filter and returns what it did, for the candidate page.
func (loop *absentStrategyClose) closeStrategy(ctx context.Context, absent absentalerts.Absent, now time.Time) absentExecution {
	execution := absentExecution{decidedAt: now, identity: fleet.AbsentIdentityNotRead,
		business: absent.Identity.BusinessID, revision: absent.Identity.Revision}
	if absent.Identity.Revision > 0 {
		execution.identity = fleet.AbsentIdentityCatalog
	}
	if _, err := strconv.ParseInt(absent.Key.StrategyID, 10, 64); err != nil || loop.filter == nil {
		loop.observe(ctx, absentalerts.OutcomeIdentityUnknown, errors.New("strategy id is not a positive integer, or no link to close by"), 1)
		execution.word = absentalerts.OutcomeIdentityUnknown
		return execution
	}
	decision := filterclose.DecisionAbsent
	switch {
	case absent.Refused != "":
		decision = filterclose.DecisionUnsupported
	case absent.Path == absentalerts.PathDeparted && absent.Mark.State == absentalerts.MarkDisabled:
		decision = filterclose.DecisionDisabled
	case absent.Path == absentalerts.PathDeparted && absent.Mark.State == absentalerts.MarkDeleted:
		decision = filterclose.DecisionDeleted
	}
	// The expectation is alarmd's own record of the strategy's open
	// episodes; without one each alert is closed by its id
	// (filterclose.Closer.Absent).
	// Run again within the round while a part has more and the run closed
	// something; sent is a real close whose items the link answered
	// (Result.Sent: closed, refused, busy), whatever the word to wait on -
	// a part refused beside one that closed hands on the refusal, and a
	// strategy the link refuses item by item is sent every round.
	started := time.Now()
	result := loop.filter.closer.UntilProgressStops(ctx, func() filterclose.Result {
		return loop.filter.closer.AbsentResult(ctx, loop.filter.scope(absent.Key), decision, absent.Identity.Revision)
	})
	execution.word = result.Word
	observeCloseJob(ctx, loop.bundle.dependencies.Observer, observability.StageAbsentStrategyClose, absentalerts.OutcomeCloseSent,
		absent.Key.StrategyID, result.Word, result.Sent, time.Since(started))
	if result.Sent {
		execution.sent = true
		loop.count(absentalerts.OutcomeCloseSent, 1)
	}
	return execution
}

// record writes the round's line: its refusal or its decision, with every
// denominator on it.
// elapsed is how long the round took to decide: the snapshot, the discovery
// of the link's active strategies and the difference; its closes, after,
// write their own lines.
func (loop *absentStrategyClose) record(ctx context.Context, result absentalerts.Result, sizes absentRoundSizes, elapsed time.Duration) {
	counts := result.Counts
	loop.countsMu.Lock()
	loop.last = sizes
	loop.counts[result.Refusal]++
	loop.countsMu.Unlock()
	loop.count(absentalerts.OutcomeCloseDecided, counts.CloseDecided)
	loop.count(absentalerts.OutcomeWithinGrace, counts.WithinGrace)
	loop.count(absentalerts.OutcomeUnconfirmed, counts.Unconfirmed)
	loop.count(absentalerts.OutcomeDeferred, counts.Deferred)
	loop.count(absentalerts.OutcomeIndexUnreadable, counts.RosterUnreadable)
	loop.count(absentalerts.OutcomeDepartedCloseDecided, counts.DepartedCloseDecided)
	loop.count(absentalerts.OutcomeDepartedWaiting, counts.DepartedWaiting)
	loop.count(absentalerts.OutcomeMarkInvalid, counts.MarkForeign)
	outcome := observability.Result(observability.ResultSuccess)
	if result.Refusal != absentalerts.RefusalNone {
		outcome = observability.ResultDegraded
	}
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageAbsentStrategyClose,
		Result: outcome, ReasonCode: observability.ReasonCode(result.Refusal), Duration: elapsed, DurationKnown: true,
		Counts: observability.Counts{Events: int64(counts.Candidates)},
	})
}

// observe counts an outcome and writes its line. A refusal word passed with
// a zero count writes the line only: the round's own record counts it.
func (loop *absentStrategyClose) observe(ctx context.Context, outcome string, err error, count int) {
	loop.count(outcome, count)
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageAbsentStrategyClose,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(outcome),
		Counts: observability.Counts{Events: int64(count)}, Err: err})
}

func identitiesByKey(entries []controlplane.DepartedStrategy) map[absentalerts.Key]absentalerts.Identity {
	identities := make(map[absentalerts.Key]absentalerts.Identity, len(entries))
	for _, entry := range entries {
		identities[absentalerts.Key{TenantID: entry.TenantID, StrategyID: entry.StrategyID}] =
			absentalerts.Identity{BusinessID: entry.BusinessID, Revision: entry.Revision, GlobalBusiness: entry.GlobalBusiness}
	}
	return identities
}

// capabilityRefusals is the refused strategies whose alerts close as no
// longer detected: refused for a capability alarmd does not support
// (fleet.ClosesAsUnsupported). A refusal a configuration change lifts - the
// dynamic-group source, the downstream reserve, snapshot retention, a target
// plan not yet written - keeps its alerts open with the refusal named.
func capabilityRefusals(refused map[string]string) map[string]string {
	if len(refused) == 0 {
		return nil
	}
	closing := make(map[string]string, len(refused))
	for strategyID, reason := range refused {
		if fleet.ClosesAsUnsupported(reason) {
			closing[strategyID] = reason
		}
	}
	return closing
}

func snapshotKeys(observed controlplane.ObservedSnapshot) map[absentalerts.Key]struct{} {
	keys := make(map[absentalerts.Key]struct{}, len(observed.Strategies))
	for _, entry := range observed.Strategies {
		keys[absentalerts.Key{TenantID: entry.TenantID, StrategyID: entry.StrategyID}] = struct{}{}
	}
	return keys
}
