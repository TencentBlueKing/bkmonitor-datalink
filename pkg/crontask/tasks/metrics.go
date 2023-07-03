// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package tasks

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	taskCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cron_task_count",
			Help: "cron task run count",
		},
		[]string{"name", "status"},
	)

	taskCostTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cron_task_cost",
			Help: "cron task run cost time",
		},
		[]string{"name"},
	)
)

// RunTaskSuccessCount task success count
func RunTaskSuccessCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "success")
	if err != nil {
		logger.Errorf("prom get metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

// RunTaskFailureCount task failure count
func RunTaskFailureCount(taskName string) error {
	metric, err := taskCount.GetMetricWithLabelValues(taskName, "failure")
	if err != nil {
		logger.Errorf("prom get metric failed: %s", err)
		return err
	}
	metric.Inc()
	return nil
}

func RunTaskCostTime(taskName string, startTime time.Time) error {
	duringTime := time.Now().Sub(startTime).Seconds() * 1000
	metric, err := taskCostTime.GetMetricWithLabelValues(taskName)
	if err != nil {
		logger.Errorf("prom get metric failed: %s", err)
		return err
	}
	metric.Set(duringTime)
	return nil
}

func init() {
	// register the metrics
	prometheus.MustRegister(taskCount, taskCostTime)
}
