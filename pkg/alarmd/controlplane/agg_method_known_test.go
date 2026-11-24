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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// An agg_method the strategy page offers compiles; one UQ has no aggregation
// for is refused by name at compile time instead of failing every round at
// UQ. distinct is a log method: a metric source naming it is refused.
func TestAnUnknownAggMethodIsRefusedAtCompileTime(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	metric := `{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"usage","alias":"a",` +
		`"agg_method":%q,"agg_interval":60,"agg_dimension":["host"],"result_table_id":"system.cpu","functions":[]}`
	logSearch := `{"data_source_label":"bk_log_search","data_type_label":"time_series","index_set_id":7,"metric_field":"bytes",` +
		`"alias":"a","agg_method":%q,"agg_interval":60,"agg_dimension":["host"],"query_string":"*"}`
	compile := func(config string) error {
		_, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
			Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
			StrategyID: "14", ItemID: "3", QueryMD5: "query-3", Expression: "a",
			QueryConfigs: []json.RawMessage{json.RawMessage(config)},
		})
		return err
	}
	for _, method := range []string{"AVG", "SUM", "MAX", "MIN", "COUNT", "CP95", "sum_without_time", "REAL_TIME", ""} {
		if err := compile(fmt.Sprintf(metric, method)); err != nil {
			t.Errorf("agg_method %q refused: %v", method, err)
		}
	}
	if err := compile(fmt.Sprintf(logSearch, "DISTINCT")); err != nil {
		t.Errorf("a log source's DISTINCT refused: %v", err)
	}
	for _, config := range []string{fmt.Sprintf(metric, "MEDIAN"), fmt.Sprintf(metric, "DISTINCT"), fmt.Sprintf(logSearch, "MEDIAN")} {
		var compileErr *controlplane.QueryPlanCompileError
		if err := compile(config); !errors.As(err, &compileErr) ||
			compileErr.Disposition != controlplane.DispositionConfigRejected || compileErr.Reason != "QUERY_AGG_METHOD_UNKNOWN" {
			t.Errorf("%s compiled to %v, want config rejected with QUERY_AGG_METHOD_UNKNOWN", config, err)
		}
	}
}

// Each fallback of the platform's metric source (normalize_metric_method,
// bkmonitor/data_source/unify_query/functions.py at c0e828fa8) compiles to the
// query a strategy written with the method it falls back to gets, and is
// counted under its own reason:
//   - "$" in the method (:165), sum_without_time for one ending _without_time,
//     sum otherwise (:166-168);
//   - an "undefined" prefix (:165), the same two (:166-168);
//   - a method ending _without_time that is not in AggMethods (:116-147),
//     sum (:169-171): distinct_without_time is one.
//
// A log source sends the method as written (data_source/__init__.py:1694,
// :1717, no normalize_metric_method), so a variable there stays refused, as
// does a method that is no fallback (MEDIAN, which Python sends as
// median_over_time and UQ cannot run).
func TestAnAggMethodFallbackCompilesAsThePlatformsAndIsCounted(t *testing.T) {
	buildConfig := func(config string) controlplane.Catalog {
		t.Helper()
		document := `{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","query_configs":[` + config + `],` +
			`"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}]}],` +
			`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":5},"recovery_config":{"check_window":3}}]}`
		planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq", "UTC", testLegacyQueryRuntimeFacts())
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(document),
				Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
			Planner: planner,
		})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	build := func(method, source, dataType string) controlplane.Catalog {
		t.Helper()
		return buildConfig(fmt.Sprintf(`{"data_source_label":%q,"data_type_label":%q,"metric_field":"usage","alias":"a","agg_method":%q,`+
			`"agg_interval":60,"agg_dimension":["host"],"result_table_id":"system.cpu","index_set_id":7,"query_string":"*","functions":[]}`, source, dataType, method))
	}
	reasons := []string{controlplane.ReasonAggMethodVariableDefaulted, controlplane.ReasonAggMethodUndefinedDefaulted,
		controlplane.ReasonAggMethodWithoutTimeDefaulted}
	counted := func(catalog controlplane.Catalog) map[string]int {
		composition := controlplane.ComposeCatalog(catalog)
		out := map[string]int{}
		for _, reason := range reasons {
			out[reason] = composition.Withheld[controlplane.WithheldKey{Disposition: controlplane.DispositionConfigNoted, Reason: reason}]
		}
		return out
	}
	for _, fallback := range []struct{ written, platform, reason string }{
		{"$method", "SUM", controlplane.ReasonAggMethodVariableDefaulted},
		{"${method}", "SUM", controlplane.ReasonAggMethodVariableDefaulted},
		{"${method}_without_time", "sum_without_time", controlplane.ReasonAggMethodVariableDefaulted},
		{"$method_WITHOUT_TIME", "sum_without_time", controlplane.ReasonAggMethodVariableDefaulted},
		{"undefined", "SUM", controlplane.ReasonAggMethodUndefinedDefaulted},
		{"undefined_without_time", "sum_without_time", controlplane.ReasonAggMethodUndefinedDefaulted},
		{"distinct_without_time", "SUM", controlplane.ReasonAggMethodWithoutTimeDefaulted},
		{"P95_WITHOUT_TIME", "SUM", controlplane.ReasonAggMethodWithoutTimeDefaulted},
	} {
		got, want := build(fallback.written, "bk_monitor", "time_series"), build(fallback.platform, "bk_monitor", "time_series")
		if len(got.QueryGroups) != 1 || len(want.QueryGroups) != 1 {
			t.Fatalf("%s: groups %d and %d, dispositions %+v", fallback.written, len(got.QueryGroups), len(want.QueryGroups), got.Dispositions)
		}
		gotQuery, _ := json.Marshal(got.QueryGroups[0].QueryPlan)
		wantQuery, _ := json.Marshal(want.QueryGroups[0].QueryPlan)
		if string(gotQuery) != string(wantQuery) {
			t.Errorf("%s compiled to\n%s\nwant what %s compiles to\n%s", fallback.written, gotQuery, fallback.platform, wantQuery)
		}
		for _, reason := range reasons {
			want := 0
			if reason == fallback.reason {
				want = 1
			}
			if count := counted(got)[reason]; count != want {
				t.Errorf("%s: %d counted under %s, want %d: %+v", fallback.written, count, reason, want, got.Dispositions)
			}
		}
		for reason, count := range counted(want) {
			if count != 0 {
				t.Errorf("%s written as is: %d counted under %s, want 0", fallback.platform, count, reason)
			}
		}
	}
	// A PromQL config compiles through the PromQL path, which never reads
	// agg_method: a leftover variable from the page's other mode runs no
	// fallback and is noted under none.
	for _, written := range []string{"$m", "undefined", "distinct_without_time"} {
		promql := buildConfig(fmt.Sprintf(`{"data_source_label":"prometheus","data_type_label":"time_series","promql":"avg_over_time(up[60s])",`+
			`"agg_method":%q,"agg_interval":60,"alias":"a"}`, written))
		if len(promql.QueryGroups) != 1 || promql.QueryGroups[0].QueryPlan.PromQL == nil {
			t.Fatalf("PromQL with agg_method %s did not compile as PromQL: %+v", written, promql.Dispositions)
		}
		for reason, count := range counted(promql) {
			if count != 0 {
				t.Errorf("PromQL with agg_method %s: %d counted under %s, want 0", written, count, reason)
			}
		}
	}
	for _, refused := range []struct{ method, source, dataType string }{
		{"MEDIAN", "bk_monitor", "time_series"}, {"$method", "bk_log_search", "time_series"},
		{"distinct_without_time", "bk_log_search", "time_series"},
	} {
		catalog := build(refused.method, refused.source, refused.dataType)
		reasons := []string{}
		for _, disposition := range catalog.Dispositions {
			reasons = append(reasons, disposition.Reason)
		}
		if len(catalog.QueryGroups) != 0 || !strings.Contains(strings.Join(reasons, ","), "QUERY_AGG_METHOD_UNKNOWN") {
			t.Errorf("%+v: groups %d reasons %v, want refused as QUERY_AGG_METHOD_UNKNOWN", refused, len(catalog.QueryGroups), reasons)
		}
	}
}
