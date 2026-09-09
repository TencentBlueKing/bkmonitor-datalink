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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// The split exists so a host platform can route the query surface without
// reaching pprof. If pprof ever answers on the query surface again, routing it
// exposes goroutine dumps and CPU profiles.
func TestQuerySurfaceNeverServesPprof(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress("127.0.0.1:0"))
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/profile", "/debug/pprof/symbol", "/debug/pprof/trace"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("query surface %s = %d, want 404", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("query surface /metrics = %d, want 200", response.Code)
	}
}

func TestDiagnosticsSurfaceServesPprof(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}))
	response := httptest.NewRecorder()
	server.DiagnosticsHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics surface /debug/pprof/ = %d, want 200", response.Code)
	}
	// The diagnostics surface carries nothing else, so routing it by mistake
	// still does not expose health or metrics.
	response = httptest.NewRecorder()
	server.DiagnosticsHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("diagnostics surface /metrics = %d, want 404", response.Code)
	}
}

func TestRunServesTheTwoSurfacesOnSeparateListeners(t *testing.T) {
	queryAddress := reserveAddress(t)
	diagnosticsAddress := reserveAddress(t)
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(diagnosticsAddress))

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- server.Run(ctx, queryAddress, time.Second) }()
	waitForStatus(t, "http://"+queryAddress+"/healthz", http.StatusOK)

	if status := get(t, "http://"+queryAddress+"/debug/pprof/"); status != http.StatusNotFound {
		t.Fatalf("pprof on the query listener = %d, want 404", status)
	}
	if status := get(t, "http://"+diagnosticsAddress+"/debug/pprof/"); status != http.StatusOK {
		t.Fatalf("pprof on the diagnostics listener = %d, want 200", status)
	}

	cancel()
	if err := <-runErrors; err != nil {
		t.Fatalf("run: %v", err)
	}
	// Both listeners must be gone once Run returns, or a restart cannot rebind.
	for _, address := range []string{queryAddress, diagnosticsAddress} {
		rebound, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("%s is still bound after shutdown: %v", address, err)
		}
		rebound.Close()
	}
}

// An unset address serves no diagnostics at all. pprof must not quietly fall
// back onto the query surface, which would defeat the split.
func TestEmptyDiagnosticsAddressServesNothing(t *testing.T) {
	queryAddress := reserveAddress(t)
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(""))

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- server.Run(ctx, queryAddress, time.Second) }()
	waitForStatus(t, "http://"+queryAddress+"/healthz", http.StatusOK)

	if status := get(t, "http://"+queryAddress+"/debug/pprof/"); status != http.StatusNotFound {
		t.Fatalf("pprof on the query listener = %d, want 404", status)
	}

	cancel()
	if err := <-runErrors; err != nil {
		t.Fatalf("run: %v", err)
	}
}

// A diagnostics bind failure fails startup instead of degrading quietly: the
// address is deterministic configuration, and losing pprof without saying so
// would only surface during the next incident.
func TestRunFailsWhenDiagnosticsCannotBind(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	queryAddress := reserveAddress(t)
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(occupied.Addr().String()))
	if err := server.Run(context.Background(), queryAddress, time.Second); err == nil {
		t.Fatal("run succeeded with an unbindable diagnostics address, want failure")
	}
	// The query listener must not be left behind by the failed startup.
	probe, err := net.Listen("tcp", queryAddress)
	if err != nil {
		t.Fatalf("query address still bound after failed startup: %v", err)
	}
	probe.Close()
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url) //nolint:noctx // short-lived readiness probe in a test
		if err == nil {
			status := response.StatusCode
			response.Body.Close()
			if status == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("%s did not reach status %d before the deadline", url, want))
}

func get(t *testing.T, url string) int {
	t.Helper()
	response, err := http.Get(url) //nolint:noctx // short-lived probe in a test
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
