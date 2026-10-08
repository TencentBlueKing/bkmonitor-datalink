// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/policy/storage"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type requestLease struct{ busy bool }

func (l *requestLease) Acquire(context.Context, string) (scheduler.Lease, error) {
	if l.busy {
		return scheduler.Lease{}, scheduler.ErrLockBusy
	}
	return scheduler.Lease{}, nil
}

func (*requestLease) Renew(context.Context, scheduler.Lease) error { return nil }

func (*requestLease) Release(context.Context, scheduler.Lease) error { return nil }

type requestedCheck struct {
	calls   int
	outcome string
	err     error
}

func (c *requestedCheck) CheckRequest(_ context.Context, r shieldcheck.Request) (shieldcheck.Check, error) {
	c.calls++
	at := time.Now().UTC()
	result := shieldcheck.Check{TenantID: r.Command.TenantID, AlertID: r.Command.AlertID, Trigger: "request", RequestID: r.ID, StartedAt: at, FinishedAt: at, Report: lifecycle.ShieldCheckReport{ObservedRevision: r.Command.ExpectedRevision, ResultRevision: r.Command.ExpectedRevision, CheckedAt: at, Outcome: "retained"}}
	if c.outcome == "superseded" {
		result.Report.ObservedRevision++
		result.Report.ResultRevision++
		result.Report.Outcome = "superseded"
		result.ErrorCode = "revision_changed"
		return result, lifecycle.ErrShieldCheckStale
	}
	if c.err != nil {
		result.Report.Outcome = "failed"
		result.ErrorCode = "dependency_failed"
	}
	return result, c.err
}

type Store = storage.Store

func TestElasticsearchShieldControlContract(t *testing.T) {
	storage.WithElasticsearchTestStore(t, runShieldCheckContract)
}

func TestMySQLShieldControlContract(t *testing.T) {
	storage.WithMySQLTestStore(t, runShieldCheckContract)
}

func refreshShieldFixture(t *testing.T, s *Store) { storage.RefreshShieldTestStore(t, s) }

func runShieldCheckContract(t *testing.T, s *Store, reopen func() *Store) {
	testLargeShieldDiagnostic(t, s, reopen)
	t.Helper()
	j, err := shieldcheck.NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	a := storetest.Alert("checks", "alert", "opening", "fp", "warning")
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	checker := &requestedCheck{}
	lock := &requestLease{}
	controller := lifecycleprocess.NewShieldRequests(j, lock, repo.GetAlert, checker)
	c := shieldcheck.Command{TenantID: a.BKTenantID, AlertID: a.AlertID, ExpectedRevision: a.Revision, OperationID: "first", OperatorID: "tester", Reason: "验证复查"}
	first, err := controller.Request(t.Context(), c)
	if err != nil || first.State != "pending" {
		t.Fatal(first, err)
	}
	prefix := base64.RawURLEncoding.EncodeToString([]byte(c.TenantID)) + ":"
	if n, err := s.CountShieldCheckRequests(t.Context(), prefix, 1024); err != nil || n != 1 {
		t.Fatal("pending must be search visible", n, err)
	}
	if n, err := s.CountShieldCheckRequests(t.Context(), prefix+"other", 1024); err != nil || n != 0 {
		t.Fatal("prefix leaked", n, err)
	}
	duplicate, err := controller.Request(t.Context(), c)
	if err != nil || duplicate.ID != first.ID || !duplicate.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("retry changed request", err)
	}
	c.Reason = "different"
	if _, err := controller.Request(t.Context(), c); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("same op payload conflict", err)
	}
	c.Reason = first.Command.Reason
	reopened, err := shieldcheck.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetRequest(t.Context(), c.TenantID, c.AlertID, first.ID)
	if err != nil || got.Request.State != "pending" {
		t.Fatal("request restart", err)
	}
	controller = lifecycleprocess.NewShieldRequests(reopened, lock, repo.GetAlert, checker)
	lock.busy = true
	if err := controller.Execute(t.Context(), first); !errors.Is(err, scheduler.ErrLockBusy) {
		t.Fatal(err)
	}
	lock.busy = false
	if checker.calls != 0 {
		t.Fatal("checked without lease")
	}
	checker.err = scheduler.ErrLockBusy
	if err := controller.Execute(t.Context(), first); !errors.Is(err, scheduler.ErrLockBusy) {
		t.Fatal(err)
	}
	got, err = reopened.GetRequest(t.Context(), c.TenantID, c.AlertID, first.ID)
	if err != nil || got.Request.State != "pending" {
		t.Fatal("busy request became terminal", err)
	}
	checker.err = nil
	if err := controller.Execute(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	count := checker.calls
	if err := controller.Execute(t.Context(), first); err != nil || checker.calls != count {
		t.Fatal("terminal executed again", err)
	}
	duplicate, err = controller.Request(t.Context(), c)
	if err != nil || duplicate.State != "completed" {
		t.Fatal(duplicate, err)
	}
	refreshShieldFixture(t, s)
	if n, err := s.CountShieldCheckRequests(t.Context(), prefix, 1024); err != nil || n != 0 {
		t.Fatal("terminal work flag", n, err)
	}
	if p, err := reopened.List(t.Context(), c.TenantID, c.AlertID, "", 1); err != nil || len(p.Items) != 1 || p.Items[0].State != "completed" {
		t.Fatal("terminal history missing", p, err)
	}
	for _, state := range []string{"failed", "superseded"} {
		c.OperationID = state
		checker.outcome = state
		checker.err = errors.New("private backend detail")
		r, err := controller.Request(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.Execute(t.Context(), r); err != nil {
			t.Fatal("valid result should finish", err)
		}
		got, err := reopened.GetRequest(t.Context(), c.TenantID, c.AlertID, r.ID)
		if err != nil || got.Request.State != state {
			t.Fatal(got, err)
		}
		if err := reopened.SaveLatest(t.Context(), *got.Request.Result); err != nil {
			t.Fatal(err)
		}
		latest, err := j.Latest(t.Context(), c.TenantID, c.AlertID)
		if err != nil || latest.RequestID != r.ID {
			t.Fatal("latest exact GET", latest, err)
		}
	}
	c.OperationID = "stale"
	c.ExpectedRevision++
	if _, err := controller.Request(t.Context(), c); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale new request accepted", err)
	}
	if _, err := reopened.GetRequest(t.Context(), "other", c.AlertID, first.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("request tenant leak", err)
	}
	refreshShieldFixture(t, s)
	if p, err := reopened.Work(t.Context(), "", 16); err != nil || len(p.Items) != 0 {
		t.Fatal("completed scans", p, err)
	}
	if err := s.Put(t.Context(), "shield_requests", "invalid", "", json.RawMessage(`{"state":"unknown"}`)); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("unknown state indexed", err)
	}
}

func TestShieldRequestQueriesRejectIncompleteSearch(t *testing.T) {
	for _, body := range []string{`{}`, `{"timed_out":true,"hits":{"hits":[],"total":{"value":0,"relation":"eq"}}}`, `{"_shards":{"failed":1},"hits":{"hits":[],"total":{"value":0,"relation":"eq"}}}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			s, err := storage.Open(t.Context(), config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{srv.URL}}}, "shield-partial")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.CountShieldCheckRequests(t.Context(), "tenant:", 1024); err == nil {
				t.Fatal("partial count accepted")
			}
			if _, err := s.ListShieldCheckRequests(t.Context(), "", "", 16); err == nil {
				t.Fatal("partial work accepted")
			}
		})
	}
}

func testLargeShieldDiagnostic(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	at := time.Now().UTC()
	ref := store.PolicyReleaseRef{Kind: "shield", ID: strings.Repeat("p", 80), Version: 1, Digest: strings.Repeat("a", 64)}
	c := shieldcheck.Check{TenantID: "large", AlertID: "alert", Trigger: "hint", StartedAt: at, FinishedAt: at, Report: lifecycle.ShieldCheckReport{ObservedRevision: 1, ResultRevision: 1, CheckedAt: at, Outcome: "partial", RemainingBindings: 1, Decision: &store.ShieldDecision{Severity: "warning"}}}
	for range 256 {
		c.Report.Decision.Steps = append(c.Report.Decision.Steps, store.ShieldStep{Policy: ref, Outcome: "skipped", ReasonCode: strings.Repeat("s", 80)})
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) <= 64<<10 || len(raw) > shieldcheck.MaxDocumentBytes {
		t.Fatal("fixture must exercise expanded bounded diagnostic", len(raw), err)
	}
	j, err := shieldcheck.NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SaveLatest(t.Context(), c); err != nil {
		t.Fatal("large bounded hint record rejected", err)
	}
	restored, err := shieldcheck.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := restored.Latest(t.Context(), c.TenantID, c.AlertID)
	if err != nil || !reflect.DeepEqual(saved, c) {
		t.Fatal("large hint diagnostic changed on reopen", err)
	}
}
