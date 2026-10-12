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

// poolRows drives one object through the rounds a process sees after it
// takes the object over, and reads the object's row as the page does.
type poolRows struct {
	*defectTracker
}

func newPoolRows(t *testing.T) *poolRows {
	return &poolRows{newDefectTracker(t, "qg-pooled")}
}

func (p *poolRows) trace(slot int64) observability.TraceFields {
	return observability.TraceFields{QueryGroupKey: p.group, StrategyID: "410", BusinessID: "2", EvaluationTime: slot}
}

// cooldown is a pool line as the Runner emits it: the event, and the pool's
// reason as the line's reason code.
func (p *poolRows) cooldown(event, reason string, entered time.Time) {
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryCooldown,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode(reason), Trace: p.trace(0),
		QueryCooldown: &observability.QueryCooldownFacts{Event: event, EnteredAt: entered,
			Until: p.at.at.Add(time.Hour), Source: event},
	})
}

// refusedRound is a probe the backend refused for want of the table, and
// the round it completed.
func (p *poolRows) refusedRound(slot int64) {
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Result: observability.ResultDegraded, ReasonCode: "QUERY_TARGET_MISSING", Trace: p.trace(slot),
		QueryFailure: &observability.QueryFailureFacts{Stage: observability.QueryFailureStageProvider,
			Category: observability.QueryFailureCategorySourceBackend, Code: "QUERY_TARGET_MISSING",
			Detail: "response=status_space_table_id_field_is_not_exists"},
	})
	p.tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
		ProgressCompletionReason: "QUERY_TARGET_MISSING", Trace: p.trace(slot),
	})
}

// queryFree is the progress line of a Slot finalized without a query, under
// SNAPSHOT_UNAVAILABLE and the cause the finalization named, as the worker
// writes it (observeCommittedProgress): the cause on the completion cause.
func (p *poolRows) queryFree(slot int64, cause string) {
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Result: observability.ResultDegraded, ReasonCode: "SNAPSHOT_UNAVAILABLE",
		ProgressCompletionKind: "SNAPSHOT_UNAVAILABLE", ProgressCompletionCause: cause, Trace: p.trace(slot),
	})
}

// expiredReplay is a Slot that came after its recovery bound, the backlog of
// a takeover.
func (p *poolRows) expiredReplay(slot int64) { p.queryFree(slot, "EXPIRED_REPLAY") }

// row is the object's row with its finding, or nil when it is not listed.
func (p *poolRows) row() *Anomaly {
	p.t.Helper()
	rows := append(p.tracker.Anomalies(), p.tracker.Demoted()...)
	Attribute(rows, p.at.Now())
	for index := range rows {
		if rows[index].QueryGroup == p.group {
			return &rows[index]
		}
	}
	return nil
}

// After a takeover, an object in the pool for want of its table reads so from
// the restore on, through a round of its own and through the commit of a
// Slot whose replay expired during the takeover. That commit ran no query and
// says nothing about any dependency: read as DEPENDENCY_DOWN, the object
// crossed to this deployment's line for a round and back, on a live rollout.
// The commit's Slot is a skip, on the record of what was never evaluated.
func TestAPooledObjectReadsItsPoolsReasonThroughATakeover(t *testing.T) {
	p := newPoolRows(t)
	entered := p.at.at.Add(-16 * 24 * time.Hour)
	p.cooldown("restored", "QUERY_TARGET_MISSING", entered)
	restored := p.row()
	if restored == nil || restored.Finding.Check != CheckQueryTargetMissing || restored.Finding.Owner != OwnerStrategy {
		t.Fatalf("after the restore: %+v, want QUERY_TARGET_MISSING owned by the strategy", restored)
	}
	if restored.Failure == nil || restored.Failure.Code != "QUERY_TARGET_MISSING" || restored.Failure.Source != FailureFromPoolRecord {
		t.Fatalf("after the restore the failure is %+v, want the pool record's reason, named as from it", restored.Failure)
	}
	p.refusedRound(120)
	if row := p.row(); row == nil || row.Finding.Check != CheckQueryTargetMissing || row.Failure.Source == FailureFromPoolRecord {
		t.Fatalf("after its own round: %+v, want QUERY_TARGET_MISSING from the round's own failure", row)
	}
	p.expiredReplay(60)
	row := p.row()
	if row == nil || row.Finding.Check != CheckQueryTargetMissing || row.Finding.Owner != OwnerStrategy {
		t.Fatalf("after the expired replay's commit: %+v, want still QUERY_TARGET_MISSING", row)
	}
	if span, ok := p.tracker.GapSkips()[p.group]; !ok || span.FirstSlot != 60 || span.LastSlot != 60 || span.Slots != 1 || span.Reason != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("skip record = %+v (present %t), want the expired Slot under SNAPSHOT_UNAVAILABLE", span, ok)
	}
}

// The pool's reason follows the pool: a probe that failed and extended the
// cooldown leaves its own reason, and the object leaving the pool takes the
// reason with it - a row that left on a real answer does not keep reading the
// pool record's refusal.
func TestThePoolsReasonFollowsTheLatestProbeAndLeavesWithThePool(t *testing.T) {
	p := newPoolRows(t)
	entered := p.at.at.Add(-time.Hour)
	p.cooldown("restored", "QUERY_TARGET_MISSING", entered)
	p.cooldown("extended", "QUERY_TIMEOUT", entered)
	if row := p.row(); row == nil || row.Failure == nil || row.Failure.Code != "QUERY_TIMEOUT" || row.Failure.Source != FailureFromPoolRecord {
		t.Fatalf("after an extension: %+v, want the latest probe's reason as the pool's", row)
	}
	p.cooldown("recovered", "", time.Time{})
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "FULL_COMPLETED", Trace: p.trace(180),
	})
	if row := p.row(); row != nil {
		t.Fatalf("after leaving the pool on an answer and a full round: listed as %+v, want healthy", row)
	}
	// Back in the pool on a line that names no reason - a record kept before
	// records carried one - the reason it left with is not read again.
	p.cooldown("entered", "", time.Time{})
	if row := p.row(); row == nil || (row.Failure != nil && row.Failure.Source == FailureFromPoolRecord) {
		t.Fatalf("back in the pool with no reason: %+v, want listed with no failure from the pool record", row)
	}
}

// A row this process has seen fail on its own is read by that failure, not
// by what the pool record said.
func TestAnOwnFailureOutranksThePoolRecord(t *testing.T) {
	p := newPoolRows(t)
	p.cooldown("restored", "QUERY_TARGET_MISSING", p.at.at.Add(-time.Hour))
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Result: observability.ResultDegraded, ReasonCode: "QUERY_TIMEOUT", Trace: p.trace(120),
		QueryFailure: &observability.QueryFailureFacts{Stage: observability.QueryFailureStageProvider,
			Category: observability.QueryFailureCategorySourceBackend, Code: "QUERY_TIMEOUT"},
	})
	if row := p.row(); row == nil || row.Failure == nil || row.Failure.Code != "QUERY_TIMEOUT" || row.Failure.Source == FailureFromPoolRecord {
		t.Fatalf("after its own timeout: %+v, want the round's own failure", row)
	}
}

// The expired replay's commit is not a round for an object outside the pool
// either: a healthy object stays healthy, and one two degraded rounds into a
// run is not listed on what would have been its third. The Slot is a skip.
func TestAnExpiredReplaysCommitIsNotARound(t *testing.T) {
	healthy := newPoolRows(t)
	healthy.tick()
	healthy.tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED", Trace: healthy.trace(60)})
	healthy.expiredReplay(120)
	if row := healthy.row(); row != nil {
		t.Fatalf("a healthy object after an expired replay's commit is listed as %+v", row)
	}
	if _, ok := healthy.tracker.GapSkips()[healthy.group]; !ok {
		t.Fatal("the expired Slot is not on the skip record")
	}

	warming := newPoolRows(t)
	warming.complete(60, "COMPLETED_WITH_UNAVAILABLE")
	warming.complete(120, "COMPLETED_WITH_UNAVAILABLE")
	warming.expiredReplay(180)
	if row := warming.row(); row != nil {
		t.Fatalf("two degraded rounds and an expired replay's commit listed the object as %+v: the commit was counted as a round", row)
	}
	warming.complete(240, "COMPLETED_WITH_UNAVAILABLE")
	if row := warming.row(); row == nil || row.ReasonCode == "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("on its own third degraded round: %+v, want listed under its own round", row)
	}
}

// A snapshot that cannot be read while the Slot is live is a retry, not a
// commit, and is this deployment's dependency failing, as before.
func TestALiveSnapshotReadFailureIsStillADependencyDown(t *testing.T) {
	p := newPoolRows(t)
	for slot := int64(60); slot <= 180; slot += 60 {
		p.tick()
		p.tracker.Observe(context.Background(), observability.Observation{
			RunOutcome: "source_retry", ReasonCode: "SNAPSHOT_UNAVAILABLE", Trace: p.trace(slot),
		})
	}
	if row := p.row(); row == nil || row.Finding.Check != CheckDependencyDown {
		t.Fatalf("three retries on an unreadable snapshot: %+v, want DEPENDENCY_DOWN", row)
	}
}

// The cooling column's age is how long the object has been in the pool, as
// its record says, not how long this process has watched it.
func TestTheCoolingAgeIsThePoolsEntry(t *testing.T) {
	at := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	entered := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	row := Anomaly{QueryGroup: "qg-161", Kind: KindDegradedRun, QueryCooldown: &observability.QueryCooldownFacts{Event: "restored"},
		Since: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), SinceFrom: SinceProcessStart, DemotedSince: entered}
	rows := coolingRowsOf([][]Anomaly{{row}}, at)
	if !rows.Oldest.Equal(entered) {
		t.Fatalf("oldest = %v, want the pool entry %v", rows.Oldest, entered)
	}
}

// The completion word is the same for a Slot that came too late and for a
// Snapshot that failed; the cause tells them apart. A corrupt Snapshot is a
// round, filed under its cause: this deployment's defect, listed on its
// third round like any degraded run, with nothing on the skip record. One
// that could not be read until the bound is a round as it always was, the
// dependency down. A line from a build before causes is read as it was.
func TestOnlyAnExpiredReplayIsNotARound(t *testing.T) {
	for _, test := range []struct {
		cause string
		check Check
	}{
		{"SNAPSHOT_CORRUPT", CheckDefect},
		{"SNAPSHOT_UNAVAILABLE_PAST_BOUND", CheckDependencyDown},
		{"RESOLVE_FAILED_PAST_BOUND", CheckDependencyDown},
		{"", CheckDependencyDown},
	} {
		p := newPoolRows(t)
		for slot := int64(60); slot <= 180; slot += 60 {
			p.queryFree(slot, test.cause)
		}
		row := p.row()
		if row == nil || row.Finding.Check != test.check {
			t.Fatalf("cause %q, three rounds: %+v, want listed under %s", test.cause, row, test.check)
		}
		if _, skipped := p.tracker.GapSkips()[p.group]; skipped {
			t.Fatalf("cause %q left a skip record: it was a round", test.cause)
		}
	}
}

// A takeover can give some Slots up past the replay's reach and others after
// their recovery bound. The two are separate spans: a skip after an expired
// replay's commit starts its own, and is not counted into the commit's.
func TestAGapSkipAfterAnExpiredReplayStartsItsOwnSpan(t *testing.T) {
	p := newPoolRows(t)
	skip := func(slot int64) {
		p.tick()
		p.tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
			ProgressCompletionReason: "GAP_SKIPPED", Trace: p.trace(slot),
		})
	}
	skip(60)
	p.expiredReplay(120)
	if span := p.tracker.GapSkips()[p.group]; span.FirstSlot != 120 || span.Reason != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("after the expired replay: %+v, want its own span from 120", span)
	}
	skip(180)
	if span := p.tracker.GapSkips()[p.group]; span.FirstSlot != 180 || span.LastSlot != 180 || span.Slots != 1 || span.Reason == "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("after the next gap skip: %+v, want a new span from 180, not the expired replay's", span)
	}
}

// The skip record names an expired replay's span by its own word. A gap
// skip's Reason is what came before the skip - a permit deadline missed -
// and not the skip's word, so a gap skip stays GAP_SKIPPED.
func TestTheSkipRecordNamesAnExpiredReplayByItsWord(t *testing.T) {
	at := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	view := &View{GapSkips: map[string]SkippedSpan{
		"qg-expired": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Hour), Kind: SkipKindExpiredReplay, Reason: "SNAPSHOT_UNAVAILABLE"},
		"qg-skipped": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Hour), Reason: "QUERY_PERMIT_DEADLINE"},
		// A gap skip whose Slot's snapshot read had kept failing: its Reason
		// is that failure, and it is still a gap skip.
		"qg-unread": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Hour), Reason: "SNAPSHOT_UNAVAILABLE"},
	}}
	codes := map[string]string{}
	lossRecords(view, at, func(queryGroup string, _, _ Check, code string, _ SkippedSpan, _ Loss, _ bool) {
		codes[queryGroup] = code
	})
	if codes["qg-expired"] != "SNAPSHOT_UNAVAILABLE" || codes["qg-skipped"] != "GAP_SKIPPED" || codes["qg-unread"] != "GAP_SKIPPED" {
		t.Fatalf("codes = %v, want the expired replay under SNAPSHOT_UNAVAILABLE and the gap skip under GAP_SKIPPED", codes)
	}
}

// A range of Slots given up at once is on the record of what was never
// evaluated, with its bounds and size, and is not a round: a healthy object
// stays unlisted. A range that did not commit records nothing. A range past
// the replay's reach is a gap skip and keeps that word.
func TestAnExpiredRangeIsOnTheSkipRecordAndIsNotARound(t *testing.T) {
	p := newPoolRows(t)
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED", Trace: p.trace(60)})
	rangeLine := func(result, kind string, committed uint32) {
		p.tick()
		p.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned, Result: observability.ResultSuccess,
			Trace: observability.TraceFields{QueryGroupKey: p.group},
			ExpiredRange: &observability.ExpiredRangeFacts{Result: result, CommittedSlots: committed, FirstSlot: 120, LastSlot: 360,
				Slots: 5, Kind: kind, Cause: "EXPIRED_REPLAY"},
		})
	}
	rangeLine("retrying", "SNAPSHOT_UNAVAILABLE", 0)
	if _, recorded := p.tracker.GapSkips()[p.group]; recorded {
		t.Fatal("a range that did not commit was recorded")
	}
	rangeLine("committed", "SNAPSHOT_UNAVAILABLE", 5)
	span, recorded := p.tracker.GapSkips()[p.group]
	if !recorded || span.FirstSlot != 120 || span.LastSlot != 360 || span.Slots != 5 || span.Reason != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("span = %+v (recorded %t), want 120..360, five Slots, under SNAPSHOT_UNAVAILABLE", span, recorded)
	}
	if row := p.row(); row != nil {
		t.Fatalf("a healthy object after a range commit is listed as %+v", row)
	}
	rangeLine("committed", "GAP_SKIPPED", 5)
	if span := p.tracker.GapSkips()[p.group]; span.Reason != "" || span.Slots != 5 {
		t.Fatalf("a range past the replay's reach: %+v, want a gap skip's span", span)
	}
}

// The three paths that give Slots up - a gap skip, a Slot past its recovery
// bound, a range of either - continue one span when they follow each other
// with nothing evaluated between and are of one kind, and the span counts
// every Slot. A round that is not a skip ends it, whichever path comes next.
// The range's own strategies are recorded: after a restart its line may be
// the first the tracker sees of the object.
func TestTheSkipPathsContinueOneSpan(t *testing.T) {
	rangeLine := func(p *poolRows, kind string, first, last int64, committed uint32) {
		p.tick()
		p.tracker.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageExpiredRangeReturned, Result: observability.ResultSuccess,
			Trace: observability.TraceFields{QueryGroupKey: p.group},
			ExpiredRange: &observability.ExpiredRangeFacts{Result: "committed", CommittedSlots: committed, FirstSlot: first, LastSlot: last,
				Slots: committed, Kind: kind, Cause: "EXPIRED_REPLAY",
				Strategies: []observability.ExpiredRangeStrategy{{BusinessID: "2", StrategyID: "7"}}},
		})
	}
	skip := func(p *poolRows, slot int64) {
		p.tick()
		p.tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
			ProgressCompletionReason: "GAP_SKIPPED", Trace: p.trace(slot),
		})
	}
	want := func(p *poolRows, first, last int64, slots int) {
		t.Helper()
		if span := p.tracker.GapSkips()[p.group]; span.FirstSlot != first || span.LastSlot != last || span.Slots != slots {
			t.Fatalf("span = %+v, want %d..%d of %d Slots", span, first, last, slots)
		}
	}

	single := newPoolRows(t)
	skip(single, 60)
	rangeLine(single, "GAP_SKIPPED", 120, 360, 5)
	want(single, 60, 360, 6)

	ranged := newPoolRows(t)
	rangeLine(ranged, "GAP_SKIPPED", 120, 360, 5)
	skip(ranged, 420)
	want(ranged, 120, 420, 6)

	expired := newPoolRows(t)
	expired.expiredReplay(60)
	rangeLine(expired, "SNAPSHOT_UNAVAILABLE", 120, 360, 5)
	want(expired, 60, 360, 6)

	ended := newPoolRows(t)
	skip(ended, 60)
	ended.tick()
	ended.tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED", Trace: ended.trace(120)})
	rangeLine(ended, "GAP_SKIPPED", 180, 360, 4)
	want(ended, 180, 360, 4)

	// A range that does not come after the span - one reaching back over
	// Slots the span already holds - starts its own rather than counting
	// those Slots twice.
	overlapping := newPoolRows(t)
	skip(overlapping, 360)
	rangeLine(overlapping, "GAP_SKIPPED", 300, 420, 3)
	want(overlapping, 300, 420, 3)

	fresh := newPoolRows(t)
	rangeLine(fresh, "SNAPSHOT_UNAVAILABLE", 120, 360, 5)
	// The publisher fills a skip record's strategies from these.
	if strategies := fresh.tracker.StrategiesFor(fresh.group); len(strategies) != 1 || strategies[0] != (StrategyRef{StrategyID: "7", BusinessID: "2"}) {
		t.Fatalf("a range seen first: strategies %+v, want the range's own", strategies)
	}
}

// A gap skip's span records the failure before the skip as its Reason, and
// that failure can read SNAPSHOT_UNAVAILABLE - a snapshot store that kept
// failing until the Slot fell past the replay's reach. The span is still a
// gap skip: the expired replay after it starts its own, and the first keeps
// its kind and that Reason.
func TestAGapSkipAfterSnapshotFailuresStaysAGapSkip(t *testing.T) {
	p := newPoolRows(t)
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		ExecuteOutcome: "error", ReasonCode: "SNAPSHOT_UNAVAILABLE", Err: errors.New("snapshot store down"),
		QueryFailure: &observability.QueryFailureFacts{Stage: observability.QueryFailureStageProvider,
			Category: observability.QueryFailureCategorySourceBackend, Code: "SNAPSHOT_UNAVAILABLE"},
		Trace: p.trace(60),
	})
	p.tick()
	p.tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
		ProgressCompletionReason: "GAP_SKIPPED", Trace: p.trace(60),
	})
	first := p.tracker.GapSkips()[p.group]
	if first.Kind != "" || first.Reason != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("the gap skip's span = %+v, want a gap skip whose Reason is the failure before it", first)
	}
	p.expiredReplay(120)
	if span := p.tracker.GapSkips()[p.group]; span.Kind != SkipKindExpiredReplay || span.FirstSlot != 120 || span.Slots != 1 {
		t.Fatalf("after the expired replay: %+v, want a span of its own from 120", span)
	}
}
