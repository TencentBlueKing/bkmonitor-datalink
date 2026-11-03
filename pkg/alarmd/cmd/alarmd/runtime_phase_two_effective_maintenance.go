package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// Maintenance uses the existing per-QG flight and lease, not a second owner or
// an outbox. It never waits behind detection, and close batches stay small.
func (runtime *productionPhaseTwoQueryGroup) withMaintenance(ctx context.Context, run func(context.Context, func(context.Context) error) error) error {
	// Loading external facts never occupies a detection flight. Only the
	// bounded close write needs exclusion from this owner's in-flight Slot.
	var release func()
	initialLease, accepting := runtime.session.Current()
	if !accepting {
		return errors.New("maintenance owner is not accepting")
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	check := func(ctx context.Context) error {
		if release == nil {
			var ok bool
			release, ok = runtime.flights.TryMaintenance(runtime.queryGroup)
			if !ok {
				return errMaintenanceBusy
			}
		}
		_, current, err := runtime.session.ValidateCurrentWithAssignment(ctx, runtime.now())
		if err == nil && (current.ContentScope != initialLease.ContentScope || current.TimelineRecordRevision != initialLease.TimelineRecordRevision) {
			return errors.New("effective-time content changed before close")
		}
		return err
	}
	if _, err := runtime.session.ValidateCurrent(ctx, runtime.now()); err != nil {
		return err
	}
	if runtime.viewGate != nil {
		var err error
		ctx, err = gateContext(ctx, runtime.viewGate, runtime.queryGroup, runtime.session, nil)
		if err != nil {
			return err
		}
	}
	return run(execution.ContextWithLeaseAuthority(ctx, runtime.session), check)
}

// maintenanceLease is the lease as this replica holds it, read from memory:
// the content scope and timeline revision the Assignment last authorized.
// The maintenance loop reads the Query Group's Plans again only when one of
// the two moves, which is the only way its Plans can change.
func (runtime *productionPhaseTwoQueryGroup) maintenanceLease() (string, uint64, bool) {
	lease, accepting := runtime.session.Current()
	return lease.ContentScope, lease.TimelineRecordRevision, accepting
}

var errMaintenanceBusy = errors.New("detection is executing this Query Group")

type maintenanceCatalog interface {
	CurrentPlans(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (controlplane.MaintenancePlans, error)
}
type maintenanceRunner interface {
	withMaintenance(context.Context, func(context.Context, func(context.Context) error) error) error
	maintenanceLease() (contentScope string, timelineRevision uint64, accepting bool)
}

type legacyRefresher interface {
	Refresh(context.Context, string, string, []int64) error
}

// The outcomes are the observability package's closed list; the names here
// are the ones this file reads by.
const (
	closeOutcomeDone                 = string(observability.EffectiveCloseDone)
	closeOutcomeNothingMatched       = string(observability.EffectiveCloseNothingMatched)
	closeOutcomeNotSettled           = string(observability.EffectiveCloseNotSettled)
	closeOutcomeUnsupported          = string(observability.EffectiveCloseUnsupported)
	closeOutcomeUnrouted             = string(observability.EffectiveCloseUnrouted)
	closeOutcomePrecheckFailed       = string(observability.EffectiveClosePrecheckFailed)
	closeOutcomeMaintenanceBusy      = string(observability.EffectiveCloseMaintenanceBusy)
	closeOutcomePlanUncompilable     = string(observability.EffectiveClosePlanUncompilable)
	closeOutcomeIdentityInvalid      = string(observability.EffectiveCloseIdentityInvalid)
	closeOutcomeEffectiveTimeUnknown = string(observability.EffectiveCloseEffectiveTimeUnknown)
	closeOutcomeLegacyUnavailable    = string(observability.EffectiveCloseLegacyUnavailable)
	closeOutcomeUnavailable          = string(observability.EffectiveCloseUnavailable)
	closeOutcomeUnsupportedRunner    = string(observability.EffectiveCloseUnsupportedRunner)
	closeOutcomeViewNotExecutable    = string(observability.EffectiveCloseViewNotExecutable)
	closeOutcomeGroupRetiring        = string(observability.EffectiveCloseGroupRetiring)
	closeOutcomeDeletionUnsettled    = string(observability.EffectiveCloseCalendarDeletionUnsettled)
	closeOutcomeBoundaryActive       = string(observability.EffectiveCloseBoundaryActive)
	closeOutcomeBoundaryNear         = string(observability.EffectiveCloseBoundaryNear)
	closeOutcomeStepSpent            = string(observability.EffectiveCloseStepSpent)
)

// maintenanceGroup is what the loop knows about one owned Query Group from
// memory: the Plans of its last read, split by what each needs, and the
// lease the read was made under. Nothing here is read from the store per
// tick; the store is read again when the lease moves.
type maintenanceGroup struct {
	contentScope     string
	timelineRevision uint64
	// until is the end of the Segment the Plans were read from, zero while
	// it was open (controlplane.MaintenancePlans.Until).
	until execution.EvaluationTime
	// legacy are the Plans on a schedule with no frozen snapshot, whose
	// calendar and timezone entries this replica keeps warm.
	legacy []controlplane.MaintenancePlan
	// closable are the Plans whose alerts this replica closes when every
	// Level is inactive: on the standard wire, with a schedule, and compiled
	// in full.
	closable []controlplane.MaintenancePlan
	// calendars are the calendars the read Plans' effective-time snapshots
	// name, each true when at least one snapshot holds it present. See
	// settleCalendarDeletions.
	calendars map[int64]bool
}

// current reports whether the Plans the group holds are the Query Group's
// at now: read under the lease its owner holds (content scope and timeline
// revision), and from a Segment that has not ended by now. A nil group has
// none.
func (group *maintenanceGroup) current(scope string, revision uint64, now time.Time) bool {
	return group != nil && group.contentScope == scope && group.timelineRevision == revision &&
		(group.until == 0 || execution.EvaluationTime(now.Unix()) < group.until)
}

type effectiveMaintenance struct {
	trackingMu  sync.Mutex
	bundle      *phaseTwoWorkerBundle
	catalog     maintenanceCatalog
	legacy      strategy.EffectiveTimeProvider
	legacyCache legacyRefresher
	capacity    config.LinkdCapacity
	// filter is the link's close by filter every inactive close goes
	// through; nil without the link's control plane, where nothing closes
	// (close_unrouted).
	filter *filterRoute
	// logger writes one line per close sent (effectiveCloseLogStage); nil
	// writes none.
	logger *observability.Logger
	// inactiveClosed is, per strategy version outside its effective time,
	// what the link's filter did for it in this inactive period. Forgotten
	// when its Plan is found active again, or no Plan read here names it.
	inactiveClosed map[inactiveKey]inactiveClose
	// stepJobs is how many inactive closes this step has run.
	stepJobs int
	// stepCloseUntil is when this step's inactive closes run out of time,
	// on the process's clock: inactiveJobTime after its first close
	// started, shared by every close after it. Zero until then.
	// stepCloseSpent is that the step found less than a request's budget
	// left and counted it (closeOutcomeStepSpent), once a step.
	stepCloseUntil time.Time
	stepCloseSpent bool
	// closeCursor is the Query Group the next step's inactive closes start
	// from. Where the last step stopped - out of time (stepSpent) or at
	// inactiveJobsPerStep - in a Query Group that ran a close, it is the
	// one after it, so a Query Group with many closes or a slow link does
	// not take the head of every step (its own planCursor keeps its place
	// for its next turn); where the step stopped at a Query Group before it
	// ran any, it is that one. Empty after a step that closed in every
	// Query Group.
	closeCursor execution.QueryGroupIdentity
	// steps is how many steps ran to their end, the close of every Query
	// Group included.
	steps        atomic.Uint64
	groups       map[execution.QueryGroupIdentity]*maintenanceGroup
	readCursor   execution.QueryGroupIdentity
	planCursor   map[execution.QueryGroupIdentity]int
	legacyCursor map[execution.QueryGroupIdentity]int
	countsMu     sync.Mutex
	counts       map[string]uint64
	// recent is the latest outcomes of every word but close_done, at most
	// maintenanceRecentKept of each, newest last: which Query Group, when
	// and what it said, which the counts cannot say and the log lines, kept
	// minutes on a busy Pod, no longer can.
	recent map[string][]maintenanceOutcome

	// deletedSince is when each calendar the owned Plans name started to
	// read deleted in every snapshot that names it, and sourceLossAt the
	// last step at which every one of them read deleted. See
	// settleCalendarDeletions.
	deletedSince map[int64]time.Time
	sourceLossAt time.Time
}

// maintenanceRecentKept bounds the Query Groups kept of each outcome word,
// and maintenanceErrorBytes the text kept of each one's error.
const (
	maintenanceRecentKept = 32
	maintenanceErrorBytes = 256
)

// maintenanceOutcome is one Query Group, and strategy when the outcome was
// about one, that the loop did not complete, kept by outcome word: when it
// was first and last seen in this process, how many times, and the latest
// error's text, cut to maintenanceErrorBytes. The loop ticks every second
// and repeats a close it holds back or a read that failed on every tick, so
// each one is kept once and counted rather than listed per tick: one Query
// Group repeating would otherwise push every other one out within a minute,
// and "held since" is what the one-by-one list could not say.
type maintenanceOutcome struct {
	QueryGroup string    `json:"query_group"`
	StrategyID string    `json:"strategy_id,omitempty"`
	BusinessID string    `json:"business_id,omitempty"`
	FirstAt    time.Time `json:"first_at"`
	LastAt     time.Time `json:"last_at"`
	Count      uint64    `json:"count"`
	// FirstError is the error's text at FirstAt, and Error the latest, with
	// ErrorSince when that text was first seen: a run whose failure changed
	// - timeouts, then a 502 - reads as both, each with its own time, so the
	// first time is never read as the latest error's.
	FirstError string    `json:"first_error,omitempty"`
	Error      string    `json:"error,omitempty"`
	ErrorSince time.Time `json:"error_since"`
}

// maintenanceReading is the loop's counts and the Query Groups it did not
// complete, by every word but close_done, the most recently seen first,
// each list present, empty when none was seen.
type maintenanceReading struct {
	Counts map[string]uint64               `json:"counts"`
	Recent map[string][]maintenanceOutcome `json:"recent"`
}

// Reading is the counts and the outcomes kept, copied.
func (m *effectiveMaintenance) Reading() maintenanceReading {
	reading := maintenanceReading{Counts: m.Stats(), Recent: map[string][]maintenanceOutcome{}}
	m.countsMu.Lock()
	defer m.countsMu.Unlock()
	for _, outcome := range observability.EffectiveCloseOutcomes {
		if string(outcome) == closeOutcomeDone {
			continue
		}
		kept := append([]maintenanceOutcome{}, m.recent[string(outcome)]...)
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].LastAt.After(kept[j].LastAt) })
		reading.Recent[string(outcome)] = kept
	}
	return reading
}

// remember keeps an outcome that was not a close done, once per
// Query Group and strategy: a repeat moves its last time and count, and a
// new one past the bound takes the place of the one seen longest ago.
func (m *effectiveMaintenance) remember(trace observability.TraceFields, outcome string, err error) {
	if outcome == closeOutcomeDone {
		return
	}
	at := m.bundle.dependencies.Now().UTC()
	text := ""
	if err != nil {
		// Redacted as every error this process serves is: a Redis or alert
		// link failure can carry an address or a URL with its credentials.
		text = cutAtRune(observability.SanitizeErrorText(err.Error()), maintenanceErrorBytes)
	}
	m.countsMu.Lock()
	defer m.countsMu.Unlock()
	if m.recent == nil {
		m.recent = make(map[string][]maintenanceOutcome, len(observability.EffectiveCloseOutcomes))
	}
	list := m.recent[outcome]
	oldest := -1
	for index := range list {
		kept := &list[index]
		if kept.QueryGroup == trace.QueryGroupKey && kept.StrategyID == trace.StrategyID && kept.BusinessID == trace.BusinessID {
			if text != kept.Error {
				kept.Error, kept.ErrorSince = text, at
			}
			kept.LastAt, kept.Count = at, kept.Count+1
			return
		}
		if oldest < 0 || kept.LastAt.Before(list[oldest].LastAt) {
			oldest = index
		}
	}
	entry := maintenanceOutcome{QueryGroup: trace.QueryGroupKey, StrategyID: trace.StrategyID, BusinessID: trace.BusinessID,
		FirstAt: at, LastAt: at, Count: 1, FirstError: text, Error: text, ErrorSince: at}
	if len(list) < maintenanceRecentKept {
		m.recent[outcome] = append(list, entry)
		return
	}
	list[oldest] = entry
}

// cutAtRune is text cut to at most limit bytes on a rune boundary.
func cutAtRune(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// Stats is the outcome counts, for the metric that reports every cell.
func (m *effectiveMaintenance) Stats() map[string]uint64 {
	m.countsMu.Lock()
	defer m.countsMu.Unlock()
	counts := make(map[string]uint64, len(observability.EffectiveCloseOutcomes))
	for _, outcome := range observability.EffectiveCloseOutcomes {
		counts[string(outcome)] = m.counts[string(outcome)]
	}
	return counts
}

func (m *effectiveMaintenance) count(outcome string, n int) {
	if n <= 0 {
		return
	}
	m.countsMu.Lock()
	if m.counts == nil {
		m.counts = make(map[string]uint64, len(observability.EffectiveCloseOutcomes))
	}
	m.counts[outcome] += uint64(n)
	m.countsMu.Unlock()
}

func (m *effectiveMaintenance) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		m.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step is one tick. It touches the store for two things only: the Plans of
// a Query Group whose lease moved since they were last read, at most
// GroupBatch of those per tick, and a close batch that is ready to send.
// Everything else - which Query Groups have a schedule at all, which Plans
// need their legacy entries kept warm, whether every Level is inactive right
// now - is answered from memory. Before this the loop read every owned Query
// Group's Plans from the store on every tick, whether or not one of them had
// a schedule: on a deployment where none did, that was a constant read rate
// against the control store that answered nothing.
//
// The step has no deadline of its own, and neither has a group's read or a
// send: every call in it is bounded by its own client, a Redis command by
// the configured dial, read and write timeouts and the close write by the
// producer's (decisionProducerTimeout). A budget over the whole step used to
// be shared by every owned group's reads and sends, less than one Kafka write
// may take, so a slow store cut the step at whichever call came last.
func (m *effectiveMaintenance) step(ctx context.Context) {
	m.bundle.mu.RLock()
	owned := make(map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime, len(m.bundle.runners))
	if !m.bundle.draining && !m.bundle.closed {
		for qg, lifecycle := range m.bundle.runners {
			owned[qg] = lifecycle.runner
		}
	}
	m.trackingMu.Lock()
	if m.planCursor == nil {
		m.planCursor = make(map[execution.QueryGroupIdentity]int)
		m.legacyCursor = make(map[execution.QueryGroupIdentity]int)
		m.groups = make(map[execution.QueryGroupIdentity]*maintenanceGroup)
	}
	if m.inactiveClosed == nil {
		m.inactiveClosed = make(map[inactiveKey]inactiveClose)
	}
	for qg := range m.groups {
		if _, keep := owned[qg]; !keep {
			delete(m.groups, qg)
			delete(m.planCursor, qg)
			delete(m.legacyCursor, qg)
		}
	}
	m.trackingMu.Unlock()
	m.bundle.mu.RUnlock()

	now := m.bundle.dependencies.Now()
	m.stepJobs = 0
	m.stepCloseUntil, m.stepCloseSpent = time.Time{}, false
	groups := make([]execution.QueryGroupIdentity, 0, len(owned))
	for qg := range owned {
		groups = append(groups, qg)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })

	// Which Query Groups need their Plans read: the ones the loop has not
	// read yet and the ones whose lease moved since. Answered from memory.
	runners := make(map[execution.QueryGroupIdentity]maintenanceRunner, len(groups))
	stale := make([]execution.QueryGroupIdentity, 0)
	for _, qg := range groups {
		runner, ok := owned[qg].(maintenanceRunner)
		if !ok {
			m.observe(ctx, qg, closeOutcomeUnsupportedRunner, errors.New("owner runner lacks maintenance capability"), 0)
			continue
		}
		runners[qg] = runner
		scope, revision, accepting := runner.maintenanceLease()
		if !accepting {
			continue
		}
		if known := m.groups[qg]; !known.current(scope, revision, now) {
			stale = append(stale, qg)
		}
	}
	start := sort.Search(len(stale), func(i int) bool { return stale[i] > m.readCursor })
	for n := 0; n < min(m.capacity.GroupBatch, len(stale)); n++ {
		if ctx.Err() != nil {
			return
		}
		qg := stale[(start+n)%len(stale)]
		m.readCursor = qg
		err := runners[qg].withMaintenance(ctx, func(round context.Context, _ func(context.Context) error) error {
			return m.readGroup(round, qg, runners[qg])
		})
		if err != nil {
			// The view not allowing the read yet is a rollout's shape, every
			// owned Query Group once in a replica's first seconds; the store
			// not answering is not. Under one word the first hid the second.
			var notExecutable *scheduler.ViewNotExecutableError
			switch {
			case errors.As(err, &notExecutable):
				m.observe(ctx, qg, closeOutcomeViewNotExecutable, err, 0)
			case errors.Is(err, controlplane.ErrQueryGroupRetiring):
				// Leaving the catalog: the same read gives the same answer
				// until the lease moves, so it is remembered under the lease
				// it was read with, holding no Plans, and not read again
				// each second.
				m.observe(ctx, qg, closeOutcomeGroupRetiring, err, 0)
				if scope, revision, accepting := runners[qg].maintenanceLease(); accepting {
					m.groups[qg] = &maintenanceGroup{contentScope: scope, timelineRevision: revision}
				}
			default:
				m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
			}
		}
	}

	deletions := m.settleCalendarDeletions(calendarReads(readUnderCurrentLease(m.groups, runners)), now)
	m.pruneInactiveClosed()
	// From closeCursor: a step out of close time stops where it is, and
	// starting every step from the first Query Group by name would leave
	// the later ones waiting for as long as the first ones have closes to
	// run.
	first := sort.Search(len(groups), func(i int) bool { return groups[i] >= m.closeCursor })
	stopped, cursor := false, execution.QueryGroupIdentity("")
	for n := range groups {
		at := (first + n) % len(groups)
		qg := groups[at]
		if ctx.Err() != nil {
			return
		}
		known := m.groups[qg]
		runner := runners[qg]
		if known == nil || runner == nil {
			continue
		}
		m.refreshLegacy(ctx, qg, known.legacy)
		// A close is judged only on Plans read under the lease the owner
		// holds now. A lease that moved since the read is a timeline or
		// content that changed: a strategy edited out of this Query Group
		// retires its timeline, the read under the new lease finds no Plan
		// (counted unavailable above), and the Plans of the old read are the
		// edited strategy's previous revision, whose effective time is not
		// the strategy's any more. They are judged again once a read under
		// the current lease lands. So with Plans read from a Segment that has
		// ended since: a cutover can close a Segment at a boundary ahead of
		// this clock, and a read before that boundary still answers the
		// Segment's Plans under the lease that already names the cutover.
		if scope, revision, accepting := runner.maintenanceLease(); !accepting || !known.current(scope, revision, now) {
			continue
		}
		ran := m.stepJobs
		if m.closeInactive(ctx, qg, runner, known.closable, deletions) && !stopped {
			stopped, cursor = true, qg
			if m.stepJobs > ran {
				cursor = groups[(at+1)%len(groups)]
			}
		}
	}
	m.closeCursor = cursor
	m.steps.Add(1)
}

// readUnderCurrentLease is the groups whose Plans were read under the lease
// their owner holds now. A group whose lease moved holds Plans from before
// the change: in the minute a source-wide loss arrives, the groups not read
// again yet would still name the calendars present and let the groups
// already read close.
func readUnderCurrentLease(groups map[execution.QueryGroupIdentity]*maintenanceGroup,
	runners map[execution.QueryGroupIdentity]maintenanceRunner) map[execution.QueryGroupIdentity]*maintenanceGroup {
	current := make(map[execution.QueryGroupIdentity]*maintenanceGroup, len(groups))
	for qg, known := range groups {
		if runner := runners[qg]; runner != nil {
			if scope, revision, accepting := runner.maintenanceLease(); accepting &&
				known.contentScope == scope && known.timelineRevision == revision {
				current[qg] = known
			}
		}
	}
	return current
}

// calendarDeletionSettle is how long a calendar has to read deleted, in
// every snapshot that names it, and how long since every calendar last read
// deleted at once, before a deletion closes an alert.
//
// The writer rebuilds the strategies' snapshots page by page once a minute
// and reads the calendars again for each page. A calendar source lost or
// restored in the middle of a round publishes the pages before it one way
// and the pages after it the other, in one publication: a calendar read
// deleted beside ones still present, or the same calendar read both ways.
// The next round reads every page the same way. The settle time is ten of
// those rounds. Python keeps a calendar for as long as its cache entry
// lives, up to a day after the calendar stops being refreshed, so a
// deletion closing ten minutes late still closes sooner than in Python.
const calendarDeletionSettle = 10 * time.Minute

var errCalendarDeletionUnsettled = errors.New("inactive close held: calendar deletion not settled")

// calendarDeletions is which of the calendars the owned Plans read deleted
// may close an alert in this step. See settleCalendarDeletions.
type calendarDeletions struct {
	settled    map[int64]struct{}
	sourceLoss bool
}

// calendarReads is what the groups' snapshots say of each calendar they
// name: true when at least one of them holds it present.
func calendarReads(groups map[execution.QueryGroupIdentity]*maintenanceGroup) map[int64]bool {
	reads := make(map[int64]bool)
	for _, group := range groups {
		for id, present := range group.calendars {
			reads[id] = reads[id] || present
		}
	}
	return reads
}

// settleCalendarDeletions records, from this step's reads, since when each
// calendar has read deleted everywhere and whether every calendar reads
// deleted, and answers which deletions may close.
//
// One calendar deleted is a deletion, and the Plans that named it as their
// only alert days are inactive and closed, as Python closes them - once it
// has read deleted in every snapshot that names it for
// calendarDeletionSettle. Every calendar deleted at once is the writer's
// calendar source gone: the writer marks each calendar it cannot find as
// deleted and keeps the snapshot READY, so a calendar table that answered
// nothing arrives as every calendar deleted, and read as deletions it would
// close the alerts of every strategy that alerts on calendar days. No
// deletion settles while that holds or within calendarDeletionSettle of it
// last holding, the same way an empty strategy list is held back from
// removing strategies. Detection still reads the calendars as empty, as
// Python does: reading them as unknown would freeze the strategies whose
// calendars are rest days, which is missed alerts.
//
// The reads are this replica's owned Plans, from memory, and not the whole
// deployment's: each strategy carries its own effective-time snapshot,
// naming only the calendars that strategy names, so there is no calendar
// table for the deployment on the read path, and this loop reads only the
// Segments and objects of the Query Groups it owns. In a source-wide loss
// every replica's share reads the same way.
//
// Known boundary: a replica whose owned Plans name only calendars that were
// really deleted reads every calendar deleted and holds their closes for as
// long as that lasts. It does not heal by itself; the alerts stay open
// until it is assigned a Plan that names a present calendar or the
// strategies change, and each held strategy is named, with its calendar, on
// the degraded line for an operator to close by hand. A strategy that names
// a deleted calendar is misconfigured, and leaving its alerts open is the
// side this loop takes when it cannot tell a deletion from a loss.
func (m *effectiveMaintenance) settleCalendarDeletions(reads map[int64]bool, now time.Time) calendarDeletions {
	if m.deletedSince == nil {
		m.deletedSince = make(map[int64]time.Time)
	}
	sourceLoss := len(reads) > 0
	for id, present := range reads {
		if present {
			sourceLoss = false
			delete(m.deletedSince, id)
		} else if _, known := m.deletedSince[id]; !known {
			m.deletedSince[id] = now
		}
	}
	for id := range m.deletedSince {
		if _, named := reads[id]; !named {
			delete(m.deletedSince, id)
		}
	}
	if sourceLoss {
		m.sourceLossAt = now
	}
	deletions := calendarDeletions{settled: make(map[int64]struct{})}
	if !m.sourceLossAt.IsZero() && now.Sub(m.sourceLossAt) < calendarDeletionSettle {
		deletions.sourceLoss = true
		return deletions
	}
	for id, since := range m.deletedSince {
		if now.Sub(since) >= calendarDeletionSettle {
			deletions.settled[id] = struct{}{}
		}
	}
	return deletions
}

// held names why the Plan's alerts are not closed for a calendar it reads
// deleted, or is nil when none holds it. A Plan that reads no calendar
// deleted closes as before, whatever the other Plans read.
func (deletions calendarDeletions) held(plan *strategy.CompiledPlan) error {
	var unsettled int64
	plan.EffectiveTimeCalendars(func(id int64, deleted bool) {
		if _, settled := deletions.settled[id]; deleted && !settled && (unsettled == 0 || id < unsettled) {
			unsettled = id
		}
	})
	switch {
	case unsettled == 0:
		return nil
	case deletions.sourceLoss:
		return fmt.Errorf("%w: every calendar the owned strategies name read deleted within %s, calendar %d among them",
			errCalendarDeletionUnsettled, calendarDeletionSettle, unsettled)
	default:
		return fmt.Errorf("%w: calendar %d has not read deleted in every snapshot that names it for %s",
			errCalendarDeletionUnsettled, unsettled, calendarDeletionSettle)
	}
}

// readGroup reads the Query Group's activated Plans under the lease the
// caller holds and records what each needs. It is the one store read of the
// loop.
func (m *effectiveMaintenance) readGroup(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner) error {
	scope, revision, accepting := runner.maintenanceLease()
	if !accepting {
		return errors.New("maintenance owner is not accepting")
	}
	at := m.bundle.dependencies.Now()
	read, err := m.catalog.CurrentPlans(ctx, qg, execution.EvaluationTime(at.Unix()))
	if err != nil {
		return err
	} // Keep known tracking on transient reads.
	m.count(closeOutcomePlanUncompilable, read.Uncompilable)
	known := &maintenanceGroup{contentScope: scope, timelineRevision: revision, until: read.Until}
	for _, plan := range read.Plans {
		plan.Compiled.EffectiveTimeCalendars(func(id int64, deleted bool) {
			if known.calendars == nil {
				known.calendars = make(map[int64]bool)
			}
			known.calendars[id] = known.calendars[id] || !deleted
		})
		if !planHasSchedule(plan.Compiled) {
			continue
		}
		if !plan.Compiled.HasEffectiveTimeSnapshot() {
			known.legacy = append(known.legacy, plan)
		}
		if !plan.CloseUnavailable && plan.Compiled.WireFormat() == contract.WireFormatStandardRawEvent {
			known.closable = append(known.closable, plan)
		}
	}
	m.groups[qg] = known
	return nil
}

// planHasSchedule reports whether any Level of the Plan, the no-data Level
// included, is on a schedule other than ALWAYS. A Plan with none needs
// nothing from this loop: no entry to keep warm, no inactive state to close
// on.
func planHasSchedule(plan *strategy.CompiledPlan) bool {
	levels := plan.Levels().Copy()
	if level := plan.NoDataLevel(); level != nil {
		levels = append(levels, *level)
	}
	for _, level := range levels {
		if level.EffectiveTimeRequirement().Kind() != strategy.EffectiveTimeAlways {
			return true
		}
	}
	return false
}

// inactiveKey is one strategy version whose alerts a Plan outside its
// effective time closes.
type inactiveKey struct {
	tenantID, strategyID string
	revision             int64
}

// inactiveClose is one inactiveKey's inactive period: when a step first
// found it inactive (the period's close decision, filterclose's command
// key), when a step first settled it (zero until then), and whether the
// one confirming pass after inactiveConfirmAfter has run. nearBoundary is
// the boundary a close of it was last not started before, too near to send
// (closeOutcomeBoundaryNear): counted once per Plan and boundary, not once
// a step through the seconds before it. heldAt is when a close of it last
// ended neither settled nor having got somewhere (filterclose.NextAfter:
// held or transient): it is not sent again until inactiveRefusedRetry
// after. A close that settles after
// it needs nothing cleared: the confirming pass comes inactiveConfirmAfter
// later still, past the wait.
type inactiveClose struct {
	since        time.Time
	settledAt    time.Time
	confirmed    bool
	nearBoundary time.Time
	heldAt       time.Time
}

// inactiveConfirmAfter is when a settled inactive close runs once more: an
// ABNORMAL in flight at the boundary, or an alert the store had not
// refreshed into the first search, is closed by the second pass. After it
// the version is left alone until its Plan is active again, so an inactive
// strategy costs two dry runs a period, not one a step.
const inactiveConfirmAfter = 5 * time.Minute

// inactiveRefusedRetry is how long a close waits before it is sent again
// when it ended neither settled nor having got somewhere: the same command
// answered the same way (filterclose.NextHeld: items refused, the request
// refused, the dry run past the expectation or the client's bound, a filter
// that cannot be built), or a failure that may clear (NextTransient: the
// link busy or not answering, a Redis read). Every step - one a second -
// sent it again before, a dry run and a real close each time, with no end.
// A Plan whose inactive window is shorter than the interval is not closed
// again inside that window after such an end: its next chance is its next
// window (or the confirming pass's). A transient failure waits as long: an inactive close is not urgent (its
// strategy is outside its effective time, and the absent close runs a
// round every absentCloseInterval, the same five minutes), a link that is
// busy or failing is not sent a close a second, and the ids of a real close
// whose answer was lost are retried by id meanwhile (RetryPending). A fixed interval rather
// than a growing backoff: one close per Plan per interval is little for
// the link, a refusal that can clear (the source published again) is
// picked up within the interval rather than after a backoff grown long,
// and it needs nothing kept but the time of the last refusal. The same
// interval as the confirming pass. Each retry counts its refusals as the
// first did (the filter close's job_done_refused and item counts, its job
// line, the effective_close line); the steps between send and count
// nothing.
const inactiveRefusedRetry = inactiveConfirmAfter

// inactiveJobsPerStep bounds the inactive closes one step runs over every
// owned Query Group: a mass boundary (every strategy of a calendar going
// inactive at once) is walked a few a second, not sent to the link at once.
const inactiveJobsPerStep = 4

// inactiveJobTime is how long the inactive closes of one step may run
// together: a dry run and a real close, each at a request's full budget
// (filterCloseHTTPTimeout, what filterclose waits for before it starts a
// request), and inactiveJobSlack for the command and record reads around
// them. A close given less than that may have its real close refused for
// time after a slow dry run. The step's closes share it, each after the
// first with what the ones before it left (stepCloseUntil): one each, up
// to inactiveJobsPerStep of them against a slow link, kept the step - and
// every other job of the maintenance loop behind it - for four times as
// long. It stays under a minute: what proves a Plan inactive for the whole
// of it is the Plan read inactive in this minute and at the next one's
// start.
const inactiveJobTime = 2*filterCloseHTTPTimeout + inactiveJobSlack

// inactiveJobSlack is the part of inactiveJobTime that is not the link's:
// the close's own Redis reads (its command, the episode record).
const inactiveJobSlack = 5 * time.Second

// inactiveHoldsUntil is until when a Plan rechecked inactive at checkedAt
// stays inactive, which no close of it may run past. Effective time changes
// only at a minute boundary (a time range is HH:MM, a daylight-saving jump
// whole minutes), so the Plan stays inactive to the end of checkedAt's
// minute; read inactive at the next minute's start as well (inactiveNext),
// to the end of that one too.
func inactiveHoldsUntil(checkedAt time.Time, inactiveNext bool) time.Time {
	until := checkedAt.Truncate(time.Minute).Add(time.Minute)
	if inactiveNext {
		until = until.Add(time.Minute)
	}
	return until
}

// closeInactive closes, through the link's filter, the alerts of the
// current version of every Plan whose Levels are all inactive now: the
// strategy's active alerts of this source labelled with that version
// (filterclose.Closer.Inactive). The judgement is from memory; the store is
// touched only for the owner check before the close. A Plan that reads a
// calendar deleted is not closed until the deletion settles
// (settleCalendarDeletions).
//
// It reports whether the step's closes stopped here, with Plans of this
// Query Group left - out of the step's close time, or at
// inactiveJobsPerStep: the next step starts from this Query Group, and its
// cursor (planCursor) at the Plan the step stopped before, which is the
// first the next step takes.
func (m *effectiveMaintenance) closeInactive(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plans []controlplane.MaintenancePlan, deletions calendarDeletions) bool {
	if len(plans) == 0 {
		return false
	}
	at := m.bundle.dependencies.Now()
	start := m.planCursor[qg]
	for n := 0; n < len(plans); n++ {
		if ctx.Err() != nil {
			return false
		}
		if m.stepJobs >= inactiveJobsPerStep {
			return true
		}
		i := (start + n) % len(plans)
		plan := plans[i]
		m.planCursor[qg] = (i + 1) % len(plans)
		fact, err := plan.Compiled.ResolveEffectiveTimeWithProvider(ctx, at.Unix(), plan.Identity.BusinessID, m.legacy)
		if err != nil {
			m.observe(ctx, qg, closeOutcomeEffectiveTimeUnknown, err, 0)
			continue
		}
		ref := plan.Compiled.StrategyRef()
		key := inactiveKey{tenantID: plan.Identity.TenantID, strategyID: plan.Identity.StrategyID, revision: ref.SnapshotRevision}
		if fact.Status() != strategy.EffectiveTimeInactive {
			if fact.Status() == strategy.EffectiveTimeActive {
				delete(m.inactiveClosed, key)
			}
			continue
		}
		done, ok := m.inactiveClosed[key]
		if ok && !done.settledAt.IsZero() && (done.confirmed || at.Sub(done.settledAt) < inactiveConfirmAfter) {
			continue
		}
		if ok && !done.heldAt.IsZero() && at.Sub(done.heldAt) < inactiveRefusedRetry {
			continue
		}
		if !ok && len(m.inactiveClosed) < m.capacity.LocalEntries {
			// The period's first sighting: its start names the period's
			// close decision.
			done = inactiveClose{since: at}
			m.inactiveClosed[key] = done
		}
		trace := observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID}
		if err := deletions.held(plan.Compiled); err != nil {
			m.observeTrace(ctx, trace, closeOutcomeDeletionUnsettled, err, 0)
			continue
		}
		if id, err := strconv.ParseInt(ref.StrategyID, 10, 64); err != nil || id <= 0 || ref.SnapshotRevision <= 0 {
			m.observe(ctx, qg, closeOutcomeIdentityInvalid, errors.New("inactive close: no positive strategy id and revision"), 0)
			continue
		}
		if m.filter == nil {
			m.observeTrace(ctx, trace, closeOutcomeUnrouted, errInactiveUnrouted, 0)
			continue
		}
		if m.stepSpent(ctx, trace) {
			m.planCursor[qg] = i
			return true
		}
		if m.stepCloseUntil.IsZero() {
			m.stepCloseUntil = time.Now().Add(inactiveJobTime)
		}
		since := done.since
		if since.IsZero() {
			// Past the memory's bound the period is not remembered: each
			// minute is its own decision, effective_at never before an
			// alert the close selects.
			since = at.Truncate(time.Minute)
		}
		word, err := m.sendClose(ctx, qg, runner, plan, key, since)
		if err == nil && word != "" {
			// Counted only when a close ran: a recheck that refused it, a
			// boundary too near, or the step's time spent by the owner check
			// sends nothing, takes none of the step's jobs, and does not make
			// this Query Group one that ran a close (closeCursor).
			m.stepJobs++
		}
		if errors.Is(err, errCloseStepSpent) {
			// The owner check and the recheck took the step's last request
			// budget: this Plan is the first of the next step.
			m.planCursor[qg] = i
			return true
		}
		if planRecheckRefused(err) {
			continue
		}
		if err != nil {
			return false
		}
		// A settled word settles the Plan; a close that got somewhere
		// (job_done_rederived, job_incomplete) runs again the next step; any
		// other word waits inactiveRefusedRetry. No word (boundary near) is
		// none of these: the next step past the boundary rechecks first.
		if word == "" {
			continue
		}
		switch filterclose.NextAfter(word) {
		case filterclose.NextSettled:
			if done, ok := m.inactiveClosed[key]; ok {
				if done.settledAt.IsZero() {
					done.settledAt = at
				} else {
					done.confirmed = true
				}
				m.inactiveClosed[key] = done
			}
		case filterclose.NextAgain:
		default:
			if done, ok := m.inactiveClosed[key]; ok {
				done.heldAt = at
				m.inactiveClosed[key] = done
			}
		}
	}
	return false
}

// stepSpent reports that this step's close time (stepCloseUntil) has less
// than one request's budget left: no further close starts in the step, as
// no request of it could (filterclose refuses one it cannot wait for). It
// is counted once a step, closeOutcomeStepSpent, on the Plan it stopped
// before; the next step starts its own time from that Query Group and
// that Plan (closeCursor, planCursor).
func (m *effectiveMaintenance) stepSpent(ctx context.Context, trace observability.TraceFields) bool {
	if m.stepCloseUntil.IsZero() || time.Until(m.stepCloseUntil) >= filterCloseHTTPTimeout {
		return false
	}
	if !m.stepCloseSpent {
		m.stepCloseSpent = true
		m.observeTrace(ctx, trace, closeOutcomeStepSpent, nil, 1)
	}
	return true
}

// pruneInactiveClosed forgets the versions no Plan read here names.
func (m *effectiveMaintenance) pruneInactiveClosed() {
	if len(m.inactiveClosed) == 0 {
		return
	}
	named := map[inactiveKey]bool{}
	for _, known := range m.groups {
		for _, plan := range known.closable {
			named[inactiveKey{tenantID: plan.Identity.TenantID, strategyID: plan.Identity.StrategyID,
				revision: plan.Compiled.StrategyRef().SnapshotRevision}] = true
		}
	}
	for key := range m.inactiveClosed {
		if !named[key] {
			delete(m.inactiveClosed, key)
		}
	}
}

// sendClose runs one inactive close: under the Query Group's flight the
// owner check and the recheck of the Plan's effective time, then, outside
// the flight so detection never waits behind the link, the link's filter
// for the close's own time, never past the moment the Plan may be active
// again (inactiveJobTime, inactiveHoldsUntil). Each way it can not close is
// named: the flight busy, the owner check refusing, the Plan active again
// or its effective time unreadable at the recheck, less than a request's
// time before it may be active again, the link not settling it.
func (m *effectiveMaintenance) sendClose(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plan controlplane.MaintenancePlan,
	key inactiveKey, since time.Time) (string, error) {
	// Counted in flight on the Query Group's lifecycle, as a Slot is: a
	// handover waits for the close before releasing the lease it is under.
	lifecycle, owned := m.bundle.enterOwnedWork(qg)
	if !owned {
		err := fmt.Errorf("%w: %w", errClosePrecheck, errCloseNotHeld)
		m.observe(ctx, qg, closeOutcomePrecheckFailed, err, 0)
		// Also on a replica that is only draining and still holds the group:
		// the judgment it drops is made again by whoever holds it next.
		delete(m.groups, qg)
		return "", err
	}
	defer m.bundle.exitOwnedWork(lifecycle)
	var checkedAt time.Time
	inactiveNext := false
	err := runner.withMaintenance(ctx, func(round context.Context, check func(context.Context) error) error {
		// Recheck current ownership and rules immediately before side effects;
		// never act on an old inactive judgment across an active boundary.
		if err := check(round); err != nil {
			if errors.Is(err, errMaintenanceBusy) {
				return err
			}
			return fmt.Errorf("%w: %w", errClosePrecheck, err)
		}
		checkedAt = m.bundle.dependencies.Now()
		fact, err := plan.Compiled.ResolveEffectiveTimeWithProvider(round, checkedAt.Unix(), plan.Identity.BusinessID, m.legacy)
		switch {
		case err != nil:
			return fmt.Errorf("%w: %w", errCloseTimeUnknown, err)
		case fact.Status() == strategy.EffectiveTimeActive:
			return errCloseBoundaryActive
		case fact.Status() != strategy.EffectiveTimeInactive:
			return errCloseTimeUnknown
		}
		// And at the next minute's start, the one boundary a close's own
		// time can cross: inactive there too, the close may run past this
		// minute (inactiveHoldsUntil). Active, unknown or unreadable there,
		// it is held to this minute.
		next, err := plan.Compiled.ResolveEffectiveTimeWithProvider(round, checkedAt.Truncate(time.Minute).Add(time.Minute).Unix(),
			plan.Identity.BusinessID, m.legacy)
		inactiveNext = err == nil && next.Status() == strategy.EffectiveTimeInactive
		return nil
	})
	switch {
	case err == nil:
	case errors.Is(err, errMaintenanceBusy):
		m.observe(ctx, qg, closeOutcomeMaintenanceBusy, err, 0)
	case errors.Is(err, errClosePrecheck):
		m.observe(ctx, qg, closeOutcomePrecheckFailed, err, 0)
	case errors.Is(err, errCloseBoundaryActive):
		m.observe(ctx, qg, closeOutcomeBoundaryActive, nil, 0)
	case errors.Is(err, errCloseTimeUnknown):
		m.observe(ctx, qg, closeOutcomeEffectiveTimeUnknown, err, 0)
	default:
		m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
	}
	if err != nil {
		if !errors.Is(err, errMaintenanceBusy) && !planRecheckRefused(err) {
			// The owner check refused: the Plans were read under a lease that
			// has moved. Read them again before the next judgement rather
			// than judging on what an older lease authorized.
			delete(m.groups, qg)
		}
		return "", err
	}
	trace := observability.TraceFields{QueryGroupKey: string(qg), StrategyID: key.strategyID}
	// The close's time is what is left of the step's (stepCloseUntil), cut
	// where the judgement may change: never acted on past the moment the
	// Plan may be active again. Bound to the minute alone, a close started
	// in a minute's last request budget was refused by filterclose before
	// its first or second request and counted round_spent, every step of
	// those seconds.
	boundary := inactiveHoldsUntil(checkedAt, inactiveNext)
	holds := boundary.Sub(checkedAt)
	if holds < filterCloseHTTPTimeout {
		// Not one request's time before the Plan may be active again: no
		// request could start (filterclose refuses one it cannot wait for).
		// Not started, counted as its own outcome once for this Plan and
		// boundary (every step of the seconds before it gets here); the
		// next step past the boundary rechecks first. A Plan past the
		// memory's bound has no entry and is counted each time.
		done, remembered := m.inactiveClosed[key]
		if !remembered || !done.nearBoundary.Equal(boundary) {
			if remembered {
				done.nearBoundary = boundary
				m.inactiveClosed[key] = done
			}
			m.observeTrace(ctx, trace, closeOutcomeBoundaryNear, nil, 1)
		}
		return "", nil
	}
	if m.stepSpent(ctx, trace) {
		// The owner check and the recheck took the step's last request
		// budget.
		return "", errCloseStepSpent
	}
	budget := min(time.Until(m.stepCloseUntil), holds)
	// As a duration: the deadline is on the process's own clock, the
	// judgement on the injected one.
	job, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// The expectation is alarmd's own record of the version's open
	// episodes (filterclose.Closer.Inactive).
	period := time.Duration(plan.Compiled.EvaluationSemantics().EvaluationInterval) * time.Second
	word := m.filter.closer.Inactive(job, m.filter.scope(absentalerts.Key{TenantID: key.tenantID, StrategyID: key.strategyID}), strconv.FormatInt(key.revision, 10),
		key.revision, since, period)
	outcome := closeOutcomeNotSettled
	switch word {
	case filterclose.OutcomeJobDone, filterclose.OutcomeJobIncomplete, filterclose.OutcomeJobDoneRederived, filterclose.OutcomeJobDoneRefused:
		outcome = closeOutcomeDone
		m.observeTrace(ctx, trace, outcome, nil, 1)
	case filterclose.OutcomeNothingMatched:
		outcome = closeOutcomeNothingMatched
		m.observeTrace(ctx, trace, outcome, nil, 1)
	case filterclose.OutcomeUnsupported:
		outcome = closeOutcomeUnsupported
		m.observeTrace(ctx, trace, outcome, errInactiveUnsupported, 1)
	default:
		m.observeTrace(ctx, trace, outcome, errors.New("inactive close: the link's filter answered "+word), 1)
	}
	m.logger.InfoEvent(effectiveCloseLogStage, outcome, 0, slog.String("query_group_key", string(qg)),
		slog.String("bk_tenant_id", key.tenantID), slog.String("strategy_id", key.strategyID),
		slog.Int64("revision", key.revision), slog.String("filter_outcome", word))
	return word, nil
}

// effectiveCloseLogStage is the stage of the line every inactive close sent
// writes, its outcome word as the result. The observation beside it is a
// metric only: a success outside the workflow stages is not a log line, so
// without this nothing says which strategy and Query Group a close was for.
// Not sampled: a version is closed twice an inactive period (the close and
// its confirming pass), again only while the link has not settled it, and a
// step sends at most inactiveJobsPerStep.
const effectiveCloseLogStage = "effective_close"

var (
	errClosePrecheck = errors.New("inactive close: owner check before send")
	errCloseNotHeld  = errors.New("the Query Group is no longer held here, or the replica is draining")
	// The recheck of the one Plan a close is for: its effective time active
	// again, or not readable, at the close. Both are that Plan's alone.
	errCloseBoundaryActive = errors.New("inactive close: the Plan is active again at the send")
	errCloseTimeUnknown    = errors.New("inactive close: the Plan's effective time is unknown at the send")
	errInactiveUnrouted    = errors.New("inactive close: no link control plane is named, nothing closes the Plan's alerts")
	errInactiveUnsupported = errors.New("inactive close: filtered close unsupported, the link cannot search or close by filter")
	// errCloseStepSpent is a close not sent because its owner check and
	// recheck left the step less than a request's budget (stepSpent).
	errCloseStepSpent = errors.New("inactive close: the step's close time is spent")
)

// planRecheckRefused reports a send the recheck of its own Plan refused:
// counted, and the step goes on to the Query Group's next Plan, which has
// an effective time of its own; nothing about the lease moved.
func planRecheckRefused(err error) bool {
	return errors.Is(err, errCloseBoundaryActive) || errors.Is(err, errCloseTimeUnknown)
}

// refreshLegacy keeps the legacy entries of the Query Group's Plans warm:
// every Plan on a schedule without a frozen snapshot, within one bounded
// round. A Plan whose entries were read within the minute costs nothing
// here, so the round's store reads are the entries that are due, shared
// across the Plans that name the same business or calendar.
func (m *effectiveMaintenance) refreshLegacy(ctx context.Context, qg execution.QueryGroupIdentity, plans []controlplane.MaintenancePlan) {
	if m.legacyCache == nil || len(plans) == 0 {
		return
	}
	round, cancel := context.WithTimeout(ctx, strategycache.RefreshDeadline(ctx))
	defer cancel()
	start := m.legacyCursor[qg]
	for n := 0; n < len(plans); n++ {
		i := (start + n) % len(plans)
		plan := plans[i]
		if round.Err() != nil {
			// Continue from here next tick; what was refreshed stays fresh
			// for its minute.
			m.legacyCursor[qg] = i
			return
		}
		levels := plan.Compiled.Levels().Copy()
		if level := plan.Compiled.NoDataLevel(); level != nil {
			levels = append(levels, *level)
		}
		var ids []int64
		for _, level := range levels {
			r := level.EffectiveTimeRequirement()
			ids = append(ids, r.ActiveCalendarIDs()...)
			ids = append(ids, r.InactiveCalendarIDs()...)
		}
		if err := m.legacyCache.Refresh(round, plan.Identity.TenantID, plan.Identity.BusinessID, ids); err != nil {
			m.observe(ctx, qg, closeOutcomeLegacyUnavailable, err, 0)
		}
	}
	m.legacyCursor[qg] = 0
}

// observe writes the line and counts the outcome. count is what the outcome
// is measured in - one close job for close_done and its kin - and
// one for the outcomes that are events in their own right.
func (m *effectiveMaintenance) observe(ctx context.Context, qg execution.QueryGroupIdentity, outcome string, err error, count int) {
	m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg)}, outcome, err, count)
}

// observeTrace is observe with the line's identity given in full, for an
// outcome that is about one strategy rather than the whole Query Group.
func (m *effectiveMaintenance) observeTrace(ctx context.Context, trace observability.TraceFields, outcome string, err error, count int) {
	result := observability.ResultSuccess
	if err != nil {
		result = observability.ResultDegraded
	}
	m.count(outcome, max(count, 1))
	m.remember(trace, outcome, err)
	m.bundle.dependencies.Observer.Observe(ctx, observability.Observation{Component: observability.ComponentRuntime, Stage: observability.StageEffectiveTimeMaintenance, Result: observability.Result(result),
		ReasonCode: observability.ReasonCode(outcome), Trace: trace, Counts: observability.Counts{Events: int64(count)}, Err: err})
}

// maintenanceSource is the effective-time maintenance as the CLI reads it,
// bound once the loop is built: the CLI is built before it.
type maintenanceSource struct {
	loop atomic.Pointer[effectiveMaintenance]
}

func (source *maintenanceSource) bind(loop *effectiveMaintenance) {
	if source != nil {
		source.loop.Store(loop)
	}
}

// cliMaintenanceReading is maintenance.get's answer: the answering process's
// effective-time maintenance, by outcome, and the latest of every outcome
// that was not a close done.
type cliMaintenanceReading struct {
	Scope   string                          `json:"scope"`
	ReadAt  time.Time                       `json:"read_at"`
	Running bool                            `json:"running"`
	Counts  map[string]uint64               `json:"counts"`
	Recent  map[string][]maintenanceOutcome `json:"recent"`
}

// cliMaintenanceOperation reads this process's effective-time maintenance.
// The counts are effective_close_total's; the recent outcomes are what the
// counts cannot say - which Query Group, which strategy, when, and why - and
// what the log lines, rotated out within minutes on a busy Pod, no longer
// can by the time someone asks.
func cliMaintenanceOperation(source *maintenanceSource) obchannel.Operation {
	return obchannel.Operation{ID: "maintenance.get",
		Summary:       "读取实际回答进程的生效时间维护：按结局的计数（与 effective_close_total 同源），以及除 close_done 外每种结局最近 32 条（查询组、策略、时间、错误原文，截到 256 字节），用来一步读出 unavailable 等是哪个查询组、什么原因；可指定实例。",
		EvidenceScope: "process", Targetable: true, Fields: map[string]obchannel.Field{},
		OutputSchema: obchannel.SchemaOf(cliMaintenanceReading{}),
		Limits: map[string]any{"redis_commands": 0, "scope": "answering_replica", "recent": maintenanceRecentKept,
			"error_bytes": maintenanceErrorBytes},
		Run: func(context.Context, obchannel.Params) obchannel.Outcome {
			reading := cliMaintenanceReading{Scope: "answering_replica", ReadAt: time.Now().UTC(),
				Counts: map[string]uint64{}, Recent: map[string][]maintenanceOutcome{}}
			var loop *effectiveMaintenance
			if source != nil {
				loop = source.loop.Load()
			}
			if loop != nil {
				kept := loop.Reading()
				reading.Running, reading.Counts, reading.Recent = true, kept.Counts, kept.Recent
			}
			return obchannel.Outcome{Value: reading, Complete: reading.Running,
				Limitations: []string{"Use meta.answered_by to identify this process: each replica maintains the Query Groups it owns, and another replica's outcomes are read by targeting it.",
					"Counts and outcomes are this process's since it started: first_at is when this process first saw the Query Group under the word, not when the condition began. first_error is the text at first_at and error the latest, which has been the text since error_since: read a time only with its own text. At most 32 Query Groups are kept per word, the one seen longest ago giving way; each error's text is cut to 256 bytes."}}
		}}
}
