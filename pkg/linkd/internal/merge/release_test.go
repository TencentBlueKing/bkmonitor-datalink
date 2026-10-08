// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type releaseFunc func(context.Context, string, string, string) (store.StoredAlert, error)

func (f releaseFunc) ReleaseMergeWindow(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
	return f(ctx, tenant, id, window)
}

func failedDecision(t *testing.T, j *Journal) Decision {
	t.Helper()
	d := decisionFixture(t)
	d.Outcome = "failed"
	d.FrozenAt = d.Deadline
	d.MemberIDs = []string{"a"}
	d.WaitMemberIDs = []string{"a", "reserved"}
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	return saved.Decision
}

func TestReleaseJournalAdvancesOnlyAfterRealMemberAndRetriesStableWindow(t *testing.T) {
	j, docs := newJournal(t)
	d := failedDecision(t, j)
	repo := memory.New()
	a := storetest.Alert(d.TenantID, "a", "opening", "fp", "warning")
	wait := domain.MergeWait{WindowID: d.WindowID, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, GroupKey: d.GroupKey, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: d.StartedAt, Deadline: d.Deadline}
	a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{wait}}
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	action := &actionCounter{}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// 该测试串行调用，生产端口必须再取得 fingerprint lease。
	releaser := releaseFunc(processor.ReleaseMergeWindow)
	docs.failKind = "merge_decisions"
	if _, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaser, d.FrozenAt); err == nil {
		t.Fatal("journal CAS failure swallowed")
	}
	if action.calls != 1 {
		t.Fatal("member not released before journal advance")
	}
	saved, err := j.Get(t.Context(), d.TenantID, d.ID)
	if err != nil || saved.Decision.Progress.MemberOffset != 0 {
		t.Fatal("failed progress write moved cursor", err)
	}
	next, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaser, d.FrozenAt)
	if err != nil || next.Decision.Progress.MemberOffset != 1 || action.calls != 1 {
		t.Fatal("member operation was not idempotent", err)
	}
	done, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaser, d.FrozenAt)
	if err != nil || done.Decision.Progress.Phase != "completed" || done.Decision.Progress.MemberOffset != 2 {
		t.Fatal("absent reserved candidate blocked release", err)
	}
	if _, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaser, d.FrozenAt); err != nil || action.calls != 1 {
		t.Fatal("completed decision repeated action", err)
	}
}

func TestReleaseJournalRejectsFailureOrUnverifiedResponse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func(context.Context, string, string, string) (store.StoredAlert, error)
		want  error
	}{
		{"storage failed", func(context.Context, string, string, string) (store.StoredAlert, error) {
			return store.StoredAlert{}, errors.New("unavailable")
		}, nil},
		{"unverified row", func(context.Context, string, string, string) (store.StoredAlert, error) {
			return store.StoredAlert{}, nil
		}, nil},
		{"cross tenant", func(ctx context.Context, _ string, id, window string) (store.StoredAlert, error) {
			repo := memory.New()
			created, err := repo.CreateAlert(ctx, storetest.Alert("foreign", id, "event", "fp", "warning"))
			return created.StoredAlert, err
		}, policy.ErrAccess},
		{"still waiting", func(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
			repo := memory.New()
			a := storetest.Alert(tenant, id, "event", "fp", "warning")
			a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: window, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("a", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: "event", Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}}}
			created, err := repo.CreateAlert(ctx, a)
			return created.StoredAlert, err
		}, nil},
		{"cancelled", func(context.Context, string, string, string) (store.StoredAlert, error) {
			return store.StoredAlert{}, context.Canceled
		}, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, _ := newJournal(t)
			d := failedDecision(t, j)
			_, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaseFunc(tc.reply), d.FrozenAt)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal("invalid release accepted", err)
			}
			saved, err := j.Get(t.Context(), d.TenantID, d.ID)
			if err != nil || saved.Decision.Progress.MemberOffset != 0 {
				t.Fatal("failed member advanced cursor", err)
			}
		})
	}
}

func TestReleaseRejectsInvalidTimingBeforeSideEffects(t *testing.T) {
	j, _ := newJournal(t)
	d := failedDecision(t, j)
	called := false
	releaser := releaseFunc(func(context.Context, string, string, string) (store.StoredAlert, error) {
		called = true
		return store.StoredAlert{}, store.ErrNotFound
	})
	if _, err := j.ReleaseStep(t.Context(), d.TenantID, d.ID, releaser, d.FrozenAt.Add(-time.Second)); !errors.Is(err, policy.ErrInvalid) || called {
		t.Fatal("invalid time invoked member operation", err)
	}
	early := decisionFixture(t)
	early.WindowID = strings.Repeat("e", 64)
	var identityErr error
	early.ID, identityErr = domain.MergeDecisionID(early.TenantID, early.WindowID)
	if identityErr != nil {
		t.Fatal(identityErr)
	}
	early.Outcome = "failed"
	if _, err := j.Claim(t.Context(), early); err == nil {
		t.Fatal("window failure before deadline accepted")
	}
}
