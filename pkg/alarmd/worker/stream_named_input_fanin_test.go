package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
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

func TestSlotExecutionCoordinatorRejectsTamperedCompletionBeforeLoadingGapOrState(t *testing.T) {
	header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
	tampered := batches[0].Inputs[0]
	tampered.RequirementID = "tampered-requirement"
	completion.CompletionBindings = append(completion.CompletionBindings, tampered)
	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.executeOverride = streamExecution(header, batches, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v, want local completion rejection", result, err)
	}
	if ports.stateLoadCalls != 0 || len(evaluator.requests) != 0 {
		t.Fatalf("tampered completion loaded state or evaluated: state=%d evaluate=%d", ports.stateLoadCalls, len(evaluator.requests))
	}
	if trace := strings.Join(*ports.trace, ","); strings.Contains(trace, "gap_load") {
		t.Fatalf("tampered completion loaded Gap before exact input validation: %s", trace)
	}
}

func TestSlotExecutionCoordinatorRejectsTamperedNamedInputBeforeLoadingGapOrState(t *testing.T) {
	header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
	batches[0].Inputs[0].ImpactScope = ""
	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.executeOverride = streamExecution(header, batches, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v, want tampered binding rejection", result, err)
	}
	if ports.stateLoadCalls != 0 || ports.stateApplyCalls != 0 || ports.eventCount != 0 ||
		len(evaluator.requests) != 0 || !isZeroProgressCommit(ports.lastProgress) {
		t.Fatalf("tampered binding escaped: State load/apply=%d/%d Events=%d Evaluate=%d Progress=%+v",
			ports.stateLoadCalls, ports.stateApplyCalls, ports.eventCount, len(evaluator.requests), ports.lastProgress)
	}
	if trace := strings.Join(*ports.trace, ","); strings.Contains(trace, "gap_load") {
		t.Fatalf("tampered binding loaded Gap before A0 validation: %s", trace)
	}
}

func TestSlotExecutionCoordinatorRejectsCompletionOnlyMissingLevelRequirement(t *testing.T) {
	header, completion := workerG4MultiLevelCompletionOnlyFixture(t)
	for index, binding := range completion.CompletionBindings {
		if binding.Consumer.LevelID == 4 {
			completion.CompletionBindings = append(completion.CompletionBindings[:index], completion.CompletionBindings[index+1:]...)
			break
		}
	}
	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.executeOverride = streamExecution(header, nil, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v, want missing Level requirement rejection", result, err)
	}
	if ports.stateLoadCalls != 0 || ports.stateApplyCalls != 0 || ports.eventCount != 0 ||
		len(evaluator.requests) != 0 || !isZeroProgressCommit(ports.lastProgress) {
		t.Fatalf("missing Level exact set escaped: State load/apply=%d/%d Events=%d Evaluate=%d Progress=%+v",
			ports.stateLoadCalls, ports.stateApplyCalls, ports.eventCount, len(evaluator.requests), ports.lastProgress)
	}
}

func TestSlotExecutionCoordinatorValidatesCompletionOnlyBindingsBeforeLoadingGapOrState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*execution.NamedInputBinding)
	}{
		{name: "impact scope", mutate: func(binding *execution.NamedInputBinding) { binding.ImpactScope = "" }},
		{name: "dataset view", mutate: func(binding *execution.NamedInputBinding) { binding.View = nil }},
		{name: "disposition", mutate: func(binding *execution.NamedInputBinding) {
			binding.Disposition = execution.AccessDegraded
		}},
		{name: "reason", mutate: func(binding *execution.NamedInputBinding) {
			binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, completion := workerG4MultiLevelCompletionOnlyFixture(t)
			test.mutate(&completion.CompletionBindings[0])
			ports, evaluator, coordinator := workerG4Coordinator(t)
			ports.executeOverride = streamExecution(header, nil, completion)

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v, want invalid completion-only binding rejection", result, err)
			}
			if ports.stateLoadCalls != 0 || ports.stateApplyCalls != 0 || ports.eventCount != 0 ||
				len(evaluator.requests) != 0 || !isZeroProgressCommit(ports.lastProgress) {
				t.Fatalf("invalid completion-only binding escaped: State load/apply=%d/%d Events=%d Evaluate=%d Progress=%+v",
					ports.stateLoadCalls, ports.stateApplyCalls, ports.eventCount, len(evaluator.requests), ports.lastProgress)
			}
			if trace := strings.Join(*ports.trace, ","); strings.Contains(trace, "gap_load") {
				t.Fatalf("invalid completion-only binding loaded Gap: %s", trace)
			}
		})
	}
}

func TestSlotExecutionCoordinatorFansInTwoLevelsOnceAfterAllQueriesComplete(t *testing.T) {
	header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.executeOverride = streamExecution(header, batches, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(evaluator.requests) != 1 {
		t.Fatalf("Evaluate requests=%d, want one Plan+series request after completion", len(evaluator.requests))
	}
	request := evaluator.requests[0]
	if len(request.Inputs) != 2 {
		t.Fatalf("plural A0 inputs=%+v", request.Inputs)
	}
	inputsByLevel := make(map[uint32]execution.SeriesEvaluationInputRequest, len(request.Inputs))
	for _, input := range request.Inputs {
		inputsByLevel[input.Consumer.LevelID] = input
	}
	if len(inputsByLevel[5].Inputs) != 2 || len(inputsByLevel[4].Inputs) != 1 {
		t.Fatalf("named input exact cover=%+v", request.Inputs)
	}
	if inputsByLevel[5].Inputs[0].Provenance.PhysicalQuery != inputsByLevel[4].Inputs[0].Provenance.PhysicalQuery {
		t.Fatalf("shared PRIMARY logical query was not reused: %+v", request.Inputs)
	}
	if ports.stateLoadCalls != 1 {
		t.Fatalf("State loads=%d, want once for one Plan+series", ports.stateLoadCalls)
	}
}

func TestSlotExecutionCoordinatorKeepsHealthyLevelAndQueryGroupRunningWhenOneDependencyIsUnavailable(t *testing.T) {
	header, batches, completion := workerG4MultiLevelStreamFixture(t, true)
	ports, evaluator, coordinator := workerG4Coordinator(t)
	ports.gapMissing = true
	ports.executeOverride = streamExecution(header, batches, completion)

	result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
	if err != nil || !result.Completed || result.Result != observability.ResultDegraded {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(evaluator.results) != 1 || len(evaluator.results[0].Plans) != 1 {
		t.Fatalf("evaluation results=%+v", evaluator.results)
	}
	outcomes := evaluator.results[0].Plans[0].LevelOutcomes
	outcomeByLevel := make(map[uint32]execution.LevelOutcome, len(outcomes))
	for _, outcome := range outcomes {
		outcomeByLevel[outcome.LevelID] = outcome
	}
	if len(outcomeByLevel) != 2 || outcomeByLevel[5].Outcome != execution.LevelOutcomeUnknown ||
		outcomeByLevel[4].Outcome != execution.LevelOutcomeAbnormal {
		t.Fatalf("Level-local unavailable result=%+v", outcomes)
	}
	if ports.stateApplyCalls != 1 || ports.eventCount != 1 {
		t.Fatalf("healthy Level side effects state=%d events=%d", ports.stateApplyCalls, ports.eventCount)
	}
	if len(ports.gapMutations) != 1 || len(ports.gapMutations[0].Scopes) != 1 ||
		ports.gapMutations[0].Scopes[0].Scope.LevelID != 5 {
		t.Fatalf("unavailable dependency was not isolated to Level 5: %+v", ports.gapMutations)
	}

	healthy := newFixture(t, true, "")
	healthyResult, healthyErr := healthy.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if healthyErr != nil || !healthyResult.Completed || healthy.ports.stateApplyCalls != 1 {
		t.Fatalf("healthy sibling QG result=%+v error=%v state=%d", healthyResult, healthyErr, healthy.ports.stateApplyCalls)
	}
}

type recordingEvaluator struct {
	inner    execution.Evaluator
	requests []execution.EvaluationRequest
	results  []execution.EvaluationResult
	// fail replaces every evaluation with this error; mutate alters a
	// successful result before the worker validates it.
	fail   error
	mutate func(*execution.EvaluationResult)
}

func (e *recordingEvaluator) Evaluate(ctx context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	e.requests = append(e.requests, request)
	if e.fail != nil {
		return execution.EvaluationResult{}, e.fail
	}
	result, err := e.inner.Evaluate(ctx, request)
	if err == nil {
		if e.mutate != nil {
			e.mutate(&result)
		}
		e.results = append(e.results, result)
	}
	return result, err
}

func workerG4Coordinator(t *testing.T) (*recordingPorts, *recordingEvaluator, *worker.SlotExecutionCoordinator) {
	t.Helper()
	return workerG4CoordinatorWithObserver(t, observability.ObserverFunc(func(context.Context, observability.Observation) {}))
}

func workerG4CoordinatorWithObserver(
	t *testing.T,
	observer observability.Observer,
) (*recordingPorts, *recordingEvaluator, *worker.SlotExecutionCoordinator) {
	t.Helper()
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := evaluation.New(detector, evaluation.Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16,
		Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16,
			MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
			MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingEvaluator{inner: inner}
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: recorder,
		Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: ports,
		Observer: observer,
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return ports, recorder, coordinator
}

func streamExecution(
	header execution.InternalExecutionHeader,
	batches []execution.SeriesExecutionBatch,
	completion execution.QueryExecutionCompletion,
) func(context.Context, execution.QueryExecutionRequest, execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
	return func(ctx context.Context, _ execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
		if err := consumer.Begin(ctx, header); err != nil {
			return execution.QueryExecutionCompletion{}, err
		}
		for _, batch := range batches {
			if err := consumer.ConsumeSeries(ctx, batch); err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
		}
		return completion, nil
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

func workerG4MultiLevelStreamFixture(
	t *testing.T,
	previousUnavailable bool,
) (execution.InternalExecutionHeader, []execution.SeriesExecutionBatch, execution.QueryExecutionCompletion) {
	t.Helper()
	projection := strategy.AlgorithmInputProjection{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"},
	}
	simpleRequirements := []strategy.AlgorithmInputRequirement{
		workerAlgorithmRequirementForLevel(t, 5, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil,
			strategy.AlgorithmReadinessEager, projection),
		workerAlgorithmRequirementForLevel(t, 5, "previous", strategy.AlgorithmInputDependency, -120, -60,
			[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}},
			strategy.AlgorithmReadinessFinalizedRequired, projection),
	}
	simpleConfig, err := json.Marshal(map[string]any{
		"floor": 20, "ceil": nil, "input_projection": projection, "requirements": simpleRequirements,
	})
	if err != nil {
		t.Fatal(err)
	}
	thresholdConfig := json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)
	compiled := compileWorkerG4MultiLevelPlan(t, simpleConfig, thresholdConfig)

	var algorithmRequirements []strategy.AlgorithmInputRequirement
	for _, level := range compiled.Levels() {
		if level.Definition().LevelID == 5 {
			algorithmRequirements = append(algorithmRequirements, level.Algorithms()[0].InputRequirements()...)
		}
	}
	algorithmRequirements = append(algorithmRequirements,
		workerAlgorithmRequirementForLevel(t, 4, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil,
			strategy.AlgorithmReadinessEager, projection))
	requirements := make([]execution.DataRequirement, 0, len(algorithmRequirements))
	identity := planIdentity()
	due := execution.DuePlan{Identity: identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule-v1", CompletionDeadlineUnixMilli: 1_788_000_060_000}
	for _, requirement := range algorithmRequirements {
		materialized := materializeWorkerRequirement(t, requirement)
		materialized.Consumers = []execution.DataRequirementConsumer{{
			Consumer:                  execution.ConsumerRef{Plan: identity, LevelID: requirement.ConsumerLevelID, HasLevel: true},
			ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000,
		}}
		requirements = append(requirements, materialized)
	}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.QueryRevision = "query-primary"
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{ExecutionID: "g4-two-level-fan-in", Contract: contractRef,
		DuePlans: []execution.DuePlan{due}, Requirements: requirements, DeadlineUnixMilli: due.CompletionDeadlineUnixMilli}

	byQuery := make(map[execution.LogicalQueryRef][]execution.DataRequirement)
	for _, requirement := range requirements {
		byQuery[requirement.LogicalQueryRef] = append(byQuery[requirement.LogicalQueryRef], requirement)
	}
	queryRefs := make([]execution.LogicalQueryRef, 0, len(byQuery))
	for queryRef := range byQuery {
		queryRefs = append(queryRefs, queryRef)
	}
	sort.Slice(queryRefs, func(i, j int) bool { return queryRefs[i] < queryRefs[j] })
	series := execution.SeriesIdentityDigest(strings.Repeat("c", 64))
	var batches []execution.SeriesExecutionBatch
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
	for queryIndex, queryRef := range queryRefs {
		physical := execution.PhysicalQueryDigest("physical-" + string(queryRef))
		queryRevision := execution.QueryRevision(queryRef)
		header.RequiredPhysicalQueries = append(header.RequiredPhysicalQueries,
			execution.PlannedPhysicalQueryRef{Digest: physical, QueryRevision: queryRevision})
		providerRef := execution.ProviderResultRef("provider-" + string(queryRef))
		representative := byQuery[queryRef][0]
		sourceTime := int64(contractRef.Slot.EvaluationTime) + representative.RelativeWindow.EndOffsetSeconds - 1
		value := json.RawMessage(`80`)
		if queryRef == "query-previous" {
			value = json.RawMessage(`100`)
		}
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
			RecordID: fmt.Sprintf("%064x", queryIndex+1), SourceTime: sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: string(series)},
			Values:            map[string]json.RawMessage{"value": value}, Dimensions: map[string]json.RawMessage{},
			ReceivedTime: sourceTime,
		}})
		view, viewErr := execution.NewDatasetView(dataset, []uint32{0})
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		bindings := make([]execution.NamedInputBinding, 0, len(byQuery[queryRef]))
		for _, requirement := range byQuery[queryRef] {
			bindings = append(bindings, execution.NamedInputBinding{
				Consumer: requirement.Consumers[0].Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role, ProviderResult: providerRef,
				QueryWindow: requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime), Dataset: dataset, View: view,
				Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactSeries,
				Provenance: execution.InputProvenance{PhysicalQuery: physical, AttemptNo: 1},
			})
		}
		delivery := execution.SeriesDelivery{PhysicalQuery: physical, QueryRevision: queryRevision,
			Series: 1, Records: 1, Bytes: 64, Digest: strings.Repeat(string(rune('e'+queryIndex)), 64)}
		if previousUnavailable && queryRef == "query-previous" {
			completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
				Ref: providerRef, PhysicalQuery: physical, QueryRevision: queryRevision,
				Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
				RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{
					AttemptNo: 1, Endpoint: "uq", Result: execution.RouteAttemptFailed,
					ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable),
				}}},
			})
			continue
		}
		batches = append(batches, execution.SeriesExecutionBatch{PhysicalQuery: physical,
			QueryRevision: queryRevision, CompletionRef: providerRef, Dataset: dataset, Inputs: bindings, Delivery: delivery})
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: providerRef, PhysicalQuery: physical, QueryRevision: queryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: delivery,
		})
	}
	return header, batches, completion
}

func workerG4MultiLevelCompletionOnlyFixture(t *testing.T) (execution.InternalExecutionHeader, execution.QueryExecutionCompletion) {
	t.Helper()
	header, _, _ := workerG4MultiLevelStreamFixture(t, false)
	queries := make(map[execution.LogicalQueryRef]execution.PlannedPhysicalQueryRef, len(header.RequiredPhysicalQueries))
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
	for _, query := range header.RequiredPhysicalQueries {
		queries[execution.LogicalQueryRef(query.QueryRevision)] = query
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: "empty-" + execution.ProviderResultRef(query.Digest), PhysicalQuery: query.Digest,
			QueryRevision: query.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
		})
	}
	empty := execution.NewDataset(nil)
	view, err := execution.NewDatasetView(empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range header.Requirements {
		query := queries[requirement.LogicalQueryRef]
		for _, consumer := range requirement.Consumers {
			completion.CompletionBindings = append(completion.CompletionBindings, execution.NamedInputBinding{
				Consumer: consumer.Consumer, RequirementID: requirement.RequirementID,
				DatasetName: requirement.DatasetName, Role: requirement.Role,
				ProviderResult: "empty-" + execution.ProviderResultRef(query.Digest),
				QueryWindow:    requirement.AbsoluteWindow(header.Contract.Slot.EvaluationTime),
				Dataset:        empty, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
				Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
				Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1},
			})
		}
	}
	return header, completion
}

func compileWorkerG4MultiLevelPlan(t *testing.T, simpleConfig, thresholdConfig json.RawMessage) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "worker-g4-two-level-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"},
		BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent",
		MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := func(id uint32, kind string, config json.RawMessage) contract.LevelIRV2 {
		return contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: id, Priority: id}, Connector: contract.LevelConnectorAND,
			DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: kind, Version: 1, Config: config}}},
			TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1,
				Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
			RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1,
				Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	}
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2},
			StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries,
				QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			Levels: []contract.LevelIRV2{level(5, strategy.DetectorKindSimpleRingRatio, simpleConfig),
				level(4, strategy.DetectorKindThreshold, thresholdConfig)}}}
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
		t.Fatalf("compile terminal=%+v levels=%+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func materializeWorkerRequirement(t *testing.T, requirement strategy.AlgorithmInputRequirement) execution.DataRequirement {
	t.Helper()
	points := make([]execution.NamedInputPoint, len(requirement.NamedPoints))
	for index, point := range requirement.NamedPoints {
		points[index] = execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds}
	}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
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
	if err != nil || string(template.RequirementID) != requirement.RequirementID {
		t.Fatalf("materialize %q: template=%+v error=%v", requirement.DatasetName, template, err)
	}
	return template.Bind(execution.DataRequirementConsumer{})
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
	return workerAlgorithmRequirementForLevel(t, 5, name, role, start, end, points, readiness, projection)
}

func workerAlgorithmRequirementForLevel(
	t *testing.T,
	levelID uint32,
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
		DatasetName: execution.DatasetName(name), Role: execution.InputRole(role), ConsumerLevelID: levelID,
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
		RequirementID: string(template.RequirementID), DatasetName: name, Role: role, ConsumerLevelID: levelID,
		LogicalQueryRef: "query-" + name,
		RelativeWindow:  strategy.AlgorithmRelativeWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis:      60_000, AlignmentMillis: 60_000, ReadinessClass: readiness, InputProjection: projection,
		PointOffsetsSeconds: offsets, NamedPoints: points,
	}
}
