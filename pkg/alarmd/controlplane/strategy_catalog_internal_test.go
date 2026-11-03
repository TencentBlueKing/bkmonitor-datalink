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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Reading the catalog page by page lists every accepted strategy exactly
// once, each with all of its Plans: a page ends between strategies, never
// inside one, so a strategy whose Plans sit in three Query Groups is not
// split across pages or repeated. A strategy id that two businesses both
// use is two strategies, each with only its own Plans.
func TestTheCatalogListsEveryStrategyOnceWithAllItsPlans(t *testing.T) {
	plan := func(business, strategy string) FrozenPlan {
		return FrozenPlan{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: business, StrategyID: strategy}}
	}
	groups := []QueryGroup{
		{Identity: "qg-a", Plans: []FrozenPlan{plan("2", "101"), plan("2", "103")}},
		{Identity: "qg-b", Plans: []FrozenPlan{plan("2", "102"), plan("2", "103")}},
		{Identity: "qg-c", Plans: []FrozenPlan{plan("2", "103"), plan("5", "104")}},
		{Identity: "qg-d", Plans: []FrozenPlan{plan("5", "105"), plan("5", "106")}},
		{Identity: "qg-e", Plans: []FrozenPlan{plan("5", "103")}},
	}
	index := buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: "rev-1", PublicationEpoch: 3}, groups, nil)

	var listed []StrategyCatalogEntry
	offset, pages := 0, 0
	for {
		page := index.catalogPage(offset, 2)
		pages++
		if page.Revision != "rev-1:3" || page.Total != 7 {
			t.Fatalf("page %d revision %q total %d, want rev-1:3 and 7", pages, page.Revision, page.Total)
		}
		if len(page.Strategies) > 2 {
			t.Fatalf("page %d holds %d strategies, limit 2", pages, len(page.Strategies))
		}
		listed = append(listed, page.Strategies...)
		offset += len(page.Strategies)
		if offset >= page.Total || len(page.Strategies) == 0 {
			break
		}
	}
	if pages != 4 {
		t.Fatalf("read %d pages, want 4", pages)
	}
	want := []string{
		"t/2/101 [qg-a]", "t/2/102 [qg-b]", "t/2/103 [qg-a qg-b qg-c]",
		"t/5/103 [qg-e]", "t/5/104 [qg-c]", "t/5/105 [qg-d]", "t/5/106 [qg-d]",
	}
	var got []string
	for _, entry := range listed {
		var queryGroups []string
		for _, p := range entry.Plans {
			queryGroups = append(queryGroups, string(p.QueryGroup))
		}
		got = append(got, fmt.Sprintf("%s/%s/%s %v", entry.Tenant, entry.Business, entry.StrategyID, queryGroups))
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("catalog\n got %v\nwant %v", got, want)
	}
	if page := index.catalogPage(7, 2); len(page.Strategies) != 0 || page.Total != 7 {
		t.Fatalf("a page past the end = %+v", page)
	}
	var none *strategyIndex
	if page := none.catalogPage(0, 2); page.Reason != "LEADER_CATALOG_NOT_READY" || page.Strategies == nil {
		t.Fatalf("no index = %+v, want the not-ready reason and an empty list", page)
	}
}
