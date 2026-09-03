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
	"linkd/internal/logging"
	"linkd/internal/telemetry"
)

type processCommand struct {
	use      string
	short    string
	role     telemetry.Role
	validate ProcessValidator
	run      ProcessRunner
}

// newProcessCommand 统一装配拆分部署的常驻进程入口。配置和职责校验必须先完成，随后每个进程
// 独立启动 telemetry runtime，最后才允许职责实现连接外部系统或接管消息。
func newProcessCommand(options *commandOptions, process processCommand) *cobra.Command {
	return &cobra.Command{
		Use:   process.use,
		Short: process.short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if process.use == "" || process.role == "" || process.run == nil {
				return fmt.Errorf("initialize process command: use, role and runner are required")
			}
			cfg, err := loadConfig(cmd, options)
			if err != nil {
				return err
			}
			if process.validate != nil {
				if err := process.validate(cfg); err != nil {
					return err
				}
			}
			logger, err := logging.New(cfg.Logging, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("initialize logging: %w", err)
			}
			return runWithTelemetry(
				cmd.Context(), cfg, process.role, options.version,
				func(ctx context.Context, telemetryRuntime *telemetry.Runtime) error {
					return process.run(ctx, cfg, logger, telemetryRuntime)
				},
			)
		},
	}
}
