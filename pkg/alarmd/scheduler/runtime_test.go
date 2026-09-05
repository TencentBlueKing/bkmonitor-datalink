// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
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
	var operation execution.Operation
	if _, attempted, denied, runErr := runner.RunOneAdmitted(context.Background(), func(actual execution.Operation) (func(), bool) {
		operation = actual
		return func() {}, true
	}); runErr != nil || denied || !attempted || source.calls != 2 {
		t.Fatalf("RunOne(after repair) attempted=%t denied=%t calls=%d err=%v", attempted, denied, source.calls, runErr)
	}
	if operation != execution.OperationNormal {
		t.Fatalf("operation after source retry = %s, want %s", operation, execution.OperationNormal)
	}
}

func TestRunnerNextReadyAtUsesLatestSourceOrExecutionBackoff(t *testing.T) {
	base := time.Unix(200, 0)
	tests := []struct {
		name         string
		sourceNextAt time.Time
		attemptNext  time.Time
		want         time.Time
	}{
		{name: "immediate"},
		{name: "source", sourceNextAt: base.Add(time.Second), attemptNext: base, want: base.Add(time.Second)},
		{name: "execution", sourceNextAt: base, attemptNext: base.Add(2 * time.Second), want: base.Add(2 * time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &Runner{sourceNextAt: test.sourceNextAt}
			if !test.attemptNext.IsZero() {
				runner.attempt = &recoveryAttempt{nextAt: test.attemptNext}
			}
			if got := runner.NextReadyAt(); !got.Equal(test.want) {
				t.Fatalf("NextReadyAt() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestRunnerReusesDelayedReadyAtForExecutionReadiness(t *testing.T) {
	current := time.UnixMilli(1_700_000_000_000)
	readyAt := current.Add(30 * time.Second)
	slot := frozenSlot("query-group-1")
	executor := &readinessDeferredExecutor{readyAt: readyAt}
	runner, err := NewRunner(
		"query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot}, executor,
		NewFlightCoordinator(), func() time.Time { return current },
	)
	if err != nil {
		t.Fatal(err)
	}
	admissions, releases := 0, 0
	admission := func(execution.Operation) (func(), bool) {
		admissions++
		return func() { releases++ }, true
	}
	result, attempted, denied, err := runner.RunOneAdmitted(context.Background(), admission)
	if err != nil || !attempted || denied || result != (execution.SlotExecutionResult{}) {
		t.Fatalf("RunOneAdmitted(deferred)=(%+v,%t,%t,%v), want zero/true/false/nil", result, attempted, denied, err)
	}
	if admissions != 1 || releases != 1 {
		t.Fatalf("deferred admission calls/releases=%d/%d, want 1/1", admissions, releases)
	}
	if got := runner.NextReadyAt(); !got.Equal(readyAt) {
		t.Fatalf("NextReadyAt()=%s, want %s", got, readyAt)
	}
	if _, attempted, denied, err = runner.RunOneAdmitted(context.Background(), admission); err != nil || attempted || denied ||
		executor.calls != 1 || admissions != 1 || releases != 1 {
		t.Fatalf("RunOneAdmitted(before ready) attempted=%t denied=%t calls=%d admission=%d/%d err=%v",
			attempted, denied, executor.calls, admissions, releases, err)
	}
	current = readyAt
	executor.deferred = false
	result, attempted, denied, err = runner.RunOneAdmitted(context.Background(), admission)
	if err != nil || !attempted || denied || !result.Completed || executor.calls != 2 || admissions != 2 || releases != 2 {
		t.Fatalf("RunOneAdmitted(at ready)=(%+v,%t,%t,%v) calls=%d admission=%d/%d",
			result, attempted, denied, err, executor.calls, admissions, releases)
	}
}

func TestRunnerUsesFrozenOperationForExecutionAdmission(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	slot := frozenSlot("query-group-1")
	slot.Dispatch.Operation = execution.OperationReplay
	slot.Recovery = SlotRecoveryFacts{Disposition: ReplayEligible, Distance: 1, Age: time.Minute}
	source := &fakeSlotSource{slot: slot}
	executor := &blockingExecutor{}
	runner, err := NewRunner(
		"query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, source, executor,
		NewFlightCoordinator(), func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	var operation execution.Operation
	result, attempted, denied, err := runner.RunOneAdmitted(context.Background(), func(actual execution.Operation) (func(), bool) {
		operation = actual
		return nil, false
	})
	if err != nil || !denied || attempted || result != (execution.SlotExecutionResult{}) {
		t.Fatalf("RunOneAdmitted(denied) = (%+v, %t, %t, %v)", result, attempted, denied, err)
	}
	if operation != execution.OperationReplay {
		t.Fatalf("admission operation = %s, want %s", operation, execution.OperationReplay)
	}
	if source.calls != 1 || executor.calls != 0 || runner.attempt != nil {
		t.Fatalf("denied admission changed execution state: source=%d executor=%d attempt=%+v",
			source.calls, executor.calls, runner.attempt)
	}
	released := false
	result, attempted, denied, err = runner.RunOneAdmitted(context.Background(), func(actual execution.Operation) (func(), bool) {
		if actual != execution.OperationReplay {
			t.Fatalf("second admission operation = %s, want %s", actual, execution.OperationReplay)
		}
		return func() { released = true }, true
	})
	if err != nil || denied || !attempted || !result.Completed {
		t.Fatalf("RunOneAdmitted(retry) = (%+v, %t, %t, %v)", result, attempted, denied, err)
	}
	if source.calls != 2 || executor.calls != 1 || !released {
		t.Fatalf("re-read open Slot state: source=%d executor=%d released=%t, want 2/1/true",
			source.calls, executor.calls, released)
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

type readinessDeferredExecutor struct {
	readyAt  time.Time
	deferred bool
	calls    int
}

type readinessDeferredTestError struct{ readyAt time.Time }

func (err readinessDeferredTestError) Error() string               { return "readiness deferred" }
func (err readinessDeferredTestError) ReadinessReadyAt() time.Time { return err.readyAt }

func (executor *readinessDeferredExecutor) Execute(
	_ context.Context,
	_ execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.calls++
	if executor.deferred || executor.calls == 1 {
		return execution.SlotExecutionResult{}, fmt.Errorf("wrapped executor result: %w", readinessDeferredTestError{readyAt: executor.readyAt})
	}
	return execution.SlotExecutionResult{Completed: true}, nil
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
