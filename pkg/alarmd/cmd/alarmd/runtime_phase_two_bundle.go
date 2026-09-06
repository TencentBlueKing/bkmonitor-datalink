// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type productionStrategySourceFactory func(
	redis.Cmdable,
	string,
) (controlplane.StrategySource, error)

type productionPhaseTwoEventSink interface {
	execution.EventSink
	Shutdown(context.Context) error
	Close() error
}

type phaseTwoProductionExternalDependencies struct {
	Now                func() time.Time
	HTTPClient         *http.Client
	OpenEvents         func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error)
	AdditionalObserver observability.Observer
	OpenFinalEvidence  func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits, enginekafka.ReceiptPublisherDiagnostics) (phaseTwoFinalPublisher, error)
}

func defaultPhaseTwoProductionExternalDependencies() phaseTwoProductionExternalDependencies {
	return phaseTwoProductionExternalDependencies{
		Now: time.Now, HTTPClient: &http.Client{},
		OpenEvents: func(coordinates enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
			return enginekafka.OpenTriggerEventSink(coordinates)
		},
	}
}

func openProductionPhaseTwoBundle(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	health *phaseTwoApplicationHealth,
) (*phaseTwoWorkerBundle, error) {
	return openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder, logger, health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		defaultPhaseTwoProductionExternalDependencies(),
	)
}

func openProductionPhaseTwoBundleWithStrategySource(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	health *phaseTwoApplicationHealth,
	newStrategySource productionStrategySourceFactory,
) (_ *phaseTwoWorkerBundle, resultErr error) {
	return openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder, logger, health, newStrategySource,
		defaultPhaseTwoProductionExternalDependencies(),
	)
}

func openProductionPhaseTwoBundleWithDependencies(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	health *phaseTwoApplicationHealth,
	newStrategySource productionStrategySourceFactory,
	external phaseTwoProductionExternalDependencies,
) (_ *phaseTwoWorkerBundle, resultErr error) {
	if ctx == nil || recorder == nil || logger == nil || health == nil || newStrategySource == nil ||
		external.Now == nil || external.HTTPClient == nil || external.OpenEvents == nil {
		return nil, errors.New("phase-two production Bundle dependencies are incomplete")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	manifest, err := loadPhaseTwoShadowManifest(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if !phaseTwoProductionBudgetsFitPlatform(cfg) {
		return nil, errors.New("phase-two production budgets overflow provider limits")
	}
	observer, err := newPhaseOneRuntimeObserver(recorder, logger)
	if err != nil {
		return nil, err
	}
	targetFlow, err := observability.NewTargetFlow(logger, cfg.PhaseTwo.TargetFlow)
	if err != nil {
		return nil, err
	}
	observer = observability.Multi(observer, external.AdditionalObserver, targetFlow)
	observer = phaseTwoRuntimeObserver(observer)
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		return nil, err
	}
	stateSemantics, err := state.RuntimeStateSemantics()
	if err != nil {
		return nil, err
	}
	strategySemantics := strategy.StateSemantics{
		StateSchemaVersion:          stateSemantics.StateSchemaVersion,
		CodecSemanticsVersion:       stateSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest:        stateSemantics.IdentitySchemaDigest,
		SourceTimeSemanticsVersion:  stateSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: stateSemantics.HistoryCellSemanticsVersion,
	}

	sourceConnection := cfg.StrategySourceRedis()
	runtimeConnection := cfg.ResolvedRuntimeRedis()
	controlClient, err := openProductionRedis(ctx, sourceConnection)
	if err != nil {
		return nil, err
	}
	controlClosed := false
	defer func() {
		if resultErr != nil && !controlClosed {
			resultErr = errors.Join(resultErr, controlClient.Close())
		}
	}()
	runtimeClient := controlClient
	runtimeClientIsSource := reflect.DeepEqual(runtimeConnection, sourceConnection)
	if !runtimeClientIsSource {
		runtimeClient, err = openProductionRedis(ctx, runtimeConnection)
		if err != nil {
			return nil, err
		}
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, runtimeClient.Close())
			}
		}()
	}
	strategySource, err := newStrategySource(controlClient, cfg.PhaseTwo.Control.StrategyCachePrefix)
	if err != nil {
		return nil, err
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler(
		execution.ProviderRouteRef(cfg.PhaseTwo.Control.ProviderRoute),
		cfg.PhaseTwo.Control.Timezone,
		phaseTwoLegacyQueryRuntimeFacts(cfg.PhaseTwo.Control.LegacyQueryRuntime),
	)
	if err != nil {
		return nil, err
	}
	repository, err := controlplane.NewRedisCatalogRepository(
		runtimeClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), cfg.PhaseTwo.Control.CatalogTTL.Duration(),
	)
	if err != nil {
		return nil, err
	}
	if err := repository.ConfigureLegacyMigration(cfg.PhaseTwo.Control.LegacyMigrationMaxScanKeys, cfg.PhaseTwo.Control.LegacyMigrationTimeout.Duration()); err != nil {
		return nil, err
	}
	repository.ConfigureObserver(observer)
	if cfg.PhaseTwo.Control.CatalogTTL.Duration() < phaseTwoSnapshotMinimumRetention(cfg, 0) {
		return nil, scheduler.ErrSnapshotRetentionInsufficient
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, strategySemantics,
		phaseTwoCatalogRetentionValidator(cfg))
	if err != nil {
		return nil, err
	}
	catalog, err := controlplane.NewRedisCatalogRuntime(
		repository, compiler, strategySemantics, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
	)
	if err != nil {
		return nil, err
	}
	ownershipStore, err := ownership.NewRedisStoreWithClient(
		runtimeClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"),
	)
	if err != nil {
		return nil, err
	}
	if err := ownershipStore.Ping(ctx); err != nil {
		return nil, err
	}

	stateBackend, err := state.NewRedisBackendWithClient(productionRedisAddress(runtimeConnection), runtimeClient)
	if err != nil {
		return nil, err
	}
	if err := stateBackend.Ping(ctx); err != nil {
		return nil, err
	}
	storageRouter, err := state.NewFixedRouter("phase-two-primary", stateBackend)
	if err != nil {
		return nil, err
	}
	executionStore, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: cfg.Redis.StatePrefix, Router: storageRouter, MaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes,
		MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch, RuntimeTTL: cfg.Redis.MaxTTL.Duration(),
	})
	if err != nil {
		return nil, err
	}
	progressStore, err := progress.NewStore(progress.StoreOptions{
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), Control: ownershipStore,
		Slots: catalog, Now: external.Now,
	})
	if err != nil {
		return nil, err
	}
	activator, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, strategySemantics, progressStore, external.Now,
	)
	if err != nil {
		return nil, err
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: strategySource, Planner: planner, Reconciler: reconciler, Activator: activator,
		Repository: repository, Schedules: catalog, Progress: progressStore,
		Observer:        observer,
		RefreshInterval: cfg.PhaseTwo.Control.RefreshInterval.Duration(), Wait: waitProductionControl,
		Close: func() error {
			controlClosed = true
			return controlClient.Close()
		},
	})
	if err != nil {
		return nil, err
	}
	frozen, err := newProductionFrozenExecution(catalog, repository, external.Now)
	if err != nil {
		return nil, err
	}
	queryClient, err := accessuq.NewClientWithLimits(
		cfg.PhaseTwo.Access.UQEndpoint, cfg.PhaseTwo.Access.QuerySource, external.HTTPClient, phaseTwoUQLimits(cfg),
	)
	if err != nil {
		return nil, err
	}
	recoveryLimits := cfg.PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(recoveryLimits, external.Now, observer)
	if err != nil {
		return nil, err
	}
	var finalEmitter *phaseTwoFinalEmitter
	var frozenSource access.FrozenPlanSource = frozen
	if manifest != nil {
		finalEmitter = &phaseTwoFinalEmitter{manifest: *manifest, observer: observer, maxEvents: cfg.PhaseTwo.Coordinator.MaxEvents}
		frozenSource = phaseTwoShadowResolver{next: frozen}
	}
	querySource, err := access.NewSource(frozenSource, queryClient, productionQueryPermitAcquirer{flights: flights}, access.Config{
		MinReadyDelay: cfg.PhaseTwo.Access.MinReadyDelay.Duration(),
		Now:           external.Now,
		Observer:      observer,
	})
	if err != nil {
		return nil, err
	}
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), detectObserver(observer))
	if err != nil {
		return nil, err
	}
	evaluator, err := evaluation.New(detector, evaluation.Limits{
		MaxPlans: cfg.Limits.Detect.MaxPlans, MaxRecords: cfg.Limits.Detect.MaxRecordsPerSeries,
		MaxLevels: uint64(cfg.Limits.Compiler.MaxLevelsPerPlan), Trigger: cfg.TriggerLimits(),
	})
	if err != nil {
		return nil, err
	}
	sequencer, err := worker.NewKeyedSideEffectSequencer(cfg.PhaseTwo.Coordinator.MaxSequencerReservations)
	if err != nil {
		return nil, err
	}
	events, err := external.OpenEvents(cfg.Kafka.TriggerEventCoordinates())
	if err != nil {
		return nil, err
	}
	eventsClosed := false
	defer func() {
		if resultErr != nil && !eventsClosed {
			resultErr = errors.Join(resultErr, events.Close())
		}
	}()
	var evaluatorPort execution.Evaluator = evaluator
	var eventsPort execution.EventSink = events
	if finalEmitter != nil {
		openFinal := external.OpenFinalEvidence
		if openFinal == nil {
			openFinal = func(c enginekafka.DecisionSinkConfig, l enginekafka.ReceiptPublisherLimits, d enginekafka.ReceiptPublisherDiagnostics) (phaseTwoFinalPublisher, error) {
				return enginekafka.OpenReceiptPublisherWithDiagnostics(c, l, d)
			}
		}
		coordinates := cfg.Kafka.TriggerEventCoordinates()
		coordinates.OutputTopic = manifest.GoTopic.Name
		diagnostics := enginekafka.ReceiptPublisherDiagnostics{OnACKed: func(count uint64) {
			finalEmitter.observeCount(context.Background(), observability.StageFinalEvidenceACKed, observability.ResultSuccess, count)
		}, OnDrop: func(drop enginekafka.ReceiptDropEvidence) {
			finalEmitter.observeCount(context.Background(), observability.StageFinalEvidenceDropped, observability.ResultFailed, drop.Count)
		}}
		finalEmitter.publisher, err = openFinal(coordinates, enginekafka.ReceiptPublisherLimits{MaxQueuedMessages: int(manifest.Limits.MaxQueueEntries), MaxQueuedBytes: int(manifest.Limits.MaxQueueBytes)}, diagnostics)
		if err != nil {
			finalEmitter.observe(ctx, observability.StageFinalEvidenceDropped, observability.ResultFailed)
		}
		defer func() {
			if resultErr != nil && finalEmitter.publisher != nil {
				finalEmitter.shutdown(ctx)
			}
		}()
		evaluatorPort = phaseTwoShadowEvaluator{next: evaluator, emitter: finalEmitter}
		eventsPort = phaseTwoShadowEventSink{productionPhaseTwoEventSink: events, emitter: finalEmitter}
	}
	activation := productionPhaseTwoActivation{source: repository}
	admitter, err := ownership.NewAdmitter(ownershipStore, activation, external.Now)
	if err != nil {
		return nil, err
	}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: frozen, Activation: repository, Query: querySource, Sequencer: sequencer,
		Evaluator: evaluatorPort, Admission: admitter, GapGuard: executionStore, Events: eventsPort,
		State: executionStore, Progress: progressStore, Observer: observer,
	}, worker.ProvisionalBudget{
		MaxSeries: cfg.PhaseTwo.Coordinator.MaxSeries, MaxRetainedBytes: cfg.PhaseTwo.Coordinator.MaxRetainedBytes,
		MaxStateMutations: cfg.PhaseTwo.Coordinator.MaxStateMutations, MaxEvents: cfg.PhaseTwo.Coordinator.MaxEvents,
		MaxGapMutations: cfg.PhaseTwo.Coordinator.MaxGapMutations,
	})
	if err != nil {
		return nil, err
	}
	registration, err := phaseTwoWorkerRegistration(cfg, ownership.WorkerStarting, external.Now())
	if err != nil {
		return nil, err
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(registration.Compatibility())
	if err != nil {
		return nil, err
	}
	assignmentReconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), ownershipStore)
	if err != nil {
		return nil, err
	}
	var executor scheduler.Executor = coordinator
	if finalEmitter != nil {
		executor = phaseTwoShadowExecutor{next: coordinator, emitter: finalEmitter}
	}
	productionOwnership, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: ownershipStore, WorkerID: cfg.PhaseTwo.Worker.ID, Catalog: catalog, Progress: progressStore,
		Executor: executor, Now: external.Now, ControlLeaderTTL: cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
		Observer: observer, Reconcile: assignmentReconciler, Flights: flights, RecoveryLimits: recoveryLimits,
		PostRecoveryTerminalDelay: phaseTwoPostRecoveryTerminalDelay(cfg),
		QueryDeadlineReserve:      cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
		SnapshotRetention:         cfg.PhaseTwo.Control.CatalogTTL.Duration(),
		PublicationDelayAllowance: phaseTwoPublicationDelayAllowance(cfg),
	})
	if err != nil {
		return nil, err
	}
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: productionOwnership,
		Recorder: recorder, Observer: observer, TargetFlow: targetFlow, Now: external.Now,
		CloseResources: func(shutdownCtx context.Context) error {
			eventsClosed = true
			if finalEmitter != nil && finalEmitter.publisher != nil {
				finalEmitter.shutdown(shutdownCtx)
			}
			if runtimeClientIsSource {
				return events.Shutdown(shutdownCtx)
			}
			return errors.Join(events.Shutdown(shutdownCtx), runtimeClient.Close())
		},
	})
	if err != nil {
		return nil, err
	}
	return bundle, nil
}

func phaseTwoPostRecoveryTerminalDelay(cfg config.Config) time.Duration {
	takeover := maxDuration(cfg.PhaseTwo.Worker.RegistrationTTL.Duration(), cfg.PhaseTwo.Ownership.LeaseTTL.Duration()) +
		cfg.PhaseTwo.Control.ReconcileInterval.Duration() + cfg.PhaseTwo.Scheduler.TickInterval.Duration()
	drain := cfg.ShutdownTimeout.Duration() + cfg.PhaseTwo.Ownership.LeaseTTL.Duration() +
		cfg.PhaseTwo.Control.ReconcileInterval.Duration() + cfg.PhaseTwo.Scheduler.TickInterval.Duration()
	terminal := maxDuration(cfg.PhaseTwo.Scheduler.TickInterval.Duration()+cfg.PhaseTwo.Control.ReconcileInterval.Duration(), takeover, drain)
	safety := maxDuration(cfg.PhaseTwo.Control.RefreshInterval.Duration(), cfg.PhaseTwo.Ownership.LeaseRenewInterval.Duration(),
		cfg.PhaseTwo.Worker.RegistrationRenewInterval.Duration(), cfg.PhaseTwo.Scheduler.RetryMaxDelay.Duration())
	return terminal + safety
}

func phaseTwoPublicationDelayAllowance(cfg config.Config) time.Duration {
	return cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration() + 2*cfg.PhaseTwo.Control.RefreshInterval.Duration()
}

func phaseTwoSnapshotMinimumRetention(cfg config.Config, queryDeadlineOffset time.Duration) time.Duration {
	return phaseTwoPublicationDelayAllowance(cfg) + queryDeadlineOffset +
		cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration() + phaseTwoPostRecoveryTerminalDelay(cfg)
}

func phaseTwoCatalogRetentionValidator(cfg config.Config) func(controlplane.Catalog) error {
	return func(catalog controlplane.Catalog) error {
		var maximumOffset time.Duration
		for _, group := range catalog.QueryGroups {
			for _, plan := range group.Plans {
				offset := time.Duration(plan.ScheduleSpec.CompletionOffsetSeconds())*time.Second -
					cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
				if offset <= 0 {
					return scheduler.ErrSnapshotRetentionInsufficient
				}
				if offset > maximumOffset {
					maximumOffset = offset
				}
			}
		}
		if cfg.PhaseTwo.Control.CatalogTTL.Duration() < phaseTwoSnapshotMinimumRetention(cfg, maximumOffset) {
			return scheduler.ErrSnapshotRetentionInsufficient
		}
		return nil
	}
}

func maxDuration(values ...time.Duration) time.Duration {
	var maximum time.Duration
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func productionPhaseTwoPrefix(prefix, component string) string {
	return prefix + ":phase-two:" + component
}

func phaseTwoUQLimits(cfg config.Config) accessuq.Limits {
	maximumBody := int64(cfg.PhaseTwo.Coordinator.MaxRetainedBytes)
	maximumSeries := maximumBody
	if defaults := accessuq.DefaultLimits(); defaults.MaxSeriesBytes < maximumSeries {
		maximumSeries = defaults.MaxSeriesBytes
	}
	maximumRecords := cfg.PhaseTwo.Coordinator.MaxSeries * cfg.Limits.Detect.MaxRecordsPerSeries
	return accessuq.Limits{
		MaxBodyBytes: maximumBody, MaxSeriesBytes: maximumSeries,
		MaxSeries: cfg.PhaseTwo.Coordinator.MaxSeries, MaxRecords: maximumRecords,
	}
}

func waitProductionControl(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return errors.New("phase-two Control refresh delay must be positive")
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func phaseTwoProductionBudgetsFitPlatform(cfg config.Config) bool {
	return cfg.PhaseTwo.Coordinator.MaxRetainedBytes <= math.MaxInt64 &&
		cfg.PhaseTwo.Coordinator.MaxSeries <= math.MaxUint64/cfg.Limits.Detect.MaxRecordsPerSeries
}
