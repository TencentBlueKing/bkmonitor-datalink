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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBKRUMViewLifecycle(t *testing.T) {
	pv := testRecord("pv", map[string]any{"msg": "page_performance", "responseStart": 12.5})
	spa := testRecord("pv", map[string]any{"msg": "spa"})
	spa.Fields.View.LoadingType = "route_change"
	spa.Fields.View.Referrer = "https://example.com/users/456?sort=name"
	spans := testSpans(t, pv, spa)
	for i := 0; i < spans.Len(); i++ {
		span := spans.At(i)
		assert.Equal(t, "browser.view", span.Name())
		assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
		assert.Equal(t, 0, span.Events().Len())
		assertAttrs(t, span.Attributes(), map[string]any{"span_type": "view", "view.phase": "start"})
		assertAbsent(t, span.Attributes(), "view.version", "view.started_at", "view.loading_time", "view.loading_time_source", "view.first_byte")
	}
	assertAttrs(t, spans.At(0).Attributes(), map[string]any{"view.previous": "", "view.previous_url_template": ""})
	assertAttrs(t, spans.At(1).Attributes(), map[string]any{"view.previous_url_template": "/users/:id"})
	assertAbsent(t, spans.At(1).Attributes(), "view.referrer")
}

func TestBKRUMPagePerformanceViewUpdate(t *testing.T) {
	spans := testSpans(t,
		testRecord("page_performance", map[string]any{
			"msg":           "pv",
			"responseStart": 125.5, "domInteractive": 640, "domContentLoadedEventEnd": "650.5",
			"domComplete": 1180, "loadEventEnd": 1190,
			"ttfb": 16, "firstScreenTiming": 627, "engineInit": 3, "bundleLoad": 4,
		}),
		testRecord("web_vitals", map[string]any{"TTFB": 900, "aegisv2_goto": "vital-ttfb"}),
	)
	require.Equal(t, 2, spans.Len())
	view := spans.At(0)
	assert.Equal(t, "browser.view", view.Name())
	assert.Equal(t, ptrace.SpanKindInternal, view.Kind())
	assert.Equal(t, testTimestamp, view.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, view.StartTimestamp(), view.EndTimestamp())
	assertAttrs(t, view.Attributes(), map[string]any{
		"span_type": "view", "view.phase": "update", "outcome.type": "success",
		"view.first_byte": 125.5, "view.dom_interactive": float64(640),
		"view.dom_content_loaded": 650.5, "view.dom_complete": float64(1180), "view.load_event": float64(1190),
	})
	assertAbsent(t, view.Attributes(), "view.version", "view.started_at", "view.loading_time", "view.loading_time_source", "vital.metric", "vital.value", "vital.id")
	for key := range view.Attributes().AsRaw() {
		assert.NotContains(t, key, "view.performance.")
	}
	vital := spans.At(1)
	assert.Equal(t, "browser.web_vital", vital.Name())
	assertAttrs(t, vital.Attributes(), map[string]any{
		"span_type": "vital", "vital.metric": "ttfb", "vital.value": float64(900),
		"vital.id": "vital-ttfb.ttfb", "outcome.type": "warning",
	})
	assertAbsent(t, vital.Attributes(), "view.phase", "view.first_byte")
	assert.Equal(t, view.TraceID(), vital.TraceID())
	for i := 0; i < spans.Len(); i++ {
		assertAttrs(t, spans.At(i).Attributes(), map[string]any{"view.id": "view-1", "session.id": "session-1"})
	}
}

func TestBKRUMPagePerformanceNavigationValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		valid bool
		want  float64
	}{
		{"zero", 0, false, 0}, {"fraction", 12.5, true, 12.5}, {"numeric string", "602.5", true, 602.5},
		{"negative", -1, false, 0}, {"NaN", "NaN", false, 0}, {"positive infinity", "+Inf", false, 0},
		{"negative infinity", "-Inf", false, 0}, {"null", nil, false, 0}, {"bool", false, false, 0},
		{"object", map[string]any{"value": 3}, false, 0}, {"empty", "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := testSpans(t, testRecord("page_performance", map[string]any{
				"responseStart": tc.value, "domInteractive": tc.value, "domContentLoadedEventEnd": tc.value,
				"domComplete": tc.value, "loadEventEnd": tc.value,
			}))
			require.Equal(t, 1, spans.Len())
			attrs := spans.At(0).Attributes()
			assertAttrs(t, attrs, map[string]any{"view.phase": "update"})
			for _, key := range []string{"view.first_byte", "view.dom_interactive", "view.dom_content_loaded", "view.dom_complete", "view.load_event"} {
				if tc.valid {
					assertAttrs(t, attrs, map[string]any{key: tc.want})
				} else {
					assertAbsent(t, attrs, key)
				}
			}
		})
	}
}

func TestBKRUMPagePerformanceNavigationOnlyInitialLoad(t *testing.T) {
	for _, loadingType := range []string{"route_change", "bf_cache", "session_renewal", "", "unknown"} {
		t.Run(loadingType, func(t *testing.T) {
			record := testRecord("page_performance", map[string]any{
				"responseStart": 12.5, "domInteractive": 640, "domContentLoadedEventEnd": 650,
				"domComplete": 1180, "loadEventEnd": 1190,
			})
			record.Fields.View.LoadingType = loadingType
			record.Fields.View.Referrer = "https://example.com/users/456"
			spans := testSpans(t, record)
			require.Equal(t, 1, spans.Len())
			assertAttrs(t, spans.At(0).Attributes(), map[string]any{
				"view.phase": "update", "view.previous": record.Fields.View.Referrer, "view.previous_url_template": "/users/:id",
			})
			assertAbsent(t, spans.At(0).Attributes(), "view.first_byte", "view.dom_interactive", "view.dom_content_loaded", "view.dom_complete", "view.load_event", "view.referrer")
		})
	}
}

func TestConvertJSONPagePerformanceInitialLoad(t *testing.T) {
	data, err := os.ReadFile("../../testdata/page_performance_initial_load.json")
	require.NoError(t, err)
	traces, err := ConvertJSON(data)
	require.NoError(t, err)
	require.Equal(t, 1, traces.ResourceSpans().Len())
	require.Equal(t, 1, traces.SpanCount())
	rs := traces.ResourceSpans().At(0)
	assert.Equal(t, map[string]any{
		"service.name": "unknown_service", "telemetry.sdk.name": "aegis", "telemetry.sdk.language": "webjs",
		"telemetry.sdk.version": "1.0.0", "deployment.environment.name": "production", "user_agent.os.name": "macOS",
	}, rs.Resource().Attributes().AsRaw())
	require.Equal(t, 1, rs.ScopeSpans().Len())
	ss := rs.ScopeSpans().At(0)
	assert.Equal(t, "aegisv2.collect", ss.Scope().Name())
	assert.Equal(t, "1.0.0", ss.Scope().Version())
	span := ss.Spans().At(0)
	assert.Equal(t, "browser.view", span.Name())
	assert.Equal(t, ptrace.SpanKindInternal, span.Kind())
	assert.False(t, span.TraceID().IsEmpty())
	assert.False(t, span.SpanID().IsEmpty())
	assert.True(t, span.ParentSpanID().IsEmpty())
	assert.Equal(t, int64(1791949053633), span.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	assert.Equal(t, ptrace.StatusCodeUnset, span.Status().Code())
	assert.Zero(t, span.Events().Len())
	assert.Zero(t, span.Links().Len())
	// 样例只有阶段耗时，不将 ttfb 或 firstScreenTiming 当成标准导航或页面稳定时间。
	assert.Equal(t, map[string]any{
		"span_type": "view", "outcome.type": "success", "view.phase": "update",
		"view.referrer": "http://127.0.0.1:8081/", "view.previous": "", "view.previous_url_template": "",
		"session.id": "6de67fbd608f8bb1c81484101b9269f3", "session.type": "user",
		"user.id": "user_1791892849875_5312722e", "device.id": "b17745df-9347-4d9d-8658-f27677d04d9d",
		"view.id": "e6ec88756f2be63c", "view.name": "VideoFlow - 分区页", "view.loading_type": "initial_load",
		"view.url": "http://127.0.0.1:8081/channels.html", "view.url_template": "/channels.html",
		"network.connection.type": "cellular", "network.effective_type": "4g",
		"browser.viewport.width": int64(1456), "browser.viewport.height": int64(538),
		"browser.screen.width": int64(1512), "browser.screen.height": int64(982),
	}, span.Attributes().AsRaw())
}
