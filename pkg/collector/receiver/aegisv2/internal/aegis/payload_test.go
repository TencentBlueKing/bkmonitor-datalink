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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

var sampleAegisPayload = loadTestPayload("../../testdata/resource.json")

func loadTestPayload(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func TestD2RecordUnmarshalJSON(t *testing.T) {
	t.Run("object payload", func(t *testing.T) {
		var record d2Record
		err := json.Unmarshal([]byte(`{
			"fields":"{\"type\":\"session\",\"session\":{\"id\":\"session-1\"}}",
			"message":["{\"msg\":\"session\",\"timestamp\":123}"]
		}`), &record)
		require.NoError(t, err)
		assert.Equal(t, "session", record.Fields.Type)
		assert.Equal(t, "session-1", record.Fields.Session.ID)
		require.Len(t, record.Message, 1)
		assert.Equal(t, "session", record.Message[0].Msg)
		assert.Equal(t, int64(123), record.Message[0].Timestamp)
	})

	t.Run("string wrapped payload", func(t *testing.T) {
		var record d2Record
		err := json.Unmarshal([]byte(`{
			"fields":"{\"type\":\"session\",\"session\":{\"id\":\"session-2\"}}",
			"message":["{\"msg\":\"session\",\"timestamp\":456}"]
		}`), &record)
		require.NoError(t, err)
		assert.Equal(t, "session", record.Fields.Type)
		assert.Equal(t, "session-2", record.Fields.Session.ID)
		require.Len(t, record.Message, 1)
		assert.Equal(t, "session", record.Message[0].Msg)
		assert.Equal(t, int64(456), record.Message[0].Timestamp)
	})
}

func FuzzD2MessageUnmarshalJSON(f *testing.F) {
	seeds := []string{
		`{"k":"v","n":1}`,
		`"{\"k\":\"v\",\"n\":1}"`,
		`null`,
		``,
		`""`,
		`[]`,
		`{`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		var msg d2Message
		_ = msg.UnmarshalJSON([]byte(input))
	})
}

func TestConvertJSONSkipsEmptyMessages(t *testing.T) {
	for _, typ := range []string{"pv", "error", "api", "session", "custom_event", "websocket"} {
		t.Run(typ, func(t *testing.T) {
			fields, err := json.Marshal(map[string]any{"type": typ})
			require.NoError(t, err)
			messages := []any{nil, "", "null", "{}"}
			for _, withValidMessage := range []bool{false, true} {
				want := 0
				if withValidMessage {
					messages = append(messages, `{"msg":"event","timestamp":1782214911075}`)
					want = 1
				}
				data, err := json.Marshal(map[string]any{
					"topic": "SDK-test",
					"d2":    []any{map[string]any{"fields": string(fields), "message": messages}},
				})
				require.NoError(t, err)
				traces, err := ConvertJSON(data)
				require.NoError(t, err)
				assert.Equal(t, want, traces.SpanCount())
			}
		})
	}
}

func TestDecodeAegisV2Traces_UsesRequestTraceID(t *testing.T) {
	id := random.TraceID()
	payload, records, err := decodePayload(sampleAegisPayload)
	require.NoError(t, err)
	traces, err := convertTraces(payload, records, id)
	require.NoError(t, err)
	assert.Equal(t, id, traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
	// A complete OTLP encode/decode also verifies native attribute types and timestamps.
	b, err := ptraceotlp.NewRequestFromTraces(traces).MarshalJSON()
	require.NoError(t, err)
	request := ptraceotlp.NewRequest()
	err = request.UnmarshalJSON(b)
	decoded := request.Traces()
	require.NoError(t, err)
	assert.Equal(t, traces.SpanCount(), decoded.SpanCount())
	assert.Equal(t, id, decoded.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
}
