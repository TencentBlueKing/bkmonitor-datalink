// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	appUptime = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "uptime",
			Help:      "uptime of program",
		},
	)

	appBuildInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "build_info",
			Help:      "build information of app",
		},
		[]string{"version", "git_hash", "build_time"},
	)

	activeChildConfigCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_config_count",
			Help:      "active child config count",
		},
		[]string{"node"},
	)

	activeSharedDiscoveryCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_shared_discovery_count",
			Help:      "active shared discovery count",
		},
	)

	activeMonitorResourceCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_monitor_resource_count",
			Help:      "active monitor resource count",
		},
		[]string{"kind"},
	)

	handledEventDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_event_duration_seconds",
			Help:      "handled kubernetes event duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"monitor_kind", "action"},
	)

	handledSecretSuccessTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_success_total",
			Help:      "handled secret success total",
		},
		[]string{"secret_name", "action"},
	)

	handledSecretFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_failed_total",
			Help:      "handled secret failed total",
		},
		[]string{"secret_name", "action"},
	)

	dispatchedTaskTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_total",
			Help:      "dispatched task total",
		},
	)

	dispatchedTaskDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_duration_seconds",
			Help:      "dispatched task duration seconds",
			Buckets:   define.DefObserveDuration,
		},
	)

	handledDiscoverNotifyTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_discover_notify_total",
			Help:      "handled discover notify total",
		},
	)

	activeSecretFileCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_secret_file_count",
			Help:      "active secret file count",
		},
		[]string{"task_type", "secret_name"},
	)

	secretsExceeded = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "secrets_exceeded",
			Help:      "secrets exceeded",
		},
	)

	scaledStatefulSetFailedTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scaled_statefulset_failed_total",
			Help:      "scaled statefulset replicas failed total",
		},
	)

	scaledStatefulSetSuccessTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scaled_statefulset_success_total",
			Help:      "scaled statefulset replicas success total",
		},
	)
)

// BuildInfo 代表程序构建信息
type BuildInfo struct {
	Version string `json:"version"`
	GitHash string `json:"git_hash"`
	Time    string `json:"build_time"`
}

func newMetricMonitor() *metricMonitor {
	return &metricMonitor{}
}

type metricMonitor struct {
	receivedK8sEvent int
	handledK8sEvent  int

	handledSecretFailed      int       // 记录 secrets 处理失败次数
	handledSecretFailedTime  time.Time // 记录 secrets 处理失败时间
	handledSecretSuccessTime time.Time // 记录 secrets 处理成功时间
}

// UpdateUptime 更新进程活跃时间
func (m *metricMonitor) UpdateUptime(n int) {
	appUptime.Add(float64(n))
}

// SetAppBuildInfo 更新进程构建信息
func (m *metricMonitor) SetAppBuildInfo(info BuildInfo) {
	appBuildInfo.WithLabelValues(info.Version, info.GitHash, info.Time).Set(1)
}

// SetActiveChildConfigCount 记录活跃子配置数量
func (m *metricMonitor) SetActiveChildConfigCount(node string, n int) {
	activeChildConfigCount.WithLabelValues(node).Set(float64(n))
}

// SetActiveSharedDiscoveryCount 记录活跃 sharedDiscovery 数量
func (m *metricMonitor) SetActiveSharedDiscoveryCount(n int) {
	activeSharedDiscoveryCount.Set(float64(n))
}

// SetActiveMonitorResourceCount 记录活跃监控资源数量
func (m *metricMonitor) SetActiveMonitorResourceCount(kind string, n int) {
	activeMonitorResourceCount.WithLabelValues(kind).Set(float64(n))
}

// ObserveHandledEventDuration 观测 k8s 事件处理耗时
func (m *metricMonitor) ObserveHandledEventDuration(t time.Time, monitorKing, action string) {
	handledEventDuration.WithLabelValues(monitorKing, action).Observe(time.Since(t).Seconds())
}

// IncHandledSecretSuccessCounter 递增 secrets 处理成功计数器
func (m *metricMonitor) IncHandledSecretSuccessCounter(name, action string) {
	m.handledSecretSuccessTime = time.Now()
	handledSecretSuccessTotal.WithLabelValues(name, action).Inc()
}

// IncHandledSecretFailedCounter 递增 secrets 处理失败计数器
func (m *metricMonitor) IncHandledSecretFailedCounter(name, action string) {
	m.handledSecretFailed++
	m.handledSecretFailedTime = time.Now()
	handledSecretFailedTotal.WithLabelValues(name, action).Inc()
}

func (m *metricMonitor) IncHandledDiscoverNotifyCounter() {
	handledDiscoverNotifyTotal.Inc()
}

func (m *metricMonitor) SetActiveSecretFileCount(taskType, secretName string, count int) {
	activeSecretFileCount.WithLabelValues(taskType, secretName).Set(float64(count))
}

func (m *metricMonitor) IncSecretsExceededCounter() {
	secretsExceeded.Inc()
}

func (m *metricMonitor) IncDispatchedTaskCounter() {
	dispatchedTaskTotal.Inc()
}

func (m *metricMonitor) ObserveDispatchedTaskDuration(t time.Time) {
	dispatchedTaskDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncScaledStatefulSetFailedCounter() {
	scaledStatefulSetFailedTotal.Inc()
}

func (m *metricMonitor) IncScaledStatefulSetSuccessCounter() {
	scaledStatefulSetSuccessTotal.Inc()
}
