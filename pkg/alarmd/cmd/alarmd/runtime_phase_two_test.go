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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
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
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger, *phaseTwoApplicationHealth) (*phaseTwoWorkerBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
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
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
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
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
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
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
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

func TestPhaseTwoWorkerBundleDoesNotDuplicateAttemptedRunnerFailureObservation(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	failed := newFakePhaseTwoQueryGroup()
	failed.attempted = true
	failed.runErr = errors.New("executor already observed this failure")
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: failed}
	var observations []observability.Observation
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner, Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
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
		t.Fatalf("runScheduledOnce(attempted failure) error = %v", err)
	}
	if failures := schedulerFailureObservations(observations); len(failures) != 0 {
		t.Fatalf("attempted runner failure was observed twice: %+v", failures)
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
	beforeInitialRefresh func() error
	initialRefreshCalls  int
	refreshCalls         int
	loadActiveCalls      int
	closeCalls           int
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
	if control.refreshCalls < len(control.refreshResults) {
		result := clonePhaseTwoControlRefreshResult(control.refreshResults[control.refreshCalls])
		control.refreshCalls++
		return result, nil
	}
	control.refreshCalls++
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
	assigned       []execution.QueryGroupIdentity
	runner         phaseTwoQueryGroupRuntime
	runners        map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime
	openErrors     map[execution.QueryGroupIdentity]error
	follower       bool
	controlLeader  int
	published      int
	closeCalls     int
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

func (owner *fakePhaseTwoOwnership) RegisterWorker(_ context.Context, registration ownership.WorkerRegistration) error {
	if owner.beforeRegister != nil {
		owner.beforeRegister(registration)
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
	owner.published++
	return nil
}

func (owner *fakePhaseTwoOwnership) AssignedQueryGroups(
	context.Context,
	[]execution.QueryGroupIdentity,
) ([]execution.QueryGroupIdentity, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
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
}

func newFakePhaseTwoQueryGroup() *fakePhaseTwoQueryGroup {
	return &fakePhaseTwoQueryGroup{leaseStarted: make(chan struct{}), leaseFinished: make(chan struct{})}
}

func (runner *fakePhaseTwoQueryGroup) RunOne(context.Context) (execution.SlotExecutionResult, bool, error) {
	runner.mu.Lock()
	runner.runCalls++
	runner.mu.Unlock()
	if runner.runStarted != nil {
		close(runner.runStarted)
	}
	if runner.runRelease != nil {
		<-runner.runRelease
	}
	return execution.SlotExecutionResult{}, runner.attempted, runner.runErr
}

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

func validGoAccessRuntimeConfig() config.Config {
	cfg := config.Default()
	accessBKData := false
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "alarmd-trigger-event"
	cfg.Kafka.AllowedOutputTopics = []string{"alarmd-trigger-event"}
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Redis.Address = "127.0.0.1:6379"
	cfg.Redis.StatePrefix = "alarmd-phase-two"
	cfg.PhaseTwo.Worker.ID = "alarmd-worker-0"
	cfg.PhaseTwo.Worker.DeploymentProfile = "shadow"
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
