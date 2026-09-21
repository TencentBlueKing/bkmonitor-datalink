// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"reflect"
	"time"

	"github.com/go-redis/redis/v8"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/evaluation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type productionStrategySourceFactory func(
	redis.Cmdable,
	string,
) (controlplane.StrategySource, error)

type productionPhaseTwoEventSink interface {
	execution.EventSink
	ConfigureLegacyOutput(enginekafka.LegacyEventConverter, string, int) error
	ConfigureStandardOutput(enginekafka.StandardEventConverter) error
	// ProtocolNegotiation is what the sink and its brokers agreed to speak
	// when it opened, or nil for a sink that asked nobody. Required rather
	// than optional so a sink that forgets it is a compile error, not a
	// dependency entry that quietly reads "not asked yet" forever.
	ProtocolNegotiation() *enginekafka.ProtocolNegotiation
	Shutdown(context.Context) error
	Close() error
}

type phaseTwoProductionExternalDependencies struct {
	// Now is the clock the bundle schedules and freezes by. It is not the
	// clock every deadline is measured by: access/uq Client.Execute and
	// worker/completion_deadline.go turn a Slot's deadline, derived from this
	// clock, into context.WithDeadline, and Go measures that against the real
	// clock. In production the two agree. A test that injects a clock must
	// only ever set it ahead of the real clock: a Slot whose deadline lies in
	// the real past has its query context expire on entry, and the failure
	// reads as a query timeout rather than as the clock skew it is.
	Now        func() time.Time
	HTTPClient *http.Client
	// PrepareEvents validates the output coordinates and returns the opener
	// that connects; validation errors refuse startup, the opener's errors
	// are retried while the replica reports itself not ready. OpenEvents is
	// the older hook that does both at once; a test that sets only it gets
	// an opener that calls it, so its sink is opened on the first attempt.
	PrepareEvents      func(enginekafka.DecisionSinkConfig) (outputSinkOpener, error)
	OpenEvents         func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error)
	AdditionalObserver observability.Observer
}

func defaultPhaseTwoProductionExternalDependencies() phaseTwoProductionExternalDependencies {
	return phaseTwoProductionExternalDependencies{
		Now: time.Now, HTTPClient: newPhaseTwoUQHTTPClient(),
		PrepareEvents: func(coordinates enginekafka.DecisionSinkConfig) (outputSinkOpener, error) {
			opener, err := enginekafka.PrepareTriggerEventSink(coordinates)
			if err != nil {
				return nil, err
			}
			return outputSinkOpenerFunc(func() (productionPhaseTwoEventSink, error) { return opener.Open() }), nil
		},
	}
}

// prepareEvents resolves whichever hook the dependencies carry into an opener.
func (external phaseTwoProductionExternalDependencies) prepareEvents(coordinates enginekafka.DecisionSinkConfig) (outputSinkOpener, error) {
	if external.PrepareEvents != nil {
		return external.PrepareEvents(coordinates)
	}
	return outputSinkOpenerFunc(func() (productionPhaseTwoEventSink, error) { return external.OpenEvents(coordinates) }), nil
}

// phaseTwoUQDialer bounds connection establishment to the query provider and
// keeps established connections alive between Slot queries.
func phaseTwoUQDialer() *net.Dialer {
	return &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
}

// newPhaseTwoUQHTTPClient returns the client used for query provider
// requests. The zero-value http.Client kept only two idle connections per
// host, so with P concurrent Slot queries most connections were torn down and
// re-established after every request. The pool below covers the P/R profiles
// in use with headroom. Requests are bounded by the per-attempt context
// deadline, so no client-level or response-header timeout is set.
func newPhaseTwoUQHTTPClient() *http.Client {
	dialer := phaseTwoUQDialer()
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 0,
	}}
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
		external.Now == nil || external.HTTPClient == nil || (external.OpenEvents == nil && external.PrepareEvents == nil) {
		return nil, errors.New("phase-two production Bundle dependencies are incomplete")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !phaseTwoProductionBudgetsFitPlatform(cfg) {
		return nil, errors.New("phase-two production budgets overflow provider limits")
	}
	// Phase two limits repeated diagnostics per (reason, Query Group) bucket
	// and reports suppressed counts; the phase-one per-reason budget hid every
	// other Query Group's coordinates once one object became noisy.
	baseObserver, err := newPhaseTwoRuntimeObserver(recorder, logger)
	if err != nil {
		return nil, err
	}
	// The tracker reads the stream the replica already emits, so building the
	// anomaly list costs no reads of its own. It forwards every observation
	// untouched: diagnostics must not change what the pipeline reports.
	fleetTracker := fleet.NewTracker(baseObserver, cfg.PhaseTwo.Worker.ID, external.Now)
	var observer observability.Observer = fleetTracker
	targetFlow, err := observability.NewTargetFlow(logger)
	if err != nil {
		return nil, err
	}
	// Counted here rather than read back from the metric: the page answers from
	// the replica's snapshot, so "no budget refused anything" must be sayable
	// without collection having run.
	rejectionTally := fleet.NewRejectionTally()
	// Counted in the replica's own snapshot rather than read back from a
	// metric, for the same reason the rejections are: the page answers from the
	// snapshot and has to be right one minute after a restart.
	seriesPullTally := fleet.NewSeriesPullTally()
	observationCapacity := config.DeriveObservationCapacity(config.DetectCapacityInputs(), cfg.PhaseTwo.Observation)
	costSummary := observability.NewCostSummary(observationCostOptions(observationCapacity, fmt.Sprintf("%s:%d", cfg.PhaseTwo.Worker.ID, external.Now().UnixNano()), external.Now))
	observer = observability.Multi(observer, external.AdditionalObserver, targetFlow, rejectionTally, costSummary)
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
	runtimeConnection := cfg.RuntimeStoreRedis()
	cmdbConnection := cfg.CMDBCacheRedis()
	controlClient, err := openProductionRedisWithHook(ctx, sourceConnection, recorder.RedisHook("source"))
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
	sharing := endpointSharing{runtimeIsSource: runtimeClientIsSource, compatOutputPresent: true}
	if !runtimeClientIsSource {
		runtimeClient, err = openProductionRedisWithHook(ctx, runtimeConnection, recorder.RedisHook("runtime"))
		if err != nil {
			return nil, err
		}
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, runtimeClient.Close())
			}
		}()
	}
	// The platform's host cache is a third location, and whether it is a third
	// connection is the deployment's answer, not an assumption: the platform
	// routes its cache backend per module, so this may be the instance the
	// strategy cache is on, the one alarmd's own state is on, or neither.
	cmdbClient := controlClient
	cmdbClientOwned := false
	switch {
	case reflect.DeepEqual(cmdbConnection, sourceConnection):
		sharing.cmdbSharedWith = fleet.EndpointStrategyCache
	case reflect.DeepEqual(cmdbConnection, runtimeConnection):
		cmdbClient = runtimeClient
		sharing.cmdbSharedWith = fleet.EndpointStateRedis
	default:
		cmdbClient, err = openProductionRedisWithHook(ctx, cmdbConnection, recorder.RedisHook("cmdb"))
		if err != nil {
			return nil, err
		}
		cmdbClientOwned = true
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, cmdbClient.Close())
			}
		}()
	}
	// The platform's dynamic configuration is a fourth location, rendered or
	// absent: absent means the deployment has no distribution to read and the
	// settings copy says so, rather than reading some other instance as
	// "nothing published".
	var dynamicConfigClient redis.UniversalClient
	dynamicConfigClientOwned := false
	dynamicConfigConnection, dynamicConfigConfigured := cfg.DynamicConfigRedis()
	if dynamicConfigConfigured {
		sharing.dynamicConfigured = true
		switch {
		case reflect.DeepEqual(dynamicConfigConnection, sourceConnection):
			dynamicConfigClient = controlClient
			sharing.dynamicSharedWith = fleet.EndpointStrategyCache
		case reflect.DeepEqual(dynamicConfigConnection, runtimeConnection):
			dynamicConfigClient = runtimeClient
			sharing.dynamicSharedWith = fleet.EndpointStateRedis
		case reflect.DeepEqual(dynamicConfigConnection, cmdbConnection):
			dynamicConfigClient = cmdbClient
			sharing.dynamicSharedWith = fleet.EndpointCMDBCache
		default:
			opened, err := openProductionRedisWithHook(ctx, dynamicConfigConnection, recorder.RedisHook("dynamic_config"))
			if err != nil {
				return nil, err
			}
			dynamicConfigClient = opened
			dynamicConfigClientOwned = true
			defer func() {
				if resultErr != nil {
					resultErr = errors.Join(resultErr, opened.Close())
				}
			}()
		}
	}
	// Report each pool by its role. Where two roles resolve to one connection
	// there is a single client, and reporting it twice would double count the
	// same connections.
	recorder.SetRedisPoolSource(func() []metric.RedisPoolCounts {
		counts := []metric.RedisPoolCounts{redisPoolCounts("source", sourceConnection.PoolSize, controlClient)}
		if !runtimeClientIsSource {
			counts = append(counts, redisPoolCounts("runtime", runtimeConnection.PoolSize, runtimeClient))
		}
		if cmdbClientOwned {
			counts = append(counts, redisPoolCounts("cmdb", cmdbConnection.PoolSize, cmdbClient))
		}
		if dynamicConfigClientOwned {
			counts = append(counts, redisPoolCounts("dynamic_config", dynamicConfigConnection.PoolSize, dynamicConfigClient))
		}
		return counts
	})
	// The platform's settings this process evaluates by, read once here so the
	// compiler and the admission filters start on the platform's word where
	// there is one, then kept current by the runtime once a minute.
	platformSettings, err := buildPlatformSettings(ctx, cfg, dynamicConfigClient, external.Now)
	if err != nil {
		return nil, err
	}
	recorder.SetPlatformSettingsSource(platformSettings.Stats)
	hostStatus := newDynamicHostStatusFilter(platformSettings.Current().HostDisableMonitorStates)
	strategySource, err := newStrategySource(controlClient, cfg.PhaseTwo.Control.StrategyCachePrefix)
	if err != nil {
		return nil, err
	}
	// The compiler reads the copy when each control round opens, so a
	// setting the platform changes reaches the plans on the next round:
	// every strategy recompiles under it and the cutover carries the new
	// Catalog out. Within a round the facts are frozen.
	planner, err := newPlatformBoundPlanner(cfg, platformSettings)
	if err != nil {
		return nil, err
	}
	repository, err := controlplane.NewRedisCatalogRepository(
		runtimeClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg),
	)
	if err != nil {
		return nil, err
	}
	if err := repository.ConfigureLegacyMigration(cfg.PhaseTwo.Control.LegacyMigrationMaxScanKeys, cfg.PhaseTwo.Control.LegacyMigrationTimeout.Duration()); err != nil {
		return nil, err
	}
	if err := repository.ConfigureDrainingTermination(cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration()); err != nil {
		return nil, err
	}
	// The cutover prunes a closed Schedule Segment only when the scheduler's
	// own arithmetic says no Slot in it is read anymore. The three durations
	// that arithmetic takes are resolved once here and handed to both sides
	// from the same values, so a change to how one side takes them cannot
	// leave the other on the old reading.
	recoveryLimits := cfg.PhaseTwo.Scheduler.RecoveryLimits()
	retention := execution.SlotRetention{
		QueryReserve:  cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
		MaxReplayAge:  recoveryLimits.MaxReplayAge,
		TerminalDelay: phaseTwoPostRecoveryTerminalDelay(cfg),
	}
	if err := repository.ConfigureSegmentRetention(retention); err != nil {
		return nil, err
	}
	// The timeline cache budget is derived here rather than inside the
	// repository: the derivation belongs to the container, and the control
	// plane package does not depend on configuration.
	timelineCache := config.DeriveControlTimelineCache(config.DetectCapacityInputs())
	if err := repository.ConfigureControlTimelineCache(timelineCache.MaxEntries, timelineCache.MaxBytes); err != nil {
		return nil, err
	}
	// Catalog objects are a few kilobytes each and immutable; they take the
	// same container-derived budget as the timelines they are read next to.
	if err := repository.ConfigureObjectCache(timelineCache.MaxEntries, timelineCache.MaxBytes); err != nil {
		return nil, err
	}
	repository.ConfigureObserver(observer)
	// The cache counters are what said a decoded-timeline cache was worth
	// building, and nothing consumed them before. The timeline occupancy joins
	// them so the derived budget can be read against the working set it was
	// sized for: entries against the Query Groups this Worker owns, evictions
	// against a version header that is not moving.
	recorder.SetControlCacheSource(func() []metric.ControlCacheCounts {
		stats := repository.ControlReadCacheStats()
		occupancy := stats.TimelineOccupancy
		counts := []metric.ControlCacheCounts{
			{Object: "version", Hits: stats.Version.Hits, Misses: stats.Version.Misses, Refreshes: stats.Version.Refreshes},
			{Object: "activation", Hits: stats.Activation.Hits, Misses: stats.Activation.Misses, Refreshes: stats.Activation.Refreshes},
			// A skipped revision is reported as a clear: the cache dropped
			// everything because a Worker fell more than one publication
			// behind, which the delta design assumes does not happen.
			{Object: "activation_delta", Hits: stats.Delta.Hits, Misses: stats.Delta.Misses, Clears: stats.DeltaSkips,
				Audit: &metric.ControlCacheAudit{
					Samples: stats.DeltaAudit.Samples, Agreed: stats.DeltaAudit.Agreed,
					OverNamed: stats.DeltaAudit.OverNamed, Missed: stats.DeltaAudit.Missed,
				}},
			{Object: "catalog_index", Hits: stats.Index.Hits, Misses: stats.Index.Misses},
			{Object: "timeline", Hits: stats.Timeline.Hits, Misses: stats.Timeline.Misses,
				Refreshes: stats.Timeline.Refreshes, Evictions: occupancy.Evictions,
				Occupancy: &metric.ControlCacheOccupancy{
					Entries: float64(occupancy.Entries), Bytes: float64(occupancy.Bytes),
					BytesLimit: float64(occupancy.MaxBytes),
				}},
		}
		// The key segment memos are caches too, and they report through the same
		// metric rather than a new one. Their interesting outcome is the clear:
		// the memo drops everything on reaching its bound because its design
		// assumes the populations stay inside it, so a clear is that assumption
		// failing rather than a cache being warmed. The two domains differ in how
		// safe that assumption is - a tenant population is bounded by
		// configuration, a state generation advances with every publish - so they
		// are reported apart.
		//
		// Occupancy is deliberately not reported for them. Their bound is in
		// entries rather than bytes, and an entry count that resets to zero on
		// every clear says more about when the page was scraped than about the
		// memo. The clear counter does not have that problem.
		for _, memo := range state.KeySegmentMemoCounts() {
			counts = append(counts, metric.ControlCacheCounts{
				Object: memo.Domain,
				Hits:   memo.Hits, Misses: memo.Misses, Clears: memo.Clears,
			})
		}
		return counts
	})
	if phaseTwoCatalogRetention(cfg) < phaseTwoSnapshotMinimumRetention(cfg, 0) {
		return nil, scheduler.ErrSnapshotRetentionInsufficient
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, strategySemantics,
		phaseTwoCatalogRetentionAdmission(cfg))
	if err != nil {
		return nil, err
	}
	if err := reconciler.ConfigureOutputProtocol(cfg.OutputProtocol()); err != nil {
		return nil, err
	}
	if err := reconciler.ConfigureClock(external.Now); err != nil {
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
		MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch,
		// The store derives each write TTL from the Plan retention the request
		// carries; these only bound it, exactly as they do for the phase-one
		// store built in config.StoreOptions.
		MinTTL: cfg.Redis.MinTTL.Duration(), MaxTTL: cfg.Redis.MaxTTL.Duration(),
		RestartMargin: cfg.Redis.RestartMargin.Duration(),
		// Runtime State writes verify the owner lease inside Redis; without the
		// resolver the store would fall back to unfenced batched writes.
		FenceKeys: ownershipStore,
	})
	if err != nil {
		return nil, err
	}
	// A worker that has forgotten the key lives it remembered is back to one
	// renewal round trip per Plan per Slot, and nothing else in the process
	// says so: the Slots keep passing and the keys keep being renewed.
	recorder.SetRenewalGateSource(executionStore.RenewalGateResets)
	progressStore, err := progress.NewStore(progress.StoreOptions{
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), Control: ownershipStore,
		Slots: catalog, Now: external.Now, Observer: observer,
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
		Recorder:        recorder,
		RefreshInterval: cfg.PhaseTwo.Control.RefreshInterval.Duration(), Wait: waitProductionControl,
		Now: external.Now, MaxReplayAge: cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
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
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(recoveryLimits, external.Now, observer)
	if err != nil {
		return nil, err
	}
	// A series is evaluated for a strategy only inside that strategy's
	// monitoring target. The facts it is decided on come from the platform's
	// CMDB host cache, on the database this client already uses.
	seriesAdmission, cmdbIndex, err := buildSeriesAdmission(ctx, cfg, cmdbClient, recorder, logger, hostStatus)
	if err != nil {
		return nil, err
	}
	// The index outlives the constructor's context and is stopped with the
	// rest of the Bundle's resources.
	cmdbIndexCtx, stopCMDBIndex := context.WithCancel(context.Background())
	defer func() {
		if resultErr != nil {
			stopCMDBIndex()
		}
	}()
	go maintainCMDBIndex(cmdbIndexCtx, cmdbIndex, recorder)
	querySource, err := access.NewSource(frozen, queryClient, productionQueryPermitAcquirer{flights: flights}, access.Config{
		MinReadyDelay:       cfg.PhaseTwo.Access.MinReadyDelay.Duration(),
		Now:                 external.Now,
		Observer:            observer,
		Admission:           seriesAdmission,
		ObserveAdmission:    recorder.RecordSeriesAdmission,
		ObserveSeriesPulled: seriesPullTally.Add,
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
	// The coordinates are checked here and refuse startup when wrong; the
	// connection is made by Start below, after the converters are configured
	// and the bundle exists to be told, and is retried if it fails.
	eventsOpener, err := external.prepareEvents(cfg.Kafka.TriggerEventCoordinates())
	if err != nil {
		return nil, err
	}
	events, err := newLazyOutputSink(eventsOpener, external.Now)
	if err != nil {
		return nil, err
	}
	eventsClosed := false
	defer func() {
		if resultErr != nil && !eventsClosed {
			resultErr = errors.Join(resultErr, events.Close())
		}
	}()
	var legacyClients []redis.UniversalClient
	stopDiagnosticWriter := func() {}
	closeLegacyClients := func() error {
		var errs []error
		for _, client := range legacyClients {
			errs = append(errs, client.Close())
		}
		return errors.Join(errs...)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, closeLegacyClients())
		}
	}()
	{
		// Conversion is built in; these settings provide environment coordinates,
		// never an enable switch. Native events do not access the snapshot store.
		//
		// The protocol is chosen per event from the strategy's frozen revision,
		// which this process does not control and which can stop being present
		// between two refreshes, so the compatibility protocol is always
		// reachable. Configuration validation has already established that the
		// topic is permitted and the coordinates are complete; what is left here
		// is the one thing configuration cannot check, that the service Redis
		// answers. Discovering that at the first event is what turned a release
		// into twenty-five minutes of failed emissions while Slots kept
		// completing, and control-plane state written in those minutes then made
		// the rollback worse than the defect.
		serviceRedis, err := openProductionRedisWithHook(ctx, cfg.Kafka.LegacyAdapter.ServiceRedis, recorder.RedisHook("legacy_output"))
		if err != nil {
			return nil, fmt.Errorf("open kafka.legacy_adapter.service_redis: %w", err)
		}
		legacyClients = append(legacyClients, serviceRedis)
		converter := &legacyoutput.Converter{
			Store:          legacyoutput.RedisSnapshotStore{Client: serviceRedis},
			SnapshotPrefix: cfg.Kafka.LegacyAdapter.SnapshotPrefix,
			Now:            external.Now,
			PluginID:       cfg.Kafka.LegacyAdapter.PluginID,
		}
		if podConfig := cfg.Kafka.LegacyAdapter.PodCache; podConfig != nil {
			// Enrichment is optional: do not require a successful cache Ping to start.
			podClient := redis.NewUniversalClient(productionRedisOptions(podConfig.Connection))
			podClient.AddHook(recorder.RedisHook("legacy_pod_cache"))
			legacyClients = append(legacyClients, podClient)
			resolver, err := legacyoutput.NewDjangoPodResolver(podClient, legacyoutput.PodCacheConfig{KeyPrefix: podConfig.KeyPrefix, Version: podConfig.Version, Observe: recorder.RecordLegacyPodCache, OnFallback: func(reason string) {
				observer.Observe(context.Background(), observability.Observation{Component: observability.ComponentRuntime, Stage: observability.StageLegacyPodCache, Result: observability.ResultDegraded, Err: fmt.Errorf("legacy Pod cache fallback: %s", reason)})
			}})
			if err != nil {
				return nil, err
			}
			converter.Pods = resolver
		}
		standard, standardErr := linkdoutput.NewConverter(recorder.RecordUnmappedSeverity)
		if standardErr != nil {
			return nil, standardErr
		}
		if err := events.ConfigureStandardOutput(standard); err != nil {
			return nil, err
		}
		if err := events.ConfigureLegacyOutput(converter, cfg.Kafka.LegacyAdapter.Topic, cfg.Kafka.TriggerEvent.MaxMessageBytes); err != nil {
			return nil, err
		}
	}
	activation := productionPhaseTwoActivation{source: repository}
	admitter, err := ownership.NewAdmitter(ownershipStore, activation)
	if err != nil {
		return nil, err
	}
	// The copy of the consumer's open alert set reads the publisher's keys
	// from the same Redis the runtime objects live in; the publisher is
	// another service, and it getting that connection is a deployment item.
	// Not a configuration key: the policy for an unavailable publication is
	// a ruling recorded in the openalerts package, not an operator setting.
	openAlertSource, err := openalerts.NewRedisSource(runtimeClient)
	if err != nil {
		return nil, err
	}
	openAlertCopy, err := openalerts.New(openalerts.Options{Source: openAlertSource, Now: external.Now})
	if err != nil {
		return nil, err
	}
	recorder.SetOpenAlertSetSource(openAlertCopy.Stats)
	// The mark a failed attempt leaves behind. Wired here and asserted by a
	// test on this function: the port is allowed to be nil, and a production
	// runtime that left it nil would lose every query-free completion's
	// evidence without failing anything -- which is the shape that cost two
	// releases when the deadline port was implemented by every fake and by no
	// production runtime.
	slotAppliedMarks, err := state.NewSlotAppliedMarkStore(cfg.Redis.StatePrefix, storageRouter)
	if err != nil {
		return nil, err
	}
	workerPorts := worker.Ports{
		Finalization: frozen, Activation: repository, Query: querySource, Sequencer: sequencer,
		Evaluator: evaluator, Admission: admitter, GapGuard: executionStore, Events: events,
		NoData: executionStore, Hosts: cmdbcache.NewHostBusinessLookup(cmdbIndex), State: executionStore, Progress: progressStore, Observer: observer,
		ExecutionEvidence: slotAppliedMarks,
		OpenAlerts:        openAlertCopyPort{cache: openAlertCopy},
	}
	coordinator, err := worker.NewSlotExecutionCoordinator(workerPorts, worker.ProvisionalBudget{
		MaxSeries: cfg.PhaseTwo.Coordinator.MaxSeries, MaxRetainedBytes: cfg.PhaseTwo.Coordinator.MaxRetainedBytes,
		MaxStateMutations: cfg.PhaseTwo.Coordinator.MaxStateMutations, MaxEvents: cfg.PhaseTwo.Coordinator.MaxEvents,
		MaxGapMutations: cfg.PhaseTwo.Coordinator.MaxGapMutations, StoreMaxItems: uint64(cfg.Limits.Store.MaxKeysPerBatch),
	})
	if err != nil {
		return nil, err
	}
	// Only the static compatibility is read from this one; the heartbeat that
	// carries acknowledgement and load is written by the bundle once it exists.
	// The view stream this process serves as Leader and joins as Worker
	// (decision-016): one identity per process, written into the
	// registration; one server, led and stepped down with the control
	// authority; the desired set of every round published through it.
	streamIdentity, err := newViewStreamIdentity(cfg.HTTP.Listen, viewStreamRoutes(cfg.RuntimeStoreRedis())...)
	if err != nil {
		return nil, err
	}
	if streamIdentity.Unadvertised != "" {
		// Said once here, by the process that cannot be reached, rather than
		// on every other Worker at every reconnect as LEADER_NO_ENDPOINT.
		observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageViewSession, Result: observability.ResultDegraded,
			ViewStream: &observability.ViewStreamFacts{Event: "endpoint_unadvertised", WorkerID: cfg.PhaseTwo.Worker.ID, Reason: streamIdentity.Unadvertised},
		})
	}
	viewServer, err := viewstream.NewServer(viewStreamAdmission{registry: ownershipStore, now: external.Now}, observer,
		viewstream.ServerOptions{Now: external.Now})
	if err != nil {
		return nil, err
	}
	registration, err := phaseTwoWorkerRegistration(cfg, ownership.WorkerStarting, external.Now(), nil, nil, streamIdentity)
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
	productionOwnership, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		ExpiredRangeEnabled: cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled,
		Store:               ownershipStore, WorkerID: cfg.PhaseTwo.Worker.ID, Catalog: catalog, Progress: progressStore,
		Executor: executor, Now: external.Now, ControlLeaderTTL: cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration(),
		Observer: observer, Reconcile: assignmentReconciler, Flights: flights, RecoveryLimits: recoveryLimits,
		PostRecoveryTerminalDelay: retention.TerminalDelay,
		QueryDeadlineReserve:      retention.QueryReserve,
		SettlingWait:              cfg.PhaseTwo.Access.MinReadyDelay.Duration(),
		SnapshotRetention:         phaseTwoCatalogRetention(cfg),
		PublicationDelayAllowance: phaseTwoPublicationDelayAllowance(cfg),
		LeaseTTL:                  cfg.PhaseTwo.Ownership.LeaseTTL.Duration(),
		ReconcileInterval:         cfg.PhaseTwo.Control.ReconcileInterval.Duration(),
		ContentScopes:             currentContentScopes(repository),
		ViewStream:                viewServer, ViewSource: repository,
	})
	if err != nil {
		return nil, err
	}
	controlStream := grpc.NewServer()
	pb.RegisterControlServiceServer(controlStream, viewServer)
	recorder.SetViewStreamSource(func() metric.ViewStreamCounts { return viewStreamCounts(viewServer.Stats()) })
	// This Worker's side of the same stream: it finds the Leader from the
	// lease and the Leader's registration, installs what it is sent, and
	// asks the catalog whether the objects a view names are there. In the
	// shadow step nothing executes off the installed view.
	incarnation, err := newViewStreamIncarnation()
	if err != nil {
		return nil, err
	}
	viewClient, err := viewstream.NewClient(
		viewstream.ClientIdentity{WorkerID: cfg.PhaseTwo.Worker.ID, Incarnation: incarnation, StreamToken: streamIdentity.Token},
		viewStreamDiscovery{store: ownershipStore}, repository, observer, viewstream.ClientOptions{Now: external.Now},
	)
	if err != nil {
		return nil, err
	}
	recorder.SetViewClientSource(func() metric.ViewClientCounts { return viewClientCounts(viewClient.Stats()) })
	// The cutover names each changing Query Group's content in its record
	// before it cuts the Segment that carries it (decision-016 batch 3).
	activator.WithContentScopeWriter(productionOwnership)
	// Retention is derived from the publish cadence rather than fixed, because
	// the cadence is configurable and the relationship between the two is what
	// makes the states meaningful. A constant retention against a configurable
	// cadence has a breaking point: past a reconcile interval of about a minute
	// every snapshot would expire before its replica published the next one, and
	// the whole view would sit at UNKNOWN forever with no validation error to say
	// why. Deriving it means the same three states hold at any cadence.
	fleetRetention := 4 * cfg.PhaseTwo.Control.ReconcileInterval.Duration()
	if fleetRetention < fleet.DefaultTTL {
		fleetRetention = fleet.DefaultTTL
	}
	// Same client and prefix convention as the catalog and ownership stores:
	// these snapshots are phase-two runtime state, not a separate channel.
	fleetStore, err := fleet.NewRedisStore(runtimeClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), fleetRetention, 0)
	if err != nil {
		return nil, err
	}
	// Freshness has to be shorter than the snapshot TTL, or a replica that
	// stops publishing goes straight from fresh to absent and the stale branch
	// never fires. The two say different things: stale means alive but stuck,
	// absent means gone, and an operator acts differently on each.
	fleetFreshness := 6 * cfg.PhaseTwo.Control.ReconcileInterval.Duration()
	fleetService, err := fleet.NewService(
		controlPlaneExpectation{repository: repository},
		registryReplicas{store: ownershipStore},
		fleetStore,
		fleetFreshness,
		external.Now,
	)
	if err != nil {
		return nil, err
	}
	// Windows live under the same phase-two prefix as the rest of the runtime
	// objects, and every replica reads them on the reconcile tick it already
	// runs, so opening one needs neither a restart nor a release.
	windowStore, err := fleet.NewWindowStore(runtimeClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"))
	if err != nil {
		return nil, err
	}
	// The stall budget is the scheduler's own replay age rather than a page
	// constant: past it the deployment has already promised to terminate a Slot
	// that cannot complete, so an object still failing beyond it is one nothing
	// will resolve on its own.
	stallAfter := cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration()
	// An observation window's output comes back where the window was opened.
	// Its client and pool are separate from the control plane's even though the
	// connection is the same: a diagnostic burst must not consume connections
	// the pipeline sized for query permits, and the small pool below is what
	// keeps that true.
	diagnosticsClient, err := openProductionRedisOptionsWithHook(ctx,
		observationRedisOptions(runtimeConnection), recorder.RedisHook("diagnostics"))
	if err != nil {
		// Reaching the store is not a startup requirement: losing it costs the
		// window read-back and nothing else, and refusing to start would let a
		// diagnostic dependency stop the pipeline it only describes.
		observer.Observe(ctx, observability.Observation{
			Component: observability.ComponentRuntime, Stage: observability.StageStartup,
			Result: observability.ResultDegraded, Err: fmt.Errorf("open diagnostics redis: %w", err),
		})
		diagnosticsClient = nil
	}
	var diagnostics *fleet.DiagnosticStore
	var directory *controlplane.ObservationDirectory
	var seriesSampler *observability.SeriesSampler
	if diagnosticsClient != nil {
		legacyClients = append(legacyClients, diagnosticsClient)
		diagnostics, err = fleet.NewDiagnosticStore(diagnosticsClient,
			productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"))
		if err != nil {
			return nil, err
		}
		if observationCapacity.DirectoryBytes > 0 {
			directory, err = controlplane.NewObservationDirectory(repository, controlplane.DirectoryLimits{
				WireBytes: observationCapacity.DirectoryReadBytes, Commands: observationCapacity.DirectoryCommands,
				Entries:  observationCapacity.DirectoryBytes / controlplane.DirectoryEntryReservationBytes(),
				Timeout:  min(time.Second, cfg.PhaseTwo.Control.ReconcileInterval.Duration()/2),
				FreshFor: 3 * cfg.PhaseTwo.Control.RefreshInterval.Duration(),
			}, diagnosticsClient)
			if err != nil {
				return nil, err
			}
		}
		if limits, enabled := observationSampleLimits(observationCapacity); enabled {
			seriesSampler, err = observability.NewSeriesSampler(limits)
			if err != nil {
				return nil, err
			}
			if err = diagnostics.AttachSeriesSampler(seriesSampler, min(fleet.DiagnosticRecordsPerObject, observationCapacity.SampleRecordsPerMinute)); err != nil {
				return nil, err
			}
			evaluator.SetSeriesSampler(seriesSampler)
		}
		// The writer outlives the constructor's context and is stopped with the
		// rest of the Bundle's resources.
		diagnosticsCtx, stopDiagnostics := context.WithCancel(context.Background())
		defer func() {
			if resultErr != nil {
				stopDiagnostics()
			}
		}()
		stopDiagnosticWriter = stopDiagnostics
		go diagnostics.Run(diagnosticsCtx)
		targetFlow.SetSink(diagnostics.Record)
	}
	fleetAPI, err := fleet.NewHandler(fleetService, windowStore, external.Now, stallAfter,
		fleetRangeProvider(queryClient, cfg.PhaseTwo.Access.SelfMetricsSpaceUID), diagnostics,
		cfg.PhaseTwo.Access.MonitorWebBaseURL)
	if err != nil {
		return nil, err
	}
	fleetAPI = fleet.WithStrategyDirectory(fleetAPI, directory, external.Now)
	costCandidatesCache := fleet.NewCostCandidatesCache(external.Now, 3*cfg.PhaseTwo.Control.RefreshInterval.Duration())
	var costRefresh *observationCostRefresh
	if diagnosticsClient != nil && observationCapacity.CostBytes > 0 {
		limits := observationProjectionLimits(observationCapacity, cfg.PhaseTwo.Control.RefreshInterval.Duration())
		projection, projectionErr := fleet.NewCostProjectionStore(diagnosticsClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), limits)
		if projectionErr != nil {
			return nil, projectionErr
		}
		costRefresh = &observationCostRefresh{store: projection, registry: ownershipStore, reader: diagnosticsClient, cache: costCandidatesCache, limits: limits,
			registryLimits: ownership.ObservationRegistryLimits{Bytes: int64(observationCapacity.CostBytes / 64), Commands: observationCapacity.DirectoryCommands / 2, Rows: observationCapacity.DirectoryCommands/2 - 2, Timeout: time.Second},
			replica:        cfg.PhaseTwo.Worker.ID, interval: cfg.PhaseTwo.Control.RefreshInterval.Duration()}
	}
	fleetAPI = fleet.WithCostCandidates(fleetAPI, costCandidatesCache)
	fleetAPI = fleet.WithSeriesSamples(fleetAPI, directory, windowStore, diagnostics, seriesSampler, external.Now)
	observationRefresh := &observationRefresh{directory: directory, cost: costSummary, now: external.Now,
		interval: cfg.PhaseTwo.Control.RefreshInterval.Duration(), entries: observationCapacity.DirectoryBytes / controlplane.DirectoryEntryReservationBytes()}
	// The same judgment the page shows, exported so the host writes alert rules
	// against it instead of reimplementing the arithmetic. The deadline is a
	// ceiling on hanging, not a tuning knob: the read is one control plane fetch
	// over a snapshot set the size of the replica count, so it finishes in
	// milliseconds or something is wrong -- and a scrape that blocks takes every
	// other metric down with it.
	if err := recorder.BindFleet(fleetVerdictSource(
		fleetService, external.Now, stallAfter, fleetVerdictScrapeCeiling,
	)); err != nil {
		return nil, err
	}
	// How full the query permit budget is, read when the metric is scraped
	// rather than pushed when a permit changes hands. It is bound here because
	// this is where the coordinator that owns the budget is available.
	if err := recorder.BindQueryPermits(queryPermitOccupancySource(flights)); err != nil {
		return nil, err
	}
	var publisher fleetPublisher
	bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: control, Ownership: productionOwnership,
		Recorder: recorder, Observer: observer, TargetFlow: targetFlow, Now: external.Now,
		FleetAPI:      fleetAPI,
		ControlStream: controlStream, StreamIdentity: streamIdentity, ViewStreamStats: viewServer.Stats, ViewClient: viewClient,
		PublishFleet: func(ctx context.Context) {
			observationRefresh.publish(ctx)
			publisher.publishOnce(ctx)
			if costRefresh != nil {
				costRefresh.publish(ctx, external.Now(), costSummary.Snapshot())
			}
		},
		RefreshOpenAlerts:       openAlertCopy.Refresh,
		RefreshPlatformSettings: platformSettingsRefresher(platformSettings, hostStatus, recorder),
		ApplyObservationWindows: observationWindowApplier{
			store: windowStore, flow: targetFlow, samples: seriesSampler, now: external.Now,
			observe: observationWindowObserver(observer),
		}.applyOnce,
		ProbeControlRedis: func(probeCtx context.Context) error {
			return controlClient.Ping(probeCtx).Err()
		},
		CloseResources: func(shutdownCtx context.Context) error {
			stopDiagnosticWriter()
			stopCMDBIndex()
			viewServer.Close()
			eventsClosed = true
			closers := []error{events.Shutdown(shutdownCtx)}
			if !runtimeClientIsSource {
				closers = append(closers, runtimeClient.Close())
			}
			if cmdbClientOwned {
				closers = append(closers, cmdbClient.Close())
			}
			return errors.Join(append(closers, closeLegacyClients())...)
		},
	})
	if err != nil {
		return nil, err
	}
	bundle.workerPorts = workerPorts
	// The walk's counts, from the same published facts the verdict page reads.
	//
	// None of them were on /metrics, so the one signal that says this replica
	// stopped covering its objects -- truncated climbing while completed does
	// not -- could not be alerted on, and the only count that answers "was
	// anything turned away for lack of a place" could be read only by a person
	// opening a page. Bound to rotationFacts rather than counted a second time
	// here, so the metric and the page cannot disagree about one walk.
	recorder.SetDispatchRotationSource(func() *metric.DispatchRotationCounts {
		facts := bundle.rotationFacts()
		if facts == nil {
			return nil
		}
		return &metric.DispatchRotationCounts{
			Completed: facts.Completed, Truncated: facts.Truncated,
			Offered: facts.Offered, Queued: facts.Queued,
			DeferredQueueFull: facts.DeferredQueueFull,
			DeferredNotBetter: facts.DeferredNotBetter,
		}
	})
	// The view this Worker holds by content, summed over what it owns right
	// now. Bound after the bundle exists for the same reason as the fleet
	// snapshot below: only the bundle knows the owned set, and the view is
	// defined over it, not over what the object cache happens to retain.
	recorder.SetLocalViewSource(func() metric.LocalViewCounts {
		view := repository.LocalView(bundle.ownedQueryGroups())
		return metric.LocalViewCounts{QueryGroups: view.QueryGroups, ObjectBytes: view.ObjectBytes, OutputContextBytes: view.OutputContextBytes}
	})
	// Bound after the bundle exists: the snapshot reports what this replica
	// currently owns, which only the bundle knows.
	publisher = fleetPublisher{
		tracker: fleetTracker, store: fleetStore, replica: cfg.PhaseTwo.Worker.ID,
		owned: bundle.ownedQueryGroups, now: external.Now,
		// Captured once, here, rather than read per publish. It is the ceiling
		// on every duration this replica reports, and a ceiling that moves is
		// not one.
		startedAt: external.Now(),
		// The same three facts the recorder puts on build_info, so the page and
		// the metric cannot name different builds for one process.
		build:   fleetBuildFacts(recorder.Build()),
		observe: publishOutcomeObserver(observer),
		// What survived the restart is read back rather than re-learned. The
		// staleness bound is the deployment's own replay age: past it a Slot
		// that cannot complete has already been promised an end, so a Progress
		// cursor older than that describes an object that stopped rather than
		// one between rounds.
		capacity: capacitySnapshotSource(flights, cfg, rejectionTally, bundle.rotationFacts, seriesPullTally),
		applied:  repository.AppliedActivationRevision,
		// The due index is the only thing that knows an object was passed over
		// rather than evaluated. It lives on the bundle precisely so a reader
		// outside the dispatch loop can ask it.
		overdue:    bundle.ensureDueIndex(),
		schedule:   bundle.ensureDueIndex(),
		strategies: fleetTracker.StrategiesFor,
		// Whether anything can be parked at all, from the same bundle. The
		// overdue count above is only readable next to this: on a build that
		// suppresses nothing it can only be zero.
		dispatch:         bundle.dispatchSuppressionFacts,
		restore:          progressRestoreSource(progressStore),
		staleAfter:       stallAfter,
		restoreBudget:    fleetRestoreBudgetPerPublish,
		openAlerts:       openAlertSetFactsSource(openAlertCopy, external.Now),
		controlSource:    bundle.controlSourceFleetFacts,
		platformSettings: platformSettingsFactsSource(platformSettings, external.Now),
		activation:       bundle.activationFleetFacts,
		rebalance:        bundle.rebalanceFleetFacts,
		assignmentScope:  bundle.assignmentScopeFleetFacts,
		assignmentSweep:  bundle.assignmentSweepFleetFacts,
		viewStream:       viewStreamFleetFacts(bundle.dependencies.ViewStreamStats, external.Now),
		source:           bundle.sourceFleetFacts,
		endpoints: endpointFactsSource(cfg, sharing, recorder, cmdbIndex, platformSettings,
			bundle.sourceFleetFacts, events.State, external.Now),
		// The same snapshot the readiness endpoint serves, so the fleet and
		// the probe cannot disagree about one replica.
		readiness: readinessFactsSource(health),
		// The same word the reconciler above was configured with.
		outputProtocol: fleetOutputProtocolFacts(cfg),
	}
	// The heartbeat reports the same acknowledgement and occupancy the fleet
	// snapshot publishes, from the same sources.
	observationRefresh.owned = bundle.ownedQueryGroups
	bundle.applied = publisher.applied
	bundle.capacity = publisher.capacity
	// The first attempt runs now, so a broker that answers is open before the
	// worker registers, as it always was; one that does not leaves the sink
	// retrying and the bundle told, which is what keeps this replica out of
	// the ready set until it can publish.
	events.SetOnChange(bundle.outputSinkChanged)
	events.Start()
	return bundle, nil
}

// publishOutcomeObserver reports whether this replica is still contributing to
// the aggregated view.
//
// A replica whose publish keeps failing disappears from that view, and the
// aggregate can then say only that a replica is missing. Missing and failing to
// publish call for different actions, and nothing else in the process can tell
// them apart, because the evidence that would distinguish them is precisely the
// write that failed.
//
// Only transitions are reported, matching the registration renewal: a Redis
// outage lasting an hour is one fact, not one per reconcile tick. The observer
// runs on the single publishing goroutine, so the transition flag needs no lock.
// observationWindowObserver reports what this replica actually observes.
//
// Only changes are reported, because the applier runs on every reconcile tick
// and a steady selection is one fact, not one per tick. A shortfall repeats,
// though: while windows are being dropped for want of budget, every tick says
// so, because the person waiting on that window has no other way to find out.
func observationWindowObserver(observer observability.Observer) func(int, int, int, error) {
	applied, requested, failing := -1, -1, false
	return func(nowApplied, nowRequested, dropped int, err error) {
		switch {
		case err != nil:
			failing = true
			observeRuntime(context.Background(), observer, observability.Observation{
				Component: observability.ComponentRuntime, Stage: observability.StageObservationWindow,
				Result: observability.ResultFailed, Direction: observability.DirectionInternal, Err: err,
			})
		case dropped > 0:
			failing = false
			applied, requested = nowApplied, nowRequested
			observeRuntime(context.Background(), observer, observability.Observation{
				Component: observability.ComponentRuntime, Stage: observability.StageObservationWindow,
				Result: observability.ResultDegraded, Direction: observability.DirectionInternal,
				Err: fmt.Errorf("observation windows exceed the diagnostic budget: %d of %d requested objects are not observed", dropped, nowRequested),
			})
		case failing || nowApplied != applied || nowRequested != requested:
			failing = false
			applied, requested = nowApplied, nowRequested
			observeRuntime(context.Background(), observer, observability.Observation{
				Component: observability.ComponentRuntime, Stage: observability.StageObservationWindow,
				Result: observability.Result(observability.ResultSuccess), Direction: observability.DirectionInternal,
			})
		}
	}
}

func publishOutcomeObserver(observer observability.Observer) func(error) {
	failing := false
	return func(err error) {
		switch {
		case err != nil:
			failing = true
			observeRuntime(context.Background(), observer, observability.Observation{
				Component: observability.ComponentRuntime, Stage: observability.StageFleetSnapshotPublish,
				Result: observability.ResultFailed, Direction: observability.DirectionInternal, Err: err,
			})
		case failing:
			failing = false
			observeRuntime(context.Background(), observer, observability.Observation{
				Component: observability.ComponentRuntime, Stage: observability.StageFleetSnapshotPublish,
				Result: observability.ResultResumed, Direction: observability.DirectionInternal,
			})
		}
	}
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

// phaseTwoMaxSupportedEvaluationInterval is the longest evaluation cadence a
// Plan may have and still be served. It is stated rather than discovered
// because it decides how long a published Catalog has to be kept: a Plan
// evaluated once a day is still owed its Snapshot a day later, so a
// deployment that keeps Catalogs for a day cannot serve one.
//
// A day is the cadence the platform's own strategies reach. It is not a
// limit anybody configures; it is the number the retention is derived from,
// and a Plan beyond it is refused with this bound named.
const phaseTwoMaxSupportedEvaluationInterval = 24 * time.Hour

// phaseTwoCatalogRetention is how long published Catalogs are kept: long
// enough for the longest cadence the deployment supports, never shorter than
// the configured floor.
//
// It is derived and not configured because the number is not an opinion.
// It was a 24 hour constant, and a single strategy evaluated once a day
// needed 24h13m -- thirteen minutes more than the constant -- so every
// Catalog build was refused, for every strategy, and the deployment ran on
// the last good Catalog while reporting healthy. A constant chosen to clear
// today's longest strategy would be the same defect waiting for tomorrow's.
func phaseTwoCatalogRetention(cfg config.Config) time.Duration {
	supported := phaseTwoMaxSupportedEvaluationInterval - cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	return maxDuration(cfg.PhaseTwo.Control.CatalogTTL.Duration(), phaseTwoSnapshotMinimumRetention(cfg, supported))
}

func phaseTwoSnapshotMinimumRetention(cfg config.Config, queryDeadlineOffset time.Duration) time.Duration {
	return phaseTwoPublicationDelayAllowance(cfg) + queryDeadlineOffset +
		cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration() + phaseTwoPostRecoveryTerminalDelay(cfg)
}

// phaseTwoCatalogRetentionAdmission withholds the Plans this deployment cannot
// serve and publishes the rest.
//
// Two conditions, both of them properties of one Plan: a completion offset that
// does not clear the downstream execution reserve, which leaves the Plan with
// no time to query in; and a Plan whose recovery contract needs a Snapshot kept
// longer than the deployment's Catalog retention, which would let a Slot be
// replayed after the content it names is gone.
//
// Both used to refuse the whole Catalog. One strategy asking for sixty hours of
// retention stopped every strategy in the deployment from publishing, every
// round, for as long as it existed -- the fleet went stale, no cutover was
// attempted, nothing was written, and the account of it was one sentence naming
// a strategy nobody had touched. The retention is the deployment's capacity and
// does not follow a Plan; a Plan that asks for more than there is, is the Plan
// that cannot run.
//
// Withholding names both numbers, because the two readings are different
// actions: shorten the strategy's evaluation cadence, or raise the deployment's
// retention. A refusal saying only that the Catalog cannot be retained left the
// reader to work out which of those it was.
func phaseTwoCatalogRetentionAdmission(cfg config.Config) controlplane.CatalogAdmission {
	return func(catalog controlplane.Catalog) (controlplane.Catalog, error) {
		reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
		retention := phaseTwoCatalogRetention(cfg)
		// One record per withheld source, in the order they were met, because
		// the dispositions are a partition: every object counted once. The
		// compiler has already recorded an ACCEPTED for each source it
		// compiled, so a withheld source's record replaces that one rather
		// than joining it -- appending both would count the object twice, put
		// it in two partitions at once, and leave the ACCEPTED total unmoved
		// while the strategy stopped running.
		withheld := map[string]controlplane.ObjectDisposition{}
		order := make([]string, 0)
		withhold := func(plan controlplane.FrozenPlan, reason, detail string) {
			if _, already := withheld[plan.Identity.StrategyID]; already {
				// A source with several Plans is still one object. The first
				// reason met is the one reported; the rest are the same
				// verdict about the same strategy.
				return
			}
			order = append(order, plan.Identity.StrategyID)
			withheld[plan.Identity.StrategyID] = controlplane.ObjectDisposition{
				SourceID: plan.Identity.StrategyID, Scope: "PLAN",
				Disposition: controlplane.DispositionUnsupported, Reason: reason, FieldPath: detail,
			}
		}

		groups := make([]controlplane.QueryGroup, 0, len(catalog.QueryGroups))
		for _, group := range catalog.QueryGroups {
			kept := make([]controlplane.FrozenPlan, 0, len(group.Plans))
			for _, plan := range group.Plans {
				completion := time.Duration(plan.ScheduleSpec.CompletionOffsetSeconds()) * time.Second
				offset := completion - reserve
				if offset <= 0 {
					withhold(plan, contract.ReasonCompletionOffsetBelowReserve,
						fmt.Sprintf("completion_offset=%s downstream_execution_reserve=%s", completion, reserve))
					continue
				}
				if required := phaseTwoSnapshotMinimumRetention(cfg, offset); retention < required {
					withhold(plan, contract.ReasonSnapshotRetentionInsufficient,
						fmt.Sprintf("required_retention=%s catalog_retention=%s", required, retention))
					continue
				}
				kept = append(kept, plan)
			}
			if len(kept) == 0 {
				// Every Plan of this Query Group was withheld, so the group has
				// nothing to execute. Publishing it empty would put a Query
				// Group on the fleet that no worker can do anything with.
				continue
			}
			group.Plans = kept
			groups = append(groups, group)
		}
		catalog.QueryGroups = groups
		if len(withheld) == 0 {
			return catalog, nil
		}
		dispositions := make([]controlplane.ObjectDisposition, 0, len(catalog.Dispositions)+len(withheld))
		for _, disposition := range catalog.Dispositions {
			if _, replaced := withheld[disposition.SourceID]; replaced && disposition.Scope == "PLAN" {
				continue
			}
			dispositions = append(dispositions, disposition)
		}
		for _, sourceID := range order {
			dispositions = append(dispositions, withheld[sourceID])
		}
		catalog.Dispositions = dispositions
		return catalog, nil
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

// openAlertCopyPort adapts the process copy to the worker's port: the
// worker speaks in Plan identities, the copy in strategy keys.
type openAlertCopyPort struct{ cache *openalerts.Cache }

func (port openAlertCopyPort) Contains(tenantID, strategyID, fingerprint string) bool {
	return port.cache.Contains(tenantID, strategyID, fingerprint)
}

func (port openAlertCopyPort) TrackPlans(plans []execution.PlanIdentity) {
	keys := make([]openalerts.StrategyKey, 0, len(plans))
	for _, plan := range plans {
		keys = append(keys, openalerts.StrategyKey{TenantID: plan.TenantID, StrategyID: plan.StrategyID})
	}
	port.cache.Track(keys...)
}

func (port openAlertCopyPort) Acknowledged(events []contract.TriggerEventV1) {
	port.cache.Acknowledged(events)
}

// openAlertSetFactsSource is what this replica publishes about its copy. The
// age is absent until there has been an authoritative read: a zero would
// read as "just now" on a copy that never loaded.
func openAlertSetFactsSource(cache *openalerts.Cache, now func() time.Time) func() *fleet.OpenAlertSetFacts {
	return func() *fleet.OpenAlertSetFacts {
		stats := cache.Stats()
		facts := &fleet.OpenAlertSetFacts{Mode: string(stats.Mode), StaleBeyondBound: cache.StaleBeyondBound()}
		if !stats.LoadedAt.IsZero() {
			age := now().Sub(stats.LoadedAt).Seconds()
			facts.AuthoritativeAgeSeconds = &age
		}
		return facts
	}
}

func productionPhaseTwoPrefix(prefix, component string) string {
	return prefix + ":phase-two:" + component
}

func phaseTwoUQLimits(cfg config.Config) accessuq.Limits {
	// One UQ response body is capped independently of the Coordinator retained
	// budget: raising that budget to gigabytes must not allow a multi-gigabyte
	// single response. Series, record and per-series byte limits are unchanged.
	const maximumBodyCap = int64(512 << 20)
	maximumBody := int64(cfg.PhaseTwo.Coordinator.MaxRetainedBytes)
	if maximumBody > maximumBodyCap {
		maximumBody = maximumBodyCap
	}
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
