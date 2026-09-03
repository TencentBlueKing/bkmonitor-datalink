// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveCleanerBackpressureCheck 记录一次进程共享 Signal 背压采样。
// 指标不携带 tenant、MailboxID、EventID 或 EventSourceID。
func (r *Runtime) ObserveCleanerBackpressureCheck(
	ctx context.Context,
	outcome string,
	unresolved int64,
	paused bool,
) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.cleanerBackpressureChecks.Add(ctx, 1, metric.WithAttributes(
		attribute.String("linkd.outcome", outcome),
	))
	r.metrics.cleanerBackpressureUnresolved.Record(ctx, unresolved)
	pausedValue := int64(0)
	if paused {
		pausedValue = 1
	}
	r.metrics.cleanerBackpressurePaused.Record(ctx, pausedValue)
}

// ObserveCleanerBackpressureTransition 记录一次进程共享背压暂停或恢复转换。
func (r *Runtime) ObserveCleanerBackpressureTransition(ctx context.Context, action string) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.cleanerBackpressureTransitions.Add(ctx, 1, metric.WithAttributes(
		attribute.String("linkd.action", action),
	))
}
