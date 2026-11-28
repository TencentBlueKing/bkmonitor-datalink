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
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"linkd/internal/collector"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
	"linkd/internal/redisclient"
	"linkd/internal/taskdispatch"
	"linkd/internal/telemetry"
)

func newCollectorCommand(options *commandOptions) *cobra.Command {
	return newProcessCommand(options, processCommand{use: "collector", short: "启动告警源拉取执行进程", role: telemetry.RoleCollector, validate: func(cfg config.Config) error {
		if cfg.Collector == nil || cfg.Storage == nil || cfg.Storage.Redis == nil {
			return fmt.Errorf("collector and storage.redis configuration required")
		}
		return nil
	}, run: runCollector})
}

func runCollector(ctx context.Context, cfg config.Config, logger *slog.Logger, _ *telemetry.Runtime) error {
	// 接收端和 worker 必须使用同一默认部署命名空间，不能把空配置当成另一个 Redis 前缀。
	cfg.Dispatch = cfg.Dispatch.WithDefaults()
	redis, err := redisclient.New(cfg.Storage.Redis.ClientOptions())
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	budget := time.Duration(cfg.Collector.TimeoutSeconds) * time.Second
	executor, err := pluginruntime.NewHTTPClient(&http.Client{Transport: transport, Timeout: budget}, cfg.Collector.Endpoint, cfg.Collector.Token, cfg.Collector.MaxResponseBytes)
	if err != nil {
		return err
	}
	progress := collector.NewRedisProgress(redis, cfg.Dispatch.Deployment)
	receipts := collector.NewRedisReceipts(redis, cfg.Dispatch.Deployment, time.Duration(cfg.Collector.CompletedRetentionSeconds)*time.Second)
	return taskdispatch.Serve(ctx, cfg, "collector", func(taskCtx context.Context, task taskdispatch.Task, source config.EventSource) error {
		collection := source.Collection
		if collection == nil || (collection.Mode != "pull" && collection.RecoveryIntervalMinutes == 0) || source.RelatedTenantID == "" {
			return fmt.Errorf("pull source requires collection and explicit tenant")
		}
		// Kafka 连接与结果写入器绑定本任务代次；任务退出时关闭，不让继任任务复用旧授权。
		results := receipts.WithWriter(taskdispatch.NewTaskWriter(redis, cfg.Dispatch.Deployment, task))
		factory := collector.NewFactory(executor, results)
		defer factory.Close()
		service, err := factory.Build(taskCtx, eventsource.Release{ID: task.Source, Version: task.Version, Spec: source})
		if err != nil {
			return err
		}
		request := pluginruntime.Request{TenantID: source.RelatedTenantID, SourceID: source.EventSourceID, SourceVersion: task.Version, SourceName: collection.SourceName, Timezone: collection.Timezone, State: collection.InitialState, PluginSourceID: collection.PluginSourceID, Plugin: collection.Plugin, Mode: collection.Mode, Configs: collection.Configs, IntervalMinutes: collection.IntervalMinutes, RecoveryIntervalMinutes: collection.RecoveryIntervalMinutes}
		var recovery *collector.Recovery
		if collection.Mode == "pull" {
			batch := cfg.Collector.RecoveryBatchSize
			if batch == 0 {
				batch = 32
			}
			client := taskdispatch.Client{URL: cfg.Dispatch.URL, Token: cfg.Dispatch.WorkerToken, WorkerID: task.Worker, HTTP: &http.Client{Transport: transport, Timeout: budget}}
			recovery = &collector.Recovery{Pending: receipts, BatchSize: batch, Timeout: budget, Logger: logger, Replay: func(ctx context.Context, original pluginruntime.Request) (pluginruntime.Result, error) {
				// 旧 push 记录可能仍由接收进程处理，模式切换不等于取得它的恢复所有权。
				if original.Mode == "push" {
					return pluginruntime.Result{}, fmt.Errorf("push request recovery requires intake execution ownership")
				}
				release, err := collectorReplayRelease(ctx, client, task.ID, original.RequestID)
				if err != nil {
					return pluginruntime.Result{}, err
				}
				replayService, err := factory.Build(ctx, release)
				if err != nil {
					return pluginruntime.Result{}, err
				}
				return replayService.Run(ctx, original)
			}}
		}
		return collector.RunPull(taskCtx, request, service, progress, budget, logger, recovery)
	}, logger)
}

func collectorReplayRelease(ctx context.Context, client taskdispatch.Client, task, request string) (eventsource.Release, error) {
	query := url.Values{"task": {task}, "request_id": {request}}
	var release eventsource.Release
	err := client.Call(ctx, http.MethodGet, "/internal/collector/replay-release?"+query.Encode(), nil, &release)
	return release, err
}
