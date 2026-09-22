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
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"linkd/internal/activeindex"
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
		count := 500
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
			hits[i] = map[string]any{"_source": map[string]any{"bk_tenant_id": "tenant", "event_source_id": "source", "fingerprint": strconv.Itoa(i), "labels": map[string]any{"strategy_id": "123"}}, "sort": []any{"tenant", strconv.Itoa(i), "index", i}}
		}
		return jsonResponse(t, map[string]any{"pit_id": pit, "hits": map[string]any{"hits": hits}}), nil
	}), router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := r.ReadActiveIndex(t.Context(), activeindex.Query{Sources: []string{"source"}, MaxRows: 1000, MaxBytes: 1 << 20})
	if err != nil || len(rows) != 501 || pages != 2 || !closed {
		t.Fatalf("rows=%d pages=%d closed=%v err=%v", len(rows), pages, closed, err)
	}
}

func TestActiveIndexPITCompleteness(t *testing.T) {
	for _, mode := range []string{"success", "partial", "timeout", "over-limit", "missing"} {
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
				hits := []any{map[string]any{"_source": map[string]any{"bk_tenant_id": "tenant", "event_source_id": "source", "fingerprint": "fp", "labels": map[string]any{"strategy_id": 123}}}}
				if mode == "over-limit" {
					hits = append(hits, hits[0])
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
			rows, err := r.ReadActiveIndex(t.Context(), activeindex.Query{Sources: []string{"source"}, Scope: &activeindex.Scope{BKTenantID: "tenant", StrategyID: "123"}, MaxRows: 1, MaxBytes: 1024})
			if (err == nil) != (mode == "success") {
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
