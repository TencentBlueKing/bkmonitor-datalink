// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestOperator(t *testing.T) {
	c := Config{
		Operations: []OperationConfig{
			{
				Type:       "duration",
				MetricName: "test_bk_apm_duration",
				Rules: []RuleConfig{
					{
						Kind:         "SPAN_KIND_CLIENT",
						PredicateKey: "attributes.http.method",
						Dimensions: []string{
							"span_name",
							"attributes.http.uri",
							"resource.service.name",
						},
					},
				},
			},
		},
	}

	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"http.method": "POST",
				"http.uri":    "/api/v1/healthz",
			},
			Resources: map[string]string{
				"service.name": "echo",
			},
		},
		SpanCount: 1,
		SpanKind:  3,
	})

	pdTraces := g.Generate()
	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       pdTraces,
	}

	operator := NewTracesOperator(c)
	derived := operator.Operate(record)

	pdMetrics := derived.Data.(pmetric.Metrics)
	resourceMetricsSlice := pdMetrics.ResourceMetrics()

	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		scopeMetricsSlice := resourceMetricsSlice.At(i).ScopeMetrics()
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				metric := metrics.At(k)
				assert.Equal(t, "test_bk_apm_duration", metric.Name())
				dataPoints := metric.Gauge().DataPoints()
				for n := 0; n < dataPoints.Len(); n++ {
					dp := dataPoints.At(n)
					v, ok := dp.Attributes().Get("http.uri")
					assert.True(t, ok)
					assert.Equal(t, "/api/v1/healthz", v.AsString())

					v, ok = dp.Attributes().Get("service.name")
					assert.True(t, ok)
					assert.Equal(t, "echo", v.AsString())
				}
			}
		}
	}
}
