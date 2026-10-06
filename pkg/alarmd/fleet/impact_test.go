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
	"testing"
	"time"
)

func withStrategies(queryGroup string, refs ...StrategyRef) Anomaly {
	item := anomaly(queryGroup)
	item.Strategies = refs
	return item
}

// A strategy with objects in two columns is one strategy.
//
// The page's lead sentence is how many strategies are getting no detection
// result, and adding the two columns overstates it -- by most when the
// deployment is worst, which is when the sentence is read.
func TestBlindImpactIsTheUnionOfTheTwoColumnsNotTheirSum(t *testing.T) {
	shared := StrategyRef{StrategyID: "100", BusinessID: "7"}
	view := View{
		Anomalies: []Anomaly{
			withStrategies("qg-a", shared),
			withStrategies("qg-b", StrategyRef{StrategyID: "200", BusinessID: "7"}),
		},
		AnomaliesTotal: 2,
		Demoted: []Anomaly{
			withStrategies("qg-c", shared),
			withStrategies("qg-d", StrategyRef{StrategyID: "300", BusinessID: "9"}),
		},
		DemotedTotal: 2,
	}
	impact := ImpactOf(view, now)
	if impact.Anomalies.Strategies != 2 || impact.Demoted.Strategies != 2 {
		t.Fatalf("columns = %d and %d strategies, want 2 and 2 -- the union below would be"+
			" comparing against the wrong thing", impact.Anomalies.Strategies, impact.Demoted.Strategies)
	}
	if impact.Blind.Strategies != 3 {
		t.Errorf("blind = %d strategies, want 3: strategy 100 has objects in both columns and"+
			" adding the two reports it twice", impact.Blind.Strategies)
	}
	if impact.Blind.Businesses != 2 {
		t.Errorf("blind = %d businesses, want 2", impact.Blind.Businesses)
	}
	if impact.Blind.Objects != 4 {
		t.Errorf("blind = %d objects, want 4: an object is in exactly one column, so those do add",
			impact.Blind.Objects)
	}
}

// An object that named no strategy is not an object affecting no strategy.
//
// A blocked round never got a Slot and the strategy references are read off the
// Slot, so those rows carry none by construction. Counted as zero, a column of
// them reports "0 条策略受影响", which reads as "nothing is affected" and means
// "we cannot say what is affected".
func TestObjectsThatNameNoStrategyAreReportedRatherThanCountedAsZero(t *testing.T) {
	blocked := anomaly("qg-blocked")
	blocked.Strategies = nil
	view := View{
		Anomalies:      []Anomaly{blocked, withStrategies("qg-a", StrategyRef{StrategyID: "100"})},
		AnomaliesTotal: 2,
	}
	impact := ImpactOf(view, now)
	if impact.NoStrategies != 1 {
		t.Errorf("no_strategies = %d, want 1", impact.NoStrategies)
	}
	if impact.Anomalies.Strategies != 1 {
		t.Errorf("strategies = %d, want 1", impact.Anomalies.Strategies)
	}
}

// A strategy count taken off a truncated list is a lower bound in the same
// shape as an exact one, on the line a reader uses to decide whether to act.
func TestImpactSaysWhenItsListWasCutBeforeItCounted(t *testing.T) {
	view := View{
		Anomalies:      []Anomaly{withStrategies("qg-a", StrategyRef{StrategyID: "100"})},
		AnomaliesTotal: 900,
	}
	impact := ImpactOf(view, now)
	if !impact.Anomalies.Partial {
		t.Error("the column does not report that its list was cut: 1 strategy off 900 objects" +
			" renders as the whole answer")
	}
	// The verdict's own subset inherits the limit, because it is counted off the
	// same cut list and is the number that decides whether anyone acts.
	if !impact.Ours.Partial {
		t.Error("the ours subset does not inherit the limit of the list it was counted from")
	}
}

// The act line said "其余要找策略配置或数据源的人" over lines that still read
// 待确认: not confirmed as this deployment's is not confirmed as anybody's.
// The impact cuts every column three ways by who acts -- this deployment,
// nobody yet, the strategy's or the data's people -- from each object's
// line, with the objects losing rounds now counted as this deployment's,
// and an object under two of one owner's lines counted once.
func TestImpactCutsEveryColumnByWhoActs(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	ref := func(strategy, business string) []StrategyRef {
		return []StrategyRef{{StrategyID: strategy, BusinessID: business}}
	}
	anomalies := []Anomaly{
		{QueryGroup: "qg-overdue", Kind: KindOverdueWake, Strategies: ref("1", "7")},
		{QueryGroup: "qg-timeout", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", Strategies: ref("2", "7")},
	}
	demoted := []Anomaly{
		{QueryGroup: "qg-refused", Kind: KindQueryCooldown, Strategies: ref("3", "8"),
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"}},
		{QueryGroup: "qg-missing", Kind: KindQueryCooldown, Strategies: ref("4", "8"),
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}},
	}
	// The overdue object again, under an undecided window: one object of
	// this deployment's and one undetermined, not two of either.
	undecidable := []Anomaly{{QueryGroup: "qg-overdue", Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Strategies: ref("1", "7"),
		Coverage: &HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40}}}
	for _, list := range [][]Anomaly{anomalies, demoted, undecidable} {
		Attribute(list, at)
	}
	// Data that stopped: the one line that is the data side's.
	noData := []Anomaly{{QueryGroup: "qg-nodata", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Strategies: ref("7", "8")}}
	Attribute(noData, at)
	view := View{Anomalies: anomalies, AnomaliesTotal: 2, Demoted: demoted, DemotedTotal: 2,
		Undecidable: undecidable, UndecidableTotal: 1, NoData: noData,
		GapSkips: map[string]SkippedSpan{
			// Losing rounds now: this deployment's, whatever column it is not in.
			"qg-losing": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a", Strategies: ref("5", "9")},
			// Stopped an hour ago: the record, nobody's work.
			"qg-stopped": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Hour), Replica: "pod-a", Strategies: ref("6", "9")},
		}}

	impact := ImpactOf(view, at)

	if impact.Alarmd.Objects != 2 || impact.Alarmd.Strategies != 2 || impact.Alarmd.Businesses != 2 {
		t.Errorf("alarmd = %+v, want the overdue object and the one losing rounds now: 2 objects, strategies 1 and 5, businesses 7 and 9", impact.Alarmd)
	}
	// The timed-out object is undetermined too: a client-side timeout does
	// not establish a fault on the data side.
	if impact.Undetermined.Objects != 3 || impact.Undetermined.Strategies != 3 {
		t.Errorf("undetermined = %+v, want the bare refusal, the timeout and the overdue object's undecided window: 3 objects, strategies 1, 2 and 3", impact.Undetermined)
	}
	if impact.Strategy.Objects != 1 || impact.Strategy.Strategies != 1 || impact.Strategy.Businesses != 1 {
		t.Errorf("strategy = %+v, want the object whose target is missing: strategy 4, business 8", impact.Strategy)
	}
	if impact.Data.Objects != 1 || impact.Data.Strategies != 1 {
		t.Errorf("data = %+v, want the object whose data stopped: strategy 7", impact.Data)
	}
	if impact.Alarmd.Partial || impact.Undetermined.Partial {
		t.Errorf("no column was cut, yet a part reads partial: %+v %+v", impact.Alarmd, impact.Undetermined)
	}
	// A cut column makes every part a lower bound: a line draws from all four.
	view.DemotedTotal = 50
	cut := ImpactOf(view, at)
	if !cut.Alarmd.Partial || !cut.Undetermined.Partial || !cut.Strategy.Partial || !cut.Data.Partial {
		t.Errorf("with the demoted column cut, parts read %v/%v/%v/%v partial, want all four",
			cut.Alarmd.Partial, cut.Undetermined.Partial, cut.Strategy.Partial, cut.Data.Partial)
	}
}
