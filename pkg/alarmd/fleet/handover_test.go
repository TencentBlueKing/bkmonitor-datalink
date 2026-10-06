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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object the rebalance moved away stops having rounds on the replica it
// left, and that is not the rounds failing to end. The fence's refusal is
// the moment this replica stops running it, so the stall clock stops there:
// read on, a deterministic defect the move carried away was marked stalled
// on the old owner while the new one had not yet listed it, and the first
// screen read it as a stall and then as fixed. The row keeps what it last
// said until the publisher forgets an object this replica no longer owns.
func TestOwnershipRejectionStopsTheStallClockAndKeepsTheRow(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	fail := func() {
		tracker.Observe(context.Background(), gapGuardRefusal("qg-moved", 1_700_000_000))
	}
	for round := 0; round < DefaultDegradedRounds; round++ {
		fail()
	}
	before := tracker.Anomalies()
	if len(before) != 1 || before[0].FailingSince.IsZero() {
		t.Fatalf("rows before the move = %+v, want the failing object with its stall clock running", before)
	}
	// The fence refuses the next round: the object is somebody else's now.
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), observability.Observation{RunOutcome: "ownership_rejected",
		Trace: observability.TraceFields{QueryGroupKey: "qg-moved"}})
	after := tracker.Anomalies()
	if len(after) != 1 || !after[0].FailingSince.IsZero() {
		t.Fatalf("rows after the refusal = %+v, want the row kept with its stall clock stopped", after)
	}
	if after[0].Failure == nil || after[0].Failure.Code != "GAP_GUARD_CONFLICT" || after[0].LastError == nil || after[0].ReasonCode != "error" {
		t.Fatalf("row after the refusal = %+v, want what it last said kept", after[0])
	}
	// An hour later, still on this replica's list for want of a publish: not
	// stalled, because nothing here is running it.
	MarkStalled(after, now.Add(time.Hour), time.Minute)
	Attribute(after, now.Add(time.Hour))
	if after[0].Stalled || after[0].Finding.Check != CheckDefect {
		t.Fatalf("row an hour after the refusal = stalled %v under %s, want not stalled, under DEFECT", after[0].Stalled, after[0].Finding.Check)
	}
	// And a round that fails again on this replica -- the refusal was the
	// store, not a move -- starts the clock over from that round.
	at.at = at.at.Add(time.Minute)
	fail()
	if rows := tracker.Anomalies(); len(rows) != 1 || !rows[0].FailingSince.Equal(at.at) {
		t.Fatalf("rows after failing again = %+v, want the stall clock restarted at that round", rows)
	}
}

// A code the table files as this deployment's own defect is the line even
// when the object also stalls: a gap guard in conflict with itself stops
// the Slot, so the rounds stop ending too, and the defect is the fact to
// act on -- "restart the replica" is the wrong next step for a conflict that
// repeats until fixed. A stall with no defect code behind it is a stall.
func TestADefectCodeOutranksTheStall(t *testing.T) {
	defect := Anomaly{Kind: KindDegradedRun, ReasonCode: "error", Stalled: true, FailingSince: now.Add(-time.Hour),
		Failure:   &FailureRef{Stage: "execute", Category: "completion_contract", Code: "GAP_GUARD_CONFLICT"},
		LastError: &LastError{Text: "alarmd state: gap guard conflict: expected 41 got 43", At: now}}
	stalled := Anomaly{Kind: KindDegradedRun, ReasonCode: "error", Stalled: true, FailingSince: now.Add(-time.Hour),
		Failure:   &FailureRef{Stage: "execute", Category: "source_backend", Code: "QUERY_TIMEOUT"},
		LastError: &LastError{Text: "context deadline exceeded", At: now}}
	rows := []Anomaly{defect, stalled}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDefect || rows[0].Finding.Group != "EVALUATE/NONE/CONTRACT" || !rows[0].Stalled {
		t.Fatalf("a stalled defect = %+v, want under DEFECT on its code, still marked stalled", rows[0].Finding)
	}
	if rows[1].Finding.Check != CheckRoundsStalled {
		t.Fatalf("a stalled timeout = %+v, want under ROUNDS_STALLED", rows[1].Finding)
	}
	// The same object before and after a change of owner reads the same:
	// the replica that lost it and the one that got it both file it as the
	// defect, so the line's count does not dip to zero between them.
	moved := defect
	moved.Stalled, moved.FailingSince = false, time.Time{}
	fresh := []Anomaly{moved}
	Attribute(fresh, now)
	if fresh[0].Finding.Check != CheckDefect || fresh[0].Finding.Group != rows[0].Finding.Group {
		t.Fatalf("the same defect on its new owner = %+v, want the same line and fold as on the old", fresh[0].Finding)
	}
}

// gapGuardRefusal is the observation the scheduler actually emits when a gap
// guard refuses a query-free Slot at finalize: a terminal slot_completed
// with the refusal's reason on the observation and no query failure before
// it, because the Slot never went through the query stage. The earlier
// version of these tests put the code on a QueryFailure, which the emitter
// never does for this path, and so exercised a row the deployment never
// produces.
func gapGuardRefusal(queryGroup string, slot int64) observability.Observation {
	return observability.Observation{
		ExecuteOutcome: "error", ReasonCode: "GAP_GUARD_CONFLICT",
		Err:   errors.New("alarmd worker: finalize query-free Slot: alarmd worker: activated Plan gap marker conflicts with the Slot"),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
	}
}

// The reason the terminal observation names is the round's code when the
// query stage named none. Read on: a live object refused by its gap guard
// every thirty seconds carried no code at all -- the code was on the
// slot_completed observation and the tracker read only query failures -- so
// it sat under the unclassified defect, and ten minutes after its replica
// started, the stall budget, it moved to "rounds stalled" with "restart the
// replica" as the next step. The first screen read DEFECT 3→2 and
// ROUNDS_STALLED 0→1 in the same minute, once per rollout.
func TestTheTerminalsOwnReasonIsTheRoundsCode(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	slot := int64(1_700_000_000)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), gapGuardRefusal("qg-refused", slot))
		at.at = at.at.Add(30 * time.Second)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].Failure == nil || rows[0].Failure.Code != "GAP_GUARD_CONFLICT" || rows[0].Failure.Slot != slot {
		t.Fatalf("rows = %+v, want the terminal's reason as the round's failure code, on the round's Slot", rows)
	}
	if rows[0].Internal == nil || rows[0].Internal.Code != "GAP_GUARD_CONFLICT" {
		t.Fatalf("internal = %+v, want the refusal filed as this deployment's own", rows[0].Internal)
	}
	// Past the stall budget the object is stalled -- and still the defect.
	for at.at.Sub(now) <= 10*time.Minute {
		tracker.Observe(context.Background(), gapGuardRefusal("qg-refused", slot))
		at.at = at.at.Add(30 * time.Second)
	}
	rows = tracker.Anomalies()
	MarkStalled(rows, at.at, 10*time.Minute)
	Attribute(rows, at.at)
	if len(rows) != 1 || !rows[0].Stalled {
		t.Fatalf("rows = %+v, want the object marked stalled after the budget", rows)
	}
	if rows[0].Finding.Check != CheckDefect || rows[0].Finding.Group != "EVALUATE/NONE/CONTRACT" {
		t.Fatalf("finding = %+v, want DEFECT on the refusal's code, not ROUNDS_STALLED", rows[0].Finding)
	}
	if rows[0].Blocked == nil || rows[0].Blocked.Code != "GAP_GUARD_CONFLICT" || rows[0].Blocked.Stage != StageEvaluate || rows[0].Blocked.Class != ClassContract || rows[0].Blocked.Effect != EffectRetrying {
		t.Fatalf("blocked = %+v, want the refusal read as EVALUATE/CONTRACT and retrying", rows[0].Blocked)
	}
}

// A state version conflict named at the terminal is this deployment's own
// defect at the commit step: both versions compared are this system's
// writes, and the evaluation had finished. Before the scheduler named it the
// rounds arrived as internal_unknown and sat under the unclassified defect
// until the stall budget moved them; named, they stay the defect and fold
// with the other state-write contract refusals.
func TestAStateVersionConflictIsTheCommitStepsOwnDefect(t *testing.T) {
	for _, code := range []string{"STATE_VERSION_CONFLICT", "STATE_STALE_VERSION"} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		for round := 0; round < DefaultDegradedRounds; round++ {
			tracker.Observe(context.Background(), observability.Observation{
				ExecuteOutcome: "error", ReasonCode: observability.ReasonCode(code),
				Err:   errors.New("alarmd worker: execute frozen Slot: alarmd worker: apply state: state apply did not complete: " + code),
				Trace: observability.TraceFields{QueryGroupKey: "qg-" + code, EvaluationTime: 1_700_000_000},
			})
			at.at = at.at.Add(30 * time.Second)
		}
		rows := tracker.Anomalies()
		MarkStalled(rows, at.at.Add(time.Hour), 10*time.Minute)
		Attribute(rows, at.at.Add(time.Hour))
		if len(rows) != 1 || rows[0].Finding.Check != CheckDefect || rows[0].Finding.Group != "COMMIT/NONE/CONTRACT" {
			t.Fatalf("%s rows = %+v, want DEFECT folded as the commit step's contract refusal, stalled or not", code, rows)
		}
		if rows[0].Blocked == nil || rows[0].Blocked.Code != code || rows[0].Blocked.Stage != StageCommit || rows[0].Blocked.Class != ClassContract || rows[0].Blocked.Dependency != DependencyNone {
			t.Fatalf("%s blocked = %+v, want COMMIT/CONTRACT with no dependency", code, rows[0].Blocked)
		}
	}
}

// The scheduler's class words are not codes. internal_unknown says the
// scheduler could not name the failure; reading it as a code would file every
// unnamed failure under one invented name and hide that nobody named it.
func TestTheTerminalsClassWordIsNotACode(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error", ReasonCode: "internal_unknown",
			Err:   errors.New("alarmd worker: execute frozen Slot: alarmd worker: apply state: state apply did not complete: STATE_VERSION_CONFLICT"),
			Trace: observability.TraceFields{QueryGroupKey: "qg-unnamed", EvaluationTime: 1_700_000_000},
		})
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].Failure != nil {
		t.Fatalf("rows = %+v, want no failure code for a terminal that named only its class", rows)
	}
}

// A query stage that named this Slot's failure saw it closer to where it
// happened, and keeps the name; the terminal's reason fills in only where
// the query stage said nothing. A failure named on an earlier Slot does not
// stand in for this one.
func TestTheQueryStagesNameForThisSlotOutranksTheTerminals(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	slot := int64(1_700_000_000)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "completion_contract", Code: "OUTCOME_DUPLICATE"},
			Trace:        observability.TraceFields{QueryGroupKey: "qg-named", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), gapGuardRefusal("qg-named", slot))
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].Failure == nil || rows[0].Failure.Code != "OUTCOME_DUPLICATE" {
		t.Fatalf("rows = %+v, want the query stage's name for this Slot kept", rows)
	}
	// The next Slot is refused with nothing from the query stage: the
	// terminal's name is that round's.
	tracker.Observe(context.Background(), gapGuardRefusal("qg-named", slot+60))
	rows = tracker.Anomalies()
	if len(rows) != 1 || rows[0].Failure == nil || rows[0].Failure.Code != "GAP_GUARD_CONFLICT" || rows[0].Failure.Slot != slot+60 {
		t.Fatalf("rows = %+v, want the terminal's name on the Slot the query stage said nothing about", rows)
	}
}
