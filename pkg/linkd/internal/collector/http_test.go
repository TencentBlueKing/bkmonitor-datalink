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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/logging"
	"linkd/internal/pluginruntime"
)

type intakeStub struct {
	authErr    error
	prepareErr error
	service    *Service
	prepared   bool
}

func TestReceiverPreservesRuntimeCapacityAndTimeout(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "private runtime details")
			}))
			defer runtime.Close()
			executor, err := pluginruntime.NewHTTPClient(runtime.Client(), runtime.URL, "test-token", 1024)
			if err != nil {
				t.Fatal(err)
			}
			logger, err := logging.New(logging.DefaultConfig(), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			intake := &intakeStub{service: New(executor, publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
				t.Error("failed execution published events")
				return nil
			}), nil)}
			receiver, err := NewReceiver(intake, 1024, 1, logger)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
			r.SetPathValue("source_id", "source")
			r.Header.Set("X-Secret", "secret")
			w := httptest.NewRecorder()
			receiver.ServeHTTP(w, r)
			want := status
			if status == http.StatusUnauthorized {
				want = http.StatusBadGateway
			}
			if w.Code != want || calls != 1 || strings.Contains(w.Body.String(), "private runtime details") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func (i *intakeStub) Authorize(context.Context, string, string) error { return i.authErr }

func (i *intakeStub) Prepare(_ context.Context, _ string, _ string, body json.RawMessage) (pluginruntime.Request, *Service, error) {
	i.prepared = true
	return pluginruntime.Request{RequestID: "persisted-receipt", TenantID: "trusted", SourceID: "source", ReceivedAt: "2026-11-14T12:00:00Z", Data: body}, i.service, i.prepareErr
}

func TestReceiverWaitsForPublication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        error
		body        string
		failPublish bool
		status      int
	}{
		{"success", nil, `{"bk_tenant_id":"untrusted"}`, false, 200},
		{"wrong secret", ErrSourceUnauthorized, `{}`, false, 401},
		{"lookup failure", errors.New("storage failed"), `{}`, false, 503},
		{"bad JSON", nil, `invalid`, false, 400},
		{"large request", nil, strings.Repeat("x", 1025), false, 413},
		{"broker failure", nil, `{}`, true, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			published := false
			source := &intakeStub{authErr: tc.auth}
			source.service = New(executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
				if r.TenantID != "trusted" || r.RequestID != "persisted-receipt" {
					t.Fatal("intake identity lost")
				}
				return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{}`)}}, nil
			}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
				if tc.failPublish {
					return errors.New("broker unavailable password=hidden-password")
				}
				published = true
				return nil
			}), nil)
			var logs bytes.Buffer
			logger, err := logging.New(logging.DefaultConfig(), &logs)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewReceiver(source, 1024, 4, logger)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sources/source", strings.NewReader(tc.body))
			request.SetPathValue("source_id", "source")
			request.Header.Set("X-Secret", "secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if tc.status == 200 && !published {
				t.Fatal("responded before publish")
			}
			if tc.auth != nil && source.prepared {
				t.Fatal("unauthorized intake")
			}
			if strings.Contains(response.Body.String(), "hidden-password") {
				t.Fatal("error leaked")
			}
			if strings.Contains(logs.String(), "hidden-password") || strings.Contains(logs.String(), "untrusted") {
				t.Fatal("sensitive data logged")
			}
			if tc.failPublish {
				for _, field := range []string{"publish source events: broker unavailable", "collector", "source_process", "persisted-receipt", "trusted"} {
					if !strings.Contains(logs.String(), field) {
						t.Fatalf("missing diagnostic %s: %s", field, logs.String())
					}
				}
			} else if tc.name == "lookup failure" {
				if !strings.Contains(logs.String(), "storage failed") {
					t.Fatal("lookup cause lost")
				}
			} else if logs.Len() != 0 {
				t.Fatalf("unexpected error log: %s", logs.String())
			}
		})
	}
}

func TestReceiverLogsIntakeFailureWithoutPayload(t *testing.T) {
	var logs bytes.Buffer
	logger, err := logging.New(logging.DefaultConfig(), &logs)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewReceiver(&intakeStub{prepareErr: errors.New("persist source receipt: storage unavailable")}, 1024, 4, logger)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sources/source", strings.NewReader(`{"content":"sensitive-body"}`))
	request.SetPathValue("source_id", "source")
	request.Header.Set("X-Secret", "private-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatalf("status=%d", response.Code)
	}
	for _, value := range []string{"persist source receipt: storage unavailable", "source_intake", "trusted", "persisted-receipt"} {
		if !strings.Contains(logs.String(), value) {
			t.Fatalf("missing diagnostic %s: %s", value, logs.String())
		}
	}
	for _, value := range []string{"sensitive-body", "private-token"} {
		if strings.Contains(logs.String(), value) {
			t.Fatal("request content logged")
		}
	}
}

func TestReceiverRejectsBusyWithoutReadingAndReleasesOnCancellation(t *testing.T) {
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var calls atomic.Int32
	service := New(executorFunc(func(ctx context.Context, _ pluginruntime.Request) (pluginruntime.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return pluginruntime.Result{}, ctx.Err()
		}
		return pluginruntime.Result{}, nil
	}), nil, nil)
	handler, err := NewReceiver(&intakeStub{service: service}, 1024, 1, logger)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(ctx context.Context, body io.Reader) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/sources/source", body)
		request.SetPathValue("source_id", "source")
		request.Header.Set("X-Secret", "secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	firstCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- serve(firstCtx, strings.NewReader(`{}`)) }()
	<-started
	busyBody := strings.NewReader(`{"sentinel":"unread"}`)
	before := busyBody.Len()
	busy := make(chan *httptest.ResponseRecorder, 1)
	go func() { busy <- serve(t.Context(), busyBody) }()
	select {
	case response := <-busy:
		if response.Code != http.StatusServiceUnavailable || busyBody.Len() != before {
			t.Fatalf("busy status=%d remaining=%d", response.Code, busyBody.Len())
		}
	case <-time.After(time.Second):
		t.Fatal("busy request queued")
	}
	cancel()
	if response := <-first; response.Code != http.StatusBadGateway {
		t.Fatalf("cancelled status=%d", response.Code)
	}
	if response := serve(t.Context(), strings.NewReader(`{}`)); response.Code != http.StatusOK {
		t.Fatalf("slot not released: %d", response.Code)
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected plugin executions: %d", calls.Load())
	}
}

func TestReceiverReturnsBusyWithoutWaitingForRecovery(t *testing.T) {
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	intake := &intakeStub{service: New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, ErrExecutionBusy
	}), nil, nil)}
	handler, err := NewReceiver(intake, 1024, 1, logger)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{}`))
	request.SetPathValue("source_id", "source")
	request.Header.Set("X-Secret", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
