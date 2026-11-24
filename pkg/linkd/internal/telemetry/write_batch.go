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
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// WriteBatchObserver 记录实际 _bulk/_mget 请求及操作量，避免把逻辑调用误计为物理请求。
type WriteBatchObserver struct {
	operations metric.Int64Histogram
	bytes      metric.Int64Histogram
	queue      metric.Float64Histogram
	duration   metric.Float64Histogram
	items      metric.Int64Counter
	batches    metric.Int64Counter
	phases     metric.Float64Histogram
	triggers   metric.Int64Counter
}

// 批次 instrument 在启动时统一注册；只有启用观察器后才产生零值样本。
func newWriteBatchInstruments(meter *instrumentRegistry) (*WriteBatchObserver, error) {
	o := &WriteBatchObserver{}
	var e1, e2, e3, e4, e5, e6, e7, e8 error
	o.operations, e1 = meter.Int64Histogram("linkd.elasticsearch.write_batch.operations", describeMetric("物理批次操作数量", "elasticsearch", "throughput", "linkd.batch_kind"), metric.WithUnit("{operation}"), metric.WithDescription("每个物理 _bulk/_mget 请求包含的操作数"), metric.WithExplicitBucketBoundaries(1, 2, 4, 8, 16, 32, 64, 100, 128, 512, 1000))
	o.bytes, e2 = meter.Int64Histogram("linkd.elasticsearch.write_batch.size", describeMetric("物理批次请求大小", "elasticsearch", "capacity", "linkd.batch_kind"), metric.WithUnit("By"),
		metric.WithDescription("物理批次编码请求字节数"),
		metric.WithExplicitBucketBoundaries(1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216))
	durationBuckets := metric.WithExplicitBucketBoundaries(0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30)
	o.queue, e3 = meter.Float64Histogram("linkd.elasticsearch.write_batch.queue_duration", describeMetric("物理批次排队耗时", "elasticsearch", "latency", "linkd.batch_kind", "linkd.metric_schema"), metric.WithUnit("s"),
		metric.WithDescription("每个物理批次首项从入队到请求开始的等待；包含聚合与执行槽位等待"), durationBuckets)
	o.duration, e4 = meter.Float64Histogram("linkd.elasticsearch.write_batch.duration", describeMetric("物理批次请求耗时", "elasticsearch", "latency", "linkd.batch_kind", "linkd.metric_schema"), metric.WithUnit("s"),
		metric.WithDescription("物理批次请求执行耗时，包含网络与响应解码，不包含入队等待"), durationBuckets)
	o.items, e5 = meter.Int64Counter("linkd.elasticsearch.write_batch.items", describeMetric("物理批次逐项结果", "elasticsearch", "throughput", "linkd.batch_kind", "linkd.outcome"), metric.WithUnit("{item}"),
		metric.WithDescription("物理批次逐项结果；failed 包含传输错误导致的结果未知，不等同于写入一定未生效"))
	o.batches, e6 = meter.Int64Counter("linkd.elasticsearch.write_batch.batches", describeMetric("物理批次执行结果", "elasticsearch", "throughput", "linkd.batch_kind", "linkd.outcome"), metric.WithUnit("{batch}"),
		metric.WithDescription("已执行的物理批次尝试次数，按全部成功、部分失败或全部失败分类"))
	o.phases, e7 = meter.Float64Histogram("linkd.elasticsearch.write_batch.phase_duration", describeMetric("批次诊断阶段耗时", "elasticsearch", "latency", "linkd.batch_kind", "linkd.batch_phase"), metric.WithUnit("s"),
		metric.WithDescription("批次诊断阶段耗时；各阶段样本口径不同，不可直接相加；HTTP 阶段不是 ES 服务端耗时"), durationBuckets)
	o.triggers, e8 = meter.Int64Counter("linkd.elasticsearch.write_batch.triggers", describeMetric("批次提交触发原因", "elasticsearch", "state", "linkd.batch_kind", "linkd.batch_trigger"), metric.WithUnit("{transition}"),
		metric.WithDescription("聚合器提交次数及触发原因；提交后可能因编码字节限制切成多个物理批次"))
	if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8); err != nil {
		return nil, err
	}
	return o, nil
}

// NewWriteBatchObserver 创建 Lifecycle 物理批次观察器，并初始化已接入批次的零值。
func (r *Runtime) NewWriteBatchObserver() (*WriteBatchObserver, error) {
	if r == nil || r.metrics == nil {
		return nil, fmt.Errorf("write batch observer requires telemetry runtime")
	}
	o := r.metrics.writeBatch
	// 显式区分已接入但空闲的零值与未启用/未上报指标。
	for _, kind := range []string{"read", "write"} {
		for _, outcome := range []string{"succeeded", "partial_failed", "failed"} {
			o.batches.Add(context.Background(), 0, metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.outcome", outcome)))
		}
		for _, outcome := range []string{"succeeded", "failed"} {
			o.items.Add(context.Background(), 0, metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.outcome", outcome)))
		}
	}
	return o, nil
}

// BatchPhase 使用固定阶段名记录诊断耗时，不包含文档、租户或请求身份标签。
func (o *WriteBatchObserver) BatchPhase(ctx context.Context, kind, phase string, duration time.Duration) {
	o.phases.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.batch_phase", phase)))
}

// BatchTriggered 记录聚合器触发原因，不计为已执行的物理请求。
func (o *WriteBatchObserver) BatchTriggered(ctx context.Context, kind, reason string) {
	o.triggers.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.batch_trigger", reason)))
}

// BatchFinished 记录逐项成功/失败及批次耗时，kind 只允许 read/write。
func (o *WriteBatchObserver) BatchFinished(ctx context.Context, kind string, count, size int, wait, duration time.Duration, failed int) {
	a := metric.WithAttributes(attribute.String("linkd.batch_kind", kind))
	o.operations.Record(ctx, int64(count), a)
	o.bytes.Record(ctx, int64(size), a)
	// 固定 schema 标签防止历史窗口混算默认秒级桶与新的毫秒级桶。
	timing := metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.metric_schema", "2"))
	o.queue.Record(ctx, wait.Seconds(), timing)
	o.duration.Record(ctx, duration.Seconds(), timing)
	outcome := "succeeded"
	if failed > 0 {
		outcome = "partial_failed"
		if failed == count {
			outcome = "failed"
		}
	}
	o.batches.Add(ctx, 1, metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.outcome", outcome)))
	for outcome, n := range map[string]int{"succeeded": count - failed, "failed": failed} {
		o.items.Add(ctx, int64(n), metric.WithAttributes(attribute.String("linkd.batch_kind", kind), attribute.String("linkd.outcome", outcome)))
	}
}
