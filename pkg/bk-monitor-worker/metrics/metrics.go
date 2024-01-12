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
			Help: "es change count",
		},
		[]string{"key", "operation"},
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

var Registry *prometheus.Registry

func init() {
	// register the metrics
	Registry = prometheus.NewRegistry()
	Registry.MustRegister(apiRequestCount, apiRequestCost, taskCount, taskCostTime, consulCount, gseCount, esCount, redisCount)
}
