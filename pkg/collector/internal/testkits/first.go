// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testkits

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func FirstSpan(traces ptrace.Traces) ptrace.Span {
	return traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
}

func FirstSpanAttrs(a any) pcommon.Map {
	traces := a.(ptrace.Traces)
	return traces.ResourceSpans().At(0).Resource().Attributes()
}

func FirstGaugeDataPoint(metrics pmetric.Metrics) pmetric.NumberDataPoint {
	return metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
}

func FirstHistogramPoint(metrics pmetric.Metrics) pmetric.HistogramDataPoint {
	return metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0)
}

func FirstSummaryPoint(metrics pmetric.Metrics) pmetric.SummaryDataPoint {
	return metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Summary().DataPoints().At(0)
}

func FirstSumPoint(metrics pmetric.Metrics) pmetric.NumberDataPoint {
	return metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
}

func FirstMetric(metrics pmetric.Metrics) pmetric.Metric {
	return metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
}

func FirstMetricAttrs(a any) pcommon.Map {
	metrics := a.(pmetric.Metrics)
	return metrics.ResourceMetrics().At(0).Resource().Attributes()
}

func FirstLogRecord(logs plog.Logs) plog.LogRecord {
	return logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
}

func FirstLogRecordAttrs(a any) pcommon.Map {
	logs := a.(plog.Logs)
	return logs.ResourceLogs().At(0).Resource().Attributes()
}
