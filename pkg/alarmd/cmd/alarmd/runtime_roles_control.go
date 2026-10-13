// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/go-redis/redis/v8"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/memoryline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// openProductionControlRoleBundle owns compilation, activation, placement and
// source-driven global closes. It opens no CMDB cache, downstream UQ client,
// execution store, evaluator, flight coordinator or Query Group runner.
func openProductionControlRoleBundle(
	ctx context.Context, cfg config.Config, recorder *metric.Recorder, logger *observability.Logger,
	health *phaseTwoApplicationHealth, newStrategySource productionStrategySourceFactory,
	external phaseTwoProductionExternalDependencies,
) (_ *phaseTwoWorkerBundle, resultErr error) {
	if ctx == nil || recorder == nil || logger == nil || health == nil || newStrategySource == nil ||
		external.Now == nil || external.PrepareEvents == nil {
		return nil, errors.New("phase-two production Control role dependencies are incomplete")
	}
	if !cfg.HasRole(roles.Control) || cfg.HasRole(roles.Worker) {
		return nil, errors.New("phase-two isolated Control assembly requires control role without worker role")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.WithResolvedRedisPoolSize()
	wait := external.startupWaiter(recorder, logger, health)
	cfg, linkdDiscovery := adoptLinkdLocation(ctx, cfg, openalerts.DiscoverTarget)
	baseObserver, err := newPhaseTwoRuntimeObserver(recorder, logger)
	if err != nil {
		return nil, err
	}
	observer := phaseTwoRuntimeObserver(observability.Multi(
		observability.Named(observability.ObserverTracker, baseObserver),
		observability.Named(observability.ObserverAdditional, external.AdditionalObserver)))
	observationMemory := memoryline.New()
	if err := recorder.BindObservationMemory(observationMemory.Read); err != nil {
		return nil, err
	}
	var ownedClients []redis.UniversalClient
	closeOwned := func() error {
		var errs []error
		for _, client := range ownedClients {
			errs = append(errs, client.Close())
		}
		return errors.Join(errs...)
	}
	defer func() {
		if resultErr != nil {
			observationMemory.Close()
			resultErr = errors.Join(resultErr, closeOwned())
		}
	}()
	sourceConnection, runtimeConnection := cfg.StrategySourceRedis(), cfg.RuntimeStoreRedis()
	controlClient, err := wait.openRedis(ctx, "redis_source", sourceConnection, recorder.RedisHook("source"))
	if err != nil {
		return nil, err
	}
	var closeSourceOnce sync.Once
	var closeSourceErr error
	closeSource := func() error {
		closeSourceOnce.Do(func() { closeSourceErr = controlClient.Close() })
		return closeSourceErr
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, closeSource())
		}
	}()
	runtimeClient := controlClient
	runtimeIsSource := sameRedisConnection(sourceConnection, runtimeConnection)
	if !runtimeIsSource {
		runtimeClient, err = wait.openRedis(ctx, "redis_runtime", runtimeConnection, recorder.RedisHook("runtime"))
		if err != nil {
			return nil, err
		}
		ownedClients = append(ownedClients, runtimeClient)
	}
	var dynamicClient redis.UniversalClient
	dynamicOwned := false
	dynamicConnection, dynamicConfigured := cfg.DynamicConfigRedis()
	if dynamicConfigured {
		switch {
		case sameRedisConnection(dynamicConnection, sourceConnection):
			dynamicClient = controlClient
		case sameRedisConnection(dynamicConnection, runtimeConnection):
			dynamicClient = runtimeClient
		default:
			dynamicClient, err = wait.openRedis(ctx, "redis_dynamic_config", dynamicConnection, recorder.RedisHook("dynamic_config"))
			if err != nil {
				return nil, err
			}
			dynamicOwned = true
			ownedClients = append(ownedClients, dynamicClient)
		}
	}
	recorder.SetRedisPoolSource(func() []metric.RedisPoolCounts {
		counts := []metric.RedisPoolCounts{redisPoolCounts("source", sourceConnection.PoolSize, controlClient)}
		if !runtimeIsSource {
			counts = append(counts, redisPoolCounts("runtime", runtimeConnection.PoolSize, runtimeClient))
		}
		if dynamicOwned {
			counts = append(counts, redisPoolCounts("dynamic_config", dynamicConnection.PoolSize, dynamicClient))
		}
		return counts
	})
	settings, err := buildPlatformSettings(ctx, cfg, redisForCaller(dynamicClient, redisfailure.CallerDynamicConfig), external.Now)
	if err != nil {
		return nil, err
	}
	recorder.SetPlatformSettingsSource(settings.Stats)
	core, err := openPhaseTwoCoreResources(ctx, cfg, recorder, observer, controlClient, runtimeClient,
		settings, external.Now, wait, observationMemory, newStrategySource, closeSource)
	if err != nil {
		return nil, err
	}

	// A control process still publishes global closes. Keep the same native
	// and compatibility conversion and lazy, retrying output lifecycle.
	opener, err := external.PrepareEvents(cfg.Kafka.TriggerEventCoordinates())
	if err != nil {
		return nil, err
	}
	events, err := newLazyOutputSink(opener, external.Now)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, events.Close())
		}
	}()
	serviceClient, err := wait.openRedis(ctx, "redis_legacy_output", cfg.Kafka.LegacyAdapter.ServiceRedis, recorder.RedisHook("legacy_output"))
	if err != nil {
		return nil, fmt.Errorf("open kafka.legacy_adapter.service_redis: %w", err)
	}
	ownedClients = append(ownedClients, serviceClient)
	converter := &legacyoutput.Converter{Store: legacyoutput.RedisSnapshotStore{Client: serviceClient},
		SnapshotPrefix: cfg.Kafka.LegacyAdapter.SnapshotPrefix, Now: external.Now, PluginID: cfg.Kafka.LegacyAdapter.PluginID}
	if podConfig := cfg.Kafka.LegacyAdapter.PodCache; podConfig != nil {
		podClient := redis.NewUniversalClient(productionRedisOptions(podConfig.Connection))
		podClient.AddHook(recorder.RedisHook("legacy_pod_cache"))
		ownedClients = append(ownedClients, podClient)
		resolver, err := legacyoutput.NewDjangoPodResolver(podClient, legacyoutput.PodCacheConfig{
			KeyPrefix: podConfig.KeyPrefix, Version: podConfig.Version, Observe: recorder.RecordLegacyPodCache,
		})
		if err != nil {
			return nil, err
		}
		converter.Pods = resolver
	}
	standard, err := linkdoutput.NewConverter(recorder.RecordUnmappedSeverity)
	if err != nil {
		return nil, err
	}
	if err := events.ConfigureStandardOutput(standard); err != nil {
		return nil, err
	}
	if err := events.ConfigureLegacyOutput(converter, cfg.Kafka.LegacyAdapter.Topic, cfg.Kafka.TriggerEvent.MaxMessageBytes); err != nil {
		return nil, err
	}
	openLinkd := func(connection config.RedisConnectionConfig) (redis.UniversalClient, bool) {
		if sameRedisConnection(connection, runtimeConnection) {
			return redisForCaller(runtimeClient, redisfailure.CallerLinkd), false
		}
		client := redis.NewUniversalClient(productionRedisOptions(connection))
		client.AddHook(recorder.RedisHook("linkd"))
		return client, true
	}
	linkdConnection := runtimeConnection
	linkdClient := redisForCaller(runtimeClient, redisfailure.CallerLinkd)
	if cfg.PhaseTwo.Linkd.Connection != nil {
		linkdConnection = *cfg.PhaseTwo.Linkd.Connection
		var owned bool
		linkdClient, owned = openLinkd(linkdConnection)
		if owned {
			ownedClients = append(ownedClients, linkdClient)
		}
	}
	linkd, err := newLinkdIndex(cfg, linkdClient, linkdConnection, linkdDiscovery, openLinkd, external.Now, logger)
	if err != nil {
		return nil, err
	}
	closeMovedLinkd := func() error {
		if client := linkd.Location.OwnedClient(); client != nil {
			return client.Close()
		}
		return nil
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, closeMovedLinkd())
		}
	}()
	recorder.SetOpenAlertSetSource(linkd.Cache.Stats)
	recorder.SetLinkdConsoleSource(fleet.LinkdConsoleStates, openalerts.ConsoleOps, openalerts.ConsoleFailureClasses,
		linkdConsoleReading(linkd.Console, external.Now))
	recorder.SetActivationRebuildSource(core.Repository.ActivationRebuildCounts)
	recorder.SetTimelineRepairSource(core.Repository.TimelineRepairCounts)
	recorder.SetActivationSkippedTimelinesSource(core.Repository.SkippedTimelinesReading)
	recorder.SetActivationHeaderSource(core.Repository.ActivationHeaderReading)
	recorder.SetActivationBlockedSource(core.Repository.ActivationBlockedReading)
	recorder.SetActivationBodyBytesSource(core.Repository.ActivationBodyBytes)

	streamIdentity, incarnation := viewStreamIdentity{}, ""
	if external.RoleRuntime != nil {
		streamIdentity, incarnation = external.RoleRuntime.StreamIdentity, external.RoleRuntime.Incarnation
	} else {
		streamIdentity, err = newViewStreamIdentity(cfg.HTTP.Listen, viewStreamRoutes(runtimeConnection)...)
		if err != nil {
			return nil, err
		}
		incarnation, err = newViewStreamIncarnation()
		if err != nil {
			return nil, err
		}
	}
	costs := scheduler.NewCostLedger(external.Now)
	viewServer, err := viewstream.NewServer(viewStreamAdmission{registry: core.Ownership, now: external.Now}, observer,
		viewstream.ServerOptions{Now: external.Now, Costs: costLedgerSink{ledger: costs}})
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			viewServer.Close()
		}
	}()
	compatibility, err := phaseTwoExpectedWorkerCompatibility(cfg)
	if err != nil {
		return nil, err
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(compatibility)
	if err != nil {
		return nil, err
	}
	assignments, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), core.Ownership)
	if err != nil {
		return nil, err
	}
	assignments.WithTimelineRevisions(core.Repository)
	controlOwnership, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Roles: cfg.EffectiveRoles(), Store: core.Ownership, WorkerID: cfg.PhaseTwo.Worker.ID,
		Catalog: core.Catalog, Progress: core.Progress, Now: external.Now, Observer: observer,
		ControlLeaderTTL: cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration(), Reconcile: assignments,
		LeaseTTL: cfg.PhaseTwo.Ownership.LeaseTTL.Duration(), ReconcileInterval: cfg.PhaseTwo.Control.ReconcileInterval.Duration(),
		ContentScopes: currentContentScopes(core.Repository), SteppedDownAsLeader: controlLeaderSteppedDown(core.Reconciler, recorder),
		ViewStream: viewServer, ViewSource: core.Repository, Costs: costs, TimelineRepairs: core.TimelineRepairs,
	})
	if err != nil {
		return nil, err
	}
	core.Activator.WithContentScopeWriter(controlOwnership)
	controlStream := grpc.NewServer()
	pb.RegisterControlServiceServer(controlStream, viewServer)
	recorder.SetViewStreamSource(func() metric.ViewStreamCounts { return viewStreamCounts(viewServer.Stats()) })
	fleetRetention := max(fleet.DefaultTTL, 4*cfg.PhaseTwo.Control.ReconcileInterval.Duration())
	fleetStore, err := fleet.NewRedisStore(redisForCaller(runtimeClient, redisfailure.CallerFleet),
		productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), fleetRetention, 0)
	if err != nil {
		return nil, err
	}
	fleetStore.Meter(recorder)
	fleetStore.HoldLoads(observationHold(observationMemory, memoryline.ConsumerFleetView))
	service, err := fleet.NewService(controlPlaneExpectation{repository: core.Repository}, registryReplicas{store: core.Ownership},
		fleetStore, 6*cfg.PhaseTwo.Control.ReconcileInterval.Duration(), external.Now)
	if err != nil {
		return nil, err
	}
	service.SetReplica(cfg.PhaseTwo.Worker.ID)
	if err := wireRoleControlFacts(service, core.Ownership, fleetStore, external.RoleRuntime); err != nil {
		return nil, err
	}
	stallAfter := cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration()
	if err := recorder.BindFleet(fleetVerdictSource(service, external.Now, stallAfter, fleetVerdictScrapeCeiling)); err != nil {
		return nil, err
	}
	windows, err := fleet.NewWindowStore(redisForCaller(runtimeClient, redisfailure.CallerFleet), productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"))
	if err != nil {
		return nil, err
	}
	native, err := fleet.NewHandler(service, windows, external.Now, cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration(), nil, nil,
		cfg.PhaseTwo.Access.MonitorWebBaseURL)
	if err != nil {
		return nil, err
	}
	var bundle *phaseTwoWorkerBundle
	directory, err := controlplane.NewDirectoryView(core.Reconciler, core.Repository, directoryReadTimeout)
	if err != nil {
		return nil, err
	}
	discovery := viewStreamDiscovery{store: core.Ownership}
	absence := catalogAbsenceSource(func() *phaseTwoWorkerBundle { return bundle })
	native = fleet.WithStrategyDirectory(native, directory,
		leaderForwarderWithin(discovery, cfg.PhaseTwo.Worker.ID, nil, directoryForwardTimeout, "directory", recorder.ObserveLeaderForward),
		absence, strategyStandingReplica(cfg.PhaseTwo.Worker.ID), external.Now)
	native = fleet.WithStrategyStanding(native, service, strategyLookupSource(core.Reconciler),
		leaderForwarder(discovery, cfg.PhaseTwo.Worker.ID, nil, recorder.ObserveLeaderForward), strategyObjectLoader(core.Repository),
		absence, strategyStandingReplica(cfg.PhaseTwo.Worker.ID), external.Now, cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration())
	diagnosisWarmer := fleet.NewDiagnosisWarmer(service, strategyLookupSource(core.Reconciler),
		diagnosisUniverse(core.StrategySource), diagnosisProgress(core.Progress, observationAdmit(observationMemory, memoryline.ConsumerDiagnosisProgress)), external.Now, stallAfter)
	native = fleet.WithDiagnosis(native, service, strategyLookupSource(core.Reconciler),
		leaderForwarderWithin(discovery, cfg.PhaseTwo.Worker.ID, nil, diagnosisForwardTimeout, "diagnosis", recorder.ObserveLeaderForward),
		diagnosisUniverse(core.StrategySource), diagnosisProgress(core.Progress, observationAdmit(observationMemory, memoryline.ConsumerDiagnosisProgress)),
		strategyStandingReplica(cfg.PhaseTwo.Worker.ID), external.Now, stallAfter, diagnosisWarmer)
	absentPage := &absentCandidatePage{}
	native = fleet.WithAbsentCandidates(native, absentPage.source,
		leaderForwarderWithin(discovery, cfg.PhaseTwo.Worker.ID, nil, absentForwardTimeout, absentForwardRoute, recorder.ObserveLeaderForward), cfg.PhaseTwo.Worker.ID)
	var closeCLI = func() error { return nil }
	publicRestricted := false
	binding := cliControlBinding{Incarnation: incarnation, StreamToken: streamIdentity.Token, Server: viewServer,
		Metrics: recorder.Gatherer(), PublicWindows: fleet.NewPublicWindowsHandler(windows, external.Now),
		RedisFailures: cliRedisFailures(recorder, observer), RedisDialRetries: recorder.ObserveDiagnosticRedisDialRetry}
	facts := func() *observability.RuntimeConfigFacts {
		if bundle == nil {
			return nil
		}
		return bundle.runtimeConfig
	}
	var channelBinding *ChannelBinding
	if external.RoleRuntime != nil {
		channelBinding = &ChannelBinding{Native: native, Catalog: core.Repository, Progress: core.Progress,
			Settings: settings, Control: binding, Roles: cfg.EffectiveRoles()}
		native, publicRestricted = external.RoleRuntime.Channel.Handler, external.RoleRuntime.Channel.Restricted
	} else {
		native, closeCLI, publicRestricted = buildPhaseTwoCLI(cfg, native, core.Repository, core.Progress, settings, facts, binding)
		defer func() {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, closeCLI())
			}
		}()
	}
	census := &storeCensus{now: external.Now, stores: censusStoresOf(cfg, controlClient, runtimeClient, serviceClient), vocabulary: censusVocabulary(cfg)}
	if err := recorder.BindStoreCensus(census.read); err != nil {
		return nil, err
	}
	bundle, err = newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
		Config: cfg, Health: health, Control: core.Control, Ownership: controlOwnership, Recorder: recorder, Observer: observer, Now: external.Now,
		FleetAPI: native, PublicSurfaceRestricted: publicRestricted, ControlStream: controlStream, StreamIdentity: streamIdentity,
		ViewStreamStats: viewServer.Stats, ActivationBlocked: core.Repository.ActivationBlockedReading,
		ActivationSkipped: core.Repository.SkippedTimelinesReading, ActivationHeader: core.Repository.ActivationHeaderReading,
		RunOpenAlerts: func(runCtx context.Context) error {
			go linkd.Location.retry(runCtx, cfg, openalerts.DiscoverTarget, linkdRetryFirst, linkdRetryCeiling)
			return linkd.Cache.Run(runCtx)
		},
		RefreshPlatformSettings: func(refreshCtx context.Context) { settings.Refresh(refreshCtx) },
		ProbeControlRedis:       func(probeCtx context.Context) error { return controlClient.Ping(probeCtx).Err() }, MeasureStores: census.measure,
		PublishFleet: func(publishCtx context.Context) {
			publishRoleControlFacts(publishCtx, bundle, fleetStore, external.RoleRuntime)
			if warm := diagnosisWarmer.Tick(); warm != nil {
				go warm(publishCtx)
			}
		},
		CloseResources: func(shutdownCtx context.Context) error {
			observationMemory.Close()
			viewServer.Close()
			return errors.Join(events.Shutdown(shutdownCtx), closeCLI(), closeMovedLinkd(), closeOwned())
		},
	})
	if err != nil {
		return nil, err
	}
	if linkd.Console != nil {
		closeLoop := newAbsentStrategyClose(bundle, core.Reconciler, linkd.Console, events)
		closeLoop.documents, _ = core.StrategySource.(controlplane.StrategyDocumentPresence)
		absentPage.bind(closeLoop)
		bundle.dependencies.RunAbsentClose = closeLoop.run
		recorder.SetAbsentCloseSource(closeLoop.Stats, closeLoop.Rounds, closeLoop.Difference)
	}
	profile, err := phaseTwoRuntimeProfile(cfg, "container_derived", runtime.GOMAXPROCS(0))
	if err != nil {
		return nil, err
	}
	bundle.runtimeConfig = &profile
	events.SetOnChange(bundle.outputSinkChanged)
	events.Start()
	if channelBinding != nil {
		external.RoleRuntime.Channel.Bind(*channelBinding)
	}
	return bundle, nil
}
