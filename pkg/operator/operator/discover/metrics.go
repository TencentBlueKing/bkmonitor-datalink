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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	discoverStartedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_started_total",
			Help:      "discover started total",
		},
		[]string{"name"},
	)

	discoverStoppedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_stopped_total",
			Help:      "discover stopped total",
		},
		[]string{"name"},
	)

	discoverCreatedChildConfigSuccessTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_created_config_success_total",
			Help:      "discover created child config success total",
		},
		[]string{"name"},
	)

	discoverCreatedChildConfigFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_created_config_failed_total",
			Help:      "discover created child config failed total",
		},
		[]string{"name"},
	)

	discoverCreatedChildConfigCachedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_created_config_cached_total",
			Help:      "discover created child config cached total",
		},
		[]string{"name"},
	)

	discoverHandledTgTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_handled_tg_total",
			Help:      "discover handled tg total",
		},
		[]string{"name"},
	)

	discoverDeletedTgSourceTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_deleted_tg_source_total",
			Help:      "discover deleted tg source total",
		},
		[]string{"name"},
	)
)

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

func (m *metricMonitor) IncCreatedChildConfigSuccessCounter() {
	discoverCreatedChildConfigSuccessTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncCreatedChildConfigFailedCounter() {
	discoverCreatedChildConfigFailedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncCreatedChildConfigCachedCounter() {
	discoverCreatedChildConfigCachedTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncHandledTgCounter() {
	discoverHandledTgTotal.WithLabelValues(m.name).Inc()
}

func (m *metricMonitor) IncDeletedTgSourceCounter() {
	discoverDeletedTgSourceTotal.WithLabelValues(m.name).Inc()
}
