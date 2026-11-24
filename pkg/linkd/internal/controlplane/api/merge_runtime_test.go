// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store/storetest"
)

func TestMergeWindowViewDoesNotCapLargeMemberSets(t *testing.T) {
	for _, count := range []int{5000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			w := redisstate.MergeWindow{GroupCount: 2, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("a", 64)}}
			for i := range count {
				w.Members = append(w.Members, redisstate.MergeMember{Groups: []int{i % 2}, Committed: true})
			}
			view, err := windowView(w, true)
			if err != nil || view.MemberCount != count || view.CommittedCount != count || len(view.Members) != count || view.GroupCounts[0]+view.GroupCounts[1] != count {
				t.Fatal("large window view truncated members", view.MemberCount, err)
			}
		})
	}
}

func TestMergeRuntimeLargeWindowReadsAcrossRealHTTP(t *testing.T) {
	const count = 10000
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	w := redisstate.MergeWindow{ID: strings.Repeat("b", 64), TenantID: "tenant", GroupCount: 2, StartedAtMillis: at.UnixMilli(), DeadlineMillis: at.Add(time.Minute).UnixMilli(), Revision: 2, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("a", 64)}, Frozen: &redisstate.MergeVerdict{Outcome: "succeeded", OperationID: strings.Repeat("c", 64), AtMillis: at.Add(time.Minute).UnixMilli()}}
	for i := range count {
		id := fmt.Sprintf("%s-%05d", strings.Repeat("m", 100), i)
		w.Members = append(w.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: id, EventID: fmt.Sprintf("event-%d", i), EventSourceID: "source", Fingerprint: fmt.Sprintf("fp-%d", i), Severity: "warning"}, Groups: []int{i % 2}, FirstAtMillis: at.UnixMilli(), Committed: true})
		w.Frozen.MemberIDs = append(w.Frozen.MemberIDs, id)
	}
	a, _, _ := queryAPI(t, &queryWindows{window: w})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	for _, detail := range []bool{false, true} {
		path := "/api/v1/policy-runtime/merge/windows"
		if detail {
			path += "/" + w.ID
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path+"?bk_tenant_id=tenant", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Internal-Token", testJWT(t, "admin"))
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || len(raw) <= 1<<20 || len(raw) > 32<<20 {
			t.Fatal("large window rejected by old HTTP response budget", detail, response.StatusCode, len(raw), err)
		}
		var actual mergeWindowView
		if detail {
			err = json.Unmarshal(raw, &actual)
		} else {
			var page struct {
				Items []mergeWindowView `json:"items"`
			}
			err = json.Unmarshal(raw, &page)
			if len(page.Items) != 1 {
				t.Fatal("large summary page lost window", len(page.Items))
			}
			actual = page.Items[0]
		}
		if err != nil || actual.MemberCount != count || len(actual.Frozen.MemberIDs) != count || (detail && len(actual.Members) != count) {
			t.Fatal("large HTTP response truncated member set", detail, actual.MemberCount, err)
		}
	}
}

type queryWindows struct {
	window  redisstate.MergeWindow
	err     error
	entered chan struct{}
	release chan struct{}
}

func (s *queryWindows) ReadMergeWindow(ctx context.Context, tenant, id string) (redisstate.MergeWindow, bool, error) {
	if s.entered != nil {
		s.entered <- struct{}{}
		select {
		case <-s.release:
		case <-ctx.Done():
			return redisstate.MergeWindow{}, false, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return redisstate.MergeWindow{}, false, err
	}
	return s.window, s.window.ID == id && s.window.TenantID == tenant, s.err
}

func (s *queryWindows) ListMergeWindows(ctx context.Context, tenant, after string, limit int) (redisstate.MergeWindowPage, error) {
	w, ok, err := s.ReadMergeWindow(ctx, tenant, s.window.ID)
	p := redisstate.MergeWindowPage{Items: []redisstate.MergeWindow{}}
	if ok {
		p.Items = append(p.Items, w)
	}
	return p, err
}

func queryDecision(t *testing.T, j *merge.Journal, tenant, window string) merge.Decision {
	t.Helper()
	spec := json.RawMessage(`{"name":"private-policy-body","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[],"policy":[{"expression":"A","A":{"condition":"term","target_key":"level","target_value":"warning"}}],"merge_cycle":60,"is_cycle_merge":false,"aggregate_fields":[],"new_alarm_config":[{"key":"name","value":"private-parent-name"},{"key":"level","value":"warning"}]}`)
	c, err := policy.Compile(policy.Merge, spec)
	if err != nil {
		t.Fatal(err)
	}
	id, err := domain.MergeDecisionID(tenant, window)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	d := merge.Decision{ID: id, TenantID: tenant, WindowID: window, GroupKey: strings.Repeat("a", 64), Policy: policy.Release{Scope: policy.Scope{TenantID: tenant, Kind: policy.Merge}, ID: "merge", Version: 1, Spec: c.Canonical, Compiled: c.Summary}, FrozenAt: at, StartedAt: at, Deadline: at.Add(time.Minute), Outcome: "succeeded", MemberIDs: []string{"a", "b"}, WaitMemberIDs: []string{"a", "b"}}
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	return saved.Decision
}

func (d *policyDocuments) PutReferences(ctx context.Context, rows map[string]json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for id, raw := range rows {
		key := "merge_relation_refs/" + id
		d.rows[key] = policyDocument{raw: append(json.RawMessage(nil), raw...), version: d.rows[key].version + 1}
	}
	return nil
}

func queryAPI(t *testing.T, w MergeWindows) (*API, *merge.Journal, *policyDocuments) {
	t.Helper()
	docs := &policyDocuments{rows: map[string]policyDocument{}}
	j, err := merge.NewJournal(docs)
	if err != nil {
		t.Fatal(err)
	}
	return &API{MergeRuntime: NewMergeRuntime(j, w), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}, j, docs
}

func TestMergeRuntimeScopedPagesAndSafeSummaries(t *testing.T) {
	a, j, docs := queryAPI(t, nil)
	d := queryDecision(t, j, "tenant", strings.Repeat("b", 64))
	queryDecision(t, j, "tenant", strings.Repeat("c", 64))
	queryDecision(t, j, "other", strings.Repeat("b", 64))
	member := storetest.Alert("tenant", "a", "event-a", "fp-a", "warning")
	member.Content = "private-alert-body"
	if _, err := j.Capture(t.Context(), "tenant", d.ID, member); err != nil {
		t.Fatal(err)
	}
	before := len(docs.rows)
	h := a.Handler()
	base := "/api/v1/policy-runtime/merge/decisions"
	w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant&phase=completed&limit=1", "")
	var page struct {
		Items []mergeDecisionView `json:"items"`
		Next  string              `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 0 || page.Next == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, suffix := range []string{"&bk_tenant_id=other", "&phase=capturing"} {
		path := base + "?bk_tenant_id=tenant&phase=completed&limit=1&after=" + url.QueryEscape(page.Next) + suffix
		if w := policyHTTP(t, h, "GET", path, ""); w.Code != 400 {
			t.Fatal("cross-query cursor", w.Code)
		}
	}
	for _, path := range []string{base + "?bk_tenant_id=tenant", base + "/" + d.ID + "?bk_tenant_id=tenant", base + "/" + d.ID + "/members?bk_tenant_id=tenant"} {
		w := policyHTTP(t, h, "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(docs.rows) != before {
		t.Fatal("query wrote documents")
	}
	w = policyHTTP(t, h, "GET", base+"/"+d.ID+"/members/a?bk_tenant_id=tenant", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private-alert-body") {
		t.Fatal("explicit frozen snapshot missing", w.Code, w.Body.String())
	}
	for _, path := range []string{base + "/" + d.ID + "/members/unrelated?bk_tenant_id=tenant", base + "/" + d.ID + "/members/a?bk_tenant_id=other"} {
		if w := policyHTTP(t, h, "GET", path, ""); w.Code != 404 {
			t.Fatal("snapshot scope", w.Code)
		}
	}
	for _, test := range []struct {
		path string
		code int
	}{
		{base + "/" + d.ID + "?bk_tenant_id=other", 404}, {base + "?bk_tenant_id=tenant&limit=5", 400}, {base, 400},
		{base + "?bk_tenant_id=tenant&after=bad", 400}, {base + "?bk_tenant_id=tenant&url=bad", 400},
		{"/api/v1/policy-runtime/merge/relations?bk_tenant_id=tenant", 400},
		{"/api/v1/policy-runtime/merge/windows?bk_tenant_id=tenant", 503},
	} {
		if w := policyHTTP(t, h, "GET", test.path, ""); w.Code != test.code {
			t.Fatal(test.path, w.Code, w.Body.String())
		}
	}
	if w := policyHTTP(t, h, "POST", base+"?bk_tenant_id=tenant", ""); w.Code != 405 {
		t.Fatal("write route exposed", w.Code)
	}
	req := httptest.NewRequestWithContext(t.Context(), "GET", base+"?bk_tenant_id=tenant", nil)
	req.Header.Set("Authorization", "Bearer worker")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal("worker bypassed management auth")
	}
	docs.fail = true
	if w := policyHTTP(t, h, "GET", base+"/"+d.ID+"?bk_tenant_id=tenant", ""); w.Code != 500 || strings.Contains(w.Body.String(), "secret-driver") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMergeRuntimeWindowMissingIsNotFailedDecision(t *testing.T) {
	windows := &queryWindows{}
	a, _, _ := queryAPI(t, windows)
	h := a.Handler()
	path := "/api/v1/policy-runtime/merge/windows/" + strings.Repeat("b", 64) + "?bk_tenant_id=tenant"
	if w := policyHTTP(t, h, "GET", path, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	windows.err = errors.New("secret-redis-address")
	if w := policyHTTP(t, h, "GET", path, ""); w.Code != 500 || strings.Contains(w.Body.String(), "secret-") {
		t.Fatal(w.Code, w.Body.String())
	}
	windows.err = nil
	windows.window = redisstate.MergeWindow{ID: strings.Repeat("b", 64), TenantID: "tenant", GroupCount: -1}
	if w := policyHTTP(t, h, "GET", path, ""); w.Code != 503 {
		t.Fatal("invalid window did not fail closed", w.Code)
	}
}

func TestMergeRuntimeConcurrentCapacityAndCancellation(t *testing.T) {
	windows := &queryWindows{entered: make(chan struct{}, 2), release: make(chan struct{})}
	a, _, _ := queryAPI(t, windows)
	h := a.Handler()
	path := "/api/v1/policy-runtime/merge/windows?bk_tenant_id=tenant"
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if w := policyHTTP(t, h, "GET", path, ""); w.Code != 200 {
				t.Error(w.Code)
			}
		})
	}
	for range 2 {
		select {
		case <-windows.entered:
		case <-time.After(time.Second):
			t.Fatal("query did not start")
		}
	}
	if w := policyHTTP(t, h, "GET", path, ""); w.Code != 429 {
		t.Error("capacity", w.Code)
	}
	close(windows.release)
	wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	req.Header.Set("Internal-Token", testJWT(t, "admin"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatal(rec.Code)
	}
}
