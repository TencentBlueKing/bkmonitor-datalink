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
) (ActivationState, error) {
	if activator == nil || activator.repository == nil || activator.compiler == nil || activator.now == nil || publication.validate() != nil {
		return ActivationState{}, errors.New("alarmd controlplane: valid initial activation publication is required")
	}
	existing, err := activator.repository.LoadActivation(ctx)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrActivationUnavailable) {
		return ActivationState{}, err
	}

	snapshot, err := activator.repository.LoadSnapshot(ctx, publication.SnapshotRevision)
	if err != nil {
		return ActivationState{}, err
	}
	if snapshot.Publication != publication || len(snapshot.QueryGroups) != 1 {
		return ActivationState{}, errors.New("alarmd controlplane: initial activation requires one confirmed Query Group publication")
	}
	boundary := execution.EvaluationTime(activator.now().Unix())
	if boundary <= 0 {
		return ActivationState{}, errors.New("alarmd controlplane: initial activation clock must produce a positive Unix second")
	}

	group := snapshot.QueryGroups[0]
	plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
	records := make([]PlanActivationRecord, len(group.Plans))
	for index, plan := range group.Plans {
		compiledResult, err := activator.compiler.Compile(ctx, strategy.CompileRequest{
			Plan: plan.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: activator.stateSemantics,
		})
		if err != nil {
			return ActivationState{}, err
		}
		compiled, ok := compiledResult.Plan()
		if !ok || compiledResult.PlanTerminal() != nil || len(compiledResult.LevelTerminals()) != 0 {
			return ActivationState{}, errors.New("alarmd controlplane: initial activation Plan cannot be compiled")
		}
		requiredFullSlots := requiredFullSlots(compiled)
		if requiredFullSlots == 0 {
			return ActivationState{}, errors.New("alarmd controlplane: initial activation Plan has no recovery window")
		}
		plans[index] = execution.FrozenPlanSchedule{
			Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec,
		}
		fact := execution.PlanActivationFact{Plan: plan.Identity, Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{Identity: plan.Identity,
				StateGeneration:  execution.StateGeneration(compiled.StateCompatibilityHash()),
				StateApplyEpoch:  execution.StateApplyEpoch(publication.PublicationEpoch),
				ScheduleRevision: plan.ScheduleRevision, RequiredFullSlots: requiredFullSlots}}
		records[index] = PlanActivationRecord{Fact: fact, Publication: publication}
	}
	segment := execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{SnapshotRevision: publication.SnapshotRevision,
			PublicationEpoch: execution.PublicationEpoch(publication.PublicationEpoch)},
		QueryGroup: group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, Start: boundary,
	}
	schedule := execution.FrozenQueryGroupSchedule{Segment: segment, Plans: plans}
	if err := schedule.Validate(); err != nil {
		return ActivationState{}, err
	}
	next := ActivationState{RecordRevision: 1, Current: publication, Plans: records}
	if err := activator.repository.CompareAndSetInitialScheduleActivation(ctx, ActivationExpectation{}, next,
		[]execution.InitialScheduleActivationFact{{Segment: segment}}); err != nil && !errors.Is(err, ErrActivationConflict) {
		return ActivationState{}, err
	}
	return activator.repository.LoadActivation(ctx)
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
