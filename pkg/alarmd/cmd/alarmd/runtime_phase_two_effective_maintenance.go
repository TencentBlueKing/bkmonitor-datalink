package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
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
type closeWriter interface {
	WriteCloseBatch(context.Context, []linkdoutput.CloseRequest) error
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
	closeOutcomeAcked                = string(observability.EffectiveCloseAcked)
	closeOutcomePrecheckFailed       = string(observability.EffectiveClosePrecheckFailed)
	closeOutcomeSendFailed           = string(observability.EffectiveCloseSendFailed)
	closeOutcomeMaintenanceBusy      = string(observability.EffectiveCloseMaintenanceBusy)
	closeOutcomePlanUncompilable     = string(observability.EffectiveClosePlanUncompilable)
	closeOutcomeIdentityInvalid      = string(observability.EffectiveCloseIdentityInvalid)
	closeOutcomeEffectiveTimeUnknown = string(observability.EffectiveCloseEffectiveTimeUnknown)
	closeOutcomeLegacyUnavailable    = string(observability.EffectiveCloseLegacyUnavailable)
	closeOutcomeUnavailable          = string(observability.EffectiveCloseUnavailable)
	closeOutcomeUnsupportedRunner    = string(observability.EffectiveCloseUnsupportedRunner)
	closeOutcomeViewNotExecutable    = string(observability.EffectiveCloseViewNotExecutable)
	closeOutcomeDeletionUnsettled    = string(observability.EffectiveCloseCalendarDeletionUnsettled)
	closeOutcomeBoundaryActive       = string(observability.EffectiveCloseBoundaryActive)
	closeOutcomeBusinessUnknown      = string(observability.EffectiveCloseBusinessUnknown)
	closeOutcomeSourceLastKnown      = string(observability.EffectiveCloseSourceLastKnown)
	closeOutcomeSourceUnknown        = string(observability.EffectiveCloseSourceUnknown)
	closeOutcomeEvidenceUnavailable  = string(observability.EffectiveCloseEvidenceUnavailable)
	closeOutcomeIndexSetAcked        = string(observability.EffectiveCloseIndexSetAcked)
	closeOutcomeIndexSetDraining     = string(observability.EffectiveCloseIndexSetGroupDraining)
)

// maintenanceGroup is what the loop knows about one owned Query Group from
// memory: the Plans of its last read, split by what each needs, and the
// lease the read was made under. Nothing here is read from the store per
// tick; the store is read again when the lease moves.
type maintenanceGroup struct {
	contentScope     string
	timelineRevision uint64
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

type effectiveMaintenance struct {
	trackingMu  sync.Mutex
	bundle      *phaseTwoWorkerBundle
	catalog     maintenanceCatalog
	cache       *openalerts.Cache
	writer      closeWriter
	legacy      strategy.EffectiveTimeProvider
	legacyCache legacyRefresher
	capacity    config.LinkdCapacity
	sourceID    string
	// sourceOf, when set, is where sourceID comes from: the source of the
	// target the alert link's Console lists for this deployment, read rather
	// than configured.
	sourceOf func(context.Context) (string, error)
	// sourceRetryAt is when the source is read again after a failed read,
	// sourceBackoff the wait that set it and sourceErr the failure, kept for
	// the steps that wait it out.
	sourceRetryAt time.Time
	sourceBackoff time.Duration
	sourceErr     error
	// businesses answers, for a global business Plan, which business each
	// of its alerts was filed under; nil without the link's Console, where
	// such a Plan's alerts are not closed.
	businesses           *openalerts.AlertBusinesses
	byGroup              map[execution.QueryGroupIdentity][]openalerts.StrategyKey
	refs                 map[openalerts.StrategyKey]int
	calibrationRequested map[openalerts.StrategyKey]time.Time
	// ACK suppresses only immediate repeat sends. It never deletes the
	// external member or declares Linkd closed; reconciliation confirms that.
	sent         map[string]time.Time
	groups       map[execution.QueryGroupIdentity]*maintenanceGroup
	readCursor   execution.QueryGroupIdentity
	planCursor   map[execution.QueryGroupIdentity]int
	legacyCursor map[execution.QueryGroupIdentity]int
	countsMu     sync.Mutex
	counts       map[string]uint64
	// recent is the latest outcomes of every word but close_acked, at most
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
// complete, by every word but close_acked, the most recently seen first,
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
		if string(outcome) == closeOutcomeAcked {
			continue
		}
		kept := append([]maintenanceOutcome{}, m.recent[string(outcome)]...)
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].LastAt.After(kept[j].LastAt) })
		reading.Recent[string(outcome)] = kept
	}
	return reading
}

// remember keeps an outcome that was not an acknowledged close, once per
// Query Group and strategy: a repeat moves its last time and count, and a
// new one past the bound takes the place of the one seen longest ago.
func (m *effectiveMaintenance) remember(trace observability.TraceFields, outcome string, err error) {
	if outcome == closeOutcomeAcked {
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
	if m.byGroup == nil {
		m.byGroup = make(map[execution.QueryGroupIdentity][]openalerts.StrategyKey)
	}
	if m.refs == nil {
		m.refs = make(map[openalerts.StrategyKey]int)
	}
	if m.calibrationRequested == nil {
		m.calibrationRequested = make(map[openalerts.StrategyKey]time.Time)
	}
	if m.planCursor == nil {
		m.planCursor = make(map[execution.QueryGroupIdentity]int)
		m.legacyCursor = make(map[execution.QueryGroupIdentity]int)
		m.groups = make(map[execution.QueryGroupIdentity]*maintenanceGroup)
	}
	if m.sent == nil {
		m.sent = make(map[string]time.Time)
	}
	for qg := range m.byGroup {
		if _, keep := owned[qg]; !keep {
			m.releaseGroupLocked(qg)
		}
	}
	for qg := range m.groups {
		if _, keep := owned[qg]; !keep {
			delete(m.groups, qg)
		}
	}
	for key := range m.calibrationRequested {
		if m.refs[key] == 0 {
			delete(m.calibrationRequested, key)
		}
	}
	m.trackingMu.Unlock()
	m.bundle.mu.RUnlock()

	now := m.bundle.dependencies.Now()
	for key, at := range m.sent {
		if now.Sub(at) >= time.Minute {
			delete(m.sent, key)
		}
	}
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
		if known := m.groups[qg]; known == nil || known.contentScope != scope || known.timelineRevision != revision {
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
			if errors.As(err, &notExecutable) {
				m.observe(ctx, qg, closeOutcomeViewNotExecutable, err, 0)
			} else {
				m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
			}
		}
	}

	deletions := m.settleCalendarDeletions(calendarReads(readUnderCurrentLease(m.groups, runners)), now)
	sourceErr := m.readSource(ctx, groups, runners)
	for _, qg := range groups {
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
		// the current lease lands.
		if scope, revision, accepting := runner.maintenanceLease(); !accepting ||
			known.contentScope != scope || known.timelineRevision != revision {
			continue
		}
		m.closeInactive(ctx, qg, runner, known.closable, deletions, sourceErr)
	}
}

// effectiveSourceCoverage is what the read of the closes' source may take:
// effective time is resolved to the minute (a time range is HH:MM, a Level's
// step sixty seconds), so a close held back by the read for less than a
// minute lands in the minute it would have, and past that the next step asks
// again rather than this one waiting on.
const effectiveSourceCoverage = time.Minute

// readSource reads, once a step, the source the closes are for, when any
// group has a Plan it may close: a Console that does not answer is asked
// once, not once for each owned group in turn.
//
// The source is a property of this deployment, used to keep the closes to
// its own alerts; a Console outage does not change it. So a failed read is
// stale-if-error: the closes go on with the source last read - by this loop,
// or else by the open-alert copy's last calibration - under
// source_last_known, once per step. Only with no source read yet are the
// closes deferred, under source_unknown, and the error returned is what
// tells closeInactive to hold them. A failed read is not repeated every
// step: it waits from one step, doubling, up to effectiveSourceCoverage.
func (m *effectiveMaintenance) readSource(ctx context.Context, groups []execution.QueryGroupIdentity,
	runners map[execution.QueryGroupIdentity]maintenanceRunner) error {
	if m.sourceOf == nil {
		return nil
	}
	wanted := false
	for _, qg := range groups {
		if known := m.groups[qg]; known != nil && runners[qg] != nil && len(known.closable) > 0 {
			wanted = true
			break
		}
	}
	if !wanted {
		return nil
	}
	if now := m.bundle.dependencies.Now(); !now.Before(m.sourceRetryAt) {
		source, err := m.sourceOf(ctx)
		if err == nil {
			m.sourceID, m.sourceRetryAt, m.sourceBackoff, m.sourceErr = source, time.Time{}, 0, nil
			return nil
		}
		m.sourceBackoff = min(max(2*m.sourceBackoff, effectiveSourceRetryFloor), effectiveSourceCoverage)
		m.sourceRetryAt, m.sourceErr = now.Add(m.sourceBackoff), err
	}
	if m.sourceID == "" {
		m.sourceID = m.cache.OwnEventSourceID()
	}
	if m.sourceID != "" {
		m.observe(ctx, "", closeOutcomeSourceLastKnown, m.sourceErr, 0)
		return nil
	}
	m.observe(ctx, "", closeOutcomeSourceUnknown, m.sourceErr, 0)
	return m.sourceErr
}

// effectiveSourceRetryFloor is the first wait after a failed source read:
// one step of the loop.
const effectiveSourceRetryFloor = time.Second

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
// caller holds, records what each needs, and registers every Plan's strategy
// with the open alert index. It is the one store read of the loop.
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
	keys := make([]openalerts.StrategyKey, 0, len(read.Plans))
	for _, plan := range read.Plans {
		keys = append(keys, openalerts.StrategyKey{TenantID: plan.Identity.TenantID, StrategyID: plan.Identity.StrategyID})
	}
	if err := m.registerKeys(qg, keys, true); err != nil {
		return err
	}
	known := &maintenanceGroup{contentScope: scope, timelineRevision: revision}
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

// closeInactive closes the current alerts of every Plan whose Levels are all
// inactive now. The judgement is from memory; the store is touched only
// when a batch is ready to send, for the owner check before the send and
// the send itself. A Plan that reads a calendar deleted is not closed until
// the deletion settles (settleCalendarDeletions).
func (m *effectiveMaintenance) closeInactive(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plans []controlplane.MaintenancePlan, deletions calendarDeletions, sourceErr error) {
	if len(plans) == 0 {
		return
	}
	if sourceErr != nil {
		// Counted once for the step by readSource (source_unknown), not
		// again for each Query Group it holds back.
		return
	}
	at := m.bundle.dependencies.Now()
	start := m.planCursor[qg]
	remaining := m.capacity.CloseBatch
	for n := 0; n < len(plans); n++ {
		i := (start + n) % len(plans)
		plan := plans[i]
		m.planCursor[qg] = (i + 1) % len(plans)
		if ctx.Err() != nil {
			return
		}
		fact, err := plan.Compiled.ResolveEffectiveTimeWithProvider(ctx, at.Unix(), plan.Identity.BusinessID, m.legacy)
		if err != nil {
			m.observe(ctx, qg, closeOutcomeEffectiveTimeUnknown, err, 0)
			continue
		}
		if fact.Status() != strategy.EffectiveTimeInactive {
			continue
		}
		key := openalerts.StrategyKey{TenantID: plan.Identity.TenantID, StrategyID: plan.Identity.StrategyID}
		alerts, listed := m.cache.ListActiveAlerts(key)
		fromSet := false
		if !listed {
			// The set has no current calibration: its alerts to close are
			// not listed, which is not "none open". Its members, read fresh,
			// stand in for them (openalerts.UnlistedMembers); without them
			// the Plan is counted once each step that finds it inactive,
			// until a calibration lists its alerts.
			m.requestCalibration(key, at)
			members, fresh := m.cache.UnlistedMembers(key)
			if !fresh {
				m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
					closeOutcomeEvidenceUnavailable, errInactiveAlertsUnlisted, 0)
				continue
			}
			if m.currentAndDraining(qg) {
				// The set stands in for alerts nobody listed, by this Plan's
				// judgement alone; a Query Group current and draining at once
				// may hold a revision the strategy has left. The listing path
				// is the link's own answer and is not held here.
				m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
					closeOutcomeIndexSetDraining, errIndexSetGroupDraining, 0)
				continue
			}
			alerts, fromSet = membersAsAlerts(members, m.sourceID), true
		} else {
			known := make(map[string]bool, len(alerts))
			for _, alert := range alerts {
				known[alert.Fingerprint] = true
			}
			for _, member := range m.cache.Members(key) {
				if !known[member] {
					m.requestCalibration(key, at)
					break
				}
			}
		}
		if len(alerts) == 0 {
			continue
		}
		if err := deletions.held(plan.Compiled); err != nil {
			m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
				closeOutcomeDeletionUnsettled, err, 0)
			continue
		}
		business, err := strconv.ParseInt(plan.Identity.BusinessID, 10, 64)
		if err != nil {
			m.observe(ctx, qg, closeOutcomeIdentityInvalid, err, 0)
			continue
		}
		ref := plan.Compiled.StrategyRef()
		strategyID, err := strconv.ParseInt(ref.StrategyID, 10, 64)
		if err != nil {
			m.observe(ctx, qg, closeOutcomeIdentityInvalid, err, 0)
			continue
		}
		// A global business Plan's alerts are each filed under the business
		// they are about, and a close carries its alert's, read from the
		// alert's record; every other Plan's carry the Plan's business.
		global := plan.Compiled.GlobalBusiness()
		if global && fromSet {
			// A member has no record to read its business from.
			m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
				closeOutcomeBusinessUnknown, errSetMemberBusinessUnknown, len(alerts))
			continue
		}
		batch := make([]linkdoutput.CloseRequest, 0, m.capacity.CloseBatch)
		unknown := 0
		for _, alert := range alerts {
			if alert.EventSourceID != m.sourceID {
				continue
			}
			sentKey := key.TenantID + "\x00" + alert.EventSourceID + "\x00" + alert.AlertID
			if _, sent := m.sent[sentKey]; sent {
				continue
			}
			alertBusiness := business
			if global {
				if ctx.Err() != nil {
					break
				}
				read, err := m.alertBusiness(ctx, key, alert)
				if err != nil {
					unknown++
					continue
				}
				alertBusiness = read
			}
			batch = append(batch, linkdoutput.CloseRequest{TenantID: key.TenantID, Fingerprint: alert.Fingerprint, AlertInstanceID: alert.AlertID,
				StrategyID: strategyID, StrategyRevision: ref.SnapshotRevision, BusinessID: alertBusiness, OccurredAt: at})
			if len(batch) == remaining {
				break
			}
		}
		if unknown > 0 {
			m.observeTrace(ctx, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
				closeOutcomeBusinessUnknown, errors.New("an alert's record names no business for a global business Plan"), unknown)
		}
		if len(batch) == 0 {
			continue
		}
		sent, err := m.sendClose(ctx, qg, runner, plan, key, batch, fromSet)
		if planRecheckRefused(err) {
			continue
		}
		if err != nil {
			return
		}
		remaining -= sent
		if remaining == 0 {
			return
		}
	}
}

// sendClose sends one batch under the Query Group's flight and owner check.
// Each way it can not send is named: the flight busy, the owner check
// refusing, the Plan's boundary having moved to active or its effective
// time unreadable at the send, the producer failing.
func (m *effectiveMaintenance) sendClose(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plan controlplane.MaintenancePlan, key openalerts.StrategyKey, batch []linkdoutput.CloseRequest, fromSet bool) (int, error) {
	sent := 0
	// Counted in flight on the Query Group's lifecycle, as a Slot is: a
	// handover waits for the send before releasing the lease it is under.
	lifecycle, owned := m.bundle.enterOwnedWork(qg)
	if !owned {
		err := fmt.Errorf("%w: %w", errClosePrecheck, errCloseNotHeld)
		m.observe(ctx, qg, closeOutcomePrecheckFailed, err, 0)
		// Also on a replica that is only draining and still holds the group:
		// the judgment it drops is made again by whoever holds it next.
		delete(m.groups, qg)
		return 0, err
	}
	defer m.bundle.exitOwnedWork(lifecycle)
	err := runner.withMaintenance(ctx, func(round context.Context, check func(context.Context) error) error {
		// Recheck current ownership and rules immediately before side effects;
		// never retry an old inactive judgment across an active boundary.
		if err := check(round); err != nil {
			if errors.Is(err, errMaintenanceBusy) {
				return err
			}
			return fmt.Errorf("%w: %w", errClosePrecheck, err)
		}
		now := m.bundle.dependencies.Now()
		fact, err := plan.Compiled.ResolveEffectiveTimeWithProvider(round, now.Unix(), plan.Identity.BusinessID, m.legacy)
		switch {
		case err != nil:
			return fmt.Errorf("%w: %w", errCloseTimeUnknown, err)
		case fact.Status() == strategy.EffectiveTimeActive:
			return errCloseBoundaryActive
		case fact.Status() != strategy.EffectiveTimeInactive:
			return errCloseTimeUnknown
		}
		// Sarama's synchronous ACK wait follows the producer's timeout
		// (decisionProducerTimeout, 10s: a picked number, on the timeout
		// audit's follow-up list); a context deadline cannot cancel a
		// message already handed to Kafka.
		if err := m.writer.WriteCloseBatch(round, batch); err != nil {
			return fmt.Errorf("%w: %w", errCloseSend, err)
		}
		for _, request := range batch {
			if len(m.sent) < m.capacity.LocalEntries {
				m.sent[key.TenantID+"\x00"+m.sourceID+"\x00"+request.AlertInstanceID] = now
			}
		}
		m.requestCalibration(key, now)
		sent = len(batch)
		m.observe(ctx, qg, closeOutcomeAcked, nil, sent)
		if fromSet {
			m.observeTrace(round, observability.TraceFields{QueryGroupKey: string(qg), StrategyID: plan.Identity.StrategyID},
				closeOutcomeIndexSetAcked, nil, sent)
		}
		return nil
	})
	switch {
	case err == nil:
	case errors.Is(err, errMaintenanceBusy):
		m.observe(ctx, qg, closeOutcomeMaintenanceBusy, err, 0)
	case errors.Is(err, errClosePrecheck):
		m.observe(ctx, qg, closeOutcomePrecheckFailed, err, 0)
	case errors.Is(err, errCloseSend):
		m.observe(ctx, qg, closeOutcomeSendFailed, err, 0)
	case errors.Is(err, errCloseBoundaryActive):
		m.observe(ctx, qg, closeOutcomeBoundaryActive, nil, 0)
	case errors.Is(err, errCloseTimeUnknown):
		m.observe(ctx, qg, closeOutcomeEffectiveTimeUnknown, err, 0)
	default:
		m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
	}
	if err != nil && !errors.Is(err, errMaintenanceBusy) && !errors.Is(err, errCloseSend) && !planRecheckRefused(err) {
		// The owner check refused: the Plans were read under a lease that
		// has moved. Read them again before the next judgement rather than
		// judging on what an older lease authorized. A busy flight and a
		// failed send say nothing about the lease.
		delete(m.groups, qg)
	}
	return sent, err
}

var (
	errClosePrecheck = errors.New("inactive close: owner check before send")
	errCloseNotHeld  = errors.New("the Query Group is no longer held here, or the replica is draining")
	errCloseSend     = errors.New("inactive close: send")
	// The recheck of the one Plan a batch is for: its effective time active
	// again, or not readable, at the send. Both are that Plan's alone.
	errCloseBoundaryActive = errors.New("inactive close: the Plan is active again at the send")
	errCloseTimeUnknown    = errors.New("inactive close: the Plan's effective time is unknown at the send")
	// The Plan is inactive and the open-alert copy holds no current
	// calibration of its strategy's set to list the alerts to close.
	errInactiveAlertsUnlisted = errors.New("inactive close: the strategy's open alerts cannot be listed, its set has no current calibration")
	// The Query Group is in the current active set and in the draining
	// projection at once (productionPhaseTwoControl.CurrentAndDraining).
	errIndexSetGroupDraining = errors.New("inactive close: the index set does not stand in for a Query Group that is current and draining at once")
	// A global business Plan's close carries each alert's business, read
	// from the alert's record; a member of the set has none to read.
	errSetMemberBusinessUnknown = errors.New("inactive close: the alerts are addressed from the index set, which names no business for a global business Plan")
)

// membersAsAlerts addresses each member of a strategy's index set as an
// alert of this deployment's source, its fingerprint standing in for the
// instance id (linkdoutput.CloseRequest). The link resolves a close to the
// active alert by tenant, this deployment's event source and fingerprint,
// so a member that is another producer's alert, or no alert at all, is
// closed by nothing there. A member that is not a native fingerprint is not
// one a close can address and is left out, as an alert of another source
// is.
func membersAsAlerts(members []string, source string) []openalerts.Alert {
	alerts := make([]openalerts.Alert, 0, len(members))
	for _, member := range members {
		if linkdoutput.NativeFingerprint(member) {
			alerts = append(alerts, openalerts.Alert{AlertID: member, EventSourceID: source, Fingerprint: member})
		}
	}
	return alerts
}

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

// registerExecutedPlans uses the same ownership registry as background
// maintenance. It is a memory-only registration before the first evaluation,
// including a new Plan added to an already owned QG, so its first ACK survives.
func (m *effectiveMaintenance) registerExecutedPlans(qg execution.QueryGroupIdentity, plans []execution.PlanIdentity) {
	keys := make([]openalerts.StrategyKey, 0, len(plans))
	for _, p := range plans {
		keys = append(keys, openalerts.StrategyKey{TenantID: p.TenantID, StrategyID: p.StrategyID})
	}
	if err := m.registerKeys(qg, keys, false); err != nil {
		m.observe(context.Background(), qg, closeOutcomeUnavailable, err, 0)
	}
}
func (m *effectiveMaintenance) registerKeys(qg execution.QueryGroupIdentity, keys []openalerts.StrategyKey, replace bool) error {
	m.trackingMu.Lock()
	defer m.trackingMu.Unlock()
	if m.byGroup == nil {
		m.byGroup = make(map[execution.QueryGroupIdentity][]openalerts.StrategyKey)
		m.refs = make(map[openalerts.StrategyKey]int)
	}
	old := m.byGroup[qg]
	if !replace {
		merged := old
		for _, key := range keys {
			found := false
			for _, v := range merged {
				if v == key {
					found = true
					break
				}
			}
			if !found {
				merged = append(merged, key)
			}
		}
		keys = merged
	}
	if sameStrategyKeys(old, keys) {
		return nil
	}
	if err := m.cache.TrackOwned(keys...); err != nil {
		return err
	}
	for _, key := range keys {
		m.refs[key]++
	}
	for _, key := range old {
		m.refs[key]--
		if m.refs[key] == 0 {
			delete(m.refs, key)
			m.cache.Untrack(key)
		}
	}
	m.byGroup[qg] = append([]openalerts.StrategyKey(nil), keys...)
	return nil
}

func sameStrategyKeys(a, b []openalerts.StrategyKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *effectiveMaintenance) releaseGroupLocked(qg execution.QueryGroupIdentity) {
	for _, key := range m.byGroup[qg] {
		m.refs[key]--
		if m.refs[key] <= 0 {
			delete(m.refs, key)

			m.cache.Untrack(key)
		}
	}
	delete(m.byGroup, qg)
	delete(m.planCursor, qg)
	delete(m.legacyCursor, qg)
}

func (m *effectiveMaintenance) requestCalibration(key openalerts.StrategyKey, at time.Time) {
	if at.Sub(m.calibrationRequested[key]) < time.Minute {
		return
	}
	m.calibrationRequested[key] = at
	m.cache.RequestReconcile(key)
}

// observe writes the line and counts the outcome. count is what the outcome
// is measured in - alerts for close_acked - and
// one for the outcomes that are events in their own right.
// alertBusiness is the business a global business Plan's alert was filed
// under, from its record. Without the Console there is no record to read and
// the alert is not closed.
func (m *effectiveMaintenance) alertBusiness(ctx context.Context, key openalerts.StrategyKey, alert openalerts.Alert) (int64, error) {
	if m.businesses == nil {
		return 0, openalerts.ErrAlertBusinessUnknown
	}
	return m.businesses.Of(ctx, key.TenantID, alert.EventSourceID, key.StrategyID, alert.AlertID)
}

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
// that was not an acknowledged close.
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
		Summary:       "读取实际回答进程的生效时间维护：按结局的计数（与 effective_close_total 同源），以及除 close_acked 外每种结局最近 32 条（查询组、策略、时间、错误原文，截到 256 字节），用来一步读出 unavailable 等是哪个查询组、什么原因；可指定实例。",
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

// currentAndDraining reports whether the bundle's Control last found the
// Query Group in the current active set and in the draining projection at
// once; false without a Control that says.
func (m *effectiveMaintenance) currentAndDraining(qg execution.QueryGroupIdentity) bool {
	control, ok := m.bundle.dependencies.Control.(interface {
		CurrentAndDraining(execution.QueryGroupIdentity) bool
	})
	return ok && control.CurrentAndDraining(qg)
}
