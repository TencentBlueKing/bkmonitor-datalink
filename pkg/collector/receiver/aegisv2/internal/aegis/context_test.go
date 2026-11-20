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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

func TestBKRUMCommonContextAndResource(t *testing.T) {
	a := testRecord("pv", map[string]any{"msg": "pv"})
	b := testRecord("custom_event", map[string]any{"name": "checkout"})
	b.Fields.Session.ID = "session-2"
	b.Fields.View.ID = "view-2"
	b.Fields.Action.ID = "action-1"
	b.Fields.Action.ActionType = "click"
	replay := true
	b.Fields.Session.HasReplay = &replay
	bean := clientBean{Version: "2.1.0", Env: "production", UID: "user-1", AID: "device-1", Platform: "macOS", NetType: "4G", VP: "576 * 918", SR: "1728 * 1117", Referer: "https://referrer.test/"}
	traces, err := ConvertJSON(testPayload(t, bean, a, b))
	require.NoError(t, err)
	rs := traces.ResourceSpans().At(0)
	assertAttrs(t, rs.Resource().Attributes(), map[string]any{
		"service.name": rum.DefaultServiceName, "telemetry.sdk.name": "aegis", "telemetry.sdk.language": "webjs",
		"telemetry.sdk.version": "2.5.42", "service.version": "2.1.0", "deployment.environment.name": "production",
		"user_agent.os.name": "macOS",
	})
	assertAbsent(t, rs.Resource().Attributes(), "session.id", "referer", "version", "env", "aid", "netType", "vp", "sr", "session.sample_rate")
	spans := rs.ScopeSpans().At(0).Spans()
	require.Equal(t, 3, spans.Len())
	for i := 0; i < spans.Len(); i++ {
		span := spans.At(i)
		assertAttrs(t, span.Attributes(), map[string]any{
			"user.id": "user-1", "device.id": "device-1", "session.type": "user", "view.url_template": "/orders/:id",
			"network.connection.type": "cellular", "network.effective_type": "4g",
			"browser.viewport.width": int64(576), "browser.viewport.height": int64(918),
			"browser.screen.width": int64(1728), "browser.screen.height": int64(1117),
		})
		assertAbsent(t, span.Attributes(), "event.type", "event.timestamp", "span_subtype", "result", "error_type", "rum.page.path", "http.url")
		assert.False(t, span.TraceID().IsEmpty())
		assert.False(t, span.SpanID().IsEmpty())
		assert.Equal(t, 0, span.Links().Len()) // referrer never creates an invalid link.
	}
	assertAttrs(t, spans.At(0).Attributes(), map[string]any{"session.id": "session-1", "view.id": "view-1", "view.referrer": "https://referrer.test/"})
	assertAttrs(t, spans.At(2).Attributes(), map[string]any{"session.id": "session-2", "view.id": "view-2", "session.has_replay": true, "action.id": "action-1"})
	assertAbsent(t, spans.At(2).Attributes(), "view.referrer")
}

// bean.version 是应用版本，SDK 版本来自 envelope 的 v。
func TestBKRUMVersionSources(t *testing.T) {
	attrs := pcommon.NewMap()
	buildResourceAttrs(attrs, collectPayload{Bean: clientBean{Version: "1.2.3"}})
	assertAttrs(t, attrs, map[string]any{"service.version": "1.2.3"})
	assertAbsent(t, attrs, "telemetry.sdk.version")

	attrs = pcommon.NewMap()
	buildResourceAttrs(attrs, collectPayload{V: "2.5.42", Bean: clientBean{Version: "1.2.3"}})
	assertAttrs(t, attrs, map[string]any{"telemetry.sdk.version": "2.5.42", "service.version": "1.2.3"})
}

func TestBKRUMNetworkNormalization(t *testing.T) {
	for _, tc := range []struct{ input, connection, effective string }{
		{"wifi", "wifi", ""}, {"wired", "ethernet", ""}, {"2G", "cellular", "2g"},
		{"5G", "cellular", ""}, {"6G", "cellular", ""}, {"slow-2g", "cellular", "slow-2g"},
		{"unknown", "unknown", ""}, {"", "", ""}, {"invalid", "", ""}, {"none", "none", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			attrs := pcommon.NewMap()
			putBrowserContextAttrs(attrs, collectPayload{Bean: clientBean{NetType: tc.input}})
			for key, want := range map[string]string{"network.connection.type": tc.connection, "network.effective_type": tc.effective} {
				if want == "" {
					assertAbsent(t, attrs, key)
				} else {
					assertAttrs(t, attrs, map[string]any{key: want})
				}
			}
			if tc.input == "none" {
				assertAttrs(t, attrs, map[string]any{"network.status": "not_connected"})
			} else {
				assertAbsent(t, attrs, "network.status")
			}
		})
	}
}

func TestBKRUMURLTemplate(t *testing.T) {
	for raw, expected := range map[string]string{
		"https://example.com/users/123?sort=desc":                    "/users/:id",
		"https://example.com/a/550e8400-e29b-41d4-a716-446655440000": "/a/:id",
		"https://example.com/a/ABCDEF0123456789":                     "/a/:id",
		"https://example.com/users/%31%32%33":                        "/users/:id",
		"https://example.com":                                        "/", "https://example.com/%zz": "", "": "",
	} {
		assert.Equal(t, expected, urlTemplate(raw), raw)
	}
}

func TestDecodeTracesKeepsSessionIDOnSpanOnly(t *testing.T) {
	buf := []byte(`{
	"topic": "SDK-xxxxx",
	"scheme": "v2",
	"bean": {
		"version": "1.0.0"
	},
	"d2": [
		{
			"fields": "{\"type\": \"session\", \"plugin\": \"session\", \"session\": {\"id\": \"session-1\"}}",
			"message": [
				"{\"msg\": \"session\", \"timestamp\": 123}"
			]
		}
	]
}`)

	traces, err := ConvertJSON(buf)
	require.NoError(t, err)

	rs := traces.ResourceSpans().At(0)
	_, ok := rs.Resource().Attributes().Get("session.id")
	assert.False(t, ok)

	span := rs.ScopeSpans().At(0).Spans().At(0)
	v, ok := span.Attributes().Get("session.id")
	require.True(t, ok)
	assert.Equal(t, "session-1", v.Str())
}
