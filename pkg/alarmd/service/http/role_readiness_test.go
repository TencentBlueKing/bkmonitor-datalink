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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type roleProbeHealth struct{ snapshot observability.HealthSnapshot }

func (h roleProbeHealth) HealthSnapshot() observability.HealthSnapshot { return h.snapshot }

func TestRoleReadinessIsIndependentOfBusinessReadiness(t *testing.T) {
	t.Parallel()

	source := roleProbeHealth{snapshot: observability.HealthSnapshot{
		State:        observability.HealthNotReady,
		ConfigLoaded: true,
		RoleReadiness: map[string]string{
			"channel": "ready",
			"control": "degraded",
			"worker":  "not_ready",
		},
	}}
	server, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), source)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, server.Handler(), "/readyz", http.StatusServiceUnavailable)
	assertStatus(t, server.Handler(), "/readyz?roles=channel", http.StatusOK)
	assertStatus(t, server.Handler(), "/readyz?roles=channel,control", http.StatusOK)
	assertStatus(t, server.Handler(), "/readyz?roles=control,worker", http.StatusServiceUnavailable)
	assertStatus(t, server.Handler(), "/readyz?roles=worker", http.StatusServiceUnavailable)

	response := httptest.NewRecorder()
	server.internalHandler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz?roles=channel", nil))
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 5 || string(body["ready"]) != "true" {
		t.Fatalf("role probe body = %s", response.Body.String())
	}
	var selected map[string]string
	if err := json.Unmarshal(body["role_readiness"], &selected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, map[string]string{"channel": "ready"}) {
		t.Fatalf("role probe includes unrequested roles: %v", selected)
	}
}

func TestRoleReadinessGatesAndPublicRestriction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		query    string
		state    observability.HealthState
		loaded   bool
		draining bool
		roles    map[string]string
		want     int
	}{
		{name: "ready", query: "channel", loaded: true, roles: map[string]string{"channel": "ready"}, want: http.StatusOK},
		{name: "degraded", query: "channel", loaded: true, roles: map[string]string{"channel": "degraded"}, want: http.StatusOK},
		{name: "starting", query: "channel", loaded: true, roles: map[string]string{"channel": "starting"}, want: http.StatusServiceUnavailable},
		{name: "auth unavailable", query: "channel", loaded: true, roles: map[string]string{"channel": "not_ready"}, want: http.StatusServiceUnavailable},
		{name: "disabled", query: "channel", loaded: true, roles: map[string]string{"channel": "disabled"}, want: http.StatusServiceUnavailable},
		{name: "unknown state", query: "channel", loaded: true, roles: map[string]string{"channel": "unexpected"}, want: http.StatusServiceUnavailable},
		{name: "missing role", query: "channel", loaded: true, roles: map[string]string{"worker": "ready"}, want: http.StatusServiceUnavailable},
		{name: "unknown role", query: "other", loaded: true, roles: map[string]string{"channel": "ready"}, want: http.StatusServiceUnavailable},
		{name: "control and worker", query: "control,worker", loaded: true, roles: map[string]string{"control": "ready", "worker": "degraded"}, want: http.StatusOK},
		{name: "config loading", query: "channel", roles: map[string]string{"channel": "ready"}, want: http.StatusServiceUnavailable},
		{name: "fatal", query: "channel", state: observability.HealthFatal, loaded: true, roles: map[string]string{"channel": "ready"}, want: http.StatusServiceUnavailable},
		{name: "draining state", query: "channel", state: observability.HealthDraining, loaded: true, roles: map[string]string{"channel": "ready"}, want: http.StatusServiceUnavailable},
		{name: "draining flag", query: "channel", loaded: true, draining: true, roles: map[string]string{"channel": "ready"}, want: http.StatusServiceUnavailable},
		{name: "empty", query: "", loaded: true, want: http.StatusBadRequest},
		{name: "empty entry", query: "channel,", loaded: true, want: http.StatusBadRequest},
		{name: "empty first entry", query: ",channel", loaded: true, want: http.StatusBadRequest},
		{name: "duplicate", query: "channel,channel", loaded: true, want: http.StatusBadRequest},
		{name: "duplicate query", query: "channel&roles=worker", loaded: true, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			source := roleProbeHealth{snapshot: observability.HealthSnapshot{
				State: tt.state, ConfigLoaded: tt.loaded, Draining: tt.draining, RoleReadiness: tt.roles,
			}}
			server, err := NewWithHealth(metric.NewRecorder(metric.BuildInfo{}), source, WithRestrictedPublicSurface())
			if err != nil {
				t.Fatal(err)
			}
			for _, surface := range []struct {
				name    string
				handler http.Handler
				public  bool
			}{
				{name: "restricted public", handler: server.Handler(), public: true},
				{name: "internal", handler: server.internalHandler},
			} {
				response := httptest.NewRecorder()
				surface.handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz?roles="+tt.query, nil))
				if response.Code != tt.want {
					t.Fatalf("%s response = %d, want %d: %s", surface.name, response.Code, tt.want, response.Body.String())
				}
				if surface.public && (response.Body.Len() != 0 || len(response.Header()) != 0) {
					t.Fatalf("restricted public response exposes probe details: headers %v, body %s", response.Header(), response.Body.String())
				}
			}
		})
	}
}

func TestRoleReadinessWithoutHealthSourceFailsClosed(t *testing.T) {
	t.Parallel()

	server := New(metric.NewRecorder(metric.BuildInfo{}))
	server.SetReady(true)
	assertStatus(t, server.Handler(), "/readyz", http.StatusOK)
	assertStatus(t, server.Handler(), "/readyz?roles=channel", http.StatusServiceUnavailable)
	assertStatus(t, server.internalHandler, "/readyz?roles=channel", http.StatusServiceUnavailable)
}
