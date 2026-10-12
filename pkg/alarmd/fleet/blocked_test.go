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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every code that reaches a line has a reading: the two tables are held to
// the same keys, both ways. A code with a check and no reading is a row
// whose "where is it stuck" is blank; a reading for a code no check knows
// is a reading nothing can reach.
func TestEveryCheckedCodeHasAReading(t *testing.T) {
	for code := range codeChecks {
		if _, read := failureFacets[code]; !read {
			t.Errorf("%s reaches a check and has no stage/class reading", code)
		}
	}
	viaFailureRef := map[string]bool{}
	for _, code := range failureRefCodes {
		viaFailureRef[code] = true
	}
	for code := range failureFacets {
		if _, checked := codeChecks[code]; !checked && !viaFailureRef[code] {
			t.Errorf("%s has a reading and reaches no check and no failure reference", code)
		}
	}
	// And every reading is on the closed lists the page has words for.
	for code, reading := range failureFacets {
		if !containsStage(reading.stage) || !containsClass(reading.class) || (reading.dependency != "" && !containsDependency(reading.dependency)) {
			t.Errorf("%s reads as %+v, off the closed lists", code, reading)
		}
		if reading.dependency == DependencyUnlocated {
			t.Errorf("%s names UNLOCATED as a dependency; leave it empty and let the text decide", code)
		}
	}
}

// The first accuracy constraint: a code does not name a dependency it only
// implies. A query timeout is the query step timing out, and whether the
// time went to the backend, the network or the budget is not in the code,
// so the dependency stays unlocated -- and the reading says so rather than
// pointing the reader at a backend that may be fine.
func TestAmbiguousCodesLeaveTheDependencyUnlocated(t *testing.T) {
	for _, code := range []string{"QUERY_TIMEOUT", "QUERY_UNAVAILABLE", "QUERY_PARTIAL", "QUERY_NOT_READY", "LATE_OUT_OF_WINDOW",
		"SNAPSHOT_UNAVAILABLE", "STATE_WRITE_RETRYABLE", "ACTIVATION_READ_FAILED", "PROGRESS_BEGIN_FAILED", "SLOT_SOURCE_RETRY", "source_error"} {
		row := Anomaly{Kind: KindDegradedRun, CauseReason: code}
		Attribute([]Anomaly{row}, now)
		blocked := blockedOf(row, ScheduleOnTime)
		if blocked == nil || blocked.Dependency != DependencyUnlocated || blocked.DependencyEvidence != "" {
			t.Errorf("%s: dependency = %+v, want UNLOCATED with no evidence", code, blocked)
		}
		if blocked != nil && blocked.Stage == StageUnlocated {
			t.Errorf("%s: stage unlocated; the step is in the code even when the dependency is not", code)
		}
	}
	// Codes that name the dependency name it, and say it was the code.
	for code, want := range map[string]Dependency{"REDIS_UNAVAILABLE": DependencyRedis, "KAFKA_UNAVAILABLE": DependencyKafka,
		"PROVIDER_UNAVAILABLE": DependencyQueryBackend, "EXECUTION_BUDGET_EXHAUSTED": DependencyNone, "ACTIVATION_MISSING": DependencyRedis} {
		blocked := blockedOf(Anomaly{Kind: KindDegradedRun, CauseReason: code}, ScheduleOnTime)
		if blocked.Dependency != want || blocked.DependencyEvidence != dependencyByCode {
			t.Errorf("%s: dependency = %s by %q, want %s by code", code, blocked.Dependency, blocked.DependencyEvidence, want)
		}
	}
}

// The error's own text names the dependency where the code does not: the
// Redis client's prefix, the server's LOADING reply. A live outage had
// SNAPSHOT_UNAVAILABLE on every row with "LOADING Redis is loading the
// dataset in memory" in the text, which is the state store saying so.
func TestTheErrorTextNamesTheDependencyTheCodeDoesNot(t *testing.T) {
	at := now.Add(-time.Minute)
	cases := []struct {
		name string
		row  Anomaly
		want Dependency
		by   string
	}{
		{"go-redis prefix in the round's error", Anomaly{Kind: KindDegradedRun, CauseReason: "SNAPSHOT_UNAVAILABLE",
			LastError: &LastError{Text: "alarmd progress: commit: redis: connection pool timeout", At: at}}, DependencyRedis, dependencyByText},
		{"the server's LOADING reply", Anomaly{Kind: KindDegradedRun, ReasonCode: "error",
			LastError: &LastError{Text: "LOADING Redis is loading the dataset in memory", At: at}}, DependencyRedis, dependencyByText},
		{"the query failure's detail", Anomaly{Kind: KindDegradedRun, CauseReason: "STATE_WRITE_RETRYABLE", ReasonLastAt: at,
			Failure: &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout", At: &at}}, DependencyRedis, dependencyByText},
		{"a timeout whose text names nothing stays unlocated", Anomaly{Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT",
			LastError: &LastError{Text: "context deadline exceeded", At: at}}, DependencyUnlocated, ""},
		{"the code wins over the text when it names one", Anomaly{Kind: KindDegradedRun, CauseReason: "KAFKA_UNAVAILABLE",
			LastError: &LastError{Text: "redis: something unrelated", At: at}}, DependencyKafka, dependencyByCode},
	}
	for _, tc := range cases {
		blocked := blockedOf(tc.row, ScheduleOnTime)
		if blocked == nil || blocked.Dependency != tc.want || blocked.DependencyEvidence != tc.by {
			t.Errorf("%s: blocked = %+v, want dependency %s by %q", tc.name, blocked, tc.want, tc.by)
		}
	}
}

// Every field of the reading comes from the row's latest round. The error's
// words and the query failure's detail are kept until a healthy completion,
// so on a row whose latest round ended some other way they describe an
// earlier round: read as current, a new reason borrowed an old Redis error
// as its dependency, and a round that had just ended was called a retry and
// then silence. A failure reference from before the timestamp existed is
// not current either -- it cannot say when it was, so it says nothing.
func TestTheReadingIsTakenFromTheLatestRoundOnly(t *testing.T) {
	old := now.Add(-20 * time.Minute)
	fresh := now.Add(-time.Minute)
	stale := &LastError{Text: "alarmd progress: commit: redis: connection pool timeout", At: old, Operation: "commit"}
	row := Anomaly{Kind: KindDegradedRun, Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT", ReasonLastAt: fresh, LastError: stale,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout", At: &old}}
	blocked := blockedOf(row, ScheduleOnTime)
	if blocked.Dependency != DependencyUnlocated || blocked.DependencyEvidence != "" || blocked.Text != "" || blocked.Operation != "" {
		t.Fatalf("blocked = %+v, want the old error's words and dependency left out of this round's reading", blocked)
	}
	if blocked.At == nil || !blocked.At.Equal(fresh) || blocked.Effect != EffectUnconfirmed || blocked.Retrying {
		t.Fatalf("blocked = %+v, want the latest round's time and its effect (a round that ended, unconfirmed), not a retry", blocked)
	}
	// The same error on the round that is the latest: read in full.
	current := row
	current.ReasonLastAt, current.LastError = old, stale
	current.Cause, current.CauseReason = "", ""
	current.ReasonCode = "error"
	blocked = blockedOf(current, ScheduleOnTime)
	if blocked.Dependency != DependencyRedis || blocked.DependencyEvidence != dependencyByText || blocked.Operation != "commit" ||
		blocked.At == nil || !blocked.At.Equal(old) || blocked.Effect != EffectRetrying {
		t.Fatalf("blocked = %+v, want the error read in full when its round is the latest", blocked)
	}
	// A failure reference without a time is from a publisher that could not
	// say when it was: not this round's evidence.
	undated := Anomaly{Kind: KindDegradedRun, CauseReason: "STATE_WRITE_RETRYABLE", ReasonLastAt: fresh,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout"}}
	if blocked := blockedOf(undated, ScheduleOnTime); blocked.Dependency != DependencyUnlocated || blocked.Text != "" {
		t.Fatalf("blocked = %+v, want an undated failure reference left out", blocked)
	}
}

// What the failure did to the detection, one effect per shape of evidence,
// in the order that separates them. The third accuracy constraint is the
// SKIPPED case: only a persisted skip record is a confirmed loss; a round
// that failed will run again, and a round that ended without a usable
// result is unconfirmed, not lost.
func TestEffectIsReadFromTheRowsEvidence(t *testing.T) {
	at := now.Add(-time.Minute)
	cases := []struct {
		name     string
		row      Anomaly
		schedule Schedule
		want     Effect
	}{
		{"a persisted skip record", Anomaly{Kind: KindSkippedSpan, ReasonCode: "GAP_SKIPPED", Skip: &SkippedSpan{At: at, Slots: 3}}, ScheduleOnTime, EffectSkipped},
		{"a skip record on a demoted row outranks the cooldown", Anomaly{Kind: KindQueryCooldown, QueryCooldown: &cooldownFacts, Skip: &SkippedSpan{At: at}}, ScheduleOnTime, EffectSkipped},
		{"a round that failed will run again", Anomaly{Kind: KindDegradedRun, ReasonCode: "error", ReasonLastAt: at, LastError: &LastError{Text: "x", At: at}}, ScheduleOnTime, EffectRetrying},
		{"a cooldown is a retry scheduled", Anomaly{Kind: KindQueryCooldown, QueryCooldown: &cooldownFacts}, ScheduleOnTime, EffectRetrying},
		{"a round nothing came of", Anomaly{Kind: KindBlockedRun, ReasonCode: "source_error"}, ScheduleOnTime, EffectRetrying},
		{"a stalled round", Anomaly{Kind: KindDegradedRun, Stalled: true, ReasonCode: "error"}, ScheduleOnTime, EffectRetrying},
		{"a degraded completion is a result nobody can rely on", Anomaly{Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{Levels: 1, Short: 1}}, ScheduleOnTime, EffectUnconfirmed},
		{"a state restored without its cause", Anomaly{Kind: KindDegradedRun, ReasonCode: "LEVEL_OUTCOME_UNKNOWN", SinceFrom: SinceSnapshotContinuity}, ScheduleOnTime, EffectUnconfirmed},
		{"late with nothing else wrong", Anomaly{Kind: KindOverdueWake, ReasonCode: ReasonWakeMissed}, ScheduleOverdue, EffectDelayed},
		{"overdue with a failure is the failure's effect", Anomaly{Kind: KindDegradedRun, ReasonCode: "error", LastError: &LastError{Text: "x", At: at}}, ScheduleOverdue, EffectRetrying},
	}
	for _, tc := range cases {
		blocked := blockedOf(tc.row, tc.schedule)
		if blocked == nil || blocked.Effect != tc.want {
			t.Errorf("%s: blocked = %+v, want effect %s", tc.name, blocked, tc.want)
		}
		if blocked != nil && blocked.Retrying != (tc.want == EffectRetrying) {
			t.Errorf("%s: retrying = %v, want it to follow the effect", tc.name, blocked.Retrying)
		}
	}
	// Data that stopped is not this deployment stuck anywhere.
	if blocked := blockedOf(Anomaly{Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED"}, ScheduleOnTime); blocked != nil {
		t.Errorf("a no-data row reads as blocked: %+v", blocked)
	}
}

var cooldownFacts = observability.QueryCooldownFacts{Event: "entered", Until: now.Add(5 * time.Minute), LastQueryAt: now.Add(-30 * time.Second), Failures: 1}

// The reading rides on the row the way the finding does: decided in
// Attribute from the same code the check was decided on, with the error's
// words, when it was seen, the last healthy completion this process saw,
// and the operation the failing round named. And it is on the wire under
// its own key, with the zero last-healthy time left off.
func TestAttributePutsTheReadingOnTheRow(t *testing.T) {
	at := now.Add(-2 * time.Minute)
	healthy := now.Add(-time.Hour)
	rows := []Anomaly{{Kind: KindDegradedRun, ReasonCode: "error", Since: at, ReasonSince: at,
		LastError:     &LastError{Text: "alarmd progress: commit: redis: connection pool timeout", Type: "*errors.errorString", At: at, Attempts: 3, Operation: "commit"},
		LastHealthyAt: healthy}}
	Attribute(rows, now)
	blocked := rows[0].Blocked
	if blocked == nil || blocked.Code != "error" || blocked.Stage != StageUnlocated || blocked.Class != ClassUnlocated {
		// "error" is the tracker's own outcome word: a round that reached
		// execution and returned an error. The word says nothing about the
		// step or the kind, and the reading says so rather than guessing;
		// the code is kept so the row names what nobody has read.
		t.Fatalf("blocked = %+v, want the outcome word kept and the step and kind unlocated", blocked)
	}
	if blocked.Dependency != DependencyRedis || blocked.DependencyEvidence != dependencyByText || blocked.Operation != "commit" ||
		!strings.Contains(blocked.Text, "connection pool timeout") || blocked.At == nil || !blocked.At.Equal(at) ||
		blocked.LastSuccessAt == nil || !blocked.LastSuccessAt.Equal(healthy) || !blocked.Retrying || blocked.Effect != EffectRetrying {
		t.Fatalf("blocked = %+v, want Redis by text, the commit operation, the words, the time, the last success, retrying", blocked)
	}
	encoded, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"blocked":{`, `"stage":"UNLOCATED"`, `"dependency":"REDIS"`, `"dependency_evidence":"text"`, `"effect":"RETRYING"`, `"last_healthy_at":"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("the row on the wire lacks %s:\n%s", want, encoded)
		}
	}
	// A restored row never saw a healthy completion: nothing on the wire
	// claims one.
	restored := []Anomaly{{Kind: KindDegradedRun, ReasonCode: "LEVEL_OUTCOME_UNKNOWN", SinceFrom: SinceSnapshotContinuity}}
	Attribute(restored, now)
	encoded, _ = json.Marshal(restored[0])
	if strings.Contains(string(encoded), "last_healthy_at") || restored[0].Blocked.LastSuccessAt != nil {
		t.Errorf("a restored row claims a last healthy completion:\n%s", encoded)
	}
	// A code nobody has read: every axis unlocated, the code kept so the
	// row says which one.
	unread := []Anomaly{{Kind: KindDegradedRun, CauseReason: "SOMETHING_NEW"}}
	Attribute(unread, now)
	if b := unread[0].Blocked; b == nil || b.Code != "SOMETHING_NEW" || b.Stage != StageUnlocated || b.Dependency != DependencyUnlocated || b.Class != ClassUnlocated {
		t.Errorf("an unread code = %+v, want every axis UNLOCATED and the code named", b)
	}
}

// The tracker keeps the last healthy completion across the run resets, so
// the next failure's row carries it; and never invents one for an object
// it has only seen fail.
func TestTrackerKeepsTheLastHealthyCompletionAcrossRuns(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	fail := func(tracker *Tracker, queryGroup string, slot int64, text string) {
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", Err: errors.New(text), Operation: "commit",
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
		})
	}
	tracker.Observe(context.Background(), completion("qg-1", "FULL_COMPLETED", "852"))
	healthyAt := at.at
	at.at = at.at.Add(time.Minute)
	for round := 0; round < DefaultDegradedRounds; round++ {
		fail(tracker, "qg-1", 160, "redis: connection pool timeout")
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || !rows[0].LastHealthyAt.Equal(healthyAt) || rows[0].LastError == nil || rows[0].LastError.Operation != "commit" {
		t.Fatalf("rows = %+v, want the failure row carrying the healthy completion a minute earlier and the commit operation", rows)
	}
	never := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		fail(never, "qg-2", 100, "boom")
	}
	if rows := never.Anomalies(); len(rows) != 1 || !rows[0].LastHealthyAt.IsZero() {
		t.Fatalf("rows = %+v, want no healthy completion claimed for an object only ever seen failing", rows)
	}
}

// A failure the pipeline classified reaches the step through its category
// when no code names it: the evaluation that errored is the evaluate step,
// the permit wait that ended at the deadline is the query step. The class
// stays with the code, and only the code.
func TestTheFailureCategoryNamesTheStepWhenNoCodeDoes(t *testing.T) {
	for _, tc := range []struct {
		category string
		code     string
		want     Stage
	}{
		{"evaluation", "SOMETHING_THE_EVALUATOR_SAID", StageEvaluate},
		{"admission", "SOME_ADMISSION_CODE", StageQuery},
		{"provider_transport", "TRANSPORT_X", StageQuery},
		{"completion_contract", "CONTRACT_Y", StageEvaluate},
	} {
		row := Anomaly{Kind: KindDegradedRun, ReasonCode: "error", Failure: &FailureRef{Stage: "execute", Category: tc.category, Code: tc.code}}
		blocked := blockedOf(row, ScheduleOnTime)
		if blocked.Stage != tc.want || blocked.Code != tc.code || blocked.Class != ClassUnlocated {
			t.Errorf("%s/%s: blocked = %+v, want stage %s from the category, the code named, the class unlocated", tc.category, tc.code, blocked, tc.want)
		}
	}
	// A code with a reading is not overridden by the category.
	row := Anomaly{Kind: KindDegradedRun, ReasonCode: "error", Failure: &FailureRef{Stage: "execute", Category: "evaluation", Code: "GAP_GUARD_CONFLICT"}}
	if blocked := blockedOf(row, ScheduleOnTime); blocked.Stage != StageEvaluate || blocked.Class != ClassContract || blocked.Code != "GAP_GUARD_CONFLICT" {
		t.Errorf("a read code under a category = %+v, want the code's reading", blocked)
	}
}

func containsStage(stage Stage) bool {
	for _, known := range Stages {
		if known == stage {
			return true
		}
	}
	return false
}

func containsClass(class Class) bool {
	for _, known := range Classes {
		if known == class {
			return true
		}
	}
	return false
}

func containsDependency(dependency Dependency) bool {
	for _, known := range Dependencies {
		if known == dependency {
			return true
		}
	}
	return false
}

// The tracker stamps the query failure with when it was seen and which Slot
// it was on, so the reading can tell this round's failure from one kept
// since an earlier round -- and use this round's detail for the dependency.
//
// The clock runs: the failure is observed on its way to the round's end, so
// it is stamped a moment before the completion that ends the same Slot.
// Frozen, the clock hid that order, and a reading that compared the two
// stamps dropped every real failure filed one millisecond before its own
// round completed -- the row then said the round was stuck with nothing to
// show for it.
func TestTrackerStampsTheQueryFailureWithItsTimeAndSlot(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	var failedAt time.Time
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		failedAt = at.at
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout"},
			Trace:        observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN", ProgressCompletionReason: "STATE_WRITE_RETRYABLE",
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].Failure == nil || rows[0].Failure.At == nil || !rows[0].Failure.At.Equal(failedAt) || rows[0].Failure.Slot != 100+60*(DefaultDegradedRounds-1) {
		t.Fatalf("rows = %+v, want the failure reference stamped with the time it was seen and the Slot it was on", rows)
	}
	if rows[0].RoundSlot != rows[0].Failure.Slot {
		t.Fatalf("round slot = %d, failure slot = %d, want the row's latest round to be the Slot the failure was on", rows[0].RoundSlot, rows[0].Failure.Slot)
	}
	Attribute(rows, at.at)
	blocked := rows[0].Blocked
	if blocked == nil || blocked.Dependency != DependencyRedis || blocked.DependencyEvidence != dependencyByText || blocked.Text != "dial tcp 10.0.0.1:6379: i/o timeout" {
		t.Fatalf("blocked = %+v, want Redis named from this round's failure detail, and the detail as the round's words", blocked)
	}
	// The round ended -- degraded, but ended -- so it is not being retried,
	// whatever the failure on its way there said.
	if blocked.Effect != EffectUnconfirmed {
		t.Fatalf("effect = %s, want UNCONFIRMED for a round that ended degraded after its own failure", blocked.Effect)
	}
}

// A Slot that failed and was then retried to a degraded completion is one
// round with an error in its history and an end: the error is that round's
// words, and the round is over, not being retried. The effect is read from
// how the round ended, not from the error being this round's -- the two
// used to be one flag.
func TestARoundThatEndedAfterItsErrorIsNotBeingRetried(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	slot := int64(1_700_000_000)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", Err: errors.New("alarmd worker: commit: redis: connection pool timeout"),
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(30 * time.Second)
	}
	rows := tracker.Anomalies()
	Attribute(rows, at.at)
	if len(rows) != 1 || rows[0].Blocked == nil || rows[0].Blocked.Effect != EffectRetrying || rows[0].Blocked.Dependency != DependencyRedis {
		t.Fatalf("rows = %+v, want the failing Slot read as retrying against Redis", rows)
	}
	// The retry of the same Slot ends it, degraded.
	tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "STATE_WRITE_RETRYABLE",
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
	})
	rows = tracker.Anomalies()
	Attribute(rows, at.at)
	blocked := rows[0].Blocked
	if blocked == nil || blocked.Effect != EffectUnconfirmed || blocked.Retrying {
		t.Fatalf("blocked = %+v, want the ended round not read as retrying", blocked)
	}
	if blocked.Text != "alarmd worker: commit: redis: connection pool timeout" || blocked.Dependency != DependencyRedis {
		t.Fatalf("blocked = %+v, want the error kept as this round's words, it was this Slot's", blocked)
	}
}

// A timeout's row carries this round's query read against its budget, from
// the observation that reported the failure to the blocked reading: begun
// 11 s after its 30 s settling wait with 9 s left, and all of it used,
// reads as a query begun late - which UNLOCATED alone could not say. A
// failure kept from an earlier Slot times nothing on this round's row.
func TestATimeoutsRowCarriesThisRoundsQueryAgainstItsBudget(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	timing := observability.QueryTiming{SettleMillis: 30_000, StartLateMillis: 11_000, BudgetMillis: 9_000, ElapsedMillis: 9_004}
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		reported := timing
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_TIMEOUT",
				Detail: "transport=timeout", Timing: &reported},
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "QUERY_TIMEOUT",
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	Attribute(rows, at.at)
	if len(rows) != 1 || rows[0].Failure == nil || rows[0].Failure.Timing == nil || *rows[0].Failure.Timing != timing {
		t.Fatalf("rows = %+v, want the failure carrying its timing", rows)
	}
	blocked := rows[0].Blocked
	if blocked == nil || blocked.Timing == nil || *blocked.Timing != timing || blocked.Text != "transport=timeout" {
		t.Fatalf("blocked = %+v, want this round's timing beside its detail", blocked)
	}
	if blocked.Timing == rows[0].Failure.Timing {
		t.Fatal("the blocked reading aliases the failure's timing")
	}

	earlier := rows[0]
	failure := *earlier.Failure
	failure.Slot = earlier.RoundSlot - 60
	earlier.Failure = &failure
	if blocked := blockedOf(earlier, ""); blocked.Timing != nil {
		t.Fatalf("blocked = %+v, want no timing from an earlier Slot's failure", blocked)
	}
}

// Two Slots that are known and differ are two rounds, whatever the clocks
// say: a failure stamped after the latest round ended, on the next Slot, is
// the next round's -- in flight -- and not this one's. The clock decides
// only when a Slot is missing, which a publisher before the field is.
func TestKnownAndDifferentSlotsAreTwoRoundsWhateverTheClocksSay(t *testing.T) {
	later := now.Add(time.Second)
	inFlight := Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", CauseReason: "HISTORY_GAPPED",
		ReasonLastAt: now, RoundSlot: 1000,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout", At: &later, Slot: 1060}}
	blocked := blockedOf(inFlight, "")
	if blocked.Dependency == DependencyRedis || blocked.Text != "" {
		t.Fatalf("blocked = %+v, want the next Slot's failure not read as this round's although stamped later", blocked)
	}
	// The same row from a publisher that stamps no Slot: the clock decides,
	// as it did before the field existed.
	unslotted := inFlight
	unslotted.RoundSlot = 0
	unslotted.Failure = &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout", At: &later}
	if blocked := blockedOf(unslotted, ""); blocked.Dependency != DependencyRedis {
		t.Fatalf("blocked = %+v, want the clock to decide when no Slot is known", blocked)
	}
}

// The cause describes the last round that completed. When the latest round
// failed instead, and the failure named this round, the failure decides the
// line and the reading: a row whose last completion was skipped past the
// replay window and whose rounds since are refused by a gap guard is the
// refusal, not the skip. A completion after the failure puts the cause
// back first; a failure kept from an earlier Slot never comes first.
func TestThisRoundsFailureOutranksTheLastCompletionsCause(t *testing.T) {
	failedAt := now
	refusedAfterSkip := Anomaly{Kind: KindDegradedRun, ReasonCode: "error", CauseReason: "GAP_SKIPPED", ReasonLastAt: now, RoundSlot: 1060,
		Failure:   &FailureRef{Stage: "other", Category: "completion_contract", Code: "GAP_GUARD_CONFLICT", At: &failedAt, Slot: 1060},
		LastError: &LastError{Text: "alarmd worker: finalize query-free Slot: gap marker conflicts", At: now, EvaluationTime: 1060}}
	rows := []Anomaly{refusedAfterSkip}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDefect || rows[0].Finding.Group != "EVALUATE/NONE/CONTRACT" || rows[0].Blocked == nil || rows[0].Blocked.Code != "GAP_GUARD_CONFLICT" {
		t.Fatalf("refused after a skip = %+v / %+v, want the refusal to decide the line and the reading", rows[0].Finding, rows[0].Blocked)
	}
	// The latest round completed -- degraded, skipped again: the cause is
	// this round's and comes first again.
	skippedAgain := refusedAfterSkip
	skippedAgain.ReasonCode = "GAP_SKIPPED"
	skippedAgain.RoundSlot = 1120
	rows = []Anomaly{skippedAgain}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDetectionAbandoned || rows[0].Blocked == nil || rows[0].Blocked.Code != "GAP_SKIPPED" {
		t.Fatalf("skipped again = %+v / %+v, want the completion's cause first again", rows[0].Finding, rows[0].Blocked)
	}
	// The latest round failed, and the only failure the row keeps is an
	// earlier Slot's: neither it nor the old completion's cause is this
	// round's, so the round is what it is -- a failure nobody named.
	staleFailure := refusedAfterSkip
	staleFailure.RoundSlot = 1120
	rows = []Anomaly{staleFailure}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDefect || rows[0].Finding.Group != "UNLOCATED/UNLOCATED/UNLOCATED" || rows[0].Blocked == nil || rows[0].Blocked.Code != "error" {
		t.Fatalf("failed with only a stale failure = %+v / %+v, want an unnamed failure, not the old cause or the old failure", rows[0].Finding, rows[0].Blocked)
	}
	// A refusal the backend gave an earlier Slot does not make this round's
	// unnamed failure a refusal at the query step either.
	staleRefusal := staleFailure
	staleRefusal.Failure = &FailureRef{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE", Detail: "http_status=400", At: &failedAt, Slot: 1060}
	rows = []Anomaly{staleRefusal}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDefect || rows[0].Blocked == nil || rows[0].Blocked.Class == ClassRefused || rows[0].Blocked.Dependency == DependencyQueryBackend {
		t.Fatalf("failed with only a stale refusal = %+v / %+v, want no refusal reading borrowed from another Slot", rows[0].Finding, rows[0].Blocked)
	}
	// The latest round completed after a failure on the same Slot -- the
	// retry got through, degraded: the completion's cause is the round's
	// verdict and the failure on the way is its history, not its line.
	completedAfterFailure := Anomaly{Kind: KindDegradedRun, ReasonCode: "COMPLETED_WITH_UNAVAILABLE", CauseReason: "HISTORY_GAPPED", ReasonLastAt: now, RoundSlot: 1060,
		Failure: &FailureRef{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", At: &failedAt, Slot: 1060}}
	rows = []Anomaly{completedAfterFailure}
	Attribute(rows, now)
	if rows[0].Finding.Check == CheckDependencyDown || rows[0].Blocked == nil || rows[0].Blocked.Code != "HISTORY_GAPPED" {
		t.Fatalf("completed after a failure = %+v / %+v, want the completion's cause to decide, not the failure on the way", rows[0].Finding, rows[0].Blocked)
	}
}

// Through the tracker, the shape a live object had: a Slot skipped past the
// replay window, then the next Slot failing with an error the terminal did
// not name. The row is not the skip -- that round is over and its record is
// kept apart -- and the error's words are this round's, so the row is an
// unnamed failure at an unlocated step, not "检测已停" with a contract
// error beside it.
func TestAnUnnamedFailureAfterASkipIsNotExplainedByTheSkip(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionReason: "GAP_SKIPPED",
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: int64(1000 + 60*round)},
		})
		at.at = at.at.Add(time.Minute)
	}
	tracker.Observe(context.Background(), observability.Observation{
		ExecuteOutcome: "error", ReasonCode: "internal_unknown",
		Err:   errors.New("alarmd worker: invalid query result: alarmd worker: invalid series evaluation: alarmd execution: degraded Level outcome lacks an exact durable guard"),
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: 1180},
	})
	rows := tracker.Anomalies()
	Attribute(rows, at.at)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one object", rows)
	}
	row := rows[0]
	if row.Finding.Check != CheckDefect || row.Finding.Group != "UNLOCATED/UNLOCATED/UNLOCATED" {
		t.Fatalf("finding = %+v, want an unnamed failure under DEFECT at no located step, not the skip's line", row.Finding)
	}
	if row.Blocked == nil || row.Blocked.Code == "GAP_SKIPPED" || row.Blocked.Stage == StageSchedule || !strings.Contains(row.Blocked.Text, "durable guard") {
		t.Fatalf("blocked = %+v, want this round's words and no reading borrowed from the skip", row.Blocked)
	}
}

// A failure on the previous Slot is the previous round's: the next round
// completing on a new Slot with a different reason does not borrow it, which
// is the guarantee the Slot comparison has to keep from the clock comparison
// it replaces.
func TestAFailureOnThePreviousSlotIsNotLentToTheNextRound(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "HISTORY_GAPPED",
			Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	// This round: a Redis failure on the way to the completion.
	tracker.Observe(context.Background(), observability.Observation{
		QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "other", Code: "STATE_WRITE_RETRYABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout"},
		Trace:        observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: 1000},
	})
	at.at = at.at.Add(time.Millisecond)
	tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "STATE_WRITE_RETRYABLE",
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: 1000},
	})
	rows := tracker.Anomalies()
	Attribute(rows, at.at)
	if len(rows) != 1 || rows[0].Blocked == nil || rows[0].Blocked.Dependency != DependencyRedis {
		t.Fatalf("rows = %+v, want this round's Redis failure read as this round's", rows)
	}
	// The next round, on the next Slot, completes with a window reason and no
	// failure of its own: the Redis detail stays with Slot 1000.
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "HISTORY_GAPPED",
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", EvaluationTime: 1060},
	})
	rows = tracker.Anomalies()
	Attribute(rows, at.at)
	if len(rows) != 1 || rows[0].RoundSlot != 1060 || rows[0].Failure == nil || rows[0].Failure.Slot != 1000 {
		t.Fatalf("rows = %+v, want the row on Slot 1060 still carrying the Slot 1000 failure reference", rows)
	}
	if rows[0].Blocked == nil || rows[0].Blocked.Dependency == DependencyRedis || rows[0].Blocked.Text != "" {
		t.Fatalf("blocked = %+v, want the previous Slot's Redis failure not lent to this round", rows[0].Blocked)
	}
}

// A round the Slot source or the view refused never completes, so its words
// arrive only on the run's own outcome. They reach the row and its reading as
// this round's, counted while they repeat; a later refusal that brings no
// words does not borrow the earlier ones as its own.
func TestARefusedRoundKeepsTheWordsItWasRefusedWith(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 20, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return now })
	refused := errors.New("alarmd scheduler: Snapshot retention cannot cover recovery contract")
	refuse := func(err error) {
		now = now.Add(time.Second)
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
			Result: observability.ResultTerminal, RunOutcome: "source_error", Err: err,
			Trace: observability.TraceFields{QueryGroupKey: "qg"},
		})
	}
	for round := 0; round < DefaultBlockedRounds+1; round++ {
		refuse(refused)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].LastError == nil || rows[0].LastError.Text != refused.Error() ||
		rows[0].LastError.Attempts != DefaultBlockedRounds+1 {
		t.Fatalf("rows = %+v, want the refusal's words, %d in a row", rows, DefaultBlockedRounds+1)
	}
	if blocked := blockedOf(rows[0], ScheduleOnTime); blocked == nil || blocked.Text != refused.Error() {
		t.Fatalf("blocked = %+v, want the refusal read as this round's", blocked)
	}

	// Other words start their own count.
	other := errors.New("alarmd scheduler: frozen schedule segment is unreadable")
	refuse(other)
	rows = tracker.Anomalies()
	if len(rows) != 1 || rows[0].LastError == nil || rows[0].LastError.Text != other.Error() || rows[0].LastError.Attempts != 1 {
		t.Fatalf("rows = %+v, want the new words counted from one", rows)
	}

	refuse(nil)
	rows = tracker.Anomalies()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if blocked := blockedOf(rows[0], ScheduleOnTime); blocked == nil || blocked.Text != "" {
		t.Fatalf("blocked = %+v, want a round without words read without the earlier ones", blocked)
	}
}

// The source answering that nothing is due ends a run the source or the view
// refused: the gate has let the round through and no Slot is owed. A refusal
// at startup that ends in seconds no longer stands as this deployment's own
// until the object's next round. Everything else stays as it was: a panic, an
// execution failure, a backoff that is not an answer, and a source that says
// "not due" with nothing refused before it.
func TestASourceAnsweringNotDueEndsTheRunItRefused(t *testing.T) {
	type tracked struct {
		tracker *Tracker
		now     *time.Time
	}
	start := func() tracked {
		now := time.Date(2026, 9, 28, 5, 20, 0, 0, time.UTC)
		return tracked{tracker: NewTracker(nil, "pod-a", func() time.Time { return now }), now: &now}
	}
	run := func(tr tracked, outcome string, err error) {
		*tr.now = tr.now.Add(time.Second)
		tr.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
			Result: observability.ResultTerminal, RunOutcome: outcome, Err: err,
			Trace: observability.TraceFields{QueryGroupKey: "qg"},
		})
	}
	refuse := func(tr tracked, outcome string, rounds int) {
		for round := 0; round < rounds; round++ {
			run(tr, outcome, errors.New(outcome))
		}
	}
	for _, outcome := range []string{"view_not_executable", "source_error", "source_retry", "source_blocked"} {
		t.Run(outcome, func(t *testing.T) {
			tr := start()
			refuse(tr, outcome, DefaultBlockedRounds+1)
			if rows := tr.tracker.Anomalies(); len(rows) != 1 || rows[0].Kind != KindBlockedRun {
				t.Fatalf("rows = %+v, want the refused run listed", rows)
			}
			run(tr, "source_not_due", nil)
			if rows := tr.tracker.Anomalies(); len(rows) != 0 {
				t.Fatalf("rows = %+v, want the run ended by the source's answer", rows)
			}
			if !tr.tracker.HasConclusion("qg") {
				t.Fatal("the object lost its conclusion")
			}
			// A refusal after the answer starts its own streak.
			refuse(tr, outcome, DefaultBlockedRounds)
			if rows := tr.tracker.Anomalies(); len(rows) != 1 || rows[0].Consecutive != DefaultBlockedRounds {
				t.Fatalf("rows = %+v, want a new streak of %d", rows, DefaultBlockedRounds)
			}
		})
	}
	t.Run("a panic is the round's own", func(t *testing.T) {
		tr := start()
		refuse(tr, "panic", DefaultBlockedRounds+1)
		run(tr, "source_not_due", nil)
		if rows := tr.tracker.Anomalies(); len(rows) != 1 || rows[0].ReasonCode != "panic" {
			t.Fatalf("rows = %+v, want the panic kept", rows)
		}
	})
	t.Run("a backoff is not an answer", func(t *testing.T) {
		tr := start()
		refuse(tr, "view_not_executable", DefaultBlockedRounds+1)
		run(tr, "source_backoff", nil)
		if rows := tr.tracker.Anomalies(); len(rows) != 1 || rows[0].ReasonCode != "view_not_executable" {
			t.Fatalf("rows = %+v, want the refusal kept through a backoff", rows)
		}
	})
	t.Run("an execution failure is not a refusal", func(t *testing.T) {
		tr := start()
		for round := 0; round < DefaultDegradedRounds+1; round++ {
			*tr.now = tr.now.Add(time.Second)
			tr.tracker.Observe(context.Background(), observability.Observation{
				ExecuteOutcome: "error", Err: errors.New("redis: connection pool timeout"), Operation: "commit",
				Trace: observability.TraceFields{QueryGroupKey: "qg", EvaluationTime: 160},
			})
		}
		before := tr.tracker.Anomalies()
		run(tr, "source_not_due", nil)
		if rows := tr.tracker.Anomalies(); len(before) != 1 || len(rows) != 1 || rows[0].Kind != before[0].Kind {
			t.Fatalf("rows = %+v (before %+v), want the failure kept", rows, before)
		}
	})
	t.Run("not due with nothing refused", func(t *testing.T) {
		tr := start()
		run(tr, "source_not_due", nil)
		if rows := tr.tracker.Anomalies(); len(rows) != 0 || tr.tracker.HasConclusion("qg") {
			t.Fatalf("rows = %+v, conclusion = %v, want nothing from an answer alone", rows, tr.tracker.HasConclusion("qg"))
		}
	})
}

// A not-due answer is not a round that went wrong: it neither starts a run nor
// lends its time to one. A refusal streak after it, or after a run it ended,
// is dated from its own first refusal; and an object the pool once exposed
// does not become a row because its source answered.
func TestANotDueAnswerStartsNoRunAndLendsNoStart(t *testing.T) {
	now := time.Date(2026, 9, 28, 5, 20, 0, 0, time.UTC)
	tracker := NewTracker(nil, "pod-a", func() time.Time { return now })
	run := func(queryGroup, outcome string) {
		now = now.Add(time.Second)
		var err error
		if outcome != "source_not_due" {
			err = errors.New(outcome)
		}
		tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
			Result: observability.ResultTerminal, RunOutcome: outcome, Err: err,
			Trace: observability.TraceFields{QueryGroupKey: queryGroup},
		})
	}
	sinceOf := func(queryGroup string) time.Time {
		for _, row := range tracker.Anomalies() {
			if row.QueryGroup == queryGroup {
				return row.Since
			}
		}
		t.Fatalf("%s is not listed: %+v", queryGroup, tracker.Anomalies())
		return time.Time{}
	}

	run("qg-a", "source_not_due")
	now = now.Add(10 * time.Minute)
	firstRefusal := now.Add(time.Second)
	for round := 0; round < DefaultBlockedRounds; round++ {
		run("qg-a", "source_error")
	}
	if since := sinceOf("qg-a"); !since.Equal(firstRefusal) {
		t.Fatalf("since = %v, want the first refusal %v, not the earlier answer", since, firstRefusal)
	}

	run("qg-a", "source_not_due")
	now = now.Add(time.Hour)
	secondStreak := now.Add(time.Second)
	for round := 0; round < DefaultBlockedRounds; round++ {
		run("qg-a", "view_not_executable")
	}
	if since := sinceOf("qg-a"); !since.Equal(secondStreak) {
		t.Fatalf("since = %v, want the new streak's first refusal %v, not the ended run's", since, secondStreak)
	}

	tracker.Observe(context.Background(), observability.Observation{QueryCooldown: &cooldownFacts,
		Trace: observability.TraceFields{QueryGroupKey: "qg-b"}})
	tracker.Observe(context.Background(), observability.Observation{
		QueryCooldown: &observability.QueryCooldownFacts{Event: "recovered"},
		Trace:         observability.TraceFields{QueryGroupKey: "qg-b"},
	})
	run("qg-b", "source_not_due")
	for _, row := range append(tracker.Anomalies(), tracker.Demoted()...) {
		if row.QueryGroup == "qg-b" {
			t.Fatalf("an object the pool once exposed became a row on a not-due answer: %+v", row)
		}
	}
}
