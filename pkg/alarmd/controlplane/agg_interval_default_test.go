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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A strategy's period is the one Python's Strategy.get_interval computes
// (alarm_backends/core/control/strategy.py):
//
//	min_interval = None
//	for query_config in self.config["items"][0]["query_configs"]:
//	    if "agg_interval" not in query_config:
//	        continue
//	    if min_interval is None:
//	        min_interval = query_config["agg_interval"]
//	        continue
//	    if query_config["agg_interval"] < min_interval:
//	        min_interval = query_config["agg_interval"]
//	return min_interval or CONST_MINUTES
//
// A config without the key sits out; a 0 takes part and makes the least 0,
// which "or CONST_MINUTES" turns into 60; a null first stays None and the
// next value replaces it. Where the result is Python's 60 rather than a
// written interval, the Plan says so. A negative or non-integer value is
// refused by name.
func TestAStrategysPeriodIsTheOnePythonComputes(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		configs   []string
		interval  int64
		defaulted bool
	}{
		{name: "none carries one", configs: []string{`{}`, `{}`}, interval: 60, defaulted: true},
		{name: "the least of those that carry one", configs: []string{`{}`, `{"agg_interval":300}`, `{"agg_interval":120}`}, interval: 120},
		{name: "a 0 among them", configs: []string{`{"agg_interval":120}`, `{"agg_interval":0}`}, interval: 60, defaulted: true},
		{name: "a 0 first", configs: []string{`{"agg_interval":0}`, `{"agg_interval":120}`}, interval: 60, defaulted: true},
		{name: "a null first", configs: []string{`{"agg_interval":null}`, `{"agg_interval":120}`}, interval: 120},
		{name: "one written", configs: []string{`{"agg_interval":300}`}, interval: 300},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := buildIntervalCatalog(t, testCase.configs)
			if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
				t.Fatalf("strategy did not become a Plan: %#v", catalog.Dispositions)
			}
			if got := catalog.QueryGroups[0].Plans[0].ScheduleSpec.EvaluationIntervalSeconds; got != testCase.interval {
				t.Fatalf("period = %d, want %d", got, testCase.interval)
			}
			defaulted := false
			for _, disposition := range catalog.Dispositions {
				if disposition.Reason == controlplane.ReasonAggIntervalDefaulted {
					defaulted = disposition.Disposition == controlplane.DispositionConfigNormalized && disposition.Scope == "PLAN"
				}
			}
			if defaulted != testCase.defaulted {
				t.Fatalf("defaulted = %t, want %t: %#v", defaulted, testCase.defaulted, catalog.Dispositions)
			}
		})
	}
	// Refused as a rejected configuration, which keeps the last good Plan.
	// Left to the schedule, a negative period reads as an unsupported
	// evaluation step instead, which does not.
	for _, value := range []string{`-60`, `60.5`, `"60"`} {
		catalog := buildIntervalCatalog(t, []string{`{"agg_interval":` + value + `}`})
		if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 ||
			catalog.Dispositions[0].Disposition != controlplane.DispositionConfigRejected || catalog.Dispositions[0].Reason != "PLAN_INVALID" {
			t.Fatalf("agg_interval %s: groups %d, dispositions %+v, want refused as PLAN_INVALID", value, len(catalog.QueryGroups), catalog.Dispositions)
		}
	}
}

func buildIntervalCatalog(t *testing.T, configs []string) controlplane.Catalog {
	t.Helper()
	document := fmt.Sprintf(`{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a",`+
		`"query_configs":[%s],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":1}]]}]}],`+
		`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":5},"recovery_config":{"check_window":3}}]}`,
		strings.Join(configs, ","))
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: json.RawMessage(document),
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// A query config without an interval queries at a minute: Python's data
// sources read it as query_config.get("agg_interval", 60)
// (bkmonitor/data_source/data_source/__init__.py). One that carries 0 keeps
// Python's hour - "window": f"{self.interval}s" if self.interval else "1h".
func TestAQueryConfigWithoutAnIntervalQueriesAtAMinute(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "Asia/Shanghai", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, interval string
		window         string
		step           int64
	}{
		{name: "none", interval: ``, window: "60s", step: 60_000},
		{name: "zero", interval: `"agg_interval":0,`, window: "3600s", step: 60_000},
		{name: "written", interval: `"agg_interval":300,`, window: "300s", step: 300_000},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			query := json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series",` +
				`"metric_field":"CPUUsage","alias":"A","agg_method":"AVG",` + testCase.interval +
				`"agg_dimension":["host"],"agg_condition":[],"result_table_id":"System.CPU","functions":[]}`)
			facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
				Identity:   controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
				StrategyID: "12", ItemID: "1", QueryMD5: "query-1", Expression: "A", QueryConfigs: []json.RawMessage{query},
			})
			if err != nil {
				t.Fatal(err)
			}
			if window := facts.QueryList[0].TimeAggregation.Window; window != testCase.window || facts.StepMillis != testCase.step {
				t.Fatalf("window %s step %d, want %s and %d", window, facts.StepMillis, testCase.window, testCase.step)
			}
		})
	}
}
