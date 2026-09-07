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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestRetainCompiledLevelsRetainsLevelDependencyClosure(t *testing.T) {
	plan := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	plan.Plan.StrategyIR.Levels[1].DetectPlan.Algorithms[0].Type = "Unsupported"
	plan.PlanRevision = runtimeClosurePlanRevision(t, plan.Plan)

	retained, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 1))
	if err != nil {
		t.Fatalf("retainCompiledLevels() error = %v", err)
	}
	if len(retained.Plan.StrategyIR.Levels) != 1 || retained.Plan.StrategyIR.Levels[0].Definition.LevelID != 1 {
		t.Fatalf("retained Levels = %+v, want only Level 1", retained.Plan.StrategyIR.Levels)
	}
	for _, requirement := range retained.RequirementTemplates {
		if requirement.ConsumerLevelID != 1 {
			t.Fatalf("retained Requirement = %+v, want only Level 1 closure", requirement)
		}
	}
	if len(retained.RequirementTemplates) != 2 || len(retained.QueryPlans) != 1 {
		t.Fatalf("retained closure = %+v / %+v, want SimpleRingRatio exact closure", retained.RequirementTemplates, retained.QueryPlans)
	}
}

func TestRetainRuntimeExecutableCatalogBindsStateCompatibilityToSnapshot(t *testing.T) {
	compiler, initialSemantics := runtimeClosureCompiler(t)
	plan := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	catalog := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", plan))

	initial, err := retainRuntimeExecutableCatalog(
		context.Background(), catalog, nil, compiler, initialSemantics,
	)
	if err != nil {
		t.Fatal(err)
	}
	changedSemantics := initialSemantics
	changedSemantics.IdentitySchemaDigest = strings.Repeat("4", 64)
	changed, err := retainRuntimeExecutableCatalog(
		context.Background(), catalog, nil, compiler, changedSemantics,
	)
	if err != nil {
		t.Fatal(err)
	}
	stable, err := retainRuntimeExecutableCatalog(
		context.Background(), catalog, nil, compiler, changedSemantics,
	)
	if err != nil {
		t.Fatal(err)
	}

	if initial.QueryGroups[0].Plans[0].PlanRevision != changed.QueryGroups[0].Plans[0].PlanRevision {
		t.Fatal("raw Threshold Plan revision changed with runtime state semantics")
	}
	initialGeneration := initial.QueryGroups[0].Plans[0].StateGeneration
	changedGeneration := changed.QueryGroups[0].Plans[0].StateGeneration
	if initialGeneration == "" || changedGeneration == "" || initialGeneration == changedGeneration {
		t.Fatalf("frozen state generations = (%q, %q), want distinct non-empty facts",
			initialGeneration, changedGeneration)
	}
	if initial.SnapshotRevision == changed.SnapshotRevision {
		t.Fatalf("Snapshot revision remained %q after state compatibility changed", initial.SnapshotRevision)
	}
	if changed.SnapshotRevision != stable.SnapshotRevision {
		t.Fatalf("unchanged state compatibility revision is unstable: changed=%q stable=%q",
			changed.SnapshotRevision, stable.SnapshotRevision)
	}
	tampered := changed
	tampered.QueryGroups = append([]QueryGroup(nil), changed.QueryGroups...)
	tampered.QueryGroups[0].Plans = append([]FrozenPlan(nil), changed.QueryGroups[0].Plans...)
	tampered.QueryGroups[0].Plans[0].StateGeneration = execution.StateGeneration(strings.Repeat("f", 64))
	if _, _, err := compilePublishedActivation(context.Background(), compiler, changedSemantics, PublishedSnapshot{
		Publication: SnapshotPublicationRef{SnapshotRevision: tampered.SnapshotRevision, PublicationEpoch: 1},
		QueryGroups: tampered.QueryGroups,
	}, 60); err == nil {
		t.Fatal("activation accepted a frozen state generation that differs from the compiler")
	}
}

func TestRetainRuntimeExecutableCatalogDoesNotResurrectDroppedSource(t *testing.T) {
	compiler, semantics := runtimeClosureCompiler(t)
	kept := runtimeClosureNamedFrozenPlan(t, "1001", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	dropped := runtimeClosureNamedFrozenPlan(t, "1002", "1", []contract.LevelIRV2{
		runtimeClosureLevel(1, strategy.DetectorKindThreshold),
	})
	lastGood := &PublishedSnapshot{
		Publication: SnapshotPublicationRef{SnapshotRevision: "last-good", PublicationEpoch: 1},
		QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", kept, dropped)},
	}
	catalog := runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", kept))
	removed := ObjectDisposition{SourceID: "1002", Scope: "STRATEGY", Disposition: DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET"}
	catalog.Dispositions = append(catalog.Dispositions, removed)

	result, err := retainRuntimeExecutableCatalog(context.Background(), catalog, lastGood, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QueryGroups) != 1 || len(result.QueryGroups[0].Plans) != 1 ||
		result.QueryGroups[0].Plans[0].Identity.StrategyID != "1001" {
		t.Fatalf("runtime Catalog resurrected a dropped source: %+v", result.QueryGroups)
	}
	sawRemoved := false
	for _, disposition := range result.Dispositions {
		if disposition.SourceID == "1002" && disposition != removed {
			t.Fatalf("dropped source gained disposition %+v", disposition)
		}
		sawRemoved = sawRemoved || disposition == removed
	}
	if !sawRemoved {
		t.Fatalf("REMOVED disposition was lost: %+v", result.Dispositions)
	}
}

func TestRetainCompiledLevelsAllRejectedClearsDependencyClosure(t *testing.T) {
	plan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{2, strategy.DetectorKindOsRestart})
	plan.Plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Type = "Unsupported"
	plan.PlanRevision = runtimeClosurePlanRevision(t, plan.Plan)

	retained, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 0))
	if err != nil {
		t.Fatalf("retainCompiledLevels() error = %v", err)
	}
	assertRuntimeClosure(t, retained, nil, nil, nil)
}

func TestRetainCompiledLevelsRejectsDanglingRequirement(t *testing.T) {
	plan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	plan.QueryPlans = nil

	if _, err := retainCompiledLevels(plan, runtimeClosureCompile(t, plan.Plan, 1)); err == nil {
		t.Fatal("retainCompiledLevels() accepted a retained Requirement without QueryPlan facts")
	}
}

func TestSupplementRejectedLevelsRestoresLastGoodLevelDependencyClosure(t *testing.T) {
	current := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{3, strategy.DetectorKindOsRestart},
	)
	lastGood := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
		runtimeClosureG4Level{3, strategy.DetectorKindOsRestart},
	)

	got, supplemented, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 2, Disposition: DispositionConfigRejected,
	}})
	if err != nil {
		t.Fatalf("supplementRejectedLevels() error = %v", err)
	}
	if _, ok := supplemented[2]; !ok || len(supplemented) != 1 {
		t.Fatalf("supplemented = %v, want only Level 2", supplemented)
	}
	compiled := runtimeClosureCompile(t, got.Plan, 3)
	if err := validateRuntimePlanDependencyClosure(got, compiled); err != nil {
		t.Fatalf("supplemented closure validation error = %v", err)
	}
}

func TestSupplementRejectedLevelsWithoutLastGoodMatchLeavesClosureUnchanged(t *testing.T) {
	current := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	lastGood := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{2, strategy.DetectorKindOsRestart})

	got, supplemented, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 3, Disposition: DispositionConfigRejected,
	}})
	if err != nil {
		t.Fatalf("supplementRejectedLevels() error = %v", err)
	}
	if len(supplemented) != 0 {
		t.Fatalf("supplemented = %v, want empty", supplemented)
	}
	if !reflect.DeepEqual(got.RequirementTemplates, current.RequirementTemplates) || !reflect.DeepEqual(got.QueryPlans, current.QueryPlans) {
		t.Fatalf("unmatched supplement changed closure: got=%+v/%+v want=%+v/%+v", got.RequirementTemplates, got.QueryPlans, current.RequirementTemplates, current.QueryPlans)
	}
}

func TestSupplementRejectedLevelsRejectsDanglingLastGoodRequirement(t *testing.T) {
	current := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	lastGood := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	for _, requirement := range lastGood.RequirementTemplates {
		if requirement.ConsumerLevelID == 2 && requirement.Role == execution.InputRoleAlgorithmDependency {
			delete(lastGood.QueryPlans, requirement.LogicalQueryRef)
			break
		}
	}

	if _, _, err := supplementRejectedLevels(current, lastGood, []ObjectDisposition{{
		Scope: "LEVEL", LevelID: 2, Disposition: DispositionConfigRejected,
	}}); err == nil {
		t.Fatal("supplementRejectedLevels() accepted a last-good Requirement without QueryPlan facts")
	}
}

func TestRetainRuntimeExecutableCatalogIsolatesDanglingCurrentPlan(t *testing.T) {
	bad := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	bad.QueryPlans = nil
	healthySibling := runtimeClosureCompletePlan(t, "1002", "1")
	healthyOtherGroup := runtimeClosureCompletePlan(t, "2001", "2")
	catalog := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", bad, healthySibling),
		runtimeClosureQueryGroup(t, "2", healthyOtherGroup),
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
	bad := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	bad.Plan.StrategyIR.Levels[1].Connector = "INVALID"
	bad.PlanRevision = runtimeClosurePlanRevision(t, bad.Plan)
	healthySibling := runtimeClosureCompletePlan(t, "1002", "1")
	healthyOtherGroup := runtimeClosureCompletePlan(t, "2001", "2")
	catalog := runtimeClosureCatalog(
		runtimeClosureQueryGroup(t, "1", bad, healthySibling),
		runtimeClosureQueryGroup(t, "2", healthyOtherGroup),
	)
	lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	for _, requirement := range lastGoodPlan.RequirementTemplates {
		if requirement.ConsumerLevelID == 2 && requirement.Role == execution.InputRoleAlgorithmDependency {
			delete(lastGoodPlan.QueryPlans, requirement.LogicalQueryRef)
			break
		}
	}
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
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
		runtimeClosureQueryGroup(t, "1", bad, healthySibling),
		runtimeClosureQueryGroup(t, "2", healthyOtherGroup),
	)
	lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	lastGoodPlan.QueryPlans = nil
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
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
	lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
	lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)

	got, err := retainRuntimeExecutableCatalog(
		context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", current)), lastGood, compiler, stateSemantics,
	)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	if len(got.QueryGroups) != 1 || len(got.QueryGroups[0].Plans) != 1 {
		t.Fatalf("QueryGroups = %+v, want one last-good Plan", got.QueryGroups)
	}
	if !reflect.DeepEqual(got.QueryGroups[0].Plans[0].RequirementTemplates, lastGoodPlan.RequirementTemplates) ||
		!reflect.DeepEqual(got.QueryGroups[0].Plans[0].QueryPlans, lastGoodPlan.QueryPlans) {
		t.Fatalf("last-good closure changed: got=%+v/%+v want=%+v/%+v", got.QueryGroups[0].Plans[0].RequirementTemplates,
			got.QueryGroups[0].Plans[0].QueryPlans, lastGoodPlan.RequirementTemplates, lastGoodPlan.QueryPlans)
	}
}

func TestValidateRuntimePlanDependencyClosureMatchesCompiledG4InputsExactly(t *testing.T) {
	plan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
	compiled := runtimeClosureCompile(t, plan.Plan, 1)
	validOtherQuery := runtimeClosureQueryFacts(t, "1", "other")

	tests := []struct {
		name   string
		mutate func(*FrozenPlan)
	}{
		{name: "missing all templates", mutate: func(plan *FrozenPlan) { plan.RequirementTemplates = nil; plan.QueryPlans = nil }},
		{name: "missing one template", mutate: func(plan *FrozenPlan) { plan.RequirementTemplates = plan.RequirementTemplates[:1] }},
		{name: "duplicate template", mutate: func(plan *FrozenPlan) {
			plan.RequirementTemplates = append(plan.RequirementTemplates, plan.RequirementTemplates[0])
		}},
		{name: "wrong consumer level", mutate: func(plan *FrozenPlan) { plan.RequirementTemplates[0].ConsumerLevelID = 2 }},
		{name: "tampered canonical fields", mutate: func(plan *FrozenPlan) {
			plan.RequirementTemplates[0].RequiredColumns = append(plan.RequirementTemplates[0].RequiredColumns, "tampered")
		}},
		{name: "missing query facts", mutate: func(plan *FrozenPlan) { plan.QueryPlans = nil }},
		{name: "extra query facts", mutate: func(plan *FrozenPlan) {
			plan.QueryPlans[execution.LogicalQueryRef(validOtherQuery.QueryRevision)] = validOtherQuery
		}},
		{name: "wrong query revision", mutate: func(plan *FrozenPlan) {
			for ref, facts := range plan.QueryPlans {
				delete(plan.QueryPlans, ref)
				plan.QueryPlans["wrong-ref"] = facts
				break
			}
		}},
		{name: "invalid query facts", mutate: func(plan *FrozenPlan) {
			for ref, facts := range plan.QueryPlans {
				facts.TenantID = ""
				plan.QueryPlans[ref] = facts
				break
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := runtimeClosureCloneFrozenPlan(plan)
			test.mutate(&candidate)
			if err := validateRuntimePlanDependencyClosure(candidate, compiled); !errors.Is(err, errRuntimeCatalogClosureInvalid) {
				t.Fatalf("validateRuntimePlanDependencyClosure() error = %v, want closure invalid", err)
			}
		})
	}
	if err := validateRuntimePlanDependencyClosure(plan, compiled); err != nil {
		t.Fatalf("validateRuntimePlanDependencyClosure(valid) error = %v", err)
	}
}

func TestRetainRuntimeExecutableCatalogIsolatesInexactWholePlanLastGoodClosure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FrozenPlan)
	}{
		{name: "missing all templates", mutate: func(plan *FrozenPlan) { plan.RequirementTemplates = nil; plan.QueryPlans = nil }},
		{name: "missing one template", mutate: func(plan *FrozenPlan) {
			removed := plan.RequirementTemplates[1]
			plan.RequirementTemplates = plan.RequirementTemplates[:1]
			delete(plan.QueryPlans, removed.LogicalQueryRef)
		}},
		{name: "missing facts", mutate: func(plan *FrozenPlan) {
			for ref := range plan.QueryPlans {
				delete(plan.QueryPlans, ref)
				break
			}
		}},
		{name: "wrong revision", mutate: func(plan *FrozenPlan) {
			for ref, facts := range plan.QueryPlans {
				facts.QueryRevision = execution.QueryRevision(strings.Repeat("f", 64))
				plan.QueryPlans[ref] = facts
				break
			}
		}},
		{name: "invalid facts", mutate: func(plan *FrozenPlan) {
			for ref, facts := range plan.QueryPlans {
				facts.ProviderRouteRef = ""
				plan.QueryPlans[ref] = facts
				break
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bad := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
			bad.Plan.StrategyIR.ExecutionSemantics.EvaluationScope = contract.EvaluationScopeCrossSeries
			bad.PlanRevision = runtimeClosurePlanRevision(t, bad.Plan)
			lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
			test.mutate(&lastGoodPlan)
			healthySibling := runtimeClosureG4Plan(t, "1002", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
			healthyOtherGroup := runtimeClosureG4Plan(t, "2001", "2", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
			catalog := runtimeClosureCatalog(
				runtimeClosureQueryGroup(t, "1", bad, healthySibling),
				runtimeClosureQueryGroup(t, "2", healthyOtherGroup),
			)
			lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
			compiler, stateSemantics := runtimeClosureCompiler(t)

			got, err := retainRuntimeExecutableCatalog(context.Background(), catalog, lastGood, compiler, stateSemantics)
			if err != nil {
				t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
			}
			assertRuntimeCatalogPlans(t, got, compiler, stateSemantics, []string{"1002", "2001"})
			assertRuntimeClosureRejected(t, got.Dispositions, "1001")
		})
	}
}

func TestRetainRuntimeExecutableCatalogIsolatesInexactPerLevelLastGoodClosure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FrozenPlan)
	}{
		{name: "missing dependency", mutate: func(plan *FrozenPlan) {
			for index, requirement := range plan.RequirementTemplates {
				if requirement.ConsumerLevelID == 2 && requirement.Role == execution.InputRoleAlgorithmDependency {
					plan.RequirementTemplates = append(plan.RequirementTemplates[:index], plan.RequirementTemplates[index+1:]...)
					return
				}
			}
		}},
		{name: "invalid facts", mutate: func(plan *FrozenPlan) {
			for _, requirement := range plan.RequirementTemplates {
				if requirement.ConsumerLevelID == 2 && requirement.Role == execution.InputRoleAlgorithmDependency {
					facts := plan.QueryPlans[requirement.LogicalQueryRef]
					facts.TenantID = ""
					plan.QueryPlans[requirement.LogicalQueryRef] = facts
					return
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := runtimeClosureG4Plan(t, "1001", "1",
				runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
				runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
			)
			current.Plan.StrategyIR.Levels[1].Connector = "INVALID"
			current.PlanRevision = runtimeClosurePlanRevision(t, current.Plan)
			lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1",
				runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
				runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
			)
			test.mutate(&lastGoodPlan)
			healthySibling := runtimeClosureG4Plan(t, "1002", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
			healthyOtherGroup := runtimeClosureG4Plan(t, "2001", "2", runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
			catalog := runtimeClosureCatalog(
				runtimeClosureQueryGroup(t, "1", current, healthySibling),
				runtimeClosureQueryGroup(t, "2", healthyOtherGroup),
			)
			lastGood := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
			compiler, stateSemantics := runtimeClosureCompiler(t)

			got, err := retainRuntimeExecutableCatalog(context.Background(), catalog, lastGood, compiler, stateSemantics)
			if err != nil {
				t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
			}
			assertRuntimeCatalogPlans(t, got, compiler, stateSemantics, []string{"1002", "2001"})
			assertRuntimeClosureRejected(t, got.Dispositions, "1001")
		})
	}
}

func TestRetainRuntimeExecutableCatalogPreservesValidQueryFacts(t *testing.T) {
	plan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
	want := runtimeClosureCloneQueryPlans(plan.QueryPlans)
	compiler, stateSemantics := runtimeClosureCompiler(t)
	got, err := retainRuntimeExecutableCatalog(
		context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", plan)), nil, compiler, stateSemantics,
	)
	if err != nil {
		t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
	}
	if len(got.QueryGroups) != 1 || len(got.QueryGroups[0].Plans) != 1 || !reflect.DeepEqual(got.QueryGroups[0].Plans[0].QueryPlans, want) {
		t.Fatalf("retained QueryPlans = %+v, want unchanged %+v", got.QueryGroups, want)
	}
}

func TestRetainRuntimeExecutableCatalogStillBubblesNonClosureFailures(t *testing.T) {
	t.Run("compiler", func(t *testing.T) {
		want := errors.New("compiler unavailable")
		_, stateSemantics := runtimeClosureCompiler(t)
		_, err := retainRuntimeExecutableCatalog(
			context.Background(),
			runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", runtimeClosureCompletePlan(t, "1001", "1"))),
			nil, runtimeClosureCompilerFunc(func(context.Context, strategy.CompileRequest) (strategy.CompileResult, error) {
				return strategy.CompileResult{}, want
			}), stateSemantics,
		)
		if !errors.Is(err, want) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want compiler error", err)
		}
	})

	t.Run("digest", func(t *testing.T) {
		plan := runtimeClosureCompletePlan(t, "1001", "1")
		validPlan := runtimeClosureCompletePlan(t, "1001", "1").Plan
		plan.Plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{`)
		normal, stateSemantics := runtimeClosureCompiler(t)
		_, err := retainRuntimeExecutableCatalog(
			context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", plan)), nil,
			runtimeClosureCompilerFunc(func(ctx context.Context, request strategy.CompileRequest) (strategy.CompileResult, error) {
				request.Plan = validPlan
				return normal.Compile(ctx, request)
			}), stateSemantics,
		)
		if err == nil || errors.Is(err, errRuntimeCatalogClosureInvalid) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want non-closure digest error", err)
		}
	})

	t.Run("query group conflict", func(t *testing.T) {
		first := runtimeClosureQueryGroup(t, "1", runtimeClosureCompletePlan(t, "1001", "1"))
		second := runtimeClosureQueryGroup(t, "1", runtimeClosureCompletePlan(t, "1002", "1"))
		second.QueryPlan.QueryRevision = execution.QueryRevision(strings.Repeat("f", 64))
		compiler, stateSemantics := runtimeClosureCompiler(t)
		_, err := retainRuntimeExecutableCatalog(
			context.Background(), runtimeClosureCatalog(first, second), nil, compiler, stateSemantics,
		)
		if err == nil || errors.Is(err, errRuntimeCatalogClosureInvalid) {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v, want Query Group conflict", err)
		}
	})
}

type runtimeClosureCompilerFunc func(context.Context, strategy.CompileRequest) (strategy.CompileResult, error)

func (compile runtimeClosureCompilerFunc) Compile(
	ctx context.Context,
	request strategy.CompileRequest,
) (strategy.CompileResult, error) {
	return compile(ctx, request)
}

type runtimeClosureG4Level struct {
	levelID uint32
	kind    string
}

func runtimeClosureG4Plan(t *testing.T, strategyID, businessID string, levels ...runtimeClosureG4Level) FrozenPlan {
	t.Helper()
	primary := runtimeClosureQueryFacts(t, businessID, "a <= 3600")
	history := runtimeClosureQueryFacts(t, businessID, "a")
	inputs := &compiledPlanInputs{primary: primary, osRestartHistory: &history}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	compiledLevels := make([]contract.LevelIRV2, 0, len(levels))
	for _, level := range levels {
		rawConfig := json.RawMessage(`{}`)
		if level.kind == strategy.DetectorKindSimpleRingRatio {
			rawConfig = json.RawMessage(`{"floor":50,"ceil":null}`)
		}
		config, err := compileAlgorithmConfig(
			legacyAlgorithm{Level: level.levelID, Type: level.kind, Config: rawConfig}, "percent", level.levelID,
			projection, runtimeClosureDatasetContract().IdentityFields, 60, inputs,
		)
		if err != nil {
			t.Fatal(err)
		}
		compiledLevel := runtimeClosureLevel(level.levelID, strategy.DetectorKindThreshold)
		compiledLevel.DetectPlan.Algorithms = []contract.AlgorithmIRV2{{Type: level.kind, Version: 1, Config: config}}
		compiledLevels = append(compiledLevels, compiledLevel)
	}
	plan := runtimeClosureNamedFrozenPlan(t, strategyID, businessID, compiledLevels)
	plan.RequirementTemplates = append([]execution.DataRequirementTemplate(nil), inputs.requirements...)
	plan.QueryPlans = runtimeClosureCloneQueryPlans(inputs.queryPlans)
	return plan
}

func runtimeClosureQueryFacts(t *testing.T, businessID, metricMerge string) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary", TenantID: "default", BusinessID: businessID,
		SpaceScope: "space-" + businessID,
		QueryList: []execution.QueryClause{{
			DataSource: "bk_monitor", Driver: "influxdb", TableID: "system.cpu", FieldName: "usage", TimeField: "time",
			ReferenceName: "a", Functions: []execution.QueryFunction{{Method: "default", Position: 0}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0},
		}},
		MetricMerge: metricMerge, StepMillis: 60000, AlignmentMillis: 60000,
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: runtimeClosureDatasetContract(), SourceTimeUnit: execution.TimeUnitMillisecond,
			CanonicalSourceTimeUnit: execution.TimeUnitSecond, SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1,
			GroupKeyRule: execution.GroupKeyStripTableSuffixV1, ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1,
			CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func runtimeClosureCloneFrozenPlan(plan FrozenPlan) FrozenPlan {
	plan.RequirementTemplates = append([]execution.DataRequirementTemplate(nil), plan.RequirementTemplates...)
	plan.QueryPlans = runtimeClosureCloneQueryPlans(plan.QueryPlans)
	return plan
}

func runtimeClosureCloneQueryPlans(source map[execution.LogicalQueryRef]execution.QueryPlanFacts) map[execution.LogicalQueryRef]execution.QueryPlanFacts {
	result := make(map[execution.LogicalQueryRef]execution.QueryPlanFacts, len(source))
	for ref, facts := range source {
		result[ref] = facts
	}
	return result
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
	return runtimeClosureG4Plan(t, strategyID, businessID, runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio})
}

func runtimeClosureQueryGroup(t *testing.T, businessID string, plans ...FrozenPlan) QueryGroup {
	return QueryGroup{QueryPlan: runtimeClosureQueryFacts(t, businessID, "a"), Plans: plans}
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
