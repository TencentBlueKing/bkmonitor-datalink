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

	operator := NewTracesOperator(c)
	derived := operator.Operate(record)

	data := derived.Data.(*define.MetricV2Data)
	for _, item := range data.Data {
		_, ok := item.Metrics["test_bk_apm_duration"]
		assert.True(t, ok)

		assert.Equal(t, item.Dimension["http.uri"], "/api/v1/healthz")
		assert.Equal(t, item.Dimension["service.name"], "echo")
		assert.Equal(t, item.Dimension["kind"], "3")
	}
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

	op := NewTracesOperator(c)
	derived := op.Operate(&define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	})
	assert.NotNil(t, derived)

	metrics := derived.Data.(*define.MetricV2Data)
	for idx, item := range metrics.Data {
		var fv float64
		switch idx {
		case 0:
			fv = float64(100)
		case 1:
			fv = float64(0)
		}
		assert.Equal(t, fv, item.Metrics["test_bk_apm_duration"])
	}
}
