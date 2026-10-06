// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "testing"

// Observe's empty branch, tested directly. The wiring that calls it with the
// window's own numbers is covered by the evaluation test; what that one cannot
// reach is a window with no valid position, because building one needs a
// record whose detection returned UNAVAILABLE for the Level. The branch is
// where "not alive long enough" and "producing nothing usable" part company,
// so it is checked here rather than left uncovered.
func TestHistoryCoverageCountsEmptyWindowsApartFromShortOnes(t *testing.T) {
	var coverage HistoryCoverage
	coverage.Observe(8, 9, false, true)   // short, has points, and a series never seen before
	coverage.Observe(0, 14, false, false) // short, has none, and a series with history
	coverage.Observe(5, 5, true, true)    // complete, reporting a held verdict, series never seen before
	coverage.Observe(3, 0, true, true)    // the window declined to judge: not counted at all

	if coverage.Levels != 3 {
		t.Errorf("levels = %d, want 3: a window that declined to judge is not a window that was judged",
			coverage.Levels)
	}
	if coverage.Short != 2 {
		t.Errorf("short = %d, want 2", coverage.Short)
	}
	if coverage.Empty != 1 {
		t.Errorf("empty = %d, want 1: without this count a dead metric and a churning strategy "+
			"report the same thing", coverage.Empty)
	}
	if coverage.WorstValid != 0 || coverage.WorstRequired != 14 {
		t.Errorf("worst pair = %d/%d, want 0/14", coverage.WorstValid, coverage.WorstRequired)
	}
	// Counted over every window, not only the short ones, and not counted for
	// the window that declined to judge. A guarded window whose live counts are
	// complete is the case this exists for: the reason says the window is not
	// complete and the numbers beside it say it is, and only this tells a reader
	// which of the two is this round's.
	if coverage.Guarded != 1 {
		t.Errorf("guarded = %d, want 1: the complete window was reporting a held verdict, and a "+
			"window that was never judged cannot have been judged by a guard either", coverage.Guarded)
	}

	// Fresh is counted over every window and ShortFresh only over the short
	// ones, and they are the two halves of the question this exists to answer.
	// Folded into one count, an object whose every window is fresh because it
	// has just started would read exactly like one whose series are replaced
	// faster than a window can fill.
	if coverage.Fresh != 2 {
		t.Errorf("fresh = %d, want 2: counted over every window that was judged, short or not, and "+
			"never over the one that declined to judge", coverage.Fresh)
	}
	if coverage.ShortFresh != 1 {
		t.Errorf("short fresh = %d, want 1: only one of the two short windows had no history, and "+
			"reading the other one as fresh sends a reader to edit a strategy whose series are fine",
			coverage.ShortFresh)
	}

	// And the merge carries it, or a Slot's empty windows vanish above the
	// first series that had one.
	var slot HistoryCoverage
	slot.Merge(coverage)
	slot.Merge(HistoryCoverage{Levels: 2, Short: 1, Empty: 1, WorstValid: 0, WorstRequired: 5, Guarded: 2,
		Fresh: 2, ShortFresh: 1})
	if slot.Fresh != 4 || slot.ShortFresh != 2 {
		t.Errorf("merged fresh = %d/%d, want 4 and 2: a Slot's series are folded one at a time, and a "+
			"count that does not accumulate reports the last series instead of the Slot",
			slot.Fresh, slot.ShortFresh)
	}
	if slot.Guarded != 3 {
		t.Errorf("merged guarded = %d, want 3: a Slot's held verdicts have to accumulate like the "+
			"rest, or they vanish above the first series that had none", slot.Guarded)
	}
	if slot.Empty != 2 {
		t.Errorf("merged empty = %d, want 2: the counts have to accumulate across a Slot's series",
			slot.Empty)
	}
	if slot.Levels != 5 || slot.Short != 3 {
		t.Errorf("merged = %+v, want levels 5 short 3", slot)
	}
}
