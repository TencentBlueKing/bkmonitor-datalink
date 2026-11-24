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
	"net/http"
	"strconv"
	"strings"
	"testing"

	"linkd/internal/activeindex"
	"linkd/internal/domain"
)

func TestActiveIndexPaginationKeepsOneSnapshot(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	closed := false
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/linkd-test-alerts/_pit" {
			return jsonResponse(t, map[string]any{"id": "first"}), nil
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if req.Method == http.MethodDelete {
			closed = true
			if body["id"] != "last" {
				t.Fatal("latest PIT not closed")
			}
			return jsonResponse(t, map[string]any{"succeeded": true}), nil
		}
		pages++
		count := 2000
		pit := "middle"
		if pages == 2 {
			count = 1
			pit = "last"
			if body["pit"].(map[string]any)["id"] != "middle" || len(body["search_after"].([]any)) != 3 {
				t.Fatal("snapshot cursor was not continued")
			}
		}
		hits := make([]any, count)
		for i := range count {
			hits[i] = map[string]any{"_source": map[string]any{"bk_tenant_id": "tenant", "event_source_id": "source", "fingerprint": strconv.Itoa(i), "labels": map[string]any{"strategy_id": "123"}}, "sort": []any{"tenant", strconv.Itoa(i), "index"}}
		}
		return jsonResponse(t, map[string]any{"pit_id": pit, "hits": map[string]any{"hits": hits}}), nil
	}), router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := readActiveIndex(r, t.Context(), activeindex.Query{Sources: []string{"source"}, PageSize: 2000, MaxBytes: 1 << 20})
	if err != nil || len(rows) != 2001 || pages != 2 || !closed {
		t.Fatalf("rows=%d pages=%d closed=%v err=%v", len(rows), pages, closed, err)
	}
}

func TestActiveIndexOnlyReadsOriginalGroupingFields(t *testing.T) {
	router, _ := NewStaticRouter("linkd-test")
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return jsonResponse(t, map[string]any{"succeeded": true}), nil
		}
		if strings.HasSuffix(req.URL.Path, "/_pit") {
			return jsonResponse(t, map[string]any{"id": "pit"}), nil
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, f := range body["_source"].([]any) {
			if strings.HasPrefix(f.(string), "enrich") {
				t.Fatal("enrich read")
			}
		}
		row := map[string]any{"bk_tenant_id": "tenant", "event_source_id": "source", "alert_id": "a", "fingerprint": "fp", "labels": map[string]any{"strategy_id": "raw"}, "enrich": map[string]any{"strategy_id": "overridden"}}
		return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": row}}}}), nil
	}), router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := readActiveIndex(r, t.Context(), activeindex.Query{Sources: []string{"source"}, Fields: []string{"labels.strategy_id"}, PageSize: 10, MaxBytes: 1024})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	v, _ := rows[0].Labels["strategy_id"].StringValue()
	if v != "raw" {
		t.Fatal("used enrichment", v)
	}
}

func TestActiveIndexPITCompleteness(t *testing.T) {
	for _, mode := range []string{"success", "tenant-batch", "partial", "timeout", "over-limit", "missing"} {
		t.Run(mode, func(t *testing.T) {
			closed := false
			searches := 0
			router, err := NewStaticRouter("linkd-test")
			if err != nil {
				t.Fatal(err)
			}
			r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					closed = true
					return jsonResponse(t, map[string]any{"succeeded": true}), nil
				}
				if req.URL.Path == "/linkd-test-alerts/_pit" {
					if req.URL.Query().Get("ignore_unavailable") != "false" {
						t.Fatal("missing index could be ignored")
					}
					response := jsonResponse(t, map[string]any{"id": "pit"})
					if mode == "missing" {
						response.StatusCode = 404
					}
					return response, nil
				}
				if req.URL.Path != "/_search" {
					t.Fatalf("unexpected request %s", req.URL.Path)
				}
				searches++
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["pit"] == nil || body["_source"] == nil {
					t.Fatal("unbounded full-document read")
				}

				if mode == "tenant-batch" {
					filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
					raw, _ := json.Marshal(filters)
					if !strings.Contains(string(raw), `{"term":{"bk_tenant_id":"tenant"}}`) || !strings.Contains(string(raw), `{"terms":{"fingerprint":["fp","second"]}}`) {
						t.Fatal("member batch lost isolation", string(raw))
					}
				}

				hits := []any{map[string]any{"_source": map[string]any{"bk_tenant_id": "tenant", "event_source_id": "source", "fingerprint": "fp", "labels": map[string]any{"strategy_id": 123}}}}
				if mode == "over-limit" {
					hits[0].(map[string]any)["_source"].(map[string]any)["fingerprint"] = strings.Repeat("x", 2048)
				}
				response := map[string]any{"pit_id": "pit", "hits": map[string]any{"hits": hits}}
				if mode == "partial" {
					response["_shards"] = map[string]any{"failed": 1}
				}
				if mode == "timeout" {
					response["timed_out"] = true
				}
				return jsonResponse(t, response), nil
			}), router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			query := activeindex.Query{Sources: []string{"source"}, Fields: []string{"labels.strategy_id"}, PageSize: 10, MaxBytes: 1024}

			if mode == "tenant-batch" {
				query.BKTenantID = "tenant"
				query.Fingerprints = []string{"fp", "second"}
			}
			rows, err := readActiveIndex(r, t.Context(), query)
			if (err == nil) != (mode == "success" || mode == "tenant-batch") {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
			if mode != "missing" && (!closed || searches != 1) {
				t.Fatal("PIT not cleaned up")
			}
			if err != nil && rows != nil {
				t.Fatal("partial snapshot escaped")
			}
		})
	}
}

func readActiveIndex(r *Repository, ctx context.Context, q activeindex.Query) ([]activeindex.Row, error) {
	var rows []activeindex.Row
	err := r.ScanActiveIndex(ctx, q, func(page []activeindex.Row) error { rows = append(rows, page...); return nil })
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func TestActiveIndexPageBudgetsAndCallbackFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			router, err := NewStaticRouter("linkd-test")
			if err != nil {
				t.Fatal(err)
			}
			searches, delivered := 0, 0
			closed := false
			stopped := errors.New("stop consuming")
			repository, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					closed = true
					return jsonResponse(t, map[string]any{"succeeded": true}), nil
				}
				if req.URL.Path == "/linkd-test-alerts/_pit" {
					return jsonResponse(t, map[string]any{"id": "pit"}), nil
				}
				searches++
				hits := []any{}
				if searches <= 3 {
					for i := range 3 {
						hits = append(hits, map[string]any{"_source": activeindex.Row{BKTenantID: "tenant", EventSourceID: "source", Fingerprint: strconv.Itoa(i), Labels: domain.DimensionMap{"strategy_id": domain.NewStringScalar("s")}}, "sort": []any{"tenant", strconv.Itoa(i), "index"}})
					}
				}
				return jsonResponse(t, map[string]any{"pit_id": "pit", "hits": map[string]any{"hits": hits}}), nil
			}), router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			err = repository.ScanActiveIndex(t.Context(), activeindex.Query{Sources: []string{"source"}, PageSize: 3, MaxBytes: 200}, func(rows []activeindex.Row) error {
				bytes := 0
				for _, row := range rows {
					projection, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					bytes += len(projection)
				}
				if len(rows) > 3 || bytes > 200 {
					t.Fatalf("page budget not applied: rows=%d bytes=%d", len(rows), bytes)
				}
				delivered += len(rows)
				if fail {
					return stopped
				}
				return nil
			})
			if !closed {
				t.Fatal("PIT not closed")
			}
			if fail {
				if !errors.Is(err, stopped) || searches != 1 || delivered == 0 {
					t.Fatalf("failed callback continued: %v searches=%d delivered=%d", err, searches, delivered)
				}
			} else if err != nil || searches != 4 || delivered != 9 {
				t.Fatalf("scan=%v searches=%d delivered=%d", err, searches, delivered)
			}
		})
	}
}
