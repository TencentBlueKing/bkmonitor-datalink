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
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBKRUMActionsAndCustomEvents(t *testing.T) {
	a := testRecord("api", map[string]any{"requestType": "fetch"})
	a.Fields.Action = actionInfo{ID: "action-1", Timestamp: testTimestamp, ActionType: "click", ActionName: "Buy", ActionTargetName: "button"}
	b := testRecord("custom_event", map[string]any{"name": "checkout", "ext1": "cart"})
	b.Fields.Action = a.Fields.Action
	spans := testSpans(t, a, b)
	require.Equal(t, 3, spans.Len())
	assert.Equal(t, "action.click", spans.At(0).Name())
	assertAttrs(t, spans.At(0).Attributes(), map[string]any{"span_type": "action", "action.target.name": "button"})
	assertAbsent(t, spans.At(0).Attributes(), "action.name", "action.target_name", "action.source_event_type", "action.timestamp")
	for i := 0; i < spans.Len(); i++ {
		assertAttrs(t, spans.At(i).Attributes(), map[string]any{"action.id": "action-1"})
	}
	assert.Equal(t, "custom.checkout", spans.At(2).Name())
	assertAttrs(t, spans.At(2).Attributes(), map[string]any{"span_type": "custom"})
	assertAbsent(t, spans.At(2).Attributes(), "aegisv2.ext.ext1", "ext1")
	a.Fields.Action.ActionType = "custom"
	assert.Equal(t, "action.Buy", testSpans(t, a).At(0).Name())
	unknown := testSpans(t, testRecord("new_plugin", map[string]any{"extra": true})).At(0)
	assert.Equal(t, "custom.new_plugin", unknown.Name())
	assert.Equal(t, ptrace.SpanKindInternal, unknown.Kind())
	assertAttrs(t, unknown.Attributes(), map[string]any{"span_type": "custom"})
	assertAbsent(t, unknown.Attributes(), "aegisv2.ext.extra", "extra")
}
