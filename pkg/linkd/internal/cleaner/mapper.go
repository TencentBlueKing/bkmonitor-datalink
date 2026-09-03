// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
)

const invalidRawEventTenantID = "_invalid_raw_event"

// Mapper 为一个 EventSource 组合来源 Cleaner 与通用 EventFactory。
type Mapper struct {
	cleaner SourceCleaner
	factory *EventFactory
}

// NewMapper 创建并冻结一个来源的清洗链路。
func NewMapper(source config.EventSource, severity config.SeverityConfig) (*Mapper, error) {
	factory, err := NewEventFactory(source, severity)
	if err != nil {
		return nil, err
	}
	source = source.WithDefaults()
	cleaner, err := registeredCleaner(source.Cleaner.Type)
	if err != nil {
		return nil, err
	}
	return &Mapper{cleaner: cleaner, factory: factory}, nil
}

func registeredCleaner(cleanerType string) (SourceCleaner, error) {
	switch cleanerType {
	case config.CleanerTypeStandard:
		return StandardCleaner{}, nil
	default:
		return nil, fmt.Errorf("source cleaner is not registered: %q", cleanerType)
	}
}

// MapMessage 执行 RawEventMessage -> EventDraft -> Event。
func (m *Mapper) MapMessage(ctx context.Context, message consume.Message) (domain.Event, error) {
	raw := RawEventMessage{
		RecordID: message.ID, BKTenantID: message.TenantID, EventSourceID: m.factory.source.EventSourceID,
		ReceivedAt: message.EnqueuedAt, Payload: append([]byte(nil), message.Body...), Headers: cloneHeaders(message.Headers),
	}
	draft, err := m.cleaner.Clean(ctx, raw)
	if err != nil {
		return domain.Event{}, err
	}
	return m.factory.Build(raw, draft)
}

// PrepareMessage 预计算租户和 order key，使同一 fingerprint 在单进程内串行。
func (m *Mapper) PrepareMessage(message consume.Message) (consume.Message, error) {
	event, err := m.MapMessage(context.Background(), message)
	if err != nil {
		if message.TenantID == "" {
			message.TenantID = invalidRawEventTenantID
		}
		return message, err
	}
	message.TenantID = event.BKTenantID
	message.OrderKey = scheduler.CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint)
	return message, nil
}

func cloneHeaders(headers map[string][]byte) map[string][]byte {
	if headers == nil {
		return nil
	}
	cloned := make(map[string][]byte, len(headers))
	for key, value := range headers {
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}
