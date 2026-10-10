// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"reflect"
	"testing"
)

// The holes are the positions the summary did not count, by name and by kind:
// a minute with no point at all is a record that never arrived for this
// series, a minute with a point the Level cannot use is a record that did.
// The two lists and the summary describe one window, so listed plus unlisted
// holes is exactly what the summary found short.
func TestTheHolesOfAWindowAreThePositionsTheSummaryDidNotCount(t *testing.T) {
	one := requirement(1, "1", 6, 8)
	five := requirement(5, "5", 6, 8)
	window, err := NewWindow([]LevelRequirement{one, five})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	// Six positions ending at 400: 100, 160, 220, 280, 340, 400. Level 1 has
	// points at 100, 220, 400 with 220 unusable; 160, 280 and 340 never came.
	mustApply(t, window, []StatePoint{
		point(100, "a", fact(one, LevelFactNormal), fact(five, LevelFactNormal)),
		point(220, "b", fact(one, LevelFactUnavailable), fact(five, LevelFactNormal)),
		point(400, "c", fact(one, LevelFactAnomalous), fact(five, LevelFactNormal)),
	})
	history, _ := window.History(1)
	summary, holes := history.SummarizeHoles(400, 6, 16)
	if only := history.Summarize(400, 6); only != summary {
		t.Fatalf("the walk with holes summarised %+v, the plain one %+v: one walk, two answers", summary, only)
	}
	if summary.ValidPositions != 2 {
		t.Fatalf("summary = %+v, want 2 valid positions", summary)
	}
	if !reflect.DeepEqual(holes.Missing, []int64{160, 280, 340}) || holes.MissingTotal != 3 {
		t.Fatalf("missing = %v (%d), want the three minutes no record arrived at", holes.Missing, holes.MissingTotal)
	}
	if !reflect.DeepEqual(holes.Unusable, []int64{220}) || holes.UnusableTotal != 1 {
		t.Fatalf("unusable = %v (%d), want the one minute whose record the Level could not use", holes.Unusable, holes.UnusableTotal)
	}
	if holes.MissingTotal+holes.UnusableTotal != summary.RequiredPositions-summary.ValidPositions {
		t.Fatalf("holes %d+%d and shortfall %d describe two different windows",
			holes.MissingTotal, holes.UnusableTotal, summary.RequiredPositions-summary.ValidPositions)
	}
	// A hole at the window's first position is a hole like any other: seven
	// positions ending at 400 start at 40, where nothing was ever applied.
	if _, leading := history.SummarizeHoles(400, 7, 16); !reflect.DeepEqual(leading.Missing, []int64{40, 160, 280, 340}) || leading.MissingTotal != 4 {
		t.Fatalf("with a hole at the first position: %+v, want 40 listed first", leading)
	}
	// The bound cuts the list, never the total: a window short by more than
	// the bound still says how short.
	_, bounded := history.SummarizeHoles(400, 6, 2)
	if !reflect.DeepEqual(bounded.Missing, []int64{160, 280}) || bounded.MissingTotal != 3 {
		t.Fatalf("bounded = %+v, want the two oldest listed and all three counted", bounded)
	}
	// A request the summary would refuse to walk names nothing: more
	// positions than the Level retains, or a window reaching before the epoch.
	refused := func(end int64, required uint32) WindowHoles {
		_, holes := history.SummarizeHoles(end, required, 16)
		return holes
	}
	for name, holes := range map[string]WindowHoles{
		"beyond retention": refused(400, 9),
		"before the epoch": refused(100, 6),
	} {
		if holes.MissingTotal != 0 || holes.UnusableTotal != 0 || holes.Missing != nil || holes.Unusable != nil {
			t.Fatalf("%s: holes = %+v, want none named -- the summary would not have walked it", name, holes)
		}
	}
	// The plain summary lists nothing; the counts it would have made are the
	// walk's and cost nothing to keep, but nothing reads them from it.
	// A full window has no holes.
	fiveHistory, _ := window.History(5)
	if _, full := fiveHistory.SummarizeHoles(400, 1, 16); full.MissingTotal != 0 || full.UnusableTotal != 0 {
		t.Fatalf("a full window reported holes: %+v", full)
	}
}
