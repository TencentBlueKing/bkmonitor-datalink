// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// sampledLog is the production log policy at the setting that suppresses:
// every line that is not a failure keeps one per hour per stage and Query
// Group.
func sampledLog(t *testing.T) (*observability.LoggingObserver, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 1, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	return observability.NewLoggingObserver(observability.New("alarmd", &output), policy), &output
}

func logLines(t *testing.T, output *bytes.Buffer, stage string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %v: %s", err, raw)
		}
		if line["stage"] == stage {
			lines = append(lines, line)
		}
	}
	return lines
}

// withoutRangeFlight hides the Runner's range flight from the source, as a
// round the range gate refuses would: the expired Slot is finalised alone.
type withoutRangeFlight struct{ next SlotSource }

func (source withoutRangeFlight) Next(ctx context.Context, queryGroup execution.QueryGroupIdentity) (FrozenSlot, bool, SlotDueFacts, error) {
	return source.next.Next(context.WithValue(ctx, rangeFlightContextKey{}, execution.QueryGroupIdentity("")), queryGroup)
}

func gapSkippedResult() execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionGapSkipped,
		Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped), QueryFreeCause: execution.CauseExpiredReplay}
}

func number(line map[string]any, key string) int64 {
	value, _ := line[key].(float64)
	return int64(value)
}

// The traced case: a replay past the distance limit is finalised without a
// query as GAP_SKIPPED, cause EXPIRED_REPLAY, and that Slot writes exactly one
// slot_skipped line naming the path, what the decision compared -- the age
// on the source's clock and the configured limits -- what held the round
// before it, and what the lookback had of the group at that moment. A second
// skip of the group inside the hour is written too, where the sampled lines
// of the same rounds are not.
func TestAReplayGivenUpForDistanceWritesOneUnsampledSlotSkippedLine(t *testing.T) {
	const interval = int64(15)
	const firstSlot = execution.EvaluationTime(600)
	at := time.Unix(int64(firstSlot)+8*interval, 0)
	schedule := schedulerSchedule(t, interval, firstSlot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	inner := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(firstSlot, firstSlot-execution.EvaluationTime(interval)), at, testRecoveryLimits())
	observer, output := sampledLog(t)
	inner.observer = observer
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{gapSkippedResult(), gapSkippedResult()}}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, withoutRangeFlight{next: inner}, executor, flights, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	busy := observability.LookbackBusyFacts{SamplePending: true, ReadsInFlight: 1}
	runner.WithSlotSkipReport(observer, func(queryGroup execution.QueryGroupIdentity) (observability.LookbackBusyFacts, bool) {
		if queryGroup != "query-group-1" {
			t.Fatalf("lookback read for %q", queryGroup)
		}
		return busy, true
	})
	readyAt := at.Add(-time.Second).UnixMilli()
	runner.heldBy = observability.HeldByFacts{Decision: observability.HeldByReadinessDeferred, AtUnixMilli: at.Add(-2 * time.Second).UnixMilli(), ReadyAtUnixMilli: readyAt}

	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("RunOne() = %t %v", attempted, err)
	}
	lines := logLines(t, output, observability.StageSlotSkipped)
	if len(lines) != 1 {
		t.Fatalf("slot_skipped lines = %d, want one: %s", len(lines), output.String())
	}
	line := lines[0]
	limits := testRecoveryLimits()
	for key, want := range map[string]any{
		"finalizing_path": observability.SlotSkippedQueryFree, "skip_cause": string(execution.CauseExpiredReplay),
		"skip_reason": contract.ReasonGapSkipped, "replay_expiry_reason": string(ReplayExpiredByDistance),
		"query_group_key": "query-group-1", "operation": string(execution.OperationNormal),
		"held_by": observability.HeldByReadinessDeferred, "held_by_lookback": true, "reason_code": contract.ReasonGapSkipped,
		"skip_gap_guard": observability.SkipGapPlanMarkers,
	} {
		if line[key] != want {
			t.Fatalf("%s = %v, want %v: %v", key, line[key], want, line)
		}
	}
	for key, want := range map[string]int64{
		"skipped_first_slot": int64(firstSlot), "skipped_last_slot": int64(firstSlot), "skipped_slot_count": 1,
		"evaluation_time": int64(firstSlot), "owner_epoch": 7,
		// The decision's own instant less the Slot, and the two limits it was
		// compared with.
		"replay_age_ms": at.UnixMilli() - int64(firstSlot)*1000, "replay_age_limit_ms": limits.MaxReplayAge.Milliseconds(),
		"replay_distance_limit_slots": int64(limits.MaxReplaySlots),
		"held_by_ready_at":            readyAt, "lookback_reads_in_flight": 1,
	} {
		if got := number(line, key); got != want {
			t.Fatalf("%s = %d, want %d: %v", key, got, want, line)
		}
	}
	if number(line, "replay_distance_slots") <= int64(limits.MaxReplaySlots) || number(line, "skip_gap_plans") < 1 {
		t.Fatalf("distance %d against limit %d, gap plans %d: %v", number(line, "replay_distance_slots"), limits.MaxReplaySlots, number(line, "skip_gap_plans"), line)
	}

	// The same group skips again inside the hour: its line is written, while
	// the sampled replay_expired line of the same round is held back.
	busy = observability.LookbackBusyFacts{}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("second RunOne() = %t %v", attempted, err)
	}
	lines = logLines(t, output, observability.StageSlotSkipped)
	if len(lines) != 2 {
		t.Fatalf("slot_skipped lines after a second skip = %d, want two (never sampled): %s", len(lines), output.String())
	}
	if expired := logLines(t, output, observability.StageReplayExpired); len(expired) != 1 {
		t.Fatalf("replay_expired lines = %d, want the sampler to have kept one, or the fixture does not suppress", len(expired))
	}
	if lines[1]["held_by"] != observability.HeldByNothing || lines[1]["held_by_lookback"] != false {
		t.Fatalf("second skip held_by %v, lookback %v; want the round before it and the lookback as read then", lines[1]["held_by"], lines[1]["held_by_lookback"])
	}
}

// A range given up on is one line for the range: its span and count, the
// distance it was sized by, and the expiry of the Slot it starts at.
func TestAnExpiredRangeWritesOneSlotSkippedLineForItsSpan(t *testing.T) {
	const interval = int64(15)
	const firstSlot = execution.EvaluationTime(600)
	at := time.Unix(int64(firstSlot)+8*interval, 0)
	schedule := schedulerSchedule(t, interval, firstSlot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(firstSlot, firstSlot-execution.EvaluationTime(interval)), at, testRecoveryLimits())
	observer, output := sampledLog(t)
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{gapSkippedResult()}}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, source, executor, flights, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	runner.WithSlotSkipReport(observer, nil)
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("RunOne() = %t %v", attempted, err)
	}
	requests := executor.Requests()
	if len(requests) != 1 || requests[0].ExpiredRange == nil {
		t.Fatalf("requests = %+v, want one expired range", requests)
	}
	proof := requests[0].ExpiredRange
	lines := logLines(t, output, observability.StageSlotSkipped)
	if len(lines) != 1 {
		t.Fatalf("slot_skipped lines = %d, want one: %s", len(lines), output.String())
	}
	line := lines[0]
	if line["finalizing_path"] != observability.SlotSkippedExpiredRange || line["held_by"] != observability.HeldByNothing ||
		line["held_by_lookback_known"] != false || line["replay_expiry_reason"] != string(ReplayExpiredByDistance) {
		t.Fatalf("range line = %v", line)
	}
	if number(line, "skipped_first_slot") != int64(proof.First.Contract.Slot.EvaluationTime) ||
		number(line, "skipped_last_slot") != int64(proof.Last.Contract.Slot.EvaluationTime) ||
		number(line, "skipped_slot_count") != int64(proof.Count) || proof.Count < 2 ||
		number(line, "range_slot_count") != int64(proof.Count) || number(line, "range_head_steps") == 0 {
		t.Fatalf("range line %v does not describe the range %+v", line, proof)
	}
}

// Every query-free finalization is a Slot given up on, SNAPSHOT_UNAVAILABLE as
// well as GAP_SKIPPED: an expired replay past its recovery bound, a Snapshot
// missing or corrupt past it. The line's reason and cause are the
// finalization's own, so the two can be told apart; a completion that names
// no query-free cause -- a range an earlier attempt already committed --
// writes none.
func TestEveryQueryFreeFinalizationWritesOneSlotSkippedLine(t *testing.T) {
	snapshotUnavailable := execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	for _, test := range []struct {
		name   string
		result execution.SlotExecutionResult
		want   int
	}{
		{name: "expired replay past its recovery bound", want: 1, result: execution.SlotExecutionResult{Completed: true,
			CompletionKind: execution.CompletionSnapshotUnavailable, ReasonCode: snapshotUnavailable, QueryFreeCause: execution.CauseExpiredReplay}},
		{name: "snapshot missing past its bound", want: 1, result: execution.SlotExecutionResult{Completed: true,
			CompletionKind: execution.CompletionSnapshotUnavailable, ReasonCode: snapshotUnavailable, QueryFreeCause: execution.CauseSnapshotUnavailablePastBound}},
		{name: "snapshot corrupt", want: 1, result: execution.SlotExecutionResult{Completed: true,
			CompletionKind: execution.CompletionSnapshotUnavailable, ReasonCode: snapshotUnavailable, QueryFreeCause: execution.CauseSnapshotCorrupt}},
		{name: "already committed", want: 0, result: execution.SlotExecutionResult{Completed: true,
			CompletionKind: execution.CompletionGapSkipped, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped)}},
	} {
		observer, output := sampledLog(t)
		slot := frozenSlot("query-group-1")
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot},
			&scriptedExecutor{results: []execution.SlotExecutionResult{test.result}}, NewFlightCoordinator(), func() time.Time { return time.Unix(100, 0) })
		if err != nil {
			t.Fatal(err)
		}
		runner.WithSlotSkipReport(observer, nil)
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("%s: RunOne() = %t %v", test.name, attempted, err)
		}
		lines := logLines(t, output, observability.StageSlotSkipped)
		if len(lines) != test.want {
			t.Fatalf("%s: slot_skipped lines = %d, want %d: %s", test.name, len(lines), test.want, output.String())
		}
		if test.want == 0 {
			continue
		}
		line := lines[0]
		if line["finalizing_path"] != observability.SlotSkippedQueryFree || line["skip_reason"] != contract.ReasonSnapshotUnavailable ||
			line["reason_code"] != contract.ReasonSnapshotUnavailable || line["skip_cause"] != string(test.result.QueryFreeCause) ||
			line["skip_gap_guard"] != observability.SkipGapPlanMarkers {
			t.Fatalf("%s: line = %v", test.name, line)
		}
	}
}

// A Slot that ran writes no slot_skipped line, and neither does one that ran
// and whose Levels copied GAP_SKIPPED up into its reason: that is a Level
// held by a guard, not a Slot given up on.
func TestASlotThatRanWritesNoSlotSkippedLine(t *testing.T) {
	for _, result := range []execution.SlotExecutionResult{
		{Completed: true, CompletionKind: execution.CompletionFull, Result: observability.ResultSuccess},
		{Completed: true, CompletionKind: execution.CompletionUnavailable, Result: observability.ResultDegraded,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped)},
	} {
		observer, output := sampledLog(t)
		slot := frozenSlot("query-group-1")
		flights := NewFlightCoordinator()
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot},
			&scriptedExecutor{results: []execution.SlotExecutionResult{result}}, flights, func() time.Time { return time.Unix(100, 0) })
		if err != nil {
			t.Fatal(err)
		}
		runner.WithSlotSkipReport(observer, nil)
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("%s: RunOne() = %t %v", result.CompletionKind, attempted, err)
		}
		if lines := logLines(t, output, observability.StageSlotSkipped); len(lines) != 0 {
			t.Fatalf("%s: slot_skipped lines = %v, want none", result.CompletionKind, lines)
		}
	}
}

// A cursor moved over a span the timeline no longer holds is a skip too: one
// line once the store applies it, under the reason recorded, naming the
// recent gap it leaves; none when the store refuses.
func TestACursorSkipWritesOneSlotSkippedLineOnceApplied(t *testing.T) {
	at := time.Unix(661, 0)
	pruned := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120,
	}}
	for _, status := range []execution.ProgressCommitStatus{execution.ProgressCommitted, execution.ProgressConflict} {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: status}}
		observer, output := sampledLog(t)
		source := prunedCursorSource(t, catalog, reader, at, observer)
		ctx := withHeldBy(context.Background(), observability.HeldByFacts{Decision: "query_cooldown", AtUnixMilli: 1, QueryCooldownFailures: 3})
		ctx = observability.ContextWithLookbackBusy(ctx, func() (observability.LookbackBusyFacts, bool) {
			return observability.LookbackBusyFacts{Capturing: true}, true
		})
		_, _, _, _ = source.Next(ctx, "query-group-1")
		lines := logLines(t, output, observability.StageSlotSkipped)
		if status != execution.ProgressCommitted {
			if len(lines) != 0 {
				t.Fatalf("a refused skip wrote %v", lines)
			}
			continue
		}
		if len(lines) != 1 {
			t.Fatalf("slot_skipped lines = %d, want one: %s", len(lines), output.String())
		}
		line := lines[0]
		if line["finalizing_path"] != observability.SlotSkippedCursorSkip || line["skip_reason"] != contract.ReasonSchedulePruned ||
			line["skip_gap_guard"] != observability.SkipGapProgressOnly || line["held_by"] != "query_cooldown" ||
			number(line, "held_by_cooldown_failures") != 3 || line["held_by_lookback"] != true || line["lookback_capturing"] != true ||
			number(line, "skipped_first_slot") != 120 || number(line, "owner_epoch") != 7 {
			t.Fatalf("cursor skip line = %v", line)
		}
	}
}
