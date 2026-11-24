// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestBucketIndexUTCAndBoundaries(t *testing.T) {
	client := openMappingTestClient(t, "http://unused.invalid")
	for _, tc := range []struct{ at, want string }{
		{"2026-11-08T23:59:59Z", "alarm-20261102"},
		{"2026-11-09T00:00:00Z", "alarm-20261109"},
		{"2026-11-09T07:59:59+08:00", "alarm-20261102"},
		{"2026-11-16T00:00:00Z", "alarm-20261116"},
		{"1970-01-04T23:59:59Z", "alarm-19691229"},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := client.bucketIndex(at); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	client.config.AlarmEventBucketDays = 1
	if got := client.bucketIndex(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)); got != "alarm-20261112" {
		t.Fatal(got)
	}
}

func TestEnsureBucketCreationFailuresAndConcurrentCreation(t *testing.T) {
	for _, mode := range []string{"create", "existing", "concurrent", "invalid-mapping", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			puts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/alarm-20261109" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Method == http.MethodHead {
					switch mode {
					case "existing":
						w.WriteHeader(200)
					case "unavailable":
						w.WriteHeader(503)
					default:
						w.WriteHeader(404)
					}
					return
				}
				puts++
				var body struct {
					Aliases map[string]struct {
						Write bool `json:"is_write_index"`
					} `json:"aliases"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if alias, ok := body.Aliases["alarm"]; !ok || alias.Write {
					t.Error("bucket must join read alias")
				}
				if mode == "concurrent" {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception"}}`))
					return
				}
				if mode == "invalid-mapping" {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"type":"mapper_parsing_exception"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"acknowledged":true}`))
			}))
			defer server.Close()
			client := openMappingTestClient(t, server.URL)
			err := client.ensureBucket(t.Context(), "alarm-20261109")
			if (err != nil) != (mode == "invalid-mapping" || mode == "unavailable") {
				t.Fatalf("error=%v", err)
			}
			if (mode == "existing" || mode == "unavailable") && puts != 0 {
				t.Fatal("unexpected creation")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := client.ensureBucket(ctx, "alarm-20261109"); err == nil {
				t.Fatal("cancellation ignored")
			}
		})
	}
}

func TestBucketBatchRoutesNewAndHistoricalDocuments(t *testing.T) {
	var targets []string
	var prepared []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/alarm/_search":
			_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"legacy","_index":"alarm-000001","_source":{"bk_tenant_id":"tenant"}}]}}`))
		case r.Method == http.MethodHead:
			prepared = append(prepared, r.URL.Path)
			w.WriteHeader(200)
		case r.URL.Path == "/_bulk":
			decoder := json.NewDecoder(r.Body)
			items := []any{}
			for decoder.More() {
				var head struct {
					Update struct {
						Index string `json:"_index"`
					} `json:"update"`
				}
				var body map[string]any
				if err := decoder.Decode(&head); err != nil {
					t.Error(err)
					return
				}
				if err := decoder.Decode(&body); err != nil {
					t.Error(err)
					return
				}
				targets = append(targets, head.Update.Index)
				items = append(items, map[string]any{"update": map[string]any{"status": 200, "_index": head.Update.Index}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := openMappingTestClient(t, server.URL)
	prepareClientForWriteTest(t, client)
	alerts := []domain.Alert{}
	for i, id := range []string{"legacy", "new-a", "new-b", "next"} {
		alert := storetest.Alert("tenant", id, "event", "fp", "warning")
		alert.CreateAt = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
		if i == 3 {
			alert.CreateAt = alert.CreateAt.AddDate(0, 0, 7)
		}
		alerts = append(alerts, alert)
	}
	for _, result := range client.WriteBatch(t.Context(), alerts, false) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	want := []string{"alarm-000001", "alarm-20261109", "alarm-20261109", "alarm-20261116"}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("targets=%v", targets)
		}
	}
	if len(prepared) != 2 {
		t.Fatalf("bucket requests=%v", prepared)
	}
	// 清空进程缓存，模拟重启后的重投；新文档即使尚未被搜索发现仍落原创建时间桶。
	clear(client.locations)
	for i := range alerts {
		alerts[i].UpdateAt = alerts[i].CreateAt.AddDate(0, 0, 30)
	}
	for _, result := range client.WriteBatch(t.Context(), alerts, false) {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	for i := range want {
		if targets[i+4] != want[i] {
			t.Fatalf("retry targets=%v", targets)
		}
	}
}

func TestRemoveTemplateLifecyclePreservesOtherSettings(t *testing.T) {
	for _, settings := range []map[string]any{
		{"lifecycle": map[string]any{"name": "old"}},
		{"index.lifecycle.name": "old", "index.lifecycle.rollover_alias": "alarm"},
		{"index": map[string]any{"lifecycle": map[string]any{"name": "old"}}},
	} {
		settings["number_of_shards"] = "5"
		template := map[string]any{"settings": settings}
		if !removeTemplateLifecycle(template) || removeTemplateLifecycle(template) {
			t.Fatal("removal is not idempotent")
		}
		if settings["number_of_shards"] != "5" {
			t.Fatal("operational setting lost")
		}
	}
}
