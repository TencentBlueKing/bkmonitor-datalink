// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command linkd-eventgen 按周期生成 standard 告警事件并写入指定 EventSource。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	linkdconfig "linkd/internal/config"
	"linkd/internal/eventgen"
	"linkd/internal/kafkaclient"
	"linkd/internal/logging"
)

const defaultConfigPath = "./configs/linkd.yaml"

// 镜像构建通过 ldflags 注入独立版本和对应源码提交。
var (
	version   = "dev"
	gitCommit = "unknown"
)

type managedPublisher interface {
	eventgen.Publisher
	Close()
}

type dependencies struct {
	loadConfig   func(path string) (linkdconfig.Config, error)
	newPublisher func(source linkdconfig.EventSource) (managedPublisher, error)
	newRunID     func() (string, error)
	resolveSeed  func(seed uint64) (uint64, error)
}

type commandOptions struct {
	configPath         string
	kafkaBrokers       []string
	kafkaTopic         string
	eventSourceID      string
	tenantID           string
	newAlertsPerMinute int
	cycleDuration      time.Duration
	meanLifetimeCycles int
	duplicatePercent   int
	scenarios          string
	seed               uint64
	maxActiveAlerts    int
	cycles             int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newRootCommand(version, gitCommit, defaultDependencies())
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintln(command.ErrOrStderr(), err)
		os.Exit(1)
	}
}

func defaultDependencies() dependencies {
	return dependencies{
		loadConfig: func(path string) (linkdconfig.Config, error) {
			return linkdconfig.Load(path, linkdconfig.Overrides{})
		},
		newPublisher: func(source linkdconfig.EventSource) (managedPublisher, error) {
			return eventgen.NewKafkaPublisher(source)
		},
		newRunID:    eventgen.NewRunID,
		resolveSeed: eventgen.ResolveSeed,
	}
}

func newRootCommand(buildVersion, commit string, deps dependencies) *cobra.Command {
	if buildVersion == "" {
		buildVersion = "dev"
	}
	if commit == "" {
		commit = "unknown"
	}
	options := commandOptions{}
	command := &cobra.Command{
		Use:           "linkd-eventgen",
		Short:         "向 Linkd EventSource 持续推送 standard 模拟告警",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), cmd, options, deps)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.configPath, "config", defaultConfigPath, "Linkd YAML 配置文件路径")
	flags.StringSliceVar(&options.kafkaBrokers, "kafka-brokers", nil, "直接推送的 Kafka brokers，逗号分隔；与 --kafka-topic 配套使用")
	flags.StringVar(&options.kafkaTopic, "kafka-topic", "", "直接推送的 Kafka topic；直连模式不读取配置文件")
	flags.StringVar(&options.eventSourceID, "event-source-id", "", "必填的 EventSource event_source_id")
	flags.StringVar(&options.tenantID, "tenant-id", "", "消息租户；来源未固定 related_tenant_id 时必填")
	flags.IntVar(
		&options.newAlertsPerMinute,
		"new-alerts-per-minute",
		eventgen.DefaultNewAlertsPerMinute,
		"每分钟新生成的告警数量",
	)
	flags.IntVar(&options.duplicatePercent, "duplicate-percent", 0, "随机追加完全相同 Event delivery 的百分比")
	flags.DurationVar(&options.cycleDuration, "cycle-duration", eventgen.DefaultCycleDuration, "调度周期")
	flags.IntVar(
		&options.meanLifetimeCycles,
		"mean-lifetime-cycles",
		eventgen.DefaultMeanLifetimeCycles,
		"告警平均存活周期数",
	)
	flags.StringVar(&options.scenarios, "scenarios", eventgen.SupportedScenariosCSV(), "逗号分隔的告警场景")
	flags.Uint64Var(&options.seed, "seed", 0, "随机种子；0 表示自动生成")
	flags.IntVar(
		&options.maxActiveAlerts,
		"max-active-alerts",
		eventgen.DefaultMaxActiveAlerts,
		"进程内活动告警硬上限",
	)
	flags.IntVar(&options.cycles, "cycles", 0, "运行周期数；0 表示持续运行")
	command.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "显示模拟器版本和 Git commit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 版本查询不加载配置、不创建 producer，也不向 Kafka 发送消息。
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "version: %s\ngit_commit: %s\n", buildVersion, commit)
			return err
		},
	})
	return command
}

func run(ctx context.Context, command *cobra.Command, options commandOptions, deps dependencies) error {
	if deps.newPublisher == nil || deps.newRunID == nil || deps.resolveSeed == nil {
		return fmt.Errorf("event generator dependencies are incomplete")
	}
	cfg, err := generatorConfig(command, options, deps)
	if err != nil {
		return err
	}
	source, tenantID, err := eventgen.ResolveSource(cfg, options.eventSourceID, options.tenantID)
	if err != nil {
		return err
	}
	scenarios, err := eventgen.ParseScenarios(options.scenarios)
	if err != nil {
		return err
	}
	seed, err := deps.resolveSeed(options.seed)
	if err != nil {
		return err
	}
	runID, err := deps.newRunID()
	if err != nil {
		return err
	}
	logger, err := logging.New(cfg.Logging, command.ErrOrStderr())
	if err != nil {
		return fmt.Errorf("create event generator logger: %w", err)
	}
	publisher, err := deps.newPublisher(source)
	if err != nil {
		return err
	}
	defer publisher.Close()

	engine, err := eventgen.New(eventgen.Config{
		RunID: runID, TenantID: tenantID,
		NewAlertsPerMinute: options.newAlertsPerMinute,
		CycleDuration:      options.cycleDuration,
		MeanLifetimeCycles: options.meanLifetimeCycles,
		DuplicatePercent:   options.duplicatePercent,
		Scenarios:          scenarios,
		Seed:               seed,
		MaxActiveAlerts:    options.maxActiveAlerts,
		Cycles:             options.cycles,
	}, source, cfg.Severity, publisher, logger)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "event generator started",
		"run_id", runID,
		"seed", seed,
		"event_source_id", source.EventSourceID,
		"tenant_id", tenantID,
		"topic", source.Storage.Kafka.Topic,
		"new_alerts_per_minute", options.newAlertsPerMinute,
		"cycle_duration", options.cycleDuration,
		"mean_lifetime_cycles", options.meanLifetimeCycles,
		"duplicate_percent", options.duplicatePercent,
		"max_active_alerts", options.maxActiveAlerts,
		"cycles", options.cycles,
	)
	if err := engine.Run(ctx); err != nil {
		return err
	}
	logger.InfoContext(ctx, "event generator stopped", "run_id", runID)
	return nil
}

func generatorConfig(command *cobra.Command, options commandOptions, deps dependencies) (linkdconfig.Config, error) {
	direct := command.Flags().Changed("kafka-brokers") || command.Flags().Changed("kafka-topic")
	if !direct {
		if deps.loadConfig == nil {
			return linkdconfig.Config{}, fmt.Errorf("event generator config loader is required")
		}
		return deps.loadConfig(options.configPath)
	}
	if command.Flags().Changed("config") {
		return linkdconfig.Config{}, fmt.Errorf("--config cannot be combined with direct Kafka options")
	}
	if len(options.kafkaBrokers) == 0 || len(options.kafkaBrokers) > 32 || options.kafkaTopic == "" {
		return linkdconfig.Config{}, fmt.Errorf("direct Kafka mode requires 1 to 32 --kafka-brokers and --kafka-topic")
	}
	brokers, err := kafkaclient.NormalizeBrokers(options.kafkaBrokers)
	if err != nil {
		return linkdconfig.Config{}, fmt.Errorf("direct Kafka brokers: %w", err)
	}
	cfg := linkdconfig.Default()
	// 直连只创建模拟器自身的 standard 来源，不读取或修改控制面配置。
	// ConsumerGroup 仅满足来源结构校验；producer 不会加入任何消费组。
	cfg.EventSources = []linkdconfig.EventSource{{
		EventSourceID: options.eventSourceID, Enabled: true,
		Cleaner:         linkdconfig.CleanerConfig{Type: linkdconfig.CleanerTypeStandard},
		FingerprintMode: linkdconfig.FingerprintModeField, FingerprintField: "source_alert_id",
		Storage: linkdconfig.EventSourceStorageConfig{
			Type:  linkdconfig.StorageTypeKafka,
			Kafka: linkdconfig.KafkaStorageConfig{Brokers: brokers, Topic: options.kafkaTopic, ConsumerGroup: "linkd-eventgen"},
		},
	}}
	if err := linkdconfig.ValidateEventSources(cfg.EventSources, cfg.Severity); err != nil {
		return linkdconfig.Config{}, fmt.Errorf("direct Kafka source: %w", err)
	}
	return cfg, nil
}
