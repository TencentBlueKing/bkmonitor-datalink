// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datadogrum

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestNewOtelConverterDefaultConvert(t *testing.T) {
	converter := NewConverter()

	result := converter.ToOTEL(&DatadogEventV2{
		Type:      "custom",
		EventType: "custom",
		Date:      1700000000000,
	})

	assert.Equal(t, 1, result.Logs.LogRecordCount())
	assert.Equal(t, 0, result.Traces.SpanCount())
	assert.Equal(t, 0, result.Metrics.MetricCount())
}

func TestSplitConversionResultUsesPdataCounts(t *testing.T) {
	converter := NewConverter()

	result := converter.ToOTEL(&DatadogEventV2{
		Type:      "performance",
		EventType: "resource",
		Date:      1700000000000,
		Data: map[string]interface{}{
			"resource": map[string]interface{}{
				"duration": float64(123),
				"size":     float64(456),
			},
		},
	})

	records := splitConversionResult(result)

	assert.Len(t, records, 3)
	assert.Equal(t, define.RecordLogs, records[0].rtype)
	assert.Equal(t, define.RecordTraces, records[1].rtype)
	assert.Equal(t, define.RecordMetrics, records[2].rtype)
}

func TestConverterResourceEventLogsOnlyOnErrorStatus(t *testing.T) {
	converter := NewConverter()

	successResult := converter.ToOTEL(&DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Resource: &ResourceData{
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/app.js",
		},
	})

	errorResult := converter.ToOTEL(&DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000001000,
		Resource: &ResourceData{
			StatusCode: 500,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/api",
		},
	})

	assert.Equal(t, 0, successResult.Logs.LogRecordCount())
	assert.Equal(t, 1, successResult.Traces.SpanCount())
	assert.Equal(t, 2, successResult.Metrics.MetricCount())

	assert.Equal(t, 1, errorResult.Logs.LogRecordCount())
	assert.Equal(t, 1, errorResult.Traces.SpanCount())
	assert.Equal(t, 2, errorResult.Metrics.MetricCount())
}

func TestConverterTraceAndSpanIDsAreNotZero(t *testing.T) {
	converter := NewConverter()
	event := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-123",
		},
		Resource: &ResourceData{
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/app.js",
		},
	}

	traces := converter.convertResourceToTraces(event)
	resourceSpans := traces.ResourceSpans()
	if !assert.Equal(t, 1, resourceSpans.Len()) {
		return
	}

	scopeSpans := resourceSpans.At(0).ScopeSpans()
	if !assert.Equal(t, 1, scopeSpans.Len()) {
		return
	}

	spans := scopeSpans.At(0).Spans()
	if !assert.Equal(t, 1, spans.Len()) {
		return
	}

	span := spans.At(0)
	assert.Equal(t, converter.generateTraceID(event), span.TraceID().HexString())
	assert.Equal(t, converter.generateSpanID(event), span.SpanID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 32), span.TraceID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 16), span.SpanID().HexString())
}

func TestConverterUsesSessionIDAsTraceID(t *testing.T) {
	converter := NewConverter()
	firstEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		View: &ViewData{ID: "view-1", URL: "https://example.com/first"},
	}
	secondEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000005000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		View: &ViewData{ID: "view-2", URL: "https://example.com/second"},
	}

	firstResult := converter.ToOTEL(firstEvent)
	secondResult := converter.ToOTEL(secondEvent)

	firstSpan, ok := getSingleSpan(firstResult.Traces)
	if !assert.True(t, ok) {
		return
	}

	secondSpan, ok := getSingleSpan(secondResult.Traces)
	if !assert.True(t, ok) {
		return
	}

	expectedTraceID := converter.generateTraceID(firstEvent)
	assert.Equal(t, expectedTraceID, converter.generateTraceID(secondEvent))
	assert.Equal(t, expectedTraceID, firstSpan.TraceID().HexString())
	assert.Equal(t, expectedTraceID, secondSpan.TraceID().HexString())
	assert.NotEqual(t, firstSpan.SpanID().HexString(), secondSpan.SpanID().HexString())
}

func TestConverterDifferentSessionIDsYieldDifferentTraceIDs(t *testing.T) {
	converter := NewConverter()
	firstEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		View: &ViewData{ID: "view-1", URL: "https://example.com/first"},
	}
	secondEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-def",
		},
		View: &ViewData{ID: "view-2", URL: "https://example.com/second"},
	}

	firstSpan, ok := getSingleSpan(converter.ToOTEL(firstEvent).Traces)
	if !assert.True(t, ok) {
		return
	}

	secondSpan, ok := getSingleSpan(converter.ToOTEL(secondEvent).Traces)
	if !assert.True(t, ok) {
		return
	}

	assert.NotEqual(t, converter.generateTraceID(firstEvent), converter.generateTraceID(secondEvent))
	assert.NotEqual(t, firstSpan.TraceID().HexString(), secondSpan.TraceID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 32), firstSpan.TraceID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 32), secondSpan.TraceID().HexString())
}

func TestConverterSameSessionSameTimestampUsesDifferentSpanIDs(t *testing.T) {
	converter := NewConverter()
	firstEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		View: &ViewData{ID: "view-1", URL: "https://example.com/first"},
	}
	secondEvent := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		View: &ViewData{ID: "view-2", URL: "https://example.com/second"},
	}

	firstResult := converter.ToOTEL(firstEvent)
	secondResult := converter.ToOTEL(secondEvent)

	firstSpan, ok := getSingleSpan(firstResult.Traces)
	if !assert.True(t, ok) {
		return
	}

	secondSpan, ok := getSingleSpan(secondResult.Traces)
	if !assert.True(t, ok) {
		return
	}

	assert.Equal(t, firstSpan.TraceID().HexString(), secondSpan.TraceID().HexString())
	assert.NotEqual(t, firstSpan.SpanID().HexString(), secondSpan.SpanID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 16), firstSpan.SpanID().HexString())
	assert.NotEqual(t, strings.Repeat("0", 16), secondSpan.SpanID().HexString())
}

func TestConverterResourceSpanIDUsesResourceID(t *testing.T) {
	converter := NewConverter()
	firstEvent := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		Resource: &ResourceData{
			ID:         "resource-1",
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/api/orders",
		},
	}
	secondEvent := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Session: map[string]interface{}{
			"id": "session-abc",
		},
		Resource: &ResourceData{
			ID:         "resource-2",
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/api/orders",
		},
	}

	firstSpan, ok := getSingleSpan(converter.ToOTEL(firstEvent).Traces)
	if !assert.True(t, ok) {
		return
	}

	secondSpan, ok := getSingleSpan(converter.ToOTEL(secondEvent).Traces)
	if !assert.True(t, ok) {
		return
	}

	expectedFirstSpanID := converter.hashToFixedHex("resource-1", 16)
	expectedSecondSpanID := converter.hashToFixedHex("resource-2", 16)

	assert.Equal(t, expectedFirstSpanID, converter.generateSpanID(firstEvent))
	assert.Equal(t, expectedSecondSpanID, converter.generateSpanID(secondEvent))
	assert.Equal(t, expectedFirstSpanID, firstSpan.SpanID().HexString())
	assert.Equal(t, expectedSecondSpanID, secondSpan.SpanID().HexString())
	assert.NotEqual(t, firstSpan.SpanID().HexString(), secondSpan.SpanID().HexString())
}

func TestConverterResourceDNSEventsAreConditional(t *testing.T) {
	converter := NewConverter()

	withoutDNS := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Resource: &ResourceData{
			ID:         "resource-without-dns",
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/api/orders",
		},
	}

	withDNS := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000001000,
		Resource: &ResourceData{
			ID:         "resource-with-dns",
			StatusCode: 200,
			Duration:   42000000,
			Size:       512,
			URL:        "https://example.com/api/orders",
			DNS: &ResourceTiming{
				Start:    1,
				Duration: 1,
			},
		},
	}

	withoutDNSSpan, ok := getSingleSpan(converter.ToOTEL(withoutDNS).Traces)
	if !assert.True(t, ok) {
		return
	}

	withDNSSpan, ok := getSingleSpan(converter.ToOTEL(withDNS).Traces)
	if !assert.True(t, ok) {
		return
	}

	// withoutDNS：fetchStart + connectStart/End (无DNS，无FirstByte，无Download) = 3
	// withDNS：fetchStart + DNS(2) + connectStart/End + FirstByte(2) (无Download) = 7
	assert.Equal(t, 3, withoutDNSSpan.Events().Len())
	assert.Equal(t, 5, withDNSSpan.Events().Len())
	assert.Equal(t, "domainLookupStart", withDNSSpan.Events().At(1).Name())
	assert.Equal(t, "domainLookupEnd", withDNSSpan.Events().At(2).Name())
	assert.Equal(
		t,
		withDNSSpan.Events().At(0).Timestamp()+pcommon.Timestamp(1),
		withDNSSpan.Events().At(1).Timestamp(),
	)
	assert.Equal(
		t,
		withDNSSpan.Events().At(0).Timestamp()+pcommon.Timestamp(2),
		withDNSSpan.Events().At(2).Timestamp(),
	)
}

func TestConverterEventSpecificSpanIDUsesEventID(t *testing.T) {
	converter := NewConverter()
	testCases := []struct {
		name           string
		event          *DatadogEventV2
		expectedIDSeed string
	}{
		{
			name: "view id",
			event: &DatadogEventV2{
				Type:      "view",
				EventType: "view",
				Date:      1700000000000,
				Session: map[string]interface{}{
					"id": "session-abc",
				},
				View: &ViewData{ID: "view-123", URL: "https://example.com/view-a"},
			},
			expectedIDSeed: "view-123",
		},
		{
			name: "action id",
			event: &DatadogEventV2{
				Type:      "action",
				EventType: "action",
				Date:      1700000001000,
				Session: map[string]interface{}{
					"id": "session-abc",
				},
				Action: map[string]interface{}{
					"id":   "action-123",
					"type": "click",
				},
			},
			expectedIDSeed: "action-123",
		},
		{
			name: "long task id",
			event: &DatadogEventV2{
				Type:      "long_task",
				EventType: "long_task",
				Date:      1700000002000,
				Session: map[string]interface{}{
					"id": "session-abc",
				},
				LongTask: map[string]interface{}{
					"id":          "longtask-123",
					"duration":    float64(71435000),
					"attribution": "script",
				},
			},
			expectedIDSeed: "longtask-123",
		},
		{
			name: "performance resource id",
			event: &DatadogEventV2{
				Type:      "performance",
				EventType: "resource",
				Date:      1700000003000,
				Session: map[string]interface{}{
					"id": "session-abc",
				},
				Data: map[string]interface{}{
					"resource": map[string]interface{}{
						"id":       "perf-resource-123",
						"duration": float64(123),
						"size":     float64(456),
					},
				},
			},
			expectedIDSeed: "perf-resource-123",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			span, ok := getSingleSpan(converter.ToOTEL(testCase.event).Traces)
			if !assert.True(t, ok) {
				return
			}

			expectedSpanID := converter.hashToFixedHex(testCase.expectedIDSeed, 16)

			assert.Equal(t, expectedSpanID, converter.generateSpanID(testCase.event))
			assert.Equal(t, expectedSpanID, span.SpanID().HexString())
		})
	}
}

func getSingleSpan(traces ptrace.Traces) (ptrace.Span, bool) {
	resourceSpans := traces.ResourceSpans()
	if resourceSpans.Len() != 1 {
		return ptrace.NewSpan(), false
	}

	scopeSpans := resourceSpans.At(0).ScopeSpans()
	if scopeSpans.Len() != 1 {
		return ptrace.NewSpan(), false
	}

	spans := scopeSpans.At(0).Spans()
	if spans.Len() != 1 {
		return ptrace.NewSpan(), false
	}

	return spans.At(0), true
}

// ======== 资源计时事件转换单元测试 ========

// TestGetTimingValues 测试时间值计算的正确性。
func TestGetTimingValues(t *testing.T) {
	converter := NewConverter()
	baseTime := pcommon.Timestamp(1774251945412000000)

	testCases := []struct {
		name               string
		timing             *ResourceTiming
		expectedStartDelta int64
		expectedEndDelta   int64
	}{
		{
			name: "正常情况：start=66.2ms, duration=0.7ms",
			timing: &ResourceTiming{
				Start:    66200000, // 66.2ms in nanoseconds
				Duration: 700000,   // 0.7ms in nanoseconds
			},
			expectedStartDelta: 66200000,
			expectedEndDelta:   66900000,
		},
		{
			name: "0ms的情况：start=0, duration=0",
			timing: &ResourceTiming{
				Start:    0,
				Duration: 0,
			},
			expectedStartDelta: 0,
			expectedEndDelta:   0,
		},
		{
			name: "大时间值：start=129.8ms, duration=1.7ms",
			timing: &ResourceTiming{
				Start:    129800000,
				Duration: 1700000,
			},
			expectedStartDelta: 129800000,
			expectedEndDelta:   131500000,
		},
		{
			name:   "nil值：应返回基准时间",
			timing: nil,
			// when nil, both should return baseTime
			expectedStartDelta: 0,
			expectedEndDelta:   0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startTs, endTs := converter.getTimingValues(tc.timing, baseTime)

			if tc.timing == nil {
				// For nil timing, both should equal baseTime
				assert.Equal(t, baseTime, startTs)
				assert.Equal(t, baseTime, endTs)
			} else {
				// Verify start timestamp = baseTime + expectedStartDelta
				expectedStart := baseTime + pcommon.Timestamp(tc.expectedStartDelta)
				assert.Equal(t, expectedStart, startTs, "startTs mismatch")

				// Verify end timestamp = baseTime + expectedEndDelta
				expectedEnd := baseTime + pcommon.Timestamp(tc.expectedEndDelta)
				assert.Equal(t, expectedEnd, endTs, "endTs mismatch")

				// Verify endTs >= startTs
				assert.Greater(t, int64(endTs), int64(startTs)-1, "endTs should be >= startTs")
			}
		})
	}
}

// TestAddResourceTimingEventsWithAllTimings 测试包含所有计时数据的完整场景
func TestAddResourceTimingEventsWithAllTimings(t *testing.T) {
	converter := NewConverter()
	baseTime := pcommon.Timestamp(1774251945412000000)

	// 根据用户提供的真实数据构造测试数据
	resource := &ResourceData{
		ID:       "94e667e0-98fc-481a-9cd2-d19c113ba6b3",
		Type:     "css",
		URL:      "http://localhost:8000/assets/css/bootstrap.css",
		Duration: 131500000,
		DNS: &ResourceTiming{
			Start:    66200000,
			Duration: 0,
		},
		Connect: &ResourceTiming{
			Start:    66200000,
			Duration: 700000,
		},
		FirstByte: &ResourceTiming{
			Start:    67600000,
			Duration: 62200000,
		},
		Download: &ResourceTiming{
			Start:    129800000,
			Duration: 1700000,
		},
	}

	// 创建 span
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	// 调用被测试函数
	converter.addResourceTimingEvents(span, resource, baseTime)

	// 验证事件数量：fetchStart + 2*DNS + 2*Connect + 2*FirstByte + 1*Download = 8
	assert.Equal(t, 8, span.Events().Len(), "期望8个timing事件")

	// 验证事件顺序和时间戳
	events := span.Events()

	expectedEvents := []struct {
		name  string
		index int
		delta int64 // 相对于baseTime的时间偏移（单位：纳秒）
	}{
		{"fetchStart", 0, 0},
		{"domainLookupStart", 1, 66200000},
		{"domainLookupEnd", 2, 66200000},
		{"connectStart", 3, 66200000},
		{"connectEnd", 4, 66900000},
		{"requestStart", 5, 67600000},
		{"responseStart", 6, 129800000},
		{"responseEnd", 7, 131500000},
	}

	for _, expected := range expectedEvents {
		t.Run(expected.name, func(t *testing.T) {
			event := events.At(expected.index)
			assert.Equal(t, expected.name, event.Name(), "事件名称错误")

			expectedTime := baseTime + pcommon.Timestamp(expected.delta)
			assert.Equal(t, expectedTime, event.Timestamp(), "时间戳错误")
		})
	}

	// 验证时间的单调递增性
	for i := 1; i < events.Len(); i++ {
		curr := events.At(i).Timestamp()
		prev := events.At(i - 1).Timestamp()
		assert.GreaterOrEqual(t, int64(curr), int64(prev), "事件时间应单调递增")
	}
}

// TestAddResourceTimingEventsWithMissingTimings 测试缺失计时数据的处理
func TestAddResourceTimingEventsWithMissingTimings(t *testing.T) {
	converter := NewConverter()
	baseTime := pcommon.Timestamp(1774251945412000000)

	testCases := []struct {
		name               string
		resource           *ResourceData
		expectedEventCount int
		expectedNames      []string
	}{
		{
			name: "仅有Download，其他为nil",
			resource: &ResourceData{
				Download: &ResourceTiming{
					Start:    100000000,
					Duration: 2000000,
				},
			},
			expectedEventCount: 4, // fetchStart, connectStart, connectEnd, responseEnd
			expectedNames: []string{
				"fetchStart",
				"connectStart",
				"connectEnd",
				"responseEnd",
			},
		},
		{
			name: "仅有FirstByte",
			resource: &ResourceData{
				FirstByte: &ResourceTiming{
					Start:    50000000,
					Duration: 30000000,
				},
			},
			expectedEventCount: 5, // fetchStart, connectStart, connectEnd, requestStart, responseStart
			expectedNames: []string{
				"fetchStart",
				"connectStart",
				"connectEnd",
				"requestStart",
				"responseStart",
			},
		},
		{
			name:               "所有计时数据都为nil",
			resource:           &ResourceData{},
			expectedEventCount: 3, // fetchStart, connectStart(fallback), connectEnd(fallback)
			expectedNames: []string{
				"fetchStart",
				"connectStart",
				"connectEnd",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			traces := ptrace.NewTraces()
			resourceSpans := traces.ResourceSpans().AppendEmpty()
			scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
			span := scopeSpans.Spans().AppendEmpty()

			converter.addResourceTimingEvents(span, tc.resource, baseTime)

			assert.Equal(t, tc.expectedEventCount, span.Events().Len(), "事件数量错误")

			// 验证前几个事件的名称
			for i, expectedName := range tc.expectedNames {
				if i < span.Events().Len() {
					assert.Equal(t, expectedName, span.Events().At(i).Name(), "事件名称错误")
				}
			}
		})
	}
}

// TestAddResourceTimingEventsWithNilResource 测试nil资源的处理
func TestAddResourceTimingEventsWithNilResource(t *testing.T) {
	converter := NewConverter()
	baseTime := pcommon.Timestamp(1774251945412000000)

	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	// 传入nil资源，应该不添加任何事件
	converter.addResourceTimingEvents(span, nil, baseTime)

	assert.Equal(t, 0, span.Events().Len(), "nil资源应该不添加事件")
}

// TestResourceToTracesWithCompleteTimings 集成测试：完整场景转换
func TestResourceToTracesWithCompleteTimings(t *testing.T) {
	converter := NewConverter()

	// 使用真实的Datadog RUM资源事件数据
	event := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		View:      &ViewData{ID: "view-123", URL: "https://example.com/page"},
		Resource: &ResourceData{
			ID:                   "94e667e0-98fc-481a-9cd2-d19c113ba6b3",
			Type:                 "css",
			URL:                  "http://localhost:8000/assets/css/bootstrap.css",
			StatusCode:           200,
			Duration:             131500000, // 131.5ms in nanoseconds
			DeliveryType:         "network",
			RenderBlockingStatus: "blocking",
			Size:                 142104,
			Protocol:             "http/1.1",
			DNS: &ResourceTiming{
				Start:    66200000,
				Duration: 0,
			},
			Connect: &ResourceTiming{
				Start:    66200000,
				Duration: 700000,
			},
			FirstByte: &ResourceTiming{
				Start:    67600000,
				Duration: 62200000,
			},
			Download: &ResourceTiming{
				Start:    129800000,
				Duration: 1700000,
			},
		},
	}

	// 执行转换
	traces := converter.convertResourceToTraces(event)

	// 获取span
	span, ok := getSingleSpan(traces)
	if !assert.True(t, ok) {
		return
	}

	// 验证span基本属性
	// CSS 文件属于静态资源
	assert.Equal(t, "resourceFetch", span.Name())
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())

	// 验证DNS事件的存在和时间戳
	// fetchStart(0) + DNS(2) + Connect(2) + FirstByte(2) + Download(1) = 8
	assert.Equal(t, 8, span.Events().Len(), "应该有8个timing事件")

	// 验证第一个和最后一个事件
	firstEvent := span.Events().At(0)
	lastEvent := span.Events().At(span.Events().Len() - 1)

	assert.Equal(t, "fetchStart", firstEvent.Name())
	assert.Equal(t, "responseEnd", lastEvent.Name())

	// 验证span属性包含资源信息
	attrs := span.Attributes()
	assert.Equal(t, "resource", attrs.AsRaw()["event.type"])
	assert.Equal(t, "http://localhost:8000/assets/css/bootstrap.css", attrs.AsRaw()["http.url"])
	assert.Equal(t, int64(200), attrs.AsRaw()["http.status_code"])
}

func TestConverterViewTraceHandlesMissingViewData(t *testing.T) {
	converter := NewConverter()
	event := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		Resource: &ResourceData{
			StatusCode: 200,
			URL:        "https://example.com/document",
		},
	}

	assert.NotPanics(t, func() {
		result := converter.ToOTEL(event)
		span, ok := getSingleSpan(result.Traces)
		if !assert.True(t, ok) {
			return
		}

		assert.Equal(t, spanNameResourceLoad, span.Name())
	})
}

func TestConverterResourceDurationUsesNanoseconds(t *testing.T) {
	converter := NewConverter()
	event := &DatadogEventV2{
		Type:      "resource",
		EventType: "resource",
		Date:      1700000000000,
		Resource: &ResourceData{
			StatusCode: 200,
			Duration:   42000000,
			URL:        "https://example.com/api/orders",
		},
	}

	span, ok := getSingleSpan(converter.convertResourceToTraces(event))
	if !assert.True(t, ok) {
		return
	}

	assert.Equal(t, pcommon.Timestamp(42000000), span.EndTimestamp()-span.StartTimestamp())
	assert.Equal(t, int64(42000000), int64(span.EndTimestamp()-span.StartTimestamp()))
	assert.Equal(t, span.StartTimestamp()+pcommon.Timestamp(42000000), span.EndTimestamp())
	assert.Equal(t, 42.0, converterDurationMetricSum(t, converter.convertToMetrics(event)))
}

func TestConverterViewINPEventRequiresTimestamp(t *testing.T) {
	converter := NewConverter()
	event := &DatadogEventV2{
		Type:      "view",
		EventType: "view",
		Date:      1700000000000,
		View: &ViewData{
			ID:                     "view-1",
			InteractionToNextPaint: 123,
		},
	}

	span, ok := getSingleSpan(converter.convertViewToTraces(event))
	if !assert.True(t, ok) {
		return
	}

	for i := 0; i < span.Events().Len(); i++ {
		assert.NotEqual(t, "interactionToNextPaint", span.Events().At(i).Name())
	}
	assert.Equal(t, int64(123), span.Attributes().AsRaw()["view.interaction_to_next_paint"])
}

func converterDurationMetricSum(t *testing.T, metrics pmetric.Metrics) float64 {
	t.Helper()

	resourceMetrics := metrics.ResourceMetrics()
	if !assert.Equal(t, 1, resourceMetrics.Len()) {
		return 0
	}

	scopeMetrics := resourceMetrics.At(0).ScopeMetrics()
	if !assert.Equal(t, 1, scopeMetrics.Len()) {
		return 0
	}

	metricSlice := scopeMetrics.At(0).Metrics()
	if !assert.GreaterOrEqual(t, metricSlice.Len(), 1) {
		return 0
	}

	durationMetric := metricSlice.At(0)
	if !assert.Equal(t, "http.client.duration_ms", durationMetric.Name()) {
		return 0
	}

	dataPoints := durationMetric.Histogram().DataPoints()
	if !assert.Equal(t, 1, dataPoints.Len()) {
		return 0
	}

	return dataPoints.At(0).Sum()
}

// TestAddTimingEventAddsEventToSpan 测试单个timing事件的添加
func TestAddTimingEventAddsEventToSpan(t *testing.T) {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	baseTime := pcommon.Timestamp(1774251945412000000)

	// 添加第一个事件
	addTimingEvent(span, "fetchStart", baseTime)
	assert.Equal(t, 1, span.Events().Len())

	// 添加第二个事件
	addTimingEvent(span, "domainLookupStart", baseTime+pcommon.Timestamp(1000000))
	assert.Equal(t, 2, span.Events().Len())

	// 验证事件内容
	event1 := span.Events().At(0)
	event2 := span.Events().At(1)

	assert.Equal(t, "fetchStart", event1.Name())
	assert.Equal(t, baseTime, event1.Timestamp())
	assert.Equal(t, "domainLookupStart", event2.Name())
	assert.Equal(t, baseTime+pcommon.Timestamp(1000000), event2.Timestamp())
}

// TestTimingEventsMonotonic 验证timing事件的时间单调性
func TestTimingEventsMonotonic(t *testing.T) {
	converter := NewConverter()
	baseTime := pcommon.Timestamp(1774251945412000000)

	// 构造资源数据：各阶段耗时递增
	resource := &ResourceData{
		DNS: &ResourceTiming{
			Start:    10000000, // 10ms
			Duration: 5000000,  // 5ms -> 15ms
		},
		Connect: &ResourceTiming{
			Start:    20000000, // 20ms
			Duration: 10000000, // 10ms -> 30ms
		},
		FirstByte: &ResourceTiming{
			Start:    40000000, // 40ms
			Duration: 20000000, // 20ms -> 60ms
		},
		Download: &ResourceTiming{
			Start:    70000000, // 70ms
			Duration: 30000000, // 30ms -> 100ms
		},
	}

	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	converter.addResourceTimingEvents(span, resource, baseTime)

	// 验证所有事件的时间戳单调递增
	events := span.Events()
	for i := 1; i < events.Len(); i++ {
		prevTs := events.At(i - 1).Timestamp()
		currTs := events.At(i).Timestamp()
		assert.GreaterOrEqual(t, int64(currTs), int64(prevTs),
			"事件 %d(%s) 的时间戳应该 >= 事件 %d(%s) 的时间戳",
			i, events.At(i).Name(), i-1, events.At(i-1).Name())
	}
}
