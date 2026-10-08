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
)

// ControlPlaneTask 是允许进入指标属性的控制面任务固定枚举。
type ControlPlaneTask string

const (
	// ControlPlaneTaskKACIndexMaintenance 维护兼容索引并观测定义冲突或依赖故障。
	ControlPlaneTaskKACIndexMaintenance ControlPlaneTask = "kac-index-maintenance"
	// ControlPlaneTaskKACDelivery 监督可靠投影与动作循环的共享运行资源。
	ControlPlaneTaskKACDelivery ControlPlaneTask = "kac-delivery"
	// ControlPlaneTaskProjectionProducer 从持久 Alert 水位补齐投影任务。
	ControlPlaneTaskProjectionProducer ControlPlaneTask = "projection-producer"
	// ControlPlaneTaskProjectionDelivery 执行可靠投影及本地 ACK。
	ControlPlaneTaskProjectionDelivery ControlPlaneTask = "projection-delivery"
	// ControlPlaneTaskActionEnqueue 在正式指纹租约内补齐已持久化动作意图。
	ControlPlaneTaskActionEnqueue ControlPlaneTask = "action-enqueue"
	// ControlPlaneTaskActionDelivery 执行投影门槛、原顺序动作发送与受理确认。
	ControlPlaneTaskActionDelivery ControlPlaneTask = "action-delivery"
	// ControlPlaneTaskShieldHints 记录可丢失的依赖主终态提示与加速复查。
	ControlPlaneTaskShieldHints ControlPlaneTask = "shield-hints"
	// ControlPlaneTaskMergeRequests 记录显式合并接续和关系检查，不把业务条件失败等同于任务故障。
	ControlPlaneTaskMergeRequests ControlPlaneTask = "merge-requests"
	// ControlPlaneTaskSuppressionRequests 记录显式抑制对账请求的有界执行。
	ControlPlaneTaskSuppressionRequests ControlPlaneTask = "suppression-requests"
	// ControlPlaneTaskShieldRequests 记录显式屏蔽复查请求的执行轮次。
	ControlPlaneTaskShieldRequests ControlPlaneTask = "shield-requests"
	// ControlPlaneTaskMerge 记录共享合并任务运行时是否存活。
	ControlPlaneTaskMerge ControlPlaneTask = "merge"
	// ControlPlaneTaskMergeJudge 记录独立合并窗口裁决及丢失等待检查。
	ControlPlaneTaskMergeJudge ControlPlaneTask = "merge-judge"
	// ControlPlaneTaskMergeDecisions 记录持久化合并步骤与缓存提示清理。
	ControlPlaneTaskMergeDecisions ControlPlaneTask = "merge-decisions"
	// ControlPlaneTaskMergeRelations 记录父子终态检查和关系解除。
	ControlPlaneTaskMergeRelations ControlPlaneTask = "merge-relations"
	// ControlPlaneTaskShieldCheck 记录独立屏蔽解除检查轮次。
	ControlPlaneTaskShieldCheck ControlPlaneTask = "shield-check"
	// ControlPlaneTaskPolicyPublication 记录策略待发布记录恢复轮次。
	ControlPlaneTaskPolicyPublication ControlPlaneTask = "policy-publication"
	// ControlPlaneTaskScheduler 记录调度轮次。
	ControlPlaneTaskScheduler ControlPlaneTask = "scheduler"
	// ControlPlaneTaskProviders 记录来源提供方完整应用轮次。
	ControlPlaneTaskProviders ControlPlaneTask = "source-providers"
	// ControlPlaneTaskActiveIndexes 记录策略索引目标发现轮次。
	ControlPlaneTaskActiveIndexes ControlPlaneTask = "active-alert-indexes"
	// ControlPlaneTaskDynamicConfig 记录配置同步轮次。
	ControlPlaneTaskDynamicConfig ControlPlaneTask = "dynamic-config"
	// ControlPlaneTaskElasticsearchSchemaAndActiveReconciler 对账 Schema 与 Active Alert 资源。
	ControlPlaneTaskElasticsearchSchemaAndActiveReconciler ControlPlaneTask = "elasticsearch-schema-and-active-reconciler"
	// ControlPlaneTaskElasticsearchBucketManager 维护当前预创建窗口内的时间桶。
	ControlPlaneTaskElasticsearchBucketManager ControlPlaneTask = "elasticsearch-bucket-manager"
	// ControlPlaneTaskElasticsearchAlertArchiver 异步归档终态 Alert。
	ControlPlaneTaskElasticsearchAlertArchiver ControlPlaneTask = "elasticsearch-alert-archiver"
	// ControlPlaneTaskRedisStreamManager 采集并安全裁剪 Lifecycle Signal Stream。
	ControlPlaneTaskRedisStreamManager ControlPlaneTask = "redis-stream-manager"
)

// ControlPlaneTaskObserver 记录单个固定控制面任务的存活与单轮执行结果。
// 任务错误只决定 outcome，不会被指标层吞掉或改写。
type ControlPlaneTaskObserver struct {
	metrics *instruments
	task    ControlPlaneTask
}

// ControlPlaneTaskObserver 返回固定任务的低基数观察器。
func (r *Runtime) ControlPlaneTaskObserver(task ControlPlaneTask) *ControlPlaneTaskObserver {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &ControlPlaneTaskObserver{metrics: r.metrics, task: task}
}

// SetActive 记录当前进程是否正在监督该任务。
func (o *ControlPlaneTaskObserver) SetActive(ctx context.Context, active bool) {
	if o == nil || o.metrics == nil {
		return
	}
	o.metrics.controlPlaneTaskActive.Record(ctx, boolInt64(active), o.attributes())
}

// RunFinished 记录一轮任务的结果和耗时。
func (o *ControlPlaneTaskObserver) RunFinished(ctx context.Context, duration time.Duration, succeeded bool) {
	if o == nil || o.metrics == nil {
		return
	}
	outcome := "succeeded"
	if !succeeded {
		outcome = "failed"
	}
	attributes := metric.WithAttributes(
		attribute.String("linkd.task", string(o.task)),
		attribute.String("linkd.outcome", outcome),
	)
	o.metrics.controlPlaneTaskRuns.Add(ctx, 1, attributes)
	o.metrics.controlPlaneTaskRunDuration.Record(ctx, duration.Seconds(), attributes)
	if succeeded {
		o.metrics.controlPlaneTaskLastSuccess.Record(ctx, time.Now().Unix(), o.attributes())
	}
}

// ObserveElasticsearchArchiveBatch 记录 Archiver 一批的扫描、成功和隔离失败工作量。
func (r *Runtime) ObserveElasticsearchArchiveBatch(ctx context.Context, scanned, archived, failed int) {
	if r == nil || r.metrics == nil {
		return
	}
	scannedValue, archivedValue, failedValue := int64(scanned), int64(archived), int64(failed)
	// 连续归档在清空积压后会立即执行一次空扫描；空结果不应覆盖最近一次真实工作批次，
	// 否则 Console 在任务空闲时只能看到 0，无法判断上一批的处理效果。
	if scannedValue == 0 {
		return
	}
	r.metrics.elasticsearchScanLast.Record(ctx, scannedValue)
	r.metrics.elasticsearchArchiveLast.Record(ctx, archivedValue)
	r.metrics.elasticsearchFailureLast.Record(ctx, failedValue)
	r.metrics.elasticsearchScannedAlerts.Add(ctx, scannedValue)
	if archivedValue > 0 {
		r.metrics.elasticsearchArchivedAlerts.Add(ctx, archivedValue)
	}
	if failedValue > 0 {
		r.metrics.elasticsearchFailedAlerts.Add(ctx, failedValue)
	}
}

func (o *ControlPlaneTaskObserver) attributes() metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("linkd.task", string(o.task)))
}
