// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The protocol's own samples, one per rule, frozen exactly. The
// model_inst_id samples carry the writer's model_match, because without it
// the plan's model code and the data's model id never meet (the case below
// this one).
func TestTheProtocolSamplesFreezeIntoTheirRuleKeys(t *testing.T) {
	defaultPairs := [][2]string{{"cw_object_model_id", "cw_object_model_inst_id"}}
	for name, test := range map[string]struct {
		document string
		options  targetplan.Options
		want     contract.TargetPlanV1
	}{
		"5.3 host system metric": {
			document: `{"schema_version":1,"model_id":"cw-Host","target_rule":"host_id","failure_policy":"no_match",
				"static_targets":[{"bk_host_id":102},{"bk_host_id":101},{"bk_host_id":"101"}],
				"dynamic_groups":[{"dynamic_group_id":"1001"},{"dynamic_group_id":1001}],
				"dynamic_topologies":[{"bk_biz_id":2,"bk_obj_id":"set","bk_inst_id":12},{"bk_biz_id":2,"bk_obj_id":"module","bk_inst_id":31}]}`,
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
				Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
				StaticKeys: []string{"101", "102"}, DynamicGroups: []string{"1001"},
				DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "module", InstanceID: "31"}, {BusinessID: "2", ObjectID: "set", InstanceID: "12"}}},
		},
		"5.4 host collected metric, as the protocol writes it": {
			// No model_match and the platform's default identity pair: the
			// members are read as hosts, (model, instance) as the writer
			// spelled them, and the host cache maps them to host ids per
			// Slot. The key is the record's host identity.
			document: `{"schema_version":1,"model_id":"cw-Host","target_rule":"model_inst_id","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-Host","model_inst_id":"102"},{"model_id":"cw-Host","model_inst_id":"101"},{"model_id":"cw-Host","model_inst_id":101}],
				"dynamic_groups":[{"dynamic_group_id":"1001"}],
				"dynamic_topologies":[{"bk_biz_id":2,"bk_obj_id":"set","bk_inst_id":12}]}`,
			options: targetplan.Options{ObjectIdentities: defaultPairs},
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity:          contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
				StaticKeys:        []string{},
				StaticMembers:     []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}, {ModelID: "cw-Host", ModelInstID: "102"}},
				DynamicGroups:     []string{"1001"},
				DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "set", InstanceID: "12"}}},
		},
		"5.4 host collected metric, model named by the writer": {
			document: `{"schema_version":1,"model_id":"cw-Host","target_rule":"model_inst_id","failure_policy":"no_match",
				"model_match":{"cw_object_model_id":"17"},
				"static_targets":[{"model_id":"cw-Host","model_inst_id":"101"},{"model_id":"cw-Host","model_inst_id":"102"}],
				"dynamic_groups":[{"dynamic_group_id":"1001"}],
				"dynamic_topologies":[{"bk_biz_id":2,"bk_obj_id":"set","bk_inst_id":12}]}`,
			options: targetplan.Options{ObjectIdentities: defaultPairs},
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
				Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_inst_id"}, ModelDimension: "cw_object_model_id", ModelValue: "17"},
				StaticKeys: []string{"101", "102"}, DynamicGroups: []string{"1001"},
				DynamicTopologies: []contract.TargetPlanTopologyV1{{BusinessID: "2", ObjectID: "set", InstanceID: "12"}}},
		},
		"5.5 non-host model, query names a code dimension": {
			document: `{"schema_version":1,"model_id":"cw-MySQL","target_rule":"model_inst_id","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-MySQL","model_inst_id":"mysql-prod-01"}],
				"dynamic_groups":[{"dynamic_group_id":"2001"}],"dynamic_topologies":[]}`,
			options: targetplan.Options{ObjectIdentities: [][2]string{{"cw_object_model_code", "cw_object_model_inst_id"}, {"cw_object_model_id", "cw_object_model_inst_id"}}},
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-MySQL", Rule: contract.TargetPlanRuleModelInstID,
				Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_code", "cw_object_model_inst_id"}},
				StaticKeys: []string{"cw-MySQL|mysql-prod-01"}, DynamicGroups: []string{"2001"}},
		},
		"5.6 k8s cluster": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Cluster","target_rule":"k8s_cluster","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Cluster","model_inst_id":"cluster-a","match":{"bcs_cluster_id":"cluster-a"}}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Cluster", Rule: contract.TargetPlanRuleK8sCluster,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}}, StaticKeys: []string{"cluster-a"}},
		},
		"5.6 k8s node": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Node","target_rule":"k8s_node","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Node","model_inst_id":"cluster-a|node-01","match":{"node":"node-01","bcs_cluster_id":"cluster-a"}}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Node", Rule: contract.TargetPlanRuleK8sNode,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "node"}}, StaticKeys: []string{"cluster-a|node-01"}},
		},
		"5.6 k8s workload": {
			document: `{"schema_version":1,"model_id":"cw-K8s_Workload","target_rule":"k8s_workload","failure_policy":"no_match",
				"static_targets":[{"model_id":"cw-K8s_Workload","model_inst_id":"cluster-a|prod|Deployment|web",
				"match":{"bcs_cluster_id":"cluster-a","namespace":"prod","workload_kind":"Deployment","workload_name":"web"}}],
				"dynamic_groups":[],"dynamic_topologies":[]}`,
			want: contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Workload", Rule: contract.TargetPlanRuleK8sWorkload,
				Identity:   contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"}},
				StaticKeys: []string{"cluster-a|prod|Deployment|web"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := targetplan.Decode(json.RawMessage(test.document), test.options)
			if err != nil {
				t.Fatalf("Decode() refused: %v", err)
			}
			if !reflect.DeepEqual(*plan, test.want) {
				t.Fatalf("Decode() = %+v\nwant %+v", *plan, test.want)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("the frozen form does not validate: %v", err)
			}
		})
	}
}

// Every field the protocol names is required and closed: a missing one, an
// extra one, a wrong type, an unknown rule or version, a dynamic reference
// on a static rule, a static target of another rule's shape - each is
// refused with the path of the field that decided it. Nothing here is read
// as the old target.
func TestEveryDeviationFromTheProtocolIsRefusedAtItsField(t *testing.T) {
	base := map[string]any{
		"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id", "failure_policy": "no_match",
		"static_targets": []any{map[string]any{"bk_host_id": 101}}, "dynamic_groups": []any{}, "dynamic_topologies": []any{},
	}
	with := func(edits ...func(map[string]any)) json.RawMessage {
		document := map[string]any{}
		for key, value := range base {
			document[key] = value
		}
		for _, edit := range edits {
			edit(document)
		}
		raw, err := json.Marshal(document)
		if err != nil {
			panic(err)
		}
		return raw
	}
	set := func(key string, value any) func(map[string]any) { return func(m map[string]any) { m[key] = value } }
	del := func(key string) func(map[string]any) { return func(m map[string]any) { delete(m, key) } }
	k8s := func(m map[string]any) {
		m["model_id"], m["target_rule"] = "cw-K8s_Node", "k8s_node"
		m["static_targets"] = []any{map[string]any{"model_id": "cw-K8s_Node", "model_inst_id": "cluster-a|node-01", "match": map[string]any{"bcs_cluster_id": "cluster-a", "node": "node-01"}}}
	}
	modelInst := func(m map[string]any) {
		m["model_id"], m["target_rule"] = "cw-MySQL", "model_inst_id"
		m["static_targets"] = []any{map[string]any{"model_id": "cw-MySQL", "model_inst_id": "db-1"}}
		m["model_match"] = map[string]any{"cw_object_model_id": "17"}
	}
	for name, test := range map[string]struct {
		document json.RawMessage
		reason   string
		path     string
	}{
		"null":                     {document: json.RawMessage(`null`), reason: targetplan.ReasonUnsupported, path: ""},
		"empty object":             {document: json.RawMessage(`{}`), reason: targetplan.ReasonUnsupported, path: "schema_version"},
		"array":                    {document: json.RawMessage(`[]`), reason: targetplan.ReasonUnsupported, path: ""},
		"unknown version":          {document: with(set("schema_version", 2)), reason: targetplan.ReasonUnsupported, path: "schema_version"},
		"version as text":          {document: with(set("schema_version", "1")), reason: targetplan.ReasonUnsupported, path: "schema_version"},
		"version as a float":       {document: with(set("schema_version", json.RawMessage(`1.0`))), reason: targetplan.ReasonUnsupported, path: "schema_version"},
		"host id as a float":       {document: with(set("static_targets", []any{map[string]any{"bk_host_id": json.RawMessage(`1.5`)}})), reason: targetplan.ReasonUnsupported, path: "static_targets[0].bk_host_id"},
		"unknown rule":             {document: with(set("target_rule", "service_instance")), reason: targetplan.ReasonUnsupported, path: "target_rule"},
		"other failure policy":     {document: with(set("failure_policy", "match_all")), reason: targetplan.ReasonUnsupported, path: "failure_policy"},
		"model missing":            {document: with(del("model_id")), reason: targetplan.ReasonUnsupported, path: "model_id"},
		"extra field":              {document: with(set("generation", 3)), reason: targetplan.ReasonUnsupported, path: "generation"},
		"static targets missing":   {document: with(del("static_targets")), reason: targetplan.ReasonUnsupported, path: "static_targets"},
		"static targets not array": {document: with(set("static_targets", map[string]any{})), reason: targetplan.ReasonUnsupported, path: "static_targets"},
		"host target extra key":    {document: with(set("static_targets", []any{map[string]any{"bk_host_id": 101, "ip": "192.0.2.1"}})), reason: targetplan.ReasonUnsupported, path: "static_targets[0].ip"},
		"host target zero":         {document: with(set("static_targets", []any{map[string]any{"bk_host_id": 0}})), reason: targetplan.ReasonUnsupported, path: "static_targets[0].bk_host_id"},
		"host target text":         {document: with(set("static_targets", []any{map[string]any{"bk_host_id": "host-a"}})), reason: targetplan.ReasonUnsupported, path: "static_targets[0].bk_host_id"},
		"host target of model shape": {document: with(set("static_targets", []any{map[string]any{"model_id": "cw-Host", "model_inst_id": "101"}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].model_id"},
		"group without id": {document: with(set("dynamic_groups", []any{map[string]any{"provider": "x"}})), reason: targetplan.ReasonUnsupported, path: "dynamic_groups[0].provider"},
		"group id empty":   {document: with(set("dynamic_groups", []any{map[string]any{"dynamic_group_id": ""}})), reason: targetplan.ReasonUnsupported, path: "dynamic_groups[0].dynamic_group_id"},
		"topology no business": {document: with(set("dynamic_topologies", []any{map[string]any{"bk_obj_id": "set", "bk_inst_id": 12}})),
			reason: targetplan.ReasonUnsupported, path: "dynamic_topologies[0].bk_biz_id"},
		"topology instance text": {document: with(set("dynamic_topologies", []any{map[string]any{"bk_biz_id": 2, "bk_obj_id": "set", "bk_inst_id": "twelve"}})),
			reason: targetplan.ReasonUnsupported, path: "dynamic_topologies[0].bk_inst_id"},
		"k8s with a group":    {document: with(k8s, set("dynamic_groups", []any{map[string]any{"dynamic_group_id": "1"}})), reason: targetplan.ReasonUnsupported, path: "dynamic_groups"},
		"k8s with a topology": {document: with(k8s, set("dynamic_topologies", []any{map[string]any{"bk_biz_id": 2, "bk_obj_id": "set", "bk_inst_id": 12}})), reason: targetplan.ReasonUnsupported, path: "dynamic_topologies"},
		"k8s match missing a dimension": {document: with(k8s, set("static_targets", []any{map[string]any{"model_id": "cw-K8s_Node", "model_inst_id": "cluster-a|node-01", "match": map[string]any{"bcs_cluster_id": "cluster-a"}}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].match.node"},
		"k8s match with an extra dimension": {document: with(k8s, set("static_targets", []any{map[string]any{"model_id": "cw-K8s_Node", "model_inst_id": "cluster-a|node-01", "match": map[string]any{"bcs_cluster_id": "cluster-a", "node": "node-01", "pod": "p"}}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].match.pod"},
		"k8s match value empty": {document: with(k8s, set("static_targets", []any{map[string]any{"model_id": "cw-K8s_Node", "model_inst_id": "cluster-a|node-01", "match": map[string]any{"bcs_cluster_id": "cluster-a", "node": ""}}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].match.node"},
		"k8s target of another model": {document: with(k8s, set("static_targets", []any{map[string]any{"model_id": "cw-K8s_Pod", "model_inst_id": "x", "match": map[string]any{"bcs_cluster_id": "cluster-a", "node": "node-01"}}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].model_id"},
		"k8s with a model match": {document: with(k8s, set("model_match", map[string]any{"bcs_cluster_id": "cluster-a"})), reason: targetplan.ReasonUnsupported, path: "model_match"},
		"model match with two entries": {document: with(modelInst, set("model_match", map[string]any{"cw_object_model_id": "17", "cw_object_model_code": "cw-MySQL"})),
			reason: targetplan.ReasonUnsupported, path: "model_match"},
		"model match on an unread dimension": {document: with(modelInst, set("model_match", map[string]any{"model": "17"})),
			reason: targetplan.ReasonUnsupported, path: "model_match.model"},
		"model target of another model": {document: with(modelInst, set("static_targets", []any{map[string]any{"model_id": "cw-Redis", "model_inst_id": "r1"}})),
			reason: targetplan.ReasonUnsupported, path: "static_targets[0].model_id"},
		"nothing named": {document: with(set("static_targets", []any{})), reason: targetplan.ReasonEmpty, path: ""},
		// A model_inst_id plan with neither model_match nor a code
		// dimension is not a deviation any more: it is the protocol's own
		// spelling of a host target, frozen as read by host identity and
		// decided against the host cache per Slot (see the positive samples
		// and the resolver's tests). It stays out of this table on purpose.
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := targetplan.Decode(test.document, targetplan.Options{ObjectIdentities: [][2]string{{"cw_object_model_id", "cw_object_model_inst_id"}}})
			if err == nil {
				t.Fatalf("Decode() accepted %+v", *plan)
			}
			if err.Reason != test.reason || err.Path != test.path {
				t.Fatalf("Decode() refused with %s at %q (%s), want %s at %q", err.Reason, err.Path, err.Detail, test.reason, test.path)
			}
		})
	}
}

// What the strategy cache writer emitted at its first review, run through
// this decoder. The file records the writer's output, not the protocol's
// intent; where the two disagree the verdict here is what alarmd will say
// about that strategy on the first screen, and the writer's defects that no
// verdict can see (an exclusion turned into an inclusion, an intersection
// turned into a union) are noted beside the cases that carry them.
func TestTheWritersObservedPlansGetTheVerdictsTheContractGives(t *testing.T) {
	raw, err := os.ReadFile("testdata/writer-observed-plans.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Case       string          `json:"case"`
			TargetPlan json.RawMessage `json:"target_plan"`
			Expect     struct {
				Accepted      bool     `json:"accepted"`
				Reason        string   `json:"reason"`
				Path          string   `json:"path"`
				Rule          string   `json:"rule"`
				HostIdentity  bool     `json:"host_identity"`
				StaticKeys    []string `json:"static_keys"`
				StaticMembers []string `json:"static_members"`
				Groups        []string `json:"groups"`
				Topologies    []string `json:"topologies"`
			} `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) != 19 {
		t.Fatalf("fixture holds %d cases, want the writer's 18 PLAN cases and the one synthetic case", len(file.Cases))
	}
	// The writer's queries carried the platform's default identity pair and
	// no model_match - the writer never sends one - so every model_inst_id
	// plan is frozen as read by host identity, and whether its members are
	// hosts is the host cache's answer per Slot, not the decoder's.
	options := targetplan.Options{ObjectIdentities: [][2]string{{"cw_object_model_id", "cw_object_model_inst_id"}}}
	accepted, refused := 0, 0
	for _, test := range file.Cases {
		t.Run(test.Case, func(t *testing.T) {
			plan, err := targetplan.Decode(test.TargetPlan, options)
			if !test.Expect.Accepted {
				if err == nil {
					t.Fatalf("accepted %+v, want %s at %q", *plan, test.Expect.Reason, test.Expect.Path)
				}
				if err.Reason != test.Expect.Reason || err.Path != test.Expect.Path {
					t.Fatalf("refused with %s at %q, want %s at %q", err.Reason, err.Path, test.Expect.Reason, test.Expect.Path)
				}
				refused++
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			accepted++
			topologies := make([]string, 0, len(plan.DynamicTopologies))
			for _, node := range plan.DynamicTopologies {
				topologies = append(topologies, node.Key())
			}
			members := make([]string, 0, len(plan.StaticMembers))
			for _, member := range plan.StaticMembers {
				members = append(members, member.ModelID+"|"+member.ModelInstID)
			}
			if string(plan.Rule) != test.Expect.Rule || plan.Identity.HostIdentity != test.Expect.HostIdentity ||
				strings.Join(plan.StaticKeys, ",") != strings.Join(test.Expect.StaticKeys, ",") || strings.Join(members, ",") != strings.Join(test.Expect.StaticMembers, ",") ||
				strings.Join(plan.DynamicGroups, ",") != strings.Join(test.Expect.Groups, ",") || strings.Join(topologies, ",") != strings.Join(test.Expect.Topologies, ",") {
				t.Fatalf("frozen %+v, want rule %s host identity %v static %v members %v groups %v topologies %v", *plan, test.Expect.Rule,
					test.Expect.HostIdentity, test.Expect.StaticKeys, test.Expect.StaticMembers, test.Expect.Groups, test.Expect.Topologies)
			}
		})
	}
	if accepted != 13 || refused != 6 {
		t.Fatalf("accepted %d refused %d, want 13 (12 of the writer's, the synthetic one) and 6: every topology without a business and every empty plan is refused; a model_inst_id plan without a model_match is read by host identity and decided per Slot", accepted, refused)
	}
}

// Ids are text on a key path: an integer literal, however long, is kept as
// written and never rounded through a float.
func TestLargeIntegerIdsAreKeptAsWritten(t *testing.T) {
	plan, err := targetplan.Decode(json.RawMessage(`{"schema_version":1,"model_id":"cw-Host","target_rule":"host_id","failure_policy":"no_match",
		"static_targets":[{"bk_host_id":9007199254740993},{"bk_host_id":"9007199254740995"}],
		"dynamic_groups":[{"dynamic_group_id":9007199254740997}],"dynamic_topologies":[]}`), targetplan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.StaticKeys, ",") != "9007199254740993,9007199254740995" || strings.Join(plan.DynamicGroups, ",") != "9007199254740997" {
		t.Fatalf("keys %v groups %v were not kept as written", plan.StaticKeys, plan.DynamicGroups)
	}
}
