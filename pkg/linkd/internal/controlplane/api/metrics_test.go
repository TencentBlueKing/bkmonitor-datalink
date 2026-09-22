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
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func TestMetricCatalogManagementAPI(t *testing.T) {
	t.Parallel()
	// Sources、Controller、Previewer 均为空，目录不能依赖已启动的业务模块。
	handler := (&API{Config: config.DispatchConfig{APIToken: "admin", WorkerToken: "worker"}}).Handler()
	for _, tc := range []struct {
		token, method string
		status        int
	}{
		{"", "GET", 401}, {"worker", "GET", 401}, {"admin", "GET", 200}, {"admin", "POST", 405},
	} {
		r := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/v1/metrics/catalog", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.token, w.Code, tc.status)
		}
		if tc.status != http.StatusOK {
			continue
		}
		var catalog telemetry.Catalog
		if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
			t.Fatal(err)
		}
		if catalog.SchemaVersion != 1 || len(catalog.Metrics) == 0 || len(catalog.Modules) == 0 {
			t.Fatal("missing catalog metadata")
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("unexpected content type")
		}
	}
}
