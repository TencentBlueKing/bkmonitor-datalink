// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
)

func TestAllInOneTargetCombinationE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.seedTargetFixtures()
			descriptor := h.seedTargetCombinations()
			h.checkTargetCombinations(descriptor)
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

const mixedTargetTenant = "target-combined"

const mixedTargetCondition = `[{"field":"zone","operator":"equal","value":"mixed"}]`

func mixedTargetRef(id string) onemodel.InstanceRef {
	return onemodel.InstanceRef{ModelID: targetModel, InstanceID: id, EntityUID: targetModel + "|" + id}
}

func (h *policyHarness) seedTargetCombinations() onemodel.TargetDescriptor {
	h.t.Helper()
	docs, nodes, members := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
	for _, tenant := range []string{mixedTargetTenant, "target-combined-foreign", "target-combined-empty"} {
		if _, err := h.metadata.ExecContext(h.ctx, "INSERT INTO metadata_space VALUES (?, 'bkcc', '2', false), (?, 'bkcc', '3', false), (?, 'bkcc', '99', true)", tenant, tenant, tenant); err != nil {
			h.t.Fatal(err)
		}
		if _, err := h.metadata.ExecContext(h.ctx, `INSERT INTO object_model_v2 VALUES (?, 'cw-Host', 'cmdb', 'host', '{"config":[{"bk_property_id":"rank","bk_property_type":"int"},{"bk_property_id":"zone","bk_property_type":"singlechar"}]}')`, tenant); err != nil {
			h.t.Fatal(err)
		}
		if tenant == "target-combined-empty" {
			continue
		}
		for _, group := range []struct {
			id    int
			space string
		}{{7, "bkcc__2"}, {8, "bkcc__99"}, {9, "bkcc__2"}} {
			condition := mixedTargetCondition
			if group.id == 9 {
				condition = `[{"field":"rank","operator":"less","value":-100}]`
			}
			if _, err := h.metadata.ExecContext(h.ctx, "INSERT INTO dynamic_group_v2 VALUES (?, ?, 'cw-Host', ?, ?)", tenant, group.id, group.space, condition); err != nil {
				h.t.Fatal(err)
			}
		}
		for _, biz := range []int{2, 3} {
			id := fmt.Sprint(biz)
			docs[tenant+"-biz-"+id] = map[string]any{"bk_tenant_id": tenant, "model_id": "cw-biz", "model_inst_id": id, "entity_uid": "cw-biz|" + id, "attributes": map[string]any{}}
			nodes[tenant+"-node-"+id] = map[string]any{"bk_tenant_id": tenant, "bk_biz_id": biz, "unique_id": "mixed-module", "model_id": "cw-Module", "model_inst_id": id, "entity_uid": "cw-Module|" + id}
		}
		for _, v := range []struct {
			id   string
			biz  int
			zone string
		}{{"shared", 2, "mixed"}, {"static", 3, "static"}, {"topology", 2, "topology"}, {"dynamic", 2, "mixed"}, {"dynamic-other-business", 3, "mixed"}, {"unauthorized", 4, "mixed"}} {
			docs[tenant+"-"+v.id] = targetInstance(tenant, v.id, v.biz, 1, v.zone)
		}
		if tenant != "target-combined" {
			docs[tenant+"-foreign-only"] = targetInstance(tenant, "foreign-only", 2, 1, "mixed")
		}
		for _, v := range []struct {
			id  string
			biz int
		}{{"shared", 2}, {"topology", 2}, {"dynamic-other-business", 3}} {
			key := fmt.Sprintf("%s-member-%s", tenant, v.id)
			member := targetMembership(tenant, v.biz, key, v.id)
			member["topology_ancestor_unique_ids"] = []string{"mixed-module"}
			members[key] = member
		}
	}
	h.targetDocuments(h.names.IndexPrefix+"-one-instances", docs)
	h.targetDocuments(h.names.IndexPrefix+"-om-cmdb_biz_topo_node", nodes)
	h.targetDocuments(h.names.IndexPrefix+"-om-cmdb_biz_topo_host_membership", members)
	biz := int64(2)
	return onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: targetModel, Selectors: []onemodel.TargetSelector{
		{Type: "instances", Instances: []onemodel.InstanceRef{mixedTargetRef("shared"), mixedTargetRef("static"), mixedTargetRef("shared")}},
		{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "mixed-module", BizID: &biz},
		{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "7"},
	}}
}

// sendMixedTarget 仅改合成输入的真实业务字段，随后仍走共享 Kafka、Enrich 和持久裁决检查。
func (h *policyHarness) sendMixedTarget(id, fp, instance string, biz int) storedEventView {
	h.t.Helper()
	record := h.rawRecord(mixedTargetTenant, "policy-b", id, fp, "target", instance, "warning", "triggered", map[string]any{"model_id": targetModel, "model_inst_id": instance})
	var body map[string]json.RawMessage
	if err := json.Unmarshal(record.Value, &body); err != nil {
		h.t.Fatal(err)
	}
	data, err := json.Marshal(map[string]int{"bk_biz_id": biz})
	if err != nil {
		h.t.Fatal(err)
	}
	body["dimensions"] = data
	record.Value, err = json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.sendRecord(mixedTargetTenant, id, "warning", record)
}

func (h *policyHarness) checkTargetCombinations(descriptor onemodel.TargetDescriptor) {
	h.t.Helper()
	runtime, err := policyruntime.Open(h.resources, config.BluekingConfig{})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	const tenant = mixedTargetTenant
	resolve := func(d onemodel.TargetDescriptor, space string) (onemodel.TargetResult, error) {
		return runtime.Targets.Resolve(h.ctx, tenant, space, d)
	}
	want := []onemodel.InstanceRef{mixedTargetRef("dynamic"), mixedTargetRef("shared"), mixedTargetRef("static"), mixedTargetRef("topology")}
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		d := descriptor
		d.Selectors = []onemodel.TargetSelector{descriptor.Selectors[order[0]], descriptor.Selectors[order[1]], descriptor.Selectors[order[2]]}
		result, err := resolve(d, "bkcc__99")
		if err != nil || !reflect.DeepEqual(result.Instances, want) || !reflect.DeepEqual(result.Scope.BusinessIDs, []int64{2, 3}) {
			h.t.Fatalf("mixed union depends on order: order=%v result=%+v err=%v", order, result, err)
		}
		for i, s := range result.Selectors {
			if s.Index != i || s.Type != d.Selectors[i].Type || s.Matched != 2 {
				h.t.Fatal("selector counts lost overlap", result.Selectors)
			}
		}
	}
	if result, err := resolve(descriptor, "bkcc__2"); !errors.Is(err, onemodel.ErrTargetUnavailable) || len(result.Instances) != 0 {
		h.t.Fatal("static instance outside concrete scope became partial success", err)
	}
	for _, group := range []struct {
		id    string
		count int
	}{{"8", 3}, {"9", 0}} {
		d := descriptor
		d.Selectors = []onemodel.TargetSelector{{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: group.id}}
		result, err := resolve(d, "bkcc__99")
		if err != nil || len(result.Instances) != group.count {
			h.t.Fatalf("global/empty dynamic group %s: %+v %v", group.id, result, err)
		}
	}
	unbound := descriptor
	unbound.Selectors = []onemodel.TargetSelector{descriptor.Selectors[1]}
	unbound.Selectors[0].BizID = nil
	if result, err := resolve(unbound, "bkcc__99"); err != nil || len(result.Instances) != 3 {
		h.t.Fatalf("unbound topology did not join two authorized businesses: %+v %v", result, err)
	}
	if result, err := runtime.Targets.Resolve(h.ctx, "target-combined-empty", "bkcc__99", descriptor); err != nil || len(result.Instances) != 0 || len(result.Scope.BusinessIDs) != 0 {
		h.t.Fatalf("empty tenant scope borrowed another tenant: %+v %v", result, err)
	}
	h.t.Log("six selector orders, overlap, global scope, fixed business groups, topology branches and empty tenant scope verified")
	h.publish(tenant, policy.Shield, map[string]any{"space_code": "bkcc__99", "policy": condition("target"), "shield_type": "time_shield", "model_id": targetModel, "target_descriptor": descriptor}, time.Now().Add(20*time.Minute))
	alerts := map[string]string{}
	for _, v := range []struct {
		id  string
		biz int
	}{{"shared", 2}, {"static", 3}, {"topology", 2}, {"dynamic", 2}} {
		id := "mixed-" + v.id
		alerts[v.id] = onlyAlert(h.t, h.sendMixedTarget(id, id, v.id, v.biz))
		h.assertTargetShield(alerts[v.id])
		h.expect(alerts[v.id])
	}
	for _, v := range []struct {
		id     string
		biz    int
		reason string
	}{{"dynamic-other-business", 3, "outside_target_instances"}, {"foreign-only", 2, "outside_target_instances"}, {"unauthorized", 4, "outside_business_scope"}} {
		e := h.sendMixedTarget("mixed-miss-"+v.id, "mixed-miss-"+v.id, v.id, v.biz)
		h.assertTargetStep(e, "not_matched", v.reason)
		h.expect(onlyAlert(h.t, e), "firing")
	}
	// 最后的动态 selector 失败，不能使用前面已经命中的静态/拓扑集合。
	h.setDynamicConditions(tenant, `[{"field":"unknown","operator":"equal","value":1}]`)
	h.retainAfterTargetFailure(alerts["shared"])
	fault := h.sendMixedTarget("mixed-dynamic-fault", "mixed-dynamic-fault", "shared", 2)
	h.assertTargetStep(fault, "skipped", "target_resolution_failed")
	h.expect(onlyAlert(h.t, fault), "firing")
	bound := h.assertTargetShield(alerts["shared"])
	h.assertManualShieldCheck(tenant, alerts["shared"], bound.Revision, "partial")
	// 合法空分组只移除该分支；仍在另两类 selector 中的实例继续屏蔽。
	h.setDynamicConditions(tenant, `[{"field":"zone","operator":"equal","value":"no-member"}]`)
	h.assertUnshieldedWithoutAdmission(alerts["dynamic"])
	for _, name := range []string{"shared", "static", "topology"} {
		h.assertTargetShield(alerts[name])
	}
	next := h.sendMixedTarget("mixed-dynamic-next", "mixed-dynamic", "dynamic", 2)
	h.alert(alerts["dynamic"], func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(alerts["dynamic"], "firing")
	index := h.names.IndexPrefix + "-om-cmdb_biz_topo_host_membership"
	for _, name := range []string{"shared", "topology"} {
		if _, err := h.es.do(h.ctx, http.MethodDelete, "/"+index+"/_doc/"+tenant+"-member-"+name, nil, nil); err != nil {
			h.t.Fatal(err)
		}
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		h.t.Fatal(err)
	}
	h.assertUnshieldedWithoutAdmission(alerts["topology"])
	h.assertTargetShield(alerts["shared"])
	h.assertTargetShield(alerts["static"])
	next = h.sendMixedTarget("mixed-topology-next", "mixed-topology", "topology", 2)
	h.alert(alerts["topology"], func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(alerts["topology"], "firing")
	// 缺失显式实例仍是完整性错误，不因另一个显式实例命中而部分生效。
	index = h.names.IndexPrefix + "-one-instances"
	if _, err := h.es.do(h.ctx, http.MethodDelete, "/"+index+"/_doc/"+tenant+"-static", nil, nil); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		h.t.Fatal(err)
	}
	h.retainAfterTargetFailure(alerts["shared"])
	fault = h.sendMixedTarget("mixed-static-fault", "mixed-static-fault", "shared", 2)
	h.assertTargetStep(fault, "skipped", "target_resolution_failed")
	h.expect(onlyAlert(h.t, fault), "firing")
	h.targetDocuments(index, map[string]map[string]any{tenant + "-static": targetInstance(tenant, "static", 3, 1, "static")})
	result, err := resolve(descriptor, "bkcc__99")
	if err != nil || !reflect.DeepEqual(result.Instances, []onemodel.InstanceRef{mixedTargetRef("shared"), mixedTargetRef("static")}) {
		h.t.Fatalf("restored target set wrong: %+v %v", result, err)
	}
	h.assertTargetShield(alerts["shared"])
	h.assertTargetShield(alerts["static"])
	h.t.Log("mixed policy retained failures, removed only empty branches, and admitted only on next Event")
}
