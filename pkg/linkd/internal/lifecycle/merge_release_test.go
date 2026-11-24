// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func releaseAlert() domain.Alert {
	a := storetest.Alert("tenant", "child", "opening", "fp", "warning")
	wait := domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}
	a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{wait}}
	return a
}

func TestMergeReleaseOnlyAdmitsEligibleLastWindow(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		actions     int
		edit        func(*domain.Alert)
	}{
		{"last window", "released", 1, func(*domain.Alert) {}},
		{"removed severity", "released", 0, func(a *domain.Alert) { a.Severity = "removed" }},
		{"another window", "pending", 0, func(a *domain.Alert) {
			w := a.Merge.Pending[0].Clone()
			w.WindowID = strings.Repeat("d", 64)
			w.Policy.ID = "another"
			a.Merge.Pending = append(a.Merge.Pending, w)
		}},
		{"successful relation", "merged", 0, func(a *domain.Alert) {
			a.Merge.RelationIDs = []string{strings.Repeat("e", 64)}
			a.Merge.State = "merged"
		}},
		{"already admitted", "released", 0, func(a *domain.Alert) {
			at := a.UpdateAt
			a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: AlertChangeCauseSourceEvent, CauseID: "old"}
		}},
		{"unadmitted upgrade", "released", 1, func(a *domain.Alert) {
			at := a.UpdateAt
			a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: "info", CauseType: AlertChangeCauseSourceEvent, CauseID: "old"}
		}},
		{"still shielded", "released", 0, func(a *domain.Alert) {
			at := a.UpdateAt.Add(time.Minute)
			a.Shield = domain.AlertShield{Active: true, NextCheckAt: &at, Bindings: []domain.ShieldBinding{{BindingID: strings.Repeat("d", 64), ActivationID: strings.Repeat("e", 64), Policy: a.Merge.Pending[0].Policy, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: a.UpdateAt}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := memory.New()
			a := releaseAlert()
			tc.edit(&a)
			if _, err := repo.CreateAlert(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			action, state := &recordingHook{}, &recordingHook{}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			p.finalHooks = []NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}, {Name: "state", Purpose: "state", Hook: state}}
			got, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID)
			if err != nil || got.Alert.Merge.State != tc.state || got.Alert.Status != domain.AlertStatusActive || got.Alert.MergeChange != nil || len(action.inputs) != tc.actions || len(state.inputs) != 1 {
				t.Fatalf("release %+v actions=%d states=%d err=%v", got.Alert.Merge, len(action.inputs), len(state.inputs), err)
			}
			if !reflect.DeepEqual(a.Enrich, got.Alert.Enrich) || a.Title != got.Alert.Title || a.LatestEventID != got.Alert.LatestEventID {
				t.Fatal("release fabricated a new Event or refreshed facts")
			}
			if tc.actions > 0 && (!got.Alert.AdmittedActiveMain() || got.Alert.Admission.CauseID != action.inputs[0].Cause.ID) {
				t.Fatal("admission not tied to operation")
			}
			again, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID)
			if err != nil || again.Version != got.Version || len(action.inputs) != tc.actions || len(state.inputs) != 1 {
				t.Fatal("replayed release repeated business action", err)
			}
		})
	}
}

func TestMergeReleaseRetriesFrozenIntentBeforeNewEvent(t *testing.T) {
	base := memory.New()
	repo := &failCloseLogs{Repository: base}
	a := releaseAlert()
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	action := &recordingHook{}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	p.finalHooks = []NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}
	repo.fail = true
	if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID); err == nil {
		t.Fatal("log failure ignored")
	}
	pending, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || pending.Alert.MergeChange == nil || pending.Alert.Merge.State != "released" || len(action.inputs) != 1 {
		t.Fatal("CAS intent was lost", err)
	}
	// 新 Event 必须先补齐前一操作；普通重试可重复投递，但稳定 cause/消息身份不能变化。
	e := storetest.Event(a.BKTenantID, "next-event", a.Fingerprint, a.Severity)
	result := persistAndProcess(t, base, p, e)
	if result.Outcome != OutcomeAlertSuppressed || result.ReasonCode != "duplicate_trigger" || len(action.inputs) != 2 || action.inputs[0].Cause != action.inputs[1].Cause || !reflect.DeepEqual(action.inputs[0].Alert, action.inputs[1].Alert) {
		t.Fatal("new Event replaced or bypassed pending release")
	}
	got, err := base.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || got.Alert.MergeChange != nil || got.Alert.LatestEventID != e.EventID {
		t.Fatal(err)
	}
	if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID); err != nil || len(action.inputs) != 2 {
		t.Fatal("completed release repeated", err)
	}
}

func TestMergeReleaseTerminalMissingTenantAndCancellation(t *testing.T) {
	repo := memory.New()
	a := releaseAlert()
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	next := a.Clone()
	next.Status = domain.AlertStatusRecovered
	next.Merge = next.Merge.EndWaiting()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeSource
	next.EndReason = "resolved"
	if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next); err != nil {
		t.Fatal(err)
	}
	action := &recordingHook{}
	p := newTestProcessor(t, repo, action)
	got, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID)
	if err != nil || got.Alert.Status != domain.AlertStatusRecovered || len(action.inputs) != 0 {
		t.Fatal("terminal reactivated", err)
	}
	if _, err := p.ReleaseMergeWindow(t.Context(), "other", a.AlertID, a.Merge.Pending[0].WindowID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("cross tenant access", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.ReleaseMergeWindow(ctx, a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, "bad"); !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatal("invalid identity", err)
	}
}

func TestConcurrentMergeReleaseUnderFingerprintLease(t *testing.T) {
	repo := memory.New()
	a := releaseAlert()
	w := a.Merge.Pending[0].Clone()
	w.WindowID = strings.Repeat("d", 64)
	w.Policy.ID = "another"
	a.Merge.Pending = append(a.Merge.Pending, w)
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	action := &recordingHook{}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	p.finalHooks = []NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}
	// 用互斥锁复现调用方同 fingerprint 排他契约；真实 Redis lease 由 process 装配测试覆盖。
	var lease sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		for _, wait := range a.Merge.Pending {
			wg.Go(func() {
				lease.Lock()
				defer lease.Unlock()
				if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, wait.WindowID); err != nil {
					t.Error(err)
				}
			})
		}
	}
	wg.Wait()
	if len(action.inputs) != 1 {
		t.Fatalf("last member admitted %d times", len(action.inputs))
	}
}
