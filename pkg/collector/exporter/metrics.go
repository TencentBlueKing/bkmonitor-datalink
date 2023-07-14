// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	sentDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_sent_duration_seconds",
			Help:      "Exporter sent duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	sentTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_sent_total",
			Help:      "Exporter sent total",
		},
	)

	handleEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "exporter_handled_event_total",
			Help:      "Exporter handled event total",
		},
		[]string{"record_type", "id"},
	)
)

func init() {
	prometheus.MustRegister(
		sentTotal,
		sentDuration,
		handleEventTotal,
	)
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncSentCounter() {
	sentTotal.Inc()
}

func (m *metricMonitor) ObserveSentDuration(t time.Time) {
	sentDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) AddHandledEventCounter(n int, rtype define.RecordType, dataId int32) {
	handleEventTotal.WithLabelValues(rtype.S(), strconv.Itoa(int(dataId))).Add(float64(n))
}
