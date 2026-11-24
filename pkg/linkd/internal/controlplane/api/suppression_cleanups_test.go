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
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

type cleanupReader struct {
	row suppressioncleanup.Record
	err error
}

func (r *cleanupReader) Get(context.Context, string, string) (suppressioncleanup.Record, string, error) {
	return r.row, "1", r.err
}

func (r *cleanupReader) List(_ context.Context, _ string, after string, _ int) (suppressioncleanup.Page, error) {
	if after != "" {
		return suppressioncleanup.Page{Items: []suppressioncleanup.Record{}}, r.err
	}
	return suppressioncleanup.Page{Items: []suppressioncleanup.Record{r.row}, Next: r.row.ID}, r.err
}

func TestSuppressionCleanupHistoryScopeCursorAndFailure(t *testing.T) {
	c := suppressioncleanup.Cause{TenantID: "tenant", SourceID: "source", Fingerprint: "fp", Trigger: "alert_terminal", AlertID: "alert", Revision: 2, Status: domain.AlertStatusClosed}
	reader := &cleanupReader{row: suppressioncleanup.Record{ID: c.ID(), Cause: c, State: "pending", StartedAt: time.Now()}}
	a := &API{SuppressionCleanups: NewSuppressionCleanups(reader), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := a.Handler()
	base := "/api/v1/policy-runtime/suppression/cleanups"
	call := func(path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		r.Header.Set("Internal-Token", testJWT(t, "admin"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
		return w
	}
	first := call(base+"?bk_tenant_id=tenant&alert_id=other", 200)
	var page struct {
		Items []suppressioncleanup.Record `json:"items"`
		Next  string                      `json:"next"`
	}
	if json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Items) != 0 || page.Next == "" {
		t.Fatal("filtered empty page lost cursor")
	}
	call(base+"?bk_tenant_id=tenant&alert_id=other&after="+page.Next, 200)
	call(base+"?bk_tenant_id=tenant&after="+page.Next, 400)
	for _, q := range []string{"?bk_tenant_id=tenant&limit=5", "?bk_tenant_id=tenant&unknown=1", "?bk_tenant_id=tenant&state=failed", "?bk_tenant_id=tenant&after=bad"} {
		call(base+q, 400)
	}
	detail := call(base+"/"+c.ID()+"?bk_tenant_id=tenant", 200)
	if detail.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable history")
	}
	call(base+"/"+c.ID()+"?bk_tenant_id=other", 403)
	reader.row.State = "completed"
	call(base+"?bk_tenant_id=tenant", 503)
	reader.row.State = "pending"
	reader.err = policy.ErrInvalid
	call(base+"?bk_tenant_id=tenant", 503)
	reader.err = errors.New("private connection detail")
	w := call(base+"?bk_tenant_id=tenant", 500)
	if w.Body.String() == "private connection detail" {
		t.Fatal("raw error exposed")
	}
	reader.err = nil
	a.SuppressionCleanups.slots <- struct{}{}
	a.SuppressionCleanups.slots <- struct{}{}
	call(base+"?bk_tenant_id=tenant", 429)
	<-a.SuppressionCleanups.slots
	<-a.SuppressionCleanups.slots
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequestWithContext(t.Context(), "GET", base+"?bk_tenant_id=tenant", nil))
	if unauth.Code != 401 {
		t.Fatal("anonymous history", unauth.Code)
	}
}
