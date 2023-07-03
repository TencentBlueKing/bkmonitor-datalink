// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	discoverStartedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_started_total",
			Help:      "discover started total",
		},
		[]string{"name"},
	)

	discoverStoppedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_stopped_total",
			Help:      "discover stopped total",
		},
		[]string{"name"},
	)

	discoverWaitedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_waited_total",
			Help:      "discover waited total",
		},
		[]string{"name"},
	)

	discoverCreatedChildConfigSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_created_child_config_success_total",
			Help:      "discover created child config success total",
		},
		[]string{"name"},
	)

	discoverCreatedChildConfigFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_created_child_config_failed_total",
			Help:      "discover created child config failed total",
		},
		[]string{"name"},
	)

	discoverRemovedChildConfigTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_removed_child_config_total",
			Help:      "discover removed child config total",
		},
		[]string{"name"},
	)

	discoverReceivedTargetGroupTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_received_target_group_total",
			Help:      "discover received target group total",
		},
		[]string{"name"},
	)

	discoverHandledTargetGroupDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_handled_target_group_duration_seconds",
			Help:      "discover handled target group duration seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300},
		},
		[]string{"name"},
	)

	discoverGotSecretSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_got_secret_success_total",
			Help:      "discover got secret success total",
		},
		[]string{"name"},
	)

	discoverGotSecretFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_got_secret_failed_total",
			Help:      "discover got secret failed total",
		},
		[]string{"name"},
	)
)

func init() {
	prometheus.MustRegister(
		discoverStartedTotal,
		discoverStoppedTotal,
		discoverWaitedTotal,
		discoverCreatedChildConfigSuccessTotal,
		discoverCreatedChildConfigFailedTotal,
		discoverRemovedChildConfigTotal,
		discoverReceivedTargetGroupTotal,
		discoverHandledTargetGroupDuration,
		discoverGotSecretSuccessTotal,
		discoverGotSecretFailedTotal,
	)
}

func newMetricMonitor(name string) *metricMonitor {
	return &metricMonitor{name: name}
}

type metricMonitor struct {
	name string
}

func (m *metricMonitor) IncStartedCounter() {
	discoverStartedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncStoppedCounter() {
	discoverStoppedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncWaitedCounter() {
	discoverWaitedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncCreatedChildConfigSuccessCounter() {
	discoverCreatedChildConfigSuccessTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncCreatedChildConfigFailedCounter() {
	discoverCreatedChildConfigFailedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncRemovedChildConfigCounter() {
	discoverRemovedChildConfigTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncReceivedTargetGroupCounter() {
	discoverReceivedTargetGroupTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) ObserveTargetGroupDuration(t time.Time) {
	discoverHandledTargetGroupDuration.WithLabelValues(m.name).Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncGotSecretSuccessCounter() {
	discoverGotSecretSuccessTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncGotSecretFailedCounter() {
	discoverGotSecretFailedTotal.WithLabelValues(m.name).Inc()
}
