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
// across two owners.
//
// controlplane/runtime_executable_catalog.go files ALGORITHM_UNSUPPORTED,
// PLAN_BUDGET_EXCEEDED and LEVEL_BUDGET_EXCEEDED together as
// DispositionUnsupported: the definition cannot be run as written. The first
// version of this table put the two budget codes with the run-time budgets and
// sent the reader to look for an alarmd budget to raise -- on the runtime that
// feeds this page there is none, those codes come only from the compiler, and
// the reader would have arrived at two guards that cannot fire.
//
// The catalog is not imported here (it would be a cycle), so the three codes
// are named. If the catalog's grouping changes, this fails on the code that
// moved, which is the moment to decide whether the owner moves with it.
func TestCodesTheCatalogFilesTogetherShareAnOwner(t *testing.T) {
	unsupported := []string{"ALGORITHM_UNSUPPORTED", "PLAN_BUDGET_EXCEEDED", "LEVEL_BUDGET_EXCEEDED"}
	for _, code := range unsupported {
		situation, mapped := codeSituations[code]
		if !mapped {
			t.Errorf("%s reaches no situation", code)
			continue
		}
		got := finding(situation, 0)
		if got.Owner != OwnerStrategy {
			t.Errorf("%s is %s's: the control plane files it with ALGORITHM_UNSUPPORTED as a "+
				"definition that cannot run as written, and on this runtime it has no producer "+
				"but the compiler", code, got.Owner)
		}
		if got.Where != WhereStrategy {
			t.Errorf("%s sends the reader to %q, want the strategy", code, got.Where)
		}
	}
	// And the run-time budgets stay this deployment's. Moving the whole bucket
	// would have been the easy fix and the wrong one.
	for _, code := range []string{"EXECUTION_BUDGET_EXHAUSTED", "SLOT_BUDGET_EXCEEDED", "STATE_BUDGET_EXCEEDED"} {
		if got := finding(codeSituations[code], 0).Owner; got != OwnerAlarmd {
			t.Errorf("%s is %s's, want this deployment's: it is a budget allocated at run time", code, got)
		}
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

// The to-do list is drawn from every column and holds exactly the objects
// someone here has to act on, in the order they should be acted on.
func TestTheToDoListCrossesColumnsAndOrdersByUrgency(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	mk := func(id string, finding Finding, since time.Time) Anomaly {
		return Anomaly{QueryGroup: id, Finding: finding, Since: since}
	}
	// Every pair that the order has to separate is here with the wrong
	// tiebreak pointing the other way: the oldest object is nobody's, the
	// oldest of ours might heal while a newer one will not, and the oldest
	// undetermined one will heal on its own. A fixture where age and urgency
	// agree passes against an ordering that only reads age.
	anomalies := []Anomaly{
		mk("churn", finding(SituationSeriesChurning, 0), at.Add(-3*time.Hour)),
		mk("stalled-new", finding(SituationStalled, 0), at.Add(-time.Minute)),
		mk("never-reached-old", finding(SituationNeverReached, 0), at.Add(-7*time.Hour)),
	}
	demoted := []Anomaly{
		mk("cooldown", finding(SituationBackendCooldown, 0), at.Add(-time.Hour)),
		mk("stalled-old", finding(SituationStalled, 0), at.Add(-2*time.Hour)),
	}
	undecidable := []Anomaly{
		mk("empty", finding(SituationWindowEmpty, 0), at.Add(-4*time.Hour)),
		mk("young", finding(SituationSeriesYoung, 0), at.Add(-5*time.Hour)),
		mk("restored-old", finding(SituationRestoredWithoutCause, 0), at.Add(-8*time.Hour)),
	}
	byDesign := []Anomaly{
		mk("drift", finding(SituationConfigDrift, 0), at.Add(-time.Hour)),
		mk("offhours", finding(SituationOffHours, 0), at.Add(-6*time.Hour)),
	}
	got := ActionRequired(anomalies, demoted, undecidable, byDesign)
	// Ours before undetermined; will-not-heal before might before will; then
	// oldest first.
	want := []string{"stalled-old", "stalled-new", "never-reached-old", "empty", "drift", "restored-old"}
	if len(got) != len(want) {
		t.Fatalf("to-do = %d objects, want %d: %+v", len(got), len(want), names(got))
	}
	for index, id := range want {
		if got[index].QueryGroup != id {
			t.Errorf("to-do[%d] = %s, want %s (full order %v)", index, got[index].QueryGroup, id, names(got))
		}
	}
	// Churn, cooldown, young and off-hours are somebody's or nobody's and
	// must not be on it. The list is what "do I have to do anything" reads.
	for _, entry := range got {
		if entry.Finding.Owner != OwnerAlarmd && entry.Finding.Owner != OwnerUndetermined {
			t.Errorf("%s is on the to-do list with owner %s", entry.QueryGroup, entry.Finding.Owner)
		}
	}
}

func names(anomalies []Anomaly) []string {
	list := make([]string, len(anomalies))
	for index, anomaly := range anomalies {
		list[index] = anomaly.QueryGroup
	}
	return list
}

// The route serves the to-do column and the owner filter, and the summary
// counts partition the list.
func TestTheObjectRouteServesTheToDoListAndTheOwnerFilter(t *testing.T) {
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

	// The to-do column: ours, plus the undetermined ones from the other
	// columns (the fixture's by-design CONFIG_DRIFT and undecidable
	// HISTORY_WARMING without counts), and not the churn or the demoted
	// backend.
	status, body := get(t, handler, "/api/objects?column="+ColumnActionRequired)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %v", status, body)
	}
	rows, _ := body["anomalies"].([]any)
	got := map[string]string{}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		finding, _ := row["finding"].(map[string]any)
		owner, _ := finding["owner"].(string)
		id, _ := row["query_group"].(string)
		got[id] = owner
	}
	if got["qg-ours"] != string(OwnerAlarmd) {
		t.Errorf("qg-ours on the to-do list as %q, want %s", got["qg-ours"], OwnerAlarmd)
	}
	if got["qg-by-design"] != string(OwnerUndetermined) || got["qg-undecidable"] != string(OwnerUndetermined) {
		t.Errorf("undetermined objects from other columns = %v, want both on the list as UNDETERMINED", got)
	}
	if _, listed := got["qg-churn"]; listed {
		t.Error("qg-churn is on the to-do list: it is the strategy's, and listing it is the page " +
			"handing the reader work that is not theirs")
	}
	if _, listed := got["qg-demoted"]; listed {
		t.Error("qg-demoted is on the to-do list: the backend's")
	}
	summary, _ := body["summary"].(map[string]any)
	if required, _ := summary["action_required"].(float64); int(required) != len(rows) {
		t.Errorf("summary action_required = %v over a to-do list of %d", required, len(rows))
	}

	// The owner filter on the anomaly column narrows to that owner's.
	_, body = get(t, handler, "/api/objects?owner="+string(OwnerStrategy))
	rows, _ = body["anomalies"].([]any)
	if len(rows) != 1 {
		t.Fatalf("owner=STRATEGY on the anomaly column: %d rows, want the one churning object", len(rows))
	}
	if row, _ := rows[0].(map[string]any); row["query_group"] != "qg-churn" {
		t.Errorf("owner=STRATEGY returned %v", row["query_group"])
	}
	// And the counts partition the unfiltered list.
	_, body = get(t, handler, "/api/objects")
	summary, _ = body["summary"].(map[string]any)
	byOwner, _ := summary["by_owner"].(map[string]any)
	top, _ := byOwner["top"].([]any)
	total := 0.0
	for _, entry := range top {
		count, _ := entry.(map[string]any)
		n, _ := count["count"].(float64)
		total += n
	}
	if int(total) != 2 {
		t.Errorf("by_owner sums to %v over 2 objects: the governance counts must partition the list, "+
			"or a count that opens a list opens a different one", total)
	}

	// An owner nobody declared is refused, not defaulted: a typo that silently
	// matched nothing would return an empty list under a heading that says
	// "nothing for this owner".
	status, _ = get(t, handler, "/api/objects?owner=NOBODY_IN_PARTICULAR")
	if status != http.StatusBadRequest {
		t.Errorf("unknown owner: status = %d, want 400", status)
	}
}
