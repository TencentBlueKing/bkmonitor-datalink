// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package converter translates parsed Datadog RUM events into OTLP traces.
package converter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

const (
	scopeName         = "bk-rum"
	defaultSDKVersion = "1.0.0"

	attrViewPhase = "attributes.view.phase"
	phaseStart    = "start"
	phaseUpdate   = "update"
	phaseEnd      = "end"
)

type AttributeBuilder struct {
	attrs pcommon.Map
}

func NewAttributeBuilder(attrs pcommon.Map) *AttributeBuilder {
	return &AttributeBuilder{attrs: attrs}
}

// Set adds a supported scalar or string slice attribute. Nil pointers and empty
// strings are ignored; zero numeric values and false are intentionally kept.
func (b *AttributeBuilder) Set(key string, value interface{}) *AttributeBuilder {
	if b == nil || value == nil {
		return b
	}

	v := reflect.ValueOf(value)
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr) {
		if v.IsNil() {
			return b
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return b
	}

	switch v.Kind() {
	case reflect.String:
		if v.String() != "" {
			b.attrs.PutString(key, v.String())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.attrs.PutInt(key, v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		unsigned := v.Uint()
		if unsigned <= uint64(^uint64(0)>>1) {
			b.attrs.PutInt(key, int64(unsigned))
		} else {
			// OTLP integer attributes are signed; preserve oversized unsigned values
			// as decimal strings instead of silently dropping them.
			b.attrs.PutString(key, strconv.FormatUint(unsigned, 10))
		}
	case reflect.Float32, reflect.Float64:
		b.attrs.PutDouble(key, v.Float())
	case reflect.Bool:
		b.attrs.PutBool(key, v.Bool())

	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String || v.Len() == 0 {
			return b
		}
		slice := b.attrs.PutEmptySlice(key)
		for i := 0; i < v.Len(); i++ {
			slice.AppendEmpty().SetStr(v.Index(i).String())
		}
	}
	return b
}

// SetAll adds all supported fields and returns the builder for chaining.
func (b *AttributeBuilder) SetAll(fields map[string]interface{}) *AttributeBuilder {
	for key, value := range fields {
		b.Set(key, value)
	}
	return b
}

// Convert converts every event in batch into one OTLP span. Each event gets a
// separate ResourceSpans so events from different RUM applications or sessions
// cannot accidentally share resource attributes.
//
// userAgent is the uploader's User-Agent (e.g. the RUM SDK / browser UA),
// attached to resource spans as the http.user_agent attribute.
func Convert(batch *model.Batch, userAgent string) ptrace.Traces {
	traces := ptrace.NewTraces()
	if batch == nil {
		return traces
	}

	for _, event := range batch.Events {
		if isNilEvent(event) || event.GetCommon() == nil {
			continue
		}
		appendEvent(traces, event, userAgent)
	}
	return traces
}

func isNilEvent(event model.Event) bool {
	if event == nil {
		return true
	}
	value := reflect.ValueOf(event)
	return (value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface) && value.IsNil()
}

func appendEvent(traces ptrace.Traces, event model.Event, userAgent string) {
	common := event.GetCommon()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	resourceAttrs := NewAttributeBuilder(resourceSpans.Resource().Attributes())
	setResourceAttributes(resourceAttrs, common, userAgent)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	scopeSpans.Scope().SetName(scopeName)
	version := defaultSDKVersion
	if common.Internal != nil && common.Internal.BrowserSDKVersion != "" {
		version = common.Internal.BrowserSDKVersion
	}
	scopeSpans.Scope().SetVersion(version)

	span := scopeSpans.Spans().AppendEmpty()
	span.SetTraceID(traceIDFor(event))
	span.SetSpanID(spanIDFor(event))
	span.SetStartTimestamp(eventStart(common.Date))
	span.SetKind(ptrace.SpanKindInternal)
	attrs := NewAttributeBuilder(span.Attributes())
	attrs.Set("event.type", event.GetType().S()).Set("rum.source", common.Source)

	switch event := event.(type) {
	case *model.ViewEvent:
		convertView(span, event, attrs)
	case *model.ActionEvent:
		convertAction(span, event, attrs)
	case *model.ResourceEvent:
		convertResource(span, event, attrs)
	case *model.ErrorEvent:
		convertError(span, event, attrs)
	case *model.LongTaskEvent:
		convertLongTask(span, event, attrs)
	case *model.VitalEvent:
		convertVital(span, event, attrs)

	default:
		span.SetName(event.GetType().S())
		setEnd(span, 0)
	}
}

func eventStart(date int64) pcommon.Timestamp {
	return pcommon.NewTimestampFromTime(time.UnixMilli(date))
}

// serverDurationTimestamp converts a Datadog ServerDuration to an OTLP timestamp
// offset. ServerDuration is already encoded in nanoseconds by the RUM intake
// protocol; only CommonFields.Date is an epoch millisecond value.
func serverDurationTimestamp(duration int64) pcommon.Timestamp {
	return pcommon.Timestamp(duration)
}

func setEnd(span ptrace.Span, duration int64) {
	if duration <= 0 {
		duration = int64(time.Millisecond)
	}
	span.SetEndTimestamp(span.StartTimestamp() + serverDurationTimestamp(duration))
}

// traceIDFor 生成 OTLP TraceID。
// 优先使用 Datadog 内置的 trace_id；不存在时按以下规则派生：
//  1. 有 session.id 时：sha256("session\x00" + session.id) 取前 16 字节
//  2. 无 session.id 时：sha256("application\x00" + application.id + "\x00" + date + "\x00" + event_type) 取前 16 字节
//
// 生成的 TraceID 为空时会填充最后一个字节，保证非空。
func traceIDFor(event model.Event) pcommon.TraceID {
	common := event.GetCommon()
	if common.Internal != nil {
		if id, ok := validTraceID(common.Internal.TraceID); ok {
			return id
		}
	}

	key := "application\x00" + common.Application.ID + "\x00" + strconv.FormatInt(common.Date, 10) + "\x00" + event.GetType().S()
	if common.Session != nil && common.Session.ID != "" {
		key = "session\x00" + common.Session.ID
	}
	digest := sha256.Sum256([]byte(key))
	var id pcommon.TraceID
	copy(id[:], digest[:16])
	return nonEmptyTraceID(id)
}

// spanIDFor 生成 OTLP SpanID。
// 优先使用 Datadog 内置的 span_id；不存在时按以下规则派生：
//  1. 优先使用事件自身 ID（view/action/resource/error/long_task/vital id）：
//     key = event_type + "\x00" + event_id，sha256 取前 8 字节
//  2. 事件无 ID 时回退到：session.id + "\x00" + date + "\x00" + event_type
//     sha256 取前 8 字节
//
// 生成的 SpanID 为空时会填充最后一个字节，保证非空。
func spanIDFor(event model.Event) pcommon.SpanID {
	common := event.GetCommon()
	if common.Internal != nil {
		if id, ok := validSpanID(common.Internal.SpanID); ok {
			return id
		}
	}

	identifier := eventIdentifier(event)
	key := event.GetType().S() + "\x00" + identifier
	if identifier == "" {
		sessionID := ""
		if common.Session != nil {
			sessionID = common.Session.ID
		}
		key = sessionID + "\x00" + strconv.FormatInt(common.Date, 10) + "\x00" + event.GetType().S()
	}
	digest := sha256.Sum256([]byte(key))
	var id pcommon.SpanID
	copy(id[:], digest[:8])
	return nonEmptySpanID(id)
}

// eventIdentifier 提取各类 RUM 事件自身的业务 ID，用于 spanIDFor 生成 SpanID。
func eventIdentifier(event model.Event) string {
	switch event := event.(type) {
	case *model.ViewEvent:
		return event.View.ID
	case *model.ActionEvent:
		return event.Action.ID
	case *model.ResourceEvent:
		return event.Resource.ID
	case *model.ErrorEvent:
		return event.Error.ID
	case *model.LongTaskEvent:
		return event.LongTask.ID
	case *model.VitalEvent:
		return event.Vital.ID
	default:
		return ""
	}
}

func validTraceID(value string) (pcommon.TraceID, bool) {
	var id pcommon.TraceID
	if len(value) != 32 {
		return pcommon.TraceID{}, false
	}
	if _, err := hex.Decode(id[:], []byte(value)); err != nil || id.IsEmpty() {
		return pcommon.TraceID{}, false
	}
	return id, true
}

func validSpanID(value string) (pcommon.SpanID, bool) {
	var id pcommon.SpanID
	if len(value) != 16 {
		return pcommon.SpanID{}, false
	}
	if _, err := hex.Decode(id[:], []byte(value)); err != nil || id.IsEmpty() {
		return pcommon.SpanID{}, false
	}
	return id, true
}

// nonEmptyTraceID 保证 TraceID 非空；若 hash 结果全 0，则填充最后一个字节为 1。
func nonEmptyTraceID(id pcommon.TraceID) pcommon.TraceID {
	if id.IsEmpty() {
		id[15] = 1
	}
	return id
}

// nonEmptySpanID 保证 SpanID 非空；若 hash 结果全 0，则填充最后一个字节为 1。
func nonEmptySpanID(id pcommon.SpanID) pcommon.SpanID {
	if id.IsEmpty() {
		id[7] = 1
	}
	return id
}

func setResourceAttributes(b *AttributeBuilder, common *model.CommonFields, userAgent string) {
	serviceName := common.Application.Name
	if serviceName == "" {
		serviceName = common.Service
	}
	if serviceName == "" {
		serviceName = "datadog-rum"
	}
	b.Set("service.name", serviceName)

	serviceVersion := common.Version
	if serviceVersion == "" {
		serviceVersion = common.BuildVersion
	}
	b.Set("service.version", serviceVersion)
	b.Set("http.user_agent", userAgent)
	b.Set("deployment.environment.name", environmentFromTags(common.Tags))
	b.Set("application.id", common.Application.ID)
	b.Set("session.id", sessionValue(common, func(session *model.Session) string { return session.ID }))
	b.Set("session.type", sessionValue(common, func(session *model.Session) string { return session.Type }))
	if common.Session != nil {
		b.Set("session.has_replay", common.Session.HasReplay)
	}
	if common.User != nil {
		b.Set("user.id", common.User.ID)
		b.Set("user.anonymous_id", common.User.AnonymousID)
	}

	version := defaultSDKVersion
	if common.Internal != nil && common.Internal.BrowserSDKVersion != "" {
		version = common.Internal.BrowserSDKVersion
	}
	b.Set("telemetry.sdk.name", "@datadog@browser-sdk")
	b.Set("telemetry.sdk.language", "webjs")
	b.Set("telemetry.sdk.version", version)
	if common.Internal != nil {
		b.Set("dd.sdk.name", common.Internal.SDKName)
		b.Set("dd.browser_sdk_version", common.Internal.BrowserSDKVersion)
		b.Set("dd.start_session_replay_recording_manually", common.Internal.StartSessionReplayRecordingManually)
		b.Set("dd.remote_configuration_id", common.Internal.RemoteConfigurationID)
		b.Set("dd.cls.device_pixel_ratio", common.Internal.CLSDevicePixelRatio)
		putSampleRate(b, "trace.sample_rate", common.Internal.TraceSampleRate)
		putSampleRate(b, "profiling.sample_rate", common.Internal.ProfilingSampleRate)
		if common.Internal.Configuration != nil {
			configuration := common.Internal.Configuration
			putSampleRate(b, "session.sample_rate", configuration.SessionSampleRate)
			putSampleRate(b, "session.replay_sample_rate", configuration.SessionReplaySampleRate)
			if common.Internal.TraceSampleRate == nil {
				putSampleRate(b, "trace.sample_rate", configuration.TraceSampleRate)
			}
			if common.Internal.ProfilingSampleRate == nil {
				putSampleRate(b, "profiling.sample_rate", configuration.ProfilingSampleRate)
			}
		}
	}
	b.Set("rum.source", common.Source)
	b.Set("dd.tags", common.Tags)

	if common.OS != nil {
		b.Set("os.name", common.OS.Name)
		b.Set("os.version", common.OS.Version)
		b.Set("os.version_major", common.OS.VersionMajor)
		b.Set("os.build", common.OS.Build)
	}
	if common.Device != nil {
		b.Set("device.type", common.Device.Type)
		b.Set("device.name", common.Device.Name)
		b.Set("device.brand", common.Device.Brand)
		b.Set("device.model", common.Device.Model)
		b.Set("device.architecture", common.Device.Architecture)
		b.Set("device.locale", common.Device.Locale)
		b.Set("device.time_zone", common.Device.TimeZone)
		b.Set("device.battery_level", common.Device.BatteryLevel)
		b.Set("device.power_saving_mode", common.Device.PowerSavingMode)
		b.Set("device.brightness_level", common.Device.BrightnessLevel)
		b.Set("device.logical_cpu_count", common.Device.LogicalCPUCount)
		b.Set("device.total_ram", common.Device.TotalRAM)
		b.Set("device.is_low_ram", common.Device.IsLowRAM)
	}
	if common.Connectivity != nil {
		b.Set("connectivity.status", common.Connectivity.Status)
		b.Set("connectivity.effective_type", common.Connectivity.EffectiveType)
		b.Set("connectivity.interfaces", common.Connectivity.Interfaces)
		if common.Connectivity.Cellular != nil {
			b.Set("connectivity.cellular.technology", common.Connectivity.Cellular.Technology)
			b.Set("connectivity.cellular.carrier_name", common.Connectivity.Cellular.CarrierName)
		}
	}
	if common.Display != nil && common.Display.Viewport != nil {
		b.Set("viewport.width", common.Display.Viewport.Width)
		b.Set("viewport.height", common.Display.Viewport.Height)
	}
}

func sessionValue(common *model.CommonFields, getter func(*model.Session) string) string {
	if common.Session == nil {
		return ""
	}
	return getter(common.Session)
}

func environmentFromTags(tags string) string {
	for _, tag := range strings.Split(tags, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(tag), ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "env") && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "production"
}

func putSampleRate(b *AttributeBuilder, key string, value *float64) {
	if value == nil {
		return
	}
	// Datadog sample rates are percentages (0-100); normalize to [0, 1].
	rate := *value / 100
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	b.Set(key, rate)
}

func putJSON(b *AttributeBuilder, key string, value any) {
	if value == nil {
		return
	}
	data, err := json.Marshal(value)
	if err == nil {
		b.Set(key, string(data))
	}
}

func convertView(span ptrace.Span, event *model.ViewEvent, b *AttributeBuilder) {
	view := event.View
	span.SetName("page.view")
	span.SetKind(ptrace.SpanKindInternal)
	b.Set("view.id", view.ID)
	b.Set("view.name", view.Name)
	b.Set("view.url", view.URL)
	b.Set("view.referrer", view.Referrer)
	b.Set("view.previous", view.Referrer)
	b.Set("view.loading_type", view.LoadingType)
	b.Set("view.is_active", view.IsActive)
	docVersion := -1
	if event.Internal != nil {
		docVersion = utils.Val(event.Internal.DocumentVersion, -1)
	}
	b.Set("view.version", docVersion)

	// 优先级：end > start > update
	switch {
	case view.IsActive != nil && !*view.IsActive:
		b.Set(attrViewPhase, phaseEnd)
	case docVersion == 1:
		b.Set(attrViewPhase, phaseStart)
	case view.IsActive != nil && *view.IsActive && docVersion > 1:
		b.Set(attrViewPhase, phaseUpdate)
	}
	b.Set("view.loading_time", view.LoadingTime)
	// 代表当前文档卸载上一个页面的时间点，作为整个页面生命周期的零点
	if view.Performance != nil {
		b.Set("view.started_at", view.Performance.NavigationStart)
	}
	b.Set("view.url_path_group", view.URL)
	b.Set("view.network_settled_time", view.NetworkSettledTime)
	b.Set("view.interaction_to_next_view_time", view.InteractionToNextViewTime)
	b.Set("view.time_spent", view.TimeSpent)
	putViewCounts(b, view)
	putViewVitals(b, view)
	putViewPerformanceAttrs(b, view.Performance)
	for name, value := range view.CustomTimings {
		b.Set("view.custom_timing."+name, value)
	}
	putJSON(b, "view.page_state", view.PageState)
	putJSON(b, "view.in_foreground_periods", view.InForegroundPeriods)
	if event.GetType() == model.EventTypeViewUpdate {
		b.Set("event.type", model.EventTypeViewUpdate.S())
	}

	duration := int64(time.Millisecond)
	if view.TimeSpent != nil && *view.TimeSpent > 0 {
		duration = *view.TimeSpent
	} else if view.LoadingTime != nil && *view.LoadingTime > 0 {
		duration = *view.LoadingTime
	}
	setEnd(span, duration)
	addViewPerformanceEvents(span, view.Performance)
}

func putViewCounts(b *AttributeBuilder, view model.View) {
	// EventCount 相关（带下划线）
	if view.EventCount != nil {
		b.Set("view.action_count", view.EventCount.Action)
		b.Set("view.error_count", view.EventCount.Error)
		b.Set("view.resource_count", view.EventCount.Resource)
		b.Set("view.long_task_count", view.EventCount.LongTask)
	}

	// 独立的计数器（带点）
	if view.Action != nil {
		b.Set("view.action.count", view.Action.Count)
	}
	if view.Error != nil {
		b.Set("view.error.count", view.Error.Count)
	}
	if view.Crash != nil {
		b.Set("view.crash.count", view.Crash.Count)
	}
	if view.Resource != nil {
		b.Set("view.resource.count", view.Resource.Count)
	}
	if view.LongTask != nil {
		b.Set("view.long_task.count", view.LongTask.Count)
	}
	if view.Frustration != nil {
		b.Set("view.frustration.count", view.Frustration.Count)
	}
}

func putViewVitals(b *AttributeBuilder, view model.View) {
	b.Set("view.first_contentful_paint", view.FirstContentfulPaint)
	b.Set("view.largest_contentful_paint", view.LargestContentfulPaint)
	b.Set("view.largest_contentful_paint_target_selector", view.LargestContentfulPaintTargetSelector)
	b.Set("view.first_input_delay", view.FirstInputDelay)
	b.Set("view.first_input_time", view.FirstInputTime)
	b.Set("view.first_input_target_selector", view.FirstInputTargetSelector)
	b.Set("view.interaction_to_next_paint", view.InteractionToNextPaint)
	b.Set("view.interaction_to_next_paint_time", view.InteractionToNextPaintTime)
	b.Set("view.interaction_to_next_paint_target_selector", view.InteractionToNextPaintTargetSelector)
	b.Set("view.cumulative_layout_shift", view.CumulativeLayoutShift)
	b.Set("view.cumulative_layout_shift_time", view.CumulativeLayoutShiftTime)
	b.Set("view.cumulative_layout_shift_target_selector", view.CumulativeLayoutShiftTargetSelector)
}

func putViewPerformanceAttrs(b *AttributeBuilder, performance *model.ViewPerformance) {
	if performance == nil {
		return
	}
	b.Set("view.performance.navigation_start", performance.NavigationStart)
	b.Set("view.performance.first_byte", performance.FirstByte)
	b.Set("view.performance.dom_interactive", performance.DOMInteractive)
	b.Set("view.performance.dom_content_loaded", performance.DOMContentLoaded)
	b.Set("view.performance.dom_complete", performance.DOMComplete)
	b.Set("view.performance.load_event", performance.LoadEvent)
}

func addViewPerformanceEvents(span ptrace.Span, performance *model.ViewPerformance) {
	if performance == nil {
		return
	}
	var last pcommon.Timestamp
	addValueEvent(span, "firstContentfulPaint", fcpValue(performance), &last)
	if performance.LCP != nil {
		attrs := pcommon.NewMap()
		b := NewAttributeBuilder(attrs)
		b.SetAll(map[string]interface{}{
			"target":          performance.LCP.Target,
			"target_selector": performance.LCP.TargetSelector,
			"resource_url":    performance.LCP.ResourceURL,
			"element":         performance.LCP.Element,
		})
		putJSON(b, "sub_parts", performance.LCP.SubParts)

		addValueEventWithAttrs(span, "largestContentfulPaint", performance.LCP.Timestamp, attrs, &last)
	}
	if performance.FID != nil {
		attrs := pcommon.NewMap()
		b := NewAttributeBuilder(attrs)
		b.Set("duration", performance.FID.Duration)
		b.Set("target_selector", performance.FID.TargetSelector)
		addValueEventWithAttrs(span, "firstInputDelay", performance.FID.Timestamp, attrs, &last)
	}
	if performance.INP != nil {
		attrs := pcommon.NewMap()
		b := NewAttributeBuilder(attrs)
		b.SetAll(map[string]interface{}{
			"duration":        performance.INP.Duration,
			"value":           performance.INP.Value,
			"target_selector": performance.INP.TargetSelector,
			"target":          performance.INP.Target,
		})
		putJSON(b, "sub_parts", performance.INP.SubParts)

		addValueEventWithAttrs(span, "interactionToNextPaint", performance.INP.Timestamp, attrs, &last)
	}
	if performance.CLS != nil {
		attrs := pcommon.NewMap()
		b := NewAttributeBuilder(attrs)
		b.Set("score", performance.CLS.Score)
		b.Set("value", performance.CLS.Value)
		b.Set("target_selector", performance.CLS.TargetSelector)
		putJSON(b, "previous_rect", performance.CLS.PreviousRect)
		putJSON(b, "current_rect", performance.CLS.CurrentRect)
		addValueEventWithAttrs(span, "cumulativeLayoutShift", performance.CLS.Timestamp, attrs, &last)
	}
	addValueEvent(span, "responseStart", performance.FirstByte, &last)
	addValueEvent(span, "domInteractive", performance.DOMInteractive, &last)
	addValueEvent(span, "domContentLoadedEventEnd", performance.DOMContentLoaded, &last)
	addValueEvent(span, "domComplete", performance.DOMComplete, &last)
	addValueEvent(span, "loadEventEnd", performance.LoadEvent, &last)
}

func fcpValue(performance *model.ViewPerformance) *int64 {
	if performance == nil || performance.FCP == nil {
		return nil
	}
	return performance.FCP.Timestamp
}

func addValueEvent(span ptrace.Span, name string, value *int64, last *pcommon.Timestamp) {
	addValueEventWithAttrs(span, name, value, pcommon.NewMap(), last)
}

func addValueEventWithAttrs(span ptrace.Span, name string, value *int64, attrs pcommon.Map, last *pcommon.Timestamp) {
	if value == nil {
		return
	}
	offset := *value
	if offset < 0 {
		offset = 0
	}
	timestamp := span.StartTimestamp() + serverDurationTimestamp(offset)
	if last != nil && timestamp < *last {
		attrs.PutInt("original_offset_ns", *value)
		timestamp = *last
	}
	event := span.Events().AppendEmpty()
	event.SetName(name)
	event.SetTimestamp(timestamp)
	attrs.CopyTo(event.Attributes())
	if last != nil {
		*last = timestamp
	}
}

func convertAction(span ptrace.Span, event *model.ActionEvent, b *AttributeBuilder) {
	action := event.Action
	span.SetName("ui.action")
	span.SetKind(ptrace.SpanKindInternal)
	b.Set("action.id", action.ID)
	b.Set("action.type", action.Type)
	b.Set("action.name", action.Name)
	b.Set("action.loading_time", action.LoadingTime)
	b.Set("action.long_task_count", action.LongTaskCount)
	b.Set("action.resource_count", action.ResourceCount)
	b.Set("action.error_count", action.ErrorCount)
	b.Set("action.in_foreground", action.ViewActive)
	if action.Target != nil {
		b.Set("action.target.name", action.Target.Name)
		b.Set("action.target.selector", action.Target.Selector)
		b.Set("action.target.width", action.Target.Width)
		b.Set("action.target.height", action.Target.Height)
	}
	if action.Frustration != nil {
		b.Set("action.frustration.types", action.Frustration.Type)
	}
	if action.EventCount != nil {
		b.Set("action.event.error_count", action.EventCount.Error)
		b.Set("action.event.resource_count", action.EventCount.Resource)
		b.Set("action.event.long_task_count", action.EventCount.LongTask)
	}
	if action.CrashCount != nil {
		b.Set("action.crash_count", action.CrashCount.Count)
	}
	if action.Position != nil {
		b.Set("action.position.x", action.Position.X)
		b.Set("action.position.y", action.Position.Y)
	}
	if action.Internal != nil {
		b.Set("action.dd.target_selector_path", action.Internal.TargetSelectorPath)
		b.Set("action.dd.selector", action.Internal.Selector)
		b.Set("action.dd.composed_path_selector", action.Internal.ComposedPathSelector)
		b.Set("action.dd.permanent_id", action.Internal.PermanentID)
		b.Set("action.dd.name_source", action.Internal.NameSource)
		b.Set("action.dd.width", action.Internal.Width)
		b.Set("action.dd.height", action.Internal.Height)
		b.Set("action.dd.position_x", action.Internal.PositionX)
		b.Set("action.dd.position_y", action.Internal.PositionY)
	}
	if event.GetCommon().ViewContext != nil {
		b.Set("view.id", event.GetCommon().ViewContext.ID)
		b.Set("view.url", event.GetCommon().ViewContext.URL)
	}
	setEnd(span, valueOrDefault(action.LoadingTime, int64(time.Millisecond)))
}

func convertResource(span ptrace.Span, event *model.ResourceEvent, b *AttributeBuilder) {
	resource := event.Resource
	method := strings.ToUpper(strings.TrimSpace(resource.Method))
	if method != "" {
		span.SetName(method)
	} else {
		span.SetName("resource.load")
	}
	span.SetKind(ptrace.SpanKindClient)

	resourceType := resource.Type
	if resourceType == "" {
		resourceType = "other"
	}
	b.Set("resource.type", resourceType)

	parsed := parseURL(resource.URL)
	b.Set("url.full", redactedURL(parsed, resource.URL))
	b.Set("server.address", serverAddress(parsed, resource.URLHost))
	if port, ok := serverPort(parsed); ok {
		b.Set("server.port", port)
	}

	// http.request.method 与 url.template 仅适用于 Fetch/XHR；静态资源缺失。
	if isRequestType(resource.Type) {
		b.Set("http.request.method", method)
		b.Set("url.template", urlTemplate(parsed))
	}

	if resource.StatusCode != nil && *resource.StatusCode > 0 {
		b.Set("http.response.status_code", *resource.StatusCode)
	}
	b.Set("resource.protocol", resource.Protocol)

	deliveryType := resource.DeliveryType
	if deliveryType == "" {
		deliveryType = "other"
	}
	b.Set("resource.delivery_type", deliveryType)
	b.Set("resource.render_blocking_status", resource.RenderBlockingStatus)

	// resource.size 协议定义为 decodedBodySize。
	b.Set("resource.size", resource.DecodedBodySize)
	b.Set("resource.encoded_body_size", resource.EncodedBodySize)
	b.Set("resource.decoded_body_size", resource.DecodedBodySize)
	b.Set("resource.transfer_size", resource.TransferSize)

	if cacheHit(resource) {
		b.Set("resource.cache.hit", true)
	}

	if resource.StatusCode != nil && *resource.StatusCode >= 100 && *resource.StatusCode <= 599 {
		status := *resource.StatusCode
		switch {
		case status >= 200 && status < 300:
			span.Status().SetCode(ptrace.StatusCodeOk)
		case status >= 400 && status <= 599:
			span.Status().SetCode(ptrace.StatusCodeError)
			span.Status().SetMessage(http.StatusText(status))
		default: // 1xx Informational 与 3xx Redirection 不视为错误
			span.Status().SetCode(ptrace.StatusCodeUnset)
		}
	}

	setEnd(span, valueOrDefault(resource.Duration, int64(time.Millisecond)))
	addResourceTimingEvents(span, resource.Timing)
}

func addResourceTimingEvents(span ptrace.Span, timing *model.ResourceTiming) {
	if timing == nil {
		return
	}
	var last pcommon.Timestamp
	addTimingEvent(span, "resource.redirect", timing.Redirect, &last)
	addTimingEvent(span, "resource.worker", timing.Worker, &last)
	addTimingEvent(span, "resource.dns", timing.DNS, &last)
	addTimingEvent(span, "resource.connect", timing.Connect, &last)
	addTimingEvent(span, "resource.ssl", timing.SSL, &last)
	addTimingEvent(span, "resource.first_byte", timing.FirstByte, &last)
	addTimingEvent(span, "resource.download", timing.Download, &last)
}

func addTimingEvent(span ptrace.Span, name string, timing *model.Timing, last *pcommon.Timestamp) {
	if timing == nil || timing.Start == nil {
		return
	}
	start := *timing.Start
	if start < 0 {
		start = 0
	}
	timestamp := span.StartTimestamp() + serverDurationTimestamp(start)
	if timestamp < *last {
		timestamp = *last
	}
	event := span.Events().AppendEmpty()
	event.SetName(name)
	event.SetTimestamp(timestamp)
	b := NewAttributeBuilder(event.Attributes())
	b.Set("timing.start_ns", start)
	b.Set("timing.duration_ns", timing.Duration)
	if timing.Duration != nil && *timing.Duration >= 0 {
		b.Set("timing.end_ns", start+*timing.Duration)
	}
	*last = timestamp
}

var (
	numericRe = regexp.MustCompile(`^\d+$`)
	uuidRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	longHexRe = regexp.MustCompile(`^[0-9a-fA-F]{8,}$`)
)

func parseURL(raw string) *url.URL {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return parsed
}

// redactedURL 返回去除 query 后的绝对 URL，避免在 url.full 上保留原始查询参数。
func redactedURL(parsed *url.URL, fallback string) string {
	if parsed == nil {
		return fallback
	}
	redacted := *parsed
	redacted.RawQuery = ""
	return redacted.String()
}

// serverAddress 返回不含端口的 hostname；URL 无法解析时回退到 SDK 提供的 host。
func serverAddress(parsed *url.URL, fallback string) string {
	if parsed != nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return fallback
}

// serverPort 返回 URL 中显式书写的端口；URL 未写端口（含协议默认端口）时返回 false。
func serverPort(parsed *url.URL) (int, bool) {
	if parsed == nil {
		return 0, false
	}
	portStr := parsed.Port()
	if portStr == "" {
		return 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, false
	}
	return port, true
}

// urlTemplate 由 URL path 派生低基数路由模板：数字 ID、UUID、长十六进制段折叠为 :id。
// 只保留 path，不含 host。URL 无 path 时返回空串。
func urlTemplate(parsed *url.URL) string {
	if parsed == nil || parsed.Path == "" {
		return ""
	}
	segments := strings.Split(parsed.Path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		if numericRe.MatchString(seg) || uuidRe.MatchString(seg) || longHexRe.MatchString(seg) {
			segments[i] = ":id"
		}
	}
	return strings.Join(segments, "/")
}

// isRequestType 判断 resource.type 是否为 Fetch/XHR。
func isRequestType(resourceType string) bool {
	switch strings.ToLower(resourceType) {
	case "fetch", "xhr":
		return true
	}
	return false
}

// cacheHit 推断资源是否命中缓存：
//   - deliveryType 为 cache；
//   - 或 transferSize=0 且 decodedBodySize>0。
//
// 无法确认命中时不写该字段（而非写 false）。
func cacheHit(resource model.Resource) bool {
	if strings.EqualFold(resource.DeliveryType, "cache") {
		return true
	}
	return resource.TransferSize != nil && *resource.TransferSize == 0 &&
		resource.DecodedBodySize != nil && *resource.DecodedBodySize > 0
}

func convertError(span ptrace.Span, event *model.ErrorEvent, b *AttributeBuilder) {
	rumError := event.Error
	span.SetName("exception")
	span.SetKind(ptrace.SpanKindInternal)

	source := convertErrorSource(rumError.Source, rumError.Type)
	b.Set("error.source", source)
	b.Set("error.type", errorTypeFromSource(source))
	b.Set("error.message", rumError.Message)
	b.Set("error.handled", strings.EqualFold(rumError.Handling, "handled"))
	b.Set("error.stack", rumError.Stack)
	b.Set("error.cross_origin", rumError.CrossOrigin)
	b.Set("code.filepath", rumError.File)
	b.Set("code.lineno", rumError.Line)
	b.Set("code.column", rumError.Column)
	if source == "resource" {
		if rumError.Resource != nil {
			b.Set("error.resource_url", rumError.Resource.URL)
		}
		b.Set("error.resource_tag", rumError.ResourceType)
	}

	span.Status().SetCode(ptrace.StatusCodeError)
	span.Status().SetMessage(rumError.Message)
	setEnd(span, int64(time.Millisecond))
}

// convertErrorSource 将 Datadog error.source 映射为 bk-rum 枚举：
//   - network -> resource
//   - source 且 error.type 含 Unhandledrejection -> unhandledrejection
//   - source（其余） -> window.error
//   - cross-origin -> window.error
//   - 其余（console/report 等）原样透传
func convertErrorSource(source, errorType string) string {
	switch source {
	case "network":
		return "resource"
	case "source":
		if strings.Contains(strings.ToLower(errorType), "unhandledrejection") {
			return "unhandledrejection"
		}
		return "window.error"
	case "cross-origin":
		return "window.error"
	default:
		return source
	}
}

// errorTypeFromSource 由 bk-rum source 派生 bk-rum error.type 枚举。
// 无法映射的 source（如原样透传的 console/report）返回空串，不输出该字段。
func errorTypeFromSource(source string) string {
	switch source {
	case "window.error":
		return "JavaScriptError"
	case "unhandledrejection":
		return "PromiseRejection"
	case "resource":
		return "ResourceError"
	default:
		return ""
	}
}

func convertLongTask(span ptrace.Span, event *model.LongTaskEvent, b *AttributeBuilder) {
	longTask := event.LongTask
	span.SetName("browser.long_task")
	span.SetKind(ptrace.SpanKindInternal)
	b.Set("long_task.id", longTask.ID)
	b.Set("long_task.name", longTask.Name)
	b.Set("long_task.entry_type", longTask.EntryType)
	b.Set("long_task.blocking_duration", longTask.BlockingDuration)
	b.Set("long_task.first_ui_event_timestamp", longTask.FirstUIEventTimestamp)
	b.Set("long_task.render_start", longTask.RenderStart)
	b.Set("long_task.style_and_layout_start", longTask.StyleAndLayoutStart)
	setEnd(span, valueOrDefault(longTask.Duration, int64(time.Millisecond)))
}

func convertVital(span ptrace.Span, event *model.VitalEvent, b *AttributeBuilder) {
	vital := event.Vital
	name := string(vital.Name)
	if name == "" {
		name = "vital"
	} else {
		name = "vital." + name
	}
	span.SetName(name)
	span.SetKind(ptrace.SpanKindInternal)
	b.Set("vital.id", vital.ID)
	b.Set("vital.type", string(vital.Type))
	b.Set("vital.name", vital.Name)
	b.Set("vital.description", vital.Description)
	b.Set("vital.step_type", string(vital.StepType))
	b.Set("vital.operation_key", vital.OperationKey)
	b.Set("vital.duration", vital.Duration)
	b.Set("vital.failure_reason", string(vital.FailureReason))
	if vital.Internal != nil {
		b.Set("vital.computed_value", vital.Internal.ComputedValue)
	}
	setEnd(span, valueOrDefault(vital.Duration, int64(time.Millisecond)))
}

func valueOrDefault(value *int64, fallback int64) int64 {
	if value == nil || *value <= 0 {
		return fallback
	}
	return *value
}
