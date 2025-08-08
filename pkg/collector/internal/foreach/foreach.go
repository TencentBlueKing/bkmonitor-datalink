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

func Spans(resourceSpansSlice ptrace.ResourceSpansSlice, f func(span ptrace.Span)) {
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

func SpansWithResourceAttrs(resourceSpansSlice ptrace.ResourceSpansSlice, f func(rsAttrs pcommon.Map, span ptrace.Span)) {
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		rsAttrs := resourceSpans.Resource().Attributes()
		scopeSpansSlice := resourceSpans.ScopeSpans()
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			spans := scopeSpansSlice.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				f(rsAttrs, spans.At(k))
			}
		}
	}
}

func SpansRemoveIf(resourceSpansSlice ptrace.ResourceSpansSlice, f func(span ptrace.Span) bool) {
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

func SpansSliceResource(resourceSpansSlice ptrace.ResourceSpansSlice, f func(rs pcommon.Resource)) {
	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		f(resourceSpans.Resource())
	}
}

func Metrics(resourceMetricsSlice pmetric.ResourceMetricsSlice, f func(metric pmetric.Metric)) {
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

func MetricsWithResourceAttrs(resourceMetricsSlice pmetric.ResourceMetricsSlice, f func(rsAttrs pcommon.Map, metric pmetric.Metric)) {
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		rsAttrs := resourceMetrics.Resource().Attributes()
		scopeMetricsSlice := resourceMetrics.ScopeMetrics()
		for j := 0; j < scopeMetricsSlice.Len(); j++ {
			metrics := scopeMetricsSlice.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				f(rsAttrs, metrics.At(k))
			}
		}
	}
}

func MetricsSliceResource(resourceMetricsSlice pmetric.ResourceMetricsSlice, f func(rs pcommon.Resource)) {
	for i := 0; i < resourceMetricsSlice.Len(); i++ {
		resourceMetrics := resourceMetricsSlice.At(i)
		f(resourceMetrics.Resource())
	}
}

// MetricsSliceDataPointsAttrs 遍历 MetricsSlice 的所有数据点属性
func MetricsSliceDataPointsAttrs(resourceMetricsSlice pmetric.ResourceMetricsSlice, f func(name string, attrs pcommon.Map)) {
	Metrics(resourceMetricsSlice, func(metric pmetric.Metric) {
		MetricDataPointsAttrs(metric, func(attrs pcommon.Map) {
			f(metric.Name(), attrs)
		})
	})
}

// MetricDataPointsAttrs 遍历单个 Metric 的数据点属性
func MetricDataPointsAttrs(metric pmetric.Metric, f func(attrs pcommon.Map)) {
	switch metric.DataType() {
	case pmetric.MetricDataTypeGauge:
		dps := metric.Gauge().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			attrs := dps.At(i).Attributes()
			f(attrs)
		}
	case pmetric.MetricDataTypeSum:
		dps := metric.Sum().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			attrs := dps.At(i).Attributes()
			f(attrs)
		}
	case pmetric.MetricDataTypeSummary:
		dps := metric.Summary().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			attrs := dps.At(i).Attributes()
			f(attrs)
		}
	case pmetric.MetricDataTypeHistogram:
		dps := metric.Histogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			attrs := dps.At(i).Attributes()
			f(attrs)
		}
	case pmetric.MetricDataTypeExponentialHistogram:
		dps := metric.ExponentialHistogram().DataPoints()
		for i := 0; i < dps.Len(); i++ {
			attrs := dps.At(i).Attributes()
			f(attrs)
		}
	}
}

func Logs(resourceLogsSlice plog.ResourceLogsSlice, f func(logRecord plog.LogRecord)) {
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

func LogsWithResourceAttrs(resourceLogsSlice plog.ResourceLogsSlice, f func(rsAttrs pcommon.Map, logRecord plog.LogRecord)) {
	for i := 0; i < resourceLogsSlice.Len(); i++ {
		resourceLogs := resourceLogsSlice.At(i)
		rsAttrs := resourceLogs.Resource().Attributes()
		scopeLogsSlice := resourceLogs.ScopeLogs()
		for j := 0; j < scopeLogsSlice.Len(); j++ {
			logs := scopeLogsSlice.At(j).LogRecords()
			for k := 0; k < logs.Len(); k++ {
				f(rsAttrs, logs.At(k))
			}
		}
	}
}

func LogsSliceResource(resourceLogsSlice plog.ResourceLogsSlice, f func(rs pcommon.Resource)) {
	for i := 0; i < resourceLogsSlice.Len(); i++ {
		resourceLogs := resourceLogsSlice.At(i)
		f(resourceLogs.Resource())
	}
}
