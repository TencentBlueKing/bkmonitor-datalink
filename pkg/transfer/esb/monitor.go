// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esb

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
)

var (
	// MonitorRequestSuccess ESB 接口调用成功计数器
	MonitorRequestSuccess = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "esb_request_success_total",
		Help:      "Successes for esb api requests",
	}, []string{"name"})

	// MonitorRequestFails ESB 接口调用异常计数
	MonitorRequestFails = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: define.AppName,
		Name:      "esb_request_failed_total",
		Help:      "Fails for esb api requests",
	}, []string{"name"})

	// MonitorRequestHandledDuration ESB 接口调用计时
	MonitorRequestHandledDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: define.AppName,
		Name:      "esb_request_handled_seconds",
		Help:      "Duration for each esb api request time",
		Buckets:   monitor.DefBuckets,
	}, []string{"name"})
)

func init() {
	prometheus.MustRegister(
		MonitorRequestSuccess,
		MonitorRequestFails,
		MonitorRequestHandledDuration,
	)
}
