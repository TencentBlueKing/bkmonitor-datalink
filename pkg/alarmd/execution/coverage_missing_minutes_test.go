// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"reflect"
	"testing"
)

// Windows names at most MaxCoverageWindows short windows; the missing-minute
// union and its two companions are over every short window, named or not, so
// a reader can still place the holes of the ones the list left off.
func TestTheMissingMinutesCoverEveryShortWindowNamedOrNot(t *testing.T) {
	var coverage HistoryCoverage
	// Twelve short windows, each missing its own minute and one shared one:
	// four of them are pushed off the list, and their minutes must stay.
	for i := int64(0); i < 12; i++ {
		coverage.ObserveWindow(WindowCoverage{Series: SeriesIdentityDigest("s"), LevelID: uint32(i + 1), Valid: 7, Required: 9,
			Missing: []int64{600, 60 * (20 + i)}, MissingTotal: 2})
	}
	if len(coverage.Windows) != MaxCoverageWindows {
		t.Fatalf("named %d windows, want the bound of %d", len(coverage.Windows), MaxCoverageWindows)
	}
	want := []int64{600}
	for i := int64(0); i < 12; i++ {
		want = append(want, 60*(20+i))
	}
	if !reflect.DeepEqual(coverage.MissingMinutes, want) {
		t.Fatalf("missing minutes = %v, want the sorted union over all twelve windows %v", coverage.MissingMinutes, want)
	}
	if coverage.MissingMinutesTruncated || coverage.ShortUnusable != 0 {
		t.Fatalf("truncated = %v, short unusable = %d, want a whole union and no unusable point", coverage.MissingMinutesTruncated, coverage.ShortUnusable)
	}
	// A window that lists fewer missing positions than it has leaves the union
	// not whole; one holding an unusable point is counted; a full window adds
	// nothing.
	coverage.ObserveWindow(WindowCoverage{Series: "more", LevelID: 1, Valid: 1, Required: 20, Missing: []int64{60}, MissingTotal: 19})
	coverage.ObserveWindow(WindowCoverage{Series: "bad", LevelID: 1, Valid: 8, Required: 9, Unusable: []int64{660}, UnusableTotal: 1})
	coverage.ObserveWindow(WindowCoverage{Series: "full", LevelID: 1, Valid: 9, Required: 9, Missing: []int64{9999}, MissingTotal: 1})
	if !coverage.MissingMinutesTruncated || coverage.ShortUnusable != 1 {
		t.Fatalf("truncated = %v, short unusable = %d, want truncated by the under-listed window and one unusable window",
			coverage.MissingMinutesTruncated, coverage.ShortUnusable)
	}
	for _, minute := range coverage.MissingMinutes {
		if minute == 9999 {
			t.Fatal("a full window's minute entered the union")
		}
	}
}

// The union stops at MaxCoverageMissingMinutes and says so, rather than
// growing with the longest window in the round.
func TestTheMissingMinutesStopAtTheirBound(t *testing.T) {
	var coverage HistoryCoverage
	missing := make([]int64, 0, MaxCoverageMissingMinutes+1)
	for i := 0; i <= MaxCoverageMissingMinutes; i++ {
		missing = append(missing, int64(60*(i+1)))
	}
	for start := 0; start < len(missing); start += MaxWindowHolesListed {
		end := min(start+MaxWindowHolesListed, len(missing))
		coverage.ObserveWindow(WindowCoverage{Series: SeriesIdentityDigest("s"), LevelID: uint32(start + 1),
			Valid: 100, Required: 100 + uint32(end-start), Missing: missing[start:end], MissingTotal: uint32(end - start)})
	}
	if len(coverage.MissingMinutes) != MaxCoverageMissingMinutes || !coverage.MissingMinutesTruncated {
		t.Fatalf("union of %d, truncated %v; want %d and truncated", len(coverage.MissingMinutes), coverage.MissingMinutesTruncated, MaxCoverageMissingMinutes)
	}
}

// A merge folds the other side's counts once: its named windows are already
// in them, so placing the names must not count their minutes or unusable
// points a second time, and the result is the one a single run over every
// record would give.
func TestAMergeFoldsTheMissingMinutesOnce(t *testing.T) {
	windows := []WindowCoverage{
		{Series: "a", LevelID: 1, Valid: 7, Required: 9, Missing: []int64{120, 180}, MissingTotal: 2},
		{Series: "b", LevelID: 1, Valid: 8, Required: 9, Unusable: []int64{240}, UnusableTotal: 1},
		{Series: "c", LevelID: 1, Valid: 8, Required: 9, Missing: []int64{300}, MissingTotal: 1},
	}
	var single, left, right HistoryCoverage
	for i, window := range windows {
		single.Levels++
		single.ObserveWindow(window)
		target := &left
		if i > 0 {
			target = &right
		}
		target.Levels++
		target.ObserveWindow(window)
	}
	left.Merge(right)
	if !reflect.DeepEqual(left.MissingMinutes, single.MissingMinutes) || left.ShortUnusable != single.ShortUnusable ||
		left.MissingMinutesTruncated != single.MissingMinutesTruncated {
		t.Fatalf("merged minutes %v, unusable %d, truncated %v; a single run gives %v, %d, %v",
			left.MissingMinutes, left.ShortUnusable, left.MissingMinutesTruncated,
			single.MissingMinutes, single.ShortUnusable, single.MissingMinutesTruncated)
	}
}
