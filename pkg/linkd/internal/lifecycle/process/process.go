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
	"strings"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/consume/redisstream"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/redisclient"
	repositoryassembly "linkd/internal/store/assembly"
	elasticsearchstore "linkd/internal/store/elasticsearch"
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

	lockClient, err := redisclient.New(storageConfig.Redis.ClientOptions())
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

	return taskdispatch.Serve(ctx, cfg, "lifecycle", func(taskCtx context.Context, task taskdispatch.Task, source config.EventSource) (taskErr error) {
		openCtx, cancelOpen := context.WithTimeout(taskCtx, startupTimeout)
		enrichRuntime, err := openEnrichRuntime(
			openCtx, source, lifecycleConfig.Concurrency+4,
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
		enricher, err := enrichRuntime.router(source, telemetryRuntime)
		if err != nil {
			return fmt.Errorf("initialize lifecycle source enricher: %w", err)
		}
		hooks, closeHooks, err := openHooks(source.Hooks, telemetryRuntime)
		if err != nil {
			return fmt.Errorf("initialize source hooks: %w", err)
		}
		defer func() {
			if err := closeHooks(); err != nil {
				logger.WarnContext(taskCtx, "source hook cleanup failed", "event_source_id", source.EventSourceID)
			}
		}()
		processor, err := lifecycle.NewProcessor(
			observedRepository,
			recentAlerts,
			lifecycle.DeterministicAlertIDGenerator{},
			enricher,
			hooks,
			cfg.Severity,
			lifecycle.SystemClock{},
			logger,
			lifecycle.WithEnrichObserver(telemetryRuntime.EnrichObserver()),
		)
		if err != nil {
			return fmt.Errorf("initialize lifecycle processor: %w", err)
		}

		lc := lifecycleConfig.ForSource(cfg.Dispatch.WithDefaults().Deployment, source.EventSourceID)
		mailboxStore, err := mailbox.NewStore(lockClient, lc.MailboxConfig())
		if err != nil {
			return err
		}
		sc := lc.RedisStreamConfig(*storageConfig.Redis, taskdispatch.ConsumerName(task))
		sc.RetiredConsumers = append([]string(nil), task.Retired...)
		session, err := redisstream.NewSession(sc)
		if err != nil {
			return err
		}
		locker, err := scheduler.NewRedisLocker(lockClient, lc.SchedulerConfig())
		if err != nil {
			closeSession(session)
			return err
		}
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
		return consume.New(rc, session, handler, consume.WithObserver(labels, telemetryRuntime.ConsumeObserver(labels))).Run(taskCtx)
	}, telemetryRuntime.DispatchObserver())

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
