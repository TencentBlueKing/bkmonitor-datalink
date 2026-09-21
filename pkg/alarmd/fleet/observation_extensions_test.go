// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestObservationCostHTTPReadsOnlyCachedPayloadAndRejectsStale(t *testing.T) {
	at := time.Now()
	cache := NewCostCandidatesCache(func() time.Time { return at }, time.Minute)
	h := WithCostCandidates(http.NotFoundHandler(), cache)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects?scope=cost", nil))
		return w
	}
	if w := request(); w.Code != 503 {
		t.Fatalf("unobserved cost %d", w.Code)
	}
	cache.Update(CostCandidatesSnapshot{ObservedAt: at, Projection: CostProjectionView{Snapshots: []CostProjectionRecord{{Replica: "worker", Cost: json.RawMessage(`{"process_id":"process","enabled":true}`)}}}})
	first := request()
	if first.Code != 200 {
		t.Fatalf("cache=%d", first.Code)
	}
	for i := 0; i < 100; i++ {
		if w := request(); w.Body.String() != first.Body.String() {
			t.Fatal("HTTP recomputed snapshot")
		}
	}
	at = at.Add(2 * time.Minute)
	if w := request(); w.Code != 503 || !strings.Contains(w.Body.String(), "COST_SNAPSHOT_STALE") {
		t.Fatalf("stale %d %s", w.Code, w.Body.String())
	}
}

func TestObservationSampleAPIPreservesLegacyAndExposesExtraBudget(t *testing.T) {
	s, _ := fleetSampleFixture(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	h := WithSeriesSamples(next, nil, nil, nil, s, time.Now)
	for _, path := range []string{"/api/objects/group?records=2", "/api/objects/group?check=COMPLETENESS&group=source"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 202 {
			t.Fatalf("legacy changed %s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/windows", strings.NewReader(`{"query_groups":["group"]}`)))
	if w.Code != 202 {
		t.Fatalf("legacy window changed %d", w.Code)
	}
	for _, p := range []string{"records=1", "check=A", "group=B"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects/group?samples=1&"+p, nil))
		if w.Code != 400 {
			t.Fatalf("sample silently replaces detail: %d", w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/windows?mode=sample", nil))
	var result struct {
		Enabled bool `json:"enabled"`
		Budget  struct {
			Bytes  int                              `json:"combined_bytes_per_minute"`
			Limits observability.SeriesSampleLimits `json:"sample_extra_allocation"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || result.Budget.Bytes != observability.TargetFlowMaxBytes+s.Limits().BytesPerMinute || result.Budget.Limits != s.Limits() {
		t.Fatalf("budget projection %+v", result)
	}
}

func TestObservationUnavailableDirectoryDoesNotDelegateToAnomalyList(t *testing.T) {
	h := WithStrategyDirectory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("strategy request became anomaly list") }), nil, time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects?scope=strategies", nil))
	if w.Code != 503 {
		t.Fatalf("missing directory=%d", w.Code)
	}
}
