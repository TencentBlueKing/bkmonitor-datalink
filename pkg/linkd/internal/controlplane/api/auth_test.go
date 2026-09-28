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
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/internaltoken"
)

func testJWT(t *testing.T, key string) string {
	t.Helper()
	if key == "" {
		return ""
	}
	signer, err := internaltoken.New(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := signer.Sign("test-caller")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAuthenticationBoundary(t *testing.T) {
	handler := (&API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "secret"}, WorkerToken: "worker"}, Tasks: taskstate.New("test", nil)}).Handler()
	token := testJWT(t, "secret")
	for _, tc := range []struct {
		name, path, authorization string
		jwt                       []string
		status                    int
	}{
		{"JWT management", "/api/v1/control-plane/tasks", "", []string{token}, 200},
		{"legacy token denied", "/api/v1/control-plane/tasks", "Bearer secret", nil, 401},
		{"worker token denied", "/api/v1/control-plane/tasks", "Bearer worker", nil, 401},
		{"missing token", "/api/v1/control-plane/tasks", "", nil, 401},
		{"no JWT fallback", "/api/v1/control-plane/tasks", "Bearer secret", []string{"invalid"}, 401},
		{"duplicate headers", "/api/v1/control-plane/tasks", "", []string{token, token}, 401},
		{"JWT cannot call worker", "/internal/missing", "", []string{token}, 401},
		{"worker reaches router", "/internal/missing", "Bearer worker", nil, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", tc.path, nil)
			r.Header.Set("Authorization", tc.authorization)
			for _, value := range tc.jwt {
				r.Header.Add(internaltoken.HeaderName, value)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
		})
	}
}

func TestMissingJWTSecretFailsClosed(t *testing.T) {
	handler := (&API{Config: config.DispatchConfig{WorkerToken: "worker"}}).Handler()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/control-plane/tasks", nil)
	r.Header.Set(internaltoken.HeaderName, testJWT(t, "secret"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}
