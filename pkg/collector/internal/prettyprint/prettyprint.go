// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prettyprint

import (
	"runtime"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func Pretty(rtype define.RecordType, data interface{}) {
	// 只在 debug level 级别打印
	if logger.LoggerLevel() != logger.DebugLevelDesc {
		return
	}

	switch rtype {
	case define.RecordTraces:
		if traces, ok := data.(ptrace.Traces); ok {
			Traces(traces)
		}
	case define.RecordMetrics:
		if metrics, ok := data.(pmetric.Metrics); ok {
			Metrics(metrics)
		}
	}
}

func Traces(traces ptrace.Traces) {
	foreach.SpansWithResource(traces.ResourceSpans(), func(resource pcommon.Resource, span ptrace.Span) {
		logger.Debugf("Pretty/Tracing: resource=%#v, traceID=%s, spanID=%s, spanName=%s, spanKind=%s, spanStatus=%s, spanAttributes=%#v",
			resource.Attributes().AsRaw(),
			span.TraceID().HexString(),
			span.SpanID().HexString(),
			span.Name(),
			span.Kind().String(),
			span.Status().Code().String(),
			span.Attributes().AsRaw(),
		)
	})
}

func Metrics(metrics pmetric.Metrics) {
	foreach.MetricsWithResource(metrics.ResourceMetrics(), func(resource pcommon.Resource, metric pmetric.Metric) {
		logger.Debugf("Pretty/Metrics: resource=%#v, metric=%s, dataType=%s",
			resource.Attributes().AsRaw(),
			metric.Name(),
			metric.DataType().String(),
		)
	})
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}

func RuntimeMemStats(f func(format string, args ...interface{})) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// For info on each, see: https://golang.org/pkg/runtime/#MemStats
	f("Alloc: %v MiB\n", bToMb(m.Alloc))
	f("TotalAlloc: %v MiB\n", bToMb(m.TotalAlloc))
	f("Sys: %v MiB\n", bToMb(m.Sys))
	f("NumGC: %v\n", m.NumGC)
}
