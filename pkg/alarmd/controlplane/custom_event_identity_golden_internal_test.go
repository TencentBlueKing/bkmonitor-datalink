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
)

// customEventGoldenSource is a custom event strategy whose conditions are one
// AND group: the shape the filters were always placed right for.
func customEventGoldenSource() PrimaryQuerySource {
	return PrimaryQuerySource{Identity: SourceIdentity{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2"},
		StrategyID: "106", ItemID: "1", QueryMD5: "query-106", Expression: "a",
		QueryConfigs: []json.RawMessage{json.RawMessage(`{"data_source_label":"custom","data_type_label":"event",
			"result_table_id":"base_tenant_2_event","custom_event_name":"AgentLost","agg_method":"COUNT","agg_interval":60,
			"agg_dimension":["bk_target_ip","bk_target_cloud_id"],
			"agg_condition":[{"key":"bk_target_ip","method":"eq","value":["192.0.2.10"]},
				{"condition":"and","key":"bk_target_cloud_id","method":"eq","value":["0"]}]}`)}}
}

// customEventGolden is that strategy's query revision and Query Group
// identity as the build before the OR-group fix derives them: putting the
// filters into every group must leave a strategy of one group where it was.
var customEventGolden = [2]string{"80002d5f1e8c6c28ee1954202cb1c4b564cc583a078c6d8e65f371c63c0f90f4",
	"2ab7257c4ff294a1c6ee31175c5684e769156974ac141da15d3ac1c051dbd70a"}

func TestACustomEventStrategyOfOneGroupKeepsItsQueryIdentity(t *testing.T) {
	compiler, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := compiler.CompilePrimaryQuery(context.Background(), customEventGoldenSource())
	if err != nil {
		t.Fatal(err)
	}
	group, err := deriveQueryGroupIdentity(facts)
	if err != nil {
		t.Fatal(err)
	}
	if customEventGolden[0] == "" {
		t.Fatalf("not pinned; release value revision %s group %s", facts.QueryRevision, group)
	}
	if string(facts.QueryRevision) != customEventGolden[0] || string(group) != customEventGolden[1] {
		t.Fatalf("revision %s group %s, the build before the fix derives %s / %s", facts.QueryRevision, group,
			customEventGolden[0], customEventGolden[1])
	}
}
