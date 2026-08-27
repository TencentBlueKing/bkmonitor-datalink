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
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

var (
	ErrApplicationShutdownTimeout = errors.New("alarmd runtime: shutdown timeout")
	errKafkaServiceStopped        = errors.New("alarmd runtime: Kafka service stopped unexpectedly")
	errHTTPServiceStopped         = errors.New("alarmd runtime: HTTP service stopped unexpectedly")
	errFatalWithoutReason         = errors.New("alarmd runtime: fatal signal has no error")
)

type serviceRuntime interface {
	lifecycle.Source
	Run(context.Context) error
	Close() error
	FatalSignal() <-chan struct{}
	FatalError() error
}

type httpRuntime interface {
	Run(context.Context, string, time.Duration) error
}

type redisRuntime interface {
	state.Backend
	Ping(context.Context) error
	Close() error
}

type triggerEventRuntime interface {
	coordinator.TriggerEventSink
	Shutdown(context.Context) error
}

type receiptRuntime interface {
	coordinator.ReceiptPublisher
	Shutdown(context.Context) enginekafka.ReceiptDrainResult
}

// unavailableReceiptRuntime makes the non-critical audit loss explicit while
// the evaluation path continues. Phase one recovers this dependency only by
// restarting the process and reopening the real publisher.
type unavailableReceiptRuntime struct {
	err     error
	dropped atomic.Uint64
}

func (runtime *unavailableReceiptRuntime) TryEnqueue(*contract.MessageReceiptV1) bool {
	if runtime != nil {
		runtime.dropped.Add(1)
	}
	return false
}

func (runtime *unavailableReceiptRuntime) Shutdown(context.Context) enginekafka.ReceiptDrainResult {
	if runtime == nil {
		return enginekafka.ReceiptDrainResult{Status: enginekafka.ReceiptDrainFailed}
	}
	return enginekafka.ReceiptDrainResult{
		Status: enginekafka.ReceiptDrainFailed,
		Drops:  enginekafka.ReceiptDropCounts{Closed: runtime.dropped.Load()},
		Err:    runtime.err,
	}
}

type applicationDependencies struct {
	logger     *observability.Logger
	openBundle func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error)
	newHTTP    func(*metric.Recorder, observability.HealthSource) (httpRuntime, error)
}

type applicationComponentFactories struct {
	newEffectiveTime      func() strategy.EffectiveTimeProvider
	openRedis             func(state.RedisBackendOptions) (redisRuntime, error)
	openTriggerEvents     func(enginekafka.DecisionSinkConfig) (triggerEventRuntime, error)
	openReceipts          func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits) (receiptRuntime, error)
	openEvaluationService func(
		enginekafka.Config,
		coordinator.MessageOutcomeRouter,
		coordinator.CriticalCompletion,
		coordinator.ReceiptPublisher,
		*coordinator.CriticalDependencyGate,
		enginekafka.EvaluationDiagnostics,
		coordinator.DependencyRetryConfig,
		time.Duration,
	) (serviceRuntime, error)
}

type criticalCompletionRuntime struct {
	coordinator.CriticalPhaseCompletion
}

func (completion *criticalCompletionRuntime) Complete(ctx context.Context, result coordinator.CriticalResult) error {
	if completion == nil || completion.CriticalPhaseCompletion == nil {
		return errors.New("alarmd runtime: phased critical completion is required")
	}
	if err := completion.CompleteEvents(ctx, result.Events); err != nil {
		return err
	}
	return completion.CompleteState(ctx, result.StateWrite)
}

type applicationBundle struct {
	service       serviceRuntime
	gate          *coordinator.CriticalDependencyGate
	receipts      receiptRuntime
	triggerEvents triggerEventRuntime
	redis         redisRuntime
	logger        *observability.Logger
	resources     *observability.ObservingResourceGovernor

	shutdownOnce sync.Once
	shutdownErr  error
}

func (bundle *applicationBundle) Shutdown(ctx context.Context) error {
	if bundle == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("alarmd runtime: shutdown context is required")
	}
	bundle.shutdownOnce.Do(func() {
		var result []error
		if bundle.service != nil {
			result = append(result, callWithin(ctx, bundle.service.Close))
		}
		if bundle.receipts != nil {
			drain := bundle.receipts.Shutdown(ctx)
			if drain.Err != nil {
				result = append(result, drain.Err)
				if bundle.logger != nil {
					bundle.logger.Error(
						string(observability.StageReceiptQueued), observability.ResultDropped, drain.PendingMessages, 0,
						slog.String("reason", string(drain.Status)),
					)
				}
			}
		}
		if bundle.triggerEvents != nil {
			result = append(result, bundle.triggerEvents.Shutdown(ctx))
		}
		if bundle.redis != nil {
			result = append(result, callWithin(ctx, bundle.redis.Close))
		}
		bundle.shutdownErr = errors.Join(result...)
	})
	return bundle.shutdownErr
}

type startupDependencyError struct {
	err error
}

func (err *startupDependencyError) Error() string {
	return "alarmd runtime: startup dependency unavailable"
}

func (err *startupDependencyError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func retryableStartupDependency(err error) error {
	if err == nil {
		return nil
	}
	return &startupDependencyError{err: err}
}

type applicationHealth struct {
	tracker *observability.HealthTracker

	mu        sync.RWMutex
	service   serviceRuntime
	gate      *coordinator.CriticalDependencyGate
	resources *observability.ObservingResourceGovernor
	draining  bool
	fatal     bool
}

func newApplicationHealth() *applicationHealth {
	return &applicationHealth{tracker: observability.NewHealthTracker(observability.HealthSnapshot{
		State: observability.HealthStarting, ConfigLoaded: true, SchemaReady: true,
	})}
}

func (health *applicationHealth) attach(bundle *applicationBundle) {
	if health == nil || bundle == nil {
		return
	}
	health.mu.Lock()
	health.service = bundle.service
	health.gate = bundle.gate
	health.resources = bundle.resources
	health.mu.Unlock()
	base := health.tracker.HealthSnapshot()
	base.RuntimeStateReady = true
	base.OutputSinkReady = true
	base.State = observability.HealthNotReady
	health.tracker.Update(base)
}

func (health *applicationHealth) markDraining() {
	if health == nil {
		return
	}
	health.mu.Lock()
	health.draining = true
	health.mu.Unlock()
}

func (health *applicationHealth) markFatal() {
	if health == nil {
		return
	}
	health.mu.Lock()
	health.fatal = true
	health.mu.Unlock()
}

func (health *applicationHealth) HealthSnapshot() observability.HealthSnapshot {
	if health == nil || health.tracker == nil {
		return observability.NormalizeHealthSnapshot(observability.HealthSnapshot{})
	}
	base := health.tracker.HealthSnapshot()
	health.mu.RLock()
	service := health.service
	gate := health.gate
	resources := health.resources
	draining := health.draining
	fatal := health.fatal
	health.mu.RUnlock()
	if service != nil {
		snapshot := service.LifecycleSnapshot()
		base.AssignmentReady = snapshot.AssignedClaims > 0
		base.AssignedClaims = snapshot.AssignedClaims
		base.InflightMessages = snapshot.InflightRecords
		base.ConsumerLagRecords = snapshot.ConsumerLagRecords
		base.ConsumerLagKnown = snapshot.ConsumerLagKnown
		if resources != nil {
			resourceSnapshot := resources.ResourceSnapshot()
			resourceSnapshot.ObservedAt = time.Now()
			resourceSnapshot.InflightMessages = float64(snapshot.InflightRecords)
			if snapshot.ConsumerLagKnown {
				resourceSnapshot.ConsumerLagRecords = float64(snapshot.ConsumerLagRecords)
			} else {
				resourceSnapshot.ConsumerLagRecords = -1
			}
			base.ResourceState = resources.Observe(resourceSnapshot).State
		}
	}
	if gate != nil {
		for _, blocker := range gate.Snapshot().Blockers {
			base.Reasons = append(base.Reasons, observability.ReasonCode(blocker.ReasonCode))
		}
	}
	switch {
	case fatal:
		base.State = observability.HealthFatal
	case draining:
		base.State = observability.HealthDraining
	case service == nil:
		base.State = observability.HealthStarting
	case !base.RuntimeStateReady || !base.OutputSinkReady || gate == nil || !gate.Ready() || !base.AssignmentReady:
		base.State = observability.HealthNotReady
	default:
		base.State = observability.HealthReady
	}
	return observability.NormalizeHealthSnapshot(base)
}

func runApplication(ctx context.Context, cfg config.Config, recorder *metric.Recorder, dependencies applicationDependencies) error {
	if ctx == nil {
		return errors.New("alarmd runtime: context is required")
	}
	if ctx.Err() != nil {
		return nil
	}
	if recorder == nil {
		return errors.New("alarmd runtime: metric recorder is required")
	}
	if dependencies.openBundle == nil {
		return errors.New("alarmd runtime: v2 bundle factory is required")
	}
	if dependencies.newHTTP == nil {
		return errors.New("alarmd runtime: HTTP service factory is required")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	eventLogger := dependencies.logger
	if eventLogger == nil {
		eventLogger = observability.Discard(observability.ComponentTrigger)
	}
	started := time.Now()
	eventLogger.Info(
		observability.StageStartup, observability.ResultStarted, 0, 0,
		slog.Int("consumer_buffer_bytes_per_partition", enginekafka.MaxConsumerBytesPerPartition()),
	)
	health := newApplicationHealth()
	server, err := dependencies.newHTTP(recorder, health)
	if err != nil {
		eventLogger.Error(observability.StageStartup, observability.ResultFailed, 0, time.Since(started), slog.String("reason", "http"))
		return err
	}

	httpContext, cancelHTTP := context.WithCancel(context.Background())
	defer cancelHTTP()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Run(httpContext, cfg.HTTP.Listen, cfg.ShutdownTimeout.Duration()) }()

	bundle, err := openBundleWithRetry(ctx, cfg, recorder, eventLogger, dependencies.openBundle, httpDone)
	if err != nil {
		cancelHTTP()
		deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
		httpErr := waitRuntimeComponent(httpDone, deadline)
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return normalizeRuntimeShutdownError(httpErr, false)
		}
		return errors.Join(err, normalizeRuntimeShutdownError(httpErr, false))
	}
	health.attach(bundle)

	serviceContext, cancelService := context.WithCancel(context.Background())
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- bundle.service.Run(serviceContext) }()

	var triggerErr, serviceErr, httpErr error
	var fatalComponent string
	serviceFinished := false
	httpFinished := false
	serviceStoppedEarly := false
	httpStoppedEarly := false
	select {
	case <-ctx.Done():
	case <-bundle.service.FatalSignal():
		triggerErr = bundle.service.FatalError()
		if triggerErr == nil {
			triggerErr = errFatalWithoutReason
		}
		health.markFatal()
		fatalComponent = "service"
	case serviceErr = <-serviceDone:
		serviceFinished = true
		serviceStoppedEarly = ctx.Err() == nil
		if serviceErr == nil && serviceStoppedEarly {
			serviceErr = errKafkaServiceStopped
		}
		if serviceStoppedEarly {
			health.markFatal()
			fatalComponent = "service"
		}
	case httpErr = <-httpDone:
		httpFinished = true
		httpStoppedEarly = ctx.Err() == nil
		if httpErr == nil && httpStoppedEarly {
			httpErr = errHTTPServiceStopped
		}
		if httpStoppedEarly {
			health.markFatal()
			fatalComponent = "http"
		}
	}
	if fatalComponent != "" {
		eventLogger.Error(
			observability.StageFatal, observability.ResultFailed, 0, 0,
			slog.String("reason", fatalComponent),
		)
	}

	health.markDraining()
	shutdownStarted := time.Now()
	deadline := time.Now().Add(cfg.ShutdownTimeout.Duration())
	cancelService()
	if !serviceFinished {
		serviceErr = waitRuntimeComponent(serviceDone, deadline)
	}
	shutdownContext, cancelShutdown := context.WithDeadline(context.Background(), deadline)
	bundleErr := bundle.Shutdown(shutdownContext)
	cancelShutdown()
	cancelHTTP()
	if !httpFinished {
		httpErr = waitRuntimeComponent(httpDone, deadline)
	}
	result := errors.Join(
		triggerErr,
		normalizeRuntimeShutdownError(serviceErr, serviceStoppedEarly),
		normalizeRuntimeShutdownError(httpErr, httpStoppedEarly),
		bundleErr,
	)
	shutdownResult := observability.ResultSuccess
	if errors.Is(result, ErrApplicationShutdownTimeout) || errors.Is(result, context.DeadlineExceeded) {
		shutdownResult = observability.ResultTimeout
	} else if result != nil {
		shutdownResult = observability.ResultFailed
	}
	if result == nil {
		eventLogger.Info(observability.StageShutdown, shutdownResult, 0, time.Since(shutdownStarted))
	} else {
		eventLogger.Error(observability.StageShutdown, shutdownResult, 0, time.Since(shutdownStarted))
	}
	return result
}

func openBundleWithRetry(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	open func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error),
	httpDone <-chan error,
) (*applicationBundle, error) {
	delay := cfg.DependencyRetry.MinDelay.Duration()
	maximum := cfg.DependencyRetry.MaxDelay.Duration()
	for {
		bundle, err := open(ctx, cfg, recorder, logger)
		if err == nil {
			if bundle == nil || bundle.service == nil || bundle.gate == nil {
				return nil, errors.New("alarmd runtime: v2 bundle factory returned an incomplete bundle")
			}
			return bundle, nil
		}
		var dependency *startupDependencyError
		if !errors.As(err, &dependency) {
			return nil, err
		}
		logger.Error(observability.StageStartup, observability.ResultFailed, 0, 0, slog.String("reason", "dependency"))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case httpErr := <-httpDone:
			if !timer.Stop() {
				<-timer.C
			}
			if httpErr == nil {
				httpErr = errHTTPServiceStopped
			}
			return nil, httpErr
		case <-timer.C:
		}
		if delay < maximum {
			delay *= 2
			if delay > maximum {
				delay = maximum
			}
		}
	}
}

func waitRuntimeComponent(done <-chan error, deadline time.Time) error {
	wait := time.Until(deadline)
	if wait <= 0 {
		select {
		case err := <-done:
			return err
		default:
			return ErrApplicationShutdownTimeout
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return ErrApplicationShutdownTimeout
	}
}

func normalizeRuntimeShutdownError(err error, stoppedBeforeShutdown bool) error {
	if err == nil || (errors.Is(err, context.Canceled) && !stoppedBeforeShutdown) {
		return nil
	}
	return fmt.Errorf("runtime component: %w", err)
}

func callWithin(ctx context.Context, operation func() error) error {
	if operation == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func defaultApplicationComponentFactories() applicationComponentFactories {
	return applicationComponentFactories{
		newEffectiveTime: phaseOneEffectiveTimeProvider,
		openRedis: func(options state.RedisBackendOptions) (redisRuntime, error) {
			return state.NewRedisBackend(options)
		},
		openTriggerEvents: func(coordinates enginekafka.DecisionSinkConfig) (triggerEventRuntime, error) {
			if err := coordinates.Validate(); err != nil {
				return nil, err
			}
			runtime, err := enginekafka.OpenTriggerEventSink(coordinates)
			if err != nil {
				return nil, retryableStartupDependency(err)
			}
			return runtime, nil
		},
		openReceipts: func(
			coordinates enginekafka.DecisionSinkConfig,
			limits enginekafka.ReceiptPublisherLimits,
		) (receiptRuntime, error) {
			if err := coordinates.Validate(); err != nil {
				return nil, err
			}
			runtime, err := enginekafka.OpenReceiptPublisher(coordinates, limits)
			if err != nil {
				return nil, retryableStartupDependency(err)
			}
			return runtime, nil
		},
		openEvaluationService: func(
			coordinates enginekafka.Config,
			router coordinator.MessageOutcomeRouter,
			critical coordinator.CriticalCompletion,
			receipts coordinator.ReceiptPublisher,
			gate *coordinator.CriticalDependencyGate,
			diagnostics enginekafka.EvaluationDiagnostics,
			retryConfig coordinator.DependencyRetryConfig,
			drainTimeout time.Duration,
		) (serviceRuntime, error) {
			if err := coordinates.Validate(); err != nil {
				return nil, err
			}
			runtime, err := enginekafka.OpenEvaluationService(
				coordinates, router, critical, receipts, gate, diagnostics, retryConfig, drainTimeout,
			)
			if err != nil {
				return nil, retryableStartupDependency(err)
			}
			return runtime, nil
		},
	}
}

func openApplicationBundle(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
) (*applicationBundle, error) {
	return openApplicationBundleWithFactories(ctx, cfg, recorder, logger, defaultApplicationComponentFactories())
}

func openApplicationBundleWithFactories(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	factories applicationComponentFactories,
) (*applicationBundle, error) {
	if ctx == nil || recorder == nil || logger == nil {
		return nil, errors.New("alarmd runtime: context, recorder and logger are required")
	}
	if factories.newEffectiveTime == nil || factories.openRedis == nil || factories.openTriggerEvents == nil || factories.openReceipts == nil ||
		factories.openEvaluationService == nil {
		return nil, errors.New("alarmd runtime: all v2 component factories are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	runtimeObserver := observability.Multi(recorder)
	resources, err := observability.NewResourceGovernor(observability.ResourceGovernorConfig{})
	if err != nil {
		return nil, err
	}
	resources.Observe(observability.ResourceSnapshot{
		ObservedAt: time.Now(), CPUCores: -1, RSSBytes: -1, HeapBytes: -1, GCPauseSeconds: -1,
		WorkerQueueDepth: -1, WorkerQueueBytes: -1, InflightMessages: -1, InflightBytes: -1,
		ConsumerLagRecords: -1, StateBytes: -1,
	})

	adapter := inputv2.New(cfg.ReaderLimits())
	if err := adapter.Validate(); err != nil {
		return nil, err
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		return nil, err
	}
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), detectObserver(runtimeObserver))
	if err != nil {
		return nil, err
	}
	codec, err := state.NewCodec(cfg.CodecLimits())
	if err != nil {
		return nil, err
	}
	semantics, err := state.RuntimeStateSemantics()
	if err != nil {
		return nil, err
	}

	redis, err := factories.openRedis(cfg.RedisBackendOptions())
	if err != nil {
		return nil, err
	}
	partial := &applicationBundle{redis: redis, logger: logger, resources: resources}
	cleanup := func(openErr error) (*applicationBundle, error) {
		deadline, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout.Duration())
		defer cancel()
		return nil, errors.Join(openErr, partial.Shutdown(deadline))
	}
	if err := redis.Ping(ctx); err != nil {
		return cleanup(retryableStartupDependency(err))
	}
	storageRouter, err := state.NewFixedRouter("phase-one", redis)
	if err != nil {
		return cleanup(err)
	}
	store, err := state.NewStore(cfg.StateStoreOptions(codec, storageRouter, stateObserver(runtimeObserver)))
	if err != nil {
		return cleanup(err)
	}
	gate := coordinator.NewCriticalDependencyGate(dependencyGateObserver(recorder, logger))
	partial.gate = gate
	retryingState, err := coordinator.NewRetryingRuntimeState(store, gate, cfg.DependencyRetryOptions())
	if err != nil {
		return cleanup(err)
	}
	effectiveTime := factories.newEffectiveTime()
	if effectiveTime == nil {
		return cleanup(errors.New("alarmd runtime: EffectiveTime provider is required"))
	}
	effectiveTime, err = coordinator.NewRetryingEffectiveTimeProvider(effectiveTime, gate, cfg.DependencyRetryOptions())
	if err != nil {
		return cleanup(err)
	}
	pipeline, err := coordinator.NewEvaluationPipeline(coordinator.PipelineOptions{
		Compiler:       compiler,
		Detector:       detector,
		EffectiveTime:  effectiveTime,
		State:          retryingState,
		StateAdmission: store,
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
			IdentitySchemaDigest:        semantics.IdentitySchemaDigest,
			SourceTimeSemanticsVersion:  semantics.SourceTimeSemanticsVersion,
			HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
		},
		DetectLimits: cfg.DetectLimits(), TriggerLimits: cfg.TriggerLimits(),
		Observer: runtimeObserver,
	})
	if err != nil {
		return cleanup(err)
	}
	router, err := coordinator.NewMessageRouter(observedMessageDecoder{next: adapter, observer: runtimeObserver}, pipeline)
	if err != nil {
		return cleanup(err)
	}
	events, err := factories.openTriggerEvents(cfg.Kafka.TriggerEventCoordinates())
	if err != nil {
		return cleanup(err)
	}
	observedEvents := &observedTriggerEventRuntime{next: events, observer: runtimeObserver}
	partial.triggerEvents = observedEvents
	critical, err := coordinator.NewCriticalCompleter(observedEvents, store)
	if err != nil {
		return cleanup(err)
	}
	retryingCritical, err := coordinator.NewRetryingCriticalPhaseCompletion(critical, gate, cfg.DependencyRetryOptions())
	if err != nil {
		return cleanup(err)
	}
	receipts, err := factories.openReceipts(cfg.Kafka.MessageReceiptCoordinates(), cfg.ReceiptPublisherLimits())
	if err != nil {
		var dependency *startupDependencyError
		if !errors.As(err, &dependency) {
			return cleanup(err)
		}
		receipts = &unavailableReceiptRuntime{err: err}
		observeReceiptUnavailable(recorder, logger)
	}
	partial.receipts = receipts
	consumerCoordinates := cfg.Kafka.ConsumerCoordinates()
	consumerCoordinates.Diagnostics = consumerDiagnostics(recorder, logger)
	service, err := factories.openEvaluationService(
		consumerCoordinates, router, &criticalCompletionRuntime{CriticalPhaseCompletion: retryingCritical}, receipts, gate,
		enginekafka.EvaluationDiagnostics{
			OnRejected: rejectedMessageDiagnostics(logger), OnOffsetCommitted: offsetCommitDiagnostics(runtimeObserver),
		},
		cfg.DependencyRetryOptions(),
		cfg.ShutdownTimeout.Duration(),
	)
	if err != nil {
		return cleanup(err)
	}
	partial.service = service
	if err := recorder.BindResources(resources); err != nil {
		return cleanup(err)
	}
	return partial, nil
}

func observeReceiptUnavailable(recorder observability.Observer, logger *observability.Logger) {
	observability.Multi(recorder).Observe(context.Background(), observability.Observation{
		Component:  observability.ComponentCoverage,
		Stage:      observability.StageReceiptQueued,
		Result:     observability.ResultDegraded,
		Operation:  observability.OperationProduce,
		Direction:  observability.DirectionOutput,
		ReasonCode: observability.ReasonCode(contract.ReasonKafkaUnavailable),
	})
	logger.Error(
		string(observability.StageReceiptQueued), observability.ResultDropped, 0, 0,
		slog.String("reason_code", contract.ReasonKafkaUnavailable),
		slog.Bool("coverage_acceptable", false),
		slog.String("recovery", "process_restart"),
	)
}

func dependencyGateObserver(recorder observability.Observer, logger *observability.Logger) coordinator.DependencyGateObserver {
	observer := observability.Multi(recorder)
	return func(transition coordinator.DependencyGateTransition) {
		blocker, added := changedDependencyBlocker(transition)
		result := observability.Result(observability.ResultResumed)
		if added {
			result = observability.ResultPaused
		}
		reason := observability.ReasonCode(blocker.ReasonCode)
		observer.Observe(context.Background(), observability.Observation{
			Component:  observability.ComponentState,
			Stage:      observability.StageDependencyLoaded,
			Result:     result,
			Operation:  observability.OperationTransition,
			Direction:  observability.DirectionInternal,
			ReasonCode: reason,
		})
		if logger == nil {
			return
		}
		attributes := []slog.Attr{
			slog.String("reason_code", string(reason)),
			slog.String("dependency", string(blocker.Dependency)),
			slog.Uint64("revision", transition.Current.Revision),
			slog.Int("blocker_count", len(transition.Current.Blockers)),
		}
		if result == observability.ResultPaused {
			logger.Error("dependency_gate", string(result), 0, 0, attributes...)
			return
		}
		logger.Info("dependency_gate", string(result), 0, 0, attributes...)
	}
}

func changedDependencyBlocker(transition coordinator.DependencyGateTransition) (coordinator.DependencyBlocker, bool) {
	for _, candidate := range transition.Current.Blockers {
		found := false
		for _, previous := range transition.Previous.Blockers {
			if candidate == previous {
				found = true
				break
			}
		}
		if !found {
			return candidate, true
		}
	}
	for _, candidate := range transition.Previous.Blockers {
		found := false
		for _, current := range transition.Current.Blockers {
			if candidate == current {
				found = true
				break
			}
		}
		if !found {
			return candidate, false
		}
	}
	return coordinator.DependencyBlocker{}, false
}

func phaseOneEffectiveTimeProvider() strategy.EffectiveTimeProvider {
	return strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(
		func(context.Context, string, string, string) (*time.Location, error) {
			return nil, strategy.ErrEffectiveTimeUnknown
		},
	))
}

func rejectedMessageDiagnostics(logger *observability.Logger) func(enginekafka.RejectedMessageEvidence) {
	return func(evidence enginekafka.RejectedMessageEvidence) {
		reason := "unknown"
		if len(evidence.ReasonCodes) > 0 {
			reason = evidence.ReasonCodes[0]
		}
		logger.Error(
			string(observability.StageRejected), observability.ResultSkipped, 1, 0,
			slog.String("topic", evidence.Topic), slog.Int("partition", int(evidence.Partition)),
			slog.Int64("offset", evidence.Offset), slog.String("reason", reason),
			slog.Int("reason_count", len(evidence.ReasonCodes)),
		)
	}
}

func consumerDiagnostics(recorder observability.Observer, logger *observability.Logger) enginekafka.ConsumerDiagnostics {
	observer := observability.Multi(recorder)
	return enginekafka.ConsumerDiagnostics{
		OnOffsetReset: func(event enginekafka.OffsetReset) {
			logger.Info(
				observability.StageOffsetReset, observability.ResultRecovered, 0, 0,
				slog.String("topic", event.Topic), slog.Int("partition", int(event.Partition)), slog.Int64("offset", event.Offset),
			)
		},
		OnConsumeRetry: func(event enginekafka.ConsumerRetry) {
			observer.Observe(context.Background(), observability.Observation{
				Component:  observability.ComponentConsumer,
				Stage:      observability.StageKafkaAssigned,
				Result:     observability.ResultRetrying,
				Operation:  observability.OperationConsume,
				Direction:  observability.DirectionInput,
				ReasonCode: observability.ReasonCode(contract.ReasonKafkaUnavailable),
			})
			logger.Error(
				"consume_retry", string(observability.ResultRetrying), 0, 0,
				slog.String("reason_code", contract.ReasonKafkaUnavailable),
				slog.String("source", string(event.Source)),
			)
		},
	}
}
