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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// Each Plan a lookup answers names what it reads and what it groups by, so
// one pass over the strategies sorts them by source and grouping: the
// source the query carries, the dimensions every series is told apart by,
// and whether the query is PromQL. The dimensions are author-written names,
// bounded: sixteen of them with the count of all, each cut to 128 bytes.
func TestALookupNamesEachPlansSourceAndGrouping(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	wide := make([]any, 0, 20)
	for index := 0; index < 19; index++ {
		wide = append(wide, fmt.Sprintf("dimension_%02d", index))
	}
	long := strings.Repeat("d", 200)
	wide = append(wide, long)
	fixtures := map[string]json.RawMessage{
		"1001": editedDocument(t, documents[0], nil, func(item map[string]any) {
			item["query_configs"] = []any{map[string]any{"data_source_label": "custom", "data_type_label": "event",
				"result_table_id": "k8s_event", "custom_event_name": "PodError", "agg_method": "COUNT", "agg_interval": 60,
				"agg_dimension": []any{"pod", "namespace"}}}
		}),
		"1002": editedDocument(t, documents[1], nil, func(item map[string]any) {
			item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = []any{}
		}),
		"1003": editedDocument(t, documents[1], func(top map[string]any) { top["id"] = 1003 }, func(item map[string]any) {
			item["query_configs"] = []any{map[string]any{"data_source_label": "prometheus", "data_type_label": "time_series",
				"promql": "sum by (host) (up)", "agg_interval": 60}}
		}),
		"1004": editedDocument(t, documents[1], func(top map[string]any) { top["id"] = 1004 }, func(item map[string]any) {
			item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = wide
		}),
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002,1003,1004]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for id, document := range fixtures {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:lookup-source", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics,
		func(catalog controlplane.Catalog) (controlplane.Catalog, error) { return catalog, nil })
	if err != nil {
		t.Fatal(err)
	}
	if published, err := reconciler.Refresh(ctx, source, planner); err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("publish = (%#v, %v)", published, err)
	}
	plan := func(id string) controlplane.StrategyPlanRef {
		t.Helper()
		answer := reconciler.LookupStrategy(id)
		if len(answer.Plans) != 1 {
			t.Fatalf("strategy %s answered %+v, want its one Plan", id, answer)
		}
		return answer.Plans[0]
	}

	events := plan("1001")
	if !reflect.DeepEqual(events.SourceSemantics, []string{"custom/event"}) {
		t.Errorf("the event count names source %v, want custom/event", events.SourceSemantics)
	}
	if !reflect.DeepEqual(events.GroupBy, []string{"namespace", "pod"}) || events.GroupByTotal != 2 || events.PromQL {
		t.Errorf("the event count groups by %v (total %d, promql %t), want its two dimensions in order", events.GroupBy, events.GroupByTotal, events.PromQL)
	}
	flat := plan("1002")
	if !reflect.DeepEqual(flat.SourceSemantics, []string{"bk_monitor/time_series"}) || len(flat.GroupBy) != 0 || flat.GroupByTotal != 0 || flat.PromQL {
		t.Errorf("the ungrouped query reads %+v, want its source, no grouping and not PromQL", flat)
	}
	if promql := plan("1003"); !promql.PromQL {
		t.Errorf("the PromQL query reads %+v, want promql", promql)
	}
	bounded := plan("1004")
	// In sorted order the long name comes first, cut; the fifteen after it
	// are the first of the rest.
	if bounded.GroupByTotal != 20 || len(bounded.GroupBy) != 16 || bounded.GroupBy[1] != "dimension_00" || bounded.GroupBy[15] != "dimension_14" {
		t.Fatalf("twenty dimensions read %v of total %d, want the first sixteen in order of twenty", bounded.GroupBy, bounded.GroupByTotal)
	}
	for index, name := range bounded.GroupBy {
		if len(name) > 128+len("...") {
			t.Errorf("dimension %d is %d bytes, want at most 128 and the cut mark", index, len(name))
		}
		if strings.HasPrefix(name, "ddd") && name != long[:128]+"..." {
			t.Errorf("the long dimension reads %q, want its first 128 bytes and the cut mark", name)
		}
	}
}
