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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// These fixtures lived with the batch evaluation tests. They outlived them:
// the prepared-record and named-input tests build Plans the same way, and only
// the envelope round trip that used to sit between the fixture and the
// compiler went with the phase-one input. compileFixturePlan replaces it by
// compiling the fixture Plan directly, which is what the round trip amounted
// to once the decoded view was no longer needed.
func compileFixturePlan(t testing.TB, plan contract.EvaluationPlanV2) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), compilerLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "state-v1", CodecSemanticsVersion: "codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64),
			SourceTimeSemanticsVersion: "source-time-v1", HistoryCellSemanticsVersion: "history-cell-v1",
		},
	})
	if err != nil {
		t.Fatalf("Compile(%s) error = %v", plan.PlanID, err)
	}
	compiled, ok := result.Plan()
	if !ok || result.PlanTerminal() != nil || len(result.LevelTerminals()) != 0 {
		t.Fatalf("Compile(%s) = plan:%v planTerminal:%#v levelTerminals:%#v", plan.PlanID, ok, result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func fixturePlan(strategyID string, levels []contract.LevelIRV2) contract.EvaluationPlanV2 {
	strategyRef := contract.StrategyRefV2{TenantID: "default", StrategyID: strategyID, Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	strategyIR := contract.StrategyIRV2{
		Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: strategyRef,
		ExecutionSemantics: contract.ExecutionSemanticsV2{
			EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
			EvaluationInterval: 60, LatenessTolerance: 120,
		},
		InputProjection: projection, Levels: levels,
	}
	return contract.EvaluationPlanV2{PlanID: strategyID, StrategyRef: strategyRef, InputProjection: projection, StrategyIR: strategyIR}
}

func fixtureLevel(id, priority uint32, connector string, algorithms ...contract.AlgorithmIRV2) contract.LevelIRV2 {
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: id, Priority: priority}, Connector: connector,
		DetectPlan:   contract.DetectPlanV2{Algorithms: algorithms},
		TriggerPlan:  contract.TypedPlanV1{Type: strategy.TriggerPlanTypeNOfM, Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
		RecoveryPlan: contract.TypedPlanV1{Type: strategy.RecoveryPlanTypeContinuousTriggerMiss, Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
	}
}

func fixtureThresholdAlgorithm(operator, threshold, dataUnit, prefix string) contract.AlgorithmIRV2 {
	return fixtureThresholdAlgorithmFor("value", operator, threshold, dataUnit, prefix)
}

func fixtureThresholdAlgorithmFor(valueField, operator, threshold, dataUnit, prefix string) contract.AlgorithmIRV2 {
	return contract.AlgorithmIRV2{
		Type: strategy.DetectorKindThreshold, Version: 1,
		Config: thresholdConfigFor(valueField, dataUnit, prefix, []thresholdTestGroup{{{operator: operator, threshold: threshold}}}),
	}
}

func compilerLimits() strategy.Limits {
	return strategy.Limits{
		MaxPlanBytes: 1 << 20, MaxLevelsPerPlan: 32, MaxAlgorithmsPerLevel: 32, MaxGroupsPerAlgorithm: 64,
		MaxConditionsPerAlgorithm: 256, MaxASTNodesPerLevel: 4096, MaxRequiredHistoryPoints: 4096,
		MaxTriggerWindowSize: 4096, MaxRecoveryConsecutiveWindows: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 128, MaxCacheBytes: 16 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "test-v1",
	}
}

func newTestEvaluator(t testing.TB) *Evaluator {
	t.Helper()
	evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	return evaluator
}

func evaluateSingleFact(
	t testing.TB,
	algorithms []contract.AlgorithmIRV2,
	connector string,
	value json.RawMessage,
	dataUnit string,
) LevelFact {
	t.Helper()
	plan := fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(5, 1, connector, algorithms...)})
	plan.InputProjection.DataUnit = dataUnit
	plan.StrategyIR.InputProjection.DataUnit = dataUnit
	evaluator := newTestEvaluator(t)
	prepared, err := evaluator.PreparePlan(compileFixturePlan(t, plan))
	if err != nil {
		t.Fatalf("PreparePlan() error = %v", err)
	}
	facts, _, _, err := evaluator.EvaluatePreparedRecord(context.Background(), prepared, recordValueMap{"value": value})
	if err != nil {
		t.Fatalf("EvaluatePreparedRecord() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want one Level fact", len(facts))
	}
	return facts[0]
}

func thresholdConfigFor(valueField, dataUnit, prefix string, groups []thresholdTestGroup) json.RawMessage {
	type condition struct {
		Operator         string `json:"operator"`
		ThresholdDecimal string `json:"threshold_decimal"`
	}
	type group struct {
		Conditions []condition `json:"conditions"`
	}
	payloadGroups := make([]group, len(groups))
	for groupIndex, sourceGroup := range groups {
		payloadGroups[groupIndex].Conditions = make([]condition, len(sourceGroup))
		for conditionIndex, source := range sourceGroup {
			payloadGroups[groupIndex].Conditions[conditionIndex] = condition{Operator: source.operator, ThresholdDecimal: source.threshold}
		}
	}
	payload, err := json.Marshal(struct {
		ValueField          string `json:"value_field"`
		DataUnit            string `json:"data_unit"`
		ThresholdUnitPrefix string `json:"threshold_unit_prefix"`
		Precision           struct {
			DecimalPlaces uint32 `json:"decimal_places"`
			Rounding      string `json:"rounding"`
		} `json:"precision"`
		Groups []group `json:"groups"`
	}{
		ValueField: valueField, DataUnit: dataUnit, ThresholdUnitPrefix: prefix,
		Precision: struct {
			DecimalPlaces uint32 `json:"decimal_places"`
			Rounding      string `json:"rounding"`
		}{DecimalPlaces: 6, Rounding: "HALF_EVEN"}, Groups: payloadGroups,
	})
	if err != nil {
		panic(err)
	}
	return payload
}

type thresholdTestGroup []thresholdTestCondition
type thresholdTestCondition struct {
	operator  string
	threshold string
}
