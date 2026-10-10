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
	"context"
	"testing"
	"time"
)

// An object whose late series the supplements could not recover is a row of
// its own kind, named under the strategies this process saw behind it, with
// the latest few windows as evidence; one never seen evaluate has no row.
// The rows land on their own lines: past the round is the strategy's to move
// the read, the tail of a window recovered in part is the data's.
func TestUnrecoveredLateSeriesAreRowsOnTheirOwnLines(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), completion("qg-past", "FULL_COMPLETED", "4101"))
	tracker.Observe(context.Background(), completion("qg-tail", "FULL_COMPLETED", "4102"))
	samples := []LatePastRoundSample{{EvaluationTime: 1}, {EvaluationTime: 2}, {EvaluationTime: 3}, {EvaluationTime: 4}}
	rows := tracker.LateSeries(
		map[string]LatePastRoundFacts{"qg-past": {StepSeconds: 60, CurrentDelaySeconds: 60, SuggestedDelaySeconds: 300,
			Since: now.Add(-time.Hour), Samples: samples}},
		map[string]LateSeriesMissedFacts{"qg-tail": {Windows: 2, CrossedSeries: 3, Since: now.Add(-time.Hour)},
			"qg-never-seen": {Windows: 1, CrossedSeries: 1}})
	if len(rows) != 2 {
		t.Fatalf("rows %+v, want the two objects this process saw", rows)
	}
	byKind := map[string]Anomaly{}
	for _, row := range rows {
		byKind[row.Kind] = row
	}
	past, tail := byKind[KindLatePastRound], byKind[KindLateSeriesMissed]
	if past.QueryGroup != "qg-past" || past.LatePastRound == nil || len(past.LatePastRound.Samples) != MaxLateSeriesSamples ||
		past.LatePastRound.Samples[0].EvaluationTime != 2 || len(past.Strategies) != 1 || past.Strategies[0].StrategyID != "4101" {
		t.Fatalf("past-round row %+v facts %+v, want the latest samples and the strategy", past, past.LatePastRound)
	}
	if tail.QueryGroup != "qg-tail" || tail.LateSeriesMissed == nil || tail.LateSeriesMissed.CrossedSeries != 3 || !tail.Since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("tail row %+v facts %+v", tail, tail.LateSeriesMissed)
	}
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 2, Determined: 2, LateSeries: rows}
	view := decidedView([]Snapshot{snapshot})
	screen := Report(&view, now)
	// Neither object is this deployment's to act on: both count with the
	// other owners' objects on the to-do.
	if screen.Todo.GovernanceObjects != 2 || screen.Todo.Objects != view.Unknown {
		t.Fatalf("to-do %+v, want both objects with the strategy's and the data's", screen.Todo)
	}
	lines := map[Check]CheckReport{}
	for _, report := range screen.Checks {
		lines[report.Code] = report
	}
	if lines[CheckLatePastRound].Objects != 1 || lines[CheckLatePastRound].Owner != OwnerStrategy ||
		lines[CheckLateSeriesMissed].Objects != 1 || lines[CheckLateSeriesMissed].Owner != OwnerData {
		t.Fatalf("lines %+v", lines)
	}
	for _, row := range view.LateSeries {
		want := wordPair{StateResultUntrusted, ActionStrategyEdit}
		if row.Kind == KindLateSeriesMissed {
			want = wordPair{StateResultUntrusted, ActionDataCheck}
		}
		// The words the strategy and object routes put on the row.
		if standing := standingOf(row); standing.State != want.State || standing.Action != want.Action {
			t.Errorf("%s standing %+v, want %v", row.Kind, standing, want)
		}
	}
}
