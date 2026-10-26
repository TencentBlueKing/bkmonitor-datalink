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
	"log/slog"
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
		makeTask("elasticsearch-bucket-manager", "Bucket Manager", "消息与存储维护", "periodic", "维护当前及相邻时间桶；等待后台首次 Schema 对账成功。", esEnabled, es.BucketReconcileInterval().Seconds(), 0, source, map[string]any{}),
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
	tasks = append(tasks, makeTask("control-storage", "控制配置存储准备", "消息与存储维护", "continuous", "后台准备来源、策略集合和内置合并来源，失败重试，不阻塞已有调度与管理 API。", true, taskRetryInterval.Seconds(), 0, "default", map[string]any{}))
	rs := cfg.RedisStreamSettings()
	if rs == nil {
		v := config.RedisStreamManagerConfig{}.WithDefaults()
		rs = &v
	}
	redisSource := "default"
	if cfg.ControlPlane != nil && cfg.ControlPlane.RedisStream != nil {
		redisSource = "explicit"
	}
	redisTask := makeTask("redis-stream-manager", "Redis Stream Manager", "消息与存储维护", "periodic", "每轮扫描最多 16 个来源，各来源独立限时采集并检查安全裁剪边界。", hasRedisStreamTask(cfg), rs.ReconcileInterval().Seconds(), rs.OperationTimeout().Seconds()*float64(sourceStreamPageSize+1), redisSource, map[string]any{"sourcePageSize": sourceStreamPageSize, "operationTimeoutSeconds": rs.OperationTimeoutSeconds, "maxEntries": rs.MaxEntries, "trimBatchSize": rs.TrimBatchSize, "maxTrimEntriesPerCycle": rs.MaxTrimEntriesPerCycle})
	redisTask.DisabledReason = "需要 Redis 与 Lifecycle 配置"
	if !rs.IsEnabled() {
		redisTask.DisabledReason = "control_plane.redis_stream.enabled 显式设为 false"
	}
	tasks = append(tasks, redisTask)
	ai := config.ActiveIndexConfig{}.WithDefaults()
	if cfg.ControlPlane != nil && cfg.ControlPlane.ActiveIndex != nil {
		ai = cfg.ControlPlane.ActiveIndex.WithDefaults()
	}
	tasks = append(tasks, makeTask("active-alert-indexes", "活跃告警缓存修复", "投影与配置", "periodic", "按已发布限流规则增量修复活跃身份及分组；普通事件不依赖任务成功。", true, float64(ai.ReconcileIntervalSeconds), 0, "default", map[string]any{"pollIntervalSeconds": ai.PollIntervalSeconds, "reconcileIntervalSeconds": ai.ReconcileIntervalSeconds, "operationTimeoutSeconds": ai.OperationTimeoutSeconds, "pageSize": ai.PageSize, "maxBytes": ai.MaxBytes}))
	tasks = append(tasks, makeTask("source-limit-defaults", "来源限流默认规则", "调度与来源", "periodic", "仅给已有且启用的 built_in_bk 补齐缺失的策略限流规则，不覆盖显式配置。", true, 30, 5, "default", map[string]any{}))
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
	tasks = append(tasks, makeTask("policy-publication", "策略发布恢复", "投影与配置", "periodic", "分页完成已经持久化的待发布策略；每条策略通过 CAS 推进不可变发布。", true, 5, 10, "default", map[string]any{"pageSize": 16}), dynamic, makeTask("source-api", "管理 API", "服务", "service", "提供管理 token 保护的只读观测和来源管理接口。", true, 0, 0, "explicit", map[string]any{"requestTimeoutSeconds": 10}))
	tasks = append(tasks, makeTask("policy-catalog", "策略目录准备", "投影与配置", "periodic", "共享已发布策略目录和候选索引，发布失效后准备新的完整目录。", true, 1, 0, "default", map[string]any{"refreshSeconds": 60}))
	dependency := makeTask("dependency-candidate-index", "依赖主候选准备", "投影与配置", "periodic", "新策略和共享目标变化后，批量准备依赖屏蔽主候选。", cfg.Lifecycle != nil, 1, 0, "default", map[string]any{"concurrency": 4, "dependencyMaxAgeSeconds": 60})
	dependency.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, dependency)
	shield := makeTask("shield-check", "屏蔽定时解除", "投影与配置", "periodic", "分页检查持久化屏蔽及待完成状态输出；同租约下重读、解除，只同步状态。", cfg.Lifecycle != nil, 5, 45, "default", map[string]any{"pageSize": 16, "concurrency": 4, "operationTimeoutSeconds": 10})
	shield.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, shield)
	hints := makeTask("shield-hints", "屏蔽事件提示", "投影与配置", "notification", "订阅依赖主终态提示，按当前子绑定有界复查；断连、丢失或容量满由定时扫描补偿。", cfg.Lifecycle != nil, 0, 45, "default", map[string]any{"pendingMains": 64, "pageSize": 16, "sharedConcurrency": 4, "subscriptionRetrySeconds": 5})
	hints.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, hints)

	requests := makeTask("shield-requests", "屏蔽手动复查", "投影与配置", "periodic", "执行已排队的复查请求；版本变化使请求失效，失败保留诊断，不强制解除或补发处置。", cfg.Lifecycle != nil, 1, 90, "default", map[string]any{"pageSize": 16, "sharedConcurrency": 4, "operationTimeoutSeconds": 20, "pendingPerTenant": 1024})
	requests.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, requests)
	suppressionRequests := makeTask("suppression-requests", "抑制受控对账", "投影与配置", "periodic", "按原 owner/代次复核真实告警，仅移除失效登记；不补发处置、不重建计数。", cfg.Lifecycle != nil, 1, 90, "default", map[string]any{"pageSize": 16, "concurrency": 4, "operationTimeoutSeconds": 20, "pendingPerTenant": 1024})
	suppressionRequests.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, suppressionRequests)
	mergeRequests := makeTask("merge-requests", "合并受控请求", "投影与配置", "periodic", "校验原裁决/关系版本，复用正式窗口租约接续有界步骤；不重置业务结果。", cfg.Lifecycle != nil, 1, 90, "default", map[string]any{"pageSize": 16, "concurrency": 4, "operationTimeoutSeconds": 20, "pendingPerTenant": 1024})
	mergeRequests.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, mergeRequests)
	mergeBudget := cfg.MergeControlSettings()
	mergeRuntime := makeTask("merge", "合并任务运行时", "投影与配置", "service", "监督组级协调与进程内成员批次，共用连接池；组锁不包住成员批次。", cfg.Lifecycle != nil, 0, 0, "default", map[string]any{"sharedConcurrency": mergeBudget.Concurrency, "memberBatchSize": mergeBudget.MemberBatchSize})
	mergeRuntime.DisabledReason = "需要 Lifecycle 配置"
	tasks = append(tasks, mergeRuntime)
	for _, spec := range []struct {
		id, name, description string
		interval              float64
	}{
		{"merge-judge", "合并窗口裁决", "逐窗口检查持久化等待，冻结条件或按原截止时间清理丢失窗口。", 1},
		{"merge-decisions", "合并执行恢复", "推进成员快照、内部父 Event、关系建立及失败释放，并确认窗口提示清理。", 1},
		{"merge-relations", "合并关系对账", "周期检查父子终态，解除已终结父的关系；活动子等待下一条 Event 才判断处置。", 30},
		{"merge-batches", "合并成员批次", "内存拆批，按空闲名额启动 goroutine；结果直接回报，崩溃后从关系重建。", 1},
	} {
		pageSize := 16
		if spec.id == "merge-judge" {
			pageSize = 128
		}
		task := makeTask(spec.id, spec.name, "投影与配置", "periodic", spec.description, cfg.Lifecycle != nil, spec.interval, 45, "default", map[string]any{"pageSize": pageSize, "sharedConcurrency": mergeBudget.Concurrency, "operationTimeoutSeconds": 10, "memberBatchSize": mergeBudget.MemberBatchSize})
		if spec.id == "merge-batches" {
			task.Kind, task.IntervalSeconds, task.DeadlineSeconds = "continuous", 0, 0
			task.Settings = map[string]any{"concurrency": mergeBudget.Concurrency, "memberBatchSize": mergeBudget.MemberBatchSize, "retryIntervalSeconds": 1}
		}
		task.DisabledReason = "需要 Lifecycle 配置"
		tasks = append(tasks, task)
	}
	delivery := config.KACDeliveryConfig{}.WithDefaults()
	if cfg.Plugins.KAC != nil {
		delivery = cfg.Plugins.KAC.Delivery.WithDefaults()
	}
	for _, spec := range []struct{ id, name, description string }{
		{"kac-delivery", "KAC 队列投影", "消费 Redis Stream，批量局部更新兼容告警并独立并发发送动作。"},
		{"kac-index-maintenance", "KAC 兼容索引维护", "维护原 alarm_event 模板、ILM 与字段，不创建同步元数据。"},
	} {
		task := makeTask(spec.id, spec.name, "投影与配置", "service", spec.description, hasKACDelivery(cfg), 0, 0, "explicit", map[string]any{"projectionConcurrency": delivery.ProjectionConcurrency, "actionConcurrency": delivery.ActionConcurrency, "batchSize": delivery.BatchSize})
		if spec.id == "kac-index-maintenance" {
			task.Kind = "periodic"
			task.IntervalSeconds = 60
			task.DeadlineSeconds = 30
		}
		task.DisabledReason = "需要 Lifecycle 和已启用的 plugins.kac"
		tasks = append(tasks, task)
	}
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
	logger   *slog.Logger
}

func (o dispatchTaskObserver) ReconcileFailed(ctx context.Context, err error) {
	o.logger.ErrorContext(ctx, "scheduler reconciliation failed; retrying", "module", "controlplane", "operation", "scheduler_reconcile", "error", err)
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
