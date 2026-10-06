// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type fakeKafkaSyncClient struct {
	calls  [][]*kgo.Record
	failAt int
	seen   int
	closed bool
}

func (c *fakeKafkaSyncClient) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	cloned := make([]*kgo.Record, len(records))
	results := make(kgo.ProduceResults, len(records))
	for index, record := range records {
		copyRecord := *record
		copyRecord.Key = append([]byte(nil), record.Key...)
		copyRecord.Value = append([]byte(nil), record.Value...)
		copyRecord.Headers = append([]kgo.RecordHeader(nil), record.Headers...)
		cloned[index] = &copyRecord
		results[index].Record = record
		c.seen++
		if c.failAt != 0 && c.seen == c.failAt {
			results[index].Err = errors.New("produce failed")
		}
	}
	c.calls = append(c.calls, cloned)
	return results
}

func (c *fakeKafkaSyncClient) Close() { c.closed = true }

func TestKafkaPublisherMapsAndChunksRecords(t *testing.T) {
	t.Parallel()
	client := &fakeKafkaSyncClient{}
	publisher := newKafkaPublisher(client, "events-topic")
	records := make([]Record, kafkaPublishChunkSize+1)
	for index := range records {
		records[index] = Record{
			Key: []byte("fingerprint"), Body: []byte(`{"action":"triggered"}`),
			Headers: map[string]string{
				"message_id": "event-id", "bk_tenant_id": "tenant-a", "order_key": "tenant/source/fingerprint",
			},
			Timestamp: time.Date(2026, 9, 2, 0, 0, index%60, 0, time.UTC),
		}
	}
	if err := publisher.Publish(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 2 || len(client.calls[0]) != kafkaPublishChunkSize || len(client.calls[1]) != 1 {
		t.Fatalf("Kafka call sizes = %v", kafkaCallSizes(client.calls))
	}
	first := client.calls[0][0]
	if first.Topic != "events-topic" || string(first.Key) != "fingerprint" || len(first.Headers) != 3 {
		t.Fatalf("mapped Kafka record = %#v", first)
	}
	wantHeaders := map[string]string{
		"message_id": "event-id", "bk_tenant_id": "tenant-a", "order_key": "tenant/source/fingerprint",
	}
	for _, header := range first.Headers {
		if string(header.Value) != wantHeaders[header.Key] {
			t.Fatalf("Kafka header %q = %q", header.Key, header.Value)
		}
	}
	publisher.Close()
	if !client.closed {
		t.Fatal("Kafka client was not closed")
	}
}

func TestKafkaPublisherReportsPartialFailure(t *testing.T) {
	t.Parallel()
	client := &fakeKafkaSyncClient{failAt: 2}
	publisher := newKafkaPublisher(client, "events-topic")
	record := Record{Headers: map[string]string{}}
	err := publisher.Publish(context.Background(), []Record{record, record, record})
	if err == nil || !strings.Contains(err.Error(), "succeeded=2 failed=1 total=3") ||
		!strings.Contains(err.Error(), "produce failed") {
		t.Fatalf("Publish() error = %v", err)
	}
}

func kafkaCallSizes(calls [][]*kgo.Record) []int {
	result := make([]int, len(calls))
	for index, call := range calls {
		result[index] = len(call)
	}
	return result
}
