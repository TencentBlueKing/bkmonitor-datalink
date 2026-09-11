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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
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

func (session scopeTestSession) ValidateCurrentWithAssignment(
	context.Context,
	time.Time,
) (execution.OwnerFence, ownership.AssignmentRecord, error) {
	return session.fence, ownership.AssignmentRecord{}, nil
}

type scopeTestSlotSource struct{ slot scheduler.FrozenSlot }

func (source scopeTestSlotSource) Next(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
	return source.slot, true, scheduler.SlotDueFacts{IntervalSeconds: 60}, nil
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

func (group *schedulerRunnerQueryGroup) DueBound() scheduler.RunnerDueBound {
	return group.runner.DueBound()
}

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

// scopeTestWakeCadence is how often each arm's wake is refreshed. A saturating
// pump would hide the very thing the rate comparison is for: with wakes always
// pending, a generation the healthy Query Group loses costs no measurable time,
// so a dispatcher that spent seven generations in eight on a parked sibling
// would still read as full speed. A cadence makes a generation cost something.
const scopeTestWakeCadence = 200 * time.Microsecond

// scopeTestComparisonWindow is how long both arms are watched side by side.
// Both dispatchers run through it at once in this process, so whatever the
// machine is doing during the window it does to both.
const scopeTestComparisonWindow = 200 * time.Millisecond

// scopeTestBackoffArm is one dispatcher under test, with the Query Groups it
// owns and the wake channel that drives it.
type scopeTestBackoffArm struct {
	name    string
	bundle  *phaseTwoWorkerBundle
	wake    chan struct{}
	done    chan error
	cancel  context.CancelFunc
	healthy *countingExecutor
	failing *countingExecutor
	// generations counts the wakes this arm's dispatcher actually consumed.
	generations atomic.Int64
	stopped     bool
}

// newScopeTestBackoffArm opens a dispatcher over a healthy Query Group and,
// when withFailingSibling is set, a second one whose executions always fail.
//
// Both arms are built from the same Config and the same clock so that the only
// difference between them is the sibling. That is what makes a comparison of
// the two readable as a statement about the sibling.
func newScopeTestBackoffArm(
	t *testing.T,
	name string,
	cfg config.Config,
	now func() time.Time,
	withFailingSibling bool,
) *scopeTestBackoffArm {
	t.Helper()
	// One FlightCoordinator per arm. The two arms own Query Groups of the same
	// name, and a shared coordinator would let one arm's single-flight state
	// decide the other's - which is the one thing a comparison between them
	// must not depend on.
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(cfg.PhaseTwo.Scheduler.RecoveryLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	arm := &scopeTestBackoffArm{name: name, healthy: &countingExecutor{}}
	runners := map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
		"query-group-healthy": {runner: &schedulerRunnerQueryGroup{
			runner: newScopeTestSchedulerRunner(t, "query-group-healthy", flights, now, arm.healthy),
		}},
	}
	if withFailingSibling {
		arm.failing = &countingExecutor{err: errors.New("commit progress: redis timeout")}
		runners["query-group-failing"] = &phaseTwoQueryGroupLifecycle{runner: &schedulerRunnerQueryGroup{
			runner: newScopeTestSchedulerRunner(t, "query-group-failing", flights, now, arm.failing),
		}}
	}
	arm.bundle = &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: now},
		runners:      runners,
	}
	ctx, cancel := context.WithCancel(context.Background())
	arm.cancel = cancel
	arm.wake = make(chan struct{}, 1)
	arm.done = make(chan error, 1)
	go func() { arm.done <- arm.bundle.runScheduler(ctx, arm.wake, false) }()
	// The wake is refreshed on a cadence, which is what the deployment's
	// scheduler ticker does: one pending wake, replaced on an interval. The
	// cadence is short because nothing here is waiting on real work, and it is
	// the same in every arm, so it cancels out of any comparison between them.
	// It is not there to give anything time to settle.
	//
	// The version of this test that did count them asserted one healthy
	// execution per wake, and that is not a property the dispatcher has. The
	// executor's counter is incremented inside the Runner, while the round it
	// belongs to only ends once the dispatcher has taken the result back off
	// its results channel and dropped the Query Group from its active set. A
	// wake that lands in between opens a generation whose walk finds that Query
	// Group still active, spends its turn on it without queueing it and without
	// recording a skip, and the generation is gone. The next wake only came
	// after the count moved, so the test then waited for a count that nothing
	// would ever move again - a permanent stall that the deadline reported as
	// slowness. Driving the wake the way the deployment does removes the
	// assumption instead of widening the deadline that hid it.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case arm.wake <- struct{}{}:
				// A blocking send returns exactly when the dispatcher takes the
				// wake, so this counts generations rather than attempts.
				arm.generations.Add(1)
			}
			time.Sleep(scopeTestWakeCadence)
		}
	}()
	t.Cleanup(arm.stop)
	return arm
}

func (arm *scopeTestBackoffArm) stop() {
	if arm.stopped {
		return
	}
	arm.stopped = true
	arm.cancel()
	<-arm.done
}

// awaitRounds waits until the named executor has completed want executions.
//
// The budget is a deadline on a machine, not on the behaviour: every assertion
// that depends on this call is made against a clock the test owns, so a Query
// Group that is being held back is held back for good and no budget lets it
// through. Reaching the deadline therefore means the rounds are not coming.
func (arm *scopeTestBackoffArm) awaitRounds(
	t *testing.T,
	which string,
	executor *countingExecutor,
	want int64,
) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if executor.calls.Load() >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("%s arm: %s Query Group reached %d executions in 30s, want %d",
		arm.name, which, executor.calls.Load(), want)
	return false
}

func scopeTestBackoffClock() (func() time.Time, func(time.Duration)) {
	var mu sync.Mutex
	current := time.Unix(1_700_000_000, 0)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return current
	}
	advance := func(delta time.Duration) {
		mu.Lock()
		current = current.Add(delta)
		mu.Unlock()
	}
	return now, advance
}

// TestPhaseTwoWorkerBundleExecutionErrorBacksOffForAsLongAsItsDelayHolds pins
// the backoff itself: a Query Group whose execution failed does not run again
// until its delay has expired, however often the dispatcher is woken in the
// meantime.
//
// The clock is the test's, and it does not move while the rounds are counted.
// That is what makes the assertion absolute rather than a race: no number of
// wakes can reach the failing Query Group's next instant, so a second execution
// inside the window is a backoff that was not applied, never a slow machine.
func TestPhaseTwoWorkerBundleExecutionErrorBacksOffForAsLongAsItsDelayHolds(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	limits := cfg.PhaseTwo.Scheduler.RecoveryLimits()
	now, advance := scopeTestBackoffClock()
	const rounds = 8
	arm := newScopeTestBackoffArm(t, "failing sibling", cfg, now, true)

	if !arm.awaitRounds(t, "healthy", arm.healthy, rounds) {
		t.FailNow()
	}
	if got := arm.failing.calls.Load(); got != 1 {
		t.Fatalf("failing Query Group executed %d times while its backoff held, want 1", got)
	}

	// The expired backoff releases exactly one more attempt, and that attempt's
	// failure schedules a longer delay that the rounds after it must respect.
	advance(limits.RetryMinDelay)
	if !arm.awaitRounds(t, "failing", arm.failing, 2) {
		t.FailNow()
	}
	if !arm.awaitRounds(t, "healthy", arm.healthy, 2*rounds) {
		t.FailNow()
	}
	if got := arm.failing.calls.Load(); got != 2 {
		t.Fatalf("failing Query Group executed %d times after one backoff expiry, want 2", got)
	}

	arm.cancel()
	arm.stopped = true
	select {
	case err := <-arm.done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduler(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("dispatcher did not stop after cancellation")
	}
}

// TestPhaseTwoWorkerBundleBackoffSiblingDoesNotCostHealthyQueryGroupItsRounds
// pins the other half: the Query Group that is backing off must not cost its
// healthy sibling anything.
//
// It is asserted as a comparison rather than as an absolute count. Counting the
// healthy Query Group's rounds on its own cannot tell "the sibling costs it
// nothing" from "the driver only produced that many rounds", so the same driver
// is run twice - once over a bundle that owns the healthy Query Group alone,
// once over one that also owns a permanently failing sibling - and the two
// counts are compared. The clock never moves, so the failing sibling can never
// become due again: every round the second arm is short of the first is a round
// the sibling took, and no amount of waiting returns it.
func TestPhaseTwoWorkerBundleBackoffSiblingDoesNotCostHealthyQueryGroupItsRounds(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	now, _ := scopeTestBackoffClock()
	const rounds = 16
	alone := newScopeTestBackoffArm(t, "healthy alone", cfg, now, false)
	beside := newScopeTestBackoffArm(t, "beside a failing sibling", cfg, now, true)

	aloneReached := alone.awaitRounds(t, "healthy", alone.healthy, rounds)
	besideReached := beside.awaitRounds(t, "healthy", beside.healthy, rounds)
	if !aloneReached || !besideReached {
		t.Fatalf("healthy rounds: alone=%d beside a failing sibling=%d, want %d in both arms",
			alone.healthy.calls.Load(), beside.healthy.calls.Load(), rounds)
	}
	if got := beside.failing.calls.Load(); got != 1 {
		t.Fatalf("failing sibling executed %d times while its backoff held, want 1; "+
			"the comparison only means anything while the sibling stays parked", got)
	}
	// A parked Query Group holds its place in the recovery queue for as long as
	// its delay lasts. Nothing may be turned away for want of a queue place
	// because of it: a deferral here is the walk being unable to seat a Query
	// Group it reached, which is exactly how a parked sibling would start
	// costing its healthy neighbour its turns.
	if facts := beside.bundle.rotationFacts(); facts != nil && facts.Deferred != 0 {
		t.Fatalf("rotation deferred %d Query Groups beside a parked sibling, want 0; rotation=%+v",
			facts.Deferred, facts)
	}

	// Reaching the same round count says the sibling cannot stop the healthy
	// Query Group. It does not yet say the sibling costs it nothing, because a
	// Query Group that is merely slowed still arrives. So the two arms are also
	// compared on rate, over one window, with both dispatchers running at the
	// same time in this process: whatever the machine is doing during that
	// window it is doing to both arms, so the comparison is between the two
	// bundles rather than between two moments. Only the ratio is asserted, and
	// loosely - the arm with the sibling walks one more Query Group per
	// generation, so it is expected to be somewhat slower, and the bound is
	// there to catch a sibling that costs a multiple of that, not to pin a
	// throughput number.
	aloneBefore, besideBefore := alone.healthy.calls.Load(), beside.healthy.calls.Load()
	aloneGenerationsBefore, besideGenerationsBefore := alone.generations.Load(), beside.generations.Load()
	time.Sleep(scopeTestComparisonWindow)
	aloneRounds := alone.healthy.calls.Load() - aloneBefore
	besideRounds := beside.healthy.calls.Load() - besideBefore
	aloneGenerations := alone.generations.Load() - aloneGenerationsBefore
	besideGenerations := beside.generations.Load() - besideGenerationsBefore
	if aloneRounds == 0 || aloneGenerations == 0 || besideGenerations == 0 {
		t.Fatalf("comparison window produced nothing to compare: alone=%d/%d beside=%d/%d rounds/generations",
			aloneRounds, aloneGenerations, besideRounds, besideGenerations)
	}
	// Rounds per generation, not rounds per second: the wake cadence is the
	// same in both arms, but comparing the ratios keeps the statement about the
	// dispatcher even if one arm is woken fewer times than the other.
	// A quarter of the turns is the bound. Measured rather than guessed: with
	// the sibling parked the two arms come out within a percent of each other,
	// run after run, and injecting a dispatcher that skips the healthy Query
	// Group on one generation in two reads as exactly half. The bound sits far
	// enough above the noise to be quiet and far enough below half to catch the
	// mildest version of the regression it is here for.
	if 4*besideRounds*aloneGenerations < 3*aloneRounds*besideGenerations {
		t.Fatalf("over one shared window the healthy Query Group completed %d rounds in %d generations beside "+
			"a failing sibling, against %d in %d alone; a sibling parked on backoff may not cost it its turns",
			besideRounds, besideGenerations, aloneRounds, aloneGenerations)
	}
	t.Logf("shared window: alone=%d rounds/%d generations, beside a failing sibling=%d/%d",
		aloneRounds, aloneGenerations, besideRounds, besideGenerations)
}
