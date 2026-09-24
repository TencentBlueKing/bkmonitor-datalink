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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

type fixedStalls []Stall

func (stalls fixedStalls) Stalls() []Stall { return stalls }

// /healthz answers 200 until a liveness source is installed and while it
// names nothing, and 503 naming each stalled loop once it does.
func TestHealthzFailsNamingEachStalledLoop(t *testing.T) {
	server := New(metric.NewRecorder(metric.BuildInfo{}))
	assertStatus(t, server.Handler(), "/healthz", http.StatusOK)

	server.SetLiveness(fixedStalls(nil))
	assertStatus(t, server.Handler(), "/healthz", http.StatusOK)

	server.SetLiveness(fixedStalls{
		{Loop: "control", Age: 16 * time.Minute, Bound: 15 * time.Minute},
		{Loop: "executions", Age: 6 * time.Minute, Bound: 5 * time.Minute},
	})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz = %d, want 503", recorder.Code)
	}
	var body struct {
		Alive   bool `json:"alive"`
		Stalled []struct {
			Loop         string  `json:"loop"`
			AgeSeconds   float64 `json:"age_seconds"`
			BoundSeconds float64 `json:"bound_seconds"`
		} `json:"stalled"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", recorder.Body.String(), err)
	}
	if body.Alive || len(body.Stalled) != 2 || body.Stalled[0].Loop != "control" || body.Stalled[0].AgeSeconds != 960 ||
		body.Stalled[0].BoundSeconds != 900 || body.Stalled[1].Loop != "executions" {
		t.Fatalf("body = %+v", body)
	}
}
