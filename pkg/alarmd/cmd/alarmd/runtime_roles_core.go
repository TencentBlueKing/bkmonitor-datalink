// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/memoryline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

// phaseTwoCoreResources assembles the shared control/read contract. Worker
// execution storage is created only for a process that executes Query Groups.
type phaseTwoCoreResources struct {
	Repository      *controlplane.RedisCatalogRepository
	Catalog         *controlplane.RedisCatalogRuntime
	Ownership       *ownership.RedisStore
	Progress        *progress.Store
	Control         *productionPhaseTwoControl
	Reconciler      *controlplane.SourceReconciler
	Activator       *controlplane.ScheduleActivationReconciler
	TimelineRepairs *controlplane.TimelineRepairRequests
	TimelineCache   config.DerivedControlTimelineCache
	RecoveryLimits  scheduler.RecoveryLimits
	Retention       execution.SlotRetention
	StorageRouter   *state.FixedRouter
	ExecutionStore  *state.ExecutionStore
	StrategySource  controlplane.StrategySource
}

func openPhaseTwoCoreResources(ctx context.Context, cfg config.Config, recorder *metric.Recorder, observer observability.Observer,
	controlClient, runtimeClient redis.UniversalClient, platformSettings *platformsettings.Cache, now func() time.Time,
	wait startupWaiter, observationMemory *memoryline.Line, newStrategySource productionStrategySourceFactory, closeControl func() error,
) (*phaseTwoCoreResources, error) {
	compiler, err := newPlanCompiler(cfg)
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

	var strategySource controlplane.StrategySource
	var planner controlplane.PrimaryQueryCompiler
	// A fallback candidate builds the same source and compiler as a control
	// process and leaves them idle: a follower's rounds never read them, and
	// a fallback that takes the lease runs the core control rounds with them
	// unchanged. Building them costs a client handle and a compiler, no
	// reads.
	candidacy := phaseTwoControlCandidacy(cfg)
	if candidacy != "" {
		strategySource, err = newStrategySource(redisForCaller(controlClient, redisfailure.CallerStrategySource), cfg.PhaseTwo.Control.StrategyCachePrefix)
		if err != nil {
			return nil, err
		}
		// The compiler reads the copy when each control round opens, so a
		// setting the platform changes reaches the plans on the next round:
		// every strategy recompiles under it and the cutover carries the new
		// Catalog out. Within a round the facts are frozen.
		planner, err = newPlatformBoundPlanner(cfg, platformSettings)
		if err != nil {
			return nil, err
		}
	}
	repository, err := controlplane.NewRedisCatalogRepository(
		redisForCaller(runtimeClient, redisfailure.CallerControlPlane), productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg),
	)
	if err != nil {
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
		// A Query Group's read hold is bounded by the replay age: a Slot the
		// scheduler already tolerates running that late (readhold design,
		// bound (c)).
		ReadHoldBound: recoveryLimits.MaxReplayAge,
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
	// The cache's own reading of what its objects take decoded, against the
	// charge its unused budget is reserved at.
	if err := recorder.BindDecodedObjects(func() (uint64, float64, float64) {
		reading := repository.DecodedObjectReading()
		return reading.Samples, reading.Last, reading.Max
	}); err != nil {
		return nil, err
	}
	observationMemory.Reserve("timeline_cache", repository.TimelineCacheBudget)
	observationMemory.Reserve("object_cache", repository.ObjectCacheBudget)
	repository.ConfigureObserver(observer)
	// The cache counters are what said a decoded-timeline cache was worth
	// building, and nothing consumed them before. The timeline occupancy joins
	// them so the derived budget can be read against the working set it was
	// sized for: entries against the Query Groups this Worker owns, evictions
	// against a version header that is not moving.
	recorder.SetControlCacheSource(func() []metric.ControlCacheCounts {
		stats := repository.ControlReadCacheStats()
		occupancy := stats.TimelineOccupancy
		objects := repository.ObjectCacheStats()
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
			// The Query Group objects and output contexts by digest, in the
			// stored bytes it is bounded by; the observation memory line
			// charges them decoded (observation_memory_budget_*_bytes).
			{Object: "catalog_object", Hits: objects.Hits, Misses: objects.Misses, Evictions: objects.Evictions,
				Occupancy: &metric.ControlCacheOccupancy{
					Entries: float64(objects.Occupancy.Entries), Bytes: float64(objects.Occupancy.Bytes),
					BytesLimit: float64(objects.Occupancy.MaxBytes),
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
	if err := reconciler.ConfigureClock(now); err != nil {
		return nil, err
	}
	catalog, err := controlplane.NewRedisCatalogRuntime(
		repository, compiler, strategySemantics, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration(),
	)
	if err != nil {
		return nil, err
	}
	ownershipStore, err := newProductionOwnershipStore(cfg, redisForCaller(runtimeClient, redisfailure.CallerOwnership))
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

	var storageRouter *state.FixedRouter
	var executionStore *state.ExecutionStore
	if cfg.HasRole(roles.Worker) {
		stateBackend, err := state.NewRedisBackendWithClient(productionRedisAddress(cfg.RuntimeStoreRedis()), redisForCaller(runtimeClient, redisfailure.CallerRuntimeState))
		if err != nil {
			return nil, err
		}
		if err := wait.await(ctx, "state_store", stateBackend.Ping); err != nil {
			return nil, err
		}
		storageRouter, err = state.NewFixedRouter("phase-two-primary", stateBackend)
		if err != nil {
			return nil, err
		}
		executionStore, err = state.NewExecutionStore(state.ExecutionStoreOptions{
			Prefix: cfg.Redis.StatePrefix, Router: storageRouter, MaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes,
			MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch,
			// The store derives each write TTL from the Plan retention the request
			// carries; these only bound it, exactly as they do for the phase-one
			// store built in config.StoreOptions.
			MinTTL: cfg.Redis.MinTTL.Duration(), MaxTTL: cfg.Redis.MaxTTL.Duration(),
			RestartMargin: cfg.Redis.RestartMargin.Duration(),
			ReadHoldBound: recoveryLimits.MaxReplayAge,
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
	}
	progressStore, err := progress.NewStore(progress.StoreOptions{
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), Control: ownershipStore,
		Slots: catalog, Now: now, Observer: observer,
	})
	if err != nil {
		return nil, err
	}
	activator, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, strategySemantics, progressStore, now,
	)
	if err != nil {
		return nil, err
	}
	// What the leader's round reads off the registrations and the next
	// activation reads: the Query Groups the Workers report an unreadable
	// timeline for.
	timelineRepairs := &controlplane.TimelineRepairRequests{}
	activator.WithTimelineRepairs(timelineRepairs)
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Roles:            cfg.EffectiveRoles(),
		ControlCandidate: candidacy == ownership.ControlCandidacyFallback,
		Source:           strategySource, Planner: planner, Reconciler: reconciler, Activator: activator,
		Repository: repository, Schedules: catalog, Progress: progressStore,
		Observer: observer,
		Recorder: recorder,
		Now:      now, MaxReplayAge: cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
		Close: closeControl,
	})
	if err != nil {
		return nil, err
	}
	return &phaseTwoCoreResources{
		Repository:      repository,
		Catalog:         catalog,
		Ownership:       ownershipStore,
		Progress:        progressStore,
		Control:         control,
		Reconciler:      reconciler,
		Activator:       activator,
		TimelineRepairs: timelineRepairs,
		TimelineCache:   timelineCache,
		RecoveryLimits:  recoveryLimits,
		Retention:       retention,
		StorageRouter:   storageRouter,
		ExecutionStore:  executionStore,
		StrategySource:  strategySource,
	}, nil
}
