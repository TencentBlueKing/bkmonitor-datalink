// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/processingerror"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestMaintainRepairsLatestEventTimeTemplate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		field   map[string]any
		updates int
	}{
		{"missing", nil, 1},
		{"text", map[string]any{"type": "text"}, 1},
		{"wrong-date-format", map[string]any{"type": "date", "format": "epoch_millis"}, 1},
		{"default-date-format", map[string]any{"type": "date"}, 1},
		{"correct", map[string]any{"type": "date", "format": "yyyy-MM-dd HH:mm:ss", "ignore_malformed": true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			properties := alarmMapping()["properties"].(map[string]any)
			properties["latest_event_time"] = tc.field
			properties["custom_field"] = map[string]any{"type": "keyword"}
			template := map[string]any{"index_patterns": []string{"alarm*"}, "mappings": map[string]any{"properties": properties}}
			updates, mappings := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead && r.URL.Path == "/alarm-20260831" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /_template/alarm":
					_ = json.NewEncoder(w).Encode(map[string]any{"alarm": template})
				case "PUT /_template/alarm":
					updates++
					if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
						t.Error(err)
					}
				case "GET /_alias/alarm":
					_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
				case "PUT /alarm/_mapping":
					mappings++
				case "PUT /_ilm/policy/alarm_policy":
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := openMappingTestClient(t, server.URL)
			for range 2 {
				if err := client.Maintain(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			fields := template["mappings"].(map[string]any)["properties"].(map[string]any)
			field, _ := fields["latest_event_time"].(map[string]any)
			if field["type"] != "date" || field["format"] != "yyyy-MM-dd HH:mm:ss" || fields["custom_field"] == nil {
				t.Fatalf("template not repaired or custom mapping lost: latest_event_time=%v, custom_field=%v", field, fields["custom_field"])
			}
			if tc.updates == 0 && field["ignore_malformed"] != true {
				t.Fatal("compatible date options were removed")
			}
			if updates != tc.updates || mappings != 2 {
				t.Fatalf("template updates=%d, index mapping calls=%d", updates, mappings)
			}
		})
	}
}

func openMappingTestClient(t *testing.T, address string) *Client {
	t.Helper()
	client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{address}}}, func(level string) (string, error) { return level, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

// 写入内容和性能测试从已完成维护的状态开始，维护使用独立的模拟传输，
// 不干扰这些测试对写入请求数的断言。首次准备与失败恢复由本文件的专项测试覆盖。
func prepareClientForWriteTest(t *testing.T, client *Client) {
	t.Helper()
	transport := client.transport
	defer func() { client.transport = transport }()
	alias := client.config.AlarmEventIndex
	client.transport = closeTransportFunc(func(r *http.Request) (*http.Response, error) {
		var body any = map[string]bool{"acknowledged": true}
		switch r.Method + " " + r.URL.Path {
		case "GET /_template/" + url.PathEscape(alias):
			body = map[string]any{alias: map[string]any{"mappings": alarmMapping()}}
		case "GET /_alias/" + url.PathEscape(alias):
			body = map[string]any{alias + "-000001": map[string]any{"aliases": map[string]any{alias: map[string]bool{"is_write_index": true}}}}
		case "PUT /_ilm/policy/" + url.PathEscape(alias+"_policy"), "PUT /" + url.PathEscape(alias) + "/_mapping":
		default:
			t.Errorf("unexpected preparation request: %s %s", r.Method, r.URL.Path)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})
	if err := client.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionRequiresPreparedMapping(t *testing.T) {
	for _, mode := range []string{"write", "close", "synchronous-close-create"} {
		t.Run(mode, func(t *testing.T) {
			var requests, bulks atomic.Int32
			var mappingStatus atomic.Int32
			mappingStatus.Store(http.StatusBadRequest)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead && r.URL.Path == "/alarm-20260831" {
					w.WriteHeader(http.StatusOK)
					return
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "GET /_template/alarm":
					_ = json.NewEncoder(w).Encode(map[string]any{"alarm": map[string]any{"index_patterns": []string{"alarm*"}, "mappings": alarmMapping()}})
				case "GET /_alias/alarm":
					_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
				case "PUT /alarm/_mapping":
					w.WriteHeader(int(mappingStatus.Load()))
				case "PUT /_ilm/policy/alarm_policy":
				case "POST /alarm/_search":
					_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
				case "POST /_bulk":
					bulks.Add(1)
					if mode == "write" || mode == "synchronous-close-create" && bulks.Load() == 1 {
						decoder := json.NewDecoder(r.Body)
						var head map[string]any
						var body struct {
							Script struct {
								Params struct {
									Create map[string]any `json:"create"`
									Fields map[string]any `json:"fields"`
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
						if body.Script.Params.Fields["latest_event_time"] == nil || bulks.Load() == 1 && body.Script.Params.Create["latest_event_time"] == nil {
							t.Error("write missing latest_event_time")
						}
					}
					_, _ = w.Write([]byte(`{"items":[{"update":{"status":200,"_index":"alarm-000001"}}]}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := openMappingTestClient(t, server.URL)
			alert := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
			alert.Status, alert.EndAt, alert.EndType = domain.AlertStatusClosed, &alert.UpdateAt, domain.AlertEndTypeUser
			reader := &compactCloseReader{readerFunc: func(context.Context, string, []string) (store.AlertBatch, error) {
				return store.AlertBatch{Alerts: []store.StoredAlert{{Alert: alert}}}, nil
			}}
			runtime := NewRuntime(config.KACDeliveryConfig{}, nil, "", reader, client, nil, nil, nil)
			if mode == "close" {
				client.locations[alarmKey{"tenant", "alert"}] = "alarm-000001"
			}
			write := func(ctx context.Context) error {
				switch mode {
				case "write":
					return client.WriteBatch(ctx, []domain.Alert{alert}, false)[0].Err
				case "close":
					return client.WriteCloseBatch(ctx, []domain.Alert{alert})[0].Err
				default:
					return runtime.ProjectClosedBatchNow(ctx, []domain.Alert{alert})[0]
				}
			}
			assertNotReady := func() {
				t.Helper()
				before := requests.Load()
				if err := write(t.Context()); err == nil || !processingerror.Retryable(err) {
					t.Fatalf("expected retryable readiness failure, got %v", err)
				}
				if requests.Load() != before {
					t.Fatal("unprepared projection issued ES requests")
				}
			}
			assertNotReady()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := write(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if err := client.Maintain(t.Context()); !hasStatus(err, http.StatusBadRequest) {
				t.Fatalf("mapping conflict not returned: %v", err)
			}
			assertNotReady()
			mappingStatus.Store(http.StatusOK)
			if err := client.Maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := write(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if bulks.Load() != 2 {
				t.Fatalf("bulk calls=%d", bulks.Load())
			}
		})
	}
}

func TestWritesDuringFirstMappingPreparationFailWithoutWaiting(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var bulks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/alarm-20260831" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /_template/alarm":
			_ = json.NewEncoder(w).Encode(map[string]any{"alarm": map[string]any{"mappings": alarmMapping()}})
		case "GET /_alias/alarm":
			_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
		case "PUT /alarm/_mapping":
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "PUT /_ilm/policy/alarm_policy":
		case "POST /_bulk":
			bulks.Add(1)
			_, _ = w.Write([]byte(`{"items":[{"update":{"status":200,"_index":"alarm-000001"}}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := openMappingTestClient(t, server.URL)
	alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
	client.locations[alarmKey{"tenant", "alert"}] = "alarm-000001"
	done := make(chan error, 1)
	go func() { done <- client.Maintain(t.Context()) }()
	var once sync.Once
	defer once.Do(func() { close(release) })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance did not reach mapping")
	}
	var writers sync.WaitGroup
	for range 16 {
		writers.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			for _, err := range []error{client.WriteBatch(ctx, []domain.Alert{alert}, false)[0].Err, client.WriteCloseBatch(ctx, []domain.Alert{alert})[0].Err} {
				if err == nil || !processingerror.Retryable(err) {
					t.Errorf("write bypassed pending mapping: %v", err)
				}
			}
		})
	}
	writers.Wait()
	if bulks.Load() != 0 {
		t.Fatal("bulk started before mapping acknowledgement")
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := client.WriteBatch(t.Context(), []domain.Alert{alert}, false)[0].Err; err != nil {
		t.Fatal(err)
	}
}

func TestEmptyWriteDoesNotRequireMappingPreparation(t *testing.T) {
	client := newClient(config.KACPluginConfig{}, closeTransportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("empty write issued an ES request")
		return nil, errors.New("unexpected request")
	}), nil, nil)
	if results := client.WriteBatch(t.Context(), nil, false); len(results) != 0 {
		t.Fatal(results)
	}
	if results := client.WriteCloseBatch(t.Context(), nil); len(results) != 0 {
		t.Fatal(results)
	}
}
