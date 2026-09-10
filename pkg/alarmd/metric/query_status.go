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

type queryStatusMetrics struct {
	responses *prometheus.CounterVec
}

func newQueryStatusMetrics() queryStatusMetrics {
	return queryStatusMetrics{
		responses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem,
			Name: "access_response_status_total",
			Help: "UQ responses carrying a top-level status code, by that code and what this " +
				"deployment did with the response. An allowed outcome means the series travelled " +
				"with the code and was kept, which happens when an expression computes an answer " +
				"without reading any table; nothing else can report that a deployment is running " +
				"on a fallback answer rather than on data. The code set is closed by UQ's own " +
				"constants and an unrecognised code counts as OTHER.",
		}, []string{"code", "outcome"}),
	}
}

func (m queryStatusMetrics) observe(o observability.Observation) {
	// Normalized here as well as at the source, so using the metric directly
	// cannot invent a label value the closed set does not contain.
	o = observability.NormalizeObservation(o)
	facts := o.QueryStatus
	if facts == nil {
		return
	}
	m.responses.WithLabelValues(facts.Code, facts.Outcome).Inc()
}
