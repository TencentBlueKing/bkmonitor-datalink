package worker_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSlotExecutionCoordinatorEmitsValidatedAlgorithmFacts(t *testing.T) {
	tests := []struct {
		name                string
		previousUnavailable bool
		poisonHeaderScan    bool
		wantEvaluations     []observability.AlgorithmEvaluationFact
		wantInputs          []observability.AlgorithmInputFact
	}{
		{
			name: "available named inputs",
			wantEvaluations: []observability.AlgorithmEvaluationFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, Result: observability.AlgorithmEvaluationResultAbnormal, ReasonCode: observability.ReasonNone, Provenance: observability.AlgorithmProvenance{LevelID: 4}},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, Result: observability.AlgorithmEvaluationResultAbnormal, ReasonCode: observability.ReasonNone, Provenance: observability.AlgorithmProvenance{LevelID: 5}},
			},
			wantInputs: []observability.AlgorithmInputFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNameHistory, DependencyPoint: observability.AlgorithmDependencyPointPrevious, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
			},
		},
		{
			name:                "unavailable dependency stays level local",
			previousUnavailable: true,
			wantEvaluations: []observability.AlgorithmEvaluationFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, Result: observability.AlgorithmEvaluationResultAbnormal, ReasonCode: observability.ReasonNone, Provenance: observability.AlgorithmProvenance{LevelID: 4}},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, Result: observability.AlgorithmEvaluationResultUnavailable, ReasonCode: observability.ReasonCode(contract.ReasonQueryUnavailable), Provenance: observability.AlgorithmProvenance{LevelID: 5}},
			},
			wantInputs: []observability.AlgorithmInputFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNameHistory, DependencyPoint: observability.AlgorithmDependencyPointPrevious, Result: observability.AlgorithmInputResultUnavailable, ReasonCode: observability.ReasonCode(contract.ReasonQueryUnavailable)},
			},
		},
		{
			name:             "prepared index remains authoritative after begin",
			poisonHeaderScan: true,
			wantEvaluations: []observability.AlgorithmEvaluationFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, Result: observability.AlgorithmEvaluationResultAbnormal, ReasonCode: observability.ReasonNone, Provenance: observability.AlgorithmProvenance{LevelID: 4}},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, Result: observability.AlgorithmEvaluationResultAbnormal, ReasonCode: observability.ReasonNone, Provenance: observability.AlgorithmProvenance{LevelID: 5}},
			},
			wantInputs: []observability.AlgorithmInputFact{
				{SourceAlgorithmFamily: observability.AlgorithmFamilyThreshold, DetectorKind: observability.AlgorithmDetectorKindThreshold, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNamePrimary, DependencyPoint: observability.AlgorithmDependencyPointCurrent, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
				{SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio, DetectorKind: observability.AlgorithmDetectorKindSimpleRingRatio, InputName: observability.AlgorithmInputNameHistory, DependencyPoint: observability.AlgorithmDependencyPointPrevious, Result: observability.AlgorithmInputResultAvailable, ReasonCode: observability.ReasonNone},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, batches, completion := workerG4MultiLevelStreamFixture(t, test.previousUnavailable)
			var completed []observability.Observation
			ports, coordinator := workerAlgorithmObservationCoordinator(t, func(observation observability.Observation) {
				observation = observability.NormalizeObservation(observation)
				if observation.Component == observability.ComponentEvaluation && observation.Stage == observability.StageEvaluationCompleted {
					completed = append(completed, observation)
				}
			})
			ports.gapMissing = test.previousUnavailable
			ports.executeOverride = streamExecution(header, batches, completion)
			if test.poisonHeaderScan {
				ports.executeOverride = func(ctx context.Context, _ execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
					if err := consumer.Begin(ctx, header); err != nil {
						return execution.QueryExecutionCompletion{}, err
					}
					// The prepared index owns the validated frozen facts after Begin.
					// Poisoning the shallow header copy makes any later full-header
					// rescan lose the historical named point.
					for index := range header.Requirements {
						if header.Requirements[index].Role != execution.InputRolePrimary {
							header.Requirements[index] = execution.DataRequirement{}
						}
					}
					for _, batch := range batches {
						if err := consumer.ConsumeSeries(ctx, batch); err != nil {
							return execution.QueryExecutionCompletion{}, err
						}
					}
					return completion, nil
				}
			}

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if len(completed) != 1 {
				t.Fatalf("evaluation observations=%+v, want one validated series observation", completed)
			}
			gotEvaluations := completed[0].AlgorithmEvaluations
			for index := range gotEvaluations {
				if gotEvaluations[index].Provenance.SourceTime <= 0 {
					t.Fatalf("algorithm evaluation provenance=%+v, want frozen source time", gotEvaluations[index].Provenance)
				}
				gotEvaluations[index].Provenance = observability.AlgorithmProvenance{LevelID: gotEvaluations[index].Provenance.LevelID}
			}
			if !reflect.DeepEqual(gotEvaluations, test.wantEvaluations) {
				t.Fatalf("algorithm evaluations=%+v, want %+v", gotEvaluations, test.wantEvaluations)
			}
			gotInputs := completed[0].AlgorithmInputs
			for index := range gotInputs {
				provenance := gotInputs[index].Provenance
				if provenance.LevelID == 0 || provenance.RequirementID == "" || provenance.QueryRef == "" ||
					provenance.QueryRevision == "" || provenance.SourceTime <= 0 || provenance.QueryEnd <= provenance.QueryStart {
					t.Fatalf("algorithm input provenance=%+v, want complete frozen query/time binding", provenance)
				}
				gotInputs[index].Provenance = observability.AlgorithmProvenance{}
			}
			if !reflect.DeepEqual(gotInputs, test.wantInputs) {
				t.Fatalf("algorithm inputs=%+v, want %+v", gotInputs, test.wantInputs)
			}
		})
	}
}

func TestSlotExecutionCoordinatorObservesCompletionOnlyG4InputsWithoutEvaluation(t *testing.T) {
	tests := []struct {
		name         string
		completeness execution.Completeness
		dataState    execution.DataState
		disposition  execution.AccessDisposition
		reason       execution.ReasonCode
		operation    execution.Operation
		want         observability.AlgorithmInputResult
	}{
		{
			name:         "full empty is an explicit missing input exclusion",
			completeness: execution.CompletenessFull, dataState: execution.DataStateEmpty,
			disposition: execution.AccessAvailable, reason: observability.ReasonNone,
			operation: execution.OperationNormal, want: observability.AlgorithmInputResultMissing,
		},
		{
			name:         "partial primary is visible",
			completeness: execution.CompletenessPartial, dataState: execution.DataStateEmpty,
			disposition: execution.AccessDegraded, reason: execution.ReasonCode(contract.ReasonQueryPartial),
			operation: execution.OperationNormal, want: observability.AlgorithmInputResultPartial,
		},
		{
			name:         "unavailable primary is visible",
			completeness: execution.CompletenessUnavailable, dataState: execution.DataStateUnknown,
			disposition: execution.AccessUnavailable, reason: execution.ReasonCode(contract.ReasonQueryUnavailable),
			operation: execution.OperationNormal, want: observability.AlgorithmInputResultUnavailable,
		},
		{
			name:         "probe unavailable primary is visible",
			completeness: execution.CompletenessUnavailable, dataState: execution.DataStateUnknown,
			disposition: execution.AccessUnavailable, reason: execution.ReasonCode(contract.ReasonQueryUnavailable),
			operation: execution.OperationProbe, want: observability.AlgorithmInputResultUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, _, completion := workerG4MultiLevelStreamFixture(t, false)
			completion = workerG4CompletionOnly(t, header, completion, test.completeness, test.dataState,
				test.disposition, test.reason)
			var completed []observability.Observation
			ports, coordinator := workerAlgorithmObservationCoordinator(t, func(observation observability.Observation) {
				observation = observability.NormalizeObservation(observation)
				if observation.Component == observability.ComponentEvaluation && observation.Stage == observability.StageEvaluationCompleted {
					completed = append(completed, observation)
				}
			})
			ports.executeOverride = streamExecution(header, nil, completion)

			request := workerSlotRequest(header.Contract)
			request.Operation = test.operation
			result, err := coordinator.Execute(context.Background(), request)
			wantCompleted := test.operation != execution.OperationProbe
			if err != nil || result.Completed != wantCompleted {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if len(completed) != 1 {
				t.Fatalf("evaluation observations=%+v, want one completion-only Plan observation", completed)
			}
			observation := completed[0]
			if len(observation.AlgorithmEvaluations) != 0 {
				t.Fatalf("completion-only input fabricated algorithm evaluation=%+v", observation.AlgorithmEvaluations)
			}
			if observation.Trace.StrategyID != header.DuePlans[0].Identity.StrategyID {
				t.Fatalf("strategy trace=%q, want %q", observation.Trace.StrategyID, header.DuePlans[0].Identity.StrategyID)
			}
			primary := make(map[observability.AlgorithmFamily]observability.AlgorithmInputFact)
			for _, fact := range observation.AlgorithmInputs {
				if fact.InputName != observability.AlgorithmInputNamePrimary {
					continue
				}
				primary[fact.SourceAlgorithmFamily] = fact
				if fact.Result != test.want || fact.ReasonCode != observability.ReasonCode(test.reason) {
					t.Fatalf("primary input fact=%+v, want result=%q reason=%q", fact, test.want, test.reason)
				}
				if fact.Provenance.LevelID == 0 || fact.Provenance.RequirementID == "" || fact.Provenance.QueryRef == "" ||
					fact.Provenance.QueryRevision == "" || fact.Provenance.QueryEnd <= fact.Provenance.QueryStart ||
					fact.Provenance.SourceTime != 0 {
					t.Fatalf("completion-only provenance=%+v, want frozen query identity without fabricated source time", fact.Provenance)
				}
			}
			for _, family := range []observability.AlgorithmFamily{
				observability.AlgorithmFamilyThreshold, observability.AlgorithmFamilySimpleRingRatio,
			} {
				if _, found := primary[family]; !found {
					t.Fatalf("primary input facts=%+v, want family %q", observation.AlgorithmInputs, family)
				}
			}
		})
	}
}

func TestSlotExecutionCoordinatorDistinguishesCompletionOnlyPlansByStrategyTrace(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	second := plans[0]
	second.Identity.StrategyID = "8"
	second.CompiledPlan = compiledPlanForStrategyTest(t, second.Identity.StrategyID)
	plans = append(plans, second)
	requirements[0].Consumers = append(requirements[0].Consumers, execution.DataRequirementConsumer{
		Consumer:                  execution.ConsumerRef{Plan: second.Identity, LevelID: 5, HasLevel: true},
		ConsumerDeadlineUnixMilli: second.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000,
	})
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	strategies := make(map[string]int)
	for _, observation := range *fixture.observations {
		if observation.Component == observability.ComponentEvaluation && observation.Stage == observability.StageEvaluationCompleted {
			strategies[observation.Trace.StrategyID]++
		}
	}
	if !reflect.DeepEqual(strategies, map[string]int{"7": 1, "8": 1}) {
		t.Fatalf("completion-only strategy traces=%v, want one Plan-scoped observation for each strategy", strategies)
	}
}

func workerG4CompletionOnly(
	t *testing.T,
	header execution.InternalExecutionHeader,
	streamed execution.QueryExecutionCompletion,
	completeness execution.Completeness,
	dataState execution.DataState,
	disposition execution.AccessDisposition,
	reason execution.ReasonCode,
) execution.QueryExecutionCompletion {
	t.Helper()
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
	empty := execution.NewDataset(nil)
	view, err := execution.NewDatasetView(empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	queries := make(map[execution.PhysicalQueryDigest]execution.PhysicalQueryCompletion, len(streamed.PhysicalQueries))
	for _, physical := range streamed.PhysicalQueries {
		physical.Delivery = execution.SeriesDelivery{}
		physical.Completeness = completeness
		physical.DataState = dataState
		queries[physical.PhysicalQuery] = physical
		completion.PhysicalQueries = append(completion.PhysicalQueries, physical)
	}
	for _, batch := range streamedCompletionBindings(header, streamed) {
		physical := queries[batch.Provenance.PhysicalQuery]
		batch.ProviderResult = physical.Ref
		batch.Dataset, batch.View = empty, view
		batch.Completeness, batch.DataState = completeness, dataState
		batch.Disposition, batch.ReasonCode = disposition, reason
		if completeness == execution.CompletenessUnavailable {
			batch.Dataset, batch.View = nil, nil
		}
		completion.CompletionBindings = append(completion.CompletionBindings, batch)
	}
	return completion
}

func streamedCompletionBindings(
	header execution.InternalExecutionHeader,
	completion execution.QueryExecutionCompletion,
) []execution.NamedInputBinding {
	bindings := make([]execution.NamedInputBinding, 0)
	queries := make(map[execution.LogicalQueryRef]execution.PhysicalQueryCompletion, len(completion.PhysicalQueries))
	for _, physical := range completion.PhysicalQueries {
		queries[execution.LogicalQueryRef(physical.QueryRevision)] = physical
	}
	for _, requirement := range header.Requirements {
		physical := queries[requirement.LogicalQueryRef]
		for _, consumer := range requirement.Consumers {
			bindings = append(bindings, execution.NamedInputBinding{
				Consumer: consumer.Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: physical.Ref, QueryWindow: requirement.AbsoluteWindow(header.Contract.Slot.EvaluationTime),
				ImpactScope: execution.ImpactPlan,
				Provenance:  execution.InputProvenance{PhysicalQuery: physical.PhysicalQuery, AttemptNo: 1},
			})
		}
	}
	return bindings
}

func workerAlgorithmObservationCoordinator(
	t *testing.T,
	observe func(observability.Observation),
) (*recordingPorts, *worker.SlotExecutionCoordinator) {
	t.Helper()
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16,
		Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16,
			MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
			MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observe(observation)
		}),
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return ports, coordinator
}

var _ execution.Evaluator = (*evaluation.Evaluator)(nil)
