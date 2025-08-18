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
	Metrics(g.Generate().ResourceMetrics(), func(metric pmetric.Metric) { n++ })
	assert.Equal(t, 8, n)

	n = 1
	MetricsSliceResource(g.Generate().ResourceMetrics(), func(rs pcommon.Resource) { n++ })

	n = 0
	MetricsWithResourceAttrs(g.Generate().ResourceMetrics(), func(rsAttrs pcommon.Map, metric pmetric.Metric) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	MetricDataPointsAttrs(testkits.FirstMetric(g.Generate()), func(attrs pcommon.Map) { n++ })
	assert.Equal(t, 1, n)
}

func TestTraces(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 8,
	})

	var n int

	n = 0
	Spans(g.Generate().ResourceSpans(), func(span ptrace.Span) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	SpansSliceResource(g.Generate().ResourceSpans(), func(rs pcommon.Resource) { n++ })
	assert.Equal(t, 1, n)

	n = 0
	SpansWithResourceAttrs(g.Generate().ResourceSpans(), func(rsAttrs pcommon.Map, span ptrace.Span) { n++ })
	assert.Equal(t, 8, n)

	spans := g.Generate().ResourceSpans()
	SpansRemoveIf(spans, func(span ptrace.Span) bool { return true })
	assert.Equal(t, 0, spans.Len())
}

func TestLogs(t *testing.T) {
	g := generator.NewLogsGenerator(define.LogsOptions{
		LogCount: 8,
	})

	var n int

	n = 0
	Logs(g.Generate().ResourceLogs(), func(logRecord plog.LogRecord) { n++ })
	assert.Equal(t, 8, n)

	n = 0
	LogsSliceResource(g.Generate().ResourceLogs(), func(rs pcommon.Resource) { n++ })
	assert.Equal(t, 1, n)

	n = 0
	LogsWithResourceAttrs(g.Generate().ResourceLogs(), func(rsAttrs pcommon.Map, logRecord plog.LogRecord) { n++ })
	assert.Equal(t, 8, n)
}
