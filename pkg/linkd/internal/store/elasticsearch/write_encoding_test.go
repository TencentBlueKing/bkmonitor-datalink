// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type countedTarget struct{ calls *atomic.Int64 }

func (c countedTarget) MarshalJSON() ([]byte, error) { c.calls.Add(1); return []byte(`"alerts"`), nil }

func TestWriteEncodingReusedAcrossSplitAndSend(t *testing.T) {
	var count atomic.Int64
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if len(lines) != 2 || !json.Valid([]byte(lines[0])) || lines[1] != `{"value":1}` {
			t.Fatalf("invalid wire body %q", body)
		}
		if !strings.Contains(lines[0], `"if_seq_no":5`) || !strings.Contains(lines[0], `"if_primary_term":2`) {
			t.Fatal("CAS metadata lost")
		}
		return batchHTTPResponse(200, []byte(`{"items":[{"index":{"status":200}}]}`)), nil
	}), nil)
	c := &batchCall{ctx: t.Context(), reply: make(chan batchReply, 1), operations: []batchOperation{{Action: "index", Metadata: map[string]any{"_index": countedTarget{&count}, "_id": "a", "if_seq_no": 5, "if_primary_term": 2}, Source: json.RawMessage(`{"value":1}`)}}}
	b.slots <- struct{}{}
	b.execute([]*batchCall{c})
	r := <-c.reply
	if r.err != nil || r.status != 200 {
		t.Fatalf("reply %#v", r)
	}
	if count.Load() != 1 {
		t.Fatalf("operation encoded %d times, want once", count.Load())
	}
}

func BenchmarkWriteOperationEncoding(b *testing.B) {
	op := batchOperation{Action: "index", Metadata: map[string]any{"_index": "events", "_id": "a", "if_seq_no": 5, "if_primary_term": 2}, Source: json.RawMessage(`{"payload":"` + strings.Repeat("x", 1024) + `"}`)}
	for _, reuse := range []bool{false, true} {
		name := "encode"
		if reuse {
			name = "reuse"
		}
		b.Run(name, func(b *testing.B) {
			item := op
			if reuse {
				var err error
				item.encodedWrite, err = encodeBatchOperation(item, false)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				data, err := encodeBatchOperation(item, false)
				if err != nil || len(data) == 0 {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestBulkItemResponsePreservesValidatedItems(t *testing.T) {
	items := []batchItem{
		{raw: json.RawMessage(`{"create":{"status":201,"_seq_no":9007199254740993,"extra":"中文<&>"}}`)},
		{raw: json.RawMessage(`{"create":{"status":429,"error":{"type":"rejected","reason":"retry"}}}`)},
	}
	data := bulkItemResponse(items)
	if !json.Valid(data) || !strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("invalid or lossy response: %s", data)
	}
	var decoded struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if string(decoded.Items[i]) != string(items[i].raw) {
			t.Fatalf("item %d changed", i)
		}
	}
	if string(bulkItemResponse(nil)) != `{"items":[]}` {
		t.Fatal("invalid empty envelope")
	}
}

func BenchmarkBulkItemResponse(b *testing.B) {
	items := make([]batchItem, 256)
	raw := make([]json.RawMessage, len(items))
	for i := range items {
		raw[i] = json.RawMessage(`{"create":{"status":201,"_index":"logs","_id":"event-log","_seq_no":123,"_primary_term":1}}`)
		items[i].raw = raw[i]
	}
	b.Run("marshal-raw-items", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := json.Marshal(map[string]any{"items": raw}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("reuse-validated-items", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if len(bulkItemResponse(items)) == 0 {
				b.Fatal("empty response")
			}
		}
	})
}
