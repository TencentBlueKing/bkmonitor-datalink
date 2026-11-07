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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

// 旧版 SDK 直接以 session_type 表达阶段，没有 is_active 时不推断。
func TestBKRUMSessionPhase(t *testing.T) {
	for _, tc := range []struct{ typ, from, phase string }{
		{"session", "local_generate", "start"}, {"rotate", "", "rotate"}, {"end", "", "end"}, {"session", "cache", ""},
	} {
		span := testSpans(t, testRecord("session", map[string]any{"session_type": tc.typ, "session_from": tc.from})).At(0)
		assert.Equal(t, "browser.session", span.Name())
		assertAttrs(t, span.Attributes(), map[string]any{"span_type": "session", "session.type": "user"})
		if tc.phase == "" {
			assertAbsent(t, span.Attributes(), "session.phase")
		} else {
			assertAttrs(t, span.Attributes(), map[string]any{"session.phase": tc.phase})
		}
	}
}

// aegis-web-sdk-v2 2.5.38/2.6.12 起统一上报 fields.type=session，
// 事件种类在 message.session_type，阶段在 is_active。
func TestBKRUMSessionPluginRouting(t *testing.T) {
	t.Run("view start and end map to view phases", func(t *testing.T) {
		r := testRecord("session", sessionMessage("view", true, testTimestamp))
		r.Message = append(r.Message, d2Message{raw: sessionMessage("view", false, testTimestamp+803)})
		spans := testSpans(t, r)
		require.Equal(t, 2, spans.Len())
		for i, phase := range []string{"start", "end"} {
			assert.Equal(t, "browser.view", spans.At(i).Name())
			assertAttrs(t, spans.At(i).Attributes(), map[string]any{"span_type": "view", "view.phase": phase})
		}
	})

	t.Run("view without is_active omits phase", func(t *testing.T) {
		span := testSpans(t, testRecord("session", map[string]any{"session_type": "view"})).At(0)
		assert.Equal(t, "browser.view", span.Name())
		assertAbsent(t, span.Attributes(), "view.phase")
	})

	t.Run("session kind maps is_active to start and end", func(t *testing.T) {
		for _, tc := range []struct {
			active bool
			phase  string
		}{{true, "start"}, {false, "end"}} {
			span := testSpans(t, testRecord("session", sessionMessage("session", tc.active, testTimestamp))).At(0)
			assert.Equal(t, "browser.session", span.Name())
			assertAttrs(t, span.Attributes(), map[string]any{"session.phase": tc.phase})
		}
	})

	t.Run("action start and end records yield one span carrying duration", func(t *testing.T) {
		r := testRecord("session", sessionMessage("action", true, testTimestamp))
		r.Message = append(r.Message, d2Message{raw: sessionMessage("action", false, testTimestamp+100)})
		r.Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click", ActionTargetName: "button"}
		spans := testSpans(t, r)
		require.Equal(t, 1, spans.Len())
		span := spans.At(0)
		assert.Equal(t, "action.click", span.Name())
		assert.Equal(t, unixMilliTimestamp(testTimestamp), span.StartTimestamp())
		assert.Equal(t, unixMilliTimestamp(testTimestamp+100), span.EndTimestamp())
	})

	// 消息时间戳经服务端校时，与客户端时钟的 action.timestamp 存在偏差；
	// 耗时必须来自两条消息的差值，否则会把时钟偏差算进 Action 耗时。
	t.Run("action duration ignores the server time gap", func(t *testing.T) {
		const gap int64 = 57501
		r := testRecord("session", sessionMessage("action", true, testTimestamp+gap))
		r.Message = append(r.Message, d2Message{raw: sessionMessage("action", false, testTimestamp+gap+100)})
		r.Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click"}
		span := testSpans(t, r).At(0)
		assert.Equal(t, unixMilliTimestamp(testTimestamp), span.StartTimestamp())
		assert.Equal(t, unixMilliTimestamp(testTimestamp+100), span.EndTimestamp())
	})

	t.Run("action end without a start record keeps the span instantaneous", func(t *testing.T) {
		r := testRecord("session", sessionMessage("action", false, testTimestamp+100))
		r.Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click"}
		span := testSpans(t, r).At(0)
		assert.Equal(t, unixMilliTimestamp(testTimestamp), span.StartTimestamp())
		assert.Equal(t, unixMilliTimestamp(testTimestamp), span.EndTimestamp())
	})

	t.Run("action end without a matching action span emits nothing", func(t *testing.T) {
		assert.Equal(t, 0, testSpans(t, testRecord("session", sessionMessage("action", false, testTimestamp))).Len())
	})

	t.Run("action end before start does not extend the span", func(t *testing.T) {
		r := testRecord("session", sessionMessage("action", true, testTimestamp+100))
		r.Message = append(r.Message, d2Message{raw: sessionMessage("action", false, testTimestamp+50)})
		r.Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click"}
		span := testSpans(t, r).At(0)
		assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	})
}

func sessionMessage(kind string, active bool, timestamp int64) map[string]any {
	return map[string]any{
		"msg": "session", "session_type": kind, "is_active": active,
		"session_from": "self_generate", "timestamp": timestamp,
	}
}

func unixMilliTimestamp(ms int64) pcommon.Timestamp {
	return pcommon.NewTimestampFromTime(time.UnixMilli(ms))
}
