// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	controlplaneprocess "linkd/internal/controlplane/process"
	"linkd/internal/logging"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

func newAllInOneCommand(options *commandOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "all-in-one",
		Aliases: []string{"all"},
		Short:   "在一个进程中启动全部 Linkd 服务",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd, options)
			if err != nil {
				return err
			}
			logger, err := logging.New(cfg.Logging, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("initialize logging: %w", err)
			}

			// 所有服务先完成无副作用校验，再启动任何外部连接，避免已知配置错误造成半启动。
			if options.cleanerValidate != nil {
				if err := options.cleanerValidate(cfg); err != nil {
					return err
				}
			}
			if options.lifecycleValidate != nil {
				if err := options.lifecycleValidate(cfg); err != nil {
					return fmt.Errorf("initialize lifecycle: %w", err)
				}
			}
			if err := controlplaneprocess.PrepareDataPlane(cmd.Context(), cfg); err != nil {
				return err
			}

			return runWithTelemetry(
				cmd.Context(),
				cfg,
				telemetry.RoleAllInOne,
				options.version,
				func(ctx context.Context, telemetryRuntime *telemetry.Runtime) error {
					tasks := []taskgroup.Task{
						{
							Name: "cleaner",
							Run: func(ctx context.Context) error {
								return runCleaner(
									ctx,
									cfg,
									options,
									options.configPath,
									logger,
									telemetryRuntime,
								)
							},
						},
						{
							Name: "lifecycle",
							Run: func(ctx context.Context) error {
								return options.lifecycleRunner(ctx, cfg, logger, telemetryRuntime)
							},
						},
					}
					if controlplaneprocess.HasManagementTasks(cfg) {
						tasks = append(tasks, taskgroup.Task{
							Name: "control-plane",
							Run: func(ctx context.Context) error {
								return options.controlPlaneRunner(ctx, cfg, logger, telemetryRuntime)
							},
						})
					}

					logger.InfoContext(ctx, "linkd all-in-one started", "tasks", len(tasks))
					defer logger.InfoContext(context.Background(), "linkd all-in-one stopped")
					if err := taskgroup.Run(ctx, tasks); err != nil {
						return fmt.Errorf("run all-in-one: %w", err)
					}
					return nil
				},
			)
		},
	}
}
