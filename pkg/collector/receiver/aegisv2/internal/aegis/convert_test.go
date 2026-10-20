// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

const testTimestamp int64 = 1782214911075

// Exercise the wire format: fields and each message are JSON-encoded strings.
func testPayload(t *testing.T, bean clientBean, records ...d2Record) []byte {
	t.Helper()
	var d2 []map[string]any
	for _, record := range records {
		fields, err := json.Marshal(record.Fields)
		require.NoError(t, err)
		var messages []string
		for _, msg := range record.Message {
			b, err := json.Marshal(msg.raw)
			require.NoError(t, err)
			messages = append(messages, string(b))
		}
		d2 = append(d2, map[string]any{"fields": string(fields), "message": messages})
	}
	b, err := json.Marshal(map[string]any{"topic": "SDK-test", "v": "2.5.42", "scheme": "v2", "bean": bean, "d2": d2})
	require.NoError(t, err)
	return b
}

func testRecord(typ string, raw map[string]any) d2Record {
	if _, ok := raw["timestamp"]; !ok {
		raw["timestamp"] = testTimestamp
	}
	return d2Record{
		Fields: d2Fields{Type: typ, Session: sessionInfo{ID: "session-1"}, View: viewInfo{
			ID: "view-1", ViewName: "Orders", LoadingType: "initial_load", ViewURL: "https://example.com/orders/123?tab=all",
		}},
		Message: []d2Message{{raw: raw}},
	}
}

func testSpans(t *testing.T, records ...d2Record) ptrace.SpanSlice {
	t.Helper()
	traces, err := ConvertJSON(testPayload(t, clientBean{}, records...))
	require.NoError(t, err)
	return traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
}

func assertAttrs(t *testing.T, attrs pcommon.Map, want map[string]any) {
	t.Helper()
	for key, expected := range want {
		_, ok := attrs.Get(key)
		if assert.True(t, ok, "missing %s", key) {
			assert.Equal(t, expected, attrs.AsRaw()[key], key)
		}
	}
}

func assertAbsent(t *testing.T, attrs pcommon.Map, keys ...string) {
	t.Helper()
	for _, key := range keys {
		_, ok := attrs.Get(key)
		assert.False(t, ok, "unexpected %s", key)
	}
}

func TestEventMappingPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, typ, msg, level, plugin, want string }{
		{"websocket error", "websocket", "", "error", "websocket", "browser.websocket"},
		{"custom error", "custom_event", "", "error", "", "custom.custom_event"},
		{"session alias", "", "session", "", "", "browser.session"},
		{"page performance alias", "normal", "page_performance", "", "", "browser.view"},
		{"SPA alias", "normal", "spa", "", "spa", "browser.view"},
		{"normal error", "normal", "", "error", "", "browser.error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRecord(tc.typ, map[string]any{"msg": tc.msg, "firstScreenTiming": 627})
			r.Fields.Level, r.Fields.Plugin = tc.level, tc.plugin
			assert.Equal(t, tc.want, testSpans(t, r).At(0).Name())
		})
	}
}

func TestMessageAliasesCannotOverrideEventType(t *testing.T) {
	for _, tc := range []struct {
		name, typ, plugin, level, msg, want, outcome string
		isErr                                        bool
	}{
		{"error with PV text", "error", "", "", "pv", "browser.error", "error", false},
		{"error with vital text", "error", "error", "", "web_vitals", "browser.error", "error", false},
		{"error with performance text", "error", "error", "", "page_performance", "browser.error", "error", false},
		{"error with session text", "error", "", "", "session", "browser.error", "error", false},
		{"error with resource text", "error", "", "", "asset_speed", "browser.error", "error", false},
		{"error with custom text", "error", "", "", "custom_event", "browser.error", "error", false},
		{"error plugin", "normal", "error", "", "pv", "browser.error", "error", false},
		{"error level", "normal", "", "error", "web_vitals", "browser.error", "error", false},
		{"error flag", "", "", "", "page_performance", "browser.error", "error", true},
		{"API with vital text", "api", "", "", "web_vitals", "browser.resource", "error", true},
		{"custom with PV text", "custom_event", "", "", "pv", "custom.custom_event", "error", true},
		{"session with performance text", "session", "", "", "page_performance", "browser.session", "success", false},
		{"websocket plugin", "normal", "websocket", "", "pv", "browser.websocket", "success", false},
		{"unknown explicit type", "new_plugin", "", "", "pv", "custom.new_plugin", "success", false},
		{"legacy resource alias", "normal", "assetSpeed", "", "asset_speed", "browser.resource", "success", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRecord(tc.typ, map[string]any{"msg": tc.msg, "isErr": tc.isErr})
			r.Fields.Plugin, r.Fields.Level = tc.plugin, tc.level
			spans := testSpans(t, r)
			require.Equal(t, 1, spans.Len())
			assert.Equal(t, tc.want, spans.At(0).Name())
			assertAttrs(t, spans.At(0).Attributes(), map[string]any{"outcome.type": tc.outcome})
		})
	}
}

// Exercise every event path through the wire decoder. Unknown fields must not
// leak through either the former extension namespace or standard attributes.
func TestConvertJSONDropsUnmappedFields(t *testing.T) {
	traceparent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	records := []d2Record{
		testRecord("session", map[string]any{"session_phase": "start"}),
		testRecord("pv", map[string]any{"responseStart": 12.5, "firstScreenTiming": 912}),
		testRecord("api", map[string]any{"requestType": "fetch", "status": 200, "transferSize": 0, "traceparent": traceparent}),
		testRecord("error", map[string]any{"errorName": "TypeError", "errorMsg": "failed", "handled": false, "stack": "at checkout"}),
		testRecord("websocket", map[string]any{"successFlag": false, "connectionId": "ws-1"}),
		testRecord("web_vitals", map[string]any{"FCP": 0, "LCP": 2501}),
		testRecord("custom_event", map[string]any{"name": "checkout"}),
		testRecord("new_plugin", map[string]any{}),
	}
	records[2].Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click"}
	replay := false
	for i := range records {
		records[i].Fields.Session.HasReplay = &replay
		raw := records[i].Message[0].raw
		raw["unmapped"] = "discard me"
		raw["nested"] = map[string]any{"secret": "discard me", "values": []any{0, false, "value"}}
		raw["param"] = map[string]any{"token": "discard me"}
		raw["span_type"] = "spoofed"
		raw["outcome.type"] = "spoofed"
		raw["view.id"] = "spoofed"
		raw["aegisv2.ext.injected"] = "discard me"
	}
	traces, err := ConvertJSON(testPayload(t, clientBean{}, records...))
	require.NoError(t, err)
	require.Equal(t, 10, traces.SpanCount())
	assertClean := func(attrs pcommon.Map) {
		attrs.Range(func(key string, _ pcommon.Value) bool {
			assert.False(t, strings.HasPrefix(key, "aegisv2.ext"), "unexpected extension %s", key)
			return true
		})
		assertAbsent(t, attrs, "unmapped", "nested", "param", "firstScreenTiming")
	}
	rs := traces.ResourceSpans().At(0)
	assertClean(rs.Resource().Attributes())
	ss := rs.ScopeSpans().At(0)
	seen := make(map[string]int)
	for i := 0; i < ss.Spans().Len(); i++ {
		span := ss.Spans().At(i)
		assertClean(span.Attributes())
		assertAttrs(t, span.Attributes(), map[string]any{"view.id": "view-1", "session.has_replay": false})
		typ, _ := span.Attributes().Get("span_type")
		seen[typ.Str()]++
		switch typ.Str() {
		case "action":
			assert.Equal(t, "action.click", span.Name())
			assertAttrs(t, span.Attributes(), map[string]any{"action.id": "action-1", "action.type": "click"})
		case "session":
			assertAttrs(t, span.Attributes(), map[string]any{"session.phase": "start"})
		case "view":
			assertAttrs(t, span.Attributes(), map[string]any{"view.phase": "start"})
		case "resource":
			assertAttrs(t, span.Attributes(), map[string]any{"http.response.status_code": int64(200), "resource.transfer_size": int64(0)})
			require.Equal(t, 1, span.Links().Len())
			assertAttrs(t, span.Links().At(0).Attributes(), map[string]any{"format": "traceparent", "traceparent": traceparent})
		case "error":
			assertAttrs(t, span.Attributes(), map[string]any{"error.message": "failed", "error.handled": false})
			require.Equal(t, 1, span.Events().Len())
			assertAttrs(t, span.Events().At(0).Attributes(), map[string]any{"exception.type": "TypeError", "exception.stacktrace": "at checkout"})
		case "websocket":
			assertAttrs(t, span.Attributes(), map[string]any{"websocket.phase": "error", "websocket.connection.id": "ws-1"})
		case "vital":
			metric, _ := span.Attributes().Get("vital.metric")
			if metric.Str() == "fcp" {
				assertAttrs(t, span.Attributes(), map[string]any{"vital.value": float64(0), "outcome.type": "success"})
			} else {
				assertAttrs(t, span.Attributes(), map[string]any{"vital.value": float64(2501), "outcome.type": "warning"})
			}
		case "custom":
			assert.Contains(t, []string{"custom.checkout", "custom.new_plugin"}, span.Name())
		}
		for j := 0; j < span.Events().Len(); j++ {
			assertClean(span.Events().At(j).Attributes())
		}
		for j := 0; j < span.Links().Len(); j++ {
			assertClean(span.Links().At(j).Attributes())
		}
	}
	assert.Equal(t, map[string]int{"action": 1, "session": 1, "view": 1, "resource": 1, "error": 1, "websocket": 1, "vital": 2, "custom": 2}, seen)
}
