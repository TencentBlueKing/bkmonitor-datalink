// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	handledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "cluster_handled_total",
			Help:      "Cluster handled total",
		},
		[]string{"token"},
	)

	droppedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "cluster_dropped_total",
			Help:      "Cluster dropped total",
		},
	)

	handledDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "cluster_handled_duration_seconds",
			Help:      "Cluster handled duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"token"},
	)

	preCheckFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "cluster_precheck_failed_total",
			Help:      "Cluster records precheck failed total",
		},
		[]string{"record_type", "processor", "token", "code"},
	)
)

func init() {
	prometheus.MustRegister(
		handledTotal,
		droppedTotal,
		handledDuration,
		preCheckFailedTotal,
	)
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncHandledCounter(token string) {
	handledTotal.WithLabelValues(token).Inc()
}

func (m *metricMonitor) IncDroppedCounter() {
	droppedTotal.Inc()
}

func (m *metricMonitor) ObserveHandledDuration(t time.Time, token string) {
	handledDuration.WithLabelValues(token).Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncFailedCheckFailedCounter(processor, token string, code int) {
	preCheckFailedTotal.WithLabelValues(define.RecordTraces.S(), processor, token, strconv.Itoa(code))
}
