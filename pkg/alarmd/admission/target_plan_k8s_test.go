// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// From the writer's document to the record: two node targets, (cluster-a,
// node-01) and (cluster-b, node-02), decoded into static keys and used as
// the members; a record on (cluster-a, node-02) shares a value with each
// target and is outside both. Keys and members are built by the production
// code on both sides, so a decoder and a filter that agreed on one IN per
// dimension would agree here too - and admit it.
func TestKubernetesTargetsMatchAsWholeCombinationsNotPerDimension(t *testing.T) {
	plan, err := targetplan.Decode(json.RawMessage(`{"schema_version":1,"model_id":"cw-K8s_Node","target_rule":"k8s_node","failure_policy":"no_match",
		"static_targets":[
			{"model_id":"cw-K8s_Node","model_inst_id":"cluster-a|node-01","match":{"bcs_cluster_id":"cluster-a","node":"node-01"}},
			{"model_id":"cw-K8s_Node","model_inst_id":"cluster-b|node-02","match":{"bcs_cluster_id":"cluster-b","node":"node-02"}}],
		"dynamic_groups":[],"dynamic_topologies":[]}`), targetplan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	members := memberSet{}
	for _, key := range plan.StaticKeys {
		members[key] = struct{}{}
	}
	context := PlanContext{TargetPlan: &TargetPlanContext{Identity: plan.Identity, Members: members}}
	for name, test := range map[string]struct {
		record map[string]string
		admit  bool
	}{
		"first target":                  {record: map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-01"}, admit: true},
		"second target":                 {record: map[string]string{"bcs_cluster_id": "cluster-b", "node": "node-02"}, admit: true},
		"cluster of one, node of other": {record: map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-02"}, admit: false},
		"node only":                     {record: map[string]string{"node": "node-01"}, admit: false},
	} {
		t.Run(name, func(t *testing.T) {
			if decision := (TargetPlanFilter{}).Admit(context, recordFacts(test.record)); decision.Admit != test.admit {
				t.Fatalf("Admit(%v) = %+v, want admit=%v", test.record, decision, test.admit)
			}
		})
	}
}
