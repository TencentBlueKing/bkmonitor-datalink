// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/pluginruntime"
)

type recordingProducer struct {
	records []*kgo.Record
	err     error
}

func (p *recordingProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.records = records
	result := make(kgo.ProduceResults, len(records))
	for i, r := range records {
		result[i] = kgo.ProduceResult{Record: r, Err: p.err}
	}
	return result
}

func TestKafkaPublication(t *testing.T) {
	producer := &recordingProducer{}
	publisher := &KafkaPublisher{producer: producer, topic: "source-input"}
	request := pluginruntime.Request{TenantID: "tenant", RequestID: "receipt", ReceivedAt: "2026-11-14T12:00:00Z"}
	result := pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"alert"}`)}}
	if err := publisher.Publish(t.Context(), request, result); err != nil {
		t.Fatal(err)
	}
	record := producer.records[0]
	if len(record.Headers) != 2 || record.Headers[1].Key != "message_id" || string(record.Headers[1].Value) != "collector/receipt/0" {
		t.Fatal("stable record identity missing")
	}
	if record.Topic != "source-input" || string(record.Key) != "tenant:alert" || string(record.Headers[0].Value) != "tenant" || record.Timestamp.Unix() != 1794657600 {
		t.Fatalf("incorrect Kafka envelope: %+v", record)
	}
	producer.err = errors.New("broker unavailable")
	if err := publisher.Publish(t.Context(), request, result); !errors.Is(err, producer.err) {
		t.Fatalf("broker failure lost: %v", err)
	}
	request.ReceivedAt = "invalid"
	if err := publisher.Publish(t.Context(), request, result); err == nil {
		t.Fatal("invalid timestamp accepted")
	}
}

func TestKafkaPublicationErrorClassification(t *testing.T) {
	for _, cause := range []error{kerr.TopicAuthorizationFailed, kerr.RequestTimedOut, errors.New("transport failure")} {
		p := &KafkaPublisher{producer: &recordingProducer{err: cause}, topic: "source"}
		err := p.Publish(t.Context(), pluginruntime.Request{RequestID: "r", ReceivedAt: "2026-11-14T12:00:00Z"}, pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}})
		var permanent *permanentPublicationError
		if errors.As(err, &permanent) != errors.Is(cause, kerr.TopicAuthorizationFailed) || !errors.Is(err, cause) {
			t.Fatalf("wrong classification: %v", err)
		}
	}
}
