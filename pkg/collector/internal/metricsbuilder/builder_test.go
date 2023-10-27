// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsbuilder

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestMetricsBuilder(t *testing.T) {
	builder := New()

	now := time.Now()
	dimensions := map[string]string{
		"k1": "v1",
		"k2": "v2",
		"k3": "v3",
	}

	builder.Build("my_metrics1", []Metric{
		{
			Val:        1.0,
			Ts:         pcommon.NewTimestampFromTime(now),
			Dimensions: dimensions,
		},
		{
			Val:        2.0,
			Ts:         pcommon.NewTimestampFromTime(now),
			Dimensions: dimensions,
		},
	}...)

	builder.Build("my_metrics2", []Metric{
		{
			Val:        3.0,
			Ts:         pcommon.NewTimestampFromTime(now),
			Dimensions: dimensions,
		},
		{
			Val:        4.0,
			Ts:         pcommon.NewTimestampFromTime(now),
			Dimensions: dimensions,
		},
	}...)

	pdMetrics := builder.Get()
	assert.Equal(t, 2, pdMetrics.MetricCount())

	resourceMetricsSlice := pdMetrics.ResourceMetrics()
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		scopeMetricsSlice := resourceMetricsSlice.At(i).ScopeMetrics()
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			scopeMetricsSlice.At(j).Scope()
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				name := metrics.At(k).Name()
				switch k {
				case 0:
					assert.Equal(t, "my_metrics1", name)
				case 1:
					assert.Equal(t, "my_metrics2", name)
				}

				dataPoints := metrics.At(k).Gauge().DataPoints()
				assert.Equal(t, 2, dataPoints.Len())

				for n := 0; n < dataPoints.Len(); n++ {
					dataPoints.At(n).Attributes().Range(func(k string, v pcommon.Value) bool {
						val, ok := dimensions[k]
						assert.True(t, ok)
						assert.Equal(t, val, v.AsString())
						return true
					})
				}
			}
		}
	}
}
