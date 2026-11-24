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
	"time"

	"github.com/spf13/cobra"
)

func newStorageMigrateCommand(options *commandOptions) *cobra.Command {
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "migrate",
		Short: "检查控制面配置并执行一次性存储初始化",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if timeout < time.Second || timeout > 30*time.Minute {
				return fmt.Errorf("--timeout must be between 1s and 30m")
			}
			cfg, err := loadConfig(cmd, options)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := options.migrationRunner(ctx, cfg); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "control plane initialization completed")
			return err
		},
	}
	command.Flags().DurationVar(&timeout, "timeout", 4*time.Minute, "初始化总超时，1s 到 30m")
	return command
}
