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

type queryTruncationMetrics struct {
	truncated *prometheus.CounterVec
}

func newQueryTruncationMetrics() queryTruncationMetrics {
	truncated := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem,
		Name: "uq_answer_truncated_total",
		Help: "UQ answers that may have been cut, by source: a terms level of the Elasticsearch aggregation " +
			"held exactly UQ's bucket cap (EsMaxSize, 10000 by default), and UQ drops the groups past the " +
			"cap without marking the answer. The series it returned are evaluated as they are; the groups " +
			"it may have dropped are not known absent. A query whose groups exceed the cap is cut every " +
			"round, so any non-zero is worth reading: the query completion log line names the dimension " +
			"(truncation_dimension). Every source is reported at zero, so a steady zero is an answer. " +
			"Read with alarmd-cli invoke metrics.get.",
	}, []string{"source_semantics"})
	for _, source := range observability.TruncationSources {
		truncated.WithLabelValues(source)
	}
	return queryTruncationMetrics{truncated: truncated}
}

func (m queryTruncationMetrics) observe(o observability.Observation) {
	o = observability.NormalizeObservation(o)
	for _, cut := range o.QueryTruncation {
		m.truncated.WithLabelValues(cut.Source).Inc()
	}
}
