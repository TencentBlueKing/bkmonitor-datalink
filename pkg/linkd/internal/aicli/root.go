// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package aicli 提供 Linkd Console 的有界诊断客户端、显式操作保护及离线 skill 安装。
// 本包不装配服务端进程，不直接连接业务存储；人工最终确认由随附 skill 的流程约束。
package aicli

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

type cliError struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	Operation      string `json:"operation,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

func (e *cliError) Error() string { return e.Message }

func problem(code, message string) error { return &cliError{Code: code, Message: message} }

func emit(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return problem("output_error", "结果输出失败，命令可能已完成；请先核对状态，勿据此重复操作")
	}
	return nil
}

type options struct {
	configPath string
	profile    string
}

// Execute 执行单次命令；成功仅向 stdout 写 JSON（帮助为文本），失败向 stderr 写安全错误。
// 不回显 Cobra 的原始错误，因为未知参数或位置参数可能携带误输入的密码。
func Execute(ctx context.Context, args []string, input io.Reader, output, errorOutput io.Writer, version, commit string) int {
	root := newCommand(version, commit)
	root.SetArgs(args)
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(errorOutput)
	if err := root.ExecuteContext(ctx); err != nil {
		failure := &cliError{Code: "invalid_argument", Message: "命令或参数无效，请使用 --help；参数值不会回显"}
		var known *cliError
		if errors.As(err, &known) {
			failure = known
		}
		_ = emit(errorOutput, map[string]any{"error": failure})
		return 1
	}
	return 0
}

func newCommand(version, commit string) *cobra.Command {
	opts := &options{}
	root := &cobra.Command{Use: "linkd-cli", Short: "面向 Linkd 开发维护人员的 AI 调试运维工具", SilenceUsage: true, SilenceErrors: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "客户端配置路径（默认 $XDG_CONFIG_HOME/linkd-cli/config.yaml 或 ~/.config/linkd-cli/config.yaml）")
	root.PersistentFlags().StringVar(&opts.profile, "profile", "", "显式选择目标环境，优先于当前 profile")
	root.AddCommand(configCommand(opts), apiCommand(opts), skillsCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "显示 CLI 构建版本", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return emit(cmd.OutOrStdout(), map[string]string{"version": version, "git_commit": commit})
	}})
	return root
}
