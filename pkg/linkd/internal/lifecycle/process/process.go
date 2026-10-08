// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/consume/redisstream"
	"linkd/internal/enrich/assembly"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	policystore "linkd/internal/policy/storage"
	"linkd/internal/redisclient"
	"linkd/internal/store"
	repositoryassembly "linkd/internal/store/assembly"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/suppressioncleanup"
	"linkd/internal/taskdispatch"
	"linkd/internal/telemetry"
)

const (
	startupTimeout    = 10 * time.Second
	consumerNameLimit = 256
)

// Run 校验 lifecycle 专用依赖并持续消费 Redis signal，直到 ctx 取消或运行时失败。
// Redis Stream delivery 只会在 Processor 返回成功后 XACK；锁竞争在单次 Handler 内有界等待，
// 其他失败由消费运行时在有界预算内重试，超出预算时 Runtime 失败退出并关闭 Session；消息保留在
// PEL 供 XAUTOCLAIM 接管。Mailbox 引用由上游消息在 ACK 前可靠提交，不再扫描 Event 补投 signal。
func Run(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("run lifecycle process: context must not be nil")
	}
	if logger == nil {
		return fmt.Errorf("run lifecycle process: logger must not be nil")
	}
	if telemetryRuntime == nil {
		return fmt.Errorf("run lifecycle process: telemetry runtime must not be nil")
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	lifecycleConfig := cfg.Lifecycle.WithDefaults()
	storageConfig := cfg.Storage

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	repositoryRuntime, err := repositoryassembly.Open(startupCtx, *storageConfig, min(1024, lifecycleConfig.Concurrency*cfg.Dispatch.WithDefaults().MaxTasks+4))
	if err != nil {
		return fmt.Errorf("initialize lifecycle repository: %w", err)
	}
	defer repositoryassembly.JoinCloseError(&runErr, repositoryRuntime)
	batchConfig := lifecycleConfig.ElasticsearchWriteBatch
	if repositoryRuntime.Backend == config.RepositoryTypeElasticsearch && *batchConfig.Enabled {
		repository, ok := repositoryRuntime.Repository.(*elasticsearchstore.Repository)
		if !ok {
			return fmt.Errorf("lifecycle elasticsearch batch requires elasticsearch repository")
		}
		observer, err := telemetryRuntime.NewWriteBatchObserver()
		if err != nil {
			return err
		}
		batched, closer, err := repository.EnableWriteBatch(elasticsearchstore.WriteBatchConfig{
			MaxOperations: batchConfig.MaxOperations, MaxBytes: batchConfig.MaxBytes,
			Wait:                 time.Duration(batchConfig.WaitMilliseconds) * time.Millisecond,
			ReadWait:             time.Duration(batchConfig.ReadWaitMilliseconds) * time.Millisecond,
			MaxConcurrentBatches: batchConfig.MaxConcurrentBatches, MaxCalls: lifecycleConfig.Concurrency,
			Timeout: time.Duration(lifecycleConfig.ProcessTimeoutSeconds) * time.Second,
		}, observer)
		if err != nil {
			return err
		}
		defer closer.Close()
		repositoryRuntime.Repository = batched
	}
	observedRepository := telemetryRuntime.ObserveRepository(repositoryRuntime.Repository)
	cleanupDocs, err := policystore.Open(startupCtx, *storageConfig, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, cleanupDocs.Close()) }()
	cleanupJournal, err := suppressioncleanup.NewJournal(cleanupDocs, time.Now)
	if err != nil {
		return err
	}

	redisOptions := storageConfig.Redis.ClientOptions()
	redisOptions.ContextTimeoutEnabled = true
	lockClient, err := redisclient.New(redisOptions)
	if err != nil {
		return fmt.Errorf("initialize lifecycle redis: %w", err)
	}
	defer func() {
		if err := lockClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close lifecycle redis: %w", err))
		}
	}()
	if err := lockClient.Ping(startupCtx).Err(); err != nil {
		return fmt.Errorf("connect lifecycle redis: %w", err)
	}
	actionRecorder, closeActionTasks, err := openActionRecorder(startupCtx, *storageConfig, cfg.Dispatch.WithDefaults().Deployment, lockClient)
	if err != nil {
		return fmt.Errorf("initialize lifecycle action recorder: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, closeActionTasks()) }()
	recentAlerts := lifecycle.RecentAlertCache(lifecycle.NoopRecentAlertCache{})
	recentAlertCacheEnabled := false
	recentAlertCacheTTL := time.Duration(0)
	if repositoryRuntime.Backend == config.RepositoryTypeElasticsearch {
		cacheConfig := recentalert.Config{
			KeyPrefix:       lifecycleConfig.Mailbox.KeyPrefix + ":recent-alert",
			RefreshInterval: storageConfig.Elasticsearch.ActiveAlertRefreshInterval(),
		}
		recentAlerts, err = recentalert.NewStore(lockClient, cacheConfig, telemetryRuntime.RecentAlertCacheObserver())
		if err != nil {
			return fmt.Errorf("initialize lifecycle recent alert cache: %w", err)
		}
		recentAlertCacheEnabled = true
		recentAlertCacheTTL = cacheConfig.TTL()
	}

	policyState, err := redisstate.New(lockClient, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		return err
	}
	observations := policyruntime.NewObservations(policyState, telemetryRuntime)
	defer observations.Close()
	policyState.SetObserver(telemetryRuntime)
	policyResources, err := policyruntime.Open(cfg.Resources, cfg.Blueking)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, policyResources.Close()) }()

	severityState := taskdispatch.SeverityState(ctx, cfg.Severity)
	return taskdispatch.ServeWithSeverity(ctx, cfg, "lifecycle", func(taskCtx context.Context, task taskdispatch.Task, source config.EventSource) (taskErr error) {
		stage := "enrich_datasources"
		defer func() { taskErr = taskdispatch.WithTaskStage(stage, taskErr) }()
		openCtx, cancelOpen := context.WithTimeout(taskCtx, startupTimeout)
		enrichRuntime, err := assembly.Open(
			openCtx, source, cfg.Resources, lifecycleConfig.Concurrency+4,
			time.Duration(lifecycleConfig.ProcessTimeoutSeconds)*time.Second, telemetryRuntime,
		)
		cancelOpen()
		if err != nil {
			return fmt.Errorf("initialize lifecycle source enrich datasources: %w", err)
		}
		defer func() {
			if closeErr := enrichRuntime.Close(); closeErr != nil {
				taskErr = errors.Join(taskErr, closeErr)
			}
		}()
		stage = "enricher"
		enricher, err := enrichRuntime.Router(source, telemetryRuntime)
		if err != nil {
			return fmt.Errorf("initialize lifecycle source enricher: %w", err)
		}
		source.Version = task.Version
		versionedEnricher := &releaseEnricher{current: source, engine: enricher, slots: make(chan struct{}, min(4, lifecycleConfig.Concurrency)),
			read: func(ctx context.Context, id string, version int64) (config.EventSource, error) {
				client := taskdispatch.Client{URL: cfg.Dispatch.WithDefaults().URL, Token: cfg.Dispatch.WorkerToken, WorkerID: task.Worker}
				var release eventsource.Release
				err := client.Call(ctx, http.MethodGet, "/internal/releases/"+url.PathEscape(id)+"/"+strconv.FormatInt(version, 10)+"?purpose=enrich&task="+url.QueryEscape(task.ID), nil, &release)
				if err != nil {
					return config.EventSource{}, err
				}
				if release.Deleted {
					return config.EventSource{}, fmt.Errorf("enrich release is deleted")
				}
				release.Spec.Version = release.Version
				return release.Spec, nil
			},
			open: func(ctx context.Context, spec config.EventSource) (lifecycle.EventEnricher, func() error, error) {
				runtime, err := assembly.Open(ctx, spec, cfg.Resources, 1, time.Duration(lifecycleConfig.ProcessTimeoutSeconds)*time.Second, telemetryRuntime)
				if err != nil {
					return nil, nil, err
				}
				router, err := runtime.Router(spec, telemetryRuntime)
				if err != nil {
					_ = runtime.Close()
					return nil, nil, err
				}
				return router, runtime.Close, nil
			},
		}
		// 全局插件覆盖所有来源及租户，来源发布不再提供出口配置。
		targets := map[string]bool{}
		if cfg.Plugins.KACEnabled() {
			targets["kac"] = true
		}
		stage = "hooks"
		hooks, closeHooks, err := openHooksWithSeverity(source.Hooks, telemetryRuntime, severityState)
		if err != nil {
			return fmt.Errorf("initialize source hooks: %w", err)
		}
		defer func() {
			if err := closeHooks(); err != nil {
				logger.WarnContext(taskCtx, "source hook cleanup failed", "event_source_id", source.EventSourceID)
			}
		}()
		stage = "processor"
		policyReader := policyruntime.WorkerReader{Client: taskdispatch.Client{URL: cfg.Dispatch.WithDefaults().URL, Token: cfg.Dispatch.WorkerToken, WorkerID: task.Worker}, TaskID: task.ID}
		policyCatalog := policy.NewCatalog(policyReader)
		suppressor := &policyruntime.Suppressor{Releases: policyReader, Catalog: policyCatalog, Targets: policyResources.Targets, State: policyState, Aggregation: policyState,
			CleanupRecorder: cleanupJournal,
			CurrentAlert: func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				if current, ok := observedRepository.(store.LifecycleAlertStore); ok {
					return current.GetAlertCurrent(ctx, tenant, id)
				}
				return observedRepository.GetAlert(ctx, tenant, id)
			},
			NewAlertID: lifecycle.DeterministicAlertIDGenerator{}.Generate, Observations: observations, Observer: telemetryRuntime, Logger: logger}
		processor, err := lifecycle.NewProcessor(
			observedRepository,
			recentAlerts,
			lifecycle.DeterministicAlertIDGenerator{},
			versionedEnricher,
			hooks,
			severityState,
			lifecycle.SystemClock{},
			logger,
			lifecycle.WithEnrichObserver(telemetryRuntime.EnrichObserver()),
			lifecycle.WithActionRecorder(actionRecorder),
			lifecycle.WithInitialProjectionTargets(targets),
			lifecycle.WithPolicySnapshotter(policyruntime.Snapshotter{Catalog: policyCatalog}),
			lifecycle.WithNewAlertSuppressor(suppressor),
			lifecycle.WithMergeEvaluator(&policyruntime.Merger{Loader: suppressor, State: policyState, Logger: logger, Observer: telemetryRuntime}),
			lifecycle.WithShieldEvaluator(&policyruntime.Shielder{Loader: suppressor, Events: observedRepository.GetEvent, Candidates: observedRepository.(store.ActiveAlertReader), CurrentAlert: suppressor.CurrentAlert, Dependency: policyState, Hints: policyState, Relations: policyResources.Relations, Logger: logger, Observer: telemetryRuntime}),
			lifecycle.WithSeverityUpgradePolicy(lifecycleConfig.SeverityUpgradePolicy),
			lifecycle.WithAlertContentBuilder(enricher),
		)
		if err != nil {
			return fmt.Errorf("initialize lifecycle processor: %w", err)
		}

		stage = "mailbox"
		lc := lifecycleConfig.ForSource(cfg.Dispatch.WithDefaults().Deployment, source.EventSourceID)
		mailboxStore, err := mailbox.NewStore(lockClient, lc.MailboxConfig())
		if err != nil {
			return err
		}
		sc := lc.RedisStreamConfig(*storageConfig.Redis, taskdispatch.ConsumerName(task))
		sc.RetiredConsumers = append([]string(nil), task.Retired...)
		stage = "signal_session"
		session, err := redisstream.NewSession(sc)
		if err != nil {
			return err
		}
		stage = "lease"
		locker, err := scheduler.NewRedisLocker(lockClient, lc.SchedulerConfig())
		if err != nil {
			closeSession(session)
			return err
		}
		stage = "scheduler"
		handler, err := scheduler.NewHandler(observedRepository, mailboxStore, telemetryRuntime.ObserveLifecycleProcessor(processor), locker, lc.SchedulerConfig(), logger, telemetryRuntime.LifecycleSchedulerObserver())
		if err != nil {
			closeSession(session)
			return err
		}
		handler.BindSource(source.EventSourceID)
		labels := consume.RuntimeLabels{Stage: "lifecycle", Transport: "redis_streams", EventSourceID: source.EventSourceID}
		rc := lc.RuntimeConfig()
		rc.ShutdownDrainTimeout = taskdispatch.DrainTimeout
		logger.InfoContext(taskCtx, "lifecycle source started", "event_source_id", source.EventSourceID, "stream", lc.Signal.Stream, "consumer", sc.Consumer, "recent_alert_cache_enabled", recentAlertCacheEnabled, "recent_alert_cache_ttl_seconds", recentAlertCacheTTL.Seconds())
		stage = "consume"
		err = consume.New(rc, session, handler, consume.WithObserver(labels, telemetryRuntime.ConsumeObserver(labels))).Run(taskCtx)
		var contentFailure interface{ PermanentContentFailure() string }
		if errors.As(err, &contentFailure) {
			return taskdispatch.RequireTaskRepair(err)
		}
		return err
	}, logger, severityState, telemetryRuntime.DispatchObserver())

}

// ValidateConfig 校验 lifecycle 命令实际需要的 MySQL、Redis 和输出配置。
func ValidateConfig(cfg config.Config) error {
	if cfg.Lifecycle == nil {
		return fmt.Errorf("run lifecycle process: lifecycle config is required")
	}
	if err := cfg.Lifecycle.Validate(); err != nil {
		return fmt.Errorf("run lifecycle process: %w", err)
	}
	if cfg.Storage == nil {
		return fmt.Errorf("run lifecycle process: storage config is required")
	}
	if cfg.Storage.Repository == "" {
		return fmt.Errorf("run lifecycle process: storage.repository is required")
	}
	if cfg.Storage.Redis == nil {
		return fmt.Errorf("run lifecycle process: storage.redis is required")
	}
	return nil
}

func newConsumerName(prefix, host string, processID int) string {
	name := fmt.Sprintf("%s-%s-%d", sanitizeNamePart(prefix), sanitizeNamePart(host), processID)
	if len(name) <= consumerNameLimit {
		return name
	}
	return strings.TrimRight(name[:consumerNameLimit], "-")
}

func sanitizeNamePart(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	lastSeparator := false
	for _, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-'
		if valid {
			builder.WriteRune(character)
			lastSeparator = false
			continue
		}
		if !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "unknown"
	}
	return result
}

func closeSession(session *redisstream.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = session.Close(ctx)
}
