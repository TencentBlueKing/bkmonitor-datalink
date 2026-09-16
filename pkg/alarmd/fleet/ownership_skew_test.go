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
	"testing"
	"time"
)

// A leader's rebalance round on a running deployment: two ready replicas,
// one holding everything after the other left and came back, and the
// scheduler planning a batch of moves it does not publish.
func skewedRound(at time.Time) *RebalanceFacts {
	return &RebalanceFacts{PlannedAt: at, ReadyWorkers: 2, Assigned: 2370, Target: 1185, MostOwned: 2370, LeastOwned: 0,
		MostOwnedBy: "pod-a", LeastOwnedBy: "pod-b", Batch: 23, PlannedMoves: 23, StopSpreadPercent: 5, Shadow: true}
}

// The leader's round is the deployment's, kept whole on the view with the
// replica that planned it. Newest wins: a replica that stopped being the
// leader still publishes the last round it planned, and after a leader
// change the view would otherwise depend on which snapshot the fold
// visited last. A deployment where no replica planned has no round, which
// is not the same as a round that moves nothing.
func TestAggregateCarriesTheNewestRebalanceRound(t *testing.T) {
	older := skewedRound(now.Add(-2 * time.Minute))
	older.PlannedMoves, older.MostOwnedBy, older.LeastOwnedBy = 0, "", ""
	newer := skewedRound(now.Add(-10 * time.Second))
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, Owned: 2, Determined: 2, Rebalance: newer},
		{Replica: "pod-b", TakenAt: now, Owned: 0, Determined: 0, Rebalance: older},
	}
	view := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if view.Rebalance == nil || *view.Rebalance != *newer || view.RebalanceReplica != "pod-a" {
		t.Fatalf("view rebalance = %+v from %q, want pod-a's newer round, whole", view.Rebalance, view.RebalanceReplica)
	}
	// The same two snapshots in the other order reach the same round.
	reversed := Aggregate(Expectation{Known: true, QueryGroups: 2}, []Snapshot{snapshots[1], snapshots[0]}, []string{"pod-b", "pod-a"}, now, freshness)
	if reversed.Rebalance == nil || *reversed.Rebalance != *newer || reversed.RebalanceReplica != "pod-a" {
		t.Fatalf("reversed: view rebalance = %+v from %q, want the same newer round", reversed.Rebalance, reversed.RebalanceReplica)
	}
	// The view's copy is its own: a later change to the snapshot's facts
	// does not reach a view already built.
	newer.PlannedMoves = 0
	if view.Rebalance.PlannedMoves != 23 {
		t.Fatalf("the view shares the snapshot's facts; want a copy")
	}
	snapshots[0].Rebalance, snapshots[1].Rebalance = nil, nil
	none := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a", "pod-b"}, now, freshness)
	if none.Rebalance != nil || none.RebalanceReplica != "" {
		t.Fatalf("with no round planned: rebalance = %+v from %q, want absent", none.Rebalance, none.RebalanceReplica)
	}
}

// The split is the third standing: a line after the other two and before
// every object check, no objects under it, the pair it is between named on
// its one group, and the leader's round whole on the line for the sentence.
// The judgement is the scheduler's: a round that would move nothing is no
// line, whatever the counts look like.
func TestOwnershipSkewIsAStandingDecidedByTheSchedulersRound(t *testing.T) {
	view := View{
		Rebalance: skewedRound(now), RebalanceReplica: "pod-a",
		Degradations: []Degradation{{Kind: DegradationOpenAlertSetStale, Replica: "pod-b"}},
		Anomalies:    []Anomaly{{QueryGroup: "qg-1", Kind: KindOverdueWake, Finding: Finding{Check: CheckSlotsOverdue, Group: "pod-a"}}},
	}
	reports := ReportChecks([][]Anomaly{view.Anomalies}, nil, &view, now)
	if len(reports) != 3 || reports[0].Code != CheckReplicaDegraded || reports[1].Code != CheckOwnershipSkewed || reports[2].Code != CheckSlotsOverdue {
		t.Fatalf("reports = %+v, want REPLICA_DEGRADED, OWNERSHIP_SKEWED, then the object checks", reports)
	}
	skew := reports[1]
	if skew.Owner != OwnerAlarmd || skew.GroupBy != GroupByReplica || skew.Objects != 0 || skew.Current != 0 ||
		skew.Replica != "pod-a" || skew.Rebalance == nil || *skew.Rebalance != *view.Rebalance {
		t.Fatalf("OWNERSHIP_SKEWED = %+v, want ours, folded on replica, no objects, the leader's round whole", skew)
	}
	if len(skew.Groups) != 1 || skew.Groups[0].Key != "pod-a" || len(skew.Groups[0].Replicas) != 2 ||
		skew.Groups[0].Replicas[0] != "pod-a" || skew.Groups[0].Replicas[1] != "pod-b" {
		t.Fatalf("OWNERSHIP_SKEWED groups = %+v, want one group on the loaded replica naming the pair", skew.Groups)
	}
	if got := skew.LineCount(); got != 2 {
		t.Fatalf("line count = %d, want the two replicas", got)
	}
	if rows := UnderCheck(CheckOwnershipSkewed, "", &view, now); len(rows) != 0 {
		t.Fatalf("OWNERSHIP_SKEWED lists %d objects, want none", len(rows))
	}
	// On the first screen's arithmetic it is a line of this deployment's
	// with nothing to add to the object count.
	todo := SummarizeTodo(reports, [][]Anomaly{view.Anomalies}, &view, now)
	if todo.Checks != 3 || todo.Objects != 1 {
		t.Fatalf("todo = %+v, want three lines of ours over the one object", todo)
	}

	// A round that moves nothing: the counts may still differ, and the line
	// stays down because the scheduler's tolerance says the split is even
	// enough. That tolerance is the only judge here.
	even := skewedRound(now)
	even.MostOwned, even.LeastOwned, even.PlannedMoves, even.MostOwnedBy, even.LeastOwnedBy = 1200, 1170, 0, "", ""
	view.Rebalance = even
	reports = ReportChecks([][]Anomaly{view.Anomalies}, nil, &view, now)
	for _, report := range reports {
		if report.Code == CheckOwnershipSkewed {
			t.Fatalf("a round that moves nothing put the line up: %+v", report)
		}
	}
	view.Rebalance = nil
	reports = ReportChecks([][]Anomaly{view.Anomalies}, nil, &view, now)
	for _, report := range reports {
		if report.Code == CheckOwnershipSkewed {
			t.Fatalf("a deployment with no round put the line up: %+v", report)
		}
	}
}

// The screen and the metric read the same line: a skewed round is a line
// with a count of two on the page and the same two on fleet_checks, and an
// even round is a zero on both.
func TestOwnershipSkewReachesTheScreenAndTheMetricTogether(t *testing.T) {
	view := View{Rebalance: skewedRound(now), RebalanceReplica: "pod-a", Replicas: []string{"pod-a", "pod-b"}}
	screen := Report(&view, now)
	var line *CheckReport
	for index := range screen.Checks {
		if screen.Checks[index].Code == CheckOwnershipSkewed {
			line = &screen.Checks[index]
		}
	}
	if line == nil || line.LineCount() != 2 || screen.Todo.Checks != 1 || screen.Todo.Objects != 0 {
		t.Fatalf("screen = %+v with line %+v, want the split as one line of ours counting two replicas and no objects", screen.Todo, line)
	}
}
