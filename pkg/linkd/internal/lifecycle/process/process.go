// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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
	"os"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/consume/redisstream"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kafkahook"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	repositoryassembly "linkd/internal/store/assembly"
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
	consumerName := newConsumerName(lifecycleConfig.Signal.ConsumerPrefix, hostname(), os.Getpid())

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	repositoryRuntime, err := repositoryassembly.Open(startupCtx, *storageConfig, lifecycleConfig.Concurrency+4)
	if err != nil {
		return fmt.Errorf("initialize lifecycle repository: %w", err)
	}
	defer repositoryassembly.JoinCloseError(&runErr, repositoryRuntime)
	observedRepository := telemetryRuntime.ObserveRepository(repositoryRuntime.Repository)

	lockClient := redis.NewClient(&redis.Options{
		Addr:     storageConfig.Redis.Address,
		Username: storageConfig.Redis.Username,
		Password: storageConfig.Redis.Password,
		DB:       storageConfig.Redis.Database,
	})
	defer func() {
		if err := lockClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close lifecycle redis: %w", err))
		}
	}()
	if err := lockClient.Ping(startupCtx).Err(); err != nil {
		return fmt.Errorf("connect lifecycle redis: %w", err)
	}
	mailboxStore, err := mailbox.NewStore(lockClient, lifecycleConfig.MailboxConfig())
	if err != nil {
		return fmt.Errorf("initialize lifecycle mailbox: %w", err)
	}

	session, err := redisstream.NewSession(
		lifecycleConfig.RedisStreamConfig(*storageConfig.Redis, consumerName),
	)
	if err != nil {
		return fmt.Errorf("initialize lifecycle signal session: %w", err)
	}
	// 从这里开始 Session 的所有权交给 consume.Runtime，Run 会在所有返回路径关闭它。

	hook, err := kafkahook.New(lifecycleConfig.KafkaHookConfig())
	if err != nil {
		closeSession(session)
		return fmt.Errorf("initialize lifecycle kafka hook: %w", err)
	}
	defer hook.Close()
	observedHook := telemetryRuntime.ObserveFinalHook(hook)

	processor, err := lifecycle.NewProcessor(
		observedRepository,
		lifecycle.DeterministicAlertIDGenerator{},
		lifecycle.NoopEnricher{},
		observedHook,
		cfg.Severity,
		lifecycle.SystemClock{},
		logger,
	)
	if err != nil {
		closeSession(session)
		return fmt.Errorf("initialize lifecycle processor: %w", err)
	}
	locker, err := scheduler.NewRedisLocker(lockClient, lifecycleConfig.SchedulerConfig())
	if err != nil {
		closeSession(session)
		return fmt.Errorf("initialize lifecycle fingerprint locker: %w", err)
	}
	observedProcessor := telemetryRuntime.ObserveLifecycleProcessor(processor)
	handler, err := scheduler.NewHandler(
		observedRepository,
		mailboxStore,
		observedProcessor,
		locker,
		lifecycleConfig.SchedulerConfig(),
		logger,
		telemetryRuntime.LifecycleSchedulerObserver(),
	)
	if err != nil {
		closeSession(session)
		return fmt.Errorf("initialize lifecycle scheduler: %w", err)
	}
	logger.InfoContext(
		ctx,
		"linkd lifecycle started",
		"consumer", consumerName,
		"stream", lifecycleConfig.Signal.Stream,
		"group", lifecycleConfig.Signal.Group,
		"concurrency", lifecycleConfig.Concurrency,
		"mailbox_max_drain_events", lifecycleConfig.Mailbox.MaxDrainEvents,
		"output_topic", lifecycleConfig.Output.Kafka.Topic,
	)
	defer logger.InfoContext(context.Background(), "linkd lifecycle stopped", "consumer", consumerName)

	labels := consume.RuntimeLabels{Stage: "lifecycle", Transport: "redis_streams"}
	runtime := consume.New(
		lifecycleConfig.RuntimeConfig(),
		session,
		handler,
		consume.WithObserver(labels, telemetryRuntime.ConsumeObserver(labels)),
	)
	return runtime.Run(ctx)
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

func hostname() string {
	value, err := os.Hostname()
	if err != nil || value == "" {
		return "unknown-host"
	}
	return value
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
