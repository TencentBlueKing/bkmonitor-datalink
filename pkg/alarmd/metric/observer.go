// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type observationMetrics struct {
	total      *prometheus.CounterVec
	operations *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	counts     [8]*prometheus.CounterVec
}

var observationDurationBuckets = []float64{0.005, 0.01, 0.05, 0.1, 1, 30}

func newObservationMetrics() observationMetrics {
	total := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "observation_total",
			Help:      "Bounded alarmd observations by pipeline stage and result.",
		},
		[]string{"component", "stage", "result", "reason_code"},
	)
	operations := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "operation_total",
			Help:      "Bounded alarmd observations by operation and result.",
		},
		[]string{"operation", "result", "reason_code"},
	)
	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "observation_duration_seconds",
			Help:      "Bounded alarmd observation duration by pipeline stage.",
			Buckets:   append([]float64(nil), observationDurationBuckets...),
		},
		[]string{"component", "stage", "result"},
	)
	countNames := []string{"messages", "records", "plans", "levels", "events", "bytes", "keys", "state_bytes"}
	var counts [8]*prometheus.CounterVec
	for index, name := range countNames {
		// Counts express affected volume by stage, direction and result. Component
		// is omitted because the fixed catalog maps every stage to one component.
		counts[index] = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: metricNamespace,
				Subsystem: metricSubsystem,
				Name:      "observed_" + name + "_total",
				Help:      "Total " + name + " reported through bounded alarmd observations.",
			},
			[]string{"stage", "direction", "result"},
		)
	}
	return observationMetrics{total: total, operations: operations, duration: duration, counts: counts}
}

func (m observationMetrics) collectors() []prometheus.Collector {
	collectors := []prometheus.Collector{m.total, m.operations, m.duration}
	for _, counter := range m.counts {
		collectors = append(collectors, counter)
	}
	return collectors
}

func (r *Recorder) Observe(_ context.Context, observation observability.Observation) {
	if r == nil || r.observations.total == nil {
		return
	}
	duration := observation.Duration
	observation = observability.NormalizeObservation(observation)
	metricReason := observability.NormalizeMetricReason(observation.Component, observation.ReasonCode, observation.Result)
	r.observations.total.WithLabelValues(
		string(observation.Component), string(observation.Stage),
		string(observation.Result), string(metricReason),
	).Inc()
	r.observations.operations.WithLabelValues(
		string(observation.Operation), string(observation.Result), string(metricReason),
	).Inc()
	if duration >= 0 {
		r.observations.duration.WithLabelValues(
			string(observation.Component), string(observation.Stage), string(observation.Result),
		).Observe(observation.Duration.Seconds())
	}
	values := [...]int64{
		observation.Counts.Messages, observation.Counts.Records, observation.Counts.Plans,
		observation.Counts.Levels, observation.Counts.Events, observation.Counts.Bytes,
		observation.Counts.Keys, observation.Counts.StateBytes,
	}
	for index, value := range values {
		if value > 0 {
			r.observations.counts[index].WithLabelValues(
				string(observation.Stage), string(observation.Direction), string(observation.Result),
			).Add(float64(value))
		}
	}
}

func observationCustomSeries() int {
	histogramSeries := len(observationDurationBuckets) + 3
	total := 0
	for _, pair := range observability.AllComponentStages() {
		for _, result := range observability.AllResults() {
			total += metricReasonCount(pair.Component, result)
		}
	}
	duration := len(observability.AllComponentStages()) * len(observability.AllResults()) * histogramSeries
	operationReasons := 0
	for _, result := range observability.AllResults() {
		operationReasons += metricReasonCount(observability.ComponentResource, result)
	}
	operations := len(observability.AllOperations()) * operationReasons
	counts := 8 * len(observability.AllStages()) * len(observability.AllDirections()) * len(observability.AllResults())
	return total + operations + duration + counts
}

func metricReasonCount(component observability.Component, result observability.Result) int {
	count := len(observability.AllReasons(component))
	if result != observability.ResultStarted && result != observability.ResultSuccess && result != observability.ResultResumed {
		count-- // ReasonNone is normalized to internal_unknown for non-success results.
	}
	return count
}

var _ observability.Observer = (*Recorder)(nil)
