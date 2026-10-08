// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package allinone_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/policy"
	"linkd/internal/projection"
)

// TestAllInOneKACPolicyDeliveryE2E 组合真实策略、控制任务、投影确认和动作投递。
// 全部 Event 均经 Kafka/Cleaner/Worker；全局插件覆盖原始来源和内置合并来源。
func TestAllInOneKACPolicyDeliveryE2E(t *testing.T) {
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
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 8*time.Minute, kacPolicyCredentials(t, receiver, remote.URL, "shield"))
			f := newKACPolicyFixture(h, receiver, remote.URL, "shield")
			f.checkShieldDelivery()
			f.checkFailedMergeDelivery()
			f.checkMergedMemberDelivery()
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
			f.assertActions(6)
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.actions) != 8 || len(receiver.projections) != 6 || receiver.wrongRoute.Load() != 0 {
				t.Fatal("unexpected remote action/projection identities")
			}
			for _, a := range receiver.projectionAlerts {
				if !a.Status.Terminal() {
					t.Fatal("terminal policy state was not projected")
				}
			}
		})
	}
}

func kacPolicyCredentials(t *testing.T, receiver *kacDeliveryReceiver, endpoint string, _ ...string) func(string) {
	t.Helper()
	plugins := kacESPlugin(t, receiver, endpoint)
	return func(path string) {
		//nolint:gosec // G304: 仅本测试生成的配置文件。
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		document["plugins"] = plugins
		raw, err = yaml.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type kacPolicyFixture struct {
	h        *policyHarness
	tenant   string
	source   eventsource.Record
	actions  *actionstore.Store
	receiver *kacDeliveryReceiver
}

func newKACPolicyFixture(h *policyHarness, receiver *kacDeliveryReceiver, endpoint, tenant string) *kacPolicyFixture {
	t := h.t
	t.Helper()
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	source := kacSource(h, "policy-a")
	actions, err := actionstore.OpenExisting(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = actions.Close() })
	return &kacPolicyFixture{h: h, tenant: tenant, source: source, actions: actions, receiver: receiver}
}

func (f *kacPolicyFixture) seed(name string) domain.Alert {
	f.h.t.Helper()
	return f.seedBlocked(name, name, "host-1")
}

func (f *kacPolicyFixture) seedBlocked(name, title, instance string) domain.Alert {
	t, h := f.h.t, f.h
	t.Helper()
	event := h.send(f.tenant, f.source.ID, name, name, title, "shared", "warning", "triggered", map[string]any{"model_id": "cmdb.host", "model_inst_id": instance})
	a := h.alert(onlyAlert(t, event), func(a domain.Alert) bool { return a.Shield.Active || a.Merge.Blocking() })
	if a.EventSourceVersion != f.source.Published || a.Projection.Targets["kac"].SourceVersion != f.source.Published {
		t.Fatal("Worker did not bind opening Release")
	}
	if a.Admission.AdmittedAt != nil || a.ActionPending != nil || !a.Projection.Targets["kac"].ActionEnabled {
		t.Fatal("initial blocked alert admitted or lost target")
	}
	return a
}

func (f *kacPolicyFixture) projected(a domain.Alert) {
	f.h.t.Helper()
	f.h.until("policy state projected "+a.Title, func() bool {
		f.receiver.mu.Lock()
		defer f.receiver.mu.Unlock()
		got, exists := f.receiver.projectionAlerts[a.AlertID]
		if !exists || got.Revision != a.Revision {
			return false
		}
		// Projection 去除调度元数据，使用正式编码比较屏蔽/合并/准入等全部业务字段。
		want, err := projection.BuildRequest(a, "kac")
		if err != nil {
			f.h.t.Fatal(err)
		}
		var decoded domain.Alert
		if err := json.Unmarshal(want.Alert, &decoded); err != nil {
			f.h.t.Fatal(err)
		}
		if !reflect.DeepEqual(got, decoded) {
			f.h.t.Fatal("projection changed policy business snapshot")
		}
		return true
	})
}

func (f *kacPolicyFixture) assertActions(count int) []actiondelivery.StoredTask {
	f.h.t.Helper()
	rows, err := f.actions.List(f.h.ctx, actiondelivery.Query{TenantID: f.tenant, Limit: 16})
	rows = slices.DeleteFunc(rows, func(row actiondelivery.StoredTask) bool { return row.Task.SourceID != f.source.ID })
	if err != nil || len(rows) != count {
		f.h.t.Fatal("unexpected policy action count", len(rows), count, err)
	}
	return rows
}

func (f *kacPolicyFixture) accepted(a domain.Alert, count int, action, causeType string) {
	f.h.t.Helper()
	f.projected(a)
	f.h.until("policy action accepted "+a.Title, func() bool {
		rows := f.assertActions(count)
		for _, row := range rows {
			q := row.Task.Request
			if q.AlertID == a.AlertID && q.Revision == a.Revision {
				if q.Action != action || q.Cause.Type != causeType || row.Task.SourceVersion != f.source.Published {
					f.h.t.Fatal("policy action lost original cause/version")
				}
				return row.Task.Progress.State == "succeeded"
			}
		}
		return false
	})
}

func (f *kacPolicyFixture) checkShieldDelivery() {
	h := f.h
	shield := func(name string) map[string]any {
		return map[string]any{"policy": condition(name), "shield_type": "time_shield", "model_id": "cmdb.host", "target_descriptor": map[string]any{"schema_version": 1, "model_id": "cmdb.host", "selectors": []any{map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": "cmdb.host", "model_inst_id": "host-1", "entity_uid": "cmdb.host|host-1"}}}}}}
	}
	h.publishID("shield", policy.Shield, "delivery-expiry", shield("delivery-shield"), time.Now().Add(30*time.Second))
	h.publishID("shield", policy.Shield, "delivery-never", shield("delivery-unadmitted"), time.Now().Add(15*time.Minute))
	a, never := f.seed("delivery-shield"), f.seed("delivery-unadmitted")
	f.projected(a)
	f.projected(never)
	f.assertActions(0)
	h.close("shield", never.AlertID)
	ended := h.alert(never.AlertID, func(a domain.Alert) bool { return a.Status.Terminal() })
	f.projected(ended)
	f.assertActions(0)
	released := h.alert(a.AlertID, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
	if released.Admission.AdmittedAt != nil || released.Status != domain.AlertStatusActive {
		h.t.Fatal("shield expiry admitted or ended alert")
	}
	f.projected(released)
	f.assertActions(0)
	next := h.send("shield", f.source.ID, "delivery-shield-next", a.Fingerprint, "ordinary", "shared", "warning", "triggered")
	admitted := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID && a.ActionPending == nil })
	f.accepted(admitted, 1, "firing", "source_event")
	h.close("shield", a.AlertID)
	closed := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed && a.ActionPending == nil })
	f.accepted(closed, 2, "close", "user_operation")
}

func (f *kacPolicyFixture) checkFailedMergeDelivery() {
	h := f.h
	h.publishID("shield", policy.Merge, "delivery-failed", mergeFixture("delivery-failed", "missing-member", "unused-parent", 30, false), time.Now().Add(15*time.Minute))
	a := f.seed("delivery-failed")
	f.projected(a)
	f.assertActions(2)
	released := h.alert(a.AlertID, func(a domain.Alert) bool {
		return !a.Merge.Blocking() && a.MergeChange == nil && a.Admission.AdmittedAt != nil && a.ActionPending == nil
	})
	if released.Admission.CauseType != "system_operation" || released.Status != domain.AlertStatusActive {
		h.t.Fatal("failed merge lost release admission")
	}
	f.accepted(released, 3, "firing", "system_operation")
	h.send("shield", f.source.ID, "delivery-failed-end", a.Fingerprint, "ordinary", "shared", "warning", "resolved")
	ended := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.ActionPending == nil })
	f.accepted(ended, 4, "resolved", "source_event")
}

func (f *kacPolicyFixture) checkMergedMemberDelivery() {
	h := f.h
	h.publishID("shield", policy.Merge, "delivery-success", mergeFixture("delivery-member-a", "delivery-member-b", "delivery-parent", 120, false), time.Now().Add(15*time.Minute))
	a := f.seed("delivery-member-a")
	f.projected(a)
	b := f.seed("delivery-member-b")
	parent := h.mergedParent("shield", "delivery-parent 2")
	if !parent.Projection.Targets["kac"].ActionEnabled {
		h.t.Fatal("parent omitted global plugin")
	}
	// 可靠投递是异步的；若先关闭父，Gate 可以合法跳过尚未发送的旧 firing。
	// 本场景验证已经通知的父关闭，先等待其 firing 受理再继续。
	h.until("global parent firing accepted", func() bool {
		f.receiver.mu.Lock()
		defer f.receiver.mu.Unlock()
		for _, receipt := range f.receiver.actions {
			if receipt.AlertID == parent.AlertID {
				return true
			}
		}
		return false
	})
	for _, id := range []string{a.AlertID, b.AlertID} {
		f.projected(h.mergeMember(id, 0, 1))
	}
	f.assertActions(4)
	// 未放行成员恢复只同步状态；人工关闭父也不得给仍活动的另一个成员补发处置。
	h.send("shield", f.source.ID, "delivery-member-b-end", b.Fingerprint, "ordinary", "shared", "warning", "resolved")
	b = h.alert(b.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.MergeChange == nil })
	f.projected(b)
	f.assertActions(4)
	h.close("shield", parent.AlertID)
	unlinked := h.alert(a.AlertID, func(a domain.Alert) bool { return !a.Merge.Blocking() && a.MergeChange == nil })
	if unlinked.Status != domain.AlertStatusActive || unlinked.Admission.AdmittedAt != nil {
		h.t.Fatal("parent close recovered or admitted child")
	}
	f.projected(unlinked)
	f.assertActions(4)
	next := h.send("shield", f.source.ID, "delivery-member-a-next", a.Fingerprint, "ordinary", "shared", "warning", "triggered")
	admitted := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Admission.CauseID == next.Event.EventID && a.ActionPending == nil })
	f.accepted(admitted, 5, "firing", "source_event")
	h.send("shield", f.source.ID, "delivery-member-a-end", a.Fingerprint, "ordinary", "shared", "warning", "resolved")
	ended := h.alert(a.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered && a.ActionPending == nil })
	f.accepted(ended, 6, "resolved", "source_event")
	h.until("global parent actions accepted", func() bool {
		f.receiver.mu.Lock()
		defer f.receiver.mu.Unlock()
		count := 0
		for _, r := range f.receiver.actions {
			if r.AlertID == parent.AlertID {
				count++
			}
		}
		return count == 2
	})
}

func kacSource(h *policyHarness, sourceID string) eventsource.Record {
	h.t.Helper()
	var source eventsource.Record
	h.call(http.MethodGet, "/api/v1/event-sources/"+sourceID, nil, &source)
	return source
}
