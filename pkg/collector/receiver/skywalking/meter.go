// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package skywalking

import (
	"go.opentelemetry.io/collector/pdata/pmetric"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metricsbuilder"
)

const (
	activeConnections = "activeConnections"
	idleConnections   = "idleConnections"
	totalConnections  = "totalConnections"
)

var keepMetricsList = map[string]map[string]map[string]string{
	"datasource": {
		"status": {
			// hikaricp-3.x-4.x
			"activeConnections": activeConnections,
			"idleConnections":   idleConnections,
			"totalConnections":  totalConnections,

			// dbcp-2.x
			"numActive": activeConnections,
			"numIdle":   idleConnections,
			"maxTotal":  totalConnections,

			// druid-1.x
			"activeCount":  activeConnections,
			"idleCount":    idleConnections,
			"poolingCount": totalConnections,
		},
	},
}

func newMeterConverter(service string, instance string, ts int64, token string) *meterConverter {
	converter := &meterConverter{
		mb: metricsbuilder.New(
			metricsbuilder.ResourceKv{Key: "service_name", Value: service},
			metricsbuilder.ResourceKv{Key: "bk_instance_id", Value: instance},
			metricsbuilder.ResourceKv{Key: "bk.data.token", Value: token},
		),
		timestamp: ts,
	}
	return converter
}

type meterConverter struct {
	timestamp int64
	mb        *metricsbuilder.Builder
}

func (c *meterConverter) Get() pmetric.Metrics {
	return c.mb.Get()
}

func (c *meterConverter) Convert(meter *agentv3.MeterData) {
	metric := meter.GetMetric()
	switch metric.(type) {
	case *agentv3.MeterData_SingleValue:
		c.convertSingleValue(meter)
	}
}

func (c *meterConverter) convertDimension(labels []*agentv3.Label) map[string]string {
	dimension := make(map[string]string)
	for _, label := range labels {
		dimension[label.GetName()] = label.GetValue()
	}
	return dimension
}

func (c *meterConverter) convertSingleValue(meter *agentv3.MeterData) {
	ts := microsecondsToTimestamp(c.timestamp)
	gaugeMetric := meter.GetSingleValue()
	gaugeDimension := c.convertDimension(gaugeMetric.GetLabels())
	if c.filterMetrics(gaugeMetric.GetName(), gaugeDimension) {
		c.mb.Build(gaugeMetric.GetName(), metricsbuilder.Metric{Val: float64(gaugeMetric.Value), Dimensions: gaugeDimension, Ts: ts})
	}
}

func (c *meterConverter) filterMetrics(name string, dims map[string]string) bool {
	filter, set := keepMetricsList[name]
	if !set {
		return false
	}
	for dim, dimMapping := range filter {
		if oldVal, set := dims[dim]; set {
			if newVal, set := dimMapping[oldVal]; set {
				dims[dim] = newVal
				return true
			}
		}
	}
	return false
}
