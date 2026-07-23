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
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Converter  OTEL 转换器
type Converter struct {
	strategies map[string]ConversionStrategy
}

// ConversionStrategy 转换策略接口
type ConversionStrategy interface {
	CanHandle(event *DatadogEventV2) bool
	Convert(event *DatadogEventV2, converter *Converter) ConversionOutput
}

// ConversionOutput 转换输出
type ConversionOutput struct {
	Logs    plog.Logs
	Traces  ptrace.Traces
	Metrics pmetric.Metrics
}

const (
	rumScopeName          = "datadog rum"
	rumScopeVersion       = "1.0.0"
	spanNameResourceLoad  = "resource.load"
	spanNameResourceFetch = "resourceFetch"
)

// NewConverter 创建新的转换器
func NewConverter() *Converter {
	converter := &Converter{
		strategies: make(map[string]ConversionStrategy),
	}

	// 注册所有策略
	strategies := map[string]ConversionStrategy{
		"error":       &errorEventStrategy{},
		"performance": &performanceEventStrategy{},
		"view":        &viewEventStrategy{},
		"action":      &actionEventStrategy{},
		"log":         &simpleEventStrategy{eventType: "log"},
		"resource":    &resourceEventStrategy{},
		"long_task":   &longTaskEventStrategy{},
	}

	for eventType, strategy := range strategies {
		converter.strategies[eventType] = strategy
	}

	return converter
}

// ToOTEL 根据事件类型进行转换
func (c *Converter) ToOTEL(event *DatadogEventV2) ConversionOutput {
	strategy := c.strategies[event.Type]
	if strategy != nil {
		return strategy.Convert(event, c)
	}

	// 默认转换为日志
	return c.defaultConvert(event)
}

// defaultConvert 默认转换为日志数据
func (c *Converter) defaultConvert(event *DatadogEventV2) ConversionOutput {
	logs := plog.NewLogs()
	resourceLog := logs.ResourceLogs().AppendEmpty()

	// 配置 Resource
	c.enrichResource(resourceLog.Resource(), event)

	scopeLog := resourceLog.ScopeLogs().AppendEmpty()
	logRecord := scopeLog.LogRecords().AppendEmpty()

	logRecord.SetTimestamp(pcommon.NewTimestampFromTime(time.UnixMilli(event.Date)))
	logRecord.Body().SetStringVal("unknown event")
	logRecord.SetSeverityNumber(plog.SeverityNumberINFO)
	logRecord.Attributes().UpsertString("event.type", event.Type)
	logRecord.Attributes().UpsertString("event.domain", event.EventType)

	return ConversionOutput{
		Logs:    logs,
		Traces:  ptrace.NewTraces(),
		Metrics: pmetric.NewMetrics(),
	}
}

// ======== 转换策略实现 ========

// errorEventStrategy 错误事件转换策略
type errorEventStrategy struct{}

func (s *errorEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "error"
}

func (s *errorEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Logs:    converter.convertToLogs(event, true),
		Traces:  converter.convertExceptionToTraces(event),
		Metrics: pmetric.NewMetrics(),
	}
	return output
}

// performanceEventStrategy 性能事件转换策略
type performanceEventStrategy struct{}

func (s *performanceEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "performance"
}

func (s *performanceEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Logs:    converter.convertToLogs(event, false),
		Traces:  converter.convertPerformanceToTraces(event),
		Metrics: converter.convertToMetrics(event),
	}
	return output
}

// simpleEventStrategy 简单事件转换策略（view、action、log）
type simpleEventStrategy struct {
	eventType string
}

func (s *simpleEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == s.eventType
}

func (s *simpleEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Logs:    converter.convertToLogs(event, false),
		Traces:  converter.convertSimpleEventToTraces(event),
		Metrics: pmetric.NewMetrics(),
	}
	return output
}

// actionEventStrategy action 事件转换策略
type actionEventStrategy struct{}

func (s *actionEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "action"
}

func (s *actionEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Traces:  converter.convertActionEventToTraces(event),
		Logs:    plog.NewLogs(),
		Metrics: pmetric.NewMetrics(),
	}
	return output
}

// resourceEventStrategy 资源事件转换策略
type resourceEventStrategy struct{}

func (s *resourceEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "resource"
}

func (s *resourceEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	logs := plog.NewLogs()
	if converter.shouldGenerateLogForResource(event) {
		logs = converter.convertToLogs(event, false)
	}

	output := ConversionOutput{
		Traces:  converter.convertResourceToTraces(event),
		Logs:    logs,
		Metrics: converter.convertToMetrics(event),
	}
	return output
}

// viewEventStrategy view 视图事件转换策略
type viewEventStrategy struct{}

func (s *viewEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "view"
}

func (s *viewEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Logs:    plog.NewLogs(),
		Traces:  converter.convertViewToTraces(event),
		Metrics: pmetric.NewMetrics(),
	}
	return output
}

// longTaskEventStrategy 长任务事件转换策略
type longTaskEventStrategy struct{}

func (s *longTaskEventStrategy) CanHandle(event *DatadogEventV2) bool {
	return event.Type == "long_task"
}

func (s *longTaskEventStrategy) Convert(event *DatadogEventV2, converter *Converter) ConversionOutput {
	output := ConversionOutput{
		Logs:    converter.convertToLogs(event, false),
		Traces:  converter.convertLongTaskToTraces(event),
		Metrics: converter.convertLongTaskToMetrics(event),
	}
	return output
}

// ======== 日志转换 ========

// convertToLogs 将事件转换为日志数据
func (c *Converter) convertToLogs(event *DatadogEventV2, isError bool) plog.Logs {
	logs := plog.NewLogs()
	resourceLog := logs.ResourceLogs().AppendEmpty()

	// 配置 Resource
	c.enrichResource(resourceLog.Resource(), event)

	scopeLog := resourceLog.ScopeLogs().AppendEmpty()
	logRecord := scopeLog.LogRecords().AppendEmpty()

	// 设置时间戳
	logRecord.SetTimestamp(pcommon.NewTimestampFromTime(time.UnixMilli(event.Date)))

	// 提取消息和级别
	message, severity := c.extractMessageAndLevel(event, isError)
	logRecord.Body().SetStringVal(message)
	logRecord.SetSeverityText(severity)
	logRecord.SetSeverityNumber(c.mapSeverityNumber(severity))

	// 设置 Trace 信息
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	logRecord.SetTraceID(c.stringToTraceID(traceID))
	logRecord.SetSpanID(c.stringToSpanID(spanID))

	// 添加属性
	attrs := logRecord.Attributes()
	attrs.UpsertString("event.type", event.Type)
	attrs.UpsertString("event.domain", event.EventType)

	// 根据事件类型添加特定数据
	c.addEventAttributes(attrs, event)

	return logs
}

// extractMessageAndLevel 从事件提取消息和级别
func (c *Converter) extractMessageAndLevel(event *DatadogEventV2, isError bool) (string, string) {
	message := ""
	severity := "INFO"

	// 优先使用专用字段
	switch event.Type {
	case "error":
		if event.Error != nil {
			if msg, ok := event.Error["message"].(string); ok {
				message = msg
			}
		}
	case "action":
		if event.Action != nil {
			if msg, ok := event.Action["message"].(string); ok {
				message = msg
			}
		}
	case "view":
		// view 事件在 ViewData 中没有 message 字段
		// 如果需要从其他地方提取，可在此扩展
	case "long_task":
		if event.LongTask != nil {
			if msg, ok := event.LongTask["message"].(string); ok {
				message = msg
			}
		}
	}

	// 如果由专用字段未得到消息，尝试从 Data 提取
	if message == "" && event.Data != nil {
		if msg, ok := event.Data["message"].(string); ok {
			message = msg
		}
	}

	// 如果是错误事件，强制设置 ERROR 级别
	if isError {
		severity = "ERROR"
	}

	return message, severity
}

// addEventAttributes 根据事件类型添加属性
func (c *Converter) addEventAttributes(attrs pcommon.Map, event *DatadogEventV2) {
	switch event.Type {
	case "view":
		if event.View != nil {
			if event.View.ID != "" {
				attrs.UpsertString("view.id", event.View.ID)
			}
			if event.View.URL != "" {
				attrs.UpsertString("view.url", event.View.URL)
			}
		}
	case "action":
		if event.Action != nil {
			if id, ok := event.Action["id"].(string); ok {
				attrs.UpsertString("action.id", id)
			}
			if actionType, ok := event.Action["type"].(string); ok {
				attrs.UpsertString("action.type", actionType)
			}
		}
	case "error":
		if event.Error != nil {
			if errorType, ok := event.Error["type"].(string); ok {
				attrs.UpsertString("error.type", errorType)
			}
		}
	}

	// 添加会话和用户信息
	if event.Session != nil {
		if sid, ok := event.Session["id"].(string); ok {
			attrs.UpsertString("session.id", sid)
		}
	}
	if event.User != nil {
		if uid, ok := event.User["id"].(string); ok {
			attrs.UpsertString("user.id", uid)
		}
	}
}

// mapSeverityNumber 将文本级别映射到数字
func (c *Converter) mapSeverityNumber(level string) plog.SeverityNumber {
	switch level {
	case "TRACE":
		return plog.SeverityNumberTRACE
	case "DEBUG":
		return plog.SeverityNumberDEBUG
	case "INFO":
		return plog.SeverityNumberINFO
	case "WARN":
		return plog.SeverityNumberWARN
	case "ERROR":
		return plog.SeverityNumberERROR
	case "FATAL":
		return plog.SeverityNumberFATAL
	default:
		return plog.SeverityNumberUNDEFINED
	}
}

// stringToTraceID 将十六进制字符串转换为 TraceID。
func (c *Converter) stringToTraceID(hexStr string) pcommon.TraceID {
	var traceID [16]byte
	if len(hexStr) >= 32 {
		hexStr = hexStr[:32]
	} else {
		// Pad with zeros
		hexStr = strings.Repeat("0", 32-len(hexStr)) + hexStr
	}
	_, _ = hex.Decode(traceID[:], []byte(hexStr))
	return pcommon.NewTraceID(traceID)
}

// stringToSpanID 将十六进制字符串转换为 SpanID。
func (c *Converter) stringToSpanID(hexStr string) pcommon.SpanID {
	var spanID [8]byte
	if len(hexStr) >= 16 {
		hexStr = hexStr[:16]
	} else {
		// Pad with zeros
		hexStr = strings.Repeat("0", 16-len(hexStr)) + hexStr
	}
	_, _ = hex.Decode(spanID[:], []byte(hexStr))
	return pcommon.NewSpanID(spanID)
}

// convertSimpleEventToTraces 简单事件（view, action, log）转换为 Trace
func (c *Converter) convertActionEventToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	// 确定 Span Name
	span.SetName("action")
	span.SetKind(ptrace.SpanKindInternal)

	// 时间戳
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(ts)
	span.SetEndTimestamp(ts + (pcommon.Timestamp(time.Millisecond)))

	// Trace & Span ID
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))

	// 属性
	attrs := span.Attributes()
	attrs.UpsertString("event.domain", event.EventType)

	if event.Action != nil {
		if actionType, ok := event.Action["type"].(string); ok {
			attrs.UpsertString("event_type", actionType)
			attrs.UpsertString("target_element", actionType)
			attrs.UpsertString("action.type", actionType)
		}
		if id, ok := event.Action["id"].(string); ok {
			attrs.UpsertString("action.id", id)
		}
	}
	if event.DD != nil && event.DD.Action != nil && event.DD.Action.Target != nil {
		attrs.UpsertString("target_xpath", event.DD.Action.Target.Selector)
	}
	if event.View != nil {
		attrs.UpsertString("http.url", event.View.URL)
	}

	return traces
}

// convertSimpleEventToTraces 简单事件（view, action, log）转换为 Trace
func (c *Converter) convertSimpleEventToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	// 确定 Span Name
	spanName := c.getSpanNameForEvent(event)
	span.SetName(spanName)
	span.SetKind(ptrace.SpanKindInternal)

	// 时间戳
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(ts)
	span.SetEndTimestamp(ts + (pcommon.Timestamp(time.Millisecond)))

	// Trace & Span ID
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))

	// 属性
	attrs := span.Attributes()
	attrs.UpsertString("event.type", event.Type)
	attrs.UpsertString("event.domain", event.EventType)

	// 根据类型添加特定属性
	switch event.Type {
	case "view":
		if event.View != nil {
			if event.View.ID != "" {
				attrs.UpsertString("view.id", event.View.ID)
			}
			if event.View.URL != "" {
				attrs.UpsertString("view.url", event.View.URL)
			}
			if event.View.LoadingTime > 0 {
				attrs.UpsertInt("view.loading_time", event.View.LoadingTime)
			}
		}
	case "action":
		if event.Action != nil {
			if actionType, ok := event.Action["type"].(string); ok {
				attrs.UpsertString("action.type", actionType)
			}
			if id, ok := event.Action["id"].(string); ok {
				attrs.UpsertString("action.id", id)
			}
		}
	}

	return traces
}

// convertExceptionToTraces 错误事件转换为异常 Trace
func (c *Converter) convertExceptionToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	span.SetName("exception")
	span.SetKind(ptrace.SpanKindInternal)

	// 时间戳
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(ts)
	span.SetEndTimestamp(ts + (pcommon.Timestamp(time.Millisecond)))

	// Trace & Span ID
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))

	// 属性
	attrs := span.Attributes()
	attrs.UpsertString("event.type", event.Type)
	attrs.UpsertString("event.domain", event.EventType)

	// 错误状态和属性
	errorMsg := ""
	if event.Error != nil {
		if msg, ok := event.Error["message"].(string); ok {
			errorMsg = msg
		}
		if errorType, ok := event.Error["type"].(string); ok {
			attrs.UpsertString("exception.type", errorType)
		}
		if stacktrace, ok := event.Error["stacktrace"].(string); ok {
			attrs.UpsertString("exception.stacktrace", stacktrace)
		}
	}

	span.Status().SetCode(ptrace.StatusCodeError)
	if errorMsg != "" {
		span.Status().SetMessage(errorMsg)
	}

	return traces
}

// convertPerformanceToTraces performance 事件转换为 Trace
func (c *Converter) convertPerformanceToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	span.SetName(spanNameResourceLoad)
	span.SetKind(ptrace.SpanKindInternal)

	// 时间戳
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(ts)

	// 设置结束时间（由 duration 决定）
	endTs := ts + (pcommon.Timestamp(time.Millisecond))
	if event.Data != nil {
		if resourceData, ok := event.Data["resource"].(map[string]interface{}); ok {
			if duration, ok := resourceData["duration"].(float64); ok {
				endTs = ts + (pcommon.Timestamp(time.Duration(duration) * time.Millisecond))
			}
		}
	}
	span.SetEndTimestamp(endTs)

	// Trace & Span ID
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))

	// 属性
	attrs := span.Attributes()
	attrs.UpsertString("event.type", event.Type)
	attrs.UpsertString("event.domain", event.EventType)

	return traces
}

func (c *Converter) newClientEventTrace(
	event *DatadogEventV2,
	spanName string,
) (ptrace.Traces, ptrace.Span, pcommon.Timestamp) {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	scope := scopeSpans.Scope()
	scope.SetName(rumScopeName)
	scope.SetVersion(rumScopeVersion)

	span := scopeSpans.Spans().AppendEmpty()
	span.SetName(spanName)
	span.SetKind(ptrace.SpanKindClient)

	startTs := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(startTs)
	span.SetEndTimestamp(c.getClientSpanEndTimestamp(event, startTs))
	c.populateSpanIdentity(span, event)
	span.Attributes().UpsertString("event.type", event.Type)

	return traces, span, startTs
}

func (c *Converter) getClientSpanEndTimestamp(event *DatadogEventV2, startTs pcommon.Timestamp) pcommon.Timestamp {
	endTs := startTs + pcommon.Timestamp(time.Millisecond)
	if event == nil || event.Resource == nil || event.Resource.Duration <= 0 {
		return endTs
	}

	return startTs + pcommon.Timestamp(event.Resource.Duration)
}

func resourceDurationToMilliseconds(duration int64) float64 {
	if duration <= 0 {
		return 0
	}

	return float64(duration) / float64(time.Millisecond)
}

func (c *Converter) populateSpanIdentity(span ptrace.Span, event *DatadogEventV2) {
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))
}

func (c *Converter) addSharedClientTraceAttributes(
	span ptrace.Span,
	event *DatadogEventV2,
	resourceURL string,
) {
	attrs := span.Attributes()
	c.addViewTraceAttributes(attrs, event.View)
	c.addConnectivityAttributes(attrs, event.Connectivity)
	c.addResourceTraceAttributes(span, attrs, event.Resource, resourceURL)
}

func (c *Converter) addViewTraceAttributes(attrs pcommon.Map, view *ViewData) {
	if view == nil {
		return
	}

	if view.ID != "" {
		attrs.UpsertString("view.id", view.ID)
	}
	if view.URL != "" {
		attrs.UpsertString("view.url", view.URL)
	}
	if view.Referrer != "" {
		attrs.UpsertString("view.referrer", view.Referrer)
	}
	if view.FirstContentfulPaint > 0 {
		attrs.UpsertInt("view.first_contentful_paint", view.FirstContentfulPaint)
	}
	if view.LargestContentfulPaint > 0 {
		attrs.UpsertInt("view.largest_contentful_paint", view.LargestContentfulPaint)
	}
	if view.InteractionToNextPaint > 0 {
		attrs.UpsertInt("view.interaction_to_next_paint", view.InteractionToNextPaint)
	}
	if view.CumulativeLayoutShift > 0 {
		attrs.UpsertDouble("view.cumulative_layout_shift", view.CumulativeLayoutShift)
	}
	if view.LoadingTime > 0 {
		attrs.UpsertInt("view.loading_time", view.LoadingTime)
	}
	if view.TimeSpent > 0 {
		attrs.UpsertInt("view.time_spent", view.TimeSpent)
	}

	addCounterAttribute(attrs, "view.action.count", view.Action)
	addCounterAttribute(attrs, "view.error.count", view.Error)
	addCounterAttribute(attrs, "view.long_task.count", view.LongTask)
	addCounterAttribute(attrs, "view.resource.count", view.Resource)
	addCounterAttribute(attrs, "view.frustration.count", view.Frustration)
}

func addCounterAttribute(attrs pcommon.Map, key string, counter *Counter) {
	if counter == nil || counter.Count <= 0 {
		return
	}

	attrs.UpsertInt(key, int64(counter.Count))
}

func (c *Converter) addConnectivityAttributes(attrs pcommon.Map, connectivity map[string]interface{}) {
	if connectivity == nil {
		return
	}

	if status, ok := c.lookupStringValueWithOk(connectivity, "status"); ok {
		attrs.UpsertString("connectivity.status", status)
	}
	if effectiveType, ok := c.lookupStringValueWithOk(connectivity, "effective_type"); ok {
		attrs.UpsertString("connectivity.effective_type", effectiveType)
	}
}

func (c *Converter) addResourceTraceAttributes(
	span ptrace.Span,
	attrs pcommon.Map,
	resource *ResourceData,
	resourceURL string,
) {
	if resource == nil {
		return
	}

	if resourceURL != "" {
		attrs.UpsertString("http.url", resourceURL)
	}
	attrs.UpsertInt("http.status_code", int64(resource.StatusCode))
	c.setHTTPSpanStatus(span.Status(), resource.StatusCode)

	if resource.Type != "" {
		attrs.UpsertString("resource.type", resource.Type)
	}
	if resource.Protocol != "" {
		attrs.UpsertString("http.protocol", resource.Protocol)
	}
}

func (c *Converter) setHTTPSpanStatus(status ptrace.SpanStatus, statusCode int) {
	if statusCode >= 200 && statusCode < 300 {
		status.SetCode(ptrace.StatusCodeOk)
		status.SetMessage(fmt.Sprintf("HTTP status: %d", statusCode))
		return
	}

	status.SetCode(ptrace.StatusCodeError)
	status.SetMessage(fmt.Sprintf("HTTP Error: %d", statusCode))
}

func (c *Converter) getViewSpanName(event *DatadogEventV2) string {
	if event != nil && event.View != nil && !event.View.IsActive {
		return spanNameResourceFetch
	}

	return spanNameResourceLoad
}

func (c *Converter) addViewTimingEvents(span ptrace.Span, view *ViewData, baseTime pcommon.Timestamp) {
	if view == nil {
		return
	}

	if view.FirstContentfulPaint > 0 {
		addTimingEvent(span, "firstContentfulPaint", baseTime+pcommon.Timestamp(view.FirstContentfulPaint))
	}
	if view.LargestContentfulPaint > 0 {
		addTimingEvent(span, "largestContentfulPaint", baseTime+pcommon.Timestamp(view.LargestContentfulPaint))
	}
	if view.InteractionToNextPaintTime > 0 {
		addTimingEvent(span, "interactionToNextPaint", baseTime+pcommon.Timestamp(view.InteractionToNextPaintTime))
	}
	if view.CumulativeLayoutShiftTime > 0 {
		addTimingEvent(span, "cumulativeLayoutShift", baseTime+pcommon.Timestamp(view.CumulativeLayoutShiftTime))
	}
	if view.FirstByte > 0 {
		addTimingEvent(span, "responseStart", baseTime+pcommon.Timestamp(view.FirstByte))
	}
	if view.DOMInteractive > 0 {
		addTimingEvent(span, "domInteractive", baseTime+pcommon.Timestamp(view.DOMInteractive))
	}
	if view.DOMContentLoaded > 0 {
		addTimingEvent(span, "domContentLoadedEventEnd", baseTime+pcommon.Timestamp(view.DOMContentLoaded))
	}
	if view.DOMComplete > 0 {
		addTimingEvent(span, "domComplete", baseTime+pcommon.Timestamp(view.DOMComplete))
	}
	if view.LoadEvent > 0 {
		addTimingEvent(span, "loadEventEnd", baseTime+pcommon.Timestamp(view.LoadEvent))
	}
}

// convertResourceToTraces resource 事件转换为 Trace
func (c *Converter) convertResourceToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	scope := scopeSpans.Scope()
	scope.SetName(rumScopeName)
	scope.SetVersion(rumScopeVersion)

	span := scopeSpans.Spans().AppendEmpty()
	span.SetKind(ptrace.SpanKindClient)
	// span开始时间
	startTs := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(startTs)
	span.SetEndTimestamp(c.getClientSpanEndTimestamp(event, startTs))
	c.populateSpanIdentity(span, event)
	span.Attributes().UpsertString("event.type", event.Type)
	// 判断是否为发送 API 请求类型
	if event.Resource.Type == "xhr" || event.Resource.Type == "fetch" {
		span.SetName(event.Resource.Method)

		span.Attributes().UpsertString("http.method", event.Resource.Method)
		span.Attributes().UpsertString("http.url", event.Resource.URL)
		// 从 url 中获取 host
		parsedURL, err := url.Parse(event.Resource.URL)
		if err != nil {
			logger.Debugf("解析 URL %s 失败: %v", event.Resource.URL, err)
		}
		host := parsedURL.Host
		span.Attributes().UpsertString("http.host", host)
		span.Attributes().UpsertString("http.scheme", parsedURL.Scheme)
		span.Attributes().UpsertString("http.status_code", parsedURL.Scheme)
		if event.DD != nil {
			span.Attributes().UpsertString("datadog.trace_id", event.DD.TraceID)
			span.Attributes().UpsertString("datadog.span_id", event.DD.SpanID)
		}
		return traces
	}
	resourceURL := c.extractResourceURL(event)
	span.SetName(c.getResourceSpanNameForURL(resourceURL))
	c.addSharedClientTraceAttributes(span, event, resourceURL)

	// 添加完整的 resource timing events 链
	// 包括 fetchStart, DNS lookup, TCP connect, first byte, download 等
	if event.Resource != nil {
		c.addResourceTimingEvents(span, event.Resource, startTs)
	}
	return traces
}

// convertViewToTraces view 事件转换为 Trace
func (c *Converter) convertViewToTraces(event *DatadogEventV2) ptrace.Traces {
	resourceURL := c.extractResourceURL(event)
	traces, span, ts := c.newClientEventTrace(event, c.getViewSpanName(event))
	c.addSharedClientTraceAttributes(span, event, resourceURL)

	// 添加完整的 view 性能指标事件
	// 包括 FCP, LCP, INP, CLS 等
	c.addViewTimingEvents(span, event.View, ts)

	return traces
}

// convertLongTaskToTraces long_task 事件转换为 Trace
func (c *Converter) convertLongTaskToTraces(event *DatadogEventV2) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	c.enrichResource(resourceSpans.Resource(), event)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()

	span.SetName("browser.long_task")
	span.SetKind(ptrace.SpanKindInternal)

	// 时间戳
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))
	span.SetStartTimestamp(ts)

	// 设置结束时间
	endTs := ts + (pcommon.Timestamp(time.Millisecond))
	if event.LongTask != nil {
		if duration, ok := event.LongTask["duration"].(float64); ok {
			endTs = ts + (pcommon.Timestamp(time.Duration(duration) * time.Millisecond))
		}
	}
	span.SetEndTimestamp(endTs)

	// Trace & Span ID
	traceID := c.generateTraceID(event)
	spanID := c.generateSpanID(event)
	span.SetTraceID(c.stringToTraceID(traceID))
	span.SetSpanID(c.stringToSpanID(spanID))

	// 属性
	attrs := span.Attributes()
	attrs.UpsertString("event.type", event.Type)
	attrs.UpsertString("event.domain", event.EventType)

	if event.LongTask != nil {
		if duration, ok := event.LongTask["duration"].(float64); ok {
			attrs.UpsertDouble("longtask.duration", duration)
		}
		if attribution, ok := event.LongTask["attribution"].(string); ok {
			attrs.UpsertString("longtask.attribution", attribution)
		}
	}

	return traces
}

// ======== 指标转换 ========

// convertToMetrics 将事件转换为指标数据
func (c *Converter) convertToMetrics(event *DatadogEventV2) pmetric.Metrics {
	if event.Type != "performance" && event.Type != "resource" && event.Type != "long_task" {
		return pmetric.NewMetrics()
	}

	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	c.enrichResource(resourceMetrics.Resource(), event)

	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))

	switch event.Type {
	case "performance":
		if event.Data != nil {
			if resourceData, ok := event.Data["resource"].(map[string]interface{}); ok {
				c.addPerformanceMetrics(scopeMetrics, resourceData, ts)
			}
		}
	case "resource":
		if event.Resource != nil {
			c.addResourceMetrics(scopeMetrics, event.Resource, ts)
		}
	case "long_task":
		if event.LongTask != nil {
			c.addLongTaskMetrics(scopeMetrics, event.LongTask, ts)
		}
	}

	return metrics
}

// addPerformanceMetrics 添加性能指标
func (c *Converter) addPerformanceMetrics(scopeMetrics pmetric.ScopeMetrics, resourceData map[string]interface{}, ts pcommon.Timestamp) {
	// Duration 指标
	if duration, ok := resourceData["duration"].(float64); ok {
		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName("rum.request.duration_ms")
		metric.SetDescription("Duration of RUM request in milliseconds")
		metric.SetUnit("ms")

		metric.SetDataType(pmetric.MetricDataTypeHistogram)
		histogram := metric.Histogram()
		histogram.SetAggregationTemporality(pmetric.MetricAggregationTemporalityCumulative)
		dataPoint := histogram.DataPoints().AppendEmpty()
		dataPoint.SetTimestamp(ts)
		dataPoint.SetCount(1)
		dataPoint.SetSum(duration)
		dataPoint.SetMExplicitBounds([]float64{10, 50, 100, 500, 1000})
		bucketCounts := []uint64{0, 0, 0, 0, 0, 1}
		dataPoint.SetMBucketCounts(bucketCounts)
		dataPoint.Attributes().UpsertString("event.type", "performance")
	}

	// Size 指标
	if size, ok := resourceData["size"].(float64); ok {
		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName("rum.response.size_bytes")
		metric.SetDescription("Size of RUM response in bytes")
		metric.SetUnit("bytes")

		metric.SetDataType(pmetric.MetricDataTypeGauge)
		gauge := metric.Gauge()
		dataPoint := gauge.DataPoints().AppendEmpty()
		dataPoint.SetTimestamp(ts)
		dataPoint.SetDoubleVal(size)
		dataPoint.Attributes().UpsertString("event.type", "performance")
	}
}

// addResourceMetrics 添加资源指标
func (c *Converter) addResourceMetrics(scopeMetrics pmetric.ScopeMetrics, resource *ResourceData, ts pcommon.Timestamp) {
	if resource == nil {
		return
	}

	// Duration 指标
	if resource.Duration > 0 {
		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName("http.client.duration_ms")
		metric.SetDescription("HTTP client request duration")
		metric.SetUnit("ms")

		metric.SetDataType(pmetric.MetricDataTypeHistogram)
		histogram := metric.Histogram()
		histogram.SetAggregationTemporality(pmetric.MetricAggregationTemporalityCumulative)
		dataPoint := histogram.DataPoints().AppendEmpty()
		dataPoint.SetTimestamp(ts)
		dataPoint.SetCount(1)
		dataPoint.SetSum(resourceDurationToMilliseconds(resource.Duration))
		dataPoint.SetMExplicitBounds([]float64{10, 50, 100, 500, 1000})
		bucketCounts := []uint64{0, 0, 0, 0, 0, 1}
		dataPoint.SetMBucketCounts(bucketCounts)
		dataPoint.Attributes().UpsertString("event.type", "resource")
	}

	// Size 指标
	if resource.Size > 0 {
		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName("http.client.response_size_bytes")
		metric.SetDescription("HTTP client response size")
		metric.SetUnit("bytes")

		metric.SetDataType(pmetric.MetricDataTypeGauge)
		gauge := metric.Gauge()
		dataPoint := gauge.DataPoints().AppendEmpty()
		dataPoint.SetTimestamp(ts)
		dataPoint.SetDoubleVal(float64(resource.Size))
		dataPoint.Attributes().UpsertString("event.type", "resource")
	}
}

// addLongTaskMetrics 添加长任务指标
func (c *Converter) addLongTaskMetrics(scopeMetrics pmetric.ScopeMetrics, longTaskData map[string]interface{}, ts pcommon.Timestamp) {
	if duration, ok := longTaskData["duration"].(float64); ok {
		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName("browser.long_task.duration_ms")
		metric.SetDescription("Duration of browser long task in milliseconds")
		metric.SetUnit("ms")

		metric.SetDataType(pmetric.MetricDataTypeHistogram)
		histogram := metric.Histogram()
		histogram.SetAggregationTemporality(pmetric.MetricAggregationTemporalityCumulative)
		dataPoint := histogram.DataPoints().AppendEmpty()
		dataPoint.SetTimestamp(ts)
		dataPoint.SetCount(1)
		dataPoint.SetSum(duration)
		dataPoint.SetMExplicitBounds([]float64{10, 50, 100, 500, 1000})
		bucketCounts := []uint64{0, 0, 0, 0, 0, 1}
		dataPoint.SetMBucketCounts(bucketCounts)
		dataPoint.Attributes().UpsertString("event.type", "long_task")
	}
}

// convertLongTaskToMetrics 长任务事件转换为 Metrics
func (c *Converter) convertLongTaskToMetrics(event *DatadogEventV2) pmetric.Metrics {
	if event.Type != "long_task" {
		return pmetric.NewMetrics()
	}

	if event.LongTask == nil {
		return pmetric.NewMetrics()
	}

	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	c.enrichResource(resourceMetrics.Resource(), event)

	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()
	ts := pcommon.NewTimestampFromTime(time.UnixMilli(event.Date))

	c.addLongTaskMetrics(scopeMetrics, event.LongTask, ts)

	return metrics
}

// ======== 辅助方法 ========

// enrichResource 将事件信息添加到 Resource。
func (c *Converter) enrichResource(resource pcommon.Resource, event *DatadogEventV2) {
	attrs := resource.Attributes()

	// 复制基础 Resource 属性
	attrs.UpsertString("service.name", "datadog-rum")
	attrs.UpsertString("service.source", "datadog")
	attrs.UpsertString("telemetry.sdk.name", "datadog-browser")
	attrs.UpsertString("telemetry.sdk.language", "javascript")

	// 添加服务信息
	if event.Service != "" {
		attrs.UpsertString("service.name", event.Service)
	}
	if event.Version != "" {
		attrs.UpsertString("service.version", event.Version)
	}

	// 添加应用信息
	if event.Application != nil {
		if appID, ok := event.Application["id"].(string); ok {
			attrs.UpsertString("application.id", appID)
		}
	}

	// 添加会话信息
	if event.Session != nil {
		if sessionID, ok := event.Session["id"].(string); ok {
			attrs.UpsertString("session.id", sessionID)
		}
		if sessionType, ok := event.Session["type"].(string); ok {
			attrs.UpsertString("session.type", sessionType)
		}
	}

	// 添加用户信息
	if event.User != nil {
		if userID, ok := event.User["id"].(string); ok {
			attrs.UpsertString("user.id", userID)
		}
		if anonymousID, ok := event.User["anonymous_id"].(string); ok {
			attrs.UpsertString("user.anonymous_id", anonymousID)
		}
	}

	// 添加源和标签
	if event.Source != "" {
		attrs.UpsertString("rum.source", event.Source)
	}
	if event.DDTags != "" {
		attrs.UpsertString("dd.tags", event.DDTags)
	}
}

// getSpanNameForEvent 根据事件类型获取 Span Name
func (c *Converter) getSpanNameForEvent(event *DatadogEventV2) string {
	switch event.Type {
	case "view":
		return "page.view"
	case "action":
		return "ui.action"
	case "log":
		return "log"
	case "resource":
		return c.getResourceSpanName(event)
	case "long_task":
		return "browser.long_task"
	case "error":
		return "exception"
	case "performance":
		return spanNameResourceLoad
	default:
		return fmt.Sprintf("%s.%s", event.Type, event.EventType)
	}
}

// getResourceSpanName 根据 resource URL 识别 Span Name
func (c *Converter) getResourceSpanName(event *DatadogEventV2) string {
	resourceURL := c.extractResourceURL(event)
	return c.getResourceSpanNameForURL(resourceURL)
}

// getResourceSpanNameForURL 根据 resource URL 识别 Span Name。
func (c *Converter) getResourceSpanNameForURL(resourceURL string) string {
	if c.isStaticResourceURL(resourceURL) {
		return spanNameResourceFetch
	}
	return spanNameResourceLoad
}

// extractResourceURL 提取 resource URL
func (c *Converter) extractResourceURL(event *DatadogEventV2) string {
	if event.Resource != nil && event.Resource.URL != "" {
		return event.Resource.URL
	}

	if event.Data != nil {
		if resourceData, ok := event.Data["resource"].(map[string]interface{}); ok {
			if resourceURL, ok := resourceData["url"].(string); ok && resourceURL != "" {
				return resourceURL
			}
			if name, ok := resourceData["name"].(string); ok && name != "" {
				return name
			}
		}
	}

	return ""
}

// shouldGenerateLogForResource 判断 resource 事件是否需要额外日志。
func (c *Converter) shouldGenerateLogForResource(event *DatadogEventV2) bool {
	if event == nil || event.Resource == nil {
		return false
	}

	// 仅当 HTTP 状态码非 2xx 时生成额外日志
	return event.Resource.StatusCode < 200 || event.Resource.StatusCode >= 300
}

// isStaticResourceURL 判断 URL 是否为静态资源
func (c *Converter) isStaticResourceURL(resourceURL string) bool {
	if resourceURL == "" {
		return false
	}

	ext := ""
	if parsedURL, err := url.Parse(resourceURL); err == nil {
		ext = strings.ToLower(path.Ext(parsedURL.Path))
	}

	if ext == "" {
		cleanURL := strings.SplitN(resourceURL, "?", 2)[0]
		cleanURL = strings.SplitN(cleanURL, "#", 2)[0]
		ext = strings.ToLower(path.Ext(cleanURL))
	}

	switch ext {
	case ".js", ".css", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico", ".bmp":
		return true
	default:
		return false
	}
}

// generateTraceID 生成 Trace ID（16 字节 / 32 hex chars）。
// Datadog RUM 事件本身没有稳定的 trace_id，因此优先使用 session.id 做确定性映射。
func (c *Converter) generateTraceID(event *DatadogEventV2) string {
	var source string

	// 优先使用会话 ID
	if event.Session != nil {
		if sid, ok := event.Session["id"].(string); ok && sid != "" {
			source = sid
		}
	}

	// 如果没有会话 ID，使用时间戳和事件类型
	if source == "" {
		source = fmt.Sprintf("%d-%s-%s", event.Date, event.Type, event.EventType)
	}

	return c.hashToFixedHex(source, 32)
}

// generateSpanID 生成 Span ID（8 字节 / 16 hex chars）。
// 优先使用各类 RUM 事件自身的 id，其他事件回退到通用稳定哈希策略。
func (c *Converter) generateSpanID(event *DatadogEventV2) string {
	if event == nil {
		return c.hashToFixedHex("nil-event", 16)
	}

	if spanIDSeed, ok := c.lookupEventSpecificSpanID(event); ok {
		return c.hashToFixedHex(spanIDSeed, 16)
	}

	source := fmt.Sprintf("%d-%s-%s", event.Date, event.Type, event.EventType)
	if sessionID, ok := c.lookupStringValueWithOk(event.Session, "id"); ok {
		source = fmt.Sprintf("%s-%s", sessionID, source)
	}

	return c.hashToFixedHex(source, 16)
}

// lookupEventSpecificSpanID 提取不同事件类型自身的唯一 id。
func (c *Converter) lookupEventSpecificSpanID(event *DatadogEventV2) (string, bool) {
	if event == nil {
		return "", false
	}

	switch event.Type {
	case "resource":
		if event.Resource != nil && event.Resource.ID != "" {
			return event.Resource.ID, true
		}
	case "view":
		if event.View != nil && event.View.ID != "" {
			return event.View.ID, true
		}
	case "action":
		if event.Action != nil {
			if id, ok := event.Action["id"].(string); ok && id != "" {
				return id, true
			}
		}
	case "long_task":
		if event.LongTask != nil {
			if id, ok := event.LongTask["id"].(string); ok && id != "" {
				return id, true
			}
		}
	case "error":
		if event.Error != nil {
			if id, ok := event.Error["id"].(string); ok && id != "" {
				return id, true
			}
		}
	case "performance":
		if event.Data != nil {
			if resourceData, ok := event.Data["resource"].(map[string]interface{}); ok {
				if id, ok := resourceData["id"].(string); ok && id != "" {
					return id, true
				}
			}
		}
	}

	return "", false
}

// lookupStringValueWithOk 从 map 中提取字符串值并返回是否存在。
func (c *Converter) lookupStringValueWithOk(fields map[string]interface{}, key string) (string, bool) {
	if fields == nil {
		return "", false
	}

	value, ok := fields[key].(string)
	if !ok {
		return "", false
	}

	if value == "" {
		return "", false
	}

	return value, true
}

// hashToFixedHex 使用 FNV-1a 生成固定长度的十六进制字符串。
func (c *Converter) hashToFixedHex(source string, length int) string {
	if length <= 0 {
		return ""
	}

	chunkCount := (length + 15) / 16
	var builder strings.Builder
	builder.Grow(chunkCount * 16)

	for i := 0; i < chunkCount; i++ {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(source))
		if i > 0 {
			_, _ = hasher.Write([]byte{byte(i)})
		}
		builder.WriteString(fmt.Sprintf("%016x", hasher.Sum64()))
	}

	result := builder.String()
	if len(result) > length {
		return result[:length]
	}
	return result
}

// getTimingValues 从 ResourceData 中提取 start 和 duration 时间值。
// 返回相对于基准时间（baseTime）的计算时间戳。
func (c *Converter) getTimingValues(
	timing *ResourceTiming,
	baseTime pcommon.Timestamp,
) (pcommon.Timestamp, pcommon.Timestamp) {
	if timing == nil {
		return baseTime, baseTime
	}

	startTs := baseTime + pcommon.Timestamp(uint64(timing.Start))
	endTs := startTs + pcommon.Timestamp(uint64(timing.Duration))

	return startTs, endTs
}

// addTimingEvent 向 span 中添加单个 timing event。
func addTimingEvent(span ptrace.Span, name string, timestamp pcommon.Timestamp) {
	event := span.Events().AppendEmpty()
	event.SetName(name)
	event.SetTimestamp(timestamp)
	event.SetDroppedAttributesCount(0)
}

// addResourceTimingEvents 为资源请求添加完整的 timing events 链。
// 包括 DNS、Connect、FirstByte 和 Download 事件。
func (c *Converter) addResourceTimingEvents(span ptrace.Span, resource *ResourceData, baseTime pcommon.Timestamp) {
	if resource == nil {
		return
	}

	// fetchStart
	addTimingEvent(span, "fetchStart", baseTime)

	// DNS timing: domainLookupStart 和 domainLookupEnd
	if resource.DNS != nil {
		dnsStart, dnsEnd := c.getTimingValues(resource.DNS, baseTime)
		addTimingEvent(span, "domainLookupStart", dnsStart)
		addTimingEvent(span, "domainLookupEnd", dnsEnd)
	}

	// TCP Connect timing: connectStart 和 connectEnd
	if resource.Connect != nil {
		connectStart, connectEnd := c.getTimingValues(resource.Connect, baseTime)
		addTimingEvent(span, "connectStart", connectStart)
		addTimingEvent(span, "connectEnd", connectEnd)
	} else {
		// 无连接数据时使用基准时间
		addTimingEvent(span, "connectStart", baseTime)
		addTimingEvent(span, "connectEnd", baseTime)
	}

	// First Byte timing: requestStart 和 responseStart
	if resource.FirstByte != nil {
		fbStart, fbEnd := c.getTimingValues(resource.FirstByte, baseTime)
		addTimingEvent(span, "requestStart", fbStart)
		addTimingEvent(span, "responseStart", fbEnd)
	}

	// Download timing: responseEnd
	if resource.Download != nil {
		_, dlEnd := c.getTimingValues(resource.Download, baseTime)
		addTimingEvent(span, "responseEnd", dlEnd)
	}
}
