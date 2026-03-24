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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
)

func mockMeters() *agentv3.MeterDataCollection {
	meters := []*agentv3.MeterData{
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "datasource",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "status", Value: "activeConnections"},
						{Name: "name", Value: "1.1.1.1:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "datasource",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "status", Value: "numActive"},
						{Name: "name", Value: "1.1.1.2:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "datasource",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "status", Value: "activeCount"},
						{Name: "name", Value: "1.1.1.3:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "datasource",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "status", Value: "dropDimVal"},
						{Name: "name", Value: "1.1.1.4:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "datasource",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "dropDim", Value: "activeConnections"},
						{Name: "name", Value: "1.1.1.4:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
		{
			Metric: &agentv3.MeterData_SingleValue{
				SingleValue: &agentv3.MeterSingleValue{
					Name:  "dropMetric",
					Value: 1,
					Labels: []*agentv3.Label{
						{Name: "status", Value: "activeConnections"},
						{Name: "name", Value: "1.1.1.4:3306"},
					},
				},
			},
			Service:         "service",
			ServiceInstance: "instance",
			Timestamp:       1758276000000000,
		},
	}
	return &agentv3.MeterDataCollection{
		MeterData: meters,
	}
}

func TestConvertMeters(t *testing.T) {
	converter := newMeterConverter("service", "instance", 1758276000000000, "my-token")
	for _, meter := range mockMeters().GetMeterData() {
		converter.Convert(meter)
	}

	tests := []struct {
		metric     string
		dimensions map[string]string
		val        float64
	}{
		{metric: "datasource", val: 1, dimensions: map[string]string{"status": "activeConnections", "name": "1.1.1.1:3306"}},
		{metric: "datasource", val: 1, dimensions: map[string]string{"status": "activeConnections", "name": "1.1.1.2:3306"}},
		{metric: "datasource", val: 1, dimensions: map[string]string{"status": "activeConnections", "name": "1.1.1.3:3306"}},
	}
	metrics := converter.Get()

	n := 0
	foreach.Metrics(metrics, func(metric pmetric.Metric) {
		c := tests[n]
		assert.Equal(t, metric.Name(), c.metric)
		assert.Equal(t, metric.Gauge().DataPoints().Len(), 1)
		assert.Equal(t, metric.Gauge().DataPoints().At(0).DoubleVal(), c.val)
		for k, v := range c.dimensions {
			dimVal, set := metric.Gauge().DataPoints().At(0).Attributes().Get(k)
			assert.Equal(t, set, true)
			assert.Equal(t, v, dimVal.AsString())
		}
		n++
	})

	assert.Equal(t, 3, n)
}
