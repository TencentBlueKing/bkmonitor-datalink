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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
)

// readinessWithBody is a health source, whose readiness answer carries a
// body a test can tell sent from dropped.
type readinessWithBody struct{ ready bool }

func (s readinessWithBody) HealthSnapshot() observability.HealthSnapshot {
	if !s.ready {
		return observability.HealthSnapshot{State: observability.HealthNotReady, ConfigLoaded: true}
	}
	return observability.HealthSnapshot{State: observability.HealthReady, ConfigLoaded: true, SchemaReady: true,
		AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true}
}

func serve(handler http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

// Restricted: /metrics is refused in public as moved and served on the
// internal listener; readiness keeps its status code in public, for a
// kubelet that still probes there, and loses its body; the internal listener
// has the body. Liveness and the page are unchanged.
func TestARestrictedSurfaceMovesMetricsAndTheReadinessBodyInside(t *testing.T) {
	for _, ready := range []bool{true, false} {
		server, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), readinessWithBody{ready: ready}, WithRestrictedPublicSurface())
		if err != nil {
			t.Fatal(err)
		}
		public, internal := server.Handler(), server.InternalHandler()
		if got := serve(public, "/metrics"); got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), publicsurface.RestrictedCode) {
			t.Fatalf("public /metrics = %d %.80s, want the restricted refusal", got.Code, got.Body.String())
		}
		if got := serve(internal, "/metrics"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "# HELP") {
			t.Fatalf("internal /metrics = %d", got.Code)
		}
		want := http.StatusOK
		if !ready {
			want = http.StatusServiceUnavailable
		}
		publicReady, internalReady := serve(public, "/readyz"), serve(internal, "/readyz")
		if publicReady.Code != want || publicReady.Body.Len() != 0 {
			t.Fatalf("ready=%v public /readyz = %d with %d bytes, want %d and no body", ready, publicReady.Code, publicReady.Body.Len(), want)
		}
		if internalReady.Code != want || !strings.Contains(internalReady.Body.String(), `"config_loaded":true`) {
			t.Fatalf("ready=%v internal /readyz = %d %.80s, want the full answer", ready, internalReady.Code, internalReady.Body.String())
		}
		if got := serve(public, "/healthz"); got.Code != http.StatusOK {
			t.Fatalf("public /healthz = %d", got.Code)
		}
		if got := serve(public, "/"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "alarmd 对象与判定") {
			t.Fatalf("public page = %d", got.Code)
		}
	}
}

// Unrestricted: the query surface is what it always was -- /metrics and the
// readiness body in public -- whatever internal listener is also served.
func TestAnUnrestrictedSurfaceIsUnchanged(t *testing.T) {
	for _, internalAddress := range []string{"", "127.0.0.1:0"} {
		server, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), readinessWithBody{ready: true}, WithInternalAddress(internalAddress))
		if err != nil {
			t.Fatal(err)
		}
		if got := serve(server.Handler(), "/metrics"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "# HELP") {
			t.Fatalf("internal=%q public /metrics = %d", internalAddress, got.Code)
		}
		if got := serve(server.Handler(), "/readyz"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"config_loaded":true`) {
			t.Fatalf("internal=%q public /readyz lost its body: %.80s", internalAddress, got.Body.String())
		}
	}
}

// Run binds the internal listener: the scrape reads /metrics there while the
// query listener refuses it, and both are gone once Run returns.
func TestRunServesMetricsOnTheInternalListener(t *testing.T) {
	queryAddress, internalAddress := reserveAddress(t), reserveAddress(t)
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(""), WithInternalAddress(internalAddress), WithRestrictedPublicSurface())
	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- server.Run(ctx, queryAddress, time.Second) }()
	waitForStatus(t, "http://"+internalAddress+"/healthz", http.StatusOK)
	response, err := http.Get("http://" + internalAddress + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "# HELP") {
		t.Fatalf("internal /metrics = %d", response.StatusCode)
	}
	if status := get(t, "http://"+queryAddress+"/metrics"); status != http.StatusForbidden {
		t.Fatalf("query /metrics = %d, want 403", status)
	}
	cancel()
	if err := <-runErrors; err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, address := range []string{queryAddress, internalAddress} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("%s still bound after Run returned: %v", address, err)
		}
		_ = listener.Close()
	}
}

// An internal address that cannot bind fails Run, as the diagnostics one
// does: a restricted surface without it has no scrape.
func TestRunFailsWhenTheInternalListenerCannotBind(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithDiagnosticsAddress(""), WithInternalAddress(occupied.Addr().String()))
	err = server.Run(context.Background(), reserveAddress(t), time.Second)
	if err == nil || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("run = %v, want the internal bind failure", err)
	}
}

// The runtime settles the surface after the listener starts: a restricted
// start opens when told, and closes again when told.
func TestTheRuntimeSettlesTheRestriction(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithRestrictedPublicSurface())
	for _, tc := range []struct {
		restricted bool
		want       int
	}{{true, http.StatusForbidden}, {false, http.StatusOK}, {true, http.StatusForbidden}} {
		server.SetPublicSurfaceRestricted(tc.restricted)
		if got := serve(server.Handler(), "/metrics"); got.Code != tc.want {
			t.Fatalf("restricted=%v public /metrics = %d, want %d", tc.restricted, got.Code, tc.want)
		}
	}
}

// The login page is public on a restricted surface, under both its names,
// and loads nothing else: every resource it needs is inside it, so there is
// no second route that restriction could cut. The refusal of /metrics names
// it relative to where it was refused.
func TestTheLoginPageIsPublicAndSelfContained(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}), WithRestrictedPublicSurface())
	for _, path := range []string{"/cli", "/cli.html", "/cli/"} {
		got := serve(server.Handler(), path)
		if got.Code != http.StatusOK || !strings.HasPrefix(got.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s = %d %q", path, got.Code, got.Header().Get("Content-Type"))
		}
		page := got.Body.String()
		for _, loads := range []string{"src=", "<link", "@import", "url("} {
			if strings.Contains(page, loads) {
				t.Errorf("%s loads a resource through %q; it would need a public route of its own", path, loads)
			}
		}
	}
	refused := serve(server.Handler(), "/metrics")
	if !strings.Contains(refused.Body.String(), `"login_href":"cli"`) || !strings.Contains(refused.Body.String(), `"login_page":"cli"`) {
		t.Fatalf("/metrics refusal = %s, want the login page named", refused.Body.String())
	}
}
