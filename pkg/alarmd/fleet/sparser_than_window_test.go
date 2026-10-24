// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package fleet

import (
	"testing"
	"time"
)

// sparserCoverage is the coverage of a long-lived series reporting about
// every five steps under a nine position window: two of nine valid in the
// worst window and in the one named, every hole a minute the query answered
// whole without the series, short for forty rounds.
func sparserCoverage() *HistoryCoverage {
	return &HistoryCoverage{Levels: 9, Short: 1, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
		Windows: []WindowRow{{Valid: 2, Required: 9, Verdict: VerdictDataAbsentWhenQueried, MissingTotal: 7,
			HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 7}}}}
}

// A series sparser than its window is its own line under every reason that
// says its window is short, folded on its cadence, and never the data's
// SERIES_SPARSE or the undecided window: its recovery is decided.
func TestASeriesSparserThanItsWindowHasALineOfItsOwn(t *testing.T) {
	at := time.Unix(1_790_000_000, 0)
	for _, reason := range []string{"HISTORY_WARMING", "HISTORY_GAPPED", "GAP_GUARD_WARMING"} {
		coverage := sparserCoverage()
		if reason == "GAP_GUARD_WARMING" {
			coverage.Guarded = 1
		}
		list := []Anomaly{{Kind: KindDegradedRun, CauseReason: reason, Coverage: coverage}}
		Attribute(list, at)
		finding := list[0].Finding
		if finding.Check != CheckSeriesSparserThanWindow || finding.Owner != OwnerStrategy || finding.Group != "every_5_steps" {
			t.Errorf("%s: finding = %+v, want SERIES_SPARSER_THAN_WINDOW, the strategy's, folded every_5_steps", reason, finding)
		}
	}
}

// The predicate tells a cadence from data that went missing. Each of these
// keeps the line the counts put it on.
func TestOnlyACadenceIsSparserThanItsWindow(t *testing.T) {
	if !sparserCoverage().SparserThanWindow() || sparserCoverage().PointEverySteps() != 5 {
		t.Fatalf("the fixture is not sparser than its window: every %d steps", sparserCoverage().PointEverySteps())
	}
	cases := map[string]func(*HistoryCoverage){
		"more than half the window valid":     func(c *HistoryCoverage) { c.WorstValid, c.Windows[0].Valid = 5, 5 },
		"a named window more than half valid": func(c *HistoryCoverage) { c.Windows[0].Valid = 6 },
		"a hole the query did not answer whole": func(c *HistoryCoverage) {
			c.Windows[0].HolesBy = WindowHoleCounts{AnsweredWithoutSeries: 6, InputIncomplete: 1}
			c.Windows[0].Verdict = ""
		},
		"new series":            func(c *HistoryCoverage) { c.Fresh, c.ShortFresh = 1, 1 },
		"not yet past filling":  func(c *HistoryCoverage) { c.ShortRounds = 9 },
		"an unusable record":    func(c *HistoryCoverage) { c.Unusable = 1 },
		"an empty window":       func(c *HistoryCoverage) { c.Empty, c.EmptyRounds = 1, 40 },
		"no point in the worst": func(c *HistoryCoverage) { c.WorstValid = 0 },
	}
	for name, change := range cases {
		coverage := sparserCoverage()
		change(coverage)
		if coverage.SparserThanWindow() || coverage.PointEverySteps() != 0 {
			t.Errorf("%s: read as sparser than its window", name)
		}
	}
	var none *HistoryCoverage
	if none.SparserThanWindow() {
		t.Error("no coverage read as sparser than its window")
	}
}

// Under a reason that would call it undecidable, a series sparser than its
// window goes to the column that says it is working as configured, and the
// summary does not count it among the windows whose alerts cannot clear.
func TestASeriesSparserThanItsWindowIsNotUndecidable(t *testing.T) {
	for _, reason := range []string{"HISTORY_WARMING", "GAP_GUARD_WARMING"} {
		state := &queryGroupState{currentKind: KindDegradedRun, causeReason: reason, coverage: sparserCoverage()}
		if column := columnOf(state); column != ColumnByDesign {
			t.Errorf("%s: column = %q, want %q", reason, column, ColumnByDesign)
		}
		// The same reason over a window that is short for another cause stays
		// undecidable.
		state.coverage = &HistoryCoverage{Levels: 9, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 40}
		if column := columnOf(state); column != ColumnUndecidable {
			t.Errorf("%s over a window missing two minutes: column = %q, want %q", reason, column, ColumnUndecidable)
		}
	}

	at := time.Unix(1_790_000_000, 0)
	list := []Anomaly{
		{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: sparserCoverage()},
		{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING",
			Coverage: &HistoryCoverage{Levels: 9, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 40}},
	}
	Attribute(list, at)
	summary := summarize(list, at)
	if summary.SparserThanWindow != 1 || summary.WindowNeverFills != 1 {
		t.Fatalf("sparser_than_window = %d, window_never_fills = %d, want 1 and 1: the sparse series is decided "+
			"and must not be told back as an alert that cannot clear", summary.SparserThanWindow, summary.WindowNeverFills)
	}
}
