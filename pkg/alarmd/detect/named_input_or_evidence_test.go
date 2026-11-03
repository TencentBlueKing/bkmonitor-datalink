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

// compileTwoRingRatioPlan compiles one Level holding a SimpleRingRatio
// algorithm per config, joined by connector, all reading the same
// primary/previous inputs.
func compileTwoRingRatioPlan(t *testing.T, connector string, configs ...map[string]any) *strategy.CompiledPlan {
	t.Helper()
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := []strategy.AlgorithmInputRequirement{
		namedAlgorithmRequirement(t, "primary", strategy.AlgorithmInputPrimary, -60, 0, nil, projection),
		namedAlgorithmRequirement(t, "previous", strategy.AlgorithmInputDependency, -120, -60,
			[]strategy.AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}}, projection),
	}
	algorithms := make([]contract.AlgorithmIRV2, 0, len(configs))
	for _, config := range configs {
		config["input_projection"], config["requirements"] = projection, requirements
		payload, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		algorithms = append(algorithms, contract.AlgorithmIRV2{Type: strategy.DetectorKindSimpleRingRatio, Version: 1, Config: payload})
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "revision-v1"}
	inputProjection := contract.InputProjectionV2{ValueFields: projection.ValueFields, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "none", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: connector,
		DetectPlan:   contract.DetectPlanV2{Algorithms: algorithms},
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

func evaluateTwoRingRatioLevel(t *testing.T, plan *strategy.CompiledPlan) LevelFact {
	t.Helper()
	algorithm := plan.Levels().At(0).Algorithms().At(0)
	// current 80 vs previous 100: a 20% decline.
	bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{
		"primary":  {namedRecord(t, 600, `80`, nil)},
		"previous": {namedRecord(t, 540, `100`, nil)},
	})
	input := execution.SeriesEvaluationInputRequest{
		Consumer:       execution.ConsumerRef{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, LevelID: 5, HasLevel: true},
		SeriesIdentity: execution.SeriesIdentityDigest(primary.DimensionIdentity().Digest), Inputs: bindings,
	}
	for _, requirement := range algorithm.InputRequirements() {
		input.RequirementIDs = append(input.RequirementIDs, execution.RequirementID(requirement.RequirementID))
	}
	evaluator, err := NewEvaluator(NewDefaultRegistry())
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
	if len(facts) != 1 {
		t.Fatalf("facts = %+v, want one Level fact", facts)
	}
	return facts[0]
}

// The named-input path must keep the first deciding algorithm as evidence,
// matching the canonical Threshold path (first match for OR, first miss for AND).
func TestNamedInputLevelEvidencePointsAtFirstDecidingAlgorithm(t *testing.T) {
	anomalous := func() map[string]any { return map[string]any{"floor": 20, "ceil": nil} } // 80 <= 100*(1-20%)
	normal := func() map[string]any { return map[string]any{"floor": nil, "ceil": 20} }    // no rise
	t.Run("OR anomalous then normal", func(t *testing.T) {
		plan := compileTwoRingRatioPlan(t, contract.LevelConnectorOR, anomalous(), normal())
		fact := evaluateTwoRingRatioLevel(t, plan)
		want := plan.Levels().At(0).Algorithms().At(0).AlgorithmPlanID()
		if fact.Result != FactResultAnomalous {
			t.Fatalf("Result = %s, want ANOMALOUS", fact.Result)
		}
		if !fact.Evidence.HasMatchedAlgorithm || fact.Evidence.MatchedAlgorithmOrdinal != 0 || fact.Evidence.PredicateDigest != want {
			t.Fatalf("Evidence = %+v, want matched algorithm 0 with digest %s", fact.Evidence, want)
		}
	})
	t.Run("AND normal then anomalous", func(t *testing.T) {
		plan := compileTwoRingRatioPlan(t, contract.LevelConnectorAND, normal(), anomalous())
		fact := evaluateTwoRingRatioLevel(t, plan)
		want := plan.Levels().At(0).Algorithms().At(0).AlgorithmPlanID()
		if fact.Result != FactResultNormal {
			t.Fatalf("Result = %s, want NORMAL", fact.Result)
		}
		if fact.Evidence.HasMatchedAlgorithm || fact.Evidence.PredicateDigest != want {
			t.Fatalf("Evidence = %+v, want unmatched algorithm 0 with digest %s", fact.Evidence, want)
		}
	})
	// The decider is not the first algorithm, which is also what the
	// fallback holds: only these tell the decider from the first available.
	t.Run("OR normal, anomalous, normal", func(t *testing.T) {
		plan := compileTwoRingRatioPlan(t, contract.LevelConnectorOR, normal(), anomalous(), normal())
		fact := evaluateTwoRingRatioLevel(t, plan)
		want := plan.Levels().At(0).Algorithms().At(1).AlgorithmPlanID()
		if fact.Result != FactResultAnomalous || !fact.Evidence.HasMatchedAlgorithm || fact.Evidence.MatchedAlgorithmOrdinal != 1 ||
			fact.Evidence.PredicateDigest != want {
			t.Fatalf("Result %s Evidence %+v, want ANOMALOUS matched algorithm 1 with digest %s", fact.Result, fact.Evidence, want)
		}
	})
	t.Run("AND anomalous, normal, anomalous", func(t *testing.T) {
		plan := compileTwoRingRatioPlan(t, contract.LevelConnectorAND, anomalous(), normal(), anomalous())
		fact := evaluateTwoRingRatioLevel(t, plan)
		want := plan.Levels().At(0).Algorithms().At(1).AlgorithmPlanID()
		if fact.Result != FactResultNormal || fact.Evidence.HasMatchedAlgorithm || fact.Evidence.PredicateDigest != want {
			t.Fatalf("Result %s Evidence %+v, want NORMAL unmatched algorithm 1 with digest %s", fact.Result, fact.Evidence, want)
		}
	})
}
