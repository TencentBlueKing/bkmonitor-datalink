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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/logging"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type alertSearchFunc func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error)

func (f alertSearchFunc) SearchAlerts(ctx context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
	return f(ctx, q)
}

func alertSearchHandler(t *testing.T, reader AlertSearcher, cfg config.AlertSearchConfig) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &logs)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{AlertSearch: NewAlertSearch(reader, cfg, logger), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
	return a.Handler(), &logs
}

func TestAlertSearchHTTPPreservesDSLAndPagination(t *testing.T) {
	t.Parallel()
	dsl := `{"bool":{"should":[{"term":{"labels.strategy_id":9007199254740993}},{"term":{"severity":"fatal"}}]}}`
	calls := 0
	reader := alertSearchFunc(func(ctx context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("missing query deadline")
		}
		if q.TenantID != "tenant" {
			t.Fatal(q)
		}
		if calls == 1 && (string(q.Query) != dsl || q.After != "a" || q.Limit != 1) {
			t.Fatal(q)
		}
		if calls == 2 && (len(q.Query) != 0 || q.Limit != store.DefaultPageSize) {
			t.Fatal(q)
		}
		if calls == 3 && q.Limit != store.MaxPageSize {
			t.Fatal(q)
		}
		return store.AlertSearchPage{Items: []domain.Alert{storetest.Alert("tenant", "b", "event", "fingerprint", "warning")}, Next: "b"}, nil
	})
	handler, _ := alertSearchHandler(t, reader, config.AlertSearchConfig{})
	for _, body := range []string{`{"bk_tenant_id":"tenant","query":` + dsl + `,"after":"a","limit":1}`, `{"bk_tenant_id":"tenant"}`, `{"bk_tenant_id":"tenant","limit":500}`} {
		w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", body)
		var page store.AlertSearchPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].AlertID != "b" || page.Next != "b" {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || strings.Contains(w.Body.String(), "_index") || strings.Contains(w.Body.String(), "Version") {
			t.Fatal(w.Header(), w.Body.String())
		}
	}
}

func TestAlertSearchHTTPRejectsInvalidRequestsAndWorkerToken(t *testing.T) {
	t.Parallel()
	reader := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		t.Error("invalid request reached storage")
		return store.AlertSearchPage{}, nil
	})
	handler, _ := alertSearchHandler(t, reader, config.AlertSearchConfig{})
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"bk_tenant_id":""}`, `{"bk_tenant_id":"` + strings.Repeat("t", 65) + `"}`,
		`{"bk_tenant_id":"tenant","limit":0}`, `{"bk_tenant_id":"tenant","limit":-1}`, `{"bk_tenant_id":"tenant","limit":501}`,
		`{"bk_tenant_id":"tenant","query":null}`, `{"bk_tenant_id":"tenant","query":[]}`, `{"bk_tenant_id":"tenant","query":"status:active"}`,
		`{"bk_tenant_id":"tenant","after":"` + strings.Repeat("a", domain.EntityIDMaxBytes+1) + `"}`,
		`{"bk_tenant_id":"tenant","index":"other"}`, `{"bk_tenant_id":"tenant","aggs":{}}`, `{"bk_tenant_id":"tenant","size":10}`,
		`{"bk_tenant_id":"tenant"} {}`, `{"bk_tenant_id":"tenant"} garbage`, `{"bk_tenant_id":"tenant","query":{`,
	} {
		w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", body)
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, authorization := range []string{"", "Bearer worker"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/alerts/search", strings.NewReader(`{"bk_tenant_id":"tenant"}`))
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	handler, _ = alertSearchHandler(t, reader, config.AlertSearchConfig{MaxRequestBytes: 32})
	for _, body := range []string{`{"bk_tenant_id":"tenant","query":{"match_all":{}}}`, `{"bk_tenant_id":"tenant"}` + strings.Repeat(" ", 32)} {
		if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", body); w.Code != 413 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestAlertSearchHTTPFailuresAndEmptyPage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code int
	}{
		{"empty", nil, 200},
		{"invalid DSL", fmt.Errorf("%w: rejected", store.ErrInvalidArgument), 400},
		{"storage failure", errors.New("private storage address"), 502},
		{"deadline", context.DeadlineExceeded, 504},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
				return store.AlertSearchPage{Items: []domain.Alert{}}, test.err
			})
			handler, logs := alertSearchHandler(t, reader, config.AlertSearchConfig{})
			w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`)
			if w.Code != test.code || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if test.err == nil && strings.TrimSpace(w.Body.String()) != `{"items":[],"next":""}` {
				t.Fatal(w.Body.String())
			}
			if test.err != nil && (!strings.Contains(logs.String(), "alert_search") || !strings.Contains(logs.String(), "tenant") || !strings.Contains(logs.String(), test.err.Error())) {
				t.Fatal("missing failure context", logs.String())
			}
		})
	}
	handler, _ := alertSearchHandler(t, nil, config.AlertSearchConfig{})
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`); w.Code != 501 {
		t.Fatal(w.Code)
	}
	handler = (&API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}).Handler()
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
	reader := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return store.AlertSearchPage{Items: []domain.Alert{storetest.Alert("tenant", "a", "event", "fingerprint", "warning")}}, nil
	})
	handler, _ = alertSearchHandler(t, reader, config.AlertSearchConfig{MaxResponseBytes: 16})
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`); w.Code != 502 || strings.Contains(w.Body.String(), "fingerprint") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAlertSearchHTTPConcurrencyAndCancellation(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 1)
	reader := alertSearchFunc(func(ctx context.Context, _ store.AlertSearchRequest) (store.AlertSearchPage, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return store.AlertSearchPage{}, ctx.Err()
	})
	handler, _ := alertSearchHandler(t, reader, config.AlertSearchConfig{MaxConcurrent: 1})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alerts/search", strings.NewReader(`{"bk_tenant_id":"tenant"}`))
	r.Header.Set("Internal-Token", testJWT(t, "admin"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not start")
	}
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`); w.Code != 429 {
		t.Fatal(w.Code)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("query ignored cancellation")
	}
	// 取消后必须释放槽位，后续请求能进入后端并被自身 Context 取消。
	w := httptest.NewRecorder()
	r = httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alerts/search", strings.NewReader(`{"bk_tenant_id":"tenant"}`))
	r.Header.Set("Internal-Token", testJWT(t, "admin"))
	handler.ServeHTTP(w, r)
	if w.Code != 502 || len(entered) != 1 {
		t.Fatal("query slot leaked after cancellation", w.Code)
	}
}

func TestAlertSearchHTTPEnforcesConfiguredDeadline(t *testing.T) {
	t.Parallel()
	reader := alertSearchFunc(func(ctx context.Context, _ store.AlertSearchRequest) (store.AlertSearchPage, error) {
		<-ctx.Done()
		return store.AlertSearchPage{}, ctx.Err()
	})
	handler, _ := alertSearchHandler(t, reader, config.AlertSearchConfig{TimeoutSeconds: 1})
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/search", `{"bk_tenant_id":"tenant"}`); w.Code != 504 {
		t.Fatal(w.Code, w.Body.String())
	}
}
