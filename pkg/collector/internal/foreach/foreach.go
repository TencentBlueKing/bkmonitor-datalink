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
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func Spans(traces ptrace.Traces, f func(span ptrace.Span)) {
	resourceSpansSlice := traces.ResourceSpans()
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		scopeSpansSlice := resourceSpans.ScopeSpans()
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			spans := scopeSpansSlice.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				f(spans.At(k))
			}
		}
	}
}

func SpansRemoveIf(traces ptrace.Traces, f func(span ptrace.Span) bool) {
	resourceSpansSlice := traces.ResourceSpans()
	resourceSpansSlice.RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		resourceSpans.ScopeSpans().RemoveIf(func(scopeSpans ptrace.ScopeSpans) bool {
			scopeSpans.Spans().RemoveIf(func(span ptrace.Span) bool {
				return f(span)
			})
			return scopeSpans.Spans().Len() == 0
		})
		return resourceSpans.ScopeSpans().Len() == 0
	})
}

func SpansWithResource(traces ptrace.Traces, f func(rs pcommon.Map, span ptrace.Span)) {
	resourceSpansSlice := traces.ResourceSpans()
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		rs := resourceSpans.Resource().Attributes()
		scopeSpansSlice := resourceSpans.ScopeSpans()
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			spans := scopeSpansSlice.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				f(rs, spans.At(k))
			}
		}
	}
}

func SpansSliceResource(traces ptrace.Traces, f func(rs pcommon.Resource)) {
	resourceSpansSlice := traces.ResourceSpans()
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		f(resourceSpans.Resource())
	}
}

func Metrics(metrics pmetric.Metrics, f func(metric pmetric.Metric)) {
	resourceMetricsSlice := metrics.ResourceMetrics()
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		scopeMetricsSlice := resourceMetricsSlice.At(i).ScopeMetrics()
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				f(metrics.At(k))
			}
		}
	}
}

func MetricsDataPoint(metric pmetric.Metric, f func(attrs pcommon.Map)) {
	switch metric.DataType() {
	case pmetric.MetricDataTypeGauge:
		dps := metric.Gauge().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			f(dps.At(i).Attributes())
		}

	case pmetric.MetricDataTypeSum:
		dps := metric.Sum().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			f(dps.At(i).Attributes())
		}

	case pmetric.MetricDataTypeSummary:
		dps := metric.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			f(dps.At(i).Attributes())
		}

	case pmetric.MetricDataTypeHistogram:
		dps := metric.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			f(dps.At(i).Attributes())
		}

	case pmetric.MetricDataTypeExponentialHistogram:
		dps := metric.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			f(dps.At(i).Attributes())
		}
	}
}

func MetricsWithResource(metrics pmetric.Metrics, f func(rs pcommon.Map, metric pmetric.Metric)) {
	resourceMetricsSlice := metrics.ResourceMetrics()
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		rs := resourceMetrics.Resource().Attributes()
		scopeMetricsSlice := resourceMetrics.ScopeMetrics()
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				f(rs, metrics.At(k))
			}
		}
	}
}

func MetricsDataPointWithResource(metrics pmetric.Metrics, f func(metric pmetric.Metric, rs, attrs pcommon.Map)) {
	MetricsWithResource(metrics, func(rs pcommon.Map, metric pmetric.Metric) {
		MetricsDataPoint(metric, func(attrs pcommon.Map) {
			f(metric, rs, attrs)
		})
	})
}

func MetricsSliceResource(metrics pmetric.Metrics, f func(rs pcommon.Resource)) {
	resourceMetricsSlice := metrics.ResourceMetrics()
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		f(resourceMetrics.Resource())
	}
}

func Logs(logs plog.Logs, f func(logRecord plog.LogRecord)) {
	resourceLogsSlice := logs.ResourceLogs()
	for i := 0; i < resourceLogsSlice.Len(); i++ {
		scopeLogsSlice := resourceLogsSlice.At(i).ScopeLogs()
		for j := 0; j < scopeLogsSlice.Len(); j++ {
			logs := scopeLogsSlice.At(j).LogRecords()
			for k := 0; k < logs.Len(); k++ {
				f(logs.At(k))
			}
		}
	}
}

func LogsWithResource(logs plog.Logs, f func(rs pcommon.Map, logRecord plog.LogRecord)) {
	resourceLogsSlice := logs.ResourceLogs()
	for i := 0; i < resourceLogsSlice.Len(); i++ {
		resourceLogs := resourceLogsSlice.At(i)
		rs := resourceLogs.Resource().Attributes()
		scopeLogsSlice := resourceLogs.ScopeLogs()
		for j := 0; j < scopeLogsSlice.Len(); j++ {
			logs := scopeLogsSlice.At(j).LogRecords()
			for k := 0; k < logs.Len(); k++ {
				f(rs, logs.At(k))
			}
		}
	}
}

func LogsSliceResource(logs plog.Logs, f func(rs pcommon.Resource)) {
	resourceLogsSlice := logs.ResourceLogs()
	for i := 0; i < resourceLogsSlice.Len(); i++ {
		resourceLogs := resourceLogsSlice.At(i)
		f(resourceLogs.Resource())
	}
}
