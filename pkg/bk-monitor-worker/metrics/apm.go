// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var apmTaskNamespace = "bmw_apm_pre_calc"

var (
	// APM task metric
	// apmPreCalcFilterEsQueryCount apm预计算任务过滤器返回true然后查询ES的次数
	apmPreCalcFilterEsQueryCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: apmTaskNamespace,
			Name: "filter_es_query_count",
			Help: "apm pre calc filter es query count",
		},
		[]string{"data_id", "status"},
	)
	// apmPreCalcSaveRequestCount apm预计算任务存储需求次数
	apmPreCalcSaveRequestCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: apmTaskNamespace,
			Name: "save_request_count",
			Help: "apm pre calc save request count",
		},
		[]string{"data_id", "storage_type"},
	)
	// apmPreCalcMessageCount apm预计算任务消息接收数量
	apmPreCalcMessageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: apmTaskNamespace,
			Name: "message_count",
			Help: "apm pre calc message count",
		},
		[]string{"data_id"},
	)
	// apmPreCalcWindowTraceCount apm预计算任务窗口trace数量
	apmPreCalcWindowTraceCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "bmw",
			Name: "window_trace_count",
			Help: "apm pre calc window trace count",
		},
		[]string{"data_id", "distributive_window_id"},
	)
	// apmPreCalcWindowTraceCount apm预计算任务窗口span数量
	apmPreCalcWindowSpanCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "bmw",
			Name: "window_span_count",
			Help: "apm pre calc window span count",
		},
		[]string{"data_id", "distributive_window_id"},
	)
)

// RunApmPreCalcFilterEsQuery APM预计算ES查询次数指标 + 1
func RunApmPreCalcFilterEsQuery(dataId, status string) {
	metric, err := apmPreCalcFilterEsQueryCount.GetMetricWithLabelValues(dataId, status)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}

	metric.Inc()
}

// IncreaseApmSaveRequestCount APM预计算ES存储请求指标 + 1
func IncreaseApmSaveRequestCount(dataId, storageType string) {
	metric, err := apmPreCalcSaveRequestCount.GetMetricWithLabelValues(dataId, storageType)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// DecreaseApmSaveRequestCount APM预计算ES存储请求指标 - 1
func DecreaseApmSaveRequestCount(dataId, storageType string) {
	metric, err := apmPreCalcSaveRequestCount.GetMetricWithLabelValues(dataId, storageType)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Dec()
}

// IncreaseApmMessageChanCount APM预计算ES存储请求指标 + 1
func IncreaseApmMessageChanCount(dataId string) {
	metric, err := apmPreCalcMessageCount.GetMetricWithLabelValues(dataId)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// DecreaseApmMessageChanCount APM预计算ES存储请求指标 - 1
func DecreaseApmMessageChanCount(dataId string) {
	metric, err := apmPreCalcMessageCount.GetMetricWithLabelValues(dataId)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Dec()
}

// IncreaseApmWindowsTraceCount APM预计算窗口Trace数量指标 + 1
func IncreaseApmWindowsTraceCount(dataId, id string) {
	metric, err := apmPreCalcWindowTraceCount.GetMetricWithLabelValues(dataId, id)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// DecreaseApmWindowsTraceCount APM预计算窗口Trace数量指标 - 1
func DecreaseApmWindowsTraceCount(dataId, id string) {
	metric, err := apmPreCalcWindowTraceCount.GetMetricWithLabelValues(dataId, id)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Dec()
}

// IncreaseApmWindowsSpanCount APM预计算窗口Span数量指标 + 1
func IncreaseApmWindowsSpanCount(dataId, id string) {
	metric, err := apmPreCalcWindowSpanCount.GetMetricWithLabelValues(dataId, id)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// DecreaseApmWindowsSpanCount APM预计算窗口Span数量指标 - n
func DecreaseApmWindowsSpanCount(dataId, id string, n int) {
	metric, err := apmPreCalcWindowSpanCount.GetMetricWithLabelValues(dataId, id)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return
	}
	metric.Sub(float64(n))
}