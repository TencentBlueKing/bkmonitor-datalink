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
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	linkdconfig "linkd/internal/config"
	"linkd/internal/kafkaclient"
)

const (
	kafkaPublishChunkSize = 1_000
	kafkaBatchMaxBytes    = 1 << 20
)

type kafkaSyncClient interface {
	ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults
	Close()
}

// KafkaPublisher 把 eventgen Record 同步写入所选 EventSource 的 Kafka topic。
// 单次 Publish 会按固定消息数分块，避免把无限规模的周期一次性加入 producer 缓冲区。
type KafkaPublisher struct {
	client kafkaSyncClient
	topic  string
}

// NewKafkaPublisher 使用 EventSource 的 brokers、TLS 和 SASL 配置创建 producer。
func NewKafkaPublisher(source linkdconfig.EventSource) (*KafkaPublisher, error) {
	source = source.WithDefaults()
	options, err := kafkaclient.ClientOptions(source.Storage.Kafka.Brokers, "linkd-eventgen", source.Storage.Kafka.Security)
	if err != nil {
		return nil, fmt.Errorf("create event generator Kafka options: %w", err)
	}
	options = append(options,
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.MaxBufferedRecords(kafkaPublishChunkSize),
		kgo.ProducerBatchMaxBytes(kafkaBatchMaxBytes),
	)
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("create event generator Kafka producer: %w", err)
	}
	return &KafkaPublisher{client: client, topic: source.Storage.Kafka.Topic}, nil
}

func newKafkaPublisher(client kafkaSyncClient, topic string) *KafkaPublisher {
	return &KafkaPublisher{client: client, topic: topic}
}

// Publish 同步等待所有消息的最终发送结果；任意消息失败都会返回包含失败数量的错误。
func (p *KafkaPublisher) Publish(ctx context.Context, records []Record) error {
	for start := 0; start < len(records); start += kafkaPublishChunkSize {
		end := min(start+kafkaPublishChunkSize, len(records))
		batch := make([]*kgo.Record, 0, end-start)
		for _, record := range records[start:end] {
			batch = append(batch, &kgo.Record{
				Topic: p.topic,
				Key:   append([]byte(nil), record.Key...), Value: append([]byte(nil), record.Body...),
				Timestamp: record.Timestamp,
				Headers: []kgo.RecordHeader{
					{Key: "message_id", Value: []byte(record.Headers["message_id"])},
					{Key: "bk_tenant_id", Value: []byte(record.Headers["bk_tenant_id"])},
					{Key: "order_key", Value: []byte(record.Headers["order_key"])},
				},
			})
		}
		results := p.client.ProduceSync(ctx, batch...)
		failed := 0
		var firstErr error
		for _, result := range results {
			if result.Err == nil {
				continue
			}
			failed++
			if firstErr == nil {
				firstErr = result.Err
			}
		}
		if failed != 0 {
			return fmt.Errorf(
				"publish Kafka records: succeeded=%d failed=%d total=%d chunk_start=%d: %w",
				len(batch)-failed,
				failed,
				len(batch),
				start,
				firstErr,
			)
		}
	}
	return nil
}

// Close 排空并关闭底层 Kafka producer。
func (p *KafkaPublisher) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}

var _ Publisher = (*KafkaPublisher)(nil)
