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
	"testing"
	"time"
)

// A problem is one step, one dependency, one kind of failure: a line folds
// on that and counts the reason codes under it, so a Redis restart is "commit,
// Redis, unavailable: 86 objects (REDIS_UNAVAILABLE 60, STATE_WRITE_RETRYABLE
// 26)" and not two lines, and two query symptoms whose dependency nobody
// located are two problems at the query step that both say so.
func TestALineFoldsOnTheProblemAndCountsTheCodesUnderIt(t *testing.T) {
	at := now.Add(-time.Minute)
	rows := []Anomaly{
		{QueryGroup: "a", Kind: KindDegradedRun, CauseReason: "REDIS_UNAVAILABLE", ReasonLastAt: at, Since: now.Add(-time.Hour)},
		{QueryGroup: "b", Kind: KindDegradedRun, CauseReason: "REDIS_UNAVAILABLE", ReasonLastAt: at, Since: now.Add(-30 * time.Minute)},
		{QueryGroup: "c", Kind: KindDegradedRun, CauseReason: "STATE_WRITE_RETRYABLE", ReasonLastAt: at, Since: now.Add(-2 * time.Hour),
			LastError: &LastError{Text: "alarmd state: write: redis: connection pool timeout", At: at}},
		{QueryGroup: "d", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", ReasonLastAt: at, Since: now.Add(-time.Hour)},
		{QueryGroup: "e", Kind: KindDegradedRun, CauseReason: "QUERY_PARTIAL", ReasonLastAt: at, Since: now.Add(-time.Hour)},
	}
	Attribute(rows, now)
	reports := ReportChecks([][]Anomaly{rows}, nil, nil, now)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	dependency := byCode[CheckDependencyDown]
	if len(dependency.Groups) != 1 || dependency.Groups[0].Key != "COMMIT/REDIS/UNAVAILABLE" || dependency.Groups[0].Objects != 3 {
		t.Fatalf("DEPENDENCY_DOWN groups = %+v, want one Redis problem at the commit step over three objects", dependency.Groups)
	}
	if codes := dependency.Groups[0].Codes; codes["REDIS_UNAVAILABLE"] != 2 || codes["STATE_WRITE_RETRYABLE"] != 1 {
		t.Fatalf("codes under the Redis problem = %v, want REDIS_UNAVAILABLE 2 and STATE_WRITE_RETRYABLE 1 (named Redis by its text)", codes)
	}
	if first := dependency.Groups[0].FirstFailure; first == nil || !first.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("first failure = %v, want the earliest onset among the three", first)
	}
	backend := byCode[CheckBackendNotAnswering]
	if len(backend.Groups) != 2 || backend.Groups[0].Key != "QUERY/UNLOCATED/TIMEOUT" || backend.Groups[1].Key != "QUERY/UNLOCATED/UNAVAILABLE" {
		t.Fatalf("BACKEND_NOT_ANSWERING groups = %+v, want two problems at the query step, dependency unlocated on both", backend.Groups)
	}
	// The second fact under DEFECT counts the internal code, not the code
	// the row is listed for.
	internal := []Anomaly{{QueryGroup: "f", Kind: KindQueryCooldown, QueryCooldown: &cooldownFacts, ReasonLastAt: at,
		Failure:  &FailureRef{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"},
		Internal: &FailureRef{Stage: "execute", Category: "completion_contract", Code: "GAP_SCOPE_REASON_CONFLICT"}}}
	Attribute(internal, now)
	reports = ReportChecks([][]Anomaly{nil, internal}, nil, nil, now)
	for _, report := range reports {
		if report.Code != CheckDefect {
			continue
		}
		if len(report.Groups) != 1 || report.Groups[0].Key != "GAP_SCOPE_REASON_CONFLICT" || report.Groups[0].Codes["GAP_SCOPE_REASON_CONFLICT"] != 1 || len(report.Groups[0].Codes) != 1 {
			t.Fatalf("DEFECT second-fact group = %+v, want folded and counted on the internal code alone", report.Groups)
		}
	}
}

// The second fact is read on a row under no line too. A warming object is
// nobody's -- the window fills and it heals -- but a warming object whose
// every round also ends in a state version conflict carries this
// deployment's own failure, and the DEFECT line has to say so: on a live
// deployment two such rows were skipped as nothing to report, so the line
// read two while opening it listed four. The line and its rows come from
// the same read, and they have to count the same objects.
func TestAnInternalFailureOnARowUnderNoLineIsStillOnTheDefectLine(t *testing.T) {
	at := now.Add(-time.Minute)
	young := Anomaly{QueryGroup: "warming", Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN",
		CauseReason: "HISTORY_WARMING", ReasonLastAt: at, Since: now.Add(-time.Hour),
		Coverage: &HistoryCoverage{Levels: 3, Short: 1, WorstValid: 7, WorstRequired: 9, ShortRounds: 2},
		Internal: &FailureRef{Stage: "other", Category: "completion_contract", Code: "STATE_VERSION_CONFLICT"}}
	rows := []Anomaly{young}
	Attribute(rows, now)
	if rows[0].Finding.Check != "" || rows[0].Finding.Owner != OwnerNobody {
		t.Fatalf("the fixture is under %q / %s, want under no line: the test needs a row the column decided is nobody's",
			rows[0].Finding.Check, rows[0].Finding.Owner)
	}
	view := View{Anomalies: rows}
	reports := ReportChecks([][]Anomaly{rows}, nil, &view, now)
	var defect *CheckReport
	for index := range reports {
		if reports[index].Code == CheckDefect {
			defect = &reports[index]
		}
	}
	if defect == nil {
		t.Fatalf("checks = %+v, want a DEFECT line for the internal failure on a row under no line", reports)
	}
	if defect.Objects != 1 || len(defect.Groups) != 1 || defect.Groups[0].Key != "STATE_VERSION_CONFLICT" {
		t.Fatalf("DEFECT line = %d objects, groups %+v, want one object folded on STATE_VERSION_CONFLICT", defect.Objects, defect.Groups)
	}
	// And the line's count is the count of the rows it opens.
	if listed := UnderCheck(CheckDefect, "", &view, now); len(listed) != defect.Objects {
		t.Fatalf("opening the DEFECT line lists %d rows, the line says %d", len(listed), defect.Objects)
	}
}

// Where a problem is between failing and fixed, read from its objects'
// rows within the recent window. The second accuracy constraint is the
// UNCONFIRMED case: a failure that left the window is not a recovery, and
// nothing seen since is "not confirmed", not "recovered". Partial success
// does not lift a group either: one object still failing keeps it blocked.
func TestRecoveryIsReadFromTheObjectsNotTheDependency(t *testing.T) {
	fresh := now.Add(-time.Minute)
	stale := now.Add(-RecentSkipWindow - time.Minute)
	failing := func(qg string, at time.Time) Anomaly {
		return Anomaly{QueryGroup: qg, Kind: KindDegradedRun, ReasonCode: "error", Since: now.Add(-time.Hour), ReasonLastAt: at,
			Failure:   &FailureRef{Stage: "execute", Category: "source_backend", Code: "QUERY_TIMEOUT"},
			LastError: &LastError{Text: "context deadline exceeded", At: at}}
	}
	completing := func(qg string, at time.Time) Anomaly {
		return Anomaly{QueryGroup: qg, Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT",
			Since: now.Add(-time.Hour), ReasonLastAt: at, LastHealthyAt: now.Add(-3 * time.Hour)}
	}
	cases := []struct {
		name string
		rows []Anomaly
		want Recovery
		// what the counts behind it say
		failingNow, completingNow, silent int
	}{
		{"failing within the window", []Anomaly{failing("a", fresh)}, RecoveryBlocked, 1, 0, 0},
		{"rounds ending within the window without a usable result", []Anomaly{completing("a", fresh)}, RecoveryRecovering, 0, 1, 0},
		{"nothing heard within the window is not recovered", []Anomaly{failing("a", stale)}, RecoveryUnconfirmed, 0, 0, 1},
		{"one still failing keeps the group blocked", []Anomaly{completing("a", fresh), failing("b", fresh)}, RecoveryBlocked, 1, 1, 0},
		{"one ending, one silent: recovering, not confirmed for all", []Anomaly{completing("a", fresh), failing("b", stale)}, RecoveryRecovering, 0, 1, 1},
	}
	for _, tc := range cases {
		rows := tc.rows
		Attribute(rows, now)
		reports := ReportChecks([][]Anomaly{rows}, nil, nil, now)
		if len(reports) != 1 || len(reports[0].Groups) != 1 {
			t.Fatalf("%s: reports = %+v, want one line with one problem", tc.name, reports)
		}
		group := reports[0].Groups[0]
		if group.Recovery != tc.want || group.FailingNow != tc.failingNow || group.CompletingNow != tc.completingNow || group.Silent != tc.silent {
			t.Errorf("%s: recovery = %s (failing %d, completing %d, silent %d), want %s (%d, %d, %d)", tc.name,
				group.Recovery, group.FailingNow, group.CompletingNow, group.Silent, tc.want, tc.failingNow, tc.completingNow, tc.silent)
		}
	}
	// The clocks on the fold: the latest failure, and the latest healthy
	// completion any of its objects had -- older than the failure, which is
	// exactly why it is not a recovery.
	rows := []Anomaly{completing("a", fresh), failing("b", fresh)}
	Attribute(rows, now)
	group := ReportChecks([][]Anomaly{rows}, nil, nil, now)[0].Groups[0]
	if group.LastFailure == nil || !group.LastFailure.Equal(fresh) || group.LastSuccess == nil || !group.LastSuccess.Equal(now.Add(-3*time.Hour)) {
		t.Fatalf("clocks = last failure %v, last success %v, want %v and %v", group.LastFailure, group.LastSuccess, fresh, now.Add(-3*time.Hour))
	}
	if group.Retrying != 1 {
		t.Fatalf("retrying = %d, want the one failing round counted as a retry", group.Retrying)
	}
}

// Records of loss read by their own clock: a skip within the window is a
// problem still blocked, a stopped one is history -- and the retained fold
// never claims the process saw nothing succeed, since its objects usually
// run normally now.
func TestRecordFoldsReadHistoryFromTheSkipsClock(t *testing.T) {
	view := &View{
		Replicas: []string{"pod-a"},
		GapSkips: map[string]SkippedSpan{
			"qg-now":  {At: now.Add(-2 * time.Minute), Replica: "pod-a", Slots: 3, FirstSlot: 1, LastSlot: 3},
			"qg-then": {At: now.Add(-2 * time.Hour), Replica: "pod-a", Slots: 3, FirstSlot: 1, LastSlot: 3},
		},
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: now.Add(-24 * time.Hour)}},
	}
	reports := ReportChecks(nil, nil, view, now)
	if len(reports) != 1 || reports[0].Code != CheckDetectionAbandoned {
		t.Fatalf("reports = %+v, want the record line", reports)
	}
	states := map[string]Recovery{}
	for _, group := range reports[0].Groups {
		states[group.Key] = group.Recovery
		if group.LastSuccess != nil {
			t.Errorf("record fold %s claims a last success: %+v", group.Key, group)
		}
	}
	if states[string(LossOngoing)] != RecoveryBlocked || states[string(LossHistorical)] != RecoveryHistorical {
		t.Fatalf("record folds = %v, want the loss in progress blocked and the stopped one historical", states)
	}
}

// Every recovery state has a producer, or is named as waiting for one: a
// state on the page's list with nothing producing it would read as a
// mechanism that is wired. RECOVERED's producer is the recoveries the
// trackers record at the healthy completion, carried on the view.
func TestEveryRecoveryStateIsProducedOrNamedAsWaiting(t *testing.T) {
	produced := map[Recovery]bool{}
	fresh := now.Add(-time.Minute)
	stale := now.Add(-RecentSkipWindow - time.Minute)
	for _, rows := range [][]Anomaly{
		{{QueryGroup: "a", Kind: KindDegradedRun, ReasonCode: "error", ReasonLastAt: fresh, LastError: &LastError{Text: "x", At: fresh}}},
		{{QueryGroup: "a", Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT", ReasonLastAt: fresh}},
		{{QueryGroup: "a", Kind: KindDegradedRun, ReasonCode: "error", ReasonLastAt: stale, LastError: &LastError{Text: "x", At: stale}}},
	} {
		Attribute(rows, now)
		for _, report := range ReportChecks([][]Anomaly{rows}, nil, nil, now) {
			for _, group := range report.Groups {
				produced[group.Recovery] = true
			}
		}
	}
	view := &View{Replicas: []string{"pod-a"}, GapSkips: map[string]SkippedSpan{"qg-then": {At: now.Add(-2 * time.Hour), Replica: "pod-a"}},
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: now.Add(-24 * time.Hour)}},
		Recovered: []RecoveredProblem{{Check: CheckDependencyDown, Key: "COMMIT/REDIS/UNAVAILABLE", Objects: 3,
			FirstFailure: now.Add(-30 * time.Minute), LastFailure: now.Add(-12 * time.Minute),
			FirstRecovery: now.Add(-11 * time.Minute), LastRecovery: now.Add(-10 * time.Minute)}}}
	for _, report := range ReportChecks(nil, nil, view, now) {
		for _, group := range report.Groups {
			produced[group.Recovery] = true
		}
	}
	waiting := map[Recovery]bool{}
	for _, state := range RecoveryStatesWithoutAProducer {
		waiting[state] = true
	}
	for _, state := range RecoveryStates {
		switch {
		case produced[state] && waiting[state]:
			t.Errorf("%s is produced and also listed as having no producer", state)
		case !produced[state] && !waiting[state]:
			t.Errorf("%s has no producer and is not listed as waiting for one: it reads as wired", state)
		}
	}
}

// The reason's clock has two ends on the row: when the reason began and
// the latest round that said it. The second is what "still happening" is
// read from, and it advances with every round while the first stays.
func TestTheRowCarriesBothEndsOfTheReasonsClock(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds+2; round++ {
		tracker.Observe(context.Background(), completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8709"))
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || !rows[0].ReasonSince.Equal(now) || !rows[0].ReasonLastAt.Equal(at.at.Add(-time.Minute)) {
		t.Fatalf("rows = %+v, want the reason since the first round and last said at the latest", rows)
	}
	if rows[0].Blocked == nil {
		Attribute(rows, at.at)
	}
	if rows[0].Blocked == nil || rows[0].Blocked.At == nil || !rows[0].Blocked.At.Equal(at.at.Add(-time.Minute)) {
		t.Fatalf("blocked = %+v, want its time read from the latest round", rows[0].Blocked)
	}
}
