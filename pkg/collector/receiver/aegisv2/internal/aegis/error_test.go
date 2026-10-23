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

// SDK aegis.error() 的真实报文：三种入参（字符串、Error、带 msg 的对象）都应转为错误 Span。
func TestBKRUMCustomErrorFromSDK(t *testing.T) {
	data, err := os.ReadFile("../../testdata/custom_error.json")
	require.NoError(t, err)
	traces, err := ConvertJSON(data)
	require.NoError(t, err)
	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	require.Equal(t, 3, spans.Len())
	for i, message := range []string{"string boom", "object boom", "mapped boom"} {
		span := spans.At(i)
		assert.Equal(t, "browser.error", span.Name())
		assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
		assert.Equal(t, message, span.Status().Message())
		assertAttrs(t, span.Attributes(), map[string]any{
			"span_type": "error", "outcome.type": "error", "error.source": "window.error",
			"error.handled": false, "error.message": message,
		})
		require.Equal(t, 1, span.Events().Len())
		assertAttrs(t, span.Events().At(0).Attributes(), map[string]any{
			"exception.type": "Error", "exception.message": message,
		})
	}
}

func TestBKRUMErrors(t *testing.T) {
	for _, tc := range []struct{ level, name, source, exception string }{
		{"error", "browser.error", "window.error", "Error"},
		{"custom_error", "browser.error", "window.error", "Error"},
		{"promise_error", "browser.unhandledrejection", "unhandledrejection", "UnhandledRejection"},
		{"resource_error", "browser.resource_error", "resource", "ResourceError"},
	} {
		t.Run(tc.level, func(t *testing.T) {
			r := testRecord("normal", map[string]any{"errorMsg": "failed", "stack": "Error: failed\n at app.js:3:4", "filename": "app.js", "lineno": 3, "colno": 4})
			r.Fields.Level = tc.level
			span := testSpans(t, r).At(0)
			assert.Equal(t, tc.name, span.Name())
			assert.Equal(t, ptrace.StatusCodeError, span.Status().Code())
			assert.Equal(t, "failed", span.Status().Message())
			assertAttrs(t, span.Attributes(), map[string]any{"span_type": "error", "outcome.type": "error", "error.source": tc.source, "error.handled": false, "code.filepath": "app.js", "code.lineno": int64(3), "code.column": int64(4)})
			assertAbsent(t, span.Attributes(), "exception.type", "exception.message", "exception.stacktrace")
			require.Equal(t, 1, span.Events().Len())
			event := span.Events().At(0)
			assert.Equal(t, "exception", event.Name())
			assert.Equal(t, span.EndTimestamp(), event.Timestamp())
			assertAttrs(t, event.Attributes(), map[string]any{"exception.type": tc.exception, "exception.message": "failed", "exception.stacktrace": "Error: failed\n at app.js:3:4"})
		})
	}
}

func TestAegisEventIsError(t *testing.T) {
	tests := []struct {
		name   string
		level  string
		plugin string
		msgRaw map[string]any
		want   bool
	}{
		{name: "exact error level", level: "error", want: true},
		{name: "critical level", level: "critical", want: true},
		{name: "panic level", level: "panic", want: true},
		{name: "exception level", level: "exception", want: true},
		{name: "custom error level", level: "custom_error", want: true},
		{name: "false positive no error avoided", level: "no_error", want: false},
		{name: "false positive error bypass avoided", level: "error_bypass", want: false},
		{name: "plugin error still wins", level: "info", plugin: "error", want: true},
		{name: "websocket success flag false is error", plugin: "websocket", msgRaw: map[string]any{"successFlag": false}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := aegisEvent{
				record: d2Record{Fields: d2Fields{Level: tc.level, Plugin: tc.plugin}},
				msg:    d2Message{raw: tc.msgRaw},
			}
			assert.Equal(t, tc.want, e.isError())
		})
	}
}
