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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/rules"
)

type enrichObserver struct{ metrics *instruments }

// EnrichObserver 创建 Lifecycle 同步丰富观察器。
func (r *Runtime) EnrichObserver() lifecycle.EnrichObserver {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &enrichObserver{metrics: r.metrics}
}

func (o *enrichObserver) Started(ctx context.Context, eventSourceID string) {
	o.metrics.enrichInflight.Add(ctx, 1, metric.WithAttributes(
		attribute.String("linkd.event_source_id", eventSourceID),
	))
}

func (o *enrichObserver) Finished(ctx context.Context, observation lifecycle.EnrichObservation) {
	status := enrichStatus(observation.Status)
	outcome := enrichOutcome(observation.Outcome)
	chainKind := enrichChainKind(observation.ChainKind)
	attributes := metric.WithAttributes(
		attribute.String("linkd.event_source_id", observation.EventSourceID),
		attribute.String("linkd.status", status),
		attribute.String("linkd.outcome", outcome),
		attribute.String("linkd.chain_kind", chainKind),
	)
	o.metrics.enrichAttempts.Add(ctx, 1, attributes)
	o.metrics.enrichAttemptDuration.Record(ctx, observation.Duration.Seconds(), attributes)
	o.metrics.enrichInflight.Add(ctx, -1, metric.WithAttributes(
		attribute.String("linkd.event_source_id", observation.EventSourceID),
	))
	o.metrics.enrichPayloadSize.Record(ctx, observation.PayloadBytes, metric.WithAttributes(
		attribute.String("linkd.event_source_id", observation.EventSourceID),
		attribute.String("linkd.status", status),
	))
}

type enrichProcessorObserver struct{ metrics *instruments }

// EnrichProcessorObserver 创建 Chain Processor 观察器。
func (r *Runtime) EnrichProcessorObserver() enrich.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &enrichProcessorObserver{metrics: r.metrics}
}

func (o *enrichProcessorObserver) ProcessorFinished(ctx context.Context, observation enrich.ProcessorObservation) {
	processor := enrichProcessor(observation.Processor)
	attributes := metric.WithAttributes(
		attribute.String("linkd.processor", processor),
		attribute.String("linkd.status", enrichStatus(observation.Status)),
		attribute.String("linkd.outcome", enrichProcessorOutcome(observation.Outcome)),
	)
	o.metrics.enrichProcessorAttempts.Add(ctx, 1, attributes)
	o.metrics.enrichProcessorDuration.Record(ctx, observation.Duration.Seconds(), attributes)
	for _, diagnostic := range observation.Diagnostics {
		o.metrics.enrichProcessorDiagnostics.Add(ctx, 1, metric.WithAttributes(
			attribute.String("linkd.processor", processor),
			attribute.String("linkd.diagnostic_code", enrichDiagnosticCode(diagnostic.Code)),
			attribute.String("linkd.dependency", enrichDependency(diagnostic.Dependency)),
		))
	}
}

func enrichStatus(status domain.EnrichStatus) string {
	switch status {
	case domain.EnrichStatusSucceeded, domain.EnrichStatusPartial, domain.EnrichStatusFailed, domain.EnrichStatusSkipped:
		return string(status)
	default:
		return "unknown"
	}
}

func enrichOutcome(outcome string) string {
	switch outcome {
	case lifecycle.EnrichOutcomeCompleted, lifecycle.EnrichOutcomeError, lifecycle.EnrichOutcomePanic,
		lifecycle.EnrichOutcomeInvalidStatus, lifecycle.EnrichOutcomeInvalidData:
		return outcome
	default:
		return "unknown"
	}
}

func enrichChainKind(kind lifecycle.EnrichChainKind) string {
	switch kind {
	case lifecycle.EnrichChainConfigured, lifecycle.EnrichChainNoop, lifecycle.EnrichChainUnknown:
		return string(kind)
	default:
		return string(lifecycle.EnrichChainUnknown)
	}
}

func enrichProcessor(name string) string {
	switch name {
	case rules.StrategyProcessor, rules.ResourceProcessor, rules.DisplayProcessor, rules.MetricProcessor, rules.SourceProcessor:
		return name
	default:
		return "unknown"
	}
}

func enrichProcessorOutcome(outcome string) string {
	switch outcome {
	case enrich.ProcessorOutcomeCompleted, enrich.ProcessorOutcomeMatchError, enrich.ProcessorOutcomeProcessError,
		enrich.ProcessorOutcomePanic, enrich.ProcessorOutcomeInvalidResult:
		return outcome
	default:
		return "unknown"
	}
}

func enrichDiagnosticCode(code enrich.DiagnosticCode) string {
	if code.Valid() {
		return string(code)
	}
	return "other"
}

func enrichDependency(dependency string) string {
	switch dependency {
	case "":
		return "none"
	case rules.DependencyKingeyeStrategy, rules.DependencyMetricLibrary,
		rules.DependencyOneModel, rules.DependencyAlarmSource:
		return dependency
	default:
		return "other"
	}
}
