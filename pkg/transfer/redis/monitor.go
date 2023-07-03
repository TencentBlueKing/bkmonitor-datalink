// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

var (
	// MonitorBackendHandled redis 写入了多少条
	MonitorBackendHandled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_backend_handled_total",
		Help:      "Count of writing redis backend successfully",
	}, []string{"id", "pipeline", "key"})

	// MonitorBackendDropped redis 写入时丢弃了多少条
	MonitorBackendDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "redis_backend_dropped_total",
		Help:      "Count of writing redis backend unsuccessfully",
	}, []string{"id", "pipeline", "key"})
)

// NewBackendProcessorMonitor :
func NewRedisBackendProcessorMonitor(pipe *config.PipelineConfig, redisKey string) *define.ProcessorMonitor {
	dataID := strconv.Itoa(pipe.DataID)
	return &define.ProcessorMonitor{
		CounterMixin: monitor.NewCounterMixin(
			MonitorBackendHandled.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": pipe.ETLConfig,
				"key":      redisKey,
			}),
			MonitorBackendDropped.With(prometheus.Labels{
				"id":       dataID,
				"pipeline": pipe.ETLConfig,
				"key":      redisKey,
			}),
		),
	}
}

func init() {
	prometheus.MustRegister(
		MonitorBackendHandled,
		MonitorBackendDropped,
	)
}
