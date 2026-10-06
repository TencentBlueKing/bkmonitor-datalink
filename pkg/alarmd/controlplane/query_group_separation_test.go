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
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// twoThresholdStrategiesWithDelay is the fixture two strategies whose queries
// are identical, with the item delay of the second set to delaySeconds. It is
// the smallest difference that produced the production failure: one Catalog
// build stopping for every strategy because two of them disagreed on a fact
// the Query Group identity did not read.
func twoThresholdStrategiesWithDelay(t *testing.T, delaySeconds int) []controlplane.SourceStrategy {
	t.Helper()
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("fixture holds %d strategies, want 2", len(documents))
	}
	if delaySeconds != 0 {
		var second map[string]any
		if err := json.Unmarshal(documents[1], &second); err != nil {
			t.Fatal(err)
		}
		items, ok := second["items"].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("fixture strategy carries no items: %v", second["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("fixture item is not an object: %v", items[0])
		}
		item["time_delay"] = delaySeconds
		delayed, err := json.Marshal(second)
		if err != nil {
			t.Fatal(err)
		}
		documents[1] = delayed
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	return []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: identity},
		{SourceID: "1002", Document: documents[1], Identity: identity},
	}
}

// Two strategies that differ only in their item's query delay are two
// queries, because the delay moves the window each one asks for. They
// therefore build two Query Groups and the Catalog builds.
//
// Before the identity read the delay they were one group with two
// revisions, which BuildCatalog refuses -- and it refuses the whole build,
// so on a running deployment a single strategy saved with a delay stopped
// every strategy from being compiled. The Catalog was never written again,
// the last good one kept executing, and the deployment reported healthy.
func TestBuildCatalogSeparatesQueryGroupsByItemQueryDelay(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Planner: planner, Strategies: twoThresholdStrategiesWithDelay(t, 60)})
	if err != nil {
		t.Fatalf("Catalog build stopped on two strategies with different query delays: %v", err)
	}
	if len(catalog.QueryGroups) != 2 {
		t.Fatalf("groups=%d, want one per delay", len(catalog.QueryGroups))
	}
	delays := map[int64]bool{}
	for _, group := range catalog.QueryGroups {
		if len(group.Plans) != 1 {
			t.Fatalf("group %s holds %d Plans, want the one strategy with its delay", group.Identity, len(group.Plans))
		}
		if _, duplicate := delays[group.QueryPlan.QueryDelaySeconds]; duplicate {
			t.Fatalf("two groups carry query delay %d", group.QueryPlan.QueryDelaySeconds)
		}
		delays[group.QueryPlan.QueryDelaySeconds] = true
	}
	if _, ok := delays[0]; !ok {
		t.Fatalf("no group carries the undelayed strategy: %v", delays)
	}
	if _, ok := delays[60]; !ok {
		t.Fatalf("no group carries the delayed strategy: %v", delays)
	}
}

// The same two strategies without a delay difference stay one group, sharing
// the one query between both Plans. This is the arm that says the separation
// above comes from the delay and not from the fixture being rebuilt: same
// file, same compiler, same call, one field changed.
func TestBuildCatalogKeepsOneQueryGroupWithoutADelayDifference(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Planner: planner, Strategies: twoThresholdStrategiesWithDelay(t, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 2 {
		t.Fatalf("groups=%d, plans=%d, want one group carrying both Plans",
			len(catalog.QueryGroups), len(catalog.QueryGroups[0].Plans))
	}
	if catalog.QueryGroups[0].QueryPlan.QueryDelaySeconds != 0 {
		t.Fatalf("query delay=%d, want 0", catalog.QueryGroups[0].QueryPlan.QueryDelaySeconds)
	}
}
