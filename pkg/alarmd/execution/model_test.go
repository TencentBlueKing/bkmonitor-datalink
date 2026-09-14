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
	coverage.Observe(8, 9)  // short, has points
	coverage.Observe(0, 14) // short, has none
	coverage.Observe(5, 5)  // complete
	coverage.Observe(3, 0)  // the window declined to judge: not counted at all

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

	// And the merge carries it, or a Slot's empty windows vanish above the
	// first series that had one.
	var slot HistoryCoverage
	slot.Merge(coverage)
	slot.Merge(HistoryCoverage{Levels: 2, Short: 1, Empty: 1, WorstValid: 0, WorstRequired: 5})
	if slot.Empty != 2 {
		t.Errorf("merged empty = %d, want 2: the counts have to accumulate across a Slot's series",
			slot.Empty)
	}
	if slot.Levels != 5 || slot.Short != 3 {
		t.Errorf("merged = %+v, want levels 5 short 3", slot)
	}
}
