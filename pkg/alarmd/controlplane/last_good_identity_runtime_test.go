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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// otherIdentity is the last good snapshot as a writer whose numbering
// started over would leave it: the same strategy number, built for a
// strategy with another global switch.
func otherIdentity(snapshot *PublishedSnapshot) *PublishedSnapshot {
	groups := append([]QueryGroup(nil), snapshot.QueryGroups...)
	for i := range groups {
		groups[i].QueryPlan.GlobalBusiness = !groups[i].QueryPlan.GlobalBusiness
	}
	return &PublishedSnapshot{Publication: snapshot.Publication, QueryGroups: groups}
}

func identityChangedFor(catalog Catalog, sourceID string) bool {
	for _, disposition := range catalog.Dispositions {
		if disposition.SourceID == sourceID && disposition.Scope == "PLAN" && disposition.Disposition == DispositionConfigRejected &&
			disposition.Reason == reasonLastGoodIdentityChanged {
			return true
		}
	}
	return false
}

// A Plan whose runtime compile is refused whole stands on its last good
// definition -- when that definition was built for the strategy the source
// names now. Built for another identity under the same number, it is named,
// counted and not run.
func TestTheRuntimeCatalogDoesNotStandAWholePlanOnAnotherStrategysLastGood(t *testing.T) {
	current := runtimeClosureCompletePlan(t, "1001", "1")
	current.Plan.StrategyIR.ExecutionSemantics.EvaluationScope = contract.EvaluationScopeCrossSeries
	current.PlanRevision = runtimeClosurePlanRevision(t, current.Plan)
	lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1", runtimeClosureG4Level{1, strategy.DetectorKindOsRestart})
	same := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)
	build := func(lastGood *PublishedSnapshot) Catalog {
		t.Helper()
		got, err := retainRuntimeExecutableCatalog(context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", current)),
			lastGood, compiler, stateSemantics)
		if err != nil {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
		}
		return got
	}
	if got := build(same); len(got.QueryGroups) != 1 || got.LastGoodIdentityChanged != 0 || identityChangedFor(got, "1001") {
		t.Fatalf("same identity: groups %d, identity changed %d; want the last good Plan standing, nothing refused",
			len(got.QueryGroups), got.LastGoodIdentityChanged)
	}
	got := build(otherIdentity(same))
	if len(got.QueryGroups) != 0 || got.LastGoodIdentityChanged != 1 || !identityChangedFor(got, "1001") {
		t.Fatalf("another identity: groups %+v, identity changed %d, dispositions %+v; want no Plan, one LAST_GOOD_IDENTITY_CHANGED",
			got.QueryGroups, got.LastGoodIdentityChanged, got.Dispositions)
	}
}

// A Plan whose runtime compile refuses one Level has that Level supplied
// from its last good definition -- the same rule, Level by Level: from a
// definition built for another identity nothing is supplied.
func TestTheRuntimeCatalogDoesNotSupplyALevelFromAnotherStrategysLastGood(t *testing.T) {
	bad := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	bad.Plan.StrategyIR.Levels[1].Connector = "INVALID"
	bad.PlanRevision = runtimeClosurePlanRevision(t, bad.Plan)
	lastGoodPlan := runtimeClosureG4Plan(t, "1001", "1",
		runtimeClosureG4Level{1, strategy.DetectorKindSimpleRingRatio},
		runtimeClosureG4Level{2, strategy.DetectorKindOsRestart},
	)
	same := &PublishedSnapshot{QueryGroups: []QueryGroup{runtimeClosureQueryGroup(t, "1", lastGoodPlan)}}
	compiler, stateSemantics := runtimeClosureCompiler(t)
	build := func(lastGood *PublishedSnapshot) Catalog {
		t.Helper()
		got, err := retainRuntimeExecutableCatalog(context.Background(), runtimeClosureCatalog(runtimeClosureQueryGroup(t, "1", bad)),
			lastGood, compiler, stateSemantics)
		if err != nil {
			t.Fatalf("retainRuntimeExecutableCatalog() error = %v", err)
		}
		return got
	}
	levels := func(catalog Catalog) int {
		if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
			t.Fatalf("groups = %+v, want the one Plan", catalog.QueryGroups)
		}
		return len(catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels)
	}
	if got := build(same); levels(got) != 2 || got.LastGoodIdentityChanged != 0 {
		t.Fatalf("same identity: %d Levels, identity changed %d; want the refused Level supplied", levels(got), got.LastGoodIdentityChanged)
	}
	got := build(otherIdentity(same))
	if levels(got) != 1 || got.LastGoodIdentityChanged != 1 || !identityChangedFor(got, "1001") {
		t.Fatalf("another identity: %d Levels, identity changed %d, dispositions %+v; want the refused Level left out and named",
			levels(got), got.LastGoodIdentityChanged, got.Dispositions)
	}
}
