// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

var (
	// MonitorWriteSuccess consul 写成功计数器
	MonitorWriteSuccess = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_write_success_total",
		Help:      "Count of write consul success totals",
	})

	// MonitorWriteFailed consul 写失败计数器
	MonitorWriteFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_write_failed_total",
		Help:      "Count of write consul fail totals",
	})

	// MonitorAccessedSuccess consul 读成功计数器
	MonitorAccessedSuccess = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_accessed_success_total",
		Help:      "Count of consul accessed successfully",
	})

	// MonitorAccessedFailed consul 读失败计数器
	MonitorAccessedFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_accessed_failed_total",
		Help:      "Count of consul accessed failed",
	})

	// MonitorHeartBeatSuccess consul 心跳成功计数器
	MonitorHeartBeatSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_heartbeat_success_total",
		Help:      "Count of consul heartbeatTask updated success totals",
	}, []string{"name", "type"})

	// MonitorHeartBeatFailed consul 心跳失败计数器
	MonitorHeartBeatFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_heartbeat_failed_total",
		Help:      "Count of consul heartbeatTask updated fail total",
	}, []string{"name", "type"})

	// MonitorElectSuccess consul 选举成功计数器
	MonitorElectSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_elect_success_total",
		Help:      "Count of consul elect leader success totals",
	}, []string{"name", "type"})

	// MonitorElectFailed consul 选举失败计数器
	MonitorElectFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_elect_failed_total",
		Help:      "Count of consul elect leader fail total",
	}, []string{"name", "type"})

	// MonitorDispatchTotal consul 调度分发次数
	MonitorDispatchTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "consul_dispatch_total",
		Help:      "Count of consul dispatcher dispatch total",
	})

	// MonitorDispatchDuration consul 调度分发耗时
	MonitorDispatchDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "consul_dispatch_duration_seconds",
		Help:      "Consul dispatch duration in seconds",
		Buckets:   monitor.LargeDefBuckets,
	})
)

func init() {
	prometheus.MustRegister(
		MonitorWriteSuccess,
		MonitorWriteFailed,
		MonitorHeartBeatSuccess,
		MonitorHeartBeatFailed,
		MonitorElectSuccess,
		MonitorElectFailed,
		MonitorDispatchTotal,
		MonitorAccessedSuccess,
		MonitorAccessedFailed,
		MonitorDispatchDuration,
	)
}
