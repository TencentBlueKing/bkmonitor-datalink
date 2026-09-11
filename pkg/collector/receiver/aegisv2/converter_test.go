package aegisv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestDecodeAegisV2Traces_ObjectPayload(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-1",
		"env": "production",
		"platform": "macOS",
		"netType": "4G",
		"vp": "1512 * 345",
		"sr": "1512 * 982",
		"referer": "http://127.0.0.1:8080/trending.html"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"session\": {\"id\": \"session-1\"}, \"view\": {\"id\": \"view-1\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow\", \"view_url\": \"http://127.0.0.1:8080/index.html\", \"referrer\": \"\"}, \"action\": {\"id\": \"action-1\", \"timestamp\": 1780994775565, \"action_type\": \"click\", \"action_name\": \"测试 fetch\", \"action_target_name\": \"button\"}, \"type\": \"api\", \"level\": \"error\", \"plugin\": \"api\"}",
			"message": [
				"{\"duration\": 182.3, \"msg\": \"url: https://example.com/api\", \"url\": \"https://example.com/api\", \"status\": 200, \"method\": \"GET\", \"isErr\": true, \"requestType\": \"fetch\", \"aegisv2_goto\": \"goto-1\", \"timestamp\": 1780992434065}",
				"{\"duration\": 183.5, \"msg\": \"url: https://example.com/api2\", \"url\": \"https://example.com/api2\", \"status\": 200, \"method\": \"GET\", \"isErr\": true, \"requestType\": \"fetch\", \"aegisv2_goto\": \"goto-2\", \"timestamp\": 1780992434067}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	rs := traces.ResourceSpans().At(0)
	assert.Equal(t, 1, rs.ScopeSpans().Len())
	assert.Equal(t, 3, rs.ScopeSpans().At(0).Spans().Len())
	v, ok := rs.Resource().Attributes().Get("service.name")
	require.True(t, ok)
	assert.Equal(t, defaultServiceName, v.StringVal())
	actionSpan := rs.ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "action.click", actionSpan.Name())
	assert.Equal(t, ptrace.SpanKindInternal, actionSpan.Kind())
	assert.Equal(t, 0, actionSpan.Events().Len())
	v, ok = actionSpan.Attributes().Get("action.id")
	require.True(t, ok)
	assert.Equal(t, "action-1", v.StringVal())
	v, ok = actionSpan.Attributes().Get("action.type")
	require.True(t, ok)
	assert.Equal(t, "click", v.StringVal())
	v, ok = actionSpan.Attributes().Get("action.name")
	require.True(t, ok)
	assert.Equal(t, "测试 fetch", v.StringVal())
	v, ok = actionSpan.Attributes().Get("action.target_name")
	require.True(t, ok)
	assert.Equal(t, "button", v.StringVal())
	v, ok = actionSpan.Attributes().Get("action.source_event_type")
	require.True(t, ok)
	assert.Equal(t, "api", v.StringVal())

	span := rs.ScopeSpans().At(0).Spans().At(1)
	assert.Equal(t, "api", span.Name())
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())

	v, ok = span.Attributes().Get("event.type")
	require.True(t, ok)
	assert.Equal(t, "api", v.StringVal())
	v, ok = span.Attributes().Get("view.loading_type")
	require.True(t, ok)
	assert.Equal(t, "initial_load", v.StringVal())
	_, ok = span.Attributes().Get("action.id")
	assert.False(t, ok)
	v, ok = span.Attributes().Get("aegisv2.ext.url")
	require.True(t, ok)
	assert.Equal(t, "https://example.com/api", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.status")
	require.True(t, ok)
	assert.EqualValues(t, 200, v.IntVal())
	v, ok = span.Attributes().Get("aegisv2.ext.duration")
	require.True(t, ok)
	assert.Equal(t, 182.3, v.DoubleVal())
	v, ok = span.Attributes().Get("aegisv2.ext.method")
	require.True(t, ok)
	assert.Equal(t, "GET", v.StringVal())
	v, ok = span.Attributes().Get("error.message")
	require.True(t, ok)
	assert.Equal(t, "url: https://example.com/api", v.StringVal())
	v, ok = span.Attributes().Get("exception.type")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("exception.message")
	require.True(t, ok)
	assert.Equal(t, "url: https://example.com/api", v.StringVal())
	// api: timestamp 是请求起始时间，endTs = startTs + duration（182.3ms → 182ms）
	assert.Equal(t, int64(1780992434065), span.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, int64(1780992434065+182), span.EndTimestamp().AsTime().UnixMilli())
	assert.True(t, span.EndTimestamp() > span.StartTimestamp())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1780992434065, v.IntVal())
	assert.Equal(t, actionSpan.TraceID(), span.TraceID())
	assert.Equal(t, span.TraceID(), rs.ScopeSpans().At(0).Spans().At(2).TraceID())
}

func TestDecodeAegisV2Traces_APINoDurationFallbackToInstant(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"session\": {\"id\": \"session-1\"}, \"type\": \"api\", \"level\": \"info\", \"plugin\": \"api\"}",
			"message": [
				"{\"msg\": \"url: https://example.com/without-duration\", \"url\": \"https://example.com/without-duration\", \"status\": 200, \"method\": \"GET\", \"timestamp\": 1781992434000}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, int64(1781992434000), span.StartTimestamp().AsTime().UnixMilli())

	v, ok := span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1781992434000, v.IntVal())
}

func TestDecodeAegisV2Traces_PVPayload(t *testing.T) {

	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"referer": "http://localhost:8080/entry.html"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://localhost:8080/lab.html\", \"session\": {\"id\": \"6aa41df8aff21ffb4472c1ebfd28a4df\"}, \"view\": {\"id\": \"9ab3140153ac2cab\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow - 实验页\", \"view_url\": \"http://localhost:8080/lab.html\", \"referrer\": \"\"}, \"type\": \"pv\", \"level\": \"info\", \"plugin\": \"spa\"}",
			"message": [
				"{\"msg\": \"spa\", \"aegisv2_goto\": \"bea28aa54671e581\", \"timestamp\": 1781754057008}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "pv", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, int64(1781754057008), span.StartTimestamp().AsTime().UnixMilli())

	v, ok := span.Attributes().Get("event.type")
	require.True(t, ok)
	assert.Equal(t, "pv", v.StringVal())
	v, ok = span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "spa", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.msg")
	require.True(t, ok)
	assert.Equal(t, "spa", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.aegisv2_goto")
	require.True(t, ok)
	assert.Equal(t, "bea28aa54671e581", v.StringVal())
	v, ok = span.Attributes().Get("view.url")
	require.True(t, ok)
	assert.Equal(t, "http://localhost:8080/lab.html", v.StringVal())
	assert.Equal(t, 1, span.Links().Len())
	v, ok = span.Links().At(0).Attributes().Get(attrLink)
	require.True(t, ok)
	assert.Equal(t, "http://localhost:8080/entry.html", v.StringVal())

}

func TestDecodeAegisV2Traces_ErrorPayload(t *testing.T) {

	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://localhost:8080/lab.html\", \"session\": {\"id\": \"6aa41df8aff21ffb4472c1ebfd28a4df\"}, \"view\": {\"id\": \"9ab3140153ac2cab\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow - 实验页\", \"view_url\": \"http://localhost:8080/lab.html\", \"referrer\": \"\"}, \"type\": \"normal\", \"level\": \"error\", \"plugin\": \"error\"}",
			"message": [
				"{\"msg\": \"Script error. @ (:0:0)\\n          \\n\", \"errorMsg\": \"Script error.\", \"aegisv2_goto\": \"77f5090941c43972\", \"timestamp\": 1781754057105}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "error", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, int64(1781754057105), span.StartTimestamp().AsTime().UnixMilli())

	v, ok := span.Attributes().Get("event.type")
	require.True(t, ok)
	assert.Equal(t, "normal", v.StringVal())
	v, ok = span.Attributes().Get("event.level")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("error.message")
	require.True(t, ok)
	assert.Equal(t, "Script error.", v.StringVal())
	v, ok = span.Attributes().Get("exception.type")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("exception.message")
	require.True(t, ok)
	assert.Equal(t, "Script error.", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.errorMsg")
	require.True(t, ok)
	assert.Equal(t, "Script error.", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.msg")
	require.True(t, ok)
	assert.Equal(t, "Script error. @ (:0:0)\n          \n", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.aegisv2_goto")
	require.True(t, ok)
	assert.Equal(t, "77f5090941c43972", v.StringVal())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1781754057105, v.IntVal())

}

func TestDecodeAegisV2Traces_CustomEventPayload(t *testing.T) {

	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://localhost:8080/lab.html\", \"session\": {\"id\": \"6aa41df8aff21ffb4472c1ebfd28a4df\"}, \"view\": {\"id\": \"9ab3140153ac2cab\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow - 实验页\", \"view_url\": \"http://localhost:8080/lab.html\", \"referrer\": \"\"}, \"type\": \"custom_event\", \"level\": \"info\", \"plugin\": \"custom\"}",
			"message": [
				"{\"name\": \"demo_page_enter\", \"ext1\": \"lab\", \"ext2\": \"http://localhost:8080/lab.html\", \"ext3\": \"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like \", \"msg\": \"\", \"aegisv2_goto\": \"36e5ddc588b6e0e3\", \"timestamp\": 1781754057004}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "custom_event", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, int64(1781754057004), span.StartTimestamp().AsTime().UnixMilli())

	v, ok := span.Attributes().Get("event.type")
	require.True(t, ok)
	assert.Equal(t, "custom_event", v.StringVal())
	v, ok = span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "custom", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.name")
	require.True(t, ok)
	assert.Equal(t, "demo_page_enter", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.ext1")
	require.True(t, ok)
	assert.Equal(t, "lab", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.ext2")
	require.True(t, ok)
	assert.Equal(t, "http://localhost:8080/lab.html", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.ext3")
	require.True(t, ok)
	assert.Equal(t, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like ", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.aegisv2_goto")
	require.True(t, ok)
	assert.Equal(t, "36e5ddc588b6e0e3", v.StringVal())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1781754057004, v.IntVal())

}

func TestDecodeAegisV2Traces_WebsocketPayload(t *testing.T) {

	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://localhost:8080/lab.html\", \"session\": {\"id\": \"059a88ea18880c3d439fa5b64222a3b9\"}, \"view\": {\"id\": \"410921270af93222\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow - 实验页\", \"view_url\": \"http://localhost:8080/lab.html\", \"referrer\": \"\"}, \"type\": \"websocket\", \"level\": \"info\", \"plugin\": \"websocket\"}",
			"message": [
				"{\"isTrusted\": true, \"msg\": \"WebSocket connection failed\", \"url\": \"wss://echo.websocket.events\", \"successFlag\": false, \"retryFlag\": false, \"aegisv2_goto\": \"156bd5bedc31161f\", \"timestamp\": 1781766492492}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "websocket", span.Name())
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, int64(1781766492492), span.StartTimestamp().AsTime().UnixMilli())

	v, ok := span.Attributes().Get("event.type")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.url")
	require.True(t, ok)
	assert.Equal(t, "wss://echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("span_type")
	require.True(t, ok)
	assert.Equal(t, "network", v.StringVal())
	v, ok = span.Attributes().Get("span_subtype")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("result")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("error_type")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("network.protocol.name")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("url.full")
	require.True(t, ok)
	assert.Equal(t, "wss://echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("target_domain")
	require.True(t, ok)
	assert.Equal(t, "echo.websocket.events", v.StringVal())
	_, ok = span.Attributes().Get("target_path_template")
	assert.False(t, ok)
	v, ok = span.Attributes().Get("target_label")
	require.True(t, ok)
	assert.Equal(t, "echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.successFlag")
	require.True(t, ok)
	assert.False(t, v.BoolVal())
	v, ok = span.Attributes().Get("error.message")
	require.True(t, ok)
	assert.Equal(t, "WebSocket connection failed", v.StringVal())
	v, ok = span.Attributes().Get("exception.type")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("exception.message")
	require.True(t, ok)
	assert.Equal(t, "WebSocket connection failed", v.StringVal())

}

func TestDecodeAegisV2Traces_WebsocketReplayPayload(t *testing.T) {

	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"referer": "http://localhost:8080/entry.html"
	},
	"d2": [
		{
			"fields": "{\"from\":\"http://localhost:8080/lab.html\",\"session\":{\"id\":\"059a88ea18880c3d439fa5b64222a3b9\"},\"view\":{\"id\":\"410921270af93222\",\"loading_type\":\"initial_load\",\"view_name\":\"VideoFlow - 实验页\",\"view_url\":\"http://localhost:8080/lab.html\",\"referrer\":\"\"},\"type\":\"websocket\",\"level\":\"info\",\"plugin\":\"websocket\"}",
			"message": [
				"{\"isTrusted\":true,\"msg\":\"WebSocket connection failed\",\"url\":\"wss://echo.websocket.events\",\"successFlag\":false,\"retryFlag\":false,\"duration\":312.6,\"aegisv2_goto\":\"156bd5bedc31161f\",\"timestamp\":1781766492492}"
			]
		}
	]
}`)

	traceID := random.TraceID()
	traces, err := decodeTracesWithTraceID(buf, traceID)
	require.NoError(t, err)

	rss := traces.ResourceSpans()
	require.Equal(t, 1, rss.Len())
	rs := rss.At(0)
	spans := rs.ScopeSpans().At(0).Spans()
	require.Equal(t, 1, spans.Len())

	span := spans.At(0)
	assert.Equal(t, traceID, span.TraceID())
	assert.Equal(t, "websocket", span.Name())
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
	assert.Equal(t, 0, span.Events().Len())
	assert.Equal(t, int64(1781766492492), span.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, int64(1781766492804), span.EndTimestamp().AsTime().UnixMilli())
	assert.True(t, span.EndTimestamp() > span.StartTimestamp())

	v, ok := rs.Resource().Attributes().Get("referer")
	require.True(t, ok)
	assert.Equal(t, "http://localhost:8080/entry.html", v.StringVal())
	v, ok = span.Attributes().Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "059a88ea18880c3d439fa5b64222a3b9", v.StringVal())
	v, ok = span.Attributes().Get("span_type")
	require.True(t, ok)
	assert.Equal(t, "network", v.StringVal())
	v, ok = span.Attributes().Get("span_subtype")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("trace_scene")
	require.True(t, ok)
	assert.Equal(t, "realtime_connection", v.StringVal())
	v, ok = span.Attributes().Get("duration_bucket")
	require.True(t, ok)
	assert.Equal(t, "100-500ms", v.StringVal())
	v, ok = span.Attributes().Get("result")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("error_type")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("network.protocol.name")
	require.True(t, ok)
	assert.Equal(t, "websocket", v.StringVal())
	v, ok = span.Attributes().Get("view.name")
	require.True(t, ok)
	assert.Equal(t, "VideoFlow - 实验页", v.StringVal())
	v, ok = span.Attributes().Get("url.full")
	require.True(t, ok)
	assert.Equal(t, "wss://echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("target_domain")
	require.True(t, ok)
	assert.Equal(t, "echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("target_label")
	require.True(t, ok)
	assert.Equal(t, "echo.websocket.events", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.retryFlag")
	require.True(t, ok)
	assert.False(t, v.BoolVal())
	v, ok = span.Attributes().Get("aegisv2.ext.duration")
	require.True(t, ok)
	assert.Equal(t, 312.6, v.DoubleVal())
	v, ok = span.Attributes().Get("error.message")
	require.True(t, ok)
	assert.Equal(t, "WebSocket connection failed", v.StringVal())

	assert.Equal(t, 1, span.Links().Len())
	v, ok = span.Links().At(0).Attributes().Get(attrLink)
	require.True(t, ok)
	assert.Equal(t, "http://localhost:8080/entry.html", v.StringVal())

}

func TestDecodeAegisV2Traces_StringifiedPayload(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\":\"http://127.0.0.1:8080/index.html\",\"type\":\"page_performance\",\"level\":\"info\",\"plugin\":\"pagePerformance\",\"session\":{\"id\":\"session-1\"},\"view\":{\"id\":\"view-1\",\"loading_type\":\"initial_load\",\"view_name\":\"VideoFlow\",\"view_url\":\"http://127.0.0.1:8080/index.html\",\"referrer\":\"\"}}",
			"message": [
				"{\"msg\":\"page_performance\",\"dnsLookup\":0,\"tcp\":0,\"ssl\":0,\"ttfb\":2,\"contentDownload\":1,\"domParse\":209,\"resourceDownload\":88,\"firstScreenTiming\":429,\"fetchStart\":0,\"domInteractive\":377,\"domContentLoadedEventStart\":378,\"domContentLoadedEventEnd\":380,\"domComplete\":426,\"loadEventStart\":428,\"loadEventEnd\":429,\"firstPaint\":382,\"firstContentfulPaint\":382,\"aegisv2_goto\":\"goto-1\",\"timestamp\":1780992436877}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "documentLoad", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	// 9 个 W3C Navigation Timing 事件 + 5 个阶段耗时事件（ttfb/contentDownload/domParse/resourceDownload/firstScreen）
	assert.Equal(t, 14, span.Events().Len())
	v, ok := span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "pagePerformance", v.StringVal())
	v, ok = span.Attributes().Get("span_type")
	require.True(t, ok)
	assert.Equal(t, "document", v.StringVal())
	v, ok = span.Attributes().Get("span_subtype")
	require.True(t, ok)
	assert.Equal(t, "navigate", v.StringVal())
	v, ok = span.Attributes().Get("result")
	require.True(t, ok)
	assert.Equal(t, "success", v.StringVal())
	v, ok = span.Attributes().Get("error_type")
	require.True(t, ok)
	assert.Equal(t, "none", v.StringVal())
	v, ok = span.Attributes().Get("event_label")
	require.True(t, ok)
	assert.Equal(t, "文档加载", v.StringVal())
	v, ok = span.Attributes().Get("trace_scene")
	require.True(t, ok)
	assert.Equal(t, "page_load", v.StringVal())
	v, ok = span.Attributes().Get("url.full")
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:8080/index.html", v.StringVal())
	v, ok = span.Attributes().Get("rum.page.host")
	require.True(t, ok)
	assert.Equal(t, "127.0.0.1:8080", v.StringVal())
	v, ok = span.Attributes().Get("rum.page.path")
	require.True(t, ok)
	assert.Equal(t, "/index.html", v.StringVal())
	v, ok = span.Attributes().Get("target_label")
	require.True(t, ok)
	assert.Equal(t, "/index.html", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.firstScreenTiming")
	require.True(t, ok)
	assert.EqualValues(t, 429, v.IntVal())
	v, ok = span.Attributes().Get("aegisv2.ext.ttfb")
	require.True(t, ok)
	assert.EqualValues(t, 2, v.IntVal())
	assert.Equal(t, "fetchStart", span.Events().At(0).Name())
	assert.Equal(t, span.StartTimestamp(), span.Events().At(0).Timestamp())
	assert.Equal(t, "loadEventEnd", span.Events().At(6).Name())
	// span 总时长为各阶段耗时之和（300ms），loadEventEnd 偏移量（429ms）超出 span 范围，
	// 只断言事件时间戳晚于 span 起始点。
	assert.True(t, span.Events().At(6).Timestamp() > span.StartTimestamp())
	assert.Equal(t, "firstPaint", span.Events().At(7).Name())
	assert.Equal(t, "firstContentfulPaint", span.Events().At(8).Name())
	assert.True(t, span.EndTimestamp() > span.StartTimestamp())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1780992436877, v.IntVal()) // page_performance: timestamp == endTs
	assert.NotEqual(t, span.TraceID().HexString(), "")
	assert.NotEqual(t, span.SpanID().HexString(), "")
}

func TestDecodeAegisV2Traces_AssetSpeedBecomesStandaloneSpan(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"netType": "4g",
		"vp": "1512x287",
		"sr": "1512x982"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"session\": {\"id\": \"session-1\"}, \"view\": {\"id\": \"view-1\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow\", \"view_url\": \"http://127.0.0.1:8080/index.html\", \"referrer\": \"\"}, \"type\": \"assets_speed\", \"level\": \"info\", \"plugin\": \"assetSpeed\"}",
			"message": [
				"{\"msg\": \"asset_speed\", \"url\": \"http://127.0.0.1:8080/styles.css\", \"status\": 200, \"assetType\": \"css\", \"isHttps\": false, \"nextHopProtocol\": \"h2\", \"urlQuery\": \"\", \"transferSize\": 0, \"method\": \"get\", \"preHandleTime\": 0, \"duration\": 47.3, \"domainLookup\": 0, \"connectTime\": 0, \"tlsTime\": 139.4, \"tcpAndRequestGap\": 42.2, \"requestTime\": 1.4, \"responseTime\": 3.7, \"aegisv2_goto\": \"c773d7036abaab38\", \"timestamp\": 1781593481995}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	// dns=0, tcp=0 跳过; tls=139.4, wait=42.2, request=1.4, response=3.7 → 4 个事件
	assert.Equal(t, 4, span.Events().Len())
	assert.Equal(t, "browser.resource", span.Name())

	v, ok := span.Attributes().Get("aegisv2.ext.url")
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:8080/styles.css", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.status")
	require.True(t, ok)
	assert.EqualValues(t, 200, v.IntVal())
	v, ok = span.Attributes().Get("aegisv2.ext.duration")
	require.True(t, ok)
	assert.Equal(t, 47.3, v.DoubleVal())
	v, ok = span.Attributes().Get("span_type")
	require.True(t, ok)
	assert.Equal(t, "resource", v.StringVal())
	v, ok = span.Attributes().Get("span_subtype")
	require.True(t, ok)
	assert.Equal(t, "link", v.StringVal())
	v, ok = span.Attributes().Get("result")
	require.True(t, ok)
	assert.Equal(t, "success", v.StringVal())
	v, ok = span.Attributes().Get("error_type")
	require.True(t, ok)
	assert.Equal(t, "none", v.StringVal())
	v, ok = span.Attributes().Get("duration_bucket")
	require.True(t, ok)
	assert.Equal(t, "<100ms", v.StringVal())
	v, ok = span.Attributes().Get("event_label")
	require.True(t, ok)
	assert.Equal(t, "静态资源", v.StringVal())
	v, ok = span.Attributes().Get("target_domain")
	require.True(t, ok)
	assert.Equal(t, "127.0.0.1:8080", v.StringVal())
	v, ok = span.Attributes().Get("target_path_template")
	require.True(t, ok)
	assert.Equal(t, "/styles.css", v.StringVal())
	v, ok = span.Attributes().Get("target_label")
	require.True(t, ok)
	assert.Equal(t, "127.0.0.1:8080/styles.css", v.StringVal())
	v, ok = span.Attributes().Get("http.response.status_code")
	require.True(t, ok)
	assert.EqualValues(t, 200, v.IntVal())
	v, ok = span.Attributes().Get("status_class")
	require.True(t, ok)
	assert.Equal(t, "2xx", v.StringVal())
	v, ok = span.Attributes().Get("url.full")
	require.True(t, ok)
	assert.Equal(t, "http://127.0.0.1:8080/styles.css", v.StringVal())
	v, ok = span.Attributes().Get("transfer_size")
	require.True(t, ok)
	assert.EqualValues(t, 0, v.IntVal())
	v, ok = span.Attributes().Get("cache_hit")
	require.True(t, ok)
	assert.True(t, v.BoolVal())
	v, ok = span.Attributes().Get("next_hop_protocol")
	require.True(t, ok)
	assert.Equal(t, "h2", v.StringVal())
	v, ok = span.Attributes().Get("initiator_type")
	require.True(t, ok)
	assert.Equal(t, "link", v.StringVal())
	v, ok = span.Attributes().Get("network.effective_type")
	require.True(t, ok)
	assert.Equal(t, "4g", v.StringVal())
	v, ok = span.Attributes().Get("browser.viewport.width")
	require.True(t, ok)
	assert.EqualValues(t, 1512, v.IntVal())
	v, ok = span.Attributes().Get("browser.viewport.height")
	require.True(t, ok)
	assert.EqualValues(t, 287, v.IntVal())
	v, ok = span.Attributes().Get("browser.screen.width")
	require.True(t, ok)
	assert.EqualValues(t, 1512, v.IntVal())
	v, ok = span.Attributes().Get("browser.screen.height")
	require.True(t, ok)
	assert.EqualValues(t, 982, v.IntVal())
	v, ok = span.Attributes().Get("rum.page.host")
	require.True(t, ok)
	assert.Equal(t, "127.0.0.1:8080", v.StringVal())
	v, ok = span.Attributes().Get("rum.page.path")
	require.True(t, ok)
	assert.Equal(t, "/index.html", v.StringVal())
	v, ok = span.Attributes().Get("view.url_path_group")
	require.True(t, ok)
	assert.Equal(t, "/index.html", v.StringVal())
	// asset_speed: timestamp 是起始时间，endTs = startTs + duration
	assert.Equal(t, int64(1781593481995), span.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, int64(1781593481995+47), span.EndTimestamp().AsTime().UnixMilli()) // 47.3ms 截断为 47ms
	assert.True(t, span.EndTimestamp() > span.StartTimestamp())
	// Resource Timing 各阶段事件（dns=0/tcp=0 跳过，从 tls 开始累加偏移）
	assert.Equal(t, "tls", span.Events().At(0).Name())
	assert.Equal(t, int64(1781593481995), span.Events().At(0).Timestamp().AsTime().UnixMilli()) // offset=0
	assert.Equal(t, "wait", span.Events().At(1).Name())
	assert.Equal(t, int64(1781593481995+139), span.Events().At(1).Timestamp().AsTime().UnixMilli()) // +139.4ms
	assert.Equal(t, "request", span.Events().At(2).Name())
	assert.Equal(t, int64(1781593481995+139+42), span.Events().At(2).Timestamp().AsTime().UnixMilli()) // +42.2ms
	assert.Equal(t, "response", span.Events().At(3).Name())
	assert.True(t, span.Events().At(3).Timestamp() > span.Events().At(2).Timestamp())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1781593481995, v.IntVal()) // asset_speed: timestamp == startTs
}

func TestDecodeAegisV2Traces_SessionBecomesStandaloneSpan(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"session\": {\"id\": \"ec763eeb7207bc0b49bd29b4b8a6cab7\"}, \"type\": \"session\", \"level\": \"info\", \"plugin\": \"session\"}",
			"message": [
				"{\"session_type\": \"session\", \"is_active\": true, \"session_from\": \"local_generate\", \"msg\": \"session\", \"aegisv2_goto\": \"8bd4a5ea56801604\", \"timestamp\": 1780992433875}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "session", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.Equal(t, 0, span.Events().Len())

	v, ok := span.Attributes().Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "ec763eeb7207bc0b49bd29b4b8a6cab7", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.session_type")
	require.True(t, ok)
	assert.Equal(t, "session", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.is_active")
	require.True(t, ok)
	assert.True(t, v.BoolVal())
	v, ok = span.Attributes().Get("aegisv2.ext.session_from")
	require.True(t, ok)
	assert.Equal(t, "local_generate", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.aegisv2_goto")
	require.True(t, ok)
	assert.Equal(t, "8bd4a5ea56801604", v.StringVal())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1780992433875, v.IntVal())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
}

func TestDecodeAegisV2Traces_PromiseErrorBecomesErrorSpan(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"type\": \"normal\", \"level\": \"promise_error\", \"plugin\": \"error\"}",
			"message": [
				"{\"msg\": \"PROMISE_ERROR: Error.message: func sseError not found\", \"errorMsg\": \"func sseError not found\", \"aegisv2_goto\": \"8a75c2d530087cfe\", \"timestamp\": 1780993960686}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "promise_error", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
	assert.Equal(t, 0, span.Events().Len())

	v, ok := span.Attributes().Get("event.plugin")
	require.True(t, ok)
	assert.Equal(t, "error", v.StringVal())
	v, ok = span.Attributes().Get("error.message")
	require.True(t, ok)
	assert.Equal(t, "func sseError not found", v.StringVal())
	v, ok = span.Attributes().Get("exception.type")
	require.True(t, ok)
	assert.Equal(t, "promise_error", v.StringVal())
	v, ok = span.Attributes().Get("exception.message")
	require.True(t, ok)
	assert.Equal(t, "func sseError not found", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.msg")
	require.True(t, ok)
	assert.Equal(t, "PROMISE_ERROR: Error.message: func sseError not found", v.StringVal())
	v, ok = span.Attributes().Get("aegisv2.ext.aegisv2_goto")
	require.True(t, ok)
	assert.Equal(t, "8a75c2d530087cfe", v.StringVal())
	v, ok = span.Attributes().Get("event.timestamp")
	require.True(t, ok)
	assert.EqualValues(t, 1780993960686, v.IntVal())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
}

func TestDecodeAegisV2Traces_PagePerformancePhaseTimings(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"from\": \"http://127.0.0.1:8080/index.html\", \"type\": \"page_performance\", \"level\": \"info\", \"plugin\": \"pagePerformance\", \"session\": {\"id\": \"session-1\"}, \"view\": {\"id\": \"view-1\", \"loading_type\": \"initial_load\", \"view_name\": \"VideoFlow\", \"view_url\": \"http://127.0.0.1:8080/index.html\", \"referrer\": \"\"}}",
			"message": [
				"{\"msg\": \"page_performance\", \"dnsLookup\": 0, \"tcp\": 0, \"ssl\": 0, \"ttfb\": 52, \"contentDownload\": 1, \"domParse\": 517, \"resourceDownload\": 181, \"firstScreenTiming\": 912, \"aegisv2_goto\": \"8bd5cb98427bdea1\", \"timestamp\": 1781685646787}"
			]
		}
	]
}`)

	traces, err := decodeTraces(buf)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, "documentLoad", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())

	// TimeRange: totalMs = 52+1+517+181 = 751ms; dnsLookup/tcp/ssl 均为 0 跳过
	// W3C 字段均缺失 → appendPagePerformanceEvents 产生 0 个事件
	// appendPagePerformancePhaseEvents 产生 5 个事件：ttfb/contentDownload/domParse/resourceDownload/firstScreen
	assert.Equal(t, 5, span.Events().Len())

	// span 时间范围：endTs = timestamp(ms)，startTs = endTs - 751ms
	assert.Equal(t, int64(1781685646787), span.EndTimestamp().AsTime().UnixMilli())
	assert.Equal(t, int64(1781685646787-751), span.StartTimestamp().AsTime().UnixMilli())

	// 阶段事件顺序与时间戳（累加偏移）
	assert.Equal(t, "ttfb", span.Events().At(0).Name())
	assert.Equal(t, span.StartTimestamp(), span.Events().At(0).Timestamp()) // offset=0ms

	assert.Equal(t, "contentDownload", span.Events().At(1).Name())
	assert.Equal(t, int64(1781685646787-751+52), span.Events().At(1).Timestamp().AsTime().UnixMilli())

	assert.Equal(t, "domParse", span.Events().At(2).Name())
	assert.Equal(t, int64(1781685646787-751+53), span.Events().At(2).Timestamp().AsTime().UnixMilli())

	assert.Equal(t, "resourceDownload", span.Events().At(3).Name())
	assert.Equal(t, int64(1781685646787-751+570), span.Events().At(3).Timestamp().AsTime().UnixMilli())

	// firstScreen 是绝对里程碑，时间戳 = startTs + 912ms（不依赖累加偏移）
	assert.Equal(t, "firstScreen", span.Events().At(4).Name())
	assert.Equal(t, int64(1781685646787-751+912), span.Events().At(4).Timestamp().AsTime().UnixMilli())

	// aegisv2.ext 中保留原始字段
	v, ok := span.Attributes().Get("aegisv2.ext.ttfb")
	require.True(t, ok)
	assert.EqualValues(t, 52, v.IntVal())
	v, ok = span.Attributes().Get("aegisv2.ext.firstScreenTiming")
	require.True(t, ok)
	assert.EqualValues(t, 912, v.IntVal())
	v, ok = span.Attributes().Get("aegisv2.ext.dnsLookup")
	require.True(t, ok)
	assert.EqualValues(t, 0, v.IntVal())
}

func TestDecodeAegisV2Traces_UsesRequestTraceID(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"type\": \"session\", \"plugin\": \"session\"}",
			"message": [
				"{\"msg\": \"session\", \"timestamp\": 1780992433875}"
			]
		}
	]
}`)

	requestTraceID := random.TraceID()
	traces, err := decodeTracesWithTraceID(buf, requestTraceID)
	require.NoError(t, err)

	span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	assert.Equal(t, requestTraceID, span.TraceID())
}

func TestDecodeAegisV2Metrics_WebVitals(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-metrics-1",
		"env": "production",
		"platform": "macOS",
		"netType": "4G",
		"vp": "1512 * 315",
		"sr": "1512 * 982",
		"referer": ""
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://example.com/index.html\", \"session\": {\"id\": \"session-wv-1\"}, \"view\": {\"id\": \"view-wv-1\", \"loading_type\": \"initial_load\", \"view_name\": \"Test Page\", \"view_url\": \"https://example.com/index.html\", \"referrer\": \"\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 452, \"LCP\": 452, \"FID\": -1, \"CLS\": 0.467, \"INP\": -1, \"aegisv2_goto\": \"abc123\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`)

	metrics, err := decodeMetrics(buf)
	require.NoError(t, err)

	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	rm := metrics.ResourceMetrics().At(0)

	// 资源属性
	rAttrs := rm.Resource().Attributes()
	aid, _ := rAttrs.Get("aid")
	assert.Equal(t, "aid-metrics-1", aid.AsString())

	require.Equal(t, 1, rm.ScopeMetrics().Len())
	sm := rm.ScopeMetrics().At(0)
	assert.Equal(t, "aegisv2.collect", sm.Scope().Name())

	// 单一 Histogram 指标，包含所有有效的 web_vitals
	assert.Equal(t, 1, sm.Metrics().Len())
	m := sm.Metrics().At(0)
	assert.Equal(t, "browser.web_vital.duration", m.Name())
	assert.Equal(t, "ms", m.Unit())

	// FID=-1 和 INP=-1 跳过，预期 FCP、LCP、CLS 三个数据点
	hist := m.Histogram()
	assert.Equal(t, 2, hist.DataPoints().Len())

	// 验证数据点
	metricSet := make(map[string]bool)
	metricDP := make(map[string]int)
	for i := 0; i < hist.DataPoints().Len(); i++ {
		dp := hist.DataPoints().At(i)
		metric, _ := dp.Attributes().Get("vital.metric")
		metricSet[metric.AsString()] = true
		metricDP[metric.AsString()] = i
		assert.Equal(t, uint64(1), dp.Count())
	}
	assert.True(t, metricSet["fcp"])
	assert.True(t, metricSet["lcp"])
	assert.False(t, metricSet["cls"])
	assert.False(t, metricSet["fid"])
	assert.False(t, metricSet["inp"])

	// LCP=452ms 应落在 <500 的桶（Prometheus 累计桶语义下对应 le="500" 桶及以上都会累加）。
	lcpIdx, ok := metricDP["lcp"]
	require.True(t, ok)
	lcpDP := hist.DataPoints().At(lcpIdx)
	bounds := lcpDP.MExplicitBounds()
	assert.Equal(t, histogramBoundsMS, bounds)
	bucketCounts := lcpDP.MBucketCounts()
	require.Len(t, bucketCounts, len(bounds)+1)

	lcpBucketIdx := findBucketIndex(452, bounds)
	assert.Equal(t, 8, lcpBucketIdx)
	for i := range bucketCounts {
		if i == lcpBucketIdx {
			assert.Equal(t, uint64(1), bucketCounts[i])
		} else {
			assert.Equal(t, uint64(0), bucketCounts[i])
		}
	}
}

func TestDecodeAegisV2Metrics_WebVitalsDataPointAttrs(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "aid-dp-1",
		"netType": "WiFi"
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://example.com/\", \"session\": {\"id\": \"sess-dp\"}, \"view\": {\"id\": \"view-dp\", \"view_name\": \"Home\", \"view_url\": \"https://example.com/\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 300, \"LCP\": -1, \"FID\": -1, \"CLS\": -1, \"INP\": -1, \"timestamp\": 1782214911075}"
			]
		}
	]
}`)

	metrics, err := decodeMetrics(buf)
	require.NoError(t, err)

	sm := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)
	require.Equal(t, 1, sm.Metrics().Len())

	m := sm.Metrics().At(0)
	assert.Equal(t, "browser.web_vital.duration", m.Name())
	assert.Equal(t, "ms", m.Unit())

	hist := m.Histogram()
	require.Equal(t, 1, hist.DataPoints().Len())

	dp := hist.DataPoints().At(0)
	assert.Equal(t, uint64(1), dp.Count())
	assert.Equal(t, 300.0, dp.Sum())

	dpAttrs := dp.Attributes()
	sessID, _ := dpAttrs.Get("session.id")
	assert.Equal(t, "sess-dp", sessID.AsString())
	metric, _ := dpAttrs.Get("vital.metric")
	assert.Equal(t, "fcp", metric.AsString())
	rating, _ := dpAttrs.Get("vital.rating")
	assert.Equal(t, "good", rating.AsString())
	netType, _ := dpAttrs.Get("network.effective_type")
	assert.Equal(t, "wifi", netType.AsString())
	urlFull, _ := dpAttrs.Get("url.full")
	assert.Equal(t, "https://example.com/", urlFull.AsString())
}

func TestDecodeAegisV2Metrics_OTELStructureAndFieldMapping(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0",
		"aid": "demo-app",
		"env": "production",
		"platform": "webjs",
		"netType": "4G"
	},
	"d2": [
		{
			"fields": "{\"from\": \"https://apps.paas3-dev.bktencent.com/otelfrontenddemo/\", \"session\": {\"id\": \"sess-otel-1\"}, \"view\": {\"id\": \"view-otel-1\", \"loading_type\": \"reload\", \"view_name\": \"OTel Frontend Demo\", \"view_url\": \"https://apps.paas3-dev.bktencent.com/otelfrontenddemo/\"}, \"type\": \"web_vitals\", \"level\": \"info\", \"plugin\": \"webVitals\"}",
			"message": [
				"{\"msg\": \"web_vitals\", \"FCP\": 632, \"LCP\": 956, \"TTFB\": 38.7, \"FID\": -1, \"CLS\": -1, \"INP\": -1, \"timestamp\": 1782736188098}"
			]
		}
	]
}`)

	metrics, err := decodeMetrics(buf)
	require.NoError(t, err)

	require.Equal(t, 1, metrics.ResourceMetrics().Len())
	rm := metrics.ResourceMetrics().At(0)

	// ResourceMetrics -> ScopeMetrics -> Metrics -> Histogram 的 OTEL 结构应完整。
	require.Equal(t, 1, rm.ScopeMetrics().Len())
	sm := rm.ScopeMetrics().At(0)
	require.Equal(t, 1, sm.Metrics().Len())
	m := sm.Metrics().At(0)
	assert.Equal(t, "browser.web_vital.duration", m.Name())
	assert.Equal(t, "Web Vitals duration metrics from aegisv2", m.Description())
	assert.Equal(t, "ms", m.Unit())

	hist := m.Histogram()
	require.Equal(t, 3, hist.DataPoints().Len())

	byMetric := make(map[string]int)
	for i := 0; i < hist.DataPoints().Len(); i++ {
		dp := hist.DataPoints().At(i)
		v, ok := dp.Attributes().Get("vital.metric")
		require.True(t, ok)
		byMetric[v.AsString()] = i

		assert.Equal(t, uint64(1), dp.Count())
		assert.Equal(t, dp.Timestamp(), dp.StartTimestamp())
		require.NotZero(t, dp.MExplicitBounds())
		assert.Equal(t, len(dp.MExplicitBounds())+1, len(dp.MBucketCounts()))
	}

	_, ok := byMetric["fcp"]
	assert.True(t, ok)
	_, ok = byMetric["lcp"]
	assert.True(t, ok)
	ttfbIdx, ok := byMetric["ttfb"]
	assert.True(t, ok)

	ttfb := hist.DataPoints().At(ttfbIdx)
	assert.InDelta(t, 38.7, ttfb.Sum(), 0.0001)
	rating, ok := ttfb.Attributes().Get("vital.rating")
	require.True(t, ok)
	assert.Equal(t, "good", rating.AsString())
}

func TestDecodeAegisV2Metrics_NonWebVitalsPayloadHandled(t *testing.T) {
	// 非 web_vitals 类型的 payload：handled=true，但无指标数据点
	buf := []byte(`{
	"topic": "SDK-test",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"type\": \"pv\", \"plugin\": \"pv\"}",
			"message": [
				"{\"msg\": \"pv\", \"timestamp\": 1782214911075}"
			]
		}
	]
}`)

	metrics, err := decodeMetrics(buf)
	require.NoError(t, err)

	sm := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)
	assert.Equal(t, 0, sm.Metrics().Len())
}

func TestDecodeAegisV2Metrics_NotAegisPayload(t *testing.T) {
	// OTLP 格式不被 aegisv2 处理
	buf := []byte(`{"resourceMetrics":[]}`)

	_, err := decodeMetrics(buf)
	assert.ErrorIs(t, err, ErrNotAegisV2)
}
