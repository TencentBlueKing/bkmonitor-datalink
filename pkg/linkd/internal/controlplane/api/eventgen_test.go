// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventgen"
)

type emptyEventgenDocuments struct{}

func (emptyEventgenDocuments) Get(context.Context, string, string) (json.RawMessage, string, error) {
	return nil, "", eventgen.ErrNotFound
}

func (emptyEventgenDocuments) Put(context.Context, string, string, string, json.RawMessage) error {
	return nil
}

func (emptyEventgenDocuments) List(context.Context, string, string, string, int) ([]json.RawMessage, error) {
	return []json.RawMessage{}, nil
}

func TestEventgenAuthenticationTenantAndInput(t *testing.T) {
	service := eventgen.NewService(emptyEventgenDocuments{}, nil)
	handler := (&API{Eventgen: service, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
	for _, tc := range []struct {
		name, method, path, body, token string
		worker                          bool
		want                            int
	}{
		{"no authentication", "GET", "/api/v1/eventgen/tasks?bk_tenant_id=tenant-a", "", "", false, 401},
		{"tenant is required", "GET", "/api/v1/eventgen/tasks", "", testJWT(t, "admin"), false, 400},
		{"scoped list", "GET", "/api/v1/eventgen/tasks?bk_tenant_id=tenant-a", "", testJWT(t, "admin"), false, 200},
		{"management token cannot impersonate runner", "POST", "/internal/eventgen/heartbeat", `{"session_id":"test"}`, testJWT(t, "admin"), false, 401},
		{"worker token cannot read management API", "GET", "/api/v1/eventgen/tasks?bk_tenant_id=tenant-a", "", "worker", true, 401},
		{"worker registers", "POST", "/internal/eventgen/heartbeat", `{"session_id":"test","new_session":true}`, "worker", true, 200},
		{"preview tenant mismatch", "POST", "/api/v1/eventgen/preview?bk_tenant_id=tenant-a", `{"config":{"bk_tenant_id":"tenant-b"},"count":1}`, testJWT(t, "admin"), false, 400},
		{"unknown input", "POST", "/api/v1/eventgen/import?bk_tenant_id=tenant-a", `{"csv":"id\n1","columns":{},"command":"forbidden"}`, testJWT(t, "admin"), false, 400},
		{"typed import", "POST", "/api/v1/eventgen/import?bk_tenant_id=tenant-a", `{"csv":"id\n1","columns":{"id":"number"}}`, testJWT(t, "admin"), false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
			if tc.worker {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			} else {
				r.Header.Set("Internal-Token", tc.token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d wanted=%d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
