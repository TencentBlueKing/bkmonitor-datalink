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
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
)

func TestTaskStatusUsesManagementAuthentication(t *testing.T) {
	a := &API{Config: config.DispatchConfig{APIToken: "management", WorkerToken: "worker"}, Tasks: taskstate.New("test", []taskstate.Definition{{ID: "scheduler", Enabled: true, Settings: map[string]any{}}})}
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {"worker", 401}, {"management", 200}} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/control-plane/tasks", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d", w.Code)
		}
		if tc.status == 200 && (w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"id":"scheduler"`)) {
			t.Fatal(w.Body.String())
		}
	}
	a.Tasks = nil
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/control-plane/tasks", nil)
	r.Header.Set("Authorization", "Bearer management")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
