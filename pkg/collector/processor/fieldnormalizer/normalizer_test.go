// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fieldnormalizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestNormalizer(t *testing.T) {
	t.Run("FuncContact", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			GeneratorOptions: define.GeneratorOptions{
				Attributes: map[string]string{
					"client.address": "localhost",
					"client.port":    "8080",
					"http.method":    "GET",
				},
			},
			SpanCount: 2,
			SpanKind:  2,
		})
		data := g.Generate()

		conf := Config{
			Fields: []FieldConfig{
				{
					Kind:         "SPAN_KIND_SERVER",
					PredicateKey: "attributes.http.method",
					Rules: []FieldRule{
						{
							Key: "attributes.net.peer.name",
							Values: []string{
								"attributes.client.address",
								"attributes.client.port",
							},
							Op: funcContact,
						},
					},
				},
			},
		}

		var n int
		normalizer := NewSpanFieldNormalizer(conf)
		foreach.Spans(data, func(span ptrace.Span) {
			normalizer.Normalize(span)
			v, ok := span.Attributes().Get("net.peer.name")
			assert.True(t, ok)
			assert.Equal(t, "localhost:8080", v.AsString())
			n++
		})
		assert.Equal(t, 2, n)
	})

	t.Run("FuncOr", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			GeneratorOptions: define.GeneratorOptions{
				Attributes: map[string]string{
					"network.peer.address": "localhost",
					"http.method":          "GET",
				},
			},
			SpanCount: 2,
			SpanKind:  3,
		})
		data := g.Generate()

		conf := Config{
			Fields: []FieldConfig{
				{
					Kind:         "SPAN_KIND_CLIENT",
					PredicateKey: "attributes.http.method",
					Rules: []FieldRule{
						{
							Key: "attributes.net.peer.ip",
							Values: []string{
								"attributes.server.address",
								"attributes.network.peer.address",
							},
							Op: funcOr,
						},
					},
				},
			},
		}

		var n int
		normalizer := NewSpanFieldNormalizer(conf)
		foreach.Spans(data, func(span ptrace.Span) {
			normalizer.Normalize(span)
			v, ok := span.Attributes().Get("net.peer.ip")
			assert.True(t, ok)
			assert.Equal(t, "localhost", v.AsString())
			n++
		})
		assert.Equal(t, 2, n)
	})
}
