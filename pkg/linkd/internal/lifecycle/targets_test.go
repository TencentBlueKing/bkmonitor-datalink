// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package lifecycle

import (
	"fmt"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func TestOpeningTargetsRemainForEntireLifecycle(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, upgradePolicy := range []string{"update_current", "close_and_create"} {
			t.Run(fmt.Sprintf("empty=%v/%s", empty, upgradePolicy), func(t *testing.T) {
				repo := memory.New()
				p := actionProcessor(t, repo, &intentRecorder{})
				if empty {
					p.initialProjectionTargets = nil
				} else {
					p.initialProjectionTargets = map[string]bool{"kac": true}
				}
				p.upgradePolicy = upgradePolicy
				opening := storetest.Event("tenant", "opening", "fp", "warning")
				first := persistAndProcess(t, repo, p, opening)
				upgrade := storetest.Event("tenant", "upgrade", "fp", "critical")
				upgrade.EventSourceVersion = 2
				// 模拟下一进程已启用全局插件；既有空绑定不做历史补齐。
				p.initialProjectionTargets = map[string]bool{"kac": true}
				second := persistAndProcess(t, repo, p, upgrade)
				old, err := repo.GetAlert(t.Context(), "tenant", first.AlertIDs[0])
				if err != nil {
					t.Fatal(err)
				}
				if empty && len(old.Alert.Projection.Targets) != 0 || !empty && (len(old.Alert.Projection.Targets) != 1 || old.Alert.Projection.Targets["kac"].SourceVersion != 1) {
					t.Fatal("existing Alert was rebound")
				}
				activeID := first.AlertIDs[0]
				if upgradePolicy == "close_and_create" {
					active, err := repo.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: "tenant", EventSourceID: "source", Fingerprint: "fp"})
					if err != nil || active.Alert.Projection.Targets["kac"].SourceVersion != 2 {
						t.Fatal("new lifecycle did not bind opening version", err)
					}
					activeID = active.Alert.AlertID
					if second.Outcome != OutcomeAlertRotated || activeID == first.AlertIDs[0] {
						t.Fatal("upgrade did not rotate lifecycle")
					}
				}
				terminal := storetest.Event("tenant", "terminal", "fp", "critical")
				terminal.EventSourceVersion = 3
				terminal.Evaluations[0].Action = domain.EventActionResolved
				persistAndProcess(t, repo, p, terminal)
				ended, err := repo.GetAlert(t.Context(), "tenant", activeID)
				if err != nil || !ended.Alert.Status.Terminal() {
					t.Fatal("terminal selected targets again", err)
				}
				newEvent := storetest.Event("tenant", "new-lifecycle", "fp", "warning")
				newEvent.EventSourceVersion = 3
				newEvent.OccurredAt = terminal.OccurredAt.Add(time.Minute)
				third := persistAndProcess(t, repo, p, newEvent)
				created, err := repo.GetAlert(t.Context(), "tenant", third.AlertIDs[0])
				if err != nil || created.Alert.Projection.Targets["kac"].SourceVersion != 3 {
					t.Fatal("subsequent lifecycle did not select its release", err)
				}
			})
		}
	}
}

func TestFrozenPlanRetainsGlobalPluginTarget(t *testing.T) {
	repo := memory.New()
	rec := &intentRecorder{fail: true}
	p := actionProcessor(t, repo, rec)
	p.initialProjectionTargets = map[string]bool{"kac": true}
	event := storetest.Event("tenant", "opening", "fp", "warning")
	created, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("enqueue failure swallowed")
	}
	pending := mustGetStoredEvent(t, repo, event)
	if pending.Processing.Plan == nil {
		t.Fatal("missing frozen plan")
	}
	rec.fail = false
	p.initialProjectionTargets = nil
	if _, err = p.ProcessEvent(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	if len(rec.requests) != 1 {
		t.Fatal("frozen action target was lost")
	}
}
