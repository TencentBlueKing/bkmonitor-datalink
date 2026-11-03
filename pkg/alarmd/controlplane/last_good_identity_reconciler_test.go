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
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The refresh round reports what its Catalog refused: a strategy whose
// document stops compiling keeps its last good Plan while it names the same
// strategy, and the round counts none; the same document moved to another
// business names another strategy, and the round counts the refusal. Counted
// on the round and not only on the Catalog, or the metric reads zero for a
// refusal that happened -- which is also what a steady deployment reads.
func TestARefreshCountsTheLastGoodPlansItRefusedForAnotherIdentity(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	put := func(document json.RawMessage) {
		t.Helper()
		if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	broken := func(document json.RawMessage, business float64, space string) json.RawMessage {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal(document, &value); err != nil {
			t.Fatal(err)
		}
		delete(value, "detects")
		value["bk_biz_id"], value["space_uid"] = business, space
		out, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	documents := realThresholdDocuments(t)
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:last-good-identity", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	// A changed source is confirmed by a second read before it is published;
	// the round reported is the one that published, and the last good Plan
	// the next change is read against is what it published.
	settle := func(round string) controlplane.SourceRefreshResult {
		t.Helper()
		var result controlplane.SourceRefreshResult
		for read := 0; read < 3; read++ {
			var err error
			if result, err = reconciler.Refresh(ctx, source, planner); err != nil {
				t.Fatalf("%s: Refresh() error = %v", round, err)
			}
			if result.Publication.PublicationEpoch > 0 {
				return result
			}
		}
		t.Fatalf("%s: nothing published after three reads: %+v", round, result)
		return result
	}
	put(documents[0])
	if result := settle("opening"); result.LastGoodIdentityChanged != 0 {
		t.Fatalf("opening: identity changed %d, want 0", result.LastGoodIdentityChanged)
	}
	put(broken(documents[0], 2, "bkcc__2"))
	if result := settle("broken, same strategy"); result.LastGoodIdentityChanged != 0 {
		t.Fatalf("broken, same strategy: identity changed %d, want 0 -- its last good Plan stands", result.LastGoodIdentityChanged)
	}
	put(broken(documents[0], 3, "bkcc__3"))
	if result := settle("broken, another business"); result.LastGoodIdentityChanged != 1 {
		t.Fatalf("broken, another business: identity changed %d, want the one refusal counted on the round",
			result.LastGoodIdentityChanged)
	}
}
