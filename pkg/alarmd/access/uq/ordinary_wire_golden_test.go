// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// compiledSpec is a physical query compiled from one query config, as a
// strategy's would be.
func compiledSpec(t *testing.T, config string) execution.PhysicalQuerySpec {
	t.Helper()
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq", "Asia/Shanghai", controlplane.LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := planner.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{
		Identity:   controlplane.SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "14", ItemID: "3", QueryMD5: "golden", Expression: "a", QueryConfigs: []json.RawMessage{json.RawMessage(config)}})
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window,
		ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// An ordinary query is the same request, the same query revision and the
// same physical digest it was before the FTA fields left the query model:
// the values below were recorded from the build that still had them. A
// changed revision would re-key every Plan's frozen facts; a changed request
// would be a different query.
func TestOrdinaryQueriesAreUnchangedByteForByte(t *testing.T) {
	for _, test := range []struct {
		name, path, revision, digest, wire string
		spec                               func(*testing.T) execution.PhysicalQuerySpec
	}{
		{name: "valid", path: "/query/ts",
			revision: "467b5e00518b88e30c2e81e28326e0cd60a9bde3a4b9e26ecbff6be86b190eb0",
			digest:   "7089ce86ff435065c48ba119322f81a29f2b014b166f4df76b4b4d5416312085",
			wire:     `{"query_list":[{"data_source":"bkmonitor","table_id":"system.cpu","field_name":"usage","driver":"influxdb","time_field":"time","is_regexp":false,"reference_name":"a","function":[{"method":"abs","position":0}],"time_aggregation":{"function":"avg","window":"60s","position":0},"conditions":{"field_list":[{"field_name":"host","op":"eq","value":["127.0.0.1"],"is_prefix":true}]},"query_string":""}],"metric_merge":"a","start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"UTC","not_time_align":false}`,
			spec:     func(t *testing.T) execution.PhysicalQuerySpec { return validAttempt(t).Spec }},
		{name: "custom_event", path: "/query/ts",
			revision: "0b3f3e0b4c6d639132b031a4ba10659b2020ad98456916ad1e6d6ba2a44acc91",
			digest:   "926ebda52e1bd38b9320e11ce1160139b55031a0c4f98f87df58117961387e51",
			wire:     `{"query_list":[{"data_source":"bkapm","table_id":"k8s_event","field_name":"_index","driver":"influxdb","time_field":"time","is_regexp":false,"reference_name":"a","function":[{"method":"sum","dimensions":["target"],"position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"dimensions":["target"],"conditions":{"field_list":[{"field_name":"target","op":"eq","value":["x"]},{"field_name":"event_name","op":"eq","value":["OOMKilled"]},{"field_name":"dimensions.event_type","op":"ne","value":["recovery"]}],"condition_list":["and","and"]},"query_string":"*"}],"metric_merge":"a","start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":false}`,
			spec: func(t *testing.T) execution.PhysicalQuerySpec {
				return compiledSpec(t, `{"data_source_label":"custom","data_type_label":"event","result_table_id":"k8s_event","custom_event_name":"OOMKilled","agg_interval":60,"alias":"a","agg_dimension":["target"],"agg_condition":[{"key":"target","method":"eq","value":["x"]}]}`)
			}},
		{name: "log_search", path: "/query/ts",
			revision: "4ff39458629c312fffdedb6da4a2f135020da41a1aadf536ecee0063edbb732c",
			digest:   "11bacfe6a908c4b4ea413b5d5ca461fb9e67258141f96df0281a1df11b112bb5",
			wire:     `{"query_list":[{"data_source":"bklog","table_id":"bklog_index_set_71","field_name":"_index","driver":"influxdb","time_field":"dtEventTimeStamp","is_regexp":false,"reference_name":"a","function":[{"method":"sum","dimensions":["serverIp"],"position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"dimensions":["serverIp"],"conditions":{},"query_string":"*error*"}],"metric_merge":"a","start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":false}`,
			spec: func(t *testing.T) execution.PhysicalQuerySpec {
				return compiledSpec(t, `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":"71","query_string":"error","agg_interval":60,"alias":"a","agg_dimension":["serverIp"]}`)
			}},
	} {
		spec := test.spec(t)
		path, wire, err := buildWireRequest(spec)
		if err != nil {
			t.Fatal(err)
		}
		if path != test.path || string(spec.PlanFacts.QueryRevision) != test.revision || string(spec.Digest) != test.digest || string(wire) != test.wire {
			t.Errorf("%s changed:\npath %s revision %s digest %s\nwire %s", test.name, path, spec.PlanFacts.QueryRevision, spec.Digest, wire)
		}
	}
}
