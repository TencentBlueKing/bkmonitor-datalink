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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A release restarts every replica, and each restarted process starts
// remembering rounds from nothing: every sparse object's window is short at
// minutes this process never saw, for as long as the window reaches back past
// its first round. Filed under this side's fix, that is every sparse object
// of the deployment on the service's list after every release, up to an hour.
// It waits instead -- under NEXT_ROUND, the wait for a cause that did not
// survive a restart -- and only while the rest of the window is the data's
// or answered; a minute this side did not see whole, did not record, or
// forgot does not end by waiting and is not one.
func TestAWindowShortOnlyBeforeThisProcessWaits(t *testing.T) {
	window := func(counts WindowHoleCounts) WindowRow {
		total := counts.AnsweredWithoutSeries + counts.AnsweredEmpty + counts.InputIncomplete + counts.PrimaryUnrecorded +
			counts.NotInMemory + counts.BeforeThisProcess
		return WindowRow{Valid: 9 - total, Required: 9, MissingTotal: total, UnusableTotal: counts.Unusable, HolesBy: counts,
			Verdict: verdictOf(counts)}
	}
	before := window(WindowHoleCounts{BeforeThisProcess: 3})
	mixed := window(WindowHoleCounts{BeforeThisProcess: 2, AnsweredWithoutSeries: 1, AnsweredEmpty: 1})
	answered := window(WindowHoleCounts{AnsweredWithoutSeries: 3})
	// A window flat for StalledRounds rounds: stalled, read on its own, files
	// it under this side's fix.
	row := func(check Check, short uint32, unlistedAnswered, unlistedBefore bool, windows ...WindowRow) Anomaly {
		return Anomaly{Finding: Finding{Check: check}, ReasonLastAt: now, Coverage: &HistoryCoverage{
			Levels: short, Short: short, WorstValid: 6, WorstRequired: 9, Guarded: short, UnchangedRounds: StalledRounds,
			Windows: windows, UnlistedHolesAnswered: unlistedAnswered, UnlistedHolesBeforeThisProcess: unlistedBefore}}
	}
	for name, tc := range map[string]struct {
		row  Anomaly
		wait bool
	}{
		"every hole before this process":            {row(CheckWindowUndecided, 1, false, false, before), true},
		"before this process beside answered holes": {row(CheckWindowUndecided, 2, false, false, mixed, answered), true},
		"series data missing, before this process":  {row(CheckSeriesDataMissing, 1, false, false, before), true},
		"one minute not seen whole":                 {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 2, InputIncomplete: 1})), false},
		"one minute forgotten":                      {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 2, NotInMemory: 1})), false},
		"one answer not recorded":                   {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 2, PrimaryUnrecorded: 1})), false},
		"an unusable point":                         {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 2, Unusable: 1})), false},
		"one unusable point, one minute before":     {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 1, Unusable: 1})), false},
		// As many unusable points as holes of another cause: counting the
		// unusable among the answered would square the sum and wait.
		"an unusable point beside a forgotten minute":  {row(CheckWindowUndecided, 1, false, false, window(WindowHoleCounts{BeforeThisProcess: 1, NotInMemory: 1, Unusable: 1})), false},
		"nothing before this process":                  {row(CheckWindowUndecided, 1, false, false, answered), false},
		"unnamed windows before this process":          {row(CheckWindowUndecided, 10, false, true, answered), true},
		"unnamed windows answered, a named one before": {row(CheckWindowUndecided, 10, true, false, before), true},
		"unnamed windows unread":                       {row(CheckWindowUndecided, 10, false, false, before), false},
		"a check that is not the window's":             {row(CheckObservationGap, 1, false, false, before), false},
		"every hole before this process, nothing flat yet": {func() Anomaly {
			r := row(CheckWindowUndecided, 1, false, false, before)
			r.Coverage.UnchangedRounds = 0
			return r
		}(), true},
	} {
		standing := standingOf(tc.row)
		waiting := standing.Action == ActionWatch && standing.Watch == WatchNextRound && standing.RefinedBy == RuleWatch
		if waiting != tc.wait {
			t.Errorf("%s: standing = %+v, want a NEXT_ROUND wait %v", name, standing, tc.wait)
		}
		if !tc.wait && tc.row.Finding.Check == CheckWindowUndecided && standing.Action == ActionWatch && standing.Watch == WatchNextRound {
			t.Errorf("%s: a row that does not end by waiting was put on the wait", name)
		}
	}
}

// The unnamed windows' minutes are read the same way: each answered whole
// or before the first round this process remembers, at least one of the
// second. Not knowing when that first round was, one minute after it that no
// remembered round covers, one answered in part, a truncated union or an
// unusable point: none of these is a wait.
func TestTheUnnamedMinutesBeforeThisProcessAreToldApart(t *testing.T) {
	rounds := []roundMark{
		mark(540, "FULL_COMPLETED", "", primary("FULL", "DATA"), false),
		mark(600, "COMPLETED_WITH_UNAVAILABLE", "", primary("PARTIAL", "DATA"), false),
		mark(720, "FULL_EMPTY_COMPLETED", "", primary("FULL", "EMPTY"), false),
	}
	facts := func(minutes ...int64) *observability.HistoryCoverageFacts {
		return &observability.HistoryCoverageFacts{Levels: 10, Short: 10, End: 720, MissingMinutes: minutes,
			Windows: make([]observability.HistoryWindowFact, 8)}
	}
	for name, tc := range map[string]struct {
		facts *observability.HistoryCoverageFacts
		since int64
		want  bool
	}{
		"before, and answered whole": {facts(300, 360, 540, 720), 540, true},
		"all answered whole":         {facts(540, 720), 540, false},
		"first round not known":      {facts(300, 540), 0, false},
		"a minute after, forgotten":  {facts(300, 660), 540, false},
		"a minute answered in part":  {facts(300, 600), 540, false},
		"truncated union": {func() *observability.HistoryCoverageFacts {
			f := facts(300, 540)
			f.MissingMinutesTruncated = true
			return f
		}(), 540, false},
		"an unusable point": {func() *observability.HistoryCoverageFacts {
			f := facts(300, 540)
			f.ShortUnusable = 1
			return f
		}(), 540, false},
		"every window named": {func() *observability.HistoryCoverageFacts {
			f := facts(300, 540)
			f.Short = 8
			return f
		}(), 540, false},
	} {
		if got := unlistedHolesBeforeThisProcess(rounds, tc.facts, tc.since); got != tc.want {
			t.Errorf("%s: before this process = %v, want %v", name, got, tc.want)
		}
		if tc.want && unlistedHolesAnswered(rounds, tc.facts) {
			t.Errorf("%s: read as both answered and before this process", name)
		}
	}
	// The first round's own minute, once it has rolled out of the ring, was
	// seen and forgotten: not before this process.
	if unlistedHolesBeforeThisProcess(rounds[1:], facts(300, 540), 540) {
		t.Fatal("the first remembered round's own minute, forgotten, was read as before this process")
	}
}

// Every cause windowRows files a hole under is in the closed list the page
// checks its words against: one of each, each named in HoleCauses.
func TestEveryCauseAHoleIsFiledUnderIsListed(t *testing.T) {
	rounds := []roundMark{
		mark(540, "FULL_COMPLETED", "", primary("FULL", "DATA"), false),
		mark(600, "FULL_EMPTY_COMPLETED", "", primary("FULL", "EMPTY"), false),
		mark(660, "COMPLETED_WITH_UNAVAILABLE", "", primary("PARTIAL", "DATA"), false),
		mark(720, "FULL_COMPLETED", "", nil, false),
		mark(840, "FULL_COMPLETED", "", primary("FULL", "DATA"), false),
	}
	rows := windowRows(rounds, &observability.HistoryCoverageFacts{Levels: 1, Short: 1, End: 840,
		Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: 1, Required: 9, End: 840,
			Missing: []int64{300, 540, 560, 600, 660, 720, 780}, MissingTotal: 7, Unusable: []int64{840}, UnusableTotal: 1}}}, 540, 560)
	listed := map[HoleCause]bool{}
	for _, cause := range HoleCauses {
		listed[cause] = true
	}
	seen := map[HoleCause]bool{}
	for _, hole := range rows[0].Holes {
		if !listed[hole.Cause] {
			t.Errorf("hole at %v filed under %s, which HoleCauses does not list", hole.At, hole.Cause)
		}
		seen[hole.Cause] = true
	}
	if len(seen) != len(HoleCauses) {
		t.Fatalf("causes filed %v, want one of each of the %d listed", seen, len(HoleCauses))
	}
}

// Through the tracker: a process whose first round of the object evaluated
// minute 540 reads the window's minutes before 540 as BEFORE_THIS_PROCESS and
// waits; once the window has slid past 540 the same object is the data's.
// A minute after 540 that rolled out of the remembered rounds is forgotten,
// NOT_IN_MEMORY, and the wait does not cover it.
func TestARestartedProcessWaitsUntilTheWindowSlidesPastItsFirstRound(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-restart"})
	short := func(end int64, missing ...int64) *observability.HistoryCoverageFacts {
		valid := uint32(7 - len(missing))
		return &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: valid, WorstRequired: 7, End: end,
			Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: valid, Required: 7, End: end,
				Missing: missing, MissingTotal: uint32(len(missing))}}}
	}
	settle := func(slot int64, facts *observability.HistoryCoverageFacts) Anomaly {
		t.Helper()
		// A gapped window is a check once it has been short for more rounds
		// than it requires positions (windowCheck).
		for i := 0; i < 10; i++ {
			round(ctx, tracker, slot, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), facts)
		}
		rows := anyColumn(tracker)
		if len(rows) != 1 || rows[0].Coverage == nil || len(rows[0].Coverage.Windows) != 1 {
			t.Fatalf("rows = %+v, want the one object with its one named window", rows)
		}
		row := rows[0]
		row.Finding.Check, _, _ = checkOf(row, ScheduleOnTime)
		return row
	}
	// This process's first round of the object: Slot 600, minute 540, the
	// series in it.
	round(ctx, tracker, 600, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), &observability.HistoryCoverageFacts{Levels: 1, End: 540})
	// Two short windows, one named; the unnamed one is short at the same two
	// minutes, both before this process.
	first := short(600, 300, 360)
	first.Levels, first.Short, first.MissingMinutes = 2, 2, []int64{300, 360}
	row := settle(660, first)
	window := row.Coverage.Windows[0]
	if window.HolesBy != (WindowHoleCounts{BeforeThisProcess: 2}) || window.Holes[0].Cause != HoleBeforeThisProcess || window.Verdict != VerdictUnknown {
		t.Fatalf("window = %+v, want both holes before this process and nothing decided", window)
	}
	if !row.Coverage.UnlistedHolesBeforeThisProcess || row.Coverage.UnlistedHolesAnswered {
		t.Fatalf("unnamed windows: before this process %v, answered %v; want before this process only",
			row.Coverage.UnlistedHolesBeforeThisProcess, row.Coverage.UnlistedHolesAnswered)
	}
	if clause := evidenceClause(row); !strings.Contains(clause, "2 分钟早于本副本接手这个对象") {
		t.Fatalf("evidence clause = %q, want the two minutes before this process named", clause)
	}
	if !planScopedCheck(row.Finding.Check) {
		t.Fatalf("check = %s, want the undecided window's", row.Finding.Check)
	}
	if standing := standingOf(row); standing.Action != ActionWatch || standing.Watch != WatchNextRound {
		t.Fatalf("standing = %+v, want a NEXT_ROUND wait while the window reaches back past this process", standing)
	}
	// The window has slid past 540: its one hole is a minute this process saw
	// answered without the series.
	round(ctx, tracker, 720, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), &observability.HistoryCoverageFacts{Levels: 1, End: 660})
	row = settle(780, short(720, 660))
	if row.Coverage.Windows[0].HolesBy != (WindowHoleCounts{AnsweredWithoutSeries: 1}) || row.Finding.Check != CheckSeriesSparse {
		t.Fatalf("after the slide: window %+v, check %s; want the data's", row.Coverage.Windows[0], row.Finding.Check)
	}
	// Enough rounds that minutes 540 and 660 roll out of the ring: 540 is
	// this process's first round and 660 after it, both seen, so forgotten,
	// and no wait.
	for slot := int64(840); slot < 840+60*RecentRoundsKept; slot += 60 {
		round(ctx, tracker, slot, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), &observability.HistoryCoverageFacts{Levels: 1, End: slot - 60})
	}
	last := int64(840 + 60*RecentRoundsKept)
	row = settle(last, short(last-60, 300, 540, 660))
	if got := row.Coverage.Windows[0].HolesBy; got != (WindowHoleCounts{BeforeThisProcess: 1, NotInMemory: 2}) {
		t.Fatalf("holes by cause = %+v, want 300 before this process, 540 and 660 forgotten", got)
	}
	if standing := standingOf(row); standing.Action == ActionWatch && standing.Watch == WatchNextRound {
		t.Fatalf("standing = %+v: a forgotten minute was put on the wait", standing)
	}
}

// When the first round is known and when not: it needs both a remembered
// Slot and the object's Slot-to-minute offset, and without either no hole is
// filed before this process. A minute before this process decides nothing on
// its own window, beside answered minutes too.
func TestTheFirstRememberedMinuteNeedsTheSlotOffset(t *testing.T) {
	for name, tc := range map[string]struct {
		state queryGroupState
		want  int64
	}{
		"slot and offset known": {queryGroupState{firstSlot: 600, slotOffset: 60, slotOffsetKnown: true}, 540},
		"offset not known":      {queryGroupState{firstSlot: 600}, 0},
		"no round remembered":   {queryGroupState{slotOffset: 60, slotOffsetKnown: true}, 0},
	} {
		state := tc.state
		if got := rememberedSince(&state); got != tc.want {
			t.Errorf("%s: remembered since %d, want %d", name, got, tc.want)
		}
	}
	if verdict := verdictOf(WindowHoleCounts{BeforeThisProcess: 1, AnsweredWithoutSeries: 2}); verdict != VerdictUnknown {
		t.Fatalf("a window with a minute before this process decided %s, want UNKNOWN", verdict)
	}
}
