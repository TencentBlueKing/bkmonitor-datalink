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
	clusterVersion = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "cluster_version",
			Help:      "kubernetes server version",
		},
		[]string{"version"},
	)

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

	nodeConfigCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "node_config_count",
			Help:      "node configs count",
		},
		[]string{"node"},
	)

	monitorEndpointCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "monitor_endpoint_count",
			Help:      "monitor endpoint count",
		},
		[]string{"name"},
	)

	workloadCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "workload_count",
			Help:      "workload count",
		},
		[]string{"resource"},
	)

	nodeCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "node_count",
			Help:      "node count",
		},
	)

	sharedDiscoveryCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "shared_discovery_count",
			Help:      "shared discovery count",
		},
	)

	discoverCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "discover_count",
			Help:      "discover count",
		},
	)

	handledSecretSuccessTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_success_total",
			Help:      "handled secret success total",
		},
		[]string{"secret", "action"},
	)

	handledSecretFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "handled_secret_failed_total",
			Help:      "handled secret failed total",
		},
		[]string{"secret", "action"},
	)

	dispatchedTaskTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_total",
			Help:      "dispatched task total",
		},
		[]string{"trigger"},
	)

	dispatchedTaskDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dispatched_task_duration_seconds",
			Help:      "dispatched task duration seconds",
			Buckets:   define.DefObserveDuration,
		},
		[]string{"trigger"},
	)

	secretsExceeded = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "secrets_exceeded",
			Help:      "secrets exceeded",
		},
	)

	statefulSetWorkerCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "statefulset_workers",
			Help:      "statefulset workers count",
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
	handledSecretFailed      int       // 记录 secrets 处理失败次数
	handledSecretFailedTime  time.Time // 记录 secrets 处理失败时间
	handledSecretSuccessTime time.Time // 记录 secrets 处理成功时间
}

func (m *metricMonitor) UpdateUptime(n int) {
	appUptime.Add(float64(n))
}

func (m *metricMonitor) SetAppBuildInfo(info BuildInfo) {
	appBuildInfo.WithLabelValues(info.Version, info.GitHash, info.Time).Set(1)
}

func (m *metricMonitor) SetNodeConfigCount(node string, n int) {
	nodeConfigCount.WithLabelValues(node).Set(float64(n))
}

func (m *metricMonitor) SetMonitorEndpointCount(name string, n int) {
	monitorEndpointCount.WithLabelValues(name).Set(float64(n))
}

func (m *metricMonitor) SetWorkloadCount(resource string, n int) {
	workloadCount.WithLabelValues(resource).Set(float64(n))
}

func (m *metricMonitor) SetNodeCount(n int) {
	nodeCount.Set(float64(n))
}

func (m *metricMonitor) SetSharedDiscoveryCount(n int) {
	sharedDiscoveryCount.Set(float64(n))
}

func (m *metricMonitor) SetDiscoverCount(n int) {
	discoverCount.Set(float64(n))
}

func (m *metricMonitor) IncHandledSecretSuccessCounter(name, action string) {
	m.handledSecretSuccessTime = time.Now()
	handledSecretSuccessTotal.WithLabelValues(name, action).Inc()
}

func (m *metricMonitor) IncHandledSecretFailedCounter(name, action string) {
	m.handledSecretFailed++
	m.handledSecretFailedTime = time.Now()
	handledSecretFailedTotal.WithLabelValues(name, action).Inc()
}

func (m *metricMonitor) IncDispatchedTaskCounter(trigger string) {
	dispatchedTaskTotal.WithLabelValues(trigger).Inc()
}

func (m *metricMonitor) IncSecretsExceededCounter() {
	secretsExceeded.Inc()
}

func (m *metricMonitor) ObserveDispatchedTaskDuration(trigger string, t time.Time) {
	dispatchedTaskDuration.WithLabelValues(trigger).Observe(time.Since(t).Seconds())
}

func (m *metricMonitor) IncScaledStatefulSetFailedCounter() {
	scaledStatefulSetFailedTotal.Inc()
}

func (m *metricMonitor) IncScaledStatefulSetSuccessCounter() {
	scaledStatefulSetSuccessTotal.Inc()
}

func (m *metricMonitor) SetStatefulSetWorkerCount(count int) {
	statefulSetWorkerCount.Set(float64(count))
}

func (m *metricMonitor) SetKubernetesVersion(v string) {
	clusterVersion.WithLabelValues(v).Set(1)
}
