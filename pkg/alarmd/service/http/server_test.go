// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpservice

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestLifecycleReadinessAndMetricUseTheSameSource(t *testing.T) {
	t.Parallel()

	source := &mutableLifecycleSource{}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	server, err := NewWithLifecycle(recorder, source)
	if err != nil {
		t.Fatalf("NewWithLifecycle() error = %v", err)
	}
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
	server.SetReady(true)
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)

	source.Set(lifecycle.Snapshot{Ready: true, ConsumerLagKnown: true})
	assertStatus(t, server.Handler(), "/readyz", http.StatusOK)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "bkmonitor_alarmd_ready 1") {
		t.Fatalf("ready metric did not use the readiness source:\n%s", response.Body.String())
	}
}

func TestProbeStateTransitions(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}))

	assertStatus(t, server.Handler(), "/healthz", http.StatusOK)
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)

	server.SetReady(true)
	assertStatus(t, server.Handler(), "/readyz", http.StatusOK)

	server.SetReady(false)
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
}

func TestHealthSnapshotControlsReadinessAndResponse(t *testing.T) {
	t.Parallel()

	source := observability.NewHealthTracker(observability.HealthSnapshot{
		State:             observability.HealthDegraded,
		ConfigLoaded:      true,
		SchemaReady:       true,
		AssignmentReady:   true,
		RuntimeStateReady: true,
		OutputSinkReady:   true,
		Reasons: []observability.ReasonCode{
			observability.ReasonWorkerQueue,
		},
	})
	server, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), source)
	if err != nil {
		t.Fatalf("NewWithHealth() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("ready response = %d/%q", response.Code, response.Header().Get("Content-Type"))
	}
	var snapshot observability.HealthSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if !snapshot.Ready || snapshot.State != observability.HealthDegraded {
		t.Fatalf("readiness snapshot = %#v", snapshot)
	}

	source.Update(observability.HealthSnapshot{State: observability.HealthNotReady})
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
	source.Update(observability.HealthSnapshot{State: observability.HealthFatal})
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
	assertStatus(t, server.Handler(), "/healthz", http.StatusOK)
}

func TestNewWithHealthRequiresInputsAndSingleBinding(t *testing.T) {
	t.Parallel()

	source := observability.NewHealthTracker(observability.HealthSnapshot{})
	if _, err := NewWithHealth(nil, source); err == nil {
		t.Fatal("NewWithHealth(nil, source) returned nil")
	}
	if _, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), nil); err == nil {
		t.Fatal("NewWithHealth(recorder, nil) returned nil")
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	if _, err := NewWithHealth(recorder, source); err != nil {
		t.Fatalf("first NewWithHealth() error = %v", err)
	}
	if _, err := NewWithHealth(recorder, source); err == nil {
		t.Fatal("second NewWithHealth() returned nil")
	}
}

func TestMetricsRemainAvailableBeforeReady(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{Version: "v-test"}))
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "bkmonitor_alarmd_build_info") {
		t.Fatalf("metrics response does not contain build info:\n%s", response.Body.String())
	}
}

func TestRunReturnsBindError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := New(metric.NewRecorder(metric.BuildInfo{}))
	err = server.Run(context.Background(), listener.Addr().String(), time.Second)
	if err == nil {
		t.Fatal("Run() returned nil for an occupied address")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Run() error = %q, want listen context", err)
	}
}

func TestRunStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	server := New(metric.NewRecorder(metric.BuildInfo{}))
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, "127.0.0.1:0", time.Second)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after cancellation = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
}

func TestRunForceClosesConnectionsAfterShutdownTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	server := New(metric.NewRecorder(metric.BuildInfo{}))
	server.handler = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		response.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- server.Run(ctx, address, 10*time.Millisecond)
	}()
	waitUntilReady(t, server)

	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://" + address)
		if response != nil {
			response.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking request did not reach handler")
	}
	cancel()

	select {
	case err := <-runDone:
		if err == nil || !strings.Contains(err.Error(), "shutdown HTTP") {
			t.Fatalf("Run() after shutdown timeout = %v, want shutdown error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after forced close")
	}

	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("request unexpectedly completed without forced connection close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active connection was not force closed")
	}
}

func waitUntilReady(t *testing.T, server *Server) {
	t.Helper()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if server.ready.Load() {
				return
			}
		case <-deadline.C:
			t.Fatal("server did not become ready")
		}
	}
}

func assertStatus(t *testing.T, handler http.Handler, path string, want int) {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s status = %d, want %d", path, response.Code, want)
	}
}

type mutableLifecycleSource struct {
	mu       sync.Mutex
	snapshot lifecycle.Snapshot
}

func (s *mutableLifecycleSource) LifecycleSnapshot() lifecycle.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

func (s *mutableLifecycleSource) Set(snapshot lifecycle.Snapshot) {
	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
}
