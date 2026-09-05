package execution_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestBuildSeriesEvaluationInputRequestSupportsG4InputShapes(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		wantNames []execution.DatasetName
	}{
		{name: "threshold primary only", kind: strategy.DetectorKindThreshold, wantNames: []execution.DatasetName{"primary"}},
		{name: "simple ring ratio primary and previous", kind: strategy.DetectorKindSimpleRingRatio, wantNames: []execution.DatasetName{"primary", "previous"}},
		{name: "os restart primary and uptime history", kind: strategy.DetectorKindOsRestart, wantNames: []execution.DatasetName{"primary", "uptime_history"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, requirements := compiledG4Requirements(t, test.kind)
			header, consumer, series, bindings, completions := namedInputFixture(t, plan, requirements)
			request, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, bindings, completions)
			if err != nil {
				t.Fatalf("BuildSeriesEvaluationInputRequest() error = %v", err)
			}
			if err := request.Validate(header, completions); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if request.Contract != header.Contract || request.Consumer != consumer || request.SeriesIdentity != series {
				t.Fatalf("request scope = %+v", request)
			}
			gotNames := make([]execution.DatasetName, 0, len(request.Inputs))
			wantIDs := make([]execution.RequirementID, 0, len(request.Inputs))
			for _, input := range request.Inputs {
				gotNames = append(gotNames, input.DatasetName)
				wantIDs = append(wantIDs, input.RequirementID)
				if input.Provenance.PhysicalQuery == "" || input.ProviderResult == "" || input.View == nil {
					t.Fatalf("named input lacks query or immutable view provenance: %+v", input)
				}
			}
			if !reflect.DeepEqual(gotNames, test.wantNames) {
				t.Fatalf("input order = %v, want %v", gotNames, test.wantNames)
			}
			if !reflect.DeepEqual(request.RequirementIDs, wantIDs) {
				t.Fatalf("requirement exact set = %v, inputs = %v", request.RequirementIDs, wantIDs)
			}
		})
	}
}

func TestValidateNamedInputCompletionNarrowsOnlyReadinessInvalidConsumer(t *testing.T) {
	completion := execution.PhysicalQueryCompletion{
		Ref: "provider-1", PhysicalQuery: "query-1", QueryRevision: "revision-1",
		Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
	}
	binding := execution.NamedInputBinding{
		ProviderResult: completion.Ref,
		Completeness:   execution.CompletenessUnavailable,
		DataState:      execution.DataStateUnknown,
		Disposition:    execution.AccessUnavailable,
		ReasonCode:     execution.ReasonCode(contract.ReasonReadinessBudgetInvalid),
		ImpactScope:    execution.ImpactPlan,
		Provenance:     execution.InputProvenance{PhysicalQuery: completion.PhysicalQuery, AttemptNo: 1},
	}
	if err := execution.ValidateNamedInputCompletion(binding, completion); err != nil {
		t.Fatalf("readiness-invalid consumer should narrow shared completion: %v", err)
	}
	binding.ReasonCode = execution.ReasonCode(contract.ReasonQueryUnavailable)
	if err := execution.ValidateNamedInputCompletion(binding, completion); err == nil {
		t.Fatal("ordinary query failure widened the readiness-only completion exception")
	}
}

func TestBuildSeriesEvaluationInputRequestFailsClosedAtConsumerSeriesScope(t *testing.T) {
	plan, requirements := compiledG4Requirements(t, strategy.DetectorKindSimpleRingRatio)
	header, consumer, series, bindings, completions := namedInputFixture(t, plan, requirements)

	tests := []struct {
		name   string
		mutate func([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion)
	}{
		{name: "missing requirement", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			return bindings[:1], completions
		}},
		{name: "duplicate requirement", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			return append(bindings, bindings[0]), completions
		}},
		{name: "wrong consumer", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].Consumer.LevelID = 4
			return bindings, completions
		}},
		{name: "wrong query", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].Provenance.PhysicalQuery = bindings[0].Provenance.PhysicalQuery
			return bindings, completions
		}},
		{name: "wrong query revision", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			completions[1].QueryRevision = "query-wrong"
			return bindings, completions
		}},
		{name: "wrong time binding", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			bindings[1].QueryWindow.Start--
			return bindings, completions
		}},
		{name: "record outside frozen time window", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			fields, _ := namedInputIdentity(t, "host-a")
			dataset := namedInputDataset(fields, series, bindings[1].QueryWindow.End)
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			bindings[1].Dataset, bindings[1].View = dataset, view
			return bindings, completions
		}},
		{name: "wrong series", mutate: func(bindings []execution.NamedInputBinding, completions []execution.PhysicalQueryCompletion) ([]execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
			fields, otherSeries := namedInputIdentity(t, "host-b")
			dataset := namedInputDataset(fields, otherSeries, int64(header.Contract.Slot.EvaluationTime)-61)
			view, err := execution.NewDatasetView(dataset, []uint32{0})
			if err != nil {
				t.Fatal(err)
			}
			bindings[1].Dataset, bindings[1].View = dataset, view
			return bindings, completions
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutatedBindings := append([]execution.NamedInputBinding(nil), bindings...)
			mutatedCompletions := append([]execution.PhysicalQueryCompletion(nil), completions...)
			mutatedBindings, mutatedCompletions = test.mutate(mutatedBindings, mutatedCompletions)
			_, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, mutatedBindings, mutatedCompletions)
			if err == nil {
				t.Fatal("invalid named-input exact set must fail closed")
			}
			assertScopedInputError(t, err, consumer, series)
		})
	}
}

func TestSeriesEvaluationInputRequestRejectsRequestLocalCompletionTampering(t *testing.T) {
	plan, requirements := compiledG4Requirements(t, strategy.DetectorKindSimpleRingRatio)
	header, consumer, series, bindings, completions := namedInputFixture(t, plan, requirements)
	request, err := execution.BuildSeriesEvaluationInputRequest(header, consumer, series, bindings, completions)
	if err != nil {
		t.Fatal(err)
	}
	request.Inputs[0].ProviderResult = "forged-provider"
	err = request.Validate(header, completions)
	if err == nil {
		t.Fatal("request-local facts must not replace authoritative completion facts")
	}
	assertScopedInputError(t, err, consumer, series)
}

func TestPreparedSeriesEvaluationInputBuilderKeepsValidatedFrozenFacts(t *testing.T) {
	plan, requirements := compiledG4Requirements(t, strategy.DetectorKindSimpleRingRatio)
	header, consumer, series, bindings, completions := namedInputFixture(t, plan, requirements)
	builder, err := execution.PrepareSeriesEvaluationInputBuilder(header)
	if err != nil {
		t.Fatal(err)
	}

	header.Requirements[0].DatasetName = "mutated-after-prepare"
	header.Requirements[0].Consumers = nil
	header.RequiredPhysicalQueries[0].QueryRevision = "mutated-after-prepare"
	request, err := builder.Build(consumer, series, bindings, completions)
	if err != nil {
		t.Fatalf("Build() after source header mutation error = %v", err)
	}
	if request.Contract.Slot.QueryGroup == "" || len(request.Inputs) != 2 {
		t.Fatalf("Build() request = %+v", request)
	}
}

func TestPrepareSeriesEvaluationInputBuilderRejectsInvalidHeader(t *testing.T) {
	plan, requirements := compiledG4Requirements(t, strategy.DetectorKindSimpleRingRatio)
	header, _, _, _, _ := namedInputFixture(t, plan, requirements)
	header.Contract.DuePlanSetDigest = "tampered"
	if _, err := execution.PrepareSeriesEvaluationInputBuilder(header); err == nil {
		t.Fatal("PrepareSeriesEvaluationInputBuilder() accepted an invalid frozen header")
	}
}

func TestSeriesEvaluationInputBuilderZeroValueFailsClosed(t *testing.T) {
	var builder execution.SeriesEvaluationInputBuilder
	consumer := execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"}, LevelID: 1, HasLevel: true}
	if _, err := builder.Build(consumer, "series", nil, nil); err == nil {
		t.Fatal("zero-value SeriesEvaluationInputBuilder.Build() must fail closed")
	}
	if err := builder.ValidateCompletionOnly(consumer, nil, nil); err == nil {
		t.Fatal("zero-value SeriesEvaluationInputBuilder.ValidateCompletionOnly() must fail closed")
	}
}

func assertScopedInputError(t *testing.T, err error, consumer execution.ConsumerRef, series execution.SeriesIdentityDigest) {
	t.Helper()
	var violation *execution.EvaluationInputContractError
	if !errors.As(err, &violation) {
		t.Fatalf("error %T is not a scoped EvaluationInputContractError: %v", err, err)
	}
	if violation.Consumer != consumer || violation.SeriesIdentity != series {
		t.Fatalf("violation scope = %+v", violation)
	}
}

func compiledG4Requirements(t *testing.T, kind string) (*strategy.CompiledPlan, []execution.DataRequirement) {
	t.Helper()
	if kind == strategy.DetectorKindThreshold {
		return compiledPlanForTest(t), []execution.DataRequirement{
			namedInputRequirement(t, "primary", execution.InputRolePrimary, -60, 0, nil, execution.ReadinessEager),
		}
	}

	projection := strategy.AlgorithmInputProjection{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"},
	}
	algorithmRequirements := []strategy.AlgorithmInputRequirement{
		algorithmInputRequirement(t, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil, strategy.AlgorithmReadinessEager, projection),
	}
	config := map[string]any{}
	switch kind {
	case strategy.DetectorKindSimpleRingRatio:
		algorithmRequirements = append(algorithmRequirements,
			algorithmInputRequirement(t, "previous", strategy.AlgorithmInputDependency, -120, -60,
				[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}},
				strategy.AlgorithmReadinessFinalizedRequired, projection))
		config["floor"], config["ceil"] = 50, nil
	case strategy.DetectorKindOsRestart:
		algorithmRequirements = append(algorithmRequirements,
			algorithmInputRequirement(t, "uptime_history", strategy.AlgorithmInputDependency, -1560, 0,
				[]strategy.AlgorithmNamedInputPoint{
					{Name: "previous", OffsetSeconds: 60}, {Name: "previous_10m", OffsetSeconds: 600},
					{Name: "previous_25m", OffsetSeconds: 1500},
				}, strategy.AlgorithmReadinessFinalizedRequired, projection))
	default:
		t.Fatalf("unsupported test detector %q", kind)
	}
	config["input_projection"] = projection
	config["requirements"] = algorithmRequirements
	compiled := compileG4Plan(t, kind, config)
	algorithms := compiled.Levels()[0].Algorithms()
	if len(algorithms) != 1 || algorithms[0].Kind() != kind {
		t.Fatalf("compiled algorithms = %+v", algorithms)
	}
	compiledRequirements := algorithms[0].InputRequirements()
	if len(compiledRequirements) != len(algorithmRequirements) {
		t.Fatalf("compiled InputRequirements = %+v", compiledRequirements)
	}

	requirements := make([]execution.DataRequirement, 0, len(compiledRequirements))
	for _, requirement := range compiledRequirements {
		template := executionRequirementTemplate(t, requirement)
		requirements = append(requirements, template.Bind(execution.DataRequirementConsumer{}))
		requirements[len(requirements)-1].Consumers = nil
	}
	return compiled, requirements
}

func algorithmInputRequirement(
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
		ReadinessClass:      execution.ReadinessClass(readiness),
		InputProjection:     execution.InputProjection{ValueFields: projection.ValueFields, DimensionFields: projection.DimensionFields, IdentityFields: projection.IdentityFields},
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

func executionRequirementTemplate(t *testing.T, requirement strategy.AlgorithmInputRequirement) execution.DataRequirementTemplate {
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
		t.Fatalf("materialize compiled requirement %q: template=%+v err=%v", requirement.DatasetName, template, err)
	}
	return template
}

func compileG4Plan(t *testing.T, kind string, config map[string]any) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "named-input-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2},
			StrategyRef: ref, InputProjection: projection,
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
	return compiled
}

func namedInputRequirement(
	t *testing.T,
	name execution.DatasetName,
	role execution.InputRole,
	start, end int64,
	points []execution.NamedInputPoint,
	readiness execution.ReadinessClass,
) execution.DataRequirement {
	t.Helper()
	offsets := make([]int64, 0, len(points))
	for _, point := range points {
		offsets = append(offsets, point.OffsetSeconds)
	}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{
		DatasetName: name, Role: role, ConsumerLevelID: 5, LogicalQueryRef: execution.LogicalQueryRef("query-" + string(name)),
		RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis:     60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass:      readiness,
		InputProjection:     execution.InputProjection{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"}},
		PointOffsetsSeconds: offsets, NamedPoints: points,
	})
	if err != nil {
		t.Fatal(err)
	}
	return template.Bind(execution.DataRequirementConsumer{})
}

func namedInputFixture(t *testing.T, compiled *strategy.CompiledPlan, requirements []execution.DataRequirement) (execution.InternalExecutionHeader, execution.ConsumerRef, execution.SeriesIdentityDigest, []execution.NamedInputBinding, []execution.PhysicalQueryCompletion) {
	t.Helper()
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	plan := execution.DuePlan{Identity: identity, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule", CompletionDeadlineUnixMilli: 1_788_000_120_000}
	plans := []execution.DuePlan{plan}
	consumer := execution.ConsumerRef{Plan: identity, LevelID: 5, HasLevel: true}
	fields, series := namedInputIdentity(t, "host-a")
	for index := range requirements {
		requirements[index].Consumers = []execution.DataRequirementConsumer{{Consumer: consumer,
			ConsumerDeadlineUnixMilli: plan.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000}}
	}
	digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef := frozenContract()
	contractRef.QueryRevision = "query-primary"
	contractRef.DuePlanSetDigest = digest
	header := execution.InternalExecutionHeader{ExecutionID: "named-input-test", Contract: contractRef, DuePlans: plans,
		Requirements: requirements, DeadlineUnixMilli: plan.CompletionDeadlineUnixMilli}
	bindings := make([]execution.NamedInputBinding, 0, len(requirements))
	completions := make([]execution.PhysicalQueryCompletion, 0, len(requirements))
	for index, requirement := range requirements {
		query := execution.PlannedPhysicalQueryRef{Digest: execution.PhysicalQueryDigest("physical-" + string(requirement.RequirementID)),
			QueryRevision: execution.QueryRevision(requirement.LogicalQueryRef)}
		header.RequiredPhysicalQueries = append(header.RequiredPhysicalQueries, query)
		sourceTime := int64(contractRef.Slot.EvaluationTime) + requirement.RelativeWindow.EndOffsetSeconds - 1
		dataset := namedInputDataset(fields, series, sourceTime)
		view, viewErr := execution.NewDatasetView(dataset, []uint32{0})
		if viewErr != nil {
			t.Fatal(viewErr)
		}
		providerRef := execution.ProviderResultRef("provider-" + string(requirement.RequirementID))
		bindings = append(bindings, execution.NamedInputBinding{Consumer: consumer, RequirementID: requirement.RequirementID,
			DatasetName: requirement.DatasetName, Role: requirement.Role, ProviderResult: providerRef,
			QueryWindow: requirement.AbsoluteWindow(contractRef.Slot.EvaluationTime), Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable,
			ImpactScope: execution.ImpactSeries, Provenance: execution.InputProvenance{PhysicalQuery: query.Digest, AttemptNo: 1}})
		completions = append(completions, execution.PhysicalQueryCompletion{Ref: providerRef, PhysicalQuery: query.Digest,
			QueryRevision: query.QueryRevision, Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			Delivery: execution.SeriesDelivery{PhysicalQuery: query.Digest, QueryRevision: query.QueryRevision,
				Series: 1, Records: 1, Digest: strings.Repeat(string(rune('a'+index)), 64)}})
	}
	return header, consumer, series, bindings, completions
}

func namedInputIdentity(t *testing.T, host string) ([]contract.DimensionFieldV2, execution.SeriesIdentityDigest) {
	t.Helper()
	value, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	fields := []contract.DimensionFieldV2{{Name: "host", Value: value}}
	digest, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
	if err != nil {
		t.Fatal(err)
	}
	return fields, execution.SeriesIdentityDigest(digest)
}

func namedInputDataset(fields []contract.DimensionFieldV2, series execution.SeriesIdentityDigest, sourceTime int64) *execution.Dataset {
	host := append(json.RawMessage(nil), fields[0].Value...)
	return execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: strings.Repeat("b", 64), SourceTime: sourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: string(series)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`1`)},
		Dimensions:        map[string]json.RawMessage{"host": host}, ReceivedTime: sourceTime,
	}})
}
