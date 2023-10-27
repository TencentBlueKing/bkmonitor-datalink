// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

var (
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
			Buckets:   define.DefObserveDuration,
		},
	)

	clusterVersion = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "cluster_version",
			Help:      "kubernetes server version",
		},
		[]string{"version"},
	)
)

func init() {
	prometheus.MustRegister(
		workloadLookupRequestTotal,
		workloadLookupDuration,
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
}

func decClusterNodeCount() {
	clusterNode--
	clusterNodeUpdatedAt = time.Now()
}

func setClusterVersion(v string) {
	clusterVersion.WithLabelValues(v).Set(1)
}
