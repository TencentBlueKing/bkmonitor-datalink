// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveActiveAlertCache 只使用低基数操作和结果，不把租户、策略或 fingerprint 放入指标标签。
func (r *Runtime) ObserveActiveAlertCache(ctx context.Context, operation, outcome string, n int) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.activeAlertCache.Add(ctx, int64(n), metric.WithAttributes(attribute.String("linkd.operation", operation), attribute.String("linkd.outcome", outcome)))
}
