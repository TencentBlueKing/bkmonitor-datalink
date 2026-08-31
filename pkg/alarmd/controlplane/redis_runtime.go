package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const scheduleTimelineSchemaVersion = "alarmd-control-schedule-timeline-v1"

var (
	ErrScheduleUnavailable = errors.New("alarmd controlplane: schedule unavailable")
	ErrScheduleConflict    = errors.New("alarmd controlplane: schedule activation conflict")
)

type persistedScheduleSegment struct {
	Schedule execution.FrozenQueryGroupSchedule `json:"schedule"`
	Plans    []PlanActivationRecord             `json:"plans"`
}

type persistedScheduleTimeline struct {
	SchemaVersion  string                       `json:"schema_version"`
	RecordRevision uint64                       `json:"record_revision"`
	QueryGroup     execution.QueryGroupIdentity `json:"query_group"`
	Segments       []persistedScheduleSegment   `json:"segments"`
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
for index = 3, #KEYS do
  if redis.call('EXISTS', KEYS[index]) == 1 then return 0 end
end
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
for index = 3, #KEYS do
  redis.call('SET', KEYS[index], ARGV[index + 1])
end
return 1
`

const compareAndSetCutoverSchedulesScript = `
local header = redis.call('GET', KEYS[1])
if not header or header ~= ARGV[1] then return 0 end
for index = 3, #KEYS do
  local current = redis.call('GET', KEYS[index])
  local expected_index = 2 * index - 2
  if not current or current ~= ARGV[expected_index] then return 0 end
end
redis.call('SET', KEYS[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3])
for index = 3, #KEYS do
  local next_index = 2 * index - 1
  redis.call('SET', KEYS[index], ARGV[next_index])
end
return 1
`

// CompareAndSetInitialScheduleActivation establishes one or more first
// Schedule Segments together with their Plan activation facts. Module 02 owns
// the choice of facts; this method only validates and atomically persists them.
func (repository *RedisCatalogRepository) CompareAndSetInitialScheduleActivation(
	ctx context.Context,
	expected ActivationExpectation,
	next ActivationState,
	facts []execution.InitialScheduleActivationFact,
) error {
	if repository == nil || repository.client == nil || len(facts) == 0 {
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
	expectedHeader := ""
	nextHeader, err := activationHeader(next.RecordRevision, next.Current, next.Pending)
	if err != nil {
		return err
	}
	activationPayload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	keys := []string{repository.activationHeaderKey(), repository.activationKey()}
	args := []interface{}{expectedHeader, nextHeader, activationPayload}
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
		return fmt.Errorf("alarmd controlplane: persist initial schedule activation: %w", err)
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
	keys := []string{repository.activationHeaderKey(), repository.activationKey()}
	args := []interface{}{expectedHeader, nextHeader, activationPayload}
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
		return fmt.Errorf("alarmd controlplane: persist schedule cutover: %w", err)
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
	snapshot, err := repository.LoadSnapshot(ctx, segment.Publication.SnapshotRevision)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	if execution.PublicationEpoch(snapshot.Publication.PublicationEpoch) != segment.Publication.PublicationEpoch {
		return execution.FrozenQueryGroupSchedule{}, errors.New("alarmd controlplane: Schedule Segment publication epoch mismatch")
	}
	for _, group := range snapshot.QueryGroups {
		if group.Identity != segment.QueryGroup {
			continue
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
	return execution.FrozenQueryGroupSchedule{}, ErrCatalogObjectUnavailable
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
		if index+1 < len(timeline.Segments) {
			next := timeline.Segments[index+1].Schedule.Segment
			if segment.Schedule.Segment.End == nil || *segment.Schedule.Segment.End != next.Start {
				return errors.New("alarmd controlplane: persisted Schedule Segments are not adjacent")
			}
		} else if segment.Schedule.Segment.End != nil {
			return errors.New("alarmd controlplane: final persisted Schedule Segment must remain open")
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

func (repository *RedisCatalogRepository) loadScheduleTimeline(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (persistedScheduleTimeline, []byte, error) {
	if repository == nil || repository.client == nil || queryGroup == "" {
		return persistedScheduleTimeline{}, nil, errors.New("alarmd controlplane: Query Group schedule is required")
	}
	payload, err := repository.client.Get(ctx, repository.scheduleTimelineKey(queryGroup)).Bytes()
	if errors.Is(err, redis.Nil) {
		return persistedScheduleTimeline{}, nil, ErrScheduleUnavailable
	}
	if err != nil {
		return persistedScheduleTimeline{}, nil, err
	}
	var timeline persistedScheduleTimeline
	if err := json.Unmarshal(payload, &timeline); err != nil {
		return persistedScheduleTimeline{}, nil, fmt.Errorf("alarmd controlplane: decode Schedule timeline: %w", err)
	}
	if timeline.QueryGroup != queryGroup {
		return persistedScheduleTimeline{}, nil, errors.New("alarmd controlplane: persisted Schedule timeline identity mismatch")
	}
	if err := validateScheduleTimeline(timeline); err != nil {
		return persistedScheduleTimeline{}, nil, err
	}
	return timeline, payload, nil
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

func (runtime *RedisCatalogRuntime) NextSlotAfter(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	completed execution.EvaluationTime,
) (execution.EvaluationTime, error) {
	segment, err := runtime.readPersistedSegment(ctx, queryGroup, completed)
	if err != nil {
		return 0, err
	}
	if next, ok := segment.Schedule.NextSlotAfter(completed); ok {
		return next, nil
	}
	if segment.Schedule.Segment.End == nil {
		return 0, ErrScheduleUnavailable
	}
	successor, err := runtime.readPersistedSegment(ctx, queryGroup, *segment.Schedule.Segment.End)
	if err != nil {
		return 0, err
	}
	next, ok := successor.Schedule.FirstSlot()
	if !ok || next <= completed {
		return 0, ErrScheduleUnavailable
	}
	return next, nil
}

func (runtime *RedisCatalogRuntime) FreezeSlotContract(
	ctx context.Context,
	request execution.FreezeSlotContractRequest,
) (execution.FrozenSlotContractFact, error) {
	if err := request.Validate(); err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	segment, err := runtime.readPersistedSegment(ctx, request.QueryGroup, request.EvaluationTime)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	schedule := segment.Schedule
	if schedule.Segment.ScheduleRevision != request.ScheduleRevision || schedule.Segment.Start != request.ScheduleSegmentStart {
		return execution.FrozenSlotContractFact{}, errors.New("alarmd controlplane: frozen Slot request differs from persisted Segment")
	}
	expectedDue := schedule.DuePlanRefs(request.EvaluationTime)
	if !equalDuePlanRefs(expectedDue, request.DuePlans) {
		return execution.FrozenSlotContractFact{}, errors.New("alarmd controlplane: frozen Slot request differs from exact due Plan set")
	}
	snapshot, err := runtime.repository.LoadSnapshot(ctx, schedule.Segment.Publication.SnapshotRevision)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	group, err := findQueryGroup(snapshot, request.QueryGroup)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
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
			return execution.FrozenSlotContractFact{}, errors.New("alarmd controlplane: due Plan is absent from frozen Catalog or activation")
		}
		compiledResult, err := runtime.compiler.Compile(ctx, strategy.CompileRequest{Plan: plan.Plan,
			DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: runtime.stateSemantics})
		if err != nil {
			return execution.FrozenSlotContractFact{}, err
		}
		compiled, compiledOK := compiledResult.Plan()
		if !compiledOK || compiledResult.PlanTerminal() != nil || len(compiledResult.LevelTerminals()) != 0 {
			return execution.FrozenSlotContractFact{}, errors.New("alarmd controlplane: frozen Plan cannot be compiled for G1 FULL execution")
		}
		if execution.StateGeneration(compiled.StateCompatibilityHash()) != record.Fact.Selected.StateGeneration {
			return execution.FrozenSlotContractFact{}, errors.New("alarmd controlplane: Plan activation state generation differs from compiled Plan")
		}
		deadline, err := completionDeadline(request.EvaluationTime, plan.ScheduleSpec)
		if err != nil {
			return execution.FrozenSlotContractFact{}, err
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
	requirements, err := runtime.primaryRequirements(group, duePlans)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	digest, err := execution.DeriveDuePlanSetDigest(duePlans, requirements)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	fact := execution.FrozenSlotContractFact{Contract: execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: request.QueryGroup, EvaluationTime: request.EvaluationTime},
		SnapshotRevision: schedule.Segment.Publication.SnapshotRevision, QueryRevision: schedule.Segment.QueryRevision,
		ScheduleRevision: request.ScheduleRevision, ScheduleSegmentStart: request.ScheduleSegmentStart,
		DuePlanSetDigest: digest,
	}, DuePlans: duePlans, Requirements: requirements}
	if err := fact.Validate(request); err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	return fact, nil
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

func (runtime *RedisCatalogRuntime) primaryRequirements(group QueryGroup, duePlans []execution.DuePlan) ([]execution.DataRequirement, error) {
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
		requirement.Consumers = append(requirement.Consumers, execution.DataRequirementConsumer{
			Consumer: execution.ConsumerRef{Plan: due.Identity}, ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli,
			DownstreamExecutionReserveMilliSec: reserve,
		})
	}
	result := make([]execution.DataRequirement, 0, len(byWindow))
	for _, requirement := range byWindow {
		sort.Slice(requirement.Consumers, func(i, j int) bool {
			return lessPlanIdentity(requirement.Consumers[i].Consumer.Plan, requirement.Consumers[j].Consumer.Plan)
		})
		result = append(result, *requirement)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequirementID < result[j].RequirementID })
	return result, nil
}

func completionDeadline(at execution.EvaluationTime, spec execution.ScheduleSpec) (int64, error) {
	if err := spec.Validate(); err != nil || at <= 0 || int64(at) > math.MaxInt64-spec.EvaluationIntervalSeconds {
		return 0, errors.New("alarmd controlplane: invalid frozen Plan deadline")
	}
	seconds := int64(at) + spec.EvaluationIntervalSeconds
	if seconds > math.MaxInt64/1000 {
		return 0, errors.New("alarmd controlplane: frozen Plan deadline overflows")
	}
	return seconds * 1000, nil
}

func findQueryGroup(snapshot PublishedSnapshot, identity execution.QueryGroupIdentity) (QueryGroup, error) {
	for _, group := range snapshot.QueryGroups {
		if group.Identity == identity {
			return group, nil
		}
	}
	return QueryGroup{}, ErrCatalogObjectUnavailable
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
