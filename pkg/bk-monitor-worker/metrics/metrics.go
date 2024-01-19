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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	// API request metrics
	apiRequestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bmw_api_request_count",
			Help: "api request count",
		},
		[]string{"method", "path", "status"},
	)
	apiRequestCost = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_api_request_cost",
			Help: "api request cost time",
		},
		[]string{"method", "path"},
	)

	// task metrics
	taskCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bmw_task_count",
			Help: "task run count",
		},
		[]string{"name", "status"}, // name 包含类型
	)

	taskCostTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_task_cost",
			Help: "task run cost time",
		},
		[]string{"name"},
	)

	// APM task metric
	// apmPreCalcFilterEsQueryCount apm预计算任务过滤器返回true然后查询ES的次数
	apmPreCalcFilterEsQueryCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_apm_pre_calc_filter_es_query_count",
			Help: "apm pre calc filter es query count",
		},
		[]string{"data_id", "status"},
	)
	// apmPreCalcSaveRequestCount apm预计算任务存储需求次数
	apmPreCalcSaveRequestCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_apm_pre_calc_save_request_count",
			Help: "apm pre calc save request count",
		},
		[]string{"data_id", "storage_type"},
	)
	// apmPreCalcMessageCount apm预计算任务消息接收数量
	apmPreCalcMessageCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_apm_pre_calc_message_count",
			Help: "apm pre calc message count",
		},
		[]string{"data_id"},
	)
	// apmPreCalcWindowTraceCount apm预计算任务窗口trace数量
	apmPreCalcWindowTraceCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_apm_pre_calc_window_trace_count",
			Help: "apm pre calc window trace count",
		},
		[]string{"data_id", "distributive_window_id"},
	)
	// apmPreCalcWindowTraceCount apm预计算任务窗口span数量
	apmPreCalcWindowSpanCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bmw_apm_pre_calc_window_span_count",
			Help: "apm pre calc window span count",
		},
		[]string{"data_id", "distributive_window_id"},
	)
)

// RequestApiCount request api count metric
func RequestApiCount(method, apiPath, status string) error {
	metric, err := apiRequestCount.GetMetricWithLabelValues(method, apiPath, status)
	if err != nil {
		logger.Errorf("prom get request api count metric failed: %v", err)
		return err
	}
	metric.Inc()
	return nil
}

// RequestApiCostTime cost time of request api
func RequestApiCostTime(method, apiPath string, startTime time.Time) error {
	duringTime := time.Now().Sub(startTime).Seconds()
	metric, err := apiRequestCost.GetMetricWithLabelValues(method, apiPath)
	if err != nil {
		logger.Errorf("prom get request api time metric failed: %v", err)
		return err
	}
	metric.Set(duringTime)
	return nil
}

// RegisterTaskCount registered task count
func RegisterTaskCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "registered")
	if err != nil {
		logger.Errorf("prom get register task count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// EnqueueTaskCount enqueued task count
func EnqueueTaskCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "enqueue")
	if err != nil {
		logger.Errorf("prom get enqueue task count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RunTaskCount run task count
func RunTaskCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "received")
	if err != nil {
		logger.Errorf("prom get run task count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RunTaskSuccessCount task success count
func RunTaskSuccessCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "success")
	if err != nil {
		logger.Errorf("prom get run task success count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RunTaskFailureCount task failure count
func RunTaskFailureCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "failure")
	if err != nil {
		logger.Errorf("prom get run task failure count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RunTaskCostTime cost time of task, duration(ms)
func RunTaskCostTime(taskName string, startTime time.Time) error {
	duringTime := time.Now().Sub(startTime).Seconds()
	metric, err := taskCostTime.GetMetricWithLabelValues(taskName)
	if err != nil {
		logger.Errorf("prom get run task count time metric failed: %s", err)
		return err
	}
	metric.Set(duringTime)
	return nil
}

// RunApmPreCalcFilterEsQuery APM预计算ES查询次数指标 + 1
func RunApmPreCalcFilterEsQuery(dataId, status string) error {
	metric, err := apmPreCalcFilterEsQueryCount.GetMetricWithLabelValues(dataId, status)
	if err != nil {
		logger.Errorf("prom get apm pre calc filter es query count metric failed: %s", err)
		return err
	}

	metric.Inc()
	return nil
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

// 设置 api 请求的耗时
func SetApiRequestCostTime(method, apiPath string) func() {
	start := time.Now()
	return func() {
		RequestApiCostTime(method, apiPath, start)
	}
}

// 设置任务的耗时
func SetTaskCostTime(taskName string) func() {
	start := time.Now()
	return func() {
		RunTaskCostTime(taskName, start)
	}
}

// metadata metrics
var (
	//consul数据操作统计
	consulCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "metadata_consul_count",
			Help: "consul execute count",
		},
		[]string{"key", "operation"},
	)

	//GSE变动统计
	gseCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "metadata_gse_count",
			Help: "gse change count",
		},
		[]string{"dataid", "operation"},
	)

	//ES变动统计
	esCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "metadata_es_count",
			Help: "es change count",
		},
		[]string{"table_id", "operation"},
	)

	// redis数据操作统计
	redisCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "metadata_redis_count",
			Help: "redis change count",
		},
		[]string{"key", "operation"},
	)

	// mysql数据操作统计
	mysqlCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "metadata_mysql_count",
			Help: "mysql change count",
		},
		[]string{"table", "operation"},
	)
)

// ConsulPutCount consul put count
func ConsulPutCount(key string) error {
	metric, err := consulCount.GetMetricWithLabelValues(key, "PUT")
	if err != nil {
		logger.Errorf("prom get consul put count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// ConsulDeleteCount consul delete count
func ConsulDeleteCount(key string) error {
	metric, err := consulCount.GetMetricWithLabelValues(key, "DELETE")
	if err != nil {
		logger.Errorf("prom get consul delete count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// GSEUpdateCount gse update count
func GSEUpdateCount(dataid uint) error {
	metric, err := gseCount.GetMetricWithLabelValues(strconv.Itoa(int(dataid)), "UPDATE")
	if err != nil {
		logger.Errorf("prom get gse update count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// ESChangeCount es change count
func ESChangeCount(tableId, operation string) error {
	metric, err := esCount.GetMetricWithLabelValues(tableId, operation)
	if err != nil {
		logger.Errorf("prom get es change count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RedisCount redis count
func RedisCount(key, operation string) error {
	metric, err := redisCount.GetMetricWithLabelValues(key, operation)
	if err != nil {
		logger.Errorf("prom get redis count metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// MysqlCount mysql count
func MysqlCount(tableName, operation string, count float64) error {
	metric, err := mysqlCount.GetMetricWithLabelValues(tableName, operation)
	if err != nil {
		logger.Errorf("prom get mysql count metric failed: %s", err)
		return err
	}
	metric.Add(count)
	return nil
}

var Registry *prometheus.Registry

func init() {
	// register the metrics
	Registry = prometheus.NewRegistry()
	Registry.MustRegister(
		apiRequestCount,
		apiRequestCost,
		taskCount,
		taskCostTime,
		apmPreCalcFilterEsQueryCount,
		apmPreCalcSaveRequestCount,
		apmPreCalcMessageCount,
		apmPreCalcWindowTraceCount,
		apmPreCalcWindowSpanCount,
		consulCount,
		gseCount,
		esCount,
		redisCount,
		mysqlCount,
	)
}
