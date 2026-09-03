// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

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

func TestRetainCompiledLevelsRetainsLevelDependencyClosure(t *testing.T) {
	plan := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
		runtimeClosureLevel(2, "Unsupported"),
	})
	plan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-1-shared", 1, "shared"),
		runtimeClosureRequirement("level-2-shared", 2, "shared"),
		runtimeClosureRequirement("level-2-only", 2, "level-2-only"),
	}
	plan.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"shared":       {TenantID: "shared"},
		"level-2-only": {TenantID: "level-2-only"},
	}

	retained, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 1))
	if err != nil {
		t.Fatalf("retainCompiledLevels() error = %v", err)
	}
	assertRuntimeClosure(t, retained, []uint32{1}, []execution.RequirementID{"level-1-shared"}, []execution.LogicalQueryRef{"shared"})
}

func TestRetainCompiledLevelsAllRejectedClearsDependencyClosure(t *testing.T) {
	plan := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{runtimeClosureLevel(2, "Unsupported")})
	plan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-2-only", 2, "level-2-only"),
	}
	plan.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"level-2-only": {TenantID: "level-2-only"},
	}

	retained, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 0))
	if err != nil {
		t.Fatalf("retainCompiledLevels() error = %v", err)
	}
	assertRuntimeClosure(t, retained, nil, nil, nil)
}

func TestRetainCompiledLevelsRejectsDanglingRequirement(t *testing.T) {
	plan := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{runtimeClosureLevel(1, strategy.DetectorKindThreshold)})
	plan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-1", 1, "missing"),
	}

	if _, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 1)); err == nil {
		t.Fatal("retainCompiledLevels() accepted a retained Requirement without QueryPlan facts")
	}
}

func TestSupplementRejectedLevelsRestoresLastGoodLevelDependencyClosure(t *testing.T) {
	current := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
		runtimeClosureLevel(3, strategy.DetectorKindThreshold),
	})
	current.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-1-shared", 1, "shared"),
		runtimeClosureRequirement("level-3-only", 3, "level-3-only"),
	}
	current.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"shared":       {TenantID: "current-shared"},
		"level-3-only": {TenantID: "current-level-3"},
	}
	lastGood := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
		runtimeClosureLevel(2, strategy.DetectorKindThreshold),
		runtimeClosureLevel(3, strategy.DetectorKindThreshold),
	})
	lastGood.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-1-shared", 1, "shared"),
		runtimeClosureRequirement("level-2-shared", 2, "shared"),
		runtimeClosureRequirement("level-2-only", 2, "level-2-only"),
		runtimeClosureRequirement("level-3-only", 3, "level-3-only"),
	}
	lastGood.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"shared":       {TenantID: "last-good-shared"},
		"level-2-only": {TenantID: "last-good-level-2"},
		"level-3-only": {TenantID: "last-good-level-3"},
	}

	got, supplemented, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 2, Disposition: DispositionConfigRejected,
	}})
	if err != nil {
		t.Fatalf("supplementRejectedLevels() error = %v", err)
	}
	if _, ok := supplemented[2]; !ok || len(supplemented) != 1 {
		t.Fatalf("supplemented = %v, want only Level 2", supplemented)
	}
	assertRuntimeClosure(t, got, []uint32{1, 2, 3}, []execution.RequirementID{
		"level-1-shared", "level-2-shared", "level-2-only", "level-3-only",
	}, []execution.LogicalQueryRef{"shared", "level-2-only", "level-3-only"})
	if got.QueryPlans["shared"].TenantID != "current-shared" {
		t.Fatalf("shared QueryPlan = %+v, want retained current facts", got.QueryPlans["shared"])
	}
	if got.QueryPlans["level-2-only"].TenantID != "last-good-level-2" {
		t.Fatalf("Level 2 QueryPlan = %+v, want last-good facts", got.QueryPlans["level-2-only"])
	}
	if got.QueryPlans["level-3-only"].TenantID != "current-level-3" {
		t.Fatalf("Level 3 QueryPlan = %+v, want retained current facts", got.QueryPlans["level-3-only"])
	}
}

func TestSupplementRejectedLevelsWithoutLastGoodMatchLeavesClosureUnchanged(t *testing.T) {
	current := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{runtimeClosureLevel(1, strategy.DetectorKindThreshold)})
	current.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-1", 1, "level-1"),
	}
	current.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"level-1": {TenantID: "current"},
	}
	lastGood := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{runtimeClosureLevel(2, strategy.DetectorKindThreshold)})

	got, supplemented, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 3, Disposition: DispositionConfigRejected,
	}})
	if err != nil {
		t.Fatalf("supplementRejectedLevels() error = %v", err)
	}
	if len(supplemented) != 0 {
		t.Fatalf("supplemented = %v, want empty", supplemented)
	}
	assertRuntimeClosure(t, got, []uint32{1}, []execution.RequirementID{"level-1"}, []execution.LogicalQueryRef{"level-1"})
}

func TestSupplementRejectedLevelsRejectsDanglingLastGoodRequirement(t *testing.T) {
	current := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{runtimeClosureLevel(1, strategy.DetectorKindThreshold)})
	lastGood := runtimeClosureFrozenPlan(t, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
		runtimeClosureLevel(2, strategy.DetectorKindThreshold),
	})
	lastGood.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("level-2", 2, "missing"),
	}

	if _, _, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 2, Disposition: DispositionConfigRejected,
	}}); err == nil {
		t.Fatal("supplementRejectedLevels() accepted a last-good Requirement without QueryPlan facts")
	}
}

func runtimeClosureFrozenPlan(t *testing.T, levels []contract.LevelIRV2) FrozenPlan {
	t.Helper()
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: ref.StrategyID, StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, StrategyRef: ref,
			InputProjection: projection, ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
				EvaluationInterval: 60, LatenessTolerance: 120,
			}, Levels: levels,
		},
	}
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan)
	if err != nil {
		t.Fatal(err)
	}
	return FrozenPlan{Plan: plan, PlanRevision: revision}
}

func runtimeClosureLevel(levelID uint32, algorithmKind string) contract.LevelIRV2 {
	config, _ := json.Marshal(map[string]any{
		"value_field": "value", "data_unit": "percent", "threshold_unit_prefix": "",
		"precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
		"groups": []any{map[string]any{"conditions": []any{
			map[string]any{"operator": "GTE", "threshold_decimal": "50"},
		}}},
	})
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: levelID, Priority: levelID}, Connector: contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: algorithmKind, Version: 1, Config: config}}},
		TriggerPlan: contract.TypedPlanV1{Type: strategy.TriggerPlanTypeNOfM, Version: 1,
			Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
		RecoveryPlan: contract.TypedPlanV1{Type: strategy.RecoveryPlanTypeContinuousTriggerMiss, Version: 1,
			Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
	}
}

func runtimeClosureRequirement(id string, levelID uint32, queryRef execution.LogicalQueryRef) execution.DataRequirementTemplate {
	return execution.DataRequirementTemplate{
		RequirementID: execution.RequirementID(id), ConsumerLevelID: levelID, LogicalQueryRef: queryRef,
	}
}

func runtimeClosureCompile(t *testing.T, plan contract.EvaluationPlanV2, wantLevels int) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "runtime-closure-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok || len(compiled.Levels()) != wantLevels {
		t.Fatalf("Compile() plan=%+v terminals=%+v, want %d retained Levels", compiled, result.LevelTerminals(), wantLevels)
	}
	return compiled
}

func assertRuntimeClosure(
	t *testing.T,
	plan FrozenPlan,
	wantLevels []uint32,
	wantRequirements []execution.RequirementID,
	wantQueries []execution.LogicalQueryRef,
) {
	t.Helper()
	if len(plan.Plan.StrategyIR.Levels) != len(wantLevels) {
		t.Fatalf("Levels = %+v, want IDs %v", plan.Plan.StrategyIR.Levels, wantLevels)
	}
	for index, level := range plan.Plan.StrategyIR.Levels {
		if level.Definition.LevelID != wantLevels[index] {
			t.Fatalf("Levels[%d].LevelID = %d, want %d", index, level.Definition.LevelID, wantLevels[index])
		}
	}
	if len(plan.RequirementTemplates) != len(wantRequirements) {
		t.Fatalf("RequirementTemplates = %+v, want IDs %v", plan.RequirementTemplates, wantRequirements)
	}
	for index, requirement := range plan.RequirementTemplates {
		if requirement.RequirementID != wantRequirements[index] {
			t.Fatalf("RequirementTemplates[%d].RequirementID = %q, want %q", index, requirement.RequirementID, wantRequirements[index])
		}
	}
	if len(plan.QueryPlans) != len(wantQueries) {
		t.Fatalf("QueryPlans = %+v, want refs %v", plan.QueryPlans, wantQueries)
	}
	for _, queryRef := range wantQueries {
		if _, ok := plan.QueryPlans[queryRef]; !ok {
			t.Fatalf("QueryPlans missing %q: %+v", queryRef, plan.QueryPlans)
		}
	}
}
