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
	"reflect"
	"testing"
	"time"
)

// The counts take the store's per-list budget like every list in the
// snapshot, cut on a copy and said cut.
func TestTheStoreCutsOpenAlertCountsToTheirBudget(t *testing.T) {
	store := &RedisStore{maxAnomalyBytes: 120}
	counts := []StrategyOpenCount{}
	for index := 0; index < 20; index++ {
		counts = append(counts, StrategyOpenCount{Strategy: fmt.Sprintf("%04d", index), Count: index + 1})
	}
	snapshot := Snapshot{Replica: "r1", TakenAt: time.Unix(10_000, 0), OpenAlerts: &OpenAlertCounts{Counts: counts, Unknown: []string{"9001"}}}
	written, err := store.written(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !written.OpenAlerts.Cut || len(written.OpenAlerts.Counts) >= 20 || len(written.OpenAlerts.Counts) == 0 {
		t.Fatalf("counts %d cut %v, want a cut prefix", len(written.OpenAlerts.Counts), written.OpenAlerts.Cut)
	}
	if !reflect.DeepEqual(written.OpenAlerts.Counts, counts[:len(written.OpenAlerts.Counts)]) || len(snapshot.OpenAlerts.Counts) != 20 {
		t.Fatalf("kept %v; the caller's list is %d long", written.OpenAlerts.Counts, len(snapshot.OpenAlerts.Counts))
	}
}

func openBoard(t *testing.T, snapshots []Snapshot, plans map[string][]DiagnosisPlan) map[string]BoardRow {
	t.Helper()
	view := &View{}
	aggregateOpenAlerts(view, snapshots)
	universe := []string{}
	for id := range plans {
		universe = append(universe, id)
	}
	index := buildBoard("c1", time.Unix(1, 0), "d", universe, func(id string) DiagnosisRow {
		return DiagnosisRow{StrategyID: id, Verdict: StateDetecting, Plans: plans[id]}
	}, nil, nil, nil, view.OpenAlerts, nil)
	return boardRows(index)
}

// Two replicas holding a strategy's objects keep copies of one consumer set:
// the count is the larger, exact when both counted it whole; a replica that
// could not count it leaves a lower bound if the other counted some, and
// nothing known otherwise; a strategy no replica holds has no count.
func TestBoardOpenAlertsAreTheLargestCopyAndSayHowSure(t *testing.T) {
	rows := openBoard(t, []Snapshot{
		{Replica: "a", OpenAlerts: &OpenAlertCounts{Counts: []StrategyOpenCount{{Strategy: "1", Count: 3}, {Strategy: "2", Count: 4}}, Unknown: []string{"3"}}},
		{Replica: "b", OpenAlerts: &OpenAlertCounts{Counts: []StrategyOpenCount{{Strategy: "1", Count: 5}}, Unknown: []string{"2"}}},
		{Replica: "c"},
	}, map[string][]DiagnosisPlan{
		"1": {boardPlan("g1", "a"), boardPlan("g2", "b")},
		"2": {boardPlan("g3", "a"), boardPlan("g4", "b")},
		"3": {boardPlan("g5", "a")},
		"4": {boardPlan("g6", "a")},
		"5": {boardPlan("g7", "c")},
		"6": nil,
	})
	want := map[string]*BoardCount{
		"1": {Value: 5},                // both whole: the larger
		"2": {Value: 4, AtLeast: true}, // b could not count it
		"3": nil,                       // its only replica could not count it
		"4": {Value: 0},                // counted whole, nothing open
		"5": nil,                       // its replica published no counts
		"6": nil,                       // no object anywhere
	}
	for id, count := range want {
		if !reflect.DeepEqual(rows[id].OpenAlerts, count) {
			t.Errorf("strategy %s open alerts %+v, want %+v", id, rows[id].OpenAlerts, count)
		}
	}
}

// A replica whose counts were cut leaves its strategies a lower bound.
func TestBoardOpenAlertsAreAtLeastWhenAReplicaCut(t *testing.T) {
	rows := openBoard(t, []Snapshot{
		{Replica: "a", OpenAlerts: &OpenAlertCounts{Counts: []StrategyOpenCount{{Strategy: "1", Count: 2}}, Cut: true}},
	}, map[string][]DiagnosisPlan{"1": {boardPlan("g1", "a")}, "2": {boardPlan("g2", "a")}})
	if !reflect.DeepEqual(rows["1"].OpenAlerts, &BoardCount{Value: 2, AtLeast: true}) || rows["2"].OpenAlerts != nil {
		t.Fatalf("strategy 1 %+v strategy 2 %+v, want at least 2 and unknown", rows["1"].OpenAlerts, rows["2"].OpenAlerts)
	}
}

// A replica that could not name its owned objects' strategies may not track
// one of them, so absence there is not zero: no exact count, never Value 0.
func TestBoardOpenAlertsAreNotExactWhenAReplicaCouldNotNameItsStrategies(t *testing.T) {
	rows := openBoard(t, []Snapshot{
		{Replica: "a", OpenAlerts: &OpenAlertCounts{Counts: []StrategyOpenCount{{Strategy: "1", Count: 2}}, OwnedUnnamed: true}},
	}, map[string][]DiagnosisPlan{"1": {boardPlan("g1", "a")}, "2": {boardPlan("g2", "a")}})
	if !reflect.DeepEqual(rows["1"].OpenAlerts, &BoardCount{Value: 2, AtLeast: true}) || rows["2"].OpenAlerts != nil {
		t.Fatalf("strategy 1 %+v strategy 2 %+v, want at least 2 and unknown", rows["1"].OpenAlerts, rows["2"].OpenAlerts)
	}
}
