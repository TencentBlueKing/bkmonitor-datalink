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
	"strconv"
	"testing"
)

// diagnosisScale is the design's large deployment: 20,000 strategies over
// 15,000 objects on 40 replicas, one percent of the objects on a row.
type diagnosisScale struct {
	universe    []string
	facts       map[string]StrategyLookupFacts
	view        View
	snapshots   []Snapshot
	names       []string
	expectation Expectation
}

func newDiagnosisScale(strategies, objects, replicaCount int) diagnosisScale {
	names := make([]string, replicaCount)
	snapshots := make([]Snapshot, replicaCount)
	for r := range names {
		names[r] = fmt.Sprintf("pod-%02d", r)
		snapshots[r] = Snapshot{Replica: names[r], TakenAt: now}
	}
	ids := make([]string, 0, objects)
	for o := 0; o < objects; o++ {
		qg := fmt.Sprintf("qg-%06d", o)
		ids = append(ids, qg)
		s := &snapshots[o%replicaCount]
		s.Owned++
		s.Determined++
		s.OwnedObjects = append(s.OwnedObjects, qg)
		if o%100 == 0 {
			row := anomaly(qg)
			row.Replica = s.Replica
			row.Strategies = []StrategyRef{{StrategyID: strconv.Itoa(100000 + o%strategies), BusinessID: "2"}}
			s.Anomalies = append(s.Anomalies, row)
			s.TotalAnomalies++
		}
	}
	scale := diagnosisScale{facts: make(map[string]StrategyLookupFacts, strategies)}
	pub := StrategyPublication{SnapshotRevision: "s1", Epoch: 7}
	for i := 0; i < strategies; i++ {
		id := strconv.Itoa(100000 + i)
		scale.universe = append(scale.universe, id)
		qg := fmt.Sprintf("qg-%06d", i%objects)
		scale.facts[id] = StrategyLookupFacts{Available: true, Found: true, Publication: pub,
			Plans:        []StrategyPlanRef{{Tenant: "default", Business: "2", QueryGroup: qg, SnapshotRevision: "s1", QueryRevision: "q", ScheduleRevision: "r"}},
			Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	}
	scale.snapshots, scale.names = snapshots, names
	scale.expectation = Expectation{QueryGroups: objects, Known: true, IDs: ids}
	scale.view = Aggregate(scale.expectation, snapshots, names, now, freshness)
	return scale
}

// BenchmarkDiagnosisFirstPageView is the first page's in-memory part: the
// view aggregated from every replica's snapshot and decided, which a later
// page of the same diagnosis reuses instead of repeating.
func BenchmarkDiagnosisFirstPageView(b *testing.B) {
	scale := newDiagnosisScale(20000, 15000, 40)
	snapshots, names, expectation := scale.snapshots, scale.names, scale.expectation
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		view := Aggregate(expectation, snapshots, names, now, freshness)
		Decide(&view, now, 0)
		_ = newDiagnosisContext(&view, "pod-00", now)
	}
}

// BenchmarkDiagnosisPage is one page of rows against a built view: the work
// every page does, first or cached.
func BenchmarkDiagnosisPage(b *testing.B) {
	scale := newDiagnosisScale(20000, 15000, 40)
	universe, _ := NormalizeUniverse(scale.universe)
	ctx := newDiagnosisContext(&scale.view, "pod-00", now)
	lookup := func(id string) StrategyLookupFacts { return scale.facts[id] }
	for _, rows := range []int{500, 2000} {
		b.Run(strconv.Itoa(rows), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				page := buildDiagnosisPage(universe, "", rows, func(id string) DiagnosisRow { return diagnoseStrategy(id, lookup(id), ctx) })
				if page.RowsWritten != rows {
					b.Fatalf("rows %d", page.RowsWritten)
				}
			}
		})
	}
}
