package aegisv2

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var errMappingNotImplemented = errors.New("aegisv2 mapping rule is not implemented")

const (
	defaultServiceName = "unknown_service"
)

// OTel 标准直方图桶边界（毫秒级，用于时间指标：FCP/LCP/FID/INP）
var histogramBoundsMS = []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}

// OTel 标准直方图桶边界（比例级，用于无量纲指标：CLS）
var histogramBoundsRatio = []float64{0, 0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 1.0}

// vitalThresholds 定义 Web Vital 指标的质量阈值
type vitalThresholds struct {
	good             float64
	needsImprovement float64
}

var vitalRatingConfig = map[string]vitalThresholds{
	"fcp": {1800, 3000}, // FCP: Good ≤ 1.8s, Needs Improvement ≤ 3s
	"lcp": {2500, 4000}, // LCP: Good ≤ 2.5s, Needs Improvement ≤ 4s
	"fid": {100, 300},   // FID: Good ≤ 100ms, Needs Improvement ≤ 300ms
	"inp": {200, 500},   // INP: Good ≤ 200ms, Needs Improvement ≤ 500ms
	"cls": {0.1, 0.25},  // CLS: Good ≤ 0.1, Needs Improvement ≤ 0.25
}

func decodeTraces(buf []byte) (ptrace.Traces, bool, error) {
	return decodeTracesWithTraceID(buf, pcommon.TraceID{})
}

func decodeTracesWithTraceID(buf []byte, requestTraceID pcommon.TraceID) (ptrace.Traces, bool, error) {
	payload, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return ptrace.Traces{}, handled, err
	}
	records, err := parseD2Records(payload.D2)
	if err != nil {
		return ptrace.Traces{}, true, err
	}
	traces, err := splitTraces(payload, records, requestTraceID)
	return traces, true, err
}

func decodeMetrics(buf []byte) (pmetric.Metrics, bool, error) {
	payload, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return pmetric.Metrics{}, handled, err
	}
	records, err := parseD2Records(payload.D2)
	if err != nil {
		return pmetric.Metrics{}, true, err
	}
	metrics, err := splitMetrics(payload, records)
	return metrics, true, err
}

func decodeLogs(buf []byte) (plog.Logs, bool, error) {
	_, handled, err := parseCollectPayload(buf)
	if !handled || err != nil {
		return plog.Logs{}, handled, err
	}
	logs, err := splitLogs()
	return logs, true, err
}

func parseCollectPayload(buf []byte) (collectPayload, bool, error) {
	var payload collectPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		// Log detailed error for debugging malformed payloads
		preview := getPayloadPreview(buf, 50)
		logger.Debugf("aegisv2 parseCollectPayload failed to unmarshal, payload preview: %s, error: %v", preview, err)
		return collectPayload{}, false, nil
	}
	if payload.Topic == "" && payload.Scheme == "" && len(payload.D2) == 0 {
		// This looks like an OTLP payload, not aegisv2 format
		return collectPayload{}, false, nil
	}
	return payload, true, nil
}

// getPayloadPreview returns a safe preview of the payload for logging
func getPayloadPreview(buf []byte, maxLen int) string {
	if len(buf) == 0 {
		return "<empty>"
	}
	if len(buf) <= maxLen {
		return escapeString(string(buf))
	}
	return escapeString(string(buf[:maxLen])) + "..."
}

// escapeString escapes non-printable characters in a string for safe logging
func escapeString(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 32 && c < 127 && c != '\\' && c != '"' {
			b = append(b, c)
		} else {
			switch c {
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '?')
			}
		}
	}
	return string(b)
}

func parseD2Records(raw json.RawMessage) ([]d2Record, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var records []d2Record
	return records, json.Unmarshal(raw, &records)
}

// splitTraces 将 aegisv2 Payload 转换为 OTel Traces。
func splitTraces(payload collectPayload, records []d2Record, traceID pcommon.TraceID) (ptrace.Traces, error) {
	traces := ptrace.NewTraces()
	if len(records) == 0 {
		return traces, nil
	}

	resourceSpans := traces.ResourceSpans().AppendEmpty()
	resourceAttrs := resourceSpans.Resource().Attributes()
	setCommonResourceAttrs(resourceAttrs, payload, records[0].Fields.Session.ID)
	upsertString(resourceAttrs, "referer", payload.Bean.Referer)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	setCollectorScope(scopeSpans.Scope(), payload.Bean.Version)

	now := pcommon.NewTimestampFromTime(time.Now())
	if traceID == (pcommon.TraceID{}) {
		traceID = random.TraceID()
	}

	for _, record := range records {
		if record.Fields.Action.IsValid() {
			appendActionSpan(scopeSpans, traceID, now, payload, record)
		}
		for _, msg := range record.Message {
			event := aegisEvent{record, msg}
			if event.IsWebVitals() {
				appendWebVitalsSpans(scopeSpans, traceID, now, payload, record, msg)
			} else {
				appendMessageSpan(scopeSpans, traceID, now, payload, record, msg)
			}
		}
	}

	if logger.LoggerLevel() == logger.DebugLevelDesc {
		if b, err := json.Marshal(records); err == nil {
			logger.Debugf("aegisv2/splitTraces: %s", b)
		}
	}
	return traces, nil
}

func appendSpan(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, name string, kind ptrace.SpanKind, startTs, endTs pcommon.Timestamp) ptrace.Span {
	span := scopeSpans.Spans().AppendEmpty()
	span.SetTraceID(traceID)
	span.SetSpanID(random.SpanID())
	span.SetName(name)
	span.SetKind(kind)
	span.SetStartTimestamp(startTs)
	span.SetEndTimestamp(endTs)
	return span
}

func appendActionSpan(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record) {
	actionTs := millisToTimestamp(record.Fields.Action.Timestamp, now)
	actionSpan := appendSpan(scopeSpans, traceID, record.Fields.Action.SpanName(), ptrace.SpanKindInternal, actionTs, actionTs)

	actionAttrs := actionSpan.Attributes()
	setCommonSpanAttrs(actionAttrs, payload, record)
	upsertString(actionAttrs, "event.type", "action")
	upsertString(actionAttrs, "event.plugin", "action")
	upsertString(actionAttrs, "action.id", record.Fields.Action.ID)
	upsertNonZeroInt(actionAttrs, "action.timestamp", record.Fields.Action.Timestamp)
	upsertString(actionAttrs, "action.type", record.Fields.Action.ActionType)
	upsertString(actionAttrs, "action.name", record.Fields.Action.ActionName)
	upsertString(actionAttrs, "action.target_name", record.Fields.Action.ActionTargetName)
	upsertString(actionAttrs, "action.source_event_type", record.Fields.Type)
}

// appendMessageSpan 构造一条消息对应的 Span。
// 已识别事件类型按语义填充 span kind 和领域属性；未识别类型退化为通用 SpanEvent。
func appendMessageSpan(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record, msg d2Message) {
	e := aegisEvent{record: record, msg: msg}
	eventType := e.EventType()

	spanStartTs, spanEndTs := messageSpanTimeRange(eventType, msg, now)
	span := appendSpan(scopeSpans, traceID, e.SpanName(), ptrace.SpanKindUnspecified, spanStartTs, spanEndTs)

	spanAttrs := span.Attributes()
	setCommonSpanAttrs(spanAttrs, payload, record)
	upsertNonZeroInt(spanAttrs, attrEventTimestamp, msg.Timestamp)

	if payload.Bean.Referer != "" {
		link := span.Links().AppendEmpty()
		link.Attributes().UpsertString(attrLink, payload.Bean.Referer)
	}

	if spanKind, ok := e.SpanKind(); ok {
		span.SetKind(spanKind)
		upsertFlattenedMap(spanAttrs, attrAegisExtPrefix, msg.raw)
		switch eventType {
		case EventTypeAssetSpeed:
			setAssetSpeedSpanAttrs(spanAttrs, payload, record, msg)
			appendResourceTimingEvents(span.Events(), msg, spanStartTs)
		case EventTypeWebsocket:
			setWebsocketSpanAttrs(spanAttrs, payload, record, msg)
		case EventTypePagePerformance:
			setPagePerformanceSpanAttrs(spanAttrs, record)
			appendPagePerformanceEvents(span.Events(), msg, spanStartTs, spanEndTs)
			appendPagePerformancePhaseEvents(span.Events(), msg, spanStartTs)
		}
		if e.IsError() {
			setSpanException(span, spanAttrs, e.ExceptionType(), firstNonEmptyString(msg.raw, "errorMsg", "msg"))
		}
		return
	}

	// 未识别类型：退化为通用事件，保留原始字段，避免信息丢失。
	fallbackEvent := span.Events().AppendEmpty()
	fallbackEvent.SetName(spanEventAegisFallback)
	fallbackEvent.SetTimestamp(spanEndTs)
	upsertFlattenedMap(fallbackEvent.Attributes(), attrAegisExtPrefix, msg.raw)
}

// messageSpanTimeRange 统一计算消息类 span 的时间范围。
func messageSpanTimeRange(eventType EventType, msg d2Message, now pcommon.Timestamp) (pcommon.Timestamp, pcommon.Timestamp) {
	if eventType == EventTypeAssetSpeed || eventType == EventTypeAPI || eventType == EventTypeWebsocket {
		startTs := millisToTimestamp(msg.Timestamp, now)
		durationMs, ok := extractFloat64(msg.raw, msgKeyDuration)
		if !ok || durationMs <= 0 {
			return startTs, startTs
		}
		endTs := pcommon.Timestamp(startTs.AsTime().Add(time.Duration(durationMs * float64(time.Millisecond))).UnixNano())
		return startTs, endTs
	}
	return msg.TimeRange(now)
}

func setSpanException(span ptrace.Span, attrs pcommon.Map, exceptionType, message string) {
	span.Status().SetCode(ptrace.StatusCodeError)
	upsertString(attrs, "exception.type", exceptionType)
	if message != "" {
		upsertString(attrs, "error.message", message)
		upsertString(attrs, "exception.message", message)
	}
}

// appendWebVitalsSpans 将一条 web_vitals 消息拆解为每个指标各自独立的 Span。
// 值为 -1 表示客户端未采集，跳过不写。
func appendWebVitalsSpans(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record, msg d2Message) {
	timestamp := millisToTimestamp(msg.Timestamp, now)
	gotoID := firstNonEmptyString(msg.raw, "aegisv2_goto")
	pageURL := recordPageURL(record)

	for _, v := range webVitalsDefs {
		value, ok := extractFloat64(msg.raw, v.key)
		if !ok || value < 0 {
			continue
		}

		span := appendSpan(scopeSpans, traceID, spanNameBrowserVital, ptrace.SpanKindInternal, timestamp, timestamp)
		setWebVitalSpanAttrs(span.Attributes(), payload, record, msg.Timestamp, v.metric, value, gotoID, pageURL)
	}
}

func setWebVitalSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record, timestamp int64, metric string, value any, gotoID, pageURL string) {
	setCommonSpanAttrs(attrs, payload, record)
	upsertNonZeroInt(attrs, attrEventTimestamp, timestamp)
	upsertString(attrs, "span_type", "vital")
	upsertString(attrs, "span_subtype", metric)
	upsertString(attrs, "event_label", "Web 指标")
	setResultAttrs(attrs, "success", "none")
	upsertString(attrs, "target_label", metric)
	upsertAny(attrs, "target_value", value)
	upsertString(attrs, "vital.metric", metric)
	upsertAny(attrs, "vital.value", value)
	if gotoID != "" {
		upsertString(attrs, "vital.id", gotoID+"."+metric)
	}
	setFullPageAttrs(attrs, pageURL)
	setBrowserContextAttrs(attrs, payload)
}

// appendScopeMetrics 构建 ResourceMetrics/ScopeMetrics 并设置公共属性
// 消除 splitTraces/splitMetrics 中的代码重复
func appendScopeMetrics(metrics pmetric.Metrics, payload collectPayload, records []d2Record) pmetric.ScopeMetrics {
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	resourceAttrs := resourceMetrics.Resource().Attributes()
	if len(records) > 0 {
		setCommonResourceAttrs(resourceAttrs, payload, records[0].Fields.Session.ID)
	} else {
		setCommonResourceAttrs(resourceAttrs, payload, "")
	}

	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()
	setCollectorScope(scopeMetrics.Scope(), payload.Bean.Version)
	return scopeMetrics
}

func splitMetrics(payload collectPayload, records []d2Record) (pmetric.Metrics, error) {
	metrics := pmetric.NewMetrics()
	if len(records) == 0 {
		return metrics, nil
	}

	scopeMetrics := appendScopeMetrics(metrics, payload, records)
	now := pcommon.NewTimestampFromTime(time.Now())
	collector := newWebVitalsCollector()

	for _, record := range records {
		pageURL := recordPageURL(record)
		for _, msg := range record.Message {
			event := aegisEvent{record: record, msg: msg}
			if event.IsWebVitals() {
				collector.collect(event, millisToTimestamp(msg.Timestamp, now), pageURL, payload.Bean.NetType)
			}
		}
	}

	return metrics, collector.export(scopeMetrics)
}

type webVitalsCollector struct {
	data []webVitalDataPoint
}

func newWebVitalsCollector() *webVitalsCollector {
	return &webVitalsCollector{data: make([]webVitalDataPoint, 0, 32)}
}

func (c *webVitalsCollector) collect(event aegisEvent, timestamp pcommon.Timestamp, pageURL, netType string) {
	for _, def := range webVitalsDefs {
		value, ok := extractFloat64(event.msg.raw, def.key)
		if !ok || value < 0 {
			continue
		}
		c.data = append(c.data, webVitalDataPoint{
			timestamp:  timestamp,
			metricName: def.metric,
			value:      value,
			sessionID:  event.record.Fields.Session.ID,
			viewID:     event.record.Fields.View.ID,
			viewName:   event.record.Fields.View.ViewName,
			viewURL:    event.record.Fields.View.ViewURL,
			pageURL:    pageURL,
			netType:    netType,
		})
	}
}

func (c *webVitalsCollector) export(scopeMetrics pmetric.ScopeMetrics) error {
	if len(c.data) == 0 {
		return nil
	}

	histogram := newWebVitalsHistogram(scopeMetrics)

	for _, data := range c.data {
		data.appendTo(histogram)
	}

	return nil
}

func newWebVitalsHistogram(scopeMetrics pmetric.ScopeMetrics) pmetric.Histogram {
	histogramMetric := scopeMetrics.Metrics().AppendEmpty()
	histogramMetric.SetName("browser.web_vital.duration")
	histogramMetric.SetDescription("Web Vitals duration metrics from aegisv2")
	histogramMetric.SetUnit("ms")
	histogramMetric.SetDataType(pmetric.MetricDataTypeHistogram)
	return histogramMetric.Histogram()
}

type webVitalDataPoint struct {
	timestamp  pcommon.Timestamp
	metricName string
	value      float64
	sessionID  string
	viewID     string
	viewName   string
	viewURL    string
	pageURL    string
	netType    string
}

func (d webVitalDataPoint) appendTo(histogram pmetric.Histogram) {
	dataPoint := histogram.DataPoints().AppendEmpty()
	dataPoint.SetTimestamp(d.timestamp)
	dataPoint.SetCount(1)
	dataPoint.SetSum(d.value)
	dataPoint.SetMin(d.value)
	dataPoint.SetMax(d.value)

	d.fillAttrs(dataPoint.Attributes())
	bounds := d.bounds()
	dataPoint.SetMExplicitBounds(bounds)
	dataPoint.SetMBucketCounts(bucketCountsForValue(d.value, bounds))
}

func (d webVitalDataPoint) fillAttrs(attrs pcommon.Map) {
	upsertString(attrs, "session.id", d.sessionID)
	upsertString(attrs, "view.id", d.viewID)
	upsertString(attrs, "view.name", d.viewName)
	upsertString(attrs, "view.url", d.viewURL)
	upsertString(attrs, "vital.metric", d.metricName)
	upsertString(attrs, "vital.rating", webVitalRating(d.metricName, d.value))
	if d.pageURL != "" {
		upsertString(attrs, "url.full", d.pageURL)
	}
	if d.netType != "" {
		upsertString(attrs, "network.effective_type", strings.ToLower(d.netType))
	}
}

func (d webVitalDataPoint) bounds() []float64 {
	if d.metricName == "cls" {
		return histogramBoundsRatio
	}
	return histogramBoundsMS
}

func bucketCountsForValue(value float64, bounds []float64) []uint64 {
	bucketCounts := make([]uint64, len(bounds)+1)
	bucketCounts[findBucketIndex(value, bounds)] = 1
	return bucketCounts
}

// findBucketIndex 根据值找出在直方图中的桶索引
func findBucketIndex(value float64, bounds []float64) int {
	for i := 0; i < len(bounds); i++ {
		if value < bounds[i] {
			return i
		}
	}
	return len(bounds)
}

// webVitalRating 根据指标类型和值返回评级（good/needs improvement/poor）
func webVitalRating(metric string, value float64) string {
	thresholds, ok := vitalRatingConfig[metric]
	if !ok {
		return "unknown"
	}
	if value <= thresholds.good {
		return "good"
	}
	if value <= thresholds.needsImprovement {
		return "needs improvement"
	}
	return "poor"
}

func splitLogs() (plog.Logs, error) {
	return plog.NewLogs(), errors.Wrap(errMappingNotImplemented, "logs mapping")
}
