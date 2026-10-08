// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// Each Kubernetes rule takes an optional business on its static targets and
// freezes it by the target's key: the business a global business Plan files
// an event on that target under.
func TestAKubernetesStaticTargetKeepsTheBusinessOfItsCluster(t *testing.T) {
	for name, test := range map[string]struct {
		document string
		want     map[string]string
	}{
		"k8s cluster": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":11},
				{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-b","match":{"bcs_cluster_id":"cluster-b"},"bk_biz_id":"22"},
				{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-c","match":{"bcs_cluster_id":"cluster-c"}}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: map[string]string{"cluster-a": "11", "cluster-b": "22"},
		},
		"k8s node": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Node","target_rule":"k8s_node","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Node","model_inst_id":"cluster-a|node-01","match":{"node":"node-01","bcs_cluster_id":"cluster-a"},"bk_biz_id":11}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: map[string]string{"cluster-a|node-01": "11"},
		},
		"k8s workload": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Workload","target_rule":"k8s_workload","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Workload","model_inst_id":"cluster-a|prod|Deployment|web","bk_biz_id":33,
				"match":{"bcs_cluster_id":"cluster-a","namespace":"prod","workload_kind":"Deployment","workload_name":"web"}}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: map[string]string{"cluster-a|prod|Deployment|web": "33"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := targetplan.Decode(json.RawMessage(test.document), targetplan.Options{})
			if err != nil {
				t.Fatalf("Decode() refused: %v", err)
			}
			if !reflect.DeepEqual(plan.StaticBusinesses, test.want) {
				t.Fatalf("static businesses = %v, want %v", plan.StaticBusinesses, test.want)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("the frozen form does not validate: %v", err)
			}
		})
	}
}

// A plan whose static targets carry no business freezes no map at all, so
// its bytes - and every digest over them - are what they were before the
// field existed.
func TestAKubernetesPlanWithoutBusinessesFreezesTheSameBytes(t *testing.T) {
	plan, err := targetplan.Decode(json.RawMessage(`{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
		"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"}}],
		"dynamic_groups":[],"dynamic_topologies":[]}`), targetplan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, encodeErr := json.Marshal(plan)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	want := `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","identity":{"dimensions":["bcs_cluster_id"]},"static_keys":["cluster-a"]}`
	if string(encoded) != want {
		t.Fatalf("frozen bytes\n%s\nwant\n%s", encoded, want)
	}
}

// Two static targets with one key and two businesses give that key neither:
// the plan cannot say which is the target's, and a choice by document order
// would move the alert's business when the writer reorders the list. The
// same business twice, or a business beside a target that carries none, is
// that business.
func TestAKeyGivenTwoBusinessesKeepsNeither(t *testing.T) {
	plan, err := targetplan.Decode(json.RawMessage(`{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
		"static_targets":[
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":11},
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":12},
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-b","match":{"bcs_cluster_id":"cluster-b"},"bk_biz_id":21},
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-b","match":{"bcs_cluster_id":"cluster-b"},"bk_biz_id":"21"},
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-c","match":{"bcs_cluster_id":"cluster-c"}},
		{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-c","match":{"bcs_cluster_id":"cluster-c"},"bk_biz_id":31}],
		"dynamic_groups":[],"dynamic_topologies":[]}`), targetplan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"cluster-b": "21", "cluster-c": "31"}
	if !reflect.DeepEqual(plan.StaticBusinesses, want) {
		t.Fatalf("static businesses = %v, want %v", plan.StaticBusinesses, want)
	}
	if !reflect.DeepEqual(plan.StaticKeys, []string{"cluster-a", "cluster-b", "cluster-c"}) {
		t.Fatalf("static keys = %v, the conflict must not drop the target itself", plan.StaticKeys)
	}
}

// The business is a positive integer where it is given, and it is a field
// only of a Kubernetes static target: a host target's business is the
// host's, read from the host cache, and the closed key list still refuses
// it there.
func TestAStaticTargetBusinessIsRefusedWhereItCannotBeRead(t *testing.T) {
	for name, test := range map[string]struct {
		document string
		path     string
	}{
		"zero": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":0}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			path: "static_targets[0].bk_biz_id",
		},
		"negative": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":-3}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			path: "static_targets[0].bk_biz_id",
		},
		"not a number": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"},"bk_biz_id":"biz"}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			path: "static_targets[0].bk_biz_id",
		},
		"on a host target": {
			document: `{"schema_version":1,"model_id":"cw-Host","target_rule":"host_id","failure_policy":"no_match",
				"static_targets":[{"bk_host_id":101,"bk_biz_id":2}],"dynamic_groups":[],"dynamic_topologies":[]}`,
			path: "static_targets[0].bk_biz_id",
		},
		"on an object member": {
			document: `{"schema_version":1,"model_id":"cw-MySQL","target_rule":"model_inst_id","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-MySQL","model_inst_id":"mysql-01","bk_biz_id":2}],"dynamic_groups":[],"dynamic_topologies":[]}`,
			path: "static_targets[0].bk_biz_id",
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := targetplan.Decode(json.RawMessage(test.document), targetplan.Options{})
			if err == nil {
				t.Fatalf("Decode() = %+v, want a refusal", plan)
			}
			if err.Reason != targetplan.ReasonUnsupported || err.Path != test.path {
				t.Fatalf("refusal = %+v, want %s at %s", err, targetplan.ReasonUnsupported, test.path)
			}
		})
	}
}

// The frozen form holds a business only on a static key of a Kubernetes
// plan, and only as a positive decimal; anything else is a compiler defect.
func TestAFrozenStaticBusinessIsValidatedAgainstThePlan(t *testing.T) {
	k8s := func() *contract.TargetPlanV1 {
		return &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Cluster", Rule: contract.TargetPlanRuleK8sCluster,
			Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}}, StaticKeys: []string{"cluster-a"}}
	}
	valid := k8s()
	valid.StaticBusinesses = map[string]string{"cluster-a": "11"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a business on a static key refused: %v", err)
	}
	for name, businesses := range map[string]map[string]string{
		"a key that is not a static key": {"cluster-b": "11"},
		"zero":                           {"cluster-a": "0"},
		"not canonical":                  {"cluster-a": "011"},
		"negative":                       {"cluster-a": "-11"},
	} {
		plan := k8s()
		plan.StaticBusinesses = businesses
		if err := plan.Validate(); err == nil {
			t.Fatalf("%s: validated", name)
		}
	}
	host := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		StaticKeys: []string{"101"}, StaticBusinesses: map[string]string{"101": "2"}}
	if err := host.Validate(); err == nil {
		t.Fatal("a host plan carrying a static business validated")
	}
}
