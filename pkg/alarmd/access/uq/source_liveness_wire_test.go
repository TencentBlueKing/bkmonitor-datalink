// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// livenessFacts is the primary query facts the compiler builds for configs,
// in a business space or, global, across the tenant.
func livenessFacts(t *testing.T, global bool, expression string, configs ...string) execution.QueryPlanFacts {
	t.Helper()
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq", "Asia/Shanghai", controlplane.LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]json.RawMessage, 0, len(configs))
	for _, config := range configs {
		raw = append(raw, json.RawMessage(config))
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2", GlobalBusiness: global},
		StrategyID: "14", ItemID: "3", QueryMD5: "probe", Expression: expression, QueryConfigs: raw})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// judged is the period a round judged, and lastPoint the series' last real
// point, three periods before it: the probe counts from two periods after
// lastPoint - the first empty period's end - through judged, one period a
// bucket.
const judged, lastPoint = 1_700_124_000, 1_700_123_820

// Each source the probe supports, compiled from a strategy that carries its
// own conditions - a bk_biz_id among them - grouping and query string: the
// probe is the design's count (sum over count_over_time of one period, step
// one period, the judged period and the one before) on the same table, data
// source and time field, with none of the strategy's conditions, no
// dimensions, no offset, and no business condition added for any source.
// Logs count documents (_index, query string *); a time series
// counts its own field, under the data source its own query names (none for
// a monitor time series, as that query sends none). The expectations are the shapes design §3 names and
// §8 ran against the provider.
func TestTheProbeIsTheSourcesCountWithoutTheStrategysConditions(t *testing.T) {
	for _, test := range []struct {
		name, config string
		kind         sourceliveness.Kind
		wire         string
	}{
		{name: "log index set", kind: sourceliveness.KindLog,
			config: `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":"71","query_string":"error","agg_interval":60,"alias":"a","agg_dimension":["serverIp"],"agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]},{"key":"level","method":"eq","value":["error"],"condition":"and"}]}`,
			wire:   `{"query_list":[{"data_source":"bklog","table_id":"bklog_index_set_71","field_name":"_index","driver":"influxdb","time_field":"dtEventTimeStamp","is_regexp":false,"reference_name":"a","function":[{"method":"sum","position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"conditions":{},"query_string":"*"}],"metric_merge":"a","start_time":"1700123940","end_time":"1700124060","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":true}`},
		{name: "keyword log", kind: sourceliveness.KindLog,
			config: `{"data_source_label":"bk_monitor","data_type_label":"log","result_table_id":"2_bkmonitor_event_7","agg_method":"COUNT","agg_interval":60,"agg_dimension":["host"],"query_string":"panic","agg_condition":[{"key":"host","method":"eq","value":["192.0.2.1"]}]}`,
			wire:   `{"query_list":[{"data_source":"bkapm","table_id":"2_bkmonitor_event_7.__default__","field_name":"_index","driver":"influxdb","time_field":"time","is_regexp":false,"reference_name":"a","function":[{"method":"sum","position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"conditions":{},"query_string":"*"}],"metric_merge":"a","start_time":"1700123940","end_time":"1700124060","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":true}`},
		{name: "time series", kind: sourceliveness.KindTimeSeries,
			config: `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary","metric_field":"usage","agg_method":"AVG","agg_interval":60,"agg_dimension":["bk_target_ip"],"agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]},{"key":"bk_target_ip","method":"eq","value":["192.0.2.1"],"condition":"and"}],"alias":"a"}`,
			wire:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","driver":"influxdb","time_field":"time","is_regexp":false,"reference_name":"a","function":[{"method":"sum","position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"conditions":{},"query_string":""}],"metric_merge":"a","start_time":"1700123940","end_time":"1700124060","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":true}`},
	} {
		facts := livenessFacts(t, false, "a", test.config)
		probe, verdict, ok := sourceliveness.Derive(facts, lastPoint, judged, 180)
		if !ok || probe.Kind != test.kind {
			t.Fatalf("%s: probe %+v verdict %+v, want a %s probe", test.name, probe, verdict, test.kind)
		}
		if len(probe.Clause.Conditions.Fields) != 0 || len(probe.Clause.Dimensions) != 0 || probe.Clause.Offset != "" {
			t.Errorf("%s: the probe keeps the strategy's conditions, grouping or offset: %+v", test.name, probe.Clause)
		}
		path, wire, headers, err := buildProbeWire(probe)
		if err != nil {
			t.Fatal(err)
		}
		if path != "/query/ts" || string(wire) != test.wire {
			t.Errorf("%s: path %s\nwire %s\nwant %s", test.name, path, wire, test.wire)
		}
		if headers[headerSpace] != "bkcc__2" || headers[headerTenant] != "tenant" || headers[headerSkipSpace] != "" {
			t.Errorf("%s: headers %v, want the strategy's space", test.name, headers)
		}
		if strings.Contains(string(wire), "bk_biz_id") {
			t.Errorf("%s: a business condition in the probe: %s", test.name, wire)
		}
	}
}

// The strategy's own query carries its conditions - a bk_biz_id one among
// them - and the probe of the same strategy carries none: the business is
// the space's to scope, never a condition the probe adds or keeps. A global
// business Plan probes as its query reads: space skipped, still no business
// condition.
func TestTheProbeAddsNoBusinessConditionInEitherScope(t *testing.T) {
	config := `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":"71","query_string":"*","agg_interval":60,"alias":"a","agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]}]}`
	for _, global := range []bool{false, true} {
		facts := livenessFacts(t, global, "a", config)
		window := execution.QueryWindow{Start: judged - 60, End: judged + 60}
		spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window,
			ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
		if err != nil {
			t.Fatal(err)
		}
		_, own, err := buildWireRequest(spec)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(own), `"field_name":"bk_biz_id"`) {
			t.Fatalf("global %t: the strategy's own query lost its condition: %s", global, own)
		}
		probe, _, ok := sourceliveness.Derive(facts, lastPoint, judged, 180)
		if !ok {
			t.Fatalf("global %t: no probe", global)
		}
		_, wire, headers, err := buildProbeWire(probe)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(wire), "bk_biz_id") || !strings.Contains(string(wire), `"conditions":{}`) {
			t.Errorf("global %t: probe carries a condition: %s", global, wire)
		}
		wantSpace, wantSkip := "bkcc__2", ""
		if global {
			wantSpace, wantSkip = "", skipSpaceValue
		}
		if headers[headerSpace] != wantSpace || headers[headerSkipSpace] != wantSkip || !strings.Contains(string(wire), `"space_uid":"`+wantSpace+`"`) {
			t.Errorf("global %t: headers %v body %s, want the query's own scope", global, headers, wire)
		}
	}
}

// The sources this version cannot probe are refused by name, each with its
// own reason, from the facts the compiler builds for them.
func TestTheSourcesWithoutAProbeAreRefusedByName(t *testing.T) {
	for _, test := range []struct {
		name, expression string
		configs          []string
		reason           sourceliveness.Reason
	}{
		{name: "promql", expression: "a", reason: sourceliveness.ReasonPromQL,
			configs: []string{`{"data_source_label":"prometheus","data_type_label":"time_series","expression_mode":"promql","alias":"a","promql":"sum by (job) (up)","agg_interval":60}`}},
		{name: "bkdata", expression: "a + 1", reason: sourceliveness.ReasonBKData,
			configs: []string{`{"data_source_label":"bk_data","data_type_label":"time_series","result_table_id":"2_metric","metric_field":"value","alias":"a","agg_method":"AVG","agg_interval":60}`}},
		{name: "apm", expression: "a", reason: sourceliveness.ReasonAPM,
			configs: []string{`{"data_source_label":"custom","data_type_label":"time_series","result_table_id":"2_bkapm_metric_shop.__default__","metric_field":"bk_apm_count","alias":"a","agg_method":"SUM","agg_interval":60}`}},
		{name: "two tables", expression: "a + b", reason: sourceliveness.ReasonMultiTable,
			configs: []string{`{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary","metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60}`,
				`{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.mem","metric_field":"pct_used","alias":"b","agg_method":"AVG","agg_interval":60}`}},
		{name: "custom event", expression: "a", reason: sourceliveness.ReasonEventSparse,
			configs: []string{`{"data_source_label":"custom","data_type_label":"event","result_table_id":"k8s_event","custom_event_name":"OOMKilled","agg_interval":60,"alias":"a","agg_dimension":["target"],"agg_condition":[{"key":"bk_biz_id","method":"eq","value":["2"]}]}`}},
		{name: "data label", expression: "a", reason: sourceliveness.ReasonDataLabel,
			configs: []string{`{"data_source_label":"custom","data_type_label":"time_series","result_table_id":"2_bkmonitor_time_series_9.__default__","data_label":"shop_metrics","metric_field":"orders","alias":"a","agg_method":"SUM","agg_interval":60}`}},
	} {
		facts := livenessFacts(t, false, test.expression, test.configs...)
		_, verdict, ok := sourceliveness.Derive(facts, lastPoint, judged, 180)
		if ok || verdict.Result != sourceliveness.ResultUnsupported || verdict.Reason != test.reason {
			t.Errorf("%s: verdict %+v ok %t, want unsupported %s", test.name, verdict, ok, test.reason)
		}
	}
	// The same table twice, two fields: one source, probed.
	facts := livenessFacts(t, false, "a + b",
		`{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary","metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60}`,
		`{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu_summary","metric_field":"idle","alias":"b","agg_method":"AVG","agg_interval":60}`)
	if _, verdict, ok := sourceliveness.Derive(facts, lastPoint, judged, 180); !ok {
		t.Errorf("one table read twice: %+v, want a probe", verdict)
	}
}

// The probe's APM table is access's: a table it carries the application on
// is one the probe refuses.
func TestTheProbesAPMTableIsAccesss(t *testing.T) {
	if sourceliveness.APMTablePattern() != apmTable.String() {
		t.Fatalf("probe %s, access %s", sourceliveness.APMTablePattern(), apmTable.String())
	}
}
