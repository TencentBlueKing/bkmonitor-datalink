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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	appUptime = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "uptime",
			Help:      "uptime of program",
		},
	)

	appBuildInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "build_info",
			Help:      "build information of app",
		},
		[]string{"version", "git_hash", "build_time"},
	)

	activeChildConfigCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_child_config_count",
			Help:      "active child config count",
		},
		[]string{"node"},
	)

	activeSharedDiscoveryCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_shared_discovery_count",
			Help:      "active shared discovery count",
		},
	)

	activeMonitorResourceCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_monitor_resource_count",
			Help:      "active monitor resource count",
		},
		[]string{"kind"},
	)

	receivedK8sEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "received_k8s_event_total",
			Help:      "received k8s event total",
		},
		[]string{"monitor_kind", "action"},
	)

	handledK8sEventTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_k8s_event_total",
			Help:      "handled k8s event total",
		},
		[]string{"monitor_kind", "action"},
	)

	handledK8sEventDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_k8s_event_duration_seconds",
			Help:      "handled k8s event duration seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300},
		},
		[]string{"monitor_kind", "action"},
	)

	handledSecretSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_success_total",
			Help:      "handled secret success total",
		},
		[]string{"secret_name", "action"},
	)

	handledSecretFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_failed_total",
			Help:      "handled secret failed total",
		},
		[]string{"secret_name", "action"},
	)

	dispatchedTaskTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_total",
			Help:      "dispatched task total",
		},
	)

	skippedSecretTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "skipped_secret_total",
			Help:      "skipped_secret_total",
		},
		[]string{"task_type", "secret_name"},
	)

	dispatchedTaskDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_duration_seconds",
			Help:      "dispatched task duration seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300},
		},
	)

	compressedConfigFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "compressed_config_failed_total",
			Help:      "compressed config failed total",
		},
		[]string{"task_type", "secret_name"},
	)

	handledDiscoverNotifyTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_discover_notify_total",
			Help:      "handled discover notify total",
		},
	)

	handledDataIDWatcherNotifyTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_dataid_watcher_notify_total",
			Help:      "handled dataid watcher notify total",
		},
	)

	reloadedDiscoverDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reloaded_discover_duration_seconds",
			Help:      "reloaded discover duration seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300},
		},
	)

	activeSecretFileCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_secret_file_count",
			Help:      "active secret file count",
		},
		[]string{"task_type", "secret_name"},
	)

	activeSecretBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "active_secret_bytes",
			Help:      "active secret bytes",
		},
		[]string{"task_type", "secret_name"},
	)

	secretsExceeded = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "secrets_exceeded",
			Help:      "secrets exceeded",
		},
	)

	reconciledNodeEndpointsSuccessTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reconciled_node_endpoints_success_total",
			Help:      "reconciled node endpoints success total",
		},
	)

	reconciledNodeEndpointsFailedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "reconciled_node_endpoints_failed_total",
			Help:      "reconciled node endpoints failed total",
		},
	)

	scrapeLinesCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scraped_lines_count",
			Help:      "scraped lines count",
		},
		[]string{"name"},
	)

	scrapeErrorsCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scraped_errors_count",
			Help:      "scraped errors count",
		},
		[]string{"name"},
	)

	scrapeDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scraped_duration_seconds",
			Help:      "scraped duration seconds",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600},
		},
	)

	scaledStatefulSetFailedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scaled_statefulset_failed_total",
			Help:      "scaled statefulset replicas failed total",
		},
	)

	scaledStatefulSetSuccessTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "scaled_statefulset_success_total",
			Help:      "scaled statefulset replicas success total",
		},
	)
)

func init() {
	prometheus.MustRegister(
		appUptime,
		appBuildInfo,
		activeChildConfigCount,
		activeSharedDiscoveryCount,
		activeMonitorResourceCount,
		activeSecretFileCount,
		activeSecretBytes,
		receivedK8sEventTotal,
		handledK8sEventTotal,
		handledK8sEventDuration,
		handledSecretSuccessTotal,
		handledSecretFailedTotal,
		handledDiscoverNotifyTotal,
		handledDataIDWatcherNotifyTotal,
		reloadedDiscoverDuration,
		skippedSecretTotal,
		dispatchedTaskTotal,
		dispatchedTaskDuration,
		compressedConfigFailedTotal,
		secretsExceeded,
		reconciledNodeEndpointsSuccessTotal,
		reconciledNodeEndpointsFailedTotal,
		scrapeLinesCount,
		scrapeErrorsCount,
		scrapeDuration,
		scaledStatefulSetFailedTotal,
		scaledStatefulSetSuccessTotal,
	)
}

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

// IncReceivedK8sEventCounter 增加接收 k8s 事件计数器
func (m *metricMonitor) IncReceivedK8sEventCounter(monitorKing, action string) {
	m.receivedK8sEvent++
	receivedK8sEventTotal.WithLabelValues(monitorKing, action).Inc()
}

// IncHandledK8sEventCounter 递增处理 k8s 事件计数器
func (m *metricMonitor) IncHandledK8sEventCounter(monitorKing, action string) {
	m.handledK8sEvent++
	handledK8sEventTotal.WithLabelValues(monitorKing, action).Inc()
}

// ObserveHandledK8sEventDuration 观测 k8s 事件处理耗时
func (m *metricMonitor) ObserveHandledK8sEventDuration(t time.Time, monitorKing, action string) {
	handledK8sEventDuration.WithLabelValues(monitorKing, action).Observe(time.Since(t).Seconds())
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

func (m *metricMonitor) IncHandledDataIDWatcherNotifyCounter() {
	handledDataIDWatcherNotifyTotal.Inc()
}

func (m *metricMonitor) ObserveReloadedDiscoverDuration(t time.Time) {
	reloadedDiscoverDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) SetActiveSecretFileCount(taskType, secretName string, count int) {
	activeSecretFileCount.WithLabelValues(taskType, secretName).Set(float64(count))
}

func (m *metricMonitor) SetActiveSecretBytes(taskType, secretName string, count int) {
	activeSecretBytes.WithLabelValues(taskType, secretName).Set(float64(count))
}

func (m *metricMonitor) IncSecretsExceededCounter() {
	secretsExceeded.Inc()
}

func (m *metricMonitor) IncSkippedSecretCounter(taskType, secretName string) {
	skippedSecretTotal.WithLabelValues(taskType, secretName).Inc()
}

func (m *metricMonitor) IncDispatchedTaskCounter() {
	dispatchedTaskTotal.Inc()
}

func (m *metricMonitor) ObserveDispatchedTaskDuration(t time.Time) {
	dispatchedTaskDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncCompressedConfigFailedCounter(taskType, secretName string) {
	compressedConfigFailedTotal.WithLabelValues(taskType, secretName).Inc()
}

func (m *metricMonitor) IncReconciledNodeEndpointsSuccessCounter() {
	reconciledNodeEndpointsSuccessTotal.Inc()
}

func (m *metricMonitor) IncReconciledNodeEndpointsFailedCounter() {
	reconciledNodeEndpointsFailedTotal.Inc()
}

func (m *metricMonitor) SetScrapedLinesCount(name string, n int) {
	scrapeLinesCount.WithLabelValues(name).Set(float64(n))
}

func (m *metricMonitor) SetScrapedErrorsCount(name string, n int) {
	scrapeErrorsCount.WithLabelValues(name).Set(float64(n))
}

func (m *metricMonitor) ObserveScrapedDuration(t time.Time) {
	scrapeDuration.Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncScaledStatefulSetFailedCounter() {
	scaledStatefulSetFailedTotal.Inc()
}

func (m *metricMonitor) IncScaledStatefulSetSuccessCounter() {
	scaledStatefulSetSuccessTotal.Inc()
}
