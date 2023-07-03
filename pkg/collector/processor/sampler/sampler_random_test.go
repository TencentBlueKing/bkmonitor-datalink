// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sampler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestRandomSampler_0_Percent(t *testing.T) {
	sampler := RandomSampler(Config{
		Type:               "random",
		SamplingPercentage: 0,
	})

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	traces := g.Generate()
	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	}
	assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())

	sampler.Sample(record)
	assert.Equal(t, 0, record.Data.(ptrace.Traces).SpanCount())
}

func TestRandomSampler_10_Percent(t *testing.T) {
	sampler := RandomSampler(Config{
		Type:               "random",
		SamplingPercentage: 0.1,
	})

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	traces := g.Generate()
	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	}
	assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())

	sampler.Sample(record)
	assert.True(t, record.Data.(ptrace.Traces).SpanCount() <= 2)
}

func TestRandomSamplerPriority(t *testing.T) {
	sampler := RandomSampler(Config{
		Type:               "random",
		SamplingPercentage: 0.1,
	})

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	t.Run("String", func(t *testing.T) {
		traces := g.Generate()
		foreach.Spans(traces.ResourceSpans(), func(span ptrace.Span) {
			span.Attributes().UpsertString("sampling.priority", "1")
		})
		record := &define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())

		sampler.Sample(record)
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())
	})

	t.Run("Int", func(t *testing.T) {
		traces := g.Generate()
		foreach.Spans(traces.ResourceSpans(), func(span ptrace.Span) {
			span.Attributes().UpsertInt("sampling.priority", 1)
		})
		record := &define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())

		sampler.Sample(record)
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())
	})

	t.Run("Float", func(t *testing.T) {
		traces := g.Generate()
		foreach.Spans(traces.ResourceSpans(), func(span ptrace.Span) {
			span.Attributes().UpsertDouble("sampling.priority", 1.0)
		})
		record := &define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())

		sampler.Sample(record)
		assert.Equal(t, 10, record.Data.(ptrace.Traces).SpanCount())
	})
}

func benchmarkSamplerPercent(b *testing.B, percent float64) {
	sampler := RandomSampler(Config{
		Type:               "random",
		SamplingPercentage: percent,
	})

	g := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{
				"sampling.priority1",
				"sampling.priority2",
				"sampling.priority3",
			},
		},
		SpanCount: 10,
	})

	traces := g.Generate()
	for i := 0; i < b.N; i++ {
		sampler.Sample(&define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		})
	}
	g.Stop()
}

func BenchmarkSampler_99_99_Percent(b *testing.B) {
	benchmarkSamplerPercent(b, 99.99)
}

func BenchmarkSampler_100_Percent(b *testing.B) {
	benchmarkSamplerPercent(b, 100)
}
