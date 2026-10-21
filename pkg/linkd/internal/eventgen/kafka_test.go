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
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

type outOfOrderKafka struct {
	failure error
	records []*kgo.Record
}

func (c *outOfOrderKafka) ProduceSync(_ context.Context, r ...*kgo.Record) kgo.ProduceResults {
	c.records = r
	return kgo.ProduceResults{{Record: r[2]}, {Record: r[0], Err: c.failure}, {Record: r[1]}}
}

func (*outOfOrderKafka) Close() {}

func TestKafkaPartialResultsFollowRecordIdentity(t *testing.T) {
	failure := errors.New("injected failure")
	client := &outOfOrderKafka{failure: failure}
	publisher := &KafkaPublisher{targets: map[string]kafkaTarget{"source": {client: client, topic: "topic"}}}
	records := []Record{{SourceID: "source", Body: []byte(`{"i":0}`), Timestamp: startTime(), Headers: map[string]string{"message_id": "first", "custom": "test"}}, {SourceID: "source", Body: []byte(`{"i":1}`)}, {SourceID: "source", Body: []byte(`{"i":2}`)}}
	result := publisher.Publish(t.Context(), records)
	if !errors.Is(result[0], failure) || result[1] != nil || result[2] != nil {
		t.Fatalf("misattributed acknowledgments: %v", result)
	}
	if !client.records[0].Timestamp.Equal(startTime()) {
		t.Fatal("historical timestamp changed")
	}
	found := false
	for _, h := range client.records[0].Headers {
		if h.Key == "custom" && string(h.Value) == "test" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom header lost")
	}
}
