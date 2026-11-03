// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// retryReadingExecutor fails with err and records what the execution was told
// about that failure being retried.
type retryReadingExecutor struct {
	err     error
	retried bool
}

func (executor *retryReadingExecutor) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.retried = FailureRetried(ctx, executor.err)
	return execution.SlotExecutionResult{}, executor.err
}

// The execution is told a failure will be retried exactly when the Runner
// then backs off and retries it: an ordinary error under recovery is
// retried, an ownership refusal or a cancellation is not, and a Runner
// without recovery retries nothing.
func TestTheExecutionKnowsWhenItsFailureIsRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"an ordinary failure", errors.New("query permit deadline")},
		{"a stale fence", ownership.ErrStaleFence},
		{"ownership moved", ErrSlotOwnershipChanged},
		{"a cancellation", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newMutableClock(time.Unix(200, 0))
			executor := &retryReadingExecutor{err: tc.err}
			runner := newBackoffTestRunner(t, clock, executor)
			_, _, _ = runner.RunOne(context.Background())
			backedOff := !runner.NextReadyAt().IsZero()
			if executor.retried != backedOff {
				t.Fatalf("told retried=%t, runner backed off=%t", executor.retried, backedOff)
			}
		})
	}
	slot := frozenSlot("query-group-1")
	executor := &retryReadingExecutor{err: errors.New("query permit deadline")}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot}, executor, NewFlightCoordinator(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = runner.RunOne(context.Background())
	if executor.retried {
		t.Fatal("a Runner without recovery told its execution the failure would be retried")
	}
}
