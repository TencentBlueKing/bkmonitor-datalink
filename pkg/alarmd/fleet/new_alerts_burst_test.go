// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/newopens"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func newOpensAt(queryGroup, strategy string, hours ...observability.NewOpensHourFact) observability.Observation {
	return observability.Observation{Component: observability.ComponentOutput, Stage: observability.StageNewOpens,
		Result: observability.ResultSuccess, NewOpens: &observability.NewOpensFact{Hours: hours},
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2"}}
}

// burstHour is the clock hour the counts are of.
var burstHour = now.Truncate(time.Hour)

// replicaShare is one replica's tracker after the strategy's rounds wrote
// its share of the hour, as its snapshot carries it.
func replicaShare(t *testing.T, replica string, shares map[string]observability.NewOpensHourFact) Snapshot {
	t.Helper()
	tracker := NewTracker(nil, replica, func() time.Time { return now })
	for queryGroup, hour := range shares {
		tracker.Observe(context.Background(), newOpensAt(queryGroup, "801", hour))
	}
	return Snapshot{Replica: replica, TakenAt: now, NoData: tracker.NoData()}
}

func burstsOf(view View) []Anomaly {
	var rows []Anomaly
	for _, row := range view.NoData {
		if row.Kind == KindNewAlertsBurst {
			rows = append(rows, row)
		}
	}
	return rows
}

// The threshold is the strategy's, so the fleet adds the replicas' shares
// of one hour up before comparing: two shares of 3,000 make a line naming
// both and the widest dimension as at least what one replica saw; one
// share past the threshold makes a line by itself; two of 2,000 make none.
func TestTheReplicasSharesOfAStrategysHourAreAddedUpBeforeTheThreshold(t *testing.T) {
	start := burstHour.Unix()
	for _, test := range []struct {
		name      string
		snapshots []Snapshot
		want      *NewAlertsBurst
		clause    string
	}{
		{name: "two shares past it together", snapshots: []Snapshot{
			replicaShare(t, "pod-a", map[string]observability.NewOpensHourFact{"qg-a": {StartUnix: start, Opened: 3000, Dimension: "span_name", Distinct: 2900}}),
			replicaShare(t, "pod-b", map[string]observability.NewOpensHourFact{"qg-b": {StartUnix: start, Opened: 3200, Dimension: "span_name", Distinct: 3100}}),
		}, want: &NewAlertsBurst{Opened: 6200, Dimension: "span_name", Distinct: 3100, DistinctCapped: true, Replicas: 2},
			clause: "一小时内新开告警 6200 条（2 个副本合计，发现线 5000 条），取值最多的维度 span_name 有至少 3100 个不同取值"},
		{name: "one share past it", snapshots: []Snapshot{
			replicaShare(t, "pod-a", map[string]observability.NewOpensHourFact{"qg-a": {StartUnix: start, Opened: 14249, Dimension: "span_name", Distinct: 14100}}),
		}, want: &NewAlertsBurst{Opened: 14249, Dimension: "span_name", Distinct: 14100, Replicas: 1},
			clause: "一小时内新开告警 14249 条（发现线 5000 条），取值最多的维度 span_name 有 14100 个不同取值"},
		{name: "two shares under it together", snapshots: []Snapshot{
			replicaShare(t, "pod-a", map[string]observability.NewOpensHourFact{"qg-a": {StartUnix: start, Opened: 2000, Dimension: "span_name", Distinct: 1900}}),
			replicaShare(t, "pod-b", map[string]observability.NewOpensHourFact{"qg-b": {StartUnix: start, Opened: 2000, Dimension: "span_name", Distinct: 1900}}),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var replicas []string
			for _, snapshot := range test.snapshots {
				replicas = append(replicas, snapshot.Replica)
			}
			rows := burstsOf(Aggregate(Expectation{QueryGroups: 2, Known: true}, test.snapshots, replicas, now, time.Minute))
			if test.want == nil {
				if len(rows) != 0 {
					t.Fatalf("rows %+v, want none under the threshold", rows)
				}
				return
			}
			if len(rows) != 1 || rows[0].NewAlertsBurst == nil {
				t.Fatalf("rows %+v, want one for the strategy", rows)
			}
			got := rows[0]
			burst := *got.NewAlertsBurst
			if burst.Opened != test.want.Opened || burst.Dimension != test.want.Dimension || burst.Distinct != test.want.Distinct ||
				burst.DistinctCapped != test.want.DistinctCapped || burst.Replicas != test.want.Replicas || !burst.WindowStart.Equal(burstHour) ||
				got.Strategies[0].StrategyID != "801" || got.Finding.Check != CheckNewAlertsBurst || got.Finding.Owner != OwnerStrategy {
				t.Fatalf("row %+v burst %+v, want %+v", got, burst, *test.want)
			}
			if clause := evidenceClause(got); clause != test.clause {
				t.Fatalf("clause %q, want %q", clause, test.clause)
			}
		})
	}
}

// The count is per strategy on a replica, not per object: a strategy whose
// Plans sit in two objects is one row, and a round of either object that
// says no hour is past the floor any more clears it; so do two hours
// without a word.
func TestAStrategyInTwoObjectsIsOneRowClearedAsOne(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	hour := observability.NewOpensHourFact{StartUnix: burstHour.Unix(), Opened: 5200, Dimension: "span_name", Distinct: 5100}
	tracker.Observe(context.Background(), newOpensAt("qg-a", "801", hour))
	tracker.Observe(context.Background(), newOpensAt("qg-b", "801", hour))
	if rows := burstsOf(View{NoData: tracker.NoData()}); len(rows) != 1 || rows[0].QueryGroup != "qg-b" {
		t.Fatalf("rows %+v, want one for the strategy, under the object that wrote last", rows)
	}
	tracker.Observe(context.Background(), newOpensAt("qg-a", "801"))
	if rows := burstsOf(View{NoData: tracker.NoData()}); len(rows) != 0 {
		t.Fatalf("after qg-a said none: %+v, want the strategy cleared", rows)
	}
	tracker.Observe(context.Background(), newOpensAt("qg-b", "801", hour))
	at.at = at.at.Add(newAlertsBurstStale + time.Minute)
	if rows := burstsOf(View{NoData: tracker.NoData()}); len(rows) != 0 {
		t.Fatalf("two hours without a word: %+v, want the row gone", rows)
	}
}

// Of a strategy's hours past the threshold the line names the latest.
func TestTheLineNamesTheLatestHourPastTheThreshold(t *testing.T) {
	earlier := observability.NewOpensHourFact{StartUnix: burstHour.Add(-time.Hour).Unix(), Opened: 9000, Dimension: "span_name", Distinct: 8000}
	later := observability.NewOpensHourFact{StartUnix: burstHour.Unix(), Opened: 5100, Dimension: "trace_id", Distinct: 5000}
	tracker := NewTracker(nil, "pod-a", func() time.Time { return now })
	tracker.Observe(context.Background(), newOpensAt("qg-a", "801", earlier, later))
	rows := burstsOf(View{NoData: FoldNewAlertsBursts(tracker.NoData())})
	if len(rows) != 1 || !rows[0].NewAlertsBurst.WindowStart.Equal(burstHour) || rows[0].NewAlertsBurst.Dimension != "trace_id" {
		t.Fatalf("rows %+v, want the latest hour", rows)
	}
	if rows[0].NewAlertsBurst.Threshold != newopens.Threshold {
		t.Fatalf("threshold %d", rows[0].NewAlertsBurst.Threshold)
	}
}

// A dimension counted past its cap reads "at least".
func TestACappedDimensionReadsAtLeast(t *testing.T) {
	clause := newAlertsBurstClause(&NewAlertsBurst{Opened: 30000, Threshold: 5000, Dimension: "trace_id", Distinct: 20000, DistinctCapped: true, Replicas: 1})
	if want := "一小时内新开告警 30000 条（发现线 5000 条），取值最多的维度 trace_id 有至少 20000 个不同取值"; clause != want {
		t.Fatalf("clause %q, want %q", clause, want)
	}
}
