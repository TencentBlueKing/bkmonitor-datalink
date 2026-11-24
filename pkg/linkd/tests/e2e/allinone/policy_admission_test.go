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
	"os"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// 三类策略共用真实事件链：每个阶段分别验证生命周期、策略状态和处置资格。
func TestAllInOnePolicyAdmissionE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkAllPolicyAdmission()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

func (h *policyHarness) checkAllPolicyAdmission() {
	h.t.Helper()
	// 使用本轮已有的 shield 租户静态主机夹具，所有策略/运行记录均在独立测试部署中。
	const tenant = "shield"
	end := time.Now().Add(15 * time.Minute)
	match := map[string]any{"expression": "A OR B", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": "triple-a"}, "B": map[string]any{"condition": "term", "target_key": "name", "target_value": "triple-b"}}
	h.publishID(tenant, policy.Suppression, "front", map[string]any{"policy": match, "scheme": []any{map[string]any{"type": "clip", "count": 2, "duration": 120, "duration_type": "second"}, map[string]any{"type": "aggregation", "duration": 120, "duration_type": "second", "fields": []string{"object"}}}}, end)
	h.publishID(tenant, policy.Merge, "combine", mergeFixture("triple-a", "triple-b", "triple-parent", 120, false), end)
	h.publishID(tenant, policy.Shield, "maintenance", map[string]any{"policy": match, "shield_type": "time_shield", "model_id": "cmdb.host", "target_descriptor": map[string]any{"schema_version": 1, "model_id": "cmdb.host", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-1", "entity_uid": "cmdb.host|host-1"}}}}}}, time.Now().Add(50*time.Second))
	assertNoAlert(h.t, h.send(tenant, "policy-a", "triple-a-1", "triple-a", "triple-a", "shared", "warning", "triggered"))
	first := onlyAlert(h.t, h.send(tenant, "policy-a", "triple-a-2", "triple-a", "triple-a", "shared", "warning", "triggered"))
	h.alert(first, func(a domain.Alert) bool { return a.Shield.Active && a.PolicyChange == nil })
	assertNoAlert(h.t, h.send(tenant, "policy-b", "triple-b-1", "triple-b", "triple-b", "shared", "warning", "triggered"))
	second := onlyAlert(h.t, h.send(tenant, "policy-b", "triple-b-2", "triple-b", "triple-b", "shared", "warning", "triggered"))
	if first == second {
		h.t.Fatal("shielded Alert became cross-source aggregation owner")
	}
	for _, id := range []string{first, second} {
		a := h.alert(id, func(a domain.Alert) bool { return a.Shield.Active && a.PolicyChange == nil })
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil || a.Merge.Blocking() {
			h.t.Fatal("shielded Alert entered merge or admission")
		}
	}
	var windows struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	h.call(http.MethodGet, "/api/v1/policy-runtime/merge/windows?bk_tenant_id="+tenant, nil, &windows)
	if len(windows.Items) != 0 {
		h.t.Fatal("shielded Events created merge windows")
	}
	for _, id := range []string{first, second} {
		a := h.alert(id, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil || a.Merge.Blocking() {
			h.t.Fatal("timer admitted Alert or entered merge")
		}
	}
	for _, c := range []struct{ source, id, title string }{{"policy-a", first, "triple-a"}, {"policy-b", second, "triple-b"}} {
		e := h.send(tenant, c.source, c.title+"-3", c.title, c.title, "shared", "warning", "triggered")
		if onlyAlert(h.t, e) != c.id || e.Processing.PolicyDecision == nil || e.Processing.PolicyDecision.Suppression == nil || e.Processing.PolicyDecision.Suppression.BypassReason != "active_alert" {
			h.t.Fatal("unshielded active Alert did not bypass new-Alert suppression")
		}
	}
	parent := h.mergedParent(tenant, "triple-parent 2")
	for _, id := range []string{first, second} {
		h.mergeMember(id, 0, 1)
	}
	h.close(tenant, parent.AlertID)
	for _, id := range []string{first, second} {
		a := h.alert(id, func(a domain.Alert) bool { return !a.Merge.Blocking() && a.MergeChange == nil })
		if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil || a.Shield.Active {
			h.t.Fatal("last relation unlink recovered or admitted child")
		}
	}
	for _, c := range []struct{ source, id, title string }{{"policy-a", first, "triple-a"}, {"policy-b", second, "triple-b"}} {
		e := h.send(tenant, c.source, c.title+"-4", c.title, "ordinary", "shared", "warning", "triggered")
		if onlyAlert(h.t, e) != c.id {
			h.t.Fatal("next Event replaced active child")
		}
		a := h.alert(c.id, func(a domain.Alert) bool { return a.Admission.CauseID == e.Event.EventID })
		if a.Title != c.title || a.Content != "content-"+c.title+"-2" {
			h.t.Fatal("admission refreshed opening Event facts")
		}
	}
	h.send(tenant, "policy-a", "triple-a-end", "triple-a", "ordinary", "shared", "warning", "resolved")
	h.alert(first, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered })
	h.close(tenant, second)
	assertNoAlert(h.t, h.send(tenant, "policy-a", "triple-a-restart", "triple-a", "triple-a", "shared", "warning", "triggered"))
	h.expect(parent.AlertID, "firing", "close")
	h.expect(first, "firing", "resolved")
	h.expect(second, "firing", "close")
}
