// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.
package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// An attempt the owner fence refused at its first write as a stale owner is
// the round's ownership rejection, as the Runner's own check before dispatch
// reported it: run_one says ownership_rejected, the attempt does not count as
// executed, and nothing backs off -- the next round reads ownership again.
func TestAnAttemptRefusedAtItsBeginIsAnOwnershipRejection(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	executor := &failingExecutor{err: &execution.SlotBeginRefusedError{Status: execution.ProgressStaleOwner, Err: ownership.ErrStaleFence}, failures: 1}
	runner := newBackoffTestRunner(t, clock, executor)
	observed := observeRunnerReturns(runner)
	_, attempted, err := runner.RunOne(context.Background())
	if attempted || !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("RunOne() attempted=%v err=%v, want an unattempted round carrying the stale fence", attempted, err)
	}
	if len(*observed) != 1 || (*observed)[0].RunOutcome != "ownership_rejected" {
		t.Fatalf("run_one lines %+v, want one ownership_rejected", *observed)
	}
	if !runner.NextReadyAt().IsZero() {
		t.Fatalf("NextReadyAt() = %v, want no backoff after an ownership rejection", runner.NextReadyAt())
	}
}

// A begin refused for a moved content scope is the lease holding and the view
// behind the record: the view gate's answer, retried on its delay. Retried at
// once, a view that stays behind would spin a round per tick on the refusal.
func TestABeginRefusedForAMovedScopeWaitsForTheView(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	executor := &failingExecutor{err: &execution.SlotBeginRefusedError{Status: execution.ProgressContentMoved, Err: ownership.ErrContentScopeMoved}, failures: 1}
	runner := newBackoffTestRunner(t, clock, executor)
	observed := observeRunnerReturns(runner)
	result, attempted, err := runner.RunOne(context.Background())
	if err != nil || !attempted || result.ReasonCode != execution.ReasonCode(contract.ReasonViewNotExecutable) {
		t.Fatalf("RunOne() = (%+v, %v, %v), want the view's refusal", result, attempted, err)
	}
	if len(*observed) != 1 || (*observed)[0].RunOutcome != "view_not_executable" {
		t.Fatalf("run_one lines %+v, want one view_not_executable", *observed)
	}
	if !runner.NextReadyAt().After(clock.Now()) {
		t.Fatalf("NextReadyAt() = %v at %v, want the round held for the view", runner.NextReadyAt(), clock.Now())
	}
}

func observeRunnerReturns(runner *Runner) *[]observability.Observation {
	observed := &[]observability.Observation{}
	runner.flights.observer = observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Stage == observability.StageRunnerReturned {
			*observed = append(*observed, o)
		}
	})
	return observed
}
