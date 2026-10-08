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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// 依赖屏蔽使用真实业务空间、实例、双向关系和自动控制任务；只对关系 HTTP 注入一次可恢复故障。
func TestAllInOneDependencyShieldE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkDependencyShield("custom_shield", "dependency-custom")
			h.checkDependencyShield("cmdb_shield", "dependency-cmdb")
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

func (h *policyHarness) checkDependencyShield(mode, tenant string) {
	h.t.Logf("dependency %s: real targets, fixed main, newest selection, autonomous release", mode)
	model, instance := "cmdb.host", "host-1"
	rely := condition("child")
	if mode == "cmdb_shield" {
		model, instance = "cmdb.switch", "switch-1"
		rely = map[string]any{"expression": "A AND B", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": "child"}, "B": map[string]any{"condition": "term", "target_key": "model_id", "target_value": "cmdb.host", "bk_obj_asst_id": "switch_connect_host"}}
	}
	mainLabels := map[string]any{"model_id": model, "model_inst_id": instance}
	h.publish(tenant, policy.Shield, map[string]any{"policy": condition("root"), "rely_policy": rely, "shield_type": "rely_shield", "shield_mode": mode, "time_range_before": 5, "time_range_after": 10, "model_id": model, "target_descriptor": map[string]any{"schema_version": 1, "model_id": model, "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": model, "model_inst_id": instance, "entity_uid": model + "|" + instance}}}}}}, time.Now().Add(15*time.Minute))
	mainA := onlyAlert(h.t, h.send(tenant, "policy-a", "root-a", "root-a", "root", "root-a", "warning", "triggered", mainLabels))
	childA := onlyAlert(h.t, h.send(tenant, "policy-b", "child-a", "child-a", "child", "host-a", "warning", "triggered"))
	bound := h.assertDependencyBinding(childA, mainA, mode)
	if mode == "cmdb_shield" {
		h.failRelations.Store(true)
		defer h.failRelations.Store(false)
		// 既有关系在依赖不可用时保留，复查仍被重新安排；新的候选明确记录跳过。
		h.alert(childA, func(a domain.Alert) bool {
			return a.Shield.NextCheckAt != nil && a.Shield.NextCheckAt.After(*bound.Shield.NextCheckAt)
		})
		h.assertDependencyBinding(childA, mainA, mode)
		h.assertManualShieldCheck(tenant, childA, bound.Revision, "partial")
		fault := h.send(tenant, "policy-b", "relation-fault", "relation-fault", "child", "fault-host", "warning", "triggered")
		if fault.Processing.PolicyDecision == nil || fault.Processing.PolicyDecision.Shield == nil {
			h.t.Fatal("missing dependency failure diagnostic")
		}
		skipped := false
		for _, step := range fault.Processing.PolicyDecision.Shield.Steps {
			skipped = skipped || (step.Outcome == "skipped" && step.ReasonCode == "dependency_child_unavailable")
		}
		if !skipped {
			h.t.Fatalf("dependency fault was not recorded as skipped: %+v", fault.Processing.PolicyDecision)
		}
		h.expect(onlyAlert(h.t, fault), "firing")
		h.failRelations.Store(false)
	}
	mainB := onlyAlert(h.t, h.send(tenant, "policy-a", "root-b", "root-b", "root", "root-b", "warning", "triggered", mainLabels))
	h.alert(mainB, func(a domain.Alert) bool { return a.AdmittedActiveMain() })
	childB := onlyAlert(h.t, h.send(tenant, "policy-b", "child-b", "child-b", "child", "host-b", "warning", "triggered", map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-2"}))
	h.assertDependencyBinding(childB, mainB, mode)
	closedChild := onlyAlert(h.t, h.send(tenant, "policy-b", "child-close", "child-close", "child", "close-host", "warning", "triggered"))
	h.assertDependencyBinding(closedChild, mainB, mode)
	h.close(tenant, closedChild)
	h.assertShieldHistory(tenant, closedChild)
	h.expect(closedChild)
	recoveredChild := onlyAlert(h.t, h.send(tenant, "policy-b", "child-recover", "child-recover", "child", "recover-host", "warning", "triggered"))
	h.assertDependencyBinding(recoveredChild, mainB, mode)
	h.send(tenant, "policy-b", "child-recover-end", "child-recover", "child", "recover-host", "warning", "resolved")
	h.alert(recoveredChild, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && !a.Shield.Active })
	h.assertShieldHistory(tenant, recoveredChild)
	h.expect(recoveredChild)
	// 新主不迁移已建立关系，定时检查后仍然绑定原主。
	beforeRecheck := h.assertDependencyBinding(childA, mainA, mode)
	h.alert(childA, func(a domain.Alert) bool {
		return a.Shield.NextCheckAt != nil && a.Shield.NextCheckAt.After(*beforeRecheck.Shield.NextCheckAt)
	})
	h.assertDependencyBinding(childA, mainA, mode)
	h.armShieldHints(tenant, childA)
	h.send(tenant, "policy-a", "root-a-end", "root-a", "root", "root-a", "warning", "resolved", mainLabels)
	h.assertHintUnshield(tenant, childA)
	h.assertDependencyBinding(childB, mainB, mode)
	// 只有下一条子触发才重新选主；新的绑定仍不会立即准入。
	h.send(tenant, "policy-b", "child-a-rebind", "child-a", "child", "host-a", "warning", "triggered")
	h.assertDependencyBinding(childA, mainB, mode)
	h.armShieldHints(tenant, childA, childB)
	h.close(tenant, mainB)
	h.assertHintUnshield(tenant, childA)
	h.assertHintUnshield(tenant, childB)
	next := h.send(tenant, "policy-b", "child-a-next", "child-a", "child", "host-a", "warning", "triggered")
	h.alert(childA, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(mainA, "firing", "resolved")
	h.expect(mainB, "firing", "close")
	h.expect(childA, "firing")
	h.expect(childB)
}

func (h *policyHarness) assertDependencyBinding(child, main, mode string) domain.Alert {
	h.t.Helper()
	a := h.alert(child, func(a domain.Alert) bool {
		return a.Shield.Active && len(a.Shield.Bindings) == 1 && a.PolicyChange == nil
	})
	b := a.Shield.Bindings[0]
	if b.Type != "rely_shield" || b.Mode != mode || b.MainAlertID != main || a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
		h.t.Fatalf("invalid dependency binding: %+v", a)
	}
	var queried domain.Alert
	query := "?bk_tenant_id=" + a.BKTenantID
	h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts/"+url.PathEscape(child)+query, nil, &queried)
	if queried.AlertID != child || !queried.Shield.Active || len(queried.Shield.Bindings) != 1 || queried.Shield.Bindings[0].MainAlertID != main || queried.Admission.AdmittedAt != nil {
		h.t.Fatal("shield detail query changed binding semantics")
	}
	var page struct {
		Items []domain.Alert `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts"+query+"&main_alert_id="+url.QueryEscape(main)+"&binding_type="+mode, nil, &page)
	found := false
	for _, row := range page.Items {
		found = found || row.AlertID == child
		if row.BKTenantID != a.BKTenantID {
			h.t.Fatal("shield query tenant leak")
		}
	}
	if !found {
		h.t.Fatal("shield main query missed bound child")
	}
	return a
}

func (h *policyHarness) assertUnshieldedWithoutAdmission(id string) {
	h.t.Helper()
	a := h.alert(id, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
	if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
		h.t.Fatal("timer changed child lifecycle/admission")
	}
	h.assertShieldHistory(a.BKTenantID, id)
}

func (h *policyHarness) assertShieldHistory(tenant, id string) {
	h.t.Helper()
	h.until("unshield history visible through management API", func() bool {
		after := ""
		for range 32 {
			var page struct {
				Items []domain.AlertLog `json:"items"`
				Next  string            `json:"next"`
			}
			h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts/"+url.PathEscape(id)+"/history?"+url.Values{"bk_tenant_id": {tenant}, "after": {after}}.Encode(), nil, &page)
			for _, log := range page.Items {
				if log.BKTenantID != tenant || log.AlertID != id {
					h.t.Fatal("shield history scope leak")
				}
				if log.OperationKind == domain.OperationKindUnshield {
					_, before := log.Params["before_bindings"]
					_, next := log.Params["after_bindings"]
					if !before || !next {
						h.t.Fatal("unshield history lost transition snapshot")
					}
					return true
				}
			}
			if page.Next == "" {
				return false
			}
			after = page.Next
		}
		h.t.Fatal("shield history exceeded scenario budget")
		return false
	})
}
