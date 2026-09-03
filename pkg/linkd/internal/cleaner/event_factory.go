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
	"encoding/json"
	"fmt"

	"linkd/internal/config"
	"linkd/internal/domain"
)

// SeverityResolver 根据来源取值和 EventSource 配置生成 Linkd 标准 severity。
type SeverityResolver interface {
	Resolve(source config.EventSource, global config.SeverityConfig, sourceValue string) (string, error)
}

// FingerprintResolver 根据已经展开的 Event 字段和 EventSource 配置生成 fingerprint。
type FingerprintResolver interface {
	Resolve(source config.EventSource, event domain.Event) (string, error)
}

// DefaultSeverityResolver 实现 Linkd 默认的来源 severity 解析规则。
type DefaultSeverityResolver struct{}

// Resolve 按 mapping、全局同名、来源默认和全局默认的顺序解析 severity。
func (DefaultSeverityResolver) Resolve(
	source config.EventSource,
	global config.SeverityConfig,
	sourceValue string,
) (string, error) {
	return source.MapSeverity(sourceValue, global)
}

// DefaultFingerprintResolver 实现 EventSource field/fields fingerprint 规则。
type DefaultFingerprintResolver struct{}

// Resolve 从已经展开的 Event 稳定字段计算 fingerprint。
func (DefaultFingerprintResolver) Resolve(source config.EventSource, event domain.Event) (string, error) {
	return computeFingerprint(event, source)
}

// EventFactory 把来源 Cleaner 生成的 EventDraft 补全为受领域约束保护的 Event。
// 具体 Cleaner 不能覆盖租户、来源、Event ID、fingerprint、标准 severity、接收时间或原始 payload。
type EventFactory struct {
	source              config.EventSource
	severity            config.SeverityConfig
	severityResolver    SeverityResolver
	fingerprintResolver FingerprintResolver
}

// NewEventFactory 使用 Linkd 默认 resolver 创建 EventFactory。
func NewEventFactory(source config.EventSource, severity config.SeverityConfig) (*EventFactory, error) {
	return NewEventFactoryWithResolvers(
		source,
		severity,
		DefaultSeverityResolver{},
		DefaultFingerprintResolver{},
	)
}

// NewEventFactoryWithResolvers 使用显式 resolver 创建 EventFactory。
// resolver 只覆盖配置驱动算法，不改变 EventFactory 独占的系统字段。
func NewEventFactoryWithResolvers(
	source config.EventSource,
	severity config.SeverityConfig,
	severityResolver SeverityResolver,
	fingerprintResolver FingerprintResolver,
) (*EventFactory, error) {
	source = source.WithDefaults()
	severity = severity.WithDefaults()
	if err := config.ValidateEventSources([]config.EventSource{source}, severity); err != nil {
		return nil, fmt.Errorf("create event factory: %w", err)
	}
	if severityResolver == nil {
		return nil, fmt.Errorf("create event factory: severity resolver is required")
	}
	if fingerprintResolver == nil {
		return nil, fmt.Errorf("create event factory: fingerprint resolver is required")
	}
	return &EventFactory{
		source: source, severity: severity,
		severityResolver: severityResolver, fingerprintResolver: fingerprintResolver,
	}, nil
}

// Build 使用信封和 EventDraft 构造新的 Event，并执行统一规范化和领域终检。
func (f *EventFactory) Build(message RawEventMessage, draft EventDraft) (domain.Event, error) {
	if message.RecordID == "" {
		return domain.Event{}, fmt.Errorf("raw event record id is required")
	}
	if message.ReceivedAt.IsZero() {
		return domain.Event{}, fmt.Errorf("raw event received_at is required")
	}
	tenantID := message.BKTenantID
	if f.source.RelatedTenantID != "" {
		tenantID = f.source.RelatedTenantID
	}
	if tenantID == "" {
		return domain.Event{}, fmt.Errorf("event tenant is required")
	}
	if err := validateJSONObject(message.Payload); err != nil {
		return domain.Event{}, err
	}
	var sourceRawData domain.JSONObject
	if err := json.Unmarshal(message.Payload, &sourceRawData); err != nil {
		return domain.Event{}, fmt.Errorf("preserve source payload: %w", err)
	}
	severity, err := f.severityResolver.Resolve(f.source, f.severity, draft.SourceSeverity)
	if err != nil {
		return domain.Event{}, err
	}
	receivedAt := message.ReceivedAt.Round(0).UTC()
	producedAt := draft.ProducedAt
	if producedAt.IsZero() {
		producedAt = receivedAt
	}
	occurredAt := draft.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = receivedAt
	}
	stableSourceID := draft.SourceEventID
	if stableSourceID == "" {
		stableSourceID = message.RecordID
	}
	eventID, err := domain.GenerateEventID(tenantID, f.source.EventSourceID, stableSourceID, receivedAt)
	if err != nil {
		return domain.Event{}, err
	}
	event := domain.Event{
		BKTenantID: tenantID, EventSourceID: f.source.EventSourceID,
		EventID: eventID,
		Title:   draft.Title, Content: draft.Content, Severity: severity,
		Action: draft.Action, ActionReason: draft.ActionReason,
		ConditionKey: draft.ConditionKey, ConditionName: draft.ConditionName,
		Dimensions: draft.Dimensions.Clone(), SubjectSystem: draft.SubjectSystem,
		SubjectType: draft.SubjectType, SubjectID: draft.SubjectID, SubjectName: draft.SubjectName,
		OccurredAt: occurredAt, ProducedAt: producedAt, ReceivedAt: receivedAt, CreateAt: receivedAt,
		SourceEventID: draft.SourceEventID, SourceAlertID: draft.SourceAlertID,
		SourceRawData: sourceRawData, Labels: draft.Labels.Clone(), ExtraData: draft.ExtraData.Clone(),
	}
	event.Fingerprint, err = f.fingerprintResolver.Resolve(f.source, event)
	if err != nil {
		return domain.Event{}, err
	}
	normalized, err := event.Normalize()
	if err != nil {
		return domain.Event{}, fmt.Errorf("normalize Event: %w", err)
	}
	if err := domain.ValidateNewEvent(normalized); err != nil {
		return domain.Event{}, fmt.Errorf("validate new Event: %w", err)
	}
	return normalized, nil
}
