package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestSlotExecutionCoordinatorWaitsForAuthoritativeG4NamedInputCompletion(t *testing.T) {
	tests := []struct {
		name string
		kind string
	}{
		{name: "SimpleRingRatio primary and previous", kind: strategy.DetectorKindSimpleRingRatio},
		{name: "OsRestart primary and uptime history", kind: strategy.DetectorKindOsRestart},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, batches := workerG4StreamFixture(t, test.kind)
			for _, delivered := range []int{1, len(batches)} {
				t.Run(fmt.Sprintf("delivered_%d", delivered), func(t *testing.T) {
					fixture := newFixture(t, true, "")
					stop := errors.New("stop before authoritative completion")
					fixture.ports.executeOverride = func(
						ctx context.Context,
						_ execution.QueryExecutionRequest,
						consumer execution.QueryExecutionConsumer,
					) (execution.QueryExecutionCompletion, error) {
						if err := consumer.Begin(ctx, header); err != nil {
							return execution.QueryExecutionCompletion{}, err
						}
						for _, batch := range batches[:delivered] {
							if err := consumer.ConsumeSeries(ctx, batch); err != nil {
								return execution.QueryExecutionCompletion{}, err
							}
						}
						return execution.QueryExecutionCompletion{}, stop
					}

					result, err := fixture.coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
					if err == nil || result.Completed {
						t.Fatalf("Execute() result=%+v error=%v, want incomplete query", result, err)
					}
					if got := append([]string(nil), (*fixture.trace)...); !reflect.DeepEqual(got, []string{"query"}) {
						t.Fatalf("pre-completion trace=%v, want no Gap/State/Evaluate before authoritative completion", got)
					}
				})
			}
		})
	}
}

func workerSlotRequest(contractRef execution.FrozenExecutionContractRef) execution.SlotExecutionRequest {
	return execution.SlotExecutionRequest{
		Contract: contractRef,
		DuePlanTargets: execution.FrozenDuePlanTargets{
			DuePlanSetDigest: contractRef.DuePlanSetDigest,
			Plans:            []execution.PlanIdentity{planIdentity()},
		},
		EarliestQueryDeadlineUnixMilli: int64(contractRef.Slot.EvaluationTime)*1000 + 1_000,
		RecoveryUntilUnixMilli:         int64(contractRef.Slot.EvaluationTime)*1000 + 601_000,
		KeepUntilUnixMilli:             int64(contractRef.Slot.EvaluationTime)*1000 + 677_000,
		Operation:                      execution.OperationNormal,
		AttemptNo:                      1,
		OwnerFence: execution.OwnerFence{
			QueryGroup: contractRef.Slot.QueryGroup, OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1",
		},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
	}
}

func workerG4StreamFixture(t *testing.T, kind string) (execution.InternalExecutionHeader, []execution.SeriesExecutionBatch) {
	t.Helper()
	compiled, requirements := workerG4CompiledPlan(t, kind)
	identity := planIdentity()
	due := execution.DuePlan{
		Identity: identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule-v1", CompletionDeadlineUnixMilli: 1_788_000_060_000,
	}
	consumer := execution.ConsumerRef{Plan: identity, LevelID: 5, HasLevel: true}
	for index := range requirements {
		requirements[index].Consumers = []execution.DataRequirementConsumer{{
			Consumer: consumer, ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli,
			DownstreamExecutionReserveMilliSec: 5_000,
		}}
	}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.QueryRevision = "query-primary"
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{
		ExecutionID: "g4-fan-in-red", Contract: contractRef, DuePlans: []execution.DuePlan{due},
		Requirements: requirements, DeadlineUnixMilli: due.CompletionDeadlineUnixMilli,
	}
	series := execution.SeriesIdentityDigest(strings.Repeat("c", 64))
	batches := make([]execution.SeriesExecutionBatch, 0, len(requirements))
	for index, requirement := range requirements {
		query := execution.PlannedPhysicalQueryRef{
			Digest:        execution.PhysicalQueryDigest("physical-" + string(requirement.RequirementID)),
			QueryRevision: execution.QueryRevision(requirement.LogicalQueryRef),
		}
		header.RequiredPhysicalQueries = append(header.RequiredPhysicalQueries, query)
		sourceTime := int64(contractRef.Slot.EvaluationTime) + requirement.RelativeWindow.EndOffsetSeconds - 1
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
			RecordID: strings.Repeat(string(rune('a'+index)), 64), SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: string(series)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`1`)},
			Dimensions:        map[string]json.RawMessage{}, ReceivedTime: sourceTime,
		}})
		view, viewErr := execution.NewDatasetView(dataset, []uint32{0})
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		providerRef := execution.ProviderResultRef("provider-" + string(requirement.RequirementID))
		binding := execution.NamedInputBinding{
			Consumer: consumer, RequirementID: requirement.RequirementID, DatasetName: requirement.DatasetName,
			Role: requirement.Role, ProviderResult: providerRef, QueryWindow: requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime),
			Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactSeries,
			Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1},
		}
		batches = append(batches, execution.SeriesExecutionBatch{
			PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision, CompletionRef: providerRef,
			Dataset: dataset, Inputs: []execution.NamedInputBinding{binding},
			Delivery: execution.SeriesDelivery{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
				Series: 1, Records: 1, Bytes: 64, Digest: strings.Repeat(string(rune('f'-index)), 64)},
		})
	}
	return header, batches
}

func workerG4CompiledPlan(t *testing.T, kind string) (*strategy.CompiledPlan, []execution.DataRequirement) {
	t.Helper()
	projection := strategy.AlgorithmInputProjection{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"},
	}
	requirements := []strategy.AlgorithmInputRequirement{
		workerAlgorithmRequirement(t, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil,
			strategy.AlgorithmReadinessEager, projection),
	}
	config := map[string]any{}
	switch kind {
	case strategy.DetectorKindSimpleRingRatio:
		requirements = append(requirements, workerAlgorithmRequirement(t, "previous", strategy.AlgorithmInputDependency,
			-120, -60, []strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}},
			strategy.AlgorithmReadinessFinalizedRequired, projection))
		config["floor"], config["ceil"] = 50, nil
	case strategy.DetectorKindOsRestart:
		requirements = append(requirements, workerAlgorithmRequirement(t, "uptime_history", strategy.AlgorithmInputDependency,
			-1560, 0, []strategy.AlgorithmNamedInputPoint{
				{Name: "previous", OffsetSeconds: 60}, {Name: "previous_10m", OffsetSeconds: 600},
				{Name: "previous_25m", OffsetSeconds: 1500},
			}, strategy.AlgorithmReadinessFinalizedRequired, projection))
	default:
		t.Fatalf("unsupported G4 detector %q", kind)
	}
	config["input_projection"] = projection
	config["requirements"] = requirements
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "worker-g4-fan-in-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	inputProjection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: inputProjection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2},
			StrategyRef: ref, InputProjection: inputProjection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries,
				QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			Levels: []contract.LevelIRV2{{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1},
				Connector:  contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: kind, Version: 1, Config: payload}}},
				TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
					Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1,
					Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}}}}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64),
			NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-v1",
			HistoryCellSemanticsVersion: "history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compile %s terminal=%+v levels=%+v", kind, result.PlanTerminal(), result.LevelTerminals())
	}
	algorithmRequirements := compiled.Levels()[0].Algorithms()[0].InputRequirements()
	executionRequirements := make([]execution.DataRequirement, 0, len(algorithmRequirements))
	for _, requirement := range algorithmRequirements {
		points := make([]execution.NamedInputPoint, len(requirement.NamedPoints))
		for index, point := range requirement.NamedPoints {
			points[index] = execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds}
		}
		template, buildErr := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
			DatasetName: execution.DatasetName(requirement.DatasetName), Role: execution.InputRole(requirement.Role),
			ConsumerLevelID: requirement.ConsumerLevelID, LogicalQueryRef: execution.LogicalQueryRef(requirement.LogicalQueryRef),
			RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: requirement.RelativeWindow.StartOffsetSeconds,
				EndOffsetSeconds: requirement.RelativeWindow.EndOffsetSeconds, HalfOpen: requirement.RelativeWindow.HalfOpen},
			StepMillis: requirement.StepMillis, AlignmentMillis: requirement.AlignmentMillis,
			ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessClass(requirement.ReadinessClass),
			InputProjection: execution.InputProjection{ValueFields: requirement.InputProjection.ValueFields,
				DimensionFields: requirement.InputProjection.DimensionFields, IdentityFields: requirement.InputProjection.IdentityFields},
			PointOffsetsSeconds: requirement.PointOffsetsSeconds, NamedPoints: points,
		})
		if buildErr != nil || string(template.RequirementID) != requirement.RequirementID {
			t.Fatalf("materialize %q: template=%+v error=%v", requirement.DatasetName, template, buildErr)
		}
		executionRequirements = append(executionRequirements, template.Bind(execution.DataRequirementConsumer{}))
	}
	return compiled, executionRequirements
}

func workerAlgorithmRequirement(
	t *testing.T,
	name string,
	role strategy.AlgorithmInputRole,
	start, end int64,
	points []strategy.AlgorithmNamedInputPoint,
	readiness strategy.AlgorithmReadinessClass,
	projection strategy.AlgorithmInputProjection,
) strategy.AlgorithmInputRequirement {
	t.Helper()
	offsets := make([]int64, 0, len(points))
	executionPoints := make([]execution.NamedInputPoint, 0, len(points))
	for _, point := range points {
		offsets = append(offsets, point.OffsetSeconds)
		executionPoints = append(executionPoints, execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds})
	}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
		DatasetName: execution.DatasetName(name), Role: execution.InputRole(role), ConsumerLevelID: 5,
		LogicalQueryRef: execution.LogicalQueryRef("query-" + name),
		RelativeWindow:  execution.RelativeQueryWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis:      60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass: execution.ReadinessClass(readiness),
		InputProjection: execution.InputProjection{ValueFields: projection.ValueFields,
			DimensionFields: projection.DimensionFields, IdentityFields: projection.IdentityFields},
		PointOffsetsSeconds: offsets, NamedPoints: executionPoints,
	})
	if err != nil {
		t.Fatal(err)
	}
	return strategy.AlgorithmInputRequirement{
		RequirementID: string(template.RequirementID), DatasetName: name, Role: role, ConsumerLevelID: 5,
		LogicalQueryRef: "query-" + name,
		RelativeWindow:  strategy.AlgorithmRelativeWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis:      60_000, AlignmentMillis: 60_000, ReadinessClass: readiness, InputProjection: projection,
		PointOffsetsSeconds: offsets, NamedPoints: points,
	}
}
