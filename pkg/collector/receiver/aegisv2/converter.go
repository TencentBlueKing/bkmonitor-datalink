package aegisv2

import (
	"encoding/json"
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

// ErrNotAegisV2 indicates the payload is not in aegisv2 format.
var ErrNotAegisV2 = errors.New("aegisv2: not an aegisv2 payload")

const (
	defaultServiceName = "unknown_service"
)

func decodeTraces(buf []byte) (ptrace.Traces, error) {
	return decodeTracesWithTraceID(buf, pcommon.TraceID{})
}

func decodeTracesWithTraceID(buf []byte, requestTraceID pcommon.TraceID) (ptrace.Traces, error) {
	payload, err := parseCollectPayload(buf)
	if err != nil {
		return ptrace.Traces{}, err
	}
	records, err := parseD2Records(payload.D2)
	if err != nil {
		return ptrace.Traces{}, err
	}
	traces, _, err := convertTraces(payload, records, requestTraceID)
	return traces, err
}

func decodeMetrics(buf []byte) (pmetric.Metrics, error) {
	payload, err := parseCollectPayload(buf)
	if err != nil {
		return pmetric.Metrics{}, err
	}
	records, err := parseD2Records(payload.D2)
	if err != nil {
		return pmetric.Metrics{}, err
	}
	return convertMetrics(payload, records)
}

func decodeLogs(buf []byte) (plog.Logs, error) {
	_, err := parseCollectPayload(buf)
	if err != nil {
		return plog.Logs{}, err
	}
	return convertLogs()
}

// convertTraces 将 aegisv2 Payload 转换为 OTel Traces，同时收集 Web Vitals 数据点用于指标派生。
func convertTraces(payload collectPayload, records []d2Record, traceID pcommon.TraceID) (ptrace.Traces, *webVitalsCollector, error) {
	traces := ptrace.NewTraces()
	collector := newWebVitalsCollector()
	if len(records) == 0 {
		return traces, collector, nil
	}

	resourceSpans := traces.ResourceSpans().AppendEmpty()
	resourceAttrs := resourceSpans.Resource().Attributes()
	putCommonResourceAttrs(resourceAttrs, payload, records[0].Fields.Session.ID)
	upsertString(resourceAttrs, "referer", payload.Bean.Referer)

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	putCollectorScope(scopeSpans.Scope(), payload.Bean.Version)

	now := pcommon.NewTimestampFromTime(time.Now())
	if traceID == (pcommon.TraceID{}) {
		traceID = random.TraceID()
	}

	for _, record := range records {
		pageURL := recordPageURL(record)
		if record.Fields.Action.IsValid() {
			appendActionSpan(scopeSpans, traceID, now, payload, record)
		}
		for _, msg := range record.Message {
			event := aegisEvent{record, msg}
			if event.IsWebVitals() {
				appendWebVitalsSpans(scopeSpans, traceID, now, payload, record, msg)
				collector.collect(event, millisToTimestamp(msg.Timestamp, now), pageURL, payload.Bean.NetType)
			} else {
				appendMessageSpan(scopeSpans, traceID, now, payload, record, msg)
			}
		}
	}

	if logger.LoggerLevel() == logger.DebugLevelDesc {
		if b, err := json.Marshal(records); err == nil {
			logger.Debugf("aegisv2/convertTraces: %s", b)
		}
	}
	return traces, collector, nil
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
	putCommonSpanAttrs(actionAttrs, payload, record)
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
	putCommonSpanAttrs(spanAttrs, payload, record)
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
			putAssetSpeedSpanAttrs(spanAttrs, payload, record, msg)
			appendResourceTimingEvents(span.Events(), msg, spanStartTs)
		case EventTypeWebsocket:
			putWebsocketSpanAttrs(spanAttrs, payload, record, msg)
		case EventTypePagePerformance:
			putPagePerformanceSpanAttrs(spanAttrs, record)
			appendPagePerformanceEvents(span.Events(), msg, spanStartTs, spanEndTs)
			appendPagePerformancePhaseEvents(span.Events(), msg, spanStartTs)
		}
		if e.IsError() {
			putSpanException(span, spanAttrs, e.ExceptionType(), firstNonEmptyString(msg.raw, "errorMsg", "msg"))
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

func convertMetrics(payload collectPayload, records []d2Record) (pmetric.Metrics, error) {
	metrics := pmetric.NewMetrics()
	if len(records) == 0 {
		return metrics, nil
	}

	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	resourceAttrs := resourceMetrics.Resource().Attributes()
	putCommonResourceAttrs(resourceAttrs, payload, records[0].Fields.Session.ID)

	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()
	putCollectorScope(scopeMetrics.Scope(), payload.Bean.Version)
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

func convertLogs() (plog.Logs, error) {
	return plog.NewLogs(), errors.Wrap(errMappingNotImplemented, "logs mapping")
}
