// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package allinone_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/projection"
)

// TestAllInOneKACDependencyDeliveryE2E 核对依赖关系变化只同步状态，下一 Event 才准入。
// 初始子告警显式绑定可靠目标；主告警来自另一实际来源，使用普通 KAC Hook。
func TestAllInOneKACDependencyDeliveryE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			receiver := &kacDeliveryReceiver{t: t, projections: map[string]projection.Receipt{}, actions: map[string]actiondelivery.Receipt{}}
			receiver.allowProjection.Store(true)
			receiver.allowAction.Store(true)
			remote := httptest.NewServer(receiver)
			t.Cleanup(remote.Close)
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 12*time.Minute, kacPolicyCredentials(t, receiver, remote.URL, "dependency-custom", "dependency-cmdb"))
			for _, scenario := range []struct{ tenant, mode string }{{"dependency-custom", "custom_shield"}, {"dependency-cmdb", "cmdb_shield"}} {
				t.Logf("reliable dependency %s: main-only targets, fixed binding, hinted release and next-Event admission", scenario.mode)
				f := newKACPolicyFixture(h, receiver, remote.URL, scenario.tenant)
				f.checkDependencyDelivery(scenario.mode)
			}
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.actions) != 12 || len(receiver.projections) != 8 || receiver.wrongRoute.Load() != 0 {
				t.Fatal("unexpected dependency delivery identities")
			}
			for _, a := range receiver.projectionAlerts {
				if !a.Status.Terminal() || a.Shield.Active {
					t.Fatal("dependency terminal projection incomplete")
				}
			}
		})
	}
}

func (f *kacPolicyFixture) checkDependencyDelivery(mode string) {
	h, tenant := f.h, f.tenant
	rootTitle, childTitle := "delivery-root", "delivery-child"
	model, instance := "cmdb.host", "host-1"
	rely := condition(childTitle)
	if mode == "cmdb_shield" {
		model, instance = "cmdb.switch", "switch-1"
		rely = map[string]any{"expression": "A AND B", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": childTitle}, "B": map[string]any{"condition": "term", "target_key": "model_id", "target_value": "cmdb.host", "bk_obj_asst_id": "switch_connect_host"}}
	}
	mainLabels := map[string]any{"model_id": model, "model_inst_id": instance}
	childLabels := map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-2"}
	h.publishID(tenant, policy.Shield, "delivery-dependency", map[string]any{"policy": condition(rootTitle), "rely_policy": rely, "shield_type": "rely_shield", "shield_mode": mode, "time_range_before": 5, "time_range_after": 10, "model_id": model, "target_descriptor": map[string]any{"schema_version": 1, "model_id": model, "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": model, "model_inst_id": instance, "entity_uid": model + "|" + instance}}}}}}, time.Now().Add(15*time.Minute))
	mainA := onlyAlert(h.t, h.send(tenant, "policy-b", "delivery-root-a", "delivery-root-a", rootTitle, "root-a", "warning", "triggered", mainLabels))
	h.alert(mainA, func(a domain.Alert) bool { return a.AdmittedActiveMain() })
	// 两类配置的子实例都不在主目标集合内；CMDB 夹具使用反向存储的关系边。
	a := f.seedBlocked("delivery-child-a", childTitle, "host-2")
	f.projected(h.assertDependencyBinding(a.AlertID, mainA, mode))
	mainB := onlyAlert(h.t, h.send(tenant, "policy-b", "delivery-root-b", "delivery-root-b", rootTitle, "root-b", "warning", "triggered", mainLabels))
	h.alert(mainB, func(a domain.Alert) bool { return a.AdmittedActiveMain() })
	b := f.seedBlocked("delivery-child-b", childTitle, "host-2")
	f.projected(h.assertDependencyBinding(b.AlertID, mainB, mode))
	bound := h.assertDependencyBinding(a.AlertID, mainA, mode)
	f.assertActions(0)
	if mode == "cmdb_shield" {
		h.failRelations.Store(true)
		defer h.failRelations.Store(false)
		h.assertManualShieldCheck(tenant, a.AlertID, bound.Revision, "partial")
		kept := h.assertDependencyBinding(a.AlertID, mainA, mode)
		if kept.Revision != bound.Revision {
			h.t.Fatal("relation failure changed business state")
		}
		f.projected(kept)
		f.assertActions(0)
		h.failRelations.Store(false)
	}
	h.armShieldHints(tenant, a.AlertID, b.AlertID)
	h.send(tenant, "policy-b", "delivery-root-a-end", "delivery-root-a", rootTitle, "root-a", "warning", "resolved", mainLabels)
	h.assertHintUnshield(tenant, a.AlertID)
	unbound := h.alert(a.AlertID, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
	f.projected(unbound)
	h.assertDependencyBinding(b.AlertID, mainB, mode)
	f.assertActions(0)
	// 新主不会追溯替换既有关系；只有下一条子触发才重新匹配并绑定仍活动的主 B。
	h.send(tenant, f.source.ID, "delivery-child-a-rebind", a.Fingerprint, childTitle, "shared", "warning", "triggered", childLabels)
	f.projected(h.assertDependencyBinding(a.AlertID, mainB, mode))
	f.assertActions(0)
	h.armShieldHints(tenant, a.AlertID, b.AlertID)
	h.close(tenant, mainB)
	for _, id := range []string{a.AlertID, b.AlertID} {
		h.assertHintUnshield(tenant, id)
		unbound := h.alert(id, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
		if unbound.Status != domain.AlertStatusActive || unbound.Admission.AdmittedAt != nil {
			h.t.Fatal("main close ended/admitted child")
		}
		f.projected(unbound)
	}
	f.assertActions(0)
	next := h.send(tenant, f.source.ID, "delivery-child-a-next", a.Fingerprint, childTitle, "shared", "warning", "triggered", childLabels)
	admitted := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID && a.ActionPending == nil })
	if admitted.Title != a.Title || admitted.Content != a.Content {
		h.t.Fatal("dependency readmission refreshed opening facts")
	}
	f.accepted(admitted, 1, "firing", "source_event")
	h.close(tenant, b.AlertID)
	closed := h.alert(b.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed && a.PolicyChange == nil })
	f.projected(closed)
	f.assertActions(1)
	h.send(tenant, f.source.ID, "delivery-child-a-end", a.Fingerprint, childTitle, "shared", "warning", "resolved", childLabels)
	ended := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.ActionPending == nil })
	f.accepted(ended, 2, "resolved", "source_event")
	h.until("global main actions delivered", func() bool {
		f.receiver.mu.Lock()
		defer f.receiver.mu.Unlock()
		counts := map[string]int{}
		for _, r := range f.receiver.actions {
			counts[r.AlertID]++
		}
		return counts[mainA] == 2 && counts[mainB] == 2
	})
}
