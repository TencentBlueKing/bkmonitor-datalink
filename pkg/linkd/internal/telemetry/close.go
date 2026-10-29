// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveClosePhase 记录关闭链路的固定阶段，不带租户、告警或操作身份标签。
func (r *Runtime) ObserveClosePhase(ctx context.Context, phase string, duration time.Duration) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.closePhase.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("linkd.phase", phase)))
}
