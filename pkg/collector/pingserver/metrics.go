// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pingserver

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	targetsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pingserver_targets_total",
			Help:      "Pingserver targets total",
		},
		[]string{"id"},
	)

	pingTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pingserver_ping_total",
			Help:      "Pingserver ping total",
		},
		[]string{"id"},
	)

	rollPingTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pingserver_rollping_total",
			Help:      "Pingserver roll ping total",
		},
		[]string{"id"},
	)

	droppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pingserver_dropped_total",
			Help:      "Pingserver dropped records total",
		},
		[]string{"id"},
	)

	pingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pingserver_ping_duration_seconds",
			Help:      "Pingserver ping duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"id"},
	)
)

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) SetTargetsCount(id int64, n int) {
	targetsTotal.WithLabelValues(strconv.Itoa(int(id))).Set(float64(n))
}

func (m *metricMonitor) IncDroppedCounter(id int64) {
	droppedTotal.WithLabelValues(strconv.Itoa(int(id))).Inc()
}

func (m *metricMonitor) IncRollPingCounter(id int64) {
	rollPingTotal.WithLabelValues(strconv.Itoa(int(id))).Inc()
}

func (m *metricMonitor) IncPingCounter(id int64) {
	pingTotal.WithLabelValues(strconv.Itoa(int(id))).Inc()
}

func (m *metricMonitor) ObservePingDuration(t time.Time, id int64) {
	pingDuration.WithLabelValues(strconv.Itoa(int(id))).Observe(time.Since(t).Seconds())
}
