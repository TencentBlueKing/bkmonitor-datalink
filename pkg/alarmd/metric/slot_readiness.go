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
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type slotReadinessMetrics struct {
	slack    prometheus.Histogram
	boundary *prometheus.CounterVec
}

// slotReadinessSlackBuckets is bounded above by the longest wait worth
// distinguishing: past five minutes the answer is the same either way, and the
// cardinality budget counts every bucket.
var slotReadinessSlackBuckets = []float64{0.1, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300}

func newSlotReadinessMetrics() slotReadinessMetrics {
	return slotReadinessMetrics{
		slack: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem,
			Name: "slot_readiness_slack_seconds",
			Help: "How long a Slot's data had already been readable when an execution arrived to read " +
				"it, for normal operations whose queries share one readiness moment. Arriving early is " +
				"turned away before this point and counted as a deferral, so this measures the other " +
				"direction only, and the two have to be read together: a scheduler that stops arriving " +
				"early by arriving late shows no deferrals at all and looks like a success. It does not " +
				"say why the time was lost -- sleeping too long and waiting for dispatch capacity produce " +
				"the same figure, and the dispatcher's own queue wait is what tells them apart.",
			Buckets: slotReadinessSlackBuckets,
		}),
		boundary: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem,
			Name: "slot_readiness_boundary_total",
			Help: "Normal executions by whether their queries agree on one moment the data becomes " +
				"readable. Only a unified boundary contributes to slot_readiness_slack_seconds, so this " +
				"is what says how much of the population that histogram covers: a mixed Slot has no one " +
				"readiness moment and therefore no single answer for how late anything was. Reading the " +
				"histogram without this reads a sample as the whole.",
		}, []string{"boundary"}),
	}
}

func (m slotReadinessMetrics) observe(o observability.Observation) {
	// Normalized here as well as at the source, so using the metric directly
	// cannot invent a label value the closed set does not contain.
	o = observability.NormalizeObservation(o)
	facts := o.SlotReadiness
	if facts == nil || o.Component != observability.ComponentAccess ||
		o.Stage != observability.StageSlotReadinessArrival {
		return
	}
	m.boundary.WithLabelValues(facts.Boundary).Inc()
	if facts.Slack {
		m.slack.Observe(facts.SlackSeconds)
	}
}
