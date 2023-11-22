// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

type ProcessorMonitor struct {
	*monitor.CounterMixin
}

var (
	// MonitorFrontendKafka 前端 kafka 来源
	MonitorFrontendKafka = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: AppName,
		Name:      "pipeline_frontend_kafka",
		Help:      "Frontend kafka cluster",
	}, []string{"id", "cluster", "kafka", "topic"})

	// MonitorFrontendHandled pipeline 前端处理计数器
	MonitorFrontendHandled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_frontend_handled_total",
		Help:      "Frontend handled payloads",
	}, []string{"id", "pipeline", "cluster"})

	// MonitorFrontendRecvDeltaDuration 前端接收延迟
	MonitorFrontendRecvDeltaDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: AppName,
		Name:      "frontend_receive_delta_seconds",
		Help:      "Frontend receive delta seconds",
		Buckets:   monitor.LargeDefBuckets,
	}, []string{"id", "cluster"})

	// MonitorFrontendDropped pipeline 前端丢弃计数器
	MonitorFrontendDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_frontend_dropped_total",
		Help:      "Frontend dropped payloads",
	}, []string{"id", "pipeline", "cluster"})

	// MonitorProcessorHandled pipeline 处理器处理计数器
	MonitorProcessorHandled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_processor_handled_total",
		Help:      "Processor handled payloads",
	}, []string{"id", "pipeline"})

	// MonitorProcessorDropped pipeline 处理器丢弃计数器
	MonitorProcessorDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_processor_dropped_total",
		Help:      "Processor dropped payloads",
	}, []string{"id", "pipeline"})

	// MonitorProcessorSkipped pipeline 处理器跳过计数器
	MonitorProcessorSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_processor_skipped_total",
		Help:      "Processor skipped payloads",
	}, []string{"id", "pipeline"})

	// MonitorProcessorHandleDuration pipeline 处理器处理耗时
	MonitorProcessorHandleDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: AppName,
		Name:      "pipeline_processor_handle_seconds",
		Help:      "Processor handle seconds",
		Buckets:   monitor.DefBuckets,
	}, []string{"id", "pipeline"})

	// MonitorBackendHandled pipeline 后端处理计数器
	MonitorBackendHandled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_backend_handled_total",
		Help:      "Backend handled payloads",
	}, []string{"id", "target"})

	// MonitorBackendDropped pipeline 后端丢弃计数器
	MonitorBackendDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "pipeline_backend_dropped_total",
		Help:      "Backend dropped payloads",
	}, []string{"id", "target"})

	// MonitorBuildInfo 进程构建信息
	MonitorBuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: AppName,
		Name:      "build_info",
		Help:      "Build information",
	}, []string{"version", "git_hash"})

	// MonitorUptime 进程运行时间
	MonitorUptime = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "uptime",
		Help:      "Uptime of program",
	})

	// MonitorFlowBytes 流量计数器
	MonitorFlowBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: AppName,
		Name:      "flow_bytes_total",
		Help:      "Flow bytes total",
	}, []string{"name"})

	// MonitorFlowBytesConsumedDuration 流量消费耗时
	MonitorFlowBytesConsumedDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: AppName,
		Name:      "flow_bytes_consumed_seconds",
		Help:      "Flow bytes consumed seconds",
		Buckets:   monitor.DefBuckets,
	}, []string{"name"})
)

const (
	kb = 1024
	mb = 1024 * kb
)

var MonitorFlowBytesDistribution = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: AppName,
	Name:      "flow_bytes_distribution",
	Help:      "Flow bytes distribution",
	Buckets:   []float64{5 * kb, 10 * kb, 50 * kb, 100 * kb, 500 * kb, 1 * mb, 5 * mb, 10 * mb},
}, []string{"name"})

func init() {
	prometheus.MustRegister(
		MonitorFrontendKafka,
		MonitorFrontendHandled,
		MonitorFrontendRecvDeltaDuration,
		MonitorFrontendDropped,
		MonitorProcessorHandled,
		MonitorProcessorDropped,
		MonitorProcessorSkipped,
		MonitorProcessorHandleDuration,
		MonitorBackendHandled,
		MonitorBackendDropped,
		MonitorBuildInfo,
		MonitorUptime,
		MonitorFlowBytes,
		MonitorFlowBytesConsumedDuration,
		MonitorFlowBytesDistribution,
	)

	// 初始化 version/buildHash
	MonitorBuildInfo.WithLabelValues(Version, BuildHash).Set(1)

	// 周期性更新程序运行时间
	go func() {
		for range time.Tick(time.Second * 5) {
			MonitorUptime.Add(float64(5))
		}
	}()
}
