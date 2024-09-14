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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	ApmNamespace      = "bmw_apm_pre_calc"
	defDurationBucket = []float64{
		0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1000, 1500, 2000, 3000, 5000,
	}

	// apmPreCalcNotifierReceiveMessageCount apm预计算任务接收数量
	apmPreCalcNotifierReceiveMessageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "notifier_receive_message_count",
			Help:      "notifier receive message count",
		},
		[]string{"data_id", "topic"},
	)
	// apmPreCalcNotifierRejectMessageCount apm预计算任务拒绝数量(触发限流)
	apmPreCalcNotifierRejectMessageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "notifier_reject_message_count",
			Help:      "notifier reject message count",
		},
		[]string{"data_id", "topic"},
	)
	apmPreCalcParseSpanDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: ApmNamespace,
			Name:      "notifier_parse_span_duration",
			Help:      "notifier parse span duration",
			Buckets:   defDurationBucket,
		},
		[]string{"data_id", "topic"},
	)

	TaskProcessChan          = "task_process_chan"
	WindowProcessEventChan   = "window_process_event_chan"
	SaveRequestChan          = "save_request_chan"
	apmPreCalcSemaphoreTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "semaphore_total",
			Help:      "semaphore total",
		},
		[]string{"data_id", "scene"},
	)

	apmPreCalcProcessEventDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: ApmNamespace,
			Name:      "process_event_duration",
			Help:      "process event duration",
			Buckets:   defDurationBucket,
		},
		[]string{"data_id", "sub_window_id"},
	)

	QueryBloomFilterFailed    = "query_bloom_filter_failed"
	QueryCacheResponseInvalid = "query_cache_response_invalid"
	QueryEsFailed             = "query_es_failed"
	QueryEsReturnEmpty        = "query_es_return_empty"
	QueryESResponseInvalid    = "query_es_response_invalid"
	SaveEsFailed              = "save_es_failed"
	SaveCacheFailed           = "save_cache_failed"
	SaveBloomFilterFailed     = "save_bloom_filter_failed"
	SavePrometheusFailed      = "save_prometheus_failed"
	// apmPreCalcOperateStorageFailedTotal apm预计算对 trace 进行预计算时发生存储层查询/保存失败的计数指标
	apmPreCalcOperateStorageFailedTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "operate_storage_failed",
			Help:      "operate event storage failed",
		},
		[]string{"data_id", "error"},
	)

	StorageSaveEs      = "save_es"
	StorageTraceEs     = "trace_es"
	StorageCache       = "cache"
	StorageBloomFilter = "bloom_filter"
	StoragePrometheus  = "prometheus"
	OperateSave        = "save"
	OperateQuery       = "query"
	// apmPreCalcOperateStorageCount APM 预计算查询存储层的保存次数
	apmPreCalcOperateStorageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "operate_storage_count",
			Help:      "operate storage count",
		},
		[]string{"data_id", "storage", "operate"},
	)
	// apmPreCalcSaveStorageTotal APM 预计算保存存储层的保存数量
	apmPreCalcSaveStorageTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "save_storage_total",
			Help:      "save storage total",
		},
		[]string{"data_id", "storage"},
	)

	// apmPreCalcExpiredKeyTotal APM 预计算过期 traceId 数量
	apmPreCalcExpiredKeyTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "expired_key_total",
			Help:      "expired key total",
		},
		[]string{"data_id", "sub_window_id"},
	)

	// apmPreCalcLocateSpanDuration apm 预计算分配 span 到窗口的耗时
	apmPreCalcLocateSpanDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: ApmNamespace,
			Name:      "locate_span_duration",
			Help:      "locate span duration",
			Buckets:   defDurationBucket,
		},
		[]string{"data_id"},
	)

	LimiterEs = "limiter_es"
	// apmPreCalcRateLimitedCount apm 预计算 ES/KAFKA 触发限流而拒绝的计数
	apmPreCalcRateLimitedCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "rate_limited",
			Help:      "rate limited",
		},
		[]string{"data_id", "limiter_type"},
	)

	// **** APM 父子窗口实现指标
	// apmPreCalcWindowTraceTotal trace count of distributive windows
	apmPreCalcWindowTraceTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "window_trace_count",
			Help:      "window trace count",
		},
		[]string{"data_id", "sub_window_id"},
	)
	// apmPreCalcWindowSpanTotal apm预计算任务窗口span数量
	apmPreCalcWindowSpanTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "window_span_count",
			Help:      "window span count",
		},
		[]string{"data_id", "sub_window_id"},
	)

	apmRelationMetricFindCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "relation_metric_find_count",
			Help:      "relation metric find count",
		},
		[]string{"data_id", "metric"},
	)

	apmQueueSpanDelta = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "queue_message_delta",
			Help:      "queue_message_delta",
		},
		[]string{"data_id"},
	)

	apmHandleTraceDelta = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: ApmNamespace,
			Name:      "handle_trace_delta",
			Help:      "handle_trace_delta",
		},
		[]string{"data_id"},
	)
)

func RecordQueueSpanDelta(dataId string, t int) {
	delta := time.Now().UnixMicro() - int64(t)
	apmQueueSpanDelta.WithLabelValues(dataId).Set(float64(delta))
}

func RecordHandleTraceDelta(dataId string, t int) {
	delta := time.Now().UnixMicro() - int64(t)
	apmHandleTraceDelta.WithLabelValues(dataId).Set(float64(delta))
}

func RecordApmRelationMetricFindCount(dataId, metric string, n int) {
	apmRelationMetricFindCount.WithLabelValues(dataId, metric).Add(float64(n))
}

func AddApmPreCalcRateLimitedCount(dataId, limiter string) {
	apmPreCalcRateLimitedCount.WithLabelValues(dataId, limiter).Add(1)
}

func RecordApmPreCalcLocateSpanDuration(dataId string, t time.Time) {
	apmPreCalcLocateSpanDuration.WithLabelValues(dataId).Observe(time.Since(t).Seconds())
}

func RecordApmPreCalcExpiredKeyTotal(dataId string, subWindowId int, n int) {
	apmPreCalcExpiredKeyTotal.WithLabelValues(dataId, strconv.Itoa(subWindowId)).Add(float64(n))
}

func RecordApmPreCalcSaveStorageTotal(dataId, storage string, n int) {
	apmPreCalcSaveStorageTotal.WithLabelValues(dataId, storage).Add(float64(n))
}

func RecordApmPreCalcOperateStorageCount(dataId, storage, operate string) {
	apmPreCalcOperateStorageCount.WithLabelValues(dataId, storage, operate).Add(1)
}

func RecordApmPreCalcOperateStorageFailedTotal(dataId, error string) {
	apmPreCalcOperateStorageFailedTotal.WithLabelValues(dataId, error).Add(1)
}

func RecordApmPreCalcProcessEventDuration(dataId string, subWindowId int, t time.Time) {
	apmPreCalcProcessEventDuration.WithLabelValues(dataId, strconv.Itoa(subWindowId)).Observe(time.Since(t).Seconds())
}

func RecordNotifierParseSpanDuration(dataId, topic string, t time.Time) {
	apmPreCalcParseSpanDuration.WithLabelValues(dataId, topic).Observe(time.Since(t).Seconds())
}

func RecordApmPreCalcSemaphoreTotal(dataId, scene string, n int) {
	apmPreCalcSemaphoreTotal.WithLabelValues(dataId, scene).Set(float64(n))
}

// AddApmNotifierReceiveMessageCount apm预计算任务接收数量指标 + 1
func AddApmNotifierReceiveMessageCount(dataId, topic string) {
	apmPreCalcNotifierReceiveMessageCount.WithLabelValues(dataId, topic).Inc()
}

// AddApmPreCalcNotifierRejectMessageCount apm预计算任务拒绝数量指标 + 1
func AddApmPreCalcNotifierRejectMessageCount(dataId, topic string) {
	apmPreCalcNotifierRejectMessageCount.WithLabelValues(dataId, topic).Inc()
}

func RecordApmPreCalcWindowTraceTotal(dataId string, subWindowId int, n int) {
	apmPreCalcWindowTraceTotal.WithLabelValues(dataId, strconv.Itoa(subWindowId)).Set(float64(n))
}

func RecordApmPreCalcWindowSpanTotal(dataId string, subWindowId int, n int) {
	apmPreCalcWindowSpanTotal.WithLabelValues(dataId, strconv.Itoa(subWindowId)).Set(float64(n))
}

func init() {
	// register the metrics
	Registry.MustRegister(
		apmPreCalcNotifierReceiveMessageCount,
		apmPreCalcParseSpanDuration,
		apmPreCalcSemaphoreTotal,
		apmPreCalcProcessEventDuration,
		apmPreCalcOperateStorageFailedTotal,
		apmPreCalcOperateStorageCount,
		apmPreCalcSaveStorageTotal,
		apmPreCalcExpiredKeyTotal,
		apmPreCalcLocateSpanDuration,
		apmPreCalcWindowTraceTotal,
		apmPreCalcWindowSpanTotal,
	)
}
