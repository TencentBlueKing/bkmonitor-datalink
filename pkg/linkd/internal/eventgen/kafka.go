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
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/config"
	"linkd/internal/kafkaclient"
)

type kafkaSyncClient interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
	Close()
}

type kafkaTarget struct {
	client kafkaSyncClient
	topic  string
}

// KafkaPublisher 复用来源连接；同一来源内保持输入顺序，并逐条返回最终 ACK。
type KafkaPublisher struct {
	targets     map[string]kafkaTarget
	concurrency int
}

// NewKafkaPublisher 仅接受控制面已解析的 Kafka 来源，不读取任意请求中的连接参数。
func NewKafkaPublisher(sources map[string]config.EventSource, limits Limits) (*KafkaPublisher, error) {
	limits = (Config{Limits: limits}).defaults().Limits
	p := &KafkaPublisher{concurrency: limits.SendConcurrency, targets: map[string]kafkaTarget{}}
	for id, source := range sources {
		source = source.WithDefaults()
		if source.Storage.Type != config.StorageTypeKafka {
			p.Close()
			return nil, fmt.Errorf("source %s does not use Kafka", id)
		}
		opts, err := kafkaclient.ClientOptions(source.Storage.Kafka.Brokers, "linkd-eventgen", source.Storage.Kafka.Security)
		if err != nil {
			p.Close()
			return nil, err
		}
		//nolint:gosec // G115: batch 字节数在这里限定到正 int32 范围。
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()), kgo.RecordRetries(2), kgo.MaxBufferedRecords(limits.QueueRecords), kgo.MaxBufferedBytes(int(limits.QueueBytes)), kgo.ProducerBatchMaxBytes(int32(min(limits.MaxMessageBytes+1024, 1<<30))))
		client, err := kgo.NewClient(opts...)
		if err != nil {
			p.Close()
			return nil, err
		}
		p.targets[id] = kafkaTarget{client, source.Storage.Kafka.Topic}
	}
	return p, nil
}

// Publish 不重试整个批次，底层 producer 最多尝试三次；重试始终使用原始消息。
// 发送超时由调用 Context 限定。franz-go 的 RecordDeliveryTimeout 基于消息 Timestamp，
// 不能用于允许历史时间和显式延迟的模拟事件，否则会在发送前误判超时。
func (p *KafkaPublisher) Publish(ctx context.Context, records []Record) []error {
	result := make([]error, len(records))
	bySource := map[string][]int{}
	order := []string{}
	for i, r := range records {
		if _, ok := bySource[r.SourceID]; !ok {
			order = append(order, r.SourceID)
		}
		bySource[r.SourceID] = append(bySource[r.SourceID], i)
	}
	send := func(id string) {
		indices := bySource[id]
		target, ok := p.targets[id]
		if !ok {
			for _, i := range indices {
				result[i] = fmt.Errorf("source unavailable")
			}
			return
		}
		batch := make([]*kgo.Record, 0, len(indices))
		for _, i := range indices {
			r := records[i]
			headers := make([]kgo.RecordHeader, 0, len(r.Headers))
			keys := make([]string, 0, len(r.Headers))
			for k := range r.Headers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				headers = append(headers, kgo.RecordHeader{Key: k, Value: []byte(r.Headers[k])})
			}
			batch = append(batch, &kgo.Record{Topic: target.topic, Key: r.Key, Value: r.Body, Timestamp: r.Timestamp, Headers: headers})
		}
		// ProduceSync 按回调完成顺序返回，不能把结果下标当成输入下标；跨分区会乱序。
		positions := make(map[*kgo.Record]int, len(batch))
		for n, record := range batch {
			positions[record] = indices[n]
			result[indices[n]] = fmt.Errorf("kafka result unavailable")
		}
		for _, produced := range target.client.ProduceSync(ctx, batch...) {
			result[positions[produced.Record]] = produced.Err
		}

	}
	if p.concurrency <= 1 || len(order) <= 1 {
		for _, id := range order {
			send(id)
		}
	} else {
		var wg sync.WaitGroup
		jobs := make(chan string)
		for range min(p.concurrency, len(order)) {
			wg.Go(func() {
				for id := range jobs {
					send(id)
				}
			})
		}
		for _, id := range order {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
	}

	return result
}

// Close 释放当前运行的 producer；Run 保证发送协程已退出。
func (p *KafkaPublisher) Close() {
	if p != nil {
		for _, target := range p.targets {
			target.client.Close()
		}
	}
}

// NewRunID 由显式启动建立独立测试命名空间，重试事件不调用它。
func NewRunID() string { return uuid.NewString() }
