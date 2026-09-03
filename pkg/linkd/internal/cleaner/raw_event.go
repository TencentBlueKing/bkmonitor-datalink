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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"linkd/internal/domain"
)

// RawEventMessage 是传输适配器交给来源 Cleaner 的只读原始告警信封。
type RawEventMessage struct {
	RecordID      string
	BKTenantID    string
	EventSourceID string
	ReceivedAt    time.Time
	Payload       []byte
	Headers       map[string][]byte
}

// EventDraft 是来源 Cleaner 从 payload 中提取的来源事实。
// 租户、来源、Event ID、fingerprint、标准 severity、接收时间和原始 payload
// 由 EventFactory 统一生成或保存，具体 Cleaner 不得覆盖。
type EventDraft struct {
	Title          string
	Content        string
	SourceSeverity string
	Action         domain.EventAction
	ActionReason   string
	ConditionKey   string
	ConditionName  string
	Dimensions     domain.DimensionMap
	SubjectSystem  string
	SubjectType    string
	SubjectID      string
	SubjectName    string
	OccurredAt     time.Time
	ProducedAt     time.Time
	SourceEventID  string
	SourceAlertID  string
	Labels         domain.DimensionMap
	ExtraData      domain.JSONObject
}

// SourceCleaner 把一种来源 payload 确定性投影为 EventDraft。
type SourceCleaner interface {
	Clean(ctx context.Context, message RawEventMessage) (EventDraft, error)
}

type standardSubject struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
}

type standardPayload struct {
	EventID       string              `json:"event_id"`
	AlertID       string              `json:"alert_id"`
	Title         string              `json:"title"`
	Content       string              `json:"content"`
	Severity      string              `json:"severity"`
	Action        domain.EventAction  `json:"action"`
	ActionReason  string              `json:"action_reason"`
	ConditionKey  string              `json:"condition_key"`
	ConditionName string              `json:"condition_name"`
	Dimensions    domain.DimensionMap `json:"dimensions"`
	Subject       standardSubject     `json:"subject"`
	OccurredAt    time.Time           `json:"occurred_at"`
	ProducedAt    time.Time           `json:"produced_at"`
	Labels        domain.DimensionMap `json:"labels"`
	ExtraData     domain.JSONObject   `json:"extra_data"`
}

// StandardCleaner 解析 Linkd 标准事件 payload，并把来源字段投影为 EventDraft。
// 未知字段只由 EventFactory 保存在 SourceRawData 中，不进入 EventDraft。
type StandardCleaner struct{}

func (StandardCleaner) Clean(ctx context.Context, message RawEventMessage) (EventDraft, error) {
	if err := ctx.Err(); err != nil {
		return EventDraft{}, err
	}
	if err := validateJSONObject(message.Payload); err != nil {
		return EventDraft{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Payload))
	var payload standardPayload
	if err := decoder.Decode(&payload); err != nil {
		return EventDraft{}, fmt.Errorf("decode standard payload: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return EventDraft{}, err
	}
	if !payload.Action.Valid() {
		return EventDraft{}, fmt.Errorf("standard action is invalid: %q", payload.Action)
	}
	if err := payload.Dimensions.Validate(); err != nil {
		return EventDraft{}, fmt.Errorf("standard dimensions: %w", err)
	}
	if err := payload.Labels.Validate(); err != nil {
		return EventDraft{}, fmt.Errorf("standard labels: %w", err)
	}
	return EventDraft{
		Title: payload.Title, Content: payload.Content,
		SourceSeverity: payload.Severity, Action: payload.Action, ActionReason: payload.ActionReason,
		ConditionKey: payload.ConditionKey, ConditionName: payload.ConditionName,
		Dimensions: payload.Dimensions.Clone(), SubjectSystem: payload.Subject.System,
		SubjectType: payload.Subject.Type, SubjectID: payload.Subject.ID, SubjectName: payload.Subject.Name,
		OccurredAt: payload.OccurredAt, ProducedAt: payload.ProducedAt,
		SourceEventID: payload.EventID, SourceAlertID: payload.AlertID,
		Labels: payload.Labels.Clone(), ExtraData: payload.ExtraData.Clone(),
	}, nil
}

func validateJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode source JSON: %w", err)
	}
	if token != json.Delim('{') {
		return fmt.Errorf("source payload must be a JSON object")
	}
	if err := validateObjectBody(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("source payload contains trailing JSON data")
		}
		return fmt.Errorf("decode trailing source JSON: %w", err)
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return validateObjectBody(decoder)
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array closing delimiter")
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func validateObjectBody(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("JSON object key must be a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("source payload contains duplicate key %q", key)
		}
		seen[key] = struct{}{}
		if err := validateJSONValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("invalid JSON object closing delimiter")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == nil {
		return fmt.Errorf("source payload contains trailing JSON data")
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing source JSON: %w", err)
	}
	return nil
}

var _ SourceCleaner = StandardCleaner{}
