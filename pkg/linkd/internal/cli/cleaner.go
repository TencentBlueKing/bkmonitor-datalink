// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func newCleanerCommand(options *commandOptions) *cobra.Command {
	return newProcessCommand(options, processCommand{
		use:      "cleaner",
		short:    "启动告警清洗进程",
		role:     telemetry.RoleCleaner,
		validate: options.cleanerValidate,
		run: func(
			ctx context.Context,
			cfg config.Config,
			logger *slog.Logger,
			telemetryRuntime *telemetry.Runtime,
		) error {
			return runCleaner(ctx, cfg, options, options.configPath, logger, telemetryRuntime)
		},
	})
}

func newCleanerScheduler(
	sources []config.EventSource,
	severity config.SeverityConfig,
	factory cleaner.FlowFactory,
) (*cleaner.Scheduler, error) {
	scheduler, err := cleaner.NewScheduler(sources, severity, factory)
	if err != nil {
		return nil, fmt.Errorf("initialize cleaner: %w", err)
	}
	return scheduler, nil
}

func runCleaner(
	ctx context.Context,
	cfg config.Config,
	options *commandOptions,
	configPath string,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) error {
	logger.InfoContext(
		ctx,
		"linkd cleaner started",
		"config", configPath,
		"enabled_event_sources", enabledEventSourceCount(cfg.EventSources),
	)
	defer logger.InfoContext(context.Background(), "linkd cleaner stopped")
	if options.cleanerFactory != nil {
		scheduler, err := newCleanerScheduler(cfg.EventSources, cfg.Severity, options.cleanerFactory)
		if err != nil {
			return err
		}
		if err := scheduler.Run(ctx); err != nil {
			return fmt.Errorf("run cleaner: %w", err)
		}
		return nil
	}
	if err := options.cleanerRunner(ctx, cfg, logger, telemetryRuntime); err != nil {
		return fmt.Errorf("run cleaner: %w", err)
	}
	return nil
}

func enabledEventSourceCount(sources []config.EventSource) int {
	count := 0
	for _, source := range sources {
		if source.Enabled {
			count++
		}
	}
	return count
}
