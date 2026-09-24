// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"fmt"
	"os"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/dynamicconfig"
	"linkd/internal/taskdispatch"
	"linkd/internal/taskdispatch/observation"
	"linkd/internal/telemetry"
)

// taskCatalog 与实际装配共用配置判断，只挑选可公开的生效预算，不复制连接对象。
func taskCatalog(cfg config.Config, providers int) *taskstate.Registry {
	es := elasticsearchTaskSettings(cfg)
	esEnabled := hasElasticsearchTask(cfg)
	source := "default"
	if cfg.ControlPlane != nil && cfg.ControlPlane.Elasticsearch != nil {
		source = "explicit"
	}
	makeTask := func(id, name, group, kind, description string, enabled bool, interval, deadline float64, origin string, settings map[string]any) taskstate.Definition {
		return taskstate.Definition{ID: id, Name: name, Group: group, Kind: kind, Description: description, Enabled: enabled, IntervalSeconds: interval, DeadlineSeconds: deadline, ConfigSource: origin, Settings: settings, DependsOn: []string{}}
	}
	tasks := []taskstate.Definition{
		makeTask("scheduler", "调度协调", "调度与来源", "periodic", "恢复未完成发布、探测 Kafka 元数据并分配任务；中心资格由 Redis 租约保护。", true, 1, 20, "default", map[string]any{"probeConcurrency": 4, "probeTimeoutSeconds": 5}),
		makeTask("source-providers", "来源 Provider 同步", "调度与来源", "periodic", "拉取增量并完整应用；没有注入 Provider 时不启动。", providers > 0, 30, 30, "injected", map[string]any{"providers": providers, "maxChangesPerRound": 1000}),
		makeTask("elasticsearch-schema-and-active-reconciler", "Schema & Active", "消息与存储维护", "periodic", "对账 Schema、模板、Active 索引和静态 alias。", esEnabled, es.SchemaAndActiveReconcileInterval().Seconds(), 0, source, map[string]any{}),
		makeTask("elasticsearch-bucket-manager", "Bucket Manager", "消息与存储维护", "periodic", "维护当前及相邻时间桶；依赖启动阶段 Schema 对账。", esEnabled, es.BucketReconcileInterval().Seconds(), 0, source, map[string]any{}),
		makeTask("elasticsearch-alert-archiver", "Alert Archiver", "消息与存储维护", "continuous", "有积压时连续归档，到达尾部或失败后等待。", esEnabled, es.ArchiveInterval().Seconds(), 0, source, map[string]any{"archiveBatchSize": es.ArchiveBatchSize, "archiveWorkerCount": es.ArchiveWorkerCount}),
	}
	tasks[1].DisabledReason = "未注入来源 Provider"
	tasks[3].DependsOn = []string{tasks[2].ID}
	tasks[4].DependsOn = []string{tasks[3].ID}
	for i := 2; i < 5; i++ {
		tasks[i].DisabledReason = "当前仓储不是 Elasticsearch"
	}
	if esEnabled {
		p := cfg.Storage.Elasticsearch.TimePartition.WithDefaults()
		tasks[3].Settings = map[string]any{"eventBucketDays": p.EventBucketDays, "alertHistoryBucketDays": p.AlertHistoryBucketDays, "alertLogBucketDays": p.AlertLogBucketDays, "precreatePastBuckets": p.PrecreatePastBuckets, "precreateFutureBuckets": p.PrecreateFutureBuckets, "maxBucketsPerEntity": p.MaxBucketsPerEntity}
	}
	rs := cfg.RedisStreamSettings()
	if rs == nil {
		v := config.RedisStreamManagerConfig{}.WithDefaults()
		rs = &v
	}
	redisSource := "default"
	if cfg.ControlPlane != nil && cfg.ControlPlane.RedisStream != nil {
		redisSource = "explicit"
	}
	redisTask := makeTask("redis-stream-manager", "Redis Stream Manager", "消息与存储维护", "periodic", "每轮扫描最多 16 个来源并采集、检查安全裁剪边界。", hasRedisStreamTask(cfg), rs.ReconcileInterval().Seconds(), rs.OperationTimeout().Seconds(), redisSource, map[string]any{"sourcePageSize": 16, "operationTimeoutSeconds": rs.OperationTimeoutSeconds, "maxEntries": rs.MaxEntries, "trimBatchSize": rs.TrimBatchSize, "maxTrimEntriesPerCycle": rs.MaxTrimEntriesPerCycle})
	redisTask.DisabledReason = "需要 Redis 与 Lifecycle 配置"
	if !rs.IsEnabled() {
		redisTask.DisabledReason = "control_plane.redis_stream.enabled 显式设为 false"
	}
	tasks = append(tasks, redisTask)
	ai := config.ActiveIndexConfig{}.WithDefaults()
	if cfg.ControlPlane != nil && cfg.ControlPlane.ActiveIndex != nil {
		ai = cfg.ControlPlane.ActiveIndex.WithDefaults()
	}
	tasks = append(tasks, makeTask("active-alert-indexes", "策略活跃索引维护", "投影与配置", "periodic", "发现已发布 Hook 目标，独立初始化并刷新到期策略；无目标时正常空闲。", true, 5, 0, "default", map[string]any{"pollIntervalSeconds": ai.PollIntervalSeconds, "reconcileIntervalSeconds": ai.ReconcileIntervalSeconds, "operationTimeoutSeconds": ai.OperationTimeoutSeconds, "batchSize": ai.BatchSize, "maxRows": ai.MaxRows, "maxBytes": ai.MaxBytes, "maxTargets": 32}))
	dynamic := makeTask("dynamic-config", "动态配置同步", "投影与配置", "notification", "合并上游通知并周期补读，校验和持久化后发布有效配置。", false, 0, 0, "explicit", map[string]any{})
	dynamic.DisabledReason = "未启用 control_plane.dynamic_config"
	if c := cfg.ControlPlane; c != nil && c.DynamicConfig != nil && c.DynamicConfig.Enabled && c.DynamicConfig.Bindings.Severity != nil {
		s := c.DynamicConfig.Sources[c.DynamicConfig.Bindings.Severity.Source].WithDefaults()
		dynamic.Enabled = true
		dynamic.IntervalSeconds = s.Interval().Seconds()
		dynamic.DeadlineSeconds = s.Timeout().Seconds() * 3
		dynamic.Settings = map[string]any{"sourceType": s.Type, "operationTimeoutSeconds": s.OperationTimeoutSeconds}
	}
	if cfg.ControlPlane != nil && cfg.ControlPlane.ActiveIndex != nil {
		tasks[len(tasks)-1].ConfigSource = "explicit"
	}
	tasks = append(tasks, dynamic, makeTask("source-api", "管理 API", "服务", "service", "提供管理 token 保护的只读观测和来源管理接口。", true, 0, 0, "explicit", map[string]any{"requestTimeoutSeconds": 10}))
	for i := range tasks {
		if tasks[i].Enabled {
			tasks[i].DisabledReason = ""
		}
	}
	host, _ := os.Hostname()
	return taskstate.New(fmt.Sprintf("%s-%d", host, os.Getpid()), tasks)
}

// taskObserver 将同一轮执行同时交给当前状态与历史指标，保持已有指标计数语义。
type taskObserver struct {
	registry *taskstate.Registry
	id       string
	metric   *telemetry.ControlPlaneTaskObserver
}

func (o taskObserver) SetActive(ctx context.Context, active bool) { o.metric.SetActive(ctx, active) }

func (o taskObserver) RunStarted() { o.registry.Begin(o.id, "", "") }

func (o taskObserver) RunFinished(ctx context.Context, d time.Duration, ok bool) {
	code := ""
	if !ok {
		code = "execution_failed"
	}
	o.registry.Finish(ctx, o.id, "", "", d, -1, 0, code)
	if ctx.Err() == nil {
		o.metric.RunFinished(ctx, d, ok)
	}
}

// dispatchTaskObserver 保留协议遥测；当前状态仅接收固定子流程名及提交后的聚合。
type dispatchTaskObserver struct {
	taskdispatch.Observer
	registry *taskstate.Registry
	metric   *telemetry.ControlPlaneTaskObserver
}

func (o dispatchTaskObserver) Operation(ctx context.Context, op string, ok bool, d time.Duration) {
	o.Observer.Operation(ctx, op, ok, d)
	step, name := op, map[string]string{"reconcile": "调度轮次", "release_recovery": "发布恢复与版本读取", "kafka_probe": "Kafka 元数据探测"}[op]
	if name == "" {
		return
	}
	if op == "reconcile" {
		step = ""
	}
	code := ""
	if !ok {
		code = op + "_failed"
	}
	o.registry.Finish(ctx, "scheduler", step, name, d, -1, 0, code)
	if op == "reconcile" && ctx.Err() == nil {
		o.metric.RunFinished(ctx, d, ok)
	}
}

func (o dispatchTaskObserver) ControllerSnapshot(ctx context.Context, s observation.ControllerObservation) {
	o.Observer.ControllerSnapshot(ctx, s)
	total := 0
	for _, role := range s.Tasks {
		for _, n := range role {
			total += int(n)
		}
	}
	o.registry.Finish(ctx, "scheduler", "assignment", "任务分配", 0, total, 0, "")
	code := ""
	if s.Metadata["error"] > 0 {
		code = "metadata_unavailable"
	}
	o.registry.Finish(ctx, "scheduler", "metadata", "来源元数据健康", 0, int(s.Metadata["ready"]), int(s.Metadata["error"]), code)
}

func (o dispatchTaskObserver) OperationStarted(op string) {
	if op == "reconcile" {
		o.registry.Begin("scheduler", "", "")
	}
}

type dynamicTaskObserver struct {
	registry *taskstate.Registry
	metric   *telemetry.ControlPlaneTaskObserver
}

func (o dynamicTaskObserver) SyncStarted() { o.registry.Begin("dynamic-config", "", "") }

func (o dynamicTaskObserver) SyncFinished(ctx context.Context, d time.Duration, status dynamicconfig.Status) {
	o.registry.Finish(ctx, "dynamic-config", "", "", d, -1, 0, status.Error)
	if ctx.Err() == nil {
		o.metric.RunFinished(ctx, d, status.Error == "")
	}
}

type providerTaskObserver struct {
	registry *taskstate.Registry
	metric   *telemetry.ControlPlaneTaskObserver
	index    int
}

func (o providerTaskObserver) RoundStarted() {
	o.registry.Begin("source-providers", fmt.Sprintf("provider-%d", o.index), fmt.Sprintf("Provider %d", o.index+1))
}

func (o providerTaskObserver) RoundFinished(ctx context.Context, d time.Duration, n int, ok bool) {
	code := ""
	if !ok {
		code = "provider_apply_failed"
	}
	o.registry.Finish(ctx, "source-providers", fmt.Sprintf("provider-%d", o.index), fmt.Sprintf("Provider %d", o.index+1), d, n, 0, code)
	o.registry.Finish(ctx, "source-providers", "", "", d, n, 0, code)
	if ctx.Err() == nil {
		o.metric.RunFinished(ctx, d, ok)
	}
}

func (o taskObserver) RunCanceled(ctx context.Context, d time.Duration) {
	o.registry.Finish(ctx, o.id, "", "", d, -1, 0, "")
}
