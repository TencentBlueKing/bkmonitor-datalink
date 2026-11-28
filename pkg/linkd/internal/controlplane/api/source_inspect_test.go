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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
)

func TestSourceInspectionManagementAuthentication(t *testing.T) {
	calls := 0
	a := &API{Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}, SourceInspector: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.PathValue("source_id") != "source" {
			t.Error("source path lost")
		}
		w.WriteHeader(200)
	})}
	path := "/api/v1/event-sources/source/plugin-preview"
	for _, header := range []string{"X-Secret", "Authorization", "Internal-Token"} {
		r := httptest.NewRequestWithContext(t.Context(), "POST", path, strings.NewReader(`{}`))
		r.Header.Set(header, "worker")
		if header == "Authorization" {
			r.Header.Set(header, "Bearer worker")
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 401 || calls != 0 {
			t.Fatalf("%s bypassed management auth: %d", header, w.Code)
		}
	}
	if w := policyHTTP(t, a.Handler(), "POST", path, `{}`); w.Code != 200 || calls != 1 {
		t.Fatalf("JWT preview: %d calls=%d", w.Code, calls)
	}
	a.SourceInspector = nil
	if w := policyHTTP(t, a.Handler(), "POST", path, `{}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
