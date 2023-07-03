// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package workload

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
	workloadCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "workload_count",
			Help:      "workload count",
		},
		[]string{"namespace", "kind"},
	)

	workloadLookupRequestTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "workload_lookup_request_total",
			Help:      "workload lookup request total",
		},
	)

	workloadLookupDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: define.MonitorNamespace,
			Name:      "workload_lookup_duration_seconds",
			Help:      "workload lookup duration seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60},
		},
	)

	clusterNodeCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "cluster_node_count",
			Help:      "cluster node count",
		},
	)

	clusterVersion = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "k8s_cluster_version",
			Help:      "kubernetes server version",
		},
		[]string{"version"},
	)
)

func init() {
	prometheus.MustRegister(
		workloadCount,
		workloadLookupRequestTotal,
		workloadLookupDuration,
		clusterNodeCount,
		clusterVersion,
	)
}

type namespaceKind struct {
	namespace string
	kind      string
}

var (
	nsUpdated     time.Time
	nkWorkloadMut sync.Mutex
	nkWorkload    = map[namespaceKind]int{}
)

func GetWorkloadInfo() (map[string]int, time.Time) {
	ret := make(map[string]int)
	nkWorkloadMut.Lock()
	for k, v := range nkWorkload {
		ret[k.kind] += v
	}
	nkWorkloadMut.Unlock()
	return ret, nsUpdated
}

type metricMonitor struct{}

func newMetricMonitor() *metricMonitor {
	return &metricMonitor{}
}

func (mm *metricMonitor) SetWorkloadCount(v int, namespace, kind string) {
	nkWorkloadMut.Lock()
	nsUpdated = time.Now()
	nkWorkload[namespaceKind{namespace: namespace, kind: kind}] = v
	nkWorkloadMut.Unlock()

	workloadCount.WithLabelValues(namespace, kind).Set(float64(v))
}

func (mm *metricMonitor) ObserveWorkloadLookupDuration(t time.Time) {
	workloadLookupDuration.Observe(time.Since(t).Seconds())
}

func (mm *metricMonitor) IncWorkloadRequestCounter() {
	workloadLookupRequestTotal.Inc()
}

var (
	clusterNode          int
	clusterNodeUpdatedAt time.Time
)

func GetClusterNodeInfo() (int, time.Time) {
	return clusterNode, clusterNodeUpdatedAt
}

func incClusterNodeCount() {
	clusterNode++
	clusterNodeUpdatedAt = time.Now()
	clusterNodeCount.Inc()
}

func decClusterNodeCount() {
	clusterNode--
	clusterNodeUpdatedAt = time.Now()
	clusterNodeCount.Dec()
}

func setClusterVersion(v string) {
	clusterVersion.WithLabelValues(v).Set(1)
}
