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

type queryUnavailableMetrics struct {
	attributions *prometheus.CounterVec
}

func newQueryUnavailableMetrics() queryUnavailableMetrics {
	m := queryUnavailableMetrics{
		attributions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem,
			Name: "query_unavailable_attribution_total",
			Help: "Physical queries that completed UNAVAILABLE, by where their reason code came from: " +
				"attempt (an attempt named the reason; the code means what it says), no_attempt_reason " +
				"(attempts were made and none was classified; the code is a fallback guess about a " +
				"provider that was reached), no_attempts (nothing was sent; the code says nothing about " +
				"the provider). QUERY_UNAVAILABLE reads as a provider fault and sends a reader to the " +
				"provider, and two of these three would send them somewhere there is nothing to find; " +
				"this is the only reading that tells them apart. One increment per physical query, so a " +
				"Query Group with several Plans counts once per Plan.",
		}, []string{"attribution"}),
	}
	// Pre-created so that "no unavailable completions" reads as zeros, not
	// as an absent family, and so that a fallback that never fires is a
	// reading rather than a missing series.
	for _, attribution := range observability.QueryUnavailableAttributions {
		m.attributions.WithLabelValues(attribution)
	}
	return m
}

func (m queryUnavailableMetrics) observe(o observability.Observation) {
	// Normalized here as well as at the source, so using the metric directly
	// cannot invent a label value the closed set does not contain.
	o = observability.NormalizeObservation(o)
	for _, facts := range o.QueryUnavailable {
		m.attributions.WithLabelValues(facts.Attribution).Inc()
	}
}
