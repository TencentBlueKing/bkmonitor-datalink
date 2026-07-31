// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

type (
	attributeString  string
	attributeInt     int
	attributeUint    uint
	attributeFloat   float32
	attributeBool    bool
	attributeStrings []string
)

func TestAttributeBuilderSetScalarsPointersAliasesAndSlices(t *testing.T) {
	attrs := pcommon.NewMap()
	zeroInt := attributeInt(0)
	zeroUint := attributeUint(0)
	zeroFloat := attributeFloat(0)
	falseValue := attributeBool(false)
	nilString := (*attributeString)(nil)
	nilInt := (*attributeInt)(nil)

	builder := NewAttributeBuilder(attrs)
	assert.Same(t, builder, builder.Set("string", attributeString("value")))
	builder.Set("signed", attributeInt(-7))
	builder.Set("unsigned", attributeUint(7))
	builder.Set("float", attributeFloat(1.5))
	builder.Set("bool", attributeBool(true))
	builder.Set("zero_int", zeroInt)
	builder.Set("zero_uint", zeroUint)
	builder.Set("zero_float", zeroFloat)
	builder.Set("false", falseValue)
	builder.Set("pointer_int", &zeroInt)
	pointerString := attributeString("pointer")
	builder.Set("pointer_string", &pointerString)
	builder.Set("nil_string", nilString)
	builder.Set("nil_int", nilInt)
	builder.Set("strings", []string{"first", ""})
	builder.Set("named_strings", attributeStrings{"named", ""})
	assert.Same(t, builder, builder.SetAll(map[string]interface{}{
		"set_all_string": attributeString("set-all"),
		"set_all_int":    int64(0),
		"set_all_bool":   false,
	}))
	builder.Set("empty_string", "")

	raw := attrs.AsRaw()
	assert.Equal(t, "value", raw["string"])
	assert.Equal(t, int64(-7), raw["signed"])
	assert.Equal(t, int64(7), raw["unsigned"])
	assert.InDelta(t, 1.5, raw["float"], 0)
	assert.Equal(t, true, raw["bool"])
	assert.Equal(t, int64(0), raw["zero_int"])
	assert.Equal(t, int64(0), raw["zero_uint"])
	assert.Equal(t, float64(0), raw["zero_float"])
	assert.Equal(t, false, raw["false"])
	assert.Equal(t, int64(0), raw["pointer_int"])
	assert.Equal(t, "pointer", raw["pointer_string"])
	assert.Equal(t, []interface{}{"first", ""}, raw["strings"])
	assert.Equal(t, []interface{}{"named", ""}, raw["named_strings"])
	assert.Equal(t, "set-all", raw["set_all_string"])
	assert.Equal(t, int64(0), raw["set_all_int"])
	assert.Equal(t, false, raw["set_all_bool"])
	assert.NotContains(t, raw, "empty_string")
	assert.NotContains(t, raw, "nil_string")
	assert.NotContains(t, raw, "nil_int")
}

func TestAttributeBuilderSetOversizedUnsignedAsString(t *testing.T) {
	attrs := pcommon.NewMap()
	maxInt64 := uint64(1<<63 - 1)
	builder := NewAttributeBuilder(attrs)
	builder.Set("max_int64", maxInt64)
	builder.Set("oversized", maxInt64+1)

	raw := attrs.AsRaw()
	assert.Equal(t, int64(maxInt64), raw["max_int64"])
	assert.Equal(t, "9223372036854775808", raw["oversized"])
}

func TestConvertCreatesOneSpanPerEvent(t *testing.T) {
	date := int64(1_700_000_000_000)
	batch := &model.Batch{Events: []model.Event{
		&model.ViewEvent{CommonFields: common(date, model.EventTypeView, "view-1"), View: model.View{ViewContext: model.ViewContext{ID: "view-1", Name: "home"}}},
		&model.ViewEvent{CommonFields: common(date+1, model.EventTypeViewUpdate, "view-update-1"), View: model.View{ViewContext: model.ViewContext{ID: "view-1"}}},
		&model.ActionEvent{CommonFields: common(date+2, model.EventTypeAction, "action-1"), Action: model.Action{ID: "action-1", Type: "click"}},
		&model.ResourceEvent{CommonFields: common(date+3, model.EventTypeResource, "resource-1"), Resource: model.Resource{ID: "resource-1", Method: "get"}},
		&model.ErrorEvent{CommonFields: common(date+4, model.EventTypeError, "error-1"), Error: model.Error{ID: "error-1", Message: "boom"}},
		&model.LongTaskEvent{CommonFields: common(date+5, model.EventTypeLongTask, "long-task-1"), LongTask: model.LongTask{ID: "long-task-1"}},
		&model.VitalEvent{CommonFields: common(date+6, model.EventTypeVital, "vital-1"), Vital: model.Vital{ID: "vital-1", Name: "fcp"}},
	}}

	traces := Convert(batch, "")
	require.Equal(t, 7, traces.ResourceSpans().Len())
	for i := 0; i < traces.ResourceSpans().Len(); i++ {
		scopeSpans := traces.ResourceSpans().At(i).ScopeSpans()
		require.Equal(t, 1, scopeSpans.Len())
		require.Equal(t, "bk-rum", scopeSpans.At(0).Scope().Name())
		require.Equal(t, defaultSDKVersion, scopeSpans.At(0).Scope().Version())
		require.Equal(t, 1, scopeSpans.At(0).Spans().Len())
		span := scopeSpans.At(0).Spans().At(0)
		assert.False(t, span.TraceID().IsEmpty())
		assert.False(t, span.SpanID().IsEmpty())
		if i != 3 {
			assert.Equal(t, ptrace.SpanKindInternal, span.Kind(), "event %d", i)
		}
		assert.Equal(t, "session-1", traces.ResourceSpans().At(i).Resource().Attributes().AsRaw()["session.id"])
	}
	assert.Equal(t, "page.view", traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, "ui.action", traces.ResourceSpans().At(2).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, ptrace.SpanKindClient, traces.ResourceSpans().At(3).ScopeSpans().At(0).Spans().At(0).Kind())
	assert.Equal(t, "exception", traces.ResourceSpans().At(4).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, "browser.long_task", traces.ResourceSpans().At(5).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, "vital.fcp", traces.ResourceSpans().At(6).ScopeSpans().At(0).Spans().At(0).Name())
}

func TestConvertIDsAndResourceAttributes(t *testing.T) {
	commonFields := common(1_700_000_000_000, model.EventTypeView, "view-1")
	commonFields.Application = model.Application{ID: "app-id", Name: "shop"}
	commonFields.Service = "fallback-service"
	commonFields.Version = "1.2.3"
	commonFields.Tags = "team:web,env:staging"
	commonFields.User = &model.User{ID: "user-1", AnonymousID: "anon-1"}
	commonFields.Internal = &model.InternalFields{
		TraceID:           "00112233445566778899aabbccddeeff",
		SpanID:            "0011223344556677",
		BrowserSDKVersion: "5.0.0",
		Configuration:     &model.Configuration{SessionSampleRate: float64Ptr(25)},
	}
	commonFields.Raw = json.RawMessage(`{"type":"view","unknown":{"kept":true}}`)

	traces := Convert(&model.Batch{Events: []model.Event{&model.ViewEvent{
		CommonFields: commonFields,
		View:         model.View{ViewContext: model.ViewContext{ID: "view-1"}},
	}}}, "Mozilla/5.0 (test)")
	require.Equal(t, 1, traces.ResourceSpans().Len())
	resource := traces.ResourceSpans().At(0)
	attrs := resource.Resource().Attributes().AsRaw()
	assert.Equal(t, "shop", attrs["service.name"])
	assert.Equal(t, "1.2.3", attrs["service.version"])
	assert.Equal(t, "Mozilla/5.0 (test)", attrs["http.user_agent"])
	assert.Equal(t, "staging", attrs["deployment.environment.name"])
	assert.InDelta(t, 0.25, attrs["session.sample_rate"], 0.0001)
	assert.Equal(t, "5.0.0", resource.ScopeSpans().At(0).Scope().Version())

	span := resource.ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "00112233445566778899aabbccddeeff", span.TraceID().HexString())
	assert.Equal(t, "0011223344556677", span.SpanID().HexString())

	withoutIDs := commonFields
	withoutIDs.Internal = &model.InternalFields{}
	withoutIDs.Session = &model.Session{ID: "session-other"}
	other := Convert(&model.Batch{Events: []model.Event{&model.ViewEvent{
		CommonFields: withoutIDs,
		View:         model.View{ViewContext: model.ViewContext{ID: "view-other"}},
	}}}, "")
	assert.NotEqual(t, span.TraceID().HexString(), other.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID().HexString())
}

func TestConvertViewResourceErrorAndLongTaskDetails(t *testing.T) {
	fcp := int64(10 * time.Millisecond)
	lcp := int64(20 * time.Millisecond)
	timingStart := int64(5 * time.Millisecond)
	duration := int64(30 * time.Millisecond)
	loadingTime := int64(25 * time.Millisecond)
	timeSpent := int64(35 * time.Millisecond)
	status := 503
	performance := &model.ViewPerformance{
		FCP:       &model.FCPPerformance{Timestamp: &fcp},
		LCP:       &model.LCPPerformance{Timestamp: &lcp, TargetSelector: "#hero"},
		FirstByte: &duration,
	}
	resource := &model.ResourceEvent{CommonFields: common(1000, model.EventTypeResource, "resource"), Resource: model.Resource{
		Method: "get", StatusCode: &status, Duration: &duration,
		Timing: &model.ResourceTiming{DNS: &model.Timing{Start: &timingStart, Duration: &duration}},
	}}
	view := &model.ViewEvent{CommonFields: common(1000, model.EventTypeView, "view"), View: model.View{
		ViewContext: model.ViewContext{ID: "view"}, Performance: performance,
		LoadingTime: &loadingTime, TimeSpent: &timeSpent,
	}}
	errorEvent := &model.ErrorEvent{CommonFields: common(1000, model.EventTypeError, "error"), Error: model.Error{Message: "failed"}}
	longTaskDuration := int64(40 * time.Millisecond)
	longTask := &model.LongTaskEvent{CommonFields: common(1000, model.EventTypeLongTask, "long"), LongTask: model.LongTask{
		Duration: &longTaskDuration,
	}}

	traces := Convert(&model.Batch{Events: []model.Event{view, resource, errorEvent, longTask}}, "")
	start := pcommon.NewTimestampFromTime(time.UnixMilli(1000))
	viewSpan := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, start, viewSpan.StartTimestamp())
	assert.Equal(t, pcommonTimestamp(int64(35*time.Millisecond)), viewSpan.EndTimestamp()-viewSpan.StartTimestamp())
	assert.Equal(t, pcommonTimestamp(int64(10*time.Millisecond)), viewSpan.Events().At(0).Timestamp()-viewSpan.StartTimestamp())
	assert.Equal(t, 3, viewSpan.Events().Len())

	resourceSpan := traces.ResourceSpans().At(1).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, pcommonTimestamp(int64(30*time.Millisecond)), resourceSpan.EndTimestamp()-resourceSpan.StartTimestamp())
	assert.Equal(t, "GET", resourceSpan.Name())
	assert.Equal(t, int64(503), resourceSpan.Attributes().AsRaw()["http.response.status_code"])
	assert.Equal(t, ptrace.StatusCodeError, resourceSpan.Status().Code())
	assert.Equal(t, 1, resourceSpan.Events().Len())
	assert.Equal(t, "resource.dns", resourceSpan.Events().At(0).Name())
	assert.Equal(t, pcommonTimestamp(int64(5*time.Millisecond)), resourceSpan.Events().At(0).Timestamp()-resourceSpan.StartTimestamp())
	timingAttrs := resourceSpan.Events().At(0).Attributes().AsRaw()
	assert.Equal(t, int64(5*time.Millisecond), timingAttrs["timing.start_ns"])
	assert.Equal(t, int64(30*time.Millisecond), timingAttrs["timing.duration_ns"])
	assert.Equal(t, int64(35*time.Millisecond), timingAttrs["timing.end_ns"])

	errorSpan := traces.ResourceSpans().At(2).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, ptrace.StatusCodeError, errorSpan.Status().Code())
	assert.Equal(t, "failed", errorSpan.Status().Message())
	assert.Equal(t, pcommonTimestamp(int64(time.Millisecond)), errorSpan.EndTimestamp()-errorSpan.StartTimestamp())

	longTaskSpan := traces.ResourceSpans().At(3).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, pcommonTimestamp(int64(40*time.Millisecond)), longTaskSpan.EndTimestamp()-longTaskSpan.StartTimestamp())
}

func TestConvertDurationsUseServerNanoseconds(t *testing.T) {
	duration := int64(30 * time.Millisecond)
	cases := []struct {
		name  string
		event model.Event
		span  string
	}{
		{
			name: "action loading time",
			event: &model.ActionEvent{
				CommonFields: common(1000, model.EventTypeAction, "action"),
				Action:       model.Action{LoadingTime: &duration},
			},
			span: "ui.action",
		},
		{
			name: "resource duration",
			event: &model.ResourceEvent{
				CommonFields: common(1000, model.EventTypeResource, "resource"),
				Resource:     model.Resource{Duration: &duration},
			},
			span: "resource.load",
		},
		{
			name: "long task duration",
			event: &model.LongTaskEvent{
				CommonFields: common(1000, model.EventTypeLongTask, "long-task"),
				LongTask:     model.LongTask{Duration: &duration},
			},
			span: "browser.long_task",
		},
		{
			name: "vital duration",
			event: &model.VitalEvent{
				CommonFields: common(1000, model.EventTypeVital, "vital"),
				Vital:        model.Vital{Name: "fcp", Duration: &duration},
			},
			span: "vital.fcp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			span := Convert(&model.Batch{Events: []model.Event{tc.event}}, "").
				ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
			assert.Equal(t, tc.span, span.Name())
			assert.Equal(t, pcommonTimestamp(duration), span.EndTimestamp()-span.StartTimestamp())
		})
	}
}

func TestConvertDurationFallbackUsesOneMillisecond(t *testing.T) {
	zero := int64(0)
	negative := int64(-1)
	cases := []model.Event{
		&model.ViewEvent{CommonFields: common(1000, model.EventTypeView, "view")},
		&model.ActionEvent{CommonFields: common(1000, model.EventTypeAction, "action"), Action: model.Action{LoadingTime: &zero}},
		&model.ResourceEvent{CommonFields: common(1000, model.EventTypeResource, "resource"), Resource: model.Resource{Duration: &negative}},
		&model.LongTaskEvent{CommonFields: common(1000, model.EventTypeLongTask, "long-task")},
		&model.VitalEvent{CommonFields: common(1000, model.EventTypeVital, "vital")},
	}

	for _, event := range cases {
		span := Convert(&model.Batch{Events: []model.Event{event}}, "").
			ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
		assert.Equal(t, pcommonTimestamp(int64(time.Millisecond)), span.EndTimestamp()-span.StartTimestamp())
	}
}
func TestConvertErrorMapping(t *testing.T) {
	line, column := int64(42), int64(7)
	crossOrigin := true
	cases := []struct {
		name        string
		err         model.Error
		wantSource  string
		wantType    string // "" 表示该字段不应输出
		wantHandled bool
	}{
		{
			name:        "js error from window.onerror",
			err:         model.Error{Message: "boom", Source: "source", Type: "TypeError", Handling: "unhandled"},
			wantSource:  "window.error",
			wantType:    "JavaScriptError",
			wantHandled: false,
		},
		{
			name:        "unhandled promise rejection",
			err:         model.Error{Message: "rejected", Source: "source", Type: "Unhandledrejection", Handling: "unhandled"},
			wantSource:  "unhandledrejection",
			wantType:    "PromiseRejection",
			wantHandled: false,
		},
		{
			name:        "handled error",
			err:         model.Error{Message: "caught", Source: "source", Type: "RangeError", Handling: "handled"},
			wantSource:  "window.error",
			wantType:    "JavaScriptError",
			wantHandled: true,
		},
		{
			name:        "network resource error",
			err:         model.Error{Message: "not found", Source: "network", Handling: "unhandled"},
			wantSource:  "resource",
			wantType:    "ResourceError",
			wantHandled: false,
		},
		{
			name:        "cross-origin maps to window.error",
			err:         model.Error{Message: "Script error.", Source: "cross-origin", Handling: "unhandled"},
			wantSource:  "window.error",
			wantType:    "JavaScriptError",
			wantHandled: false,
		},
		{
			name:        "console passthrough no type",
			err:         model.Error{Message: "logged", Source: "console", Handling: "handled"},
			wantSource:  "console",
			wantType:    "",
			wantHandled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			traces := Convert(&model.Batch{Events: []model.Event{&model.ErrorEvent{
				CommonFields: common(1000, model.EventTypeError, "error"),
				Error:        tc.err,
			}}}, "")
			span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
			raw := span.Attributes().AsRaw()
			assert.Equal(t, tc.wantSource, raw["error.source"], "error.source")
			if tc.wantType == "" {
				assert.NotContains(t, raw, "error.type", "error.type should be absent")
			} else {
				assert.Equal(t, tc.wantType, raw["error.type"], "error.type")
			}
			assert.Equal(t, tc.wantHandled, raw["error.handled"], "error.handled")
			assert.Equal(t, tc.err.Message, raw["error.message"], "error.message")
			for _, key := range []string{
				"exception.type", "exception.message", "exception.stacktrace", "exception.source",
				"exception.handling", "exception.component_stack", "exception.fingerprint",
				"exception.causes", "exception.csp", "exception.debug_ids", "exception.resource.url",
			} {
				assert.NotContains(t, raw, key, "legacy %s should be dropped", key)
			}
			assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
			assert.Equal(t, tc.err.Message, span.Status().Message())
		})
	}

	t.Run("detail fields emitted", func(t *testing.T) {
		traces := Convert(&model.Batch{Events: []model.Event{&model.ErrorEvent{
			CommonFields: common(1000, model.EventTypeError, "error"),
			Error: model.Error{
				Message:      "boom",
				Source:       "network",
				Handling:     "unhandled",
				Stack:        "Error: boom\n  at foo (app.js:42:7)",
				File:         "app.js",
				Line:         &line,
				Column:       &column,
				CrossOrigin:  &crossOrigin,
				ResourceType: "SCRIPT",
				Resource:     &model.ErrorResource{URL: "https://example.com/app.js"},
			},
		}}}, "")
		raw := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
		assert.Equal(t, "resource", raw["error.source"])
		assert.Equal(t, "ResourceError", raw["error.type"])
		assert.Equal(t, "app.js", raw["code.filepath"])
		assert.Equal(t, int64(42), raw["code.lineno"])
		assert.Equal(t, int64(7), raw["code.column"])
		assert.Equal(t, true, raw["error.cross_origin"])
		assert.Equal(t, "https://example.com/app.js", raw["error.resource_url"])
		assert.Equal(t, "SCRIPT", raw["error.resource_tag"])
		assert.Equal(t, "Error: boom\n  at foo (app.js:42:7)", raw["error.stack"])
	})

	t.Run("resource fields only when source is resource", func(t *testing.T) {
		traces := Convert(&model.Batch{Events: []model.Event{&model.ErrorEvent{
			CommonFields: common(1000, model.EventTypeError, "error"),
			Error: model.Error{
				Message:      "boom",
				Source:       "source",
				Handling:     "unhandled",
				ResourceType: "SCRIPT",
				Resource:     &model.ErrorResource{URL: "https://example.com/app.js"},
			},
		}}}, "")
		raw := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
		assert.NotContains(t, raw, "error.resource_url")
		assert.NotContains(t, raw, "error.resource_tag")
	})
}

func TestConvertResourceHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status      int
		wantCode    ptrace.StatusCode
		wantMessage string
	}{
		{100, ptrace.StatusCodeUnset, ""},                    // 1xx Informational
		{200, ptrace.StatusCodeOk, ""},                       // 2xx Success
		{302, ptrace.StatusCodeUnset, ""},                    // 3xx Redirection
		{404, ptrace.StatusCodeError, "Not Found"},           // 4xx Client Error
		{503, ptrace.StatusCodeError, "Service Unavailable"}, // 5xx Server Error
		{419, ptrace.StatusCodeError, ""},                    // Non-standard 4xx status
	}

	for _, tc := range cases {
		status := tc.status
		resource := &model.ResourceEvent{
			CommonFields: common(1000, model.EventTypeResource, "resource"),
			Resource:     model.Resource{StatusCode: &status},
		}
		span := Convert(&model.Batch{Events: []model.Event{resource}}, "").
			ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
		assert.Equal(t, tc.wantCode, span.Status().Code(), "status %d code", status)
		assert.Equal(t, tc.wantMessage, span.Status().Message(), "status %d message", status)
		assert.Equal(t, int64(status), span.Attributes().AsRaw()["http.response.status_code"], "status %d attr", status)
		assert.NotContains(t, span.Attributes().AsRaw(), "http.status_text", "http.status_text dropped")
	}

	// 无状态码时不设置 HTTP 状态相关字段
	noStatus := &model.ResourceEvent{CommonFields: common(1000, model.EventTypeResource, "resource")}
	noStatusSpan := Convert(&model.Batch{Events: []model.Event{noStatus}}, "").
		ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, ptrace.StatusCodeUnset, noStatusSpan.Status().Code())
	assert.Equal(t, "", noStatusSpan.Status().Message())
	assert.NotContains(t, noStatusSpan.Attributes().AsRaw(), "http.response.status_code")

	// 状态码 <= 0 不输出 http.response.status_code
	zeroStatus := 0
	zeroSpan := Convert(&model.Batch{Events: []model.Event{&model.ResourceEvent{
		CommonFields: common(1000, model.EventTypeResource, "resource"),
		Resource:     model.Resource{StatusCode: &zeroStatus},
	}}}, "").ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.NotContains(t, zeroSpan.Attributes().AsRaw(), "http.response.status_code")
}

func TestConvertResourceProtocolFields(t *testing.T) {
	decoded, encoded, transferZero := int64(2048), int64(512), int64(0)
	status := 200
	resource := &model.ResourceEvent{
		CommonFields: common(1000, model.EventTypeResource, "resource"),
		Resource: model.Resource{
			Type:                 "xhr",
			Method:               "post",
			URL:                  "https://api.example.com:8443/orders/12345/items/550e8400-e29b-41d4-a716-446655440000?token=secret",
			StatusCode:           &status,
			Protocol:             "h2",
			DeliveryType:         "",
			DecodedBodySize:      &decoded,
			EncodedBodySize:      &encoded,
			TransferSize:         &transferZero,
			RenderBlockingStatus: "non-blocking",
		},
	}
	raw := Convert(&model.Batch{Events: []model.Event{resource}}, "").
		ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()

	// url.full: query 被脱敏
	assert.Equal(t, "https://api.example.com:8443/orders/12345/items/550e8400-e29b-41d4-a716-446655440000", raw["url.full"])
	// url.template: 数字 ID 与 UUID 折叠为 :id，只保留 path
	assert.Equal(t, "/orders/:id/items/:id", raw["url.template"])
	// server.address: hostname 不含端口
	assert.Equal(t, "api.example.com", raw["server.address"])
	// server.port: 仅显式端口
	assert.Equal(t, int64(8443), raw["server.port"])
	// http.request.method: 归一大写，仅 Fetch/XHR
	assert.Equal(t, "POST", raw["http.request.method"])
	assert.Equal(t, int64(200), raw["http.response.status_code"])
	assert.Equal(t, "h2", raw["resource.protocol"])
	// delivery_type 空 -> other
	assert.Equal(t, "other", raw["resource.delivery_type"])
	assert.Equal(t, "non-blocking", raw["resource.render_blocking_status"])
	// resource.size 协议定义为 decodedBodySize
	assert.Equal(t, int64(2048), raw["resource.size"])
	assert.Equal(t, int64(2048), raw["resource.decoded_body_size"])
	assert.Equal(t, int64(512), raw["resource.encoded_body_size"])
	assert.Equal(t, int64(0), raw["resource.transfer_size"])
	// cache.hit: transferSize=0 且 decodedBodySize>0
	assert.Equal(t, true, raw["resource.cache.hit"])
	// 已删除的旧字段不再输出
	for _, key := range []string{
		"resource.id", "http.method", "http.url", "http.host", "http.target",
		"http.query", "http.scheme", "http.status_code", "http.protocol",
		"http.status_text", "resource.duration", "resource.provider.type",
		"resource.provider.name", "resource.provider.domain", "resource.request",
		"resource.response", "dd.trace_id", "dd.span_id",
	} {
		assert.NotContains(t, raw, key, "legacy %s should be dropped", key)
	}

	// 静态资源：无 http.request.method / url.template；默认端口无 server.port
	staticTransfer, staticDecoded := int64(600), int64(100)
	static := &model.ResourceEvent{
		CommonFields: common(1000, model.EventTypeResource, "resource"),
		Resource: model.Resource{
			Type:            "img",
			URL:             "https://cdn.example.com/logo.png",
			TransferSize:    &staticTransfer,
			DecodedBodySize: &staticDecoded,
		},
	}
	staticRaw := Convert(&model.Batch{Events: []model.Event{static}}, "").
		ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
	assert.Equal(t, "img", staticRaw["resource.type"])
	assert.NotContains(t, staticRaw, "http.request.method")
	assert.NotContains(t, staticRaw, "url.template")
	assert.Equal(t, "cdn.example.com", staticRaw["server.address"])
	assert.NotContains(t, staticRaw, "server.port")        // 默认端口
	assert.NotContains(t, staticRaw, "resource.cache.hit") // transferSize>0 且 deliveryType!=cache

	// deliveryType=cache -> cache.hit=true
	cache := &model.ResourceEvent{
		CommonFields: common(1000, model.EventTypeResource, "resource"),
		Resource:     model.Resource{Type: "script", URL: "https://cdn.example.com/a.js", DeliveryType: "cache"},
	}
	cacheRaw := Convert(&model.Batch{Events: []model.Event{cache}}, "").
		ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
	assert.Equal(t, true, cacheRaw["resource.cache.hit"])
}

func common(date int64, eventType model.EventType, id string) model.CommonFields {
	return model.CommonFields{
		Date:        date,
		Type:        eventType,
		Application: model.Application{ID: "app-1"},
		Session:     &model.Session{ID: "session-1", Type: "user"},
		ViewContext: &model.ViewContext{ID: "view-1"},
		Raw:         json.RawMessage(`{"type":"` + eventType.S() + `","id":"` + id + `"}`),
	}
}

func float64Ptr(value float64) *float64 { return &value }

func pcommonTimestamp(value int64) pcommon.Timestamp { return pcommon.Timestamp(value) }
