// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"bytes"
	"context"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestComparisonAuditProducerCoordinatesKeepAuditTopicIsolated(t *testing.T) {
	t.Parallel()

	coordinates := ComparisonAuditSinkConfig{
		Brokers:             []string{"127.0.0.1:9092"},
		InputTopics:         []string{"trigger-input", "go-decision", "python-decision"},
		OutputTopic:         "comparison-audit",
		AllowedOutputTopics: []string{"comparison-audit"},
		ClientID:            "alarm-engine-comparator",
		BrokerVersion:       "2.6.0",
	}
	config, err := NewComparisonAuditProducerConfig(coordinates)
	if err != nil {
		t.Fatalf("NewComparisonAuditProducerConfig() error = %v", err)
	}
	if config.Producer.MaxMessageBytes != contract.MaxComparisonAuditBytesV1+decisionProducerMessageOverheadCap {
		t.Fatalf("Producer.MaxMessageBytes = %d", config.Producer.MaxMessageBytes)
	}

	coordinates.OutputTopic = "python-decision"
	coordinates.AllowedOutputTopics = []string{"python-decision"}
	if _, err := NewComparisonAuditProducerConfig(coordinates); err == nil {
		t.Fatal("NewComparisonAuditProducerConfig() accepted an input topic as output")
	}
}

func TestComparisonAuditSinkPublishesOfficialWireBeforeInputCommit(t *testing.T) {
	t.Parallel()

	var produced *sarama.ProducerMessage
	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		produced = message
		return 0, 1, nil
	}}
	sink, err := newComparisonAuditSink("comparison-audit", producer, &fakeCloser{})
	if err != nil {
		t.Fatalf("newComparisonAuditSink() error = %v", err)
	}
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		if produced == nil {
			t.Fatal("input offset committed before the audit producer ACK")
		}
		events = append(events, "input-offset-ack")
		return nil
	})
	coordinator, _, _, _ := setupComparatorRecordCoordinatorWithAudit(t, 100, committer, sink, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if produced == nil || produced.Topic != "comparison-audit" {
		t.Fatalf("produced message = %#v", produced)
	}
	encoded, err := produced.Value.Encode()
	if err != nil {
		t.Fatalf("Value.Encode() error = %v", err)
	}
	batch, err := contract.DecodeComparisonAuditBatch(encoded)
	if err != nil {
		t.Fatalf("DecodeComparisonAuditBatch() error = %v", err)
	}
	wantKey, err := batch.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	encodedKey, err := produced.Key.Encode()
	if err != nil || !bytes.Equal(encodedKey, wantKey) {
		t.Fatalf("produced key = %x, error=%v, want %x", encodedKey, err, wantKey)
	}
}
