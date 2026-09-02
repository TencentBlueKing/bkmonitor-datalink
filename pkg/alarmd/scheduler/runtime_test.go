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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func TestRunnerUsesCurrentOwnerFenceAndOperationNormal(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	session := &fakeSession{fence: fence}
	source := &fakeSlotSource{slot: frozenSlot("query-group-1")}
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	flights := NewFlightCoordinator()
	runner, err := NewRunner("query-group-1", session, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, _, runErr := runner.RunOne(context.Background())
		firstDone <- runErr
	}()
	<-executor.started
	if _, _, err := runner.RunOne(context.Background()); !errors.Is(err, ErrSlotInFlight) {
		t.Fatalf("RunOne(concurrent) error = %v, want ErrSlotInFlight", err)
	}
	close(executor.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("RunOne(first) error = %v", err)
	}
	request := executor.Request()
	if request.Operation != execution.OperationNormal || request.OwnerFence != fence ||
		request.ExpectedNextSlot != request.Contract.Slot.EvaluationTime ||
		!request.DuePlanTargets.Equal(source.slot.DuePlanTargets) ||
		request.EarliestQueryDeadlineUnixMilli != source.slot.EarliestQueryDeadlineUnixMilli {
		t.Fatalf("execution request = %+v", request)
	}
	request.DuePlanTargets.Plans[0].StrategyID = "mutated"
	if source.slot.DuePlanTargets.Plans[0].StrategyID == "mutated" {
		t.Fatal("Runner request aliases FrozenSlot due Plan targets")
	}
}

func TestRunnerSharesQueryGroupSingleFlightAcrossInstances(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	session := &fakeSession{fence: fence}
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	flights := NewFlightCoordinator()
	first, err := NewRunner("query-group-1", session, &fakeSlotSource{slot: frozenSlot("query-group-1")}, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	second, err := NewRunner("query-group-1", session, &fakeSlotSource{slot: frozenSlot("query-group-1")}, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner(second) error = %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, _, runErr := first.RunOne(context.Background())
		firstDone <- runErr
	}()
	<-executor.started
	if _, _, err := second.RunOne(context.Background()); !errors.Is(err, ErrSlotInFlight) {
		t.Fatalf("RunOne(second runner) error = %v, want ErrSlotInFlight", err)
	}
	close(executor.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("RunOne(first) error = %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
}

func TestRunnerRejectsStaleFenceBeforeResolvingSlot(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	session := &fakeSession{err: ownership.ErrStaleFence}
	source := &fakeSlotSource{slot: frozenSlot("query-group-1")}
	executor := &blockingExecutor{}
	runner, err := NewRunner("query-group-1", session, source, executor, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if _, _, err := runner.RunOne(context.Background()); !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("RunOne(stale) error = %v, want ErrStaleFence", err)
	}
	if source.calls != 0 || executor.calls != 0 {
		t.Fatalf("stale fence reached source/executor: source=%d executor=%d", source.calls, executor.calls)
	}
}

func TestRunnerRejectsDispatchFenceThatChangedAfterSlotFreeze(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	slot := frozenSlot("query-group-1")
	session := &sequenceOwnerSession{fences: []execution.OwnerFence{
		slot.Dispatch.OwnerFence,
		{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 2, LeaseToken: "token-2"},
	}}
	executor := &blockingExecutor{}
	runner, err := NewRunner("query-group-1", session, &fakeSlotSource{slot: slot}, executor, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	if _, _, err := runner.RunOne(context.Background()); !errors.Is(err, ErrSlotOwnershipChanged) {
		t.Fatalf("RunOne() error = %v, want ErrSlotOwnershipChanged", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
}

func TestRunnerBacksOffQGLocalSourceFailureAndAutomaticallyRechecks(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	source := &fakeSlotSource{err: &SourceBlockedError{Err: errors.New("exact set unavailable")}}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	result, attempted, err := runner.RunOne(context.Background())
	if err != nil || !attempted || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(execution.ReasonBlockedExactSetUnavailable) || !result.SourceRetry {
		t.Fatalf("RunOne(blocked) = (%+v, %t, %v)", result, attempted, err)
	}
	if _, attempted, err = runner.RunOne(context.Background()); err != nil || attempted || source.calls != 1 {
		t.Fatalf("RunOne(within backoff) attempted=%t calls=%d err=%v", attempted, source.calls, err)
	}
	clock.Advance(testRecoveryLimits().RetryMinDelay)
	source.err = nil
	source.slot = frozenSlot("query-group-1")
	if _, attempted, err = runner.RunOne(context.Background()); err != nil || !attempted || source.calls != 2 {
		t.Fatalf("RunOne(after repair) attempted=%t calls=%d err=%v", attempted, source.calls, err)
	}
}

func frozenSlot(queryGroup execution.QueryGroupIdentity) FrozenSlot {
	contract := execution.FrozenExecutionContractRef{
		Slot:                 execution.SlotIdentity{QueryGroup: queryGroup, EvaluationTime: 100},
		SnapshotRevision:     "snapshot-1",
		QueryRevision:        "query-1",
		ScheduleRevision:     "schedule-1",
		ScheduleSegmentStart: 60,
		DuePlanSetDigest:     "plans-1",
	}
	return FrozenSlot{Contract: contract, Dispatch: SlotDispatchContext{
		Operation:            execution.OperationNormal,
		OwnerFence:           execution.OwnerFence{QueryGroup: queryGroup, OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
		AssignmentGeneration: 1,
	}, DuePlanTargets: execution.FrozenDuePlanTargets{
		DuePlanSetDigest: contract.DuePlanSetDigest,
		Plans:            []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}},
	}, EarliestQueryDeadlineUnixMilli: 101_000, RecoveryUntilUnixMilli: 701_000,
		KeepUntilUnixMilli: 777_000, ExpectedNextSlot: contract.Slot.EvaluationTime}
}

type fakeSession struct {
	fence execution.OwnerFence
	err   error
}

func (session *fakeSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	return session.fence, session.err
}

type fakeSlotSource struct {
	slot  FrozenSlot
	due   bool
	calls int
	err   error
}

func (source *fakeSlotSource) Next(context.Context, execution.QueryGroupIdentity) (FrozenSlot, bool, error) {
	source.calls++
	if source.err != nil {
		return FrozenSlot{}, false, source.err
	}
	if !source.due && source.slot.Contract.Slot.QueryGroup != "" {
		return source.slot, true, nil
	}
	return source.slot, source.due, nil
}

type blockingExecutor struct {
	started chan struct{}
	release chan struct{}

	mu      sync.Mutex
	request execution.SlotExecutionRequest
	calls   int
}

func (executor *blockingExecutor) Execute(
	_ context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.mu.Lock()
	executor.request = request
	executor.calls++
	executor.mu.Unlock()
	if executor.started != nil {
		close(executor.started)
	}
	if executor.release != nil {
		<-executor.release
	}
	return execution.SlotExecutionResult{Completed: true}, nil
}

func (executor *blockingExecutor) Request() execution.SlotExecutionRequest {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.request
}
