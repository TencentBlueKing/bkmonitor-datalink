// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func makeTracesGenerator(n int) *generator.TracesGenerator {
	return generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"http.method1", "http.method2", "http.method3", "http.method4"},
			RandomResourceKeys:  []string{"http.resource1", "http.resource3", "http.resource3", "http.resource4"},
		},
		SpanCount: n,
	})
}

func TestBaseEncoder(t *testing.T) {
	t.Run("EncodeTraces", func(t *testing.T) {
		g := makeTracesGenerator(10)
		traces := g.Generate()
		req := ptraceotlp.NewRequestFromTraces(traces)

		b, err := req.MarshalProto()
		assert.NoError(t, err)
		_, err = PbEncoder().UnmarshalTraces(b)
		assert.NoError(t, err)
		assert.Equal(t, "pb", PbEncoder().Type())
	})

	t.Run("EncodeMetrics", func(t *testing.T) {
		g := makeMetricsGenerator(10, 10, 10)
		metrics := g.Generate()
		req := pmetricotlp.NewRequestFromMetrics(metrics)

		b, err := req.MarshalProto()
		assert.NoError(t, err)
		_, err = PbEncoder().UnmarshalMetrics(b)
		assert.NoError(t, err)
	})

	t.Run("EncodeLogs", func(t *testing.T) {
		g := makeLogsGenerator(10, 10)
		logs := g.Generate()
		req := plogotlp.NewRequestFromLogs(logs)

		b, err := req.MarshalProto()
		assert.NoError(t, err)
		_, err = PbEncoder().UnmarshalLogs(b)
		assert.NoError(t, err)
	})
}

func BenchmarkTracesUnmarshal_10_Spans(b *testing.B) {
	g := makeTracesGenerator(10)
	traces := g.Generate()
	req := ptraceotlp.NewRequestFromTraces(traces)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalTraces(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkTracesUnmarshal_100_Spans(b *testing.B) {
	g := makeTracesGenerator(100)
	traces := g.Generate()
	req := ptraceotlp.NewRequestFromTraces(traces)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalTraces(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkTracesUnmarshal_1000_Spans(b *testing.B) {
	g := makeTracesGenerator(1000)
	traces := g.Generate()
	req := ptraceotlp.NewRequestFromTraces(traces)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalTraces(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkTracesUnmarshal_10000_Spans(b *testing.B) {
	g := makeTracesGenerator(10000)
	traces := g.Generate()
	req := ptraceotlp.NewRequestFromTraces(traces)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalTraces(bs)
		if err != nil {
			panic(err)
		}
	}
}

func makeMetricsGenerator(gaugeCount, counterCount, histogramCount int) *generator.MetricsGenerator {
	return generator.NewMetricsGenerator(define.MetricsOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"http.method1", "http.method2", "http.method3", "http.method4"},
			RandomResourceKeys:  []string{"http.resource1", "http.resource3", "http.resource3", "http.resource4"},
		},
		GaugeCount:     gaugeCount,
		CounterCount:   counterCount,
		HistogramCount: histogramCount,
	})
}

func BenchmarkMetricsUnmarshal_100_DataPoints(b *testing.B) {
	g := makeMetricsGenerator(100, 100, 100)
	metrics := g.Generate()
	req := pmetricotlp.NewRequestFromMetrics(metrics)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalMetrics(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_1000_DataPoints(b *testing.B) {
	g := makeMetricsGenerator(1000, 1000, 1000)
	metrics := g.Generate()
	req := pmetricotlp.NewRequestFromMetrics(metrics)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalMetrics(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_10000_DataPoints(b *testing.B) {
	g := makeMetricsGenerator(10000, 10000, 10000)
	metrics := g.Generate()
	req := pmetricotlp.NewRequestFromMetrics(metrics)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalMetrics(bs)
		if err != nil {
			panic(err)
		}
	}
}

func makeLogsGenerator(logLength, logCount int) *generator.LogsGenerator {
	return generator.NewLogsGenerator(define.LogsOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"http.method1", "http.method2", "http.method3", "http.method4"},
			RandomResourceKeys:  []string{"http.resource1", "http.resource3", "http.resource3", "http.resource4"},
		},
		LogLength: logLength,
		LogCount:  logCount,
	})
}

func BenchmarkMetricsUnmarshal_100x10KB_Logs(b *testing.B) {
	g := makeLogsGenerator(10240, 100)
	logs := g.Generate()
	req := plogotlp.NewRequestFromLogs(logs)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalLogs(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_100x100KB_Logs(b *testing.B) {
	g := makeLogsGenerator(102400, 100)
	logs := g.Generate()
	req := plogotlp.NewRequestFromLogs(logs)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalLogs(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_100x1000KB_Logs(b *testing.B) {
	g := makeLogsGenerator(1024000, 100)
	logs := g.Generate()
	req := plogotlp.NewRequestFromLogs(logs)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalLogs(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_1000x10KB_Logs(b *testing.B) {
	g := makeLogsGenerator(10240, 1000)
	logs := g.Generate()
	req := plogotlp.NewRequestFromLogs(logs)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalLogs(bs)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkMetricsUnmarshal_1000x100KB_Logs(b *testing.B) {
	g := makeLogsGenerator(102400, 1000)
	logs := g.Generate()
	req := plogotlp.NewRequestFromLogs(logs)

	bs, err := req.MarshalProto()
	if err != nil {
		panic(err)
	}

	for i := 0; i < b.N; i++ {
		_, err = PbEncoder().UnmarshalLogs(bs)
		if err != nil {
			panic(err)
		}
	}
}
