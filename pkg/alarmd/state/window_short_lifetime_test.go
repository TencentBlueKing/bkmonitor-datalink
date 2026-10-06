// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import "testing"

// These two anchor a property that is true today and that a future change could
// remove without any symptom.
//
// FULL is decided by walking the grid the requirement defines and asking the
// stored window whether each position is there and valid. The two sides are
// independent: one comes from the compiled Plan, the other from what was
// actually accumulated. Any change that starts filling the window from the same
// grid it is later checked against -- rebuilding the retention window from a
// query, for instance, rather than accumulating it round by round -- makes
// ValidPositions equal requiredPositions by construction. WARMING becomes
// unreachable, and the check that was supposed to catch a short window passes
// by always saying FULL.
//
// A test written after such a change would be written against the new
// behaviour and would agree with it. These are written now, while the answer is
// still produced by two independent facts, and they hold the answer in place.
//
// They deliberately use a series built here rather than a population identified
// by another instrument: a sample taken from someone else's classifier makes
// this assertion depend on that classifier's boundaries, and if those move the
// sample changes underneath without anything failing.

// A series whose lifetime is shorter than the window it is measured over is
// short on every round, by design, for as long as it exists. It must read
// WARMING: the positions before it was born are missing, and nothing that
// arrives later can fill them.
func TestShortLivedSeriesStaysWarmingAcrossTheWholeWindow(t *testing.T) {
	level := requirement(1, "1", 5, 8)
	window, err := NewWindow([]LevelRequirement{level})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	// The grid for endTime 600 with five required points at a minute apart is
	// 360, 420, 480, 540, 600. This series first reported at 480.
	results := mustApply(t, window, []StatePoint{
		point(480, "a", fact(level, LevelFactNormal)),
		point(540, "b", fact(level, LevelFactNormal)),
		point(600, "c", fact(level, LevelFactNormal)),
	})
	assertPointStatuses(t, results, PointApplied, PointApplied, PointApplied)

	history, ok := window.History(1)
	if !ok {
		t.Fatal("History(level 1) missing")
	}
	summary := history.Summarize(600, 5)
	if summary.Completeness != HistoryWarming {
		t.Fatalf("summary = %+v, want WARMING: a series younger than the window is short on every round", summary)
	}
	if summary.ValidPositions != 3 {
		t.Fatalf("ValidPositions = %d, want 3; the two positions before the series existed must stay missing",
			summary.ValidPositions)
	}
	if summary.ValidPositions == summary.RequiredPositions {
		t.Fatal("the window reports itself complete; whatever fills it is being read as evidence that it is full")
	}
}

// Grid positions are matched on an exact source time. An off-grid point does
// not fill the position next to it, so data that lands between grid positions
// leaves the window incomplete rather than completing it. The failure direction
// is the safe one, and a rebuild must not "fix" it by snapping timestamps onto
// the grid: that would turn arrival at any time into a full window.
func TestOffGridPointsDoNotFillTheGridPositionTheyLandNear(t *testing.T) {
	level := requirement(1, "1", 5, 8)
	window, err := NewWindow([]LevelRequirement{level})
	if err != nil {
		t.Fatalf("NewWindow() error = %v", err)
	}
	// Every grid position is covered except 420, which is offered ten seconds
	// late instead.
	results := mustApply(t, window, []StatePoint{
		point(360, "a", fact(level, LevelFactNormal)),
		point(430, "b", fact(level, LevelFactNormal)),
		point(480, "c", fact(level, LevelFactNormal)),
		point(540, "d", fact(level, LevelFactNormal)),
		point(600, "e", fact(level, LevelFactNormal)),
	})
	assertPointStatuses(t, results, PointApplied, PointApplied, PointApplied, PointApplied, PointApplied)

	history, ok := window.History(1)
	if !ok {
		t.Fatal("History(level 1) missing")
	}
	summary := history.Summarize(600, 5)
	if summary.Completeness == HistoryFull {
		t.Fatalf("summary = %+v, want an incomplete window: a point at 430 must not stand in for 420", summary)
	}
	if summary.ValidPositions >= summary.RequiredPositions {
		t.Fatalf("ValidPositions = %d of %d; the off-grid point was counted into a position it does not occupy",
			summary.ValidPositions, summary.RequiredPositions)
	}
}
