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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
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
	// StartupWaitInitial overrides the first retry delay of a startup
	// dependency that does not answer; zero is the production delay.
	StartupWaitInitial time.Duration
}

func (external phaseTwoProductionExternalDependencies) startupWaiter(
	recorder *metric.Recorder, logger *observability.Logger, health *phaseTwoApplicationHealth,
) startupWaiter {
	waiter := newStartupWaiter(recorder, logger, health)
	if external.StartupWaitInitial > 0 {
		waiter.initial, waiter.ceiling = external.StartupWaitInitial, 4*external.StartupWaitInitial
	}
	return waiter
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
	wait := external.startupWaiter(recorder, logger, health)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !phaseTwoProductionBudgetsFitPlatform(cfg) {
		return nil, errors.New("phase-two production budgets overflow provider limits")
	}
	// Where the link writes its open alert sets is the link's answer; it is
	// read before anything connects, so the index, the difference and the
	// endpoint list all read the one location.
	cfg, linkdDiscovery := adoptLinkdLocation(ctx, cfg, openalerts.DiscoverTarget)
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
	// The census beside the summary, not instead of it: the summary is the
	// bounded diagnostic; the census is every owned Query Group's peak for
	// the heartbeat, which has to be a census.
	retainedPeaks := observability.NewRetainedPeakCensus(5*time.Minute, external.Now)
	observer = observability.Multi(observer, external.AdditionalObserver, targetFlow, rejectionTally, costSummary, retainedPeaks)
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
	controlClient, err := wait.openRedis(ctx, "redis_source", sourceConnection, recorder.RedisHook("source"))
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
		runtimeClient, err = wait.openRedis(ctx, "redis_runtime", runtimeConnection, recorder.RedisHook("runtime"))
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
		cmdbClient, err = wait.openRedis(ctx, "redis_cmdb", cmdbConnection, recorder.RedisHook("cmdb"))
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
			opened, err := wait.openRedis(ctx, "redis_dynamic_config", dynamicConfigConnection, recorder.RedisHook("dynamic_config"))
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
	var targetGroupClient redis.UniversalClient
	targetGroupClientOwned := false
	targetGroupConnection, targetGroupConfigured := cfg.TargetGroupRedis()
	if targetGroupConfigured {
		switch {
		case reflect.DeepEqual(targetGroupConnection, cmdbConnection):
			targetGroupClient = cmdbClient
			sharing.targetGroupSharedWith = fleet.EndpointCMDBCache
		case reflect.DeepEqual(targetGroupConnection, sourceConnection):
			targetGroupClient = controlClient
			sharing.targetGroupSharedWith = fleet.EndpointStrategyCache
		case reflect.DeepEqual(targetGroupConnection, runtimeConnection):
			targetGroupClient = runtimeClient
			sharing.targetGroupSharedWith = fleet.EndpointStateRedis
		case dynamicConfigConfigured && reflect.DeepEqual(targetGroupConnection, dynamicConfigConnection):
			targetGroupClient = dynamicConfigClient
			sharing.targetGroupSharedWith = fleet.EndpointDynamicConfig
		default:
			targetGroupClient, err = wait.openRedis(ctx, "redis_target_group", targetGroupConnection, recorder.RedisHook("target_group"))
			if err != nil {
				return nil, err
			}
			targetGroupClientOwned = true
			defer func() {
				if resultErr != nil {
					resultErr = errors.Join(resultErr, targetGroupClient.Close())
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
		if targetGroupClientOwned {
			counts = append(counts, redisPoolCounts("target_group", targetGroupConnection.PoolSize, targetGroupClient))
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
	// The same resolved horizon the reconciler freezes into every Plan, read
	// the same way, so a row can say whether a Plan's horizon is the
	// platform's or the strategy's own. A Plan compiled under an earlier value
	// reads as strategy until the reconciler's round key recompiles it, which
	// is the propagation delay and nothing else.
	fleetTracker.SetPlatformNoDataHorizon(func() int64 {
		return platformSettings.Current().NoDataTrackingHorizonSeconds
	})
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
			// A timeline read answered by the Worker's revision hint
			// (decision-016 batch 4): a hit is no Redis command at all, a
			// miss is one body read, a refresh is a hint the body did not
			// bear and the read went the header way. Header reads for
			// hinted Query Groups are what batch 4 removes; the header's
			// own object above is where that has to fall.
			{Object: "timeline_by_revision", Hits: stats.HintedTimeline.Hits, Misses: stats.HintedTimeline.Misses,
				Refreshes: stats.HintedTimeline.Refreshes},
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
	_, dynamicGroups := cfg.DynamicGroupKeyPrefix()
	if err := reconciler.ConfigureTargetSources(controlplane.TargetSources{DynamicGroups: dynamicGroups}); err != nil {
		return nil, err
	}
	if err := reconciler.ConfigureOutputProtocol(cfg.OutputProtocol()); err != nil {
		return nil, err
	}
	// Read per round rather than captured as a value: the reconciler's round
	// key covers the horizon, so the seam is what lets a deployment move the
	// setting without every strategy document having to change for the new
	// value to reach its Plan.
	if err := reconciler.ConfigureNoDataPolicy(func() controlplane.NoDataPolicy {
		// Resolved by the platform settings copy: a dynamic value, else the
		// deployment's values, else the contract's one day. Always positive,
		// so every Plan with no-data enabled carries a finite horizon.
		return controlplane.NoDataPolicy{TrackingHorizonSeconds: platformSettings.Current().NoDataTrackingHorizonSeconds}
	}); err != nil {
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
	if err := wait.await(ctx, "ownership_store", ownershipStore.Ping); err != nil {
		return nil, err
	}
	// A cutover stamps each rewritten timeline's revision on the Query
	// Group's Assignment record in the same script (decision-016 batch 4);
	// both live on the runtime Redis, and the catalog learns the record
	// keys here rather than guessing the ownership prefix.
	repository.WithAssignmentRecordKey(ownershipStore.AssignmentKey)

	stateBackend, err := state.NewRedisBackendWithClient(productionRedisAddress(runtimeConnection), runtimeClient)
	if err != nil {
		return nil, err
	}
	if err := wait.await(ctx, "state_store", stateBackend.Ping); err != nil {
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
	seriesAdmission, cmdbIndex, err := buildSeriesAdmission(ctx, cfg, cmdbClient, recorder, logger, hostStatus, wait)
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
	// What a target plan's dynamic references resolve against, once per
	// Plan per Slot (decision-017). The group store, when there is one,
	// refreshes on the same cadence as the host index and stops with it.
	targetResolver, groupStore, err := buildTargetResolver(cfg, targetGroupClient, cmdbIndex)
	if err != nil {
		return nil, err
	}
	if groupStore != nil {
		go groupStore.Run(cmdbIndexCtx)
	}
	// The close of alerts whose target left the strategy's scope hears the
	// target filters' rejections from the query path below; the open set it
	// judges them against and the producer it closes through are bound once
	// they exist. It exists only with the alert link's Console, as the
	// absent-strategy close does; see targetScopeCloseFor.
	scopeClose, scopeDrops := targetScopeCloseFor(cfg, external.Now)
	querySource, err := access.NewSource(frozen, queryClient, productionQueryPermitAcquirer{flights: flights}, access.Config{
		MinReadyDelay:       cfg.PhaseTwo.Access.MinReadyDelay.Duration(),
		Now:                 external.Now,
		Observer:            observer,
		Admission:           seriesAdmission,
		ObserveAdmission:    recorder.RecordSeriesAdmission,
		ScopeDrops:          scopeDrops,
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
		serviceRedis, err := wait.openRedis(ctx, "redis_legacy_output", cfg.Kafka.LegacyAdapter.ServiceRedis, recorder.RedisHook("legacy_output"))
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
	// External facts reuse runtime Redis unless the deployment binds Linkd
	// elsewhere. A failed index is not a startup dependency of detection.
	linkdConnection := runtimeConnection
	linkdClient := runtimeClient
	linkdClientOwned := false
	if cfg.PhaseTwo.Linkd.Connection != nil {
		linkdConnection = *cfg.PhaseTwo.Linkd.Connection
		if !reflect.DeepEqual(linkdConnection, runtimeConnection) {
			linkdClient = redis.NewUniversalClient(productionRedisOptions(linkdConnection))
			linkdClient.AddHook(recorder.RedisHook("linkd"))
			linkdClientOwned = true
			defer func() {
				if resultErr != nil {
					_ = linkdClient.Close()
				}
			}()
		}
	}
	// A later discovery that finds the link at another held connection opens
	// a client for it once; the runtime connection keeps the runtime client.
	openLinkdClient := func(connection config.RedisConnectionConfig) (redis.UniversalClient, bool) {
		if sameRedisConnection(connection, runtimeConnection) {
			return runtimeClient, false
		}
		client := redis.NewUniversalClient(productionRedisOptions(connection))
		client.AddHook(recorder.RedisHook("linkd"))
		return client, true
	}
	linkd, err := newLinkdIndex(cfg, linkdClient, linkdConnection, linkdDiscovery, openLinkdClient, external.Now)
	if err != nil {
		return nil, err
	}
	openAlertCopy := linkd.Cache
	recorder.SetOpenAlertSetSource(openAlertCopy.Stats)
	recorder.SetActivationRebuildSource(repository.ActivationRebuildCounts)
	recorder.SetActivationBlockedSource(repository.ActivationBlockedReading)
	recorder.SetActivationBodyBytesSource(repository.ActivationBodyBytes)
	linkdBudget := config.DeriveLinkdCapacity(config.DetectCapacityInputs())
	legacyTime := strategycache.NewLegacyEffectiveTime(controlClient, cmdbClient, cfg.PlatformKeyPrefix(), external.Now, linkdBudget.Strategies, linkdBudget.Bytes/4)
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
		EffectiveTime: legacyTime.Provider(),
		Finalization:  frozen, Activation: repository, Query: querySource, Sequencer: sequencer,
		Evaluator: evaluator, Admission: admitter, GapGuard: executionStore, Events: events,
		NoData: executionStore, Hosts: cmdbcache.NewHostBusinessLookup(cmdbIndex), State: executionStore, Census: executionStore, Progress: progressStore, Observer: observer,
		ExecutionEvidence: slotAppliedMarks,
		OpenAlerts:        &openAlertCopyPort{cache: openAlertCopy},
		// The horizon the platform settings copy resolves now, read per Slot:
		// runtime state lives at most this long past a series' last write.
		StateHorizon: func() int64 { return platformSettings.Current().NoDataTrackingHorizonSeconds },
		Targets:      targetResolver,
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
	// The Leader's ledger of what each Worker's heartbeat reports its
	// Query Groups cost, fed by the stream and judged by the reconcile
	// round for the byte constraint (decision-020 section 5.7).
	costs := scheduler.NewCostLedger(external.Now)
	viewServer, err := viewstream.NewServer(viewStreamAdmission{registry: ownershipStore, now: external.Now}, observer,
		viewstream.ServerOptions{Now: external.Now, Costs: costLedgerSink{ledger: costs}})
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
	// A placement names the timeline revision on the record it creates, read
	// from the catalog; a record that already says it is not asked about.
	assignmentReconciler.WithTimelineRevisions(repository)
	var executor scheduler.Executor = coordinator
	productionOwnership, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		SteppedDownAsLeader: recorder.ControlLeaderStepDown,
		ExpiredRangeEnabled: cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled,
		QueryCooldowns:      newProductionQueryCooldownStore(cfg, runtimeClient, recorder, observer),
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
		ViewStream:                viewServer, ViewSource: repository, Costs: costs,
		SplitCensus: newCatalogSplitCensusSource(repository, repository, executionStore),
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
	// Batch 4a of decision-016: each Slot read decides, from the installed
	// view and the lease the renewal last brought, whether the Query Group
	// is executed from the view; the receipt counts those that are.
	viewGate := newViewExecutionGate()
	workerCosts := newWorkerCostSource(retainedPeaks, recorder, external.Now)
	viewClient, err := viewstream.NewClient(
		viewstream.ClientIdentity{WorkerID: cfg.PhaseTwo.Worker.ID, Incarnation: incarnation, StreamToken: streamIdentity.Token},
		viewStreamDiscovery{store: ownershipStore}, repository, observer, viewstream.ClientOptions{Now: external.Now, Switched: viewGate,
			Costs: workerCosts},
	)
	if err != nil {
		return nil, err
	}
	viewGate.attach(viewClient)
	productionOwnership.WithViewExecutionGate(viewGate)
	recorder.SetViewClientSource(func() metric.ViewClientCounts {
		counts := viewClientCounts(viewClient.Stats())
		counts.ExecutedFromView = viewGate.Counts()
		counts.GateRenewals, counts.GateRenewalsSettled, counts.GateRenewalsFailed = viewGate.Renewals()
		return counts
	})
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
	// The fleet store's own meters. It shares the runtime client with
	// everything else on this connection, so its traffic is invisible in the
	// per-connection counters; these are written by it alone.
	fleetStore.Meter(recorder)
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
	fleetService.SetReplica(cfg.PhaseTwo.Worker.ID)
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
	// Declared before the API is wrapped: the strategy point read's refusal
	// reads the control plane's live state through it, and the wrapping
	// happens before the bundle that owns that state is built.
	var bundle *phaseTwoWorkerBundle
	fleetAPI, err := fleet.NewHandler(fleetService, windowStore, external.Now, stallAfter,
		fleetRangeProvider(queryClient, cfg.PhaseTwo.Access.SelfMetricsSpaceUID), diagnostics,
		cfg.PhaseTwo.Access.MonitorWebBaseURL)
	if err != nil {
		return nil, err
	}
	fleetAPI = fleet.WithStrategyDirectory(fleetAPI, directory, external.Now)
	// One strategy's standing by id, from the Leader's catalog memory: no
	// Redis, no copy, no background work; a follower forwards the one
	// request to the Leader's listener, found the way the view stream's
	// clients find it.
	fleetAPI = fleet.WithStrategyStanding(fleetAPI, fleetService, strategyLookupSource(reconciler),
		leaderForwarder(viewStreamDiscovery{store: ownershipStore}, cfg.PhaseTwo.Worker.ID, nil, recorder.ObserveLeaderForward),
		strategyObjectLoader(repository), catalogAbsenceSource(func() *phaseTwoWorkerBundle { return bundle }, directory != nil),
		strategyStandingReplica(cfg.PhaseTwo.Worker.ID), external.Now, stallAfter)
	// The environment diagnosis: every strategy of the source's active set,
	// one row each, answered where the catalog is the way a standing is.
	// The warmer reads one first page through the same readers when this
	// process takes the catalog over, on the fleet publish cadence below, so
	// the first page asked after a hand-over is not the process's first read.
	diagnosisWarmer := fleet.NewDiagnosisWarmer(fleetService, strategyLookupSource(reconciler),
		diagnosisUniverse(strategySource), diagnosisProgress(progressStore), external.Now, stallAfter)
	fleetAPI = fleet.WithDiagnosis(fleetAPI, fleetService, strategyLookupSource(reconciler),
		leaderForwarderWithin(viewStreamDiscovery{store: ownershipStore}, cfg.PhaseTwo.Worker.ID, nil, diagnosisForwardTimeout, "diagnosis", recorder.ObserveLeaderForward),
		diagnosisUniverse(strategySource), diagnosisProgress(progressStore),
		strategyStandingReplica(cfg.PhaseTwo.Worker.ID), external.Now, stallAfter, diagnosisWarmer)
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
	fleetAPI, closeCLI, publicRestricted := buildPhaseTwoCLI(cfg, fleetAPI, repository, progressStore, platformSettings, func() *observability.RuntimeConfigFacts {
		if bundle == nil {
			return nil
		}
		return bundle.runtimeConfig
	}, cliControlBinding{Incarnation: incarnation, StreamToken: streamIdentity.Token, Server: viewServer, Metrics: recorder.Gatherer(),
		PublicWindows: fleet.NewPublicWindowsHandler(windowStore, external.Now)})
	defer func() {
		if resultErr != nil {
			_ = closeCLI()
		}
	}()
	surface := publicSurfaceStandingOf(cfg, publicRestricted)
	surface.warn(logger)
	bundle, err = newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		ActivationBlocked: repository.ActivationBlockedReading,
		Config:            cfg, Health: health, Control: control, Ownership: productionOwnership,
		Recorder: recorder, Observer: observer, TargetFlow: targetFlow, Now: external.Now,
		FleetAPI: fleetAPI, PublicSurfaceRestricted: publicRestricted,
		ControlStream: controlStream, StreamIdentity: streamIdentity, ViewStreamStats: viewServer.Stats, ViewClient: viewClient,
		PublishFleet: func(ctx context.Context) {
			observationRefresh.publish(ctx)
			publisher.publishOnce(ctx)
			if costRefresh != nil {
				costRefresh.publish(ctx, external.Now(), costSummary.Snapshot())
			}
			// Off the cadence: the warm-up is bounded by a first page's read
			// timeout, and a publish must not wait on it.
			if warm := diagnosisWarmer.Tick(); warm != nil {
				go warm(ctx)
			}
		},
		RunOpenAlerts: func(runCtx context.Context) error {
			// A process whose startup discovery failed keeps asking in the
			// background, on the same lifetime as the copy it may rebind.
			go linkd.Location.retry(runCtx, cfg, openalerts.DiscoverTarget, linkdRetryFirst, linkdRetryCeiling)
			return openAlertCopy.Run(runCtx)
		},
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
			closers := []error{events.Shutdown(shutdownCtx), closeCLI()}
			if moved := linkd.Location.OwnedClient(); moved != nil {
				closers = append(closers, moved.Close())
			}
			if linkdClientOwned {
				closers = append(closers, linkdClient.Close())
			}
			if !runtimeClientIsSource {
				closers = append(closers, runtimeClient.Close())
			}
			if cmdbClientOwned {
				closers = append(closers, cmdbClient.Close())
			}
			if dynamicConfigClientOwned {
				closers = append(closers, dynamicConfigClient.Close())
			}
			if targetGroupClientOwned {
				closers = append(closers, targetGroupClient.Close())
			}
			return errors.Join(append(closers, closeLegacyClients())...)
		},
	})
	if err != nil {
		return nil, err
	}
	bundle.workerPorts = workerPorts
	maintenance := &effectiveMaintenance{bundle: bundle, catalog: catalog, cache: openAlertCopy, writer: events,
		capacity: linkdBudget, sourceID: cfg.PhaseTwo.Linkd.EventSourceID, legacy: legacyTime.Provider(), legacyCache: legacyTime}
	if linkd.Console != nil {
		maintenance.sourceOf = func(ctx context.Context) (string, error) {
			binding, err := linkd.Console.Binding(ctx)
			return binding.EventSourceID, err
		}
	}
	bundle.dependencies.RunEffectiveTime = maintenance.run
	bindTargetScopeClose(bundle, scopeClose, openAlertCopy, events, recorder)
	openAlertFacts := withTargetScopeClose(openAlertSetFactsSource(openAlertCopy, external.Now), scopeClose)
	// The control leader's difference against the strategies that no longer
	// exist. It runs on every replica's loop and does nothing on a follower;
	// the leader check is inside the round, so a failover needs no wiring of
	// its own. It exists only with the alert link's Console: the difference
	// is the link's roster minus the snapshot, and a deployment without the
	// link has neither the roster nor the alerts it would close.
	if linkd.Console != nil {
		absentClose := newAbsentStrategyClose(bundle, reconciler, linkd.Console, events, cfg.PhaseTwo.Linkd.AbsentCloseSend)
		bundle.dependencies.RunAbsentClose = absentClose.run
		recorder.SetAbsentCloseSource(absentClose.Stats, absentClose.Rounds, absentClose.Difference)
	}
	recorder.SetEffectiveCloseSource(maintenance.Stats)
	// Bound whether or not a Console is configured: not_configured is a
	// reading, and the absent_strategy families are missing there by
	// construction.
	recorder.SetLinkdConsoleSource(fleet.LinkdConsoleStates, openalerts.ConsoleOps,
		linkdConsoleReading(linkd.Console, external.Now))
	workerPorts.OpenAlerts.(*openAlertCopyPort).registerOwned = maintenance.registerExecutedPlans
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
		openAlerts:       openAlertFacts,
		controlSource:    bundle.controlSourceFleetFacts,
		platformSettings: platformSettingsFactsSource(platformSettings, external.Now),
		publicSurface:    surface,
		activation:       bundle.activationFleetFacts,
		rebalance:        bundle.rebalanceFleetFacts,
		assignmentScope:  bundle.assignmentScopeFleetFacts,
		assignmentSweep:  bundle.assignmentSweepFleetFacts,
		leaderRound:      bundle.leaderRoundFleetFacts,
		viewStream:       viewStreamFleetFacts(bundle.dependencies.ViewStreamStats, external.Now),
		source:           bundle.sourceFleetFacts,
		endpoints: withLinkdConsole(endpointFactsSource(cfg, sharing, recorder, cmdbIndex, platformSettings,
			bundle.sourceFleetFacts, events.State, openAlertFacts, external.Now),
			linkd.Console, linkd.Location, external.Now),
		// The same snapshot the readiness endpoint serves, so the fleet and
		// the probe cannot disagree about one replica.
		readiness: readinessFactsSource(health),
		// The same word the reconciler above was configured with.
		outputProtocol: fleetOutputProtocolFacts(cfg),
	}
	// The heartbeat reports the same acknowledgement and occupancy the fleet
	// snapshot publishes, from the same sources.
	observationRefresh.owned = bundle.ownedQueryGroups
	workerCosts.boundByOwned(bundle.ownedQueryGroups)
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

// phaseTwoMaxSupportedEvaluationInterval is the cadence the catalog keys other
// than content -- manifests, timelines, the active set -- are kept for. It is
// no longer the longest cadence a Plan may have: a Plan evaluated every sixty
// hours reads its content by digest when its Slot completes, and the content
// objects are kept for as long as the publication's longest Plan needs
// (Catalog.ObjectRetention, bounded by phaseTwoObjectRetentionLimit). It was
// the only bound once, and it refused a sixty-hour strategy for a retention
// the deployment could have given it at the cost of a few megabytes, where
// stretching every catalog key would have cost a quarter of a gigabyte in
// manifests alone.
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

// phaseTwoObjectRetentionLimit is the longest a Plan's content is kept for
// it: the state store's own ceiling on how long a series' state may live. A
// Plan whose frozen Slot would need its content past it could not keep its
// state across one of its own periods either, so it is refused by name here
// rather than admitted to run on state that expires between its rounds. Never
// below the catalog retention, which every Plan up to a day's cadence needs.
func phaseTwoObjectRetentionLimit(cfg config.Config) time.Duration {
	return maxDuration(cfg.Redis.MaxTTL.Duration(), phaseTwoCatalogRetention(cfg))
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
// A Plan's retention need is met up to phaseTwoObjectRetentionLimit by keeping
// its content longer, and only a Plan past that limit is withheld, with both
// numbers named. The limit is the state store's own ceiling, so the action is
// the strategy's -- a shorter cadence -- and not a deployment parameter.
func phaseTwoCatalogRetentionAdmission(cfg config.Config) controlplane.CatalogAdmission {
	return func(catalog controlplane.Catalog) (controlplane.Catalog, error) {
		reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
		retention := phaseTwoCatalogRetention(cfg)
		limit := phaseTwoObjectRetentionLimit(cfg)
		// The longest any accepted Plan's frozen Slot may still read its
		// content, which is what the content objects are then kept for.
		objectRetention := retention
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
				required := phaseTwoSnapshotMinimumRetention(cfg, offset)
				if required > limit {
					withhold(plan, contract.ReasonSnapshotRetentionInsufficient,
						fmt.Sprintf("required_retention=%s retention_limit=%s", required, limit))
					continue
				}
				objectRetention = maxDuration(objectRetention, required)
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
		catalog.ObjectRetention = objectRetention
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
type openAlertCopyPort struct {
	cache         *openalerts.Cache
	registerOwned func(execution.QueryGroupIdentity, []execution.PlanIdentity)
}

func (port openAlertCopyPort) Contains(tenantID, strategyID, fingerprint string) bool {
	return port.cache.Contains(tenantID, strategyID, fingerprint)
}

func (port openAlertCopyPort) TrackPlans(qg execution.QueryGroupIdentity, plans []execution.PlanIdentity) {
	if port.registerOwned != nil {
		port.registerOwned(qg, plans)
		return
	}
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
		facts := openAlertSetFacts(cache.Stats(), cache.StaleBeyondBound(), now())
		facts.Comparison = openAlertComparisonFacts(cache.Comparison())
		return facts
	}
}

// openAlertSetFacts is the copy's stats as the replica publishes them.
func openAlertSetFacts(stats openalerts.Stats, staleBeyondBound bool, at time.Time) *fleet.OpenAlertSetFacts {
	facts := &fleet.OpenAlertSetFacts{Mode: string(stats.Mode), StaleBeyondBound: staleBeyondBound,
		IndexProtocol: stats.IndexProtocol, CalibrationConfigured: stats.CalibrationConfigured, SubscriptionReady: stats.SubscriptionReady, CalibratedSets: stats.Calibrated,
		PendingReads: stats.PendingReads, PendingReconciles: stats.PendingReconciles, MemberBytes: stats.MemberBytes,
		Available: stats.Available, UnavailableReason: string(stats.UnavailableReason),
		ReaderFingerprintVersion: openalerts.FingerprintVersion,
		TrackedSets:              stats.Tracked, LoadedSets: stats.Loaded, Members: stats.Members,
		SentInSet: stats.SentInSet, SentNotInSet: stats.SentNotInSet, Disjoint: stats.Disjoint}
	if !stats.IndexReadAt.IsZero() {
		age := at.Sub(stats.IndexReadAt).Seconds()
		facts.IndexReadAgeSeconds = &age
	}
	if !stats.LoadedAt.IsZero() {
		age := at.Sub(stats.LoadedAt).Seconds()
		facts.AuthoritativeAgeSeconds = &age
	}
	// The publisher's heartbeat as last read, by its own clock. A copy
	// that never read one carries none: a zero would read as a cycle
	// completed at the epoch.
	if !stats.Heartbeat.PublishedAt.IsZero() {
		age := at.Sub(stats.Heartbeat.PublishedAt).Seconds()
		facts.HeartbeatAgeSeconds = &age
		facts.CycleSeconds = int64(stats.Heartbeat.Cycle / time.Second)
		facts.FingerprintVersion = stats.Heartbeat.FingerprintVersion
	}
	// Every answer word, zero included: a word missing from the map
	// cannot be told from one never given.
	facts.Lookups = make(map[string]uint64, len(openalerts.Answers))
	facts.GateOwnLookups = make(map[string]uint64, len(openalerts.Answers))
	for _, answer := range openalerts.Answers {
		facts.Lookups[string(answer)] = stats.Lookups[answer]
		facts.GateOwnLookups[string(answer)] = stats.OwnLookups[answer]
	}
	facts.GateOwnHeld = stats.OwnHeld
	if !stats.GateSince.IsZero() {
		since := stats.GateSince
		facts.GateSince = &since
	}
	facts.GateRecent, facts.GateRecentOwnHeld = gateLookupFacts(stats.RecentLookups), gateLookupFacts(stats.RecentOwnHeld)
	return facts
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

// openAlertComparisonFacts carries the copy's comparison into the replica's
// facts field for field.
func openAlertComparisonFacts(comparison *openalerts.Comparison) *fleet.OpenAlertComparison {
	if comparison == nil {
		return nil
	}
	facts := &fleet.OpenAlertComparison{OwnEventSourceID: comparison.OwnEventSourceID, Sent: comparison.Sent,
		SentShapes: comparison.SentShapes, MemberShapes: comparison.MemberShapes, AlertSources: comparison.AlertSources,
		SentInCalibrated: comparison.SentInCalibrated, SentMatchingAlertID: comparison.SentMatchingAlertID,
		SentMatchingFingerprint: comparison.SentMatchingFingerprint}
	for _, row := range comparison.Strategies {
		strategy := fleet.OpenAlertComparisonStrategy{TenantID: row.TenantID, StrategyID: row.StrategyID, Sent: row.Sent,
			Members: row.Members, Alerts: row.Alerts, Calibrated: row.Calibrated, SentSample: row.SentSample, MemberSample: row.MemberSample}
		for _, alert := range row.AlertSample {
			strategy.AlertSample = append(strategy.AlertSample, fleet.OpenAlertComparisonAlert{
				AlertID: alert.AlertID, Fingerprint: alert.Fingerprint, EventSourceID: alert.EventSourceID})
		}
		facts.Strategies = append(facts.Strategies, strategy)
	}
	return facts
}

// gateLookupFacts is the kept gate lookups as the replica publishes them.
func gateLookupFacts(lookups []openalerts.GateLookup) []fleet.GateLookupFact {
	if len(lookups) == 0 {
		return nil
	}
	out := make([]fleet.GateLookupFact, 0, len(lookups))
	for _, lookup := range lookups {
		out = append(out, fleet.GateLookupFact{At: lookup.At, TenantID: lookup.TenantID, StrategyID: lookup.StrategyID,
			Fingerprint: lookup.Fingerprint, Answer: string(lookup.Answer), Open: lookup.Open, Own: lookup.Own,
			InOtherSets: append([]string(nil), lookup.InOtherSets...)})
	}
	return out
}
