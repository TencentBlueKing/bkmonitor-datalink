package aegisv2

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func setCommonSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record) {
	upsertString(attrs, "aegisv2.topic", payload.Topic)
	upsertString(attrs, "aegisv2.scheme", payload.Scheme)
	upsertString(attrs, "http.url", record.Fields.From)
	upsertString(attrs, "session.id", record.Fields.Session.ID)
	upsertString(attrs, "event.type", record.Fields.Type)
	upsertString(attrs, "event.level", record.Fields.Level)
	upsertString(attrs, "event.plugin", record.Fields.Plugin)
	upsertString(attrs, "view.id", record.Fields.View.ID)
	upsertString(attrs, "view.name", record.Fields.View.ViewName)
	upsertString(attrs, "view.loading_type", record.Fields.View.LoadingType)
	upsertString(attrs, "view.url", record.Fields.View.ViewURL)
	upsertString(attrs, "view.referrer", record.Fields.View.Referrer)
}

// setAssetSpeedSpanAttrs 填充静态资源加载（assets_speed）Span 的扩展属性。
func setAssetSpeedSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record, msg d2Message) {
	upsertString(attrs, "span_type", "resource")
	resourceURL := firstNonEmptyString(msg.raw, "url")
	statusCode, hasStatusCode := extractInt64(msg.raw, "status")
	subtype := assetSubtype(firstNonEmptyString(msg.raw, "initiatorType"), firstNonEmptyString(msg.raw, "assetType"))
	upsertString(attrs, "span_subtype", subtype)
	upsertString(attrs, "event_label", "静态资源")

	if durationMs, ok := extractFloat64(msg.raw, msgKeyDuration); ok && durationMs > 0 {
		upsertString(attrs, "duration_bucket", bucketDuration(durationMs))
	}
	if resourceURL != "" {
		upsertString(attrs, "url.full", resourceURL)
		setTargetURLAttrs(attrs, resourceURL)
	}
	if record.Fields.View.ViewURL != "" {
		setPageURLAttrs(attrs, record.Fields.View.ViewURL)
	}
	if hasStatusCode {
		attrs.UpsertInt("http.response.status_code", statusCode)
		upsertString(attrs, "status_class", httpStatusClass(statusCode))
	}
	if transferSize, ok := extractInt64(msg.raw, "transferSize"); ok && transferSize >= 0 {
		attrs.UpsertInt("transfer_size", transferSize)
		attrs.UpsertBool("cache_hit", transferSize == 0)
	}
	if nextHopProtocol := firstNonEmptyString(msg.raw, "nextHopProtocol"); nextHopProtocol != "" {
		upsertString(attrs, "next_hop_protocol", nextHopProtocol)
	}
	if subtype != "" {
		upsertString(attrs, "initiator_type", subtype)
	}
	if encodedBodySize, ok := extractInt64(msg.raw, "encodedBodySize"); ok && encodedBodySize >= 0 {
		attrs.UpsertInt("encoded_body_size", encodedBodySize)
	}
	if decodedBodySize, ok := extractInt64(msg.raw, "decodedBodySize"); ok && decodedBodySize >= 0 {
		attrs.UpsertInt("decoded_body_size", decodedBodySize)
	}
	if payload.Bean.NetType != "" {
		upsertString(attrs, "network.effective_type", payload.Bean.NetType)
	}
	setViewportAttrs(attrs, payload.Bean.VP, "browser.viewport")
	setViewportAttrs(attrs, payload.Bean.SR, "browser.screen")

	if isErr, ok := extractBool(msg.raw, "isErr"); ok && isErr {
		upsertString(attrs, "result", "error")
		upsertString(attrs, "error_type", aegisEvent{record, msg}.ExceptionType())
		return
	}
	if hasStatusCode && statusCode >= 400 {
		upsertString(attrs, "result", "error")
		upsertString(attrs, "error_type", httpStatusClass(statusCode))
		return
	}
	upsertString(attrs, "result", "success")
	upsertString(attrs, "error_type", "none")
}

func setPagePerformanceSpanAttrs(attrs pcommon.Map, record d2Record) {
	upsertString(attrs, "span_type", "document")
	upsertString(attrs, "span_subtype", "navigate")
	upsertString(attrs, "result", "success")
	upsertString(attrs, "error_type", "none")
	upsertString(attrs, "event_label", "文档加载")
	upsertString(attrs, "trace_scene", "page_load")

	pageURL := record.Fields.View.ViewURL
	if pageURL == "" {
		pageURL = record.Fields.From
	}
	if pageURL != "" {
		upsertString(attrs, "url.full", pageURL)
		setPageURLAttrs(attrs, pageURL)
		if targetLabel := pathFromURL(pageURL); targetLabel != "" {
			upsertString(attrs, "target_label", targetLabel)
		}
	}
}

func setWebsocketSpanAttrs(attrs pcommon.Map, payload collectPayload, record d2Record, msg d2Message) {
	event := aegisEvent{record: record, msg: msg}

	upsertString(attrs, "span_type", "network")
	upsertString(attrs, "span_subtype", "websocket")
	upsertString(attrs, "event_label", "WebSocket")
	upsertString(attrs, "trace_scene", "realtime_connection")
	upsertString(attrs, "network.protocol.name", "websocket")

	if durationMs, ok := extractFloat64(msg.raw, msgKeyDuration); ok && durationMs > 0 {
		upsertString(attrs, "duration_bucket", bucketDuration(durationMs))
	}

	if endpointURL := firstNonEmptyString(msg.raw, "url"); endpointURL != "" {
		upsertString(attrs, "url.full", endpointURL)
		setTargetURLAttrs(attrs, endpointURL)
	}

	pageURL := record.Fields.View.ViewURL
	if pageURL == "" {
		pageURL = record.Fields.From
	}
	if pageURL != "" {
		setPageURLAttrs(attrs, pageURL)
	}

	if payload.Bean.NetType != "" {
		upsertString(attrs, "network.effective_type", payload.Bean.NetType)
	}
	setViewportAttrs(attrs, payload.Bean.VP, "browser.viewport")
	setViewportAttrs(attrs, payload.Bean.SR, "browser.screen")

	if success, ok := extractBool(msg.raw, "successFlag"); ok {
		if success {
			upsertString(attrs, "result", "success")
			upsertString(attrs, "error_type", "none")
			return
		}
		upsertString(attrs, "result", "error")
		upsertString(attrs, "error_type", event.ExceptionType())
		return
	}

	if event.IsError() {
		upsertString(attrs, "result", "error")
		upsertString(attrs, "error_type", event.ExceptionType())
		return
	}

	upsertString(attrs, "result", "success")
	upsertString(attrs, "error_type", "none")
}

// appendResourceTimingEvents 将 Resource Timing API 各阶段耗时转换为 SpanEvent。
func appendResourceTimingEvents(events ptrace.SpanEventSlice, msg d2Message, startTs pcommon.Timestamp) {
	var offsetMs float64
	for _, phase := range resourceTimingPhases {
		durationMs, ok := extractFloat64(msg.raw, phase.key)
		if !ok || durationMs <= 0 {
			continue
		}
		ts := pcommon.Timestamp(startTs.AsTime().Add(time.Duration(offsetMs * float64(time.Millisecond))).UnixNano())
		event := events.AppendEmpty()
		event.SetName(phase.name)
		event.SetTimestamp(ts)
		offsetMs += durationMs
	}
}

// appendPagePerformanceEvents 将 W3C Navigation Timing 各里程碑转换为 SpanEvent。
func appendPagePerformanceEvents(events ptrace.SpanEventSlice, msg d2Message, startTs, endTs pcommon.Timestamp) {
	for _, key := range pagePerformanceEventKeys {
		eventTs, ok := pagePerformanceEventTimestamp(msg.raw, key, startTs, endTs)
		if !ok {
			continue
		}
		event := events.AppendEmpty()
		event.SetName(key)
		event.SetTimestamp(eventTs)
	}
}

// appendPagePerformancePhaseEvents 将 aegisv2 页面性能各阶段耗时转换为 SpanEvent。
func appendPagePerformancePhaseEvents(events ptrace.SpanEventSlice, msg d2Message, startTs pcommon.Timestamp) {
	var offsetMs float64
	for _, phase := range pagePerformancePhases {
		durationMs, ok := extractFloat64(msg.raw, phase.key)
		if !ok || durationMs <= 0 {
			continue
		}
		ts := pcommon.Timestamp(startTs.AsTime().Add(time.Duration(offsetMs * float64(time.Millisecond))).UnixNano())
		event := events.AppendEmpty()
		event.SetName(phase.name)
		event.SetTimestamp(ts)
		offsetMs += durationMs
	}
	if msg.FirstScreenTiming > 0 {
		var ts pcommon.Timestamp
		if msg.FirstScreenTiming > absoluteTimestampThresholdMs {
			ts = millisToTimestamp(msg.FirstScreenTiming, startTs)
		} else {
			ts = pcommon.Timestamp(startTs.AsTime().Add(time.Duration(msg.FirstScreenTiming) * time.Millisecond).UnixNano())
		}
		event := events.AppendEmpty()
		event.SetName("firstScreen")
		event.SetTimestamp(ts)
	}
}

func pagePerformanceEventTimestamp(raw map[string]any, key string, startTs, endTs pcommon.Timestamp) (pcommon.Timestamp, bool) {
	value, ok := extractFloat64(raw, key)
	if !ok {
		return 0, false
	}
	// Unix 毫秒时间戳在 2001-09-09 之后会超过该阈值；更小的值按相对导航起点偏移处理。
	if value > float64(absoluteTimestampThresholdMs) {
		return millisToTimestamp(int64(value), endTs), true
	}
	if value < 0 {
		return 0, false
	}
	return pcommon.Timestamp(startTs.AsTime().Add(time.Duration(value * float64(time.Millisecond))).UnixNano()), true
}

func assetSubtype(initiatorType, assetType string) string {
	if initiatorType != "" {
		return initiatorType
	}
	switch strings.ToLower(assetType) {
	case "css":
		return "link"
	case "js", "script":
		return "script"
	case "img", "image":
		return "img"
	default:
		return assetType
	}
}

func bucketDuration(durationMs float64) string {
	switch {
	case durationMs < 100:
		return "<100ms"
	case durationMs < 500:
		return "100-500ms"
	case durationMs < 1000:
		return "500ms-1s"
	default:
		return ">=1s"
	}
}

func httpStatusClass(statusCode int64) string {
	if statusCode <= 0 {
		return ""
	}
	return strconv.FormatInt(statusCode/100, 10) + "xx"
}

func setTargetURLAttrs(attrs pcommon.Map, rawURL string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		upsertString(attrs, "target_label", strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://"))
		return
	}
	if parsed.Host != "" {
		upsertString(attrs, "target_domain", parsed.Host)
	}
	targetPath := parsed.EscapedPath()
	if targetPath == "" {
		targetPath = parsed.Path
	}
	upsertString(attrs, "target_path_template", targetPath)
	targetLabel := parsed.Host + targetPath
	if parsed.RawQuery != "" {
		targetLabel += "?" + parsed.RawQuery
	}
	if targetLabel == "" {
		targetLabel = strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	}
	upsertString(attrs, "target_label", targetLabel)
}

func setPageURLAttrs(attrs pcommon.Map, rawURL string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		if path := fallbackPathFromRawURL(rawURL); path != "" {
			upsertString(attrs, "rum.page.path", path)
			upsertString(attrs, "view.url_path_group", path)
		}
		return
	}
	if parsed.Host != "" {
		upsertString(attrs, "rum.page.host", parsed.Host)
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	if path != "" {
		upsertString(attrs, "rum.page.path", path)
		upsertString(attrs, "view.url_path_group", path)
	}
}

func pathFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fallbackPathFromRawURL(rawURL)
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	return path
}

func fallbackPathFromRawURL(rawURL string) string {
	stripped := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	if idx := strings.IndexByte(stripped, '/'); idx >= 0 {
		return stripped[idx:]
	}
	return ""
}

func setViewportAttrs(attrs pcommon.Map, rawValue, prefix string) {
	parts := strings.FieldsFunc(rawValue, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if len(parts) != 2 {
		return
	}
	width, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	height, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	attrs.UpsertInt(prefix+".width", width)
	attrs.UpsertInt(prefix+".height", height)
}
