// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestServiceSizerTraces(t *testing.T) {
	sizer := newServiceSizer()

	t.Run("empty", func(t *testing.T) {
		sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: ptrace.NewTraces()})
		require.NoError(t, err)
		assert.Empty(t, sizes)
	})

	t.Run("single service", func(t *testing.T) {
		data := ptrace.NewTraces()
		appendTraceResource(data, "checkout", true, 128)
		appendTraceResource(data, "checkout", true, 256)

		sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: data})
		require.NoError(t, err)
		require.Len(t, sizes, 1)
		assert.Equal(t, sizer.tracesSizer.TracesSize(data), sizes["checkout"])
		assert.Equal(t, 2, data.ResourceSpans().Len())
	})

	t.Run("multiple services", func(t *testing.T) {
		data := ptrace.NewTraces()
		appendTraceResource(data, "checkout", true, 128)
		appendTraceResource(data, "payment", true, 256)
		appendTraceResource(data, "checkout", true, 512)

		sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: data})
		require.NoError(t, err)
		require.Len(t, sizes, 2)
		assert.Equal(t, sizer.tracesSizer.TracesSize(data), sumSizes(sizes))
		assert.Equal(t, 3, data.ResourceSpans().Len())
		assert.Equal(t, strings.Repeat("x", 256), data.ResourceSpans().At(1).ScopeSpans().At(0).Spans().At(0).Name())
	})

	t.Run("unknown service", func(t *testing.T) {
		data := ptrace.NewTraces()
		appendTraceResource(data, "", false, 64)
		appendTraceResource(data, "", true, 64)

		sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: data})
		require.NoError(t, err)
		require.Len(t, sizes, 1)
		assert.Equal(t, sizer.tracesSizer.TracesSize(data), sizes[unknownServiceName])
	})
}

func TestServiceSizerMetrics(t *testing.T) {
	sizer := newServiceSizer()
	data := pmetric.NewMetrics()
	appendMetricResource(data, "checkout", true, 128)
	appendMetricResource(data, "payment", true, 256)
	appendMetricResource(data, "checkout", true, 512)

	sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordMetrics, Data: data})
	require.NoError(t, err)
	require.Len(t, sizes, 2)
	assert.Equal(t, sizer.metricsSizer.MetricsSize(data), sumSizes(sizes))
	assert.Equal(t, 3, data.ResourceMetrics().Len())
}

func TestServiceSizerLogs(t *testing.T) {
	sizer := newServiceSizer()
	data := plog.NewLogs()
	appendLogResource(data, "checkout", true, 128)
	appendLogResource(data, "payment", true, 256)
	appendLogResource(data, "checkout", true, 512)

	sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordLogs, Data: data})
	require.NoError(t, err)
	require.Len(t, sizes, 2)
	assert.Equal(t, sizer.logsSizer.LogsSize(data), sumSizes(sizes))
	assert.Equal(t, 3, data.ResourceLogs().Len())
}

// TestServiceSizerDoesNotMutate 校验 MoveTo 借用计算后原始数据被完整复原，包括 Resource 顺序。
func TestServiceSizerDoesNotMutate(t *testing.T) {
	sizer := newServiceSizer()
	marshaler := ptrace.NewProtoMarshaler().(ptrace.Marshaler)

	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	appendTraceResource(data, "payment", true, 256)
	appendTraceResource(data, "checkout", true, 512)

	before, err := marshaler.MarshalTraces(data)
	require.NoError(t, err)

	sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: data})
	require.NoError(t, err)
	require.Len(t, sizes, 2)

	after, err := marshaler.MarshalTraces(data)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestServiceSizerDropRejected(t *testing.T) {
	sizer := newServiceSizer()
	rejected := map[string]struct{}{"payment": {}}

	t.Run("traces", func(t *testing.T) {
		data := ptrace.NewTraces()
		appendTraceResource(data, "checkout", true, 64)
		appendTraceResource(data, "payment", true, 64)
		appendTraceResource(data, "checkout", true, 64)

		remaining, err := sizer.DropRejected(&define.Record{RecordType: define.RecordTraces, Data: data}, rejected)
		require.NoError(t, err)
		assert.True(t, remaining)
		require.Equal(t, 2, data.ResourceSpans().Len())
		for i := 0; i < data.ResourceSpans().Len(); i++ {
			assert.Equal(t, "checkout", serviceName(data.ResourceSpans().At(i).Resource()))
		}
	})

	t.Run("metrics", func(t *testing.T) {
		data := pmetric.NewMetrics()
		appendMetricResource(data, "checkout", true, 64)
		appendMetricResource(data, "payment", true, 64)

		remaining, err := sizer.DropRejected(&define.Record{RecordType: define.RecordMetrics, Data: data}, rejected)
		require.NoError(t, err)
		assert.True(t, remaining)
		require.Equal(t, 1, data.ResourceMetrics().Len())
		assert.Equal(t, "checkout", serviceName(data.ResourceMetrics().At(0).Resource()))
	})

	t.Run("logs all rejected", func(t *testing.T) {
		data := plog.NewLogs()
		appendLogResource(data, "payment", true, 64)

		remaining, err := sizer.DropRejected(&define.Record{RecordType: define.RecordLogs, Data: data}, rejected)
		require.NoError(t, err)
		assert.False(t, remaining)
		assert.Zero(t, data.ResourceLogs().Len())
	})
}

func TestServiceSizerUnsupportedAndInvalidData(t *testing.T) {
	sizer := newServiceSizer()

	sizes, err := sizer.Sizes(&define.Record{RecordType: define.RecordProfiles})
	require.NoError(t, err)
	assert.Nil(t, sizes)

	_, err = sizer.Sizes(&define.Record{RecordType: define.RecordTraces, Data: pmetric.NewMetrics()})
	assert.Error(t, err)

	_, err = sizer.DropRejected(
		&define.Record{RecordType: define.RecordTraces, Data: pmetric.NewMetrics()},
		map[string]struct{}{"checkout": {}},
	)
	assert.Error(t, err)
}

func appendTraceResource(data ptrace.Traces, service string, setService bool, payloadBytes int) {
	resourceSpans := data.ResourceSpans().AppendEmpty()
	if setService {
		resourceSpans.Resource().Attributes().PutString(serviceNameKey, service)
	}
	span := resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName(strings.Repeat("x", payloadBytes))
}

func appendMetricResource(data pmetric.Metrics, service string, setService bool, payloadBytes int) {
	resourceMetrics := data.ResourceMetrics().AppendEmpty()
	if setService {
		resourceMetrics.Resource().Attributes().PutString(serviceNameKey, service)
	}
	metric := resourceMetrics.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName(strings.Repeat("x", payloadBytes))
	metric.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleVal(1)
}

func appendLogResource(data plog.Logs, service string, setService bool, payloadBytes int) {
	resourceLogs := data.ResourceLogs().AppendEmpty()
	if setService {
		resourceLogs.Resource().Attributes().PutString(serviceNameKey, service)
	}
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.Body().SetStr(strings.Repeat("x", payloadBytes))
}

func sumSizes(sizes map[string]int) int {
	var total int
	for _, size := range sizes {
		total += size
	}
	return total
}

var benchmarkServiceSizes map[string]int

// benchmarkAttrsPerItem Sizer 的开销由 Protobuf 字段数量决定而不是单个字符串的长度，
// 因此 Benchmark 以 span/datapoint/logrecord 条数和属性个数为变量构造数据。
const benchmarkAttrsPerItem = 20

func BenchmarkServiceSizer(b *testing.B) {
	for _, recordType := range []define.RecordType{define.RecordTraces, define.RecordMetrics, define.RecordLogs} {
		for _, services := range []int{1, 4, 20} {
			for _, itemsPerService := range []int{100, 1000} {
				record := benchmarkRecord(recordType, services, itemsPerService, benchmarkAttrsPerItem)
				sizer := newServiceSizer()
				sizes, err := sizer.Sizes(record)
				if err != nil {
					b.Fatal(err)
				}
				name := fmt.Sprintf(
					"%s/%d_services/%d_items_per_service/%dKiB",
					recordType.S(), services, itemsPerService, sumSizes(sizes)>>10,
				)
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						benchmarkServiceSizes, err = sizer.Sizes(record)
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func benchmarkRecord(recordType define.RecordType, services, itemsPerService, attrs int) *define.Record {
	record := &define.Record{RecordType: recordType}
	switch recordType {
	case define.RecordTraces:
		data := ptrace.NewTraces()
		for s := 0; s < services; s++ {
			resourceSpans := data.ResourceSpans().AppendEmpty()
			resourceSpans.Resource().Attributes().PutString(serviceNameKey, fmt.Sprintf("service-%d", s))
			spans := resourceSpans.ScopeSpans().AppendEmpty().Spans()
			for i := 0; i < itemsPerService; i++ {
				span := spans.AppendEmpty()
				span.SetName(fmt.Sprintf("GET /api/v1/resource/%d", i))
				span.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}))
				span.SetSpanID(pcommon.SpanID([8]byte{1, 2, 3, 4, 5, 6, 7, 8}))
				putBenchmarkAttrs(span.Attributes(), attrs)
			}
		}
		record.Data = data

	case define.RecordMetrics:
		data := pmetric.NewMetrics()
		for s := 0; s < services; s++ {
			resourceMetrics := data.ResourceMetrics().AppendEmpty()
			resourceMetrics.Resource().Attributes().PutString(serviceNameKey, fmt.Sprintf("service-%d", s))
			metric := resourceMetrics.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
			metric.SetName("rpc_server_handled_total")
			dataPoints := metric.SetEmptyGauge().DataPoints()
			for i := 0; i < itemsPerService; i++ {
				dataPoint := dataPoints.AppendEmpty()
				dataPoint.SetDoubleVal(float64(i))
				putBenchmarkAttrs(dataPoint.Attributes(), attrs)
			}
		}
		record.Data = data

	case define.RecordLogs:
		data := plog.NewLogs()
		for s := 0; s < services; s++ {
			resourceLogs := data.ResourceLogs().AppendEmpty()
			resourceLogs.Resource().Attributes().PutString(serviceNameKey, fmt.Sprintf("service-%d", s))
			logRecords := resourceLogs.ScopeLogs().AppendEmpty().LogRecords()
			for i := 0; i < itemsPerService; i++ {
				logRecord := logRecords.AppendEmpty()
				logRecord.Body().SetStr(fmt.Sprintf("request %d handled in 12ms", i))
				putBenchmarkAttrs(logRecord.Attributes(), attrs)
			}
		}
		record.Data = data
	}
	return record
}

func putBenchmarkAttrs(attributes pcommon.Map, n int) {
	for i := 0; i < n; i++ {
		attributes.PutString(fmt.Sprintf("attr.key.%d", i), strings.Repeat("v", 24))
	}
}
