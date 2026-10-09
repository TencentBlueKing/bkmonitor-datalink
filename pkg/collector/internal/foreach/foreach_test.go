// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package foreach

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestMetrics(t *testing.T) {
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		GaugeCount:     2,
		CounterCount:   2,
		HistogramCount: 2,
		SummaryCount:   2,
	})

	var n int

	n = 0
	Metrics(g.Generate(), func(metric pmetric.Metric) { n++ })
	assert.Equal(t, 8, n)

	n = 1
	MetricsSliceResource(g.Generate(), func(rs pcommon.Resource) { n++ })

	n = 0
	MetricsWithResource(g.Generate(), func(rs pcommon.Map, metric pmetric.Metric) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	MetricsDataPoint(testkits.FirstMetric(g.Generate()), func(attrs pcommon.Map) { n++ })

	n = 0
	MetricsDataPointWithResource(g.Generate(), func(metric pmetric.Metric, rs, attrs pcommon.Map) { n++ })
	assert.Equal(t, 8, n)
}

func TestTraces(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 8,
	})

	var n int

	n = 0
	Spans(g.Generate(), func(span ptrace.Span) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	SpansSliceResource(g.Generate(), func(rs pcommon.Resource) { n++ })
	assert.Equal(t, 1, n)

	n = 0
	SpansWithResource(g.Generate(), func(rs pcommon.Map, span ptrace.Span) { n++ })
	assert.Equal(t, 8, n)

	spans := g.Generate()
	SpansRemoveIf(spans, func(span ptrace.Span) bool { return true })
	assert.Equal(t, 0, spans.ResourceSpans().Len())

	traces := ptrace.NewTraces()

	dropResource := traces.ResourceSpans().AppendEmpty()
	dropResource.Resource().Attributes().UpsertString("service.name", "drop-service")
	dropSpans := dropResource.ScopeSpans().AppendEmpty().Spans()
	dropSpans.AppendEmpty().SetName("drop-resource-1")
	dropSpans.AppendEmpty().SetName("drop-resource-2")

	keepResource := traces.ResourceSpans().AppendEmpty()
	keepResource.Resource().Attributes().UpsertString("service.name", "keep-service")
	keepResource.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("keep-span")
	keepResource.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("drop-scope-only")

	visited := map[string][]string{}
	SpansWithResourceRemoveIf(traces, func(rs pcommon.Map, span ptrace.Span) bool {
		serviceName, ok := rs.Get("service.name")
		assert.True(t, ok)
		visited[serviceName.AsString()] = append(visited[serviceName.AsString()], span.Name())
		return serviceName.AsString() == "drop-service" || span.Name() == "drop-scope-only"
	})

	assert.Equal(t, []string{"drop-resource-1", "drop-resource-2"}, visited["drop-service"])
	assert.Equal(t, []string{"keep-span", "drop-scope-only"}, visited["keep-service"])
	assert.Equal(t, 1, traces.ResourceSpans().Len())
	assert.Equal(t, 1, traces.SpanCount())

	remainingResource := traces.ResourceSpans().At(0)
	serviceName, ok := remainingResource.Resource().Attributes().Get("service.name")
	assert.True(t, ok)
	assert.Equal(t, "keep-service", serviceName.AsString())
	assert.Equal(t, 1, remainingResource.ScopeSpans().Len())
	assert.Equal(t, 1, remainingResource.ScopeSpans().At(0).Spans().Len())
	assert.Equal(t, "keep-span", remainingResource.ScopeSpans().At(0).Spans().At(0).Name())
}

func TestLogs(t *testing.T) {
	g := generator.NewLogsGenerator(define.LogsOptions{
		LogCount: 8,
	})

	var n int

	n = 0
	Logs(g.Generate(), func(logRecord plog.LogRecord) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	LogsSliceResource(g.Generate(), func(rs pcommon.Resource) { n++ })
	assert.Equal(t, 1, n)

	n = 0
	LogsWithResource(g.Generate(), func(rs pcommon.Map, logRecord plog.LogRecord) { n++ })
	assert.Equal(t, 8, n)
}
