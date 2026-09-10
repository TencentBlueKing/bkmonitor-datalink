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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func TestPhaseTwoApplicationHealthUsesWorkerReadinessWithoutKafkaInputState(t *testing.T) {
	health := newPhaseTwoApplicationHealth()
	health.Update(phaseTwoReadiness{
		State: observability.HealthReady, SnapshotReady: true, AssignmentReady: true,
		RuntimeStateReady: true, OutputSinkReady: true,
	})

	snapshot := health.HealthSnapshot()
	if !snapshot.PhaseTwo || !snapshot.Ready {
		t.Fatalf("phase-two health = %+v, want ready phase-two worker", snapshot)
	}
	if snapshot.AssignedClaims != 0 || snapshot.ConsumerLagKnown || snapshot.ConsumerLagRecords != 0 {
		t.Fatalf("phase-two health leaked Kafka input claim/lag state: %+v", snapshot)
	}
}

func TestPhaseTwoApplicationHealthRequiresSnapshotAndWorkerPrerequisites(t *testing.T) {
	health := newPhaseTwoApplicationHealth()
	health.Update(phaseTwoReadiness{
		State: observability.HealthReady, SnapshotReady: true, AssignmentReady: true,
		RuntimeStateReady: true,
	})

	snapshot := health.HealthSnapshot()
	if snapshot.Ready || snapshot.State != observability.HealthNotReady {
		t.Fatalf("phase-two health = %+v, want not ready without output sink", snapshot)
	}
}

func TestNewPhaseTwoApplicationAcceptsOnlyGoAccess(t *testing.T) {
	goAccess := validGoAccessRuntimeConfig()
	application, err := newPhaseTwoApplication(goAccess)
	if err != nil {
		t.Fatalf("newPhaseTwoApplication() error = %v", err)
	}
	if snapshot := application.HealthSnapshot(); !snapshot.PhaseTwo || snapshot.State != observability.HealthStarting {
		t.Fatalf("new phase-two application health = %+v", snapshot)
	}

	compatibility := goAccess
	compatibility.Input = config.PhaseTwoInputConfig{
		Mode: config.InputModePhaseOneKafkaCompatibility,
		PhaseOneKafka: &config.PhaseOneKafkaCompatibilityConfig{
			InputTopic: "alarmd-input", ConsumerGroup: "alarmd", InitialOffset: "oldest", StatePrefix: "alarmd",
		},
	}
	if _, err := newPhaseTwoApplication(compatibility); err == nil {
		t.Fatal("newPhaseTwoApplication() accepted phase-one compatibility mode")
	}
}

func TestPhaseTwoCapabilitiesDigestIsStableAndExcludesWorkerIdentity(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	first, err := phaseTwoCapabilitiesDigest(cfg)
	if err != nil {
		t.Fatalf("phaseTwoCapabilitiesDigest() error = %v", err)
	}
	second := cfg
	second.PhaseTwo.Worker.ID = "another-worker"
	secondDigest, err := phaseTwoCapabilitiesDigest(second)
	if err != nil || secondDigest != first {
		t.Fatalf("worker identity changed capabilities digest: %q/%q error=%v", first, secondDigest, err)
	}
	changed := cfg
	changed.PhaseTwo.Coordinator.MaxEvents++
	changedDigest, err := phaseTwoCapabilitiesDigest(changed)
	if err != nil {
		t.Fatalf("phaseTwoCapabilitiesDigest(changed) error = %v", err)
	}
	if changedDigest == first {
		t.Fatal("Coordinator budget did not change capabilities digest")
	}
}

func TestRunPhaseTwoApplicationUsesWorkerBundleInsteadOfFixedFailure(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	ctx, cancel := context.WithCancel(context.Background())
	dependencies := phaseTwoApplicationDependencies{
		configureCPU: func() (string, error) { return "cpu_quota", nil },
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource, string) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), dependencies)
	}()
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runPhaseTwoApplicationWithDependencies() error = %v", err)
	}
	if control.initialRefreshCalls != 1 {
		t.Fatalf("initial refresh calls = %d, want 1", control.initialRefreshCalls)
	}
	if bundle.runtimeConfig == nil || bundle.runtimeConfig.CPUSource != "cpu_quota" || bundle.runtimeConfig.Digest == "" {
		t.Fatal("application did not pass resolved startup evidence to the bundle")
	}
}

func TestDefaultPhaseTwoApplicationOpensProductionWorkerBundle(t *testing.T) {
	dependencies := defaultPhaseTwoApplicationDependencies()
	if dependencies.openBundle == nil {
		t.Fatal("default phase-two entry has no production Worker Bundle factory")
	}
}

func TestRunPhaseTwoApplicationBoundsHTTPShutdownWhenBundleOpenFails(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.ShutdownTimeout = config.Duration(20 * time.Millisecond)
	want := errors.New("open production bundle")
	httpRelease := make(chan struct{})
	dependencies := phaseTwoApplicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			return nil, want
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource, string) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error {
				<-httpRelease
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(
			context.Background(), cfg, metric.NewRecorder(metric.BuildInfo{}),
			observability.Discard(observability.ComponentRuntime), dependencies,
		)
	}()
	select {
	case err := <-done:
		close(httpRelease)
		if !errors.Is(err, want) || !errors.Is(err, ErrApplicationShutdownTimeout) {
			t.Fatalf("runPhaseTwoApplicationWithDependencies() error = %v, want open and shutdown timeout", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(httpRelease)
		<-done
		t.Fatal("bundle open failure waited indefinitely for HTTP shutdown")
	}
}

func TestRunPhaseTwoApplicationCancelsWorkerAndMarksFatalWhenHTTPStopsEarly(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	want := errors.New("HTTP runtime stopped")
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	var mu sync.Mutex
	var stages []observability.Stage
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		mu.Lock()
		defer mu.Unlock()
		stages = append(stages, observation.Stage)
	})
	dependencies := phaseTwoApplicationDependencies{
		openBundle: func(
			_ context.Context,
			_ config.Config,
			_ *metric.Recorder,
			_ *observability.Logger,
			health *phaseTwoApplicationHealth,
		) (*phaseTwoWorkerBundle, error) {
			return newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
				Config: cfg, Health: health, Control: control, Ownership: owner,
				Observer: observer, Now: time.Now,
			})
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource, string) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error {
				waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
				return want
			}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(
			ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
			observability.Discard(observability.ComponentRuntime), dependencies,
		)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("runPhaseTwoApplicationWithDependencies() error = %v, want HTTP failure", err)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("HTTP runtime stopped but the Worker Bundle kept running")
	}
	if runner.releaseCount() != 1 {
		t.Fatalf("worker release calls = %d, want 1", runner.releaseCount())
	}
	mu.Lock()
	defer mu.Unlock()
	if !containsStage(stages, observability.Stage(observability.StageFatal)) {
		t.Fatalf("application stages = %v, want fatal transition", stages)
	}
}

func TestRunPhaseTwoApplicationKeepsRunningAfterQueryGroupFailure(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
	runner := newFakePhaseTwoQueryGroup()
	runner.runErr = errors.New("commit progress: deterministic completion rejection")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	var mu sync.Mutex
	var stages []observability.Stage
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		mu.Lock()
		defer mu.Unlock()
		stages = append(stages, observation.Stage)
	})
	httpCanceled := make(chan struct{})
	var applicationHealth *phaseTwoApplicationHealth
	dependencies := phaseTwoApplicationDependencies{
		openBundle: func(
			_ context.Context,
			_ config.Config,
			_ *metric.Recorder,
			_ *observability.Logger,
			health *phaseTwoApplicationHealth,
		) (*phaseTwoWorkerBundle, error) {
			applicationHealth = health
			return newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
				Config: cfg, Health: health, Control: control, Ownership: owner,
				Observer: observer, Now: time.Now,
			})
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource, string) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				close(httpCanceled)
				return ctx.Err()
			}}, nil
		},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runPhaseTwoApplicationWithDependencies(
			runCtx, cfg, metric.NewRecorder(metric.BuildInfo{}),
			observability.Discard(observability.ComponentRuntime), dependencies,
		)
	}()
	waitForRunnerCalls(t, runner, 2)
	select {
	case err := <-done:
		t.Fatalf("application stopped after local Query Group failure: %v", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runPhaseTwoApplicationWithDependencies() shutdown error = %v", err)
	}
	waitSignal(t, httpCanceled, "HTTP cancellation")
	if snapshot := applicationHealth.HealthSnapshot(); snapshot.State == observability.HealthFatal {
		t.Fatalf("application health after local Query Group failure = %+v, want non-fatal", snapshot)
	}
	mu.Lock()
	defer mu.Unlock()
	if containsStage(stages, observability.Stage(observability.StageFatal)) {
		t.Fatalf("application stages = %v, local Query Group failure became fatal", stages)
	}
}

func TestPhaseTwoWorkerBundleReportsShutdownFailureFromResourceClose(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	want := errors.New("close trigger sink")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup(),
	}
	var mu sync.Mutex
	var shutdownResult observability.Result
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner, Now: time.Now,
		CloseResources: func(context.Context) error { return want },
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.Stage(observability.StageShutdown) {
				mu.Lock()
				shutdownResult = observation.Result
				mu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Shutdown(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Shutdown() error = %v, want resource close failure", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if shutdownResult != observability.ResultFailed {
		t.Fatalf("shutdown observation result = %s, want failed", shutdownResult)
	}
}

func TestPhaseTwoWorkerBundleTransitionsReadyAndDrainingAroundOwnedRunner(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("health after Start = %+v, want READY", snapshot)
	}
	if got := owner.registrationStates(); !equalReadiness(got, []ownership.AssignmentReadiness{ownership.WorkerStarting, ownership.WorkerReady}) {
		t.Fatalf("registration states = %v, want STARTING then READY", got)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if runner.runCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.runCount())
	}

	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthDraining || snapshot.Ready {
		t.Fatalf("health after Shutdown = %+v, want DRAINING", snapshot)
	}
	if got := owner.registrationStates(); !equalReadiness(got, []ownership.AssignmentReadiness{ownership.WorkerStarting, ownership.WorkerReady, ownership.WorkerDraining}) {
		t.Fatalf("registration states = %v, want STARTING, READY, DRAINING", got)
	}
	if runner.releaseCount() != 1 || control.closeCalls != 1 || owner.closeCalls != 1 {
		t.Fatalf("shutdown release/close = %d/%d/%d, want 1/1/1", runner.releaseCount(), control.closeCalls, owner.closeCalls)
	}
	if err := bundle.runScheduledOnce(context.Background()); !errors.Is(err, errPhaseTwoWorkerDraining) {
		t.Fatalf("runScheduledOnce(after drain) error = %v, want draining rejection", err)
	}
	if runner.runCount() != 1 {
		t.Fatalf("shutdown admitted a new Slot, runner calls = %d", runner.runCount())
	}
}

func TestPhaseTwoWorkerBundleRunsOwnedQueryGroupsConcurrentlyOncePerTick(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	first := newFakePhaseTwoQueryGroup()
	first.runStarted, first.runRelease = make(chan struct{}), make(chan struct{})
	second := newFakePhaseTwoQueryGroup()
	second.runStarted, second.runRelease = make(chan struct{}), make(chan struct{})
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		queryGroups[0]: first, queryGroups[1]: second,
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}()

	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()
	waitSignal(t, first.runStarted, "first concurrent Query Group")
	waitSignal(t, second.runStarted, "second concurrent Query Group")
	close(first.runRelease)
	close(second.runRelease)
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if first.runCount() != 1 || second.runCount() != 1 {
		t.Fatalf("single tick runner calls = %d/%d, want 1/1", first.runCount(), second.runCount())
	}
}

func TestPhaseTwoWorkerBundleBoundsRunnerFanoutIndependentlyOfQueryPermits(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 2 // Explicit emergency guard under test.
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = 100
	cfg.PhaseTwo.Scheduler.MaxQueuedItemsPerQG = 1
	queryGroups := []execution.QueryGroupIdentity{
		"query-group-1", "query-group-2", "query-group-3",
		"query-group-4", "query-group-5", "query-group-6",
	}
	started := make(chan execution.QueryGroupIdentity, len(queryGroups))
	releaseRunners := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRunners) }) }
	defer release()
	runners := make(map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime, len(queryGroups))
	for _, queryGroup := range queryGroups {
		runner := newFakePhaseTwoQueryGroup()
		runner.runRelease = releaseRunners
		queryGroup := queryGroup
		runner.onRun = func() { started <- queryGroup }
		runners[queryGroup] = runner
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(),
		&fakePhaseTwoControl{queryGroups: queryGroups},
		&fakePhaseTwoOwnership{assigned: queryGroups, runners: runners})
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		release()
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}()

	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()
	wantFanout := 2 // Initial execution profile remains two, independent of one query permit.
	for index := 0; index < wantFanout; index++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatalf("pre-permit runner %d did not start", index+1)
		}
	}
	select {
	case queryGroup := <-started:
		t.Fatalf("pre-permit runner fanout exceeded %d at %s", wantFanout, queryGroup)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if len(started) != len(queryGroups)-wantFanout {
		t.Fatalf("runners admitted after fanout drained = %d, want %d", len(started), len(queryGroups)-wantFanout)
	}
	for queryGroup, runtime := range runners {
		if runtime.(*fakePhaseTwoQueryGroup).runCount() != 1 {
			t.Fatalf("single tick Query Group %s run count != 1", queryGroup)
		}
	}
}

func TestPhaseTwoWorkerBundleRunsRetiredBacklogWhenCapacityIsReleased(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 2 // Explicit emergency guard under test.
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2

	started := make(chan execution.QueryGroupIdentity, 4)
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	var firstReleaseOnce, secondReleaseOnce sync.Once
	releaseFirst := func() { firstReleaseOnce.Do(func() { close(firstRelease) }) }
	releaseSecond := func() { secondReleaseOnce.Do(func() { close(secondRelease) }) }
	defer releaseFirst()
	defer releaseSecond()

	runners := make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle, 4)
	for index := 1; index <= 4; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%d", index))
		if index == 3 {
			queryGroup = "query-group-3-retired-backlog"
		}
		runner := newFakePhaseTwoQueryGroup()
		runner.onRun = func() { started <- queryGroup }
		switch index {
		case 1:
			runner.runRelease = firstRelease
		case 2:
			runner.runRelease = secondRelease
		}
		runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: runner}
	}

	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg},
		runners:      runners,
	}
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()

	firstTwo := make(map[execution.QueryGroupIdentity]struct{}, 2)
	for len(firstTwo) < 2 {
		select {
		case queryGroup := <-started:
			firstTwo[queryGroup] = struct{}{}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for the initial runner fanout")
		}
	}
	for _, queryGroup := range []execution.QueryGroupIdentity{"query-group-1", "query-group-2"} {
		if _, ok := firstTwo[queryGroup]; !ok {
			t.Fatalf("initial runners = %v, want query-group-1 and query-group-2", firstTwo)
		}
	}

	releaseSecond()
	select {
	case queryGroup := <-started:
		if queryGroup != "query-group-3-retired-backlog" {
			t.Fatalf("runner after query-group-2 released = %s, want retired backlog", queryGroup)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the retired backlog Query Group")
	}
	releaseFirst()
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
}

func TestPhaseTwoWorkerBundleRetriesReadyQueryGroupBeforeFullSweepCompletes(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	// The complete-Runner gate is what bounds how many Runners execute at
	// once; query permits bound the query stage inside a Runner, and these
	// Runners never reach it. Leaving the gate at its default of zero means
	// unlimited, so a bound asserted against the permits would only ever hold
	// by timing - it read as a guarantee and was one loaded gate run away
	// from reporting a fanout of four against two permits.
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 2
	cfg.PhaseTwo.Scheduler.RetryMinDelay = config.Duration(5 * time.Millisecond)
	cfg.PhaseTwo.Scheduler.RetryMaxDelay = config.Duration(5 * time.Millisecond)

	var concurrencyMu sync.Mutex
	active, maxActive := 0, 0
	activeByQueryGroup := make(map[execution.QueryGroupIdentity]int)
	maxActiveByQueryGroup := make(map[execution.QueryGroupIdentity]int)
	enter := func(queryGroup execution.QueryGroupIdentity) func() {
		concurrencyMu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		activeByQueryGroup[queryGroup]++
		if activeByQueryGroup[queryGroup] > maxActiveByQueryGroup[queryGroup] {
			maxActiveByQueryGroup[queryGroup] = activeByQueryGroup[queryGroup]
		}
		concurrencyMu.Unlock()
		return func() {
			concurrencyMu.Lock()
			active--
			activeByQueryGroup[queryGroup]--
			concurrencyMu.Unlock()
		}
	}

	retried := make(chan struct{})
	var retryMu sync.Mutex
	retryCalls := 0
	retryReadyAt := time.Time{}
	var normalMu sync.Mutex
	normalCompleted := 0
	normalCompletedAtRetry := 0
	retrying := &callbackPhaseTwoQueryGroup{run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
		leave := enter("query-group-a-retrying")
		defer leave()
		retryMu.Lock()
		retryCalls++
		call := retryCalls
		if call == 1 {
			retryReadyAt = time.Now()
		} else {
			retryReadyAt = time.Time{}
		}
		retryMu.Unlock()
		if call == 1 {
			return execution.SlotExecutionResult{
				Result:     observability.ResultRetrying,
				ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable),
			}, true, nil
		}
		if call == 2 {
			normalMu.Lock()
			normalCompletedAtRetry = normalCompleted
			normalMu.Unlock()
			close(retried)
		}
		return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}, true, nil
	}, nextReadyAt: func() time.Time {
		retryMu.Lock()
		defer retryMu.Unlock()
		return retryReadyAt
	}}

	blockingStarted := make(chan struct{})
	blockingRelease := make(chan struct{})
	blocking := &callbackPhaseTwoQueryGroup{run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
		leave := enter("query-group-b-blocking")
		defer leave()
		close(blockingStarted)
		<-blockingRelease
		return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}, true, nil
	}}
	const normalBacklogCount = 500

	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-a-retrying": {runner: retrying},
			"query-group-b-blocking": {runner: blocking},
		},
	}
	for index := 0; index < normalBacklogCount; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-c-normal-%02d", index))
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				leave := enter(queryGroup)
				defer leave()
				normalMu.Lock()
				normalCompleted++
				normalMu.Unlock()
				return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}, true, nil
			},
		}}
	}
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()
	waitSignal(t, blockingStarted, "blocking Query Group")

	retriedBeforeSweepCompleted := false
	select {
	case <-retried:
		retriedBeforeSweepCompleted = true
	case <-time.After(200 * time.Millisecond):
	}
	close(blockingRelease)
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if !retriedBeforeSweepCompleted {
		t.Fatal("retry-ready Query Group was starved behind the full sweep barrier")
	}
	if normalCompletedAtRetry >= normalBacklogCount {
		t.Fatalf("retry-ready Query Group ran only after normal backlog drained: completed=%d", normalCompletedAtRetry)
	}

	concurrencyMu.Lock()
	defer concurrencyMu.Unlock()
	if maxActive > cfg.PhaseTwo.Scheduler.ActiveExecutionLimit {
		t.Fatalf("runner fanout = %d, want <= %d", maxActive, cfg.PhaseTwo.Scheduler.ActiveExecutionLimit)
	}
	if maxActiveByQueryGroup["query-group-a-retrying"] > 1 {
		t.Fatalf("retrying Query Group concurrency = %d, want single-flight", maxActiveByQueryGroup["query-group-a-retrying"])
	}
}

func TestPhaseTwoWorkerBundleNextTickReentersNormalQueryGroupBeforeSlowSweepCompletes(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2

	var normalCalls atomic.Int32
	firstNormal := make(chan struct{})
	secondNormal := make(chan struct{})
	normal := &callbackPhaseTwoQueryGroup{run: func(context.Context) (
		execution.SlotExecutionResult, bool, error,
	) {
		switch normalCalls.Add(1) {
		case 1:
			close(firstNormal)
		case 2:
			close(secondNormal)
		}
		return execution.SlotExecutionResult{}, false, nil
	}}
	blockingStarted := make(chan struct{})
	blockingRelease := make(chan struct{})
	blocking := &callbackPhaseTwoQueryGroup{run: func(context.Context) (
		execution.SlotExecutionResult, bool, error,
	) {
		close(blockingStarted)
		<-blockingRelease
		return execution.SlotExecutionResult{}, false, nil
	}}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-a-normal":   {runner: normal},
			"query-group-b-blocking": {runner: blocking},
		},
	}
	backlogProgress := make(chan struct{}, 1)
	for index := 0; index < 500; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-c-backlog-%03d", index))
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				select {
				case backlogProgress <- struct{}{}:
				default:
				}
				return execution.SlotExecutionResult{}, false, nil
			},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()
	defer func() {
		cancel()
		close(blockingRelease)
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("runScheduler(cancel) error = %v, want context canceled", err)
			}
		case <-time.After(time.Second):
			t.Error("persistent dispatcher did not drain after cancellation")
		}
	}()
	wake <- struct{}{}
	waitSignal(t, firstNormal, "first normal Query Group run")
	waitSignal(t, blockingStarted, "blocking Query Group")
	wake <- struct{}{}
	// This is a 500-QG ordering test, not a one-second throughput contract.
	// Keep a short stalled-work watchdog and a separate total test bound;
	// backlog progress alone must never substitute for normal reentry.
	stalled := time.NewTimer(time.Second)
	defer stalled.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-secondNormal:
			return
		case <-backlogProgress:
			if !stalled.Stop() {
				select {
				case <-stalled.C:
				default:
				}
			}
			stalled.Reset(time.Second)
		case <-stalled.C:
			t.Fatal("normal reentry and backlog processing both stalled")
		case <-deadline.C:
			t.Fatal("normal Query Group did not reenter within bounded test window")
		}
	}
}

func TestPhaseTwoWorkerBundleNormalQueueRotatesPastFastPrefixAcrossTicks(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 2

	started := make(chan execution.QueryGroupIdentity)
	release := make(chan struct{})
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
	}
	const queryGroupCount = 10
	for index := 0; index < queryGroupCount; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%02d", index))
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			run: func(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
				select {
				case started <- queryGroup:
				case <-ctx.Done():
					return execution.SlotExecutionResult{}, false, ctx.Err()
				}
				select {
				case <-release:
					return execution.SlotExecutionResult{}, false, nil
				case <-ctx.Done():
					return execution.SlotExecutionResult{}, false, ctx.Err()
				}
			},
		}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()
	wake <- struct{}{}
	target := execution.QueryGroupIdentity("query-group-09")
	reachedTarget := false
	for attempt := 0; attempt < queryGroupCount+2; attempt++ {
		select {
		case queryGroup := <-started:
			if queryGroup == target {
				reachedTarget = true
				cancel()
				break
			}
			wake <- struct{}{}
			release <- struct{}{}
		case <-time.After(time.Second):
			cancel()
			t.Fatal("normal dispatcher stopped making progress")
		}
		if reachedTarget {
			break
		}
	}
	if !reachedTarget {
		cancel()
		t.Fatal("tail Query Group was starved by repeated scheduler ticks")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduler(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("normal dispatcher did not drain after cancellation")
	}
}

func TestPhaseTwoWorkerBundleDelayedQueueKeepsEarliestReadyQueryGroups(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = 2
	now := time.Now()
	target := execution.QueryGroupIdentity("query-group-z-ready")
	started := make(chan execution.QueryGroupIdentity, 1)
	var targetDelayed atomic.Bool
	targetDelayed.Store(true)
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: func() time.Time { return now }},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
	}
	for _, queryGroup := range []execution.QueryGroupIdentity{"query-group-a-future", "query-group-b-future"} {
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			nextReadyAt: func() time.Time { return now.Add(time.Hour) },
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				started <- queryGroup
				return execution.SlotExecutionResult{}, false, nil
			},
		}}
	}
	bundle.runners[target] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
		nextReadyAt: func() time.Time {
			if targetDelayed.Load() {
				return now.Add(-time.Second)
			}
			return time.Time{}
		},
		run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
			targetDelayed.Store(false)
			started <- target
			return execution.SlotExecutionResult{}, false, nil
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()
	wake <- struct{}{}
	select {
	case queryGroup := <-started:
		if queryGroup != target {
			t.Fatalf("first delayed Query Group = %s, want %s", queryGroup, target)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ready Query Group was hidden behind a full future delayed queue")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduler(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("delayed dispatcher did not stop after cancellation")
	}
}

func TestPhaseTwoRunnerDispatcherPrunesRemovedGenerationState(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroup := execution.QueryGroupIdentity("query-group-removed")
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
		run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
			return execution.SlotExecutionResult{}, false, nil
		},
	}}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			queryGroup: lifecycle,
		},
	}
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.generation = 1
	dispatcher.fillQueues()
	if len(dispatcher.lastQueued) != 1 || len(dispatcher.queued) != 1 {
		t.Fatalf("dispatcher generation state = %d/%d, want 1/1", len(dispatcher.lastQueued), len(dispatcher.queued))
	}
	bundle.mu.Lock()
	delete(bundle.runners, queryGroup)
	bundle.mu.Unlock()
	dispatcher.dropStaleQueued()
	if len(dispatcher.lastQueued) != 0 || len(dispatcher.queued) != 0 || len(dispatcher.normal) != 0 {
		t.Fatalf("removed lifecycle remained in dispatcher: generation=%d queued=%d normal=%d",
			len(dispatcher.lastQueued), len(dispatcher.queued), len(dispatcher.normal))
	}
}

func TestPhaseTwoWorkerBundleDispatcherDoesNotHoldQueryRecoveryAllowanceAcrossRunner(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 4
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 1

	holderStarted := make(chan struct{})
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-a-first-replay": {runner: &callbackPhaseTwoQueryGroup{
				operation: execution.OperationReplay,
				run: func(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
					close(holderStarted)
					<-ctx.Done()
					return execution.SlotExecutionResult{}, false, ctx.Err()
				},
			}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{})
	done := make(chan error, 1)
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.start(ctx)
	go func() { done <- dispatcher.run(ctx, wake) }()
	wake <- struct{}{}
	waitSignal(t, holderStarted, "first-attempt Replay")

	normalStarted := make(chan struct{})
	var normalStartOnce sync.Once
	var normalDelayed atomic.Bool
	normalDelayed.Store(true)
	recoveryRan := make(chan struct{}, 2)
	var recoveryRuns atomic.Int32
	pastReadyAt := time.Now().Add(-time.Second)
	bundle.mu.Lock()
	for index := 0; index < 2; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-b-recovery-%d", index))
		operation := execution.OperationReplay
		var nextReadyAt func() time.Time
		var delayed atomic.Bool
		if index == 1 {
			operation = execution.OperationRetry
			delayed.Store(true)
			nextReadyAt = func() time.Time {
				if delayed.Load() {
					return pastReadyAt
				}
				return time.Time{}
			}
		}
		bundle.setRunnerLocked(queryGroup, &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			operation:   operation,
			nextReadyAt: nextReadyAt,
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				delayed.Store(false)
				recoveryRuns.Add(1)
				recoveryRan <- struct{}{}
				return execution.SlotExecutionResult{}, false, nil
			},
		}})
	}
	bundle.setRunnerLocked("query-group-c-source-retry-normal", &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
		operation: execution.OperationNormal,
		nextReadyAt: func() time.Time {
			if normalDelayed.Load() {
				return pastReadyAt
			}
			return time.Time{}
		},
		run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
			normalDelayed.Store(false)
			normalStartOnce.Do(func() { close(normalStarted) })
			return execution.SlotExecutionResult{}, false, nil
		},
	}})
	bundle.mu.Unlock()
	wake <- struct{}{}
	waitSignal(t, normalStarted, "actual Normal operation after delayed source retry")
	for index := 0; index < 2; index++ {
		select {
		case <-recoveryRan:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("recovery Runner was blocked by another Runner holding query recovery allowance")
		}
	}
	if recoveryRuns.Load() < 2 {
		cancel()
		<-done
		dispatcher.stop()
		t.Fatalf("recovery executions while earlier Runner was blocked = %d, want at least 2", recoveryRuns.Load())
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("dispatcher(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not drain recovery after cancellation")
	}
	dispatcher.stop()
}

func TestPhaseTwoWorkerBundleSchedulerCancellationStopsAdmissionAndDrainsInflight(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 2 // Explicit emergency guard under test.
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2

	started := make(chan struct{}, 2)
	blockingRunner := func() *callbackPhaseTwoQueryGroup {
		return &callbackPhaseTwoQueryGroup{run: func(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
			started <- struct{}{}
			<-ctx.Done()
			return execution.SlotExecutionResult{}, false, ctx.Err()
		}}
	}
	var queuedCalls atomic.Int32
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-a-blocking": {runner: blockingRunner()},
			"query-group-b-blocking": {runner: blockingRunner()},
			"query-group-c-queued": {runner: &callbackPhaseTwoQueryGroup{run: func(context.Context) (
				execution.SlotExecutionResult, bool, error,
			) {
				queuedCalls.Add(1)
				return execution.SlotExecutionResult{}, false, nil
			}}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(ctx) }()
	waitSignal(t, started, "first inflight Query Group")
	waitSignal(t, started, "second inflight Query Group")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduledOnce(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not drain canceled inflight Query Groups")
	}
	if got := queuedCalls.Load(); got != 0 {
		t.Fatalf("dispatcher admitted %d queued runs after cancellation", got)
	}
}

func TestPhaseTwoWorkerBundleDispatcherDropsReplacedLifecycleBeforeDispatch(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 1
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0

	blockingStarted := make(chan struct{})
	blockingRelease := make(chan struct{})
	blocking := &callbackPhaseTwoQueryGroup{run: func(context.Context) (
		execution.SlotExecutionResult, bool, error,
	) {
		close(blockingStarted)
		<-blockingRelease
		return execution.SlotExecutionResult{}, false, nil
	}}
	var oldCalls, newCalls atomic.Int32
	oldLifecycle := &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{run: func(context.Context) (
		execution.SlotExecutionResult, bool, error,
	) {
		oldCalls.Add(1)
		return execution.SlotExecutionResult{}, false, nil
	}}}
	newLifecycle := &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{run: func(context.Context) (
		execution.SlotExecutionResult, bool, error,
	) {
		newCalls.Add(1)
		return execution.SlotExecutionResult{}, false, nil
	}}}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-a-blocking": {runner: blocking},
			"query-group-z-replaced": oldLifecycle,
		},
	}
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()
	waitSignal(t, blockingStarted, "blocking Query Group")
	bundle.mu.Lock()
	bundle.setRunnerLocked("query-group-z-replaced", newLifecycle)
	bundle.mu.Unlock()
	close(blockingRelease)
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce(replacement) error = %v", err)
	}
	if got := oldCalls.Load(); got != 0 {
		t.Fatalf("replaced lifecycle ran %d times", got)
	}
	newCallsBeforeNextTick := newCalls.Load()
	bundle.mu.Lock()
	bundle.removeRunnerLocked("query-group-a-blocking")
	bundle.mu.Unlock()
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(current replacement) error = %v", err)
	}
	if got := newCalls.Load(); got != newCallsBeforeNextTick+1 {
		t.Fatalf("current replacement lifecycle runs = %d, want %d", got, newCallsBeforeNextTick+1)
	}
}

func TestPhaseTwoWorkerBundleRunKeepsControlTicksActiveWhileSchedulerIsBusy(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*config.Config)
		observe   func(*signalingPhaseTwoControl, *signalingPhaseTwoOwnership) <-chan struct{}
	}{
		{
			name: "refresh",
			configure: func(cfg *config.Config) {
				cfg.PhaseTwo.Control.RefreshInterval = config.Duration(2 * time.Millisecond)
				cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(time.Hour)
			},
			observe: func(control *signalingPhaseTwoControl, _ *signalingPhaseTwoOwnership) <-chan struct{} {
				return control.refreshed
			},
		},
		{
			name: "reconcile",
			configure: func(cfg *config.Config) {
				cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
				cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(2 * time.Millisecond)
			},
			observe: func(_ *signalingPhaseTwoControl, owner *signalingPhaseTwoOwnership) <-chan struct{} {
				return owner.assigned
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
			test.configure(&cfg)
			queryGroup := execution.QueryGroupIdentity("query-group-busy")
			var schedulerBusy atomic.Bool
			control := &signalingPhaseTwoControl{
				fakePhaseTwoControl: &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}},
				refreshed:           make(chan struct{}, 1),
				busy:                &schedulerBusy,
			}
			owner := &signalingPhaseTwoOwnership{
				fakePhaseTwoOwnership: &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}},
				assigned:              make(chan struct{}, 4),
				busy:                  &schedulerBusy,
			}
			runStarted := make(chan struct{})
			var startOnce sync.Once
			owner.runner = &callbackPhaseTwoQueryGroup{run: func(ctx context.Context) (
				execution.SlotExecutionResult, bool, error,
			) {
				schedulerBusy.Store(true)
				startOnce.Do(func() { close(runStarted) })
				<-ctx.Done()
				return execution.SlotExecutionResult{}, false, ctx.Err()
			}}
			bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- bundle.Run(ctx) }()
			waitSignal(t, runStarted, "busy scheduler Query Group")
			waitSignal(t, test.observe(control, owner), test.name+" tick")
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run(cancel) error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Run did not stop after scheduler and control cancellation")
			}
		})
	}
}

func TestPhaseTwoWorkerBundleNeverRegistersReadyAfterDraining(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(100 * time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Millisecond)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	renewalStarted := make(chan struct{})
	releaseRenewal := make(chan struct{})
	drainingObserved := make(chan struct{})
	var callsMu sync.Mutex
	readyCalls := 0
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	owner.beforeRegister = func(registration ownership.WorkerRegistration) {
		callsMu.Lock()
		if registration.AssignmentReadiness == ownership.WorkerReady {
			readyCalls++
			if readyCalls == 2 {
				close(renewalStarted)
				callsMu.Unlock()
				<-releaseRenewal
				return
			}
		}
		if registration.AssignmentReadiness == ownership.WorkerDraining {
			close(drainingObserved)
		}
		callsMu.Unlock()
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, renewalStarted, "READY registration renewal")
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- bundle.Shutdown(context.Background()) }()
	select {
	case <-drainingObserved:
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseRenewal)
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	last := owner.registrations[len(owner.registrations)-1].AssignmentReadiness
	if last != ownership.WorkerDraining {
		t.Fatalf("last worker registration = %s, want DRAINING", last)
	}
}

func TestPhaseTwoWorkerBundleAcquiresControlLeaderBeforeInitialRefresh(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	control.beforeInitialRefresh = func() error {
		if owner.controlLeaderCount() == 0 {
			return errors.New("initial refresh ran without Control Leader authority")
		}
		return nil
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleStartsReadyDegradedWhenInitialSnapshotIsUnavailableAndRecovers(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{
		beforeInitialRefresh: func() error { return controlplane.ErrSnapshotUnavailable },
		refreshResults: []phaseTwoControlRefreshResult{{
			QueryGroups: []execution.QueryGroupIdentity{queryGroup}, Status: phaseTwoControlHealthy,
		}},
	}
	owner := &fakePhaseTwoOwnership{runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start(snapshot unavailable) error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthDegraded || !snapshot.Ready {
		t.Fatalf("initial unavailable health=%+v, want ready degraded", snapshot)
	}
	if owner.publishAssignmentCount() != 0 || len(bundle.runners) != 0 {
		t.Fatalf("initial unavailable published/runners=%d/%d, want 0/0", owner.publishAssignmentCount(), len(bundle.runners))
	}

	owner.setAssigned([]execution.QueryGroupIdentity{queryGroup})
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile(recovered) error = %v", err)
	}
	waitSignal(t, runner.leaseStarted, "recovered query-group lease maintenance")
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("recovered health=%+v, want ready", snapshot)
	}
	if owner.publishAssignmentCount() != 1 {
		t.Fatalf("recovered Assignment publications=%d, want 1", owner.publishAssignmentCount())
	}
}

func TestPhaseTwoWorkerBundleFollowerStartsReadyDegradedWhenActiveSnapshotIsUnavailable(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{loadActiveErr: controlplane.ErrSnapshotUnavailable}
	owner := &fakePhaseTwoOwnership{follower: true}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start(follower snapshot unavailable) error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthDegraded || !snapshot.Ready {
		t.Fatalf("follower unavailable health=%+v, want ready degraded", snapshot)
	}
	if owner.publishAssignmentCount() != 0 || len(bundle.runners) != 0 {
		t.Fatalf("follower unavailable published/runners=%d/%d, want 0/0", owner.publishAssignmentCount(), len(bundle.runners))
	}
}

func TestPhaseTwoWorkerBundleStillRejectsNonSnapshotInitialControlFailure(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	want := errors.New("invalid initial control facts")
	control := &fakePhaseTwoControl{beforeInitialRefresh: func() error { return want }}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, &fakePhaseTwoOwnership{})

	if err := bundle.Start(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Start(non-snapshot failure) error = %v, want %v", err, want)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleKeepsHealthyQueryGroupAcrossSnapshotUnavailableRefresh(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{
		queryGroups:   []execution.QueryGroupIdentity{queryGroup},
		refreshErrors: []error{controlplane.ErrSnapshotUnavailable},
		refreshResults: []phaseTwoControlRefreshResult{{
			QueryGroups: []execution.QueryGroupIdentity{queryGroup}, Status: phaseTwoControlHealthy,
		}},
	}
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner,
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	waitSignal(t, runner.leaseStarted, "healthy query-group lease maintenance")
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile(snapshot unavailable) error = %v", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthDegraded || !snapshot.Ready {
		t.Fatalf("refresh unavailable health=%+v, want ready degraded", snapshot)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil || runner.runCount() != 1 {
		t.Fatalf("healthy runner after unavailable refresh calls/error=%d/%v, want 1/nil", runner.runCount(), err)
	}
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile(recovered) error = %v", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("recovered health=%+v, want ready", snapshot)
	}
}

func TestPhaseTwoWorkerBundleFollowerLoadsControlFactsAndRunsItsAssignment(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		follower: true, assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner,
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, runner.leaseStarted, "follower query-group lease maintenance")
	if control.initialRefreshCalls != 0 || control.loadActiveCalls != 1 {
		t.Fatalf("follower control calls initial/load = %d/%d, want 0/1", control.initialRefreshCalls, control.loadActiveCalls)
	}
	if owner.publishAssignmentCount() != 0 {
		t.Fatalf("follower published %d Assignment batches", owner.publishAssignmentCount())
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if runner.runCount() != 1 {
		t.Fatalf("follower runner calls = %d, want 1", runner.runCount())
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleLeaseFailureIsLocalToQueryGroup(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	lost := newFakePhaseTwoQueryGroup()
	lost.leaseErr = ownership.ErrStaleFence
	lost.leaseRelease = make(chan struct{})
	healthy := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": lost, "query-group-2": healthy,
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, lost.leaseStarted, "lost query-group lease maintenance")
	waitSignal(t, healthy.leaseStarted, "healthy query-group lease maintenance")
	close(lost.leaseRelease)
	waitSignal(t, lost.leaseFinished, "stale lease failure")
	waitForHealthState(t, health, observability.HealthNotReady)
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(after local lease loss) error = %v", err)
	}
	if lost.runCount() != 0 || healthy.runCount() != 1 {
		t.Fatalf("runner calls lost/healthy = %d/%d, want 0/1", lost.runCount(), healthy.runCount())
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleStaleRunnerDoesNotStopSiblingQueryGroup(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	stale := newFakePhaseTwoQueryGroup()
	stale.runErr = ownership.ErrStaleFence
	healthy := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": stale, "query-group-2": healthy,
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(stale sibling) error = %v", err)
	}
	if stale.runCount() != 1 || healthy.runCount() != 1 {
		t.Fatalf("runner calls stale/healthy = %d/%d, want 1/1", stale.runCount(), healthy.runCount())
	}
	if stale.releaseCount() != 1 {
		t.Fatalf("stale runner release calls = %d, want 1", stale.releaseCount())
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleSlotOwnershipChangeStopsOnlyInvalidQueryGroup(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	changed := newFakePhaseTwoQueryGroup()
	changed.runErr = scheduler.ErrSlotOwnershipChanged
	healthy := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": changed, "query-group-2": healthy,
	}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(ownership changed sibling) error = %v", err)
	}
	if changed.runCount() != 1 || healthy.runCount() != 1 {
		t.Fatalf("runner calls changed/healthy = %d/%d, want 1/1", changed.runCount(), healthy.runCount())
	}
	if changed.releaseCount() != 1 {
		t.Fatalf("ownership-changed runner releases = %d, want 1", changed.releaseCount())
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleProgressConflictDoesNotStopSiblingOrWorker(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	failed := newFakePhaseTwoQueryGroup()
	failed.runErr = errors.New("commit progress after cutover: CONFLICT")
	healthy := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		"query-group-1": failed, "query-group-2": healthy,
	}}
	var mu sync.Mutex
	var observations []observability.Observation
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner, Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			mu.Lock()
			defer mu.Unlock()
			observations = append(observations, observation)
		}),
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(local Query Group failure) error = %v", err)
	}
	if failed.runCount() != 1 || healthy.runCount() != 1 {
		t.Fatalf("runner calls failed/healthy = %d/%d, want 1/1", failed.runCount(), healthy.runCount())
	}
	if failed.releaseCount() != 0 {
		t.Fatalf("local Query Group failure migrated ownership, releases = %d", failed.releaseCount())
	}
	mu.Lock()
	failures := schedulerFailureObservations(observations)
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			mu.Unlock()
			t.Fatalf("local Query Group failure became fatal: %+v", observation)
		}
	}
	mu.Unlock()
	if len(failures) != 1 || failures[0].Trace.QueryGroupKey != "query-group-1" {
		t.Fatalf("scheduler failure observations = %+v, want one local Query Group trace", failures)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleQueryFreeGapConflictDoesNotStopSiblingOrWorker(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-free-gap-conflict", "healthy-sibling"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	failed := newFakePhaseTwoQueryGroup()
	failed.runErr = errors.New("finalize query-free Slot: activated Plan gap marker conflicts with the Slot")
	healthy := newFakePhaseTwoQueryGroup()
	healthy.runResult = execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		queryGroups[0]: failed,
		queryGroups[1]: healthy,
	}}
	var mu sync.Mutex
	var observations []observability.Observation
	health := newPhaseTwoApplicationHealth()
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
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error=%v", err)
	}
	if failed.runCount() != 1 || healthy.runCount() != 1 || failed.releaseCount() != 0 {
		t.Fatalf("run/release counts failed=%d/%d healthy=%d", failed.runCount(), failed.releaseCount(), healthy.runCount())
	}
	if snapshot := health.HealthSnapshot(); !snapshot.Ready || snapshot.State == observability.HealthFatal {
		t.Fatalf("worker health=%+v", snapshot)
	}
	mu.Lock()
	failures := schedulerFailureObservations(observations)
	mu.Unlock()
	if len(failures) != 1 || failures[0].Trace.QueryGroupKey != string(queryGroups[0]) {
		t.Fatalf("scheduler failures=%+v", failures)
	}
}

func TestPhaseTwoWorkerBundleMissingFrozenQueryFactsDoesNotStopThresholdSiblingOrWorker(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-g4-bad", "query-group-threshold-healthy"}
	control := &fakePhaseTwoControl{queryGroups: queryGroups}
	failed := newFakePhaseTwoQueryGroup()
	failed.attempted = true
	failed.runErr = fmt.Errorf("resolve frozen input closure: %w", access.ErrFrozenQueryPlanUnavailable)
	healthy := newFakePhaseTwoQueryGroup()
	healthy.attempted = true
	healthy.runResult = execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		queryGroups[0]: failed, queryGroups[1]: healthy,
	}}
	var mu sync.Mutex
	var observations []observability.Observation
	health := newPhaseTwoApplicationHealth()
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
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error=%v", err)
	}
	if failed.runCount() != 1 || healthy.runCount() != 1 || failed.releaseCount() != 0 || healthy.releaseCount() != 0 {
		t.Fatalf("run/release counts failed=%d/%d healthy=%d/%d", failed.runCount(), failed.releaseCount(),
			healthy.runCount(), healthy.releaseCount())
	}
	if !errors.Is(failed.runErr, access.ErrFrozenQueryPlanUnavailable) {
		t.Fatalf("failed reason=%v", failed.runErr)
	}
	if snapshot := health.HealthSnapshot(); !snapshot.Ready || snapshot.State == observability.HealthFatal {
		t.Fatalf("worker health=%+v", snapshot)
	}
	mu.Lock()
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			mu.Unlock()
			t.Fatalf("local frozen input failure became fatal: %+v", observation)
		}
	}
	mu.Unlock()
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleDoesNotDuplicateAttemptedRunnerFailureObservation(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	failed := newFakePhaseTwoQueryGroup()
	failed.attempted = true
	failed.runErr = errors.New("executor already observed this failure")
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: failed}
	var observations []observability.Observation
	var observationsMu sync.Mutex
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner, Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observationsMu.Lock()
			observations = append(observations, observation)
			observationsMu.Unlock()
		}),
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(attempted failure) error = %v", err)
	}
	observationsMu.Lock()
	failures := schedulerFailureObservations(observations)
	observationsMu.Unlock()
	if failures := failures; len(failures) != 0 {
		t.Fatalf("attempted runner failure was observed twice: %+v", failures)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleDoesNotReobserveSourceRetryResults(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-blocked", "query-group-temporary"}
	blocked := newFakePhaseTwoQueryGroup()
	blocked.attempted = true
	blocked.runResult = execution.SlotExecutionResult{Result: observability.ResultRetrying,
		ReasonCode: execution.ReasonCode(contract.ReasonBlockedExactSetUnavailable), SourceRetry: true}
	temporary := newFakePhaseTwoQueryGroup()
	temporary.attempted = true
	temporary.runResult = execution.SlotExecutionResult{Result: observability.ResultRetrying,
		ReasonCode: execution.ReasonCode(contract.ReasonProviderUnavailable), SourceRetry: true}
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
		queryGroups[0]: blocked, queryGroups[1]: temporary,
	}}
	var observations []observability.Observation
	var observationsMu sync.Mutex
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: &fakePhaseTwoControl{queryGroups: queryGroups},
		Ownership: owner, Now: time.Now, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observationsMu.Lock()
			observations = append(observations, observation)
			observationsMu.Unlock()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []observability.Observation
	for _, observation := range observations {
		if observation.Component == observability.ComponentScheduler && observation.Stage == observability.StageScheduleDue &&
			observation.Result == observability.ResultRetrying {
			got = append(got, observation)
		}
	}
	if len(got) != 0 {
		t.Fatalf("source retries were re-observed after SlotSource observation: %+v", got)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleReconcileUsesCurrentSnapshotWithoutRefreshingAgain(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.refreshAndReconcile(context.Background(), false); err != nil {
		t.Fatalf("refreshAndReconcile(reconcile only) error = %v", err)
	}
	if control.initialRefreshCalls != 1 {
		t.Fatalf("initial refresh calls = %d, want 1", control.initialRefreshCalls)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleAppliesAssignmentDiffWithoutRestart(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	first := newFakePhaseTwoQueryGroup()
	second := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{"query-group-1"},
		runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
			"query-group-1": first, "query-group-2": second,
		},
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, first.leaseStarted, "first query-group lease maintenance")
	control.queryGroups = []execution.QueryGroupIdentity{"query-group-2"}
	owner.setAssigned([]execution.QueryGroupIdentity{"query-group-2"})
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile() error = %v", err)
	}
	waitSignal(t, first.leaseFinished, "removed query-group lease cancellation")
	waitSignal(t, second.leaseStarted, "new query-group lease maintenance")
	if first.releaseCount() != 1 {
		t.Fatalf("removed runner release calls = %d, want 1", first.releaseCount())
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if first.runCount() != 0 || second.runCount() != 1 {
		t.Fatalf("runner calls old/new = %d/%d, want 0/1", first.runCount(), second.runCount())
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleKeepsTwoQueryGroupsReadyWhileControlDegradedAndRecordsRecovery(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-a", "query-group-b"}
	degraded := phaseTwoControlRefreshResult{
		QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood,
		SourceKind: observability.SourceKindLegacyStrategy, ReasonCode: observability.ReasonContractRetryable,
		Cause: errors.New("source unavailable"),
	}
	healthy := phaseTwoControlRefreshResult{QueryGroups: queryGroups, Status: phaseTwoControlHealthy}
	control := &fakePhaseTwoControl{
		initialResult:  degraded,
		refreshResults: []phaseTwoControlRefreshResult{degraded, healthy},
	}
	first := newFakePhaseTwoQueryGroup()
	second := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: queryGroups,
		runners: map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{
			"query-group-a": first,
			"query-group-b": second,
		},
	}
	health := newPhaseTwoApplicationHealth()
	recoveredAt := time.Unix(1_800_000_000, 0)
	var mu sync.Mutex
	var observations []observability.Observation
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: owner, Now: func() time.Time { return recoveredAt },
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			mu.Lock()
			defer mu.Unlock()
			observations = append(observations, observation)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	waitSignal(t, first.leaseStarted, "first healthy query-group")
	waitSignal(t, second.leaseStarted, "second healthy query-group")
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthDegraded || !snapshot.Ready {
		t.Fatalf("last-good health=%+v, want ready degraded", snapshot)
	}
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("repeated degraded refresh error = %v", err)
	}
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("healthy recovery refresh error = %v", err)
	}
	snapshot := health.HealthSnapshot()
	if snapshot.State != observability.HealthReady || !snapshot.Ready || !snapshot.LastRecoveryAt.Equal(recoveredAt) {
		t.Fatalf("recovered health=%+v", snapshot)
	}
	bundle.mu.RLock()
	runnerCount := len(bundle.runners)
	bundle.mu.RUnlock()
	if runnerCount != 2 {
		t.Fatalf("healthy runners after control episode=%d, want 2", runnerCount)
	}
	mu.Lock()
	got := append([]observability.Observation(nil), observations...)
	mu.Unlock()
	degradedCount, recoveredCount := 0, 0
	for _, observation := range got {
		if observation.Component != observability.ComponentControlPlane {
			continue
		}
		switch observation.Result {
		case observability.ResultDegraded:
			degradedCount++
		case observability.Result(observability.ResultRecovered):
			recoveredCount++
		}
	}
	if degradedCount != 1 || recoveredCount != 1 {
		t.Fatalf("control episode observations degraded/recovered=%d/%d: %+v", degradedCount, recoveredCount, got)
	}
}

func TestPhaseTwoWorkerBundleDoesNotDuplicateObservedSourceRefresh(t *testing.T) {
	observations := 0
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {
			observations++
		}),
		Now: time.Now,
	}}
	if err := bundle.applyControlRefresh(context.Background(), phaseTwoControlRefreshResult{
		Status: phaseTwoControlHealthy, SourceRefreshObserved: true,
	}); err != nil {
		t.Fatalf("applyControlRefresh() error = %v", err)
	}
	if observations != 0 {
		t.Fatalf("duplicate source refresh observations = %d", observations)
	}
}

func TestPhaseTwoWorkerBundleReportsOwnedQueryGroupsAndFixedOwnershipTransitions(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{"query-group-1"},
		runners:  map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{"query-group-1": runner},
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var mu sync.Mutex
	var observations []observability.Observation
	observer := observability.Multi(recorder, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		mu.Lock()
		defer mu.Unlock()
		observations = append(observations, observation)
	}))
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner,
		Recorder: recorder, Observer: observer, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	assertOwnedQueryGroupGauge(t, recorder, 1)

	control.queryGroups = nil
	owner.setAssigned(nil)
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile() error = %v", err)
	}
	waitSignal(t, runner.leaseFinished, "removed query-group lease cancellation")
	assertOwnedQueryGroupGauge(t, recorder, 0)

	mu.Lock()
	gotObservations := append([]observability.Observation(nil), observations...)
	mu.Unlock()
	want := []observability.Stage{
		observability.StageTakeoverStarted,
		observability.StageTakeoverCompleted,
		observability.StageAssignmentAcquired,
		observability.StageAssignmentLost,
	}
	for _, stage := range want {
		found := false
		for _, observation := range gotObservations {
			if observation.Component == observability.ComponentOwnership && observation.Stage == stage &&
				observation.Trace.QueryGroupKey == "query-group-1" && observation.Trace.OwnerID == cfg.PhaseTwo.Worker.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ownership observations = %+v, missing %s with worker/QG identity", gotObservations, stage)
		}
	}
	assertOwnershipTransitionMetrics(t, recorder, []string{
		`bkmonitor_alarmd_ownership_transition_total{reason_class="none",result="started",transition="takeover_started"} 1`,
		`bkmonitor_alarmd_ownership_transition_total{reason_class="none",result="success",transition="takeover_completed"} 1`,
		`bkmonitor_alarmd_ownership_transition_total{reason_class="none",result="success",transition="assignment_acquired"} 1`,
		`bkmonitor_alarmd_ownership_transition_total{reason_class="none",result="success",transition="assignment_lost"} 1`,
	})
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoRuntimeObserverReportsRealFenceChecks(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var observations []observability.Observation
	next := observability.Multi(recorder, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	}))
	observer := phaseTwoRuntimeObserver(next)
	want := observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageSideEffectAdmission,
		Result: observability.ResultSuccess, Operation: observability.OperationNormal,
		Direction: observability.DirectionInternal, ReasonCode: observability.ReasonNone,
		Trace: observability.TraceFields{QueryGroupKey: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 2},
	}
	observer.Observe(context.Background(), want)
	if len(observations) != 2 || observations[0].Stage != observability.StageSideEffectAdmission {
		t.Fatalf("observations = %+v, want admission followed by fence check", observations)
	}
	got := observations[1]
	if got.Component != observability.ComponentOwnership || got.Stage != observability.StageFenceChecked ||
		got.Result != want.Result || got.ReasonCode != want.ReasonCode || got.Trace != want.Trace {
		t.Fatalf("fence observation = %+v, want admission result/reason/trace", got)
	}
	assertOwnershipTransitionMetrics(t, recorder, []string{
		`bkmonitor_alarmd_ownership_transition_total{reason_class="none",result="success",transition="fence_checked"} 1`,
	})
}

func assertOwnedQueryGroupGauge(t *testing.T, recorder *metric.Recorder, count int) {
	t.Helper()
	want := strings.NewReader("# HELP bkmonitor_alarmd_worker_owned_query_groups Query groups currently owned by this complete worker role.\n" +
		"# TYPE bkmonitor_alarmd_worker_owned_query_groups gauge\n" +
		fmt.Sprintf("bkmonitor_alarmd_worker_owned_query_groups{worker_role=\"complete\"} %d\n", count))
	if err := testutil.GatherAndCompare(recorder.Gatherer(), want, "bkmonitor_alarmd_worker_owned_query_groups"); err != nil {
		t.Fatal(err)
	}
}

func assertOwnershipTransitionMetrics(t *testing.T, recorder *metric.Recorder, samples []string) {
	t.Helper()
	want := strings.NewReader("# HELP bkmonitor_alarmd_ownership_transition_total Ownership lifecycle transitions by bounded transition, result and reason class.\n" +
		"# TYPE bkmonitor_alarmd_ownership_transition_total counter\n" + strings.Join(samples, "\n") + "\n")
	if err := testutil.GatherAndCompare(recorder.Gatherer(), want, "bkmonitor_alarmd_ownership_transition_total"); err != nil {
		t.Fatal(err)
	}
}

func TestPhaseTwoWorkerBundleIsReadyWithNoCurrentAssignments(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{}}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("health without assignments = %+v, want ready idle Worker", snapshot)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleWaitsForBusyAssignedLeaseWithoutStoppingWorker(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{
		assigned:   []execution.QueryGroupIdentity{"query-group-1"},
		openErrors: map[execution.QueryGroupIdentity]error{"query-group-1": ownership.ErrLeaseBusy},
	}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want handoff wait", err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthNotReady || snapshot.Ready {
		t.Fatalf("health while assigned lease is busy = %+v, want not ready", snapshot)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce(with no acquired runner) error = %v", err)
	}
	_ = bundle.Shutdown(context.Background())
}

func TestPhaseTwoWorkerBundleDrainsInflightSlotBeforeConditionalRelease(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	runner.runStarted = make(chan struct{})
	runner.runRelease = make(chan struct{})
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	runDone := make(chan error, 1)
	go func() { runDone <- bundle.runScheduledOnce(context.Background()) }()
	waitSignal(t, runner.runStarted, "inflight Slot")
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- bundle.Shutdown(context.Background()) }()
	waitForRegistration(t, owner, ownership.WorkerDraining)
	if runner.releaseCount() != 0 {
		t.Fatal("Shutdown released the lease before the inflight Slot drained")
	}
	if err := bundle.runScheduledOnce(context.Background()); !errors.Is(err, errPhaseTwoWorkerDraining) {
		t.Fatalf("runScheduledOnce(during drain) error = %v, want draining rejection", err)
	}
	close(runner.runRelease)
	if err := <-runDone; err != nil {
		t.Fatalf("inflight run error = %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if runner.releaseCount() != 1 {
		t.Fatalf("release calls = %d, want 1 after drain", runner.releaseCount())
	}
}

func TestPhaseTwoWorkerBundleObservesLifecycleWithoutInventingSlotTransitions(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	runner.attempted = true
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
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
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := bundle.runScheduledOnce(context.Background()); err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	stages := make([]observability.Stage, len(observations))
	for index, observation := range observations {
		stages[index] = observation.Stage
		ownershipTransition := observation.Component == observability.ComponentOwnership &&
			(observation.Stage == observability.StageAssignmentAcquired || observation.Stage == observability.StageAssignmentLost ||
				observation.Stage == observability.StageTakeoverStarted || observation.Stage == observability.StageTakeoverCompleted)
		if ownershipTransition {
			if observation.Trace.QueryGroupKey != "query-group-1" || observation.Trace.OwnerID != cfg.PhaseTwo.Worker.ID {
				t.Fatalf("ownership lifecycle observation lacks diagnostic identity: %+v", observation)
			}
		} else if observation.Trace.QueryGroupKey != "" || observation.Trace.OwnerID != "" {
			t.Fatalf("non-ownership lifecycle observation leaked business identity: %+v", observation)
		}
	}
	for _, want := range []observability.Stage{
		observability.Stage(observability.StageStartup), observability.StageConfigLoaded,
		observability.StageSnapshotRefreshed, observability.StageAssignmentAcquired,
		observability.Stage(observability.StageShutdown),
	} {
		if !containsStage(stages, want) {
			t.Fatalf("lifecycle stages = %v, missing %s", stages, want)
		}
	}
	for _, absent := range []observability.Stage{
		observability.StageScheduleDue, observability.StageSlotStarted, observability.StageSlotCompleted,
	} {
		if containsStage(stages, absent) {
			t.Fatalf("fake Query Group runner produced %s without a frozen due Slot: %v", absent, stages)
		}
	}
}

func mustPhaseTwoWorkerBundle(
	t *testing.T,
	cfg config.Config,
	health *phaseTwoApplicationHealth,
	control phaseTwoControlRuntime,
	owner phaseTwoOwnershipRuntime,
) *phaseTwoWorkerBundle {
	t.Helper()
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: owner,
		Observer: observability.NopObserver{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	return bundle
}

type fakePhaseTwoControl struct {
	queryGroups          []execution.QueryGroupIdentity
	initialResult        phaseTwoControlRefreshResult
	refreshResults       []phaseTwoControlRefreshResult
	refreshErrors        []error
	beforeInitialRefresh func() error
	initialRefreshCalls  int
	refreshCalls         int
	loadActiveCalls      int
	loadActiveErr        error
	closeCalls           int
}

type signalingPhaseTwoControl struct {
	*fakePhaseTwoControl
	refreshed chan struct{}
	busy      *atomic.Bool
}

func (control *signalingPhaseTwoControl) Refresh(ctx context.Context) (phaseTwoControlRefreshResult, error) {
	if control.busy.Load() {
		select {
		case control.refreshed <- struct{}{}:
		default:
		}
	}
	return control.fakePhaseTwoControl.Refresh(ctx)
}

func (control *fakePhaseTwoControl) InitialRefresh(context.Context) (phaseTwoControlRefreshResult, error) {
	control.initialRefreshCalls++
	if control.beforeInitialRefresh != nil {
		if err := control.beforeInitialRefresh(); err != nil {
			return phaseTwoControlRefreshResult{}, err
		}
	}
	if control.initialResult.Status != "" {
		return clonePhaseTwoControlRefreshResult(control.initialResult), nil
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: append([]execution.QueryGroupIdentity(nil), control.queryGroups...),
		Status:      phaseTwoControlHealthy,
	}, nil
}

func (control *fakePhaseTwoControl) Refresh(context.Context) (phaseTwoControlRefreshResult, error) {
	call := control.refreshCalls
	control.refreshCalls++
	if call < len(control.refreshErrors) && control.refreshErrors[call] != nil {
		return phaseTwoControlRefreshResult{}, control.refreshErrors[call]
	}
	resultIndex := call - len(control.refreshErrors)
	if resultIndex >= 0 && resultIndex < len(control.refreshResults) {
		result := clonePhaseTwoControlRefreshResult(control.refreshResults[resultIndex])
		return result, nil
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: append([]execution.QueryGroupIdentity(nil), control.queryGroups...),
		Status:      phaseTwoControlHealthy,
	}, nil
}

func clonePhaseTwoControlRefreshResult(result phaseTwoControlRefreshResult) phaseTwoControlRefreshResult {
	result.QueryGroups = append([]execution.QueryGroupIdentity(nil), result.QueryGroups...)
	return result
}

func (control *fakePhaseTwoControl) LoadActive(context.Context) (phaseTwoControlRefreshResult, error) {
	control.loadActiveCalls++
	if control.loadActiveErr != nil {
		return phaseTwoControlRefreshResult{}, control.loadActiveErr
	}
	return phaseTwoControlRefreshResult{
		QueryGroups: append([]execution.QueryGroupIdentity(nil), control.queryGroups...),
		Status:      phaseTwoControlHealthy,
	}, nil
}

func (control *fakePhaseTwoControl) Close() error {
	control.closeCalls++
	return nil
}

type fakePhaseTwoOwnership struct {
	mu             sync.Mutex
	registrations  []ownership.WorkerRegistration
	beforeRegister func(ownership.WorkerRegistration)
	// registerHook may reject or block one registration. It runs before the
	// registration is recorded and receives the per-attempt context.
	registerHook  func(context.Context, ownership.WorkerRegistration) error
	assigned      []execution.QueryGroupIdentity
	runner        phaseTwoQueryGroupRuntime
	runners       map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime
	openErrors    map[execution.QueryGroupIdentity]error
	follower      bool
	controlLeader int
	published     int
	closeCalls    int
	// failSite injects failErr into the next failRemaining calls of one store
	// method ("publish", "assigned" or "open"); a negative count never recovers.
	failSite      string
	failErr       error
	failRemaining int
}

func (owner *fakePhaseTwoOwnership) injectFailure(site string, err error, count int) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.failSite, owner.failErr, owner.failRemaining = site, err, count
}

func (owner *fakePhaseTwoOwnership) consumeFailureLocked(site string) error {
	if owner.failSite != site || owner.failRemaining == 0 || owner.failErr == nil {
		return nil
	}
	if owner.failRemaining > 0 {
		owner.failRemaining--
	}
	return owner.failErr
}

type signalingPhaseTwoOwnership struct {
	*fakePhaseTwoOwnership
	assigned chan struct{}
	busy     *atomic.Bool
}

func (owner *signalingPhaseTwoOwnership) AssignedQueryGroups(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	if owner.busy.Load() {
		select {
		case owner.assigned <- struct{}{}:
		default:
		}
	}
	return owner.fakePhaseTwoOwnership.AssignedQueryGroups(ctx, queryGroups)
}

func (owner *fakePhaseTwoOwnership) TryAcquireControlLeader(context.Context, time.Time, time.Duration) (bool, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.follower {
		return false, nil
	}
	owner.controlLeader++
	return true, nil
}

func (owner *fakePhaseTwoOwnership) RegisterWorker(ctx context.Context, registration ownership.WorkerRegistration) error {
	if owner.beforeRegister != nil {
		owner.beforeRegister(registration)
	}
	if owner.registerHook != nil {
		if err := owner.registerHook(ctx, registration); err != nil {
			return err
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.registrations = append(owner.registrations, registration)
	return nil
}

func (owner *fakePhaseTwoOwnership) PublishAssignments(
	context.Context,
	[]execution.QueryGroupIdentity,
	time.Time,
) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := owner.consumeFailureLocked("publish"); err != nil {
		return err
	}
	owner.published++
	return nil
}

func (owner *fakePhaseTwoOwnership) AssignedQueryGroups(
	context.Context,
	[]execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := owner.consumeFailureLocked("assigned"); err != nil {
		return nil, err
	}
	return append([]execution.QueryGroupIdentity(nil), owner.assigned...), nil
}

func (owner *fakePhaseTwoOwnership) MaintainControlLeader(ctx context.Context, _, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func (owner *fakePhaseTwoOwnership) OpenQueryGroup(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	_ time.Time,
	_ time.Duration,
) (phaseTwoQueryGroupRuntime, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if err := owner.openErrors[queryGroup]; err != nil {
		return nil, err
	}
	if err := owner.consumeFailureLocked("open"); err != nil {
		return nil, err
	}
	if owner.runners != nil {
		return owner.runners[queryGroup], nil
	}
	return owner.runner, nil
}

func (owner *fakePhaseTwoOwnership) Close() error {
	owner.closeCalls++
	return nil
}

func (owner *fakePhaseTwoOwnership) registrationStates() []ownership.AssignmentReadiness {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	states := make([]ownership.AssignmentReadiness, len(owner.registrations))
	for index, registration := range owner.registrations {
		states[index] = registration.AssignmentReadiness
	}
	return states
}

func (owner *fakePhaseTwoOwnership) controlLeaderCount() int {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.controlLeader
}

func (owner *fakePhaseTwoOwnership) publishAssignmentCount() int {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return owner.published
}

func (owner *fakePhaseTwoOwnership) setAssigned(assigned []execution.QueryGroupIdentity) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.assigned = append([]execution.QueryGroupIdentity(nil), assigned...)
}

type fakePhaseTwoQueryGroup struct {
	mu            sync.Mutex
	runCalls      int
	releaseCalls  int
	leaseErr      error
	leaseRelease  chan struct{}
	leaseStarted  chan struct{}
	leaseFinished chan struct{}
	runStarted    chan struct{}
	runRelease    chan struct{}
	attempted     bool
	runErr        error
	runResult     execution.SlotExecutionResult
	onRun         func()
}

type callbackPhaseTwoQueryGroup struct {
	run             func(context.Context) (execution.SlotExecutionResult, bool, error)
	nextReadyAt     func() time.Time
	operation       execution.Operation
	beforeAdmission func(execution.Operation)
}

func (runner *callbackPhaseTwoQueryGroup) RunOne(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
	return runner.run(ctx)
}

func (runner *callbackPhaseTwoQueryGroup) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	operation := runner.operation
	if operation == "" {
		operation = execution.OperationNormal
	}
	if runner.beforeAdmission != nil {
		runner.beforeAdmission(operation)
	}
	release, admitted := admission(operation)
	if !admitted {
		return execution.SlotExecutionResult{}, false, true, nil
	}
	defer release()
	result, attempted, err := runner.run(ctx)
	return result, attempted, false, err
}

func (runner *callbackPhaseTwoQueryGroup) NextReadyAt() time.Time {
	if runner.nextReadyAt == nil {
		return time.Time{}
	}
	return runner.nextReadyAt()
}

func (*callbackPhaseTwoQueryGroup) MaintainLease(ctx context.Context, _, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*callbackPhaseTwoQueryGroup) Release(context.Context) error { return nil }

func newFakePhaseTwoQueryGroup() *fakePhaseTwoQueryGroup {
	return &fakePhaseTwoQueryGroup{leaseStarted: make(chan struct{}), leaseFinished: make(chan struct{})}
}

func (runner *fakePhaseTwoQueryGroup) RunOne(context.Context) (execution.SlotExecutionResult, bool, error) {
	runner.mu.Lock()
	runner.runCalls++
	runner.mu.Unlock()
	if runner.onRun != nil {
		runner.onRun()
	}
	if runner.runStarted != nil {
		close(runner.runStarted)
	}
	if runner.runRelease != nil {
		<-runner.runRelease
	}
	return runner.runResult, runner.attempted, runner.runErr
}

func (runner *fakePhaseTwoQueryGroup) RunOneAdmitted(
	ctx context.Context,
	admission scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	release, admitted := admission(execution.OperationNormal)
	if !admitted {
		return execution.SlotExecutionResult{}, false, true, nil
	}
	defer release()
	result, attempted, err := runner.RunOne(ctx)
	return result, attempted, false, err
}

func (*fakePhaseTwoQueryGroup) NextReadyAt() time.Time { return time.Time{} }

func (runner *fakePhaseTwoQueryGroup) MaintainLease(ctx context.Context, _, _ time.Duration) error {
	close(runner.leaseStarted)
	if runner.leaseRelease != nil {
		select {
		case <-runner.leaseRelease:
		case <-ctx.Done():
			close(runner.leaseFinished)
			return ctx.Err()
		}
	}
	if runner.leaseErr != nil {
		close(runner.leaseFinished)
		return runner.leaseErr
	}
	<-ctx.Done()
	close(runner.leaseFinished)
	return ctx.Err()
}

func (runner *fakePhaseTwoQueryGroup) Release(context.Context) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.releaseCalls++
	return nil
}

func (runner *fakePhaseTwoQueryGroup) releaseCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.releaseCalls
}

func (runner *fakePhaseTwoQueryGroup) runCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.runCalls
}

func waitForRunnerCalls(t *testing.T, runner *fakePhaseTwoQueryGroup, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runner.runCount() >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d runner calls; got %d", count, runner.runCount())
}

func waitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func equalReadiness(left, right []ownership.AssignmentReadiness) bool {
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

func waitForRegistration(t *testing.T, owner *fakePhaseTwoOwnership, readiness ownership.AssignmentReadiness) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		states := owner.registrationStates()
		if len(states) > 0 && states[len(states)-1] == readiness {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for worker registration %s", readiness)
}

func waitForHealthState(t *testing.T, health *phaseTwoApplicationHealth, state observability.HealthState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if health.HealthSnapshot().State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for health state %s", state)
}

func containsStage(stages []observability.Stage, wanted observability.Stage) bool {
	for _, stage := range stages {
		if stage == wanted {
			return true
		}
	}
	return false
}

func schedulerFailureObservations(observations []observability.Observation) []observability.Observation {
	var failures []observability.Observation
	for _, observation := range observations {
		if observation.Component == observability.ComponentScheduler &&
			observation.Stage == observability.StageScheduleDue &&
			observation.Result == observability.ResultFailed {
			failures = append(failures, observation)
		}
	}
	return failures
}

// withCompatibilityOutput supplies the coordinates every deployment needs for
// the built-in Python-compatible protocol: a strategy without a frozen revision
// selects that protocol, and its snapshot reaches the service Redis before the
// event is published. Tests point the service Redis at the instance they
// already run, because the snapshot destination is never what is under test.
func withCompatibilityOutput(cfg *config.Config, address string) {
	const topic = "alarmd_0bkmonitor_backend_event"
	present := false
	for _, allowed := range cfg.Kafka.AllowedOutputTopics {
		if allowed == topic {
			present = true
		}
	}
	if !present {
		cfg.Kafka.AllowedOutputTopics = append(cfg.Kafka.AllowedOutputTopics, topic)
	}
	cfg.Kafka.LegacyAdapter.Topic = topic
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "alarmd-compatibility-test"
	connection := cfg.Redis.Connection()
	connection.Address = address
	cfg.Kafka.LegacyAdapter.ServiceRedis = connection
}

func validGoAccessRuntimeConfig() config.Config {
	cfg := config.Default()
	accessBKData := false
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "alarmd-trigger-event"
	cfg.Kafka.AllowedOutputTopics = []string{"alarmd-trigger-event", "alarmd_0bkmonitor_backend_event"}
	// Every deployment carries the compatibility output: a strategy without a
	// frozen revision selects that protocol, and its snapshot is written before
	// the event is published. Tests that open a bundle point the service Redis
	// at the instance they already run, through withCompatibilityOutput.
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "alarmd-compatibility-test"
	cfg.Kafka.LegacyAdapter.ServiceRedis = config.RedisConnectionConfig{
		Mode: config.RedisModeStandalone, Address: "127.0.0.1:6379",
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
	}
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Redis.Address = "127.0.0.1:6379"
	cfg.Redis.StatePrefix = "alarmd-phase-two"
	cfg.PhaseTwo.Worker.ID = "alarmd-worker-0"
	cfg.PhaseTwo.Control.StrategyCachePrefix = "alarm-config"
	cfg.PhaseTwo.Control.ProviderRoute = "unify-query-primary"
	cfg.PhaseTwo.Control.Timezone = "Asia/Shanghai"
	cfg.PhaseTwo.Control.LegacyQueryRuntime.AccessBKData = &accessBKData
	cfg.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables = []string{}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter = config.PhaseTwoRuntimeFilterConfig{FieldName: "device_type", Values: []string{}}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemNetworkFilter = config.PhaseTwoRuntimeFilterConfig{FieldName: "device_name", Values: []string{}}
	cfg.PhaseTwo.Access.UQEndpoint = "http://unify-query.service"
	cfg.PhaseTwo.Access.QuerySource = "alarmd"
	return cfg
}

func TestPhaseTwoWorkerBundleRegistrationRenewalSurvivesTransientStoreFailure(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(100 * time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Millisecond)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	transient := errors.New("ownership store unreachable")
	var started, failed atomic.Bool
	owner.registerHook = func(_ context.Context, registration ownership.WorkerRegistration) error {
		if registration.AssignmentReadiness == ownership.WorkerReady && started.Load() && failed.CompareAndSwap(false, true) {
			return transient
		}
		return nil
	}
	health := newPhaseTwoApplicationHealth()
	bundle, observations := mustObservedPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	readyBefore := readyRegistrationCount(owner)
	started.Store(true)
	waitPhaseTwoCondition(t, 3*time.Second, "READY renewals after the transient failure", func() bool {
		return failed.Load() && readyRegistrationCount(owner) >= readyBefore+2
	})
	waitPhaseTwoCondition(t, 3*time.Second, "registration renewal failure and resumption observations", func() bool {
		return hasRegistrationRenewalObservation(observations(), observability.ResultFailed, phaseTwoControlDependencyReason) &&
			hasRegistrationRenewalObservation(observations(), observability.ResultResumed, observability.ReasonNone)
	})
	if snapshot := health.HealthSnapshot(); snapshot.State == observability.HealthNotReady {
		t.Fatalf("health after transient registration failure = %+v, want the Worker kept ready or degraded", snapshot)
	}
	for _, observation := range observations() {
		if observation.Component == observability.ComponentOwnership && observation.Stage == observability.StageLeaseRenewed &&
			(observation.Trace.QueryGroupKey != "" || observation.Trace.OwnerID != "") {
			t.Fatalf("registration renewal observation carries identity: %+v", observation)
		}
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestPhaseTwoWorkerBundleRegistrationRenewalCutsHungStoreCall(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Second)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Millisecond)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	var started, hung, deadlineMissing atomic.Bool
	hungReleased := make(chan struct{})
	owner.registerHook = func(ctx context.Context, registration ownership.WorkerRegistration) error {
		if registration.AssignmentReadiness == ownership.WorkerReady && started.Load() && hung.CompareAndSwap(false, true) {
			if _, ok := ctx.Deadline(); !ok {
				deadlineMissing.Store(true)
			}
			<-ctx.Done()
			close(hungReleased)
			return ctx.Err()
		}
		return nil
	}
	health := newPhaseTwoApplicationHealth()
	bundle, observations := mustObservedPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	readyBefore := readyRegistrationCount(owner)
	started.Store(true)
	hungAt := time.Now()
	select {
	case <-hungReleased:
	case <-time.After(5 * time.Second):
		t.Fatal("the hung registration call was not cut by the per-attempt timeout")
	}
	if cut := time.Since(hungAt); cut > 3*time.Second {
		t.Fatalf("hung registration call was cut after %s, want about the one second minimum attempt timeout", cut)
	}
	if deadlineMissing.Load() {
		t.Fatal("registration renewal attempt ran without a per-attempt deadline")
	}
	waitPhaseTwoCondition(t, 3*time.Second, "READY renewals after the hung call", func() bool {
		return readyRegistrationCount(owner) >= readyBefore+2
	})
	if !hasRegistrationRenewalObservation(observations(), observability.ResultFailed, phaseTwoControlDependencyReason) {
		t.Fatalf("observations = %+v, want a retryable registration renewal failure", observations())
	}
	if snapshot := health.HealthSnapshot(); snapshot.State == observability.HealthNotReady {
		t.Fatalf("health after hung registration call = %+v, want the Worker kept ready or degraded", snapshot)
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestPhaseTwoWorkerBundleRegistrationRenewalStopsOnInvariantError(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(100 * time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Millisecond)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	var started atomic.Bool
	owner.registerHook = func(_ context.Context, registration ownership.WorkerRegistration) error {
		if registration.AssignmentReadiness == ownership.WorkerReady && started.Load() {
			return newPhaseTwoInvariantError("phase-two registration invariant violated")
		}
		return nil
	}
	health := newPhaseTwoApplicationHealth()
	bundle, observations := mustObservedPhaseTwoWorkerBundle(t, cfg, health, control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	started.Store(true)
	waitForHealthState(t, health, observability.HealthNotReady)
	snapshot := health.HealthSnapshot()
	if len(snapshot.Reasons) != 1 || snapshot.Reasons[0] != observability.ReasonInternalUnknown {
		t.Fatalf("health reasons after invariant registration error = %+v, want internal_unknown", snapshot)
	}
	// Health flips before the observation is recorded, so waiting on health and
	// then reading the list races the emitter. Wait for the thing this test
	// actually asserts.
	unsafeObserved := false
	deadline := time.Now().Add(2 * time.Second)
	for !unsafeObserved && time.Now().Before(deadline) {
		for _, observation := range observations() {
			if observation.Component == observability.ComponentOwnership && observation.Stage == observability.StageAssignmentLost &&
				observation.Result == observability.ResultFailed && isPhaseTwoInvariantError(observation.Err) {
				unsafeObserved = true
			}
			if observation.Component == observability.ComponentOwnership && observation.Stage == observability.StageLeaseRenewed &&
				observation.Result == observability.ResultFailed {
				t.Fatalf("invariant registration error was reported as retryable: %+v", observation)
			}
		}
		if !unsafeObserved {
			time.Sleep(time.Millisecond)
		}
	}
	if !unsafeObserved {
		t.Fatalf("observations = %+v, want ownership marked unsafe with the invariant error", observations())
	}
	readyCount := readyRegistrationCount(owner)
	time.Sleep(20 * time.Millisecond)
	if again := readyRegistrationCount(owner); again != readyCount {
		t.Fatalf("READY registrations kept flowing after the invariant error: %d -> %d", readyCount, again)
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	waitForRegistration(t, owner, ownership.WorkerDraining)
}

func mustObservedPhaseTwoWorkerBundle(
	t *testing.T,
	cfg config.Config,
	health *phaseTwoApplicationHealth,
	control phaseTwoControlRuntime,
	owner phaseTwoOwnershipRuntime,
) (*phaseTwoWorkerBundle, func() []observability.Observation) {
	t.Helper()
	var mu sync.Mutex
	var observations []observability.Observation
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: owner,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
		}),
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("newPhaseTwoWorkerBundle() error = %v", err)
	}
	return bundle, func() []observability.Observation {
		mu.Lock()
		defer mu.Unlock()
		return append([]observability.Observation(nil), observations...)
	}
}

func readyRegistrationCount(owner *fakePhaseTwoOwnership) int {
	count := 0
	for _, readiness := range owner.registrationStates() {
		if readiness == ownership.WorkerReady {
			count++
		}
	}
	return count
}

func hasRegistrationRenewalObservation(
	observations []observability.Observation,
	result observability.Result,
	reason observability.ReasonCode,
) bool {
	for _, observation := range observations {
		if observation.Component == observability.ComponentOwnership && observation.Stage == observability.StageLeaseRenewed &&
			observation.Result == result && observation.ReasonCode == reason {
			return true
		}
	}
	return false
}

func waitPhaseTwoCondition(t *testing.T, timeout time.Duration, name string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", name)
}
