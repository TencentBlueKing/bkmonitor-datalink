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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
)

// 通过 Kafka 和正式屏蔽任务验证真实目标定义、PIT/Scroll 分页及成员变更，不直接调用解除函数。
// 额外用只读 SDK 核对完整目标集合，使依赖失败与生命周期错误能够分别定位。
func TestAllInOneTargetShieldE2E(t *testing.T) {
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
			h.checkDynamicTargets()
			h.checkTopologyTargets()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

const targetModel = "cw-Host"

const targetGroup = `{"condition":"OR","rules":[{"field":"rank","operator":"greater_or_equal","value":0},{"field":"zone","operator":"contains","value":"east*"}]}`

func (h *policyHarness) seedTargetFixtures() {
	h.t.Helper()
	if _, err := h.metadata.ExecContext(h.ctx, "CREATE TABLE dynamic_group_v2 (bk_tenant_id VARCHAR(64), dynamic_group_id BIGINT, object_model_code VARCHAR(128), space_code VARCHAR(64), condition_list JSON)"); err != nil {
		h.t.Fatal(err)
	}
	// 不创建 dynamic_group_member_v2：若实现误依赖成员缓存，真实 SQL 会立即失败。
	docs := map[string]map[string]any{}
	for _, tenant := range []string{"target-dynamic", "target-topology", "target-foreign"} {
		if _, err := h.metadata.ExecContext(h.ctx, "INSERT INTO metadata_space VALUES (?, 'bkcc', '2', false), (?, 'bkcc', '3', false)", tenant, tenant); err != nil {
			h.t.Fatal(err)
		}
		if _, err := h.metadata.ExecContext(h.ctx, `INSERT INTO object_model_v2 VALUES (?, 'cw-Host', 'cmdb', 'host', '{"config":[{"bk_property_id":"rank","bk_property_type":"int"},{"bk_property_id":"zone","bk_property_type":"singlechar"}]}')`, tenant); err != nil {
			h.t.Fatal(err)
		}
		conditions := targetGroup
		if tenant == "target-foreign" {
			conditions = `[{"field":"rank","operator":"less","value":-100}]`
		}
		if _, err := h.metadata.ExecContext(h.ctx, "INSERT INTO dynamic_group_v2 VALUES (?, 7, 'cw-Host', 'bkcc__2', ?)", tenant, conditions); err != nil {
			h.t.Fatal(err)
		}
		for i := 0; i < 205; i++ {
			id := fmt.Sprintf("host-%03d", i)
			docs[tenant+"-"+id] = targetInstance(tenant, id, 2, i, "east*production")
		}
		docs[tenant+"-wildcard-trap"] = targetInstance(tenant, "wildcard-trap", 2, 1, "eastZZproduction")
		docs[tenant+"-rank-trap"] = targetInstance(tenant, "rank-trap", 2, -1, "east*production")
		docs[tenant+"-business-trap"] = targetInstance(tenant, "business-trap", 3, 1, "east*production")
	}
	h.targetDocuments(h.names.IndexPrefix+"-one-instances", docs)
	for _, suffix := range []string{"node", "host_membership"} {
		index := h.names.IndexPrefix + "-om-cmdb_biz_topo_" + suffix
		raw := []byte(`{"mappings":{"dynamic":false,"properties":{"bk_tenant_id":{"type":"keyword"},"bk_biz_id":{"type":"long"},"unique_id":{"type":"keyword"},"model_id":{"type":"keyword"},"model_inst_id":{"type":"keyword"},"entity_uid":{"type":"keyword"},"topology_ancestor_unique_ids":{"type":"keyword"}}}}`)
		if _, err := h.es.do(h.ctx, http.MethodPut, "/"+index, raw, nil); err != nil {
			h.t.Fatal(err)
		}
	}
	nodes, members := map[string]map[string]any{}, map[string]map[string]any{}
	for _, tenant := range []string{"target-topology", "target-foreign"} {
		for _, biz := range []int{2, 3} {
			// 相同 locator 在不同租户/业务重复，查询必须带完整作用域。
			nodes[fmt.Sprintf("%s-%d", tenant, biz)] = map[string]any{"bk_tenant_id": tenant, "bk_biz_id": biz, "unique_id": "module-1", "model_id": "cw-Module", "model_inst_id": "10", "entity_uid": "cw-Module|10"}
			for i := 0; i < 205; i++ {
				id := fmt.Sprintf("host-%03d", i)
				key := fmt.Sprintf("%s-%d-%s", tenant, biz, id)
				members[key] = targetMembership(tenant, biz, key, id)
			}
		}
	}
	// 同一主机的多个拓扑路径只贡献一个 canonical 成员。
	members["duplicate-path"] = targetMembership("target-topology", 2, "duplicate-path", "host-000")
	h.targetDocuments(h.names.IndexPrefix+"-om-cmdb_biz_topo_node", nodes)
	h.targetDocuments(h.names.IndexPrefix+"-om-cmdb_biz_topo_host_membership", members)
}

func targetInstance(tenant, id string, biz, rank int, zone string) map[string]any {
	return map[string]any{"bk_tenant_id": tenant, "model_id": targetModel, "model_inst_id": id, "entity_uid": targetModel + "|" + id, "bk_biz_ids": []int{biz}, "attributes": map[string]any{"rank": rank, "zone": zone}, "attribute_values": []any{map[string]any{"field_name": "rank", "long_values": []int{rank}}, map[string]any{"field_name": "zone", "keyword_values": []string{zone}}}}
}

func targetMembership(tenant string, biz int, key, id string) map[string]any {
	return map[string]any{"unique_id": key, "bk_tenant_id": tenant, "bk_biz_id": biz, "model_id": targetModel, "model_inst_id": id, "entity_uid": targetModel + "|" + id, "topology_ancestor_unique_ids": []string{"biz-root", "module-1"}}
}

func (h *policyHarness) targetDocuments(index string, documents map[string]map[string]any) {
	h.t.Helper()
	if !strings.HasPrefix(index, h.names.IndexPrefix+"-") {
		h.t.Fatal("target fixture outside owned namespace")
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for id, doc := range documents {
		if err := encoder.Encode(map[string]any{"index": map[string]any{"_index": index, "_id": id}}); err != nil {
			h.t.Fatal(err)
		}
		if err := encoder.Encode(doc); err != nil {
			h.t.Fatal(err)
		}
	}
	var result struct {
		Errors bool              `json:"errors"`
		Items  []json.RawMessage `json:"items"`
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/_bulk", body.Bytes(), &result); err != nil || result.Errors || len(result.Items) != len(documents) {
		h.t.Fatalf("target fixture bulk failed: %v errors=%t items=%d", err, result.Errors, len(result.Items))
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		h.t.Fatal(err)
	}
}

func (h *policyHarness) targetPolicy(tenant string, selector map[string]any) {
	h.publish(tenant, policy.Shield, map[string]any{"policy": condition("target"), "shield_type": "time_shield", "model_id": targetModel, "target_descriptor": map[string]any{"schema_version": 1, "model_id": targetModel, "selectors": []any{selector}}}, time.Now().Add(15*time.Minute))
}

func (h *policyHarness) targetEvent(tenant, id, fp, instance string) storedEventView {
	return h.send(tenant, "policy-b", id, fp, "target", instance, "warning", "triggered", map[string]any{"model_id": targetModel, "model_inst_id": instance})
}

func (h *policyHarness) assertTargetShield(id string) domain.Alert {
	h.t.Helper()
	a := h.alert(id, func(a domain.Alert) bool { return a.Shield.Active && a.PolicyChange == nil })
	if len(a.Shield.Bindings) != 1 || a.Shield.Bindings[0].Type != "time_shield" || a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
		h.t.Fatal("invalid target shield state")
	}
	return a
}

func (h *policyHarness) assertTargetStep(event storedEventView, outcome, reason string) {
	h.t.Helper()
	if event.Processing.PolicyDecision == nil || event.Processing.PolicyDecision.Shield == nil {
		h.t.Fatal("missing target decision")
	}
	for _, step := range event.Processing.PolicyDecision.Shield.Steps {
		if step.Outcome == outcome && step.ReasonCode == reason {
			return
		}
	}
	h.t.Fatalf("missing %s/%s: %+v", outcome, reason, event.Processing.PolicyDecision.Shield)
}

func (h *policyHarness) retainAfterTargetFailure(id string) {
	h.t.Helper()
	before := h.assertTargetShield(id)
	h.alert(id, func(a domain.Alert) bool {
		return a.Shield.NextCheckAt != nil && a.Shield.NextCheckAt.After(*before.Shield.NextCheckAt)
	})
	a := h.assertTargetShield(id)
	if a.Shield.Bindings[0].BindingID != before.Shield.Bindings[0].BindingID {
		h.t.Fatal("target failure changed fixed binding")
	}
}

func (h *policyHarness) checkDynamicTargets() {
	tenant := "target-dynamic"
	h.t.Log("dynamic group: real typed filters, complete PIT pages, tenant/business isolation, definition reread")
	h.targetPolicy(tenant, map[string]any{"type": "dynamic_group", "provider": "kingeye", "dynamic_group_id": "7"})
	opening := h.targetEvent(tenant, "dynamic-open", "dynamic-open", "host-204")
	id := onlyAlert(h.t, opening)
	h.assertTargetShield(id)
	for _, instance := range []string{"wildcard-trap", "rank-trap", "business-trap"} {
		e := h.targetEvent(tenant, "dynamic-"+instance, "dynamic-"+instance, instance)
		h.assertTargetStep(e, "not_matched", "outside_target_instances")
		h.expect(onlyAlert(h.t, e), "firing")
	}
	// 对真实定义注入未知字段，不能把不完整/不可求值误判为不命中并解除已有屏蔽。
	h.setDynamicConditions(tenant, `[{"field":"unknown","operator":"equal","value":1}]`)
	h.retainAfterTargetFailure(id)
	fault := h.targetEvent(tenant, "dynamic-fault", "dynamic-fault", "host-203")
	h.assertTargetStep(fault, "skipped", "target_resolution_failed")
	h.expect(onlyAlert(h.t, fault), "firing")
	h.setDynamicConditions(tenant, `[{"field":"rank","operator":"less","value":0}]`)
	h.assertUnshieldedWithoutAdmission(id)
	next := h.targetEvent(tenant, "dynamic-next", "dynamic-open", "host-204")
	h.alert(id, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(id, "firing")
}

func (h *policyHarness) setDynamicConditions(tenant, conditions string) {
	h.t.Helper()
	result, err := h.metadata.ExecContext(h.ctx, "UPDATE dynamic_group_v2 SET condition_list=? WHERE bk_tenant_id=? AND dynamic_group_id=7", conditions, tenant)
	if err != nil {
		h.t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		h.t.Fatalf("dynamic fixture update: %d %v", count, err)
	}
}

func (h *policyHarness) checkTopologyTargets() {
	tenant := "target-topology"
	h.t.Log("topology: complete Scroll/PIT pages, duplicate paths, corrupt member retains binding, complete empty releases")
	// 先独立验证真实目标资源的完整集合，失败时保留具体依赖原因；业务断言仍经过 Kafka/控制任务。
	runtime, err := policyruntime.Open(h.resources, config.BluekingConfig{})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	biz := int64(2)
	resolved, err := runtime.Targets.Resolve(h.ctx, tenant, "bkcc__2", onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: targetModel, Selectors: []onemodel.TargetSelector{{Type: "topo_node", Provider: "cmdb_mainline", TopologyNodeID: "module-1", BizID: &biz}}})
	if err != nil || len(resolved.Instances) != 205 {
		h.t.Fatalf("topology fixture resolution: members=%d error=%v", len(resolved.Instances), err)
	}
	h.targetPolicy(tenant, map[string]any{"type": "topo_node", "provider": "cmdb_mainline", "topology_node_id": "module-1", "bk_biz_id": 2})
	id := onlyAlert(h.t, h.targetEvent(tenant, "topology-open", "topology-open", "host-204"))
	h.assertTargetShield(id)
	miss := h.targetEvent(tenant, "topology-missing", "topology-missing", "wildcard-trap")
	h.assertTargetStep(miss, "not_matched", "outside_target_instances")
	h.expect(onlyAlert(h.t, miss), "firing")
	index := h.names.IndexPrefix + "-om-cmdb_biz_topo_host_membership"
	key := "target-topology-2-host-204"
	bad := targetMembership(tenant, 2, key, "host-204")
	bad["entity_uid"] = "cw-Host|wrong"
	h.targetDocuments(index, map[string]map[string]any{key: bad})
	h.retainAfterTargetFailure(id)
	fault := h.targetEvent(tenant, "topology-fault", "topology-fault", "host-000")
	h.assertTargetStep(fault, "skipped", "target_resolution_failed")
	h.expect(onlyAlert(h.t, fault), "firing")
	// 本租户本业务成员全部移除；节点保留，其他租户和业务的同 locator 不得继续屏蔽。
	raw := []byte(`{"query":{"bool":{"filter":[{"term":{"bk_tenant_id":"target-topology"}},{"term":{"bk_biz_id":2}}]}}}`)
	var result struct {
		Deleted  int               `json:"deleted"`
		TimedOut bool              `json:"timed_out"`
		Failures []json.RawMessage `json:"failures"`
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+index+"/_delete_by_query", raw, &result); err != nil || result.Deleted != 206 || result.TimedOut || len(result.Failures) != 0 {
		h.t.Fatalf("owned membership deletion failed: %+v %v", result, err)
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		h.t.Fatal(err)
	}
	h.assertUnshieldedWithoutAdmission(id)
	next := h.targetEvent(tenant, "topology-next", "topology-open", "host-204")
	h.alert(id, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(id, "firing")
}
