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
	"strings"
	"testing"
	"time"
)

// A replica's running strategies: a strategy on one of its rows is in the
// state its line folds to, one evaluating on its objects with no row is
// DETECTING, each once; a snapshot that does not say which strategies
// evaluate gives none, which is not none running.
func TestAReplicaCountsItsRunningStrategiesByState(t *testing.T) {
	snapshot := Snapshot{Replica: "pod-b", TakenAt: now.Add(-10 * time.Second), Owned: 2, Determined: 2,
		Anomalies: []Anomaly{anomaly("qg-854")}, TotalAnomalies: 1}
	if summary := SummaryOf(snapshot, []string{"qg-854", "qg-900"}, 0); summary.Part.RunningStrategies != nil {
		t.Fatalf("a snapshot saying nothing of its strategies counted %v", summary.Part.RunningStrategies)
	}
	snapshot.EvaluatingStrategies = []StrategyRef{{StrategyID: "854", BusinessID: "2"}, {StrategyID: "900", BusinessID: "2"}}
	snapshot.EvaluatingStrategiesKnown = true
	summary := SummaryOf(snapshot, []string{"qg-854", "qg-900"}, 0)
	running := summary.Part.RunningStrategies
	view := publishedView(snapshot, 0)
	var lineState StateWord
	for _, line := range StrategyLines(&view, snapshot.TakenAt) {
		if line.StrategyID == "854" {
			lineState = line.Standing.State
		}
	}
	if lineState == "" || lineState == StateDetecting {
		t.Fatalf("the fixture's row folds to %q; it needs a state other than DETECTING to test this", lineState)
	}
	total := 0
	for _, count := range running {
		total += count
	}
	if len(running) != len(StateWords) || running[lineState] != 1 || running[StateDetecting] != 1 || total != 2 {
		t.Fatalf("running %v, want 854 under %s and 900 DETECTING, every state present", running, lineState)
	}
	encoded, _ := json.Marshal(summary)
	if !strings.Contains(string(encoded), `"running_strategies"`) || strings.Contains(string(encoded), `"evaluating_strategies":[`) {
		t.Fatalf("summary %s: want the counts and not the list", encoded)
	}
}

// Merged, the counts add; one part without them leaves them unknown, never
// short by that replica's strategies.
func TestRunningStrategiesAddAndOnePartWithoutThemLeavesThemUnknown(t *testing.T) {
	a := ReplicaPart{RunningStrategies: map[StateWord]int{StateDetecting: 3, StateDataAbsent: 1}}
	b := ReplicaPart{RunningStrategies: map[StateWord]int{StateDetecting: 2}}
	if merged := MergeReplicaParts(a, b); merged.RunningStrategies[StateDetecting] != 5 || merged.RunningStrategies[StateDataAbsent] != 1 {
		t.Fatalf("merged %v, want the counts added", merged.RunningStrategies)
	}
	if merged := MergeReplicaParts(a, ReplicaPart{}, b); merged.RunningStrategies != nil {
		t.Fatalf("a part without the counts merged into %v", merged.RunningStrategies)
	}
	if merged := MergeReplicaParts(); merged.RunningStrategies != nil {
		t.Fatalf("no part merged into %v", merged.RunningStrategies)
	}
}

// The deployment's counts are known only from a view read whole: any gap
// that leaves some replica's rows unread -- its summary missing, stale,
// unreadable, deferred or cut, no registry, no replica -- leaves them
// unknown. A gap in what the objects cover is not one: unowned objects are
// what a falling DETECTING is to show.
func TestRunningStrategiesAreKnownOnlyFromAViewReadWhole(t *testing.T) {
	part := ReplicaPart{RunningStrategies: map[StateWord]int{StateDetecting: 5}}
	whole := View{Replicas: []string{"pod-a", "pod-b"}}
	if running, known := RunningStrategiesOf(&whole, part); !known || running[StateDetecting] != 5 {
		t.Fatalf("a view read whole: %v %t", running, known)
	}
	for _, kind := range []GapKind{GapSnapshotsDeferred, GapSnapshotsUnreadable, GapReplicaMissing, GapSnapshotStale, GapListTruncated,
		GapRegistryUnavailable, GapNoReplicas} {
		view := whole
		view.Gaps = []Gap{{Kind: kind}}
		if running, known := RunningStrategiesOf(&view, part); known || running != nil {
			t.Fatalf("%s: counts %v known %t, want them unknown", kind, running, known)
		}
	}
	shortfall := whole
	shortfall.Gaps = []Gap{{Kind: GapOwnershipShortfall}}
	if _, known := RunningStrategiesOf(&shortfall, part); !known {
		t.Fatal("an ownership shortfall hid the counts that show it")
	}
	if _, known := RunningStrategiesOf(&whole, ReplicaPart{}); known {
		t.Fatal("a part from a build without the counts read as known")
	}
	if _, known := RunningStrategiesOf(&View{}, part); known {
		t.Fatal("a view of no replica read as known")
	}
}
