// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// Every fact the compiler closes over moves its identity, and nil and empty
// stay distinct in it as they are distinct to the compiler (not observed
// versus observed empty). Two compilers over equal facts share one.
func TestLegacyCompilerIdentityCoversEveryFactItClosesOver(t *testing.T) {
	baseline, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Identity() == "" {
		t.Fatal("a compiler has an identity")
	}
	twin, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	if twin.Identity() != baseline.Identity() {
		t.Fatal("equal facts must give equal identities")
	}
	for name, mutate := range map[string]func(*controlplane.LegacyQueryRuntimeFacts){
		"access_bk_data flipped":      func(facts *controlplane.LegacyQueryRuntimeFacts) { value := false; facts.AccessBKData = &value },
		"access_bk_data not observed": func(facts *controlplane.LegacyQueryRuntimeFacts) { facts.AccessBKData = nil },
		"cmdb level tables": func(facts *controlplane.LegacyQueryRuntimeFacts) {
			facts.BKDataCMDBLevelTables = []string{"system.cpu_summary"}
		},
		"cmdb level tables not observed": func(facts *controlplane.LegacyQueryRuntimeFacts) { facts.BKDataCMDBLevelTables = nil },
		"disk filter values":             func(facts *controlplane.LegacyQueryRuntimeFacts) { facts.SystemDiskFilter.Values = []string{"tmpfs"} },
		"disk filter observed empty":     func(facts *controlplane.LegacyQueryRuntimeFacts) { facts.SystemDiskFilter.Values = []string{} },
		"disk filter field":              func(facts *controlplane.LegacyQueryRuntimeFacts) { facts.SystemDiskFilter.FieldName = "fstype" },
		"network filter values": func(facts *controlplane.LegacyQueryRuntimeFacts) {
			facts.SystemNetworkFilter.Values = []string{"lo", "docker0"}
		},
		"fta event storage": func(facts *controlplane.LegacyQueryRuntimeFacts) {
			facts.FTAEventStorage = &execution.QueryStorage{TableID: "fta.event", StorageID: "1", StorageType: "elasticsearch"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			facts := testLegacyQueryRuntimeFacts()
			mutate(&facts)
			changed, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", facts)
			if err != nil {
				t.Fatal(err)
			}
			if changed.Identity() == baseline.Identity() {
				t.Fatalf("%s left the identity unchanged; a round would reuse plans compiled under the other value", name)
			}
		})
	}
	for name, build := range map[string]func() (*controlplane.LegacyPrimaryQueryCompiler, error){
		"provider route": func() (*controlplane.LegacyPrimaryQueryCompiler, error) {
			return controlplane.NewLegacyPrimaryQueryCompiler("uq-secondary-v1", "UTC", testLegacyQueryRuntimeFacts())
		},
		"timezone": func() (*controlplane.LegacyPrimaryQueryCompiler, error) {
			return controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "Asia/Shanghai", testLegacyQueryRuntimeFacts())
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed, err := build()
			if err != nil {
				t.Fatal(err)
			}
			if changed.Identity() == baseline.Identity() {
				t.Fatalf("%s left the identity unchanged", name)
			}
		})
	}
}

// roundCompilerStrategies is a disk strategy, whose plan carries the
// platform's file system filter, next to a CPU strategy, whose plan does not.
func roundCompilerStrategies(t *testing.T) []controlplane.SourceStrategy {
	t.Helper()
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	return []controlplane.SourceStrategy{
		{SourceID: "201", Identity: identity, Document: g4LegacyStrategyDocument(t, 201, strategy.DetectorKindThreshold, "in_use", "system.disk",
			[]string{"mount_point"}, []any{map[string]any{"method": "gte", "threshold": 90}})},
		{SourceID: "202", Identity: identity, Document: g4LegacyStrategyDocument(t, 202, strategy.DetectorKindThreshold, "usage", "system.cpu",
			[]string{"host"}, []any{map[string]any{"method": "gte", "threshold": 90}})},
	}
}

// planOf is the strategy's Plan and the query facts of the group it sits in.
func planOf(t *testing.T, catalog controlplane.Catalog, sourceID string) (controlplane.FrozenPlan, execution.QueryPlanFacts) {
	t.Helper()
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			if plan.Identity.StrategyID == sourceID {
				return plan, group.QueryPlan
			}
		}
	}
	t.Fatalf("strategy %s is not in the Catalog: %+v", sourceID, catalog.Dispositions)
	return controlplane.FrozenPlan{}, execution.QueryPlanFacts{}
}

func diskFilterValues(t *testing.T, facts execution.QueryPlanFacts) []string {
	t.Helper()
	for _, field := range facts.QueryList[0].Conditions.Fields {
		if field.Field != "device_type" {
			continue
		}
		values := make([]string, 0, len(field.Values))
		for _, value := range field.Values {
			values = append(values, value.StringValue)
		}
		return values
	}
	t.Fatalf("query carries no device_type condition: %+v", facts.QueryList[0].Conditions)
	return nil
}

// A change of a platform setting the plans are compiled by is a change of the
// compiler's identity: the next round compiles every strategy again, under the
// new setting, and the Catalog's revision moves with the plans that changed.
// The same setting across rounds reuses everything, as before.
func TestCandidateCacheRecompilesEverythingWhenTheCompilerIdentityChanges(t *testing.T) {
	ctx := context.Background()
	current := testLegacyQueryRuntimeFacts()
	reads := 0
	bound, err := controlplane.NewSettingsBoundLegacyCompiler("uq-primary-v1", "UTC", func() controlplane.LegacyQueryRuntimeFacts {
		reads++
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	strategies := roundCompilerStrategies(t)
	cache := controlplane.NewCandidateCache()
	first, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: bound, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("first round compiled=%d reused=%d", compiled, reused)
	}
	_, firstDisk := planOf(t, first, "201")
	if got := diskFilterValues(t, firstDisk); strings.Join(got, ",") != "iso9660,tmpfs,udf" {
		t.Fatalf("first round disk filter = %v, want the facts the round froze", got)
	}
	second, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: bound, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 0 || reused != 2 {
		t.Fatalf("same facts: compiled=%d reused=%d, want everything reused", compiled, reused)
	}
	if second.SnapshotRevision != first.SnapshotRevision {
		t.Fatal("the same facts and documents must build the same Catalog")
	}
	current.SystemDiskFilter.Values = []string{"tmpfs"}
	third, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: bound, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := cache.Stats(); compiled != 2 || reused != 0 {
		t.Fatalf("changed facts: compiled=%d reused=%d, want everything compiled again", compiled, reused)
	}
	_, thirdDisk := planOf(t, third, "201")
	if got := diskFilterValues(t, thirdDisk); strings.Join(got, ",") != "tmpfs" {
		t.Fatalf("third round disk filter = %v, want the changed setting", got)
	}
	if third.SnapshotRevision == first.SnapshotRevision {
		t.Fatal("a plan compiled under another setting must move the Catalog revision")
	}
	if thirdDisk.QueryRevision == firstDisk.QueryRevision {
		t.Fatal("the disk query's revision must move with its filter")
	}
	_, firstCPU := planOf(t, first, "202")
	_, thirdCPU := planOf(t, third, "202")
	if thirdCPU.QueryRevision != firstCPU.QueryRevision {
		t.Fatal("the CPU query does not read the disk filter; its revision must not move")
	}
	// The facts are read once per round and once at assembly: a round
	// compiles every strategy by the facts it froze when it opened.
	if reads != 4 {
		t.Fatalf("facts read %d times, want once at assembly and once per round", reads)
	}
	if _, err := bound.CompilePrimaryQuery(ctx, controlplane.PrimaryQuerySource{}); err == nil || !strings.Contains(err.Error(), "inside a round") {
		t.Fatalf("compiling outside a round: err = %v, want refused", err)
	}
}

// Without a cache a round-scoped compiler still compiles by the facts it
// freezes when the round opens, so the two builds of the same round agree.
func TestRoundScopedCompilerFreezesFactsWithoutACache(t *testing.T) {
	ctx := context.Background()
	current := testLegacyQueryRuntimeFacts()
	bound, err := controlplane.NewSettingsBoundLegacyCompiler("uq-primary-v1", "UTC", func() controlplane.LegacyQueryRuntimeFacts { return current })
	if err != nil {
		t.Fatal(err)
	}
	strategies := roundCompilerStrategies(t)
	frozen, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", current)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: frozen})
	if err != nil {
		t.Fatal(err)
	}
	built, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: bound})
	if err != nil {
		t.Fatal(err)
	}
	if !sameCatalog(built, reference) {
		t.Fatal("a round-scoped compiler over the same facts must build what the frozen compiler builds")
	}
}
