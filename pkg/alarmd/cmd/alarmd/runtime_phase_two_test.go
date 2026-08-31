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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
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

func TestPhaseTwoWorkerBundleLeaseFailureStopsBeforeAnotherSlot(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	health := newPhaseTwoApplicationHealth()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	runner.leaseErr = ownership.ErrStaleFence
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, health, control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitSignal(t, runner.leaseFinished, "stale lease failure")
	waitForHealthState(t, health, observability.HealthNotReady)
	if err := bundle.runScheduledOnce(context.Background()); !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("runScheduledOnce(stale lease) error = %v, want ErrStaleFence", err)
	}
	if runner.runCount() != 0 {
		t.Fatalf("stale lease started Slot side effects, runner calls = %d", runner.runCount())
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

func TestPhaseTwoWorkerBundleRejectsSameSizeAssignmentIdentityChange(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"}}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	control.queryGroups = []execution.QueryGroupIdentity{"query-group-2"}
	owner.assigned = []execution.QueryGroupIdentity{"query-group-2"}
	if err := bundle.refreshAndReconcile(context.Background(), true); err == nil {
		t.Fatal("refreshAndReconcile() accepted a same-size replacement Assignment")
	}
	if runner.runCount() != 0 {
		t.Fatalf("replacement Assignment admitted old runner, calls = %d", runner.runCount())
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

func TestPhaseTwoWorkerBundleObservesLifecycleWithoutBusinessIdentityLabels(t *testing.T) {
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
		if observation.Trace.QueryGroupKey != "" || observation.Trace.OwnerID != "" {
			t.Fatalf("lifecycle observation leaked business identity: %+v", observation)
		}
	}
	for _, want := range []observability.Stage{
		observability.Stage(observability.StageStartup), observability.StageConfigLoaded,
		observability.StageSnapshotRefreshed, observability.StageAssignmentAcquired,
		observability.StageScheduleDue, observability.StageSlotStarted,
		observability.StageSlotCompleted, observability.Stage(observability.StageShutdown),
	} {
		if !containsStage(stages, want) {
			t.Fatalf("lifecycle stages = %v, missing %s", stages, want)
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
	beforeInitialRefresh func() error
	initialRefreshCalls  int
	closeCalls           int
}

func (control *fakePhaseTwoControl) InitialRefresh(context.Context) ([]execution.QueryGroupIdentity, error) {
	control.initialRefreshCalls++
	if control.beforeInitialRefresh != nil {
		if err := control.beforeInitialRefresh(); err != nil {
			return nil, err
		}
	}
	return append([]execution.QueryGroupIdentity(nil), control.queryGroups...), nil
}

func (control *fakePhaseTwoControl) Refresh(context.Context) ([]execution.QueryGroupIdentity, error) {
	return append([]execution.QueryGroupIdentity(nil), control.queryGroups...), nil
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
	controlLeader  int
	closeCalls     int
}

func (owner *fakePhaseTwoOwnership) AcquireControlLeader(context.Context, time.Time, time.Duration) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.controlLeader++
	return nil
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

func (owner *fakePhaseTwoOwnership) Reconcile(
	context.Context,
	[]execution.QueryGroupIdentity,
	time.Time,
) ([]execution.QueryGroupIdentity, error) {
	return append([]execution.QueryGroupIdentity(nil), owner.assigned...), nil
}

func (owner *fakePhaseTwoOwnership) MaintainControlLeader(ctx context.Context, _, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func (owner *fakePhaseTwoOwnership) OpenQueryGroup(
	context.Context,
	execution.QueryGroupIdentity,
	time.Time,
	time.Duration,
) (phaseTwoQueryGroupRuntime, error) {
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

type fakePhaseTwoQueryGroup struct {
	mu            sync.Mutex
	runCalls      int
	releaseCalls  int
	leaseErr      error
	leaseStarted  chan struct{}
	leaseFinished chan struct{}
	runStarted    chan struct{}
	runRelease    chan struct{}
	attempted     bool
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
	return execution.SlotExecutionResult{}, runner.attempted, nil
}

func (runner *fakePhaseTwoQueryGroup) MaintainLease(ctx context.Context, _, _ time.Duration) error {
	close(runner.leaseStarted)
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
