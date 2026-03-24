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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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
							"kind",
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

	operator := NewOperator(c)
	derived := operator.Operate(record)

	pdMetrics := derived.Data.(pmetric.Metrics)
	assert.Equal(t, 1, pdMetrics.MetricCount())
	foreach.Metrics(pdMetrics, func(metric pmetric.Metric) {
		assert.Equal(t, "test_bk_apm_duration", metric.Name())
		dataPoints := metric.Gauge().DataPoints()
		for n := 0; n < dataPoints.Len(); n++ {
			dp := dataPoints.At(n)
			attrs := dp.Attributes()
			testkits.AssertAttrsStringKeyVal(t, attrs,
				"http.uri", "/api/v1/healthz",
				"service.name", "echo",
				"kind", "3",
			)
		}
	})
}

func TestOperatorDuration(t *testing.T) {
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
							"kind",
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
		SpanCount: 2,
		SpanKind:  3,
	})
	data := g.Generate()

	span1 := data.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	span1.Attributes().InsertString("http.method", "GET")
	span1.SetStartTimestamp(100)
	span1.SetEndTimestamp(200)

	// 时间戳异常 处理为 0
	span2 := data.ResourceSpans().At(0).ScopeSpans().At(1).Spans().At(0)
	span2.Attributes().InsertString("http.method", "POST")
	span2.SetStartTimestamp(300)
	span2.SetEndTimestamp(200)

	operator := NewOperator(c)
	derived := operator.Operate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	})
	assert.NotNil(t, derived)

	metrics := derived.Data.(pmetric.Metrics)
	assert.Equal(t, 2, metrics.DataPointCount())

	foreach.Metrics(metrics, func(metric pmetric.Metric) {
		assert.Equal(t, "test_bk_apm_duration", metric.Name())
		assert.Equal(t, float64(100), metric.Gauge().DataPoints().At(0).DoubleVal())
		assert.Equal(t, float64(0), metric.Gauge().DataPoints().At(1).DoubleVal())
	})
}
