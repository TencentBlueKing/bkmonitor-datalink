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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
)

// 由真实进程裁决周期/多策略窗口及前置抑制组合，测试不直接推进裁决、建联或释放用例。
func TestAllInOneMergeCombinationsE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run external-service E2E", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			h := startPolicyHarness(t, root, binary, backend)
			h.checkCyclicMerge()
			h.checkMultipleMergePolicies()
			h.checkSuppressionWithMerge()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
		})
	}
}

func mergeFixture(a, b, title string, seconds int, cyclic bool) map[string]any {
	return map[string]any{"policy": []any{condition(a), condition(b)}, "merge_cycle": seconds, "is_cycle_merge": cyclic, "aggregate_fields": []string{"object"}, "new_alarm_config": []any{map[string]any{"key": "name", "value": title + " ${alarm_num}"}, map[string]any{"key": "content", "value": "members ${alarm_num}"}, map[string]any{"key": "level", "value": "warning"}}}
}

func (h *policyHarness) mergedParent(tenant, title string) domain.Alert {
	h.t.Helper()
	var result domain.Alert
	h.until("ready parent "+title, func() bool {
		for _, a := range h.alerts() {
			if a.BKTenantID == tenant && a.Title == title && a.Status == domain.AlertStatusActive && a.EventSourceID == domain.BuiltinMergeEventSourceID && a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil {
				result = a
				return true
			}
		}
		return false
	})
	return result
}

func (h *policyHarness) mergeMember(id string, pending, relations int) domain.Alert {
	h.t.Helper()
	a := h.alert(id, func(a domain.Alert) bool {
		return a.Merge != nil && len(a.Merge.Pending) == pending && len(a.Merge.RelationIDs) == relations && a.MergeChange == nil
	})
	if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
		h.t.Fatal("merge bookkeeping changed original lifecycle/admission")
	}
	return a
}

func (h *policyHarness) checkCyclicMerge() {
	tenant := "merge-cycle"
	h.t.Log("cyclic merge: no early parent, immutable window on repeats, next cycle has independent identity")
	h.publish(tenant, policy.Merge, mergeFixture("cycle-a", "cycle-b", "cyclic-parent", 30, true), time.Now().Add(15*time.Minute))
	previousWindow := ""
	for cycle := 1; cycle <= 2; cycle++ {
		prefix := fmt.Sprintf("cycle-%d", cycle)
		firstEvent := h.send(tenant, "policy-a", prefix+"-a", prefix+"-a", "cycle-a", "same-cycle-group", "warning", "triggered")
		first := onlyAlert(h.t, firstEvent)
		wait := h.mergeMember(first, 1, 0).Merge.Pending[0]
		if wait.WindowID == previousWindow {
			h.t.Fatal("next cycle reused old window")
		}
		previousWindow = wait.WindowID
		repeated := h.send(tenant, "policy-a", prefix+"-repeat", prefix+"-a", "cycle-a", "same-cycle-group", "warning", "triggered")
		if onlyAlert(h.t, repeated) != first {
			h.t.Fatal("repeat changed member identity")
		}
		again := h.mergeMember(first, 1, 0).Merge.Pending[0]
		if again.WindowID != wait.WindowID || !again.Deadline.Equal(wait.Deadline) || again.MemberEventID != firstEvent.Event.EventID {
			h.t.Fatal("repeat refreshed first window/member")
		}
		second := onlyAlert(h.t, h.send(tenant, "policy-b", prefix+"-b", prefix+"-b", "cycle-b", "same-cycle-group", "warning", "triggered"))
		h.mergeMember(second, 1, 0)
		if time.Until(wait.Deadline) < 5*time.Second {
			h.t.Fatal("fixture did not reach the pre-deadline observation point")
		}
		var window struct {
			Cyclic    bool                     `json:"cyclic"`
			Committed int                      `json:"committed_count"`
			Groups    []int                    `json:"group_counts"`
			Deadline  time.Time                `json:"deadline"`
			Frozen    *redisstate.MergeVerdict `json:"frozen"`
		}
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/windows/"+wait.WindowID+"?bk_tenant_id="+tenant, nil, &window)
		if !window.Cyclic || window.Committed != 2 || !slices.Equal(window.Groups, []int{1, 1}) || window.Frozen != nil || !window.Deadline.Equal(wait.Deadline) {
			h.t.Fatalf("invalid pre-deadline cycle: %+v", window)
		}
		parent := h.mergedParent(tenant, "cyclic-parent 2")
		if parent.CreateAt.Before(wait.Deadline) {
			h.t.Fatal("cyclic merge created parent before deadline")
		}
		for _, id := range []string{first, second} {
			h.mergeMember(id, 0, 1)
			h.expect(id)
		}
		h.close(tenant, parent.AlertID)
		for _, id := range []string{first, second} {
			h.mergeMember(id, 0, 0)
		}
		h.expect(parent.AlertID, "firing", "close")
	}
}

func (h *policyHarness) checkMultipleMergePolicies() {
	tenant := "merge-multi"
	h.t.Log("multiple policies: two parents, one failed wait, partial unlink keeps other relation and no action")
	end := time.Now().Add(15 * time.Minute)
	h.publishID(tenant, policy.Merge, "ab", mergeFixture("multi-a", "multi-b", "parent-ab", 60, false), end)
	h.publishID(tenant, policy.Merge, "ac", mergeFixture("multi-a", "multi-c", "parent-ac", 60, false), end)
	h.publishID(tenant, policy.Merge, "ad-missing", mergeFixture("multi-a", "multi-d", "parent-ad", 30, false), end)
	first := onlyAlert(h.t, h.send(tenant, "policy-a", "multi-a", "multi-a", "multi-a", "multi-group", "warning", "triggered"))
	h.mergeMember(first, 3, 0)
	second := onlyAlert(h.t, h.send(tenant, "policy-b", "multi-b", "multi-b", "multi-b", "multi-group", "warning", "triggered"))
	third := onlyAlert(h.t, h.send(tenant, "policy-b", "multi-c", "multi-c", "multi-c", "multi-group", "warning", "triggered"))
	parentAB, parentAC := h.mergedParent(tenant, "parent-ab 2"), h.mergedParent(tenant, "parent-ac 2")
	// 第三个窗口缺少 D，到期释放不能移除成功关系，也不能因释放而补发 A。
	h.mergeMember(first, 0, 2)
	var decisions struct {
		Items []struct {
			Policy  domain.PolicyVersion `json:"policy"`
			Outcome string               `json:"outcome"`
			Phase   string               `json:"phase"`
		} `json:"items"`
	}
	h.until("failed third decision complete", func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/decisions?bk_tenant_id="+tenant+"&policy_id=ad-missing", nil, &decisions)
		for _, d := range decisions.Items {
			if d.Policy.ID == "ad-missing" && d.Outcome == "failed" && d.Phase == "completed" {
				return true
			}
		}
		return false
	})
	h.close(tenant, parentAB.AlertID)
	remaining := h.mergeMember(first, 0, 1)
	if remaining.Merge.RelationIDs[0] != parentAC.Merge.RelationIDs[0] {
		h.t.Fatal("first parent close removed the other relation")
	}
	h.mergeMember(second, 0, 0)
	h.close(tenant, parentAC.AlertID)
	h.mergeMember(first, 0, 0)
	h.mergeMember(third, 0, 0)
	var history struct {
		Items []domain.MergeRelation `json:"items"`
	}
	h.until("both completed relations remain queryable", func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/merge/relations?bk_tenant_id="+tenant+"&alert_id="+first, nil, &history)
		if len(history.Items) != 2 {
			return false
		}
		for _, relation := range history.Items {
			if relation.State != "ended" || relation.EndReason != "parent_ended" {
				return false
			}
		}
		return true
	})
	next := h.send(tenant, "policy-a", "multi-next", "multi-a", "ordinary", "multi-group", "warning", "triggered")
	h.alert(first, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID })
	h.expect(first, "firing")
	h.expect(second)
	h.expect(third)
	h.expect(parentAB.AlertID, "firing", "close")
	h.expect(parentAC.AlertID, "firing", "close")
}

func (h *policyHarness) checkSuppressionWithMerge() {
	tenant := "merge-combined"
	h.t.Log("clip/aggregation with merge: waiting child is not aggregation owner, active upgrade bypasses, all children recover parent")
	end := time.Now().Add(15 * time.Minute)
	match := map[string]any{"expression": "A OR B", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": "combo-a"}, "B": map[string]any{"condition": "term", "target_key": "name", "target_value": "combo-b"}}
	h.publishID(tenant, policy.Suppression, "front", map[string]any{"policy": match, "scheme": []any{map[string]any{"type": "clip", "count": 2, "duration": 60, "duration_type": "second"}, map[string]any{"type": "aggregation", "duration": 60, "duration_type": "second", "fields": []string{"object"}}}}, end)
	h.publishID(tenant, policy.Merge, "combine", mergeFixture("combo-a", "combo-b", "combined-parent", 60, false), end)
	assertNoAlert(h.t, h.send(tenant, "policy-a", "combo-a-1", "combo-a", "combo-a", "same-group", "warning", "triggered"))
	first := onlyAlert(h.t, h.send(tenant, "policy-a", "combo-a-2", "combo-a", "combo-a", "same-group", "warning", "triggered"))
	wait := h.mergeMember(first, 1, 0).Merge.Pending[0]
	up := h.send(tenant, "policy-a", "combo-a-up", "combo-a", "combo-a", "same-group", "critical", "triggered")
	if up.Processing.PolicyDecision == nil || up.Processing.PolicyDecision.Suppression == nil || up.Processing.PolicyDecision.Suppression.BypassReason != "active_alert" || onlyAlert(h.t, up) != first {
		h.t.Fatal("active waiting upgrade did not bypass suppression")
	}
	// Event 与 Alert 使用独立 ES 刷新，先等待新等级可搜索，不能把旧快照误判为升级丢失。
	a := h.alert(first, func(a domain.Alert) bool {
		return a.Severity == "critical" && a.Merge != nil && len(a.Merge.Pending) == 1 && a.MergeChange == nil
	})
	if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil || a.Merge.Pending[0].WindowID != wait.WindowID || !a.Merge.Pending[0].Deadline.Equal(wait.Deadline) {
		h.t.Fatal("upgrade was lost or extended merge window")
	}
	assertNoAlert(h.t, h.send(tenant, "policy-b", "combo-b-1", "combo-b", "combo-b", "same-group", "warning", "triggered"))
	secondEvent := h.send(tenant, "policy-b", "combo-b-2", "combo-b", "combo-b", "same-group", "warning", "triggered")
	second := onlyAlert(h.t, secondEvent)
	if second == first || secondEvent.Processing.State == domain.EventProcessStateSuppressed {
		h.t.Fatal("unadmitted merge member became cross-source aggregation owner")
	}
	parent := h.mergedParent(tenant, "combined-parent 2")
	for _, id := range []string{first, second} {
		h.mergeMember(id, 0, 1)
		h.expect(id)
	}
	h.send(tenant, "policy-a", "combo-a-end", "combo-a", "combo-a", "same-group", "critical", "resolved")
	h.send(tenant, "policy-b", "combo-b-end", "combo-b", "combo-b", "same-group", "warning", "resolved")
	h.alert(parent.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.MergeChange == nil })
	assertNoAlert(h.t, h.send(tenant, "policy-a", "combo-a-restart", "combo-a", "combo-a", "same-group", "warning", "triggered"))
	h.expect(parent.AlertID, "firing", "resolved")
}
