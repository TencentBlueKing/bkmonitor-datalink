// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type bkClient struct{}

// MetricData 用于存储指标数据
type MetricData struct {
	Metric string
	Value  float64
	Labels map[string]string
}

func (c *bkClient) Do(r *http.Request) (*http.Response, error) {
	r.Header.Set("X-BK-TOKEN", config.SloPushGatewayToken)
	return http.DefaultClient.Do(r)
}

// 定义指标
var (
	sloErrorTimeInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "slo_error_time_info",
			Help: "Total SLO error time info",
		},
		[]string{"bk_biz_id", "range_time", "strategy_id", "strategy_name", "velat", "scene"},
	)

	sloInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "slo_info",
			Help: "Total SLO info",
		},
		[]string{"bk_biz_id", "range_time", "velat", "scene"},
	)

	mttr = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mttr",
			Help: "Total MTTR",
		},
		[]string{"bk_biz_id", "range_time", "scene"},
	)

	mtbf = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mtbf",
			Help: "Total MTBF",
		},
		[]string{"bk_biz_id", "range_time", "scene"},
	)

	sloMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "slo",
			Help: "Total SLO",
		},
		[]string{"bk_biz_id", "range_time", "scene"},
	)

	sloErrorTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "slo_error_time",
			Help: "Total slo_error_time",
		},
		[]string{"bk_biz_id", "range_time", "scene"},
	)

	sloErrorEventTimeInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "slo_error_event_time_info",
			Help: "Total slo_error_event_time_info",
		},
		[]string{"bk_biz_id", "range_time", "strategy_id", "scene", "event_id", "event_status"},
	)

	sloMonitor = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "slo_monitor",
			Help: "Total slo_monitor",
		},
		[]string{"bk_biz_id", "scene", "name", "status"},
	)
)

// 注册指标
func InitGauge(registry *prometheus.Registry) {
	registry.MustRegister(
		sloErrorTimeInfo,
		sloInfo,
		mttr,
		mtbf,
		sloMetric,
		sloErrorTime,
		sloMonitor,
		sloErrorEventTimeInfo,
	)
}

// RecordSloMonitor updates the RecordSloMonitor metric with the provided values
func RecordSloMonitor(bk_biz_id string, scene string, name string, flag string) {
	metric, err := sloMonitor.GetMetricWithLabelValues(bk_biz_id, scene, name, flag)
	if err != nil {
		logger.Errorf("prom get [sloMonitor] right metric failed: %s", err)
		return
	}
	metric.Add(1)
}

// RecordSloErrorTimeInfo updates the sloErrorTimeInfo metric with the provided values
func RecordSloErrorTimeInfo(value float64, bk_biz_id string, range_time string, strategy_id string, strategy_name string, velat string, scene string) {
	metric, err := sloErrorTimeInfo.GetMetricWithLabelValues(bk_biz_id, range_time, strategy_id, strategy_name, velat, scene)
	if err != nil {
		logger.Errorf("prom get [sloErrorTimeInfo] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "SloErrorTimeInfo", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "SloErrorTimeInfo", "1")
	metric.Set(value)
}

// RecordSloInfo updates the sloInfo metric with the provided values
func RecordSloInfo(value float64, bk_biz_id string, range_time string, velat string, scene string) {
	metric, err := sloInfo.GetMetricWithLabelValues(bk_biz_id, range_time, velat, scene)
	if err != nil {
		logger.Errorf("prom get [sloInfo] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "SloInfo", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "SloInfo", "1")
	metric.Set(value)
}

// RecordMttr updates the mttr metric with the provided values
func RecordMttr(value float64, bk_biz_id string, range_time string, scene string) {
	metric, err := mttr.GetMetricWithLabelValues(bk_biz_id, range_time, scene)
	if err != nil {
		logger.Errorf("prom get [mttr] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "Mttr", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "Mttr", "1")
	metric.Set(value)
}

// RecordMtbf updates the mtbf metric with the provided values
func RecordMtbf(value float64, bk_biz_id string, range_time string, scene string) {
	metric, err := mtbf.GetMetricWithLabelValues(bk_biz_id, range_time, scene)
	if err != nil {
		logger.Errorf("prom get [mtbf] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "Mtbf", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "Mtbf", "1")
	metric.Set(value)
}

// RecordSlo updates the slo metric with the provided values
func RecordSlo(value float64, bk_biz_id string, range_time string, scene string) {
	metric, err := sloMetric.GetMetricWithLabelValues(bk_biz_id, range_time, scene)
	if err != nil {
		logger.Errorf("prom get [slo] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "Slo", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "Slo", "1")
	metric.Set(value)
}

// RecordSloErrorTime updates the sloErrorTime metric with the provided values
func RecordSloErrorTime(value float64, bk_biz_id string, range_time string, scene string) {
	metric, err := sloErrorTime.GetMetricWithLabelValues(bk_biz_id, range_time, scene)
	if err != nil {
		logger.Errorf("prom get [sloErrorTime] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "SloErrorTime", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "SloErrorTime", "1")
	metric.Set(value)
}

// RecordSloErrorEventTimeInfo updates the sloErrorEventTimeInfo metric with the provided values
func RecordSloErrorEventTimeInfo(value float64, bk_biz_id string, range_time string, strategy_id string, scene string, event_id string, event_status string) {
	metric, err := sloErrorEventTimeInfo.GetMetricWithLabelValues(bk_biz_id, range_time, strategy_id, scene, event_id, event_status)
	if err != nil {
		logger.Errorf("prom get [sloErrorEventTimeInfo] metric failed: %s", err)
		RecordSloMonitor(bk_biz_id, scene, "SloErrorEventTimeInfo", "0")
		return
	}
	RecordSloMonitor(bk_biz_id, scene, "SloErrorEventTimeInfo", "1")
	metric.Set(value)
}

func PushRes(registry *prometheus.Registry) {
	// 创建一个新的 Pusher
	pusher := push.New(config.SloPushGatewayEndpoint, "slo").Gatherer(registry)

	// 设置自定义客户端
	pusher.Client(&bkClient{})

	// 推送指标数据
	if err := pusher.Push(); err != nil {
		logger.Errorf("failed to push metric: %v", err)
		return
	}

	logger.Info("Pushed all metrics successfully")
}
