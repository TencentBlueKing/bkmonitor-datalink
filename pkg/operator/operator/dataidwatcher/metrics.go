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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

var (
	dataIDInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_info",
			Help:      "dataid information",
		},
		[]string{"id", "name", "usage", "system", "common", "bk_env"},
	)

	watcherHandledTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitorNamespace,
			Name:      "dataid_watcher_handled_total",
			Help:      "dataid watcher handled kubernetes event total",
		},
		[]string{"action"},
	)
)

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
	dataIDInfo.WithLabelValues(fmt.Sprintf("%d", id), name, usage, conv(system), conv(common), configs.G().BkEnv).Set(1)
}

func (m *metricMonitor) IncHandledCounter(action string) {
	watcherHandledTotal.WithLabelValues(action).Inc()
}
