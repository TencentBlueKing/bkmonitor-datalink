package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
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
	sourceOf             func(context.Context) (string, error)
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
func (m *effectiveMaintenance) step(ctx context.Context) {
	ctx, cancelStep := context.WithTimeout(ctx, 5*time.Second)
	defer cancelStep()
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
		round, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := runners[qg].withMaintenance(round, func(round context.Context, _ func(context.Context) error) error {
			return m.readGroup(round, qg, runners[qg])
		})
		cancel()
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
		m.closeInactive(ctx, qg, runner, known.closable)
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
	levels := plan.Levels()
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
// the send itself.
func (m *effectiveMaintenance) closeInactive(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plans []controlplane.MaintenancePlan) {
	if len(plans) == 0 {
		return
	}
	if m.sourceOf != nil {
		source, err := m.sourceOf(ctx)
		if err != nil {
			m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
			return
		}
		m.sourceID = source
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
		alerts := m.cache.ActiveAlerts(key)
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
		if len(alerts) == 0 {
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
		batch := make([]linkdoutput.CloseRequest, 0, m.capacity.CloseBatch)
		for _, alert := range alerts {
			if alert.EventSourceID != m.sourceID {
				continue
			}
			sentKey := key.TenantID + "\x00" + alert.EventSourceID + "\x00" + alert.AlertID
			if _, sent := m.sent[sentKey]; sent {
				continue
			}
			batch = append(batch, linkdoutput.CloseRequest{TenantID: key.TenantID, Fingerprint: alert.Fingerprint, AlertInstanceID: alert.AlertID,
				StrategyID: strategyID, StrategyRevision: ref.SnapshotRevision, BusinessID: business, OccurredAt: at})
			if len(batch) == remaining {
				break
			}
		}
		if len(batch) == 0 {
			continue
		}
		sent, err := m.sendClose(ctx, qg, runner, plan, key, batch)
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
// refusing, the boundary having moved to active, the producer failing.
func (m *effectiveMaintenance) sendClose(ctx context.Context, qg execution.QueryGroupIdentity, runner maintenanceRunner, plan controlplane.MaintenancePlan, key openalerts.StrategyKey, batch []linkdoutput.CloseRequest) (int, error) {
	sent := 0
	round, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := runner.withMaintenance(round, func(round context.Context, check func(context.Context) error) error {
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
		if err != nil || fact.Status() != strategy.EffectiveTimeInactive {
			return nil
		}
		// Sarama's synchronous ACK wait follows the existing producer timeout;
		// a context deadline cannot cancel a message already handed to Kafka.
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
	default:
		m.observe(ctx, qg, closeOutcomeUnavailable, err, 0)
	}
	if err != nil && !errors.Is(err, errMaintenanceBusy) && !errors.Is(err, errCloseSend) {
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
	errCloseSend     = errors.New("inactive close: send")
)

// refreshLegacy keeps the legacy entries of the Query Group's Plans warm:
// every Plan on a schedule without a frozen snapshot, within one bounded
// round. A Plan whose entries were read within the minute costs nothing
// here, so the round's store reads are the entries that are due, shared
// across the Plans that name the same business or calendar.
func (m *effectiveMaintenance) refreshLegacy(ctx context.Context, qg execution.QueryGroupIdentity, plans []controlplane.MaintenancePlan) {
	if m.legacyCache == nil || len(plans) == 0 {
		return
	}
	round, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
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
		levels := plan.Compiled.Levels()
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
func (m *effectiveMaintenance) observe(ctx context.Context, qg execution.QueryGroupIdentity, outcome string, err error, count int) {
	result := observability.ResultSuccess
	if err != nil {
		result = observability.ResultDegraded
	}
	m.count(outcome, max(count, 1))
	m.bundle.dependencies.Observer.Observe(ctx, observability.Observation{Component: observability.ComponentRuntime, Stage: observability.StageEffectiveTimeMaintenance, Result: observability.Result(result),
		ReasonCode: observability.ReasonCode(outcome), Trace: observability.TraceFields{QueryGroupKey: string(qg)}, Counts: observability.Counts{Events: int64(count)}, Err: err})
}
