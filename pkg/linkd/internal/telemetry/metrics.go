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
	"fmt"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type instruments struct {
	pipelineAttempts        metric.Int64Counter
	pipelineAttemptDuration metric.Float64Histogram
	pipelineInflight        metric.Int64UpDownCounter
	pipelineQueueDelay      metric.Float64Histogram
	pipelineRetries         metric.Int64Counter

	messagingHandlerOutcomes metric.Int64Counter
	messagingReceived        metric.Int64Counter
	messagingReceivedBytes   metric.Int64Counter
	messagingRedelivered     metric.Int64Counter
	messagingInflight        metric.Int64Gauge
	messagingInflightSize    metric.Int64Gauge
	messagingRetryItems      metric.Int64Gauge
	messagingRetryOldestAge  metric.Float64Gauge
	messagingSettlements     metric.Int64Counter
	messagingSettledMessages metric.Int64Counter
	messagingSettleDuration  metric.Float64Histogram
	messagingSettlementGap   metric.Int64Gauge
	messagingGapOldestAge    metric.Float64Gauge
	messagingFlowTransitions metric.Int64Counter
	messagingShutdown        metric.Float64Histogram
	messagingRemaining       metric.Int64Histogram
	messagingLaneInflight    metric.Int64Gauge
	messagingLaneBytes       metric.Int64Gauge
	messagingLanePaused      metric.Int64Gauge
	messagingLaneOwned       metric.Int64Gauge

	cleanerStepItems               metric.Int64Counter
	cleanerStepDuration            metric.Float64Histogram
	cleanerFlowActive              metric.Int64Gauge
	cleanerBackpressureChecks      metric.Int64Counter
	cleanerBackpressureUnresolved  metric.Int64Gauge
	cleanerBackpressurePaused      metric.Int64Gauge
	cleanerBackpressureTransitions metric.Int64Counter

	lifecycleResults    metric.Int64Counter
	lifecycleMailboxOps metric.Int64Counter
	lifecycleDrained    metric.Int64Histogram
	lifecycleLeaseOps   metric.Int64Counter
	finalHookOperations metric.Int64Counter
	finalHookDuration   metric.Float64Histogram

	controlPlaneTaskActive      metric.Int64Gauge
	controlPlaneTaskRuns        metric.Int64Counter
	controlPlaneTaskRunDuration metric.Float64Histogram
	controlPlaneTaskLastSuccess metric.Int64Gauge
	elasticsearchScannedAlerts  metric.Int64Counter
	elasticsearchArchivedAlerts metric.Int64Counter
	elasticsearchFailedAlerts   metric.Int64Counter
	elasticsearchScanLast       metric.Int64Gauge
	elasticsearchArchiveLast    metric.Int64Gauge
	elasticsearchFailureLast    metric.Int64Gauge

	redisStreamExists               metric.Int64Gauge
	redisStreamExpectedGroupPresent metric.Int64Gauge
	redisStreamLength               metric.Int64Gauge
	redisStreamEntriesAdded         metric.Int64Gauge
	redisStreamMemory               metric.Int64Gauge
	redisStreamGroups               metric.Int64Gauge
	redisStreamConsumers            metric.Int64Gauge
	redisStreamPending              metric.Int64Gauge
	redisStreamMaxLag               metric.Int64Gauge
	redisStreamOldestEntryAge       metric.Float64Gauge
	redisStreamOldestPendingAge     metric.Float64Gauge
	redisStreamMaxEntries           metric.Int64Gauge
	redisStreamEntriesAboveMax      metric.Int64Gauge
	redisStreamReconcileOperations  metric.Int64Counter
	redisStreamReconcileDuration    metric.Float64Histogram
	redisStreamTrimmedEntries       metric.Int64Counter
	redisStreamTrimRequired         metric.Int64Gauge
	redisStreamTrimSafe             metric.Int64Gauge
	redisStreamTrimLastEntries      metric.Int64Gauge

	storeOperations        metric.Int64Counter
	storeOperationDuration metric.Float64Histogram
	storeIdempotencyReplay metric.Int64Counter
	storeIdentityConflicts metric.Int64Counter
	storeCASConflicts      metric.Int64Counter
}

func newInstruments(meter metric.Meter) (*instruments, error) {
	if meter == nil {
		return nil, fmt.Errorf("create telemetry instruments: meter must not be nil")
	}
	result := &instruments{}
	var err error
	if result.pipelineAttempts, err = meter.Int64Counter(
		"linkd.pipeline.attempts",
		metric.WithUnit("{attempt}"),
		metric.WithDescription("阶段处理尝试次数"),
	); err != nil {
		return nil, err
	}
	if result.pipelineAttemptDuration, err = meter.Float64Histogram(
		"linkd.pipeline.attempt.duration",
		metric.WithUnit("s"),
		metric.WithDescription("单条阶段处理尝试耗时"),
	); err != nil {
		return nil, err
	}
	if result.pipelineInflight, err = meter.Int64UpDownCounter(
		"linkd.pipeline.inflight",
		metric.WithUnit("{event}"),
		metric.WithDescription("正在执行的阶段处理数"),
	); err != nil {
		return nil, err
	}
	if result.pipelineQueueDelay, err = meter.Float64Histogram(
		"linkd.pipeline.queue.delay",
		metric.WithUnit("s"),
		metric.WithDescription("对象从可消费到开始处理的等待时间"),
	); err != nil {
		return nil, err
	}
	if result.pipelineRetries, err = meter.Int64Counter(
		"linkd.pipeline.retries",
		metric.WithUnit("{retry}"),
		metric.WithDescription("明确安排的阶段重试次数"),
	); err != nil {
		return nil, err
	}

	if result.messagingHandlerOutcomes, err = meter.Int64Counter(
		"linkd.messaging.handler.outcomes",
		metric.WithUnit("{message}"),
		metric.WithDescription("消息 Handler 结构化结果"),
	); err != nil {
		return nil, err
	}
	if result.messagingReceived, err = meter.Int64Counter(
		"linkd.messaging.received.messages", metric.WithUnit("{message}"),
		metric.WithDescription("已由运行时接管的消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingReceivedBytes, err = meter.Int64Counter(
		"linkd.messaging.received.bytes", metric.WithUnit("By"),
		metric.WithDescription("已由运行时接管的消息字节数"),
	); err != nil {
		return nil, err
	}
	if result.messagingRedelivered, err = meter.Int64Counter(
		"linkd.messaging.redelivered.messages", metric.WithUnit("{message}"),
		metric.WithDescription("Broker 标记为重投或重新接管的消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingInflight, err = meter.Int64Gauge(
		"linkd.messaging.inflight",
		metric.WithUnit("{message}"),
		metric.WithDescription("已接管但尚未确认终态的消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingInflightSize, err = meter.Int64Gauge(
		"linkd.messaging.inflight.size",
		metric.WithUnit("By"),
		metric.WithDescription("已接管但尚未确认终态的消息字节数"),
	); err != nil {
		return nil, err
	}
	if result.messagingRetryItems, err = meter.Int64Gauge(
		"linkd.messaging.retry.items",
		metric.WithUnit("{message}"),
		metric.WithDescription("进程内等待重试的消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingRetryOldestAge, err = meter.Float64Gauge(
		"linkd.messaging.retry.oldest.age",
		metric.WithUnit("s"),
		metric.WithDescription("进程内最老重试项年龄"),
	); err != nil {
		return nil, err
	}
	if result.messagingSettlements, err = meter.Int64Counter(
		"linkd.messaging.settlements",
		metric.WithUnit("{operation}"),
		metric.WithDescription("消息确认操作次数"),
	); err != nil {
		return nil, err
	}
	if result.messagingSettledMessages, err = meter.Int64Counter(
		"linkd.messaging.settled.messages", metric.WithUnit("{message}"),
		metric.WithDescription("消息确认尝试涉及的实际消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingSettleDuration, err = meter.Float64Histogram(
		"linkd.messaging.settlement.duration", metric.WithUnit("s"),
		metric.WithDescription("消息确认操作耗时"),
	); err != nil {
		return nil, err
	}
	if result.messagingSettlementGap, err = meter.Int64Gauge(
		"linkd.messaging.settlement.gap",
		metric.WithUnit("{message}"),
		metric.WithDescription("已完成但被更早消息阻塞的数量"),
	); err != nil {
		return nil, err
	}
	if result.messagingGapOldestAge, err = meter.Float64Gauge(
		"linkd.messaging.settlement.gap.oldest.age",
		metric.WithUnit("s"),
		metric.WithDescription("最老确认缺口年龄"),
	); err != nil {
		return nil, err
	}
	if result.messagingFlowTransitions, err = meter.Int64Counter(
		"linkd.messaging.flow.transitions",
		metric.WithUnit("{transition}"),
		metric.WithDescription("消息流暂停和恢复转换次数"),
	); err != nil {
		return nil, err
	}
	if result.messagingShutdown, err = meter.Float64Histogram(
		"linkd.messaging.shutdown.duration",
		metric.WithUnit("s"),
		metric.WithDescription("消息运行时停止耗时"),
	); err != nil {
		return nil, err
	}
	if result.messagingRemaining, err = meter.Int64Histogram(
		"linkd.messaging.shutdown.remaining",
		metric.WithUnit("{message}"),
		metric.WithDescription("停止时交回 Broker 的未确认消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingLaneInflight, err = meter.Int64Gauge(
		"linkd.messaging.lane.inflight", metric.WithUnit("{message}"),
		metric.WithDescription("活跃 lane 当前在途消息数"),
	); err != nil {
		return nil, err
	}
	if result.messagingLaneBytes, err = meter.Int64Gauge(
		"linkd.messaging.lane.inflight.bytes", metric.WithUnit("By"),
		metric.WithDescription("活跃 lane 当前在途消息字节数"),
	); err != nil {
		return nil, err
	}
	if result.messagingLanePaused, err = meter.Int64Gauge(
		"linkd.messaging.lane.paused", metric.WithUnit("1"),
		metric.WithDescription("活跃 lane 是否暂停"),
	); err != nil {
		return nil, err
	}
	if result.messagingLaneOwned, err = meter.Int64Gauge(
		"linkd.messaging.lane.owned", metric.WithUnit("1"),
		metric.WithDescription("当前进程是否拥有该 lane"),
	); err != nil {
		return nil, err
	}

	if result.cleanerStepItems, err = meter.Int64Counter(
		"linkd.cleaner.step.items", metric.WithUnit("{item}"),
		metric.WithDescription("Cleaner 各可靠性步骤处理结果"),
	); err != nil {
		return nil, err
	}
	if result.cleanerStepDuration, err = meter.Float64Histogram(
		"linkd.cleaner.step.duration", metric.WithUnit("s"),
		metric.WithDescription("Cleaner 各步骤批次耗时"),
	); err != nil {
		return nil, err
	}
	if result.cleanerFlowActive, err = meter.Int64Gauge(
		"linkd.cleaner.flow.active", metric.WithUnit("1"),
		metric.WithDescription("当前进程中 EventSource Cleaner Flow 是否活跃"),
	); err != nil {
		return nil, err
	}
	if result.cleanerBackpressureChecks, err = meter.Int64Counter(
		"linkd.cleaner.backpressure.checks", metric.WithUnit("{check}"),
		metric.WithDescription("Cleaner Signal 积压背压的 Redis 采样结果"),
	); err != nil {
		return nil, err
	}
	if result.cleanerBackpressureUnresolved, err = meter.Int64Gauge(
		"linkd.cleaner.backpressure.unresolved", metric.WithUnit("{signal}"),
		metric.WithDescription("目标 Lifecycle Consumer Group 最近采样的 lag 加 pending；-1 表示未知"),
	); err != nil {
		return nil, err
	}
	if result.cleanerBackpressurePaused, err = meter.Int64Gauge(
		"linkd.cleaner.backpressure.paused", metric.WithUnit("1"),
		metric.WithDescription("Cleaner 是否因 Signal 近似积压而暂停发起新 Receive"),
	); err != nil {
		return nil, err
	}
	if result.cleanerBackpressureTransitions, err = meter.Int64Counter(
		"linkd.cleaner.backpressure.transitions", metric.WithUnit("{transition}"),
		metric.WithDescription("Cleaner Signal 背压暂停和恢复转换次数"),
	); err != nil {
		return nil, err
	}

	if result.lifecycleResults, err = meter.Int64Counter(
		"linkd.lifecycle.result.items", metric.WithUnit("{event}"),
		metric.WithDescription("Lifecycle Event 裁决结果"),
	); err != nil {
		return nil, err
	}
	if result.lifecycleMailboxOps, err = meter.Int64Counter(
		"linkd.lifecycle.mailbox.operations", metric.WithUnit("{operation}"),
		metric.WithDescription("Lifecycle Mailbox 操作结果"),
	); err != nil {
		return nil, err
	}
	if result.lifecycleDrained, err = meter.Int64Histogram(
		"linkd.lifecycle.mailbox.drained.events", metric.WithUnit("{event}"),
		metric.WithDescription("单个 Signal 排空的 Event 数"),
	); err != nil {
		return nil, err
	}
	if result.lifecycleLeaseOps, err = meter.Int64Counter(
		"linkd.lifecycle.lease.operations", metric.WithUnit("{operation}"),
		metric.WithDescription("Lifecycle lease 操作结果"),
	); err != nil {
		return nil, err
	}
	if result.finalHookOperations, err = meter.Int64Counter(
		"linkd.final_hook.operations", metric.WithUnit("{operation}"),
		metric.WithDescription("Lifecycle FinalHook 调用结果"),
	); err != nil {
		return nil, err
	}
	if result.finalHookDuration, err = meter.Float64Histogram(
		"linkd.final_hook.duration", metric.WithUnit("s"),
		metric.WithDescription("Lifecycle FinalHook 调用耗时"),
	); err != nil {
		return nil, err
	}

	if result.controlPlaneTaskActive, err = meter.Int64Gauge(
		"linkd.control_plane.task.active", metric.WithUnit("1"),
		metric.WithDescription("当前进程是否正在运行指定控制面管理任务"),
	); err != nil {
		return nil, err
	}
	if result.controlPlaneTaskRuns, err = meter.Int64Counter(
		"linkd.control_plane.task.runs", metric.WithUnit("{run}"),
		metric.WithDescription("控制面管理任务单轮执行结果"),
	); err != nil {
		return nil, err
	}
	if result.controlPlaneTaskRunDuration, err = meter.Float64Histogram(
		"linkd.control_plane.task.run.duration", metric.WithUnit("s"),
		metric.WithDescription("控制面管理任务单轮执行耗时"),
	); err != nil {
		return nil, err
	}
	if result.controlPlaneTaskLastSuccess, err = meter.Int64Gauge(
		"linkd.control_plane.task.last_success", metric.WithUnit("s"),
		metric.WithDescription("控制面管理任务最近成功完成时间的 Unix 秒"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchArchivedAlerts, err = meter.Int64Counter(
		"linkd.elasticsearch_alert_archiver.archived_alerts", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 已完成归档的 Alert 数"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchScannedAlerts, err = meter.Int64Counter(
		"linkd.elasticsearch_alert_archiver.scanned_alerts", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 已扫描的终态 Alert 数"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchFailedAlerts, err = meter.Int64Counter(
		"linkd.elasticsearch_alert_archiver.failed_alerts", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 未完成归档的 Alert 数"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchScanLast, err = meter.Int64Gauge(
		"linkd.elasticsearch_alert_archiver.last_batch_scanned", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 最近一批扫描的 Alert 数"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchArchiveLast, err = meter.Int64Gauge(
		"linkd.elasticsearch_alert_archiver.last_batch_items", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 最近一轮已归档 Alert 数"),
	); err != nil {
		return nil, err
	}
	if result.elasticsearchFailureLast, err = meter.Int64Gauge(
		"linkd.elasticsearch_alert_archiver.last_batch_failed", metric.WithUnit("{alert}"),
		metric.WithDescription("Elasticsearch Alert Archiver 最近一批未完成归档的 Alert 数"),
	); err != nil {
		return nil, err
	}

	if result.redisStreamExists, err = meter.Int64Gauge(
		"linkd.redis_stream.exists", metric.WithUnit("1"),
		metric.WithDescription("受管 Redis Stream 是否存在"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamExpectedGroupPresent, err = meter.Int64Gauge(
		"linkd.redis_stream.expected_group.present", metric.WithUnit("1"),
		metric.WithDescription("配置的 Consumer Group 是否存在"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamLength, err = meter.Int64Gauge(
		"linkd.redis_stream.entries", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 当前条目数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamEntriesAdded, err = meter.Int64Gauge(
		"linkd.redis_stream.entries_added", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 自创建以来累计添加条目数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamMemory, err = meter.Int64Gauge(
		"linkd.redis_stream.memory", metric.WithUnit("By"),
		metric.WithDescription("受管 Redis Stream 当前 Redis 内存占用"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamGroups, err = meter.Int64Gauge(
		"linkd.redis_stream.consumer_groups", metric.WithUnit("{group}"),
		metric.WithDescription("受管 Redis Stream 的 Consumer Group 数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamConsumers, err = meter.Int64Gauge(
		"linkd.redis_stream.consumers", metric.WithUnit("{consumer}"),
		metric.WithDescription("受管 Redis Stream 所有 Consumer Group 的 Consumer 总数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamPending, err = meter.Int64Gauge(
		"linkd.redis_stream.pending", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 所有 Consumer Group 的 Pending 总数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamMaxLag, err = meter.Int64Gauge(
		"linkd.redis_stream.consumer_group.max_lag", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 各 Consumer Group 中最大的未投递条目数；-1 表示 Redis 无法计算"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamOldestEntryAge, err = meter.Float64Gauge(
		"linkd.redis_stream.oldest_entry.age", metric.WithUnit("s"),
		metric.WithDescription("受管 Redis Stream 最老条目年龄"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamOldestPendingAge, err = meter.Float64Gauge(
		"linkd.redis_stream.oldest_pending.age", metric.WithUnit("s"),
		metric.WithDescription("受管 Redis Stream 所有 Consumer Group 中最老 Pending 条目年龄"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamMaxEntries, err = meter.Int64Gauge(
		"linkd.redis_stream.max_entries", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 配置的软长度上限"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamEntriesAboveMax, err = meter.Int64Gauge(
		"linkd.redis_stream.entries_above_max", metric.WithUnit("{entry}"),
		metric.WithDescription("受管 Redis Stream 超出软上限的条目数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamReconcileOperations, err = meter.Int64Counter(
		"linkd.redis_stream.reconcile.operations", metric.WithUnit("{operation}"),
		metric.WithDescription("Redis Stream 指标采集和安全裁剪轮次"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamReconcileDuration, err = meter.Float64Histogram(
		"linkd.redis_stream.reconcile.duration", metric.WithUnit("s"),
		metric.WithDescription("Redis Stream 指标采集和安全裁剪耗时"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamTrimmedEntries, err = meter.Int64Counter(
		"linkd.redis_stream.trimmed.entries", metric.WithUnit("{entry}"),
		metric.WithDescription("Redis Stream 已安全裁剪条目数"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamTrimRequired, err = meter.Int64Gauge(
		"linkd.redis_stream.trim.required", metric.WithUnit("1"),
		metric.WithDescription("受管 Redis Stream 当前是否超过软长度上限"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamTrimSafe, err = meter.Int64Gauge(
		"linkd.redis_stream.trim.safe", metric.WithUnit("1"),
		metric.WithDescription("受管 Redis Stream 当前是否存在不会删除未确认消息的裁剪边界"),
	); err != nil {
		return nil, err
	}
	if result.redisStreamTrimLastEntries, err = meter.Int64Gauge(
		"linkd.redis_stream.trim.last_entries", metric.WithUnit("{entry}"),
		metric.WithDescription("Redis Stream 管理任务最近一轮实际裁剪条目数"),
	); err != nil {
		return nil, err
	}

	if result.storeOperations, err = meter.Int64Counter(
		"linkd.store.operations",
		metric.WithUnit("{operation}"),
		metric.WithDescription("Repository 逻辑操作次数"),
	); err != nil {
		return nil, err
	}
	if result.storeOperationDuration, err = meter.Float64Histogram(
		"linkd.store.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Repository 逻辑操作耗时"),
	); err != nil {
		return nil, err
	}
	if result.storeIdempotencyReplay, err = meter.Int64Counter(
		"linkd.store.idempotency.replays",
		metric.WithUnit("{event}"),
		metric.WithDescription("存储幂等重放次数"),
	); err != nil {
		return nil, err
	}
	if result.storeIdentityConflicts, err = meter.Int64Counter(
		"linkd.store.identity.conflicts",
		metric.WithUnit("{conflict}"),
		metric.WithDescription("存储身份冲突次数"),
	); err != nil {
		return nil, err
	}
	if result.storeCASConflicts, err = meter.Int64Counter(
		"linkd.store.cas.conflicts",
		metric.WithUnit("{conflict}"),
		metric.WithDescription("存储 CAS 冲突次数"),
	); err != nil {
		return nil, err
	}
	return result, nil
}

func metricViews() []sdkmetric.View {
	return []sdkmetric.View{
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.pipeline.attempt.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				// Elasticsearch refresh=wait_for 会让 Lifecycle 耗时集中在 1 秒附近；
				// 加密该区间可避免 P95/P99 在 1～2.5 秒宽桶内产生过大的插值误差。
				Boundaries: []float64{
					0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
					0.75, 0.9, 1, 1.1, 1.25, 1.5, 2, 2.5, 5, 10, 30,
				},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.pipeline.queue.delay"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.01, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 900, 3600},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.store.operation.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{
					0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5,
					0.75, 0.9, 1, 1.1, 1.25, 1.5, 2, 2.5, 5, 10,
				},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.messaging.shutdown.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.01, 0.1, 0.5, 1, 5, 10, 30, 60},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.messaging.settlement.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.cleaner.step.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5, 10},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.final_hook.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.control_plane.task.run.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			}},
		),
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "linkd.redis_stream.reconcile.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			}},
		),
	}
}
