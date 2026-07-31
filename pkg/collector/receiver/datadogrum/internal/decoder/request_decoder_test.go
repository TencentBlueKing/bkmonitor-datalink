// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const defaultMaxDecodedBytes = 10 << 20

func TestDecodeEvents_Array(t *testing.T) {
	events, err := DecodeEvents([]byte(`[{"type":"view"},{"type":"action"}]`), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestDecodeEvents_Wrapped(t *testing.T) {
	events, err := DecodeEvents([]byte(`{"events":[{"type":"view"},{"type":"action"}]}`), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestDecodeEvents_NDJSON(t *testing.T) {
	events, err := DecodeEvents([]byte("{\"type\":\"view\"}\n{\"type\":\"action\"}"), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestDecodeEvents_NDJSON_CRLFAndBlankLines(t *testing.T) {
	events, err := DecodeEvents([]byte("{\"type\":\"view\"}\r\n\r\n{\"type\":\"action\"}\r\n"), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestDecodeEvents_Single(t *testing.T) {
	events, err := DecodeEvents([]byte(`{"type":"view"}`), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestDecodeEvents_PrettySingle(t *testing.T) {
	events, err := DecodeEvents([]byte(`{
  "type": "view",
  "application": {
    "id": "app"
  }
}`), defaultMaxDecodedBytes)
	assert.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestDecodeEvents_ExceedsMaxDecodedBytes(t *testing.T) {
	payload := []byte(`{"type":"view"}`)
	_, err := DecodeEvents(payload, 1)
	assert.Error(t, err)
}

func TestDecodeEvents_InvalidEntries(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`[null]`),
		[]byte(`[1]`),
		[]byte(`{"events":null}`),
		[]byte(`{"events":[null]}`),
	} {
		_, err := DecodeEvents(payload, defaultMaxDecodedBytes)
		assert.Error(t, err, string(payload))
	}
}

func TestDecodeEvents_InvalidLimit(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		_, err := DecodeEvents([]byte(`{"type":"view"}`), limit)
		assert.ErrorIs(t, err, ErrInvalidLimit)
	}
}

func BenchmarkDecodeEvents_NDJSON(b *testing.B) {
	var builder strings.Builder
	for i := 0; i < 1000; i++ {
		_, _ = fmt.Fprintf(&builder, `{"type":"view","date":%d,"application":{"id":"app"},"view":{"id":"view-%d"}}`+"\n", i+1, i)
	}
	payload := []byte(builder.String())

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		events, err := DecodeEvents(payload, defaultMaxDecodedBytes)
		if err != nil {
			b.Fatal(err)
		}
		if len(events) != 1000 {
			b.Fatalf("expected 1000 events, got %d", len(events))
		}
	}
}
