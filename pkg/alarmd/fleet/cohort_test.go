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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// cohortSnapshots is the live shape the join was built for: two replicas whose
// due indexes count a fifteen-second cohort and a sixty-second one, and rows
// of three kinds -- a fifteen-second object in an extended query cooldown
// because its table does not exist (the strategy's line), a fifteen-second
// object whose round was given up past the replay bound with a skip record,
// and a sixty-second object under this deployment's own defect line -- plus
// one row the due index knows nothing about.
func cohortSnapshots() []Snapshot {
	snapshots := healthySnapshots()
	snapshots[0].Schedule = &ScheduleCensus{Waiting: 300, Cooling: 2, Cohorts: []ScheduleCohort{
		{IntervalSeconds: 15, Objects: 4, Cooling: 2}, {IntervalSeconds: 60, Objects: 296}}}
	snapshots[1].Schedule = &ScheduleCensus{Waiting: 250, Cooling: 1, Cohorts: []ScheduleCohort{
		{IntervalSeconds: 15, Objects: 3, Cooling: 1, Overdue: 1}, {IntervalSeconds: 60, Objects: 247}}}
	cooling := Anomaly{QueryGroup: "qg-cooling", Kind: KindQueryCooldown, Replica: "pod-a",
		Since: now.Add(-50 * time.Minute), ReasonSince: now.Add(-50 * time.Minute), SinceFrom: SinceBusinessState,
		Failure:       &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"},
		QueryCooldown: &observability.QueryCooldownFacts{Event: "extended", Failures: 18, Until: now.Add(4 * time.Minute)},
		Wake:          &WakeFacts{Known: true, IntervalSeconds: 15, Cooling: true},
		Strategies:    []StrategyRef{{StrategyID: "11785", BusinessID: "7"}}}
	skipped := Anomaly{QueryGroup: "qg-skipped", Kind: KindDegradedRun, CauseReason: "GAP_SKIPPED", ReasonCode: "GAP_SKIPPED", Replica: "pod-b",
		Since: now.Add(-20 * time.Minute), SinceFrom: SinceBusinessState,
		Skip:       &SkippedSpan{FirstSlot: 100, LastSlot: 190, Slots: 7, IntervalSeconds: 15},
		Strategies: []StrategyRef{{StrategyID: "11781", BusinessID: "7"}}}
	defect := Anomaly{QueryGroup: "qg-defect", Kind: KindBlockedRun, ReasonCode: "panic", Replica: "pod-a",
		Since: now.Add(-5 * time.Minute), SinceFrom: SinceBusinessState,
		Wake:       &WakeFacts{Known: true, IntervalSeconds: 60},
		Strategies: []StrategyRef{{StrategyID: "77", BusinessID: "2"}}}
	unknown := Anomaly{QueryGroup: "qg-unknown", Kind: KindBlockedRun, ReasonCode: "source_error", Replica: "pod-b",
		Since: now.Add(-5 * time.Minute), SinceFrom: SinceBusinessState,
		Strategies: []StrategyRef{{StrategyID: "78", BusinessID: "2"}}}
	// The live shape: the cooling object is in the demoted pool, not the
	// anomaly column -- which is where the four fifteen-second objects the
	// join was built for all were.
	snapshots[0].Anomalies, snapshots[0].TotalAnomalies = []Anomaly{defect}, 1
	snapshots[0].Demoted, snapshots[0].TotalDemoted = []Anomaly{cooling}, 1
	snapshots[1].Anomalies, snapshots[1].TotalAnomalies = []Anomaly{skipped, unknown}, 2
	return snapshots
}

// A number read per period has, on the verdict route, the population it was
// counted over and what the rows of that period say -- so "15 s: 40% skipped,
// p99 59 s" leads to seven objects, of which three are listed, one skipped past
// the replay bound, one cooling under the strategy's line. Two lines of
// investigation spent two days making that join by hand.
func TestTheVerdictRouteJoinsEachPeriodToItsPopulationAndItsRows(t *testing.T) {
	handler := handlerWith(t, cohortSnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	cohorts, _ := health["cohorts"].([]any)
	if len(cohorts) != 3 {
		t.Fatalf("cohorts = %v, want three periods: unknown, 15, 60", health["cohorts"])
	}
	byInterval := map[float64]map[string]any{}
	for _, item := range cohorts {
		cohort := item.(map[string]any)
		byInterval[cohort["interval_seconds"].(float64)] = cohort
	}
	fifteen := byInterval[15]
	// Population from the two censuses added: 4+3 objects, 2+1 cooling, 0+1 overdue.
	if fifteen["objects"] != 7.0 || fifteen["cooling"] != 3.0 || fifteen["overdue"] != 1.0 {
		t.Errorf("15 s population = %v, want objects 7, cooling 3, overdue 1 from the censuses added", fifteen)
	}
	// Rows: two of them carry the period, one skipped past the bound, one in cooldown.
	if fifteen["listed"] != 2.0 || fifteen["gap_skipped"] != 1.0 || fifteen["in_cooldown"] != 1.0 {
		t.Errorf("15 s rows = %v, want listed 2, gap_skipped 1, in_cooldown 1", fifteen)
	}
	byCheck, _ := fifteen["by_check"].(map[string]any)
	if byCheck[string(CheckQueryTargetMissing)] != 1.0 || byCheck[string(CheckDetectionAbandoned)] != 1.0 {
		t.Errorf("15 s by_check = %v, want one under %s and one under %s", byCheck, CheckQueryTargetMissing, CheckDetectionAbandoned)
	}
	byOwner, _ := fifteen["by_owner"].(map[string]any)
	if byOwner[string(OwnerStrategy)] != 1.0 {
		t.Errorf("15 s by_owner = %v, want the cooling one under the strategy", byOwner)
	}
	sixty := byInterval[60]
	if sixty["objects"] != 543.0 || sixty["listed"] != 1.0 || sixty["by_owner"].(map[string]any)[string(OwnerAlarmd)] != 1.0 {
		t.Errorf("60 s = %v, want objects 543, one listed row, alarmd's own", sixty)
	}
	// The row the due index knows nothing about is counted, under 0, not lost.
	if unknown := byInterval[0]; unknown["listed"] != 1.0 || unknown["objects"] != 0.0 {
		t.Errorf("unknown-period cohort = %v, want one listed row and no population", unknown)
	}
	if _, listedOnly := fifteen["listed_only"]; listedOnly {
		t.Errorf("15 s says listed_only with two censuses published: %v", fifteen)
	}
}

// The cooldown line's arithmetic: the due indexes' count of every cooling
// object, the listed ones, how many are extended, and whose line they are
// under with how long the longest has been there.
func TestTheVerdictRouteSaysHowManyObjectsAreCoolingAndWhoseTheyAre(t *testing.T) {
	handler := handlerWith(t, cohortSnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	cooling, _ := health["cooling"].(map[string]any)
	if cooling == nil {
		t.Fatalf("no cooling on the verdict route: %v", health)
	}
	if cooling["objects"] != 3.0 || cooling["listed"] != 1.0 || cooling["extended"] != 1.0 {
		t.Errorf("cooling = %v, want objects 3 from the censuses, listed 1, extended 1", cooling)
	}
	if owners, _ := cooling["by_owner"].(map[string]any); owners[string(OwnerStrategy)] != 1.0 {
		t.Errorf("cooling by_owner = %v, want the one under the strategy", cooling["by_owner"])
	}
	if checks, _ := cooling["by_check"].(map[string]any); checks[string(CheckQueryTargetMissing)] != 1.0 {
		t.Errorf("cooling by_check = %v, want the one under %s", cooling["by_check"], CheckQueryTargetMissing)
	}
	if age, _ := cooling["oldest_since_seconds"].(float64); age < 2990 || age > 3010 {
		t.Errorf("oldest_since_seconds = %v, want about fifty minutes", cooling["oldest_since_seconds"])
	}
}

// Without a census from any replica -- an older build -- the cohorts are the
// rows' periods alone, and say so, rather than reporting a population of zero.
func TestCohortsWithoutACensusAreListedOnly(t *testing.T) {
	snapshots := cohortSnapshots()
	snapshots[0].Schedule, snapshots[1].Schedule = nil, nil
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	cohorts, _ := health["cohorts"].([]any)
	if len(cohorts) != 3 {
		t.Fatalf("cohorts = %v, want the three periods the rows carry", health["cohorts"])
	}
	for _, item := range cohorts {
		cohort := item.(map[string]any)
		if cohort["listed_only"] != true || cohort["objects"] != 0.0 {
			t.Errorf("cohort %v without a census must say listed_only and carry no population", cohort)
		}
	}
	if cooling, _ := health["cooling"].(map[string]any); cooling["objects"] != 0.0 || cooling["listed"] != 1.0 {
		t.Errorf("cooling without a census = %v, want objects 0 (no count) and listed 1", cooling)
	}
}

// interval= opens the rows of one period from every column and says it did
// -- the cohort was counted over every column, and on the live deployment the
// four objects it was built for were all in the demoted pool, so a filter on
// the anomaly column alone answered zero under a cohort that said four. A
// column named beside it narrows to that column; a period the rows do not
// know is 0; anything that is not a period is refused.
func TestTheListRouteOpensOnePeriodsRows(t *testing.T) {
	handler := handlerWith(t, cohortSnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	status, list := get(t, handler, "/api/objects?interval=15")
	rows, _ := list["anomalies"].([]any)
	if status != 200 || len(rows) != 2 || list["interval"] != 15.0 || list["filtered"] != true || list["column"] != "" {
		t.Fatalf("interval=15 -> %d rows %d interval %v filtered %v column %q; want both rows from across the columns", status, len(rows), list["interval"], list["filtered"], list["column"])
	}
	// The cohort the rows were opened from says the same two.
	for _, item := range list["cohorts"].([]any) {
		if cohort := item.(map[string]any); cohort["interval_seconds"] == 15.0 && cohort["listed"] != 2.0 {
			t.Fatalf("cohort 15 s lists %v while interval=15 opened %d rows", cohort["listed"], len(rows))
		}
	}
	// Named beside a column, the period narrows that column: the demoted
	// pool holds the cooling one alone, the anomaly column the skipped one.
	_, demoted := get(t, handler, "/api/objects?interval=15&column=demoted")
	if rows, _ := demoted["anomalies"].([]any); len(rows) != 1 || rows[0].(map[string]any)["query_group"] != "qg-cooling" || demoted["column"] != ColumnDemoted {
		t.Fatalf("interval=15&column=demoted = %v, want the cooling row alone under the demoted column", demoted["anomalies"])
	}
	_, anomalies := get(t, handler, "/api/objects?interval=15&column=anomalies")
	if rows, _ := anomalies["anomalies"].([]any); len(rows) != 1 || rows[0].(map[string]any)["query_group"] != "qg-skipped" {
		t.Fatalf("interval=15&column=anomalies = %v, want the skipped row alone", anomalies["anomalies"])
	}
	for _, item := range rows {
		row := item.(map[string]any)
		qg := row["query_group"].(string)
		if qg != "qg-cooling" && qg != "qg-skipped" {
			t.Errorf("interval=15 returned %s", qg)
		}
	}
	if cohorts, _ := list["cohorts"].([]any); len(cohorts) != 3 {
		t.Errorf("the list route carries %d cohorts, want the same three as the verdict route", len(cohorts))
	}
	_, unknown := get(t, handler, "/api/objects?interval=0")
	if rows, _ := unknown["anomalies"].([]any); len(rows) != 1 || rows[0].(map[string]any)["query_group"] != "qg-unknown" {
		t.Errorf("interval=0 = %v, want the one row whose period is not known", unknown["anomalies"])
	}
	if status, _ := get(t, handler, "/api/objects?interval=fifteen"); status != 400 {
		t.Errorf("interval=fifteen -> %d, want 400", status)
	}
	_, all := get(t, handler, "/api/objects")
	if rows, _ := all["anomalies"].([]any); len(rows) != 3 || all["filtered"] != false {
		t.Errorf("no interval filter -> %d rows filtered %v, want the anomaly column's three, unfiltered", len(rows), all["filtered"])
	}
}
