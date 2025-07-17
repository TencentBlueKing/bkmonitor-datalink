// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	handledTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_handled_total",
			Help:      "Receiver handled records total",
		},
		[]string{"source", "protocol", "record_type", "token"},
	)

	droppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_dropped_total",
			Help:      "Receiver dropped records total",
		},
		[]string{"source", "protocol", "record_type"},
	)

	skippedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_skipped_total",
			Help:      "Receiver skipped records total",
		},
		[]string{"source", "protocol", "record_type", "token"},
	)

	internalErrorTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_internal_error_total",
			Help:      "Receiver internal error total",
		},
		[]string{"source", "protocol", "record_type"},
	)

	handledDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_handled_duration_seconds",
			Help:      "Receiver handled duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"source", "protocol", "record_type", "token"},
	)

	receivedBytesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_received_bytes_total",
			Help:      "Receiver received body bytes total",
		},
		[]string{"source", "protocol", "record_type", "token"},
	)

	receivedBytesSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_received_bytes_size",
			Help:      "Receiver received body bytes size",
			Buckets:   define.DefSizeDistribution,
		},
		[]string{"source", "protocol", "record_type", "token"},
	)

	preCheckFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "receiver_precheck_failed_total",
			Help:      "Receiver records precheck failed total",
		},
		[]string{"source", "protocol", "record_type", "processor", "token", "code"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct {
	source string
}

func (m *metricMonitor) Source(s string) *metricMonitor {
	return &metricMonitor{source: s}
}

func (m *metricMonitor) IncDroppedCounter(protocol define.RequestType, rtype define.RecordType) {
	droppedTotal.WithLabelValues(m.source, protocol.S(), rtype.S()).Inc()
}

func (m *metricMonitor) IncHandledCounter(protocol define.RequestType, rtype define.RecordType, token string) {
	handledTotal.WithLabelValues(m.source, protocol.S(), rtype.S(), token).Inc()
}

func (m *metricMonitor) IncSkippedCounter(protocol define.RequestType, rtype define.RecordType, token string) {
	skippedTotal.WithLabelValues(m.source, protocol.S(), rtype.S(), token).Inc()
}

func (m *metricMonitor) IncPreCheckFailedCounter(protocol define.RequestType, rtype define.RecordType, processor, token string, code define.StatusCode) {
	preCheckFailedTotal.WithLabelValues(m.source, protocol.S(), rtype.S(), processor, token, code.S()).Inc()
}

func (m *metricMonitor) AddReceivedBytesCounter(v float64, protocol define.RequestType, rtype define.RecordType, token string) {
	receivedBytesTotal.WithLabelValues(m.source, protocol.S(), rtype.S(), token).Add(v)
}

func (m *metricMonitor) ObserveBytesDistribution(v float64, protocol define.RequestType, rtype define.RecordType, token string) {
	receivedBytesSize.WithLabelValues(m.source, protocol.S(), rtype.S(), token).Observe(v)
}

func (m *metricMonitor) IncInternalErrorCounter(protocol define.RequestType, rtype define.RecordType) {
	internalErrorTotal.WithLabelValues(m.source, protocol.S(), rtype.S()).Inc()
}

func (m *metricMonitor) ObserveHandledDuration(t time.Time, protocol define.RequestType, rtype define.RecordType, token string) {
	handledDuration.WithLabelValues(m.source, protocol.S(), rtype.S(), token).Observe(time.Since(t).Seconds())
}
