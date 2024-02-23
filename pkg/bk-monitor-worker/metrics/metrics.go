// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// basic metric for bmw
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var bmwMetricNamespace = "bmw"

var (
	// API request metrics
	apiRequestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: bmwMetricNamespace,
			Name:      "api_request_count",
			Help:      "api request count",
		},
		[]string{"method", "path", "status"},
	)
	apiRequestCost = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: bmwMetricNamespace,
			Name:      "api_request_cost",
			Help:      "api request cost time",
		},
		[]string{"method", "path"},
	)

	// task metrics
	taskCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: bmwMetricNamespace,
			Name:      "task_count",
			Help:      "task run count",
		},
		[]string{"name", "status"}, // name 包含类型
	)

	taskCostTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: bmwMetricNamespace,
			Name:      "task_cost",
			Help:      "task run cost time",
		},
		[]string{"name"},
	)
)

// RequestApiCount request api count metric
func RequestApiCount(method, apiPath, status string) {
	metric, err := apiRequestCount.GetMetricWithLabelValues(method, apiPath, status)
	if err != nil {
		logger.Errorf("prom get request api count metric failed: %v", err)
		return
	}
	metric.Inc()
}

// RequestApiCostTime cost time of request api
func RequestApiCostTime(method, apiPath string, startTime time.Time) {
	duringTime := time.Now().Sub(startTime).Seconds()
	metric, err := apiRequestCost.GetMetricWithLabelValues(method, apiPath)
	if err != nil {
		logger.Errorf("prom get request api time metric failed: %v", err)
		return
	}
	metric.Set(duringTime)
}

// RegisterTaskCount registered task count
func RegisterTaskCount(taskName string) {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "registered")
	if err != nil {
		logger.Errorf("prom get register task count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// EnqueueTaskCount enqueued task count
func EnqueueTaskCount(taskName string) {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "enqueue")
	if err != nil {
		logger.Errorf("prom get enqueue task count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskCount run task count
func RunTaskCount(taskName string) {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "received")
	if err != nil {
		logger.Errorf("prom get run task count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskSuccessCount task success count
func RunTaskSuccessCount(taskName string) {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "success")
	if err != nil {
		logger.Errorf("prom get run task success count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskFailureCount task failure count
func RunTaskFailureCount(taskName string) {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "failure")
	if err != nil {
		logger.Errorf("prom get run task failure count metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskCostTime cost time of task, duration(ms)
func RunTaskCostTime(taskName string, startTime time.Time) {
	duringTime := time.Now().Sub(startTime).Seconds()
	metric, err := taskCostTime.GetMetricWithLabelValues(taskName)
	if err != nil {
		logger.Errorf("prom get run task count time metric failed: %s", err)
		return
	}
	metric.Set(duringTime)
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

var Registry *prometheus.Registry

func init() {
	// register the metrics
	Registry.MustRegister(
		apiRequestCount,
		apiRequestCost,
		taskCount,
		taskCostTime,
	)
}
