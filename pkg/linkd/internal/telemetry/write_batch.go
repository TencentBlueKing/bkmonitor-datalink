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
}

// NewWriteBatchObserver 创建 Lifecycle 物理批次指标。
func (r *Runtime) NewWriteBatchObserver() (*WriteBatchObserver, error) {
	o := &WriteBatchObserver{}
	var e1, e2, e3, e4, e5, e6 error
	o.operations, e1 = r.meter.Int64Histogram("linkd.elasticsearch.write_batch.operations", metric.WithExplicitBucketBoundaries(1, 2, 4, 8, 16, 32, 64, 100, 128, 512, 1000))
	o.bytes, e2 = r.meter.Int64Histogram("linkd.elasticsearch.write_batch.size", metric.WithUnit("By"),
		metric.WithDescription("物理批次编码请求字节数"),
		metric.WithExplicitBucketBoundaries(1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216))
	durationBuckets := metric.WithExplicitBucketBoundaries(0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30)
	o.queue, e3 = r.meter.Float64Histogram("linkd.elasticsearch.write_batch.queue_duration", metric.WithUnit("s"),
		metric.WithDescription("每个物理批次首项从入队到请求开始的等待；包含聚合与执行槽位等待"), durationBuckets)
	o.duration, e4 = r.meter.Float64Histogram("linkd.elasticsearch.write_batch.duration", metric.WithUnit("s"),
		metric.WithDescription("物理批次请求执行耗时，包含网络与响应解码，不包含入队等待"), durationBuckets)
	o.items, e5 = r.meter.Int64Counter("linkd.elasticsearch.write_batch.items",
		metric.WithDescription("物理批次逐项结果；failed 包含传输错误导致的结果未知，不等同于写入一定未生效"))
	o.batches, e6 = r.meter.Int64Counter("linkd.elasticsearch.write_batch.batches",
		metric.WithDescription("已执行的物理批次尝试次数，按全部成功、部分失败或全部失败分类"))
	if err := errors.Join(e1, e2, e3, e4, e5, e6); err != nil {
		return nil, err
	}
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
