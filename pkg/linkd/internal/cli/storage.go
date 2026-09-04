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
	"time"

	"github.com/spf13/cobra"
	repositoryassembly "linkd/internal/store/assembly"
)

const storagePrepareElasticsearchConnections = 4

func newStorageCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "storage",
		Short: "执行 Linkd 存储管理操作",
	}
	command.AddCommand(newStoragePrepareCommand(options))
	return command
}

func newStoragePrepareCommand(options *commandOptions) *cobra.Command {
	var fromValue, toValue string
	command := &cobra.Command{
		Use:   "prepare",
		Short: "为历史回放预创建指定 UTC 时间范围的 Elasticsearch 索引桶",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd, options)
			if err != nil {
				return err
			}
			if err := repositoryassembly.ValidateElasticsearchManagerConfig(cfg); err != nil {
				return err
			}
			from, err := time.Parse(time.RFC3339, fromValue)
			if err != nil {
				return fmt.Errorf("parse --from as RFC3339: %w", err)
			}
			to, err := time.Parse(time.RFC3339, toValue)
			if err != nil {
				return fmt.Errorf("parse --to as RFC3339: %w", err)
			}
			runtime, err := repositoryassembly.OpenElasticsearchManager(
				cmd.Context(),
				*cfg.Storage.Elasticsearch,
				storagePrepareElasticsearchConnections,
			)
			if err != nil {
				return err
			}
			defer runtime.Close()
			if err := runtime.Manager.PrepareRange(cmd.Context(), from, to); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "elasticsearch buckets prepared")
			return err
		},
	}
	command.Flags().StringVar(&fromValue, "from", "", "范围起点，RFC3339")
	command.Flags().StringVar(&toValue, "to", "", "范围终点，RFC3339")
	_ = command.MarkFlagRequired("from")
	_ = command.MarkFlagRequired("to")
	return command
}
