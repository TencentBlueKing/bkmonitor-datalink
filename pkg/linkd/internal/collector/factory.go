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
	"fmt"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/kafkaclient"
	"linkd/internal/pluginruntime"
)

type ownedProducer interface {
	producer
	Close()
}

type producerEntry struct {
	client  ownedProducer
	version int64
	active  int
	retired bool
}

// Factory 装配同步执行服务并复用每个来源发布的 Kafka 连接。
// 配置查询不创建连接，首次实际发布才连接；旧连接在最后一个发布退出后关闭。
type Factory struct {
	results  ResultStore
	executor Executor
	open     func(config.KafkaStorageConfig) (ownedProducer, error)
	mu       sync.Mutex
	clients  map[string]*producerEntry
	closed   bool
}

// NewFactory 复用宿主执行客户端与进度存储，Kafka producer 使用全部 ISR 确认。
func NewFactory(executor Executor, results ResultStore) *Factory {
	return &Factory{executor: executor, results: results, clients: map[string]*producerEntry{}, open: func(cfg config.KafkaStorageConfig) (ownedProducer, error) {
		options, err := kafkaclient.ClientOptions(cfg.Brokers, "linkd-collector", cfg.Security)
		if err != nil {
			return nil, err
		}
		options = append(options, kgo.RequiredAcks(kgo.AllISRAcks()))
		return kgo.NewClient(options...)
	}}
}

// Build 创建一个绑定不可变 Release 的服务，未开始执行时不会访问 Kafka。
func (f *Factory) Build(_ context.Context, release eventsource.Release) (*Service, error) {
	service := New(f.executor, &releasePublisher{factory: f, release: release}, f.results)
	return service, nil
}

type releasePublisher struct {
	factory *Factory
	release eventsource.Release
}

func (p *releasePublisher) Publish(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) error {
	entry, err := p.factory.acquire(p.release)
	if err != nil {
		return err
	}
	defer p.factory.release(entry)
	writer := KafkaPublisher{producer: entry.client, topic: p.release.Spec.Storage.Kafka.Topic}
	return writer.Publish(ctx, request, result)
}

func (f *Factory) acquire(release eventsource.Release) (*producerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fmt.Errorf("collector factory is closed")
	}
	key := release.Spec.RelatedTenantID + ":" + release.ID
	entry := f.clients[key]
	if entry == nil || entry.version != release.Version {
		client, err := f.open(release.Spec.Storage.Kafka)
		if err != nil {
			return nil, fmt.Errorf("open collector Kafka producer: %w", err)
		}
		if entry != nil {
			entry.retired = true
			if entry.active == 0 {
				entry.client.Close()
			}
		}
		entry = &producerEntry{client: client, version: release.Version}
		f.clients[key] = entry
	}
	entry.active++
	return entry, nil
}

func (f *Factory) release(entry *producerEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry.active--
	if entry.retired && entry.active == 0 {
		entry.client.Close()
	}
}

// Close 阻止新发布并退休缓存连接；活动发布保留连接直到返回，必须先停止接入。
func (f *Factory) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	for _, entry := range f.clients {
		entry.retired = true
		if entry.active == 0 {
			entry.client.Close()
		}
	}
	f.clients = nil
}
