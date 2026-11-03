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
	"testing"
	"time"

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const defectSlot = int64(1_700_000_000)

// defectTracker is a tracker with a clock the test advances, and the two
// observations a round leaves: a failed terminal naming its reason, and a
// completion of some kind.
type defectTracker struct {
	t       *testing.T
	at      *clock
	tracker *Tracker
	group   string
}

func newDefectTracker(t *testing.T, group string) *defectTracker {
	at := &clock{at: now}
	return &defectTracker{t: t, at: at, tracker: newTracker(t, at), group: group}
}

func (d *defectTracker) tick() { d.at.at = d.at.at.Add(time.Minute) }

func (d *defectTracker) fail(slot int64, code, text string) {
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		ExecuteOutcome: "error", ReasonCode: observability.ReasonCode(code), Err: errors.New(text),
		Trace: observability.TraceFields{QueryGroupKey: d.group, StrategyID: "4101", BusinessID: "2", EvaluationTime: slot},
	})
}

func (d *defectTracker) complete(slot int64, kind string) {
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: kind, ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN", ProgressCompletionReason: "HISTORY_GAPPED",
		Trace: observability.TraceFields{QueryGroupKey: d.group, StrategyID: "4101", BusinessID: "2", EvaluationTime: slot},
	})
}

func (d *defectTracker) internal() *FailureRef {
	d.t.Helper()
	rows := append(d.tracker.Anomalies(), d.tracker.Undecidable()...)
	rows = append(rows, d.tracker.Demoted()...)
	for _, row := range rows {
		if row.QueryGroup == d.group {
			return row.Internal
		}
	}
	// Not listed anywhere: read the state directly, the way the publisher
	// would once the object is listed.
	d.tracker.mu.Lock()
	defer d.tracker.mu.Unlock()
	if state := d.tracker.groups[d.group]; state != nil {
		return state.internal
	}
	return nil
}

// ownDefectFold is the fold a row is under on its own DEFECT line while the
// failure is the latest round's: the blocked triple of the failure's code.
// Once later rounds complete, the same failure is the row's second line,
// folded by its code -- the recovery lands under whichever the row was on.
func ownDefectFold(code string) string {
	f := failureFacets[code]
	return string(f.stage) + "/" + string(f.dependency) + "/" + string(f.class)
}

func (d *defectTracker) recoveredUnderDefect(key string) *RecoveredProblem {
	for _, problem := range d.tracker.Recovered() {
		if problem.Check == CheckDefect && problem.Key == key {
			copy := problem
			return &copy
		}
	}
	return nil
}

// One state-version conflict fails one round; every round after it runs
// through the state apply and completes, degraded because the object's
// series are short. The DEFECT line's evidence of recovery is the failed
// stage passing, not a healthy completion: the object leaves DEFECT on the
// first round that ran through, the fold records it as recovered, and the
// object's own degraded row stays on its own line, without the conflict
// beside it. Before this the conflict stayed on the row until every series
// had data again, and the line read "recovering" for as long as that took.
func TestAOneOffInternalFailureIsPassedByTheNextRoundThatRunsThrough(t *testing.T) {
	d := newDefectTracker(t, "qg-conflict")
	d.fail(defectSlot, "STATE_VERSION_CONFLICT", "state apply did not complete: STATE_VERSION_CONFLICT (missing: expected revision 72, stored revision 0)")
	failedAt := d.at.at
	if got := d.internal(); got == nil || got.Code != "STATE_VERSION_CONFLICT" || got.Slot != defectSlot {
		t.Fatalf("after the failed round the internal failure = %+v, want the conflict on its Slot", got)
	}
	d.complete(defectSlot+60, "COMPLETED_WITH_UNAVAILABLE")
	passedAt := d.at.at
	if got := d.internal(); got != nil {
		t.Fatalf("after the next round ran through the row still carries %+v", got)
	}
	fold := ownDefectFold("STATE_VERSION_CONFLICT")
	recovered := d.recoveredUnderDefect(fold)
	if recovered == nil || recovered.Objects != 1 || !recovered.LastFailure.Equal(failedAt) || !recovered.FirstRecovery.Equal(passedAt) || !recovered.LastRecovery.Equal(passedAt) {
		t.Fatalf("recovered under DEFECT/%s = %+v, want one object, failed at %v, passed at %v", fold, recovered, failedAt, passedAt)
	}
	// The degraded run goes on: listed on its own line, and not on DEFECT.
	for round := 1; round < DefaultDegradedRounds+1; round++ {
		d.complete(defectSlot+60*int64(round+1), "COMPLETED_WITH_UNAVAILABLE")
	}
	rows := d.tracker.Anomalies()
	if len(rows) != 1 || rows[0].Internal != nil {
		t.Fatalf("anomalies = %+v, want the degraded object without the conflict beside it", rows)
	}
	Attribute(rows, d.at.at)
	view := &View{Anomalies: rows, Recovered: d.tracker.Recovered()}
	reports := ReportChecks([][]Anomaly{rows, nil, nil, nil}, nil, view, d.at.at)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	defect := byCode[CheckDefect]
	if defect.Current != 0 || defect.Recovered != 1 || len(defect.Groups) != 1 || defect.Groups[0].Key != fold ||
		defect.Groups[0].Recovery != RecoveryRecovered || defect.Groups[0].Objects != 0 {
		t.Fatalf("DEFECT report = %+v, want no current object and the conflict fold recovered", defect)
	}
	if rows[0].Finding.Check == CheckDefect || rows[0].Finding.Check == "" || byCode[rows[0].Finding.Check].Current != 1 {
		t.Fatalf("the object's own line = %+v, want it current under its own check and not DEFECT", rows[0].Finding)
	}
}

// A failure the round carried as a fact and still completed on is not proved
// past by that round completing: the same Slot, the same round. The next
// Slot completing without it is.
func TestAFailureInsideACompletedRoundIsPassedOnlyByTheNextSlot(t *testing.T) {
	d := newDefectTracker(t, "qg-scope")
	conflict := func(slot int64) {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "completion_contract",
				Code: "GAP_SCOPE_REASON_CONFLICT", Detail: "input_a=QUERY_UNAVAILABLE input_b=QUERY_TIMEOUT"},
			Trace: observability.TraceFields{QueryGroupKey: d.group, StrategyID: "4101", BusinessID: "2", EvaluationTime: slot},
		})
	}
	conflict(defectSlot)
	d.complete(defectSlot, "COMPLETED_WITH_UNAVAILABLE")
	if got := d.internal(); got == nil || got.Code != "GAP_SCOPE_REASON_CONFLICT" {
		t.Fatalf("the round that carried the conflict completed and the row lost it: %+v", got)
	}
	// The conflict again on the next Slot: still there, on the new Slot.
	conflict(defectSlot + 60)
	d.complete(defectSlot+60, "COMPLETED_WITH_UNAVAILABLE")
	if got := d.internal(); got == nil || got.Slot != defectSlot+60 {
		t.Fatalf("a repeating conflict = %+v, want it kept on its latest Slot", got)
	}
	// A Slot that completes without it is the stage passing.
	d.complete(defectSlot+120, "COMPLETED_WITH_UNAVAILABLE")
	if got := d.internal(); got != nil {
		t.Fatalf("after a Slot completed without the conflict the row still carries %+v", got)
	}
	// The row's own line is the gapped window's; the conflict was its second
	// line, folded by code, and that is where the recovery lands.
	if recovered := d.recoveredUnderDefect("GAP_SCOPE_REASON_CONFLICT"); recovered == nil || recovered.Objects != 1 {
		t.Fatalf("recovered under DEFECT/GAP_SCOPE_REASON_CONFLICT = %+v (all: %+v), want the object", recovered, d.tracker.Recovered())
	}
}

// What does and does not prove the failed stage passed after a round that
// failed outright: the same Slot completing (the retry got through) does; a
// Slot skipped as a gap does not, whatever its Slot; an older Slot replayed
// does not.
func TestWhichCompletionsProveAFailedRoundPassed(t *testing.T) {
	t.Run("the same Slot completing on retry", func(t *testing.T) {
		d := newDefectTracker(t, "qg-retry")
		d.fail(defectSlot, "STATE_VERSION_CONFLICT", "conflict")
		// Degraded, so that the healthy completion's own reset is not what
		// clears it: the proof under test is the Slot, not the health.
		d.complete(defectSlot, "COMPLETED_WITH_UNAVAILABLE")
		if got := d.internal(); got != nil {
			t.Fatalf("the retry of the failed Slot completed and the row still carries %+v", got)
		}
		if recovered := d.recoveredUnderDefect(ownDefectFold("STATE_VERSION_CONFLICT")); recovered == nil || recovered.Objects != 1 {
			t.Fatalf("recovered under DEFECT = %+v, want the object", recovered)
		}
	})
	t.Run("a later Slot skipped as a gap", func(t *testing.T) {
		d := newDefectTracker(t, "qg-skip")
		d.fail(defectSlot, "STATE_VERSION_CONFLICT", "conflict")
		d.complete(defectSlot+60, "GAP_SKIPPED")
		d.complete(defectSlot+120, "SNAPSHOT_UNAVAILABLE")
		if got := d.internal(); got == nil {
			t.Fatal("a skipped Slot never ran through the failed stage, and the row dropped the failure")
		}
		if recovered := d.recoveredUnderDefect(ownDefectFold("STATE_VERSION_CONFLICT")); recovered != nil {
			t.Fatalf("a skip was recorded as a recovery: %+v", recovered)
		}
	})
	t.Run("an older Slot replayed", func(t *testing.T) {
		d := newDefectTracker(t, "qg-replay")
		d.fail(defectSlot, "STATE_VERSION_CONFLICT", "conflict")
		d.complete(defectSlot-60, "COMPLETED_WITH_UNAVAILABLE")
		if got := d.internal(); got == nil {
			t.Fatal("an older Slot completing says nothing about the failed one, and the row dropped the failure")
		}
	})
	t.Run("a failure on an unnamed Slot is passed by a named one and not by another unnamed one", func(t *testing.T) {
		d := newDefectTracker(t, "qg-unnamed")
		d.fail(0, "STATE_VERSION_CONFLICT", "conflict")
		d.complete(0, "COMPLETED_WITH_UNAVAILABLE")
		if got := d.internal(); got == nil {
			t.Fatal("two unnamed Slots were read as the same Slot retried")
		}
		d.complete(defectSlot, "COMPLETED_WITH_UNAVAILABLE")
		if got := d.internal(); got != nil {
			t.Fatalf("a named Slot after an unnamed failure still leaves %+v", got)
		}
	})
}

// An output failure of this deployment's own making -- the client refusing to
// send -- is proved past by a write that went through, and by nothing else:
// the round completes before its events are written, so a completion says
// nothing about that stage.
func TestAnOutputFailureIsPassedOnlyByAWriteThatWentThrough(t *testing.T) {
	d := newDefectTracker(t, "qg-output")
	chain := "alarmd worker: acknowledge events: OUTPUT_CLIENT_REJECTED: message too large (event evt-1, strategy 4101, business 2, format standard_raw_event)"
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Result: observability.ResultFailed, ReasonCode: "OUTPUT_CLIENT_REJECTED", Err: errors.New(chain),
		OutputRejection: &observability.OutputRejectionFacts{Reason: "OUTPUT_CLIENT_REJECTED", Detail: "message too large"},
		Trace:           observability.TraceFields{QueryGroupKey: d.group, EvaluationTime: defectSlot},
	})
	d.fail(defectSlot, "OUTPUT_CLIENT_REJECTED", chain)
	if got := d.internal(); got == nil || got.Category != observability.QueryFailureCategoryOutput || got.Code != "OUTPUT_CLIENT_REJECTED" {
		t.Fatalf("after the refused write the internal failure = %+v, want the client's refusal", got)
	}
	d.complete(defectSlot+60, "COMPLETED_WITH_UNAVAILABLE")
	if got := d.internal(); got == nil {
		t.Fatal("a completion was read as the output stage passing")
	}
	d.tick()
	d.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed, Result: observability.ResultSuccess,
		Trace: observability.TraceFields{QueryGroupKey: d.group, EvaluationTime: defectSlot + 60},
	})
	if got := d.internal(); got != nil {
		t.Fatalf("after a write went through the row still carries %+v", got)
	}
	// By the time the write went through, a round had completed since the
	// failure, so the row was on DEFECT as its second line, folded by the
	// failure's code; that is the fold the recovery lands under.
	if recovered := d.recoveredUnderDefect("OUTPUT_CLIENT_REJECTED"); recovered == nil || recovered.Objects != 1 {
		t.Fatalf("recovered under DEFECT/OUTPUT_CLIENT_REJECTED = %+v (all: %+v), want the object", recovered, d.tracker.Recovered())
	}
}

// A failure kept from an earlier Slot gets no say in the line of a round
// that completed since: the row's own line is decided by that round's facts.
// Here the rounds after a one-off conflict are skipped as gaps -- a skip
// never runs through the failed stage, so the conflict stays on the row as
// its second fact -- and the row's own line is the skips', not the
// conflict's. It used to be the conflict's: a completed round read the
// failure's code whatever Slot it came from.
func TestAFailureFromAnEarlierSlotDoesNotDecideACompletedRoundsLine(t *testing.T) {
	d := newDefectTracker(t, "qg-stale")
	d.fail(defectSlot, "STATE_VERSION_CONFLICT", "conflict")
	for round := 1; round <= DefaultDegradedRounds; round++ {
		d.tick()
		d.tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "GAP_SKIPPED",
			Trace:                  observability.TraceFields{QueryGroupKey: d.group, StrategyID: "4101", BusinessID: "2", EvaluationTime: defectSlot + 60*int64(round)},
		})
	}
	rows := d.tracker.Anomalies()
	if len(rows) != 1 {
		t.Fatalf("anomalies = %+v, want the skipping object", rows)
	}
	Attribute(rows, d.at.at)
	if rows[0].Finding.Check != CheckDetectionAbandoned {
		t.Fatalf("the row's own line = %+v, want the skips' (%s), not the earlier Slot's conflict", rows[0].Finding, CheckDetectionAbandoned)
	}
	if rows[0].Internal == nil || rows[0].Internal.Code != "STATE_VERSION_CONFLICT" {
		t.Fatalf("internal = %+v, want the conflict kept: a skipped Slot never ran through the failed stage", rows[0].Internal)
	}
}

// Every completion kind, on which side of the proof it falls: a round that
// ran through the pipeline -- the five kinds a Slot ends in after being
// evaluated, a terminal among them (the store answered DETERMINISTIC_INVALID;
// the state stage was reached) -- proves the failed stage passed; the two
// kinds a Slot ends in without being evaluated do not. A kind left out of the
// first list keeps a one-off failure on every object that completes that way
// for as long as it does. The full list is the one the completion line is
// held to, the query-free pair the module's own.
func TestEveryCompletionKindIsOnOneSideOfTheProof(t *testing.T) {
	queryFree := map[string]bool{}
	for _, kind := range model.QueryFreeCompletionKinds {
		queryFree[string(kind)] = true
	}
	if len(queryFree) != 2 || !queryFree["GAP_SKIPPED"] || !queryFree["SNAPSHOT_UNAVAILABLE"] {
		t.Fatalf("query-free kinds = %v, want the two that never run", model.QueryFreeCompletionKinds)
	}
	proved := 0
	for _, kind := range observability.ShortPeriodCompletionKinds {
		failure := &FailureRef{Category: observability.QueryFailureCategoryCompletionContract, Code: "STATE_VERSION_CONFLICT", Slot: defectSlot}
		want := !queryFree[kind]
		if got := defectPassedByCompletion(failure, true, kind, defectSlot+60); got != want {
			t.Fatalf("a later %s completion proves the failure passed = %v, want %v", kind, got, want)
		}
		if want {
			proved++
		}
	}
	if proved != 5 {
		t.Fatalf("kinds that prove the stage passed = %d, want the five evaluated kinds (FULL, FULL_EMPTY, PARTIAL_GAP, UNAVAILABLE, TERMINAL)", proved)
	}
	if defectPassedByCompletion(&FailureRef{Category: observability.QueryFailureCategoryCompletionContract, Slot: defectSlot}, true, "", defectSlot+60) {
		t.Fatal("no completion at all was read as a run")
	}
}
