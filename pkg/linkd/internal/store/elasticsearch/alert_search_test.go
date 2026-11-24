// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type alertSearchRouter struct {
	Router
	history string
}

func (r alertSearchRouter) TerminalAlertTargets(context.Context) ([]string, error) {
	return []string{r.history}, nil
}

func TestAlertSearchElasticsearchScopesDSLAndPagesAcrossActiveAndHistory(t *testing.T) {
	t.Parallel()
	router, err := NewStaticRouter("search-test")
	if err != nil {
		t.Fatal(err)
	}
	dsl := json.RawMessage(`{"bool":{"should":[{"term":{"labels.strategy_id":9007199254740993}},{"term":{"bk_tenant_id":"other"}}]}}`)
	calls := 0
	repo, err := New(transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/search-test-history,search-test-alerts/_search" || r.URL.Query().Get("allow_partial_search_results") != "false" {
			t.Fatal(r.Method, r.URL.String())
		}
		var body struct {
			Size     int `json:"size"`
			Collapse struct {
				Field string `json:"field"`
			} `json:"collapse"`
			Sort  []map[string]string `json:"sort"`
			Query struct {
				Bool struct {
					Filter []json.RawMessage `json:"filter"`
				} `json:"bool"`
			} `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		filters := body.Query.Bool.Filter
		if body.Size != 3 || body.Collapse.Field != "alert_id" || !reflect.DeepEqual(body.Sort, []map[string]string{{"alert_id": "asc"}}) || len(filters) < 2 || string(filters[0]) != `{"term":{"bk_tenant_id":"tenant"}}` || string(filters[1]) != string(dsl) {
			t.Fatalf("invalid search body: %+v", body)
		}
		ids := []string{"a", "b", "c"}
		if calls == 2 {
			if len(filters) != 3 || string(filters[2]) != `{"range":{"alert_id":{"gt":"b"}}}` {
				t.Fatal("missing page anchor", filters)
			}
			ids = []string{"c"}
		}
		hits := make([]any, 0, len(ids))
		for _, id := range ids {
			a := storetest.Alert("tenant", id, "event", "fingerprint-"+id, "warning")
			source, err := encodeAlertDocument(a)
			if err != nil {
				t.Fatal(err)
			}
			hits = append(hits, map[string]any{"_index": "search-test-alerts", "_id": alertDocumentID(a), "_seq_no": 1, "_primary_term": 1, "_source": json.RawMessage(source)})
		}
		return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": hits}}), nil
	}), alertSearchRouter{Router: router, history: "search-test-history"}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	q := store.AlertSearchRequest{TenantID: "tenant", Query: dsl, Limit: 2}
	page, err := repo.SearchAlerts(t.Context(), q)
	if err != nil || len(page.Items) != 2 || page.Items[0].AlertID != "a" || page.Next != "b" {
		t.Fatal(page, err)
	}
	q.After = page.Next
	page, err = repo.SearchAlerts(t.Context(), q)
	if err != nil || len(page.Items) != 1 || page.Items[0].AlertID != "c" || page.Next != "" || calls != 2 {
		t.Fatal(page, err, calls)
	}
}

func TestAlertSearchElasticsearchFailures(t *testing.T) {
	t.Parallel()
	router, err := NewStaticRouter("search-errors")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
		body   string
		badDSL bool
	}{
		{"unknown DSL", 400, `{"error":{"type":"parsing_exception","reason":"private query value"}}`, true},
		{"storage unavailable", 503, `{"error":{"type":"unavailable_shards_exception","reason":"private query value"}}`, false},
		{"timeout", 200, `{"timed_out":true,"hits":{"hits":[]}}`, false},
		{"partial shards", 200, `{"_shards":{"failed":1},"hits":{"hits":[]}}`, false},
		{"invalid hit", 200, `{"hits":{"hits":[{"_source":{}}]}}`, false},
		{"malformed response", 200, `{`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, err := New(transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			}), router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			page, err := repo.SearchAlerts(t.Context(), store.AlertSearchRequest{TenantID: "tenant", Query: json.RawMessage(`{"match_all":{}}`), Limit: 1})
			if err == nil || len(page.Items) != 0 || errors.Is(err, store.ErrInvalidArgument) != test.badDSL || strings.Contains(err.Error(), "private") {
				t.Fatal(page, err)
			}
		})
	}
}

func TestAlertSearchElasticsearchEmptyAndCanceled(t *testing.T) {
	t.Parallel()
	router, err := NewStaticRouter("search-empty")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	repo, err := New(transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"filter":[{"term":{"bk_tenant_id":"tenant"}}]`) {
			t.Fatal("missing tenant-only match all", string(body))
		}
		return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": []any{}}}), nil
	}), router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	q := store.AlertSearchRequest{TenantID: "tenant", Limit: 1}
	page, err := repo.SearchAlerts(t.Context(), q)
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.Next != "" {
		t.Fatal(page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.SearchAlerts(ctx, q); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("query ignored cancellation", calls, err)
	}
}

// TestAlertSearchElasticsearchIntegration 仅对显式指定的集群创建独立测试索引。
func TestAlertSearchElasticsearchIntegration(t *testing.T) {
	repo, ctx := activeIndexIntegrationRepository(t)
	router := repo.router.(*StaticRouter)
	history := router.alertIndex + "-history"
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = repo.performJSON(cleanup, http.MethodDelete, "/"+history, nil, nil, nil)
		_ = repo.performJSON(cleanup, http.MethodDelete, "/_index_template/"+history, nil, nil, nil)
	})
	if err := repo.EnsureSchema(ctx, SchemaConfig{AlertHistory: TemplateSpec{Name: history, IndexPatterns: []string{history}, Priority: 100, Entity: entityAlertHistory}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureIndex(ctx, history, entityAlertHistory); err != nil {
		t.Fatal(err)
	}
	repo.router = alertSearchRouter{Router: router, history: history}
	for _, row := range []struct{ tenant, id, severity string }{
		{"tenant", "a", "warning"}, {"tenant", "b", "fatal"}, {"tenant", "c", "warning"}, {"other", "d", "fatal"},
	} {
		a := storetest.Alert(row.tenant, row.id, "event", "fingerprint-"+row.id, row.severity)
		if row.id == "b" || row.id == "c" {
			a.Revision = 2
			a.Status = domain.AlertStatusRecovered
			at := a.UpdateAt
			a.EndAt, a.EndType = &at, domain.AlertEndTypeSource
		}
		source, err := encodeAlertDocument(a)
		if err != nil {
			t.Fatal(err)
		}
		// 直接布置当前快照和归档副本，避免把新建仅允许 active 的写入规则混入检索测试。
		if row.id != "c" {
			if err := repo.performJSON(ctx, http.MethodPut, "/"+router.alertIndex+"/_doc/"+alertDocumentID(a), nil, source, nil); err != nil {
				t.Fatal(err)
			}
		}
		if row.id == "b" || row.id == "c" {
			if err := repo.performJSON(ctx, http.MethodPut, "/"+history+"/_doc/"+alertDocumentID(a), nil, source, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repo.performJSON(ctx, http.MethodPost, "/"+router.alertIndex+","+history+"/_refresh", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// should-only 的默认匹配规则必须保留；同时覆盖 bool、term、range 与跨索引副本折叠。
	q := store.AlertSearchRequest{TenantID: "tenant", Limit: 1, Query: json.RawMessage(`{"bool":{"should":[{"term":{"severity":"fatal"}},{"bool":{"filter":[{"term":{"status":"recovered"}},{"range":{"create_at":{"gte":"2026-09-01T00:00:00Z"}}}]}}]}}`)}
	page, err := repo.SearchAlerts(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].AlertID != "b" || page.Next != "b" {
		t.Fatal(page, err)
	}
	q.After = page.Next
	page, err = repo.SearchAlerts(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].AlertID != "c" || page.Next != "" {
		t.Fatal(page, err)
	}
	for _, dsl := range []string{`{"match_all":{}}`, `{"term":{"bk_tenant_id":"other"}}`, `{"query_string":{"query":"*:*"}}`} {
		q.Query, q.After, q.Limit = json.RawMessage(dsl), "", 10
		page, err := repo.SearchAlerts(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dsl, "other") && len(page.Items) != 0 {
			t.Fatal("DSL escaped tenant scope", page)
		}
		for _, a := range page.Items {
			if a.BKTenantID != "tenant" {
				t.Fatal("cross-tenant search result", a)
			}
		}
	}
	q.Query = json.RawMessage(`{"unknown_query":{}}`)
	if _, err := repo.SearchAlerts(ctx, q); !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatal("invalid DSL was not classified", err)
	}
}
