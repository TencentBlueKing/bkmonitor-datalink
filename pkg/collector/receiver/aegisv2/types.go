package aegisv2

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

const absoluteTimestampThresholdMs int64 = 1_000_000_000_000

// slice 字面量在热路径上被反复调用
var (
	resourceTimingPhases = []struct{ name, key string }{
		{"preHandle", "preHandleTime"},
		{"dns", "domainLookup"},
		{"tcp", "connectTime"},
		{"tls", "tlsTime"},
		{"wait", "tcpAndRequestGap"},
		{"request", "requestTime"},
		{"response", "responseTime"},
	}

	pagePerformanceEventKeys = []string{
		"fetchStart",
		"unloadEventStart",
		"unloadEventEnd",
		"domInteractive",
		"domContentLoadedEventStart",
		"domContentLoadedEventEnd",
		"domComplete",
		"loadEventStart",
		"loadEventEnd",
		"firstPaint",
		"firstContentfulPaint",
	}

	pagePerformancePhases = []struct{ name, key string }{
		{"dnsLookup", "dnsLookup"},
		{"tcp", "tcp"},
		{"ssl", "ssl"},
		{"ttfb", "ttfb"},
		{"contentDownload", "contentDownload"},
		{"domParse", "domParse"},
		{"resourceDownload", "resourceDownload"},
	}

	webVitalsDefs = []struct{ metric, key string }{
		{"fcp", "FCP"},
		{"lcp", "LCP"},
		{"fid", "FID"},
		{"inp", "INP"},
		{"cls", "CLS"},
	}
)

type collectPayload struct {
	Topic  string          `json:"topic"`
	Bean   clientBean      `json:"bean"`
	Scheme string          `json:"scheme"`
	D2     json.RawMessage `json:"d2"`
	Ext    json.RawMessage `json:"ext"`
}

type clientBean struct {
	Version  string `json:"version"`
	AID      string `json:"aid"`
	Env      string `json:"env"`
	Platform string `json:"platform"`
	NetType  string `json:"netType"`
	VP       string `json:"vp"`
	SR       string `json:"sr"`
	Referer  string `json:"referer"`
}

type d2Record struct {
	Fields  d2Fields    `json:"fields"`
	Message []d2Message `json:"message"`
}

// UnmarshalJSON 自定义解析，兼容「对象」和「JSON 字符串包裹对象」两种格式。
func (r *d2Record) UnmarshalJSON(data []byte) error {
	type plain struct {
		Fields  json.RawMessage   `json:"fields"`
		Message []json.RawMessage `json:"message"`
	}
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if err := unmarshalFlexibleJSON(p.Fields, &r.Fields); err != nil {
		return err
	}
	r.Message = make([]d2Message, 0, len(p.Message))
	for _, raw := range p.Message {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			r.Message = append(r.Message, d2Message{})
			continue
		}
		var msg d2Message
		if err := unmarshalFlexibleJSON(raw, &msg); err != nil {
			return err
		}
		r.Message = append(r.Message, msg)
	}
	return nil
}

type d2Fields struct {
	Level   string      `json:"level"`
	Plugin  string      `json:"plugin"`
	Type    string      `json:"type"`
	From    string      `json:"from"`
	Session sessionInfo `json:"session"`
	View    viewInfo    `json:"view"`
	Action  actionInfo  `json:"action"`
}

type sessionInfo struct {
	ID string `json:"id"`
}

type actionInfo struct {
	ID               string `json:"id"`
	Timestamp        int64  `json:"timestamp"`
	ActionType       string `json:"action_type"`
	ActionName       string `json:"action_name"`
	ActionTargetName string `json:"action_target_name"`
}

func (a actionInfo) IsValid() bool {
	return a.ID != "" || a.ActionType != "" || a.ActionName != "" || a.ActionTargetName != "" || a.Timestamp != 0
}

func (a actionInfo) SpanName() string {
	if a.ActionType != "" {
		return "action." + a.ActionType
	}
	return "action"
}

type viewInfo struct {
	ID          string `json:"id"`
	ViewName    string `json:"view_name"`
	Referrer    string `json:"referrer"`
	LoadingType string `json:"loading_type"`
	ViewURL     string `json:"view_url"`
}

type d2Message struct {
	Msg               string `json:"msg"`
	Timestamp         int64  `json:"timestamp"`
	SessionType       string `json:"session_type"`
	IsActive          bool   `json:"is_active"`
	SessionFrom       string `json:"session_from"`
	AegisV2Goto       string `json:"aegisv2_goto"`
	DNSLookup         int64  `json:"dnsLookup"`
	TCP               int64  `json:"tcp"`
	SSL               int64  `json:"ssl"`
	TTFB              int64  `json:"ttfb"`
	ContentDownload   int64  `json:"contentDownload"`
	DOMParse          int64  `json:"domParse"`
	ResourceDownload  int64  `json:"resourceDownload"`
	FirstScreenTiming int64  `json:"firstScreenTiming"`
	raw               map[string]any
}

func (m *d2Message) UnmarshalJSON(data []byte) error {
	type plain d2Message
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*m = d2Message(p)
	return json.Unmarshal(data, &m.raw)
}

// TimeRange 计算消息的起止时间戳：
// 优先使用 duration 字段，其次累加页面性能各阶段耗时，最后回退到 firstScreenTiming。
// 当 firstScreenTiming 看起来是绝对毫秒时间戳时，将其视为结束时间，而 m.Timestamp 视为开始时间。
func (m d2Message) TimeRange(fallback pcommon.Timestamp) (pcommon.Timestamp, pcommon.Timestamp) {
	endTs := millisToTimestamp(m.Timestamp, fallback)
	durationMs, ok := extractFloat64(m.raw, "duration")
	if !ok || durationMs <= 0 {
		totalMs := max(int64(0), m.DNSLookup) + max(int64(0), m.TCP) + max(int64(0), m.SSL) +
			max(int64(0), m.TTFB) + max(int64(0), m.ContentDownload) +
			max(int64(0), m.DOMParse) + max(int64(0), m.ResourceDownload)
		if totalMs > 0 {
			start := pcommon.Timestamp(endTs.AsTime().Add(-time.Duration(totalMs) * time.Millisecond).UnixNano())
			return start, endTs
		}
		firstScreenTiming, hasFirstScreenTiming := extractFloat64(m.raw, "firstScreenTiming")
		if hasFirstScreenTiming && firstScreenTiming > 0 {
			if firstScreenTiming > float64(absoluteTimestampThresholdMs) {
				firstScreenTs := millisToTimestamp(int64(firstScreenTiming), endTs)
				if firstScreenTs > endTs {
					return endTs, firstScreenTs
				}
				return firstScreenTs, endTs
			}
			durationMs, ok = firstScreenTiming, true
		} else {
			durationMs, ok = 0, false
		}
	}
	if !ok || durationMs <= 0 {
		return endTs, endTs
	}
	start := pcommon.Timestamp(endTs.AsTime().Add(-time.Duration(durationMs * float64(time.Millisecond))).UnixNano())
	return start, endTs
}

// aegisEvent 封装单条 d2Record+d2Message，提供事件分类和 Span 语义映射方法。
type aegisEvent struct {
	record d2Record
	msg    d2Message
}

type EventType string

const (
	EventTypeAPI             EventType = "api"
	EventTypeAssetSpeed      EventType = "assets_speed"
	EventTypeCustom          EventType = "custom_event"
	EventTypePagePerformance EventType = "page_performance"
	EventTypePV              EventType = "pv"
	EventTypeSession         EventType = "session"
	EventTypeWebsocket       EventType = "websocket"
	EventTypeError           EventType = "error"
	EventTypeWebVitals       EventType = "web_vitals"
	EventTypeUnknown         EventType = "unknown"
)

// SpanName 按优先级确定 Span 名称：已知语义类型 → 错误级别 → fields.type → msg → 默认值。
func (e aegisEvent) SpanName() string {
	if e.IsAssetSpeed() {
		return "browser.resource"
	}
	if e.IsPagePerformance() {
		return "documentLoad"
	}
	if e.IsError() && e.record.Fields.Type == "normal" && e.record.Fields.Level != "" {
		return e.record.Fields.Level
	}
	if e.record.Fields.Type != "" {
		return e.record.Fields.Type
	}
	if e.msg.Msg != "" {
		return e.msg.Msg
	}
	return "aegisv2.event"
}

func (e aegisEvent) isTypeOrMessage(typ string) bool {
	return e.record.Fields.Type == typ || e.msg.Msg == typ
}

func (e aegisEvent) IsAssetSpeed() bool {
	return e.record.Fields.Type == "assets_speed" || e.msg.Msg == "asset_speed"
}

func (e aegisEvent) IsAPI() bool { return e.record.Fields.Type == "api" }

func (e aegisEvent) IsCustom() bool {
	return e.isTypeOrMessage("custom_event")
}

func (e aegisEvent) IsPagePerformance() bool {
	return e.isTypeOrMessage("page_performance")
}

func (e aegisEvent) IsPV() bool {
	return e.isTypeOrMessage("pv") || (e.record.Fields.Plugin == "spa" && e.msg.Msg == "spa")
}

func (e aegisEvent) IsSession() bool { return e.isTypeOrMessage("session") }

func (e aegisEvent) IsWebVitals() bool {
	return e.isTypeOrMessage("web_vitals")
}

func (e aegisEvent) IsWebsocket() bool {
	return e.record.Fields.Type == "websocket" || e.record.Fields.Plugin == "websocket"
}

// EventType 返回当前事件的统一分类。
func (e aegisEvent) EventType() EventType {
	switch {
	case e.IsWebVitals():
		return EventTypeWebVitals
	case e.IsAssetSpeed():
		return EventTypeAssetSpeed
	case e.IsAPI():
		return EventTypeAPI
	case e.IsCustom():
		return EventTypeCustom
	case e.IsPagePerformance():
		return EventTypePagePerformance
	case e.IsPV():
		return EventTypePV
	case e.IsWebsocket():
		return EventTypeWebsocket
	case e.IsSession():
		return EventTypeSession
	case e.IsError():
		return EventTypeError
	default:
		return EventTypeUnknown
	}
}

func (e aegisEvent) IsError() bool {
	if e.record.Fields.Plugin == "error" {
		return true
	}
	if isErrorLevel(e.record.Fields.Level) {
		return true
	}
	if e.IsWebsocket() {
		if success, ok := extractBool(e.msg.raw, "successFlag"); ok && !success {
			return true
		}
	}
	if isErr, ok := extractBool(e.msg.raw, "isErr"); ok && isErr {
		return true
	}
	return false
}

// SpanKind 将事件类型映射到 OTel SpanKind。
func (e aegisEvent) SpanKind() (ptrace.SpanKind, bool) {
	switch e.EventType() {
	case EventTypeAssetSpeed, EventTypeAPI, EventTypeWebsocket:
		return ptrace.SpanKindClient, true
	case EventTypeCustom, EventTypePagePerformance, EventTypePV, EventTypeSession, EventTypeError:
		return ptrace.SpanKindInternal, true
	default:
		return ptrace.SpanKindUnspecified, false
	}
}

func (e aegisEvent) ExceptionType() string {
	if level := strings.TrimSpace(e.record.Fields.Level); isErrorLevel(level) {
		return level
	}
	if e.record.Fields.Plugin != "" {
		return e.record.Fields.Plugin
	}
	if e.record.Fields.Level != "" {
		return e.record.Fields.Level
	}
	if e.msg.Msg != "" {
		return e.msg.Msg
	}
	return ""
}

func isErrorLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "fatal", "critical", "panic", "exception", "promise_error":
		return true
	default:
		return false
	}
}
