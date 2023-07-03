// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

var (
	// MonitorBulkBackendBufferUsage bulk buffer 使用率
	MonitorBulkBackendBufferUsage = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "bulk_backend_buffer_usage",
		Help:      "Bulk backend buffer usage",
		Buckets:   []float64{0.01, 0.1, 0.25, 0.5, 0.8, 1},
	}, []string{"name", "id", "cluster"})

	// MonitorBulkBackendSendDuration bulk 发送耗时
	MonitorBulkBackendSendDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "bulk_backend_send_seconds",
		Help:      "Bulk backend send seconds",
		Buckets:   monitor.DefBuckets,
	}, []string{"name", "id", "cluster"})

	// MonitorProcessElapsedDuration pipeline 处理耗时
	MonitorProcessElapsedDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "pipeline_process_elapsed_seconds",
		Help:      "Pipeline process elapsed seconds",
		Buckets:   monitor.DefBuckets,
	}, []string{"id", "cluster"})
)

func NewFrontendProcessorMonitor(pipe *config.PipelineConfig) *define.ProcessorMonitor {
	dataID := strconv.Itoa(pipe.DataID)
	return &define.ProcessorMonitor{
		CounterMixin: monitor.NewCounterMixin(
			define.MonitorFrontendHandled.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": pipe.ETLConfig,
				"cluster":  define.ConfClusterID,
			}),
			define.MonitorFrontendDropped.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": pipe.ETLConfig,
				"cluster":  define.ConfClusterID,
			}),
		),
	}
}

func NewDataProcessorMonitor(name string, pipe *config.PipelineConfig) *define.ProcessorMonitor {
	dataID := strconv.Itoa(pipe.DataID)
	return &define.ProcessorMonitor{
		CounterMixin: monitor.NewCounterMixin(
			define.MonitorProcessorHandled.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": name,
			}),
			define.MonitorProcessorDropped.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": name,
			}),
			define.MonitorProcessorSkipped.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": name,
			}),
		),
	}
}

type ProcessorTimeObserver struct {
	frontendRecvDelta prometheus.Observer
	processElapsed    prometheus.Observer
}

func (o *ProcessorTimeObserver) ObserveRecvDelta(v float64) {
	o.frontendRecvDelta.Observe(v)
}

func (o *ProcessorTimeObserver) ObserveProcessElapsed(v float64) {
	o.processElapsed.Observe(v)
}

func NewProcessorTimeObserver(pipe *config.PipelineConfig) *ProcessorTimeObserver {
	labels := prometheus.Labels{
		"id":      strconv.Itoa(pipe.DataID),
		"cluster": define.ConfClusterID,
	}
	return &ProcessorTimeObserver{
		processElapsed:    MonitorProcessElapsedDuration.With(labels),
		frontendRecvDelta: define.MonitorFrontendRecvDeltaDuration.With(labels),
	}
}

func NewBackendProcessorMonitor(pipe *config.PipelineConfig, shipper *config.MetaClusterInfo) *define.ProcessorMonitor {
	labels := prometheus.Labels{"id": strconv.Itoa(pipe.DataID)}
	if shipper.ClusterType == "elasticsearch" {
		labels["target"] = shipper.AsElasticSearchCluster().GetTarget()
	} else if shipper.ClusterType == "kafka" {
		labels["target"] = shipper.AsKafkaCluster().GetTarget()
	} else if shipper.ClusterType == "redis" {
		labels["target"] = shipper.AsRedisCluster().GetTarget()
	} else {
		labels["target"] = shipper.AsInfluxCluster().GetTarget()
	}

	return &define.ProcessorMonitor{
		CounterMixin: monitor.NewCounterMixin(
			define.MonitorBackendHandled.With(labels),
			define.MonitorBackendDropped.With(labels),
		),
	}
}

func init() {
	prometheus.MustRegister(
		MonitorBulkBackendBufferUsage,
		MonitorBulkBackendSendDuration,
		MonitorProcessElapsedDuration,
	)
}
