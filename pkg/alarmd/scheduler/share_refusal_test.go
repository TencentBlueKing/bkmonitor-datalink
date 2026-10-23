// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// shareRefusalError stands for the worker's provisional budget rejection as
// Execute returns it: deterministic for the Slot's own total over the share,
// not for a pool others filled.
type shareRefusalError struct {
	refusal       execution.ShareRefusal
	deterministic bool
}

func (err *shareRefusalError) Error() string {
	return "alarmd worker: provisional retained_bytes budget exceeded"
}

func (err *shareRefusalError) DeterministicShareRefusal() (execution.ShareRefusal, bool) {
	return err.refusal, err.deterministic
}

func ownTotalOverShare() error {
	return fmt.Errorf("alarmd worker: complete Slot: %w", &shareRefusalError{deterministic: true,
		refusal: execution.ShareRefusal{Phase: "normal_output", OwnUsedBytes: 530 << 20, RequestedBytes: 4 << 20, LimitBytes: 512 << 20}})
}

func poolPressure() error {
	return fmt.Errorf("alarmd worker: complete Slot: %w", &shareRefusalError{deterministic: false,
		refusal: execution.ShareRefusal{Phase: "normal_output", OwnUsedBytes: 100 << 20, RequestedBytes: 4 << 20, LimitBytes: 512 << 20}})
}

func shareSlot(evaluation int64) FrozenSlot {
	slot := frozenSlot("query-group-101")
	slot.Contract.Slot.EvaluationTime = execution.EvaluationTime(evaluation)
	slot.Dispatch.OwnerFence.QueryGroup = "query-group-101"
	slot.DuePlanTargets.Plans = []execution.PlanKey{{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "101"}}}
	slot.EarliestQueryDeadlineUnixMilli = evaluation*1000 + 1_000
	slot.RecoveryUntilUnixMilli = evaluation*1000 + 601_000
	slot.KeepUntilUnixMilli = evaluation*1000 + 677_000
	slot.ExpectedNextSlot = slot.Contract.Slot.EvaluationTime
	return slot
}

// advancingSource hands out its Slots in order, moving on when the executor
// completes the current one, as the Progress cursor does.
type advancingSource struct {
	mu    sync.Mutex
	slots []FrozenSlot
	index int
}

func (source *advancingSource) Next(context.Context, execution.QueryGroupIdentity) (FrozenSlot, bool, SlotDueFacts, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.index >= len(source.slots) {
		return FrozenSlot{}, false, SlotDueFacts{}, nil
	}
	return source.slots[source.index], true, SlotDueFacts{}, nil
}

func (source *advancingSource) advance() {
	source.mu.Lock()
	source.index++
	source.mu.Unlock()
}

// shareExecutor plays the worker: a request marked ShareExceeded is
// finalized query-free as RESOURCE_SHARE_EXCEEDED; any other runs a query and
// returns the next scripted error, or completes the Slot when the script is
// spent.
type shareExecutor struct {
	source   *advancingSource
	errs     []error
	requests []execution.SlotExecutionRequest
	queries  int
}

func (executor *shareExecutor) Execute(_ context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.requests = append(executor.requests, request)
	if request.ShareExceeded {
		executor.source.advance()
		return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionGapSkipped,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			QueryFreeCause: execution.CauseResourceShareExceeded}, nil
	}
	executor.queries++
	if len(executor.errs) > 0 {
		err := executor.errs[0]
		executor.errs = executor.errs[1:]
		if err != nil {
			return execution.SlotExecutionResult{}, err
		}
	}
	executor.source.advance()
	return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull, Result: observability.ResultSuccess}, nil
}

func newShareRunner(t *testing.T, clock *mutableClock, errs ...error) (*Runner, *shareExecutor, *advancingSource) {
	t.Helper()
	return newShareRunnerOver(t, clock, []int64{100, 160}, errs...)
}

func newShareRunnerOver(t *testing.T, clock *mutableClock, evaluations []int64, errs ...error) (*Runner, *shareExecutor, *advancingSource) {
	t.Helper()
	source := &advancingSource{}
	for _, evaluation := range evaluations {
		source.slots = append(source.slots, shareSlot(evaluation))
	}
	executor := &shareExecutor{source: source, errs: errs}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-101", &fakeSession{fence: source.slots[0].Dispatch.OwnerFence}, source, executor, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return runner, executor, source
}

// The production case: a Slot whose own total is over its share is refused
// the same way on every read of its window. The second identical refusal
// decides it: the next round finalizes it without a query as a gap named
// RESOURCE_SHARE_EXCEEDED, writes the unsampled slot_skipped line with the
// refusal's bytes, and the Query Group's next Slot runs - after two reads of
// the window, not a dozen over ten minutes.
func TestTwoIdenticalShareRefusalsFinalizeTheSlotAsANamedGap(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	runner, executor, _ := newShareRunner(t, clock, ownTotalOverShare(), ownTotalOverShare())
	observer, output := sampledLog(t)
	runner.WithSlotSkipReport(observer, nil)

	if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
		t.Fatalf("first refusal: attempted=%t err=%v", attempted, err)
	}
	if runner.NextReadyAt().IsZero() {
		t.Fatal("a first share refusal was not backed off: one refusal decides nothing yet")
	}
	clock.Advance(testRecoveryLimits().RetryMinDelay)
	if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
		t.Fatalf("second refusal: attempted=%t err=%v", attempted, err)
	}
	if !runner.NextReadyAt().IsZero() {
		t.Fatalf("the deciding refusal backed off to %s: the finalization would wait for nothing", runner.NextReadyAt())
	}
	result, attempted, err := runner.RunOne(context.Background())
	if err != nil || !attempted || !result.Completed || result.QueryFreeCause != execution.CauseResourceShareExceeded {
		t.Fatalf("finalization: result=%+v attempted=%t err=%v", result, attempted, err)
	}
	if last := executor.requests[len(executor.requests)-1]; !last.ShareExceeded || last.Contract.Slot.EvaluationTime != 100 {
		t.Fatalf("finalizing request = %+v, want Slot 100 marked ShareExceeded", last)
	}
	if executor.queries != 2 {
		t.Fatalf("queries = %d, want the two refused reads and none after the decision", executor.queries)
	}
	lines := logLines(t, output, observability.StageSlotSkipped)
	if len(lines) != 1 {
		t.Fatalf("slot_skipped lines = %d, want 1: %s", len(lines), output.String())
	}
	line := lines[0]
	if line["skip_cause"] != observability.CompletionCauseResourceShareExceeded || line["skip_reason"] != contract.ReasonGapSkipped ||
		line["finalizing_path"] != observability.SlotSkippedQueryFree || line["share_refusal_phase"] != "normal_output" ||
		number(line, "share_own_used_bytes") != 530<<20 || number(line, "share_limit_bytes") != 512<<20 ||
		number(line, "share_refusals") != ShareRefusalsToFinalize || line["query_group_key"] != "query-group-101" {
		t.Fatalf("slot_skipped line = %v", line)
	}

	// The next Slot runs, as a normal round, with nothing carried over.
	result, attempted, err = runner.RunOne(context.Background())
	if err != nil || !attempted || !result.Completed || result.CompletionKind != execution.CompletionFull {
		t.Fatalf("next Slot: result=%+v attempted=%t err=%v", result, attempted, err)
	}
	if next := executor.requests[len(executor.requests)-1]; next.Contract.Slot.EvaluationTime != 160 || next.ShareExceeded {
		t.Fatalf("next Slot request = %+v, want Slot 160 queried", next)
	}
	if runner.shareRefusal != nil {
		t.Fatalf("share refusal state outlived its Slot: %+v", runner.shareRefusal)
	}
}

// A refusal on the pool says other Query Groups hold part of it, and they let
// go: it is retried with backoff as before, however often it repeats, and the
// Slot is never given up for it.
func TestAPoolPressureRefusalKeepsRetrying(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	runner, executor, _ := newShareRunner(t, clock, poolPressure(), poolPressure(), poolPressure(), poolPressure())
	for attempt := 1; attempt <= 4; attempt++ {
		if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
			t.Fatalf("attempt %d: attempted=%t err=%v", attempt, attempted, err)
		}
		readyAt := runner.NextReadyAt()
		if readyAt.IsZero() {
			t.Fatalf("attempt %d: a pool refusal was not backed off", attempt)
		}
		clock.Advance(readyAt.Sub(clock.Now()))
	}
	for _, request := range executor.requests {
		if request.ShareExceeded {
			t.Fatalf("a pool refusal finalized the Slot: %+v", request)
		}
	}
	if result, _, err := runner.RunOne(context.Background()); err != nil || !result.Completed || result.CompletionKind != execution.CompletionFull {
		t.Fatalf("after the pool freed: result=%+v err=%v, want the Slot queried and completed", result, err)
	}
}

// One refusal and then a read that fits: the Slot completes from its query
// and no gap is written. The count belongs to the Slot and ends with it.
func TestASingleShareRefusalThenSuccessWritesNoGap(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	runner, executor, _ := newShareRunner(t, clock, ownTotalOverShare())
	observer, output := sampledLog(t)
	runner.WithSlotSkipReport(observer, nil)
	if _, _, err := runner.RunOne(context.Background()); err == nil {
		t.Fatal("first attempt was not refused")
	}
	clock.Advance(testRecoveryLimits().RetryMinDelay)
	result, attempted, err := runner.RunOne(context.Background())
	if err != nil || !attempted || !result.Completed || result.CompletionKind != execution.CompletionFull || result.QueryFreeCause != "" {
		t.Fatalf("second attempt: result=%+v attempted=%t err=%v", result, attempted, err)
	}
	for _, request := range executor.requests {
		if request.ShareExceeded {
			t.Fatalf("one refusal finalized the Slot: %+v", request)
		}
	}
	if lines := logLines(t, output, observability.StageSlotSkipped); len(lines) != 0 {
		t.Fatalf("slot_skipped lines = %v, want none", lines)
	}
	if runner.shareRefusal != nil {
		t.Fatalf("share refusal state outlived its Slot: %+v", runner.shareRefusal)
	}
}

// Two refusals that are not the same decision - a different phase, or a share
// a resize changed between them - do not add up: the count starts again.
func TestShareRefusalsOfDifferentPhasesDoNotAddUp(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	input := fmt.Errorf("wrapped: %w", &shareRefusalError{deterministic: true,
		refusal: execution.ShareRefusal{Phase: "normal_input", OwnUsedBytes: 530 << 20, LimitBytes: 512 << 20}})
	runner, executor, _ := newShareRunner(t, clock, input, ownTotalOverShare())
	for attempt := 1; attempt <= 2; attempt++ {
		if _, _, err := runner.RunOne(context.Background()); err == nil {
			t.Fatalf("attempt %d was not refused", attempt)
		}
		if runner.NextReadyAt().IsZero() {
			t.Fatalf("attempt %d: refusal of a different phase decided the Slot", attempt)
		}
		clock.Advance(runner.NextReadyAt().Sub(clock.Now()))
	}
	if result, _, err := runner.RunOne(context.Background()); err != nil || result.QueryFreeCause != "" || executor.queries != 3 {
		t.Fatalf("third attempt: result=%+v err=%v queries=%d, want queried", result, err, executor.queries)
	}
}

// Once a Slot of the group was finalized for the share, the group's next Slot
// is finalized on its first identical refusal: one read per Slot of an
// oversized strategy, not two and a backoff. A Slot that fits clears the
// key, and the next refusal is a first one again, needing two.
func TestAfterAShareFinalizationTheNextSlotNeedsOneRefusal(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	runner, executor, _ := newShareRunnerOver(t, clock, []int64{100, 160, 220, 280},
		ownTotalOverShare(), ownTotalOverShare(), // Slot 100: first time, two refusals
		ownTotalOverShare(),                      // Slot 160: one refusal
		nil,                                      // Slot 220: fits
		ownTotalOverShare(), ownTotalOverShare()) // Slot 280: first time again
	run := func(step string) execution.SlotExecutionResult {
		t.Helper()
		result, attempted, _ := runner.RunOne(context.Background())
		if !attempted {
			t.Fatalf("%s: not attempted", step)
		}
		if readyAt := runner.NextReadyAt(); !readyAt.IsZero() {
			clock.Advance(readyAt.Sub(clock.Now()))
		}
		return result
	}
	finalized := func(step string, evaluation int64) {
		t.Helper()
		result := run(step)
		last := executor.requests[len(executor.requests)-1]
		if result.QueryFreeCause != execution.CauseResourceShareExceeded || !last.ShareExceeded ||
			int64(last.Contract.Slot.EvaluationTime) != evaluation {
			t.Fatalf("%s: result=%+v request=%+v, want Slot %d finalized for its share", step, result, last, evaluation)
		}
	}

	if _, _, err := runner.RunOne(context.Background()); err == nil {
		t.Fatal("Slot 100 first refusal did not fail")
	}
	if runner.NextReadyAt().IsZero() || runner.shareExceeded(shareSlot(100)) {
		t.Fatal("one refusal of a first-time key finalized the Slot")
	}
	clock.Advance(runner.NextReadyAt().Sub(clock.Now()))
	run("Slot 100 second refusal")
	finalized("Slot 100 finalization", 100)
	if executor.queries != 2 {
		t.Fatalf("queries after Slot 100 = %d, want 2", executor.queries)
	}

	run("Slot 160 refusal")
	finalized("Slot 160 finalization", 160)
	if executor.queries != 3 {
		t.Fatalf("queries after Slot 160 = %d, want 3: one read for the group's second Slot", executor.queries)
	}

	if result := run("Slot 220"); result.CompletionKind != execution.CompletionFull {
		t.Fatalf("Slot 220 = %+v, want it to fit and complete", result)
	}
	if runner.shareRefusal != nil {
		t.Fatalf("a Slot that fit left the key: %+v", runner.shareRefusal)
	}

	if _, _, err := runner.RunOne(context.Background()); err == nil {
		t.Fatal("Slot 280 first refusal did not fail")
	}
	if runner.NextReadyAt().IsZero() || runner.shareExceeded(shareSlot(280)) {
		t.Fatal("after a Slot that fit, one refusal finalized the next Slot: the key was not cleared")
	}
	clock.Advance(runner.NextReadyAt().Sub(clock.Now()))
	run("Slot 280 second refusal")
	finalized("Slot 280 finalization", 280)
	if executor.queries != 6 {
		t.Fatalf("queries = %d, want 6", executor.queries)
	}
}

// A refusal against another share - the replica was resized - is a new key:
// the group's next Slot needs two refusals again.
func TestAShareChangeStartsTheKeyAgain(t *testing.T) {
	clock := newMutableClock(time.Unix(102, 0))
	resized := fmt.Errorf("wrapped: %w", &shareRefusalError{deterministic: true,
		refusal: execution.ShareRefusal{Phase: "normal_output", OwnUsedBytes: 530 << 20, LimitBytes: 256 << 20}})
	runner, executor, _ := newShareRunnerOver(t, clock, []int64{100, 160}, ownTotalOverShare(), ownTotalOverShare(), resized)
	advance := func() {
		if readyAt := runner.NextReadyAt(); !readyAt.IsZero() {
			clock.Advance(readyAt.Sub(clock.Now()))
		}
	}
	// Two refusals and the finalization of Slot 100, each checked: a Slot 100
	// still refused after them would be refused below as well, and the case
	// would pass without its group ever reaching Slot 160.
	for refusal := 1; refusal <= 2; refusal++ {
		// The share's own refusal, not any error: another would read as one.
		var refused *shareRefusalError
		if _, attempted, err := runner.RunOne(context.Background()); !errors.As(err, &refused) || !attempted {
			t.Fatalf("Slot 100 refusal %d: attempted=%v error=%v, want the share's refusal", refusal, attempted, err)
		}
		advance()
	}
	if result, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted ||
		result.QueryFreeCause != execution.CauseResourceShareExceeded {
		t.Fatalf("Slot 100 finalization: result=%+v attempted=%v error=%v, want it finalized for its share", result, attempted, err)
	}
	advance()
	if _, _, err := runner.RunOne(context.Background()); err == nil {
		t.Fatal("Slot 160 was not refused")
	}
	if last := executor.requests[len(executor.requests)-1]; last.Contract.Slot.EvaluationTime != 160 {
		t.Fatalf("the refusal was for Slot %d, want Slot 160", last.Contract.Slot.EvaluationTime)
	}
	if runner.NextReadyAt().IsZero() || runner.shareExceeded(shareSlot(160)) {
		t.Fatal("a refusal against a changed share finalized on one refusal")
	}
}
