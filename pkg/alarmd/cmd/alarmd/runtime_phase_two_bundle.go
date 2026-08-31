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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type productionStrategySourceFactory func(
	redis.Cmdable,
	string,
) (controlplane.StrategySource, error)

func openProductionPhaseTwoBundle(
	ctx context.Context,
	cfg config.Config,
	recorder *metric.Recorder,
	logger *observability.Logger,
	health *phaseTwoApplicationHealth,
) (*phaseTwoWorkerBundle, error) {
	return openProductionPhaseTwoBundleWithStrategySource(
		ctx, cfg, recorder, logger, health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
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
	if ctx == nil || recorder == nil || logger == nil || health == nil || newStrategySource == nil {
		return nil, errors.New("phase-two production Bundle dependencies are incomplete")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !phaseTwoProductionBudgetsFitPlatform(cfg) {
		return nil, errors.New("phase-two production budgets overflow provider limits")
	}
	observer, err := newPhaseOneRuntimeObserver(recorder, logger)
	if err != nil {
		return nil, err
	}
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

	controlClient := redis.NewClient(productionRedisOptions(cfg.Redis))
	controlClosed := false
	defer func() {
		if resultErr != nil && !controlClosed {
			resultErr = errors.Join(resultErr, controlClient.Close())
		}
	}()
	if err := controlClient.Ping(ctx).Err(); err != nil {
		return nil, err
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
		controlClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), cfg.PhaseTwo.Control.CatalogTTL.Duration(),
	)
	if err != nil {
		return nil, err
	}
	reconciler, err := controlplane.NewSourceReconciler(repository)
	if err != nil {
		return nil, err
	}
	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, strategySemantics, time.Now)
	if err != nil {
		return nil, err
	}
	catalog, err := controlplane.NewRedisCatalogRuntime(
		repository, compiler, strategySemantics, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
	)
	if err != nil {
		return nil, err
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: strategySource, Planner: planner, Reconciler: reconciler, Activator: activator,
		Repository: repository, RefreshInterval: cfg.PhaseTwo.Control.RefreshInterval.Duration(),
		Wait: waitProductionControl,
		Close: func() error {
			controlClosed = true
			return controlClient.Close()
		},
	})
	if err != nil {
		return nil, err
	}

	ownershipStore, err := ownership.NewRedisStore(ownership.RedisStoreOptions{
		Address: cfg.Redis.Address, Username: cfg.Redis.Username, Password: cfg.Redis.Password, DB: cfg.Redis.DB,
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"), DialTimeout: cfg.Redis.DialTimeout.Duration(),
		ReadTimeout: cfg.Redis.ReadTimeout.Duration(), WriteTimeout: cfg.Redis.WriteTimeout.Duration(), PoolSize: cfg.Redis.PoolSize,
	})
	if err != nil {
		return nil, err
	}
	ownershipClosed := false
	defer func() {
		if resultErr != nil && !ownershipClosed {
			resultErr = errors.Join(resultErr, ownershipStore.Close())
		}
	}()
	if err := ownershipStore.Ping(ctx); err != nil {
		return nil, err
	}

	stateBackend, err := state.NewRedisBackend(cfg.RedisBackendOptions())
	if err != nil {
		return nil, err
	}
	stateClosed := false
	defer func() {
		if resultErr != nil && !stateClosed {
			resultErr = errors.Join(resultErr, stateBackend.Close())
		}
	}()
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
		Slots: catalog, Now: time.Now,
	})
	if err != nil {
		return nil, err
	}
	frozen, err := newProductionFrozenExecution(catalog, repository)
	if err != nil {
		return nil, err
	}
	queryClient, err := accessuq.NewClientWithLimits(
		cfg.PhaseTwo.Access.UQEndpoint, cfg.PhaseTwo.Access.QuerySource, &http.Client{}, phaseTwoUQLimits(cfg),
	)
	if err != nil {
		return nil, err
	}
	querySource, err := access.NewSource(frozen, queryClient, access.Config{
		MinReadyDelay: cfg.PhaseTwo.Access.MinReadyDelay.Duration(),
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
	events, err := enginekafka.OpenTriggerEventSink(cfg.Kafka.TriggerEventCoordinates())
	if err != nil {
		return nil, err
	}
	eventsClosed := false
	defer func() {
		if resultErr != nil && !eventsClosed {
			resultErr = errors.Join(resultErr, events.Close())
		}
	}()
	activation := productionPhaseTwoActivation{source: repository}
	admitter, err := ownership.NewAdmitter(ownershipStore, activation, time.Now)
	if err != nil {
		return nil, err
	}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: frozen, Activation: repository, Query: querySource, Sequencer: sequencer,
		Evaluator: evaluator, Admission: admitter, GapGuard: executionStore, Events: events,
		State: executionStore, Progress: progressStore, Observer: observer,
	}, worker.ProvisionalBudget{
		MaxSeries: cfg.PhaseTwo.Coordinator.MaxSeries, MaxRetainedBytes: cfg.PhaseTwo.Coordinator.MaxRetainedBytes,
		MaxStateMutations: cfg.PhaseTwo.Coordinator.MaxStateMutations, MaxEvents: cfg.PhaseTwo.Coordinator.MaxEvents,
		MaxGapMutations: cfg.PhaseTwo.Coordinator.MaxGapMutations,
	})
	if err != nil {
		return nil, err
	}
	productionOwnership, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: ownershipStore, WorkerID: cfg.PhaseTwo.Worker.ID, Catalog: catalog, Progress: progressStore,
		Executor: coordinator, Now: time.Now, ControlLeaderTTL: cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
		Observer: observer,
	})
	if err != nil {
		return nil, err
	}
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: productionOwnership, Observer: observer, Now: time.Now,
		CloseResources: func(shutdownCtx context.Context) error {
			eventsClosed = true
			stateClosed = true
			return errors.Join(events.Shutdown(shutdownCtx), stateBackend.Close())
		},
	})
	if err != nil {
		return nil, err
	}
	ownershipClosed = true
	return bundle, nil
}

func productionRedisOptions(redisConfig config.RedisConfig) *redis.Options {
	return &redis.Options{
		Addr: redisConfig.Address, Username: redisConfig.Username, Password: redisConfig.Password, DB: redisConfig.DB,
		DialTimeout: redisConfig.DialTimeout.Duration(), ReadTimeout: redisConfig.ReadTimeout.Duration(),
		WriteTimeout: redisConfig.WriteTimeout.Duration(), PoolSize: redisConfig.PoolSize,
	}
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
