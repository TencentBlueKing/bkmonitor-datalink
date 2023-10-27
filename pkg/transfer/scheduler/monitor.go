// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

var (
	// MonitorRunningPipeline 正在运行的 pipeline 数量
	MonitorRunningPipeline = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "scheduler_running_pipelines",
		Help:      "Count of running pipelines",
	})

	// MonitorDeclaredPipeline 已经取消的 pipeline 数量
	MonitorDeclaredPipeline = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "scheduler_declared_pipelines",
		Help:      "Count of declared pipelines",
	})

	// MonitorPendingPipeline 正在等待的 pipeline 数量
	MonitorPendingPipeline = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: define.AppName,
		Name:      "scheduler_pending_pipelines",
		Help:      "Count of pending pipelines",
	})

	// MonitorPipelinePanic pipeline panic 计数器
	MonitorPipelinePanic = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "scheduler_panic_pipeline_total",
		Help:      "Totals of panic pipelines",
	})
)

func init() {
	prometheus.MustRegister(
		MonitorRunningPipeline,
		MonitorDeclaredPipeline,
		MonitorPendingPipeline,
		MonitorPipelinePanic,
	)
}
