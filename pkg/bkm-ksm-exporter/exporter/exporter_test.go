// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubSource struct {
	out string
	err error
}

func (s stubSource) Write(w io.Writer) error {
	if s.err != nil {
		return s.err
	}
	_, err := io.WriteString(w, s.out)
	return err
}

func TestHandlerMetrics(t *testing.T) {
	s := New("127.0.0.1:0")
	s.Register(stubSource{out: "kube_hpa_metadata_generation{namespace=\"d\",hpa=\"h\"} 1\n"})
	s.SetReady(true)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "kube_hpa_metadata_generation") {
		t.Fatalf("/metrics body missing metric: %q", rec.Body.String())
	}
}

func TestHandlerMetricsSourceError(t *testing.T) {
	s := New("127.0.0.1:0")
	s.Register(stubSource{err: io.ErrUnexpectedEOF})
	s.SetReady(true)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("/metrics status = %d, want 500 on source error", rec.Code)
	}
}

func TestHandlerHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	New("127.0.0.1:0").Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
}

// TestMetricsNotReadyReturns503 is the regression test for the dark-dashboard
// fix (P2): before the cache has synced, /metrics must fail the scrape (503)
// rather than return a 200 with no samples that looks like "zero HPAs".
func TestMetricsNotReadyReturns503(t *testing.T) {
	s := New("127.0.0.1:0")
	s.Register(stubSource{out: "kube_hpa_metadata_generation{namespace=\"d\",hpa=\"h\"} 1\n"})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/metrics before ready = %d, want 503", rec.Code)
	}

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics after ready = %d, want 200", rec.Code)
	}
}

func TestReadyzReflectsReadiness(t *testing.T) {
	s := New("127.0.0.1:0")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz before sync = %d, want 503", rec.Code)
	}

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz after sync = %d, want 200", rec.Code)
	}
}

// TestRunShutsDownOnContextCancel is the regression test for the startup-hang
// fix (GS-1): Run must return promptly (nil) when its context is cancelled,
// rather than blocking forever.
func TestRunShutsDownOnContextCancel(t *testing.T) {
	s := New("127.0.0.1:0") // :0 binds an ephemeral port
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(50 * time.Millisecond) // let ListenAndServe bind
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on graceful shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of context cancel (would have hung)")
	}
}
