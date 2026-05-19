// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestOriginMessageUnmarshalBaseInfo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		wantBiz string
		wantApp string
	}{
		{
			name:    "with base info",
			message: `{"dataid":1573230,"items":[{"bk_biz_id":2,"app_name":"bkop"}]}`,
			wantBiz: "2",
			wantApp: "bkop",
		},
		{
			name:    "with negative biz id",
			message: `{"dataid":1573230,"items":[{"bk_biz_id":-111,"app_name":"space-app"}]}`,
			wantBiz: "-111",
			wantApp: "space-app",
		},
		{
			name:    "without base info",
			message: `{"dataid":1573230,"items":[{"trace_id":"trace-id"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var msg OriginMessage
			require.NoError(t, jsonx.Unmarshal([]byte(tc.message), &msg))
			require.Len(t, msg.Items, 1)

			span := ToStandardSpan(msg.Items[0])
			assert.Equal(t, tc.wantBiz, span.BkBizId)
			assert.Equal(t, tc.wantApp, span.AppName)
		})
	}
}

func TestToStandardSpanFromMappingBaseInfo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		wantBiz string
		wantApp string
	}{
		{
			name: "with base info",
			fields: map[string]any{
				"bk_biz_id": "2",
				"app_name":  "testApp",
			},
			wantBiz: "2",
			wantApp: "testApp",
		},
		{
			name: "without base info",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			span := ToStandardSpanFromMapping(mappingSpan(tc.fields))

			assert.Equal(t, tc.wantBiz, span.BkBizId)
			assert.Equal(t, tc.wantApp, span.AppName)
		})
	}
}

func mappingSpan(fields map[string]any) map[string]any {
	span := map[string]any{
		"trace_id":       "trace-id",
		"span_id":        "span-id",
		"span_name":      "span-name",
		"parent_span_id": "",
		"start_time":     float64(1),
		"end_time":       float64(2),
		"elapsed_time":   float64(1),
		"status": map[string]any{
			"code": float64(0),
		},
		"kind":       float64(1),
		"attributes": map[string]any{},
		"resource":   map[string]any{},
	}
	for key, value := range fields {
		span[key] = value
	}
	return span
}
