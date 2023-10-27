// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataidwatcher

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	dataIDInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_info",
			Help:      "dataid information",
		},
		[]string{"id", "name", "usage", "system", "common", "bk_env"},
	)

	watcherHandledTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_watcher_handled_total",
			Help:      "dataid watcher handled total",
		},
	)

	watcherHandledDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_watcher_handled_duration_seconds",
			Help:      "dataid watcher handled duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	watcherReceivedEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_watcher_received_event_total",
			Help:      "dataid watcher received kubernetes event total",
		},
		[]string{"action"},
	)

	watcherHandledEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_watcher_handled_event_total",
			Help:      "dataid watcher handled kubernetes event total",
		},
		[]string{"action"},
	)
)

func init() {
	prometheus.MustRegister(
		dataIDInfo,
		watcherHandledTotal,
		watcherHandledDuration,
		watcherReceivedEventTotal,
		watcherHandledEventTotal,
	)
}

func newMetricMonitor() *metricMonitor {
	return &metricMonitor{}
}

type metricMonitor struct{}

func (m *metricMonitor) SetDataIDInfo(id int, name, usage string, system, common bool) {
	conv := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	dataIDInfo.WithLabelValues(fmt.Sprintf("%d", id), name, usage, conv(system), conv(common), ConfBkEnv).Set(1)
}

func (m *metricMonitor) IncHandledCounter() {
	watcherHandledTotal.Inc()
}

func (m *metricMonitor) ObserveHandledDuration(t time.Time) {
	watcherHandledDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncReceivedEventCounter(action string) {
	watcherReceivedEventTotal.WithLabelValues(action).Inc()
}

func (m *metricMonitor) IncHandledEventCounter(action string) {
	watcherHandledEventTotal.WithLabelValues(action).Inc()
}
