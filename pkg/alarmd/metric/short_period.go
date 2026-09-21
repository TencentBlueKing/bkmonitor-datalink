// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type shortPeriodMetrics struct {
	completed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	lag       *prometheus.HistogramVec
}

// shortPeriodBuckets bound the two histograms. The old set jumped from 15 to
// 25 to 30: the 10s cohort's tail sits at its MinimumSettlingWait floor of
// ten seconds and the one or two percent past fifteen were interpolated to
// a p99 of twenty-two, a number no Slot ever took. 12 and 20 put edges where
// the tail is, so a quantile there is read off a bucket and not invented
// between two.
var shortPeriodBuckets = []float64{0.01, 0.1, 1, 5, 10, 12, 15, 20, 25, 30, 60}

func newShortPeriodMetrics() shortPeriodMetrics {
	metrics := shortPeriodMetrics{
		completed: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_completions_total", Help: "Acknowledged short-period Slot Progress completions by actual completion kind."}, []string{"cohort", "operation", "completion_kind"}),
		// Both histograms are told apart by completion kind. A query-free
		// closure -- GAP_SKIPPED, SNAPSHOT_UNAVAILABLE -- has a lag of how late
		// the skip was booked and a duration of nearly nothing, and in one
		// histogram with the executed kinds it made the 15s and 30s cohorts'
		// lag p99 a statistic of skips and their duration p99 a statistic of
		// almost-zeros. Read the executed kinds for how detection is doing
		// and the skips for how late the scheduler books what it gives up.
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_execution_duration_seconds", Help: "Duration of the committed executor call, excluding SlotSource preparation and earlier attempts, by the completion kind it committed.", Buckets: shortPeriodBuckets}, []string{"cohort", "completion_kind"}),
		lag:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "short_period_slot_completion_lag_seconds", Help: "Elapsed wall time from Slot EvaluationTime to acknowledged completion, including backlog, by the completion kind committed; a query-free kind's lag is how late the skip was booked, not an execution.", Buckets: shortPeriodBuckets}, []string{"cohort", "completion_kind"}),
	}
	// Every combination exists from construction, so a kind that has never
	// completed publishes a zero and "no skips" can be told from "not wired".
	for _, cohort := range observability.ShortPeriodCohorts {
		for _, kind := range observability.ShortPeriodCompletionKinds {
			metrics.duration.WithLabelValues(cohort, kind)
			metrics.lag.WithLabelValues(cohort, kind)
		}
	}
	return metrics
}

func (m shortPeriodMetrics) observe(o observability.Observation) {
	// Normalize independently so direct metric use cannot create arbitrary labels.
	o = observability.NormalizeObservation(o)
	f := o.ShortPeriodCompletion
	if f == nil {
		return
	}
	m.completed.WithLabelValues(f.Cohort, string(o.Operation), f.CompletionKind).Inc()
	m.duration.WithLabelValues(f.Cohort, f.CompletionKind).Observe(o.Duration.Seconds())
	m.lag.WithLabelValues(f.Cohort, f.CompletionKind).Observe(f.LagSeconds)
}
