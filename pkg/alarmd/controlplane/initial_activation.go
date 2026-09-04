package controlplane

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// InitialScheduleActivator creates the first persisted Schedule Segment from
// one confirmed, content-addressed Catalog publication. Workers remain readers
// of the resulting activation and Segment facts.
type InitialScheduleActivator struct {
	repository     *RedisCatalogRepository
	compiler       RuntimePlanCompiler
	stateSemantics strategy.StateSemantics
	now            func() time.Time
}

func NewInitialScheduleActivator(
	repository *RedisCatalogRepository,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	now func() time.Time,
) (*InitialScheduleActivator, error) {
	if repository == nil || compiler == nil || now == nil || !validStateSemantics(stateSemantics) {
		return nil, errors.New("alarmd controlplane: invalid initial Schedule activator")
	}
	return &InitialScheduleActivator{repository: repository, compiler: compiler, stateSemantics: stateSemantics, now: now}, nil
}

// Ensure establishes the first activation once. A concurrent winner remains
// authoritative; a loser reads and returns that persisted fact instead of
// recomputing or overwriting its boundary.
func (activator *InitialScheduleActivator) Ensure(
	ctx context.Context,
	publication SnapshotPublicationRef,
) (state ActivationState, err error) {
	failureStage := ActivationFailureStageActivationLoad
	failureClass := ActivationFailureClassOther
	defer func() { err = wrapActivationFailure(failureStage, failureClass, err) }()

	if activator == nil || activator.repository == nil || activator.compiler == nil || activator.now == nil || publication.validate() != nil {
		return ActivationState{}, errors.New("alarmd controlplane: valid initial activation publication is required")
	}
	failureClass = ActivationFailureClassDependencyIO
	existing, err := activator.repository.LoadActivation(ctx)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrActivationUnavailable) {
		return ActivationState{}, err
	}

	failureStage = ActivationFailureStageCandidateLoad
	snapshot, err := activator.repository.LoadPublishedSnapshot(ctx, publication)
	if err != nil {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStageCompile, ActivationFailureClassOther
	boundary := execution.EvaluationTime(activator.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: initial activation clock must produce a positive Unix second")
	}

	failureClass = ActivationFailureClassCorrupt
	records, segments, err := compilePublishedActivation(ctx, activator.compiler, activator.stateSemantics, snapshot, boundary)
	if err != nil {
		return ActivationState{}, err
	}
	initial := make([]execution.InitialScheduleActivationFact, len(segments))
	for index, segment := range segments {
		initial[index] = execution.InitialScheduleActivationFact{Segment: segment}
	}
	next := ActivationState{RecordRevision: 1, Current: publication, Plans: records}
	failureStage, failureClass = ActivationFailureStageScheduleCutover, ActivationFailureClassScheduleConflict
	if err := activator.repository.CompareAndSetInitialScheduleActivation(ctx, ActivationExpectation{}, next, initial); err != nil && !errors.Is(err, ErrActivationConflict) {
		return ActivationState{}, err
	}
	failureStage, failureClass = ActivationFailureStagePersist, ActivationFailureClassDependencyIO
	return activator.repository.LoadActivation(ctx)
}

func compilePublishedActivation(
	ctx context.Context,
	compiler RuntimePlanCompiler,
	stateSemantics strategy.StateSemantics,
	snapshot PublishedSnapshot,
	boundary execution.EvaluationTime,
) ([]PlanActivationRecord, []execution.ScheduleSegmentFact, error) {
	records := make([]PlanActivationRecord, 0)
	segments := make([]execution.ScheduleSegmentFact, 0, len(snapshot.QueryGroups))
	for _, group := range snapshot.QueryGroups {
		plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
		for index, plan := range group.Plans {
			compiledResult, err := compiler.Compile(ctx, strategy.CompileRequest{
				Plan: plan.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: stateSemantics,
			})
			if err != nil {
				return nil, nil, err
			}
			compiled, ok := compiledResult.Plan()
			if !ok || compiledResult.PlanTerminal() != nil || len(compiledResult.LevelTerminals()) != 0 {
				return nil, nil, errors.New("alarmd controlplane: activation Plan cannot be compiled")
			}
			compiledGeneration := execution.StateGeneration(compiled.StateCompatibilityHash())
			if plan.StateGeneration != "" && plan.StateGeneration != compiledGeneration {
				return nil, nil, errors.New("alarmd controlplane: frozen Plan state generation differs from compiled Plan")
			}
			requiredFullSlots := requiredFullSlots(compiled)
			if requiredFullSlots == 0 {
				return nil, nil, errors.New("alarmd controlplane: activation Plan has no recovery window")
			}
			plans[index] = execution.FrozenPlanSchedule{
				Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec,
			}
			fact := execution.PlanActivationFact{Plan: plan.Identity, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{Identity: plan.Identity,
					StateGeneration:  compiledGeneration,
					StateApplyEpoch:  execution.StateApplyEpoch(snapshot.Publication.PublicationEpoch),
					ScheduleRevision: plan.ScheduleRevision, RequiredFullSlots: requiredFullSlots}}
			records = append(records, PlanActivationRecord{Fact: fact, Publication: snapshot.Publication})
		}
		segment := scheduleSegmentForGroup(snapshot.Publication, group, boundary)
		if err := (execution.FrozenQueryGroupSchedule{Segment: segment, Plans: plans}).Validate(); err != nil {
			return nil, nil, err
		}
		segments = append(segments, segment)
	}
	return records, segments, nil
}

func requiredFullSlots(plan *strategy.CompiledPlan) uint32 {
	var required uint32
	for _, level := range plan.Levels() {
		if level.RequiredDetectHistoryPoints() > required {
			required = level.RequiredDetectHistoryPoints()
		}
	}
	return required
}

func validStateSemantics(semantics strategy.StateSemantics) bool {
	return semantics.StateSchemaVersion != "" && semantics.CodecSemanticsVersion != "" &&
		semantics.IdentitySchemaDigest != "" && semantics.SourceTimeSemanticsVersion != "" &&
		semantics.HistoryCellSemanticsVersion != ""
}
