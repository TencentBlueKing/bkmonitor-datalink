// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestDimensionFetcher(t *testing.T) {
	fetcher := NewSpanDimensionFetcher()
	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"a1": "attr1", "a2": "attr2", "a3": "attr3"},
			Resources:  map[string]string{"r1": "res1", "r2": "res2", "r3": "res3"},
		},
		SpanCount: 10,
		SpanKind:  3,
	})

	pdTraces := g.Generate()
	resourceSpans := pdTraces.ResourceSpans().At(0)

	assert.Equal(t, "res1", fetcher.FetchResource(resourceSpans, "r1"))
	assert.Equal(t, map[string]string{"r2": "res2", "r3": "res3"}, fetcher.FetchResources(resourceSpans, "r2", "r3"))

	foreach.Spans(pdTraces, func(span ptrace.Span) {
		assert.Equal(t, "3", fetcher.FetchMethod(span, "kind"))
		assert.Equal(t, "attr1", fetcher.FetchAttribute(span, "a1"))

		dimensions := make(map[string]string)
		fetcher.FetchAttributes(span, dimensions, []string{"a2", "a3"})
		assert.Equal(t, map[string]string{"a2": "attr2", "a3": "attr3"}, dimensions)

		dimensions = make(map[string]string)
		fetcher.FetchMethods(span, dimensions, []string{"kind", "span_name", "trace_id", "span_id", "status.code", "not_exist"})
		assert.Len(t, dimensions, 6)
	})
}

func TestDecodeDimensionFrom(t *testing.T) {
	typ, s := DecodeDimensionFrom("")
	assert.Equal(t, DimensionFromUnknown, typ)
	assert.Equal(t, "", s)

	typ, s = DecodeDimensionFrom("resource.s")
	assert.Equal(t, DimensionFromResource, typ)
	assert.Equal(t, "s", s)

	typ, s = DecodeDimensionFrom("attributes.a")
	assert.Equal(t, DimensionFromAttribute, typ)
	assert.Equal(t, "a", s)

	typ, s = DecodeDimensionFrom("other")
	assert.Equal(t, DimensionFromMethod, typ)
	assert.Equal(t, "other", s)
}

func BenchmarkFormatItoa(b *testing.B) {
	span := ptrace.NewSpan()
	span.SetKind(ptrace.SpanKindClient)

	for i := 0; i < b.N; i++ {
		_ = strconv.Itoa(int(span.Kind()))
	}
}

func BenchmarkFormatSprintf(b *testing.B) {
	span := ptrace.NewSpan()
	span.SetKind(ptrace.SpanKindClient)

	for i := 0; i < b.N; i++ {
		_ = fmt.Sprintf("%d", span.Kind())
	}
}
