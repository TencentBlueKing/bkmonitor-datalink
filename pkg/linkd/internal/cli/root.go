// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package cli 定义 Linkd 的命令行接口和进程组装逻辑。
package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"linkd/internal/cleaner"
	cleanerprocess "linkd/internal/cleaner/process"
	"linkd/internal/config"
	controlplaneprocess "linkd/internal/controlplane/process"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/telemetry"
)

const defaultConfigPath = "./configs/linkd.yaml"

type commandOptions struct {
	configPath           string
	logLevel             string
	logFormat            string
	version              string
	cleanerFactory       cleaner.FlowFactory
	cleanerRunner        CleanerRunner
	cleanerValidate      ProcessValidator
	lifecycleRunner      LifecycleRunner
	lifecycleValidate    ProcessValidator
	controlPlaneRunner   ControlPlaneRunner
	controlPlaneValidate ProcessValidator
}

// ProcessRunner 是常驻进程职责共享的装配签名。每个进程都接收独立的 telemetry runtime，
// 使 Cleaner、Lifecycle 和 Control Plane 在拆分部署时仍能分别导出指标。
type ProcessRunner func(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) error

// ProcessValidator 在启动日志、telemetry 和外部连接前校验指定进程职责所需配置。
type ProcessValidator func(config.Config) error

// CleanerRunner 是 Cleaner 进程装配入口。
type CleanerRunner = ProcessRunner

// LifecycleRunner 是 Lifecycle 进程装配入口。
type LifecycleRunner = ProcessRunner

// ControlPlaneRunner 是控制面进程装配入口。当前默认实现装配 Elasticsearch 存储和 Redis Stream
// 管理任务；后续管理任务、API 和 Leader Election 仍沿用该进程边界。
type ControlPlaneRunner = ProcessRunner

// Dependencies 汇总各职责启动命令需要的进程装配依赖。
// 已完成职责可在 nil 时使用正式默认实现；尚未完成职责必须在接管数据前明确失败。
type Dependencies struct {
	// CleanerFlowFactory 为每个启用 EventSource 创建 cleaner 处理流程。
	CleanerFlowFactory cleaner.FlowFactory
	// CleanerRunner 可在嵌入或测试场景替换默认 cleaner 进程；nil 使用正式实现。
	CleanerRunner CleanerRunner
	// LifecycleRunner 可在嵌入或测试场景替换 lifecycle 进程；nil 使用正式实现。
	LifecycleRunner LifecycleRunner
	// ControlPlaneRunner 可在嵌入或测试场景替换控制面进程；nil 使用正式任务装配。
	ControlPlaneRunner ControlPlaneRunner
}

// NewRootCommand 构造 Linkd 根命令。version 为空时按 dev 展示。
func NewRootCommand(version string, dependencies Dependencies) *cobra.Command {
	if version == "" {
		version = "dev"
	}

	lifecycleRunner := dependencies.LifecycleRunner
	lifecycleValidate := ProcessValidator(nil)
	if lifecycleRunner == nil {
		lifecycleRunner = lifecycleprocess.Run
		lifecycleValidate = lifecycleprocess.ValidateConfig
	}
	cleanerRunner := dependencies.CleanerRunner
	cleanerValidate := ProcessValidator(nil)
	if cleanerRunner == nil {
		cleanerRunner = cleanerprocess.Run
		cleanerValidate = cleanerprocess.ValidateConfig
	}
	controlPlaneRunner := dependencies.ControlPlaneRunner
	controlPlaneValidate := ProcessValidator(nil)
	if controlPlaneRunner == nil {
		controlPlaneRunner = controlplaneprocess.Run
		controlPlaneValidate = controlplaneprocess.ValidateConfig
	}
	options := &commandOptions{
		version:              version,
		cleanerFactory:       dependencies.CleanerFlowFactory,
		cleanerRunner:        cleanerRunner,
		cleanerValidate:      cleanerValidate,
		lifecycleRunner:      lifecycleRunner,
		lifecycleValidate:    lifecycleValidate,
		controlPlaneRunner:   controlPlaneRunner,
		controlPlaneValidate: controlPlaneValidate,
	}
	if dependencies.CleanerFlowFactory != nil {
		options.cleanerValidate = func(cfg config.Config) error {
			_, err := newCleanerScheduler(cfg.EventSources, cfg.Severity, dependencies.CleanerFlowFactory)
			return err
		}
	}
	root := &cobra.Command{
		Use:           "linkd",
		Short:         "Linkd 告警处理服务",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	flags := root.PersistentFlags()
	flags.StringVar(&options.configPath, "config", defaultConfigPath, "Linkd YAML 配置文件路径")
	flags.StringVar(&options.logLevel, "log-level", "", "显式覆盖日志级别")
	flags.StringVar(&options.logFormat, "log-format", "", "显式覆盖日志格式")

	root.AddCommand(
		newRunCommand(options),
		newStorageCommand(options),
		newConfigCommand(options),
		newVersionCommand(options),
	)
	return root
}

func newRunCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "run",
		Short: "启动 Linkd 常驻进程",
	}
	command.AddCommand(
		newAllInOneCommand(options),
		newCleanerCommand(options),
		newLifecycleCommand(options),
		newControlPlaneCommand(options),
	)
	return command
}

func newConfigCommand(options *commandOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "检查 Linkd 配置",
	}
	command.AddCommand(
		&cobra.Command{
			Use:   "validate",
			Short: "校验最终生效配置",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if _, err := loadConfig(cmd, options); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
				return err
			},
		},
		&cobra.Command{
			Use:   "print",
			Short: "打印最终生效配置",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := loadConfig(cmd, options)
				if err != nil {
					return err
				}
				data, err := config.MarshalRedacted(cfg)
				if err != nil {
					return err
				}
				if _, err := cmd.OutOrStdout().Write(data); err != nil {
					return fmt.Errorf("print config: %w", err)
				}
				return nil
			},
		},
	)
	return command
}

func newVersionCommand(options *commandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印 Linkd 版本",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), options.version)
			return err
		},
	}
}

func loadConfig(cmd *cobra.Command, options *commandOptions) (config.Config, error) {
	overrides := config.Overrides{}
	if cmd.Flags().Changed("log-level") {
		overrides.LogLevel = &options.logLevel
	}
	if cmd.Flags().Changed("log-format") {
		overrides.LogFormat = &options.logFormat
	}

	cfg, err := config.Load(options.configPath, overrides)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
