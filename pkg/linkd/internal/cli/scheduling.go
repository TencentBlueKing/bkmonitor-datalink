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
	"fmt"

	"github.com/spf13/cobra"
	"linkd/internal/redisclient"
	"linkd/internal/taskdispatch"
)

func newSchedulingCommand(options *commandOptions) *cobra.Command {
	root := &cobra.Command{Use: "scheduling", Short: "调度协调状态恢复工具"}
	var confirmed bool
	initialize := &cobra.Command{Use: "init", Short: "仅初始化缺失的协调记录，要求旧中心和 worker 已停止", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !confirmed {
			return fmt.Errorf("--confirm-stopped is required after stopping every old scheduler and worker")
		}
		cfg, err := loadConfig(cmd, options)
		if err != nil {
			return err
		}
		if cfg.Storage == nil || cfg.Storage.Redis == nil {
			return fmt.Errorf("redis config required")
		}
		client, err := redisclient.New(cfg.Storage.Redis.ClientOptions())
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		return taskdispatch.InitializeMissingState(cmd.Context(), client, cfg.Dispatch.WithDefaults().Deployment)
	}}
	initialize.Flags().BoolVar(&confirmed, "confirm-stopped", false, "确认旧中心和所有 worker 已停止")
	root.AddCommand(initialize)
	return root
}
