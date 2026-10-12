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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// One reason code, several lines, several owners -- decided on the counts.
//
// Every row here carries HISTORY_WARMING or HISTORY_GAPPED and nothing else
// that a code table could read, so the only thing separating them is the
// coverage. The page used to send all of them to the strategy owner on the
// strength of the code; this is the table that says where each one actually
// goes -- which line, or none -- and it is the table a reader should be able
// to check a row against.
func TestAWindowReasonIsDecidedOnItsCountsNotItsCode(t *testing.T) {
	warming := func(coverage HistoryCoverage) Anomaly {
		return Anomaly{QueryGroup: "qg", Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN",
			CauseReason: "HISTORY_WARMING", Coverage: &coverage}
	}
	gapped := func(coverage HistoryCoverage) Anomaly {
		return Anomaly{QueryGroup: "qg", Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN",
			CauseReason: "HISTORY_GAPPED", Coverage: &coverage}
	}
	for _, testCase := range []struct {
		name    string
		anomaly Anomaly
		check   Check // empty: under no line
		owner   Owner
	}{
		// A window still filling: nobody's, heals.
		{"young", warming(HistoryCoverage{Levels: 3, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 2}),
			"", OwnerNobody},
		// Every short window a new series, for longer than a window takes to
		// fill: the strategy's dimensions.
		{"churning", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 4, ShortFresh: 4, FreshRounds: 40}),
			CheckSeriesChurning, OwnerStrategy},
		// Same shortfall, same run, every short window a series with history:
		// the data.
		{"data missing", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40}),
			CheckSeriesDataMissing, OwnerUndetermined},
		// Some fresh, some not: cannot be handed to either.
		{"mixed", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 2, ShortFresh: 2, FreshRounds: 0}),
			CheckWindowUndecided, OwnerUndetermined},
		// Every short window fresh, for one round: a strategy edit looks like
		// this. Wait.
		{"renewed", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 9, ShortFresh: 4, FreshRounds: 1}),
			"", OwnerNobody},
		// Nothing in the window at all, sustained: every round's record arrived
		// and could not be used. Whose that is, the counts do not say.
		{"empty", warming(HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14,
			ShortRounds: 40, EmptyRounds: 40, Unusable: 2, UnusableReason: "REQUIRED_VALUE_MISSING"}),
			CheckWindowUndecided, OwnerUndetermined},
		// Complete under a reason that says otherwise: the verdict is held
		// over and releases itself.
		{"held", gapped(HistoryCoverage{Levels: 3, Guarded: 3}), "", OwnerNobody},
		// Holes, sustained past the window: data arriving with holes in it.
		{"intermittent", gapped(HistoryCoverage{Levels: 1, Short: 1, WorstValid: 5, WorstRequired: 9,
			ShortRounds: 29}),
			CheckSeriesDataMissing, OwnerUndetermined},
		// Holes, not yet past the window: could be data that just stopped.
		{"just gapped", gapped(HistoryCoverage{Levels: 1, Short: 1, WorstValid: 5, WorstRequired: 9,
			ShortRounds: 3}),
			"", OwnerNobody},
		// The reason without any counts -- an older replica -- falls back to
		// the coarse reading, which stays undetermined rather than guessing.
		{"no counts", Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING"},
			CheckWindowUndecided, OwnerUndetermined},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			list := []Anomaly{testCase.anomaly}
			Attribute(list, now)
			if list[0].Finding.Check != testCase.check {
				t.Errorf("check = %q, want %q", list[0].Finding.Check, testCase.check)
			}
			if list[0].Finding.Owner != testCase.owner {
				t.Errorf("owner = %s, want %s", list[0].Finding.Owner, testCase.owner)
			}
		})
	}
}

// What the control plane files as one disposition, this table must not split
// across two owners -- checked against the control plane, over every code.
//
// The first version of this named three codes and pinned their owner. That
// held the fleet side and nothing else: a fourth code added to the catalog's
// UNSUPPORTED case and mapped here to alarmd's passed it, and a code moved out
// of that case with this table unchanged passed it too, while the comment
// above it promised the opposite. The relationship was enumerated in two
// packages and guarded in one, which is the shape of the defect it was written
// for, one layer up.
//
// So this walks the whole reason catalogue instead. For every code the
// compiler can return as a terminal, the catalog says which disposition it is
// and this table says who owns an object carrying it; within one disposition
// the owners have to agree. A new code needs no one to remember this list.
//
// What it guards, exactly: that a reader is never sent to two different owners
// for one disposition. It does not guard the catalog's own assignment of codes
// to dispositions -- a code moved between two dispositions whose codes all
// resolve to the same owner passes here, and should, because nothing the
// reader is told has changed. The first run of this found PROJECTION_INVALID
// filed with the state defects while the catalog files it as a config
// rejection; the three-code list before it could not have.
func TestCodesTheCatalogFilesTogetherShareAnOwner(t *testing.T) {
	type reading struct {
		code  string
		owner Owner
	}
	byDisposition := map[controlplane.Disposition][]reading{}
	for _, definition := range contract.ReasonCatalogV2() {
		disposition, filed := controlplane.CompilerTerminalDisposition(definition.Code)
		if !filed {
			continue
		}
		verdict, mapped := codeChecks[definition.Code]
		if !mapped || verdict.normal {
			t.Errorf("%s is a compiler terminal the catalog files as %s and this table maps to "+
				"no line", definition.Code, disposition)
			continue
		}
		byDisposition[disposition] = append(byDisposition[disposition],
			reading{code: definition.Code, owner: checkAnswers[verdict.check].Owner})
	}
	if len(byDisposition) < 2 {
		t.Fatalf("only %d dispositions found across the catalogue; the comparison would be "+
			"vacuous", len(byDisposition))
	}
	for disposition, readings := range byDisposition {
		first := readings[0]
		for _, other := range readings[1:] {
			if other.owner != first.owner {
				t.Errorf("the catalog files %s and %s together as %s; this table sends one to %s "+
					"and the other to %s -- a reader is sent to two different people for one "+
					"disposition", first.code, other.code, disposition, first.owner, other.owner)
			}
		}
	}
	// And UNSUPPORTED in particular is the strategy's. The catalog's word for it
	// is "this definition cannot run as written in this build", which no
	// deployment budget changes; it is here as a named anchor so that the
	// agreement above cannot be satisfied by every code in the group being
	// wrong the same way.
	for _, entry := range byDisposition[controlplane.DispositionUnsupported] {
		if entry.owner != OwnerStrategy {
			t.Errorf("%s is filed UNSUPPORTED and this table makes it %s's", entry.code, entry.owner)
		}
	}
	// And CONFIG_REJECTED is the strategy's for the same reason: the definition
	// is wrong as written. The agreement check is satisfied by a whole group
	// being wrong together, and the first version of this anchored only one of
	// the two groups -- so the four config-rejected codes moved to alarmd's as a
	// block passed it.
	for _, entry := range byDisposition[controlplane.DispositionConfigRejected] {
		if entry.owner != OwnerStrategy {
			t.Errorf("%s is filed CONFIG_REJECTED and this table makes it %s's", entry.code, entry.owner)
		}
	}
	if len(byDisposition[controlplane.DispositionConfigRejected]) == 0 ||
		len(byDisposition[controlplane.DispositionUnsupported]) == 0 {
		t.Fatal("one of the two anchored dispositions has no codes; its anchor would pass vacuously")
	}
	// The run-time budgets stay this deployment's. Moving the whole budget
	// bucket to the strategy would have satisfied everything above and been
	// wrong.
	for _, code := range []string{"EXECUTION_BUDGET_EXHAUSTED", "SLOT_BUDGET_EXCEEDED", "STATE_BUDGET_EXCEEDED"} {
		if got := checkAnswers[codeChecks[code].check].Owner; got != OwnerAlarmd {
			t.Errorf("%s is %s's, want this deployment's: it is a budget allocated at run time", code, got)
		}
	}
}

// A backend that refused the query is not a backend that did not answer.
//
// Both arrive as QUERY_UNAVAILABLE and both put an object in the cooldown pool,
// and the page filed all of them as the data owner's. A live deployment held
// 350 objects there on a status saying the field did not exist -- the query
// was being read and rejected every round, which is either this deployment's
// routing or the strategy's reference, and in neither case the backend's
// availability. The detail is the only thing on the row that separates them,
// and it was being rendered as a symptom without deciding anything.
func TestARejectedQueryIsNotFiledAsTheBackendsAvailability(t *testing.T) {
	cooldown := func(detail string) Anomaly {
		return Anomaly{Kind: KindQueryCooldown, CauseReason: "QUERY_UNAVAILABLE",
			Failure: &FailureRef{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: detail}}
	}
	degraded := func(detail string) Anomaly {
		return Anomaly{Kind: KindDegradedRun, CauseReason: "QUERY_UNAVAILABLE",
			Failure: &FailureRef{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: detail}}
	}
	for _, testCase := range []struct {
		name    string
		anomaly Anomaly
		check   Check
		owner   Owner
	}{
		// The provider answered with a status that names what is missing: it
		// read the strategy's table or field and said it is not there. That
		// is the strategy's, confirmed by the backend itself -- the pool card
		// already called these strategies unusable, and the line under it
		// said 待确认 of the same objects.
		{"cooldown on a provider status naming a missing target", cooldown("response=status_space_table_id_field_is_not_exists"),
			CheckQueryTargetMissing, OwnerStrategy},
		{"degraded on a provider status naming a missing target", degraded("response=status_space_table_id_field_is_not_exists"),
			CheckQueryTargetMissing, OwnerStrategy},
		{"cooldown on a not-found status", cooldown("response=status_table_not_found"), CheckQueryTargetMissing, OwnerStrategy},
		// The same refusal named at the source: the round's own reason says
		// the target is missing, with no detail needed to read it.
		{"degraded under the target-missing word", Anomaly{Kind: KindDegradedRun, CauseReason: "QUERY_TARGET_MISSING"},
			CheckQueryTargetMissing, OwnerStrategy},
		// A status this build has no reading of: refused, and the query is
		// what was refused. It is the strategy's line: on one deployment every
		// refused query the platform's own detector sent for the same strategy
		// was refused the same way (a condition value the storage rejects, an
		// expression that does not parse). A query this deployment built wrong
		// would read here too, which only comparing the two requests tells.
		{"cooldown on an unknown provider status", cooldown("response=status_other"), CheckQueryRefused, OwnerStrategy},
		// An HTTP 4xx is the same statement in the transport's vocabulary.
		{"cooldown on a 4xx", cooldown("http_status=400"), CheckQueryRefused, OwnerStrategy},
		// A timeout or a 5xx is the backend not answering: the data's.
		{"cooldown on a timeout", cooldown("transport=timeout"), CheckBackendNotAnswering, OwnerUndetermined},
		{"degraded on a 503", degraded("http_status=503"), CheckBackendNotAnswering, OwnerUndetermined},
		// No detail at all: nothing says it was refused, so the coarse reading.
		{"cooldown without detail", Anomaly{Kind: KindQueryCooldown, CauseReason: "QUERY_UNAVAILABLE"},
			CheckBackendNotAnswering, OwnerUndetermined},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			list := []Anomaly{testCase.anomaly}
			Attribute(list, now)
			if got := list[0].Finding; got.Check != testCase.check || got.Owner != testCase.owner {
				t.Errorf("finding = %s/%s, want %s/%s", got.Check, got.Owner, testCase.check, testCase.owner)
			}
		})
	}
}

// A query timeout is read by whose time ran out. A round that failed on a
// query whose deadline passed while this deployment was still delivering
// what had arrived is its own - DEFECT, a timeout at the query step with no
// dependency - whichever word the round ended with: the failure reaches the
// row as the grammar publishes it, category other and code OTHER, and only
// its detail says where the time went. Read by the round's word it was an
// unnamed failure at no step, or from a source error a dependency down at
// the configuration step. A timeout that ran out inside a read of the body
// is the backend's, as before the answer began. A round that went on to
// complete is read by its completion, and a delivery timeout from an earlier
// Slot decides nothing for this round.
func TestADeliveryTimeoutIsThisDeploymentsNotTheBackends(t *testing.T) {
	observe := func(tracker *Tracker, at *clock, facts observability.QueryFailureFacts, runOutcome string, rounds int) {
		for round := 0; round < rounds; round++ {
			slot := int64(1000 + 60*round)
			reported := facts
			tracker.Observe(context.Background(), observability.NormalizeObservation(observability.Observation{
				Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: observability.ResultFailed,
				ReasonCode: observability.ReasonInternalUnknown, Err: errors.New("context deadline exceeded"), QueryFailure: &reported,
				Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot}}))
			at.at = at.at.Add(time.Millisecond)
			if runOutcome == "" {
				tracker.Observe(context.Background(), observability.Observation{ExecuteOutcome: "error", ReasonCode: "internal_unknown",
					Err: errors.New("alarmd worker: query: context deadline exceeded"), Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot}})
			} else {
				tracker.Observe(context.Background(), observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
					Result: observability.ResultTerminal, RunOutcome: runOutcome, Err: errors.New("alarmd worker: query: context deadline exceeded"),
					Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot}})
			}
			at.at = at.at.Add(time.Minute)
		}
	}
	// What the client reports for its own delivery; the grammar publishes the
	// code as OTHER whatever it is given, which is why the client gives OTHER.
	delivery := observability.QueryFailureFacts{Stage: "execute", Category: "other", Code: "QUERY_TIMEOUT", Detail: "delivery=timeout"}
	backend := observability.QueryFailureFacts{Stage: "execute", Category: "provider_transport", Code: "QUERY_TIMEOUT", Detail: "body=timeout"}
	for _, testCase := range []struct {
		name       string
		facts      observability.QueryFailureFacts
		runOutcome string
		check      Check
		dependency Dependency
	}{
		{"delivery ran out, execution error", delivery, "", CheckDefect, DependencyNone},
		{"delivery ran out, source error", delivery, "source_error", CheckDefect, DependencyNone},
		{"a body read ran out", backend, "", CheckBackendNotAnswering, DependencyUnlocated},
	} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		observe(tracker, at, testCase.facts, testCase.runOutcome, DefaultBlockedRounds+1)
		rows := tracker.Anomalies()
		Attribute(rows, at.at)
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %+v, want the one object", testCase.name, rows)
		}
		got := rows[0]
		if got.Failure == nil || got.Failure.Detail != testCase.facts.Detail || (testCase.facts.Category == "other" && got.Failure.Code != "OTHER") {
			t.Fatalf("%s: failure = %+v, want the reported detail, and OTHER for category other", testCase.name, got.Failure)
		}
		if got.Finding.Check != testCase.check || got.Blocked == nil || got.Blocked.Stage != StageQuery || got.Blocked.Dependency != testCase.dependency ||
			got.Blocked.Class != ClassTimeout {
			t.Errorf("%s: finding %s/%s blocked %+v, want %s, a timeout at the query step, dependency %s", testCase.name, got.Finding.Check, got.Finding.Owner, got.Blocked,
				testCase.check, testCase.dependency)
		}
	}

	failedAt := now.Add(-time.Second)
	completed := []Anomaly{{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", CauseReason: "HISTORY_GAPPED", ReasonLastAt: now, RoundSlot: 1060,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "OTHER", Detail: "delivery=timeout", At: &failedAt, Slot: 1060}}}
	Attribute(completed, now)
	if got := completed[0]; got.Finding.Check == CheckDefect || got.Blocked == nil || got.Blocked.Code != "HISTORY_GAPPED" || got.Blocked.Class == ClassTimeout {
		t.Errorf("a round that completed after a delivery timeout = %s / %+v, want its completion's cause to decide", got.Finding.Check, got.Blocked)
	}
	stale := []Anomaly{{Kind: KindBlockedRun, ReasonCode: "source_error", ReasonLastAt: now, RoundSlot: 1060,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "OTHER", Detail: "delivery=timeout", At: &failedAt, Slot: 1000}}}
	Attribute(stale, now)
	if got := stale[0]; got.Finding.Check == CheckDefect || (got.Blocked != nil && got.Blocked.Class == ClassTimeout) {
		t.Errorf("an earlier Slot's delivery timeout = %s / %+v, want it to decide nothing for this round", got.Finding.Check, got.Blocked)
	}
}

// Stalled and a missed turn are decided before any code is read.
//
// A stalled object's last code is usually the external thing that happened
// just before it got stuck, and reading that first would hand a deployment's
// own stuck round to the data owner.
func TestStalledAndMissedTurnsAreDecidedBeforeTheCode(t *testing.T) {
	stalled := []Anomaly{{Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", Stalled: true}}
	Attribute(stalled, now)
	if got := stalled[0].Finding; got.Check != CheckRoundsStalled || got.Owner != OwnerAlarmd {
		t.Errorf("stalled with an external last code = %+v, want ROUNDS_STALLED/ALARMD", got)
	}
	never := []Anomaly{{Kind: KindOverdueWake, ReasonCode: "HISTORY_WARMING"}}
	Attribute(never, now)
	if got := never[0].Finding; got.Check != CheckSlotsOverdue || got.Owner != OwnerAlarmd {
		t.Errorf("overdue wake = %+v, want SLOTS_OVERDUE/ALARMD", got)
	}
}

// The rows a check opens come from every column, oldest first.
func TestUnderCheckCrossesColumnsAndListsOldestFirst(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	mk := func(id string, check Check, since time.Time) Anomaly {
		return Anomaly{QueryGroup: id, Replica: "pod-a", Since: since, Finding: Finding{Check: check}}
	}
	// The same check in three columns, oldest first within it, and objects
	// under other checks (or none) in every column, which must not be listed.
	anomalies := []Anomaly{
		mk("stalled-new", CheckRoundsStalled, at.Add(-time.Minute)),
		mk("churn", CheckSeriesChurning, at.Add(-3*time.Hour)),
	}
	demoted := []Anomaly{
		mk("stalled-old", CheckRoundsStalled, at.Add(-2*time.Hour)),
		mk("cooldown", CheckBackendNotAnswering, at.Add(-time.Hour)),
	}
	undecidable := []Anomaly{
		mk("stalled-older", CheckRoundsStalled, at.Add(-4*time.Hour)),
		mk("young", "", at.Add(-5*time.Hour)),
	}
	view := &View{Anomalies: anomalies, Demoted: demoted, Undecidable: undecidable}
	got := UnderCheck(CheckRoundsStalled, "", view, now)
	want := []string{"stalled-older", "stalled-old", "stalled-new"}
	if len(got) != len(want) {
		t.Fatalf("under ROUNDS_STALLED = %v, want %v", names(got), want)
	}
	for index, id := range want {
		if got[index].QueryGroup != id {
			t.Errorf("under[%d] = %s, want %s (full order %v)", index, got[index].QueryGroup, id, names(got))
		}
	}
	// A group narrows to one fold and nothing else.
	demoted[0].Finding.Group = "pod-b"
	if got := UnderCheck(CheckRoundsStalled, "pod-b", view, now); len(got) != 1 ||
		got[0].QueryGroup != "stalled-old" {
		t.Errorf("under ROUNDS_STALLED group pod-b = %v, want [stalled-old]", names(got))
	}
}

func names(anomalies []Anomaly) []string {
	list := make([]string, len(anomalies))
	for index, anomaly := range anomalies {
		list[index] = anomaly.QueryGroup
	}
	return list
}

// The route serves the first screen -- the checks -- and the rows under one
// check, from every column, with a total that is the check's own.
func TestTheObjectRouteServesChecksAndTheRowsUnderOne(t *testing.T) {
	snapshots := columnSnapshots()
	ours := anomaly("qg-ours")
	ours.Kind = KindDegradedRun
	ours.CauseReason = "REDIS_UNAVAILABLE"
	churn := anomaly("qg-churn")
	churn.Kind = KindDegradedRun
	churn.Cause, churn.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
	churn.Coverage = &HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
		ShortRounds: 40, Fresh: 4, ShortFresh: 4, FreshRounds: 40}
	snapshots[1].Anomalies = []Anomaly{ours, churn}
	snapshots[1].TotalAnomalies = 2
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())

	// The checks are on every response, whichever rows it carries, and each
	// names its owner and its objects. The fixture's columns hold: a
	// dependency of ours, a churning strategy, a demoted backend on
	// QUERY_UNAVAILABLE, a HISTORY_WARMING without counts (undecided), and a
	// CONFIG_DRIFT (unresolved).
	status, body := get(t, handler, "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %v", status, body)
	}
	checks, _ := body["checks"].([]any)
	got := map[string]map[string]any{}
	order := []string{}
	for _, entry := range checks {
		report, _ := entry.(map[string]any)
		code, _ := report["code"].(string)
		got[code] = report
		order = append(order, code)
	}
	for code, want := range map[string]struct {
		owner   Owner
		objects int
	}{
		string(CheckDependencyDown):      {OwnerAlarmd, 1},
		string(CheckSeriesChurning):      {OwnerStrategy, 1},
		string(CheckBackendNotAnswering): {OwnerUndetermined, 1},
		string(CheckWindowUndecided):     {OwnerUndetermined, 1},
		string(CheckConfigUnresolved):    {OwnerUndetermined, 1},
	} {
		report := got[code]
		if report == nil {
			t.Errorf("no check %s on the response; checks = %v", code, order)
			continue
		}
		if owner, _ := report["owner"].(string); owner != string(want.owner) {
			t.Errorf("check %s owner = %q, want %s", code, owner, want.owner)
		}
		if objects, _ := report["objects"].(float64); int(objects) != want.objects {
			t.Errorf("check %s objects = %v, want %d", code, objects, want.objects)
		}
	}
	// Ours first, then undetermined, then the rest -- the order the reader acts in.
	rank := map[string]int{}
	for index, code := range order {
		rank[code] = index
	}
	if rank[string(CheckDependencyDown)] > rank[string(CheckWindowUndecided)] ||
		rank[string(CheckWindowUndecided)] > rank[string(CheckSeriesChurning)] ||
		rank[string(CheckWindowUndecided)] > rank[string(CheckBackendNotAnswering)] {
		t.Errorf("checks are ordered %v, want this deployment's own before undetermined before the others", order)
	}

	// Opening a check lists its objects from whichever column they sit in, and
	// the total is the check's count: not the column's, and not "filtered".
	status, body = get(t, handler, "/api/objects?check="+string(CheckWindowUndecided))
	if status != http.StatusOK {
		t.Fatalf("check=WINDOW_UNDECIDED: status = %d: %v", status, body)
	}
	rows, _ := body["anomalies"].([]any)
	if len(rows) != 1 {
		t.Fatalf("check=WINDOW_UNDECIDED: %d rows, want the one undecidable object", len(rows))
	}
	if row, _ := rows[0].(map[string]any); row["query_group"] != "qg-undecidable" {
		t.Errorf("check=WINDOW_UNDECIDED returned %v", row["query_group"])
	}
	if total, _ := body["anomalies_total"].(float64); int(total) != 1 {
		t.Errorf("anomalies_total = %v for a check of one object: the total has to be the check's "+
			"count, or the page reports the rest as filtered out", total)
	}
	if filtered, _ := body["filtered"].(bool); filtered {
		t.Error("opening a check reports filtered=true: the check is a line the reader opened, not a " +
			"narrowing they asked for")
	}
	if echoed, _ := body["check"].(string); echoed != string(CheckWindowUndecided) {
		t.Errorf("the response echoes check=%q, want %s", echoed, CheckWindowUndecided)
	}
	// A group within it narrows to that fold. The undecided object carries a
	// HISTORY_WARMING with no window counts at all, so its fold is the one
	// that says so.
	_, body = get(t, handler, "/api/objects?check="+string(CheckWindowUndecided)+"&group="+causeNoCounts)
	if rows, _ := body["anomalies"].([]any); len(rows) != 1 {
		t.Errorf("check=WINDOW_UNDECIDED group=%s: %d rows, want 1", causeNoCounts, len(rows))
	}
	_, body = get(t, handler, "/api/objects?check="+string(CheckWindowUndecided)+"&group=nobody")
	if rows, _ := body["anomalies"].([]any); len(rows) != 0 {
		t.Errorf("check=WINDOW_UNDECIDED group=nobody: %d rows, want 0", len(rows))
	}

	// A check nobody declared is refused, not defaulted: a typo that silently
	// matched nothing would return an empty list under a heading that says
	// "nothing under this check".
	status, _ = get(t, handler, "/api/objects?check=NOTHING_IN_PARTICULAR")
	if status != http.StatusBadRequest {
		t.Errorf("unknown check: status = %d, want 400", status)
	}
}

// A reason carried by a durable history guard is the guard's trigger, not
// this round's event. Six strategies sat under "配置状态说不清" for hours with
// CONFIG_DRIFT on every round: the guard had been established on a
// configuration change once, nothing had changed since, and two of them
// recovered with snapshot, query and schedule revisions identical before and
// after. The row's question is why the guard has not released, which is a
// window question, folded on the trigger and on whether the live window is
// still short or already full.
func TestAReasonHeldByAGuardIsAWindowQuestionNotAConfigOne(t *testing.T) {
	// consecutive is the reason clock, which the window filling does not
	// restart: on the round a guard converges it already reads the rounds
	// the window was short for. heldFull is the counter that does restart.
	held := func(short, guarded uint32, heldFull uint32) Anomaly {
		return Anomaly{Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "CONFIG_DRIFT",
			Consecutive: 29,
			Coverage: &HistoryCoverage{Levels: 3, Short: short, Guarded: guarded, WorstValid: 5, WorstRequired: 9,
				ShortRounds: 29, HeldFullRounds: heldFull}}
	}
	for name, testCase := range map[string]struct {
		anomaly Anomaly
		check   Check
		group   string
	}{
		// The live window is still short under the guard: the guard is
		// doing its job, and the row says what it is waiting on.
		"held and short": {held(1, 3, 0), CheckWindowUndecided, "保护未解除（最初触发 CONFIG_DRIFT）"},
		// The live window is full and the guard has held for more than one
		// round: the guard converges on the first full record, so this is a
		// guard that should have released.
		"held and full for two rounds": {held(0, 3, 2), CheckWindowUndecided, "保护未解除且窗口已满（最初触发 CONFIG_DRIFT）"},
		// Full for one round only -- with the reason clock at 29, as it is on
		// the real path: the round the guard converges on. Not a line.
		"held and full for one round": {held(0, 3, 1), "", ""},
		// No guard: CONFIG_DRIFT is this round's own finding and reads as
		// the configuration question it is.
		"not held": {Anomaly{Kind: KindDegradedRun, Cause: "CONFIG_DRIFT", CauseReason: "CONFIG_DRIFT",
			Coverage: &HistoryCoverage{Levels: 3}}, CheckConfigUnresolved, "847"},
	} {
		t.Run(name, func(t *testing.T) {
			item := testCase.anomaly
			item.Strategies = []StrategyRef{{StrategyID: "847", BusinessID: "7"}}
			list := []Anomaly{item}
			Attribute(list, now)
			if list[0].Finding.Check != testCase.check {
				t.Fatalf("check = %q, want %q", list[0].Finding.Check, testCase.check)
			}
			if testCase.check != "" && list[0].Finding.Group != testCase.group {
				t.Fatalf("group = %q, want %q", list[0].Finding.Group, testCase.group)
			}
			if testCase.check != "" && list[0].Finding.Owner != checkAnswers[testCase.check].Owner {
				t.Fatalf("owner = %s, want the check's", list[0].Finding.Owner)
			}
		})
	}
	// A guard under a window word of its own keeps reading the window: the
	// held-complete row of the render fixture is a normal value, as before.
	windowWord := []Anomaly{{Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "HISTORY_GAPPED",
		Consecutive: 5, Coverage: &HistoryCoverage{Levels: 3, Guarded: 3}}}
	Attribute(windowWord, now)
	if windowWord[0].Finding.Check != "" {
		t.Fatalf("a guard under HISTORY_GAPPED with a full window = %q, want no line (held, as before)", windowWord[0].Finding.Check)
	}
}

// The reading of a guard-held round is the window's, like its line. The
// object whose 249 Levels a plan-scope guard held for eighty rounds after
// one skipped Slot read WINDOW_UNDECIDED on the line and SCHEDULE / capacity
// / GAP_SKIPPED in the reading beside it -- the code table read the guard's
// trigger word as this round's event -- so the line said "wait for the
// window" and the reading sent the reader to the scheduler. A round that
// failed keeps its own reading, and so does a round under a code the table
// files as this deployment's defect.
func TestAGuardHeldRoundReadsAsTheWindowsNotAsItsTriggerWord(t *testing.T) {
	held := Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "GAP_SKIPPED",
		Consecutive: 47, RoundSlot: 1789992300,
		Coverage: &HistoryCoverage{Levels: 249, Short: 249, Guarded: 249, WorstValid: 1413, WorstRequired: 1469, ShortRounds: 47},
		Guards: []GapGuard{{Plan: StrategyRef{StrategyID: "4101", BusinessID: "7"}, Scope: "plan", Status: "WARMING", Reason: "GAP_SKIPPED",
			Required: 1469, Observed: 1353, Progress: "partial", Rounds: 48}}, GuardsTotal: 1,
		Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "7"}}}
	list := []Anomaly{held}
	Attribute(list, now)
	if list[0].Finding.Check != CheckWindowUndecided {
		t.Fatalf("check = %q, want WINDOW_UNDECIDED", list[0].Finding.Check)
	}
	if b := list[0].Blocked; b == nil || b.Stage != StageEvaluate || b.Class != ClassUnlocated || b.Dependency != DependencyNone || b.Code != "GAP_SKIPPED" {
		t.Fatalf("reading = %+v, want EVALUATE / unlocated / no dependency, with the trigger as the code", list[0].Blocked)
	}
	if list[0].Blocked.Effect != EffectUnconfirmed {
		t.Fatalf("effect = %s, want UNCONFIRMED: the round ended without a usable result", list[0].Blocked.Effect)
	}
	// The same shape with no guard: the code table's own reading of the
	// skip, as before.
	skipped := held
	skipped.Coverage, skipped.Guards, skipped.GuardsTotal = &HistoryCoverage{Levels: 249}, nil, 0
	list = []Anomaly{skipped}
	Attribute(list, now)
	if b := list[0].Blocked; b == nil || b.Stage != StageSchedule || b.Class != ClassCapacity {
		t.Fatalf("unguarded reading = %+v, want the skip's own SCHEDULE / CAPACITY", list[0].Blocked)
	}
	// A cause the table files as this deployment's defect keeps the defect's
	// line and reading whatever the guard says, as checkOf reads it first.
	defect := held
	defect.CauseReason = "STATE_READ_TIMEOUT"
	list = []Anomaly{defect}
	Attribute(list, now)
	if list[0].Finding.Check != CheckDefect || list[0].Blocked == nil || list[0].Blocked.Code != "STATE_READ_TIMEOUT" ||
		list[0].Blocked.Class == ClassUnlocated {
		t.Fatalf("defect under a guard: check %q reading %+v, want DEFECT with the defect's own reading", list[0].Finding.Check, list[0].Blocked)
	}
}

// A code the catalog files as a missing piece of the source reads as one at
// run time too: a strategy withheld for it and a Plan that met it after
// admission land on the same line and go to the same owner. Every code of
// the table is put to the catalog's own classification, so a code moved on
// one side and not the other fails here.
func TestACodeTheCatalogFilesAsSourceIncompleteLandsThereAtRunTime(t *testing.T) {
	matched := 0
	for code, verdict := range codeChecks {
		disposition, known := controlplane.CompilerTerminalDisposition(code)
		if !known || string(disposition) != dispositionSourceIncomplete {
			continue
		}
		matched++
		if verdict.check != sourceChecks[dispositionSourceIncomplete] {
			t.Errorf("%s: the catalog files it %s, the fleet lands it on %s", code, disposition, verdict.check)
		}
	}
	if matched == 0 {
		t.Fatal("no code of the table is one the catalog files as SOURCE_INCOMPLETE")
	}
}

// A terminal Slot is filed under the deterministic reason its progress record
// keeps, since it names no cause of its own; a Slot finalized
// SNAPSHOT_UNAVAILABLE reads as the snapshot that was not there, a Plan past
// its budget as the Plan, and a terminal with no reason at all stays the
// unclassified defect it is. Any other completion keeps its cause's reason
// and never borrows the observation's.
func TestATerminalSlotIsFiledUnderItsOwnReason(t *testing.T) {
	at := time.Date(2026, 9, 28, 6, 7, 35, 0, time.UTC)
	complete := func(tracker *Tracker, queryGroup, kind, cause, causeReason, reason string) Anomaly {
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
			Result: observability.ResultTerminal, ReasonCode: observability.ReasonCode(reason),
			ProgressCompletionKind: kind, ProgressCompletionCause: cause, ProgressCompletionReason: causeReason,
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: "848", BusinessID: "10", EvaluationTime: 1790424000},
		})
		for _, row := range tracker.Anomalies() {
			if row.QueryGroup == queryGroup {
				return row
			}
		}
		return Anomaly{}
	}
	tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
	for queryGroup, want := range map[string]Check{"qg-snapshot": CheckDependencyDown, "qg-budget": CheckPlanUnevaluable} {
		reason := map[string]string{"qg-snapshot": "SNAPSHOT_UNAVAILABLE", "qg-budget": "PLAN_BUDGET_EXCEEDED"}[queryGroup]
		row := complete(tracker, queryGroup, "COMPLETED_WITH_TERMINAL", "", "", reason)
		if row.CauseReason != reason {
			t.Fatalf("%s: cause reason = %q, want the terminal's own %q", queryGroup, row.CauseReason, reason)
		}
		if check, decided := codeVerdict(row); !decided || check != want {
			t.Fatalf("%s: check = %q (decided %v), want %q", queryGroup, check, decided, want)
		}
	}
	row := complete(tracker, "qg-bare", "COMPLETED_WITH_TERMINAL", "", "", string(observability.ReasonNone))
	if _, decided := codeVerdict(row); decided || row.CauseReason != "" {
		t.Fatalf("a terminal with no reason = %+v, want it left unclassified", row)
	}
	for _, degraded := range []struct{ causeReason, want string }{{"QUERY_TIMEOUT", "QUERY_TIMEOUT"}, {"", ""}} {
		tracker := NewTracker(nil, "pod-a", func() time.Time { return at })
		var row Anomaly
		for round := 0; round < DefaultDegradedRounds; round++ {
			row = complete(tracker, "qg-degraded", "COMPLETED_WITH_UNAVAILABLE", "PRIMARY_INPUT_UNAVAILABLE", degraded.causeReason, "SNAPSHOT_UNAVAILABLE")
		}
		if row.QueryGroup == "" || row.CauseReason != degraded.want {
			t.Fatalf("a degraded Slot = %+v, want its cause's reason %q and never the observation's", row, degraded.want)
		}
	}
}

// A window's verdict says whose the shortfall is only as far as its holes do:
// an empty answer is the query's, and a window with no hole on record is
// nobody's yet, not the data's by default.
func TestAWindowVerdictIsTheDatasOnlyOnMinutesAnsweredWithoutTheSeries(t *testing.T) {
	for name, tc := range map[string]struct {
		counts WindowHoleCounts
		want   WindowVerdict
	}{
		"answered without the series":  {WindowHoleCounts{AnsweredWithoutSeries: 3}, VerdictDataAbsentWhenQueried},
		"an empty answer among them":   {WindowHoleCounts{AnsweredWithoutSeries: 3, AnsweredEmpty: 1}, VerdictQueryAnsweredEmpty},
		"no hole on record":            {WindowHoleCounts{}, VerdictUnknown},
		"one round this side missed":   {WindowHoleCounts{AnsweredWithoutSeries: 3, InputIncomplete: 1}, VerdictInputIncomplete},
		"a minute not remembered":      {WindowHoleCounts{AnsweredWithoutSeries: 3, NotInMemory: 1}, VerdictUnknown},
		"a minute let go for the line": {WindowHoleCounts{AnsweredWithoutSeries: 3, HeldByLine: 1}, VerdictUnknown},
	} {
		if got := verdictOf(tc.counts); got != tc.want {
			t.Errorf("%s: verdict = %s, want %s", name, got, tc.want)
		}
	}
}

// A window line is the data's when every short window is short only by
// minutes the query answered whole -- without the series, or with nothing at
// all -- however the row came to the window line: by its own reason's counts,
// or held under a guard. One window that says anything else, a list cut short
// with nothing said about the rest, or minutes not all read, and the row stays
// where it was.
func TestAWindowLineIsTheDatasOnlyWhenEveryWindowIsSparse(t *testing.T) {
	sparse := func(missing uint32) WindowRow {
		return WindowRow{Verdict: VerdictDataAbsentWhenQueried, MissingTotal: missing, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: missing}}
	}
	gapped := func(windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED", Coverage: &HistoryCoverage{
			Levels: 4, Short: 2, WorstValid: 3, WorstRequired: 5, ShortRounds: 9, Windows: windows}}
	}
	guarded := func(windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "GAP_GUARD_WARMING", CauseReason: "GAP_GUARD_WARMING",
			Coverage: &HistoryCoverage{Levels: 4, Short: 2, WorstValid: 3, WorstRequired: 5, ShortRounds: 9, Guarded: 2, Windows: windows}}
	}
	incomplete := WindowRow{Verdict: VerdictInputIncomplete, MissingTotal: 2, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 1, InputIncomplete: 1}}
	empty := WindowRow{Verdict: VerdictQueryAnsweredEmpty, MissingTotal: 2, HolesBy: WindowHoleCounts{AnsweredEmpty: 2}}
	partlyRead := WindowRow{Verdict: VerdictDataAbsentWhenQueried, MissingTotal: 5, HolesBy: WindowHoleCounts{AnsweredWithoutSeries: 2}}
	for name, tc := range map[string]struct {
		row  Anomaly
		want Check
	}{
		"gapped, every window sparse":       {gapped(sparse(2), sparse(1)), CheckSeriesSparse},
		"gapped, one window incomplete":     {gapped(sparse(2), incomplete), CheckSeriesDataMissing},
		"gapped, one window answered empty": {gapped(sparse(2), empty), CheckSeriesSparse},
		"gapped, the list cut short":        {gapped(sparse(2)), CheckSeriesDataMissing},
		"gapped, minutes not all read":      {gapped(sparse(2), partlyRead), CheckSeriesDataMissing},
		"guarded, every window sparse":      {guarded(sparse(2), sparse(1)), CheckSeriesSparse},
		"guarded, one window incomplete":    {guarded(sparse(2), incomplete), CheckWindowUndecided},
	} {
		check, under, _ := checkOf(tc.row, ScheduleOnTime)
		if check != tc.want || !under {
			t.Errorf("%s: check = %s (under %v), want %s", name, check, under, tc.want)
		}
	}
	if answers := checkAnswers[CheckSeriesSparse]; answers.Owner != OwnerData || answers.Owner.actionRequired() {
		t.Errorf("SERIES_SPARSE is owned by %s, want the data owner and no action item for this deployment", answers.Owner)
	}
}

// A window short only at minutes its Level schedule puts outside the
// strategy's effective time is under no line, as a round outside its hours
// is, whichever window line the row reached: its own reason's counts, a guard
// it is held under, or the configuration's line a guard's stored CONFIG_DRIFT
// files it under. A hole in hours, a hole not named, or a short window not
// named, and the row keeps its line. The schedule decides, not the word: a
// hole that carries EFFECTIVE_TIME_INACTIVE without the schedule's mark (a
// fact from an older build) keeps the line, and a hole the schedule marks
// leaves it whatever its round's word.
func TestAWindowShortOnlyOutsideActiveHoursIsUnderNoLine(t *testing.T) {
	type spec struct {
		reason  string
		outside bool
	}
	window := func(by WindowHoleCounts, verdict WindowVerdict, holes ...spec) WindowRow {
		row := WindowRow{Verdict: verdict, HolesBy: by,
			MissingTotal: by.AnsweredWithoutSeries + by.AnsweredEmpty + by.InputIncomplete + by.PrimaryUnrecorded + by.NotInMemory}
		for _, hole := range holes {
			row.Holes = append(row.Holes, WindowHole{Reason: hole.reason, OutsideEffectiveTime: hole.outside})
		}
		return row
	}
	off := spec{reason: contract.ReasonEffectiveTimeInactive, outside: true}
	in := func(reason string) spec { return spec{reason: reason} }
	answered := func(holes ...spec) WindowRow {
		return window(WindowHoleCounts{AnsweredWithoutSeries: uint32(len(holes))}, VerdictDataAbsentWhenQueried, holes...)
	}
	incomplete := func(holes ...spec) WindowRow {
		return window(WindowHoleCounts{InputIncomplete: uint32(len(holes))}, VerdictInputIncomplete, holes...)
	}
	coverage := func(guarded uint32, windows ...WindowRow) *HistoryCoverage {
		return &HistoryCoverage{Levels: 4, Short: uint32(len(windows)), WorstValid: 3, WorstRequired: 5, ShortRounds: 9, Guarded: guarded, Windows: windows}
	}
	drift := func(windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", CauseReason: "CONFIG_DRIFT", Coverage: coverage(uint32(len(windows)), windows...)}
	}
	gapped := func(windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_GAPPED", Coverage: coverage(0, windows...)}
	}
	guarded := func(windows ...WindowRow) Anomaly {
		return Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "GAP_GUARD_WARMING", CauseReason: "GAP_GUARD_WARMING",
			Coverage: coverage(uint32(len(windows)), windows...)}
	}
	uncounted := func(windows ...WindowRow) Anomaly {
		row := drift(windows...)
		row.Coverage.Levels = 0
		return row
	}
	for name, tc := range map[string]struct {
		outside, kept Anomaly
		line          Check
	}{
		"SERIES_SPARSE: a guard's CONFIG_DRIFT, the series answered without": {
			drift(answered(off, off), answered(off)), drift(answered(off, in("LEVEL_OUTCOME_UNKNOWN")), answered(off)), CheckSeriesSparse},
		"SERIES_DATA_MISSING: gapped, a window incomplete": {
			gapped(answered(off, off), incomplete(off)), gapped(answered(off, off), incomplete(in(""))), CheckSeriesDataMissing},
		"WINDOW_UNDECIDED: held under a gap guard": {
			guarded(incomplete(off, off), incomplete(off)), guarded(incomplete(off, off), incomplete(in("FULL_COMPLETED"))), CheckWindowUndecided},
		"CONFIG_UNRESOLVED: a guard's CONFIG_DRIFT without counts": {
			uncounted(answered(off), answered(off)), uncounted(answered(off), answered(in(""))), CheckConfigUnresolved},
		"the word without the schedule's mark keeps the line": {
			gapped(answered(off, spec{reason: "", outside: true})), gapped(answered(off, in(contract.ReasonEffectiveTimeInactive))), CheckSeriesSparse},
	} {
		if check, under, _ := checkOf(tc.kept, ScheduleOnTime); check != tc.line || !under {
			t.Fatalf("%s: with an in-hours hole the row is under %s (%v), want %s", name, check, under, tc.line)
		}
		if check, under, _ := checkOf(tc.outside, ScheduleOnTime); check != "" || under {
			t.Fatalf("%s: outside its active hours the row is under %s, want no line", name, check)
		}
	}
	unnamedHole := drift(answered(off, off), answered(off))
	unnamedHole.Coverage.Windows[0].MissingTotal++
	unnamedWindow := drift(answered(off, off), answered(off))
	unnamedWindow.Coverage.Short++
	for name, row := range map[string]Anomaly{"a hole not named": unnamedHole, "a short window not named": unnamedWindow} {
		if check, under, _ := checkOf(row, ScheduleOnTime); check == "" || !under {
			t.Fatalf("%s: the row left its line on what it did not name", name)
		}
	}
}

// A Plan detected more often than it aggregates, over a table whose storage
// answered on its own grid, completes every round with its primary input
// unavailable for that reason. The line is the strategy's: the definition
// cannot be evaluated as written here, and its owner removes the step.
func TestAStepTheStorageCannotReadIsTheStrategysUnevaluablePlan(t *testing.T) {
	row := Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", Cause: "PRIMARY_INPUT_UNAVAILABLE",
		CauseReason: "DETECT_INTERVAL_STORAGE_NOT_SLIDING"}
	check, under, unclassified := checkOf(row, ScheduleOnTime)
	if check != CheckPlanUnevaluable || !under || unclassified {
		t.Fatalf("check = %s (under %v, unclassified %v), want %s", check, under, unclassified, CheckPlanUnevaluable)
	}
	if answers := checkAnswers[CheckPlanUnevaluable]; answers.Owner != OwnerStrategy {
		t.Fatalf("%s is owned by %s, want the strategy", CheckPlanUnevaluable, answers.Owner)
	}
	if pair := checkWords[CheckPlanUnevaluable]; pair.Action != ActionStrategyEdit {
		t.Fatalf("%s asks %s, want the strategy's owner to change it", CheckPlanUnevaluable, pair.Action)
	}
}
