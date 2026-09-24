package main

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// A strategy stops being a candidate when the alert link no longer lists it,
// which is the link's to decide: it rebuilds each strategy's set from its
// alert store, and a closed alert leaves the set on the next rebuild. That
// is this loop's termination condition. Until the rebuild, the next round
// may send the same closes again; the link treats a close for an alert that
// is no longer active as a no-op, so a repeat costs a message, not a state.
//
// absentCloseInterval is how often the leader takes the difference. The
// bound is the grace: a candidate has to be seen missing by two rounds under
// two observations before it is closed, and the source is read again at
// least every six minutes, so a round every five minutes is what makes a
// confirmed absence reach a close inside a quarter of an hour.
const absentCloseInterval = 5 * time.Minute

// absentCloseRosterPages bounds one walk of the link's roster. Each page is
// the link scanning part of its key space on its own connection; the bound
// is there so that a key space far larger than expected turns into an
// incomplete walk that says so, not into a round that never ends. An
// incomplete walk is a smaller roster, which delays closes and never makes
// one.
const absentCloseRosterPages = 2000

// absentCloseAlertBatch bounds the alerts one strategy's close sends at
// once.
const absentCloseAlertBatch = 64

// absentCloseIdentityReads bounds how many of a strategy's alert records are
// read looking for its business and revision. The first normally answers;
// the bound is for a strategy whose alerts were written by a build that did
// not label them.
const absentCloseIdentityReads = 3

// absentCloseMaxLinkHealthAge is how long ago the link's last successful
// full discovery may be. The link runs one a minute by default and retries a
// failure within seconds, so a quarter of an hour without one is a
// maintenance process that has stopped, not one that is slow.
const absentCloseMaxLinkHealthAge = 15 * time.Minute

// absentStrategyClose closes the unrecovered alerts of strategies that no
// longer exist. See package absentalerts for what the difference is and
// what has to hold before anything is closed.
type absentStrategyClose struct {
	bundle  *phaseTwoWorkerBundle
	control absentCloseControl
	link    absentCloseLink
	writer  closeWriter
	// send arms the close. False takes the whole difference and reports
	// every reading without sending one close; see LinkdConfig.
	// AbsentCloseSend for why the decision is a setting.
	send    bool
	tracker *absentalerts.Tracker
	bounds  absentalerts.Bounds
	// previousSnapshot is how large the last snapshot this loop decided on
	// was, which is what the next one's size is judged against.
	previousSnapshot int
	// lastDecided is the last strategy a round decided to close, where the
	// next round's walk over the candidates starts.
	lastDecided absentalerts.Key
	// wasLeader is whether the previous round ran as leader. Losing the
	// term clears the candidate clocks: a replica that comes back after an
	// hour must not close on memory it made in another term.
	wasLeader bool
	countsMu  sync.Mutex
	counts    map[string]uint64
	// last is the last round's denominators, which the gauge reports.
	last absentRoundSizes
}

// absentRoundSizes is what the last round read, beside what it decided on.
type absentRoundSizes struct {
	counts absentalerts.Counts
	// snapshotAge and linkAge are how old the two sides were, reported
	// beside their bounds so that a stale or unhealthy round can be read as
	// the side falling behind rather than as a bound that does not fit.
	snapshotAge, linkAge int
	rosterPages          int
	rosterComplete       bool
	linkPending          int
	identities           int
}

// absentCloseControl is what the loop asks the control plane: what the
// strategy cache says exists, and what it remembers about the strategies it
// let go.
type absentCloseControl interface {
	ObservedSnapshot() (controlplane.ObservedSnapshot, bool)
	DepartedStrategies() ([]controlplane.DepartedStrategy, uint64)
}

// absentCloseLink is what the loop asks the alert link: its roster, one
// strategy's active alerts, and one alert's record.
type absentCloseLink interface {
	Roster(context.Context, string) (openalerts.RosterPage, error)
	Reconcile(context.Context, openalerts.StrategyKey) (openalerts.Reconciliation, error)
	AlertRecord(context.Context, string, string) (openalerts.AlertRecord, error)
}

// eventSourceReader is a link that can also say how it keys this
// deployment's alerts. The roster walk reads it once per walk, so the
// reading is at most one walk old on the control leader; it is kept on the
// Console's record for the endpoint entry, and a failure there is the
// Console record's to show, never the walk's.
type eventSourceReader interface {
	EventSource(context.Context) (openalerts.EventSourceKeying, error)
}

func newAbsentStrategyClose(bundle *phaseTwoWorkerBundle, control absentCloseControl, link absentCloseLink,
	writer closeWriter, send bool) *absentStrategyClose {
	return &absentStrategyClose{
		bundle: bundle, control: control, link: link, writer: writer, send: send,
		tracker: absentalerts.NewTracker(controlplane.MaxDepartedStrategies),
		bounds: absentalerts.Bounds{
			Grace: controlplane.AbsenceGracePeriod,
			// Derived from how often the source is actually read rather than
			// set as a second constant: a bound that does not follow the
			// reader's own cadence refuses every round on a deployment whose
			// source is slower than whatever number was written here. Five
			// reads' worth of slack.
			MaxSnapshotAge:   5 * controlplane.SourceFullReadInterval,
			MaxLinkHealthAge: absentCloseMaxLinkHealthAge,
			// A strategy list that lost a fifth of its entries is the fact;
			// see RefusalSnapshotShrunk.
			MaxSnapshotShrinkRatio: 0.2, MinSnapshotForShrink: 20,
			MaxCloseStrategies: 8,
		},
		counts: make(map[string]uint64),
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
		"roster_strategies": last.counts.Roster, "roster_unreadable": last.counts.RosterUnreadable,
		"roster_pages": last.rosterPages, "roster_complete": boolSide(last.rosterComplete),
		"candidates": last.counts.Candidates, "snapshot_strategies": last.counts.SnapshotStrategies,
		"remembered_identities": last.identities,
		"send_armed":            boolSide(loop.send),
		"snapshot_age_seconds":  last.snapshotAge, "max_snapshot_age_seconds": int(loop.bounds.MaxSnapshotAge / time.Second),
		"link_health_age_seconds": last.linkAge, "max_link_health_age_seconds": int(loop.bounds.MaxLinkHealthAge / time.Second),
		"link_pending": last.linkPending,
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
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (loop *absentStrategyClose) step(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, absentCloseInterval/2)
	defer cancel()
	loop.bundle.mu.RLock()
	leader := loop.bundle.controlLeader && !loop.bundle.draining && !loop.bundle.closed
	loop.bundle.mu.RUnlock()
	if !leader {
		if loop.wasLeader {
			// The term ended. Every candidate's clock restarts, so a replica
			// that becomes leader again cannot close on an absence it
			// observed under a term it no longer holds.
			loop.tracker.Forget()
			loop.previousSnapshot = 0
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
		SnapshotObservation: observed.Observation, PreviousSnapshotStrategies: loop.previousSnapshot,
		After: loop.lastDecided, Now: now,
	}
	sizes := absentRoundSizes{identities: len(round.Identities)}
	if haveSnapshot {
		round.SnapshotAgeSeconds = int64(now.Sub(observed.ReadAt) / time.Second)
		sizes.snapshotAge = int(round.SnapshotAgeSeconds)
	}
	var health openalerts.LinkHealth
	health, sizes.rosterPages = loop.readRoster(ctx, &round)
	sizes.rosterComplete, sizes.linkPending = round.RosterComplete, health.PendingCount
	if !health.LastSuccess.IsZero() {
		sizes.linkAge = int(now.Sub(health.LastSuccess) / time.Second)
	}
	result := loop.tracker.Round(round, loop.bounds)
	if haveSnapshot && result.Refusal == absentalerts.RefusalNone {
		loop.previousSnapshot = result.Counts.SnapshotStrategies
	}
	if len(result.Close) > 0 {
		loop.lastDecided = result.Close[len(result.Close)-1].Key
	}
	sizes.counts = result.Counts
	loop.record(ctx, result, sizes)
	for _, absent := range result.Close {
		if ctx.Err() != nil {
			return
		}
		loop.closeStrategy(ctx, absent, now)
	}
}

// readRoster walks the link's roster into the round. The first page is what
// says whether the link could be read at all and carries the link's own
// health; a failure after it leaves the walk incomplete, which the round
// reports and decides on anyway, since a smaller roster can only cost
// closes.
func (loop *absentStrategyClose) readRoster(ctx context.Context, round *absentalerts.Round) (openalerts.LinkHealth, int) {
	round.Roster = make(map[absentalerts.Key]struct{})
	unreadable := make(map[absentalerts.Key]struct{})
	if reader, ok := loop.link.(eventSourceReader); ok {
		_, _ = reader.EventSource(ctx)
	}
	var health openalerts.LinkHealth
	cursor, pages := "", 0
	for pages < absentCloseRosterPages {
		page, err := loop.link.Roster(ctx, cursor)
		if err != nil {
			if pages == 0 {
				loop.observe(ctx, absentalerts.RefusalLinkUnavailable, err, 0)
			}
			break
		}
		if pages == 0 {
			health = page.Health
			round.LinkRead = true
			round.LinkLastSuccess, round.LinkError = health.LastSuccess, health.Error
		}
		pages++
		for _, row := range page.Rows {
			key := absentalerts.Key{TenantID: row.TenantID, StrategyID: row.StrategyID}
			switch {
			case row.Members == nil:
				unreadable[key] = struct{}{}
			case *row.Members > 0:
				round.Roster[key] = struct{}{}
			}
		}
		if page.Next == "" {
			round.RosterComplete = true
			break
		}
		cursor = page.Next
	}
	// A strategy read on one page and unreadable on another was read.
	for key := range round.Roster {
		delete(unreadable, key)
	}
	round.RosterUnreadable = len(unreadable)
	return health, pages
}

// closeStrategy reads the strategy's active alerts from the link and closes
// the ones this deployment produced.
func (loop *absentStrategyClose) closeStrategy(ctx context.Context, absent absentalerts.Absent, now time.Time) {
	key := openalerts.StrategyKey{TenantID: absent.Key.TenantID, StrategyID: absent.Key.StrategyID}
	reconciliation, err := loop.link.Reconcile(ctx, key)
	if err != nil {
		loop.observe(ctx, absentalerts.OutcomeEvidenceUnavailable, err, 1)
		return
	}
	own := make([]openalerts.Alert, 0, len(reconciliation.Alerts))
	foreign, unknown := 0, 0
	for _, alert := range reconciliation.Alerts {
		switch alert.EventSourceID {
		case "":
			unknown++
		case reconciliation.EventSourceID:
			own = append(own, alert)
		default:
			foreign++
		}
	}
	loop.count(absentalerts.OutcomeProducerForeign, foreign)
	loop.count(absentalerts.OutcomeProducerUnknown, unknown)
	if len(own) == 0 {
		return
	}
	strategyID, err := strconv.ParseInt(absent.Key.StrategyID, 10, 64)
	if err != nil || strategyID <= 0 {
		loop.observe(ctx, absentalerts.OutcomeIdentityUnknown, errors.New("strategy id is not a positive integer"), 1)
		return
	}
	identity, outcome := loop.identity(ctx, absent, own, reconciliation.EventSourceID)
	if outcome != "" {
		loop.observe(ctx, outcome, errors.New("no business or revision for a strategy that no longer exists"), 1)
		return
	}
	batch := make([]linkdoutput.CloseRequest, 0, min(len(own), absentCloseAlertBatch))
	for _, alert := range own {
		batch = append(batch, linkdoutput.CloseRequest{TenantID: key.TenantID, Fingerprint: alert.Fingerprint,
			AlertInstanceID: alert.AlertID, StrategyID: strategyID,
			StrategyRevision: identity.Revision, BusinessID: identity.BusinessID,
			OccurredAt: now, Reason: linkdoutput.CloseReasonAbsent})
		if len(batch) == absentCloseAlertBatch {
			break
		}
	}
	if !loop.send {
		// Everything up to here has run: the alerts were read, each one was
		// filed under whose it is, the identity was found and the batch was
		// built. Only the send is held, so that the counts a deployment
		// reads before arming - above all producer_foreign and
		// identity_unknown - are the counts arming would act on.
		loop.count(absentalerts.OutcomeWouldSend, len(batch))
		return
	}
	if err := loop.writer.WriteCloseBatch(ctx, batch); err != nil {
		loop.observe(ctx, absentalerts.OutcomeSendFailed, err, len(batch))
		return
	}
	loop.count(absentalerts.OutcomeAlertClosed, len(batch))
}

// identity is the business and revision a close carries. The catalog's
// memory answers for a strategy this process watched go; for the rest the
// answer is on the alerts themselves, in the labels they were created with,
// read from the link's record of the alert. A record is used only if it is
// this deployment's alert of this strategy.
func (loop *absentStrategyClose) identity(ctx context.Context, absent absentalerts.Absent, own []openalerts.Alert, source string) (absentalerts.Identity, string) {
	if absent.Identity.BusinessID != 0 && absent.Identity.Revision > 0 {
		return absent.Identity, ""
	}
	found := absent.Identity
	for i := 0; i < len(own) && i < absentCloseIdentityReads; i++ {
		record, err := loop.link.AlertRecord(ctx, absent.Key.TenantID, own[i].AlertID)
		if err != nil || record.EventSourceID != source || record.StrategyID != absent.Key.StrategyID {
			continue
		}
		if found.BusinessID == 0 {
			found.BusinessID = record.BusinessID
		}
		if found.Revision <= 0 {
			found.Revision = record.Revision
		}
		if found.BusinessID != 0 && found.Revision > 0 {
			return found, ""
		}
	}
	if found.BusinessID == 0 {
		return found, absentalerts.OutcomeIdentityUnknown
	}
	return found, absentalerts.OutcomeRevisionUnknown
}

// record writes the round's line: its refusal or its decision, with every
// denominator on it.
func (loop *absentStrategyClose) record(ctx context.Context, result absentalerts.Result, sizes absentRoundSizes) {
	counts := result.Counts
	loop.countsMu.Lock()
	loop.last = sizes
	loop.counts[result.Refusal]++
	loop.countsMu.Unlock()
	loop.count(absentalerts.OutcomeClosed, counts.Closed)
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
			absentalerts.Identity{BusinessID: entry.BusinessID, Revision: entry.Revision}
	}
	return identities
}

func snapshotKeys(observed controlplane.ObservedSnapshot) map[absentalerts.Key]struct{} {
	keys := make(map[absentalerts.Key]struct{}, len(observed.Strategies))
	for _, entry := range observed.Strategies {
		keys[absentalerts.Key{TenantID: entry.TenantID, StrategyID: entry.StrategyID}] = struct{}{}
	}
	return keys
}
