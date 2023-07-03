// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

var (
	// MonitorBackendHandled kafka 后端处理计数器
	MonitorBackendHandled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_backend_handled_total",
		Help:      "Count of writing kafka backend successfully",
	}, []string{"topic", "id"})

	// MonitorBackendStartFailed kafka 启动失败计数器
	MonitorBackendStartFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_backend_start_failed_total",
		Help:      "Count of kafka backend startup failures",
	}, []string{"topic", "id"})

	// MonitorBackendSkipped kafka 后端写入跳过计数器
	MonitorBackendSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_backend_skipped_total",
		Help:      "Count of skipping kafka backend",
	}, []string{"topic", "id"})

	// MonitorBackendDropped kafka 后端写入丢弃计数器
	MonitorBackendDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_backend_dropped_total",
		Help:      "Count of writing kafka backend unsuccessfully",
	}, []string{"topic", "id"})

	// MonitorFrontendRebalanced kafka 前端重平衡计数器
	MonitorFrontendRebalanced = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_frontend_rebalanced_total",
		Help:      "Kafka frontend rebalanced count",
	}, []string{"topic"})

	// MonitorFrontendCommitted kafka 前端提交计数器
	MonitorFrontendCommitted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "kafka_frontend_commit_total",
		Help:      "Kafka frontend commits count",
	}, []string{"topic"})
)

func NewKafkaBackendProcessorMonitor(pipe *config.PipelineConfig) *define.ProcessorMonitor {
	dataID := strconv.Itoa(pipe.DataID)
	topic := pipe.MQConfig.AsKafkaCluster().GetTopic()
	return &define.ProcessorMonitor{
		CounterMixin: monitor.NewCounterMixin(
			MonitorBackendHandled.With(prometheus.Labels{
				"id":    dataID,
				"topic": topic,
			}),
			MonitorBackendDropped.With(prometheus.Labels{
				"id":    dataID,
				"topic": topic,
			}),
		),
	}
}

func NewKafkaBackendSkippedMonitor(pipe *config.PipelineConfig) prometheus.Counter {
	dataID := strconv.Itoa(pipe.DataID)
	topic := pipe.MQConfig.AsKafkaCluster().GetTopic()
	return MonitorBackendSkipped.With(prometheus.Labels{
		"id":    dataID,
		"topic": topic,
	})
}

func NewKafkaBackendStartMonitor(pipe *config.PipelineConfig) prometheus.Counter {
	dataID := strconv.Itoa(pipe.DataID)
	topic := pipe.MQConfig.AsKafkaCluster().GetTopic()
	return MonitorBackendStartFailed.With(prometheus.Labels{
		"id":    dataID,
		"topic": topic,
	})
}

func init() {
	prometheus.MustRegister(
		MonitorBackendHandled,
		MonitorBackendStartFailed,
		MonitorBackendDropped,
		MonitorBackendSkipped,
		MonitorFrontendRebalanced,
		MonitorFrontendCommitted,
	)
}
