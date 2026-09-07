// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// failingExecutor returns err for the first failures calls and then completes.
type failingExecutor struct {
	mu       sync.Mutex
	err      error
	failures int
	calls    int
	requests []execution.SlotExecutionRequest
}

func (executor *failingExecutor) Execute(
	_ context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	executor.requests = append(executor.requests, request)
	if executor.failures < 0 || executor.calls <= executor.failures {
		return execution.SlotExecutionResult{}, executor.err
	}
	return execution.SlotExecutionResult{Completed: true}, nil
}

func (executor *failingExecutor) Calls() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls
}

func (executor *failingExecutor) Request(index int) execution.SlotExecutionRequest {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.requests[index]
}

func newBackoffTestRunner(t *testing.T, clock *mutableClock, executor Executor) *Runner {
	t.Helper()
	slot := frozenSlot("query-group-1")
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot}, executor, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestRunnerBacksOffAfterExecutionErrorAndIncrementsAttempt(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	executionErr := errors.New("commit progress: redis timeout")
	executor := &failingExecutor{err: executionErr, failures: 2}
	runner := newBackoffTestRunner(t, clock, executor)

	if _, attempted, err := runner.RunOne(context.Background()); !errors.Is(err, executionErr) || !attempted {
		t.Fatalf("RunOne(first failure) attempted=%t err=%v, want attempted with execution error", attempted, err)
	}
	firstReadyAt := clock.Now().Add(retryDelay(limits, "query-group-1", 1))
	if got := runner.NextReadyAt(); !got.Equal(firstReadyAt) || got.IsZero() {
		t.Fatalf("NextReadyAt() after first failure = %s, want %s", got, firstReadyAt)
	}
	if retryDelay(limits, "query-group-1", 1) != limits.RetryMinDelay {
		t.Fatalf("first retry delay = %s, want RetryMinDelay %s", retryDelay(limits, "query-group-1", 1), limits.RetryMinDelay)
	}

	// Within the backoff window the Runner must not execute again.
	result, attempted, err := runner.RunOne(context.Background())
	if err != nil || attempted || result != (execution.SlotExecutionResult{}) || executor.Calls() != 1 {
		t.Fatalf("RunOne(within backoff) = (%+v, %t, %v) calls=%d, want skipped with one execution", result, attempted, err, executor.Calls())
	}

	clock.Advance(limits.RetryMinDelay)
	if _, attempted, err := runner.RunOne(context.Background()); !errors.Is(err, executionErr) || !attempted || executor.Calls() != 2 {
		t.Fatalf("RunOne(second failure) attempted=%t err=%v calls=%d", attempted, err, executor.Calls())
	}
	second := executor.Request(1)
	if second.AttemptNo != 2 || second.Operation != execution.OperationRetry {
		t.Fatalf("second request attempt/operation = %d/%s, want 2/%s", second.AttemptNo, second.Operation, execution.OperationRetry)
	}
	secondDelay := retryDelay(limits, "query-group-1", 2)
	if secondDelay <= limits.RetryMinDelay || secondDelay > limits.RetryMaxDelay {
		t.Fatalf("second retry delay = %s, want doubled with jitter within (%s, %s]", secondDelay, limits.RetryMinDelay, limits.RetryMaxDelay)
	}
	if got := runner.NextReadyAt(); !got.Equal(clock.Now().Add(secondDelay)) {
		t.Fatalf("NextReadyAt() after second failure = %s, want %s", got, clock.Now().Add(secondDelay))
	}

	clock.Advance(secondDelay)
	result, attempted, err = runner.RunOne(context.Background())
	if err != nil || !attempted || !result.Completed || executor.Calls() != 3 {
		t.Fatalf("RunOne(recovered) = (%+v, %t, %v) calls=%d", result, attempted, err, executor.Calls())
	}
	if executor.Request(2).AttemptNo != 3 {
		t.Fatalf("recovered request attempt = %d, want 3", executor.Request(2).AttemptNo)
	}
	if !runner.NextReadyAt().IsZero() || runner.attempt != nil {
		t.Fatalf("completed Slot kept backoff state: readyAt=%s attempt=%+v", runner.NextReadyAt(), runner.attempt)
	}
}

func TestRunnerExecutionBackoffGrowsToMaxWithoutAttemptCap(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	executor := &failingExecutor{err: errors.New("persistent execution failure"), failures: -1}
	runner := newBackoffTestRunner(t, clock, executor)

	var delays []time.Duration
	for attempt := 1; attempt <= 12; attempt++ {
		if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
			t.Fatalf("RunOne(%d) attempted=%t err=%v, want attempted failure", attempt, attempted, err)
		}
		if executor.Request(attempt-1).AttemptNo != uint32(attempt) {
			t.Fatalf("request %d attempt number = %d", attempt, executor.Request(attempt-1).AttemptNo)
		}
		delay := runner.NextReadyAt().Sub(clock.Now())
		if delay < limits.RetryMinDelay || delay > limits.RetryMaxDelay {
			t.Fatalf("attempt %d delay = %s, want within [%s, %s]", attempt, delay, limits.RetryMinDelay, limits.RetryMaxDelay)
		}
		delays = append(delays, delay)
		clock.Advance(delay)
	}
	if delays[len(delays)-1] != limits.RetryMaxDelay {
		t.Fatalf("late delays = %v, want saturation at %s", delays, limits.RetryMaxDelay)
	}
	if executor.Calls() != 12 {
		t.Fatalf("executor calls = %d, want every attempt admitted after its backoff", executor.Calls())
	}
}

func TestRunnerDoesNotBackOffCancellationOrOwnershipExecutionErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		ctx  func() context.Context
	}{
		{name: "stale fence", err: ownership.ErrStaleFence},
		{name: "not desired", err: ownership.ErrNotDesired},
		{name: "slot ownership changed", err: ErrSlotOwnershipChanged},
		{name: "context canceled", err: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newMutableClock(time.Unix(200, 0))
			executor := &failingExecutor{err: test.err, failures: -1}
			runner := newBackoffTestRunner(t, clock, executor)
			if _, attempted, err := runner.RunOne(context.Background()); !errors.Is(err, test.err) || !attempted {
				t.Fatalf("RunOne() attempted=%t err=%v, want %v", attempted, err, test.err)
			}
			if !runner.NextReadyAt().IsZero() || runner.attempt != nil {
				t.Fatalf("%s scheduled an execution backoff: readyAt=%s attempt=%+v", test.name, runner.NextReadyAt(), runner.attempt)
			}
		})
	}
}

func TestRunnerDoesNotBackOffWhenRunContextIsCancelledDuringExecute(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	ctx, cancel := context.WithCancel(context.Background())
	executor := &cancellingExecutor{cancel: cancel}
	runner := newBackoffTestRunner(t, clock, executor)
	if _, attempted, err := runner.RunOne(ctx); err == nil || !attempted {
		t.Fatalf("RunOne() attempted=%t err=%v, want attempted error", attempted, err)
	}
	if !runner.NextReadyAt().IsZero() || runner.attempt != nil {
		t.Fatalf("cancelled execution scheduled a backoff: readyAt=%s attempt=%+v", runner.NextReadyAt(), runner.attempt)
	}
}

type cancellingExecutor struct{ cancel context.CancelFunc }

func (executor *cancellingExecutor) Execute(
	context.Context,
	execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.cancel()
	return execution.SlotExecutionResult{}, errors.New("execution interrupted by shutdown")
}

func TestRunnerKeepsBackoffForExpiredReplaySlot(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	slot := frozenSlot("query-group-1")
	slot.Recovery = SlotRecoveryFacts{Disposition: ReplayExpired, Distance: limits.MaxReplaySlots + 1, Age: limits.MaxReplayAge - time.Second}
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	executor := &failingExecutor{err: errors.New("query-free finalization failed"), failures: -1}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot}, executor, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if _, attempted, err := runner.RunOne(context.Background()); err == nil || !attempted {
			t.Fatalf("RunOne(%d) attempted=%t err=%v", attempt, attempted, err)
		}
		request := executor.Request(attempt - 1)
		if request.Operation != execution.OperationNormal || !request.ReplayExpired || request.AttemptNo != uint32(attempt) {
			t.Fatalf("expired replay request %d = op %s expired %t attempt %d", attempt, request.Operation, request.ReplayExpired, request.AttemptNo)
		}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || attempted || executor.Calls() != attempt {
			t.Fatalf("RunOne(%d within backoff) attempted=%t err=%v calls=%d", attempt, attempted, err, executor.Calls())
		}
		if attempt < 3 {
			clock.Advance(runner.NextReadyAt().Sub(clock.Now()))
		}
	}
	if third := runner.NextReadyAt().Sub(clock.Now()); third <= limits.RetryMinDelay {
		t.Fatalf("expired replay lost its growing backoff: next ready in %s", third)
	}
}
