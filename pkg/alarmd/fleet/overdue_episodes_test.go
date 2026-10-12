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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The overdue objects are the rows under SLOTS_OVERDUE, each object once,
// with its strategies and where its wake stood; rows under any other line
// are not, and the part's summary carries none of it.
func TestTheOverdueObjectsAreTheRowsUnderSlotsOverdue(t *testing.T) {
	at := time.Date(2026, 10, 7, 11, 22, 0, 0, time.UTC)
	wake := &WakeFacts{Known: true, DueAt: at.Add(-3 * time.Minute), IntervalSeconds: 60}
	strategies := []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}
	columns := [][]Anomaly{
		{{QueryGroup: "qg-b", Finding: Finding{Check: CheckSlotsOverdue}, Wake: wake, Strategies: strategies},
			{QueryGroup: "qg-c", Finding: Finding{Check: CheckDependencyDown}}},
		{{QueryGroup: "qg-a", Finding: Finding{Check: CheckSlotsOverdue}}, {QueryGroup: "qg-b", Finding: Finding{Check: CheckSlotsOverdue}}},
	}
	objects := overdueObjectsOf(columns)
	if len(objects) != 2 || objects[0].QueryGroup != "qg-a" || objects[1].QueryGroup != "qg-b" ||
		!objects[1].DueAt.Equal(wake.DueAt) || objects[1].IntervalSeconds != 60 || len(objects[1].Strategies) != 1 {
		t.Fatalf("objects %+v, want qg-a and qg-b once each, qg-b with its wake and strategy", objects)
	}
	encoded, err := json.Marshal(ReplicaPart{Overdue: objects})
	if err != nil || strings.Contains(string(encoded), "qg-a") {
		t.Fatalf("the part's summary carries its overdue objects: %s %v", encoded, err)
	}
}

// A view keeps the counted replicas' latest-ended episodes first, at most
// MaxOverdueEpisodes; a summary's head carries none.
func TestTheViewKeepsTheLatestOverdueEpisodes(t *testing.T) {
	at := time.Date(2026, 10, 7, 21, 23, 0, 0, time.UTC)
	episodes := func(replica string, n int, from time.Time) []OverdueEpisode {
		var out []OverdueEpisode
		for i := 0; i < n; i++ {
			out = append(out, OverdueEpisode{OverdueObject: OverdueObject{QueryGroup: fmt.Sprintf("%s-qg-%d", replica, i)}, Replica: replica,
				Onset: from.Add(time.Duration(i) * time.Minute), Clear: from.Add(time.Duration(i+1) * time.Minute)})
		}
		return out
	}
	a := Snapshot{Replica: "pod-a", TakenAt: at, OverdueEpisodes: episodes("pod-a", 40, at.Add(-2*time.Hour))}
	b := Snapshot{Replica: "pod-b", TakenAt: at, OverdueEpisodes: episodes("pod-b", 40, at.Add(-time.Hour))}
	view := Aggregate(Expectation{}, []Snapshot{a, b}, []string{"pod-a", "pod-b"}, at, time.Minute)
	if len(view.OverdueEpisodes) != MaxOverdueEpisodes || view.OverdueEpisodes[0].QueryGroup != "pod-b-qg-39" ||
		view.OverdueEpisodes[0].Clear.Before(view.OverdueEpisodes[1].Clear) {
		t.Fatalf("view kept %d episodes, first %+v, want the %d latest-ended first", len(view.OverdueEpisodes), view.OverdueEpisodes[0], MaxOverdueEpisodes)
	}
	if head := headOf(a); len(head.OverdueEpisodes) != 0 {
		t.Fatalf("the summary head carries %d episodes", len(head.OverdueEpisodes))
	}
}

// The hold class an episode is counted under.
func TestAnOverdueEpisodesHoldClass(t *testing.T) {
	for _, tc := range []struct {
		episode OverdueEpisode
		class   string
	}{
		{OverdueEpisode{}, "unknown"},
		{OverdueEpisode{ReadHoldKnown: true}, "zero"},
		{OverdueEpisode{ReadHoldKnown: true, ReadHoldMillis: 308000}, "positive"},
		{OverdueEpisode{ReadHoldMillis: 308000}, "unknown"},
	} {
		if got := tc.episode.HoldClass(); got != tc.class {
			t.Fatalf("%+v: class %s, want %s", tc.episode, got, tc.class)
		}
	}
}
