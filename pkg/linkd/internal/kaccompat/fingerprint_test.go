// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/storetest"
)

func TestWriteBatchIncludesFingerprintOnCreateAndUpdate(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprintf("exists=%t", exists), func(t *testing.T) {
			alerts := []domain.Alert{
				storetest.Alert("tenant-a", "same-alert", "event", "host-a:cpu-high", "warning"),
				storetest.Alert("tenant-b", "same-alert", "event", "host-b:cpu-high", "warning"),
			}
			bulkCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/alarm/_search":
					hits := []map[string]string{}
					if exists {
						for _, alert := range alerts {
							id, _ := kachook.AlarmID(alert.BKTenantID, alert.AlertID)
							hits = append(hits, map[string]string{"_id": id, "_index": "alarm-000001"})
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
				case "/_alias/alarm":
					_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
				case "/_bulk":
					bulkCalls++
					decoder := json.NewDecoder(r.Body)
					for _, alert := range alerts {
						var head map[string]any
						var body struct {
							Script struct {
								Params struct {
									Fields map[string]any `json:"fields"`
									Create map[string]any `json:"create"`
								} `json:"params"`
							} `json:"script"`
						}
						if err := decoder.Decode(&head); err != nil {
							t.Error(err)
							return
						}
						if err := decoder.Decode(&body); err != nil {
							t.Error(err)
							return
						}
						params := body.Script.Params
						if params.Fields["fingerprint"] != alert.Fingerprint {
							t.Errorf("tenant %s update fingerprint = %v", alert.BKTenantID, params.Fields["fingerprint"])
						}
						if !exists && params.Create["fingerprint"] != alert.Fingerprint {
							t.Errorf("tenant %s create fingerprint = %v", alert.BKTenantID, params.Create["fingerprint"])
						}
						for _, key := range []string{"conductor", "notify_status", "field_extra_info", "storage_time"} {
							if _, found := params.Fields[key]; found {
								t.Errorf("update resets KAC-owned field %s", key)
							}
						}
					}
					_, _ = w.Write([]byte(`{"items":[{"update":{"status":200,"_index":"alarm-000001"}},{"update":{"status":200,"_index":"alarm-000001"}}]}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(level string) (string, error) { return level, nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			for _, result := range client.WriteBatch(t.Context(), alerts, false) {
				if result.Err != nil {
					t.Fatal(result.Err)
				}
			}
			if bulkCalls != 1 {
				t.Fatalf("bulk calls = %d", bulkCalls)
			}
		})
	}
}

func TestMaintainAddsFingerprintToExistingTemplateAndIndex(t *testing.T) {
	properties := alarmMapping()["properties"].(map[string]any)
	if field, _ := properties["fingerprint"].(map[string]any); field["type"] != "keyword" {
		t.Errorf("new index fingerprint mapping = %v", field)
	}
	templateUpdated, indexUpdated := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /_template/alarm":
			_, _ = w.Write([]byte(`{"alarm":{"index_patterns":["alarm*"],"mappings":{"properties":{"linkd_disposal_status":{"type":"keyword"},"custom_field":{"type":"keyword"}}}}}`))
		case "GET /_alias/alarm":
			_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
		case "PUT /_template/alarm", "PUT /alarm/_mapping":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if r.URL.Path == "/_template/alarm" {
				templateUpdated = true
				body = body["mappings"].(map[string]any)
			} else {
				indexUpdated = true
			}
			fields := body["properties"].(map[string]any)
			if field, _ := fields["fingerprint"].(map[string]any); field["type"] != "keyword" {
				t.Errorf("fingerprint mapping = %v", field)
			}
			if r.URL.Path == "/_template/alarm" && fields["custom_field"] == nil {
				t.Error("existing custom mapping removed")
			}
		case "PUT /_ilm/policy/alarm_policy":
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(level string) (string, error) { return level, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !templateUpdated || !indexUpdated {
		t.Fatalf("mapping update missing: template=%t index=%t", templateUpdated, indexUpdated)
	}
}
