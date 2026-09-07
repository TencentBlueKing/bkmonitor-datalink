// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// This file holds the error-scope regressions for the phase-two control loop:
// a transient control or Ownership Store failure must degrade readiness and be
// retried, an invariant violation must still stop Run, and an execution error
// in one Query Group must back off without slowing its healthy sibling.

func waitForHealth(
	t *testing.T,
	health *phaseTwoApplicationHealth,
	want string,
	predicate func(observability.HealthSnapshot) bool,
) observability.HealthSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last observability.HealthSnapshot
	for time.Now().Before(deadline) {
		last = health.HealthSnapshot()
		if predicate(last) {
			return last
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for health %s; last=%+v", want, last)
	return last
}

func hasReason(reasons []observability.ReasonCode, want observability.ReasonCode) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestPhaseTwoWorkerBundleRunSurvivesControlDependencyFailuresAndRecovers(t *testing.T) {
	tests := []struct {
		name      string
		site      string
		wantState observability.HealthState
		wantReady bool
		// failed recognizes the structured observation of one failing call.
		failed func(observability.Observation) bool
	}{
		{
			name: "publish assignments", site: "publish",
			wantState: observability.HealthDegraded, wantReady: true,
			failed: func(observation observability.Observation) bool {
				return observation.Component == observability.ComponentOwnership &&
					observation.Stage == observability.StageAssignmentAcquired &&
					observation.Result == observability.ResultFailed &&
					observation.ReasonCode == phaseTwoControlDependencyReason && observation.Err != nil
			},
		},
		{
			name: "read assignments", site: "assigned",
			wantState: observability.HealthDegraded, wantReady: true,
			failed: func(observation observability.Observation) bool {
				return observation.Component == observability.ComponentOwnership &&
					observation.Stage == observability.StageAssignmentAcquired &&
					observation.Result == observability.ResultFailed &&
					observation.ReasonCode == phaseTwoControlDependencyReason && observation.Err != nil
			},
		},
		{
			// An Assignment that cannot be opened keeps the Worker NotReady, as a
			// busy lease already does; the dependency reason explains why.
			name: "open query group", site: "open",
			wantState: observability.HealthNotReady, wantReady: false,
			failed: func(observation observability.Observation) bool {
				return observation.Component == observability.ComponentOwnership &&
					observation.Stage == observability.StageTakeoverCompleted &&
					observation.Result == observability.ResultFailed && observation.Err != nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
			cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
			cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(2 * time.Millisecond)
			owned := execution.QueryGroupIdentity("query-group-owned")
			joining := execution.QueryGroupIdentity("query-group-joining")
			ownedRunner := newFakePhaseTwoQueryGroup()
			joiningRunner := newFakePhaseTwoQueryGroup()
			control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{owned, joining}}
			owner := &fakePhaseTwoOwnership{
				assigned: []execution.QueryGroupIdentity{owned},
				runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
					owned: ownedRunner, joining: joiningRunner,
				},
			}
			health := newPhaseTwoApplicationHealth()
			var mu sync.Mutex
			var observations []observability.Observation
			bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
				Config: cfg, Health: health, Control: control, Ownership: owner, Now: time.Now,
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					mu.Lock()
					defer mu.Unlock()
					observations = append(observations, observation)
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- bundle.Run(ctx) }()
			waitSignal(t, ownedRunner.leaseStarted, "owned query-group lease")
			waitForHealth(t, health, "ready", func(snapshot observability.HealthSnapshot) bool {
				return snapshot.State == observability.HealthReady
			})

			dependencyErr := errors.New("dial tcp: i/o timeout")
			owner.injectFailure(test.site, dependencyErr, -1)
			if test.site == "open" {
				owner.setAssigned([]execution.QueryGroupIdentity{owned, joining})
			}
			waitForHealth(t, health, "degraded by dependency", func(snapshot observability.HealthSnapshot) bool {
				return snapshot.State == test.wantState && snapshot.Ready == test.wantReady &&
					hasReason(snapshot.Reasons, phaseTwoControlDependencyReason)
			})
			select {
			case err := <-done:
				t.Fatalf("Run stopped on a control dependency failure: %v", err)
			default:
			}
			// The already-owned Query Group keeps executing while degraded.
			waitForRunnerCalls(t, ownedRunner, ownedRunner.runCount()+3)
			select {
			case err := <-done:
				t.Fatalf("Run stopped while the dependency kept failing: %v", err)
			default:
			}

			owner.injectFailure(test.site, nil, 0)
			recovered := waitForHealth(t, health, "recovered", func(snapshot observability.HealthSnapshot) bool {
				return snapshot.State == observability.HealthReady && snapshot.Ready &&
					!hasReason(snapshot.Reasons, phaseTwoControlDependencyReason)
			})
			if recovered.LastRecoveryAt.IsZero() {
				t.Fatalf("recovered health lacks last recovery time: %+v", recovered)
			}
			if test.site == "open" {
				waitSignal(t, joiningRunner.leaseStarted, "joining query-group lease after recovery")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run(cancel) error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Run did not stop after cancellation")
			}

			mu.Lock()
			got := append([]observability.Observation(nil), observations...)
			mu.Unlock()
			failed, recoveredCount := 0, 0
			for _, observation := range got {
				if observation.Stage == observability.Stage(observability.StageFatal) {
					t.Fatalf("control dependency failure became fatal: %+v", observation)
				}
				if test.failed(observation) {
					failed++
				}
				if observation.Component == observability.ComponentControlPlane &&
					observation.Result == observability.Result(observability.ResultRecovered) &&
					observation.ReasonCode == phaseTwoControlDependencyReason {
					recoveredCount++
				}
			}
			if failed == 0 || recoveredCount != 1 {
				t.Fatalf("dependency episode observations failed/recovered = %d/%d, want >0/1", failed, recoveredCount)
			}
		})
	}
}

func TestPhaseTwoWorkerBundleRunStopsOnControlInvariantError(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
	cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(2 * time.Millisecond)
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- bundle.Run(ctx) }()
	waitSignal(t, runner.leaseStarted, "owned query-group lease")

	invariant := newPhaseTwoInvariantError("phase-two production Assignment identity mismatch")
	owner.injectFailure("assigned", invariant, 1)
	select {
	case err := <-done:
		if !isPhaseTwoInvariantError(err) || !errors.Is(err, invariant) {
			t.Fatalf("Run(invariant) error = %v, want the invariant violation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept running after a control invariant violation")
	}
}

func TestPhaseTwoWorkerBundleMissingRunnerIsInvariantViolation(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	err := bundle.Start(context.Background())
	if !isPhaseTwoInvariantError(err) {
		t.Fatalf("Start(nil runner) error = %v, want invariant violation", err)
	}
	_ = bundle.Shutdown(context.Background())
}

// The fixtures below drive the production scheduler.Runner inside the
// dispatcher so the isolation regression exercises the real backoff path.

type scopeTestSession struct{ fence execution.OwnerFence }

func (session scopeTestSession) ValidateCurrent(context.Context, time.Time) (execution.OwnerFence, error) {
	return session.fence, nil
}

type scopeTestSlotSource struct{ slot scheduler.FrozenSlot }

func (source scopeTestSlotSource) Next(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, error) {
	return source.slot, true, nil
}

type countingExecutor struct {
	err   error
	calls atomic.Int64
}

func (executor *countingExecutor) Execute(
	context.Context,
	execution.SlotExecutionRequest,
) (execution.SlotExecutionResult, error) {
	executor.calls.Add(1)
	if executor.err != nil {
		return execution.SlotExecutionResult{}, executor.err
	}
	return execution.SlotExecutionResult{Completed: true}, nil
}

type schedulerRunnerQueryGroup struct{ runner *scheduler.Runner }

func (group *schedulerRunnerQueryGroup) RunOne(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
	return group.runner.RunOne(ctx)
}

func (group *schedulerRunnerQueryGroup) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	return group.runner.RunOneAdmitted(ctx, admission)
}

func (group *schedulerRunnerQueryGroup) NextReadyAt() time.Time { return group.runner.NextReadyAt() }

func (*schedulerRunnerQueryGroup) MaintainLease(ctx context.Context, _, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*schedulerRunnerQueryGroup) Release(context.Context) error { return nil }

func scopeTestFrozenSlot(queryGroup execution.QueryGroupIdentity) scheduler.FrozenSlot {
	contractRef := execution.FrozenExecutionContractRef{
		Slot:                 execution.SlotIdentity{QueryGroup: queryGroup, EvaluationTime: 100},
		SnapshotRevision:     "snapshot-1",
		QueryRevision:        "query-1",
		ScheduleRevision:     "schedule-1",
		ScheduleSegmentStart: 60,
		DuePlanSetDigest:     "plans-1",
	}
	return scheduler.FrozenSlot{
		Contract: contractRef,
		Dispatch: scheduler.SlotDispatchContext{
			Operation:            execution.OperationNormal,
			OwnerFence:           execution.OwnerFence{QueryGroup: queryGroup, OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
			AssignmentGeneration: 1,
		},
		DuePlanTargets: execution.FrozenDuePlanTargets{
			DuePlanSetDigest: contractRef.DuePlanSetDigest,
			Plans:            []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}},
		},
		EarliestQueryDeadlineUnixMilli: 101_000, RecoveryUntilUnixMilli: 701_000, KeepUntilUnixMilli: 777_000,
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
	}
}

func newScopeTestSchedulerRunner(
	t *testing.T,
	queryGroup execution.QueryGroupIdentity,
	flights *scheduler.FlightCoordinator,
	now func() time.Time,
	executor scheduler.Executor,
) *scheduler.Runner {
	t.Helper()
	slot := scopeTestFrozenSlot(queryGroup)
	runner, err := scheduler.NewRunner(
		queryGroup, scopeTestSession{fence: slot.Dispatch.OwnerFence}, scopeTestSlotSource{slot: slot}, executor, flights, now,
	)
	if err != nil {
		t.Fatalf("NewRunner(%s) error = %v", queryGroup, err)
	}
	return runner
}

func waitForExecutorCalls(t *testing.T, name string, executor *countingExecutor, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if executor.calls.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s executor calls >= %d; got %d", name, want, executor.calls.Load())
}

func TestPhaseTwoWorkerBundleExecutionErrorBackoffKeepsHealthySiblingOnTickRate(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	limits := cfg.PhaseTwo.Scheduler.RecoveryLimits()
	var clockMu sync.Mutex
	current := time.Unix(1_700_000_000, 0)
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return current
	}
	advance := func(delta time.Duration) {
		clockMu.Lock()
		current = current.Add(delta)
		clockMu.Unlock()
	}
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, now)
	if err != nil {
		t.Fatal(err)
	}
	failing := &countingExecutor{err: errors.New("commit progress: redis timeout")}
	healthy := &countingExecutor{}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-failing": {runner: &schedulerRunnerQueryGroup{
				runner: newScopeTestSchedulerRunner(t, "query-group-failing", flights, now, failing),
			}},
			"query-group-healthy": {runner: &schedulerRunnerQueryGroup{
				runner: newScopeTestSchedulerRunner(t, "query-group-healthy", flights, now, healthy),
			}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()

	// Within the first backoff window the failing Query Group executes once
	// while its healthy sibling completes on every tick.
	const ticks = 8
	for tick := int64(1); tick <= ticks; tick++ {
		wake <- struct{}{}
		waitForExecutorCalls(t, "healthy", healthy, tick)
	}
	if got := failing.calls.Load(); got != 1 {
		t.Fatalf("failing Query Group executed %d times over %d ticks inside its backoff, want 1", got, ticks)
	}

	// The expired backoff releases exactly one more attempt, whose failure
	// schedules a longer delay that the following ticks must respect.
	advance(limits.RetryMinDelay)
	wake <- struct{}{}
	waitForExecutorCalls(t, "healthy", healthy, ticks+1)
	waitForExecutorCalls(t, "failing", failing, 2)
	for tick := int64(ticks + 2); tick <= 2*ticks; tick++ {
		wake <- struct{}{}
		waitForExecutorCalls(t, "healthy", healthy, tick)
	}
	if got := failing.calls.Load(); got != 2 {
		t.Fatalf("failing Query Group executed %d times after one backoff expiry, want 2", got)
	}
	if got := healthy.calls.Load(); got != 2*ticks {
		t.Fatalf("healthy Query Group executed %d times over %d ticks, want one per tick", got, 2*ticks)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduler(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop after cancellation")
	}
}
