// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A threshold edit moves the Plan's generation and keeps its Query Group,
// so the cutover reads the previous version back from the object the
// previous activation ran, to carry what both versions share. When that
// object is gone - evicted, or never written back after a failed write -
// the previous version cannot be read: nothing is carried
// (none_previous_unreadable), the Plan is forced to warm up whole, and it is
// not one a Worker may take for new. With the object there, the same edit
// carries.
func TestAnEditWhosePreviousVersionCannotBeReadWarmsUpWhole(t *testing.T) {
	for _, test := range []struct {
		name    string
		evicted bool
		outcome string
	}{
		{name: "previous object there", outcome: "carried_detect_changed"},
		{name: "previous object evicted", evicted: true, outcome: "none_previous_unreadable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			client := newControlplaneRedis(t)
			const prefix = "alarmd:control:state-carry-unreadable"
			original := realThresholdDocuments(t)[0]
			var decoded map[string]any
			if err := json.Unmarshal(original, &decoded); err != nil {
				t.Fatal(err)
			}
			decoded["items"].([]any)[0].(map[string]any)["algorithms"].([]any)[0].(map[string]any)["config"] =
				[]any{[]any{map[string]any{"method": "gte", "threshold": 90}}}
			edited, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			set := func(key, value string) {
				t.Helper()
				if err := client.Set(ctx, key, value, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			set("bkmonitor.cache.strategy_ids", `[1001]`)
			set("bkmonitor.cache.strategy_1001", string(original))
			source := newRedisStrategySource(t, client)
			planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
			if err != nil {
				t.Fatal(err)
			}
			repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			compiler, semantics := runtimePlanCompiler(t)
			reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
			if err != nil {
				t.Fatal(err)
			}
			publish := func() controlplane.SnapshotPublicationRef {
				t.Helper()
				result, err := reconciler.Refresh(ctx, source, planner)
				if err != nil || result.Status != controlplane.SourceRefreshPublished {
					t.Fatalf("publish = (%+v, %v)", result, err)
				}
				return result.Publication
			}
			initialActivator, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := initialActivator.Ensure(ctx, publish()); err != nil {
				t.Fatal(err)
			}
			previousObjects, err := client.Keys(ctx, "*:qgobj:*").Result()
			if err != nil || len(previousObjects) == 0 {
				t.Fatalf("previous objects %v (%v), want the one the first activation ran", previousObjects, err)
			}

			set("bkmonitor.cache.strategy_1001", string(edited))
			publication := publish()
			if test.evicted {
				if err := client.Del(ctx, previousObjects...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			// A Control Leader that has not read the previous object yet: a
			// repository of its own, with its object cache empty.
			cold, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			var outcomes []string
			cold.ConfigureObserver(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if facts := observation.StateCarry; facts != nil && facts.Scope == "plan" {
					outcomes = append(outcomes, facts.Result)
				}
			}))
			cutover, err := controlplane.NewScheduleActivationReconciler(cold, compiler, semantics, func() time.Time { return time.Unix(120, 0) })
			if err != nil {
				t.Fatal(err)
			}
			changed, err := cutover.Ensure(ctx, publication)
			if err != nil || len(changed.Plans) != 1 {
				t.Fatalf("cutover = (%+v, %v)", changed.Plans, err)
			}
			if len(outcomes) != 1 || outcomes[0] != test.outcome {
				t.Fatalf("carry outcomes %v, want one %s", outcomes, test.outcome)
			}
			selected := changed.Plans[0].Fact.Selected
			if carried := selected.Carry != nil; carried == test.evicted || selected.ForceWarming != test.evicted || selected.Arrived {
				t.Fatalf("activation %+v, want carried %t, forced to warm up %t, and never new", selected, !test.evicted, test.evicted)
			}
		})
	}
}
