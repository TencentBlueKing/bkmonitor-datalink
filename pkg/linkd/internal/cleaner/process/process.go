// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/redisclient"
	repositoryassembly "linkd/internal/store/assembly"
	"linkd/internal/taskdispatch"
	"linkd/internal/telemetry"
)

const (
	startupTimeout = 10 * time.Second
)

// Run 装配并运行全部 enabled EventSource 的默认 Kafka cleaner。
func Run(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("run cleaner process: context must not be nil")
	}
	if logger == nil {
		return fmt.Errorf("run cleaner process: logger must not be nil")
	}
	if telemetryRuntime == nil {
		return fmt.Errorf("run cleaner process: telemetry runtime must not be nil")
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	repositoryRuntime, err := repositoryassembly.Open(startupCtx, *cfg.Storage, repositoryConnectionBudget(cfg))
	if err != nil {
		return fmt.Errorf("initialize cleaner repository: %w", err)
	}
	defer repositoryassembly.JoinCloseError(&runErr, repositoryRuntime)
	observedRepository := telemetryRuntime.ObserveRepository(repositoryRuntime.Repository)

	redisConfig := cfg.Storage.Redis
	redisClient, err := redisclient.New(redisConfig.ClientOptions())
	if err != nil {
		return fmt.Errorf("initialize cleaner redis: %w", err)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close cleaner redis: %w", err))
		}
	}()
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		return fmt.Errorf("connect cleaner redis: %w", err)
	}

	lifecycleConfig := cfg.Lifecycle.WithDefaults()
	return taskdispatch.Serve(ctx, cfg, "cleaner", func(taskCtx context.Context, task taskdispatch.Task, source config.EventSource) error {
		source.RuntimeClientID = taskdispatch.ConsumerName(task)
		lc := lifecycleConfig.ForSource(cfg.Dispatch.WithDefaults().Deployment, source.EventSourceID)
		if err := redisClient.XGroupCreateMkStream(taskCtx, lc.Signal.Stream, lc.Signal.Group, "0").Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return err
		}
		publisher, err := cleaner.NewRedisMailboxPublisher(redisClient, lc.MailboxConfig())
		if err != nil {
			return err
		}
		backpressure := lc.Mailbox.Backpressure
		receiveGate, err := cleaner.NewSignalBackpressureChecker(redisClient, cleaner.BackpressureConfig{Stream: lc.Signal.Stream, Group: lc.Signal.Group, CacheTTL: time.Duration(backpressure.CacheTTLSeconds) * time.Second, QueryTimeout: time.Duration(backpressure.QueryTimeoutSeconds) * time.Second, HighWatermark: backpressure.HighWatermark, LowWatermark: backpressure.LowWatermark}, telemetryBackpressureObserver{runtime: telemetryRuntime})
		if err != nil {
			return err
		}
		runtimeConfig := source.Cleaner.RuntimeConfig(cfg.Cleaner)
		runtimeConfig.ShutdownDrainTimeoutSeconds = int(taskdispatch.DrainTimeout / time.Second)
		source.Cleaner.Runtime = &runtimeConfig
		factory, err := cleaner.NewFactory(observedRepository, publisher, receiveGate, logger, cfg.Cleaner, cfg.Severity, func(s config.EventSource) consume.Observer {
			return telemetryRuntime.ConsumeObserver(consume.RuntimeLabels{Stage: "clean", Transport: "kafka", EventSourceID: s.EventSourceID, RecordPipelineAttempts: true})
		})
		if err != nil {
			return err
		}
		flow, err := factory.NewFlow(taskCtx, source)
		if err != nil {
			return err
		}
		return flow.Run(taskCtx)
	}, telemetryRuntime.DispatchObserver())
}

func repositoryConnectionBudget(cfg config.Config) int {
	return min(1024, cfg.Dispatch.WithDefaults().MaxTasks*64+2)
}

// ValidateConfig 校验默认 cleaner 运行所需的共享存储和 lifecycle signal 配置。
func ValidateConfig(cfg config.Config) error {

	if cfg.Storage == nil {
		return fmt.Errorf("run cleaner process: storage config is required for enabled event sources")
	}
	if cfg.Storage.Repository == "" {
		return fmt.Errorf("run cleaner process: storage.repository is required for enabled event sources")
	}
	if cfg.Storage.Redis == nil {
		return fmt.Errorf("run cleaner process: storage.redis is required for enabled event sources")
	}
	if cfg.Lifecycle == nil {
		return fmt.Errorf("run cleaner process: lifecycle config is required for lifecycle signal output")
	}
	if cfg.Lifecycle.WithDefaults().Signal.Stream == "" {
		return fmt.Errorf("run cleaner process: lifecycle.signal.stream is required")
	}
	return nil
}

type telemetryBackpressureObserver struct{ runtime *telemetry.Runtime }

func (o telemetryBackpressureObserver) BackpressureChecked(
	ctx context.Context,
	observation cleaner.BackpressureObservation,
) {
	o.runtime.ObserveCleanerBackpressureCheck(ctx, observation.Outcome, observation.Unresolved, observation.Paused)
}

func (o telemetryBackpressureObserver) BackpressureTransition(ctx context.Context, action string) {
	o.runtime.ObserveCleanerBackpressureTransition(ctx, action)
}

var _ cleaner.BackpressureObserver = telemetryBackpressureObserver{}
