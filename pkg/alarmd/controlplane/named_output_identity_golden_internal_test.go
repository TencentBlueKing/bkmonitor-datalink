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
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// identityGoldenSources are strategy items without query_output_config, one
// per compile path the named outputs touch: a single PromQL, a structured
// item of one query, and a structured item of two under an expression.
func identityGoldenSources() map[string]PrimaryQuerySource {
	identity := SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	return map[string]PrimaryQuerySource{
		"single promql": {
			Identity: identity, StrategyID: "101", ItemID: "1", QueryMD5: "query-101",
			QueryConfigs: []json.RawMessage{json.RawMessage(`{"data_source_label":"prometheus","data_type_label":"time_series",` +
				`"alias":"a","promql":"sum by (job) (rate(requests_total{status=\"error\"}[5m]))","agg_interval":60}`)},
		},
		"structured one query": {
			Identity: identity, StrategyID: "102", ItemID: "1", QueryMD5: "query-102", Expression: "a",
			QueryConfigs: []json.RawMessage{json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series",` +
				`"metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["host"],` +
				`"agg_condition":[],"result_table_id":"system.cpu_summary","functions":[]}`)},
		},
		"structured two queries": {
			Identity: identity, StrategyID: "103", ItemID: "1", QueryMD5: "query-103", Expression: "a / b * 100",
			QueryConfigs: []json.RawMessage{
				json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series",` +
					`"metric_field":"used","alias":"a","agg_method":"SUM","agg_interval":60,"agg_dimension":["host"],` +
					`"agg_condition":[],"result_table_id":"system.mem","functions":[]}`),
				json.RawMessage(`{"data_source_label":"bk_monitor","data_type_label":"time_series",` +
					`"metric_field":"total","alias":"b","agg_method":"SUM","agg_interval":60,"agg_dimension":["host"],` +
					`"agg_condition":[],"result_table_id":"system.mem","functions":[]}`),
			},
		},
	}
}

// identityGolden is each source's query revision and Query Group identity as
// the release before named outputs derives them. A strategy without
// query_output_config must keep both byte for byte: they key its Runtime
// State, its Progress and its published Plan, and moving them would retire
// every existing group and start the same queries under new names.
var identityGolden = map[string][2]string{
	"single promql": {"8970d31958645a872ced86573eab0c4f933abbad8ebcaf7a9544424fe1e5edb4",
		"c3977d9b41db8f095ecf37b9ab3c1fd5119e68af30ff1a8237155113e89ac335"},
	"structured one query": {"5a4842d1c20399920314599eb42da6c500df536e4e62fc8e8e3b26e848009309",
		"fe5725972e7ec7a4c826aeb0d6408a0e19564ce81b7de2ddbc7e9a4e5d713dcb"},
	"structured two queries": {"10afe57e7d109802abc738f1caeb7f1f8802f15f6cf53221bbe64060fa64b1d8",
		"7a0cb7cf13b44e567f2b627fef04e88e98996d4f0342235fa72c97d3bd230077"},
}

func TestAStrategyWithoutNamedOutputsKeepsItsQueryIdentity(t *testing.T) {
	compiler, err := NewLegacyPrimaryQueryCompiler("uq-primary-v1", "Asia/Shanghai", LegacyQueryRuntimeFacts{
		AccessBKData: new(bool), BKDataCMDBLevelTables: []string{},
		SystemDiskFilter:    LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660", "tmpfs", "udf"}},
		SystemNetworkFilter: LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range identityGoldenSources() {
		facts, err := compiler.CompilePrimaryQuery(context.Background(), source)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		group, err := deriveQueryGroupIdentity(facts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, pinned := identityGolden[name]
		if !pinned {
			t.Errorf("%s: not pinned; release value revision %s group %s", name, facts.QueryRevision, group)
			continue
		}
		if string(facts.QueryRevision) != want[0] || string(group) != want[1] {
			t.Errorf("%s: revision %s group %s, the release before named outputs derives revision %s group %s",
				name, facts.QueryRevision, group, want[0], want[1])
		}
	}
}

// A strategy without update_time takes its revision from a digest of the
// decoded strategy, so a field this build reads must not encode itself when
// the strategy lacks it. The revision is the one the release before named
// outputs derives for the same strategy.
func TestAStrategyWithoutUpdateTimeKeepsItsRevision(t *testing.T) {
	const strategy = `{"id":104,"bk_biz_id":2,"name":"memory usage","is_enabled":true,` +
		`"items":[{"id":1,"name":"memory","query_md5":"query-104","expression":"a","time_delay":0,` +
		`"query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"pct_used",` +
		`"alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["bk_target_ip"],"agg_condition":[],` +
		`"result_table_id":"system.mem","functions":[]}],` +
		`"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":90}]]}]}],` +
		`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":1}}]}`
	var source legacyStrategy
	if err := json.Unmarshal([]byte(strategy), &source); err != nil {
		t.Fatal(err)
	}
	revision, err := contract.DeriveCanonicalDigestV2("alarmd-legacy-strategy-revision-v1", source)
	if err != nil {
		t.Fatal(err)
	}
	if want := "47f7bf96206a922f2dc7f3494c4779846a04287f656401b78be5e1bb20570911"; revision != want {
		t.Fatalf("revision = %s, want %s: a field absent from the strategy moved its revision", revision, want)
	}
}
