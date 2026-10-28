// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/storetest"
)

func TestIdentityLookupBatchesTenantsAndCachesPhysicalIndices(t *testing.T) {
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries++
		var body struct {
			Query struct {
				Bool struct {
					Should []struct {
						Bool struct {
							Filter []json.RawMessage `json:"filter"`
						} `json:"bool"`
					} `json:"should"`
				} `json:"bool"`
			} `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Query.Bool.Should) != 2 {
			t.Error("tenants were not batched")
		}
		for _, clause := range body.Query.Bool.Should {
			var term struct {
				Term map[string]string `json:"term"`
			}
			var ids struct {
				IDs struct {
					Values []string `json:"values"`
				} `json:"ids"`
			}
			_ = json.Unmarshal(clause.Bool.Filter[0], &term)
			_ = json.Unmarshal(clause.Bool.Filter[1], &ids)
			tenant := term.Term["bk_tenant_id"]
			if (tenant != "tenant-a" && tenant != "tenant-b") || len(ids.IDs.Values) != 1 || ids.IDs.Values[0] != "same-native" {
				t.Error("query lost tenant scope or included legacy candidates")
			}
		}
		_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"same-native","_index":"alarm-a","_source":{"bk_tenant_id":"tenant-a"}},{"_id":"same-native","_index":"alarm-b","_source":{"bk_tenant_id":"tenant-b"}}]}}`))
	}))
	defer server.Close()
	client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(s string) (string, error) { return s, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	keys := []alarmKey{{tenant: "tenant-a", alert: "same-native"}, {tenant: "tenant-b", alert: "same-native"}}
	for range 2 {
		locations, err := client.locate(t.Context(), keys)
		if err != nil || locations[keys[0]] != "alarm-a" || locations[keys[1]] != "alarm-b" || queries != 1 {
			t.Fatalf("locations=%v queries=%d error=%v", locations, queries, err)
		}
	}
}

func TestBatchOfChildrenDoesNotLookUpUnprojectedParent(t *testing.T) {
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alarm/_search":
			queries++
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), "parent") || strings.Contains(string(raw), "linkd-") {
				t.Error("parent or legacy identity lookup", string(raw))
			}
			_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
		case "/_alias/alarm":
			_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
		case "/_bulk":
			decoder := json.NewDecoder(r.Body)
			items := []any{}
			for {
				var head map[string]any
				if err := decoder.Decode(&head); err == io.EOF {
					break
				} else if err != nil {
					t.Error(err)
					return
				}
				var body struct {
					Script struct {
						Params struct {
							Fields struct {
								Parents []string `json:"associate_alarm_id"`
							} `json:"fields"`
						} `json:"params"`
					} `json:"script"`
				}
				if err := decoder.Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if len(body.Script.Params.Fields.Parents) != 1 || body.Script.Params.Fields.Parents[0] != "parent" {
					t.Error("parent ID changed")
				}
				items = append(items, map[string]any{"update": map[string]any{"status": 200, "_index": "alarm-000001"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			t.Error("unexpected request", r.URL.Path)
		}
	}))
	defer server.Close()
	relations := relationFunc(func(_ context.Context, tenant, id string) (domain.MergeRelation, error) {
		return domain.MergeRelation{TenantID: tenant, ID: id, ParentAlertID: "parent"}, nil
	})
	client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(s string) (string, error) { return s, nil }, relations)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	alerts := make([]domain.Alert, 500)
	for i := range alerts {
		alerts[i] = storetest.Alert(fmt.Sprintf("tenant-%d", i%2), fmt.Sprintf("child-%d", i), "event", "fp", "warning")
		alerts[i].Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{strings.Repeat("a", 64)}}
	}
	for range 2 {
		for _, result := range client.WriteBatch(t.Context(), alerts, false) {
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		}
		if queries != 1 {
			t.Fatalf("expected one cold lookup and zero warm lookups, got %d", queries)
		}
	}
}

func TestIdentityLookupFailureNeverCreatesDocument(t *testing.T) {
	for _, response := range []string{"http-error", `{"timed_out":true,"hits":{"hits":[]}}`, `{"_shards":{"failed":1},"hits":{"hits":[]}}`} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/_search") {
					t.Error("lookup failure led to write", r.URL.Path)
				}
				if response == "http-error" {
					w.WriteHeader(502)
				} else {
					_, _ = w.Write([]byte(response))
				}
			}))
			defer server.Close()
			client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(s string) (string, error) { return s, nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result := client.WriteBatch(t.Context(), []domain.Alert{storetest.Alert("tenant", "alert", "event", "fp", "warning")}, false)
			if result[0].Err == nil {
				t.Fatal("failed lookup was treated as missing document")
			}
		})
	}
}

func TestCompatibilityPayloadUsesOpeningEventWithoutChangingLegacyHook(t *testing.T) {
	alert := storetest.Alert("tenant", "native-alert", "opening-event", "fp", "warning")
	alert.LatestEventID = "later-event"
	raw, err := kachook.CompatibilityPayload(alert, func(s string) (string, error) { return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["alarm_id"] != "native-alert" || document["event_id"] != "opening-event" {
		t.Fatal(fmt.Sprint(document))
	}
}
