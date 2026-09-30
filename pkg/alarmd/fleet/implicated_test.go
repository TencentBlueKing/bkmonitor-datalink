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
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	refA = StrategyRef{StrategyID: "4101", BusinessID: "7"}
	refB = StrategyRef{StrategyID: "4102", BusinessID: "7"}
	refC = StrategyRef{StrategyID: "4103", BusinessID: "7"}
	refD = StrategyRef{StrategyID: "4104", BusinessID: "7"}
)

// heldGuard is one held gap scope of a Plan, flat for longer than the
// stall bound, on a Plan that bound the given number of series.
func heldGuard(plan StrategyRef, matched int) GapGuard {
	return GapGuard{Plan: plan, Scope: "1", Status: "GAPPED", Required: 9, Observed: 0, Rounds: 20, UnchangedRounds: 19,
		SeriesMatched: matched, SeriesMatchedKnown: true}
}

// The evidence names each Plan by its own facts: a zero series count, a
// held guard, both, or neither -- never one Plan's fact for another, and
// never a Plan the row does not list.
func TestEachPlanIsNamedByItsOwnEvidence(t *testing.T) {
	three := []StrategyRef{refA, refB, refC}
	for name, testCase := range map[string]struct {
		row  Anomaly
		want []StrategyRef
	}{
		"one plan unbound and held":       {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, Guards: []GapGuard{heldGuard(refC, 0)}, PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 8}, {Plan: refC}}}, []StrategyRef{refC}},
		"one plan unbound, none held":     {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 8}, {Plan: refC}}}, []StrategyRef{refC}},
		"one plan held, every series in":  {Anomaly{Finding: Finding{Check: CheckWindowUndecided}, Strategies: three, Guards: []GapGuard{heldGuard(refB, 3)}, PlanSeries: []PlanSeriesMatched{{Plan: refB, Matched: 3}}}, []StrategyRef{refB}},
		"two plans unbound, one held":     {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, Guards: []GapGuard{heldGuard(refC, 0)}, PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 8}, {Plan: refB}, {Plan: refC}}}, []StrategyRef{refB, refC}},
		"held on one, unbound on another": {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, Guards: []GapGuard{heldGuard(refA, 3)}, PlanSeries: []PlanSeriesMatched{{Plan: refC}}}, []StrategyRef{refA, refC}},
		"no evidence":                     {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 8}}}, nil},
		"named plan the row lacks":        {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: []StrategyRef{refA, refB}, Guards: []GapGuard{heldGuard(refC, 0)}, PlanSeries: []PlanSeriesMatched{{Plan: refC}}}, nil},
		"one plan on the object":          {Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: []StrategyRef{refA}, Guards: []GapGuard{heldGuard(refA, 0)}, PlanSeries: []PlanSeriesMatched{{Plan: refA}}}, nil},
		"a whole-round check":             {Anomaly{Finding: Finding{Check: CheckDependencyDown}, Strategies: three, Guards: []GapGuard{heldGuard(refC, 0)}, PlanSeries: []PlanSeriesMatched{{Plan: refC}}}, nil},
	} {
		if got := implicatedStrategies(testCase.row, testCase.row.Finding.Check); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: implicated = %+v, want %+v", name, got, testCase.want)
		}
	}
}

// One object running four Plans under the missing-series check: two bound no
// series (one of them also held), one is held with its series present, one
// has no evidence. The row gives each of the first three its own words --
// the data's for the unbound, this side's for the held-with-data -- and the
// fourth no line at all; the same row under a whole-round check gives all
// four the row's words; the check's strategy group is the smallest named
// Plan, and a card for the fourth says whose the object's trouble is.
func TestARowGivesEachStrategyTheWordsOfItsOwnPlan(t *testing.T) {
	four := []StrategyRef{refA, refB, refC, refD}
	row := Anomaly{QueryGroup: "qg-shared", Finding: Finding{Check: CheckSeriesDataMissing}, Since: now.Add(-time.Hour), SinceFrom: SinceBusinessState,
		ReasonLastAt: now, Consecutive: 11, Strategies: four,
		Guards:     []GapGuard{heldGuard(refC, 0), heldGuard(refC, 0), heldGuard(refB, 843)},
		PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 12}, {Plan: refB, Matched: 843}, {Plan: refC, Matched: 0}, {Plan: refD, Matched: 0}},
		Coverage:   &HistoryCoverage{Levels: 1765, Short: 287, WorstValid: 6, WorstRequired: 9, UnchangedRounds: 1}}
	lines := StrategyLines(&View{Anomalies: []Anomaly{row}}, now)
	byID := map[string]StrategyLine{}
	for _, line := range lines {
		byID[line.StrategyID] = line
	}
	if len(lines) != 3 || byID[refA.StrategyID].StrategyID != "" {
		t.Fatalf("lines = %+v, want one each for the three Plans with evidence and none for the one without", lines)
	}
	if c := byID[refC.StrategyID].Standing; c.State != StateDataAbsent || c.Action != ActionDataCheck || c.RefinedBy != RuleStalled {
		t.Fatalf("held and unbound Plan reads %+v, want the data's words by its flat guard", c)
	}
	if d := byID[refD.StrategyID].Standing; d.State != StateDataAbsent || d.Action != ActionDataCheck || d.RefinedBy != RulePlanEvidence {
		t.Fatalf("unbound Plan with no guard reads %+v, want the data's words by its own series count", d)
	}
	if b := byID[refB.StrategyID].Standing; b.State != StateResultUntrusted || b.Action != ActionServiceFix || b.RefinedBy != RuleStalled {
		t.Fatalf("held Plan with its series present reads %+v, want this side's words by its flat guard", b)
	}
	for _, line := range lines {
		if !reflect.DeepEqual(line.Standing.About, []StrategyRef{refB, refC, refD}) {
			t.Fatalf("line %s carries About %+v, want the three named Plans", line.StrategyID, line.Standing.About)
		}
	}
	if !strings.HasPrefix(byID[refD.StrategyID].Line, "策略 4104 · 1 个对象 · 数据没到 · 数据负责人查") {
		t.Fatalf("unbound Plan's line reads %q", byID[refD.StrategyID].Line)
	}
	if got := groupKeyOf(row, CheckSeriesDataMissing); got != "4102+4103+4104" {
		t.Fatalf("group key = %q, want every named Plan joined, so the group's count is true of each", got)
	}
	// A Plan bound to no series for fewer rounds than the stall bound keeps
	// the check's own words: too early to send anyone to the data.
	young := row
	young.Consecutive = StalledRounds
	young.Guards = nil
	lines = StrategyLines(&View{Anomalies: []Anomaly{young}}, now)
	for _, line := range lines {
		if line.Standing.Action == ActionDataCheck {
			t.Fatalf("young unbound Plan %s already reads the data's: %+v", line.StrategyID, line.Standing)
		}
	}
	// The whole object's round lost: every strategy's, guards or not.
	whole := row
	whole.Finding = Finding{Check: CheckDependencyDown}
	lines = StrategyLines(&View{Anomalies: []Anomaly{whole}}, now)
	if len(lines) != 4 {
		t.Fatalf("lines under a whole-round check = %+v, want all four strategies", lines)
	}
	for _, line := range lines {
		if line.Standing.About != nil {
			t.Fatalf("line %s carries About %+v under a whole-round check", line.StrategyID, line.Standing.About)
		}
	}
	// Evidence naming no Plan keeps the row every strategy's, with no About.
	none := row
	none.Guards, none.PlanSeries = nil, []PlanSeriesMatched{{Plan: refA, Matched: 12}}
	lines = StrategyLines(&View{Anomalies: []Anomaly{none}}, now)
	if len(lines) != 4 || lines[0].Standing.About != nil {
		t.Fatalf("lines with no named Plan = %+v, want all four strategies and no About", lines)
	}
	if got := groupKeyOf(none, CheckSeriesDataMissing); got != refA.StrategyID {
		t.Fatalf("group key with no named Plan = %q, want the smallest id", got)
	}
	// The card for the Plan without evidence says whose the trouble is, in
	// states only -- no action word, so the thing to do is on one card.
	listed := withStanding(row)
	card := StrategyStanding{StrategyID: refA.StrategyID, Standing: StandingDetecting, Found: true,
		Plans: []StrategyPlanStanding{{StrategyPlanRef: StrategyPlanRef{QueryGroup: "qg-shared", Business: "7"}, Replica: "pod-a", Existence: "active", Rows: []Anomaly{listed}}}}
	line := strategyStandingLine(card)
	if !strings.Contains(line, "在检测；同对象上策略 4102：检测结果不能采信、4103：数据没到、4104：数据没到（见该策略）") || strings.Contains(line, "数据负责人查") {
		t.Fatalf("neighbour's card reads %q", line)
	}
	own := card
	own.StrategyID = refD.StrategyID
	if line := strategyStandingLine(own); !strings.Contains(line, "持有，数据没到·数据负责人查）") || strings.Contains(line, "同对象") {
		t.Fatalf("own card reads %q", line)
	}
}

// A historical row -- a loss the object has run past -- never decides a
// strategy's line over a current row, whatever the two checks' ranks; a
// strategy with only historical rows sorts after every current one.
func TestAPastLossDoesNotOutrankAPresentRow(t *testing.T) {
	historical := Anomaly{QueryGroup: "qg-one", Finding: Finding{Check: CheckDetectionAbandoned}, Loss: LossHistorical,
		Since: now.Add(-3 * time.Hour), SinceFrom: SinceProcessStart, LastHealthyAt: now.Add(-2 * time.Hour), Strategies: []StrategyRef{refA}}
	current := Anomaly{QueryGroup: "qg-one", Finding: Finding{Check: CheckWindowUndecided}, Since: now.Add(-time.Hour), SinceFrom: SinceBusinessState,
		ReasonLastAt: now, Strategies: []StrategyRef{refA}, CauseReason: "CONFIG_DRIFT",
		Coverage: &HistoryCoverage{Levels: 249, Short: 249, WorstValid: 14, WorstRequired: 1469, Guarded: 249}}
	onlyPast := Anomaly{QueryGroup: "qg-two", Finding: Finding{Check: CheckDetectionAbandoned}, Loss: LossHistorical,
		Since: now.Add(-3 * time.Hour), SinceFrom: SinceProcessStart, Strategies: []StrategyRef{refB}}
	if checkRank(CheckDetectionAbandoned) >= checkRank(CheckWindowUndecided) {
		t.Fatalf("the fixture needs the historical check to outrank the current one in the table; ranks %d vs %d",
			checkRank(CheckDetectionAbandoned), checkRank(CheckWindowUndecided))
	}
	lines := StrategyLines(&View{Anomalies: []Anomaly{historical, onlyPast, current}}, now)
	if len(lines) != 2 {
		t.Fatalf("lines = %+v", lines)
	}
	first := lines[0]
	if first.StrategyID != refA.StrategyID || first.Standing.Check != CheckWindowUndecided || first.Standing.State == StateRecovered {
		t.Fatalf("first line = %+v, want the current undecided window deciding, not the recovered loss", first)
	}
	if first.Objects != 1 || first.LastGoodAt == nil || !first.LastGoodAt.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("first line clocks/objects = %+v, want the historical row still counted for its object and its last good time", first)
	}
	if lines[1].StrategyID != refB.StrategyID || lines[1].Standing.RefinedBy != RuleHistoricalLoss {
		t.Fatalf("second line = %+v, want the strategy with only a past loss, after the current one", lines[1])
	}
}

// The strategy a row's check groups it under and the Plans its standing
// says the words are about are one relation read in two places; this pins
// them together for the checks grouped by strategy, so a change to either
// alone goes red rather than letting the group on the first page and the
// names on the card drift apart. The rows go through Attribute -- the one
// producer that files a row under a check -- because it computes the group
// before it writes the check onto the row, and a version of this that fed
// the row's own check to the group key was green while the first round of
// every object folded under the smallest id.
func TestTheStrategyGroupIsTheStrategiesTheWordsAreAbout(t *testing.T) {
	three := []StrategyRef{refA, refB, refC}
	missing := func(strategies []StrategyRef, guards []GapGuard, series []PlanSeriesMatched) Anomaly {
		return Anomaly{QueryGroup: "qg-" + strategies[0].StrategyID, Kind: "DEGRADED_RUN", ReasonCode: "COMPLETED_WITH_UNAVAILABLE",
			CauseReason: "HISTORY_GAPPED", Since: now.Add(-time.Hour), SinceFrom: SinceBusinessState, ReasonLastAt: now, Consecutive: 11,
			Coverage:   &HistoryCoverage{Levels: 30, Short: 4, WorstValid: 6, WorstRequired: 9, ShortRounds: 20},
			Strategies: strategies, Guards: guards, PlanSeries: series}
	}
	rows := []Anomaly{
		missing(three, []GapGuard{heldGuard(refC, 0)}, []PlanSeriesMatched{{Plan: refA, Matched: 8}, {Plan: refC}}),
		missing(three, []GapGuard{heldGuard(refC, 0)}, []PlanSeriesMatched{{Plan: refB}, {Plan: refC}}),
		missing(three, nil, []PlanSeriesMatched{{Plan: refA, Matched: 8}}),
		missing([]StrategyRef{refB}, []GapGuard{heldGuard(refB, 0)}, []PlanSeriesMatched{{Plan: refB}}),
	}
	Attribute(rows, now)
	for index, row := range rows {
		if row.Finding.Check != CheckSeriesDataMissing {
			t.Fatalf("row %d filed under %s, the fixture wanted the missing-series check", index, row.Finding.Check)
		}
		standing := standingOf(row)
		want := ""
		for _, ref := range row.Strategies {
			if want == "" || ref.StrategyID < want {
				want = ref.StrategyID
			}
		}
		if len(standing.About) > 0 {
			ids := make([]string, 0, len(standing.About))
			for _, ref := range standing.About {
				ids = append(ids, ref.StrategyID)
			}
			want = strings.Join(ids, "+")
		}
		if row.Finding.Group != want {
			t.Errorf("row %d: group = %q, standing about %+v: want %q", index, row.Finding.Group, standing.About, want)
		}
	}
	if rows[0].Finding.Group != "4103" || rows[1].Finding.Group != "4102+4103" || rows[2].Finding.Group != "4101" || rows[3].Finding.Group != "4102" {
		t.Fatalf("groups = %q %q %q %q", rows[0].Finding.Group, rows[1].Finding.Group, rows[2].Finding.Group, rows[3].Finding.Group)
	}
}

// A zero series count names its Plan only while it is recent. The entry is
// written from the Plan's evaluation lines and a Plan bound to nothing may
// skip a few rounds, so a lag of minutes is read; a zero older than the
// recent window is not -- it says nothing about what the Plan matches now.
func TestAStaleZeroDoesNotNameItsPlan(t *testing.T) {
	three := []StrategyRef{refA, refB, refC}
	row := Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, ReasonLastAt: now,
		PlanSeries: []PlanSeriesMatched{{Plan: refA, Matched: 8, LastSeenAt: now}, {Plan: refC, Matched: 0, LastSeenAt: now.Add(-4 * time.Minute)}}}
	if got := implicatedStrategies(row, CheckSeriesDataMissing); !reflect.DeepEqual(got, []StrategyRef{refC}) {
		t.Fatalf("a zero four minutes old names %+v, want the Plan", got)
	}
	row.PlanSeries[1].LastSeenAt = now.Add(-PlanSeriesZeroWindow - time.Minute)
	if got := implicatedStrategies(row, CheckSeriesDataMissing); got != nil {
		t.Fatalf("a zero past the recent window names %+v, want none", got)
	}
	// A held guard is this round's whatever the series count's age.
	row.Guards = []GapGuard{heldGuard(refC, 0)}
	if got := implicatedStrategies(row, CheckSeriesDataMissing); !reflect.DeepEqual(got, []StrategyRef{refC}) {
		t.Fatalf("a held Plan with a stale zero names %+v, want the Plan by its guard", got)
	}
	// Without both times there is no age to measure, and the zero is read as
	// it stands: a row that has not yet recorded a latest round, or a count
	// carrying no time, still names its Plan rather than being spared on an
	// age nobody measured.
	for _, unaged := range []struct {
		name string
		row  Anomaly
	}{
		{"no latest round on the row", Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three,
			PlanSeries: []PlanSeriesMatched{{Plan: refC, Matched: 0, LastSeenAt: now.Add(-24 * time.Hour)}}}},
		{"no time on the count", Anomaly{Finding: Finding{Check: CheckSeriesDataMissing}, Strategies: three, ReasonLastAt: now,
			PlanSeries: []PlanSeriesMatched{{Plan: refC, Matched: 0}}}},
	} {
		if got := implicatedStrategies(unaged.row, CheckSeriesDataMissing); !reflect.DeepEqual(got, []StrategyRef{refC}) {
			t.Fatalf("%s: names %+v, want the Plan read as-is", unaged.name, got)
		}
	}
}

// withStanding gives a row the standing walkObjectRows would.
func withStanding(row Anomaly) Anomaly {
	standing := standingOf(row)
	row.Standing = &standing
	return row
}
