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
	"fmt"
	"net/http"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestEventCreateConflictsBatchRealtimeAndIsolateResults(t *testing.T) {
	for _, mode := range []string{"success", "content_conflict", "wrong_tenant", "missing", "item_error", "truncated", "transport_error"} {
		t.Run(mode, func(t *testing.T) {
			a := storetest.Event("tenant-a", "duplicate", "fp", "warning")
			b := storetest.Event("tenant-b", "duplicate", "fp", "warning")
			events := []domain.Event{storetest.Event("tenant-a", "new", "fp-new", "warning"), a, b, a}
			bulkCalls, mgetCalls := 0, 0
			transport := transportFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/_bulk":
					bulkCalls++
					if req.URL.Query().Get("refresh") != "false" {
						t.Fatal("bulk refresh wait")
					}
					items := make([]any, len(events))
					for i, e := range events {
						status := 409
						if i == 0 {
							status = 201
						}
						items[i] = map[string]any{"create": map[string]any{"_index": "linkd-test-events", "_id": documentID(e.BKTenantID, e.EventID), "_seq_no": 1, "_primary_term": 1, "status": status}}
					}
					data, _ := json.Marshal(map[string]any{"items": items})
					return batchHTTPResponse(200, data), nil
				case "/_mget":
					mgetCalls++
					if req.Method != http.MethodPost || req.URL.Query().Get("realtime") != "true" {
						t.Fatal("not realtime mget")
					}
					var body struct {
						Docs []map[string]string `json:"docs"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body.Docs) != 3 {
						t.Fatalf("docs=%d", len(body.Docs))
					}
					if mode == "transport_error" {
						return nil, errors.New("response lost")
					}
					docs := make([]any, 3)
					for i, e := range events[1:] {
						if body.Docs[i]["_id"] != documentID(e.BKTenantID, e.EventID) || body.Docs[i]["_index"] != "linkd-test-events" {
							t.Fatal("request identity mismatch")
						}
						existing := e.Clone()
						existing.RelatedAlertID = "alert-1"
						at := existing.CreateAt.Add(time.Second)
						processing := store.EventProcessing{State: domain.EventProcessStateAccepted, Outcome: "accepted", ProcessedAt: &at}
						if i == 0 && mode == "content_conflict" {
							existing.Title = "different"
						}
						if i == 0 && mode == "wrong_tenant" {
							existing.BKTenantID = "wrong-tenant"
						}
						source, err := encodeEventDocument(existing, processing)
						if err != nil {
							t.Fatal(err)
						}
						doc := map[string]any{"_index": "linkd-test-events", "_id": documentID(e.BKTenantID, e.EventID), "_seq_no": 7, "_primary_term": 2, "found": true, "_source": json.RawMessage(source)}
						if i == 0 && mode == "missing" {
							doc["found"] = false
							delete(doc, "_source")
						}
						if i == 0 && mode == "item_error" {
							doc["error"] = map[string]any{"type": "es_rejected_execution_exception"}
						}
						docs[i] = doc
					}
					if mode == "truncated" {
						docs = docs[:2]
					}
					data, _ := json.Marshal(map[string]any{"docs": docs})
					return batchHTTPResponse(200, data), nil
				default:
					t.Fatalf("unexpected request: %s", req.URL.Path)
					return nil, nil
				}
			})
			r, err := New(transport, mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			results, err := r.CreateEvents(t.Context(), events)
			if err != nil {
				t.Fatal(err)
			}
			if bulkCalls != 1 || mgetCalls != 1 {
				t.Fatalf("bulk=%d mget=%d", bulkCalls, mgetCalls)
			}
			if results[0].Err != nil || !results[0].Result.Created {
				t.Fatalf("successful create changed: %+v", results[0])
			}
			for i := 1; i < len(results); i++ {
				wantError := mode == "truncated" || mode == "transport_error" || (i == 1 && mode != "success")
				if (results[i].Err != nil) != wantError {
					t.Fatalf("item %d: %+v", i, results[i])
				}
				if !wantError && (!results[i].Result.Processing.State.Terminal() || results[i].Result.Event.RelatedAlertID != "alert-1" || results[i].Result.Created) {
					t.Fatalf("lost realtime recovery state: %+v", results[i])
				}
			}
			if mode == "content_conflict" && !errors.Is(results[1].Err, store.ErrIdentityConflict) {
				t.Fatal("missing content conflict")
			}
			if mode == "missing" && !errors.Is(results[1].Err, store.ErrNotFound) {
				t.Fatal("missing not found")
			}
			if mode == "item_error" && errors.Is(results[1].Err, store.ErrInvalidArgument) {
				t.Fatal("retryable error became invalid argument")
			}
		})
	}
}

func TestEventConflictReadChunksAndResponseBudget(t *testing.T) {
	for _, shrink := range []bool{false, true} {
		t.Run(fmt.Sprint(shrink), func(t *testing.T) {
			const count = 65
			prepared := make([]*bulkEventCreateItem, count)
			results := make([]store.CreateEventItemResult, count)
			indices := make([]int, count)
			byID := map[string]domain.Event{}
			for i := range count {
				event := storetest.Event("tenant", fmt.Sprint(i), "fp", "warning")
				id := documentID(event.BKTenantID, event.EventID)
				prepared[i] = &bulkEventCreateItem{event: event, documentID: id, writeTarget: "linkd-test-events"}
				indices[i] = i
				byID[id] = event
			}
			calls := 0
			r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				var body struct {
					Docs []map[string]string `json:"docs"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Docs) > 32 {
					t.Fatal("unbounded conflict read")
				}
				if shrink && len(body.Docs) > 1 {
					return nil, ErrResponseTooLarge
				}
				docs := make([]any, len(body.Docs))
				for i, d := range body.Docs {
					source, err := encodeEventDocument(byID[d["_id"]], store.NewUnprocessedEventProcessing())
					if err != nil {
						t.Fatal(err)
					}
					docs[i] = map[string]any{"_index": "linkd-test-events", "_id": d["_id"], "_seq_no": 0, "_primary_term": 1, "found": true, "_source": json.RawMessage(source)}
				}
				data, _ := json.Marshal(map[string]any{"docs": docs})
				return batchHTTPResponse(200, data), nil
			}), mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			r.verifyEventCreateConflicts(context.Background(), prepared, indices, results)
			for i, result := range results {
				if result.Err != nil {
					t.Fatalf("item %d: %v", i, result.Err)
				}
			}
			if !shrink && calls != 3 {
				t.Fatalf("calls=%d want 3", calls)
			}
			if shrink && calls != 127 {
				t.Fatalf("calls=%d want bounded split tree", calls)
			}
		})
	}
}
