// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package qcloudmonitor

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	reconcileQCloudMonitorSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reconcile_qcloudmonitor_success_total",
			Help:      "reconcile qcloudmonitor success counter",
		},
		[]string{"name"},
	)
	reconcileQCloudMonitorFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reconcile_qcloudmonitor_failed_total",
			Help:      "reconcile qcloudmonitor failed counter",
		},
		[]string{"name"},
	)
	reconcileQCloudMonitorDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reconcile_qcloudmonitor_duration_seconds",
			Help:      "reconcile qcloudmonitor duration in seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"name"},
	)
)

func newMetricMonitor() *metricMonitor {
	return &metricMonitor{}
}

var defaultMetricMonitor = newMetricMonitor()

type metricMonitor struct{}

func (m *metricMonitor) IncReconcileQCloudMonitorSuccessCounter(name string) {
	reconcileQCloudMonitorSuccess.WithLabelValues(name).Inc()
}

func (m *metricMonitor) IncReconcileQCloudMonitorFailedCounter(name string) {
	reconcileQCloudMonitorFailed.WithLabelValues(name).Inc()
}

func (m *metricMonitor) ObserveReconcileQCloudMonitorDuration(name string, duration time.Duration) {
	reconcileQCloudMonitorDuration.WithLabelValues(name).Observe(duration.Seconds())
}
