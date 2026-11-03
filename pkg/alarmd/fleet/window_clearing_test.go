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
	"strings"
	"testing"
	"time"
)

var clearingEnd = time.Date(2026, 9, 23, 11, 0, 0, 0, time.UTC)

// isFull says whether the window ending at t holds none of the holes, and
// firstFull walks forward one interval at a time from End, as later rounds
// would, to the first such window -- the answer by simulation, independent
// of the formula under test.
func isFull(holes []time.Time, t time.Time, required uint32, interval time.Duration) bool {
	start := t.Add(-time.Duration(required-1) * interval)
	for _, hole := range holes {
		if !hole.Before(start) && !hole.After(t) {
			return false
		}
	}
	return true
}

func firstFull(holes []time.Time, end time.Time, required uint32, interval time.Duration) time.Time {
	t := end
	for !isFull(holes, t, required, interval) {
		t = t.Add(interval)
	}
	return t
}

func windowWith(required uint32, holes []time.Time, listed int) WindowRow {
	row := WindowRow{Key: "s/series/1", Required: required, Valid: required - uint32(len(holes)), End: clearingEnd,
		MissingTotal: uint32(len(holes))}
	for _, at := range holes[:listed] {
		row.Holes = append(row.Holes, WindowHole{At: at, Cause: HoleNotInMemory})
	}
	return row
}

func minutesBefore(end time.Time, minutes ...int) []time.Time {
	out := make([]time.Time, 0, len(minutes))
	for _, m := range minutes {
		out = append(out, end.Add(-time.Duration(m)*time.Minute))
	}
	return out
}

// Every hole listed: the time is exact and is the first round the
// simulation finds full, and the round before it is not.
func TestClearingIsTheFirstFullRoundWhenEveryHoleIsListed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		required uint32
		holes    []time.Time
	}{
		{"two old holes", 5, minutesBefore(clearingEnd, 4, 3)},
		{"hole at the newest position", 5, minutesBefore(clearingEnd, 0)},
		{"scattered", 30, minutesBefore(clearingEnd, 29, 17, 16, 2)},
		{"one position window", 1, minutesBefore(clearingEnd, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearing, _, ok := ClearingOf(windowWith(tc.required, tc.holes, len(tc.holes)), time.Minute)
			want := firstFull(tc.holes, clearingEnd, tc.required, time.Minute)
			if !ok || !clearing.Exact || !clearing.At.Equal(want) {
				t.Fatalf("clearing %+v ok %v, simulation says %s", clearing, ok, want)
			}
			if earlier := clearing.At.Add(-time.Minute); isFull(tc.holes, earlier, tc.required, time.Minute) {
				t.Fatalf("the round before %s, at %s, is already full", clearing.At, earlier)
			}
		})
	}
}

// Holes past the listed bound: the answer is a range, and every placement
// of the unlisted holes the listing allows clears inside it -- packed at
// the start, right after the listed ones, spread, or at End.
func TestClearingRangeHoldsEveryPlacementOfTheUnlistedHoles(t *testing.T) {
	const required = 40
	listed := minutesBefore(clearingEnd, 39, 38, 37)
	placements := map[string][]time.Time{
		"packed after the listed": minutesBefore(clearingEnd, 36, 35),
		"spread":                  minutesBefore(clearingEnd, 20, 9),
		"at End":                  minutesBefore(clearingEnd, 5, 0),
	}
	for name, unlisted := range placements {
		t.Run(name, func(t *testing.T) {
			holes := append(append([]time.Time{}, listed...), unlisted...)
			clearing, _, ok := ClearingOf(windowWith(required, holes, len(listed)), time.Minute)
			truth := firstFull(holes, clearingEnd, required, time.Minute)
			if !ok || clearing.Exact || truth.Before(clearing.At) || truth.After(clearing.Latest) {
				t.Fatalf("clearing %+v ok %v, simulation says %s", clearing, ok, truth)
			}
		})
	}
	// None listed, all packed at the start: the earliest bound is reached.
	holes := minutesBefore(clearingEnd, 39, 38, 37)
	clearing, _, _ := ClearingOf(windowWith(required, holes, 0), time.Minute)
	if truth := firstFull(holes, clearingEnd, required, time.Minute); !clearing.At.Equal(truth) {
		t.Fatalf("earliest %s, packed-at-start truth %s", clearing.At, truth)
	}
}

// What cannot be computed is not guessed, and says why: no interval, a
// hole off the interval's grid (the window's step is not the interval),
// counts that do not add up. A full window has nothing to clear and no
// reason either.
func TestClearingRefusesWhatItCannotComputeAndSaysWhy(t *testing.T) {
	good := windowWith(5, minutesBefore(clearingEnd, 4), 1)
	if _, refused, ok := ClearingOf(good, time.Minute); !ok || refused != "" {
		t.Fatal("the control case must compute")
	}
	full := good
	full.Valid, full.MissingTotal, full.Holes = 5, 0, nil
	offGrid := windowWith(5, []time.Time{clearingEnd.Add(-150 * time.Second)}, 1)
	disagree := good
	disagree.MissingTotal = 3
	for name, tc := range map[string]struct {
		row      WindowRow
		interval time.Duration
		reason   string
	}{
		"full": {full, time.Minute, ""}, "no interval": {good, 0, ClearingIntervalUnknown},
		"off the grid": {offGrid, time.Minute, ClearingStepMismatch}, "step is not the interval": {good, 7 * time.Minute, ClearingStepMismatch},
		"counts disagree": {disagree, time.Minute, ClearingCountsDisagree},
	} {
		if clearing, refused, ok := ClearingOf(tc.row, tc.interval); ok || refused != tc.reason {
			t.Errorf("%s: computed %v reason %q, want %q (%+v)", name, ok, refused, tc.reason, clearing)
		}
	}
}

// The object's answer is the window that clears last, with the windows it
// counted and did not list named beside it.
func TestLastClearingIsTheLatestWindowAndNamesTheUnlisted(t *testing.T) {
	early := windowWith(5, minutesBefore(clearingEnd, 4), 1)
	late := windowWith(5, minutesBefore(clearingEnd, 1), 1)
	late.Key = "s/other/1"
	coverage := &HistoryCoverage{Short: 12, Windows: []WindowRow{early, late}}
	clearing, ok := LastClearing(coverage, time.Minute)
	if !ok || clearing.Key != "s/other/1" || !clearing.At.Equal(clearingEnd.Add(4*time.Minute)) || clearing.Unnamed != 10 {
		t.Fatalf("clearing %+v ok %v", clearing, ok)
	}
	if _, ok := LastClearing(&HistoryCoverage{Short: 0}, time.Minute); ok {
		t.Error("no windows computed a clearing")
	}
	// None computable: the refusal is the answer, not silence.
	offGrid := windowWith(5, []time.Time{clearingEnd.Add(-150 * time.Second)}, 1)
	if refusal, ok := LastClearing(&HistoryCoverage{Short: 1, Windows: []WindowRow{offGrid}}, time.Minute); !ok || refusal.Refused != ClearingStepMismatch || !refusal.At.IsZero() {
		t.Errorf("refusal %+v ok %v", refusal, ok)
	}
}

// The line says only what the computation proves: the window is full then,
// the per-series guard converges the round after. It never says the result
// is trusted -- a full window is not that -- and names what it leaves out:
// unlisted windows, Plan-level guards, a step it could not use.
func TestTheClearingLineSaysOnlyWhatItProves(t *testing.T) {
	coverage := &HistoryCoverage{Short: 3, Windows: []WindowRow{windowWith(5, minutesBefore(clearingEnd, 4, 3), 2)}}
	filling := Standing{State: StateResultUntrusted, Action: ActionWatch, Watch: WatchWindowFilling}
	anomaly := Anomaly{Coverage: coverage, Wake: &WakeFacts{Known: true, IntervalSeconds: 60}, Guards: []GapGuard{{Status: "GAPPED"}}}
	offGrid := anomaly
	offGrid.Coverage = &HistoryCoverage{Short: 1, Windows: []WindowRow{windowWith(5, []time.Time{clearingEnd.Add(-150 * time.Second)}, 1)}}
	ranged := anomaly
	ranged.Guards = nil
	ranged.Coverage = &HistoryCoverage{Short: 1, Windows: []WindowRow{windowWith(40, minutesBefore(clearingEnd, 39, 38, 20), 2)}}
	for name, tc := range map[string]struct {
		anomaly Anomaly
		want    []string
	}{
		"exact": {anomaly, []string{"s/series/1 的 2 个洞在 2026-09-23 11:02Z 全部滑出，这个窗口届时满", "逐序列的守卫在其后一轮（2026-09-23 11:03Z）收敛",
			"另有 2 个未满窗口没有列出", "另有 1 个 Plan 级守卫，另行计数"}},
		"range":    {ranged, []string{"最早 ", "最迟 ", "只能给区间", "这个窗口届时满"}},
		"refusals": {offGrid, []string{"算不出何时满", "步长与对象周期不一致", "另有 1 个 Plan 级守卫"}},
	} {
		clearing := WindowClearsOf(tc.anomaly, filling)
		if clearing == nil {
			t.Fatalf("%s: no clearing", name)
		}
		for _, want := range tc.want {
			if !strings.Contains(clearing.Line, want) {
				t.Errorf("%s: line %q lacks %q", name, clearing.Line, want)
			}
		}
		if strings.Contains(clearing.Line, "可信") {
			t.Errorf("%s: line %q claims a trusted result", name, clearing.Line)
		}
	}
}

// Only a result waiting on its windows gets a time; the interval comes from
// the object's wake.
func TestWindowClearsOnlyForAResultWaitingOnItsWindows(t *testing.T) {
	coverage := &HistoryCoverage{Short: 1, Windows: []WindowRow{windowWith(5, minutesBefore(clearingEnd, 4), 1)}}
	anomaly := Anomaly{Coverage: coverage, Wake: &WakeFacts{Known: true, IntervalSeconds: 60}}
	if got := WindowClearsOf(anomaly, Standing{State: StateResultUntrusted, Action: ActionWatch, Watch: WatchWindowFilling}); got == nil {
		t.Fatal("a filling window got no time")
	}
	if got := WindowClearsOf(anomaly, Standing{State: StateDetecting, Action: ActionNone}); got != nil {
		t.Errorf("a detecting result got %+v", got)
	}
	anomaly.Wake = nil
	if got := WindowClearsOf(anomaly, Standing{State: StateResultUntrusted, Watch: WatchWindowFilling}); got == nil || got.Refused != ClearingIntervalUnknown {
		t.Errorf("no interval should say so, got %+v", got)
	}
}
