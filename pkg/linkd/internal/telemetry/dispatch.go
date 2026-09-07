// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/taskdispatch/observation"
)

type dispatchInstruments struct {
	operations        metric.Int64Counter
	operationDuration metric.Float64Histogram
	transitions       metric.Int64Counter
	handoffDuration   metric.Float64Histogram
	controllerTasks   metric.Int64Gauge
	controllerWorkers metric.Int64Gauge
	replicas          metric.Int64Gauge
	metadataSources   metric.Int64Gauge
	metadataAge       metric.Float64Gauge
	workerTasks       metric.Int64Gauge
	partitions        metric.Int64Gauge
	paused            metric.Int64Gauge
	failures          metric.Int64Gauge
	heartbeatAge      metric.Float64Gauge
	authorization     metric.Float64Gauge
}

func newDispatchInstruments(meter metric.Meter) (*dispatchInstruments, error) {
	result := &dispatchInstruments{}
	var err error
	if result.operations, err = meter.Int64Counter("linkd.dispatch.operations", metric.WithUnit("{operation}"), metric.WithDescription("调度协议操作次数")); err != nil {
		return nil, err
	}
	if result.operationDuration, err = meter.Float64Histogram("linkd.dispatch.operation.duration", metric.WithUnit("s"), metric.WithDescription("调度协议操作耗时"), metric.WithExplicitBucketBoundaries(0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 90, 120)); err != nil {
		return nil, err
	}
	if result.transitions, err = meter.Int64Counter("linkd.dispatch.transitions", metric.WithUnit("{transition}"), metric.WithDescription("已提交的中心状态或 worker 本地状态转换次数")); err != nil {
		return nil, err
	}
	if result.handoffDuration, err = meter.Float64Histogram("linkd.dispatch.handoff.duration", metric.WithUnit("s"), metric.WithDescription("中心停止确认或 worker 本地停止耗时"), metric.WithExplicitBucketBoundaries(0.01, 0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 90, 120)); err != nil {
		return nil, err
	}
	if result.controllerTasks, err = meter.Int64Gauge("linkd.dispatch.controller.tasks", metric.WithUnit("{task}"), metric.WithDescription("中心任务数量，按角色和阶段聚合")); err != nil {
		return nil, err
	}
	if result.controllerWorkers, err = meter.Int64Gauge("linkd.dispatch.controller.workers", metric.WithUnit("{worker}"), metric.WithDescription("中心会话数，按健康状态聚合")); err != nil {
		return nil, err
	}
	if result.replicas, err = meter.Int64Gauge("linkd.dispatch.controller.replicas", metric.WithUnit("{task}"), metric.WithDescription("匹配、目标、运行和缺额的来源角色数量之和")); err != nil {
		return nil, err
	}
	if result.metadataSources, err = meter.Int64Gauge("linkd.dispatch.kafka.sources", metric.WithUnit("{source}"), metric.WithDescription("Kafka 元数据来源数，按 ready/waiting/error 聚合")); err != nil {
		return nil, err
	}
	if result.metadataAge, err = meter.Float64Gauge("linkd.dispatch.kafka.metadata.age", metric.WithUnit("s"), metric.WithDescription("Kafka 最近成功结果的最大年龄，尚无成功结果为 -1")); err != nil {
		return nil, err
	}
	if result.workerTasks, err = meter.Int64Gauge("linkd.dispatch.worker.tasks", metric.WithUnit("{task}"), metric.WithDescription("本进程任务数，按角色和阶段聚合")); err != nil {
		return nil, err
	}
	if result.partitions, err = meter.Int64Gauge("linkd.dispatch.worker.partitions", metric.WithUnit("{partition}"), metric.WithDescription("本进程活动 Cleaner 最近报告的 partition 分配数之和")); err != nil {
		return nil, err
	}
	if result.paused, err = meter.Int64Gauge("linkd.dispatch.worker.admission.paused", metric.WithUnit("{task}"), metric.WithDescription("本进程暂停接收新消息的活动任务数")); err != nil {
		return nil, err
	}
	if result.failures, err = meter.Int64Gauge("linkd.dispatch.worker.heartbeat.failures", metric.WithUnit("{failure}"), metric.WithDescription("连续心跳失败次数")); err != nil {
		return nil, err
	}
	if result.heartbeatAge, err = meter.Float64Gauge("linkd.dispatch.worker.heartbeat.age", metric.WithUnit("s"), metric.WithDescription("距最近成功心跳的时间；首次成功前从 agent 启动计时")); err != nil {
		return nil, err
	}
	if result.authorization, err = meter.Float64Gauge("linkd.dispatch.worker.authorization.remaining", metric.WithUnit("s"), metric.WithDescription("活动任务本地安全截止时间的最小剩余秒数；无任务为 0")); err != nil {
		return nil, err
	}
	return result, nil
}

type dispatchObserver struct{ metrics *dispatchInstruments }

// DispatchObserver 使用低基数聚合观测调度协议；具体来源/owner/epoch 从运行状态 API 查询。
// 每次快照覆盖全部角色和阶段（包括 0），避免任务停止后保留旧 Gauge。
func (r *Runtime) DispatchObserver() observation.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &dispatchObserver{metrics: r.metrics.dispatch}
}

func (o *dispatchObserver) Operation(ctx context.Context, operation string, success bool, duration time.Duration) {
	outcome := "failed"
	if success {
		outcome = "succeeded"
	}
	attrs := metric.WithAttributes(attribute.String("linkd.operation", operation), attribute.String("linkd.outcome", outcome))
	o.metrics.operations.Add(ctx, 1, attrs)
	o.metrics.operationDuration.Record(ctx, duration.Seconds(), attrs)
}

func (o *dispatchObserver) Transition(ctx context.Context, side, role, from, to, reason string, duration time.Duration) {
	if from == "" {
		from = "none"
	}
	attrs := metric.WithAttributes(attribute.String("linkd.dispatch.side", side), attribute.String("linkd.task.role", role), attribute.String("linkd.task.from", from), attribute.String("linkd.task.phase", to), attribute.String("linkd.reason", reason))
	o.metrics.transitions.Add(ctx, 1, attrs)
	if to == "stopped" && duration > 0 {
		o.metrics.handoffDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("linkd.dispatch.side", side), attribute.String("linkd.task.role", role)))
	}
}

func recordDispatchTasks(ctx context.Context, gauge metric.Int64Gauge, counts map[string]map[string]int64) {
	for _, role := range []string{"cleaner", "lifecycle"} {
		for _, phase := range []string{"preparing", "prepared", "starting", "running", "stopping", "stopped"} {
			gauge.Record(ctx, counts[role][phase], metric.WithAttributes(attribute.String("linkd.task.role", role), attribute.String("linkd.task.phase", phase)))
		}
	}
}

func (o *dispatchObserver) ControllerSnapshot(ctx context.Context, state observation.ControllerObservation) {
	recordDispatchTasks(ctx, o.metrics.controllerTasks, state.Tasks)
	for _, health := range []string{"healthy", "stale", "draining", "cooldown"} {
		o.metrics.controllerWorkers.Record(ctx, state.Workers[health], metric.WithAttributes(attribute.String("linkd.worker.state", health)))
	}
	for _, role := range []string{"cleaner", "lifecycle"} {
		for _, kind := range []string{"matching", "target", "running", "shortage"} {
			o.metrics.replicas.Record(ctx, state.Replicas[role][kind], metric.WithAttributes(attribute.String("linkd.task.role", role), attribute.String("linkd.replica.kind", kind)))
		}
	}
	for _, health := range []string{"ready", "waiting", "error"} {
		o.metrics.metadataSources.Record(ctx, state.Metadata[health], metric.WithAttributes(attribute.String("linkd.metadata.state", health)))
	}
	o.metrics.metadataAge.Record(ctx, state.MetadataAge.Seconds())
}

func (o *dispatchObserver) WorkerSnapshot(ctx context.Context, state observation.WorkerObservation) {
	recordDispatchTasks(ctx, o.metrics.workerTasks, state.Tasks)
	o.metrics.partitions.Record(ctx, state.Partitions)
	o.metrics.paused.Record(ctx, state.Paused)
	o.metrics.failures.Record(ctx, int64(state.Failures))
	o.metrics.heartbeatAge.Record(ctx, max(0, state.HeartbeatAge.Seconds()))
	o.metrics.authorization.Record(ctx, max(0, state.AuthorizationRemaining.Seconds()))
}

var _ observation.Observer = (*dispatchObserver)(nil)
