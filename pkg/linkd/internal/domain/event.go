// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"fmt"
	"reflect"
	"regexp"
	"time"
)

var eventSourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Event 是来源消息经过 SourceCleaner 和通用事件工厂标准化后的事件事实。
// Event 创建后只有 RelatedAlertID 可以由生命周期处理器写入，其他来源事实不可覆盖。
type Event struct {
	BKTenantID     string       `json:"bk_tenant_id"`
	EventSourceID  string       `json:"event_source_id"`
	RelatedAlertID string       `json:"related_alert_id,omitempty"`
	EventID        string       `json:"event_id"`
	Fingerprint    string       `json:"fingerprint"`
	Title          string       `json:"title"`
	Content        string       `json:"content"`
	Severity       string       `json:"severity"`
	Action         EventAction  `json:"action"`
	ActionReason   string       `json:"action_reason"`
	ConditionKey   string       `json:"condition_key"`
	ConditionName  string       `json:"condition_name"`
	Dimensions     DimensionMap `json:"dimensions"`
	SubjectSystem  string       `json:"subject_system"`
	SubjectType    string       `json:"subject_type"`
	SubjectID      string       `json:"subject_id"`
	SubjectName    string       `json:"subject_name"`
	OccurredAt     time.Time    `json:"occurred_at"`
	ProducedAt     time.Time    `json:"produced_at"`
	ReceivedAt     time.Time    `json:"received_at"`
	CreateAt       time.Time    `json:"create_at"`
	SourceEventID  string       `json:"source_event_id"`
	SourceAlertID  string       `json:"source_alert_id"`
	SourceRawData  JSONObject   `json:"source_raw_data,omitempty"`
	Labels         DimensionMap `json:"labels"`
	ExtraData      JSONObject   `json:"extra_data,omitempty"`
}

// Normalize 深拷贝动态字段、规范 UTC 时间并校验 Event。
func (e Event) Normalize() (Event, error) {
	e.Dimensions = e.Dimensions.Normalize()
	e.Labels = e.Labels.Normalize()
	var err error
	e.SourceRawData, err = e.SourceRawData.Normalize()
	if err != nil {
		return Event{}, fmt.Errorf("event source_raw_data: %w", err)
	}
	e.ExtraData, err = e.ExtraData.Normalize()
	if err != nil {
		return Event{}, fmt.Errorf("event extra_data: %w", err)
	}
	e.OccurredAt = normalizeTime(e.OccurredAt)
	e.ProducedAt = normalizeTime(e.ProducedAt)
	e.ReceivedAt = normalizeTime(e.ReceivedAt)
	e.CreateAt = normalizeTime(e.CreateAt)
	if err := e.Validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

// Clone 返回不共享 map 或 JSON 字节的 Event 副本。
func (e Event) Clone() Event {
	e.Dimensions = e.Dimensions.Clone()
	e.Labels = e.Labels.Clone()
	e.SourceRawData = e.SourceRawData.Clone()
	e.ExtraData = e.ExtraData.Clone()
	return e
}

// Validate 校验 define.md 规定的 Event 字段和边界。
func (e Event) Validate() error {
	for _, field := range []struct {
		name  string
		value string
		min   int
		max   int
	}{
		{"bk_tenant_id", e.BKTenantID, 1, 64},
		{"event_source_id", e.EventSourceID, 1, 32},
		{"event_id", e.EventID, 1, EntityIDMaxBytes},
		{"fingerprint", e.Fingerprint, 1, 128},
		{"severity", e.Severity, 1, 32},
	} {
		if err := validateTextLength(field.name, field.value, field.min, field.max); err != nil {
			return err
		}
	}
	if err := ValidateIdentityPart("bk_tenant_id", e.BKTenantID, 64); err != nil {
		return err
	}
	if !eventSourceIDPattern.MatchString(e.EventSourceID) {
		return fmt.Errorf("event event_source_id has invalid format: %q", e.EventSourceID)
	}
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{"related_alert_id", e.RelatedAlertID, 256},
		{"title", e.Title, 256},
		{"content", e.Content, 1 << 20},
		{"action_reason", e.ActionReason, 256},
		{"condition_key", e.ConditionKey, 256},
		{"condition_name", e.ConditionName, 256},
		{"subject_system", e.SubjectSystem, 32},
		{"subject_type", e.SubjectType, 128},
		{"subject_id", e.SubjectID, 256},
		{"subject_name", e.SubjectName, 256},
		{"source_event_id", e.SourceEventID, 256},
		{"source_alert_id", e.SourceAlertID, 256},
	} {
		if err := validateOptionalTextLength(field.name, field.value, field.max); err != nil {
			return err
		}
	}
	if !e.Action.Valid() {
		return fmt.Errorf("event action is invalid: %q", e.Action)
	}
	if err := e.Dimensions.Validate(); err != nil {
		return fmt.Errorf("event dimensions: %w", err)
	}
	if err := e.Labels.Validate(); err != nil {
		return fmt.Errorf("event labels: %w", err)
	}
	if _, err := e.SourceRawData.Normalize(); err != nil {
		return fmt.Errorf("event source_raw_data: %w", err)
	}
	if _, err := e.ExtraData.Normalize(); err != nil {
		return fmt.Errorf("event extra_data: %w", err)
	}
	for name, value := range map[string]time.Time{
		"occurred_at": e.OccurredAt,
		"produced_at": e.ProducedAt,
		"received_at": e.ReceivedAt,
		"create_at":   e.CreateAt,
	} {
		if value.IsZero() {
			return fmt.Errorf("event %s must not be zero", name)
		}
	}
	return nil
}

// ValidateNewEvent 校验 Event 是否可以由 Cleaner 首次创建。
func ValidateNewEvent(event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.RelatedAlertID != "" {
		return fmt.Errorf("new event related_alert_id must be empty")
	}
	return nil
}

// WithRelatedAlertID 返回写入生命周期关联结果后的 Event 副本。
func (e Event) WithRelatedAlertID(alertID string) (Event, error) {
	if e.RelatedAlertID != "" && e.RelatedAlertID != alertID {
		return Event{}, fmt.Errorf("event related_alert_id is already set")
	}
	e = e.Clone()
	e.RelatedAlertID = alertID
	return e.Normalize()
}

// ValidateEventReplacement 只允许生命周期处理器写入 RelatedAlertID。
func ValidateEventReplacement(current, replacement Event) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("current event: %w", err)
	}
	if err := replacement.Validate(); err != nil {
		return fmt.Errorf("replacement event: %w", err)
	}
	left := current.Clone()
	right := replacement.Clone()
	left.RelatedAlertID = ""
	right.RelatedAlertID = ""
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("event replacement must preserve source facts")
	}
	if current.RelatedAlertID != "" && current.RelatedAlertID != replacement.RelatedAlertID {
		return fmt.Errorf("event related_alert_id is immutable after association")
	}
	return nil
}

func validateTextLength(name, value string, minLength, maxLength int) error {
	length := len(value)
	if length < minLength || length > maxLength {
		return fmt.Errorf("%s length must be between %d and %d bytes", name, minLength, maxLength)
	}
	return nil
}

func validateOptionalTextLength(name, value string, maxLength int) error {
	if len(value) > maxLength {
		return fmt.Errorf("%s length must not exceed %d bytes", name, maxLength)
	}
	return nil
}
