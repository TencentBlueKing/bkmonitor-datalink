// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestRunnerRetriesSameFrozenSlotWithoutBlockingHealthyQueryGroup(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatalf("NewFlightCoordinatorWithRecovery() error = %v", err)
	}
	failedExecutor := &scriptedExecutor{results: []execution.SlotExecutionResult{
		{Completed: false, Result: observability.ResultRetrying, ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable)},
		{Completed: true, Result: observability.ResultSuccess},
	}}
	failedSlot := frozenSlot("query-group-failed")
	failed, err := NewRunner("query-group-failed", &fakeSession{fence: failedSlot.Dispatch.OwnerFence},
		&fakeSlotSource{slot: failedSlot}, failedExecutor, flights, clock.Now)
	if err != nil {
		t.Fatalf("NewRunner(failed) error = %v", err)
	}
	healthySlot := frozenSlot("query-group-healthy")
	healthyExecutor := &scriptedExecutor{results: []execution.SlotExecutionResult{{Completed: true, Result: observability.ResultSuccess}}}
	healthy, err := NewRunner("query-group-healthy", &fakeSession{fence: healthySlot.Dispatch.OwnerFence},
		&fakeSlotSource{slot: healthySlot}, healthyExecutor, flights, clock.Now)
	if err != nil {
		t.Fatalf("NewRunner(healthy) error = %v", err)
	}

	first, attempted, err := failed.RunOne(context.Background())
	if err != nil || !attempted || first.Completed || first.Result != observability.ResultRetrying {
		t.Fatalf("failed first RunOne() = (%+v, %v, %v)", first, attempted, err)
	}
	if _, attempted, err := failed.RunOne(context.Background()); err != nil || attempted {
		t.Fatalf("failed backoff RunOne() attempted=%v error=%v", attempted, err)
	}
	if result, attempted, err := healthy.RunOne(context.Background()); err != nil || !attempted || !result.Completed {
		t.Fatalf("healthy RunOne() = (%+v, %v, %v)", result, attempted, err)
	}

	clock.Advance(limits.RetryMinDelay)
	result, attempted, err := failed.RunOne(context.Background())
	if err != nil || !attempted || !result.Completed {
		t.Fatalf("failed retry RunOne() = (%+v, %v, %v)", result, attempted, err)
	}
	want := []execution.Operation{execution.OperationNormal, execution.OperationRetry}
	if got := failedExecutor.Operations(); !equalOperations(got, want) {
		t.Fatalf("failed operations = %v, want %v", got, want)
	}
	requests := failedExecutor.Requests()
	if requests[0].Contract != requests[1].Contract || requests[0].ExpectedNextSlot != requests[1].ExpectedNextSlot {
		t.Fatalf("retry changed frozen Slot: first=%+v retry=%+v", requests[0], requests[1])
	}
}

func TestRunnerUsesAtMostOneProbeForRetryingPartialSlot(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatalf("NewFlightCoordinatorWithRecovery() error = %v", err)
	}
	slot := frozenSlot("query-group-1")
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{
		{Completed: false, Result: observability.ResultRetrying, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial)},
		{Completed: false, Result: observability.ResultRetrying, ReasonCode: execution.ReasonCode(contract.ReasonQueryPartial)},
		{Completed: true, Result: observability.ResultSuccess},
	}}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
		&fakeSlotSource{slot: slot}, executor, flights, clock.Now)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	for index := 0; index < 3; index++ {
		if index > 0 {
			clock.Advance(limits.RetryMaxDelay)
		}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("RunOne(%d) attempted=%v error=%v", index, attempted, err)
		}
	}
	want := []execution.Operation{execution.OperationNormal, execution.OperationProbe, execution.OperationRetry}
	if got := executor.Operations(); !equalOperations(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func TestRunnerStopsRetryWhenFrozenSlotExceedsReplayEligibility(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	slot := frozenSlot("query-group-1")
	source := &fakeSlotSource{slot: slot}
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{
		{Completed: false, Result: observability.ResultRetrying, ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable)},
		{Completed: true, Result: observability.ResultSuccess},
	}}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, source, executor, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("first RunOne() attempted=%v error=%v", attempted, err)
	}
	clock.Advance(limits.RetryMaxDelay)
	source.slot.Recovery = SlotRecoveryFacts{Disposition: ReplayExpired, Distance: limits.MaxReplaySlots + 1, Age: limits.MaxReplayAge + time.Second}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("expired RunOne() attempted=%v error=%v", attempted, err)
	}
	want := []execution.Operation{execution.OperationNormal, execution.OperationNormal}
	if got := executor.Operations(); !equalOperations(got, want) {
		t.Fatalf("expired operations = %v, want %v", got, want)
	}
}

func TestQueryPermitPoolHasProcessAndRecoveryBoundsWithoutStarvation(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits = 2
	limits.RecoveryQueryPermits = 1
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatalf("NewFlightCoordinatorWithRecovery() error = %v", err)
	}
	ctx := context.Background()
	first, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "normal-1", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "normal-2", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	recoveryResult := make(chan *QueryPermit, 1)
	normalResult := make(chan *QueryPermit, 1)
	go func() {
		permit, acquireErr := flights.AcquireQueryPermit(ctx,
			execution.SlotIdentity{QueryGroup: "recovery", EvaluationTime: 60}, execution.OperationReplay, clock.Now().Add(time.Minute))
		if acquireErr == nil {
			recoveryResult <- permit
		}
	}()
	go func() {
		permit, acquireErr := flights.AcquireQueryPermit(ctx,
			execution.SlotIdentity{QueryGroup: "normal-3", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
		if acquireErr == nil {
			normalResult <- permit
		}
	}()
	waitForPermitQueue(t, flights, 1, 1)
	first.Release()
	recovery := receivePermit(t, recoveryResult)
	if recovery.RecoveryPermit() == nil {
		t.Fatal("replay permit did not carry a recovery permit")
	}
	if snapshot := flights.queryPermitSnapshot(); snapshot.Inflight != 2 || snapshot.RecoveryInflight != 1 {
		t.Fatalf("snapshot after recovery grant = %+v", snapshot)
	}

	second.Release()
	normal := receivePermit(t, normalResult)
	if normal.RecoveryPermit() != nil {
		t.Fatal("normal permit unexpectedly carried a recovery permit")
	}
	if snapshot := flights.queryPermitSnapshot(); snapshot.Inflight > limits.ProcessQueryPermits ||
		snapshot.RecoveryInflight > limits.RecoveryQueryPermits {
		t.Fatalf("permit bounds exceeded: %+v", snapshot)
	}
	recovery.Release()
	normal.Release()
}

func TestQueryPermitCancellationRemovesBoundedWaiter(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "normal-1", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "normal-2", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := flights.AcquireQueryPermit(ctx,
			execution.SlotIdentity{QueryGroup: "normal-waiting", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Minute))
		done <- acquireErr
	}()
	waitForPermitQueue(t, flights, 1, 0)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireQueryPermit(canceled) error = %v", err)
	}
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalWaiting != 0 || snapshot.Inflight != 2 {
		t.Fatalf("snapshot after cancellation = %+v", snapshot)
	}
	first.Release()
	second.Release()
}

func TestQueryPermitDeadlineRemovesQueuedWaiterBeforeGrant(t *testing.T) {
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits = 1
	limits.RecoveryQueryPermits = 0
	flights, err := NewFlightCoordinatorWithRecovery(limits, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "holder", EvaluationTime: 60}, execution.OperationNormal, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, acquireErr := flights.AcquireQueryPermit(context.Background(),
			execution.SlotIdentity{QueryGroup: "expiring", EvaluationTime: 60}, execution.OperationNormal,
			time.Now().Add(100*time.Millisecond))
		done <- acquireErr
	}()
	waitForPermitQueue(t, flights, 1, 0)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("AcquireQueryPermit(expiring) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued permit did not expire at its business deadline")
	}
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalWaiting != 0 || snapshot.Inflight != 1 {
		t.Fatalf("snapshot after deadline = %+v", snapshot)
	}
	holder.Release()
}

func TestQueryPermitQueueUsesEarliestDeadlineWithinFairQueryGroupRotation(t *testing.T) {
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits = 1
	limits.RecoveryQueryPermits = 0
	limits.ReadyQueueCapacity = 3
	limits.MaxQueuedItemsPerQG = 2
	flights, err := NewFlightCoordinatorWithRecovery(limits, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "holder", EvaluationTime: 60}, execution.OperationNormal, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	type grant struct {
		name   string
		permit *QueryPermit
		err    error
	}
	grants := make(chan grant, 4)
	queue := func(name string, qg execution.QueryGroupIdentity, deadline time.Time) {
		go func() {
			permit, acquireErr := flights.AcquireQueryPermit(context.Background(),
				execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 60}, execution.OperationNormal, deadline)
			grants <- grant{name: name, permit: permit, err: acquireErr}
		}()
	}
	now := time.Now()
	queue("hot-late", "hot", now.Add(900*time.Millisecond))
	queue("hot-early", "hot", now.Add(700*time.Millisecond))
	waitForPermitQueue(t, flights, 2, 0)
	queue("hot-over-cap", "hot", now.Add(800*time.Millisecond))
	if got := <-grants; got.name != "hot-over-cap" || !errors.Is(got.err, ErrQueryPermitQueueFull) {
		t.Fatalf("per-QG queue overflow = %+v", got)
	}
	queue("healthy", "healthy", now.Add(800*time.Millisecond))
	waitForPermitQueue(t, flights, 3, 0)

	holder.Release()
	first := <-grants
	if first.err != nil || first.name != "hot-early" {
		t.Fatalf("first grant = %+v, want earliest hot query", first)
	}
	first.permit.Release()
	second := <-grants
	if second.err != nil || second.name != "healthy" {
		t.Fatalf("second grant = %+v, want fair healthy QG", second)
	}
	second.permit.Release()
	third := <-grants
	if third.err != nil || third.name != "hot-late" {
		t.Fatalf("third grant = %+v, want remaining hot query", third)
	}
	third.permit.Release()
}

func TestQueryPermitRejectsRecoveryWhenRecoveryPermitsAreDisabled(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	limits := testRecoveryLimits()
	limits.RecoveryQueryPermits = 0
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "replay", EvaluationTime: 60}, execution.OperationReplay, clock.Now().Add(time.Minute))
	if !errors.Is(err, ErrRecoveryPermitsOff) {
		t.Fatalf("AcquireQueryPermit(replay) error = %v, want ErrRecoveryPermitsOff", err)
	}
}

func testRecoveryLimits() RecoveryLimits {
	return RecoveryLimits{
		ProcessQueryPermits: 2, RecoveryQueryPermits: 1,
		ReadyQueueCapacity: 8, RecoveryQueueCapacity: 8,
		MaxQueuedItemsPerQG: 2,
		MaxReplaySlots:      3, MaxReplayAge: 10 * time.Minute,
		RetryMinDelay: time.Second, RetryMaxDelay: 8 * time.Second,
	}
}

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock(now time.Time) *mutableClock { return &mutableClock{now: now} }

func (clock *mutableClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *mutableClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

type scriptedExecutor struct {
	mu       sync.Mutex
	results  []execution.SlotExecutionResult
	requests []execution.SlotExecutionRequest
}

func (executor *scriptedExecutor) Execute(
	_ context.Context,
	request execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.requests = append(executor.requests, request)
	if len(executor.results) == 0 {
		return execution.SlotExecutionResult{}, errors.New("scripted executor exhausted")
	}
	result := executor.results[0]
	executor.results = executor.results[1:]
	return result, nil
}

func (executor *scriptedExecutor) Requests() []execution.SlotExecutionRequest {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]execution.SlotExecutionRequest(nil), executor.requests...)
}

func (executor *scriptedExecutor) Operations() []execution.Operation {
	requests := executor.Requests()
	operations := make([]execution.Operation, len(requests))
	for index := range requests {
		operations[index] = requests[index].Operation
	}
	return operations
}

func equalOperations(left, right []execution.Operation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func waitForPermitQueue(t *testing.T, flights *FlightCoordinator, normal, recovery int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := flights.queryPermitSnapshot()
		if snapshot.NormalWaiting == normal && snapshot.RecoveryWaiting == recovery {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("permit queue did not settle: %+v", flights.queryPermitSnapshot())
}

func receivePermit(t *testing.T, permits <-chan *QueryPermit) *QueryPermit {
	t.Helper()
	select {
	case permit := <-permits:
		return permit
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for query permit")
		return nil
	}
}
