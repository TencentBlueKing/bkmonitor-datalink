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
//
// Twenty: the sixteen rules over object dimensions; three standings of the
// deployment itself -- the fleet executing a publication that is no longer
// current, a replica past a bound, and the ready replicas holding uneven
// shares by the scheduler's own tolerance -- the first two of which decided
// the verdict with no line on the first screen until a running deployment
// spent half a day on a stale publication behind a DEGRADED badge that named
// the object list, and the third of which sat in the replica table as 2370
// against 0 with no line; and the refusal that names what is missing, split
// from the one that does not, because the pool card called those strategies
// unusable while the line said 待确认. The design names all twenty.
func TestTheCheckTableIsClosedAtTwenty(t *testing.T) {
	if got := len(Checks()); got != 20 || len(checkAnswers) != 20 {
		t.Errorf("the check table has %d rows in order and %d answered, want 20: a new check has to "+
			"be a rule over the existing dimensions or a named standing, and the design says which twenty", got, len(checkAnswers))
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

// Every check the table answers has evidence that produces it, except the one
// named as waiting on its producer. A check with no writer reads as a mechanism
// that is wired, so the gap is stated rather than discovered.
//
// The producers are enumerated as anomalies, one per path through checkOf,
// and the set of checks they reach has to be the table minus the named
// exception -- so a path that stops producing its check fails here, and so
// does a check added to the table with nothing that reaches it.
func TestEveryCheckHasAProducerExceptTheNamedOne(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	producers := map[Check]Anomaly{
		CheckRoundsStalled:       {Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", Stalled: true},
		CheckSlotsOverdue:        {Kind: KindOverdueWake},
		CheckDetectionAbandoned:  {Kind: KindDegradedRun, CauseReason: "GAP_SKIPPED"},
		CheckTimelinePruned:      {Kind: KindDegradedRun, CauseReason: "SCHEDULE_PRUNED"},
		CheckDependencyDown:      {Kind: KindBlockedRun, ReasonCode: "source_error"},
		CheckDefect:              {Kind: KindBlockedRun, ReasonCode: "panic"},
		CheckObservationGap:      {Kind: KindDegradedRun, SinceFrom: SinceRestoredLastFull},
		CheckBackendNotAnswering: {Kind: KindQueryCooldown, Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "transport=timeout"}},
		CheckQueryRefused:        {Kind: KindQueryCooldown, Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"}},
		CheckQueryTargetMissing:  {Kind: KindQueryCooldown, Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}},
		CheckNoDataPersistent:    {Kind: KindNoData},
		CheckSeriesChurning: {Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{
			Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40, Fresh: 4, ShortFresh: 4, FreshRounds: 40}},
		CheckSeriesDataMissing: {Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{
			Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40}},
		CheckWindowUndecided: {Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{
			Levels: 3, Short: 2, Empty: 2, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40}},
		CheckPlanUnevaluable:  {Kind: KindDegradedRun, CauseReason: "ALGORITHM_UNSUPPORTED"},
		CheckConfigUnresolved: {Kind: KindDegradedRun, CauseReason: "CONFIG_DRIFT"},
	}
	produced := map[Check]bool{}
	for want, item := range producers {
		list := []Anomaly{item}
		Attribute(list, at)
		if list[0].Finding.Check != want {
			t.Errorf("the producer for %s reaches %q instead", want, list[0].Finding.Check)
		}
		produced[list[0].Finding.Check] = true
	}
	// The three standings are produced from the view, not from any object:
	// one fact per deployment about the publication it executes, one per
	// replica about a bound, one per deployment about how the replicas share
	// the objects. Each is enumerated the same way, one view per path.
	standings := map[Check]View{
		CheckCutoverFailing: {Activation: &ActivationFacts{Behind: true, BehindBeyondBound: true,
			FailureStage: "schedule_cutover", FailureClass: "schedule_conflict"}, ActivationReplica: "pod-a"},
		CheckReplicaDegraded: {Degradations: []Degradation{{Kind: DegradationOpenAlertSetStale, Replica: "pod-b"}}},
		CheckOwnershipSkewed: {Rebalance: &RebalanceFacts{ReadyWorkers: 2, Assigned: 4, Target: 2, MostOwned: 4,
			MostOwnedBy: "pod-a", LeastOwnedBy: "pod-b", Batch: 1, PlannedMoves: 1, StopSpreadPercent: 5, Shadow: true}, RebalanceReplica: "pod-a"},
	}
	for want, view := range standings {
		reports := ReportChecks(nil, nil, &view, now)
		if len(reports) != 1 || reports[0].Code != want {
			t.Errorf("the standing producer for %s reaches %+v instead", want, reports)
			continue
		}
		produced[want] = true
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
	// And the normal values: objects under no line, on purpose, each one a
	// state that resolves on its own or is the configuration doing its job.
	for name, item := range map[string]Anomaly{
		"young": {Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{
			Levels: 3, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 2}},
		"held":      {Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED", Coverage: &HistoryCoverage{Levels: 3, Guarded: 3}},
		"off hours": {Kind: KindDegradedRun, CauseReason: "EFFECTIVE_TIME_INACTIVE"},
	} {
		list := []Anomaly{item}
		Attribute(list, at)
		if list[0].Finding.Check != "" || list[0].Finding.Owner != OwnerNobody || list[0].Unclassified {
			t.Errorf("%s is under %q / %s (unclassified %v), want under no line and nobody's", name,
				list[0].Finding.Check, list[0].Finding.Owner, list[0].Unclassified)
		}
	}
	// And the fall-through: a code nobody has classified is a DEFECT that says
	// so, not a line somebody chose.
	unknown := []Anomaly{{Kind: KindDegradedRun, CauseReason: "SOMETHING_NEW"}}
	Attribute(unknown, at)
	if unknown[0].Finding.Check != CheckDefect || !unknown[0].Unclassified || unknown[0].Attribution != AttributionOurs {
		t.Errorf("an unclassified code = %+v (unclassified %v, %s), want DEFECT, flagged, ours",
			unknown[0].Finding, unknown[0].Unclassified, unknown[0].Attribution)
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
		list := []Anomaly{{Kind: KindBlockedRun, ReasonCode: code}}
		Attribute(list, now)
		if list[0].Finding.Check != want {
			t.Errorf("blocked round %q is under %s, want %s", code, list[0].Finding.Check, want)
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
	// The undecided window folds on what happened, not on the strategy: a
	// starved window on the reason its record could not be used, a mixed one
	// on being mixed.
	starved := Anomaly{Coverage: &HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14, EmptyRounds: 40,
		Unusable: 2, UnusableReason: "REQUIRED_VALUE_MISSING"}}
	if got := groupKeyOf(starved, CheckWindowUndecided); got != "REQUIRED_VALUE_MISSING" {
		t.Errorf("starved window group = %q, want the detection's reason", got)
	}
	starvedNoReason := Anomaly{Coverage: &HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14, EmptyRounds: 40}}
	if got := groupKeyOf(starvedNoReason, CheckWindowUndecided); got != causeUnusableNoWord {
		t.Errorf("starved window without a reason group = %q, want %q", got, causeUnusableNoWord)
	}
	mixed := Anomaly{Coverage: &HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
		Fresh: 2, ShortFresh: 2, FreshRounds: 40}}
	if got := groupKeyOf(mixed, CheckWindowUndecided); got != causeSeriesMixed {
		t.Errorf("mixed window group = %q, want %q", got, causeSeriesMixed)
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
	Attribute(anomalies, now)
	Attribute(demoted, now)
	view := &View{Unknown: 4, Gaps: []Gap{{Kind: GapUndetermined}, {Kind: GapSnapshotStale, Replica: "pod-b"}}}
	reports := ReportChecks([][]Anomaly{anomalies, demoted, nil, nil},
		map[string]bool{ColumnDemoted: true}, view, now)

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
	Attribute(list, now)
	if list[0].Finding.Check != CheckBackendNotAnswering {
		t.Fatalf("before marking, check = %s, want BACKEND_NOT_ANSWERING", list[0].Finding.Check)
	}
	MarkStalled(list, at, 10*time.Minute)
	got := list[0]
	if !got.Stalled || got.Finding.Check != CheckRoundsStalled ||
		got.Finding.Owner != OwnerAlarmd || got.Finding.Schedule != ScheduleStalled ||
		got.Attribution != AttributionOurs {
		t.Errorf("after marking: %+v, want ROUNDS_STALLED / ALARMD / schedule STALLED / OURS", got.Finding)
	}
}

// The two dimensions the row shows, read off the anomaly and the due index's
// wake facts. Every schedule value has a case, and the empty value -- no wake
// facts at all -- is a case too, because that is what an older replica sends.
func TestScheduleAndResultReadTheDimensionsOffTheAnomaly(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	wake := func(due time.Duration, interval int64) *WakeFacts {
		return &WakeFacts{Known: true, DueAt: at.Add(due), IntervalSeconds: interval}
	}
	for name, want := range map[string]struct {
		item     Anomaly
		schedule Schedule
		result   Result
	}{
		"no wake facts at all": {Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING"},
			"", ResultCompleted},
		"waiting for the next due": {Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING",
			Wake: wake(40*time.Second, 60)}, ScheduleOnTime, ResultCompleted},
		"late within one period": {Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING",
			Wake: wake(-10*time.Second, 60)}, ScheduleLate, ResultCompleted},
		"late by exactly one period is still late": {Anomaly{Kind: KindDegradedRun,
			Wake: wake(-60*time.Second, 60)}, ScheduleLate, ResultCompleted},
		"overdue past one period": {Anomaly{Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=503"},
			Wake:    wake(-120*time.Second, 60)}, ScheduleOverdue, ResultError},
		"late with no period known stays late": {Anomaly{Kind: KindDegradedRun,
			Wake: wake(-3*time.Hour, 0)}, ScheduleLate, ResultCompleted},
		"never evaluated since takeover": {Anomaly{Kind: KindDegradedRun, SinceFrom: SinceRestoredLastFull,
			Wake: &WakeFacts{Known: false}}, ScheduleNew, ResultCompleted},
		"cooling by the wake facts": {Anomaly{Kind: KindDegradedRun,
			Wake: &WakeFacts{Known: true, DueAt: at.Add(5 * time.Minute), Cooling: true}},
			ScheduleCooling, ResultCompleted},
		"refused by the backend": {Anomaly{Kind: KindQueryCooldown,
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_no_such_field"}},
			ScheduleCooling, ResultRefused},
		"blocked": {Anomaly{Kind: KindBlockedRun, ReasonCode: "source_error", Wake: wake(30*time.Second, 60)},
			ScheduleOnTime, ResultError},
		"overdue wake": {Anomaly{Kind: KindOverdueWake}, ScheduleOverdue, ""},
		"paused":       {Anomaly{Kind: KindDegradedRun, CauseReason: "EFFECTIVE_TIME_INACTIVE"}, SchedulePaused, ResultCompleted},
		"stalled":      {Anomaly{Kind: KindDegradedRun, Stalled: true, Wake: wake(-10*time.Second, 60)}, ScheduleStalled, ResultCompleted},
		"retrying":     {Anomaly{Kind: KindDegradedRun, ReasonCode: "retrying"}, "", ResultError},
	} {
		if got := scheduleOf(want.item, at); got != want.schedule {
			t.Errorf("%s: schedule = %q, want %q", name, got, want.schedule)
		}
		if got := resultOf(want.item); got != want.result {
			t.Errorf("%s: result = %q, want %q", name, got, want.result)
		}
	}
}

// Not being evaluated outranks how the last round went. An object whose last
// round timed out at the backend and whose turn has since been missed is under
// SLOTS_OVERDUE, this deployment's, not under the backend's line: the backend
// is not what is stopping it from running now.
func TestAMissedTurnOutranksTheLastRoundsReason(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	list := []Anomaly{{QueryGroup: "qg", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
		Wake: &WakeFacts{Known: true, DueAt: at.Add(-3 * time.Minute), IntervalSeconds: 60}}}
	Attribute(list, at)
	if list[0].Finding.Check != CheckSlotsOverdue || list[0].Finding.Owner != OwnerAlarmd {
		t.Errorf("overdue object with a backend reason is under %s / %s, want SLOTS_OVERDUE / ALARMD",
			list[0].Finding.Check, list[0].Finding.Owner)
	}
	// The same object, late but within its period, is still the backend's.
	list[0].Wake.DueAt = at.Add(-10 * time.Second)
	Attribute(list, at)
	if list[0].Finding.Check != CheckBackendNotAnswering {
		t.Errorf("late-but-within-period object is under %s, want BACKEND_NOT_ANSWERING", list[0].Finding.Check)
	}
}

// A retained record is one of three things, decided from what the view
// knows about the object: a demoted object's record is the consequence of
// the line it is under and rides on that object's row; a record made within
// the window is a loss in progress, current, on the record line; a record
// older than the window is a loss that stopped, retained on the same line.
// A live page filed all of them as this deployment giving up for want of
// capacity, when most were the backend refusing and no capacity changes
// that. An object already under the same check from its current round is
// counted once; an object under a different check gets its record row as
// well, because those are two facts.
func TestRetainedRecordsAreConsequenceOngoingOrHistory(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	current := Anomaly{QueryGroup: "qg-skipping-now", Replica: "pod-a", Kind: KindDegradedRun,
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "GAP_SKIPPED"}
	backend := Anomaly{QueryGroup: "qg-backend", Replica: "pod-a", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT"}
	anomalies := []Anomaly{current, backend}
	// In the demoted pool on a refusal: its record is the refusal's consequence.
	demoted := []Anomaly{{QueryGroup: "qg-refused", Replica: "pod-b", Kind: KindQueryCooldown,
		Failure:    &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"},
		Strategies: []StrategyRef{{StrategyID: "2864", BusinessID: "7"}}}}
	Attribute(anomalies, at)
	Attribute(demoted, at)
	view := &View{Anomalies: anomalies, Demoted: demoted,
		GapSkips: map[string]SkippedSpan{
			// Skipping now and also retained: one object, one row.
			"qg-skipping-now": {FirstSlot: 100, LastSlot: 220, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a"},
			// Healthy now, skipped an hour ago: history, from the record alone.
			"qg-skipped-earlier": {FirstSlot: 1000, LastSlot: 1060, Slots: 2, At: at.Add(-time.Hour), Replica: "pod-b"},
			// Healthy now, skipped two minutes ago: a loss in progress.
			"qg-losing-now": {FirstSlot: 3000, LastSlot: 3010, Slots: 1, At: at.Add(-2 * time.Minute), Replica: "pod-b"},
			// Under the backend's line now (not demoted), and skipped earlier: both.
			"qg-backend": {FirstSlot: 2000, LastSlot: 2000, Slots: 1, At: at.Add(-30 * time.Minute), Replica: "pod-a"},
			// Demoted on a refusal, skipped three minutes ago while there.
			"qg-refused": {FirstSlot: 4000, LastSlot: 4120, Slots: 3, At: at.Add(-3 * time.Minute), Replica: "pod-b"},
		},
		PrunedSkips: map[string]PrunedSkip{
			"qg-pruned": {From: 5000, To: 8600, At: at.Add(-2 * time.Hour), Replica: "pod-b"},
		}}
	reports := ReportChecks([][]Anomaly{anomalies, demoted, nil, nil}, nil, view, at)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	abandoned := byCode[CheckDetectionAbandoned]
	if abandoned.Objects != 4 || abandoned.Current != 2 || abandoned.Retained != 2 {
		t.Errorf("DETECTION_ABANDONED = %+v, want 4 objects: the current skipper and the loss in progress current, "+
			"the earlier one and the backend's object retained; the demoted object's record is not here", abandoned)
	}
	if abandoned.GroupBy != GroupByLoss {
		t.Errorf("DETECTION_ABANDONED folds on %s, want %s", abandoned.GroupBy, GroupByLoss)
	}
	groups := map[string]int{}
	for _, group := range abandoned.Groups {
		groups[group.Key] = group.Objects
	}
	if groups[string(LossOngoing)] != 1 || groups[string(LossHistorical)] != 2 || groups["GAP_SKIPPED"] != 1 {
		t.Errorf("DETECTION_ABANDONED groups = %v, want ONGOING 1, HISTORICAL 2, and the current skipper on its code", groups)
	}
	if got := byCode[CheckTimelinePruned]; got.Objects != 1 || got.Retained != 1 || got.Groups[0].Key != string(LossHistorical) {
		t.Errorf("TIMELINE_PRUNED = %+v, want the one pruned record, retained, folded HISTORICAL", got)
	}
	if got := byCode[CheckBackendNotAnswering]; got.Objects != 1 || got.Consequence != nil {
		t.Errorf("BACKEND_NOT_ANSWERING = %+v, want 1 object and no consequence: it is not demoted, its record is a row", got)
	}
	refused := byCode[CheckQueryTargetMissing]
	if refused.Objects != 1 || refused.Consequence == nil || refused.Consequence.Skipped != 1 ||
		refused.Consequence.SkippedRecent != 1 || refused.Consequence.SkippedNewest == nil ||
		!refused.Consequence.SkippedNewest.Equal(at.Add(-3*time.Minute)) {
		t.Errorf("QUERY_TARGET_MISSING = %+v, want its one object with the consequence: 1 skipped, 1 within the window, newest 3 minutes ago", refused)
	}

	rows := UnderCheck(CheckDetectionAbandoned, "", view, at)
	if len(rows) != 4 {
		t.Fatalf("under DETECTION_ABANDONED: %v, want 4 rows", names(rows))
	}
	kinds := map[string]string{}
	losses := map[string]Loss{}
	for _, row := range rows {
		kinds[row.QueryGroup], losses[row.QueryGroup] = row.Kind, row.Loss
	}
	if kinds["qg-skipping-now"] != KindDegradedRun {
		t.Errorf("the current skipper is listed as %s, want its own row, not a synthesized one", kinds["qg-skipping-now"])
	}
	if losses["qg-losing-now"] != LossOngoing || losses["qg-skipped-earlier"] != LossHistorical || losses["qg-backend"] != LossHistorical {
		t.Errorf("record rows carry %v, want ONGOING for the loss in progress and HISTORICAL for the stopped ones", losses)
	}
	for _, row := range rows {
		if row.Kind == KindSkippedSpan && (row.Skip == nil || row.Skip.Slots == 0 || row.Finding.Group != string(row.Loss)) {
			t.Errorf("synthesized row %s = %+v, want the span and its loss as its group", row.QueryGroup, row)
		}
	}
	// Narrowing to the loss in progress lists it alone; to history, the two.
	if got := UnderCheck(CheckDetectionAbandoned, string(LossOngoing), view, at); len(got) != 1 || got[0].QueryGroup != "qg-losing-now" {
		t.Errorf("under DETECTION_ABANDONED group ONGOING = %v, want [qg-losing-now]", names(got))
	}
	if got := UnderCheck(CheckDetectionAbandoned, string(LossHistorical), view, at); len(got) != 2 {
		t.Errorf("under DETECTION_ABANDONED group HISTORICAL = %v, want the two stopped losses", names(got))
	}
	// The demoted object's row, under its own line, carries what it lost there.
	refusedRows := UnderCheck(CheckQueryTargetMissing, "", view, at)
	if len(refusedRows) != 1 || refusedRows[0].Skip == nil || refusedRows[0].Skip.Slots != 3 || refusedRows[0].Loss != LossWhileDemoted {
		t.Errorf("under QUERY_TARGET_MISSING = %+v, want the object's row with its record and WHILE_DEMOTED", refusedRows)
	}
	// The pruned record's row says its Slot count is not knowable.
	pruned := UnderCheck(CheckTimelinePruned, "", view, at)
	if len(pruned) != 1 || pruned[0].Skip == nil || pruned[0].Skip.Slots != 0 || pruned[0].Skip.FirstSlot != 5000 {
		t.Errorf("under TIMELINE_PRUNED = %+v, want one row with the span and no Slot count", pruned)
	}
}

// The record of a demoted object is its line's consequence only while the
// object is under a line; the tracker never produces a demoted object under
// none, and if one arrived its record would be read by age like any other
// rather than counted on a line that does not exist.
func TestADemotedObjectUnderNoLineIsReadByAge(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	view := &View{Demoted: []Anomaly{{QueryGroup: "qg-orphan", Kind: KindQueryCooldown}},
		GapSkips: map[string]SkippedSpan{"qg-orphan": {FirstSlot: 1, LastSlot: 2, Slots: 2, At: at.Add(-time.Minute), Replica: "pod-a"}}}
	reports := ReportChecks([][]Anomaly{nil, view.Demoted, nil, nil}, nil, view, at)
	if len(reports) != 1 || reports[0].Code != CheckDetectionAbandoned || reports[0].Current != 1 || reports[0].Consequence != nil {
		t.Fatalf("reports = %+v, want the record current on DETECTION_ABANDONED and no consequence anywhere", reports)
	}
}

// Objects whose data stopped are on the data side's line and listed under it,
// from the view's own list rather than from a column: their rounds complete
// and the equation counts them healthy.
func TestNoDataObjectsAreOnTheDataSidesLine(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	view := &View{NoData: []Anomaly{
		{QueryGroup: "qg-stopped-a", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
			Since: at.Add(-time.Hour), Strategies: []StrategyRef{{StrategyID: "77", BusinessID: "3"}}},
		{QueryGroup: "qg-stopped-b", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-b",
			Since: at.Add(-2 * time.Hour), Strategies: []StrategyRef{{StrategyID: "77", BusinessID: "3"}}},
	}}
	Attribute(view.NoData, at)
	reports := ReportChecks([][]Anomaly{nil, nil, nil, nil}, nil, view, now)
	if len(reports) != 1 || reports[0].Code != CheckNoDataPersistent || reports[0].Owner != OwnerData ||
		reports[0].Objects != 2 || reports[0].Strategies != 1 {
		t.Fatalf("reports = %+v, want one NO_DATA_PERSISTENT line, the data side's, over 2 objects of 1 strategy", reports)
	}
	if len(reports[0].Groups) != 1 || reports[0].Groups[0].Key != "77" {
		t.Errorf("groups = %+v, want one fold on strategy 77", reports[0].Groups)
	}
	rows := UnderCheck(CheckNoDataPersistent, "77", view, now)
	if len(rows) != 2 || rows[0].QueryGroup != "qg-stopped-b" {
		t.Errorf("under NO_DATA_PERSISTENT group 77 = %v, want both, oldest first", names(rows))
	}
	for _, row := range rows {
		if row.Finding.Result != ResultNoData || row.Attribution != AttributionExternal {
			t.Errorf("row %s = result %s / attribution %s, want NO_DATA / EXTERNAL", row.QueryGroup, row.Finding.Result, row.Attribution)
		}
	}
}

// A line splits what is wrong now from what was lost in the past and kept,
// and says how many of its objects the deployment already stopped querying;
// the first screen's arithmetic counts lines with something on them now and
// distinct objects, with the record apart. Adding every line's objects up
// read as "需要处理 768 个对象" on a deployment with a few dozen wrong: past
// records counted as work, and an object under two lines twice.
func TestReportsSplitCurrentFromRetainedAndTheTodoCountsDistinctObjects(t *testing.T) {
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	anomalies := []Anomaly{{QueryGroup: "qg-a", Kind: KindOverdueWake, Strategies: []StrategyRef{{StrategyID: "1", BusinessID: "7"}}}}
	demoted := []Anomaly{{QueryGroup: "qg-b", Kind: KindQueryCooldown,
		Failure:    &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"},
		Strategies: []StrategyRef{{StrategyID: "2", BusinessID: "7"}}}}
	// The same object under a second line: one object, not two.
	undecidable := []Anomaly{{QueryGroup: "qg-a", Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING",
		Coverage: &HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40}}}
	Attribute(anomalies, at)
	Attribute(demoted, at)
	Attribute(undecidable, at)
	view := &View{Unknown: 2, GapSkips: map[string]SkippedSpan{
		"qg-c": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-30 * time.Minute), Replica: "pod-a"},
		"qg-d": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-3 * time.Hour), Replica: "pod-a"},
	}}
	columns := [][]Anomaly{anomalies, demoted, undecidable, nil}
	reports := ReportChecks(columns, nil, view, at)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	abandoned := byCode[CheckDetectionAbandoned]
	if abandoned.Objects != 2 || abandoned.Current != 0 || abandoned.Retained != 2 || abandoned.RetainedLastHour != 1 ||
		abandoned.RetainedNewest == nil || !abandoned.RetainedNewest.Equal(at.Add(-30*time.Minute)) {
		t.Fatalf("DETECTION_ABANDONED = %+v, want 2 objects all retained, 1 in the last hour, newest half an hour ago", abandoned)
	}
	if gap := byCode[CheckObservationGap]; gap.Current != 2 || gap.Objects != 2 {
		t.Fatalf("OBSERVATION_GAP = %+v, want the 2 undetermined objects current", gap)
	}
	if target := byCode[CheckQueryTargetMissing]; target.Current != 1 || target.Demoted != 1 || target.Owner != OwnerStrategy {
		t.Fatalf("QUERY_TARGET_MISSING = %+v, want 1 current, 1 demoted, the strategy's", target)
	}
	if overdue := byCode[CheckSlotsOverdue]; overdue.Current != 1 || overdue.Demoted != 0 {
		t.Fatalf("SLOTS_OVERDUE = %+v, want 1 current, none demoted", overdue)
	}

	todo := SummarizeTodo(reports, columns, view, at)
	// Lines with something on them now, confirmed this deployment's:
	// SLOTS_OVERDUE and OBSERVATION_GAP; DETECTION_ABANDONED has only the
	// record. Objects: qg-a once, plus the two unknown. WINDOW_UNDECIDED is
	// the one line nobody can hand to anyone, with qg-a under it too -- a
	// third part, not folded into either of the other two.
	if todo.Checks != 2 || todo.Objects != 3 {
		t.Fatalf("todo = %+v, want 2 lines confirmed ours and 3 distinct objects (qg-a once, plus 2 unknown)", todo)
	}
	if todo.Undetermined != 1 || todo.UndeterminedObjects != 1 {
		t.Fatalf("todo undetermined = %+v, want the one undecided line with its one object", todo)
	}
	if todo.Retained != 2 || todo.RetainedLastHour != 1 || todo.RetainedNewest == nil || !todo.RetainedNewest.Equal(at.Add(-30*time.Minute)) {
		t.Fatalf("todo record = %+v, want 2 retained, 1 in the last hour, newest half an hour ago", todo)
	}
	if todo.Ongoing != 0 || todo.WhileDemoted != 0 || todo.RecentWindowSeconds != int(RecentSkipWindow/time.Second) {
		t.Fatalf("todo loss = %+v, want nothing in progress, nothing demoted, and the window it was decided on", todo)
	}
	if todo.Governance != 1 || todo.GovernanceObjects != 1 {
		t.Fatalf("todo governance = %+v, want the one strategy-side line with its one object", todo)
	}
	// A standing counts as a line to act on though it has no objects.
	standing := &View{Activation: &ActivationFacts{Behind: true, BehindBeyondBound: true}, ActivationReplica: "pod-a"}
	if only := SummarizeTodo(ReportChecks(nil, nil, standing, at), nil, standing, at); only.Checks != 1 || only.Objects != 0 {
		t.Fatalf("todo with a standing only = %+v, want 1 line, 0 objects", only)
	}
}

// The record of past loss opens newest first: what a reader can act on is
// who was just lost and which span, not the oldest entry of a list that
// only grows.
func TestRetainedLossOpensNewestFirst(t *testing.T) {
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	view := &View{GapSkips: map[string]SkippedSpan{
		"qg-old": {FirstSlot: 1, LastSlot: 2, Slots: 2, At: at.Add(-3 * time.Hour), Replica: "pod-a"},
		"qg-new": {FirstSlot: 8, LastSlot: 9, Slots: 2, At: at.Add(-2 * time.Minute), Replica: "pod-a"},
		"qg-mid": {FirstSlot: 4, LastSlot: 5, Slots: 2, At: at.Add(-time.Hour), Replica: "pod-a"},
	}}
	rows := UnderCheck(CheckDetectionAbandoned, "", view, now)
	if len(rows) != 3 || rows[0].QueryGroup != "qg-new" || rows[1].QueryGroup != "qg-mid" || rows[2].QueryGroup != "qg-old" {
		names := make([]string, 0, len(rows))
		for _, row := range rows {
			names = append(names, row.QueryGroup)
		}
		t.Fatalf("DETECTION_ABANDONED opens %v, want newest first", names)
	}
	if rows[0].Skip == nil || rows[0].Skip.FirstSlot != 8 {
		t.Fatalf("the newest row does not carry its span: %+v", rows[0].Skip)
	}
}

// A row built from a retained record carries the record's strategies, and
// the line's fold counts them: a record nobody can trace to a strategy is a
// record nobody can act on, and it rendered with an empty strategy column.
func TestRetainedRecordsCarryTheirStrategiesOntoRowsAndFolds(t *testing.T) {
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	view := &View{
		GapSkips: map[string]SkippedSpan{"qg-gap": {FirstSlot: 1, LastSlot: 2, Slots: 2, At: at.Add(-time.Minute), Replica: "pod-a",
			Strategies: []StrategyRef{{StrategyID: "1854", BusinessID: "7"}}}},
		PrunedSkips: map[string]PrunedSkip{"qg-pruned": {From: 1, To: 900, At: at.Add(-time.Hour), Replica: "pod-a",
			Strategies: []StrategyRef{{StrategyID: "2001", BusinessID: "9"}}}},
	}
	for check, want := range map[Check]string{CheckDetectionAbandoned: "1854", CheckTimelinePruned: "2001"} {
		rows := UnderCheck(check, "", view, now)
		if len(rows) != 1 || len(rows[0].Strategies) != 1 || rows[0].Strategies[0].StrategyID != want {
			t.Fatalf("%s rows = %+v, want one row naming strategy %s", check, rows, want)
		}
	}
	for _, report := range ReportChecks(nil, nil, view, at) {
		if report.Strategies != 1 || report.Businesses != 1 || len(report.Groups) != 1 || report.Groups[0].Strategies != 1 {
			t.Fatalf("%s = %+v, want one strategy and one business counted on the line and its fold", report.Code, report)
		}
	}
}

// The first screen's arithmetic tells a loss in progress from a record of a
// loss that stopped, and both from a demoted object's record, which is its
// line's consequence. "曾经漏检、不用处理" over a record that was still
// growing is what this exists for: a loss in progress is this deployment's,
// current, and its object counts as work; the record is only the stopped
// ones; and the window all of it was decided on travels with the numbers.
func TestTheTodoTellsLossInProgressFromTheRecord(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	demoted := []Anomaly{{QueryGroup: "qg-refused", Kind: KindQueryCooldown,
		Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}}}
	Attribute(demoted, at)
	view := &View{Demoted: demoted, GapSkips: map[string]SkippedSpan{
		"qg-refused":  {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a"},
		"qg-losing-a": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-2 * time.Minute), Replica: "pod-a"},
		"qg-losing-b": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-9 * time.Minute), Replica: "pod-a"},
		"qg-stopped":  {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-11 * time.Minute), Replica: "pod-a"},
		"qg-old":      {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-3 * time.Hour), Replica: "pod-a"},
	}}
	columns := [][]Anomaly{nil, demoted, nil, nil}
	reports := ReportChecks(columns, nil, view, at)
	todo := SummarizeTodo(reports, columns, view, at)
	if todo.Ongoing != 2 || todo.OngoingNewest == nil || !todo.OngoingNewest.Equal(at.Add(-2*time.Minute)) {
		t.Fatalf("todo ongoing = %+v, want the two records within the window, newest two minutes ago", todo)
	}
	if todo.Checks != 1 || todo.Objects != 2 {
		t.Fatalf("todo = %+v, want DETECTION_ABANDONED up as ours with the two objects losing rounds now", todo)
	}
	if todo.Retained != 2 || todo.RetainedLastHour != 1 || todo.RetainedNewest == nil || !todo.RetainedNewest.Equal(at.Add(-11*time.Minute)) {
		t.Fatalf("todo record = %+v, want the two stopped losses, one within the hour, newest eleven minutes ago", todo)
	}
	if todo.WhileDemoted != 1 || todo.WhileDemotedRecent != 1 {
		t.Fatalf("todo while demoted = %+v, want the refused object's record counted as its line's consequence", todo)
	}
	if todo.Governance != 1 || todo.GovernanceObjects != 1 {
		t.Fatalf("todo governance = %+v, want the refusal line with its object", todo)
	}
	// Eleven minutes on, nothing is in progress: the same records read as
	// the record, and the objects are no longer work.
	later := SummarizeTodo(ReportChecks(columns, nil, view, at.Add(11*time.Minute)), columns, view, at.Add(11*time.Minute))
	if later.Ongoing != 0 || later.Objects != 0 || later.Checks != 0 || later.Retained != 4 {
		t.Fatalf("todo eleven minutes later = %+v, want nothing in progress and four on record", later)
	}
}

// A demoted object's record is the cooldown's consequence only if it was
// made after the object's anomaly began: an object that lost rounds to the
// replay bound before it entered the pool has an older record, and folding
// that into the refusal would hide a loss the refusal did not cause. The
// onset is the bound; a record made after it is folded.
func TestARecordOlderThanTheDemotionIsNotItsConsequence(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	refused := func(queryGroup string, since time.Time) Anomaly {
		return Anomaly{QueryGroup: queryGroup, Kind: KindQueryCooldown, Since: since,
			Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}}
	}
	// Anomalous since an hour ago; one record from two hours ago, one from a
	// minute ago.
	demoted := []Anomaly{refused("qg-before", at.Add(-time.Hour)), refused("qg-after", at.Add(-time.Hour))}
	Attribute(demoted, at)
	view := &View{Demoted: demoted, GapSkips: map[string]SkippedSpan{
		"qg-before": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-2 * time.Hour), Replica: "pod-a"},
		"qg-after":  {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-time.Minute), Replica: "pod-a"},
	}}
	columns := [][]Anomaly{nil, demoted, nil, nil}
	byCode := map[Check]CheckReport{}
	for _, report := range ReportChecks(columns, nil, view, at) {
		byCode[report.Code] = report
	}
	target := byCode[CheckQueryTargetMissing]
	if target.Consequence == nil || target.Consequence.Skipped != 1 {
		t.Fatalf("QUERY_TARGET_MISSING = %+v, want exactly the record made after the onset as its consequence", target)
	}
	abandoned := byCode[CheckDetectionAbandoned]
	if abandoned.Retained != 1 || abandoned.Current != 0 {
		t.Fatalf("DETECTION_ABANDONED = %+v, want the older record on it as history, read by its age", abandoned)
	}
	rows := UnderCheck(CheckQueryTargetMissing, "", view, at)
	carried := map[string]bool{}
	for _, row := range rows {
		carried[row.QueryGroup] = row.Skip != nil
	}
	if carried["qg-before"] || !carried["qg-after"] {
		t.Fatalf("rows carry records %v, want only the object whose record post-dates its onset", carried)
	}
}

// Where the pooled row carries its pool entry, that is the bound a record is
// read against, not the anomaly's earlier onset: a record made after the
// failures began and before the pool was entered is the replay bound's
// doing, not the cooldown's.
func TestTheRecordBoundIsThePoolEntryWhereTheRowCarriesIt(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	refused := Anomaly{QueryGroup: "qg-refused", Kind: KindQueryCooldown, Since: at.Add(-time.Hour), DemotedSince: at.Add(-10 * time.Minute),
		Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}}
	demoted := []Anomaly{refused}
	Attribute(demoted, at)
	// Skipped twenty minutes ago: after the onset, before the entry.
	view := &View{Demoted: demoted, GapSkips: map[string]SkippedSpan{
		"qg-refused": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-20 * time.Minute), Replica: "pod-a"}}}
	columns := [][]Anomaly{nil, demoted, nil, nil}
	byCode := map[Check]CheckReport{}
	for _, report := range ReportChecks(columns, nil, view, at) {
		byCode[report.Code] = report
	}
	if byCode[CheckQueryTargetMissing].Consequence != nil {
		t.Fatalf("QUERY_TARGET_MISSING = %+v, want no consequence: the record predates the pool entry", byCode[CheckQueryTargetMissing])
	}
	if abandoned := byCode[CheckDetectionAbandoned]; abandoned.Retained != 1 {
		t.Fatalf("DETECTION_ABANDONED = %+v, want the record on it, read by its age", abandoned)
	}
	// The same record made after the entry is the consequence.
	view.GapSkips["qg-refused"] = SkippedSpan{FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-5 * time.Minute), Replica: "pod-a"}
	byCode = map[Check]CheckReport{}
	for _, report := range ReportChecks(columns, nil, view, at) {
		byCode[report.Code] = report
	}
	if byCode[CheckQueryTargetMissing].Consequence == nil || byCode[CheckQueryTargetMissing].Consequence.Skipped != 1 {
		t.Fatalf("QUERY_TARGET_MISSING = %+v, want the record made after the entry as its consequence", byCode[CheckQueryTargetMissing])
	}
}

// A record made within the grace after its replica's start is the restart's
// catch-up: current, on the line and in the arithmetic as its own kind, and
// not what asks the capacity question. A live rollout skipped a hundred and
// sixty short-period objects in its first minute, and for the next ten the
// page read "正在漏检" with no mechanism named. Unknown start: not the
// restart's; outside the grace: not the restart's; older than the window:
// history whatever the start.
func TestARecordInTheRestartGraceIsTheRestartsCatchUp(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	started := at.Add(-4 * time.Minute)
	view := &View{
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: started}, {Replica: "pod-b"}},
		GapSkips: map[string]SkippedSpan{
			// pod-a started four minutes ago; skipped three minutes ago: the restart's.
			"qg-catching-up": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-3 * time.Minute), Replica: "pod-a"},
			// pod-a, skipped before it started (a record it inherited): not the restart's.
			"qg-before-start": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-6 * time.Minute), Replica: "pod-a"},
			// pod-b published no start: the grace cannot be read, so not the restart's.
			"qg-no-start": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: at.Add(-3 * time.Minute), Replica: "pod-b"},
		}}
	columns := [][]Anomaly{nil, nil, nil, nil}
	reports := ReportChecks(columns, nil, view, at)
	abandoned := reports[0]
	if abandoned.Code != CheckDetectionAbandoned || abandoned.Current != 3 || abandoned.Retained != 0 {
		t.Fatalf("DETECTION_ABANDONED = %+v, want all three current", abandoned)
	}
	groups := map[string]int{}
	for _, group := range abandoned.Groups {
		groups[group.Key] = group.Objects
	}
	if groups[string(LossAfterRestart)] != 1 || groups[string(LossOngoing)] != 2 {
		t.Fatalf("groups = %v, want one AFTER_RESTART and two ONGOING", groups)
	}
	todo := SummarizeTodo(reports, columns, view, at)
	if todo.AfterRestart != 1 || todo.Ongoing != 2 || todo.Objects != 3 || todo.RestartGraceSeconds != int(RestartCatchUpGrace/time.Second) {
		t.Fatalf("todo = %+v, want 1 after restart, 2 ongoing, 3 objects, the grace named", todo)
	}
	// The restart's catch-up alone asks no capacity question; a loss by any
	// other mechanism does.
	view.GapSkips = map[string]SkippedSpan{"qg-catching-up": view.GapSkips["qg-catching-up"]}
	view.Capacity = &CapacityView{PermitAcquires: 1000, PermitWaits: 800}
	view.Schedule = &ScheduleCensus{Completed1h: 100, OnTime1h: 100}
	load := LoadOf(view, at)
	if load.Loss.State != LossInProgress || load.Loss.AfterRestart != 1 || load.Loss.Ongoing != 0 || load.Bottleneck.Resource != BottleneckNone {
		t.Fatalf("load = %+v, want the restart's loss in progress and no bottleneck asked", load)
	}
	// The grace is about when the skip happened, not about now: a record
	// made in the first minutes stays the restart's until the window ages it
	// into history, and one made seven minutes after the start was never
	// the restart's.
	view.GapSkips = map[string]SkippedSpan{
		"qg-catching-up": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: started.Add(time.Minute), Replica: "pod-a"},
		"qg-later":       {FirstSlot: 1, LastSlot: 3, Slots: 3, At: started.Add(7 * time.Minute), Replica: "pod-a"},
	}
	groups = map[string]int{}
	for _, group := range ReportChecks(columns, nil, view, started.Add(9*time.Minute))[0].Groups {
		groups[group.Key] = group.Objects
	}
	if groups[string(LossAfterRestart)] != 1 || groups[string(LossOngoing)] != 1 {
		t.Fatalf("groups nine minutes after the start = %v, want the first-minute record AFTER_RESTART and the seventh-minute one ONGOING", groups)
	}
}

// On the real path the replica's start reaches the view from its snapshot,
// and a record made in the first minute after it folds as the restart's.
// Built through Aggregate, so a view that dropped the start would fail here
// while a view assembled by hand could not.
func TestTheReplicaStartReachesTheViewFromItsSnapshot(t *testing.T) {
	started := now.Add(-3 * time.Minute)
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 2, Determined: 2, StartedAt: started,
			GapSkips: map[string]SkippedSpan{"qg-catching-up": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: started.Add(time.Minute), Replica: "pod-a"}}},
	}
	view := Aggregate(Expectation{Known: true, QueryGroups: 2}, snapshots, []string{"pod-a"}, now, freshness)
	if len(view.PerReplica) != 1 || !view.PerReplica[0].StartedAt.Equal(started) {
		t.Fatalf("per_replica = %+v, want pod-a's start carried from its snapshot", view.PerReplica)
	}
	reports := ReportChecks(nil, nil, &view, now)
	if len(reports) != 1 || len(reports[0].Groups) != 1 || reports[0].Groups[0].Key != string(LossAfterRestart) {
		t.Fatalf("reports = %+v, want the record folded as the restart's catch-up", reports)
	}
}
