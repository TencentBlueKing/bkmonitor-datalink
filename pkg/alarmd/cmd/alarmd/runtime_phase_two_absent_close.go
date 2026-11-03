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
)

// A strategy stops being a candidate when the link's search no longer finds
// an active alert of it: its alerts closed, the next round's discovery
// passes over it. That is this loop's termination condition. Until then the
// next round may run the same close again; it is the same command
// (filterclose), and an alert no longer active is not matched.
//
// absentCloseInterval is how often the leader takes the difference. The
// bound is the grace: a candidate has to be seen missing by two rounds under
// two observations before it is closed, and the source is read again at
// least every controlplane.SourceFullReadInterval, so a round no slower than
// that re-read, with absentCloseGrace, is what makes a confirmed absence
// reach a close inside absentCloseBound. Both relations are pinned by a test
// against the source's own bound, so neither number drifts from it.
const absentCloseInterval = 5 * time.Minute

// absentCloseBound is how long a confirmed absence may take to reach a close.
const absentCloseBound = 15 * time.Minute

// absentCloseGrace is how long this loop must have seen a strategy missing
// from the snapshot before it closes the strategy's alerts. It is this
// loop's own number and not the catalog's removal grace: that grace serves
// only a writer that makes no statement and goes when that writer is fixed
// (controlplane.AbsenceGracePeriod), while closing alerts takes a confirmed
// absence whatever the writer states.
const absentCloseGrace = 10 * time.Minute

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
			Grace: absentCloseGrace,
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

// step is one round. It may run until the next is due: the round has that
// deadline and no other, and each request is bounded by the link's own
// budget (filterCloseHTTPTimeout). What a round does not finish - closes it
// decided - the next one carries on with.
func (loop *absentStrategyClose) step(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, absentCloseInterval)
	defer cancel()
	if !loop.leading() {
		if loop.wasLeader {
			// The term ended. Every candidate's clock restarts, so a replica
			// that becomes leader again cannot close on an absence it
			// observed under a term it no longer holds.
			loop.tracker.Forget()
			loop.table.forget()
			loop.lastSnapshot = nil
			loop.filter.forget()
			loop.unstatedSaid = false
		}
		loop.wasLeader = false
		loop.count(absentalerts.OutcomeNotLeader, 1)
		return
	}
	loop.wasLeader = true
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
	loop.record(ctx, result, sizes)
	loop.sayUnstated(result.Refusal, round.WriterHoldsLastGood)
	loop.table.noteRound(now, result.Refusal)
	if result.Refusal == absentalerts.RefusalNone {
		loop.table.rebuild(now, result, round.Roster, nil)
	}
	for _, absent := range result.Close {
		// A round now runs for minutes: the term is asked again before each
		// strategy, not only once at the start.
		if ctx.Err() != nil || !loop.leading() {
			return
		}
		loop.table.executed(absent.Key, loop.closeStrategy(ctx, absent, now))
		loop.lastDecided = absent.Key
	}
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
	loop.logger.Warn("absent_close_writer_unstated", "refused", 0, 0, slog.String("statement_key", loop.statementKey),
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
	if absent.Refused != "" {
		decision = filterclose.DecisionUnsupported
	}
	// The expectation is alarmd's own record of the strategy's open
	// episodes; without one each alert is closed by its id
	// (filterclose.Closer.Absent).
	execution.word = loop.filter.closer.Absent(ctx, loop.filter.scope(absent.Key), decision, absent.Identity.Revision)
	switch execution.word {
	case filterclose.OutcomeJobDone, filterclose.OutcomeJobIncomplete, filterclose.OutcomeJobDoneRederived:
		execution.sent = true
		loop.count(absentalerts.OutcomeCloseSent, 1)
	}
	return execution
}

// record writes the round's line: its refusal or its decision, with every
// denominator on it.
func (loop *absentStrategyClose) record(ctx context.Context, result absentalerts.Result, sizes absentRoundSizes) {
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
	outcome := observability.Result(observability.ResultSuccess)
	if result.Refusal != absentalerts.RefusalNone {
		outcome = observability.ResultDegraded
	}
	loop.bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageAbsentStrategyClose,
		Result: outcome, ReasonCode: observability.ReasonCode(result.Refusal),
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
