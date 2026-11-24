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
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/consume"
)

const unknownConsumeOutcome consume.OutcomeKind = 255

type consumeObserver struct {
	metrics                *instruments
	stage                  string
	transport              string
	eventSourceID          string
	recordPipelineAttempts bool
	baseAttributesCache    []attribute.KeyValue
	baseOption             metric.MeasurementOption
	queueOption            metric.MeasurementOption
	retryOption            metric.MeasurementOption
	outcomeOptions         map[consume.OutcomeKind]consumeOutcomeOptions
	laneOptions            sync.Map
}

type consumeOutcomeOptions struct {
	handler         metric.MeasurementOption
	duration        metric.MeasurementOption
	pipelineAttempt metric.MeasurementOption
}

type consumeLaneOptions struct {
	attributes  []attribute.KeyValue
	measurement metric.MeasurementOption
}

// ConsumeObserver 创建一个只使用封闭 stage/transport 标签的消费运行时观察器。
func (r *Runtime) ConsumeObserver(labels consume.RuntimeLabels) consume.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	observer := &consumeObserver{
		metrics:                r.metrics,
		stage:                  labels.Stage,
		transport:              labels.Transport,
		eventSourceID:          labels.EventSourceID,
		recordPipelineAttempts: labels.RecordPipelineAttempts,
		outcomeOptions:         make(map[consume.OutcomeKind]consumeOutcomeOptions, 6),
	}
	observer.baseAttributesCache = []attribute.KeyValue{
		attribute.String("linkd.stage", labels.Stage),
		attribute.String("messaging.system", labels.Transport),
	}
	if labels.EventSourceID != "" {
		observer.baseAttributesCache = append(observer.baseAttributesCache, attribute.String("linkd.event_source_id", labels.EventSourceID))
	}
	observer.baseAttributesCache = observer.baseAttributesCache[:len(observer.baseAttributesCache):len(observer.baseAttributesCache)]
	observer.baseOption = cachedMetricOption(observer.baseAttributesCache)
	observer.queueOption = cachedMetricOption(observer.baseAttributesCache,
		attribute.String("linkd.queue.role", labels.Stage+"_signal"), attribute.String("linkd.trigger", "queue"))
	observer.retryOption = cachedMetricOption(observer.baseAttributesCache, attribute.String("linkd.reason_code", "handler_retry"))
	for _, outcome := range []consume.OutcomeKind{
		consume.OutcomeComplete, consume.OutcomeRetry, consume.OutcomeDiscard,
		consume.OutcomeBlock, consume.OutcomeDefer, unknownConsumeOutcome,
	} {
		outcomeName := consumeOutcomeName(outcome)
		observer.outcomeOptions[outcome] = consumeOutcomeOptions{
			handler: cachedMetricOption(observer.baseAttributesCache, attribute.String("linkd.outcome", outcomeName)),
			duration: cachedMetricOption(observer.baseAttributesCache,
				attribute.String("linkd.outcome", outcomeName), attribute.String("linkd.trigger", "queue")),
			pipelineAttempt: cachedMetricOption(observer.baseAttributesCache,
				attribute.String("linkd.outcome", pipelineOutcomeName(labels.Stage, outcome)), attribute.String("linkd.trigger", "queue")),
		}
	}
	return observer
}

func (o *consumeObserver) DeliveryReceived(ctx context.Context, observation consume.DeliveryObservation) {
	attributes := o.cachedLaneOptions(observation.Lane)
	o.metrics.messagingReceived.Add(ctx, 1, attributes.measurement)
	o.metrics.messagingReceivedBytes.Add(ctx, int64(observation.Bytes), attributes.measurement)
	if observation.Redelivered {
		o.metrics.messagingRedelivered.Add(ctx, 1, attributes.measurement)
	}
}

func (o *consumeObserver) HandlerStarted(ctx context.Context, message consume.Message) {
	o.metrics.pipelineInflight.Add(ctx, 1, o.baseOption)
	if !message.EnqueuedAt.IsZero() {
		delay := time.Since(message.EnqueuedAt)
		if delay >= 0 {
			o.metrics.pipelineQueueDelay.Record(
				ctx,
				delay.Seconds(),
				o.queueOption,
			)
		}
	}
}

func (o *consumeObserver) HandlerFinished(
	ctx context.Context,
	outcome consume.OutcomeKind,
	duration time.Duration,
) {
	options, ok := o.outcomeOptions[outcome]
	if !ok {
		options = o.outcomeOptions[unknownConsumeOutcome]
	}
	o.metrics.pipelineInflight.Add(
		ctx,
		-1,
		o.baseOption,
	)
	o.metrics.messagingHandlerOutcomes.Add(
		ctx,
		1,
		options.handler,
	)
	o.metrics.messagingHandlerDuration.Record(
		ctx,
		duration.Seconds(),
		options.duration,
	)
	if o.recordPipelineAttempts {
		// Lifecycle 的 Event 耗时由 Processor 记录，不能再混入整条 Signal drain。
		o.metrics.pipelineAttemptDuration.Record(ctx, duration.Seconds(), options.duration)
		o.metrics.pipelineAttempts.Add(
			ctx,
			1,
			options.pipelineAttempt,
		)
	}
}

func (o *consumeObserver) RetryScheduled(ctx context.Context) {
	o.metrics.pipelineRetries.Add(ctx, 1, o.retryOption)
}

func (o *consumeObserver) StepFinished(ctx context.Context, observation consume.StepObservation) {
	if observation.Items <= 0 || observation.Step == "" || observation.Outcome == "" {
		return
	}
	attributes := append(o.baseAttributes(),
		attribute.String("linkd.step", observation.Step),
		attribute.String("linkd.outcome", observation.Outcome),
	)
	o.metrics.cleanerStepItems.Add(ctx, int64(observation.Items), metric.WithAttributes(attributes...))
	if observation.Duration > 0 {
		o.metrics.cleanerStepDuration.Record(ctx, observation.Duration.Seconds(), metric.WithAttributes(attributes...))
	}
}

func (o *consumeObserver) SettlementFinished(
	ctx context.Context,
	observation consume.SettlementObservation,
) {
	outcome := "failed"
	if observation.Succeeded {
		outcome = "succeeded"
	}
	attributes := append(o.laneAttributes(observation.Lane),
		attribute.String("linkd.settlement.mode", settlementModeName(observation.Mode)),
		attribute.String("linkd.outcome", outcome),
	)
	o.metrics.messagingSettlements.Add(ctx, 1, metric.WithAttributes(attributes...))
	if observation.Messages > 0 {
		o.metrics.messagingSettledMessages.Add(ctx, int64(observation.Messages), metric.WithAttributes(attributes...))
	}
	if observation.Duration > 0 {
		o.metrics.messagingSettleDuration.Record(ctx, observation.Duration.Seconds(), metric.WithAttributes(o.withoutPartition(attributes)...))
	}
	if o.stage == "clean" {
		o.StepFinished(ctx, consume.StepObservation{
			Step: "source_ack", Outcome: outcome, Items: observation.Messages, Duration: observation.Duration,
		})
	}
}

func (o *consumeObserver) FlowTransition(ctx context.Context, action string) {
	if o.stage == "clean" && o.eventSourceID != "" {
		switch action {
		case "start":
			o.metrics.cleanerFlowActive.Record(ctx, 1, o.baseOption)
		case "stop":
			o.metrics.cleanerFlowActive.Record(ctx, 0, o.baseOption)
		}
	}
	attributes := append(o.baseAttributes(), attribute.String("linkd.action", action))
	if action == "pause" || action == "resume" {
		attributes = append(attributes, attribute.String("linkd.reason_code", "backpressure"))
	}
	o.metrics.messagingFlowTransitions.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (o *consumeObserver) OwnershipChanged(ctx context.Context, observation consume.OwnershipObservation) {
	owned := int64(1)
	if observation.Kind == consume.OwnershipRevoked || observation.Kind == consume.OwnershipLost {
		owned = 0
	}
	for _, lane := range observation.Lanes {
		o.metrics.messagingLaneOwned.Record(ctx, owned, o.cachedLaneOptions(lane).measurement)
	}
}

func (o *consumeObserver) Snapshot(ctx context.Context, snapshot consume.RuntimeSnapshot) {
	o.metrics.messagingInflight.Record(ctx, int64(snapshot.InflightMessages), o.baseOption)
	o.metrics.messagingInflightSize.Record(ctx, int64(snapshot.InflightBytes), o.baseOption)
	o.metrics.messagingRetryItems.Record(ctx, int64(snapshot.RetryItems), o.baseOption)
	o.metrics.messagingRetryOldestAge.Record(ctx, snapshot.RetryOldestAge.Seconds(), o.baseOption)
	o.metrics.messagingSettlementGap.Record(ctx, int64(snapshot.SettlementGap), o.baseOption)
	o.metrics.messagingGapOldestAge.Record(ctx, snapshot.SettlementGapOldestAge.Seconds(), o.baseOption)
	for _, lane := range snapshot.Lanes {
		attributes := o.cachedLaneOptions(lane.Lane).measurement
		o.metrics.messagingLaneInflight.Record(ctx, int64(lane.InflightMessages), attributes)
		o.metrics.messagingLaneBytes.Record(ctx, int64(lane.InflightBytes), attributes)
		o.metrics.messagingLanePaused.Record(ctx, boolInt64(lane.Paused), attributes)
		o.metrics.messagingLaneOwned.Record(ctx, boolInt64(lane.Owned), attributes)
	}
}

func (o *consumeObserver) baseAttributes() []attribute.KeyValue {
	return o.baseAttributesCache
}

func (o *consumeObserver) laneAttributes(lane string) []attribute.KeyValue {
	return o.cachedLaneOptions(lane).attributes
}

func (o *consumeObserver) cachedLaneOptions(lane string) consumeLaneOptions {
	if cached, ok := o.laneOptions.Load(lane); ok {
		return cached.(consumeLaneOptions)
	}
	attributes := append([]attribute.KeyValue(nil), o.baseAttributesCache...)
	if partition, ok := kafkaPartition(lane); ok && o.transport == "kafka" {
		attributes = append(attributes, attribute.Int("messaging.kafka.partition", partition))
	}
	attributes = attributes[:len(attributes):len(attributes)]
	options := consumeLaneOptions{attributes: attributes, measurement: cachedMetricOption(attributes)}
	actual, _ := o.laneOptions.LoadOrStore(lane, options)
	return actual.(consumeLaneOptions)
}

func cachedMetricOption(base []attribute.KeyValue, extra ...attribute.KeyValue) metric.MeasurementOption {
	attributes := make([]attribute.KeyValue, 0, len(base)+len(extra))
	attributes = append(attributes, base...)
	attributes = append(attributes, extra...)
	return metric.WithAttributeSet(attribute.NewSet(attributes...))
}

func (o *consumeObserver) withoutPartition(attributes []attribute.KeyValue) []attribute.KeyValue {
	filtered := make([]attribute.KeyValue, 0, len(attributes))
	for _, value := range attributes {
		if string(value.Key) != "messaging.kafka.partition" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func kafkaPartition(lane string) (int, bool) {
	separator := strings.LastIndexByte(lane, '/')
	if separator < 0 || separator == len(lane)-1 {
		return 0, false
	}
	partition, err := strconv.Atoi(lane[separator+1:])
	return partition, err == nil && partition >= 0
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (o *consumeObserver) ShutdownFinished(
	ctx context.Context,
	succeeded bool,
	duration time.Duration,
	remaining int,
) {
	outcome := "failed"
	if succeeded {
		outcome = "succeeded"
	}
	o.metrics.messagingShutdown.Record(
		ctx,
		duration.Seconds(),
		metric.WithAttributes(append(o.baseAttributes(), attribute.String("linkd.outcome", outcome))...),
	)
	o.metrics.messagingRemaining.Record(
		ctx,
		int64(remaining),
		metric.WithAttributes(o.baseAttributes()...),
	)
}

func consumeOutcomeName(outcome consume.OutcomeKind) string {
	switch outcome {
	case consume.OutcomeComplete:
		return "complete"
	case consume.OutcomeRetry:
		return "retry"
	case consume.OutcomeDiscard:
		return "discard"
	case consume.OutcomeBlock:
		return "block"
	case consume.OutcomeDefer:
		return "defer"
	default:
		return "unknown"
	}
}

func settlementModeName(mode consume.SettlementMode) string {
	if mode == consume.SettlementCumulative {
		return "cumulative"
	}
	return "individual"
}

func pipelineOutcomeName(stage string, outcome consume.OutcomeKind) string {
	if stage == "clean" {
		switch outcome {
		case consume.OutcomeComplete:
			return "normalized"
		case consume.OutcomeDiscard:
			return "rejected"
		default:
			return "failed"
		}
	}
	return consumeOutcomeName(outcome)
}
