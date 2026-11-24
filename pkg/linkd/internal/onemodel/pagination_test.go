// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func pageBody(ids ...string) string {
	hits := make([]any, 0, len(ids))
	for _, id := range ids {
		hits = append(hits, map[string]any{"_source": map[string]any{"bk_tenant_id": "t", "model_id": "host", "model_inst_id": id, "entity_uid": "host|" + id, "attributes": map[string]any{}}, "sort": []any{id, "instances"}})
	}
	body, _ := json.Marshal(map[string]any{"pit_id": "renewed", "hits": map[string]any{"hits": hits}})
	return string(body)
}

func pagerFor(t *testing.T, fn roundTripFunc) *Pager {
	t.Helper()
	c, err := NewClient(ClientConfig{Transport: fn})
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPager(c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func pageResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPagerSnapshotAndClose(t *testing.T) {
	calls, closed := 0, 0
	p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			closed++
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "renewed") {
				t.Fatal("must close latest PIT")
			}
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_pit") {
			if r.URL.Query().Get("keep_alive") != "1m" {
				t.Fatal("missing TTL")
			}
			return pageResponse(`{"id":"opened"}`), nil
		}
		calls++
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		for _, want := range []string{`"bk_tenant_id":"t"`, `"model_id":"host"`, `"model_inst_id":"asc"`, `"_index":"asc"`, `"size":3`} {
			if !strings.Contains(body, want) {
				t.Fatalf("query missing %s: %s", want, body)
			}
		}
		if strings.Contains(body, "_shard_doc") {
			t.Fatal("unsupported ES 7.10 sort")
		}
		if calls == 1 {
			return pageResponse(pageBody("1", "2", "3")), nil
		}
		if !strings.Contains(body, `"search_after":["2","instances"]`) || !strings.Contains(body, `"id":"renewed"`) {
			t.Fatalf("lost cursor: %s", body)
		}
		return pageResponse(pageBody("3")), nil
	})
	first, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 2})
	if err != nil || len(first.Instances) != 2 || first.NextCursor == "" || closed != 0 {
		t.Fatalf("first=%+v err=%v closed=%d", first, err, closed)
	}
	last, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(last.Instances) != 1 || last.NextCursor != "" || closed != 1 {
		t.Fatalf("last=%+v err=%v closed=%d", last, err, closed)
	}
}

func TestPagerCursorIsolationExpiryAndExplicitClose(t *testing.T) {
	calls, closed := 0, 0
	p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == http.MethodDelete {
			closed++
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_pit") {
			return pageResponse(`{"id":"opened"}`), nil
		}
		return pageResponse(pageBody("1", "2")), nil
	})
	first, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	originalCalls := calls
	for _, tc := range []struct {
		name, tenant string
		q            PageQuery
	}{
		{"tenant", "other", PageQuery{ModelID: "host", Limit: 1, Cursor: first.NextCursor}},
		{"model", "t", PageQuery{ModelID: "other", Limit: 1, Cursor: first.NextCursor}},
		{"limit", "t", PageQuery{ModelID: "host", Limit: 2, Cursor: first.NextCursor}},
		{"filter", "t", PageQuery{ModelID: "host", Limit: 1, Cursor: first.NextCursor, Where: Filter{Field: "model_inst_id", Type: InstanceAttributeKeyword, Operator: "eq", Value: "1"}}},
		{"signature", "t", PageQuery{ModelID: "host", Limit: 1, Cursor: "x" + first.NextCursor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Search(t.Context(), tc.tenant, tc.q); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := p.Close(t.Context(), "other", first.NextCursor); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross tenant close=%v", err)
	}
	if calls != originalCalls {
		t.Fatal("invalid cursor reached ES")
	}
	if err := p.Close(t.Context(), "t", first.NextCursor); err != nil || closed != 1 {
		t.Fatalf("close=%v count=%d", err, closed)
	}
	now := time.Now().Add(2 * time.Minute)
	p.now = func() time.Time { return now }
	if _, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expiry=%v", err)
	}
	if err := p.Close(t.Context(), "t", first.NextCursor); err != nil || closed != 1 {
		t.Fatalf("expired close=%v count=%d", err, closed)
	}
}

func TestPagerRejectsPartialCorruptAndCanceledResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		cancel     bool
	}{
		{name: "timeout", body: `{"timed_out":true}`},
		{name: "shards", body: `{"_shards":{"failed":1}}`},
		{name: "foreign tenant", body: strings.ReplaceAll(pageBody("1"), `"bk_tenant_id":"t"`, `"bk_tenant_id":"other"`)},
		{name: "duplicate", body: pageBody("1", "1")},
		{name: "malformed sort", body: strings.ReplaceAll(pageBody("1"), `["1","instances"]`, `["1"]`)},
		{name: "malformed JSON", body: `{`},
		{name: "missing hits", body: `{}`},
		{name: "too large", body: strings.Repeat("x", maxOneModelResponseBytes+1)},
		{name: "cancel", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := false
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodDelete {
					closed = true
					if r.Context().Err() != nil {
						t.Fatal("cleanup inherited cancellation")
					}
					return pageResponse(`{}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/_pit") {
					return pageResponse(`{"id":"opened"}`), nil
				}
				if tc.cancel {
					cancel()
					return nil, context.Canceled
				}
				return pageResponse(tc.body), nil
			})
			page, err := p.Search(ctx, "t", PageQuery{ModelID: "host", Limit: 2})
			if err == nil || !closed || page.NextCursor != "" {
				t.Fatalf("error=%v closed=%v page=%+v", err, closed, page)
			}
		})
	}
}

func TestPagerInvalidInputAndPartialOpen(t *testing.T) {
	p := pagerFor(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid query reached ES"); return nil, nil })
	for _, q := range []PageQuery{{ModelID: ""}, {ModelID: "host", Limit: 201}, {ModelID: "host", Limit: -1}, {ModelID: "host", Where: Filter{Operator: "eq"}}, {ModelID: "host", Where: Filter{Field: "bk_tenant_id", Type: InstanceAttributeKeyword, Operator: "eq", Value: "other"}}} {
		if _, err := p.Search(t.Context(), "t", q); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("invalid query=%+v error=%v", q, err)
		}
	}
	if _, err := p.Search(t.Context(), "", PageQuery{ModelID: "host"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatal(err)
	}
	closed := false
	p = pagerFor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			closed = true
			return pageResponse(`{}`), nil
		}
		return pageResponse(`{"id":"opened","_shards":{"failed":1}}`), nil
	})
	if _, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host"}); err == nil || !closed {
		t.Fatalf("partial open err=%v close=%v", err, closed)
	}
}

func TestPagerClosedBackendSnapshot(t *testing.T) {
	p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_pit") {
			return pageResponse(`{"id":"opened"}`), nil
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"error":"missing"}`))}, nil
	})
	if _, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host"}); !errors.Is(err, ErrCursorExpired) {
		t.Fatal(fmt.Sprint(err))
	}
}

func TestPagerNormalizesImplicitPITSort(t *testing.T) {
	calls := 0
	p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_pit") {
			return pageResponse(`{"id":"opened"}`), nil
		}
		calls++
		if calls == 2 {
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"search_after":["1","instances"]`) {
				t.Fatalf("implicit PIT field entered cursor: %s", raw)
			}
			return pageResponse(strings.ReplaceAll(pageBody("2"), `"instances"]`, `"instances",4294967297]`)), nil
		}
		return pageResponse(strings.ReplaceAll(pageBody("1", "2"), `"instances"]`, `"instances",4294967296]`)), nil
	})
	first, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	last, err := p.Search(t.Context(), "t", PageQuery{ModelID: "host", Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(last.Instances) != 1 || last.NextCursor != "" {
		t.Fatalf("last=%+v err=%v", last, err)
	}
}

func TestPagerCancellationAfterBackendResponseClosesSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closed := false
	p := pagerFor(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete {
			closed = true
			return pageResponse(`{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/_pit") {
			return pageResponse(`{"id":"opened"}`), nil
		}
		cancel()
		return pageResponse(pageBody("1", "2")), nil
	})
	page, err := p.Search(ctx, "t", PageQuery{ModelID: "host", Limit: 1})
	if !errors.Is(err, context.Canceled) || page.NextCursor != "" || !closed {
		t.Fatalf("page=%+v error=%v closed=%v", page, err, closed)
	}
}
