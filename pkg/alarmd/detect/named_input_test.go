// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestDefaultRegistryBindsAndEvaluatesCompiledG4Algorithms(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		config     map[string]any
		projection strategy.AlgorithmInputProjection
		bindings   func(*testing.T, []strategy.AlgorithmInputRequirement) ([]execution.NamedInputBinding, execution.RecordView)
		want       string
	}{
		{
			name: "simple ring ratio", kind: strategy.DetectorKindSimpleRingRatio,
			config:     map[string]any{"floor": 20, "ceil": nil},
			projection: strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}},
			bindings: func(t *testing.T, requirements []strategy.AlgorithmInputRequirement) ([]execution.NamedInputBinding, execution.RecordView) {
				return namedBindings(t, requirements, map[string][]contract.CanonicalRecordV2{
					"primary":  {namedRecord(t, 600, `80`, nil)},
					"previous": {namedRecord(t, 540, `100`, nil)},
				})
			},
			want: FactResultAnomalous,
		},
		{
			name: "os restart", kind: strategy.DetectorKindOsRestart,
			config:     map[string]any{},
			projection: strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}},
			bindings: func(t *testing.T, requirements []strategy.AlgorithmInputRequirement) ([]execution.NamedInputBinding, execution.RecordView) {
				return namedBindings(t, requirements, map[string][]contract.CanonicalRecordV2{
					"primary": {namedRecord(t, 1800, `300`, nil)},
					"uptime_history": {
						namedRecord(t, 300, `1800`, nil),
						namedRecord(t, 1200, `900`, nil),
						namedRecord(t, 1740, `700`, nil),
					},
				})
			},
			want: FactResultAnomalous,
		},
		{
			name: "proc port", kind: strategy.DetectorKindProcPort,
			config: map[string]any{},
			projection: strategy.AlgorithmInputProjection{
				ValueFields:     []string{"value"},
				DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
				IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
			},
			bindings: func(t *testing.T, requirements []strategy.AlgorithmInputRequirement) ([]execution.NamedInputBinding, execution.RecordView) {
				return namedBindings(t, requirements, map[string][]contract.CanonicalRecordV2{
					"primary": {namedRecord(t, 600, `1`, map[string]json.RawMessage{"nonlisten": json.RawMessage(`"80"`)})},
				})
			},
			want: FactResultAnomalous,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := compileNamedInputPlan(t, test.kind, test.config, test.projection)
			algorithm := plan.Levels()[0].Algorithms()[0]
			bindings, primary := test.bindings(t, algorithm.InputRequirements())
			input := execution.SeriesEvaluationInputRequest{
				Consumer:       execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, LevelID: 5, HasLevel: true},
				SeriesIdentity: execution.SeriesIdentityDigest(primary.DimensionIdentity().Digest), Inputs: bindings,
			}
			for _, requirement := range algorithm.InputRequirements() {
				input.RequirementIDs = append(input.RequirementIDs, execution.RequirementID(requirement.RequirementID))
			}

			evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := evaluator.PreparePlan(plan)
			if err != nil {
				t.Fatalf("PreparePlan() error = %v", err)
			}
			facts, _, _, err := evaluator.EvaluatePreparedSeriesRecord(context.Background(), prepared, []execution.SeriesEvaluationInputRequest{input}, primary)
			if err != nil {
				t.Fatalf("EvaluatePreparedSeriesRecord() error = %v", err)
			}
			if len(facts) != 1 || facts[0].Result != test.want || facts[0].Evidence.PredicateDigest != algorithm.AlgorithmPlanID() {
				t.Fatalf("facts = %+v, want %s with compiled predicate identity", facts, test.want)
			}
		})
	}
}

func TestNamedInputEvaluationFailsOnlyTheAffectedLevel(t *testing.T) {
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	plan := compileNamedInputPlan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil}, projection)
	algorithm := plan.Levels()[0].Algorithms()[0]
	bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{
		"primary": {namedRecord(t, 600, `80`, nil)},
	})
	input := execution.SeriesEvaluationInputRequest{
		Consumer:       execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, LevelID: 5, HasLevel: true},
		SeriesIdentity: execution.SeriesIdentityDigest(primary.DimensionIdentity().Digest), Inputs: bindings,
	}
	for _, requirement := range algorithm.InputRequirements() {
		input.RequirementIDs = append(input.RequirementIDs, execution.RequirementID(requirement.RequirementID))
	}
	evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator.PreparePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	facts, _, _, err := evaluator.EvaluatePreparedSeriesRecord(context.Background(), prepared, []execution.SeriesEvaluationInputRequest{input}, primary)
	if err != nil {
		t.Fatalf("missing history is a local detection fact, not a Worker error: %v", err)
	}
	if len(facts) != 1 || facts[0].Result != FactResultUnavailable || facts[0].ReasonCode != contract.ReasonHistoryGapped {
		t.Fatalf("facts = %+v, want level-local HISTORY_GAPPED", facts)
	}
}

func TestOSRestartNamedInputPreservesPythonMissingPointSemantics(t *testing.T) {
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	plan := compileNamedInputPlan(t, strategy.DetectorKindOsRestart, map[string]any{}, projection)
	algorithm := plan.Levels()[0].Algorithms()[0]
	evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator.PreparePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		history []contract.CanonicalRecordV2
		want    string
	}{
		{name: "previous absent with ten minute point", history: []contract.CanonicalRecordV2{namedRecord(t, 1200, `900`, nil)}, want: FactResultAnomalous},
		{name: "both older points absent", history: []contract.CanonicalRecordV2{namedRecord(t, 1740, `700`, nil)}, want: FactResultNormal},
		{name: "present older point invalid", history: []contract.CanonicalRecordV2{namedRecord(t, 1200, `"invalid"`, nil)}, want: FactResultError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{
				"primary": {namedRecord(t, 1800, `300`, nil)}, "uptime_history": test.history,
			})
			input := execution.SeriesEvaluationInputRequest{Consumer: execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, LevelID: 5, HasLevel: true},
				SeriesIdentity: execution.SeriesIdentityDigest(primary.DimensionIdentity().Digest), Inputs: bindings}
			for _, requirement := range algorithm.InputRequirements() {
				input.RequirementIDs = append(input.RequirementIDs, execution.RequirementID(requirement.RequirementID))
			}
			facts, _, _, evalErr := evaluator.EvaluatePreparedSeriesRecord(context.Background(), prepared, []execution.SeriesEvaluationInputRequest{input}, primary)
			if evalErr != nil {
				t.Fatalf("EvaluatePreparedSeriesRecord() error = %v", evalErr)
			}
			if len(facts) != 1 || facts[0].Result != test.want {
				t.Fatalf("facts = %+v, want %s", facts, test.want)
			}
		})
	}
}

func TestNamedInputPathKeepsThresholdAndPingMappingOnCanonicalDetector(t *testing.T) {
	thresholdPlan := fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(5, 1, contract.LevelConnectorAND,
		fixtureThresholdAlgorithm("GTE", "50", "percent", ""))})
	_, executions, _ := fixtureExecutions(t, fixtureEnvelope(t, []contract.EvaluationPlanV2{thresholdPlan},
		[]fixtureRecord{{host: "host-a", sourceTime: 600, value: json.RawMessage(`80`)}}, contract.QueryCompletenessFull))
	plan := executions[0].Plan
	primaryRecord := namedRecord(t, 600, `80`, nil)
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{primaryRecord})
	view, _ := execution.NewDatasetView(dataset, []uint32{0})
	primary, _ := view.Record(0)
	consumer := execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}, LevelID: 5, HasLevel: true}
	input := execution.SeriesEvaluationInputRequest{Consumer: consumer, SeriesIdentity: execution.SeriesIdentityDigest(primary.DimensionIdentity().Digest),
		Inputs: []execution.NamedInputBinding{{Consumer: consumer, DatasetName: "primary", Role: execution.InputRolePrimary,
			Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable}}}
	evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := evaluator.PreparePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	facts, _, _, err := evaluator.EvaluatePreparedSeriesRecord(context.Background(), prepared, []execution.SeriesEvaluationInputRequest{input}, primary)
	if err != nil || len(facts) != 1 || facts[0].Result != FactResultAnomalous {
		t.Fatalf("canonical Threshold named-input path = %+v, error = %v", facts, err)
	}
	if _, registered := NewDefaultRegistry().resolveNamedInput(DetectorKey{Kind: "PingUnreachable", Version: 1}); registered {
		t.Fatal("PingUnreachable must remain a Threshold source mapping, not an independent Detector")
	}
}

func compileNamedInputPlan(t *testing.T, kind string, config map[string]any, projection strategy.AlgorithmInputProjection) *strategy.CompiledPlan {
	t.Helper()
	requirements := []strategy.AlgorithmInputRequirement{namedAlgorithmRequirement(t, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil, projection)}
	switch kind {
	case strategy.DetectorKindSimpleRingRatio:
		requirements = append(requirements, namedAlgorithmRequirement(t, "previous", strategy.AlgorithmInputDependency, -120, -60,
			[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}}, projection))
	case strategy.DetectorKindOsRestart:
		requirements = append(requirements, namedAlgorithmRequirement(t, "uptime_history", strategy.AlgorithmInputDependency, -1560, 0,
			[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}, {Name: "previous_10m", OffsetSeconds: 600}, {Name: "previous_25m", OffsetSeconds: 1500}}, projection))
	}
	config["input_projection"], config["requirements"] = projection, requirements
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "revision-v1"}
	inputProjection := contract.InputProjectionV2{ValueFields: projection.ValueFields, DimensionFields: projection.DimensionFields,
		BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "none", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
		DetectPlan:   contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: kind, Version: 1, Config: payload}}},
		TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
		RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	plan := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: inputProjection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: inputProjection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries,
				QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: []contract.LevelIRV2{level}}}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 1 << 20,
		MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 64,
		MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32,
		MaxTriggerComputeCost: 1 << 20, MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 16, MaxCacheBytes: 1 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "named-detect-test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: projection.IdentityFields, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-v1", HistoryCellSemanticsVersion: "history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal = %+v/%+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func namedAlgorithmRequirement(t *testing.T, name string, role strategy.AlgorithmInputRole, start, end int64, points []strategy.AlgorithmNamedInputPoint, projection strategy.AlgorithmInputProjection) strategy.AlgorithmInputRequirement {
	t.Helper()
	offsets := make([]int64, len(points))
	executionPoints := make([]execution.NamedInputPoint, len(points))
	for i, point := range points {
		offsets[i] = point.OffsetSeconds
		executionPoints[i] = execution.NamedInputPoint{Name: point.Name, OffsetSeconds: point.OffsetSeconds}
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

func namedBindings(t *testing.T, requirements []strategy.AlgorithmInputRequirement, records map[string][]contract.CanonicalRecordV2) ([]execution.NamedInputBinding, execution.RecordView) {
	t.Helper()
	consumer := execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, LevelID: 5, HasLevel: true}
	var primary execution.RecordView
	bindings := make([]execution.NamedInputBinding, 0, len(requirements))
	for _, requirement := range requirements {
		dataset := execution.NewDataset(records[requirement.DatasetName])
		ordinals := make([]uint32, dataset.Len())
		for i := range ordinals {
			ordinals[i] = uint32(i)
		}
		view, err := execution.NewDatasetView(dataset, ordinals)
		if err != nil {
			t.Fatal(err)
		}
		dataState := execution.DataStateData
		if dataset.Len() == 0 {
			dataState = execution.DataStateEmpty
		}
		bindings = append(bindings, execution.NamedInputBinding{Consumer: consumer, RequirementID: execution.RequirementID(requirement.RequirementID),
			DatasetName: execution.DatasetName(requirement.DatasetName), Role: execution.InputRole(requirement.Role), Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: dataState, Disposition: execution.AccessAvailable})
		if requirement.Role == strategy.AlgorithmInputPrimary && view.Len() > 0 {
			primary, _ = view.Record(0)
		}
	}
	return bindings, primary
}

func namedRecord(t *testing.T, sourceTime int64, value string, dimensions map[string]json.RawMessage) contract.CanonicalRecordV2 {
	t.Helper()
	fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"host-a"`)}}
	digest, err := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
	if err != nil {
		t.Fatal(err)
	}
	if dimensions == nil {
		dimensions = map[string]json.RawMessage{}
	}
	return contract.CanonicalRecordV2{RecordID: strings.Repeat("a", 63) + string(rune('a'+sourceTime%20)), SourceTime: sourceTime,
		BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: digest}, Values: map[string]json.RawMessage{"value": json.RawMessage(value)},
		Dimensions: dimensions, ReceivedTime: sourceTime}
}
