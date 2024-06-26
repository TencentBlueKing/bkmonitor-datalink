// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// basic metric for bmw
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const metricNamespace = "bkmonitor_api_server"

var (
	// API request metrics
	apiRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Name:      "api_request_total",
			Help:      "api request total",
		},
		[]string{"method", "path", "status"},
	)
	// Api request cost time metrics
	apiRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "api_request_duration_seconds",
			Help:      "api request cost time",
			Buckets:   []float64{0.1, 0.3, 0.6, 1, 3, 6, 10, 20},
		},
		[]string{"method", "path"},
	)
)

// RequestApiTotal request api total metric
func RequestApiTotal(method, apiPath, status string) {
	metric, err := apiRequestTotal.GetMetricWithLabelValues(method, apiPath, status)
	if err != nil {
		logger.Errorf("prom get request api total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RequestApiDurationSeconds request api duration seconds metric
func RequestApiDurationSeconds(method, path string, startTime time.Time) {
	metric, err := apiRequestDurationSeconds.GetMetricWithLabelValues(method, path)
	if err != nil {
		logger.Errorf("prom get request api time seconds metric failed: %s", err)
		return
	}
	metric.Observe(time.Since(startTime).Seconds())
}

var Registry = prometheus.NewRegistry()

func init() {
	// register the metrics
	Registry.MustRegister(
		apiRequestTotal,
		apiRequestDurationSeconds,
	)
}
