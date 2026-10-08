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

	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	controlapi "linkd/internal/controlplane/api"
	streams "linkd/internal/controlplane/redisstream"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/eventsource"
	sourcestore "linkd/internal/eventsource/storage"
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
	"linkd/internal/projection"
	projectionlock "linkd/internal/projection/redislock"
	projectionstore "linkd/internal/projection/storage"
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
	if err := validateDispatchConfig(cfg); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	docs, e := sourcestore.Open(startup, *cfg.Storage, d.Deployment)
	if e != nil {
		return e
	}
	defer func() { _ = docs.Close() }()
	policyDocs, err := policystore.Open(startup, *cfg.Storage, d.Deployment)
	if err != nil {
		return err
	}
	defer func() { _ = policyDocs.Close() }()
	projectionTasks, err := projectionstore.Open(startup, *cfg.Storage, d.Deployment)
	if err != nil {
		return err
	}
	defer func() { _ = projectionTasks.Close() }()
	actionTasks, err := actionstore.Open(startup, *cfg.Storage, d.Deployment)
	if err != nil {
		return err
	}
	defer func() { _ = actionTasks.Close() }()
	shieldJournal, err := shieldcheck.NewJournal(policyDocs)
	if err != nil {
		return err
	}
	policies := policy.NewService(policyDocs)
	severity := runtimeconfig.NewSeverity(cfg.Severity)
	sources := eventsource.New(docs, cfg.Severity, eventsource.Options{Cleaner: cfg.Cleaner, Resources: cfg.Resources})
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
	controller, e := taskdispatch.NewController(startup, client, sources, d.Deployment, dispatchTaskObserver{metrics.DispatchObserver(), registry, metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskScheduler)})
	if e != nil {
		return e
	}
	// 先完成调度历史检查/首次初始化，再发布内置来源；否则新部署也会被误判为协调历史丢失。
	// 已有来源而历史确实丢失时仍由 NewController 拒绝，不能借自动来源初始化绕过停机恢复边界。
	if cfg.Lifecycle != nil {
		if _, err := sources.EnsureMergeSource(startup, "linkd"); err != nil {
			return fmt.Errorf("initialize builtin merge source: %w", err)
		}
	}
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
		defer transport.Close()
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
	api := &controlapi.API{Policies: policies, Tasks: registry, OneModel: oneModelQueries, Previewer: newEnrichPreview(sources, *cfg.Storage, cfg.Resources), DynamicConfig: dynamic, Lifecycle: lifecycle, Sources: sources, Controller: controller, Config: d}
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
	projectionLocker, err := projectionlock.New(queryRedis, d.Deployment)
	if err != nil {
		return err
	}
	projectionRetries, err := projection.NewRetrier(projectionTasks, projectionLocker, time.Now)
	if err != nil {
		return err
	}
	api.ProjectionTasks = controlapi.NewProjectionTasks(projectionTasks, projectionRetries)
	actionRetries, err := actiondelivery.NewRetrier(actionTasks, projectionLocker, time.Now)
	if err != nil {
		return err
	}
	api.ActionDeliveries = controlapi.NewActionDeliveries(actionTasks, actionRetries)
	queryRepository, err := storeassembly.OpenReadOnly(ctx, *cfg.Storage, 8)
	if err != nil {
		return err
	}
	defer func() { _ = queryRepository.Close() }()
	shieldReader, ok := metrics.ObserveRepository(queryRepository.Repository).(controlapi.ShieldRuntimeStore)
	if !ok {
		return fmt.Errorf("repository does not support shield runtime queries")
	}
	api.ShieldRuntime = controlapi.NewShieldRuntime(shieldReader)
	api.AlertCloser = lifecycleprocess.NewAlertCloser(cfg, sources, severity, logger, metrics)
	shieldObservations := policyruntime.NewObservations(queryWindows, metrics)
	defer shieldObservations.Close()
	shieldChecks := lifecycleprocess.NewShieldChecker(cfg, sources, policies, policyResources, severity, logger, metrics, shieldJournal, shieldObservations)
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
	server := &http.Server{Addr: d.Listen, Handler: api.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	// 上游读取可能耗尽最初的装配期限；已回退快照/YAML 后不应因此阻止管理 API 启动。
	listenCtx, cancelListen := context.WithTimeout(ctx, 3*time.Second)
	listener, e := (&net.ListenConfig{}).Listen(listenCtx, "tcp", d.Listen)
	cancelListen()
	if e != nil {
		return e
	}
	defer func() { _ = listener.Close() }()
	tasks := []taskgroup.Task{{Name: "scheduler", Run: controller.Run}, {Name: "source-api", Run: func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			return server.Shutdown(shutdown)
		}
	}}}
	tasks = append(tasks, taskgroup.Task{Name: "policy-publication", Run: func(ctx context.Context) error {
		return runPolicyPublication(ctx, policies, logger, registry, metrics)
	}})
	if hasKACDelivery(cfg) {
		tasks = append(tasks, taskgroup.Task{Name: "kac-delivery", Run: func(ctx context.Context) error {
			return runKACDelivery(ctx, cfg, mergeJournal, projectionTasks, actionTasks, severity, logger, registry, metrics)
		}})
	}
	if cfg.Lifecycle != nil {
		tasks = append(tasks, taskgroup.Task{Name: "merge", Run: func(ctx context.Context) error {
			return runMergeTasks(ctx, cfg, sources, policies, mergeJournal, mergeRetries, policyResources, severity, logger, registry, metrics)
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
		after := ""
		tasks = append(tasks, newSourceStreamTask(metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskRedisStreamManager), func(ctx context.Context) error {
			ticker := time.NewTicker(settings.ReconcileInterval())
			defer ticker.Stop()
			for {
				after = reconcileSourceStreams(ctx, settings.OperationTimeout(), sources, after, registry, func(call context.Context, r eventsource.Record) error {
					lc := cfg.Lifecycle.ForSource(d.Deployment, r.ID)
					manager, err := streams.NewManager(client, streams.Config{Stream: lc.Signal.Stream, ExpectedGroup: lc.Signal.Group, ReconcileInterval: settings.ReconcileInterval(), OperationTimeout: settings.OperationTimeout(), MaxEntries: settings.MaxEntries, TrimBatchSize: settings.TrimBatchSize, MaxTrimEntriesPerCycle: settings.MaxTrimEntriesPerCycle}, metrics.RedisStreamObserver())
					if err == nil {
						err = manager.ReconcileOnce(call)
					}
					if err != nil {
						logger.WarnContext(ctx, "source stream reconcile failed", "event_source_id", r.ID)
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
			return sources.RunProvider(ctx, provider, 30*time.Second, func(error) { logger.WarnContext(ctx, "event source provider reconciliation failed") }, providerTaskObserver{registry, metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskProviders), i})
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
		task := registry.Wrap(id, tasks[i])
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

// reconcileSourceStreams 把来源读取失败和逐来源失败纳入同一轮结果。
// 详情仅属于最近一页，最多 16 项；不把一次分页扫描解释为全部来源健康。
func reconcileSourceStreams(ctx context.Context, timeout time.Duration, sources interface {
	List(context.Context, string, int) ([]eventsource.Record, error)
}, after string, registry *taskstate.Registry, reconcile func(context.Context, eventsource.Record) error) string {
	started := time.Now()
	registry.Begin("redis-stream-manager", "", "")
	registry.RetainSteps("redis-stream-manager", nil)
	call, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	records, err := sources.List(call, after, 16)
	code := ""
	failed := 0
	if err != nil {
		code = "source_list_failed"
	} else {
		for _, record := range records {
			after = record.ID
			start := time.Now()
			err := reconcile(call, record)
			reason := ""
			if err != nil {
				reason = "stream_reconcile_failed"
				failed++
			}
			registry.Finish(ctx, "redis-stream-manager", record.ID, record.ID, time.Since(start), 1, 0, reason)
		}
		if len(records) < 16 {
			after = ""
		}
	}
	registry.Finish(ctx, "redis-stream-manager", "", "", time.Since(started), len(records), failed, code)
	return after
}
