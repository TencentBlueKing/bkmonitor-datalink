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
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
)

type lifecycleSchedulerObserver struct{ metrics *instruments }

// LifecycleSchedulerObserver 创建 Lifecycle Scheduler 的低基数观察器。
func (r *Runtime) LifecycleSchedulerObserver() scheduler.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &lifecycleSchedulerObserver{metrics: r.metrics}
}

func (o *lifecycleSchedulerObserver) LeaseOperation(ctx context.Context, operation, outcome string) {
	o.metrics.lifecycleLeaseOps.Add(ctx, 1, metric.WithAttributes(
		attribute.String("linkd.operation", operation),
		attribute.String("linkd.outcome", outcome),
	))
}

func (o *lifecycleSchedulerObserver) MailboxOperation(
	ctx context.Context,
	eventSourceID, operation, outcome string,
) {
	attributes := []attribute.KeyValue{
		attribute.String("linkd.operation", operation),
		attribute.String("linkd.outcome", outcome),
	}
	if eventSourceID != "" {
		attributes = append(attributes, attribute.String("linkd.event_source_id", eventSourceID))
	}
	o.metrics.lifecycleMailboxOps.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (o *lifecycleSchedulerObserver) EventProcessed(
	ctx context.Context,
	eventSourceID string,
	action domain.EventAction,
	result lifecycle.ProcessResult,
	err error,
) {
	outcome := string(result.Outcome)
	if err != nil || outcome == "" {
		outcome = "failed"
	}
	attributes := []attribute.KeyValue{
		attribute.String("linkd.event_source_id", eventSourceID),
		attribute.String("linkd.event.action", string(action)),
		attribute.String("linkd.event.state", string(result.EventState)),
		attribute.String("linkd.outcome", outcome),
	}
	if reason := lifecycleReason(result.ReasonCode); reason != "" {
		attributes = append(attributes, attribute.String("linkd.reason_code", reason))
	}
	o.metrics.lifecycleResults.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (o *lifecycleSchedulerObserver) MailboxDrained(
	ctx context.Context,
	eventSourceID, outcome string,
	events int,
) {
	attributes := []attribute.KeyValue{attribute.String("linkd.outcome", outcome)}
	if eventSourceID != "" {
		attributes = append(attributes, attribute.String("linkd.event_source_id", eventSourceID))
	}
	o.metrics.lifecycleDrained.Record(ctx, int64(events), metric.WithAttributes(attributes...))
}

type observedFinalHook struct {
	next    lifecycle.FinalHook
	metrics *instruments
}

// ObserveFinalHook 为 FinalHook 增加不改变返回值和失败语义的指标。
func (r *Runtime) ObserveFinalHook(next lifecycle.FinalHook) lifecycle.FinalHook {
	if r == nil || r.metrics == nil || next == nil {
		return next
	}
	return &observedFinalHook{next: next, metrics: r.metrics}
}

func (h *observedFinalHook) Execute(
	ctx context.Context,
	input lifecycle.FinalHookInput,
) (result lifecycle.FinalHookResult, err error) {
	startedAt := time.Now()
	defer func() {
		panicValue := recover()
		outcome := "succeeded"
		if panicValue != nil || err != nil {
			outcome = "failed"
		} else if result.Skipped {
			outcome = "skipped"
		}
		hookName := result.Name
		if hookName == "" {
			hookName = "unknown"
		}
		transport := result.Transport
		if transport == "" {
			transport = "unknown"
		}
		attributes := metric.WithAttributes(
			attribute.String("linkd.event_source_id", input.Alert.EventSourceID),
			attribute.String("linkd.hook.name", hookName),
			attribute.String("messaging.system", transport),
			attribute.String("linkd.outcome", outcome),
		)
		h.metrics.finalHookOperations.Add(ctx, 1, attributes)
		h.metrics.finalHookDuration.Record(ctx, time.Since(startedAt).Seconds(), attributes)
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	return h.next.Execute(ctx, input)
}

func lifecycleReason(reason string) string {
	switch reason {
	case "":
		return ""
	case lifecycle.ReasonActiveAlertNotFound,
		lifecycle.ReasonInvalidTransition,
		lifecycle.ReasonSeverityUpgrade,
		lifecycle.ReasonSeveritySuppressed:
		return reason
	default:
		return "other"
	}
}

var _ scheduler.Observer = (*lifecycleSchedulerObserver)(nil)

var _ lifecycle.FinalHook = (*observedFinalHook)(nil)
