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
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

var errPhaseTwoWorkerBundleNotAssembled = errors.New(
	"phase-two Go Access worker bundle is not assembled",
)

var errPhaseTwoWorkerDraining = errors.New("phase-two Go Access worker is draining")

var errPhaseTwoWorkerStopped = errors.New("phase-two Go Access worker stopped before application shutdown")

// phaseTwoControlDependencyReason is the fixed low-cardinality readiness
// reason reported while the control plane or the Ownership Store cannot be
// reached. Both are Redis-backed shared dependencies: the Worker stays alive,
// keeps already-owned Query Groups running and retries on the next tick.
var phaseTwoControlDependencyReason = observability.ReasonCode(contract.ReasonRedisUnavailable)

// phaseTwoInvariantError marks a programming or shared-runtime invariant
// violation inside the control loop. It is the only reconcile error class
// that may stop Run. Every other refresh, reconcile or Assignment error is a
// transient dependency failure by default: it degrades readiness with
// phaseTwoControlDependencyReason and is retried on the next tick.
type phaseTwoInvariantError struct{ err error }

func newPhaseTwoInvariantError(message string) error {
	return &phaseTwoInvariantError{err: errors.New(message)}
}

func (err *phaseTwoInvariantError) Error() string { return err.err.Error() }

func (err *phaseTwoInvariantError) Unwrap() error { return err.err }

func isPhaseTwoInvariantError(err error) bool {
	var invariant *phaseTwoInvariantError
	return errors.As(err, &invariant)
}

type phaseTwoApplicationDependencies struct {
	configureCPU func() (string, error)
	run          func(context.Context, config.Config, *metric.Recorder, *observability.Logger) error
	openBundle   func(
		context.Context,
		config.Config,
		*metric.Recorder,
		*observability.Logger,
		*phaseTwoApplicationHealth,
	) (*phaseTwoWorkerBundle, error)
	newHTTP func(*metric.Recorder, observability.HealthSource) (httpRuntime, error)
}

type runtimeModeDependencies struct {
	phaseOne                       applicationDependencies
	phaseTwo                       phaseTwoApplicationDependencies
	temporaryLegacyDrainingCleanup temporaryLegacyDrainingCleanupRunner
}

func defaultPhaseTwoApplicationDependencies() phaseTwoApplicationDependencies {
	return phaseTwoApplicationDependencies{
		configureCPU: configurePhaseTwoCPU,
		run:          runPhaseTwoApplication, openBundle: openProductionPhaseTwoBundle,
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
	cpuSource := "runtime_default"
	if dependencies.configureCPU != nil {
		cpuSource, err = dependencies.configureCPU()
		if err != nil {
			return err
		}
	}
	profile, err := phaseTwoRuntimeProfile(cfg, cpuSource, runtime.GOMAXPROCS(0))
	if err != nil {
		return fmt.Errorf("derive phase-two runtime profile: %w", err)
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

	if cfg.PhaseTwo.ShadowManifestPath != "" {
		runtimeContext = context.WithValue(runtimeContext, phaseTwoShadowProfileKey{}, profile)
	}
	bundle, err := dependencies.openBundle(runtimeContext, cfg, recorder, logger, application.health)
	if err != nil {
		cancelRuntime()
		cancelHTTP()
		httpErr := waitRuntimeComponent(httpDone, time.Now().Add(cfg.ShutdownTimeout.Duration()))
		return errors.Join(err, normalizeRuntimeShutdownError(httpErr, false))
	}
	bundleDone := make(chan error, 1)
	bundle.runtimeConfig = &profile
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

type phaseTwoControlRefreshStatus string

const (
	phaseTwoControlHealthy          phaseTwoControlRefreshStatus = "HEALTHY"
	phaseTwoControlDegradedLastGood phaseTwoControlRefreshStatus = "DEGRADED_LAST_GOOD"
)

type phaseTwoControlRefreshResult struct {
	QueryGroups           []execution.QueryGroupIdentity
	Status                phaseTwoControlRefreshStatus
	SourceRefreshObserved bool
	SourceKind            observability.SourceKind
	ReasonCode            observability.ReasonCode
	Cause                 error
}

type phaseTwoControlRuntime interface {
	InitialRefresh(context.Context) (phaseTwoControlRefreshResult, error)
	Refresh(context.Context) (phaseTwoControlRefreshResult, error)
	LoadActive(context.Context) (phaseTwoControlRefreshResult, error)
	Close() error
}

type phaseTwoOwnershipRuntime interface {
	RegisterWorker(context.Context, ownership.WorkerRegistration) error
	TryAcquireControlLeader(context.Context, time.Time, time.Duration) (bool, error)
	PublishAssignments(context.Context, []execution.QueryGroupIdentity, time.Time) error
	AssignedQueryGroups(context.Context, []execution.QueryGroupIdentity) ([]execution.QueryGroupIdentity, error)
	MaintainControlLeader(context.Context, time.Duration, time.Duration) error
	OpenQueryGroup(
		context.Context,
		execution.QueryGroupIdentity,
		time.Time,
		time.Duration,
	) (phaseTwoQueryGroupRuntime, error)
	Close() error
}

type phaseTwoQueryGroupLifecycle struct {
	runner phaseTwoQueryGroupRuntime
	cancel context.CancelFunc
	done   chan struct{}
}

type phaseTwoQueryGroupRuntime interface {
	RunOne(context.Context) (execution.SlotExecutionResult, bool, error)
	RunOneAdmitted(context.Context, scheduler.ExecutionAdmission) (execution.SlotExecutionResult, bool, bool, error)
	NextReadyAt() time.Time
	MaintainLease(context.Context, time.Duration, time.Duration) error
	Release(context.Context) error
}

type phaseTwoWorkerBundleDependencies struct {
	Config         config.Config
	Health         *phaseTwoApplicationHealth
	Control        phaseTwoControlRuntime
	Ownership      phaseTwoOwnershipRuntime
	Recorder       *metric.Recorder
	Observer       observability.Observer
	TargetFlow     *observability.TargetFlow
	CloseResources func(context.Context) error
	Now            func() time.Time
}

type phaseTwoWorkerBundle struct {
	runtimeConfig *observability.RuntimeConfigFacts
	dependencies  phaseTwoWorkerBundleDependencies

	registrationMu        sync.Mutex
	mu                    sync.RWMutex
	queryGroups           []execution.QueryGroupIdentity
	assigned              map[execution.QueryGroupIdentity]struct{}
	runners               map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	started               bool
	draining              bool
	closed                bool
	controlLeader         bool
	controlRunning        bool
	controlEpoch          uint64
	controlDegraded       bool
	controlSourceKind     observability.SourceKind
	controlReason         observability.ReasonCode
	lastControlRecoveryAt time.Time
	maintenanceCtx        context.Context
	cancelMaintain        context.CancelFunc
	cancelControl         context.CancelFunc
	maintenanceWG         sync.WaitGroup
	inflightWG            sync.WaitGroup
	shutdownOnce          sync.Once
	shutdownErr           error
	// dependencyDegraded is set while a control or Ownership Store call fails
	// transiently. dependencyFailureSeq counts those failures so a reconcile
	// pass only clears the flag when no new failure happened during the pass.
	dependencyDegraded   bool
	dependencyFailureSeq uint64
}

type phaseTwoScheduledRunner struct {
	queuedAt   time.Time
	queryGroup execution.QueryGroupIdentity
	lifecycle  *phaseTwoQueryGroupLifecycle
}

type phaseTwoScheduledResult struct {
	scheduled       phaseTwoScheduledRunner
	attempted       bool
	admissionDenied bool
	err             error
}

type phaseTwoQueuedRunner struct {
	scheduled phaseTwoScheduledRunner
	readyAt   time.Time
}

type phaseTwoRunnerGeneration struct {
	lifecycle  *phaseTwoQueryGroupLifecycle
	generation uint64
}

type phaseTwoRunnerDispatcher struct {
	occupancyMu sync.Mutex
	executing   int
	bundle      *phaseTwoWorkerBundle
	fanout      int

	jobs    chan phaseTwoScheduledRunner
	results chan phaseTwoScheduledResult
	workers sync.WaitGroup

	generation uint64
	cursor     execution.QueryGroupIdentity
	lastQueued map[execution.QueryGroupIdentity]phaseTwoRunnerGeneration
	queued     map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	active     map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
	normal     []phaseTwoQueuedRunner
	delayed    []phaseTwoQueuedRunner

	preferDelayed  bool
	oneShot        bool
	oneShotTargets map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle
}

func newPhaseTwoWorkerBundle(dependencies phaseTwoWorkerBundleDependencies) (*phaseTwoWorkerBundle, error) {
	if dependencies.Health == nil || dependencies.Control == nil || dependencies.Ownership == nil ||
		dependencies.Observer == nil || dependencies.Now == nil {
		return nil, errors.New("phase-two worker requires complete lifecycle dependencies")
	}
	if err := dependencies.Config.Validate(); err != nil {
		return nil, err
	}
	bundle := &phaseTwoWorkerBundle{dependencies: dependencies,
		assigned: make(map[execution.QueryGroupIdentity]struct{}),
		runners:  make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle)}
	if dependencies.Recorder != nil {
		dependencies.Recorder.SetOwnedQueryGroups(0)
	}
	return bundle, nil
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
	bundle.mu.Unlock()
	bundle.observe(ctx, observability.ComponentRuntime, observability.Stage(observability.StageStartup), observability.ResultStarted, nil)
	bundle.dependencies.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentRuntime, Stage: observability.StageConfigLoaded,
		Result: observability.ResultSuccess, RuntimeConfig: bundle.runtimeConfig,
	})
	if err := bundle.register(ctx, ownership.WorkerStarting); err != nil {
		return err
	}
	leader, err := bundle.tryAcquireControlLeader(ctx)
	if err != nil {
		return fmt.Errorf("phase-two acquire Control Leader: %w", err)
	}
	var controlResult phaseTwoControlRefreshResult
	controlFactsAvailable := true
	if leader {
		controlResult, err = bundle.dependencies.Control.InitialRefresh(ctx)
	} else {
		controlResult, err = bundle.dependencies.Control.LoadActive(ctx)
	}
	if err != nil {
		if !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			return fmt.Errorf("phase-two initial control facts: %w", err)
		}
		controlFactsAvailable = false
		controlResult = phaseTwoControlRefreshResult{
			Status: phaseTwoControlDegradedLastGood, SourceKind: observability.SourceKindCompiledSnapshot,
			ReasonCode: observability.ReasonContractRetryable, Cause: err,
		}
	}
	queryGroups := controlResult.QueryGroups
	if leader || controlResult.Status == phaseTwoControlDegradedLastGood {
		if err := bundle.applyControlRefresh(ctx, controlResult); err != nil {
			return err
		}
	} else {
		bundle.setControlQueryGroups(queryGroups)
	}
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		return err
	}
	if leader && controlFactsAvailable {
		if err := bundle.dependencies.Ownership.PublishAssignments(ctx, queryGroups, bundle.dependencies.Now()); err != nil {
			return fmt.Errorf("phase-two publish Assignment: %w", err)
		}
	}
	assigned, err := bundle.dependencies.Ownership.AssignedQueryGroups(ctx, queryGroups)
	if err != nil {
		return fmt.Errorf("phase-two read Assignment: %w", err)
	}
	if err := bundle.applyAssignment(ctx, assigned); err != nil {
		return err
	}
	bundle.startMaintenance()
	bundle.updateReadiness()
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
	schedulerCtx, cancelScheduler := context.WithCancel(ctx)
	schedulerWake := make(chan struct{}, 1)
	schedulerDone := make(chan error, 1)
	ticksDone := make(chan struct{})
	// Control refresh and reconcile can wait on dependencies. Keep normal
	// generations advancing independently, with at most one pending wake.
	go func() {
		defer close(ticksDone)
		for {
			select {
			case <-schedulerCtx.Done():
				return
			case <-scheduleTicker.C:
				select {
				case schedulerWake <- struct{}{}:
				default:
				}
			}
		}
	}()
	go func() {
		schedulerDone <- bundle.runScheduler(schedulerCtx, schedulerWake, false)
	}()

	var runErr error
	schedulerRunning := true
	for runErr == nil {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case <-refreshTicker.C:
			runErr = bundle.refreshAndReconcile(ctx, true)
		case <-reconcileTicker.C:
			runErr = bundle.refreshAndReconcile(ctx, false)
		case schedulerErr := <-schedulerDone:
			schedulerRunning = false
			if schedulerErr == nil {
				schedulerErr = errPhaseTwoWorkerStopped
			}
			runErr = schedulerErr
		}
	}
	cancelScheduler()
	<-ticksDone
	if schedulerRunning {
		schedulerErr := <-schedulerDone
		if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
			runErr = errors.Join(runErr, schedulerErr)
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
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	return bundle.runScheduler(ctx, wake, true)
}

func (bundle *phaseTwoWorkerBundle) runScheduler(
	ctx context.Context,
	wake <-chan struct{},
	oneShot bool,
) error {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return errPhaseTwoWorkerDraining
	}
	// Shutdown takes the same lock before waiting, so this sentinel prevents
	// WaitGroup Add/Wait races while the dispatcher can still run QG work.
	bundle.inflightWG.Add(1)
	bundle.mu.Unlock()
	defer bundle.inflightWG.Done()

	dispatcher := newPhaseTwoRunnerDispatcher(bundle, oneShot)
	dispatcher.start(ctx)
	defer dispatcher.stop()
	return dispatcher.run(ctx, wake)
}

func newPhaseTwoRunnerDispatcher(
	bundle *phaseTwoWorkerBundle,
	oneShot bool,
) *phaseTwoRunnerDispatcher {
	schedulerConfig := bundle.dependencies.Config.PhaseTwo.Scheduler
	fanout := schedulerConfig.ActiveExecutionLimit
	if schedulerConfig.ReadyQueueCapacity < fanout {
		fanout = schedulerConfig.ReadyQueueCapacity
	}
	return &phaseTwoRunnerDispatcher{
		bundle: bundle, fanout: fanout,
		jobs: make(chan phaseTwoScheduledRunner), results: make(chan phaseTwoScheduledResult, schedulerConfig.ReadyQueueCapacity),
		lastQueued:    make(map[execution.QueryGroupIdentity]phaseTwoRunnerGeneration),
		queued:        make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		active:        make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		preferDelayed: true, oneShot: oneShot,
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) start(ctx context.Context) {
	// Zero removes the complete-Runner gate. The dispatcher active map still
	// admits each owned QG once; this loop never spawns repeated waiters per tick.
	workers := dispatcher.fanout
	if workers == 0 {
		workers = 1
	}
	for range workers {
		dispatcher.workers.Add(1)
		go func() {
			defer dispatcher.workers.Done()
			for scheduled := range dispatcher.jobs {
				if dispatcher.fanout == 0 {
					dispatcher.workers.Add(1)
					go func(scheduled phaseTwoScheduledRunner) {
						defer dispatcher.workers.Done()
						dispatcher.executeScheduled(ctx, scheduled)
					}(scheduled)
				} else {
					dispatcher.executeScheduled(ctx, scheduled)
				}
			}
		}()
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) executeScheduled(ctx context.Context, scheduled phaseTwoScheduledRunner) {
	dispatcher.changeExecuting(ctx, 1)
	result := phaseTwoScheduledResult{scheduled: scheduled}
	runCtx := dispatcher.bundle.dependencies.TargetFlow.Context(ctx, string(scheduled.queryGroup))
	if observability.TargetFlowEnabled(runCtx) {
		runCtx = observability.ContextWithTraceFields(runCtx, observability.TraceFields{QueryGroupKey: string(scheduled.queryGroup)})
		observability.EmitTargetFlow(runCtx, "runner_dispatch", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "execution_slot_acquired", QueuedAtMS: scheduled.queuedAt.UnixMilli(), QueueWaitNS: time.Since(scheduled.queuedAt).Nanoseconds()})
	}
	func() {
		defer dispatcher.changeExecuting(ctx, -1)
		if ctx.Err() == nil && dispatcher.bundle.isCurrentScheduledRunner(scheduled) {
			func() {
				defer startSlotTiming(runCtx, dispatcher.bundle.dependencies.Observer, observability.StageRunnerCompleted, time.Now)()
				_, result.attempted, result.admissionDenied, result.err =
					scheduled.lifecycle.runner.RunOneAdmitted(runCtx, func(execution.Operation) (func(), bool) {
						// A positive F is an emergency guard; zero has no Runner gate.
						// P/R belong only to actual Query admission in Access.
						return func() {}, ctx.Err() == nil
					})
			}()
		}
	}()
	observability.EmitTargetFlow(runCtx, "runner_return", observability.TraceFields{}, observability.TargetFlowFacts{Decision: "returned", Attempted: result.attempted})
	dispatcher.results <- result
}

func (dispatcher *phaseTwoRunnerDispatcher) stop() {
	close(dispatcher.jobs)
	dispatcher.workers.Wait()
}

func (dispatcher *phaseTwoRunnerDispatcher) run(ctx context.Context, wake <-chan struct{}) error {
	var canceled error
	ctxDone := ctx.Done()
	for {
		dispatcher.observeOccupancy(ctx)
		if canceled != nil && len(dispatcher.active) == 0 {
			return canceled
		}
		if dispatcher.oneShot && dispatcher.generation > 0 && len(dispatcher.oneShotTargets) == 0 &&
			len(dispatcher.active) == 0 {
			return nil
		}

		// Consume completed work before admitting another normal item. This
		// makes a newly ready recovery visible to the fairness decision.
		select {
		case result := <-dispatcher.results:
			dispatcher.handleResult(ctx, result, canceled == nil)
			continue
		default:
		}

		dispatcher.dropStaleQueued()
		dispatcher.fillQueues()
		now := dispatcher.bundle.schedulerNow()
		dispatcher.sortDelayed()
		dispatcher.observeOccupancy(ctx)
		delayedDue := len(dispatcher.delayed) > 0 && !dispatcher.delayed[0].readyAt.After(now)
		normalReady := len(dispatcher.normal) > 0
		selectDelayed := canceled == nil && delayedDue && (!normalReady || dispatcher.preferDelayed)
		selectNormal := canceled == nil && normalReady && !selectDelayed

		var dispatch chan phaseTwoScheduledRunner
		var scheduled phaseTwoScheduledRunner
		if selectDelayed {
			dispatch = dispatcher.jobs
			scheduled = dispatcher.delayed[0].scheduled
		} else if selectNormal {
			dispatch = dispatcher.jobs
			scheduled = dispatcher.normal[0].scheduled
		}

		var retryTimer *time.Timer
		var retryReady <-chan time.Time
		if canceled == nil && dispatch == nil && len(dispatcher.delayed) > 0 {
			delay := dispatcher.delayed[0].readyAt.Sub(now)
			if delay < 0 {
				delay = 0
			}
			retryTimer = time.NewTimer(delay)
			retryReady = retryTimer.C
		}

		select {
		case dispatch <- scheduled:
			dispatcher.markDispatched(scheduled, selectDelayed, delayedDue)
		case result := <-dispatcher.results:
			dispatcher.handleResult(ctx, result, canceled == nil)
		case <-wake:
			if canceled == nil {
				dispatcher.beginGeneration()
			}
		case <-retryReady:
		case <-ctxDone:
			canceled = ctx.Err()
			ctxDone = nil
		}
		if retryTimer != nil && !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) beginGeneration() {
	dispatcher.generation++
	if dispatcher.oneShot && dispatcher.generation == 1 {
		dispatcher.oneShotTargets = make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle)
		for _, scheduled := range dispatcher.bundle.snapshotScheduledRunners() {
			dispatcher.oneShotTargets[scheduled.queryGroup] = scheduled.lifecycle
		}
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) fillQueues() {
	if dispatcher.generation == 0 {
		return
	}
	runners := dispatcher.bundle.snapshotScheduledRunners()
	if len(runners) == 0 {
		return
	}
	start := sort.Search(len(runners), func(index int) bool {
		return runners[index].queryGroup > dispatcher.cursor
	})
	for offset := 0; offset < len(runners); offset++ {
		scheduled := runners[(start+offset)%len(runners)]
		if dispatcher.active[scheduled.queryGroup] != nil || dispatcher.queued[scheduled.queryGroup] != nil {
			continue
		}
		last := dispatcher.lastQueued[scheduled.queryGroup]
		if last.lifecycle == scheduled.lifecycle && last.generation == dispatcher.generation {
			continue
		}
		if dispatcher.bundle.dependencies.TargetFlow.Selected(string(scheduled.queryGroup)) {
			scheduled.queuedAt = time.Now()
		}
		readyAt := scheduled.lifecycle.runner.NextReadyAt()
		queued := phaseTwoQueuedRunner{scheduled: scheduled, readyAt: readyAt}
		if readyAt.IsZero() {
			if len(dispatcher.normal) >= dispatcher.bundle.dependencies.Config.PhaseTwo.Scheduler.ReadyQueueCapacity {
				dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "normal_queue_full"})
				continue
			}
			dispatcher.normal = append(dispatcher.normal, queued)
		} else {
			if len(dispatcher.delayed) >= dispatcher.bundle.dependencies.Config.PhaseTwo.Scheduler.RecoveryQueueCapacity {
				latest := dispatcher.latestDelayedIndex()
				if latest < 0 || !delayedBefore(queued, dispatcher.delayed[latest]) {
					dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "delayed_queue_full", ReadyAtMS: diagnosticTimeMS(readyAt)})
					continue
				}
				evicted := dispatcher.delayed[latest].scheduled
				dispatcher.bundle.dependencies.TargetFlow.Record("queue_skipped", string(evicted.queryGroup), observability.TargetFlowFacts{Decision: "delayed_queue_evicted"})
				if dispatcher.queued[evicted.queryGroup] == evicted.lifecycle {
					delete(dispatcher.queued, evicted.queryGroup)
				}
				if dispatcher.oneShot {
					delete(dispatcher.lastQueued, evicted.queryGroup)
				}
				dispatcher.delayed[latest] = queued
			} else {
				dispatcher.delayed = append(dispatcher.delayed, queued)
			}
		}
		dispatcher.bundle.dependencies.TargetFlow.Record("runner_queued", string(scheduled.queryGroup), observability.TargetFlowFacts{Decision: "queued", QueuedAtMS: scheduled.queuedAt.UnixMilli(), ReadyAtMS: diagnosticTimeMS(readyAt)})
		dispatcher.queued[scheduled.queryGroup] = scheduled.lifecycle
		dispatcher.lastQueued[scheduled.queryGroup] = phaseTwoRunnerGeneration{
			lifecycle: scheduled.lifecycle, generation: dispatcher.generation,
		}
		dispatcher.cursor = scheduled.queryGroup
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) markDispatched(
	scheduled phaseTwoScheduledRunner,
	delayed bool,
	delayedDue bool,
) {
	delete(dispatcher.queued, scheduled.queryGroup)
	dispatcher.active[scheduled.queryGroup] = scheduled.lifecycle
	if delayed {
		dispatcher.delayed = dispatcher.delayed[1:]
		dispatcher.preferDelayed = false
		return
	}
	dispatcher.normal = dispatcher.normal[1:]
	if delayedDue {
		dispatcher.preferDelayed = true
	}
}

func (dispatcher *phaseTwoRunnerDispatcher) handleResult(
	ctx context.Context,
	result phaseTwoScheduledResult,
	requeue bool,
) {
	scheduled := result.scheduled
	if dispatcher.active[scheduled.queryGroup] == scheduled.lifecycle {
		delete(dispatcher.active, scheduled.queryGroup)
	}
	if dispatcher.oneShotTargets[scheduled.queryGroup] == scheduled.lifecycle {
		delete(dispatcher.oneShotTargets, scheduled.queryGroup)
	}
	if result.err != nil {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(result.err, ownership.ErrStaleFence) || errors.Is(result.err, ownership.ErrNotDesired) ||
			errors.Is(result.err, scheduler.ErrSlotOwnershipChanged) {
			dispatcher.bundle.stopLostQueryGroup(scheduled.queryGroup, scheduled.lifecycle, result.err)
			return
		}
		if !result.attempted {
			observeRuntime(ctx, dispatcher.bundle.dependencies.Observer, observability.Observation{
				Component: observability.ComponentScheduler, Stage: observability.StageScheduleDue,
				Result: observability.ResultFailed, ReasonCode: observability.ReasonInternalUnknown,
				Direction: observability.DirectionInternal,
				Trace:     observability.TraceFields{QueryGroupKey: string(scheduled.queryGroup)},
				Err:       result.err,
			})
		}
	}
	if result.admissionDenied {
		// A scheduler tick received while this attempt was still active cannot
		// authorize an immediate denied retry. Only a later tick may requeue it.
		dispatcher.lastQueued[scheduled.queryGroup] = phaseTwoRunnerGeneration{
			lifecycle: scheduled.lifecycle, generation: dispatcher.generation,
		}
		return
	}
	if !requeue || !dispatcher.bundle.isCurrentScheduledRunner(scheduled) {
		return
	}
	if dispatcher.bundle.dependencies.TargetFlow.Selected(string(scheduled.queryGroup)) {
		scheduled.queuedAt = time.Now()
	}
	readyAt := scheduled.lifecycle.runner.NextReadyAt()
	if readyAt.IsZero() || len(dispatcher.delayed) >=
		dispatcher.bundle.dependencies.Config.PhaseTwo.Scheduler.RecoveryQueueCapacity {
		return
	}
	dispatcher.delayed = append(dispatcher.delayed, phaseTwoQueuedRunner{scheduled: scheduled, readyAt: readyAt})
	dispatcher.queued[scheduled.queryGroup] = scheduled.lifecycle
}

func (dispatcher *phaseTwoRunnerDispatcher) sortDelayed() {
	sort.SliceStable(dispatcher.delayed, func(left, right int) bool {
		return delayedBefore(dispatcher.delayed[left], dispatcher.delayed[right])
	})
}

func (dispatcher *phaseTwoRunnerDispatcher) latestDelayedIndex() int {
	latest := -1
	for index := range dispatcher.delayed {
		if latest < 0 || delayedBefore(dispatcher.delayed[latest], dispatcher.delayed[index]) {
			latest = index
		}
	}
	return latest
}

func delayedBefore(left, right phaseTwoQueuedRunner) bool {
	if left.readyAt.Equal(right.readyAt) {
		return left.scheduled.queryGroup < right.scheduled.queryGroup
	}
	return left.readyAt.Before(right.readyAt)
}

func (dispatcher *phaseTwoRunnerDispatcher) dropStaleQueued() {
	drop := func(queue []phaseTwoQueuedRunner) []phaseTwoQueuedRunner {
		kept := queue[:0]
		for _, queued := range queue {
			if dispatcher.bundle.isCurrentScheduledRunner(queued.scheduled) {
				kept = append(kept, queued)
				continue
			}
			if dispatcher.queued[queued.scheduled.queryGroup] == queued.scheduled.lifecycle {
				delete(dispatcher.queued, queued.scheduled.queryGroup)
			}
			if dispatcher.oneShotTargets[queued.scheduled.queryGroup] == queued.scheduled.lifecycle {
				delete(dispatcher.oneShotTargets, queued.scheduled.queryGroup)
			}
		}
		return kept
	}
	dispatcher.normal = drop(dispatcher.normal)
	dispatcher.delayed = drop(dispatcher.delayed)
	for queryGroup, generation := range dispatcher.lastQueued {
		if !dispatcher.bundle.isCurrentScheduledRunner(phaseTwoScheduledRunner{
			queryGroup: queryGroup,
			lifecycle:  generation.lifecycle,
		}) {
			delete(dispatcher.lastQueued, queryGroup)
		}
	}
}

func (bundle *phaseTwoWorkerBundle) snapshotScheduledRunners() []phaseTwoScheduledRunner {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	runners := make([]phaseTwoScheduledRunner, 0, len(bundle.runners))
	for queryGroup, lifecycle := range bundle.runners {
		runners = append(runners, phaseTwoScheduledRunner{queryGroup: queryGroup, lifecycle: lifecycle})
	}
	sort.Slice(runners, func(left, right int) bool {
		return runners[left].queryGroup < runners[right].queryGroup
	})
	return runners
}

func (bundle *phaseTwoWorkerBundle) isCurrentScheduledRunner(scheduled phaseTwoScheduledRunner) bool {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return !bundle.draining && !bundle.closed && bundle.runners[scheduled.queryGroup] == scheduled.lifecycle
}

func (bundle *phaseTwoWorkerBundle) schedulerNow() time.Time {
	if bundle.dependencies.Now != nil {
		return bundle.dependencies.Now()
	}
	return time.Now()
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
		runners := make([]*phaseTwoQueryGroupLifecycle, 0, len(bundle.runners))
		queryGroups := make([]execution.QueryGroupIdentity, 0, len(bundle.runners))
		for queryGroup, lifecycle := range bundle.runners {
			delete(bundle.runners, queryGroup)
			lifecycle.cancel()
			runners = append(runners, lifecycle)
			queryGroups = append(queryGroups, queryGroup)
		}
		bundle.setOwnedQueryGroupsLocked()
		bundle.mu.Unlock()
		result = append(result, waitPhaseTwoGroup(ctx, &bundle.maintenanceWG))
		for index, lifecycle := range runners {
			releaseErr := lifecycle.runner.Release(ctx)
			result = append(result, releaseErr)
			transitionResult := observability.Result(observability.ResultSuccess)
			if releaseErr != nil {
				transitionResult = observability.ResultFailed
			}
			bundle.observeOwnership(ctx, observability.StageAssignmentLost, transitionResult, queryGroups[index], releaseErr)
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
	registration, err := phaseTwoWorkerRegistration(bundle.dependencies.Config, readiness, bundle.dependencies.Now())
	if err != nil {
		return err
	}
	if err := bundle.dependencies.Ownership.RegisterWorker(ctx, registration); err != nil {
		return fmt.Errorf("phase-two register worker as %s: %w", readiness, err)
	}
	return nil
}

func phaseTwoWorkerRegistration(
	cfg config.Config,
	readiness ownership.AssignmentReadiness,
	at time.Time,
) (ownership.WorkerRegistration, error) {
	capabilitiesDigest, err := phaseTwoCapabilitiesDigest(cfg)
	if err != nil {
		return ownership.WorkerRegistration{}, fmt.Errorf("phase-two derive worker capabilities: %w", err)
	}
	registration := ownership.WorkerRegistration{
		WorkerID: cfg.PhaseTwo.Worker.ID, AssignmentReadiness: readiness,
		DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: cfg.PhaseTwo.Worker.DeploymentProfile,
		CapabilitiesDigest: capabilitiesDigest,
		ExpiresAt:          at.Add(cfg.PhaseTwo.Worker.RegistrationTTL.Duration()),
	}
	if err := registration.Validate(); err != nil {
		return ownership.WorkerRegistration{}, err
	}
	return registration, nil
}

func (bundle *phaseTwoWorkerBundle) startMaintenance() {
	bundle.maintenanceWG.Add(1)
	go bundle.maintainRegistration()
}

func (bundle *phaseTwoWorkerBundle) tryAcquireControlLeader(ctx context.Context) (bool, error) {
	bundle.mu.RLock()
	if bundle.controlLeader {
		bundle.mu.RUnlock()
		return true, nil
	}
	if bundle.controlRunning {
		bundle.mu.RUnlock()
		return false, nil
	}
	bundle.mu.RUnlock()
	leader, err := bundle.dependencies.Ownership.TryAcquireControlLeader(
		ctx, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
	)
	if err != nil || !leader {
		return leader, err
	}
	bundle.startControlMaintenance()
	return true, nil
}

func (bundle *phaseTwoWorkerBundle) startControlMaintenance() {
	cfg := bundle.dependencies.Config.PhaseTwo
	bundle.mu.Lock()
	if bundle.draining || bundle.closed || bundle.controlRunning {
		bundle.mu.Unlock()
		return
	}
	controlCtx, cancel := context.WithCancel(bundle.maintenanceCtx)
	bundle.controlLeader = true
	bundle.controlRunning = true
	bundle.controlEpoch++
	controlEpoch := bundle.controlEpoch
	bundle.cancelControl = cancel
	bundle.maintenanceWG.Add(1)
	bundle.mu.Unlock()
	go func() {
		defer bundle.maintenanceWG.Done()
		err := bundle.dependencies.Ownership.MaintainControlLeader(
			controlCtx, cfg.Ownership.ControlLeaderRenewInterval.Duration(), cfg.Ownership.ControlLeaderTTL.Duration(),
		)
		bundle.mu.Lock()
		if bundle.controlEpoch == controlEpoch {
			bundle.controlLeader = false
			bundle.controlRunning = false
			bundle.cancelControl = nil
		}
		bundle.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			bundle.observe(context.Background(), observability.ComponentControlPlane, observability.StageSnapshotUnavailable, observability.ResultFailed, err)
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

func (bundle *phaseTwoWorkerBundle) applyAssignment(
	ctx context.Context,
	assigned []execution.QueryGroupIdentity,
) error {
	desired := make(map[execution.QueryGroupIdentity]struct{}, len(assigned))
	for _, queryGroup := range assigned {
		if queryGroup == "" {
			return newPhaseTwoInvariantError("phase-two Assignment contains empty Query Group")
		}
		if _, duplicate := desired[queryGroup]; duplicate {
			return newPhaseTwoInvariantError("phase-two Assignment contains duplicate Query Group")
		}
		desired[queryGroup] = struct{}{}
	}

	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return errPhaseTwoWorkerDraining
	}
	bundle.assigned = desired
	removed := make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle)
	for queryGroup, lifecycle := range bundle.runners {
		if _, keep := desired[queryGroup]; keep {
			continue
		}
		delete(bundle.runners, queryGroup)
		removed[queryGroup] = lifecycle
	}
	bundle.setOwnedQueryGroupsLocked()
	missing := make([]execution.QueryGroupIdentity, 0, len(desired))
	for queryGroup := range desired {
		if _, open := bundle.runners[queryGroup]; !open {
			missing = append(missing, queryGroup)
		}
	}
	bundle.mu.Unlock()

	// Every error below is scoped to one Query Group. Store I/O failures mark
	// the Worker degraded and are retried on the next reconcile; siblings and
	// the Worker continue. Only invariant violations and cancellation return.
	for queryGroup, lifecycle := range removed {
		if err := bundle.stopQueryGroup(ctx, queryGroup, lifecycle); err != nil {
			bundle.observeOwnership(ctx, observability.StageAssignmentLost, observability.ResultFailed, queryGroup, err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The lifecycle is already detached; the lease expires by TTL.
			bundle.markControlDependencyDegraded()
			continue
		}
		bundle.observeOwnership(ctx, observability.StageAssignmentLost, observability.ResultSuccess, queryGroup, nil)
	}
	for _, queryGroup := range missing {
		bundle.observeOwnership(ctx, observability.StageTakeoverStarted, observability.ResultStarted, queryGroup, nil)
		runner, err := bundle.dependencies.Ownership.OpenQueryGroup(
			ctx, queryGroup, bundle.dependencies.Now(), bundle.dependencies.Config.PhaseTwo.Ownership.LeaseTTL.Duration(),
		)
		if err != nil {
			bundle.observeOwnership(ctx, observability.StageTakeoverCompleted, observability.ResultFailed, queryGroup, err)
			if errors.Is(err, ownership.ErrLeaseBusy) || errors.Is(err, ownership.ErrNotDesired) ||
				errors.Is(err, ownership.ErrStaleFence) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isPhaseTwoInvariantError(err) {
				return fmt.Errorf("phase-two open Query Group %s: %w", queryGroup, err)
			}
			bundle.markControlDependencyDegraded()
			continue
		}
		if runner == nil {
			return newPhaseTwoInvariantError(fmt.Sprintf("phase-two open Query Group %s returned no runner", queryGroup))
		}
		if !bundle.startQueryGroup(ctx, queryGroup, runner) {
			if err := runner.Release(ctx); err != nil {
				bundle.observeOwnership(ctx, observability.StageAssignmentLost, observability.ResultFailed, queryGroup, err)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				bundle.markControlDependencyDegraded()
			}
			continue
		}
	}
	return nil
}

// markControlDependencyDegraded records one transient control or Ownership
// Store failure. The Worker stays Ready-degraded (or NotReady when the
// Assignment is incomplete) with a fixed readiness reason until a full
// refresh or reconcile pass completes without a new failure.
func (bundle *phaseTwoWorkerBundle) markControlDependencyDegraded() {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return
	}
	bundle.dependencyDegraded = true
	bundle.dependencyFailureSeq++
	bundle.mu.Unlock()
	bundle.updateReadiness()
}

func (bundle *phaseTwoWorkerBundle) controlDependencyFailureSeq() uint64 {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.dependencyFailureSeq
}

// clearControlDependencyDegraded ends the degraded episode after a reconcile
// pass that started at sinceSeq completed without another dependency failure.
func (bundle *phaseTwoWorkerBundle) clearControlDependencyDegraded(ctx context.Context, sinceSeq uint64) {
	bundle.mu.Lock()
	if !bundle.dependencyDegraded || bundle.dependencyFailureSeq != sinceSeq {
		bundle.mu.Unlock()
		return
	}
	bundle.dependencyDegraded = false
	bundle.lastControlRecoveryAt = bundle.dependencies.Now()
	bundle.mu.Unlock()
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
		Result: observability.Result(observability.ResultRecovered), Direction: observability.DirectionInternal,
		ReasonCode: phaseTwoControlDependencyReason,
	})
}

// scopeControlError classifies one refresh or reconcile error. Invariant
// violations and cancellation of the Run context propagate so Run stops.
// Everything else is a dependency failure: it is logged once with a fixed
// reason, marks the Worker degraded and is retried on the next tick.
func (bundle *phaseTwoWorkerBundle) scopeControlError(
	ctx context.Context,
	component observability.Component,
	stage observability.Stage,
	err error,
) error {
	if err == nil {
		return nil
	}
	if isPhaseTwoInvariantError(err) || errors.Is(err, errPhaseTwoWorkerDraining) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: component, Stage: stage, Result: observability.ResultFailed,
		Direction: observability.DirectionInternal, ReasonCode: phaseTwoControlDependencyReason, Err: err,
	})
	bundle.markControlDependencyDegraded()
	return nil
}

func (bundle *phaseTwoWorkerBundle) startQueryGroup(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	runner phaseTwoQueryGroupRuntime,
) bool {
	bundle.mu.Lock()
	if bundle.draining || bundle.closed {
		bundle.mu.Unlock()
		return false
	}
	if _, desired := bundle.assigned[queryGroup]; !desired {
		bundle.mu.Unlock()
		return false
	}
	if _, exists := bundle.runners[queryGroup]; exists {
		bundle.mu.Unlock()
		return false
	}
	leaseCtx, cancel := context.WithCancel(bundle.maintenanceCtx)
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: runner, cancel: cancel, done: make(chan struct{})}
	bundle.runners[queryGroup] = lifecycle
	bundle.setOwnedQueryGroupsLocked()
	bundle.maintenanceWG.Add(1)
	bundle.mu.Unlock()
	bundle.observeOwnership(ctx, observability.StageTakeoverCompleted, observability.ResultSuccess, queryGroup, nil)
	bundle.observeOwnership(ctx, observability.StageAssignmentAcquired, observability.ResultSuccess, queryGroup, nil)

	cfg := bundle.dependencies.Config.PhaseTwo.Ownership
	go func() {
		defer bundle.maintenanceWG.Done()
		defer close(lifecycle.done)
		err := runner.MaintainLease(leaseCtx, cfg.LeaseRenewInterval.Duration(), cfg.LeaseTTL.Duration())
		if err != nil && !errors.Is(err, context.Canceled) {
			bundle.detachLostQueryGroup(queryGroup, lifecycle, err)
		}
	}()
	return true
}

func (bundle *phaseTwoWorkerBundle) stopQueryGroup(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
) error {
	lifecycle.cancel()
	select {
	case <-lifecycle.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := lifecycle.runner.Release(ctx); err != nil {
		return fmt.Errorf("phase-two release Query Group %s: %w", queryGroup, err)
	}
	return nil
}

func (bundle *phaseTwoWorkerBundle) stopLostQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	err error,
) {
	if !bundle.detachQueryGroup(queryGroup, lifecycle) {
		return
	}
	lifecycle.cancel()
	select {
	case <-lifecycle.done:
	case <-time.After(bundle.dependencies.Config.ShutdownTimeout.Duration()):
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	if releaseErr := lifecycle.runner.Release(releaseCtx); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	bundle.updateReadiness()
	bundle.observeOwnership(context.Background(), observability.StageAssignmentLost, observability.ResultFailed, queryGroup, err)
}

func (bundle *phaseTwoWorkerBundle) detachLostQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
	err error,
) {
	if !bundle.detachQueryGroup(queryGroup, lifecycle) {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	if releaseErr := lifecycle.runner.Release(releaseCtx); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	bundle.updateReadiness()
	bundle.observeOwnership(context.Background(), observability.StageAssignmentLost, observability.ResultFailed, queryGroup, err)
}

func (bundle *phaseTwoWorkerBundle) detachQueryGroup(
	queryGroup execution.QueryGroupIdentity,
	lifecycle *phaseTwoQueryGroupLifecycle,
) bool {
	bundle.mu.Lock()
	defer bundle.mu.Unlock()
	if bundle.runners[queryGroup] != lifecycle {
		return false
	}
	delete(bundle.runners, queryGroup)
	bundle.setOwnedQueryGroupsLocked()
	return true
}

func (bundle *phaseTwoWorkerBundle) setOwnedQueryGroupsLocked() {
	if bundle.dependencies.Recorder != nil {
		bundle.dependencies.Recorder.SetOwnedQueryGroups(len(bundle.runners))
	}
}

func (bundle *phaseTwoWorkerBundle) updateReadiness() {
	bundle.mu.RLock()
	assignmentReady := !bundle.draining && !bundle.closed && len(bundle.runners) == len(bundle.assigned)
	if assignmentReady {
		for queryGroup := range bundle.assigned {
			if _, open := bundle.runners[queryGroup]; !open {
				assignmentReady = false
				break
			}
		}
	}
	controlDegraded := bundle.controlDegraded
	controlReason := bundle.controlReason
	dependencyDegraded := bundle.dependencyDegraded
	lastRecoveryAt := bundle.lastControlRecoveryAt
	bundle.mu.RUnlock()
	state := observability.HealthReady
	var reasons []observability.ReasonCode
	if !assignmentReady {
		state = observability.HealthNotReady
		reasons = []observability.ReasonCode{observability.ReasonInternalUnknown}
	} else if controlDegraded || dependencyDegraded {
		state = observability.HealthDegraded
		if controlDegraded {
			reasons = append(reasons, controlReason)
		}
	}
	if dependencyDegraded {
		reasons = append(reasons, phaseTwoControlDependencyReason)
	}
	bundle.dependencies.Health.Update(phaseTwoReadiness{
		State: state, Reasons: reasons, SnapshotReady: true, AssignmentReady: assignmentReady,
		RuntimeStateReady: true, OutputSinkReady: true, LastRecoveryAt: lastRecoveryAt,
	})
}

// refreshAndReconcile runs one control tick. Dependency failures never stop
// the Worker: they are scoped by scopeControlError, keep already-owned Query
// Groups running and are retried on the next tick. Only invariant violations
// and cancellation are returned to Run.
func (bundle *phaseTwoWorkerBundle) refreshAndReconcile(ctx context.Context, refresh bool) error {
	failureSeq := bundle.controlDependencyFailureSeq()
	var queryGroups []execution.QueryGroupIdentity
	var err error
	if refresh {
		leader, acquireErr := bundle.tryAcquireControlLeader(ctx)
		if acquireErr != nil {
			return bundle.scopeControlError(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable, acquireErr)
		}
		if leader {
			var result phaseTwoControlRefreshResult
			result, err = bundle.dependencies.Control.Refresh(ctx)
			queryGroups = result.QueryGroups
			if err == nil {
				err = bundle.applyControlRefresh(ctx, result)
			}
		} else {
			var result phaseTwoControlRefreshResult
			result, err = bundle.dependencies.Control.LoadActive(ctx)
			queryGroups = result.QueryGroups
			if err == nil {
				bundle.setControlQueryGroups(queryGroups)
			}
		}
	} else {
		bundle.mu.RLock()
		queryGroups = append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
		bundle.mu.RUnlock()
	}
	if err != nil {
		if !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
			return bundle.scopeControlError(ctx, observability.ComponentControlPlane, observability.StageSnapshotUnavailable, err)
		}
		bundle.mu.RLock()
		queryGroups = append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
		bundle.mu.RUnlock()
		if applyErr := bundle.applyControlRefresh(ctx, phaseTwoControlRefreshResult{
			QueryGroups: queryGroups, Status: phaseTwoControlDegradedLastGood,
			SourceKind: observability.SourceKindCompiledSnapshot,
			ReasonCode: observability.ReasonContractRetryable, Cause: err,
		}); applyErr != nil {
			return applyErr
		}
		bundle.updateReadiness()
		return nil
	}
	bundle.mu.RLock()
	leader := bundle.controlLeader
	bundle.mu.RUnlock()
	if leader {
		if err := bundle.dependencies.Ownership.PublishAssignments(ctx, queryGroups, bundle.dependencies.Now()); err != nil {
			if !errors.Is(err, ownership.ErrStaleFence) {
				return bundle.scopeControlError(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, err)
			}
			bundle.markControlFollower(err)
		}
	}
	assigned, err := bundle.dependencies.Ownership.AssignedQueryGroups(ctx, queryGroups)
	if err != nil {
		return bundle.scopeControlError(ctx, observability.ComponentOwnership, observability.StageAssignmentAcquired, err)
	}
	if err := bundle.applyAssignment(ctx, assigned); err != nil {
		return err
	}
	bundle.clearControlDependencyDegraded(ctx, failureSeq)
	bundle.updateReadiness()
	return nil
}

func (bundle *phaseTwoWorkerBundle) setControlQueryGroups(queryGroups []execution.QueryGroupIdentity) {
	bundle.mu.Lock()
	bundle.queryGroups = append(bundle.queryGroups[:0], queryGroups...)
	bundle.mu.Unlock()
}

func (bundle *phaseTwoWorkerBundle) applyControlRefresh(
	ctx context.Context,
	result phaseTwoControlRefreshResult,
) error {
	if result.Status != phaseTwoControlHealthy && result.Status != phaseTwoControlDegradedLastGood {
		return newPhaseTwoInvariantError("phase-two Control refresh returned an invalid health fact")
	}
	if result.Status == phaseTwoControlDegradedLastGood &&
		(result.SourceKind != observability.SourceKindLegacyStrategy &&
			result.SourceKind != observability.SourceKindCompiledSnapshot ||
			result.ReasonCode == "" || result.Cause == nil) {
		return newPhaseTwoInvariantError("phase-two degraded Control refresh returned an incomplete health fact")
	}
	var transitionResult observability.Result
	var transitionSource observability.SourceKind
	var transitionReason observability.ReasonCode
	var transitionCause error
	bundle.mu.Lock()
	bundle.queryGroups = append(bundle.queryGroups[:0], result.QueryGroups...)
	switch result.Status {
	case phaseTwoControlDegradedLastGood:
		if !bundle.controlDegraded {
			bundle.controlDegraded = true
			bundle.controlSourceKind = result.SourceKind
			bundle.controlReason = result.ReasonCode
			transitionResult = observability.ResultDegraded
			transitionSource = result.SourceKind
			transitionReason = result.ReasonCode
			transitionCause = result.Cause
		}
	case phaseTwoControlHealthy:
		if bundle.controlDegraded {
			transitionResult = observability.Result(observability.ResultRecovered)
			transitionSource = bundle.controlSourceKind
			transitionReason = bundle.controlReason
			bundle.controlDegraded = false
			bundle.lastControlRecoveryAt = bundle.dependencies.Now()
			bundle.controlSourceKind = ""
			bundle.controlReason = ""
		} else if !result.SourceRefreshObserved {
			transitionResult = observability.ResultSuccess
		}
	}
	bundle.mu.Unlock()

	if transitionResult == "" {
		return nil
	}
	stage := observability.Stage(observability.StageSnapshotRefreshed)
	if transitionResult == observability.ResultDegraded {
		stage = observability.Stage(observability.StageSnapshotUnavailable)
	}
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: stage, Result: transitionResult,
		Direction: observability.DirectionInternal, ReasonCode: transitionReason, Err: transitionCause,
		SourceKind: transitionSource,
	})
	return nil
}

func (bundle *phaseTwoWorkerBundle) markControlFollower(err error) {
	bundle.mu.Lock()
	bundle.controlLeader = false
	if bundle.cancelControl != nil {
		bundle.cancelControl()
	}
	bundle.mu.Unlock()
	bundle.observe(context.Background(), observability.ComponentControlPlane, observability.StageSnapshotUnavailable, observability.ResultFailed, err)
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

func (bundle *phaseTwoWorkerBundle) observeOwnership(
	ctx context.Context,
	stage observability.Stage,
	result observability.Result,
	queryGroup execution.QueryGroupIdentity,
	err error,
) {
	reason := observability.ReasonNone
	if err != nil {
		reason = observability.ReasonInternalUnknown
	}
	observeRuntime(ctx, bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentOwnership, Stage: stage, Result: result,
		Operation: observability.OperationTransition, Direction: observability.DirectionInternal,
		ReasonCode: reason, Trace: observability.TraceFields{
			QueryGroupKey: string(queryGroup), OwnerID: bundle.dependencies.Config.PhaseTwo.Worker.ID,
		}, Err: err,
	})
}

func phaseTwoRuntimeObserver(observer observability.Observer) observability.Observer {
	if observer == nil {
		return observability.NopObserver{}
	}
	return observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
		observeRuntime(ctx, observer, observation)
		if observation.Component != observability.ComponentState || observation.Stage != observability.StageSideEffectAdmission {
			return
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageFenceChecked,
			Result: observation.Result, Operation: observation.Operation, Direction: observability.DirectionInternal,
			ReasonCode: observation.ReasonCode, Duration: observation.Duration, Trace: observation.Trace, Err: observation.Err,
		})
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
	LastRecoveryAt    time.Time
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
		LastRecoveryAt: readiness.LastRecoveryAt,
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

func diagnosticTimeMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// The dispatcher loop is the sole writer; publish after handling the previous event.
func (dispatcher *phaseTwoRunnerDispatcher) observeOccupancy(ctx context.Context) {
	dispatcher.occupancyMu.Lock()
	defer dispatcher.occupancyMu.Unlock()
	observeRuntime(ctx, dispatcher.bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageDispatcherSnapshot,
		Result: observability.ResultSuccess, Dispatcher: &observability.DispatcherFacts{
			Active: dispatcher.executing, Ready: len(dispatcher.normal), Delayed: len(dispatcher.delayed), QueuesKnown: true,
		},
	})
}

// Publish under the observation lock so a delayed observer cannot roll F backward.
// This is independent of the dispatcher's pending-result membership map.
func (dispatcher *phaseTwoRunnerDispatcher) changeExecuting(ctx context.Context, delta int) {
	dispatcher.occupancyMu.Lock()
	defer dispatcher.occupancyMu.Unlock()
	dispatcher.executing += delta
	observeRuntime(ctx, dispatcher.bundle.dependencies.Observer, observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageDispatcherSnapshot,
		Result: observability.ResultSuccess, Dispatcher: &observability.DispatcherFacts{Active: dispatcher.executing},
	})
}
