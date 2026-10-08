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
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
)

type suppressionReader struct {
	window     redisstate.SuppressionWindow
	next       string
	err        error
	cross      bool
	entered    chan struct{}
	members    []redisstate.SuppressionMember
	memberNext string
}

func (s *suppressionReader) ReadSuppressionWindow(ctx context.Context, tenant, kind, id string) (redisstate.SuppressionWindow, bool, error) {
	if s.entered != nil {
		s.entered <- struct{}{}
		<-ctx.Done()
	}
	if err := ctx.Err(); err != nil {
		return redisstate.SuppressionWindow{}, false, err
	}
	v := s.window
	if s.cross {
		v.TenantID = "other"
	}
	return v, s.window.ID == id && s.window.TenantID == tenant && s.window.Kind == kind, s.err
}

func (s *suppressionReader) ListSuppressionWindows(ctx context.Context, tenant, kind, _ string, _ int) (redisstate.SuppressionWindowPage, error) {
	v, found, err := s.ReadSuppressionWindow(ctx, tenant, kind, s.window.ID)
	page := redisstate.SuppressionWindowPage{Items: []redisstate.SuppressionWindow{}, Next: s.next}
	if found {
		page.Items = append(page.Items, v)
	}
	return page, err
}

func (s *suppressionReader) ListSuppressionMembers(ctx context.Context, _, _, _, epoch, _ string, _ int) (redisstate.SuppressionMemberPage, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.SuppressionMemberPage{}, err
	}
	return redisstate.SuppressionMemberPage{Epoch: epoch, Items: s.members, Next: s.memberNext, ObservedAtMillis: time.Now().UnixMilli()}, s.err
}

func suppressionAPI(t *testing.T) (*API, *suppressionReader) {
	t.Helper()
	count := 3
	now := time.Now().UnixMilli()
	r := &suppressionReader{window: redisstate.SuppressionWindow{ID: "eb127310d9f368370acd0a6fe199de992e60b306d1b84d22a767f94ce3a32a70:0e966c7947469e8e4efa1778639ac1d184f16b03b8d2e8731c3e6d71641a28e5", TenantID: "tenant", Kind: "clip", Policy: domain.PolicyVersion{ID: "policy", Version: 1, Digest: strings.Repeat("a", 64)}, SourceID: "source", Fingerprint: "fingerprint", Epoch: "event-1", OwnerAlertID: "owner", State: "retained", ObservedAtMillis: now, RetentionMillis: 60000, DurationSeconds: 60, Threshold: 3, Count: &count, MemberCount: 3, LastEvaluatedAtMillis: now - 1000}, members: []redisstate.SuppressionMember{{EventID: "event-1", SourceID: "source", Fingerprint: "fingerprint", AtMillis: now - 1000}}}
	if err := r.window.Validate(); err != nil {
		t.Fatal(err)
	}
	a := &API{SuppressionRuntime: NewSuppressionRuntime(r), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
	return a, r
}

func TestSuppressionRuntimeScopesAndReadOnlyRoutes(t *testing.T) {
	a, reader := suppressionAPI(t)
	h := a.Handler()
	base := "/api/v1/policy-runtime/suppression/clip"
	reader.next = strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant&policy_id=absent&limit=1", "")
	var page struct {
		Items []redisstate.SuppressionWindow `json:"items"`
		Next  string                         `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 0 || page.Next == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, change := range []string{"bk_tenant_id=other&policy_id=absent", "bk_tenant_id=tenant&policy_id=changed"} {
		if w := policyHTTP(t, h, "GET", base+"?"+change+"&after="+url.QueryEscape(page.Next), ""); w.Code != 400 {
			t.Fatal("cursor crossed scope", w.Code)
		}
	}
	for _, suffix := range []string{"?bk_tenant_id=tenant&event_source_id=source&owner_alert_id=owner", "/" + reader.window.ID + "?bk_tenant_id=tenant", "/" + reader.window.ID + "/members?bk_tenant_id=tenant&epoch=event-1"} {
		w := policyHTTP(t, h, "GET", base+suffix, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(suffix, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"", "?bk_tenant_id=tenant&url=secret", "?bk_tenant_id=tenant&limit=5", "?bk_tenant_id=tenant&bk_tenant_id=tenant", "?bk_tenant_id=tenant&epoch=e", "/bad?bk_tenant_id=tenant", "/" + reader.window.ID + "?bk_tenant_id=tenant&limit=1", "/" + reader.window.ID + "/members?bk_tenant_id=tenant", "/" + reader.window.ID + "/members?bk_tenant_id=tenant&epoch=e&owner_alert_id=owner"} {
		if w := policyHTTP(t, h, "GET", base+suffix, ""); w.Code != 400 {
			t.Fatal(suffix, w.Code)
		}
	}
	if w := policyHTTP(t, h, "GET", base+"/"+reader.window.ID+"?bk_tenant_id=other", ""); w.Code != 404 {
		t.Fatal("tenant leak", w.Code)
	}
	if w := policyHTTP(t, h, "GET", base+"/"+reader.window.ID+"/members?bk_tenant_id=tenant&epoch=old", ""); w.Code != 409 {
		t.Fatal("changed generation accepted", w.Code)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if w := policyHTTP(t, h, method, base+"/"+reader.window.ID+"?bk_tenant_id=tenant", ""); w.Code != 405 {
			t.Fatal("write route exposed", w.Code)
		}
	}
	for _, header := range []string{"", "worker"} {
		req := httptest.NewRequestWithContext(t.Context(), "GET", base+"?bk_tenant_id=tenant", nil)
		req.Header.Set("X-Linkd-Token", header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatal("query bypassed management JWT", w.Code)
		}
	}
}

func TestSuppressionRuntimeRejectsInvalidBackendResults(t *testing.T) {
	a, r := suppressionAPI(t)
	h := a.Handler()
	base := "/api/v1/policy-runtime/suppression/clip"
	detail := base + "/" + r.window.ID + "?bk_tenant_id=tenant"
	r.cross = true
	for _, path := range []string{base + "?bk_tenant_id=tenant", detail} {
		if w := policyHTTP(t, h, "GET", path, ""); w.Code != 403 {
			t.Fatal("cross scope result", w.Code)
		}
	}
	r.cross = false
	r.window.Count = nil
	if w := policyHTTP(t, h, "GET", detail, ""); w.Code != 503 {
		t.Fatal("missing count reported zero", w.Code)
	}
	count := 3
	r.window.Count = &count
	r.err = errors.New("redis://secret-credential")
	if w := policyHTTP(t, h, "GET", detail, ""); w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	r.err = redisstate.ErrState
	if w := policyHTTP(t, h, "GET", detail, ""); w.Code != 503 {
		t.Fatal("broken state unavailable", w.Code)
	}
	r.err = nil
	r.memberNext = "99"
	memberPath := base + "/" + r.window.ID + "/members?bk_tenant_id=tenant&epoch=event-1"
	if w := policyHTTP(t, h, "GET", memberPath, ""); w.Code != 503 {
		t.Fatal("unbounded member cursor", w.Code)
	}
	r.memberNext = ""
	r.members[0].SourceID = "another"
	if w := policyHTTP(t, h, "GET", memberPath, ""); w.Code != 403 {
		t.Fatal("clip member changed scope", w.Code)
	}
	r.err = policy.ErrConflict
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 409 {
		t.Fatal("expired cursor reported empty", w.Code)
	}
}

func TestSuppressionRuntimeConcurrencyAndCancellation(t *testing.T) {
	a, r := suppressionAPI(t)
	r.entered = make(chan struct{}, 2)
	h := a.Handler()
	path := "/api/v1/policy-runtime/suppression/clip/" + r.window.ID + "?bk_tenant_id=tenant"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan int, 2)
	for range 2 {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.Header.Set("Internal-Token", testJWT(t, "admin"))
		go func() { w := httptest.NewRecorder(); h.ServeHTTP(w, req); results <- w.Code }()
	}
	for range 2 {
		select {
		case <-r.entered:
		case <-time.After(time.Second):
			t.Fatal("query did not enter")
		}
	}
	if w := policyHTTP(t, h, "GET", path, ""); w.Code != 429 {
		t.Fatal("unbounded query concurrency", w.Code)
	}
	cancel()
	for range 2 {
		select {
		case code := <-results:
			if code != 408 {
				t.Fatal("cancellation lost", code)
			}
		case <-time.After(time.Second):
			t.Fatal("query ignored cancellation")
		}
	}
	if len(a.SuppressionRuntime.slots) != 0 {
		t.Fatal("query leaked slot")
	}
}
