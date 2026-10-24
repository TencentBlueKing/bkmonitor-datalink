// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpservice

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// /internal/ is served on the cluster-only listener alone: 503 until the
// runtime installs it, then the installed handler; never on the query surface.
func TestTheInternalAPIIsOnlyOnTheInternalListener(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}))
	call := func(handler http.Handler) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/internal/v1/no-data-closures", nil))
		return response.Code
	}
	if code := call(server.InternalHandler()); code != http.StatusServiceUnavailable {
		t.Fatalf("before install the internal listener answered %d, want 503", code)
	}
	server.SetInternalAPI(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusTeapot)
	}))
	if code := call(server.InternalHandler()); code != http.StatusTeapot {
		t.Fatalf("after install the internal listener answered %d", code)
	}
	if code := call(server.Handler()); code == http.StatusTeapot {
		t.Fatal("the query surface serves the internal API")
	}
}
