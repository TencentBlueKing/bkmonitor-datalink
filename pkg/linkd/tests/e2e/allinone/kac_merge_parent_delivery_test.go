// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package allinone_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/projection"
)

// TestAllInOneKACMergeParentDeliveryE2E 验证真实合并控制任务放行/结束已绑定的父告警。
// 控制面创建内部父 Event，正式 Worker 自动绑定其来源目标；不注入目标或手工处理 Event。
func TestAllInOneKACMergeParentDeliveryE2E(t *testing.T) {
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
			h := startPolicyHarnessWithTimeout(t, root, binary, backend, 10*time.Minute, kacPolicyCredentials(t, receiver, remote.URL, "merge"))
			f := newKACParentFixture(h, receiver, remote.URL)
			childSources := map[string]eventsource.Record{}
			for _, sourceID := range []string{"policy-a", "policy-b"} {
				childSources[sourceID] = kacSource(h, sourceID)
			}
			h.publishID("merge", policy.Merge, "delivery-parent", mergeFixture("parent-a", "parent-b", "reliable-parent", 45, true), time.Now().Add(15*time.Minute))
			for index, ending := range []string{"recover", "close"} {
				t.Logf("reliable parent %s: real internal Event, readiness admission, terminal delivery", ending)
				children := []string{}
				for _, member := range []string{"a", "b"} {
					source := "policy-a"
					if member == "b" {
						source = "policy-b"
					}
					name := ending + "-" + member
					children = append(children, onlyAlert(t, h.send("merge", source, name, name, "parent-"+member, ending, "warning", "triggered")))
				}
				first := h.mergeMember(children[0], 1, 0)
				second := h.mergeMember(children[1], 1, 0)
				wait := first.Merge.Pending[0]
				if second.Merge.Pending[0].WindowID != wait.WindowID || time.Until(wait.Deadline) < 10*time.Second {
					t.Fatal("fixture missed cyclic pre-deadline boundary")
				}
				ids := slices.Clone(children)
				slices.Sort(ids)
				fingerprint := merge.ParentFingerprint("merge", "delivery-parent", ids)
				var parent domain.Alert
				h.until("reliable parent ready", func() bool {
					for _, a := range h.alerts() {
						if a.EventSourceID == domain.BuiltinMergeEventSourceID && a.Fingerprint == fingerprint && a.Merge != nil && a.Merge.RelationsReady && a.MergeChange == nil && a.Admission.AdmittedAt != nil && a.ActionPending == nil {
							parent = a
							return true
						}
					}
					return false
				})
				if parent.BKTenantID != "merge" || len(parent.Projection.Targets) != 1 || !parent.Projection.Targets["kac"].ActionEnabled || parent.Merge.Role != "aggregate" {
					t.Fatal("parent lost independent output binding")
				}
				for _, child := range children {
					a := h.mergeMember(child, 0, 1)
					if a.Projection.Targets["kac"].SourceVersion != childSources[a.EventSourceID].Published || !a.Projection.Targets["kac"].ActionEnabled {
						t.Fatal("child automatic binding missing")
					}
					f.projected(a)
				}
				f.accepted(parent, index*2+1, "firing", "system_operation")
				if ending == "close" {
					h.close("merge", parent.AlertID)
					closed := h.alert(parent.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed && a.ActionPending == nil })
					f.accepted(closed, 4, "close", "user_operation")
					for _, child := range children {
						a := h.mergeMember(child, 0, 0)
						if a.Status != domain.AlertStatusActive || a.Admission.AdmittedAt != nil {
							t.Fatal("parent close ended/admitted child")
						}
					}
					f.assertActions(4)
				}
				for memberIndex, member := range []string{"a", "b"} {
					source := "policy-a"
					if member == "b" {
						source = "policy-b"
					}
					name := ending + "-" + member
					h.send("merge", source, name+"-end", name, "parent-"+member, ending, "warning", "resolved")
					childEnded := h.alert(children[memberIndex], func(a domain.Alert) bool { return a.Status == domain.AlertStatusRecovered })
					f.projected(childEnded)
					if ending == "recover" && memberIndex == 0 {
						point := h.mergeControlPoint("merge", "relations", parent.Merge.RelationIDs[0])
						h.assertMergeRetry(point, "single-member-ended")
						still := h.alert(parent.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusActive })
						if still.Revision != parent.Revision {
							t.Fatal("single ended child changed parent")
						}
						f.assertActions(1)
					}
				}
				if ending == "recover" {
					recovered := h.alert(parent.AlertID, func(a domain.Alert) bool {
						return a.Status == domain.AlertStatusRecovered && a.MergeChange == nil && a.ActionPending == nil
					})
					if recovered.EndType != domain.AlertEndTypeSystem || recovered.EndReason != "merge_members_ended" {
						t.Fatal("parent automatic recovery lost reason")
					}
					f.accepted(recovered, 2, "resolved", "system_operation")
				} else {
					closed := h.alert(parent.AlertID, func(a domain.Alert) bool { return a.Status == domain.AlertStatusClosed })
					if closed.EndType != domain.AlertEndTypeUser {
						t.Fatal("member recovery replaced manual parent close")
					}
					f.projected(closed)
					f.assertActions(4)
				}
			}
			if err := h.process.stop(); err != nil {
				t.Fatal(err)
			}
			h.checkActionMessages()
			f.assertActions(4)
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.actions) != 4 || len(receiver.projections) != 6 || receiver.wrongRoute.Load() != 0 {
				t.Fatal("duplicate parent delivery identities")
			}
		})
	}
}

func newKACParentFixture(h *policyHarness, receiver *kacDeliveryReceiver, endpoint string) *kacPolicyFixture {
	t := h.t
	t.Helper()
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	source := kacSource(h, domain.BuiltinMergeEventSourceID)
	actions, err := actionstore.OpenExisting(h.ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = actions.Close() })
	return &kacPolicyFixture{h: h, tenant: "merge", source: source, actions: actions, receiver: receiver}
}
