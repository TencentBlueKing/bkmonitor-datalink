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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

var errPhaseTwoWorkerBundleNotAssembled = errors.New(
	"phase-two Go Access worker bundle is not assembled",
)

var errPhaseTwoWorkerDraining = errors.New("phase-two Go Access worker is draining")

var errPhaseTwoWorkerStopped = errors.New("phase-two Go Access worker stopped before application shutdown")

type phaseTwoApplicationDependencies struct {
	run        func(context.Context, config.Config, *metric.Recorder, *observability.Logger) error
	openBundle func(
		context.Context,
		config.Config,
		*metric.Recorder,
		*observability.Logger,
		*phaseTwoApplicationHealth,
	) (*phaseTwoWorkerBundle, error)
	newHTTP func(*metric.Recorder, observability.HealthSource) (httpRuntime, error)
}

type runtimeModeDependencies struct {
	phaseOne applicationDependencies
	phaseTwo phaseTwoApplicationDependencies
}

func defaultPhaseTwoApplicationDependencies() phaseTwoApplicationDependencies {
	return phaseTwoApplicationDependencies{
		run: runPhaseTwoApplication, openBundle: openProductionPhaseTwoBundle,
		newHTTP: func(recorder *metric.Recorder, source observability.HealthSource) (httpRuntime, error) {
			return defaultApplicationDependencies(nil).newHTTP(recorder, source)
		},
	}
}

// phaseTwoApplication is the construction and health boundary around the one
// production Worker Bundle.
type phaseTwoApplication struct {
	health *phaseTwoApplicationHealth
}

func (a *phaseTwoApplication) HealthSnapshot() observability.HealthSnapshot {
	if a == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	return a.health.HealthSnapshot()
}

func newPhaseTwoApplication(cfg config.Config) (*phaseTwoApplication, error) {
	if cfg.Input.Mode != config.InputModeGoAccess {
		return nil, fmt.Errorf("phase-two application requires input mode %q", config.InputModeGoAccess)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate phase-two application configuration: %w", err)
	}
	return &phaseTwoApplication{health: newPhaseTwoApplicationHealth()}, nil
}

func runPhaseTwoApplication(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
) error {
	return runPhaseTwoApplicationWithDependencies(ctx, cfg, recorder, logger, defaultPhaseTwoApplicationDependencies())
}

func runPhaseTwoApplicationWithDependencies(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	dependencies phaseTwoApplicationDependencies,
) error {
	if ctx == nil || recorder == nil {
		return errors.New("phase-two application requires context and metric recorder")
	}
	if dependencies.openBundle == nil {
		return errPhaseTwoWorkerBundleNotAssembled
	}
	if dependencies.newHTTP == nil {
		return errors.New("phase-two application requires HTTP service factory")
	}
	application, err := newPhaseTwoApplication(cfg)
	if err != nil {
		return err
	}
	if logger == nil {
		logger = observability.Discard(observability.ComponentRuntime)
	}
	logger.Info(observability.StageStartup, observability.ResultStarted, 0, 0)
	server, err := dependencies.newHTTP(recorder, application)
	if err != nil {
		return err
	}
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	httpContext, cancelHTTP := context.WithCancel(runtimeContext)
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Run(httpContext, cfg.HTTP.Listen, cfg.ShutdownTimeout.Duration()) }()

	bundle, err := dependencies.openBundle(runtimeContext, cfg, recorder, logger, application.health)
	if err != nil {
		cancelRuntime()
		cancelHTTP()
		httpErr := waitRuntimeComponent(httpDone, time.Now().Add(cfg.ShutdownTimeout.Duration()))
		return errors.Join(err, normalizeRuntimeShutdownError(httpErr, false))
	}
	bundleDone := make(chan error, 1)
	go func() { bundleDone <- bundle.Run(runtimeContext) }()

	var runErr, httpErr error
	bundleFinished := false
	httpFinished := false
	bundleStoppedEarly := false
	httpStoppedEarly := false
	select {
	case <-ctx.Done():
	case runErr = <-bundleDone:
		bundleFinished = true
		bundleStoppedEarly = ctx.Err() == nil
		if runErr == nil && bundleStoppedEarly {
			runErr = errPhaseTwoWorkerStopped
		}
		if bundleStoppedEarly {
			markPhaseTwoFatal(runtimeContext, bundle, application.health, runErr)
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpStoppedEarly = ctx.Err() == nil
		if httpErr == nil && httpStoppedEarly {
			httpErr = errHTTPServiceStopped
		}
		if httpStoppedEarly {
			markPhaseTwoFatal(runtimeContext, bundle, application.health, httpErr)
		}
	}
	cancelRuntime()
	cancelHTTP()
	deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
	if !bundleFinished {
		runErr = waitRuntimeComponent(bundleDone, deadline)
	}
	if !httpFinished {
		httpErr = waitRuntimeComponent(httpDone, deadline)
	}
	result := errors.Join(
		normalizeRuntimeShutdownError(runErr, bundleStoppedEarly),
		normalizeRuntimeShutdownError(httpErr, httpStoppedEarly),
	)
	if result == nil {
		logger.Info(observability.StageShutdown, observability.ResultSuccess, 0, 0)
	} else {
		logger.Error(observability.StageShutdown, observability.ResultFailed, 0, 0)
	}
	return result
}

type phaseTwoControlRuntime interface {
	InitialRefresh(context.Context) ([]execution.QueryGroupIdentity, error)
	Refresh(context.Context) ([]execution.QueryGroupIdentity, error)
	Close() error
}

type phaseTwoOwnershipRuntime interface {
	RegisterWorker(context.Context, ownership.WorkerRegistration) error
	AcquireControlLeader(context.Context, time.Time, time.Duration) error
	Reconcile(context.Context, []execution.QueryGroupIdentity, time.Time) ([]execution.QueryGroupIdentity, error)
	MaintainControlLeader(context.Context, time.Duration, time.Duration) error
	OpenQueryGroup(
		context.Context,
		execution.QueryGroupIdentity,
		time.Time,
		time.Duration,
	) (phaseTwoQueryGroupRuntime, error)
	Close() error
}

type phaseTwoQueryGroupRuntime interface {
	RunOne(context.Context) (execution.SlotExecutionResult, bool, error)
	MaintainLease(context.Context, time.Duration, time.Duration) error
	Release(context.Context) error
}

type phaseTwoWorkerBundleDependencies struct {
	Config         config.Config
	Health         *phaseTwoApplicationHealth
	Control        phaseTwoControlRuntime
	Ownership      phaseTwoOwnershipRuntime
	Observer       observability.Observer
	CloseResources func(context.Context) error
	Now            func() time.Time
}

type phaseTwoWorkerBundle struct {
	dependencies phaseTwoWorkerBundleDependencies

	registrationMu sync.Mutex
	mu             sync.RWMutex
	queryGroups    []execution.QueryGroupIdentity
	runners        map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime
	leaseFailures  map[execution.QueryGroupIdentity]error
	started        bool
	draining       bool
	closed         bool
	maintenanceCtx context.Context
	cancelMaintain context.CancelFunc
	leaseCtx       context.Context
	cancelLeases   context.CancelFunc
	maintenanceWG  sync.WaitGroup
	inflightWG     sync.WaitGroup
	shutdownOnce   sync.Once
	shutdownErr    error
}

func newPhaseTwoWorkerBundle(dependencies phaseTwoWorkerBundleDependencies) (*phaseTwoWorkerBundle, error) {
	if dependencies.Health == nil || dependencies.Control == nil || dependencies.Ownership == nil ||
		dependencies.Observer == nil || dependencies.Now == nil {
		return nil, errors.New("phase-two worker requires complete lifecycle dependencies")
	}
	if err := dependencies.Config.Validate(); err != nil {
		return nil, err
	}
	return &phaseTwoWorkerBundle{dependencies: dependencies,
		runners:       make(map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime),
		leaseFailures: make(map[execution.QueryGroupIdentity]error)}, nil
}

func (bundle *phaseTwoWorkerBundle) Start(ctx context.Context) error {
	if bundle == nil || ctx == nil {
		return errors.New("phase-two worker requires an initialized bundle and context")
	}
	bundle.mu.Lock()
	if bundle.started {
		bundle.mu.Unlock()
		return errors.New("phase-two worker is already started")
	}
	bundle.started = true
	bundle.maintenanceCtx, bundle.cancelMaintain = context.WithCancel(context.Background())
	bundle.leaseCtx, bundle.cancelLeases = context.WithCancel(context.Background())
	bundle.mu.Unlock()
	if err := bundle.dependencies.Ownership.AcquireControlLeader(
		ctx, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
	); err != nil {
		return fmt.Errorf("phase-two acquire Control Leader: %w", err)
	}
	bundle.startControlMaintenance()
	bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageStartup), observability.ResultStarted, nil)
	bundle.observe(ctx, observability.ComponentRuntime, observability.StageConfigLoaded, observability.ResultSuccess, nil)

	queryGroups, err := bundle.dependencies.Control.InitialRefresh(ctx)
	if err != nil {
		return fmt.Errorf("phase-two initial control refresh: %w", err)
	}
	bundle.mu.Lock()
	bundle.queryGroups = append([]execution.QueryGroupIdentity(nil), queryGroups...)
	bundle.mu.Unlock()
	bundle.observe(ctx, observability.ComponentControlPlane, observability.StageSnapshotRefreshed, observability.ResultSuccess, nil)
	if err := bundle.register(ctx, ownership.WorkerStarting); err != nil {
		return err
	}
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		return err
	}
	assigned, err := bundle.dependencies.Ownership.Reconcile(ctx, queryGroups, bundle.dependencies.Now())
	if err != nil {
		return fmt.Errorf("phase-two reconcile Assignment: %w", err)
	}
	if err := bundle.openAssigned(ctx, assigned); err != nil {
		return err
	}
	bundle.startMaintenance()
	ready := len(queryGroups) > 0 && len(assigned) == len(queryGroups)
	bundle.dependencies.Health.Update(phaseTwoReadiness{
		State: observability.HealthReady, SnapshotReady: true, AssignmentReady: ready,
		RuntimeStateReady: true, OutputSinkReady: true,
	})
	bundle.observe(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, observability.ResultSuccess, nil)
	return nil
}

func (bundle *phaseTwoWorkerBundle) Run(ctx context.Context) error {
	if err := bundle.Start(ctx); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
		defer cancel()
		return errors.Join(err, bundle.Shutdown(shutdownCtx))
	}
	scheduleTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Scheduler.TickInterval.Duration())
	refreshTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.RefreshInterval.Duration())
	reconcileTicker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.ReconcileInterval.Duration())
	defer scheduleTicker.Stop()
	defer refreshTicker.Stop()
	defer reconcileTicker.Stop()

	var runErr error
	for runErr == nil {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case <-scheduleTicker.C:
			runErr = bundle.runScheduledOnce(ctx)
		case <-refreshTicker.C:
			runErr = bundle.refreshAndReconcile(ctx, true)
		case <-reconcileTicker.C:
			runErr = bundle.refreshAndReconcile(ctx, false)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	shutdownErr := bundle.Shutdown(shutdownCtx)
	if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
		runErr = nil
	}
	return errors.Join(runErr, shutdownErr)
}

func (bundle *phaseTwoWorkerBundle) runScheduledOnce(ctx context.Context) error {
	bundle.mu.RLock()
	if bundle.draining || bundle.closed {
		bundle.mu.RUnlock()
		return errPhaseTwoWorkerDraining
	}
	for _, err := range bundle.leaseFailures {
		if err != nil {
			bundle.mu.RUnlock()
			return err
		}
	}
	runners := make([]phaseTwoQueryGroupRuntime, 0, len(bundle.runners))
	for _, runner := range bundle.runners {
		runners = append(runners, runner)
	}
	bundle.inflightWG.Add(len(runners))
	bundle.mu.RUnlock()

	for index, runner := range runners {
		_, _, err := runner.RunOne(ctx)
		bundle.inflightWG.Done()
		if err != nil {
			if errors.Is(err, ownership.ErrStaleFence) || errors.Is(err, ownership.ErrNotDesired) {
				bundle.markOwnershipUnsafe(err)
			}
			for remaining := index + 1; remaining < len(runners); remaining++ {
				bundle.inflightWG.Done()
			}
			return err
		}
	}
	return nil
}

func (bundle *phaseTwoWorkerBundle) Shutdown(ctx context.Context) error {
	if bundle == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("phase-two worker shutdown context is required")
	}
	bundle.shutdownOnce.Do(func() {
		bundle.mu.Lock()
		bundle.draining = true
		cancelMaintain := bundle.cancelMaintain
		bundle.mu.Unlock()
		bundle.dependencies.Health.Update(phaseTwoReadiness{State: observability.HealthDraining})
		var result []error
		result = append(result, bundle.register(ctx, ownership.WorkerDraining))
		if cancelMaintain != nil {
			cancelMaintain()
		}
		result = append(result, waitPhaseTwoGroup(ctx, &bundle.inflightWG))
		bundle.mu.Lock()
		if bundle.cancelLeases != nil {
			bundle.cancelLeases()
		}
		runners := make([]phaseTwoQueryGroupRuntime, 0, len(bundle.runners))
		for _, runner := range bundle.runners {
			runners = append(runners, runner)
		}
		bundle.mu.Unlock()
		result = append(result, waitPhaseTwoGroup(ctx, &bundle.maintenanceWG))
		for _, runner := range runners {
			result = append(result, runner.Release(ctx))
		}
		if bundle.dependencies.CloseResources != nil {
			result = append(result, bundle.dependencies.CloseResources(ctx))
		}
		result = append(result, bundle.dependencies.Control.Close(), bundle.dependencies.Ownership.Close())
		bundle.mu.Lock()
		bundle.closed = true
		bundle.mu.Unlock()
		bundle.shutdownErr = errors.Join(result...)
		shutdownResult := observability.Result(observability.ResultSuccess)
		if bundle.shutdownErr != nil {
			shutdownResult = observability.ResultFailed
		}
		bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageShutdown), shutdownResult, bundle.shutdownErr)
	})
	return bundle.shutdownErr
}

func markPhaseTwoFatal(
	ctx context.Context,
	bundle *phaseTwoWorkerBundle,
	health *phaseTwoApplicationHealth,
	err error,
) {
	health.Update(phaseTwoReadiness{
		State: observability.HealthFatal, Reasons: []observability.ReasonCode{observability.ReasonInternalUnknown},
	})
	bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageFatal), observability.ResultFailed, err)
}

func (bundle *phaseTwoWorkerBundle) register(ctx context.Context, readiness ownership.AssignmentReadiness) error {
	bundle.registrationMu.Lock()
	defer bundle.registrationMu.Unlock()
	if readiness == ownership.WorkerReady {
		bundle.mu.RLock()
		draining := bundle.draining
		bundle.mu.RUnlock()
		if draining {
			return nil
		}
	}
	cfg := bundle.dependencies.Config.PhaseTwo
	capabilitiesDigest, err := phaseTwoCapabilitiesDigest(bundle.dependencies.Config)
	if err != nil {
		return fmt.Errorf("phase-two derive worker capabilities: %w", err)
	}
	registration := ownership.WorkerRegistration{
		WorkerID: cfg.Worker.ID, AssignmentReadiness: readiness, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile:  cfg.Worker.DeploymentProfile,
		CapabilitiesDigest: capabilitiesDigest,
		ExpiresAt:          bundle.dependencies.Now().Add(cfg.Worker.RegistrationTTL.Duration()),
	}
	if err := bundle.dependencies.Ownership.RegisterWorker(ctx, registration); err != nil {
		return fmt.Errorf("phase-two register worker as %s: %w", readiness, err)
	}
	return nil
}

func (bundle *phaseTwoWorkerBundle) openAssigned(ctx context.Context, assigned []execution.QueryGroupIdentity) error {
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(assigned))
	for _, queryGroup := range assigned {
		if queryGroup == "" {
			return errors.New("phase-two Assignment contains empty Query Group")
		}
		if _, duplicate := seen[queryGroup]; duplicate {
			return errors.New("phase-two Assignment contains duplicate Query Group")
		}
		seen[queryGroup] = struct{}{}
		runner, err := bundle.dependencies.Ownership.OpenQueryGroup(
			ctx, queryGroup, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.LeaseTTL.Duration(),
		)
		if err != nil {
			return fmt.Errorf("phase-two open Query Group %s: %w", queryGroup, err)
		}
		bundle.runners[queryGroup] = runner
	}
	return nil
}

func (bundle *phaseTwoWorkerBundle) startMaintenance() {
	cfg := bundle.dependencies.Config.PhaseTwo
	bundle.maintenanceWG.Add(1)
	go bundle.maintainRegistration()
	for queryGroup, runner := range bundle.runners {
		queryGroup, runner := queryGroup, runner
		bundle.maintenanceWG.Add(1)
		go func() {
			defer bundle.maintenanceWG.Done()
			err := runner.MaintainLease(
				bundle.leaseCtx, cfg.Ownership.LeaseRenewInterval.Duration(), cfg.Ownership.LeaseTTL.Duration(),
			)
			if err != nil && !errors.Is(err, context.Canceled) {
				bundle.mu.Lock()
				bundle.leaseFailures[queryGroup] = err
				bundle.mu.Unlock()
				bundle.markOwnershipUnsafe(err)
			}
		}()
	}
}

func (bundle *phaseTwoWorkerBundle) startControlMaintenance() {
	cfg := bundle.dependencies.Config.PhaseTwo
	bundle.maintenanceWG.Add(1)
	go func() {
		defer bundle.maintenanceWG.Done()
		err := bundle.dependencies.Ownership.MaintainControlLeader(
			bundle.maintenanceCtx, cfg.Ownership.ControlLeaderRenewInterval.Duration(), cfg.Ownership.ControlLeaderTTL.Duration(),
		)
		if err != nil && !errors.Is(err, context.Canceled) {
			bundle.markOwnershipUnsafe(err)
		}
	}()
}

func (bundle *phaseTwoWorkerBundle) maintainRegistration() {
	defer bundle.maintenanceWG.Done()
	ticker := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Worker.RegistrationRenewInterval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-bundle.maintenanceCtx.Done():
			return
		case <-ticker.C:
			if err := bundle.register(bundle.maintenanceCtx, ownership.WorkerReady); err != nil {
				bundle.markOwnershipUnsafe(err)
				return
			}
		}
	}
}

func (bundle *phaseTwoWorkerBundle) refreshAndReconcile(ctx context.Context, refresh bool) error {
	var queryGroups []execution.QueryGroupIdentity
	var err error
	if refresh {
		queryGroups, err = bundle.dependencies.Control.Refresh(ctx)
		if err == nil {
			bundle.mu.Lock()
			bundle.queryGroups = append(bundle.queryGroups[:0], queryGroups...)
			bundle.mu.Unlock()
			bundle.observe(ctx, observability.ComponentControlPlane, observability.StageSnapshotRefreshed, observability.ResultSuccess, nil)
		}
	} else {
		bundle.mu.RLock()
		queryGroups = append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
		bundle.mu.RUnlock()
	}
	if err != nil {
		bundle.observe(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable, observability.ResultFailed, err)
		return err
	}
	assigned, err := bundle.dependencies.Ownership.Reconcile(ctx, queryGroups, bundle.dependencies.Now())
	if err != nil {
		return err
	}
	bundle.mu.RLock()
	if len(assigned) != len(bundle.runners) {
		bundle.mu.RUnlock()
		return errors.New("phase-two G1 Assignment changed after startup")
	}
	seen := make(map[execution.QueryGroupIdentity]struct{}, len(assigned))
	for _, queryGroup := range assigned {
		if _, duplicate := seen[queryGroup]; duplicate {
			bundle.mu.RUnlock()
			return errors.New("phase-two G1 Assignment changed after startup")
		}
		seen[queryGroup] = struct{}{}
		if _, known := bundle.runners[queryGroup]; !known {
			bundle.mu.RUnlock()
			return errors.New("phase-two G1 Assignment changed after startup")
		}
	}
	bundle.mu.RUnlock()
	return nil
}

func (bundle *phaseTwoWorkerBundle) markOwnershipUnsafe(err error) {
	bundle.dependencies.Health.Update(phaseTwoReadiness{
		State: observability.HealthNotReady, Reasons: []observability.ReasonCode{observability.ReasonInternalUnknown},
		SnapshotReady: true, RuntimeStateReady: true, OutputSinkReady: true,
	})
	bundle.observe(context.Background(), observability.ComponentOwnership, observability.StageAssignmentLost, observability.ResultFailed, err)
}

func (bundle *phaseTwoWorkerBundle) observe(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	result observability.Result,
	err error,
) {
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: component, Stage: stage, Result: result,
		Direction: observability.DirectionInternal, Err: err,
	})
}

func phaseTwoCapabilitiesDigest(cfg config.Config) (string, error) {
	return contract.DeriveCanonicalDigestV2("alarmd-phase-two-worker-capabilities-v1", struct {
		SchemaVersion     string                           `json:"schema_version"`
		AlgorithmRegistry string                           `json:"algorithm_registry"`
		Limits            config.LimitsConfig              `json:"limits"`
		Coordinator       config.PhaseTwoCoordinatorConfig `json:"coordinator"`
	}{
		SchemaVersion: schemaVersion, AlgorithmRegistry: strategy.NewDefaultAlgorithmCompilerRegistry().CapabilityDigest(),
		Limits: cfg.Limits, Coordinator: cfg.PhaseTwo.Coordinator,
	})
}

func waitPhaseTwoGroup(ctx context.Context, group *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type phaseTwoReadiness struct {
	State             observability.HealthState
	Reasons           []observability.ReasonCode
	SnapshotReady     bool
	AssignmentReady   bool
	RuntimeStateReady bool
	OutputSinkReady   bool
	ResourceState     observability.ResourceState
}

type phaseTwoApplicationHealth struct {
	tracker *observability.HealthTracker
}

func newPhaseTwoApplicationHealth() *phaseTwoApplicationHealth {
	health := &phaseTwoApplicationHealth{tracker: observability.NewHealthTracker(observability.HealthSnapshot{})}
	health.Update(phaseTwoReadiness{State: observability.HealthStarting})
	return health
}

func (h *phaseTwoApplicationHealth) Update(readiness phaseTwoReadiness) {
	if h == nil || h.tracker == nil {
		return
	}
	h.tracker.Update(observability.HealthSnapshot{
		State: readiness.State, Reasons: append([]observability.ReasonCode(nil), readiness.Reasons...),
		ConfigLoaded: true, SchemaReady: true, PhaseTwo: true, SnapshotReady: readiness.SnapshotReady,
		AssignmentReady: readiness.AssignmentReady, RuntimeStateReady: readiness.RuntimeStateReady,
		OutputSinkReady: readiness.OutputSinkReady, ResourceState: readiness.ResourceState,
	})
}

func (h *phaseTwoApplicationHealth) HealthSnapshot() observability.HealthSnapshot {
	if h == nil || h.tracker == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{PhaseTwo: true})
	}
	snapshot := h.tracker.HealthSnapshot()
	// Kafka input claims and lag belong only to phase-one compatibility. Keep
	// them structurally absent from the phase-two readiness source.
	snapshot.AssignedClaims = 0
	snapshot.ConsumerLagRecords = 0
	snapshot.ConsumerLagKnown = false
	return observability.NormalizeHealthSnapshot(snapshot)
}
