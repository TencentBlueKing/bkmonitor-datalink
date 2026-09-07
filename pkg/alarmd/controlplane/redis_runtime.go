package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	alarmdprogress "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const scheduleTimelineSchemaVersion = "alarmd-control-schedule-timeline-v1"

var (
	ErrScheduleUnavailable = errors.New("alarmd controlplane: schedule unavailable")
	ErrScheduleConflict    = errors.New("alarmd controlplane: schedule activation conflict")
)

type FreezeSlotContractFailureClass string

const (
	FreezeSlotFailureScheduleRead       FreezeSlotContractFailureClass = "schedule_read"
	FreezeSlotFailureScheduleMismatch   FreezeSlotContractFailureClass = "schedule_mismatch"
	FreezeSlotFailureSnapshotRead       FreezeSlotContractFailureClass = "snapshot_read"
	FreezeSlotFailurePlanMaterialize    FreezeSlotContractFailureClass = "plan_materialize"
	FreezeSlotFailureInputClosure       FreezeSlotContractFailureClass = "input_closure"
	FreezeSlotFailureContractValidation FreezeSlotContractFailureClass = "contract_validation"
)

// FreezeSlotContractError identifies the bounded stage that rejected a frozen
// Slot. Error deliberately omits the underlying fact while Unwrap preserves it
// for errors.Is/errors.As and local tests.
type FreezeSlotContractError struct {
	Class FreezeSlotContractFailureClass
	Err   error
}

func (err *FreezeSlotContractError) Error() string {
	return "alarmd controlplane: FreezeSlotContract failed"
}

func (err *FreezeSlotContractError) Unwrap() error { return err.Err }

func freezeSlotContractError(class FreezeSlotContractFailureClass, err error) error {
	return &FreezeSlotContractError{Class: class, Err: err}
}

type DeterministicScheduleError struct{ Err error }

func (err *DeterministicScheduleError) Error() string {
	return fmt.Sprintf("alarmd controlplane: deterministic-invalid persisted Schedule: %v", err.Err)
}

func (err *DeterministicScheduleError) Unwrap() error { return err.Err }

type persistedScheduleSegment struct {
	Schedule         execution.FrozenQueryGroupSchedule `json:"schedule"`
	Plans            []PlanActivationRecord             `json:"plans"`
	ReactivatedAfter *execution.EvaluationTime          `json:"reactivated_after,omitempty"`
}

type persistedScheduleTimeline struct {
	SchemaVersion  string                       `json:"schema_version"`
	RecordRevision uint64                       `json:"record_revision"`
	QueryGroup     execution.QueryGroupIdentity `json:"query_group"`
	Segments       []persistedScheduleSegment   `json:"segments"`
	RetiredAt      *execution.EvaluationTime    `json:"retired_at,omitempty"`
}

type scheduleTimelineUpdate struct {
	expected []byte
	next     persistedScheduleTimeline
}

const compareAndSetInitialSchedulesScript = `
local header = redis.call('GET', KEYS[1])
if ARGV[1] == '' then
  if header then return 0 end
elseif not header or header ~= ARGV[1] then
  return 0
end
if redis.call('GET', KEYS[3]) ~= ARGV[4] then return 0 end
for index = 4, #KEYS do
  if redis.call('EXISTS', KEYS[index]) == 1 then return 0 end
end
redis.call('PEXPIRE', KEYS[3], ARGV[5])
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
for index = 4, #KEYS do
  redis.call('SET', KEYS[index], ARGV[index + 2])
end
return 1
`

const compareAndSetCutoverSchedulesScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
if redis.call('GET', KEYS[3]) ~= ARGV[4] then return 0 end
for index = 4, #KEYS do
  local current = redis.call('GET', KEYS[index])
  local expected_index = 2 * index - 2
  local expected = ARGV[expected_index]
  if expected == '' then
    if current then return 0 end
  elseif not current or current ~= expected then
    return 0
  end
end
redis.call('PEXPIRE', KEYS[3], ARGV[5])
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
for index = 4, #KEYS do
  local next_index = 2 * index - 1
  redis.call('SET', KEYS[index], ARGV[next_index])
end
return 1
`

func (repository *RedisCatalogRepository) persistActivationRefUpgrade(ctx context.Context, expected ActivationExpectation, next ActivationState, activePayload []byte) error {
	expectedHeader, err := activationHeader(expected.RecordRevision, expected.Current, expected.Pending)
	if err != nil {
		return err
	}
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	changed, err := repository.client.Eval(ctx, compareAndSetCutoverSchedulesScript,
		[]string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(next.ActiveQGSetRef.Digest)},
		expectedHeader, nextHeader, payload, activePayload, repository.ttl.Milliseconds()).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist activation ref upgrade: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

// CompareAndSetPublicationScheduleActivation advances one confirmed
// publication at one shared EvaluationTime. Every previously active Query
// Group is either cut over or retired, and every new Query Group is activated
// in the same Redis CAS as the Plan activation fact.
func (repository *RedisCatalogRepository) CompareAndSetPublicationScheduleActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) error {
	if repository == nil || repository.client == nil || boundary <= 0 {
		return errors.New("alarmd controlplane: publication schedule activation is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision == 0 {
		return errors.New("alarmd controlplane: publication schedule activation requires an existing activation")
	}
	previous, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	if !activationMatchesExpectation(previous, expected) {
		return ErrActivationConflict
	}
	if next.Pending != nil || next.Current.PublicationEpoch <= previous.Current.PublicationEpoch ||
		next.Current.SnapshotRevision == previous.Current.SnapshotRevision {
		return errors.New("alarmd controlplane: publication activation must advance to one new current publication")
	}
	newSnapshot, err := repository.LoadPublishedSnapshot(ctx, next.Current)
	if err != nil {
		return err
	}
	next.SchemaVersion = activationSchemaVersion

	newGroups, err := queryGroupMap(newSnapshot.QueryGroups)
	if err != nil {
		return err
	}
	oldSnapshot, err := repository.LoadPublishedSnapshot(ctx, previous.Current)
	var oldGroups map[execution.QueryGroupIdentity]QueryGroup
	if errors.Is(err, ErrSnapshotUnavailable) {
		if previous.SchemaVersion == activationSchemaVersion {
			identities, loadErr := repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef)
			if loadErr != nil {
				return loadErr
			}
			candidates := make(map[execution.QueryGroupIdentity]QueryGroup, len(identities))
			for _, identity := range identities {
				candidates[identity] = QueryGroup{Identity: identity}
			}
			oldGroups, err = repository.loadActivatedGroupsFromOpenSchedules(ctx, previous, candidates)
		} else {
			oldGroups, err = repository.loadActivatedGroupsFromScheduleScan(ctx, previous)
		}
	} else if err == nil {
		oldGroups, err = queryGroupMap(oldSnapshot.QueryGroups)
	}
	if err != nil {
		return err
	}
	reactivating := make(map[execution.QueryGroupIdentity]struct{})
	for _, draining := range previous.Draining {
		if _, reappeared := newGroups[draining.QueryGroup]; !reappeared {
			continue
		}
		reactivatable, err := repository.drainingReactivatable(ctx, draining, boundary, progress)
		if err != nil {
			return err
		}
		if !reactivatable {
			return ErrReactivationNotDrained
		}
		reactivating[draining.QueryGroup] = struct{}{}
	}
	wantedDraining, err := expectedDrainingProjection(
		previous.Draining, oldGroups, newGroups, reactivating, boundary, repository.drainingRetirement(ctx, progress, boundary),
	)
	if err != nil {
		return err
	}
	if !sameDrainingProjection(wantedDraining, next.Draining) {
		return errors.New("alarmd controlplane: publication activation has an invalid draining projection")
	}

	updates := make([]scheduleTimelineUpdate, 0, len(oldGroups)+len(newGroups))
	coverage := make([]persistedScheduleTimeline, 0, len(newGroups))
	for queryGroup, oldGroup := range oldGroups {
		timeline, raw, err := repository.loadScheduleTimeline(ctx, queryGroup)
		if err != nil {
			return err
		}
		last := len(timeline.Segments) - 1
		if last < 0 || timeline.RetiredAt != nil {
			return ErrScheduleConflict
		}
		open := timeline.Segments[last]
		if open.Schedule.Segment.End != nil || open.Schedule.Segment.Start >= boundary ||
			open.Schedule.Segment.Publication.SnapshotRevision != previous.Current.SnapshotRevision ||
			open.Schedule.Segment.Publication.PublicationEpoch != execution.PublicationEpoch(previous.Current.PublicationEpoch) ||
			open.Schedule.Segment.QueryRevision != oldGroup.QueryPlan.QueryRevision ||
			open.Schedule.Segment.ScheduleRevision != oldGroup.ScheduleRevision {
			return ErrScheduleConflict
		}
		if err := validateOpenSegmentActivation(previous, open); err != nil {
			return err
		}
		closed := open.Schedule
		closed.Segment.End = &boundary
		if err := closed.Validate(); err != nil {
			return err
		}
		timeline.Segments[last].Schedule = closed
		timeline.RecordRevision++
		if newGroup, remains := newGroups[queryGroup]; remains {
			opened, err := repository.materializeSchedule(ctx, scheduleSegmentForGroup(newSnapshot.Publication, newGroup, boundary))
			if err != nil {
				return err
			}
			records, err := activationRecordsForSchedule(next, opened)
			if err != nil {
				return err
			}
			timeline.Segments = append(timeline.Segments, persistedScheduleSegment{Schedule: opened, Plans: records})
			coverage = append(coverage, persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
				RecordRevision: 1, QueryGroup: queryGroup,
				Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records}}})
		} else {
			retiredAt := boundary
			timeline.RetiredAt = &retiredAt
		}
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
	}
	for queryGroup, newGroup := range newGroups {
		if _, existed := oldGroups[queryGroup]; existed {
			continue
		}
		opened, err := repository.materializeSchedule(ctx, scheduleSegmentForGroup(newSnapshot.Publication, newGroup, boundary))
		if err != nil {
			return err
		}
		records, err := activationRecordsForSchedule(next, opened)
		if err != nil {
			return err
		}
		var timeline persistedScheduleTimeline
		var raw []byte
		if _, reactivated := reactivating[queryGroup]; reactivated {
			timeline, raw, err = repository.loadScheduleTimeline(ctx, queryGroup)
			if err != nil {
				return err
			}
			if timeline.RetiredAt == nil || *timeline.RetiredAt >= boundary {
				return ErrScheduleConflict
			}
			retiredAt := *timeline.RetiredAt
			timeline.RetiredAt = nil
			timeline.RecordRevision++
			timeline.Segments = append(timeline.Segments, persistedScheduleSegment{
				Schedule: opened, Plans: records, ReactivatedAfter: &retiredAt,
			})
		} else {
			timeline = persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
				RecordRevision: 1, QueryGroup: queryGroup,
				Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records}}}
		}
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
		coverage = append(coverage, persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
			RecordRevision: 1, QueryGroup: queryGroup,
			Segments: []persistedScheduleSegment{{Schedule: opened, Plans: records}}})
	}
	if len(coverage) == 0 && len(next.Plans) != 0 {
		return errors.New("alarmd controlplane: empty publication Schedule coverage")
	}
	if len(coverage) > 0 {
		if err := validateInitialActivationCoverage(next, coverage); err != nil {
			return err
		}
	}
	return repository.persistCutoverActivation(ctx, expected, next, updates)
}

// CompareAndSetInitialScheduleActivation establishes zero or more first
// Schedule Segments together with their Plan activation facts. Module 02 owns
// the choice of facts; this method only validates and atomically persists them.
func (repository *RedisCatalogRepository) CompareAndSetInitialScheduleActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	facts []execution.InitialScheduleActivationFact,
) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: initial schedule activation is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision != 0 {
		return errors.New("alarmd controlplane: initial schedule activation requires empty persisted state")
	}
	next.SchemaVersion = activationSchemaVersion
	timelines := make([]persistedScheduleTimeline, 0, len(facts))
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(facts))
	for _, fact := range facts {
		schedule, err := repository.materializeSchedule(ctx, fact.Segment)
		if err != nil {
			return err
		}
		if err := fact.Validate(schedule); err != nil {
			return err
		}
		if _, duplicate := seen[fact.Segment.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate initial Query Group schedule")
		}
		seen[fact.Segment.QueryGroup] = struct{}{}
		records, err := activationRecordsForSchedule(next, schedule)
		if err != nil {
			return err
		}
		timelines = append(timelines, persistedScheduleTimeline{SchemaVersion: scheduleTimelineSchemaVersion,
			RecordRevision: 1, QueryGroup: fact.Segment.QueryGroup,
			Segments: []persistedScheduleSegment{{Schedule: schedule, Plans: records}}})
	}
	if err := validateInitialActivationCoverage(next, timelines); err != nil {
		return err
	}
	return repository.persistInitialActivation(ctx, expected, next, timelines)
}

// CompareAndSetScheduleCutover atomically closes each old Segment, opens its
// adjacent successor and advances the shared Plan activation record.
func (repository *RedisCatalogRepository) CompareAndSetScheduleCutover(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	facts []execution.ScheduleCutoverFact,
) error {
	if repository == nil || repository.client == nil || len(facts) == 0 {
		return errors.New("alarmd controlplane: schedule cutover is required")
	}
	if err := validateActivationTransition(expected, next); err != nil {
		return err
	}
	if expected.RecordRevision == 0 {
		return errors.New("alarmd controlplane: schedule cutover requires an existing activation")
	}
	previous, err := repository.LoadActivation(ctx)
	if err != nil {
		return err
	}
	if !activationMatchesExpectation(previous, expected) {
		return ErrActivationConflict
	}
	next.SchemaVersion = activationSchemaVersion
	updates := make([]scheduleTimelineUpdate, 0, len(facts))
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(facts))
	affectedPlans := make(map[execution.PlanIdentity]struct{})
	for _, fact := range facts {
		if _, duplicate := seen[fact.NewSegment.QueryGroup]; duplicate {
			return errors.New("alarmd controlplane: duplicate Query Group cutover")
		}
		seen[fact.NewSegment.QueryGroup] = struct{}{}
		oldSchedule, err := repository.materializeSchedule(ctx, fact.OldSegment)
		if err != nil {
			return err
		}
		newSchedule, err := repository.materializeSchedule(ctx, fact.NewSegment)
		if err != nil {
			return err
		}
		if err := fact.Validate(oldSchedule, newSchedule); err != nil {
			return err
		}
		for _, plan := range oldSchedule.Plans {
			affectedPlans[plan.Identity] = struct{}{}
		}
		for _, plan := range newSchedule.Plans {
			affectedPlans[plan.Identity] = struct{}{}
		}
		timeline, raw, err := repository.loadScheduleTimeline(ctx, fact.OldSegment.QueryGroup)
		if err != nil {
			return err
		}
		last := len(timeline.Segments) - 1
		if last < 0 || !sameFrozenSchedule(timeline.Segments[last].Schedule, execution.FrozenQueryGroupSchedule{
			Segment: openVersion(fact.OldSegment), Plans: oldSchedule.Plans,
		}) {
			return ErrScheduleConflict
		}
		if err := validateOpenSegmentActivation(previous, timeline.Segments[last]); err != nil {
			return err
		}
		timeline.Segments[last].Schedule = oldSchedule
		records, err := activationRecordsForSchedule(next, newSchedule)
		if err != nil {
			return err
		}
		timeline.Segments = append(timeline.Segments, persistedScheduleSegment{Schedule: newSchedule, Plans: records})
		timeline.RecordRevision++
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		updates = append(updates, scheduleTimelineUpdate{expected: raw, next: timeline})
	}
	if err := validateUnchangedActivationRecords(previous, next, affectedPlans); err != nil {
		return err
	}
	return repository.persistCutoverActivation(ctx, expected, next, updates)
}

func validateInitialActivationCoverage(state ActivationState, timelines []persistedScheduleTimeline) error {
	wanted, err := activationRecordMap(state.Plans)
	if err != nil {
		return err
	}
	covered := make(map[execution.PlanIdentity]PlanActivationRecord, len(wanted))
	for _, timeline := range timelines {
		for _, record := range timeline.Segments[0].Plans {
			if _, duplicate := covered[record.Fact.Plan]; duplicate {
				return errors.New("alarmd controlplane: initial Schedule Segments contain duplicate Plan activation")
			}
			covered[record.Fact.Plan] = record
		}
	}
	if len(covered) != len(wanted) {
		return errors.New("alarmd controlplane: initial Schedule Segments must exactly cover Plan activation")
	}
	for identity, record := range wanted {
		if covered[identity] != record {
			return errors.New("alarmd controlplane: initial Schedule Segments must exactly cover Plan activation")
		}
	}
	return nil
}

func activationMatchesExpectation(state ActivationState, expected ActivationExpectation) bool {
	if state.RecordRevision != expected.RecordRevision || state.Current != expected.Current {
		return false
	}
	if state.Pending == nil || expected.Pending == nil {
		return state.Pending == nil && expected.Pending == nil
	}
	return *state.Pending == *expected.Pending
}

func validateOpenSegmentActivation(state ActivationState, segment persistedScheduleSegment) error {
	current, err := activationRecordMap(state.Plans)
	if err != nil {
		return err
	}
	for _, record := range segment.Plans {
		if current[record.Fact.Plan] != record {
			return errors.New("alarmd controlplane: open Schedule Segment differs from current Plan activation")
		}
	}
	return nil
}

func validateUnchangedActivationRecords(previous, next ActivationState, affected map[execution.PlanIdentity]struct{}) error {
	before, err := activationRecordMap(previous.Plans)
	if err != nil {
		return err
	}
	after, err := activationRecordMap(next.Plans)
	if err != nil {
		return err
	}
	for identity, record := range before {
		if _, changed := affected[identity]; changed {
			continue
		}
		if after[identity] != record {
			return errors.New("alarmd controlplane: activation changed outside affected Query Groups")
		}
	}
	for identity, record := range after {
		if _, changed := affected[identity]; changed {
			continue
		}
		if before[identity] != record {
			return errors.New("alarmd controlplane: activation changed outside affected Query Groups")
		}
	}
	return nil
}

func activationRecordMap(records []PlanActivationRecord) (map[execution.PlanIdentity]PlanActivationRecord, error) {
	result := make(map[execution.PlanIdentity]PlanActivationRecord, len(records))
	for _, record := range records {
		if _, duplicate := result[record.Fact.Plan]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate Plan activation")
		}
		result[record.Fact.Plan] = record
	}
	return result, nil
}

func validateActivationTransition(expected ActivationExpectation, next ActivationState) error {
	if err := validateActivationState(next); err != nil {
		return err
	}
	if next.RecordRevision != expected.RecordRevision+1 {
		return errors.New("alarmd controlplane: activation record revision must advance by one")
	}
	if expected.RecordRevision == 0 && (expected.Current != (SnapshotPublicationRef{}) || expected.Pending != nil) {
		return errors.New("alarmd controlplane: initial activation expectation must be empty")
	}
	return nil
}

func (repository *RedisCatalogRepository) persistInitialActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	timelines []persistedScheduleTimeline,
) error {
	identities := make([]execution.QueryGroupIdentity, 0, len(timelines))
	for _, timeline := range timelines {
		identities = append(identities, timeline.QueryGroup)
	}
	ref, activePayload, err := repository.persistAndVerifyActiveQGSet(ctx, identities)
	if err != nil {
		return err
	}
	next.ActiveQGSetRef = ref
	next.SchemaVersion = activationSchemaVersion
	expectedHeader := ""
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	activationPayload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	keys := []string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(ref.Digest)}
	args := []interface{}{expectedHeader, nextHeader, activationPayload, activePayload, repository.ttl.Milliseconds()}
	sort.Slice(timelines, func(i, j int) bool { return timelines[i].QueryGroup < timelines[j].QueryGroup })
	for _, timeline := range timelines {
		if err := validateScheduleTimeline(timeline); err != nil {
			return err
		}
		payload, err := json.Marshal(timeline)
		if err != nil {
			return err
		}
		keys = append(keys, repository.scheduleTimelineKey(timeline.QueryGroup))
		args = append(args, payload)
	}
	changed, err := repository.client.Eval(ctx, compareAndSetInitialSchedulesScript, keys, args...).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist initial schedule activation: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

func (repository *RedisCatalogRepository) persistCutoverActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	updates []scheduleTimelineUpdate,
) error {
	active := make(map[execution.QueryGroupIdentity]struct{})
	previous, loadErr := repository.LoadActivation(ctx)
	if loadErr != nil {
		return loadErr
	}
	if previous.SchemaVersion == activationSchemaVersion {
		current, activeErr := repository.LoadActiveQueryGroupSet(ctx, previous.ActiveQGSetRef)
		if activeErr != nil {
			return activeErr
		}
		for _, identity := range current {
			active[identity] = struct{}{}
		}
	} else {
		return errors.New("alarmd controlplane: legacy Activation must be upgraded before cutover persistence")
	}
	for _, update := range updates {
		if update.next.RetiredAt == nil {
			active[update.next.QueryGroup] = struct{}{}
		} else {
			delete(active, update.next.QueryGroup)
		}
	}
	identities := make([]execution.QueryGroupIdentity, 0, len(active))
	for identity := range active {
		identities = append(identities, identity)
	}
	ref, activePayload, err := repository.persistAndVerifyActiveQGSet(ctx, identities)
	if err != nil {
		return err
	}
	next.ActiveQGSetRef = ref
	next.SchemaVersion = activationSchemaVersion
	expectedHeader, err := activationHeader(expected.RecordRevision, expected.Current, expected.Pending)
	if err != nil {
		return err
	}
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	activationPayload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].next.QueryGroup < updates[j].next.QueryGroup })
	keys := []string{repository.activationHeaderKey(), repository.activationKey(), repository.activeQGSetKey(ref.Digest)}
	args := []interface{}{expectedHeader, nextHeader, activationPayload, activePayload, repository.ttl.Milliseconds()}
	for _, update := range updates {
		payload, err := json.Marshal(update.next)
		if err != nil {
			return err
		}
		keys = append(keys, repository.scheduleTimelineKey(update.next.QueryGroup))
		args = append(args, update.expected, payload)
	}
	changed, err := repository.client.Eval(ctx, compareAndSetCutoverSchedulesScript, keys, args...).Int()
	if err != nil {
		return activationDependencyIO(fmt.Errorf("persist schedule cutover: %w", err))
	}
	if changed != 1 {
		return ErrActivationConflict
	}
	return nil
}

func (repository *RedisCatalogRepository) materializeSchedule(
	ctx context.Context,
	segment execution.ScheduleSegmentFact,
) (execution.FrozenQueryGroupSchedule, error) {
	if err := segment.Validate(); err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	publication := SnapshotPublicationRef{SnapshotRevision: segment.Publication.SnapshotRevision,
		PublicationEpoch: uint64(segment.Publication.PublicationEpoch)}
	group, err := repository.loadPublishedQueryGroup(ctx, publication, segment.QueryGroup)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	if group.QueryPlan.QueryRevision != segment.QueryRevision || group.ScheduleRevision != segment.ScheduleRevision {
		return execution.FrozenQueryGroupSchedule{}, errors.New("alarmd controlplane: Schedule Segment differs from frozen Catalog")
	}
	plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		plans[index] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
	}
	schedule := execution.FrozenQueryGroupSchedule{Segment: segment, Plans: plans}
	if err := schedule.Validate(); err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return schedule, nil
}

func activationRecordsForSchedule(state ActivationState, schedule execution.FrozenQueryGroupSchedule) ([]PlanActivationRecord, error) {
	byPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(state.Plans))
	for _, record := range state.Plans {
		byPlan[record.Fact.Plan] = record
	}
	records := make([]PlanActivationRecord, len(schedule.Plans))
	for index, plan := range schedule.Plans {
		record, ok := byPlan[plan.Identity]
		if !ok || record.Publication.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
			execution.PublicationEpoch(record.Publication.PublicationEpoch) != schedule.Segment.Publication.PublicationEpoch ||
			record.Fact.Selected.ScheduleRevision != plan.ScheduleRevision {
			return nil, errors.New("alarmd controlplane: Plan activation differs from frozen Schedule Segment")
		}
		records[index] = record
	}
	return records, nil
}

func openVersion(segment execution.ScheduleSegmentFact) execution.ScheduleSegmentFact {
	segment.End = nil
	return segment
}

func scheduleSegmentForGroup(
	publication SnapshotPublicationRef,
	group QueryGroup,
	start execution.EvaluationTime,
) execution.ScheduleSegmentFact {
	return execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision,
			PublicationEpoch: execution.PublicationEpoch(publication.PublicationEpoch)},
		QueryGroup: group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, Start: start,
	}
}

func queryGroupMap(groups []QueryGroup) (map[execution.QueryGroupIdentity]QueryGroup, error) {
	result := make(map[execution.QueryGroupIdentity]QueryGroup, len(groups))
	for _, group := range groups {
		if group.Identity == "" {
			return nil, errors.New("alarmd controlplane: Snapshot contains an empty Query Group")
		}
		if _, duplicate := result[group.Identity]; duplicate {
			return nil, errors.New("alarmd controlplane: Snapshot contains a duplicate Query Group")
		}
		result[group.Identity] = group
	}
	return result, nil
}

// loadActivatedGroupsFromOpenSchedules recovers only the old group facts
// needed to close an activation boundary. It is intentionally limited to
// Query Groups also present in the new publication: every active Plan must be
// covered by one immutable open Schedule Segment from the old publication.
// If that exact coverage cannot be proven, the missing Snapshot remains
// unavailable instead of guessing from the new source.
func (repository *RedisCatalogRepository) loadActivatedGroupsFromOpenSchedules(
	ctx context.Context,
	activation ActivationState,
	candidates map[execution.QueryGroupIdentity]QueryGroup,
) (map[execution.QueryGroupIdentity]QueryGroup, error) {
	expected, err := activationRecordMap(activation.Plans)
	if err != nil {
		return nil, err
	}
	covered := make(map[execution.PlanIdentity]struct{}, len(expected))
	groups := make(map[execution.QueryGroupIdentity]QueryGroup)
	identities := make([]execution.QueryGroupIdentity, 0, len(candidates))
	for identity := range candidates {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i] < identities[j] })
	for _, identity := range identities {
		timeline, _, loadErr := repository.loadScheduleTimeline(ctx, identity)
		if errors.Is(loadErr, ErrScheduleUnavailable) {
			continue
		}
		if loadErr != nil {
			return nil, loadErr
		}
		last := len(timeline.Segments) - 1
		if last < 0 || timeline.RetiredAt != nil {
			continue
		}
		open := timeline.Segments[last]
		if open.Schedule.Segment.End != nil ||
			open.Schedule.Segment.Publication.SnapshotRevision != activation.Current.SnapshotRevision ||
			open.Schedule.Segment.Publication.PublicationEpoch != execution.PublicationEpoch(activation.Current.PublicationEpoch) {
			continue
		}
		if err := validateOpenSegmentActivation(activation, open); err != nil {
			return nil, err
		}
		for _, record := range open.Plans {
			if expected[record.Fact.Plan] != record {
				return nil, ErrSnapshotUnavailable
			}
			if _, duplicate := covered[record.Fact.Plan]; duplicate {
				return nil, ErrSnapshotUnavailable
			}
			covered[record.Fact.Plan] = struct{}{}
		}
		groups[identity] = QueryGroup{
			Identity: identity,
			QueryPlan: execution.QueryPlanFacts{
				QueryRevision: open.Schedule.Segment.QueryRevision,
			},
			ScheduleRevision: open.Schedule.Segment.ScheduleRevision,
		}
	}
	if len(covered) != len(expected) {
		return nil, ErrSnapshotUnavailable
	}
	return groups, nil
}

// loadActivatedGroupsFromScheduleScan is the one-time v1 migration fallback.
// It is never called for a v2 Activation and is bounded independently of the
// Redis SCAN COUNT hint.
func (repository *RedisCatalogRepository) loadActivatedGroupsFromScheduleScan(ctx context.Context, activation ActivationState) (groupsResult map[execution.QueryGroupIdentity]QueryGroup, resultErr error) {
	started := time.Now()
	scanned := 0
	defer func() {
		result := "success"
		reason := "none"
		observationResult := observability.Result(observability.ResultSuccess)
		observationReason := observability.ReasonNone
		if resultErr != nil {
			result, reason, observationResult = "fail_closed", "contract", observability.ResultFailed
			observationReason = observability.ReasonContractDeterministic
			if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
				result, reason = "canceled", "dependency"
				observationReason = observability.ReasonContractRetryable
			}
		}
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageLegacyQGMigration,
			Result: observationResult, ReasonCode: observationReason, LegacyMigration: &observability.LegacyQGMigrationFacts{Result: result, ReasonClass: reason, ScanKeys: scanned, Duration: time.Since(started)}})
	}()
	if repository.legacyMigrationMaxScanKeys <= 0 || repository.legacyMigrationTimeout <= 0 {
		return nil, errors.New("alarmd controlplane: legacy migration bounds are required")
	}
	migrationCtx, cancel := context.WithTimeout(ctx, repository.legacyMigrationTimeout)
	defer cancel()
	expected, err := activationRecordMap(activation.Plans)
	if err != nil {
		return nil, err
	}
	covered := make(map[execution.PlanIdentity]struct{}, len(expected))
	groups := make(map[execution.QueryGroupIdentity]QueryGroup)
	var cursor uint64
	pattern := repository.prefix + ":schedule_timeline:*"
	for {
		keys, next, scanErr := repository.client.Scan(migrationCtx, cursor, pattern, 500).Result()
		if scanErr != nil {
			return nil, activationDependencyIO(scanErr)
		}
		scanned += len(keys)
		if scanned > repository.legacyMigrationMaxScanKeys {
			return nil, errors.New("alarmd controlplane: legacy active Query Group migration scan limit exceeded")
		}
		for _, key := range keys {
			identity := execution.QueryGroupIdentity(strings.TrimPrefix(key, repository.prefix+":schedule_timeline:"))
			timeline, _, loadErr := repository.loadScheduleTimeline(migrationCtx, identity)
			if loadErr != nil {
				return nil, loadErr
			}
			if timeline.RetiredAt != nil || len(timeline.Segments) == 0 {
				continue
			}
			open := timeline.Segments[len(timeline.Segments)-1]
			if open.Schedule.Segment.End != nil {
				return nil, ErrSnapshotUnavailable
			}
			if open.Schedule.Segment.Publication.SnapshotRevision != activation.Current.SnapshotRevision || uint64(open.Schedule.Segment.Publication.PublicationEpoch) != activation.Current.PublicationEpoch {
				return nil, ErrSnapshotUnavailable
			}
			if err := validateOpenSegmentActivation(activation, open); err != nil {
				return nil, err
			}
			for _, record := range open.Plans {
				if expected[record.Fact.Plan] != record {
					return nil, ErrSnapshotUnavailable
				}
				if _, duplicate := covered[record.Fact.Plan]; duplicate {
					return nil, ErrSnapshotUnavailable
				}
				covered[record.Fact.Plan] = struct{}{}
			}
			groups[identity] = QueryGroup{Identity: identity, QueryPlan: execution.QueryPlanFacts{QueryRevision: open.Schedule.Segment.QueryRevision}, ScheduleRevision: open.Schedule.Segment.ScheduleRevision}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(covered) != len(expected) {
		return nil, ErrSnapshotUnavailable
	}
	return groups, nil
}

// DrainingTerminationWindow is the age past its retirement boundary after
// which a draining Query Group can no longer execute any Slot: every Slot
// before the boundary is then older than the replay age, so neither a normal
// tick nor a replay can still pick it up. The doubled replay age keeps the
// window clear of one replay window worth of clock and delivery skew.
func DrainingTerminationWindow(maxReplayAge time.Duration) time.Duration {
	if maxReplayAge <= 0 {
		return 0
	}
	return 2 * maxReplayAge
}

// DrainingQueryGroupTerminated reports whether the draining Query Group is
// past its termination window at now. A non-positive window never terminates.
func DrainingQueryGroupTerminated(draining DrainingQueryGroup, now execution.EvaluationTime, window time.Duration) bool {
	if window <= 0 || draining.RetiredBoundary <= 0 || now <= draining.RetiredBoundary {
		return false
	}
	return time.Duration(now-draining.RetiredBoundary)*time.Second > window
}

// drainingReactivatable reports whether a previously draining Query Group
// that reappears in the new Snapshot may be reactivated at boundary. Past the
// termination window the entry is retired by rule: nothing can execute its
// retired Slots anymore, so waiting for Progress that cannot advance would
// block every later activation of that Query Group. Inside the window the
// Query Group must have drained, which needs the single Progress reader. The
// reconciler and the CAS side take the same decision from the same inputs.
func (repository *RedisCatalogRepository) drainingReactivatable(
	ctx context.Context,
	draining DrainingQueryGroup,
	boundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (bool, error) {
	if DrainingQueryGroupTerminated(draining, boundary, repository.drainingRetireAfter) {
		return true, nil
	}
	if progress == nil {
		return false, errors.New("alarmd controlplane: reactivation requires the single Progress reader")
	}
	return repository.queryGroupDrained(ctx, draining.QueryGroup, draining.RetiredBoundary, progress)
}

// drainingRetirement decides whether one previously draining Query Group may
// leave the persisted Draining projection at boundary. A terminated entry is
// dropped by age alone; otherwise the entry is dropped only when the Progress
// reader confirms it drained. Every read failure keeps the entry, so pruning
// never blocks an activation and the age bound remains the only guarantee.
func (repository *RedisCatalogRepository) drainingRetirement(
	ctx context.Context,
	progress ScheduleActivationProgressReader,
	boundary execution.EvaluationTime,
) func(DrainingQueryGroup) bool {
	return func(draining DrainingQueryGroup) bool {
		if DrainingQueryGroupTerminated(draining, boundary, repository.drainingRetireAfter) {
			return true
		}
		if progress == nil {
			return false
		}
		drained, err := repository.queryGroupDrained(ctx, draining.QueryGroup, draining.RetiredBoundary, progress)
		return err == nil && drained
	}
}

// expectedDrainingProjection derives the Draining list of the next activation.
// retired, when non-nil, drops previous entries that no longer carry execution
// meaning; it must be the same decision on the reconciler and the CAS side.
func expectedDrainingProjection(
	previous []DrainingQueryGroup,
	oldGroups map[execution.QueryGroupIdentity]QueryGroup,
	newGroups map[execution.QueryGroupIdentity]QueryGroup,
	reactivating map[execution.QueryGroupIdentity]struct{},
	boundary execution.EvaluationTime,
	retired func(DrainingQueryGroup) bool,
) ([]DrainingQueryGroup, error) {
	byGroup := make(map[execution.QueryGroupIdentity]DrainingQueryGroup, len(previous)+len(oldGroups))
	for _, draining := range previous {
		if _, reactivated := reactivating[draining.QueryGroup]; reactivated {
			continue
		}
		if _, duplicate := byGroup[draining.QueryGroup]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate previous draining Query Group")
		}
		if retired != nil && retired(draining) {
			continue
		}
		byGroup[draining.QueryGroup] = draining
	}
	for queryGroup := range oldGroups {
		if _, remains := newGroups[queryGroup]; remains {
			continue
		}
		if _, alreadyDraining := byGroup[queryGroup]; alreadyDraining {
			return nil, errors.New("alarmd controlplane: active Query Group is already draining")
		}
		byGroup[queryGroup] = DrainingQueryGroup{QueryGroup: queryGroup, RetiredBoundary: boundary}
	}
	result := make([]DrainingQueryGroup, 0, len(byGroup))
	for _, draining := range byGroup {
		result = append(result, draining)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QueryGroup < result[j].QueryGroup })
	return result, nil
}

func sameDrainingProjection(left, right []DrainingQueryGroup) bool {
	if len(left) != len(right) {
		return false
	}
	ordered := append([]DrainingQueryGroup(nil), right...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].QueryGroup < ordered[j].QueryGroup })
	for index := range left {
		if left[index] != ordered[index] {
			return false
		}
	}
	return true
}

func (repository *RedisCatalogRepository) queryGroupDrained(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	retiredBoundary execution.EvaluationTime,
	progress ScheduleActivationProgressReader,
) (bool, error) {
	timeline, _, err := repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return false, err
	}
	if timeline.RetiredAt == nil || *timeline.RetiredAt != retiredBoundary {
		return false, errors.New("alarmd controlplane: draining projection differs from Schedule retirement")
	}
	identity := execution.ProgressIdentity{QueryGroup: queryGroup}
	load, err := progress.LoadProgress(ctx, identity)
	if err != nil {
		var deterministicControlFact interface{ DeterministicControlFact() }
		if errors.As(err, &deterministicControlFact) {
			return false, err
		}
		return false, activationDependencyIO(err)
	}
	if err := load.Validate(identity); err != nil {
		return false, &alarmdprogress.DeterministicInvalidError{Err: err}
	}
	if load.Status == execution.ProgressFound {
		return load.Progress.UnfinishedRange == nil && load.Progress.NextSlot >= retiredBoundary, nil
	}
	for _, segment := range timeline.Segments {
		if _, hasSlot := segment.Schedule.FirstSlot(); hasSlot {
			return false, nil
		}
	}
	return true, nil
}

func sameFrozenSchedule(left, right execution.FrozenQueryGroupSchedule) bool {
	leftPayload, leftErr := json.Marshal(left)
	rightPayload, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftPayload) == string(rightPayload)
}

func validateScheduleTimeline(timeline persistedScheduleTimeline) error {
	if timeline.SchemaVersion != scheduleTimelineSchemaVersion || timeline.RecordRevision == 0 ||
		timeline.QueryGroup == "" || len(timeline.Segments) == 0 {
		return errors.New("alarmd controlplane: incomplete persisted Schedule timeline")
	}
	for index, segment := range timeline.Segments {
		if err := segment.Schedule.Validate(); err != nil || segment.Schedule.Segment.QueryGroup != timeline.QueryGroup {
			return errors.New("alarmd controlplane: invalid persisted Schedule Segment")
		}
		if _, err := activationRecordsForPersistedSchedule(segment.Plans, segment.Schedule); err != nil {
			return err
		}
		if index == 0 && segment.ReactivatedAfter != nil {
			return errors.New("alarmd controlplane: initial Schedule Segment cannot be a reactivation")
		}
		if index+1 < len(timeline.Segments) {
			nextRecord := timeline.Segments[index+1]
			next := nextRecord.Schedule.Segment
			if segment.Schedule.Segment.End == nil {
				return errors.New("alarmd controlplane: persisted Schedule Segments overlap")
			}
			end := *segment.Schedule.Segment.End
			if end == next.Start {
				if nextRecord.ReactivatedAfter != nil {
					return errors.New("alarmd controlplane: adjacent Schedule Segment cannot carry a reactivation tombstone")
				}
			} else if nextRecord.ReactivatedAfter == nil || *nextRecord.ReactivatedAfter != end || next.Start <= end {
				return errors.New("alarmd controlplane: persisted Schedule Segments are neither adjacent nor tombstone-reactivated")
			}
		} else if timeline.RetiredAt == nil {
			if segment.Schedule.Segment.End != nil {
				return errors.New("alarmd controlplane: final persisted Schedule Segment must remain open")
			}
		} else if segment.Schedule.Segment.End == nil || *segment.Schedule.Segment.End != *timeline.RetiredAt {
			return errors.New("alarmd controlplane: retired Schedule timeline must close exactly at its retirement boundary")
		}
	}
	return nil
}

func activationRecordsForPersistedSchedule(records []PlanActivationRecord, schedule execution.FrozenQueryGroupSchedule) ([]PlanActivationRecord, error) {
	if len(records) != len(schedule.Plans) {
		return nil, errors.New("alarmd controlplane: persisted Plan activation cardinality differs from schedule")
	}
	byPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(records))
	for _, record := range records {
		if _, duplicate := byPlan[record.Fact.Plan]; duplicate {
			return nil, errors.New("alarmd controlplane: duplicate persisted Plan activation")
		}
		byPlan[record.Fact.Plan] = record
	}
	for _, plan := range schedule.Plans {
		record, ok := byPlan[plan.Identity]
		if !ok || record.Fact.Selected.ScheduleRevision != plan.ScheduleRevision ||
			record.Publication.SnapshotRevision != schedule.Segment.Publication.SnapshotRevision ||
			execution.PublicationEpoch(record.Publication.PublicationEpoch) != schedule.Segment.Publication.PublicationEpoch {
			return nil, errors.New("alarmd controlplane: persisted Plan activation differs from Schedule Segment")
		}
	}
	return records, nil
}

// loadScheduleTimeline reads the small activation header live and serves the
// timeline bytes cached under that header when present. Callers that already
// hold the header for the same authorization use loadScheduleTimelineAt.
func (repository *RedisCatalogRepository) loadScheduleTimeline(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, []byte, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, nil, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		return persistedScheduleTimeline{}, nil, err
	}
	return repository.loadScheduleTimelineAt(ctx, queryGroup, version)
}

func decodeScheduleTimeline(queryGroup execution.QueryGroupIdentity, payload []byte) (persistedScheduleTimeline, error) {
	var timeline persistedScheduleTimeline
	if err := json.Unmarshal(payload, &timeline); err != nil {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{
			Err: fmt.Errorf("decode Schedule timeline: %w", err),
		}
	}
	if timeline.QueryGroup != queryGroup {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{
			Err: errors.New("persisted Schedule timeline identity mismatch"),
		}
	}
	if err := validateScheduleTimeline(timeline); err != nil {
		return persistedScheduleTimeline{}, &DeterministicScheduleError{Err: err}
	}
	return timeline, nil
}

func (repository *RedisCatalogRepository) scheduleTimelineKey(queryGroup execution.QueryGroupIdentity) string {
	return repository.prefix + ":schedule_timeline:" + string(queryGroup)
}

type RuntimePlanCompiler interface {
	Compile(context.Context, strategy.CompileRequest) (strategy.CompileResult, error)
}

// RedisCatalogRuntime adapts the persisted Catalog and Schedule facts to the
// Scheduler and Progress ports. It owns no local cursor or activation state.
type RedisCatalogRuntime struct {
	repository        *RedisCatalogRepository
	compiler          RuntimePlanCompiler
	stateSemantics    strategy.StateSemantics
	downstreamReserve time.Duration
}

func NewRedisCatalogRuntime(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	downstreamReserve time.Duration,
) (*RedisCatalogRuntime, error) {
	if repository == nil || compiler == nil || downstreamReserve <= 0 ||
		stateSemantics.StateSchemaVersion == "" || stateSemantics.CodecSemanticsVersion == "" ||
		stateSemantics.IdentitySchemaDigest == "" || stateSemantics.SourceTimeSemanticsVersion == "" ||
		stateSemantics.HistoryCellSemanticsVersion == "" {
		return nil, errors.New("alarmd controlplane: invalid Redis Catalog runtime")
	}
	return &RedisCatalogRuntime{repository: repository, compiler: compiler,
		stateSemantics: stateSemantics, downstreamReserve: downstreamReserve}, nil
}

func (runtime *RedisCatalogRuntime) ReadInitialFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.FrozenQueryGroupSchedule, error) {
	timeline, _, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return timeline.Segments[0].Schedule, nil
}

func (runtime *RedisCatalogRuntime) ReadFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	segment, err := runtime.readPersistedSegment(ctx, queryGroup, evaluationTime)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return segment.Schedule, nil
}

func (runtime *RedisCatalogRuntime) ReadSuccessorFrozenSchedule(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	segmentEnd execution.EvaluationTime,
) (execution.FrozenQueryGroupSchedule, error) {
	segment, err := runtime.readPersistedSuccessor(ctx, queryGroup, segmentEnd)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return segment.Schedule, nil
}

func (runtime *RedisCatalogRuntime) ReadScheduleRetirement(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (execution.EvaluationTime, bool, error) {
	if runtime == nil || runtime.repository == nil || queryGroup == "" {
		return 0, false, errors.New("alarmd controlplane: valid Query Group retirement read is required")
	}
	timeline, _, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return 0, false, err
	}
	if timeline.RetiredAt == nil {
		return 0, false, nil
	}
	return *timeline.RetiredAt, true, nil
}

func (runtime *RedisCatalogRuntime) NextSlotAfter(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	segment, err := runtime.readPersistedSegment(ctx, queryGroup, completed)
	if err != nil {
		return 0, err
	}
	containsCompletion := true
	for {
		var next execution.EvaluationTime
		var ok bool
		if containsCompletion {
			next, ok = segment.Schedule.NextSlotAfter(completed)
		} else {
			next, ok = segment.Schedule.FirstSlot()
		}
		if ok && next > completed {
			return next, nil
		}
		if segment.Schedule.Segment.End == nil {
			return 0, ErrScheduleUnavailable
		}
		boundary := *segment.Schedule.Segment.End
		successor, successorErr := runtime.readPersistedSuccessor(ctx, queryGroup, boundary)
		if errors.Is(successorErr, ErrScheduleUnavailable) {
			retiredAt, retired, retirementErr := runtime.ReadScheduleRetirement(ctx, queryGroup)
			if retirementErr != nil {
				return 0, retirementErr
			}
			if retired && retiredAt == boundary && boundary > completed {
				return boundary, nil
			}
		}
		if successorErr != nil {
			return 0, successorErr
		}
		segment = successor
		containsCompletion = false
	}
}

func (runtime *RedisCatalogRuntime) readPersistedSuccessor(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	segmentEnd execution.EvaluationTime,
) (persistedScheduleSegment, error) {
	if runtime == nil || runtime.repository == nil || queryGroup == "" || segmentEnd <= 0 {
		return persistedScheduleSegment{}, errors.New("alarmd controlplane: valid Schedule successor read is required")
	}
	timeline, _, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleSegment{}, err
	}
	for index := 1; index < len(timeline.Segments); index++ {
		previous := timeline.Segments[index-1].Schedule.Segment
		if previous.End == nil || *previous.End != segmentEnd {
			continue
		}
		return timeline.Segments[index], nil
	}
	return persistedScheduleSegment{}, ErrScheduleUnavailable
}

func (runtime *RedisCatalogRuntime) FreezeSlotContract(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	if err := request.Validate(); err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	segment, err := runtime.readPersistedSegment(ctx, request.QueryGroup, request.EvaluationTime)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleRead, err)
	}
	schedule := segment.Schedule
	if schedule.Segment.ScheduleRevision != request.ScheduleRevision || schedule.Segment.Start != request.ScheduleSegmentStart {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleMismatch,
			errors.New("alarmd controlplane: frozen Slot request differs from persisted Segment"))
	}
	expectedDue := schedule.DuePlanRefs(request.EvaluationTime)
	if !equalDuePlanRefs(expectedDue, request.DuePlans) {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureScheduleMismatch,
			errors.New("alarmd controlplane: frozen Slot request differs from exact due Plan set"))
	}
	publication := SnapshotPublicationRef{SnapshotRevision: schedule.Segment.Publication.SnapshotRevision,
		PublicationEpoch: uint64(schedule.Segment.Publication.PublicationEpoch)}
	group, err := runtime.repository.loadPublishedQueryGroup(ctx, publication, request.QueryGroup)
	if err != nil {
		if errors.Is(err, ErrCatalogObjectUnavailable) {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureSnapshotRead, err)
	}
	activationByPlan := make(map[execution.PlanIdentity]PlanActivationRecord, len(segment.Plans))
	for _, record := range segment.Plans {
		activationByPlan[record.Fact.Plan] = record
	}
	planByID := make(map[execution.PlanIdentity]FrozenPlan, len(group.Plans))
	for _, plan := range group.Plans {
		planByID[plan.Identity] = plan
	}
	duePlans := make([]execution.DuePlan, 0, len(request.DuePlans))
	for _, dueRef := range request.DuePlans {
		plan, ok := planByID[dueRef.Identity]
		record, active := activationByPlan[dueRef.Identity]
		if !ok || !active || plan.ScheduleRevision != dueRef.ScheduleRevision ||
			record.Fact.Selected.ScheduleRevision != dueRef.ScheduleRevision {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: due Plan is absent from frozen Catalog or activation"))
		}
		compiledResult, err := runtime.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan.Plan,
			DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: runtime.stateSemantics})
		if err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		compiled, compiledOK := compiledResult.Plan()
		if !compiledOK || compiledResult.PlanTerminal() != nil || len(compiledResult.LevelTerminals()) != 0 {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: frozen Plan cannot be compiled for G1 FULL execution"))
		}
		compiledGeneration := execution.StateGeneration(compiled.StateCompatibilityHash())
		if plan.StateGeneration != "" && plan.StateGeneration != compiledGeneration {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: frozen Plan state generation differs from compiled Plan"))
		}
		generationCompatible, err := runtime.stateGenerationCompatibleWithSegment(plan, record, segment, compiledGeneration)
		if err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize, err)
		}
		if !generationCompatible {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailurePlanMaterialize,
				errors.New("alarmd controlplane: Plan activation state generation differs from compiled Plan"))
		}
		deadline, err := completionDeadline(request.EvaluationTime, plan.ScheduleSpec)
		if err != nil {
			return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
		}
		capabilities := make([]execution.LevelPartialCapability, 0, len(compiled.Levels()))
		for _, level := range compiled.Levels() {
			capabilities = append(capabilities, execution.LevelPartialCapability{LevelID: level.Definition().LevelID, Policy: execution.PartialRequiresFull})
		}
		duePlans = append(duePlans, execution.DuePlan{Identity: plan.Identity, CompiledPlan: compiled,
			StateGeneration: record.Fact.Selected.StateGeneration, StateApplyEpoch: record.Fact.Selected.StateApplyEpoch,
			ScheduleRevision: plan.ScheduleRevision, ScheduleSpec: plan.ScheduleSpec,
			CompletionDeadlineUnixMilli: deadline, PartialCapabilities: capabilities})
	}
	requirements, err := runtime.slotRequirements(group, duePlans, planByID)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureInputClosure, err)
	}
	digest, err := execution.DeriveDuePlanSetDigest(duePlans, requirements)
	if err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	fact := execution.FrozenSlotContractFact{Contract: execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: request.EvaluationTime},
		SnapshotRevision: schedule.Segment.Publication.SnapshotRevision, QueryRevision: schedule.Segment.QueryRevision,
		ScheduleRevision: request.ScheduleRevision, ScheduleSegmentStart: request.ScheduleSegmentStart,
		DuePlanSetDigest: digest,
	}, DuePlans: duePlans, Requirements: requirements}
	if err := fact.Validate(request); err != nil {
		return execution.FrozenSlotContractFact{}, freezeSlotContractError(FreezeSlotFailureContractValidation, err)
	}
	return fact, nil
}

func (runtime *RedisCatalogRuntime) stateGenerationCompatibleWithSegment(
	plan FrozenPlan,
	record PlanActivationRecord,
	segment persistedScheduleSegment,
	compiled execution.StateGeneration,
) (bool, error) {
	if compiled == record.Fact.Selected.StateGeneration {
		return true, nil
	}
	if plan.StateGeneration != "" || segment.Schedule.Segment.End == nil {
		return false, nil
	}
	executionSemantics := plan.Plan.StrategyIR.ExecutionSemantics
	legacy, err := contract.DeriveStateCompatibilityHashV1(contract.StateCompatibilityInputV1{
		StateSchemaVersion: runtime.stateSemantics.StateSchemaVersion, CodecSemanticsVersion: runtime.stateSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest: runtime.stateSemantics.IdentitySchemaDigest, EvaluationScope: executionSemantics.EvaluationScope,
		AggregationInterval: executionSemantics.AggregationInterval, EvaluationInterval: executionSemantics.EvaluationInterval,
		SourceTimeSemanticsVersion:  runtime.stateSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: runtime.stateSemantics.HistoryCellSemanticsVersion,
	})
	if err != nil {
		return false, err
	}
	return execution.StateGeneration(legacy) == record.Fact.Selected.StateGeneration, nil
}

func (runtime *RedisCatalogRuntime) slotRequirements(
	group QueryGroup,
	duePlans []execution.DuePlan,
	planByID map[execution.PlanIdentity]FrozenPlan,
) ([]execution.DataRequirement, error) {
	byID := make(map[execution.RequirementID]*execution.DataRequirement)
	legacyDuePlans := make([]execution.DuePlan, 0, len(duePlans))
	legacyLevels := make(map[execution.PlanIdentity]map[uint32]struct{}, len(duePlans))

	for _, due := range duePlans {
		plan, ok := planByID[due.Identity]
		if !ok {
			return nil, errors.New("alarmd controlplane: due Plan has no frozen input facts")
		}
		reserve := runtime.downstreamReserve.Milliseconds()
		if reserve <= 0 || due.ScheduleSpec.EvaluationIntervalSeconds > math.MaxInt64/1000 ||
			reserve >= due.ScheduleSpec.EvaluationIntervalSeconds*1000 {
			return nil, errors.New("alarmd controlplane: invalid downstream execution reserve")
		}

		expected := make(map[execution.RequirementID]uint32)
		explicitPrimary := make(map[uint32]map[execution.RequirementID]struct{})
		for _, level := range due.CompiledPlan.Levels() {
			levelID := level.Definition().LevelID
			for _, algorithm := range level.Algorithms() {
				algorithmRequirements := algorithm.InputRequirements()
				if len(algorithmRequirements) == 0 {
					continue
				}
				for _, requirement := range algorithmRequirements {
					if requirement.ConsumerLevelID != levelID {
						return nil, errors.New("alarmd controlplane: compiled algorithm input references a different Level")
					}
					identity := execution.RequirementID(requirement.RequirementID)
					if identity == "" {
						return nil, errors.New("alarmd controlplane: compiled algorithm input identity is missing")
					}
					if existingLevel, exists := expected[identity]; exists && existingLevel != levelID {
						return nil, errors.New("alarmd controlplane: compiled algorithm input identity is shared across Levels")
					}
					expected[identity] = levelID
					if requirement.Role == strategy.AlgorithmInputPrimary {
						if explicitPrimary[levelID] == nil {
							explicitPrimary[levelID] = make(map[execution.RequirementID]struct{})
						}
						explicitPrimary[levelID][identity] = struct{}{}
					}
				}
			}
		}
		for _, level := range due.CompiledPlan.Levels() {
			levelID := level.Definition().LevelID
			switch len(explicitPrimary[levelID]) {
			case 0:
				if legacyLevels[due.Identity] == nil {
					legacyLevels[due.Identity] = make(map[uint32]struct{})
					legacyDuePlans = append(legacyDuePlans, due)
				}
				legacyLevels[due.Identity][levelID] = struct{}{}
			case 1:
			default:
				return nil, errors.New("alarmd controlplane: compiled Level has multiple PRIMARY input requirements")
			}
		}

		templates := make(map[execution.RequirementID]execution.DataRequirementTemplate, len(plan.RequirementTemplates))
		for _, template := range plan.RequirementTemplates {
			levelID, required := expected[template.RequirementID]
			if !required {
				continue
			}
			if template.ConsumerLevelID != levelID {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement template references a different Level")
			}
			if _, duplicate := templates[template.RequirementID]; duplicate {
				return nil, errors.New("alarmd controlplane: duplicate frozen DataRequirement template")
			}
			originalID := template.RequirementID
			template.RequirementID = ""
			rebuilt, err := execution.BuildDataRequirementTemplate(template)
			if err != nil {
				return nil, fmt.Errorf("alarmd controlplane: invalid frozen DataRequirement template: %w", err)
			}
			if rebuilt.RequirementID != originalID || !equalStrings(rebuilt.RequiredColumns, template.RequiredColumns) {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement template differs from its identity")
			}
			facts, exists := plan.QueryPlans[rebuilt.LogicalQueryRef]
			if !exists {
				return nil, errors.New("alarmd controlplane: frozen DataRequirement has no QueryPlanFacts")
			}
			if err := facts.Validate(); err != nil {
				return nil, fmt.Errorf("alarmd controlplane: invalid frozen QueryPlanFacts: %w", err)
			}
			if execution.LogicalQueryRef(facts.QueryRevision) != rebuilt.LogicalQueryRef {
				return nil, errors.New("alarmd controlplane: frozen QueryPlanFacts differ from logical query reference")
			}
			templates[originalID] = rebuilt
		}

		for identity, levelID := range expected {
			template, exists := templates[identity]
			if !exists {
				return nil, errors.New("alarmd controlplane: compiled algorithm input has no frozen DataRequirement template")
			}
			bound := template.Bind(execution.DataRequirementConsumer{
				Consumer:                           execution.ConsumerRef{Plan: due.Identity, LevelID: levelID, HasLevel: true},
				ConsumerDeadlineUnixMilli:          due.CompletionDeadlineUnixMilli,
				DownstreamExecutionReserveMilliSec: reserve,
			})
			if existing := byID[identity]; existing != nil {
				existing.Consumers = append(existing.Consumers, bound.Consumers...)
			} else {
				copy := bound
				byID[identity] = &copy
			}
		}
	}

	legacy, err := runtime.primaryRequirements(group, legacyDuePlans, legacyLevels)
	if err != nil {
		return nil, err
	}
	for _, requirement := range legacy {
		if existing := byID[requirement.RequirementID]; existing != nil {
			existing.Consumers = append(existing.Consumers, requirement.Consumers...)
		} else {
			copy := requirement
			byID[requirement.RequirementID] = &copy
		}
	}

	result := make([]execution.DataRequirement, 0, len(byID))
	for _, requirement := range byID {
		sort.Slice(requirement.Consumers, func(i, j int) bool {
			left := requirement.Consumers[i].Consumer
			right := requirement.Consumers[j].Consumer
			if left.Plan != right.Plan {
				return lessPlanIdentity(left.Plan, right.Plan)
			}
			if left.HasLevel != right.HasLevel {
				return !left.HasLevel
			}
			return left.LevelID < right.LevelID
		})
		result = append(result, *requirement)
	}
	if err := validateExactlyOnePrimaryPerLevel(duePlans, result); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequirementID < result[j].RequirementID })
	return result, nil
}

func validateExactlyOnePrimaryPerLevel(duePlans []execution.DuePlan, requirements []execution.DataRequirement) error {
	primary := make(map[execution.ConsumerRef]int)
	for _, requirement := range requirements {
		if requirement.Role != execution.InputRolePrimary {
			continue
		}
		for _, consumer := range requirement.Consumers {
			primary[consumer.Consumer]++
		}
	}
	for _, due := range duePlans {
		for _, level := range due.CompiledPlan.Levels() {
			consumer := execution.ConsumerRef{Plan: due.Identity, LevelID: level.Definition().LevelID, HasLevel: true}
			if primary[consumer] != 1 {
				return errors.New("alarmd controlplane: compiled Level must have exactly one PRIMARY input requirement")
			}
		}
	}
	return nil
}

func (runtime *RedisCatalogRuntime) readPersistedSegment(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	evaluationTime execution.EvaluationTime,
) (persistedScheduleSegment, error) {
	if runtime == nil || runtime.repository == nil || evaluationTime <= 0 {
		return persistedScheduleSegment{}, errors.New("alarmd controlplane: valid Catalog runtime and EvaluationTime are required")
	}
	timeline, _, err := runtime.repository.loadScheduleTimeline(ctx, queryGroup)
	if err != nil {
		return persistedScheduleSegment{}, err
	}
	for _, segment := range timeline.Segments {
		if segment.Schedule.Segment.Contains(evaluationTime) {
			return segment, nil
		}
	}
	return persistedScheduleSegment{}, ErrScheduleUnavailable
}

func (runtime *RedisCatalogRuntime) primaryRequirements(
	group QueryGroup,
	duePlans []execution.DuePlan,
	levelsByPlan map[execution.PlanIdentity]map[uint32]struct{},
) ([]execution.DataRequirement, error) {
	type requirementKey struct {
		window int64
	}
	byWindow := make(map[requirementKey]*execution.DataRequirement)
	columns := append([]string{"value"}, group.QueryPlan.Normalization.DatasetContract.IdentityFields...)
	sort.Strings(columns)
	for _, due := range duePlans {
		window := int64(due.CompiledPlan.EvaluationSemantics().QueryWindow)
		key := requirementKey{window: window}
		requirement := byWindow[key]
		if requirement == nil {
			identity, err := contract.DeriveCanonicalDigestV2("alarmd-primary-requirement-v1", struct {
				QueryRevision execution.QueryRevision `json:"query_revision"`
				WindowSeconds int64                   `json:"window_seconds"`
			}{group.QueryPlan.QueryRevision, window})
			if err != nil {
				return nil, err
			}
			requirement = &execution.DataRequirement{RequirementID: execution.RequirementID(identity),
				DatasetName: execution.DatasetName("primary:" + identity), Role: execution.InputRolePrimary,
				LogicalQueryRef: execution.LogicalQueryRef(group.QueryPlan.QueryRevision),
				RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: -window, EndOffsetSeconds: 0, HalfOpen: true},
				StepMillis:      group.QueryPlan.StepMillis, AlignmentMillis: group.QueryPlan.AlignmentMillis,
				ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager,
				RequiredColumns: append([]string(nil), columns...)}
			byWindow[key] = requirement
		}
		reserve := runtime.downstreamReserve.Milliseconds()
		if reserve <= 0 || due.ScheduleSpec.EvaluationIntervalSeconds > math.MaxInt64/1000 ||
			reserve >= due.ScheduleSpec.EvaluationIntervalSeconds*1000 {
			return nil, errors.New("alarmd controlplane: invalid downstream execution reserve")
		}
		levelIDs := make([]uint32, 0, len(levelsByPlan[due.Identity]))
		for levelID := range levelsByPlan[due.Identity] {
			levelIDs = append(levelIDs, levelID)
		}
		sort.Slice(levelIDs, func(i, j int) bool { return levelIDs[i] < levelIDs[j] })
		for _, levelID := range levelIDs {
			requirement.Consumers = append(requirement.Consumers, execution.DataRequirementConsumer{
				Consumer: execution.ConsumerRef{
					Plan: due.Identity, LevelID: levelID, HasLevel: true,
				},
				ConsumerDeadlineUnixMilli:          due.CompletionDeadlineUnixMilli,
				DownstreamExecutionReserveMilliSec: reserve,
			})
		}
	}
	result := make([]execution.DataRequirement, 0, len(byWindow))
	for _, requirement := range byWindow {
		sort.Slice(requirement.Consumers, func(i, j int) bool {
			left := requirement.Consumers[i].Consumer
			right := requirement.Consumers[j].Consumer
			if left.Plan != right.Plan {
				return lessPlanIdentity(left.Plan, right.Plan)
			}
			return left.LevelID < right.LevelID
		})
		result = append(result, *requirement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequirementID < result[j].RequirementID })
	return result, nil
}

func completionDeadline(at execution.EvaluationTime, spec execution.ScheduleSpec) (int64, error) {
	deadline, ok := spec.CompletionDeadlineUnixMilli(at)
	if !ok {
		return 0, errors.New("alarmd controlplane: invalid frozen Plan deadline")
	}
	return deadline, nil
}

func equalDuePlanRefs(left, right []execution.FrozenPlanScheduleRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
