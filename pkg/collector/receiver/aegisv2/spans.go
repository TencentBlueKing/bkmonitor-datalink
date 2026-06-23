package aegisv2

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

type BuildErrorLevel string

const (
	BuildErrorRecoverable BuildErrorLevel = "recoverable"
	BuildErrorFatal       BuildErrorLevel = "fatal"
)

type BuildError struct {
	Level BuildErrorLevel
	Err   error
}

func (e *BuildError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Level)
	}
	return string(e.Level) + ": " + e.Err.Error()
}

func (e *BuildError) Unwrap() error { return e.Err }

func NewRecoverableBuildError(err error) error {
	if err == nil {
		return nil
	}
	return &BuildError{Level: BuildErrorRecoverable, Err: err}
}

func NewFatalBuildError(err error) error {
	if err == nil {
		return nil
	}
	return &BuildError{Level: BuildErrorFatal, Err: err}
}

type BuildContext struct {
	ScopeSpans ptrace.ScopeSpans
	TraceID    pcommon.TraceID
	Now        pcommon.Timestamp
	Payload    collectPayload
	Record     d2Record
	Msg        d2Message
	EventType  EventType
}

type SpanBuilder interface {
	Build(ctx *BuildContext) error
	SupportedTypes() []EventType
}

type BuilderRegistry struct {
	builders map[EventType]SpanBuilder
	mu       sync.RWMutex
}

func NewBuilderRegistry() *BuilderRegistry {
	return &BuilderRegistry{builders: make(map[EventType]SpanBuilder)}
}

func (r *BuilderRegistry) Register(builder SpanBuilder) error {
	if isNilSpanBuilder(builder) {
		return errors.New("nil span builder")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, typ := range builder.SupportedTypes() {
		r.builders[typ] = builder
	}
	return nil
}

func (r *BuilderRegistry) Get(typ EventType) (SpanBuilder, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.builders[typ]
	return b, ok
}

type messageSpanBuilder struct{}

func (messageSpanBuilder) SupportedTypes() []EventType {
	return []EventType{EventTypeAPI, EventTypeAssetSpeed, EventTypeCustom, EventTypePagePerformance, EventTypePV, EventTypeSession, EventTypeWebsocket, EventTypeError}
}

func (messageSpanBuilder) Build(ctx *BuildContext) error {
	appendMessageSpan(ctx.ScopeSpans, ctx.TraceID, ctx.Now, ctx.Payload, ctx.Record, ctx.Msg)
	return nil
}

type webVitalsSpanBuilder struct{}

func (webVitalsSpanBuilder) SupportedTypes() []EventType {
	return []EventType{EventTypeWebVitals}
}

func (webVitalsSpanBuilder) Build(ctx *BuildContext) error {
	appendWebVitalsSpans(ctx.ScopeSpans, ctx.TraceID, ctx.Now, ctx.Payload, ctx.Record, ctx.Msg)
	return nil
}

var defaultBuilderRegistry = newDefaultBuilderRegistry()

var builderDegradeCount uint64
var builderUnknownTypeCount uint64
var emptyMessageDropCount uint64

func builderStatsSnapshot() (degradeCount, unknownTypeCount uint64) {
	return atomic.LoadUint64(&builderDegradeCount), atomic.LoadUint64(&builderUnknownTypeCount)
}

func resetBuilderStatsForTest() {
	atomic.StoreUint64(&builderDegradeCount, 0)
	atomic.StoreUint64(&builderUnknownTypeCount, 0)
	atomic.StoreUint64(&emptyMessageDropCount, 0)
}

func emptyMessageDropCountSnapshot() uint64 {
	return atomic.LoadUint64(&emptyMessageDropCount)
}

func recordEmptyMessageDrop() {
	atomic.AddUint64(&emptyMessageDropCount, 1)
}

func newDefaultBuilderRegistry() *BuilderRegistry {
	r := NewBuilderRegistry()
	mustRegisterBuilder(r, messageSpanBuilder{})
	mustRegisterBuilder(r, webVitalsSpanBuilder{})
	return r
}

func buildSpanWithRegistry(registry *BuilderRegistry, ctx *BuildContext) error {
	builder, ok := registry.Get(ctx.EventType)
	if !ok {
		atomic.AddUint64(&builderUnknownTypeCount, 1)
		atomic.AddUint64(&builderDegradeCount, 1)
		appendBuildFallback(ctx)
		return nil
	}

	err := builder.Build(ctx)
	if err == nil {
		return nil
	}

	var bErr *BuildError
	if errors.As(err, &bErr) {
		if bErr.Level == BuildErrorFatal {
			return err
		}
		if bErr.Level == BuildErrorRecoverable {
			atomic.AddUint64(&builderDegradeCount, 1)
			appendBuildFallback(ctx)
			return nil
		}
	}
	return err
}

func appendBuildFallback(ctx *BuildContext) {
	appendMessageSpan(ctx.ScopeSpans, ctx.TraceID, ctx.Now, ctx.Payload, ctx.Record, ctx.Msg)
}

func mustRegisterBuilder(r *BuilderRegistry, builder SpanBuilder) {
	if err := r.Register(builder); err != nil {
		panic(err)
	}
}

func isNilSpanBuilder(builder SpanBuilder) bool {
	if builder == nil {
		return true
	}
	v := reflect.ValueOf(builder)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func appendActionSpan(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record) {
	actionSpan := scopeSpans.Spans().AppendEmpty()
	actionSpan.SetTraceID(traceID)
	actionSpan.SetSpanID(random.SpanID())
	actionSpan.SetKind(ptrace.SpanKindInternal)
	actionSpan.SetName(record.Fields.Action.SpanName())

	actionTs := millisToTimestamp(record.Fields.Action.Timestamp, now)
	actionSpan.SetStartTimestamp(actionTs)
	actionSpan.SetEndTimestamp(actionTs)

	aAttrs := actionSpan.Attributes()
	setCommonSpanAttrs(aAttrs, payload, record)
	upsertString(aAttrs, "event.type", "action")
	upsertString(aAttrs, "event.plugin", "action")
	upsertString(aAttrs, "action.id", record.Fields.Action.ID)
	upsertNonZeroInt(aAttrs, "action.timestamp", record.Fields.Action.Timestamp)
	upsertString(aAttrs, "action.type", record.Fields.Action.ActionType)
	upsertString(aAttrs, "action.name", record.Fields.Action.ActionName)
	upsertString(aAttrs, "action.target_name", record.Fields.Action.ActionTargetName)
	upsertString(aAttrs, "action.source_event_type", record.Fields.Type)
}

// appendMessageSpan 构造一条消息对应的 Span。
// 已识别事件类型按语义填充 span kind 和领域属性；未识别类型退化为通用 SpanEvent。
func appendMessageSpan(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record, msg d2Message) {
	span := scopeSpans.Spans().AppendEmpty()
	span.SetTraceID(traceID)
	span.SetSpanID(random.SpanID())

	e := aegisEvent{record: record, msg: msg}
	span.SetName(e.SpanName())

	spanStartTs, spanEndTs := messageSpanTimeRange(e, msg, now)
	span.SetStartTimestamp(spanStartTs)
	span.SetEndTimestamp(spanEndTs)

	sAttrs := span.Attributes()
	setCommonSpanAttrs(sAttrs, payload, record)
	upsertNonZeroInt(sAttrs, attrEventTimestamp, msg.Timestamp)

	if payload.Bean.Referer != "" {
		link := span.Links().AppendEmpty()
		link.Attributes().UpsertString(attrLink, payload.Bean.Referer)
	}

	if spanKind, ok := e.SpanKind(); ok {
		span.SetKind(spanKind)
		upsertFlattenedMap(sAttrs, attrAegisExtPrefix, msg.raw)
		if e.IsAssetSpeed() {
			setAssetSpeedSpanAttrs(sAttrs, payload, record, msg)
			appendResourceTimingEvents(span.Events(), msg, spanStartTs)
		}
		if e.IsWebsocket() {
			setWebsocketSpanAttrs(sAttrs, payload, record, msg)
		}
		if e.IsPagePerformance() {
			setPagePerformanceSpanAttrs(sAttrs, record)
			appendPagePerformanceEvents(span.Events(), msg, spanStartTs, spanEndTs)
			appendPagePerformancePhaseEvents(span.Events(), msg, spanStartTs)
		}
		if e.IsError() {
			span.Status().SetCode(ptrace.StatusCodeError)
			upsertString(sAttrs, "exception.type", e.ExceptionType())
			if errorMsg := firstNonEmptyString(msg.raw, "errorMsg", "msg"); errorMsg != "" {
				upsertString(sAttrs, "error.message", errorMsg)
				upsertString(sAttrs, "exception.message", errorMsg)
			}
		}
		return
	}

	// 未识别类型：退化为通用事件，保留原始字段，避免信息丢失。
	ev := span.Events().AppendEmpty()
	ev.SetName(spanEventAegisFallback)
	ev.SetTimestamp(spanEndTs)
	upsertFlattenedMap(ev.Attributes(), attrAegisExtPrefix, msg.raw)
}

// messageSpanTimeRange 统一计算消息类 span 的时间范围，避免时间语义分散在多个分支中。
func messageSpanTimeRange(e aegisEvent, msg d2Message, now pcommon.Timestamp) (pcommon.Timestamp, pcommon.Timestamp) {
	if e.IsAssetSpeed() || e.IsAPI() || e.IsWebsocket() {
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

// appendWebVitalsSpans 将一条 web_vitals 消息拆解为每个指标各自独立的 Span。
// 值为 -1 表示客户端未采集，跳过不写。
func appendWebVitalsSpans(scopeSpans ptrace.ScopeSpans, traceID pcommon.TraceID, now pcommon.Timestamp, payload collectPayload, record d2Record, msg d2Message) {
	ts := millisToTimestamp(msg.Timestamp, now)
	gotoID := firstNonEmptyString(msg.raw, "aegisv2_goto")

	pageURL := record.Fields.View.ViewURL
	if pageURL == "" {
		pageURL = record.Fields.From
	}

	for _, v := range webVitalsDefs {
		value, ok := extractFloat64(msg.raw, v.key)
		if !ok || value < 0 {
			continue
		}

		span := scopeSpans.Spans().AppendEmpty()
		span.SetTraceID(traceID)
		span.SetSpanID(random.SpanID())
		span.SetName(spanNameBrowserVital)
		span.SetKind(ptrace.SpanKindInternal)
		span.SetStartTimestamp(ts)
		span.SetEndTimestamp(ts)

		attrs := span.Attributes()
		setCommonSpanAttrs(attrs, payload, record)
		upsertNonZeroInt(attrs, attrEventTimestamp, msg.Timestamp)
		upsertString(attrs, "span_type", "vital")
		upsertString(attrs, "span_subtype", v.metric)
		upsertString(attrs, "event_label", "Web 指标")
		upsertString(attrs, "result", "success")
		upsertString(attrs, "error_type", "none")
		upsertString(attrs, "target_label", v.metric)
		upsertAny(attrs, "target_value", value)
		upsertString(attrs, "vital.metric", v.metric)
		upsertAny(attrs, "vital.value", value)
		if gotoID != "" {
			upsertString(attrs, "vital.id", gotoID+"."+v.metric)
		}
		if pageURL != "" {
			upsertString(attrs, "url.full", pageURL)
			setPageURLAttrs(attrs, pageURL)
		}
		setViewportAttrs(attrs, payload.Bean.VP, "browser.viewport")
		setViewportAttrs(attrs, payload.Bean.SR, "browser.screen")
		if payload.Bean.NetType != "" {
			upsertString(attrs, "network.effective_type", strings.ToLower(payload.Bean.NetType))
		}
	}
}
