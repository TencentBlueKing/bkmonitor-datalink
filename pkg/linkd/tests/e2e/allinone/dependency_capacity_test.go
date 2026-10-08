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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
)

// 关系源使用真实隔离 ES 索引和元数据；业务仓储分别运行 ES/MySQL。
// 只给测试子告警延后定时元数据，以证明多页解除由正式终态提示推进。
func TestAllInOneDependencyCapacityE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 15*time.Minute)
			h.checkDependencyCapacity()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

const dependencyCapacityTenant = "dependency-cmdb"

const dependencyCapacityRelation = "capacity_connect_host"

func capacityRelationEdge(tenant, id, relation string, reverse bool) map[string]any {
	fromModel, fromID, toModel, toID := "cmdb.switch", "switch-1", "cmdb.host", id
	if reverse {
		fromModel, fromID, toModel, toID = toModel, toID, fromModel, fromID
	}
	return map[string]any{"bk_tenant_id": tenant, "producer": "cmdb_fact", "source_model_id": fromModel, "source_entity_uid": fromModel + "|" + fromID, "target_model_id": toModel, "target_entity_uid": toModel + "|" + toID, "relation_identity": relation}
}

func capacityHostLabels(i int) map[string]any {
	return map[string]any{"model_id": "cmdb.host", "model_inst_id": fmt.Sprintf("capacity-host-%04d", i)}
}

func (h *policyHarness) checkDependencyCapacity() {
	h.t.Helper()
	const tenant = dependencyCapacityTenant
	instances, edges := map[string]map[string]any{}, map[string]map[string]any{}
	for i := range 1025 {
		id := fmt.Sprintf("capacity-host-%04d", i)
		instances[id] = map[string]any{"bk_tenant_id": tenant, "model_id": "cmdb.host", "model_inst_id": id, "entity_uid": "cmdb.host|" + id, "bk_biz_ids": []int{2}, "attributes": map[string]any{}}
		if i < 1024 {
			edges["capacity-edge-"+id] = capacityRelationEdge(tenant, id, dependencyCapacityRelation, i%2 == 1)
		}
		edges["out-edge-"+id] = capacityRelationEdge(tenant, id, "capacity_out", false)
	}
	// 同起点的其他租户边不能污染上限或结果。
	edges["other-tenant-edge"] = capacityRelationEdge("other", "foreign-host", dependencyCapacityRelation, false)
	index, edgeIndex := h.names.IndexPrefix+"-one-instances", h.names.IndexPrefix+"-one-edges"
	h.targetDocuments(index, instances)
	h.targetDocuments(edgeIndex, edges)
	runtime, err := policyruntime.Open(h.resources, config.BluekingConfig{})
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	origin := onemodel.InstanceRef{ModelID: "cmdb.switch", InstanceID: "switch-1", EntityUID: "cmdb.switch|switch-1"}
	started := time.Now()
	refs, err := runtime.Relations.Lookup(h.ctx, tenant, origin, dependencyCapacityRelation, "cmdb.host")
	if err != nil || len(refs) != 1024 {
		h.t.Fatalf("real 1024 relation boundary failed: %d %v", len(refs), err)
	}
	for i, ref := range refs {
		if ref.InstanceID != fmt.Sprintf("capacity-host-%04d", i) {
			h.t.Fatal("relation result wrong scope/order")
		}
	}
	h.t.Logf("1024 bidirectional relation members resolved in %s", time.Since(started).Round(time.Millisecond))
	if refs, err := runtime.Relations.Lookup(h.ctx, tenant, origin, "capacity_out", "cmdb.host"); !errors.Is(err, onemodel.ErrResultLimit) || len(refs) != 0 {
		h.t.Fatalf("1025 outbound edges not rejected: %d %v", len(refs), err)
	}
	mainLabels := map[string]any{"model_id": "cmdb.switch", "model_inst_id": "switch-1"}
	rely := map[string]any{"expression": "A AND B", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": "child"}, "B": map[string]any{"condition": "term", "target_key": "model_id", "target_value": "cmdb.host", "bk_obj_asst_id": dependencyCapacityRelation}}
	h.publish(tenant, policy.Shield, map[string]any{"policy": condition("root"), "rely_policy": rely, "shield_type": "rely_shield", "shield_mode": "cmdb_shield", "time_range_before": 5, "time_range_after": 30, "model_id": "cmdb.switch", "target_descriptor": map[string]any{"schema_version": 1, "model_id": "cmdb.switch", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.switch", "model_inst_id": "switch-1", "entity_uid": "cmdb.switch|switch-1"}}}}}}, time.Now().Add(30*time.Minute))
	main := onlyAlert(h.t, h.send(tenant, "policy-a", "capacity-root", "capacity-root", "root", "root", "warning", "triggered", mainLabels))
	first := onlyAlert(h.t, h.send(tenant, "policy-b", "capacity-child-000", "capacity-child-000", "child", "host-0", "warning", "triggered", capacityHostLabels(0)))
	bound := h.assertDependencyBinding(first, main, "cmdb_shield")
	overflowID := "capacity-union-overflow"
	h.targetDocuments(edgeIndex, map[string]map[string]any{overflowID: capacityRelationEdge(tenant, "capacity-host-1024", dependencyCapacityRelation, false)})
	if refs, err := runtime.Relations.Lookup(h.ctx, tenant, origin, dependencyCapacityRelation, "cmdb.host"); !errors.Is(err, onemodel.ErrResultLimit) || len(refs) != 0 {
		h.t.Fatalf("1025 union members not rejected: %d %v", len(refs), err)
	}
	overflow := h.send(tenant, "policy-b", "capacity-overflow", "capacity-overflow", "child", "overflow", "warning", "triggered", capacityHostLabels(0))
	skipped := false
	if decision := overflow.Processing.PolicyDecision; decision != nil && decision.Shield != nil {
		for _, step := range decision.Shield.Steps {
			skipped = skipped || (step.Outcome == "skipped" && step.ReasonCode == "dependency_child_unavailable")
		}
	}
	if !skipped {
		h.t.Fatal("over-limit relation did not record whole-policy skip")
	}
	h.expect(onlyAlert(h.t, overflow), "firing")
	h.assertManualShieldCheck(tenant, first, bound.Revision, "partial")
	h.assertDependencyBinding(first, main, "cmdb_shield")
	if _, err := h.es.do(h.ctx, http.MethodDelete, "/"+edgeIndex+"/_doc/"+overflowID, nil, nil); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.es.do(h.ctx, http.MethodPost, "/"+edgeIndex+"/_refresh", nil, nil); err != nil {
		h.t.Fatal(err)
	}
	h.t.Log("relation overflow skipped new candidate and retained existing binding; restored fixture")
	// 65 个子告警覆盖四个满页和一个尾页，跨两个来源共用一个主。
	records := make([]*kgo.Record, 0, 64)
	for i := 1; i < 65; i++ {
		source := "policy-a"
		if i%2 == 1 {
			source = "policy-b"
		}
		id := fmt.Sprintf("capacity-child-%03d", i)
		records = append(records, h.rawRecord(tenant, source, id, id, "child", fmt.Sprintf("host-%d", i), "warning", "triggered", capacityHostLabels(i)))
	}
	started = time.Now()
	if err := h.kafka.ProduceSync(h.ctx, records...).FirstErr(); err != nil {
		h.t.Fatal(err)
	}
	h.untilWithin("65 dependency Events completed", 3*time.Minute, func() bool {
		count := 0
		for _, e := range h.events() {
			if e.Event.BKTenantID == tenant && strings.HasPrefix(e.Event.SourceEventID, "capacity-child-") && e.Processing.State != domain.EventProcessStateUnprocessed {
				if len(e.Event.RelatedAlertIDs) != 1 || e.Event.EnrichStatus != domain.EnrichStatusSucceeded {
					h.t.Fatal("fanout Event missing enrichment or Alert")
				}
				count++
			}
		}
		return count == 65
	})
	children := map[string]bool{}
	h.untilWithin("65 fixed bindings visible", time.Minute, func() bool {
		current := map[string]bool{}
		for _, a := range h.alerts() {
			if a.BKTenantID == tenant && a.Title == "child" && strings.HasPrefix(a.Content, "content-capacity-child-") {
				if !a.Shield.Active || len(a.Shield.Bindings) != 1 || a.Shield.Bindings[0].MainAlertID != main || a.PolicyChange != nil || a.Admission.AdmittedAt != nil {
					return false
				}
				current[a.AlertID] = true
			}
		}
		children = current
		return len(children) == 65
	})
	h.t.Logf("65 cross-source fixed bindings ready in %s", time.Since(started).Round(time.Millisecond))
	ids := make([]string, 0, len(children))
	for id := range children {
		ids = append(ids, id)
		h.expect(id)
	}
	sort.Strings(ids)
	h.armShieldHints(tenant, ids...)
	started = time.Now()
	h.close(tenant, main)
	h.untilWithin("all 65 children released through paged hints", 4*time.Minute, func() bool {
		released := 0
		for _, a := range h.alerts() {
			if children[a.AlertID] {
				if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
					h.t.Fatal("hint changed lifecycle or admitted child")
				}
				if !a.Shield.Active && a.PolicyChange == nil {
					released++
				}
			}
		}
		return released == 65
	})
	h.t.Logf("65 children unshielded without Events in %s", time.Since(started).Round(time.Millisecond))
	for _, id := range ids {
		h.assertHintUnshield(tenant, id)
	}
	// 当前主筛选须排除解除历史，避免旧关系使提示游标反复扫到同一页。
	var page struct {
		Items []domain.Alert `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts?bk_tenant_id="+tenant+"&main_alert_id="+url.QueryEscape(main), nil, &page)
	if len(page.Items) != 0 {
		h.t.Fatal("released history still appears in current main binding query")
	}
	next := h.send(tenant, "policy-b", "capacity-next", "capacity-child-000", "child", "host-0", "warning", "triggered", capacityHostLabels(0))
	h.alert(first, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(main, "firing", "close")
	h.expect(first, "firing")
}
