// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
)

func inspectionSource() *releaseStub {
	return &releaseStub{record: eventsource.Record{Revision: 7, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant", Collection: &config.SourceCollection{Plugin: "rest_api_push", PluginSourceID: "original", Secret: "ingress-only", Configs: map[string]any{"host": "vendor"}, InitialState: map[string]any{"cursor": "initial"}}}}}
}

func inspectHTTP(t *testing.T, h *Inspector, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body))
	r.SetPathValue("source_id", "source")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestInspectionDraftStateAndSingleExecution(t *testing.T) {
	for _, state := range []string{"initial", "committed", "explicit"} {
		t.Run(state, func(t *testing.T) {
			calls, reads := 0, 0
			progress := progressReaderFunc(func(_ context.Context, tenant, source string) (map[string]any, error) {
				reads++
				if tenant != "tenant" || source != "source" {
					t.Fatal("wrong progress scope")
				}
				if state == "committed" {
					return map[string]any{"cursor": state}, nil
				}
				return nil, nil
			})
			h, err := NewInspector(inspectionSource(), executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
				calls++
				if r.TenantID != "tenant" || r.SourceVersion != 7 || r.PluginSourceID != "original" || r.State["cursor"] != state || r.Configs["host"] != "vendor" {
					t.Fatalf("wrong binding: %+v", r)
				}
				raw, _ := json.Marshal(r)
				if strings.Contains(string(raw), "ingress-only") {
					t.Fatal("ingress secret leaked")
				}
				return pluginruntime.Result{RequestID: r.RequestID, ProposedState: map[string]any{"cursor": "candidate"}}, nil
			}), progress, slog.New(slog.NewTextHandler(io.Discard, nil)), 1, 1024)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"request_id":"debug","mode":"pull","received_at":"2026-11-15T00:00:00Z"`
			if state == "explicit" {
				body += `,"state":{"cursor":"explicit"}`
			}
			body += "}"
			w := inspectHTTP(t, h, body)
			if w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), "candidate") {
				t.Fatalf("%d %s calls=%d", w.Code, w.Body.String(), calls)
			}
			if state == "explicit" && reads != 0 {
				t.Fatal("explicit state consulted stored cursor")
			}
		})
	}
}

func TestInspectionInputAndFailureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		failure    error
		status     int
	}{
		{"fields", `{"request_id":"d","mode":"fields"}`, nil, 200},
		{"override", `{"request_id":"d","mode":"fields","configs":{}}`, nil, 400},
		{"time", `{"request_id":"d","mode":"pull"}`, nil, 400},
		{"data", `{"request_id":"d","mode":"metric"}`, nil, 400},
		{"tenant", `{"request_id":"d","mode":"fields","bk_tenant_id":"foreign"}`, nil, 400},
		{"trailing", `{"request_id":"d","mode":"fields"} {}`, nil, 400},
		{"timeout", `{"request_id":"d","mode":"fields"}`, context.DeadlineExceeded, 504},
		{"runtime timeout", `{"request_id":"d","mode":"fields"}`, &pluginruntime.HTTPError{StatusCode: 504}, 504},
		{"runtime busy", `{"request_id":"d","mode":"fields"}`, &pluginruntime.HTTPError{StatusCode: 503}, 503},
		{"runtime credential", `{"request_id":"d","mode":"fields"}`, &pluginruntime.HTTPError{StatusCode: 401}, 502},
		{"backend", `{"request_id":"d","mode":"fields"}`, errors.New("private credential"), 502},
		{"plugin", `{"request_id":"d","mode":"fields"}`, &pluginruntime.Failure{Code: "plugin_failed", Type: "ValueError"}, 422},
		{"size", strings.Repeat(" ", 1025), nil, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, err := NewInspector(inspectionSource(), executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
				calls++
				return pluginruntime.Result{}, tc.failure
			}), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 1, 1024)
			if err != nil {
				t.Fatal(err)
			}
			w := inspectHTTP(t, h, tc.body)
			if w.Code != tc.status || calls > 1 || strings.Contains(w.Body.String(), "private credential") {
				t.Fatalf("%d %s calls=%d", w.Code, w.Body.String(), calls)
			}
			if tc.status == 400 || tc.status == 413 {
				if calls != 0 {
					t.Fatal("invalid input executed")
				}
			}
		})
	}
}

func TestInspectionAdmissionAndCancellation(t *testing.T) {
	started, done := make(chan struct{}), make(chan struct{})
	h, err := NewInspector(inspectionSource(), executorFunc(func(ctx context.Context, _ pluginruntime.Request) (pluginruntime.Result, error) {
		close(started)
		<-ctx.Done()
		return pluginruntime.Result{}, ctx.Err()
	}), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 1, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		defer close(done)
		r := httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(`{"request_id":"d","mode":"fields"}`))
		r.SetPathValue("source_id", "source")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}()
	<-started
	// 非 JSON 的第二个请求也应先被并发预算拒绝，不能进入解码。
	if w := inspectHTTP(t, h, "invalid"); w.Code != 503 {
		t.Fatal(w.Code)
	}
	cancel()
	<-done
	if w := inspectHTTP(t, h, "invalid"); w.Code != 400 {
		t.Fatal("slot not released", w.Code)
	}
}

func TestInspectionSharedTenantDraft(t *testing.T) {
	source := inspectionSource()
	source.record.Spec.RelatedTenantID = ""
	source.record.Spec.Collection.TenantBindings = []config.CollectionTenantBinding{{TenantID: "selected", Enabled: false, Configs: map[string]any{"host": "selected-vendor"}, PluginSourceID: "selected-original"}}
	calls := 0
	h, err := NewInspector(source, executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
		calls++
		if r.TenantID != "selected" || r.PluginSourceID != "selected-original" || r.Configs["host"] != "selected-vendor" {
			t.Fatalf("wrong tenant binding: %+v", r)
		}
		return pluginruntime.Result{}, nil
	}), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 1, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"", "foreign", "selected"} {
		body := `{"request_id":"d","mode":"push","received_at":"2026-11-15T00:00:00Z","data":{"bk_tenant_id":"spoof"},"bk_tenant_id":"` + tenant + `"}`
		want := 400
		if tenant == "selected" {
			want = 200
		}
		if w := inspectHTTP(t, h, body); w.Code != want {
			t.Fatalf("tenant=%s: %d %s", tenant, w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	source.record.Deleted = true
	if w := inspectHTTP(t, h, `{"request_id":"d","mode":"fields"}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
