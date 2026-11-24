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
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"linkd/internal/enrich/kingeye/convert"
)

func newEnrichCommand() *cobra.Command {
	root := &cobra.Command{Use: "enrich", Short: "丰富调试与离线配置工具"}
	var file string
	convert := &cobra.Command{Use: "convert-kingeye", Short: "将 Kingeye 导出 JSON 转为两个分组处理器及人工复核报告，不发布", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if file == "" {
			return fmt.Errorf("--file is required")
		}
		//nolint:gosec // CLI 显式指定本地配置输入，文件路径属于用户授权的读取范围。
		reader, err := os.Open(file)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("input exceeds 1 MiB")
		}
		var input convert.Input
		if err := json.Unmarshal(data, &input); err != nil {
			return err
		}
		result, err := convert.Convert(input)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}}
	convert.Flags().StringVar(&file, "file", "", "离线规则、模型目录和字段映射 JSON")
	root.AddCommand(convert)
	return root
}
