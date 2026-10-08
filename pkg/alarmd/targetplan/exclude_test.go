// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

func TestExclusionsUseTheIncludedIdentityAndRejectInvalidShapes(t *testing.T) {
	for _, tt := range []struct {
		name, rule, model, exclude, modelMatch string
		options                                targetplan.Options
		keys                                   []string
		hosts                                  []string
		members                                []contract.TargetPlanMemberV1
		bad                                    bool
	}{
		{name: "absent remains compatible", rule: "host_id", model: "cw-Host"},
		{name: "explicit empty", rule: "host_id", model: "cw-Host", exclude: `[]`},
		{name: "host sorted unique", rule: "host_id", model: "cw-Host", exclude: `[{"bk_host_id":102},{"bk_host_id":"101"},{"bk_host_id":101}]`, keys: []string{"101", "102"}},
		{name: "host mapping", rule: "model_inst_id", model: "cw-Host", exclude: `[{"model_id":"cw-Host","model_inst_id":"101"}]`, members: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}}},
		{name: "host mapping sorted unique", rule: "model_inst_id", model: "cw-Host", exclude: `[{"model_id":"cw-Host","model_inst_id":"102"},{"model_id":"cw-Host","model_inst_id":"101"},{"model_id":"cw-Host","model_inst_id":"102"}]`, members: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}, {ModelID: "cw-Host", ModelInstID: "102"}}},
		{name: "ip_cloud host ids", rule: "ip_cloud", model: "cw-Host", exclude: `[{"bk_host_id":102},{"bk_host_id":"101"},{"bk_host_id":101}]`, options: targetplan.Options{TenantID: "tenant-a"}, hosts: []string{"101", "102"}},
		{name: "ip_cloud empty", rule: "ip_cloud", model: "cw-Host", exclude: `[]`, options: targetplan.Options{TenantID: "tenant-a"}},
		{name: "ip_cloud rejects address shape", rule: "ip_cloud", model: "cw-Host", exclude: `[{"bk_target_ip":"192.0.2.1","bk_target_cloud_id":0}]`, options: targetplan.Options{TenantID: "tenant-a"}, bad: true},
		{name: "model gate", rule: "model_inst_id", model: "cw-MySQL", modelMatch: `{"cw_object_model_id":"17"}`, exclude: `[{"model_id":"cw-MySQL","model_inst_id":"db-1"}]`, keys: []string{"db-1"}},
		{name: "model code", rule: "model_inst_id", model: "cw-MySQL", exclude: `[{"model_id":"cw-MySQL","model_inst_id":"db-1"}]`, options: targetplan.Options{ObjectIdentities: [][2]string{{"cw_object_model_code", "cw_object_model_inst_id"}}}, keys: []string{"cw-MySQL|db-1"}},
		{name: "null", rule: "host_id", model: "cw-Host", exclude: `null`, bad: true},
		{name: "object", rule: "host_id", model: "cw-Host", exclude: `{}`, bad: true},
		{name: "wrong identity", rule: "host_id", model: "cw-Host", exclude: `[{"model_id":"cw-Host","model_inst_id":"101"}]`, bad: true},
		{name: "foreign model", rule: "model_inst_id", model: "cw-Host", exclude: `[{"model_id":"cw-MySQL","model_inst_id":"101"}]`, bad: true},
		{name: "unknown field", rule: "host_id", model: "cw-Host", exclude: `[{"bk_host_id":101,"method":"eq"}]`, bad: true},
		{name: "k8s must stay empty", rule: "k8s_cluster", model: "cw-K8s_Cluster", exclude: `[{"model_id":"cw-K8s_Cluster","model_inst_id":"c","match":{"bcs_cluster_id":"c"}}]`, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			doc := map[string]any{"schema_version": 1, "model_id": tt.model, "target_rule": tt.rule, "failure_policy": "no_match", "static_targets": []any{}, "dynamic_groups": []any{map[string]any{"dynamic_group_id": "g"}}, "dynamic_topologies": []any{}}
			if tt.rule == "ip_cloud" {
				doc["bk_tenant_id"] = tt.options.TenantID
			}
			if tt.exclude != "" {
				doc["exclude"] = json.RawMessage(tt.exclude)
			}
			if tt.modelMatch != "" {
				doc["model_match"] = json.RawMessage(tt.modelMatch)
			}
			raw, _ := json.Marshal(doc)
			plan, err := targetplan.Decode(raw, tt.options)
			if tt.bad {
				if err == nil || !strings.HasPrefix(err.Path, "exclude") {
					t.Fatalf("want exclusion rejection, got plan %+v error %v", plan, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(plan.ExcludeKeys, tt.keys) || !reflect.DeepEqual(plan.ExcludeMembers, tt.members) || !reflect.DeepEqual(plan.ExcludeHosts, tt.hosts) {
				t.Fatalf("exclusions = %v %v %v", plan.ExcludeKeys, plan.ExcludeMembers, plan.ExcludeHosts)
			}
		})
	}
}
