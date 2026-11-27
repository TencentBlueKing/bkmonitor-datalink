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
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBKRUMWebsocket(t *testing.T) {
	for _, success := range []bool{true, false} {
		span := testSpans(t, testRecord("websocket", map[string]any{"url": "wss://socket.example.com/ws", "successFlag": success, "duration": 312.6, "msg": "connection", "connectionId": "ws-1"})).At(0)
		assert.Equal(t, "browser.websocket", span.Name())
		assert.Equal(t, ptrace.SpanKindClient, span.Kind())
		assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
		assert.Equal(t, ptrace.StatusCodeUnset, span.Status().Code())
		assert.Zero(t, span.Events().Len())
		phase, outcome := "open", "success"
		if !success {
			phase, outcome = "error", "error"
		}
		assertAttrs(t, span.Attributes(), map[string]any{"span_type": "websocket", "websocket.phase": phase, "outcome.type": outcome, "network.protocol.name": "websocket", "websocket.connection.id": "ws-1"})
		assertAbsent(t, span.Attributes(), "server.port", "span_subtype", "trace_scene")
	}
}
