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
	"linkd/internal/lifecycle"
)

type lifecycleProcessor interface {
	ProcessEvent(ctx context.Context, bkTenantID, eventID string) (lifecycle.ProcessResult, error)
}

type observedLifecycleProcessor struct {
	next    lifecycleProcessor
	metrics *instruments
}

// ObserveLifecycleProcessor 包装生命周期处理端口，只记录低基数结果，不记录租户或实体 ID。
func (r *Runtime) ObserveLifecycleProcessor(next lifecycleProcessor) lifecycleProcessor {
	if r == nil || r.metrics == nil || next == nil {
		return next
	}
	return &observedLifecycleProcessor{next: next, metrics: r.metrics}
}

func (p *observedLifecycleProcessor) ProcessEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (lifecycle.ProcessResult, error) {
	startedAt := time.Now()
	result, err := p.next.ProcessEvent(ctx, bkTenantID, eventID)
	outcome := lifecycleMetricOutcome(result, err)
	attributes := []attribute.KeyValue{
		attribute.String("linkd.stage", "lifecycle"),
		attribute.String("linkd.outcome", outcome),
		attribute.String("linkd.trigger", "queue"),
	}
	if result.ReasonCode != "" {
		attributes = append(attributes, attribute.String("linkd.reason_code", result.ReasonCode))
	}
	p.metrics.pipelineAttempts.Add(ctx, 1, metric.WithAttributes(attributes...))
	p.metrics.pipelineAttemptDuration.Record(
		ctx,
		time.Since(startedAt).Seconds(),
		metric.WithAttributes(attributes...),
	)
	return result, err
}

func lifecycleMetricOutcome(result lifecycle.ProcessResult, err error) string {
	if err != nil {
		return "failed"
	}
	switch result.Outcome {
	case lifecycle.OutcomeRejected:
		return "rejected"
	case lifecycle.OutcomeAlreadyDone:
		return "replayed"
	default:
		return "accepted"
	}
}
