// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"linkd/internal/alertqueue"
	"linkd/internal/blueking"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	streams "linkd/internal/controlplane/redisstream"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/eventgen"
	eventgenstore "linkd/internal/eventgen/storage"
	"linkd/internal/eventsource"
	sourcestore "linkd/internal/eventsource/storage"
	"linkd/internal/ingestpreview"
	"linkd/internal/kaccompat"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/lifecycle/scheduler"
	mergeflow "linkd/internal/merge"
	"linkd/internal/onemodel"
	onemodelassembly "linkd/internal/onemodel/assembly"
	"linkd/internal/onemodel/queryservice"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/policy/simulation"
	policystore "linkd/internal/policy/storage"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/suppressioncheck"
	"linkd/internal/suppressioncleanup"
	"linkd/internal/taskdispatch"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

func runDispatch(ctx context.Context, cfg config.Config, logger *slog.Logger, metrics *telemetry.Runtime, registry *taskstate.Registry, providers ...eventsource.Provider) error {
	d := cfg.Dispatch.WithDefaults()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// 1. 先装配已有集合的连接；存储初始化失败不得阻止已有调度与管理读取。
	docs, e := sourcestore.OpenExisting(startup, *cfg.Storage, d.Deployment)
	if e != nil {
		return e
	}
	defer func() { _ = docs.Close() }()
	var generatorDocs *eventgenstore.Store
	if d.EventgenEnabled {
		generatorDocs, e = eventgenstore.OpenExisting(startup, *cfg.Storage, d.Deployment)
		if e != nil {
			return e
		}
		defer func() { _ = generatorDocs.Close() }()
	}
	policyDocs, err := policystore.OpenExisting(startup, *cfg.Storage, d.Deployment)
	if err != nil {
		return err
	}
	defer func() { _ = policyDocs.Close() }()
	shieldJournal, err := shieldcheck.NewJournal(policyDocs)
	if err != nil {
		return err
	}
	policies := policy.NewService(policyDocs)
	severity := runtimeconfig.NewSeverity(cfg.Severity)
	sources := eventsource.New(docs, cfg.Severity, eventsource.Options{Cleaner: cfg.Cleaner, Resources: cfg.Resources, SourceDefaults: sourceLimitDefaults})
	sources.UseSeverity(severity)
	mergeJournal, err := mergeflow.NewJournal(policyDocs)
	if err != nil {
		return err
	}
	client, e := redisclient.New(cfg.Storage.Redis.ClientOptions())
	if e != nil {
		return e
	}
	defer func() { _ = client.Close() }()
	controllerReady := make(chan struct{})
	initializationObserver := metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskControlStorage)
	// 2. 配置集合先在后台准备；新部署的首次调度读取可等待它推进，已有调度不等待 DDL。
	initialization := taskgroup.Task{Name: "control-storage", Run: func(ctx context.Context) error {
		registry.Begin("control-storage", "", "")
		started := time.Now()
		call, stop := context.WithTimeout(ctx, startupTimeout)
		defer stop()
		err := docs.EnsureSchema(call)
		if err == nil {
			err = policyDocs.EnsureSchema(call)
		}
		if err == nil && cfg.Lifecycle != nil {
			// 调度历史检查通过后才发布内置来源，不能绕过历史丢失时的显式恢复要求。
			select {
			case <-controllerReady:
				_, err = sources.EnsureMergeSource(call, "linkd")
			case <-call.Done():
				err = call.Err()
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				initializationObserver.RunFinished(ctx, time.Since(started), false)
			}
			return err
		}
		registry.Finish(ctx, "control-storage", "", "", time.Since(started), 0, 0, "")
		initializationObserver.RunFinished(ctx, time.Since(started), true)
		<-ctx.Done()
		return nil
	}}
	initialization = registry.Wrap("control-storage", superviseTask(logger, registry, "control-storage", initialization))
	initializationCtx, stopInitialization := context.WithCancel(ctx)
	initializationDone := make(chan error, 1)
	go func() {
		initializationObserver.SetActive(initializationCtx, true)
		defer initializationObserver.SetActive(context.WithoutCancel(initializationCtx), false)
		initializationDone <- initialization.Run(initializationCtx)
	}()
	defer func() { stopInitialization(); <-initializationDone }()

	// 3. 独立获取调度资格；初次部署允许后台集合准备完成后再读，不重建已有协调历史。
	var controller *taskdispatch.Controller
	for {
		controller, e = taskdispatch.NewController(startup, client, sources, d.Deployment, dispatchTaskObserver{metrics.DispatchObserver(), registry, metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskScheduler), logger})
		if e == nil {
			break
		}
		if !waitForArchiveRetry(startup, time.Second) {
			return e
		}
	}
	close(controllerReady)
	lifecycle := config.LifecycleConfig{}
	dynamic, closeDynamic, e := openDynamicConfig(ctx, cfg, severity, logger)
	if e != nil {
		return e
	}
	defer func() { _ = closeDynamic() }()
	if cfg.Lifecycle != nil {
		lifecycle = *cfg.Lifecycle
	}
	oneModelQueries := queryservice.New(nil, nil)
	if cfg.Resources.OneModel != nil {
		client, transport, err := onemodelassembly.Open(cfg.Resources.OneModel, 4, 10*time.Second)
		if err != nil {
			return err
		}
		defer func() { _ = transport.Close() }()
		pager, err := onemodel.NewPager(client)
		if err != nil {
			return err
		}
		oneModelQueries = queryservice.New(pager, client)
	}
	policyResources, err := policyruntime.Open(cfg.Resources, cfg.Blueking)
	if err != nil {
		return err
	}
	defer func() { _ = policyResources.Close() }()
	policyResources.Catalog = policy.NewCatalogWithTTL(policies, time.Minute)
	api := &controlapi.API{Policies: policies, Tasks: registry, OneModel: oneModelQueries, Previewer: newEnrichPreview(sources, *cfg.Storage, cfg.Resources, logger), DynamicConfig: dynamic, Lifecycle: lifecycle, Sources: sources, Controller: controller, Config: d}
	if generatorDocs != nil {
		api.Eventgen = eventgen.NewService(generatorDocs, sources)
		api.EventgenSeverity = func() config.SeverityConfig { return severity.SeveritySnapshot().Severity }
	}
	api.PolicyDependencies = policyResources.Dependencies
	api.PolicyCatalog = policyResources.Catalog
	// 单租户目录无需蓝鲸连接；多租户目录独立于 CMDB 资源，缺少连接时由接口明确报告不可用。
	if !cfg.Blueking.EnableMultiTenantMode {
		api.Tenants = blueking.SingleTenantDirectory{}
	} else if cfg.Blueking.APIURL != "" {
		tenants, err := blueking.New(cfg.Blueking)
		if err != nil {
			return err
		}
		defer tenants.Close()
		api.Tenants = tenants
	}
	api.IngestPreviewer = ingestpreview.New(sources, severity.SeveritySnapshot)
	api.PolicyPreviewer = newPolicyPreview(policies, *cfg.Storage, policyResources, severity)
	simulationUpgrade := ""
	if cfg.Lifecycle != nil {
		simulationUpgrade = cfg.Lifecycle.SeverityUpgradePolicy
	}
	api.PolicySimulator = simulation.New(policies, policyPreviewFacts{*cfg.Storage}, policyResources.Targets, severity.SeveritySnapshot, simulationUpgrade)
	// 管理读取复用上下文感知的部署 Redis 池，与后台裁决共享数据边界但不共享可写端口。
	queryOptions := cfg.Storage.Redis.ClientOptions()
	queryOptions.ContextTimeoutEnabled = true
	queryOptions.PoolSize = 4
	queryRedis, err := redisclient.New(queryOptions)
	if err != nil {
		return err
	}
	defer func() { _ = queryRedis.Close() }()
	queryWindows, err := redisstate.New(queryRedis, d.Deployment)
	if err != nil {
		return err
	}
	api.MergeRuntime = controlapi.NewMergeRuntime(mergeJournal, queryWindows)
	api.SuppressionRuntime = controlapi.NewSuppressionRuntime(queryWindows)
	api.PolicyStatistics = queryWindows
	cleanupJournal, err := suppressioncleanup.NewJournal(policyDocs, time.Now)
	if err != nil {
		return err
	}
	api.SuppressionCleanups = controlapi.NewSuppressionCleanups(cleanupJournal)
	// 查询 API 保留原 8 个连接，投影读取连接预算随通道数扩展。
	queryConnections := 8
	if hasKACDelivery(cfg) {
		queryConnections += cfg.Plugins.KAC.Delivery.WithDefaults().ProjectionConcurrency
	}
	queryRepository, err := storeassembly.OpenReadOnly(ctx, *cfg.Storage, queryConnections)
	if err != nil {
		return err
	}
	defer func() { _ = queryRepository.Close() }()
	shieldReader, ok := metrics.ObserveRepository(queryRepository.Repository).(controlapi.ShieldRuntimeStore)
	if !ok {
		return fmt.Errorf("repository does not support shield runtime queries")
	}
	sourceReader, ok := queryRepository.Repository.(store.SourceAlertReader)
	if !ok {
		return fmt.Errorf("repository does not support source alert queries")
	}
	api.SourceAlerts = controlapi.NewSourceAlerts(sourceReader)
	alertSearcher, _ := queryRepository.Repository.(controlapi.AlertSearcher)
	api.AlertSearch = controlapi.NewAlertSearch(alertSearcher, d.AlertSearch, logger)
	api.EnrichRecords = controlapi.NewEnrichRecords(queryRepository.Repository, sources)
	api.ShieldRuntime = controlapi.NewShieldRuntime(shieldReader)
	var closeProjection lifecycleprocess.AlertProjectionSynchronizer
	var kacRuntime *kaccompat.Runtime
	var projectionSender *kaccompat.Client
	if hasKACDelivery(cfg) {
		projectionSender, err = openKACProjectionSender(cfg, severity, mergeJournal)
		if err != nil {
			return err
		}
		defer projectionSender.Close()
		sender := kaccompat.NewHTTPSender(*cfg.Plugins.KAC)
		defer sender.Close()
		kacRuntime = kaccompat.NewRuntime(cfg.Plugins.KAC.Delivery, queryRedis, alertqueue.Stream(d.Deployment), kacAlertReader{queryRepository.Repository}, projectionSender, sender, metrics, logger)
		closeProjection = kacRuntime
	}
	closer := lifecycleprocess.NewAlertCloser(cfg, sources, severity, logger, metrics, closeProjection)
	api.AlertCloser = closer
	api.KACCloser = closer
	api.ManualShieldBinder = lifecycleprocess.NewManualShieldBinder(cfg, sources, policies, policyResources, severity, logger, metrics)
	shieldObservations := policyruntime.NewObservations(queryWindows, metrics)
	defer shieldObservations.Close()
	shieldChecks := lifecycleprocess.NewShieldChecker(cfg, sources, policies, policyResources, severity, logger, metrics, shieldJournal, shieldObservations)
	var controlResources *lifecycleprocess.AlertProcessorResources
	if cfg.Lifecycle != nil {
		controlResources, err = lifecycleprocess.OpenAlertProcessorResources(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = controlResources.Close() }()
		controlResources.UseCleanupRecorder(cleanupJournal)
		closer.UseResources(controlResources)
		shieldChecks.UseResources(controlResources)
	}
	lockConfig := scheduler.DefaultConfig()
	lockConfig.LockKeyPrefix = fmt.Sprintf("linkd:shield-requests:%x", sha256.Sum256([]byte(d.Deployment)))
	requestLocker, err := scheduler.NewRedisLocker(queryRedis, lockConfig)
	if err != nil {
		return err
	}
	currentAlert := shieldReader.GetAlert
	if current, ok := shieldReader.(interface {
		GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
	}); ok {
		currentAlert = current.GetAlertCurrent
	}
	shieldRequests := lifecycleprocess.NewShieldRequests(shieldJournal, requestLocker, currentAlert, shieldChecks)
	suppressionJournal, err := suppressioncheck.NewJournal(policyDocs)
	if err != nil {
		return err
	}
	api.SuppressionChecks = controlapi.NewSuppressionChecks(suppressionJournal, nil)
	api.MergeRetries = controlapi.NewMergeRetries(mergeJournal, nil)
	var mergeRetries *mergeflow.RetryController

	var suppressionRequests *suppressioncheck.Controller
	if cfg.Lifecycle != nil {
		mergeLockConfig := scheduler.DefaultConfig()
		mergeLockConfig.LockTTL = max(mergeLockConfig.LockTTL, 30*time.Second)
		mergeLockConfig.LockKeyPrefix = fmt.Sprintf("linkd:merge-requests:%x", sha256.Sum256([]byte(d.Deployment)))
		mergeLocker, err := scheduler.NewRedisLocker(queryRedis, mergeLockConfig)
		if err != nil {
			return err
		}
		mergeRetries, err = mergeflow.NewRetryController(mergeJournal, mergeLocker)
		if err != nil {
			return err
		}
		api.MergeRetries = controlapi.NewMergeRetries(mergeJournal, mergeRetries)
		if _, ok := shieldReader.(interface {
			GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
		}); !ok {
			return fmt.Errorf("repository does not support current suppression owner read")
		}
		engine, err := suppressioncheck.NewEngine(queryWindows, currentAlert, suppressionOwnerLease(cfg, queryRedis), time.Now)
		if err != nil {
			return err
		}
		checkLockConfig := scheduler.DefaultConfig()
		checkLockConfig.LockTTL = max(checkLockConfig.LockTTL, 30*time.Second)
		checkLockConfig.LockKeyPrefix = fmt.Sprintf("linkd:suppression-requests:%x", sha256.Sum256([]byte(d.Deployment)))
		checkLocker, err := scheduler.NewRedisLocker(queryRedis, checkLockConfig)
		if err != nil {
			return err
		}
		suppressionRequests, err = suppressioncheck.NewController(suppressionJournal, checkLocker, engine)
		if err != nil {
			return err
		}
		api.SuppressionChecks = controlapi.NewSuppressionChecks(suppressionJournal, suppressionRequests)
	}
	api.ShieldDiagnostics = shieldJournal
	if cfg.Lifecycle != nil {
		api.ShieldRequests = shieldRequests
	}
	api.ModuleInfo, err = controlapi.NewModuleInfo(cfg, metrics.Version(), metrics.PrometheusListenAddress())
	if err != nil {
		return err
	}
	server := &http.Server{Addr: d.Listen, Handler: api.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second}
	// 上游读取可能耗尽最初的装配期限；已回退快照/YAML 后不应因此阻止管理 API 启动。
	listenCtx, cancelListen := context.WithTimeout(ctx, 3*time.Second)
	listener, e := (&net.ListenConfig{}).Listen(listenCtx, "tcp", d.Listen)
	cancelListen()
	if e != nil {
		return e
	}
	defer func() { _ = listener.Close() }()
	tasks := []taskgroup.Task{{Name: "scheduler", Run: func(ctx context.Context) error {
		return runControlCall(func() error { return controller.Run(ctx) })
	}}, {Name: "source-api", Run: func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			return server.Shutdown(shutdown)
		}
	}}}

	tasks = append(tasks, taskgroup.Task{Name: "source-limit-defaults", Run: func(ctx context.Context) error { return runSourceLimitDefaults(ctx, sources, logger, registry) }})
	if generatorDocs != nil {
		// 测试工具的集合准备独立重试，不阻塞正常来源初始化、调度和 API。
		tasks = append(tasks, taskgroup.Task{Name: "eventgen-storage", Run: func(ctx context.Context) error {
			call, stop := context.WithTimeout(ctx, startupTimeout)
			defer stop()
			if err := generatorDocs.EnsureSchema(call); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		}})
	}
	tasks = append(tasks, taskgroup.Task{Name: "policy-publication", Run: func(ctx context.Context) error {
		return runPolicyPublication(ctx, policies, logger, registry, metrics)
	}})
	tasks = append(tasks, taskgroup.Task{Name: "policy-catalog", Run: policyResources.Catalog.Run})
	if hasKACDelivery(cfg) {
		tasks = append(tasks, taskgroup.Task{Name: "kac-index-maintenance", Run: func(ctx context.Context) error {
			return runKACIndexMaintenance(ctx, projectionSender, registry, metrics, logger)
		}})
		tasks = append(tasks, taskgroup.Task{Name: "kac-delivery", Run: func(ctx context.Context) error {
			// 首次索引准备完成后才消费，避免新部署把缺失索引当成单条失败丢弃。
			for {
				err := projectionSender.Maintain(ctx)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return nil
				}
				logger.WarnContext(ctx, "KAC queue waiting for index initialization", "module", "kac", "operation", "wait_index", "error", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(time.Second):
				}
			}
			return kacRuntime.Run(ctx)
		}})
	}
	if cfg.Lifecycle != nil {
		current := queryRepository.Repository.GetAlert
		if live, ok := queryRepository.Repository.(store.LifecycleAlertStore); ok {
			current = live.GetAlertCurrent
		}
		currentBatch := queryRepository.Repository.GetAlerts
		if live, ok := queryRepository.Repository.(store.CurrentAlertBatchStore); ok {
			currentBatch = live.GetAlertsCurrent
		}
		indexer := &dependencyIndexManager{tenants: map[string]*dependencyIndexWork{}, wake: make(chan struct{}, 1), dependencies: policyResources.Dependencies, severity: severity, logger: logger,
			shielder: &policyruntime.Shielder{Loader: &policyruntime.Suppressor{Releases: policies, Catalog: policyResources.Catalog, Targets: policyResources.Dependencies},
				Candidates: queryRepository.Repository.(store.ActiveAlertReader), CurrentAlert: current, CurrentAlerts: currentBatch, Index: queryWindows, Dependency: queryWindows, Relations: policyResources.Dependencies, Logger: logger, Observer: metrics}}
		api.PolicyPreparation = indexer
		tasks = append(tasks, taskgroup.Task{Name: "dependency-candidate-index", Run: indexer.Run})
		tasks = append(tasks, taskgroup.Task{Name: "merge", Run: func(ctx context.Context) error {
			return runMergeTasks(ctx, cfg, sources, policies, mergeJournal, mergeRetries, policyResources, severity, logger, registry, metrics, controlResources)
		}})
		tasks = append(tasks, taskgroup.Task{Name: "suppression-requests", Run: func(ctx context.Context) error {
			return runSuppressionRequests(ctx, suppressionJournal, suppressionRequests, logger, registry, metrics)
		}})
		tasks = append(tasks, taskgroup.Task{Name: "shield-requests", Run: func(ctx context.Context) error {
			return runShieldRequests(ctx, shieldJournal, shieldRequests, logger, registry, metrics)
		}})
		tasks = append(tasks, taskgroup.Task{Name: "shield-check", Run: func(ctx context.Context) error {
			return runShieldChecks(ctx, cfg, shieldChecks, logger, registry, metrics)
		}})
		dependents, ok := shieldReader.(store.ShieldDependencyReader)
		if !ok {
			return fmt.Errorf("repository does not support current shield dependencies")
		}
		tasks = append(tasks, taskgroup.Task{Name: "shield-hints", Run: func(ctx context.Context) error {
			return runShieldHints(ctx, queryWindows, dependents, shieldChecks, logger, registry, metrics)
		}})

	}
	if dynamic != nil {
		tasks = append(tasks, taskgroup.Task{Name: "dynamic-config", Run: func(ctx context.Context) error {
			return dynamic.Run(ctx, dynamicTaskObserver{registry, metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskDynamicConfig)})
		}})
	}
	tasks = append(tasks, taskgroup.Task{Name: "active-alert-indexes", Run: func(ctx context.Context) error {
		return runActiveIndexes(ctx, cfg, sources, logger, registry, metrics)
	}})
	if settings := cfg.RedisStreamSettings(); settings != nil && settings.IsEnabled() && cfg.Lifecycle != nil {
		// 维护连接单独服从来源截止时间，避免裁剪请求占用调度器的共享连接池。
		streamOptions := cfg.Storage.Redis.ClientOptions()
		streamOptions.ContextTimeoutEnabled = true
		streamClient, err := redisclient.New(streamOptions)
		if err != nil {
			return err
		}
		defer func() { _ = streamClient.Close() }()
		after := ""
		tasks = append(tasks, newSourceStreamTask(metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskRedisStreamManager), func(ctx context.Context) error {
			ticker := time.NewTicker(settings.ReconcileInterval())
			defer ticker.Stop()
			for {
				after = reconcileSourceStreams(ctx, settings.OperationTimeout(), sources, after, registry, func(call context.Context, r eventsource.Record) error {
					started := time.Now()
					lc := cfg.Lifecycle.ForSource(d.Deployment, r.ID)
					manager, err := streams.NewManager(streamClient, streams.Config{Stream: lc.Signal.Stream, ExpectedGroup: lc.Signal.Group, ReconcileInterval: settings.ReconcileInterval(), OperationTimeout: settings.OperationTimeout(), MaxEntries: settings.MaxEntries, TrimBatchSize: settings.TrimBatchSize, MaxTrimEntriesPerCycle: settings.MaxTrimEntriesPerCycle}, metrics.RedisStreamObserver())
					if err == nil {
						err = manager.ReconcileOnce(call)
					}
					if err != nil && ctx.Err() == nil {
						logSourceStreamFailure(ctx, logger, r.ID, time.Since(started), err)
					}
					return err
				})
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
			}
		}))
	}
	for i, provider := range providers {
		tasks = append(tasks, taskgroup.Task{Name: fmt.Sprintf("source-provider-%d", i), Run: func(ctx context.Context) error {
			return sources.RunProvider(ctx, provider, 30*time.Second, func(err error) {
				logger.WarnContext(ctx, "event source provider reconciliation failed", "module", "controlplane", "operation", "source_reconcile", "error", err, "provider_index", i)
			}, providerTaskObserver{registry, metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskProviders), i})
		}})
	}
	for i := range tasks {
		id := tasks[i].Name
		if id == "source-stream-managers" {
			id = "redis-stream-manager"
		}
		if strings.HasPrefix(id, "source-provider-") {
			id = "source-providers"
		}
		task := tasks[i]
		// 调度租约失效和 HTTP 监听器退出需要清理并重建 dispatch；外层仍保持进程存活。
		if id != "scheduler" && id != "source-api" {
			task = superviseTask(logger, registry, id, task)
		}
		task = registry.Wrap(id, task)
		run := task.Run
		tasks[i].Run = func(ctx context.Context) error {
			// ES 与 Redis 的 active 已由既有观察器维护；其他任务在装配边界统一采集。
			if id != "redis-stream-manager" && id != "source-api" {
				o := metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTask(id))
				o.SetActive(ctx, true)
				defer o.SetActive(context.WithoutCancel(ctx), false)
			}
			return run(ctx)
		}
	}
	return taskgroup.Run(ctx, tasks)
}

// newSourceStreamTask 以整个来源遍历循环为一个进程级 owner。
// 每条 Stream 的 ReconcileFinished 已记录运行结果，此处只记录任务存活，
// 不在来源之间反复切换 active，也不额外累计成功次数。
func newSourceStreamTask(observer interface{ SetActive(context.Context, bool) }, run func(context.Context) error) taskgroup.Task {
	return taskgroup.Task{Name: "source-stream-managers", Run: func(ctx context.Context) error {
		observer.SetActive(ctx, true)
		defer observer.SetActive(context.WithoutCancel(ctx), false)
		return run(ctx)
	}}
}

const sourceStreamPageSize = 16

// reconcileSourceStreams 把来源读取失败和逐来源失败纳入同一轮结果。
// 来源读取与每个来源各自持有超时预算，防止慢来源耗尽后续来源的期限。
// 详情仅属于最近一页，最多 16 项；不把一次分页扫描解释为全部来源健康。
func reconcileSourceStreams(ctx context.Context, timeout time.Duration, sources interface {
	List(context.Context, string, int) ([]eventsource.Record, error)
}, after string, registry *taskstate.Registry, reconcile func(context.Context, eventsource.Record) error) string {
	started := time.Now()
	registry.Begin("redis-stream-manager", "", "")
	registry.RetainSteps("redis-stream-manager", nil)
	call, stop := context.WithTimeout(ctx, timeout)
	records, err := sources.List(call, after, sourceStreamPageSize)
	stop()
	code := ""
	failed, processed := 0, 0
	if err != nil {
		code = "source_list_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "source_list_timeout"
		}
	} else {
		for _, record := range records {
			if ctx.Err() != nil {
				break
			}
			after = record.ID
			processed++
			start := time.Now()
			call, stop := context.WithTimeout(ctx, timeout)
			err := reconcile(call, record)
			stop()
			reason := ""
			if err != nil && ctx.Err() == nil {
				reason = sourceStreamErrorCode(err)
				failed++
			}
			registry.Finish(ctx, "redis-stream-manager", record.ID, record.ID, time.Since(start), 1, 0, reason)
		}
		if ctx.Err() == nil && len(records) < sourceStreamPageSize {
			after = ""
		}
	}
	registry.Finish(ctx, "redis-stream-manager", "", "", time.Since(started), processed, failed, code)
	return after
}
