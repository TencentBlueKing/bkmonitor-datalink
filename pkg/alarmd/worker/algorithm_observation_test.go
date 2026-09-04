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
