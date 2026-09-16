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
	"reflect"
	"testing"
	"time"
)

// Stalling is marked on every column, not the anomaly list alone: an object
// in the demoted pool that stopped ending rounds is stuck whether or not the
// reader is looking at that pool, and the verdict is settled with it known.
// The metric path used to mark the anomaly list only, so the page and the
// metric could disagree on the same view.
func TestDecideMarksStallingOnEveryColumnThenSettles(t *testing.T) {
	stuck := func(queryGroup string) Anomaly {
		item := anomaly(queryGroup)
		item.Kind = KindDegradedRun
		item.FailingSince = now.Add(-time.Hour)
		return item
	}
	view := View{Health: HealthHealthy,
		Anomalies: []Anomaly{}, Demoted: []Anomaly{stuck("demoted-stuck")},
		Undecidable: []Anomaly{stuck("undecidable-stuck")}, ByDesign: []Anomaly{stuck("by-design-stuck")}}

	Decide(&view, now, 10*time.Minute)

	for _, column := range [][]Anomaly{view.Demoted, view.Undecidable, view.ByDesign} {
		if !column[0].Stalled || column[0].Finding.Check != CheckRoundsStalled {
			t.Fatalf("%s: stalled=%v check=%s, want marked stalled and filed under ROUNDS_STALLED",
				column[0].QueryGroup, column[0].Stalled, column[0].Finding.Check)
		}
	}

	// Within the budget nothing is stalled, on any column.
	fresh := View{Demoted: []Anomaly{stuck("demoted-fresh")}}
	Decide(&fresh, now, 2*time.Hour)
	if fresh.Demoted[0].Stalled {
		t.Fatalf("an object failing for an hour is not stalled against a two hour budget")
	}

	// The verdict is settled with the stalling known. An object whose last
	// code was the backend's reads external, and a view with nothing but
	// external anomalies is HEALTHY -- until the object stalls, which makes it
	// ours and the verdict DEGRADED. Marking without settling leaves the
	// HEALTHY verdict standing over a stuck object.
	external := []Anomaly{{QueryGroup: "qg-stuck", Kind: KindDegradedRun, Replica: "pod-a",
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT", FailingSince: now.Add(-2 * time.Hour)}}
	Attribute(external, now)
	settled := View{Anomalies: external, PerReplica: []ReplicaView{{Replica: "pod-a"}}}
	Settle(&settled)
	if settled.Health != HealthHealthy || settled.Anomalies[0].Attribution != AttributionExternal {
		t.Fatalf("before the stall: health=%s attribution=%s; the fixture must start HEALTHY on an external anomaly",
			settled.Health, settled.Anomalies[0].Attribution)
	}
	Decide(&settled, now, time.Hour)
	if settled.Health != HealthDegraded || settled.PerReplica[0].Ours != 1 {
		t.Fatalf("after the stall: health=%s ours=%d, want DEGRADED with the replica's own count refreshed",
			settled.Health, settled.PerReplica[0].Ours)
	}
}

// Report is the page's first screen and the metric's, from one call: the
// four columns in table order, which of them a replica truncated, and the
// lines and arithmetic drawn from exactly those. A second path that built
// the columns itself is how the two drifted.
func TestReportIsTheOneFirstScreen(t *testing.T) {
	view := View{
		Anomalies: []Anomaly{anomaly("qg-1")}, AnomaliesTotal: 3,
		Demoted: []Anomaly{anomaly("qg-2")}, DemotedTotal: 1,
		Undecidable: []Anomaly{}, UndecidableTotal: 0,
		ByDesign: []Anomaly{anomaly("qg-3")}, ByDesignTotal: 5,
	}
	for _, list := range [][]Anomaly{view.Anomalies, view.Demoted, view.ByDesign} {
		attribute(&list[0], now)
	}

	screen := Report(&view, now)

	wantColumns := [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign}
	if !reflect.DeepEqual(screen.Columns, wantColumns) {
		t.Fatalf("columns = %d lists, want the four in table order", len(screen.Columns))
	}
	wantTruncated := map[string]bool{ColumnAnomalies: true, ColumnDemoted: false, ColumnUndecidable: false, ColumnByDesign: true}
	if !reflect.DeepEqual(screen.Truncated, wantTruncated) {
		t.Fatalf("truncated = %v, want %v (a column is a sample when its total exceeds its list)", screen.Truncated, wantTruncated)
	}
	if want := ReportChecks(wantColumns, wantTruncated, &view, now); !reflect.DeepEqual(screen.Checks, want) {
		t.Fatalf("checks = %+v, want the lines ReportChecks draws from the same columns: %+v", screen.Checks, want)
	}
	if want := SummarizeTodo(screen.Checks, wantColumns, &view, now); !reflect.DeepEqual(screen.Todo, want) {
		t.Fatalf("todo = %+v, want %+v", screen.Todo, want)
	}
	if len(screen.Checks) == 0 || screen.Todo.Objects+screen.Todo.GovernanceObjects != 3 {
		t.Fatalf("the fixture must put three objects on lines: checks=%d objects=%d+%d",
			len(screen.Checks), screen.Todo.Objects, screen.Todo.GovernanceObjects)
	}
}

// The count a line's metric carries is the count the line prints: current
// objects for an object check -- not the records of past loss kept under it,
// which would hold the series up for an hour after the loss -- and replicas
// for the two standings, which have no objects; a replica under two bounds
// is one replica.
func TestLineCountIsWhatTheLinePrints(t *testing.T) {
	cases := []struct {
		name   string
		report CheckReport
		want   int
	}{
		{"object check counts current objects", CheckReport{Code: CheckSlotsOverdue, Objects: 7, Current: 7}, 7},
		{"records of past loss are not current", CheckReport{Code: CheckDetectionAbandoned, Objects: 395, Current: 2, Retained: 393}, 2},
		{"a line held only by records is down", CheckReport{Code: CheckTimelinePruned, Objects: 12, Retained: 12}, 0},
		{"the activation standing is its one replica", CheckReport{Code: CheckCutoverFailing, Replica: "pod-a",
			Groups: []CheckGroup{{Key: "schedule_cutover/schedule_conflict", Replicas: []string{"pod-a"}}}}, 1},
		// Three replicas, two kinds, four entries: the answer is neither the
		// kinds nor the entries.
		{"replicas under bounds are counted once each", CheckReport{Code: CheckReplicaDegraded,
			Groups: []CheckGroup{
				{Key: string(DegradationOpenAlertSetStale), Replicas: []string{"pod-a", "pod-b", "pod-c"}},
				{Key: string(DegradationControlLeaderAbsent), Replicas: []string{"pod-a"}},
			}}, 3},
		{"a standing with no replicas is down", CheckReport{Code: CheckReplicaDegraded}, 0},
		// The split names the pair it is between, not the objects it would
		// move: 23 moves a round is the batch, 1185 over the target is the
		// excess, and neither is the line's count.
		{"the split standing is its two replicas", CheckReport{Code: CheckOwnershipSkewed, Replica: "pod-a",
			Rebalance: &RebalanceFacts{MostOwned: 2370, Target: 1185, PlannedMoves: 23},
			Groups:    []CheckGroup{{Key: "pod-a", Replicas: []string{"pod-a", "pod-b"}}}}, 2},
	}
	for _, tc := range cases {
		if got := tc.report.LineCount(); got != tc.want {
			t.Fatalf("%s: line count = %d, want %d", tc.name, got, tc.want)
		}
	}
}
