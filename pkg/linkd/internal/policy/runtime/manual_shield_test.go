// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func (d *shieldDirectory) Get(ctx context.Context, scope policy.Scope, id string) (policy.Record, error) {
	rows, err := d.List(ctx, scope, "", 16)
	if err != nil {
		return policy.Record{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return policy.Record{}, policy.ErrNotFound
}

func TestManualShieldImmediateRetryExpiryAndDependencyFailure(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 1, 0, time.UTC)}
	directory := &shieldDirectory{}
	state, action := &shieldHook{}, &shieldHook{}
	p, shielder := newShieldProcessor(t, repo, clock, directory, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	current, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	release := timeShieldRelease(t)
	directory.releases = []policy.Release{release}
	// 前一轮目录还缓存空策略；快捷绑定必须读取刚发布的版本，不等新 Event 或目录刷新。
	command := lifecycle.ShieldCommand{TenantID: "tenant", AlertID: id, OperationID: "manual-op", OperatorID: "admin", ExpectedRevision: current.Alert.Revision, Policy: domain.PolicyVersion{ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}, EffectiveAt: clock.at}
	applied, err := p.BindShield(t.Context(), command)
	if err != nil || !applied.Alert.Shield.Active || applied.Alert.Status != domain.AlertStatusActive || applied.Alert.Shield.Bindings[0].Origin != "manual" || applied.Alert.LatestEventID != opening.Event.EventID || len(action.calls) != 1 {
		t.Fatal("immediate binding", applied, err)
	}
	retry, err := p.BindShield(t.Context(), command)
	if err != nil || !retry.AlreadyApplied || retry.Alert.Revision != applied.Alert.Revision {
		t.Fatal("retry duplicated binding", err)
	}
	changed := command
	changed.OperatorID = "another"
	if _, err = p.BindShield(t.Context(), changed); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("changed command accepted", err)
	}
	shielder.Events = nil // 手动绑定定时复查不依赖旧 Event，更不重新匹配其目标条件。
	clock.at = clock.at.Add(time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || changed {
		t.Fatal("manual binding unexpectedly released", err)
	}
	directory.err = errors.New("unavailable")
	if _, err = p.CheckShield(t.Context(), "tenant", id); err != nil {
		t.Fatal(err)
	}
	kept, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !kept.Alert.Shield.Active {
		t.Fatal("dependency failure released manual binding")
	}
	directory.err = nil
	clock.at = clock.at.Add(20 * time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
		t.Fatal("expiry did not release", err)
	}
	ended, err := p.BindShield(t.Context(), command)
	if err != nil || !ended.AlreadyApplied || ended.Alert.Shield.Active || len(action.calls) != 1 {
		t.Fatal("old command revived expired binding or sent action", err)
	}
	logs, err := repo.ListAlertLogs(t.Context(), "tenant", id, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range logs.Logs {
		if row.OperationKind == domain.OperationKindShield {
			found = true
			if row.OperatorKind != domain.OperatorKindUser {
				t.Fatal("manual actor lost")
			}
		}
	}
	if !found {
		t.Fatal("missing manual audit")
	}
}

func TestManualShieldRejectsStalePolicyAndPreservesOtherBindings(t *testing.T) {
	release := timeShieldRelease(t)
	directory := &shieldDirectory{releases: []policy.Release{release}}
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 1, 0, time.UTC)}
	repo := memory.New()
	p, _ := newShieldProcessor(t, repo, clock, directory, &shieldHook{}, &shieldHook{})
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	current, _ := repo.GetAlert(t.Context(), "tenant", id)
	command := lifecycle.ShieldCommand{TenantID: "tenant", AlertID: id, OperationID: "manual", OperatorID: "admin", ExpectedRevision: current.Alert.Revision, Policy: domain.PolicyVersion{ID: release.ID, Version: 1, Digest: release.Compiled.Digest}, EffectiveAt: clock.at}
	bad := command
	bad.Policy.Version = 2
	if _, err := p.BindShield(t.Context(), bad); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("unpublished version accepted", err)
	}
	result, err := p.BindShield(t.Context(), command)
	if err != nil || len(result.Alert.Shield.Bindings) != 2 || result.Alert.Shield.Bindings[0].BindingID != current.Alert.Shield.Bindings[0].BindingID {
		t.Fatal("existing binding replaced", err)
	}
	bad = command
	bad.OperationID = "new-but-stale"
	if _, err := p.BindShield(t.Context(), bad); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatal("stale revision accepted", err)
	}
}
