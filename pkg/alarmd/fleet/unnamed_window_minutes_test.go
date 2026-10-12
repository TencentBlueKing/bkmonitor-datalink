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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The short windows a round did not name are the data's only when every minute
// any of them is missing at was evaluated by a remembered round that answered
// its primary whole, with data or empty -- the readings a named hole gets as
// ROUND_ANSWERED_WITHOUT_SERIES and ROUND_ANSWERED_EMPTY. Every other shape of
// the union, and every other kind of round, says nothing about them.
func TestUnnamedShortWindowsAreTheDatasOnlyAtMinutesAnsweredWhole(t *testing.T) {
	rounds := []roundMark{
		mark(100, "COMPLETED_WITH_UNAVAILABLE", "", primary("FULL", "DATA"), false),
		mark(160, "COMPLETED_WITH_UNAVAILABLE", "", primary("FULL", "DATA"), false),
		mark(220, "COMPLETED_WITH_UNAVAILABLE", "", primary("FULL", "EMPTY"), false),
		mark(280, "COMPLETED_WITH_PARTIAL_GAP", "", primary("PARTIAL", "DATA"), false),
		mark(340, "COMPLETED_WITH_UNAVAILABLE", "", nil, false),
	}
	for name, tc := range map[string]struct {
		mutate func(*observability.HistoryCoverageFacts)
		want   bool
	}{
		"every unnamed minute answered whole":       {func(*observability.HistoryCoverageFacts) {}, true},
		"a minute answered empty":                   {func(f *observability.HistoryCoverageFacts) { f.MissingMinutes = []int64{100, 220} }, true},
		"a minute answered partly":                  {func(f *observability.HistoryCoverageFacts) { f.MissingMinutes = []int64{160, 280} }, false},
		"a minute whose answer went unrecorded":     {func(f *observability.HistoryCoverageFacts) { f.MissingMinutes = []int64{100, 340} }, false},
		"a minute no remembered round evaluated":    {func(f *observability.HistoryCoverageFacts) { f.MissingMinutes = []int64{100, 460} }, false},
		"the union was cut short":                   {func(f *observability.HistoryCoverageFacts) { f.MissingMinutesTruncated = true }, false},
		"an unnamed window holds an unusable point": {func(f *observability.HistoryCoverageFacts) { f.ShortUnusable = 1 }, false},
		"no union at all, as an older worker sends": {func(f *observability.HistoryCoverageFacts) { f.MissingMinutes = nil }, false},
		"every short window named":                  {func(f *observability.HistoryCoverageFacts) { f.Short = 8 }, false},
	} {
		facts := &observability.HistoryCoverageFacts{Levels: 20, Short: 14, Windows: make([]observability.HistoryWindowFact, 8),
			MissingMinutes: []int64{100, 160}, End: 400}
		tc.mutate(facts)
		if got := unlistedHolesAnswered(rounds, facts); got != tc.want {
			t.Errorf("%s: unlisted holes answered = %v, want %v", name, got, tc.want)
		}
	}
	if unlistedHolesAnswered(rounds, nil) {
		t.Error("a round with no coverage said its unnamed windows were the data's")
	}
}

// A Query Group five strategies share turned three hosts that miss whole
// minutes into fifteen short windows and named eight. Every named one was the
// data's; the row went to WINDOW_UNDECIDED -- this side's, to fix -- for want of
// the other seven. With their minutes read it is SERIES_SPARSE, and without
// them it stays where it was.
func TestAWindowLineWithUnnamedWindowsIsTheDatasOnlyWhenTheirMinutesAre(t *testing.T) {
	sparse := func(missing uint32) WindowRow {
		return WindowRow{Verdict: VerdictDataAbsentWhenQueried, MissingTotal: missing, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: missing}}
	}
	named := []WindowRow{sparse(5), sparse(5), sparse(5), sparse(4), sparse(4), sparse(4), sparse(3), sparse(3)}
	guarded := func(unlisted bool, windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "GAP_GUARD_WARMING", CauseReason: "GAP_GUARD_WARMING",
			Coverage: &HistoryCoverage{Levels: 33, Short: 15, WorstValid: 4, WorstRequired: 9, ShortRounds: 35, Guarded: 15,
				Windows: windows, UnlistedHolesAnswered: unlisted}}
	}
	gapped := func(unlisted bool, windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED", Coverage: &HistoryCoverage{
			Levels: 33, Short: 15, WorstValid: 4, WorstRequired: 9, ShortRounds: 35, Windows: windows, UnlistedHolesAnswered: unlisted}}
	}
	incomplete := WindowRow{Verdict: VerdictInputIncomplete, MissingTotal: 2, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 1, InputIncomplete: 1}}
	// Its missing minute answered whole, and a record its Level could not use:
	// the verdict is the unusable point's, and the window is not the data's.
	unusable := WindowRow{Verdict: VerdictPointsUnusable, MissingTotal: 1, UnusableTotal: 1, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 1, Unusable: 1}}
	for name, tc := range map[string]struct {
		row  Anomaly
		want Check
	}{
		"guarded, named sparse, the rest answered whole":   {guarded(true, named...), CheckSeriesSparse},
		"guarded, named sparse, the rest unread":           {guarded(false, named...), CheckWindowUndecided},
		"guarded, one named window incomplete":             {guarded(true, append(append([]WindowRow{}, named[:7]...), incomplete)...), CheckWindowUndecided},
		"guarded, one named window with an unusable point": {guarded(true, append(append([]WindowRow{}, named[:7]...), unusable)...), CheckWindowUndecided},
		"gapped, named sparse, the rest answered whole":    {gapped(true, named...), CheckSeriesSparse},
		"gapped, named sparse, the rest unread":            {gapped(false, named...), CheckSeriesDataMissing},
		"guarded, nothing named, the rest answered whole":  {guarded(true), CheckWindowUndecided},
	} {
		check, under, _ := checkOf(tc.row, ScheduleOnTime)
		if check != tc.want || !under {
			t.Errorf("%s: check = %s (under %v), want %s", name, check, under, tc.want)
		}
	}
}

// The tracker reads the union against the rounds it remembers for the object
// and puts the answer on the row: every unnamed minute answered whole (one of
// them empty) is the data's; one minute the object never saw whole is not.
func TestTheTrackerReadsTheUnnamedMinutesAgainstItsOwnRounds(t *testing.T) {
	named := make([]observability.HistoryWindowFact, 0, 8)
	for i := 0; i < 8; i++ {
		named = append(named, observability.HistoryWindowFact{Series: fmt.Sprintf("s%d", i), Level: 1, Valid: 5, Required: 6, End: 420,
			Missing: []int64{240}, MissingTotal: 1})
	}
	for name, tc := range map[string]struct {
		minute300 *observability.PrimaryInputFacts
		kind300   string
		want      bool
	}{
		"every unnamed minute answered whole":   {primary("FULL", "DATA"), "FULL_COMPLETED", true},
		"one minute the object never saw whole": {primary("UNAVAILABLE", ""), "COMPLETED_WITH_UNAVAILABLE", false},
	} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-unnamed"})
		full := func(end int64) *observability.HistoryCoverageFacts {
			return &observability.HistoryCoverageFacts{Levels: 1, End: end}
		}
		// Minutes 180 and 240 answered with data, 300 as the case says (its
		// minute inferred), 360 answered empty (inferred too).
		round(ctx, tracker, 240, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), full(180))
		round(ctx, tracker, 300, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), full(240))
		round(ctx, tracker, 360, tc.kind300, "", "", tc.minute300, nil)
		round(ctx, tracker, 420, "FULL_EMPTY_COMPLETED", "", "", primary("FULL", "EMPTY"), nil)
		short := &observability.HistoryCoverageFacts{Levels: 12, Short: 10, WorstValid: 5, WorstRequired: 6, End: 420,
			Windows: named, MissingMinutes: []int64{240, 300, 360}}
		for i := 0; i < DefaultDegradedRounds; i++ {
			round(ctx, tracker, 480, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING", primary("FULL", "DATA"), short)
		}
		rows := anyColumn(tracker)
		if len(rows) != 1 || rows[0].Coverage == nil || len(rows[0].Coverage.Windows) != 8 {
			t.Fatalf("%s: rows = %+v, want the one object with its eight named windows", name, rows)
		}
		if got := rows[0].Coverage.UnlistedHolesAnswered; got != tc.want {
			t.Errorf("%s: unlisted holes answered = %v, want %v", name, got, tc.want)
		}
	}
}
