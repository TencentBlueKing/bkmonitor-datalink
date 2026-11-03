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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// memoryGapRun drives one object through the tracker the way
// TestARestartedProcessWaitsUntilTheWindowSlidesPastItsFirstRound does: a
// first round this process remembers, then a short window settled into a
// row. The window reaches back to windowStart.
type memoryGapRun struct {
	t       *testing.T
	tracker *Tracker
	ctx     context.Context
}

func newMemoryGapRun(t *testing.T, at *clock) *memoryGapRun {
	return &memoryGapRun{t: t, tracker: newTracker(t, at),
		ctx: observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-gap"})}
}

func (r *memoryGapRun) first(slot, end int64) {
	round(r.ctx, r.tracker, slot, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), &observability.HistoryCoverageFacts{Levels: 1, End: end})
}

func (r *memoryGapRun) settle(slot, end, windowStart int64, missing ...int64) Anomaly {
	r.t.Helper()
	valid := uint32(7 - len(missing))
	facts := &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: valid, WorstRequired: 7, End: end, WindowStart: windowStart,
		Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: valid, Required: 7, End: end,
			Missing: missing, MissingTotal: uint32(len(missing))}}}
	for i := 0; i < 10; i++ {
		round(r.ctx, r.tracker, slot, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), facts)
	}
	rows := anyColumn(r.tracker)
	if len(rows) != 1 {
		r.t.Fatalf("rows = %+v, want the one object", rows)
	}
	row := rows[0]
	row.Finding.Check, _, _ = checkOf(row, ScheduleOnTime)
	return row
}

// A short window whose holes reach back before the first round this replica
// remembers for the object - after a restart, or after the object moved here
// - says so on the row: since that first round, and until the window has slid
// past it, which is that first round plus the window's span from where it
// starts to its newest position. The span is read off the window itself, so
// a window stepping at a detection step shorter than the aggregation period
// settles in its own time. Once the window has slid past, the row no longer
// carries it.
func TestAWindowReachingBeforeThisReplicasMemorySaysSinceAndUntilWhen(t *testing.T) {
	t.Run("a period-stepped window", func(t *testing.T) {
		r := newMemoryGapRun(t, &clock{at: now})
		// This process's first round of the object evaluated minute 540.
		r.first(600, 540)
		// The window reaches back to 240: six minutes, two holes before 540.
		row := r.settle(660, 600, 240, 300, 360)
		if !gapIs(row.MemoryGap, 540, 540+(600-240)) {
			t.Fatalf("memory gap %+v, want since 540 until %d", row.MemoryGap, 540+(600-240))
		}
		if clause := evidenceClause(row); !strings.Contains(clause, "最晚 "+time.Unix(540+(600-240), 0).UTC().Format(time.RFC3339)+" 前") {
			t.Fatalf("evidence clause %q does not say when the gap settles", clause)
		}
		// Slid past 540: the one hole is a minute this process saw answered.
		r.first(720, 660)
		row = r.settle(780, 720, 360, 660)
		if row.MemoryGap != nil || row.Finding.Check != CheckSeriesSparse {
			t.Fatalf("after the slide: memory gap %+v, check %s; want none, the data's", row.MemoryGap, row.Finding.Check)
		}
	})
	t.Run("a window stepping at a detection step under the period", func(t *testing.T) {
		r := newMemoryGapRun(t, &clock{at: now})
		r.first(600, 540)
		// Five positions fifteen seconds apart: the window spans 75 s.
		row := r.settle(660, 600, 525, 530)
		if !gapIs(row.MemoryGap, 540, 540+75) {
			t.Fatalf("memory gap %+v, want since 540 until %d: in the window's own step", row.MemoryGap, 540+75)
		}
	})
	t.Run("moved here, not restarted", func(t *testing.T) {
		at := &clock{at: now}
		r := newMemoryGapRun(t, at)
		// The process has run for hours; the object's first round here is
		// recent, as one handed over by a rollout is.
		at.at = now.Add(6 * time.Hour)
		r.first(600, 540)
		if row := r.settle(660, 600, 240, 300); row.MemoryGap == nil || !row.MemoryGap.Since.Equal(time.Unix(540, 0)) {
			t.Fatalf("memory gap %+v, want one since the object's first round here", row.MemoryGap)
		}
	})
}

// The gap is named whatever else the window holds - a minute this side did
// not read whole beside one before this replica's memory still has the gap,
// the line decided by the incomplete minute as before - and not at all when
// no hole reaches before the memory. Without a window start the row says
// since when and not until when.
func TestTheMemoryGapIsReadOffTheHolesNotOffTheLine(t *testing.T) {
	state := func(counts WindowHoleCounts, windowStart int64) *queryGroupState {
		return &queryGroupState{coverage: &HistoryCoverage{Windows: []WindowRow{{HolesBy: counts}}},
			firstSlot: 600, slotOffset: 60, slotOffsetKnown: true, windowStart: windowStart, rounds: []roundMark{{end: 600}}}
	}
	if gap := memoryGapOf(state(WindowHoleCounts{BeforeThisProcess: 1, InputIncomplete: 1}, 240)); gap == nil || gap.Until == nil {
		t.Fatalf("an incomplete minute beside one before the memory: %+v, want the gap with its end", gap)
	}
	if gap := memoryGapOf(state(WindowHoleCounts{InputIncomplete: 1}, 240)); gap != nil {
		t.Fatalf("no hole before the memory: %+v, want none", gap)
	}
	if gap := memoryGapOf(state(WindowHoleCounts{BeforeThisProcess: 1}, 0)); gap == nil || gap.Until != nil {
		t.Fatalf("no window start: %+v, want since alone", gap)
	}
	// Not knowing where its memory starts, it names no gap: a since it
	// cannot give is not a gap it can date.
	unknownStart := state(WindowHoleCounts{BeforeThisProcess: 1}, 240)
	unknownStart.slotOffsetKnown = false
	if gap := memoryGapOf(unknownStart); gap != nil {
		t.Fatalf("the memory's start unknown: %+v, want none", gap)
	}
	unnamed := state(WindowHoleCounts{}, 240)
	unnamed.coverage.UnlistedHolesBeforeThisProcess = true
	if gap := memoryGapOf(unnamed); gap == nil {
		t.Fatal("unnamed windows before the memory: no gap")
	}
}

// Each line counts the objects under it carrying the gap, with the latest
// time any of them settles by: the page's "最晚 {until} 前会定下来".
func TestALineCountsItsObjectsInAMemoryGap(t *testing.T) {
	until := now.Add(20 * time.Minute)
	objects := []Anomaly{
		{QueryGroup: "qg-1", Finding: Finding{Check: CheckWindowUndecided, Group: "g"}, MemoryGap: &MemoryGap{Since: now, Until: timePointer(now.Add(5 * time.Minute))}},
		{QueryGroup: "qg-2", Finding: Finding{Check: CheckWindowUndecided, Group: "g"}, MemoryGap: &MemoryGap{Since: now, Until: &until}},
		{QueryGroup: "qg-3", Finding: Finding{Check: CheckWindowUndecided, Group: "g"}},
	}
	for _, report := range ReportChecks([][]Anomaly{objects}, nil, nil, now) {
		if report.Code != CheckWindowUndecided {
			continue
		}
		if report.MemoryGap != 2 || report.MemoryGapUntil == nil || !report.MemoryGapUntil.Equal(until) {
			t.Fatalf("line counts %d in a memory gap until %v, want 2 until %v", report.MemoryGap, report.MemoryGapUntil, until)
		}
		return
	}
	t.Fatal("no WINDOW_UNDECIDED line")
}

func gapIs(gap *MemoryGap, since, until int64) bool {
	return gap != nil && gap.Since.Equal(time.Unix(since, 0)) && gap.Until != nil && gap.Until.Equal(time.Unix(until, 0))
}

func timePointer(at time.Time) *time.Time { return &at }
