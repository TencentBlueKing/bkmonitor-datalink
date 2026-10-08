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
	"testing"
	"time"
)

// tallyReplicas are three replicas' snapshots, each holding its own objects:
// strategy 900 has objects on all three and business 2 on two, a strategy
// is shared between the anomaly and pooled columns, the pooled column of
// one replica is cut short, and one replica lost rounds to a skip.
func tallyReplicas() []Snapshot {
	at := now.Add(-10 * time.Second)
	row := func(queryGroup, replica, kind, reason string, strategies ...StrategyRef) Anomaly {
		return Anomaly{QueryGroup: queryGroup, Kind: kind, CauseReason: reason, Since: now.Add(-3 * time.Hour),
			SinceFrom: SinceBusinessState, Replica: replica, Strategies: strategies}
	}
	shared := StrategyRef{StrategyID: "900", BusinessID: "2"}
	snapshots := make([]Snapshot, 0, 3)
	for index, replica := range []string{"pod-a", "pod-b", "pod-c"} {
		own := StrategyRef{StrategyID: fmt.Sprintf("%d", 100+index), BusinessID: fmt.Sprintf("%d", index%2+2)}
		snapshot := Snapshot{Replica: replica, TakenAt: at, StartedAt: now.Add(-2 * time.Hour), Owned: 100, Determined: 100,
			Anomalies: []Anomaly{
				row(replica+"-1", replica, KindDegradedRun, "QUERY_TIMEOUT", shared),
				row(replica+"-2", replica, "SOURCE_BLOCKED", "", own),
				row(replica+"-3", replica, KindDegradedRun, "HISTORY_GAPPED"),
			},
			Demoted: []Anomaly{row(replica+"-4", replica, KindQueryCooldown, "QUERY_UNAVAILABLE", own, shared)},
		}
		snapshot.TotalAnomalies, snapshot.TotalDemoted = len(snapshot.Anomalies), len(snapshot.Demoted)
		if index == 1 {
			snapshot.TotalDemoted = 5
		}
		if index == 2 {
			snapshot.GapSkips = map[string]SkippedSpan{replica + "-5": {FirstSlot: 1, LastSlot: 3, Slots: 3,
				At: now.Add(-time.Minute), Replica: replica, Strategies: []StrategyRef{own}}}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

// Each replica's tally, taken from a view of its own snapshot and merged,
// is the whole view's impact, field for field: object counts add, and a
// strategy on several replicas is counted once.
func TestReplicaImpactTalliesAddUpToTheWholeViews(t *testing.T) {
	snapshots := tallyReplicas()
	expectation := Expectation{QueryGroups: 300, Known: true}
	decided := func(snapshots []Snapshot) View {
		names := make([]string, 0, len(snapshots))
		for _, snapshot := range snapshots {
			names = append(names, snapshot.Replica)
		}
		view := Aggregate(expectation, snapshots, names, now, freshness)
		Decide(&view, now, 10*time.Minute)
		return view
	}
	want := ImpactOf(decided(snapshots), now)
	tallies := make([]ImpactTally, 0, len(snapshots))
	summed := 0
	for _, snapshot := range snapshots {
		one := decided([]Snapshot{snapshot})
		tallies = append(tallies, ImpactTallyOf(one, now))
		summed += ImpactOf(one, now).Blind.Strategies
	}
	got := MergeImpactTallies(tallies...).Impact()
	if got != want {
		t.Fatalf("merged replica tallies = %+v\nwhole view = %+v", got, want)
	}
	// The fixture has to exercise what the merge is for, or the equality
	// above says nothing: a union smaller than the sum, a cut column, rows
	// with no strategy, and objects losing rounds on the record lines.
	if want.Blind.Strategies >= summed || !want.Demoted.Partial || want.Anomalies.Partial || want.NoStrategies != 3 ||
		want.Alarmd.Objects == 0 {
		t.Fatalf("the fixture does not exercise the merge: %+v (per-replica blind strategies summed %d)", want, summed)
	}
}
