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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// gapSkipped is the progress_committed observation the worker emits for a
// Slot closed without a query, as worker.observeCommittedProgress builds
// it: the completion kind, and the evidence of an earlier attempt when the
// completion carried any -- nil when it did not, not three zero fields.
func gapSkipped(ctx context.Context, tracker *Tracker, slot int64, evidence *observability.ExecutionEvidenceFacts) {
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "REPLAY_EXPIRED",
		Trace:             observability.TraceFields{EvaluationTime: slot},
		ExecutionEvidence: evidence,
	})
}

func applied(plansApplied, plansTotal int) *observability.ExecutionEvidenceFacts {
	return &observability.ExecutionEvidenceFacts{Kind: "STATE_APPLIED", PlansApplied: plansApplied, PlansTotal: plansTotal}
}

// CX-02 as the page sees it: a Slot an earlier attempt executed whole --
// events sent, state written -- whose Progress write failed and which was
// closed later as GAP_SKIPPED. Every Slot of the span reads STATE_APPLIED,
// so the span is interrupted bookkeeping and not abandoned detection: its
// own line, on the record side, folded on the replica, with the running
// count and the latest occurrence; nothing on the work list, no Blocked
// reading, and the object row that lands on the same code lands there too.
func TestAFullyExecutedSkipIsBookkeepingNotAbandonedDetection(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-book"})
	for round := 0; round < DefaultDegradedRounds; round++ {
		gapSkipped(ctx, tracker, int64(1000+60*round), applied(2, 2))
		at.at = at.at.Add(time.Minute)
	}
	skips := tracker.GapSkips()
	skip, listed := skips["qg-book"]
	if !listed || skip.Slots != 3 || skip.Evidence == nil || skip.Evidence.SlotsApplied != 3 || skip.Evidence.Reading != "STATE_APPLIED" ||
		skip.Evidence.PlansApplied != 2 || skip.Evidence.PlansTotal != 2 || !skip.FullyApplied() {
		t.Fatalf("span = %+v / %+v, want three Slots every one of which an earlier attempt executed", skip, skip.Evidence)
	}
	facts := tracker.BookkeepingAbandoned()
	if facts == nil || facts.Slots != 3 || facts.Objects != 1 || !facts.LastAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("running count = %+v, want 3 Slots on 1 object, the latest at the third round", facts)
	}
	// The object row itself, after enough degraded rounds to be one: the
	// GAP_SKIPPED code lands on the bookkeeping line by the evidence it
	// carries, and the row carries the emitter's reading.
	rows := anyColumn(tracker)
	Attribute(rows, at.at)
	if len(rows) != 1 || rows[0].Finding.Check != CheckBookkeepingAbandoned {
		t.Fatalf("rows = %+v, want the object on the bookkeeping line", rows)
	}
	if rows[0].ExecutionEvidence == nil || rows[0].ExecutionEvidence.Reading != "STATE_APPLIED" {
		t.Fatalf("row evidence = %+v, want the emitter's reading", rows[0].ExecutionEvidence)
	}
	if rows[0].Blocked != nil {
		t.Fatalf("blocked = %+v, want no reading: the Slot was executed, nothing is stuck", rows[0].Blocked)
	}
	// On the lines: a record line with the running count, nothing current,
	// folded on the replica, historical by construction, and no
	// DETECTION_ABANDONED line at all.
	view := View{GapSkips: skips, BookkeepingAbandoned: facts, PerReplica: []ReplicaView{{Replica: tracker.replica, StartedAt: now.Add(-time.Hour)}}}
	reports := ReportChecks(nil, nil, &view, at.at)
	var line *CheckReport
	for index := range reports {
		if reports[index].Code == CheckDetectionAbandoned {
			t.Errorf("a fully executed span is listed as abandoned detection: %+v", reports[index])
		}
		if reports[index].Code == CheckBookkeepingAbandoned {
			line = &reports[index]
		}
	}
	if line == nil {
		t.Fatalf("reports = %+v, want a BOOKKEEPING_ABANDONED line", reports)
	}
	if line.Current != 0 || line.Retained != 1 || line.RetainedLastHour != 1 || line.RetainedNewest == nil || !line.RetainedNewest.Equal(now.Add(2*time.Minute)) {
		t.Errorf("line = %+v, want one record, nothing current, made within the hour", line)
	}
	if line.Bookkeeping == nil || line.Bookkeeping.Slots != 3 || line.Bookkeeping.Objects != 1 {
		t.Errorf("line carries %+v, want the running count", line.Bookkeeping)
	}
	if len(line.Groups) != 1 || line.Groups[0].Key != tracker.replica || line.Groups[0].Recovery != RecoveryHistorical {
		t.Errorf("groups = %+v, want one fold on the replica, historical", line.Groups)
	}
	// Not work: the to-do arithmetic does not count it.
	todo := SummarizeTodo(reports, nil, &view, at.at)
	if todo.Checks != 0 || todo.Objects != 0 {
		t.Errorf("todo = %+v, want nothing to do for interrupted bookkeeping", todo)
	}
	// The row under the line: a record with the evidence, and no Blocked
	// reading -- the one it would get says a confirmed skip.
	under := UnderCheck(CheckBookkeepingAbandoned, "", &view, at.at)
	if len(under) != 1 || under[0].Skip == nil || !under[0].Skip.FullyApplied() || under[0].Blocked != nil {
		t.Errorf("rows under the line = %+v, want the record without a Blocked reading", under)
	}
}

// One Slot short of every Plan and the span still owes a gap: a partly
// executed Slot, or one nobody could read, keeps the span on the abandoned
// detection line, with the counts on the record so the row can say how far
// the earlier attempts got. The running count still counts the Slots that
// were executed whole.
func TestAPartlyExecutedSpanStillOwesItsGap(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-mixed"})
	gapSkipped(ctx, tracker, 1000, applied(2, 2))
	at.at = at.at.Add(time.Minute)
	gapSkipped(ctx, tracker, 1060, applied(1, 2))
	at.at = at.at.Add(time.Minute)
	gapSkipped(ctx, tracker, 1120, &observability.ExecutionEvidenceFacts{Kind: "UNREADABLE"})
	skip := tracker.GapSkips()["qg-mixed"]
	if skip.Slots != 3 || skip.Evidence == nil || skip.Evidence.SlotsApplied != 1 || skip.Evidence.SlotsPartial != 1 ||
		skip.Evidence.SlotsUnreadable != 1 || skip.Evidence.Reading != "UNREADABLE" || skip.FullyApplied() {
		t.Fatalf("span = %+v / %+v, want one applied, one partial, one unreadable, not fully applied", skip, skip.Evidence)
	}
	if facts := tracker.BookkeepingAbandoned(); facts == nil || facts.Slots != 1 {
		t.Fatalf("running count = %+v, want the one Slot executed whole", facts)
	}
	view := View{GapSkips: tracker.GapSkips(), BookkeepingAbandoned: tracker.BookkeepingAbandoned()}
	reports := ReportChecks(nil, nil, &view, at.at)
	abandoned := false
	for _, report := range reports {
		if report.Code == CheckDetectionAbandoned && report.Current == 1 {
			abandoned = true
		}
		if report.Code == CheckBookkeepingAbandoned && report.Retained != 0 {
			t.Errorf("a partly executed span is filed as bookkeeping: %+v", report)
		}
	}
	if !abandoned {
		t.Errorf("reports = %+v, want the span current on DETECTION_ABANDONED", reports)
	}
	// The row keeps the evidence and its Blocked reading: some Plan really
	// was not evaluated.
	under := UnderCheck(CheckDetectionAbandoned, "", &view, at.at)
	if len(under) != 1 || under[0].Skip == nil || under[0].Skip.Evidence == nil || under[0].Blocked == nil {
		t.Errorf("rows under the line = %+v, want the record with its evidence and reading", under)
	}
	// And the evidence read is the emitter's, not one applied Plan read as
	// all: a second object whose every Slot applied one Plan of two.
	other := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-half"})
	gapSkipped(other, tracker, 1000, applied(1, 2))
	if skip := tracker.GapSkips()["qg-half"]; skip.FullyApplied() || skip.Evidence.SlotsApplied != 0 || skip.Evidence.SlotsPartial != 1 || skip.Evidence.Reading != "MIXED" {
		t.Errorf("half-applied span = %+v / %+v, want MIXED and not fully applied", skip, skip.Evidence)
	}
}

// A completion carrying no evidence -- an older build, a runtime without the
// port -- says nothing either way: the span has no evidence, and one that had
// some reads ABSENT for the Slot that carried none. The span stays where it
// is today.
func TestAbsentEvidenceSaysNothing(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-old"})
	gapSkipped(ctx, tracker, 1000, nil)
	if skip := tracker.GapSkips()["qg-old"]; skip.Evidence != nil || skip.FullyApplied() {
		t.Fatalf("span = %+v, want no evidence at all", skip)
	}
	if tracker.BookkeepingAbandoned() != nil {
		t.Fatal("a span without evidence counts as interrupted bookkeeping")
	}
	gapSkipped(ctx, tracker, 1060, applied(2, 2))
	gapSkipped(ctx, tracker, 1120, nil)
	skip := tracker.GapSkips()["qg-old"]
	if skip.Evidence == nil || skip.Evidence.SlotsApplied != 1 || skip.Evidence.Reading != "ABSENT" || skip.FullyApplied() {
		t.Fatalf("span = %+v / %+v, want one applied Slot and the last reading ABSENT", skip, skip.Evidence)
	}
}

// The running counts add up across replicas and keep the latest; an object
// that moved is counted on both, as each replica saw it.
func TestBookkeepingCountsSumAcrossReplicas(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].BookkeepingAbandoned = &BookkeepingFacts{Slots: 5, Objects: 2, LastAt: now.Add(-3 * time.Minute)}
	snapshots[1].BookkeepingAbandoned = &BookkeepingFacts{Slots: 2, Objects: 1, LastAt: now.Add(-time.Minute)}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	if view.BookkeepingAbandoned == nil || view.BookkeepingAbandoned.Slots != 7 || view.BookkeepingAbandoned.Objects != 3 ||
		!view.BookkeepingAbandoned.LastAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("view counts = %+v, want 7 Slots, 3 objects, the newer clock", view.BookkeepingAbandoned)
	}
	// And the line exists on the count alone, with nothing under it: the
	// records keep one span per object and may already have been replaced.
	reports := ReportChecks(nil, nil, &view, now)
	found := false
	for _, report := range reports {
		if report.Code == CheckBookkeepingAbandoned {
			found = report.Bookkeeping != nil && report.Bookkeeping.Slots == 7 && report.Current == 0
		}
	}
	if !found {
		t.Errorf("reports = %+v, want the bookkeeping line carrying the summed count", reports)
	}
	if view.Health == HealthDegraded {
		t.Errorf("health = %s: interrupted bookkeeping is not a degradation", view.Health)
	}
}
