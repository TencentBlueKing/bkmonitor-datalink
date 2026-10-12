// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Design 02 §6.2 steps 2-3: an owner that is no longer desired stops
// creating Slots, finishes the commit boundary it is in, and only then
// releases its lease. The Query Group is moved away while its Slot runs:
// the reconcile that learns of the move returns without waiting for the
// Slot, no release happens while it runs, and exactly one follows it.
func TestAReassignedQueryGroupIsReleasedOnlyAfterItsRunningSlotReturns(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	first := newFakePhaseTwoQueryGroup()
	first.runStarted = make(chan struct{})
	first.runRelease = make(chan struct{})
	first.attempted = true
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{"query-group-1"},
		runners:  map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{"query-group-1": first},
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	// The Slot is let go on every way out, a failed assertion included, or
	// the deferred Shutdown would wait on it for good.
	var finishSlot sync.Once
	letSlotFinish := func() { finishSlot.Do(func() { close(first.runRelease) }) }
	defer letSlotFinish()
	waitSignal(t, first.leaseStarted, "lease maintenance")

	done := make(chan error, 1)
	go func() { done <- runScheduledOnceSettled(context.Background(), bundle) }()
	waitSignal(t, first.runStarted, "the Slot in flight")

	control.queryGroups = nil
	owner.setAssigned(nil)
	reconciled := make(chan error, 1)
	go func() { reconciled <- bundle.refreshAndReconcile(context.Background(), true) }()
	select {
	case err := <-reconciled:
		if err != nil {
			t.Fatalf("refreshAndReconcile() error = %v", err)
		}
	case <-time.After(signalWaitBound):
		t.Fatal("the reconcile that moved the Query Group away waited for its Slot; the control loop must not")
	}
	// The move is applied: no further Slot will start for it.
	if heldRunner(bundle, "query-group-1") != nil {
		t.Fatal("the moved Query Group is still a current runner")
	}
	time.Sleep(100 * time.Millisecond)
	if released := first.releaseCount(); released != 0 {
		t.Fatalf("lease released %d time(s) while the Slot was still running; want the release after "+
			"the Slot's commit boundary (02 §6.2 steps 2-3)", released)
	}
	letSlotFinish()
	<-done
	deadline := time.Now().Add(signalWaitBound)
	for first.releaseCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if released := first.releaseCount(); released != 1 {
		t.Fatalf("release calls after the Slot returned = %d, want exactly 1", released)
	}
}

// A Query Group moved away with nothing in flight is released at once, as
// it always was.
func TestAReassignedQueryGroupWithNoSlotInFlightIsReleasedAtOnce(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	first := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{"query-group-1"},
		runners:  map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{"query-group-1": first},
	}
	var drainMu sync.Mutex
	var drains []observability.SlotDrainFacts
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner, Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.Stage(observability.StageHandoverDrained) && observation.SlotDrain != nil {
				drainMu.Lock()
				drains = append(drains, *observation.SlotDrain)
				drainMu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	waitSignal(t, first.leaseStarted, "lease maintenance")
	control.queryGroups = nil
	owner.setAssigned(nil)
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile() error = %v", err)
	}
	deadline := time.Now().Add(signalWaitBound)
	for first.releaseCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if released := first.releaseCount(); released != 1 {
		t.Fatalf("release calls = %d, want 1", released)
	}
	drainMu.Lock()
	observed := append([]observability.SlotDrainFacts(nil), drains...)
	drainMu.Unlock()
	if len(observed) != 1 || observed[0].Outcome != observability.SlotDrainIdle || observed[0].Waited != 0 {
		t.Fatalf("handover drains observed = %+v, want one idle with nothing waited", observed)
	}
}

// Design 02 §6.6: on a stop signal the Worker goes DRAINING first, stops
// creating Slots, and finishes within the drain deadline the execution that
// has entered its Event/State/Progress boundary. The Slot below needs 300 ms
// to finish, far inside the shutdown timeout: stopping the Worker lets it
// finish, and the Worker was already registered DRAINING when it did.
func TestAStopLetsTheSlotInItsCommitBoundaryFinish(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroup := execution.QueryGroupIdentity("query-group-drain")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}}
	runStarted := make(chan struct{})
	outcome := make(chan string, 1)
	var drainingBeforeFinish atomic.Bool
	var once sync.Once
	owner.runner = &callbackPhaseTwoQueryGroup{run: func(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
		first := false
		once.Do(func() { first = true; close(runStarted) })
		if !first {
			return execution.SlotExecutionResult{}, false, nil
		}
		select {
		case <-ctx.Done():
			outcome <- "cancelled"
			return execution.SlotExecutionResult{}, true, ctx.Err()
		case <-time.After(300 * time.Millisecond):
			states := owner.registrationStates()
			drainingBeforeFinish.Store(len(states) > 0 && states[len(states)-1] == ownership.WorkerDraining)
			outcome <- "finished"
			return execution.SlotExecutionResult{Completed: true}, true, nil
		}
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runStarted, "the Slot in its commit boundary")
	cancel()
	got := <-outcome
	<-done
	if got != "finished" {
		t.Fatalf("Slot in its commit boundary was %s by the stop (shutdown timeout %s); want it finished (02 §6.6)",
			got, cfg.ShutdownTimeout.Duration())
	}
	if !drainingBeforeFinish.Load() {
		t.Fatal("the Worker was not registered DRAINING while its last Slot finished; DRAINING comes first (02 §6.6)")
	}
	if drain := bundle.shutdownDrain(); drain == nil || drain.Outcome != "finished" || drain.Waited != 1 || drain.Cancelled != 0 {
		t.Fatalf("shutdown drain = %+v, want finished with 1 waited and none cancelled", drain)
	}
}

// The drain deadline is a deadline: a Slot still running when it passes is
// cancelled, then, and not before, and the outcome says so.
func TestAStopCancelsTheSlotStillRunningAtTheDrainDeadline(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.ShutdownTimeout = config.Duration(300 * time.Millisecond)
	queryGroup := execution.QueryGroupIdentity("query-group-hung")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}}
	runStarted := make(chan struct{})
	cancelledAfter := make(chan time.Duration, 1)
	var stoppedAt atomic.Int64
	var once sync.Once
	owner.runner = &callbackPhaseTwoQueryGroup{run: func(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
		first := false
		once.Do(func() { first = true; close(runStarted) })
		if !first {
			return execution.SlotExecutionResult{}, false, nil
		}
		<-ctx.Done()
		cancelledAfter <- time.Since(time.UnixMilli(stoppedAt.Load()))
		return execution.SlotExecutionResult{}, true, ctx.Err()
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runStarted, "the hung Slot")
	stoppedAt.Store(time.Now().UnixMilli())
	cancel()
	after := <-cancelledAfter
	<-done
	if after < 200*time.Millisecond {
		t.Fatalf("the running Slot was cancelled %s after the stop; want it left to run until the %s drain deadline",
			after, cfg.ShutdownTimeout.Duration())
	}
	if drain := bundle.shutdownDrain(); drain == nil || drain.Outcome != "deadline" || drain.Waited != 1 || drain.Cancelled != 1 {
		t.Fatalf("shutdown drain = %+v, want deadline with 1 waited and 1 cancelled", drain)
	}
}

func heldRunner(bundle *phaseTwoWorkerBundle, queryGroup execution.QueryGroupIdentity) *phaseTwoQueryGroupLifecycle {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.runners[queryGroup]
}

// A Slot that ignores its cancellation - blocked in a call that does not
// watch its context - does not hold the stop past its bound: the drain gives
// up on it a moment after the deadline, names it, and the leases are still
// released (the hung Slot's later writes are refused by the fence).
func TestAStopDoesNotWaitForeverOnASlotThatIgnoresCancellation(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.ShutdownTimeout = config.Duration(200 * time.Millisecond)
	queryGroup := execution.QueryGroupIdentity("query-group-deaf")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	runner := newFakePhaseTwoQueryGroup()
	runner.runStarted = make(chan struct{})
	runner.runRelease = make(chan struct{}) // closed only at the end: the Slot ignores its context
	defer close(runner.runRelease)
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runner.runStarted, "the Slot that ignores cancellation")
	stopped := time.Now()
	cancel()
	bound := 2*cfg.ShutdownTimeout.Duration() + 2*time.Second
	select {
	case <-done:
	case <-time.After(bound + time.Second):
		t.Fatalf("the stop was still waiting on a Slot that ignores cancellation after %s, want it done within %s",
			time.Since(stopped), bound)
	}
	drain := bundle.shutdownDrain()
	if drain == nil || drain.Outcome != observability.SlotDrainDeadlineUnreturned || drain.Waited != 1 ||
		drain.Cancelled != 1 || drain.Unreturned != 1 {
		t.Fatalf("shutdown drain = %+v, want deadline_unreturned with 1 waited, 1 cancelled, 1 unreturned", drain)
	}
	if released := runner.releaseCount(); released != 1 {
		t.Fatalf("release calls = %d, want the lease released despite the hung Slot", released)
	}
}
