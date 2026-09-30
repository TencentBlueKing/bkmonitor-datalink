// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The record key is read by the plan's identity and nothing else: the key
// dimensions in order, a model gate when the writer named one, and no key
// at all when any of them is missing - the Kubernetes rules are one key
// over the whole dimension set, never one IN per dimension.
func TestTheRecordKeyIsOneKeyOverTheWholeIdentity(t *testing.T) {
	record := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	node := TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "node"}}
	if key, ok := node.Key(record(map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-01"})); !ok || key != "cluster-a|node-01" {
		t.Fatalf("node key = %q %v", key, ok)
	}
	if key, ok := node.Key(record(map[string]string{"bcs_cluster_id": "cluster-a"})); ok {
		t.Fatalf("a record missing the node dimension built key %q; it must have none", key)
	}
	if key, ok := node.Key(record(map[string]string{"bcs_cluster_id": "cluster-a", "node": ""})); ok {
		t.Fatalf("an empty node dimension built key %q", key)
	}
	// Two targets (cluster-a,node-01) and (cluster-b,node-02): a record on
	// (cluster-a,node-02) shares a value with each and matches neither.
	members := map[string]struct{}{"cluster-a|node-01": {}, "cluster-b|node-02": {}}
	key, _ := node.Key(record(map[string]string{"bcs_cluster_id": "cluster-a", "node": "node-02"}))
	if _, found := members[key]; found {
		t.Fatalf("record key %q matched a target it shares only one dimension with", key)
	}

	gated := TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_inst_id"}, ModelDimension: "cw_object_model_id", ModelValue: "17"}
	if key, ok := gated.Key(record(map[string]string{"cw_object_model_id": "17", "cw_object_model_inst_id": "101"})); !ok || key != "101" {
		t.Fatalf("gated key = %q %v", key, ok)
	}
	if key, ok := gated.Key(record(map[string]string{"cw_object_model_id": "18", "cw_object_model_inst_id": "101"})); ok {
		t.Fatalf("a record of another model built key %q through the gate", key)
	}
	if key, ok := gated.Key(record(map[string]string{"cw_object_model_inst_id": "101"})); ok {
		t.Fatalf("a record without the model dimension built key %q through the gate", key)
	}
	coded := TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_code", "cw_object_model_inst_id"}}
	if key, ok := coded.Key(record(map[string]string{"cw_object_model_code": "cw-MySQL", "cw_object_model_inst_id": "db-1"})); !ok || key != "cw-MySQL|db-1" {
		t.Fatalf("coded key = %q %v", key, ok)
	}
}

// A member key splits back into the group the data reports under, by the
// identity's dimension names, with the model gate's dimension and value
// added; the roster dimensions are exactly that set. The last dimension
// takes whatever remains, so a separator inside it does not shift the rest.
func TestAMemberKeySplitsBackIntoTheGroupTheDataReportsUnder(t *testing.T) {
	workload := TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"}}
	group, ok := workload.Group("cluster-a|prod|Deployment|web|v2")
	if !ok || !reflect.DeepEqual(group, map[string]string{"bcs_cluster_id": "cluster-a", "namespace": "prod", "workload_kind": "Deployment", "workload_name": "web|v2"}) {
		t.Fatalf("group = %v %v", group, ok)
	}
	if _, ok := workload.Group("cluster-a|prod"); ok {
		t.Fatal("a key with fewer parts than dimensions split into a group")
	}
	if got := strings.Join(workload.RosterDimensions(), ","); got != "bcs_cluster_id,namespace,workload_kind,workload_name" {
		t.Fatalf("roster dimensions = %s", got)
	}
	gated := TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_inst_id"}, ModelDimension: "cw_object_model_id", ModelValue: "17"}
	group, ok = gated.Group("101")
	if !ok || !reflect.DeepEqual(group, map[string]string{"cw_object_model_id": "17", "cw_object_model_inst_id": "101"}) {
		t.Fatalf("gated group = %v %v", group, ok)
	}
	if got := strings.Join(gated.RosterDimensions(), ","); got != "cw_object_model_id,cw_object_model_inst_id" {
		t.Fatalf("gated roster dimensions = %s", got)
	}
	host := TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}}
	if group, ok := host.Group("101"); !ok || !reflect.DeepEqual(group, map[string]string{"bk_host_id": "101"}) {
		t.Fatalf("host group = %v %v", group, ok)
	}
}

// The frozen form validates its own shape: the rule table, canonical
// lists, no dynamic reference on a static rule, a model gate only on
// model_inst_id, and nothing named refused. Everything here is a compiler
// defect if it ever reaches a Plan; the plan compiler refuses such a Plan.
func TestTheFrozenTargetPlanRefusesItsOwnDefects(t *testing.T) {
	valid := func() TargetPlanV1 {
		return TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: TargetPlanRuleHostID,
			Identity: TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101", "102"},
			DynamicGroups: []string{"1001"}, DynamicTopologies: []TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "set", InstanceID: "12"}}}
	}
	if err := (&TargetPlanV1{}).Validate(); err == nil {
		t.Fatal("the zero plan validated")
	}
	plan := valid()
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan refused: %v", err)
	}
	for name, edit := range map[string]func(*TargetPlanV1){
		"unknown rule":                func(p *TargetPlanV1) { p.Rule = "service_instance" },
		"rule dimensions changed":     func(p *TargetPlanV1) { p.Identity.Dimensions = []string{"ip"} },
		"unsorted static keys":        func(p *TargetPlanV1) { p.StaticKeys = []string{"102", "101"} },
		"duplicate groups":            func(p *TargetPlanV1) { p.DynamicGroups = []string{"1001", "1001"} },
		"nil static keys":             func(p *TargetPlanV1) { p.StaticKeys = nil },
		"model gate on host rule":     func(p *TargetPlanV1) { p.Identity.ModelDimension, p.Identity.ModelValue = "cw_object_model_id", "17" },
		"half a model gate":           func(p *TargetPlanV1) { p.Identity.ModelDimension = "cw_object_model_id" },
		"topology without a business": func(p *TargetPlanV1) { p.DynamicTopologies[0].BusinessID = "" },
		"unsorted topologies": func(p *TargetPlanV1) {
			p.DynamicTopologies = append(p.DynamicTopologies, TargetPlanTopologyV1{BusinessID: "1", ObjectID: "set", InstanceID: "1"})
		},
		"static rule with a group": func(p *TargetPlanV1) {
			p.Rule, p.Identity.Dimensions = TargetPlanRuleK8sCluster, []string{"bcs_cluster_id"}
			p.DynamicTopologies = nil
		},
		"nothing named": func(p *TargetPlanV1) { p.StaticKeys, p.DynamicGroups, p.DynamicTopologies = []string{}, nil, nil },
		"model gate dimension is a key": func(p *TargetPlanV1) {
			p.Rule = TargetPlanRuleModelInstID
			p.Identity = TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_id"}, ModelDimension: "cw_object_model_id", ModelValue: "17"}
		},
		// The host_id rule has one reading, the record's host identity: a
		// plan of it read by the dimension alone would be told apart by
		// nothing on the page and would drop every record named by address.
		"host rule read by dimension only": func(p *TargetPlanV1) { p.Identity.HostIdentity = false },
		"host identity on a k8s rule": func(p *TargetPlanV1) {
			p.Rule, p.Identity = TargetPlanRuleK8sCluster, TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}, HostIdentity: true}
			p.DynamicGroups, p.DynamicTopologies = nil, nil
		},
		"host identity reading another dimension": func(p *TargetPlanV1) { p.Identity.Dimensions = []string{"host"} },
		"static members on the host rule": func(p *TargetPlanV1) {
			p.StaticMembers = []TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}}
		},
		"static members beside static keys": func(p *TargetPlanV1) {
			p.Rule = TargetPlanRuleModelInstID
			p.StaticMembers = []TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}}
		},
		"static member of another model": func(p *TargetPlanV1) {
			p.Rule, p.StaticKeys = TargetPlanRuleModelInstID, []string{}
			p.StaticMembers = []TargetPlanMemberV1{{ModelID: "cw-MySQL", ModelInstID: "101"}}
		},
		"unsorted static members": func(p *TargetPlanV1) {
			p.Rule, p.StaticKeys = TargetPlanRuleModelInstID, []string{}
			p.StaticMembers = []TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "102"}, {ModelID: "cw-Host", ModelInstID: "101"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan := valid()
			edit(&plan)
			if err := plan.Validate(); err == nil {
				t.Fatalf("validated %+v", plan)
			}
		})
	}
	// A nil plan validates: a Plan without a target plan is a Plan without
	// one, and the caller checks that with the scope.
	var absent *TargetPlanV1
	if err := absent.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A Plan without a target plan encodes exactly as before the field existed,
// so every digest taken over a Plan before this change is unchanged; a Plan
// with one round-trips it.
func TestPlansWithoutATargetPlanEncodeAsBefore(t *testing.T) {
	plan := EvaluationPlanV2{PlanID: "1", StrategyIR: StrategyIRV2{RequiredFeatures: []string{}}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "target_plan") {
		t.Fatalf("a Plan without a target plan names the field: %s", encoded)
	}
	plan.TargetPlan = &TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: TargetPlanRuleHostID,
		Identity: TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}}, StaticKeys: []string{"101"}}
	encoded, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EvaluationPlanV2
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.TargetPlan, plan.TargetPlan) {
		t.Fatalf("round trip = %+v, want %+v", decoded.TargetPlan, plan.TargetPlan)
	}
}
