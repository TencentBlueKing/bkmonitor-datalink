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

func TestRetainRuntimeExecutableCatalogIsolatesDanglingCurrentPlan(t *testing.T) {
	bad := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	bad.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("bad-current", 1, "missing"),
	}
	healthySibling := runtimeClosureCompletePlan(t, "1002", "1")
	healthyOtherGroup := runtimeClosureCompletePlan(t, "2001", "2")
	catalog := runtimeClosureCatalog(
		runtimeClosureQueryGroup("1", bad, healthySibling),
		runtimeClosureQueryGroup("2", healthyOtherGroup),
	)
	compiler, stateSemantics := runtimeClosureCompiler(t)

	got, err := retainRuntimeExecutableCatalog(context.Background(), catalog, nil, compiler, stateSemantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	assertRuntimeCatalogPlans(t, got, compiler, stateSemantics, []string{"1002", "2001"})
	assertRuntimeClosureRejected(t, got.Dispositions, "1001")
}

func TestRetainRuntimeExecutableCatalogIsolatesDanglingLevelLastGood(t *testing.T) {
	invalidLevel := runtimeClosureLevel(2, strategy.DetectorKindThreshold)
	invalidLevel.Connector = "INVALID"
	bad := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold), invalidLevel,
	})
	bad.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("current-level-1", 1, "current"),
		runtimeClosureRequirement("current-level-2", 2, "current"),
	}
	bad.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{"current": {TenantID: "current"}}
	healthySibling := runtimeClosureCompletePlan(t, "1002", "1")
	healthyOtherGroup := runtimeClosureCompletePlan(t, "2001", "2")
	catalog := runtimeClosureCatalog(
		runtimeClosureQueryGroup("1", bad, healthySibling),
		runtimeClosureQueryGroup("2", healthyOtherGroup),
	)
	lastGoodPlan := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
		runtimeClosureLevel(2, strategy.DetectorKindThreshold),
	})
	lastGoodPlan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("last-good-level-1", 1, "last-good"),
		runtimeClosureRequirement("last-good-level-2", 2, "missing"),
	}
	lastGoodPlan.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{"last-good": {TenantID: "last-good"}}
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup("1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)

	got, err := retainRuntimeExecutableCatalog(context.Background(), catalog, lastGood, compiler, stateSemantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	assertRuntimeCatalogPlans(t, got, compiler, stateSemantics, []string{"1002", "2001"})
	assertRuntimeClosureRejected(t, got.Dispositions, "1001")
}

func TestRetainRuntimeExecutableCatalogValidatesWholePlanLastGoodClosure(t *testing.T) {
	bad := runtimeClosureCompletePlan(t, "1001", "1")
	bad.Plan.StrategyIR.ExecutionSemantics.EvaluationScope = contract.EvaluationScopeCrossSeries
	bad.PlanRevision = runtimeClosurePlanRevision(t, bad.Plan)
	healthySibling := runtimeClosureCompletePlan(t, "1002", "1")
	healthyOtherGroup := runtimeClosureCompletePlan(t, "2001", "2")
	catalog := runtimeClosureCatalog(
		runtimeClosureQueryGroup("1", bad, healthySibling),
		runtimeClosureQueryGroup("2", healthyOtherGroup),
	)
	lastGoodPlan := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	lastGoodPlan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("last-good", 1, "missing"),
	}
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup("1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)

	got, err := retainRuntimeExecutableCatalog(context.Background(), catalog, lastGood, compiler, stateSemantics)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	assertRuntimeCatalogPlans(t, got, compiler, stateSemantics, []string{"1002", "2001"})
	assertRuntimeClosureRejected(t, got.Dispositions, "1001")
}

func TestRetainRuntimeExecutableCatalogCarriesWholePlanLastGoodClosure(t *testing.T) {
	current := runtimeClosureCompletePlan(t, "1001", "1")
	current.Plan.StrategyIR.ExecutionSemantics.EvaluationScope = contract.EvaluationScopeCrossSeries
	current.PlanRevision = runtimeClosurePlanRevision(t, current.Plan)
	lastGoodPlan := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	lastGoodPlan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("last-good-primary", 1, "last-good-primary"),
		runtimeClosureRequirement("last-good-dependency", 1, "last-good-dependency"),
	}
	lastGoodPlan.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"last-good-primary":    {TenantID: "last-good-primary"},
		"last-good-dependency": {TenantID: "last-good-dependency"},
	}
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup("1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)

	got, err := retainRuntimeExecutableCatalog(
		context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup("1", current)), lastGood, compiler, stateSemantics,
	)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	if len(got.QueryGroups) != 1 || len(got.QueryGroups[0].Plans) != 1 {
		t.Fatalf("QueryGroups = %+v, want one last-good Plan", got.QueryGroups)
	}
	assertRuntimeClosure(t, got.QueryGroups[0].Plans[0], []uint32{1}, []execution.RequirementID{
		"last-good-primary", "last-good-dependency",
	}, []execution.LogicalQueryRef{"last-good-primary", "last-good-dependency"})
}

func runtimeClosureFrozenPlan(t *testing.T, levels []contract.LevelIRV2) FrozenPlan {
	return runtimeClosureNamedFrozenPlan(t, "1001", "1", levels)
}

func runtimeClosureNamedFrozenPlan(t *testing.T, strategyID, businessID string, levels []contract.LevelIRV2) FrozenPlan {
	t.Helper()
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: strategyID, Revision: "strategy-r1"}
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
	scheduleSpec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Alignment: 0, Timezone: "UTC"}
	scheduleRevision, err := execution.DerivePlanScheduleRevision(scheduleSpec)
	if err != nil {
		t.Fatal(err)
	}
	return FrozenPlan{
		Identity: execution.PlanIdentity{TenantID: "default", BusinessID: businessID, StrategyID: strategyID},
		Plan:     plan, PlanRevision: runtimeClosurePlanRevision(t, plan), ScheduleSpec: scheduleSpec, ScheduleRevision: scheduleRevision,
	}
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
	compiler, stateSemantics := runtimeClosureCompiler(t)
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan, DatasetContract: runtimeClosureDatasetContract(), StateSemantics: stateSemantics,
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

func runtimeClosureCompiler(t *testing.T) (*strategy.PlanCompiler, strategy.StateSemantics) {
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
	return compiler, strategy.StateSemantics{
		StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
		IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
		HistoryCellSemanticsVersion: "detect-history-cell-v1",
	}
}

func runtimeClosureDatasetContract() contract.DatasetContractV2 {
	return contract.DatasetContractV2{
		SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
		IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
	}
}

func runtimeClosureCompletePlan(t *testing.T, strategyID, businessID string) FrozenPlan {
	t.Helper()
	plan := runtimeClosureNamedFrozenPlan(t, strategyID, businessID, []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	queryRef := execution.LogicalQueryRef("query-" + strategyID)
	plan.RequirementTemplates = []execution.DataRequirementTemplate{
		runtimeClosureRequirement("requirement-"+strategyID, 1, queryRef),
	}
	plan.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		queryRef: {TenantID: "query-" + strategyID},
	}
	return plan
}

func runtimeClosureQueryGroup(businessID string, plans ...FrozenPlan) QueryGroup {
	return QueryGroup{QueryPlan: execution.QueryPlanFacts{
		QueryRevision: execution.QueryRevision("group-query-" + businessID), TenantID: "default", BusinessID: businessID,
		SpaceScope: "space-" + businessID, Normalization: execution.DatasetNormalizationSpec{DatasetContract: runtimeClosureDatasetContract()},
	}, Plans: plans}
}

func runtimeClosureCatalog(groups ...QueryGroup) Catalog {
	dispositions := make([]ObjectDisposition, 0)
	for _, group := range groups {
		for _, plan := range group.Plans {
			dispositions = append(dispositions, ObjectDisposition{
				SourceID: plan.Identity.StrategyID, Scope: "PLAN", Disposition: DispositionAccepted,
			})
		}
	}
	return Catalog{ObservationID: "runtime-closure", QueryGroups: groups, Dispositions: dispositions}
}

func runtimeClosurePlanRevision(t *testing.T, plan contract.EvaluationPlanV2) string {
	t.Helper()
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-plan-semantics-v1", plan)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func assertRuntimeCatalogPlans(
	t *testing.T,
	catalog Catalog,
	compiler *strategy.PlanCompiler,
	stateSemantics strategy.StateSemantics,
	want []string,
) {
	t.Helper()
	got := make(map[string]struct{})
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			got[plan.Identity.StrategyID] = struct{}{}
			result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
				Plan: plan.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: stateSemantics,
			})
			if err != nil {
				t.Fatalf("compile retained Plan %s: %v", plan.Identity.StrategyID, err)
			}
			compiled, ok := result.Plan()
			if !ok || result.PlanTerminal() != nil || len(result.LevelTerminals()) != 0 || len(compiled.Levels()) == 0 {
				t.Fatalf("retained Plan %s is not executable: result=%+v", plan.Identity.StrategyID, result)
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("retained Plans = %v, want %v", got, want)
	}
	for _, strategyID := range want {
		if _, ok := got[strategyID]; !ok {
			t.Fatalf("retained Plans = %v, missing %s", got, strategyID)
		}
	}
}

func assertRuntimeClosureRejected(t *testing.T, dispositions []ObjectDisposition, strategyID string) {
	t.Helper()
	accepted := false
	rejected := false
	for _, disposition := range dispositions {
		if disposition.SourceID != strategyID || disposition.Scope != "PLAN" {
			continue
		}
		accepted = accepted || disposition.Disposition == DispositionAccepted
		rejected = rejected || (disposition.Disposition == DispositionConfigRejected && disposition.Reason == "RUNTIME_CATALOG_CLOSURE_INVALID")
	}
	if accepted || !rejected {
		t.Fatalf("dispositions = %+v, want only explicit runtime closure rejection for %s", dispositions, strategyID)
	}
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
