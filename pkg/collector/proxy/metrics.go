// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	handledTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_handled_total",
			Help:      "Proxy handled records total",
		},
		[]string{"id"},
	)

	droppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_dropped_total",
			Help:      "Proxy dropped records total",
		},
		[]string{"id", "code"},
	)

	internalErrorTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_internal_error_total",
			Help:      "Proxy internal error total",
		},
	)

	handledDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_handled_duration_seconds",
			Help:      "Proxy handled duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"id"},
	)

	receivedBytesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_received_bytes_total",
			Help:      "Proxy received body bytes total",
		},
		[]string{"id"},
	)

	receivedBytesSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_received_bytes_size",
			Help:      "Proxy received body bytes size",
			Buckets:   define.DefSizeDistribution,
		},
		[]string{"id"},
	)

	preCheckFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "proxy_precheck_failed_total",
			Help:      "proxy records precheck failed total",
		},
		[]string{"processor", "token", "id", "code"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncDroppedCounter(id int64, code int) {
	droppedTotal.WithLabelValues(strconv.Itoa(int(id)), strconv.Itoa(code)).Inc()
}

func (m *metricMonitor) IncHandledCounter(id int64) {
	handledTotal.WithLabelValues(strconv.Itoa(int(id))).Inc()
}

func (m *metricMonitor) AddReceivedBytesCounter(v float64, id int64) {
	receivedBytesTotal.WithLabelValues(strconv.Itoa(int(id))).Add(v)
}

func (m *metricMonitor) ObserveBytesDistribution(v float64, id int64) {
	receivedBytesSize.WithLabelValues(strconv.Itoa(int(id))).Observe(v)
}

func (m *metricMonitor) IncInternalErrorCounter() {
	internalErrorTotal.Inc()
}

func (m *metricMonitor) ObserveHandledDuration(t time.Time, id int64) {
	handledDuration.WithLabelValues(strconv.Itoa(int(id))).Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncPreCheckFailedCounter(processor, token string, id int64, code define.StatusCode) {
	preCheckFailedTotal.WithLabelValues(processor, token, strconv.Itoa(int(id)), code.S()).Inc()
}
