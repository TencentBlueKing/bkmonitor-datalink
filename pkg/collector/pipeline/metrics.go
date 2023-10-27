// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	builtFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_built_failed_total",
			Help:      "Pipeline built failed total",
		},
		[]string{"pipeline", "record_type"},
	)

	builtSuccessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: define.MonitoringNamespace,
			Name:      "pipeline_built_success_total",
			Help:      "Pipeline built success total",
		},
		[]string{"pipeline", "record_type"},
	)
)

func init() {
	prometheus.MustRegister(
		builtFailedTotal,
		builtSuccessTotal,
	)
}

var DefaultMetricMonitor = &metricMonitor{}

type metricMonitor struct{}

func (m *metricMonitor) IncBuiltFailedCounter(pipeline, recordType string) {
	builtFailedTotal.WithLabelValues(pipeline, recordType).Inc()
}

func (m *metricMonitor) IncBuiltSuccessCounter(pipeline, recordType string) {
	builtSuccessTotal.WithLabelValues(pipeline, recordType).Inc()
}
