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

// The table is closed at sixteen. Adding a line to the first screen is a
// design change -- a rule over the dimensions -- and this is what makes it one
// rather than a word.
func TestTheCheckTableIsClosedAtSixteen(t *testing.T) {
	if got := len(Checks()); got != 16 || len(checkAnswers) != 16 {
		t.Errorf("the check table has %d rows in order and %d answered, want 16: a new check has to "+
			"be a rule over the existing dimensions, and the design says which sixteen", got, len(checkAnswers))
	}
	seen := map[Check]bool{}
	for _, check := range Checks() {
		if seen[check] {
			t.Errorf("check %s is listed twice in the order", check)
		}
		seen[check] = true
		if _, answered := checkAnswers[check]; !answered {
			t.Errorf("check %s is in the order and not in the table", check)
		}
	}
	for check := range checkAnswers {
		if !seen[check] {
			t.Errorf("check %s is in the table and not in the order: it would sort last whatever it is", check)
		}
	}
	// This deployment's own before undetermined before the others, and the
	// undetermined ones before what is confirmed as somebody else's: the order
	// is the order the reader acts in.
	rank := map[Owner]int{OwnerAlarmd: 0, OwnerUndetermined: 1, OwnerData: 2, OwnerStrategy: 2}
	previous := -1
	for _, check := range Checks() {
		if r := rank[checkAnswers[check].Owner]; r < previous {
			t.Errorf("check %s (%s) is listed after a check of a later owner", check, checkAnswers[check].Owner)
		} else {
			previous = r
		}
	}
	for _, check := range Checks() {
		answer := checkAnswers[check]
		if answer.Owner == "" || answer.GroupBy == "" {
			t.Errorf("check %s has no owner or no grouping key", check)
		}
		if answer.Owner == OwnerNobody {
			t.Errorf("check %s is nobody's: a check nobody acts on is not a check, it is a normal value", check)
		}
	}
}

// Every situation the server can decide lands under exactly one check or is a
// normal value that is on no line of the first screen. The normal values are
// named, so a situation that stops being one has to be moved on purpose.
func TestEverySituationIsUnderOneCheckOrIsANormalValue(t *testing.T) {
	normal := map[Situation]bool{
		SituationSeriesYoung:    true,
		SituationSeriesRenewed:  true,
		SituationVerdictHeld:    true,
		SituationDataJustGapped: true,
		SituationOffHours:       true,
	}
	for _, situation := range Situations() {
		check, under := checkOf(Anomaly{Finding: finding(situation, 0)})
		switch {
		case under && normal[situation]:
			t.Errorf("%s is under check %s and also declared a normal value", situation, check)
		case !under && !normal[situation]:
			t.Errorf("%s is under no check and is not a declared normal value: an object in it "+
				"vanishes from the first screen", situation)
		case under && checkAnswers[check].Owner == "":
			t.Errorf("%s is under %s, which the table does not answer", situation, check)
		}
	}
}

// Every check the table answers has something that produces it, except the two
// named as waiting on their producer. A check with no writer reads as a
// mechanism that is wired, so the gap is stated here rather than discovered.
func TestEveryCheckHasAProducerExceptTheNamedTwo(t *testing.T) {
	produced := map[Check]bool{}
	for _, situation := range Situations() {
		if check, under := checkOf(Anomaly{Finding: finding(situation, 0)}); under {
			produced[check] = true
		}
	}
	// The one split checkOf makes past the situation.
	for _, code := range []string{"panic", "source_error"} {
		if check, under := checkOf(Anomaly{ReasonCode: code, Finding: finding(SituationRoundBlocked, 0)}); under {
			produced[check] = true
		}
	}
	waiting := map[Check]bool{}
	for _, check := range ChecksWithoutAProducer {
		waiting[check] = true
	}
	for _, check := range Checks() {
		switch {
		case produced[check] && waiting[check]:
			t.Errorf("%s is produced and also listed as having no producer", check)
		case !produced[check] && !waiting[check]:
			t.Errorf("%s has no producer and is not listed as waiting for one: it reads as wired", check)
		}
	}
}

// A blocked round is a dependency or a defect depending on the outcome word,
// and the two are different work.
func TestABlockedRoundIsADefectWhenItPanicked(t *testing.T) {
	for code, want := range map[string]Check{
		"panic":          CheckDefect,
		"other_error":    CheckDefect,
		"source_error":   CheckDependencyDown,
		"source_retry":   CheckDependencyDown,
		"source_blocked": CheckDependencyDown,
	} {
		item := Anomaly{Kind: KindBlockedRun, ReasonCode: code}
		item.Finding = findingOf(item)
		got, under := checkOf(item)
		if !under || got != want {
			t.Errorf("blocked round %q is under %s (%v), want %s", code, got, under, want)
		}
	}
}

// The grouping key is the entity the objects fold on, and its absence is a
// named group rather than a dropped object.
func TestGroupKeysFoldOnTheEntityAndNameItsAbsence(t *testing.T) {
	strategyKeyed := Anomaly{Strategies: []StrategyRef{{StrategyID: "9000"}, {StrategyID: "1234"}}}
	if got := groupKeyOf(strategyKeyed, CheckSeriesChurning); got != "1234" {
		t.Errorf("strategy group = %q, want the smallest id 1234 so one object folds the same way every read", got)
	}
	if got := groupKeyOf(Anomaly{}, CheckSeriesChurning); got != groupNoStrategy {
		t.Errorf("strategy group with no strategies = %q, want %q", got, groupNoStrategy)
	}
	detailed := Anomaly{Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"}}
	if got := groupKeyOf(detailed, CheckBackendNotAnswering); got != "http_status=503" {
		t.Errorf("detail group = %q, want the detail", got)
	}
	coded := Anomaly{Failure: &FailureRef{Code: "QUERY_UNAVAILABLE"}}
	if got := groupKeyOf(coded, CheckBackendNotAnswering); got != "QUERY_UNAVAILABLE" {
		t.Errorf("detail group without a detail = %q, want the code", got)
	}
	if got := groupKeyOf(Anomaly{}, CheckBackendNotAnswering); got != groupNoDetail {
		t.Errorf("detail group with nothing = %q, want %q", got, groupNoDetail)
	}
	if got := groupKeyOf(Anomaly{Replica: "pod-a"}, CheckRoundsStalled); got != "pod-a" {
		t.Errorf("replica group = %q", got)
	}
	if got := groupKeyOf(Anomaly{CauseReason: "REDIS_UNAVAILABLE"}, CheckDependencyDown); got != "REDIS_UNAVAILABLE" {
		t.Errorf("code group = %q", got)
	}
}

// The report folds every column into the checks, counts entities distinctly,
// carries the view's own blind spots as one check, and orders the lines the way
// the reader acts.
func TestReportChecksFoldsColumnsAndCountsDistinctly(t *testing.T) {
	mk := func(id, replica string, mutate func(*Anomaly)) Anomaly {
		item := Anomaly{QueryGroup: id, Replica: replica, Kind: KindDegradedRun,
			Strategies: []StrategyRef{{StrategyID: "1", BusinessID: "7"}}}
		if mutate != nil {
			mutate(&item)
		}
		return item
	}
	anomalies := []Anomaly{
		mk("dep-1", "pod-a", func(a *Anomaly) { a.CauseReason = "REDIS_UNAVAILABLE" }),
		mk("dep-2", "pod-b", func(a *Anomaly) {
			a.CauseReason = "REDIS_UNAVAILABLE"
			a.Strategies = []StrategyRef{{StrategyID: "2", BusinessID: "7"}}
		}),
		mk("dep-3", "pod-b", func(a *Anomaly) { a.CauseReason = "KAFKA_UNAVAILABLE" }),
	}
	demoted := []Anomaly{
		mk("be-1", "pod-a", func(a *Anomaly) {
			a.Kind = KindQueryCooldown
			a.Failure = &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "transport=timeout"}
			a.Strategies = []StrategyRef{{StrategyID: "3", BusinessID: "8"}}
		}),
		mk("be-2", "pod-a", func(a *Anomaly) {
			a.Kind = KindQueryCooldown
			a.Failure = &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "transport=timeout"}
			a.Strategies = []StrategyRef{{StrategyID: "3", BusinessID: "8"}}
		}),
	}
	Attribute(anomalies)
	Attribute(demoted)
	view := &View{Unknown: 4, Gaps: []Gap{{Kind: GapUndetermined}, {Kind: GapSnapshotStale, Replica: "pod-b"}}}
	reports := ReportChecks([][]Anomaly{anomalies, demoted, nil, nil},
		map[string]bool{ColumnDemoted: true}, view)

	byCode := map[Check]CheckReport{}
	order := []Check{}
	for _, report := range reports {
		byCode[report.Code] = report
		order = append(order, report.Code)
	}
	dep := byCode[CheckDependencyDown]
	if dep.Objects != 3 || dep.Strategies != 2 || dep.Businesses != 1 {
		t.Errorf("DEPENDENCY_DOWN = %d objects / %d strategies / %d businesses, want 3 / 2 / 1: "+
			"strategies and businesses are distinct, not summed", dep.Objects, dep.Strategies, dep.Businesses)
	}
	if len(dep.Groups) != 2 || dep.Groups[0].Key != "REDIS_UNAVAILABLE" || dep.Groups[0].Objects != 2 ||
		dep.Groups[1].Key != "KAFKA_UNAVAILABLE" {
		t.Errorf("DEPENDENCY_DOWN groups = %+v, want redis (2) before kafka (1)", dep.Groups)
	}
	if dep.Partial {
		t.Error("DEPENDENCY_DOWN is partial, but the only truncated column is the demoted pool it draws nothing from")
	}
	backend := byCode[CheckBackendNotAnswering]
	if backend.Objects != 2 || !backend.Partial {
		t.Errorf("BACKEND_NOT_ANSWERING = %+v, want 2 objects and partial (its column was truncated)", backend)
	}
	if len(backend.Groups) != 1 || backend.Groups[0].Key != "transport=timeout" || backend.Groups[0].Strategies != 1 {
		t.Errorf("BACKEND_NOT_ANSWERING groups = %+v, want one fold on the symptom over one strategy", backend.Groups)
	}
	// The blind spots: four objects nobody can speak for, and a stale replica
	// that has no object count to give.
	gap := byCode[CheckObservationGap]
	if gap.Objects != 4 {
		t.Errorf("OBSERVATION_GAP objects = %d, want the view's 4 unknown", gap.Objects)
	}
	groups := map[string]int{}
	for _, group := range gap.Groups {
		groups[group.Key] = group.Objects
	}
	if groups[string(GapUndetermined)] != 4 {
		t.Errorf("OBSERVATION_GAP groups = %+v, want UNDETERMINED with 4", gap.Groups)
	}
	if _, present := groups[string(GapSnapshotStale)]; !present {
		t.Errorf("OBSERVATION_GAP groups = %+v, want the stale snapshot listed", gap.Groups)
	}
	// In the table's order: the dependency before the gap (both ours, and a
	// dependency down is work not being done while a gap is work not being
	// seen), the backend last. Not by object count -- the gap has more objects
	// and lists second.
	if len(order) != 3 || order[0] != CheckDependencyDown || order[1] != CheckObservationGap ||
		order[2] != CheckBackendNotAnswering {
		t.Errorf("report order = %v, want [DEPENDENCY_DOWN OBSERVATION_GAP BACKEND_NOT_ANSWERING]", order)
	}
}

// Marking an object stalled decides its whole finding again. Before this only
// the attribution was rewritten, so STALLED had no producer on a live view: the
// row kept the backend's situation and the page filed a stuck round as the
// data owner's.
func TestMarkStalledDecidesTheFindingAgain(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	list := []Anomaly{{QueryGroup: "qg", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
		FailingSince: at.Add(-time.Hour)}}
	Attribute(list)
	if list[0].Finding.Check != CheckBackendNotAnswering {
		t.Fatalf("before marking, check = %s, want BACKEND_NOT_ANSWERING", list[0].Finding.Check)
	}
	MarkStalled(list, at, 10*time.Minute)
	got := list[0]
	if !got.Stalled || got.Finding.Situation != SituationStalled || got.Finding.Check != CheckRoundsStalled ||
		got.Finding.Owner != OwnerAlarmd || got.Finding.Schedule != ScheduleStalled ||
		got.Attribution != AttributionOurs {
		t.Errorf("after marking: %+v, want STALLED / ROUNDS_STALLED / ALARMD / schedule STALLED / OURS", got.Finding)
	}
}

// The two dimensions the row shows, read off the anomaly.
func TestScheduleAndResultReadTheDimensionsOffTheAnomaly(t *testing.T) {
	for name, want := range map[string]struct {
		item     Anomaly
		schedule Schedule
		result   Result
	}{
		"completed degraded": {Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING"},
			ScheduleRunning, ResultCompleted},
		"failed with a backend error": {Anomaly{Kind: KindDegradedRun,
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"}},
			ScheduleRunning, ResultError},
		"refused by the backend": {Anomaly{Kind: KindQueryCooldown,
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_no_such_field"}},
			ScheduleCooling, ResultRefused},
		"blocked":  {Anomaly{Kind: KindBlockedRun, ReasonCode: "source_error"}, ScheduleRunning, ResultError},
		"overdue":  {Anomaly{Kind: KindOverdueWake}, ScheduleOverdue, ""},
		"paused":   {Anomaly{Kind: KindDegradedRun, CauseReason: "EFFECTIVE_TIME_INACTIVE"}, SchedulePaused, ResultCompleted},
		"stalled":  {Anomaly{Kind: KindDegradedRun, Stalled: true}, ScheduleStalled, ResultCompleted},
		"retrying": {Anomaly{Kind: KindDegradedRun, ReasonCode: "retrying"}, ScheduleRunning, ResultError},
	} {
		if got := scheduleOf(want.item); got != want.schedule {
			t.Errorf("%s: schedule = %s, want %s", name, got, want.schedule)
		}
		if got := resultOf(want.item); got != want.result {
			t.Errorf("%s: result = %q, want %q", name, got, want.result)
		}
	}
}
