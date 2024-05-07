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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const bmwMetricNamespace = "bmw"

var (
	// API request metrics
	apiRequestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: bmwMetricNamespace,
			Name:      "api_request_total",
			Help:      "api request total",
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
	taskTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: bmwMetricNamespace,
			Name:      "task_total",
			Help:      "task run total",
		},
		[]string{"name", "module", "status"}, // name 包含类型
	)

	taskDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: bmwMetricNamespace,
			Name:      "task_duration_seconds",
			Help:      "task run cost time",
			Buckets:   []float64{0.1, 3, 10, 15, 20, 30, 60, 90, 120, 150, 180, 300, 600, 900, 1200, 1500, 1800, 3600, 7200},
		},
		[]string{"name"},
	)

	// 常驻任务正在运行的任务统计
	daemonRunningTaskCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "daemon_running_task_count",
			Help: "daemon running task count",
		},
		[]string{"task_dimension"},
	)

	// 常驻任务任务重试次数
	daemonTaskRetryCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "daemon_task_retry_count",
			Help: "daemon task retry count",
		},
		[]string{"task_dimension"},
	)
)

// RequestApiTotal request api total metric
func RequestApiTotal(method, apiPath, status string) {
	metric, err := apiRequestTotal.GetMetricWithLabelValues(method, apiPath, status)
	if err != nil {
		logger.Errorf("prom get request api total metric failed: %v", err)
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

// RegisterTaskTotal registered task total
func RegisterTaskTotal(taskName string, moduleName string) {
	metric, err := taskTotal.GetMetricWithLabelValues(taskName, moduleName, "registered")
	if err != nil {
		logger.Errorf("prom get register task total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// EnqueueTaskTotal enqueued task total
func EnqueueTaskTotal(taskName string) {
	metric, err := taskTotal.GetMetricWithLabelValues(taskName, common.ScheduleModuleName, "enqueue")
	if err != nil {
		logger.Errorf("prom get enqueue task total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskTotal run task total
func RunTaskTotal(taskName string) {
	metric, err := taskTotal.GetMetricWithLabelValues(taskName, common.WorkerModuleName, "received")
	if err != nil {
		logger.Errorf("prom get run task total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskSuccessTotal task success total
func RunTaskSuccessTotal(taskName string) {
	metric, err := taskTotal.GetMetricWithLabelValues(taskName, common.WorkerModuleName, "success")
	if err != nil {
		logger.Errorf("prom get run task success total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskFailureTotal task failure total
func RunTaskFailureTotal(taskName string) {
	metric, err := taskTotal.GetMetricWithLabelValues(taskName, common.WorkerModuleName, "failure")
	if err != nil {
		logger.Errorf("prom get run task failure total metric failed: %s", err)
		return
	}
	metric.Inc()
}

// RunTaskDurationSeconds task cost duration
func RunTaskDurationSeconds(taskName string, startTime time.Time) {
	metric, err := taskDurationSeconds.GetMetricWithLabelValues(taskName)
	if err != nil {
		logger.Errorf("prom get run task count time metric failed: %s", err)
		return
	}
	metric.Observe(time.Since(startTime).Seconds())
}

// 设置 api 请求的耗时
func SetApiRequestCostTime(method, apiPath string) func() {
	start := time.Now()
	return func() {
		RequestApiCostTime(method, apiPath, start)
	}
}

func RecordDaemonTask(dimension string) {
	metric, err := daemonRunningTaskCount.GetMetricWithLabelValues(dimension)
	if err != nil {
		logger.Errorf("prom get [daemonRunningTaskCount] metric failed: %s", err)
		return
	}
	metric.Set(1)
}

func RecordDaemonTaskRetryCount(dimension string) {
	metric, err := daemonTaskRetryCount.GetMetricWithLabelValues(dimension)
	if err != nil {
		logger.Errorf("prom get [daemonTaskRetryCount] metric failed: %s", err)
		return
	}
	metric.Add(1)
}

var Registry = prometheus.NewRegistry()

func init() {
	// register the metrics
	Registry.MustRegister(
		apiRequestTotal,
		apiRequestCost,
		taskTotal,
		taskDurationSeconds,
		daemonRunningTaskCount,
		daemonTaskRetryCount,
	)
}
