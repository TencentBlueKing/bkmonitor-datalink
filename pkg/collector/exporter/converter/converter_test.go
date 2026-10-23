// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestCleanAttributesMap(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		attrs := map[string]any{
			"":    "1",
			"foo": "bar",
		}

		attrs = CleanAttributesMap(attrs)
		assert.Len(t, attrs, 1)
		assert.Equal(t, "bar", attrs["foo"])
	})

	t.Run("Trim", func(t *testing.T) {
		attrs := map[string]any{
			" ":   "1",
			"foo": "bar",
		}

		attrs = CleanAttributesMap(attrs)
		assert.Len(t, attrs, 1)
		assert.Equal(t, "bar", attrs["foo"])
	})
}

func TestCommonConverter(t *testing.T) {
	conv := NewCommonConverter(&Config{})
	defer conv.Clean()

	conv.Convert(&define.Record{
		RecordType: define.RecordLogPush,
		Data: &define.LogPushData{
			Data: []string{"hello", "world"},
		},
	}, func(events ...define.Event) {})
}

func TestCommonConverterRumTraces(t *testing.T) {
	conv := NewCommonConverter(&Config{})
	t.Cleanup(conv.Clean)

	traces := ptrace.NewTraces()
	resource := traces.ResourceSpans().AppendEmpty()
	resource.Resource().Attributes().PutString("service.name", "rum-browser")
	spans := resource.ScopeSpans().AppendEmpty().Spans()
	for _, name := range []string{"page.load", "resource.load"} {
		span := spans.AppendEmpty()
		span.SetName(name)
		span.SetTraceID(pcommon.TraceID{1, 2, 3})
		span.SetSpanID(pcommon.SpanID{byte(spans.Len())})
		span.Attributes().PutString("url.path", "/home")
	}
	record := &define.Record{
		RecordType:    define.RecordRumTraces,
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: "192.0.2.1"},
		Token: define.Token{
			Original:     "rum-token",
			BizId:        2,
			AppName:      "rum-app",
			TracesDataId: 1001,
		},
		Data: traces,
	}

	var events []define.Event
	conv.Convert(record, func(batch ...define.Event) { events = append(events, batch...) })
	require.Len(t, events, 2)
	for i, event := range events {
		assert.Equal(t, define.RecordTraces, event.RecordType())
		assert.Equal(t, int32(1001), event.DataId())
		assert.Equal(t, record.Token, event.Token())
		data := event.Data()
		assert.Equal(t, spans.At(i).Name(), data["span_name"])
		assert.Equal(t, spans.At(i).TraceID().HexString(), data["trace_id"])
		assert.Equal(t, spans.At(i).SpanID().HexString(), data["span_id"])
		assert.Equal(t, map[string]any{"url.path": "/home"}, data["attributes"])
		assert.Equal(t, common.MapStr{"service.name": "rum-browser"}, data["resource"])
		assert.Equal(t, "192.0.2.1", data["client_ip"])
		assert.Equal(t, int32(2), data["bk_biz_id"])
		assert.Equal(t, "rum-app", data["app_name"])
	}
}
