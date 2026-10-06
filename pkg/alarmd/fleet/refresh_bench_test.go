// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// What one page refresh costs the server, at the size of the deployment
// the page is read on: two thousand objects, a few hundred anomalous, a few
// hundred demoted, a few hundred retained records. The page loads the
// verdict, the object list, the windows and the trend every thirty
// seconds, and whether that is too much is a measurement, not a feeling --
// so this measures the two routes that do the work, over a service whose
// control plane read is already cached, which is the state every refresh
// after the first is in.
func refreshFixture(b *testing.B) (http.Handler, []Snapshot) {
	b.Helper()
	at := now
	snapshot := Snapshot{Replica: "pod-a", TakenAt: at.Add(-10 * time.Second), Owned: 2074, Determined: 2074,
		GapSkips: map[string]SkippedSpan{}, Schedule: &ScheduleCensus{Waiting: 1800, Late: 30, Overdue: 4,
			Completed1h: 9000, OnTime1h: 8964, Completed6h: 54000, OnTime6h: 53700}}
	for index := 0; index < 120; index++ {
		item := anomaly(fmt.Sprintf("qg-anomalous-%d", index))
		item.Kind = KindDegradedRun
		item.CauseReason = "QUERY_TIMEOUT"
		item.Strategies = []StrategyRef{{StrategyID: fmt.Sprintf("%d", 1000+index), BusinessID: fmt.Sprintf("%d", index%9)}}
		snapshot.Anomalies = append(snapshot.Anomalies, item)
	}
	for index := 0; index < 350; index++ {
		item := anomaly(fmt.Sprintf("qg-demoted-%d", index))
		item.Kind = KindQueryCooldown
		item.Failure = &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}
		item.Strategies = []StrategyRef{{StrategyID: fmt.Sprintf("%d", 3000+index), BusinessID: fmt.Sprintf("%d", index%9)}}
		snapshot.Demoted = append(snapshot.Demoted, item)
		snapshot.GapSkips[item.QueryGroup] = SkippedSpan{FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a"}
	}
	for index := 0; index < 50; index++ {
		snapshot.GapSkips[fmt.Sprintf("qg-stopped-%d", index)] = SkippedSpan{FirstSlot: 1, LastSlot: 3, Slots: 3,
			At: at.Add(-time.Hour), Replica: "pod-a"}
	}
	snapshot.TotalAnomalies, snapshot.TotalDemoted = len(snapshot.Anomalies), len(snapshot.Demoted)
	snapshots := []Snapshot{snapshot}
	service, err := NewService(stubExpectations{expectation: Expectation{QueryGroups: 2074, Known: true}},
		stubRegistry{replicas: []string{"pod-a"}}, stubSnapshots{snapshots: snapshots}, time.Minute,
		func() time.Time { return at })
	if err != nil {
		b.Fatal(err)
	}
	handler, err := NewHandler(service, nil, func() time.Time { return at }, 10*time.Minute, nil, nil, "")
	if err != nil {
		b.Fatal(err)
	}
	return handler, snapshots
}

func benchmarkRoute(b *testing.B, target string) {
	handler, _ := refreshFixture(b)
	// The first request pays the control plane read; every refresh after it
	// is what is measured.
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, target, nil))
	if first.Code != http.StatusOK {
		b.Fatalf("%s: status %d", target, first.Code)
	}
	b.ResetTimer()
	size := 0
	for iteration := 0; iteration < b.N; iteration++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		size = recorder.Body.Len()
	}
	b.ReportMetric(float64(size), "bytes/response")
}

func BenchmarkRefreshHealth(b *testing.B)  { benchmarkRoute(b, "/api/health") }
func BenchmarkRefreshObjects(b *testing.B) { benchmarkRoute(b, "/api/objects?limit=50") }
