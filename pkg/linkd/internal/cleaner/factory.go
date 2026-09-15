// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/consume"
	consumekafka "linkd/internal/consume/kafka"
)

// Factory 为每个启用 EventSource 创建独立 Kafka Session、Mapper、Handler 和 Runtime。
type Factory struct {
	events        EventBatchWriter
	mailboxes     MailboxWriter
	receiveGate   ReceiveGate
	logger        Logger
	runtimeConfig config.CleanerRuntimeConfig
	severity      config.SeverityConfig
	observer      func(config.EventSource) consume.Observer
}

// NewFactory 创建默认 cleaner FlowFactory。
func NewFactory(
	events EventBatchWriter,
	mailboxes MailboxWriter,
	receiveGate ReceiveGate,
	logger Logger,
	runtimeConfig config.CleanerRuntimeConfig,
	severity config.SeverityConfig,
	observerFactory func(config.EventSource) consume.Observer,
) (*Factory, error) {
	for name, dependency := range map[string]any{
		"event_writer":   events,
		"mailbox_writer": mailboxes,
		"receive_gate":   receiveGate,
		"logger":         logger,
	} {
		if dependency == nil {
			return nil, fmt.Errorf("create cleaner factory: %s must not be nil", name)
		}
	}
	if err := runtimeConfig.Validate(); err != nil {
		return nil, fmt.Errorf("create cleaner factory: runtime config: %w", err)
	}
	if err := severity.Validate(); err != nil {
		return nil, fmt.Errorf("create cleaner factory: severity: %w", err)
	}
	return &Factory{
		events: events, mailboxes: mailboxes, receiveGate: receiveGate, logger: logger, runtimeConfig: runtimeConfig,
		severity: severity.WithDefaults(), observer: observerFactory,
	}, nil
}

// NewFlow 为一个来源创建长生命周期 Kafka 清洗流程。
func (f *Factory) NewFlow(ctx context.Context, source config.EventSource) (Flow, error) {
	if ctx == nil {
		return nil, fmt.Errorf("create cleaner flow: context must not be nil")
	}
	mapper, err := NewMapper(source, f.severity)
	if err != nil {
		return nil, err
	}
	processor, err := NewMapperProcessor(mapper)
	if err != nil {
		return nil, err
	}
	session, err := consumekafka.NewSession(kafkaConfig(source))
	if err != nil {
		return nil, fmt.Errorf("create cleaner Kafka session for source %q: %w", source.EventSourceID, err)
	}
	runtimeConfig := source.Cleaner.RuntimeConfig(f.runtimeConfig)
	var observer consume.Observer
	if f.observer != nil {
		observer = f.observer(source)
	}
	runtime, err := NewRuntime(runtimeConfig, session, processor, f.events, f.mailboxes, f.receiveGate, f.logger, observer)
	if err != nil {
		_ = session.Close(context.Background())
		return nil, err
	}
	return FlowFunc(runtime.Run), nil
}

func kafkaConfig(source config.EventSource) consumekafka.Config {
	storage := source.WithDefaults().Storage.Kafka
	return consumekafka.Config{
		Brokers:       append([]string(nil), storage.Brokers...),
		Topic:         storage.Topic,
		ConsumerGroup: storage.ConsumerGroup,
		ClientID:      "linkd-cleaner-" + source.EventSourceID,
		Security:      storage.Security.Clone(),
	}
}

var _ FlowFactory = (*Factory)(nil)
