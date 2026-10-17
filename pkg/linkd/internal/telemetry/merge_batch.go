// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveMergeBatchPhase 的 batch.* 是批次墙钟；member.* 是并行成员样本，不能相加。
func (r *Runtime) ObserveMergeBatchPhase(ctx context.Context, phase string, duration time.Duration) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.mergeBatchPhase.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("linkd.phase", phase)))
}

// ObserveMergePoolWait 记录进程连接池累计等待。并发批次重复采样不能相加，应取差值。
func (r *Runtime) ObserveMergePoolWait(ctx context.Context, pool string, count int64, duration time.Duration) {
	if r == nil || r.metrics == nil {
		return
	}
	a := metric.WithAttributes(attribute.String("linkd.pool", pool))
	r.metrics.mergePoolWaits.Record(ctx, count, a)
	r.metrics.mergePoolWaitDuration.Record(ctx, duration.Seconds(), a)
}
