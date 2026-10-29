// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

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

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type closeReadRouter struct{ *StaticRouter }

func (r closeReadRouter) AlertRoute(context.Context, string) (Route, error) {
	return Route{WriteTarget: "active-write", ReadTargets: []string{"active-read", "history"}}, nil
}

func TestCloseReadsOnlyFallbackMissingDocuments(t *testing.T) {
	for _, mode := range []string{"identity", "close"} {
		t.Run(mode, func(t *testing.T) {
			for _, scenario := range []string{"write_hit", "history_and_missing", "fallback_failure", "cancelled", "empty"} {
				t.Run(scenario, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					calls := 0
					failed := errors.New("fallback unavailable")
					r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
						calls++
						var body struct {
							Docs []map[string]string `json:"docs"`
						}
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if req.URL.Path != "/_mget" || req.URL.Query().Get("realtime") != "true" {
							t.Fatal(req.URL)
						}
						if calls == 1 {
							want := 1
							if scenario != "write_hit" {
								want = 3
							}
							if len(body.Docs) != want {
								t.Fatalf("write reads=%d, want %d", len(body.Docs), want)
							}
							for _, doc := range body.Docs {
								if doc["_index"] != "active-write" {
									t.Fatal("eager fallback", doc)
								}
							}
						} else {
							if len(body.Docs) != 4 {
								t.Fatal("fallback did not contain only missing IDs", body.Docs)
							}
							for _, doc := range body.Docs {
								if doc["_id"] == documentID("tenant", "active") || doc["_index"] == "active-write" {
									t.Fatal("duplicate active read", doc)
								}
							}
							if scenario == "fallback_failure" {
								return nil, failed
							}
						}
						docs := []any{}
						for _, doc := range body.Docs {
							id := "active"
							found := calls == 1 && doc["_id"] == documentID("tenant", id)
							if calls > 1 {
								id = "archived"
								found = doc["_index"] == "history" && doc["_id"] == documentID("tenant", id)
							}
							item := map[string]any{"_index": doc["_index"], "_id": doc["_id"], "found": found}
							if found {
								a := storetest.Alert("tenant", id, "opening", id, "warning")
								raw, err := encodeAlertDocument(a)
								if err != nil {
									t.Fatal(err)
								}
								item["_source"], item["_seq_no"], item["_primary_term"] = json.RawMessage(raw), 7, 2
							}
							docs = append(docs, item)
						}
						if scenario == "cancelled" {
							cancel()
						}
						return jsonResponse(t, map[string]any{"docs": docs}), nil
					}), closeReadRouter{mustStaticRouter(t)}, DefaultConfig())
					if err != nil {
						t.Fatal(err)
					}
					ids := []string{"active", "archived", "missing"}
					if scenario == "write_hit" {
						ids = ids[:1]
					}
					if scenario == "empty" {
						ids = nil
					}
					result, err := r.getAlertsCurrent(ctx, "tenant", ids, mode)
					switch scenario {
					case "fallback_failure":
						if !errors.Is(err, failed) {
							t.Fatal(err)
						}
					case "cancelled":
						if !errors.Is(err, context.Canceled) || calls != 1 {
							t.Fatal(calls, err)
						}
					case "empty":
						if err != nil || calls != 0 || len(result.Alerts) != 0 {
							t.Fatal(result, calls, err)
						}
					case "write_hit":
						if err != nil || calls != 1 || len(result.Alerts) != 1 || result.Alerts[0].Alert.AlertID != "active" {
							t.Fatal(result, calls, err)
						}
					default:
						if err != nil || calls != 2 || len(result.Alerts) != 2 || result.Alerts[0].Alert.AlertID != "active" || result.Alerts[1].Alert.AlertID != "archived" || len(result.NotFound) != 1 || result.NotFound[0] != "missing" {
							t.Fatal(result, calls, err)
						}
					}
				})
			}
		})
	}
}

func TestCloseCAS5000OnlySendsChangedFields(t *testing.T) {
	requests, requestBytes := 0, 0
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Path != "/_bulk" || req.URL.Query().Get("refresh") != "false" {
			t.Fatal(req.URL)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBytes += len(raw)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		items := []any{}
		for i := 0; i < len(lines); i += 2 {
			var head map[string]map[string]any
			var body struct {
				Script struct {
					Source string `json:"source"`
					Params struct {
						Patch map[string]any `json:"patch"`
					} `json:"params"`
				} `json:"script"`
			}
			if err := json.Unmarshal([]byte(lines[i]), &head); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[i+1]), &body); err != nil {
				t.Fatal(err)
			}
			meta := head["update"]
			patch := body.Script.Params.Patch
			if meta["if_seq_no"] != float64(7) || meta["if_primary_term"] != float64(2) || body.Script.Source != "ctx._source.putAll(params.patch)" || len(patch) != 12 || patch["revision"] != float64(2) || patch["status"] != "closed" {
				t.Fatal("not a conditional close patch", meta, patch)
			}
			for _, field := range []string{"content", "enrich", "extra_data", "title", "labels"} {
				if _, ok := patch[field]; ok {
					t.Fatal("payload sent in close patch", field)
				}
			}
			if patch["shield"].(map[string]any)["active"] != false || patch["merge"] != nil {
				t.Fatal("terminal fields not replaced")
			}
			items = append(items, map[string]any{"update": map[string]any{"_id": meta["_id"], "_index": meta["_index"], "_seq_no": 8, "_primary_term": 2, "status": 200}})
		}
		return jsonResponse(t, map[string]any{"items": items}), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	updates := make([]store.AlertSnapshotUpdate, 5000)
	content := strings.Repeat("x", 64<<10)
	for i := range updates {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%04d", i), "opening", fmt.Sprint(i), "warning")
		a.Content = content
		version, _ := encodeVersion(versionPayload{Index: "linkd-test-alerts", DocumentID: alertDocumentID(a), SeqNo: 7, PrimaryTerm: 2})
		next := a.Clone()
		at := a.UpdateAt.Add(time.Hour)
		next.UpdateAt, next.EndAt, next.Status, next.EndType, next.EndReason = at, &at, domain.AlertStatusClosed, domain.AlertEndTypeUser, "done"
		next.EndOperation = &domain.AlertEndOperation{ID: "close-batch", Source: "manual", OperatorKind: domain.OperatorKindUser, OperatorID: "tester"}
		updates[i] = store.AlertSnapshotUpdate{Current: store.StoredAlert{Alert: a, Version: version}, Replacement: next}
	}
	started := time.Now()
	results, err := r.CloseAlertsFromSnapshots(t.Context(), updates)
	if err != nil || len(results) != 5000 {
		t.Fatal(len(results), err)
	}
	for _, result := range results {
		if result.Err != nil || result.Alert.Content != content {
			t.Fatal("snapshot or result changed", result.Err)
		}
	}
	if requests != 1 || requestBytes > 8<<20 {
		t.Fatal("close patch unexpectedly large", requests, requestBytes)
	}
	full, err := encodeAlertDocument(updates[0].Replacement)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("5000 close patches: requests=%d bytes=%d old_full_document_estimate=%d elapsed=%s (local transport, not ES throughput)", requests, requestBytes, len(full)*5000, time.Since(started))
}

func TestCloseReadsExcludePayloadAtEverySelectionStage(t *testing.T) {
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	full, _ := encodeAlertDocument(a)
	var source map[string]any
	if err := json.Unmarshal(full, &source); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(req.URL.Path, "/_search") {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if fields := body["_source"].([]any); len(fields) != 1 || fields[0] != "alert_id" || body["seq_no_primary_term"] != false {
				t.Fatal(body)
			}
			return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]string{"alert_id": "alert"}}}}}), nil
		}
		includes := req.URL.Query().Get("_source_includes")
		if includes == "" || strings.Contains(includes, "content") || strings.Contains(includes, ",enrich,") || strings.Contains(includes, "extra_data,") {
			t.Fatal("full payload requested", includes)
		}
		if calls == 2 && len(strings.Split(includes, ",")) != 5 {
			t.Fatal("identity read is not narrow", includes)
		}
		selected := map[string]any{}
		for _, name := range strings.Split(includes, ",") {
			if value, found := source[name]; found {
				selected[name] = value
			}
		}
		return jsonResponse(t, map[string]any{"docs": []any{map[string]any{"_index": "linkd-test-alerts", "_id": alertDocumentID(a), "_seq_no": 7, "_primary_term": 2, "found": true, "_source": selected}}}), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.SearchAlerts(t.Context(), store.AlertSearchRequest{TenantID: "tenant", Limit: 5000, IDsOnly: true})
	if err != nil || len(page.Items) != 1 || page.Items[0].AlertID != "alert" {
		t.Fatal(page, err)
	}
	identity, err := r.GetAlertIdentities(t.Context(), "tenant", []string{"alert"})
	if err != nil || len(identity.Alerts) != 1 || identity.Alerts[0].Alert.Fingerprint != "fp" {
		t.Fatal(identity, err)
	}
	states, err := r.GetAlertCloseStates(t.Context(), "tenant", []string{"alert"})
	if err != nil || len(states.Alerts) != 1 || states.Alerts[0].Version.IsZero() || len(states.Alerts[0].Alert.Enrich) != 0 {
		t.Fatal(states, err)
	}
}
