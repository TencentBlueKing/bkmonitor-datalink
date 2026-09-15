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
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// One reason code, six situations, five owners -- decided on the counts.
//
// Every row here carries HISTORY_WARNING or HISTORY_GAPPED and nothing else
// that a code table could read, so the only thing separating them is the
// coverage. The page used to send all of them to the strategy owner on the
// strength of the code; this is the table that says where each one actually
// goes, and it is the table a reader should be able to check a row against.
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
		want    Situation
		owner   Owner
	}{
		// A window still filling: nobody's, heals.
		{"young", warming(HistoryCoverage{Levels: 3, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 2}),
			SituationSeriesYoung, OwnerNobody},
		// Every short window a new series, for longer than a window takes to
		// fill: the strategy's dimensions.
		{"churning", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 4, ShortFresh: 4, FreshRounds: 40}),
			SituationSeriesChurning, OwnerStrategy},
		// Same shortfall, same run, every short window a series with history:
		// the data.
		{"data missing", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40}),
			SituationSeriesDataMissing, OwnerData},
		// Some fresh, some not: cannot be handed to either.
		{"mixed", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 2, ShortFresh: 2, FreshRounds: 0}),
			SituationSeriesMixed, OwnerUndetermined},
		// Every short window fresh, for one round: a strategy edit looks like
		// this. Wait.
		{"renewed", warming(HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9,
			ShortRounds: 40, Fresh: 9, ShortFresh: 4, FreshRounds: 1}),
			SituationSeriesRenewed, OwnerNobody},
		// Nothing in the window at all, sustained. No record arriving and a
		// record whose detection produced nothing look identical here and have
		// different owners, so this stays on the list.
		{"empty", warming(HistoryCoverage{Levels: 3, Short: 2, Empty: 2, WorstRequired: 14,
			ShortRounds: 40, EmptyRounds: 40}),
			SituationWindowEmpty, OwnerUndetermined},
		// Complete under a reason that says otherwise: the verdict is held
		// over and releases itself.
		{"held", gapped(HistoryCoverage{Levels: 3, Guarded: 3}),
			SituationVerdictHeld, OwnerNobody},
		// Holes, sustained past the window: data arriving with holes in it.
		{"intermittent", gapped(HistoryCoverage{Levels: 1, Short: 1, WorstValid: 5, WorstRequired: 9,
			ShortRounds: 29}),
			SituationDataIntermittent, OwnerData},
		// Holes, not yet past the window: could be data that just stopped.
		{"just gapped", gapped(HistoryCoverage{Levels: 1, Short: 1, WorstValid: 5, WorstRequired: 9,
			ShortRounds: 3}),
			SituationDataJustGapped, OwnerNobody},
		// The reason without any counts -- an older replica -- falls back to
		// the coarse reading, which stays undetermined rather than guessing.
		{"no counts", Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING"},
			SituationWindowEmpty, OwnerUndetermined},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := findingOf(testCase.anomaly)
			if got.Situation != testCase.want {
				t.Errorf("situation = %s, want %s", got.Situation, testCase.want)
			}
			if got.Owner != testCase.owner {
				t.Errorf("owner = %s, want %s", got.Owner, testCase.owner)
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
		where Where
	}
	byDisposition := map[controlplane.Disposition][]reading{}
	for _, definition := range contract.ReasonCatalogV2() {
		disposition, filed := controlplane.CompilerTerminalDisposition(definition.Code)
		if !filed {
			continue
		}
		situation, mapped := codeSituations[definition.Code]
		if !mapped {
			t.Errorf("%s is a compiler terminal the catalog files as %s and this table maps to "+
				"nothing", definition.Code, disposition)
			continue
		}
		got := finding(situation, 0)
		byDisposition[disposition] = append(byDisposition[disposition],
			reading{code: definition.Code, owner: got.Owner, where: got.Where})
	}
	if len(byDisposition) < 2 {
		t.Fatalf("only %d dispositions found across the catalogue; the comparison would be "+
			"vacuous", len(byDisposition))
	}
	for disposition, readings := range byDisposition {
		first := readings[0]
		for _, other := range readings[1:] {
			if other.owner != first.owner || other.where != first.where {
				t.Errorf("the catalog files %s and %s together as %s; this table sends one to %s/%q "+
					"and the other to %s/%q -- a reader is sent to two different people for one "+
					"disposition", first.code, other.code, disposition,
					first.owner, first.where, other.owner, other.where)
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
		if got := finding(codeSituations[code], 0).Owner; got != OwnerAlarmd {
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
		want    Situation
		owner   Owner
	}{
		// The provider answered with a status code: it read the query and
		// refused it.
		{"cooldown on a provider status", cooldown("response=status_space_table_id_field_is_not_exists"),
			SituationQueryRejected, OwnerUndetermined},
		{"degraded on a provider status", degraded("response=status_space_table_id_field_is_not_exists"),
			SituationQueryRejected, OwnerUndetermined},
		// An HTTP 4xx is the same statement in the transport's vocabulary.
		{"cooldown on a 4xx", cooldown("http_status=400"), SituationQueryRejected, OwnerUndetermined},
		// A timeout or a 5xx is the backend not answering: the data's.
		{"cooldown on a timeout", cooldown("transport=timeout"), SituationBackendCooldown, OwnerData},
		{"degraded on a 503", degraded("http_status=503"), SituationBackendUnavailable, OwnerData},
		// No detail at all: nothing says it was refused, so the coarse reading.
		{"cooldown without detail", Anomaly{Kind: KindQueryCooldown, CauseReason: "QUERY_UNAVAILABLE"},
			SituationBackendCooldown, OwnerData},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := findingOf(testCase.anomaly)
			if got.Situation != testCase.want || got.Owner != testCase.owner {
				t.Errorf("finding = %s/%s, want %s/%s", got.Situation, got.Owner, testCase.want, testCase.owner)
			}
		})
	}
}

// Stalled and never-reached are decided before any code is read.
//
// A stalled object's last code is usually the external thing that happened
// just before it got stuck, and reading that first would hand a deployment's
// own stuck round to the data owner.
func TestStalledAndNeverReachedAreDecidedBeforeTheCode(t *testing.T) {
	stalled := Anomaly{Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", Stalled: true}
	if got := findingOf(stalled); got.Situation != SituationStalled || got.Owner != OwnerAlarmd {
		t.Errorf("stalled with an external last code = %+v, want STALLED/ALARMD", got)
	}
	never := Anomaly{Kind: KindOverdueWake, ReasonCode: "HISTORY_WARMING"}
	if got := findingOf(never); got.Situation != SituationNeverReached || got.Owner != OwnerAlarmd {
		t.Errorf("never reached = %+v, want NEVER_REACHED/ALARMD", got)
	}
}

// The rows a check opens come from every column and are ordered by what
// decides whether to act first.
func TestUnderCheckCrossesColumnsAndOrdersByUrgency(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	mk := func(id string, situation Situation, since time.Time) Anomaly {
		item := Anomaly{QueryGroup: id, Replica: "pod-a", Since: since, Finding: finding(situation, 0)}
		check, under := checkOf(item)
		if under {
			item.Finding.Check = check
		}
		return item
	}
	// The same check in three columns, with the age tiebreak pointing the wrong
	// way in one pair (a stalled object that is ours and newer sorts before an
	// undetermined one that is older, within one check every row shares the
	// owner, so age decides) -- and objects under other checks in every column,
	// which must not be listed.
	anomalies := []Anomaly{
		mk("stalled-new", SituationStalled, at.Add(-time.Minute)),
		mk("churn", SituationSeriesChurning, at.Add(-3*time.Hour)),
	}
	demoted := []Anomaly{
		mk("stalled-old", SituationStalled, at.Add(-2*time.Hour)),
		mk("cooldown", SituationBackendCooldown, at.Add(-time.Hour)),
	}
	undecidable := []Anomaly{
		mk("stalled-older", SituationStalled, at.Add(-4*time.Hour)),
		mk("young", SituationSeriesYoung, at.Add(-5*time.Hour)),
	}
	got := UnderCheck(CheckRoundsStalled, "", anomalies, demoted, undecidable)
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
	if got := UnderCheck(CheckRoundsStalled, "pod-b", anomalies, demoted, undecidable); len(got) != 1 ||
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
		string(CheckBackendNotAnswering): {OwnerData, 1},
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
	// A group within it narrows to that fold. The undecided object names
	// strategy 8930, so that is its group.
	_, body = get(t, handler, "/api/objects?check="+string(CheckWindowUndecided)+"&group=8930")
	if rows, _ := body["anomalies"].([]any); len(rows) != 1 {
		t.Errorf("check=WINDOW_UNDECIDED group=8930: %d rows, want 1", len(rows))
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
