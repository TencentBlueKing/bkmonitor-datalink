// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestEvaluateSeriesProducesOneAbnormalAndRecoveryForNamedInputs(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})

	abnormalRequest := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `80`, nil)}, nil)
	abnormalInput := g4Input(t, abnormalRequest, map[string][]contract.CanonicalRecordV2{
		"primary":  {g4Record(99, `80`, nil)},
		"previous": {g4Record(39, `100`, nil)},
	})
	abnormal, err := newEvaluator(t).evaluateSeries(context.Background(), abnormalRequest.Header,
		[]execution.SeriesEvaluationInputRequest{abnormalInput}, abnormalRequest.State, abnormalRequest.Gaps)
	if err != nil {
		t.Fatalf("evaluateSeries(abnormal) error = %v", err)
	}
	if len(abnormal.LevelOutcomes) != 1 || abnormal.LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal ||
		len(abnormal.StateResults) != 1 || len(abnormal.StateResults[0].Events) != 1 {
		t.Fatalf("abnormal result = %+v", abnormal)
	}

	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("d", 64), SourceTime: 39,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous}}}}
	recoveryRequest := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `100`, nil)}, history)
	recoveryInput := g4Input(t, recoveryRequest, map[string][]contract.CanonicalRecordV2{
		"primary":  {g4Record(99, `100`, nil)},
		"previous": {g4Record(39, `100`, nil)},
	})
	recovery, err := newEvaluator(t).evaluateSeries(context.Background(), recoveryRequest.Header,
		[]execution.SeriesEvaluationInputRequest{recoveryInput}, recoveryRequest.State, recoveryRequest.Gaps)
	if err != nil {
		t.Fatalf("evaluateSeries(recovery) error = %v", err)
	}
	if len(recovery.LevelOutcomes) != 1 || recovery.LevelOutcomes[0].Outcome != execution.LevelOutcomeRecovery ||
		len(recovery.StateResults) != 1 || len(recovery.StateResults[0].Events) != 1 {
		t.Fatalf("recovery result = %+v", recovery)
	}
}

func TestEvaluateSeriesKeepsMissingHistoryLocalAndDoesNotAdvanceState(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `80`, nil)}, nil)
	input := g4Input(t, request, map[string][]contract.CanonicalRecordV2{"primary": {g4Record(99, `80`, nil)}})

	result, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
		[]execution.SeriesEvaluationInputRequest{input}, request.State, request.Gaps)
	if err != nil {
		t.Fatalf("missing previous must be a Level-local outcome: %v", err)
	}
	if result.Disposition != execution.PlanDecidedDegraded || len(result.LevelOutcomes) != 1 ||
		result.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		result.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonHistoryGapped) || len(result.StateResults) != 0 {
		t.Fatalf("missing-history result = %+v", result)
	}

	healthy := requestFixture(t, json.RawMessage(`80`), nil)
	if _, err := newEvaluator(t).Evaluate(context.Background(), healthy); err != nil {
		t.Fatalf("a bad G4 series affected a healthy Threshold request: %v", err)
	}
}

// A series whose loaded Level guard is still active (WARMING or GAPPED with a
// durable reason) and whose ring-ratio dependency point is missing produced an
// UNKNOWN outcome with the local HISTORY_GAPPED reason. The result contract
// requires every UNKNOWN outcome under an active guard to preserve the guard
// reason, so validation rejected the evaluation and the Slot failed with
// "UNKNOWN Level outcome does not preserve its active guard reason". The same
// happened when the loaded history was already FULL and the new record opened
// a hole. The evaluator must keep the durable guard reason for every UNKNOWN
// outcome while the guard stays active, whatever made the outcome UNKNOWN.
func TestEvaluateSeriesPreservesActiveStateGuardReasonForEveryUnknownOutcome(t *testing.T) {
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	config := func() map[string]any { return map[string]any{"floor": 20, "ceil": nil} }
	single := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, config(), projection)
	double := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, config(), projection, 2, 2)
	guard := execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	normal := func(plan *strategy.CompiledPlan, id string, sourceTime int64) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactNormal}}}
	}
	tests := []struct {
		name         string
		plan         *strategy.CompiledPlan
		status       execution.StateLoadStatus
		completeness execution.HistoryCompleteness
		history      []execution.StateHistoryPoint
		primary      contract.CanonicalRecordV2
		previous     []contract.CanonicalRecordV2
		advances     bool
	}{
		{
			// The evaluated production shape: the loaded Level is WARMING under
			// a durable reason and the ring-ratio previous point is missing.
			name: "warming guard and missing dependency point", plan: single,
			status: execution.StateFoundWarming, completeness: execution.HistoryWarming, primary: g4Record(99, `80`, nil),
		},
		{
			name: "gapped guard and missing dependency point", plan: single,
			status: execution.StateFoundGapped, completeness: execution.HistoryGapped, primary: g4Record(99, `80`, nil),
		},
		{
			// The loaded history already lets WARMING converge, so the evaluator
			// stops treating the guard as active while the durable state and the
			// result contract still carry it.
			name: "converged warming guard and missing dependency point", plan: single,
			status: execution.StateFoundWarming, completeness: execution.HistoryWarming,
			history: []execution.StateHistoryPoint{normal(single, "d", 39)}, primary: g4Record(99, `80`, nil),
		},
		{
			// Converged WARMING and a hole before the new record: the trigger sees
			// an incomplete window and the State advances still guarded, under the
			// same reason.
			name: "converged warming guard and a hole before the new record", plan: double,
			status: execution.StateFoundWarming, completeness: execution.HistoryWarming,
			history:  []execution.StateHistoryPoint{normal(double, "d", 180), normal(double, "e", 240)},
			primary:  g4Record(360, `80`, nil),
			previous: []contract.CanonicalRecordV2{g4Record(300, `100`, nil)},
			advances: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := requestFixtureForPlan(t, test.plan, []contract.CanonicalRecordV2{test.primary}, test.history)
			request.State.Items[0].Status = test.status
			request.State.Items[0].Levels[0].HistoryCompleteness = test.completeness
			request.State.Items[0].Levels[0].GapReasonCode = guard
			if len(test.history) != 0 {
				request.State.Items[0].Levels[0].LastProcessedEventTime = test.history[len(test.history)-1].SourceTime
			}
			inputs := map[string][]contract.CanonicalRecordV2{"primary": {test.primary}, "previous": test.previous}
			request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request, inputs)}

			result, err := newEvaluator(t).Evaluate(context.Background(), request)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			evaluated := result.Plans[0]
			if len(evaluated.LevelOutcomes) != 1 || evaluated.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
				evaluated.LevelOutcomes[0].ReasonCode != guard {
				t.Fatalf("UNKNOWN outcome did not preserve the active guard reason: %+v", evaluated.LevelOutcomes)
			}
			if !test.advances {
				if len(evaluated.StateResults) != 0 {
					t.Fatalf("missing dependency point advanced State: %+v", evaluated.StateResults)
				}
			} else if len(evaluated.StateResults) != 1 || len(evaluated.StateResults[0].Mutation.Levels) != 1 ||
				(evaluated.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryWarming &&
					evaluated.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryGapped) ||
				evaluated.StateResults[0].Mutation.Levels[0].GapReasonCode != guard {
				t.Fatalf("State mutation did not preserve the active guard: %+v", evaluated.StateResults)
			}
			if err := result.Validate(request); err != nil {
				t.Fatalf("evaluation under an active guard did not validate: %v", err)
			}
		})
	}
}

func TestEvaluateSeriesSupportsThresholdPrimaryOnlyAndProcPortDimensions(t *testing.T) {
	thresholdRequest := requestFixture(t, json.RawMessage(`80`), nil)
	thresholdInput := primaryOnlyInput(thresholdRequest, g4Record(99, `80`, nil))
	threshold, err := newEvaluator(t).evaluateSeries(context.Background(), thresholdRequest.Header,
		[]execution.SeriesEvaluationInputRequest{thresholdInput}, thresholdRequest.State, thresholdRequest.Gaps)
	if err != nil {
		t.Fatalf("Threshold primary-only evaluateSeries() error = %v", err)
	}
	if len(threshold.LevelOutcomes) != 1 || threshold.LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal {
		t.Fatalf("Threshold result = %+v", threshold)
	}

	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"},
		DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
		IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}}
	procPlan := compiledG4Plan(t, strategy.DetectorKindProcPort, map[string]any{}, projection)
	procRecord := g4Record(99, `1`, map[string]json.RawMessage{"nonlisten": json.RawMessage(`"80"`)})
	procRequest := requestFixtureForPlan(t, procPlan, []contract.CanonicalRecordV2{procRecord}, nil)
	procInput := g4Input(t, procRequest, map[string][]contract.CanonicalRecordV2{"primary": {procRecord}})
	proc, err := newEvaluator(t).evaluateSeries(context.Background(), procRequest.Header,
		[]execution.SeriesEvaluationInputRequest{procInput}, procRequest.State, procRequest.Gaps)
	if err != nil {
		t.Fatalf("ProcPort evaluateSeries() error = %v", err)
	}
	if len(proc.LevelOutcomes) != 1 || proc.LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal {
		t.Fatalf("ProcPort result = %+v", proc)
	}
}

func TestEvaluateSeriesRejectsNonExactLevelCoverWithoutAffectingSibling(t *testing.T) {
	request := requestFixture(t, json.RawMessage(`80`), nil)
	input := primaryOnlyInput(request, g4Record(99, `80`, nil))
	if _, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
		[]execution.SeriesEvaluationInputRequest{input, input}, request.State, request.Gaps); err == nil {
		t.Fatal("duplicate Level input must fail closed")
	}
	if _, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
		[]execution.SeriesEvaluationInputRequest{input}, request.State, request.Gaps); err != nil {
		t.Fatalf("healthy sibling could not continue after local contract rejection: %v", err)
	}
	mutations := []struct {
		name   string
		mutate func(*execution.SeriesEvaluationInputRequest)
	}{
		{name: "wrong contract", mutate: func(value *execution.SeriesEvaluationInputRequest) { value.Contract.QueryRevision = "other" }},
		{name: "wrong plan", mutate: func(value *execution.SeriesEvaluationInputRequest) { value.Consumer.Plan.StrategyID = "other" }},
		{name: "wrong series", mutate: func(value *execution.SeriesEvaluationInputRequest) {
			value.SeriesIdentity = execution.SeriesIdentityDigest(strings.Repeat("e", 64))
		}},
		{name: "wrong level", mutate: func(value *execution.SeriesEvaluationInputRequest) { value.Consumer.LevelID = 6 }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			invalid := input
			test.mutate(&invalid)
			if _, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
				[]execution.SeriesEvaluationInputRequest{invalid}, request.State, request.Gaps); err == nil {
				t.Fatal("invalid Plan/series/contract binding must fail closed")
			}
		})
	}
}

func primaryOnlyInput(request execution.EvaluationRequest, record contract.CanonicalRecordV2) execution.SeriesEvaluationInputRequest {
	consumer := execution.ConsumerRef{Plan: request.Header.DuePlans[0].Identity, LevelID: 5, HasLevel: true}
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{record})
	view, _ := execution.NewDatasetView(dataset, []uint32{0})
	return execution.SeriesEvaluationInputRequest{Contract: request.Header.Contract, Consumer: consumer,
		SeriesIdentity: execution.SeriesIdentityDigest(record.DimensionIdentity.Digest), Inputs: []execution.NamedInputBinding{{Consumer: consumer,
			RequirementID: "main", DatasetName: "primary", Role: execution.InputRolePrimary, Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable}}}
}

func g4Input(t *testing.T, request execution.EvaluationRequest, records map[string][]contract.CanonicalRecordV2) execution.SeriesEvaluationInputRequest {
	t.Helper()
	consumer := execution.ConsumerRef{Plan: request.Header.DuePlans[0].Identity, LevelID: 5, HasLevel: true}
	algorithm := request.Header.DuePlans[0].CompiledPlan.Levels()[0].Algorithms()[0]
	input := execution.SeriesEvaluationInputRequest{Contract: request.Header.Contract, Consumer: consumer,
		SeriesIdentity: execution.SeriesIdentityDigest(request.State.Items[0].Identity.SeriesIdentityDigest)}
	for _, requirement := range algorithm.InputRequirements() {
		dataset := execution.NewDataset(records[requirement.DatasetName])
		ordinals := make([]uint32, dataset.Len())
		for index := range ordinals {
			ordinals[index] = uint32(index)
		}
		view, err := execution.NewDatasetView(dataset, ordinals)
		if err != nil {
			t.Fatal(err)
		}
		state := execution.DataStateData
		if dataset.Len() == 0 {
			state = execution.DataStateEmpty
		}
		input.RequirementIDs = append(input.RequirementIDs, execution.RequirementID(requirement.RequirementID))
		input.Inputs = append(input.Inputs, execution.NamedInputBinding{Consumer: consumer, RequirementID: execution.RequirementID(requirement.RequirementID),
			DatasetName: execution.DatasetName(requirement.DatasetName), Role: execution.InputRole(requirement.Role), Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: state, Disposition: execution.AccessAvailable})
	}
	return input
}

func compiledG4Plan(t *testing.T, kind string, config map[string]any, projection strategy.AlgorithmInputProjection) *strategy.CompiledPlan {
	t.Helper()
	return compiledG4PlanWithTrigger(t, kind, config, projection, 1, 1)
}

func compiledG4PlanWithTrigger(t *testing.T, kind string, config map[string]any, projection strategy.AlgorithmInputProjection, windowSize, requiredAnomalies uint32) *strategy.CompiledPlan {
	t.Helper()
	requirements := []strategy.AlgorithmInputRequirement{g4Requirement(t, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil, projection)}
	if kind == strategy.DetectorKindSimpleRingRatio {
		requirements = append(requirements, g4Requirement(t, "previous", strategy.AlgorithmInputDependency, -120, -60,
			[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}}, projection))
	}
	config["input_projection"], config["requirements"] = projection, requirements
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 1 << 20,
		MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 64,
		MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
		MaxTriggerComputeCost: 1 << 20, MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 16, MaxCacheBytes: 1 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "g4-evaluation-test"})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "r1"}
	inputProjection := contract.InputProjectionV2{ValueFields: projection.ValueFields, DimensionFields: projection.DimensionFields,
		BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "none", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: kind, Version: 1, Config: payload}}},
		TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(fmt.Sprintf(
			`{"window_size":%d,"required_anomalies":%d,"step_seconds":60}`, windowSize, requiredAnomalies))},
		RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: inputProjection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: inputProjection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries,
				QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: []contract.LevelIRV2{level}}}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: projection.IdentityFields, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "s", CodecSemanticsVersion: "c", IdentitySchemaDigest: strings.Repeat("3", 64),
			SourceTimeSemanticsVersion: "t", HistoryCellSemanticsVersion: "h"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal = %+v/%+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func g4Requirement(t *testing.T, name string, role strategy.AlgorithmInputRole, start, end int64, points []strategy.AlgorithmNamedInputPoint, projection strategy.AlgorithmInputProjection) strategy.AlgorithmInputRequirement {
	t.Helper()
	offsets := make([]int64, len(points))
	executionPoints := make([]execution.NamedInputPoint, len(points))
	for index, point := range points {
		offsets[index] = point.OffsetSeconds
		executionPoints[index] = execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds}
	}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{DatasetName: execution.DatasetName(name), Role: execution.InputRole(role),
		ConsumerLevelID: 5, LogicalQueryRef: execution.LogicalQueryRef("query-" + name), RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis: 60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired,
		InputProjection:     execution.InputProjection{ValueFields: projection.ValueFields, DimensionFields: projection.DimensionFields, IdentityFields: projection.IdentityFields},
		PointOffsetsSeconds: offsets, NamedPoints: executionPoints})
	if err != nil {
		t.Fatal(err)
	}
	return strategy.AlgorithmInputRequirement{RequirementID: string(template.RequirementID), DatasetName: name, Role: role, ConsumerLevelID: 5,
		LogicalQueryRef: "query-" + name, RelativeWindow: strategy.AlgorithmRelativeWindow{StartOffsetSeconds: start, EndOffsetSeconds: end, HalfOpen: true},
		StepMillis: 60_000, AlignmentMillis: 60_000, ReadinessClass: strategy.AlgorithmReadinessFinalizedRequired,
		InputProjection: projection, PointOffsetsSeconds: offsets, NamedPoints: points}
}

func g4Record(sourceTime int64, value string, dimensions map[string]json.RawMessage) contract.CanonicalRecordV2 {
	if dimensions == nil {
		dimensions = map[string]json.RawMessage{}
	}
	return contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%064x", sourceTime), SourceTime: sourceTime,
		BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(value)},
		Dimensions: dimensions, ReceivedTime: sourceTime}
}
