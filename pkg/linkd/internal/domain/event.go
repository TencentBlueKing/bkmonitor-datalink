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
	"slices"
	"time"
)

var eventSourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Event 是来源消息经过 SourceCleaner 和通用事件工厂标准化后的事件事实。
// Event 创建后只有 RelatedAlertIDs 可以由生命周期处理器写入，其他来源事实不可覆盖。
type Event struct {
	// EventSourceVersion 是创建时实际使用的不可变来源发布版本。
	EventSourceVersion int64    `json:"event_source_version"`
	BKTenantID         string   `json:"bk_tenant_id"`
	EventSourceID      string   `json:"event_source_id"`
	RelatedAlertIDs    []string `json:"related_alert_ids,omitempty"`
	EventID            string   `json:"event_id"`
	Fingerprint        string   `json:"fingerprint"`
	Title              string   `json:"title"`
	Content            string   `json:"content"`
	// Evaluations 记录各级别的来源判定；数组顺序不携带处理顺序。
	Evaluations []EventEvaluation `json:"evaluations"`
	Dimensions  DimensionMap      `json:"dimensions"`
	// Values 是本次事件的观测数值，创建后不可覆盖，也不参与 fingerprint。
	Values        EventValues  `json:"values"`
	SubjectSystem string       `json:"subject_system"`
	SubjectType   string       `json:"subject_type"`
	SubjectID     string       `json:"subject_id"`
	SubjectName   string       `json:"subject_name"`
	OccurredAt    time.Time    `json:"occurred_at"`
	ProducedAt    time.Time    `json:"produced_at"`
	ReceivedAt    time.Time    `json:"received_at"`
	CreateAt      time.Time    `json:"create_at"`
	SourceEventID string       `json:"source_event_id"`
	SourceAlertID string       `json:"source_alert_id"`
	SourceRawData JSONObject   `json:"source_raw_data,omitempty"`
	Labels        DimensionMap `json:"labels"`
	ExtraData     JSONObject   `json:"extra_data,omitempty"`
}

// Normalize 深拷贝动态字段、规范 UTC 时间并校验 Event。
func (e Event) Normalize() (Event, error) {
	e.Dimensions = e.Dimensions.Normalize()
	e.Evaluations = slices.Clone(e.Evaluations)
	slices.SortFunc(e.Evaluations, compareEvaluation)
	e.RelatedAlertIDs = normalizeAlertIDs(e.RelatedAlertIDs)
	e.Values = e.Values.Clone()
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
	if err := e.validate(false); err != nil {
		return Event{}, err
	}
	return e, nil
}

// Clone 返回不共享 map 或 JSON 字节的 Event 副本。
func (e Event) Clone() Event {
	e.Dimensions = e.Dimensions.Clone()
	e.Evaluations = slices.Clone(e.Evaluations)
	e.RelatedAlertIDs = slices.Clone(e.RelatedAlertIDs)
	e.Values = e.Values.Clone()
	e.Labels = e.Labels.Clone()
	e.SourceRawData = e.SourceRawData.Clone()
	e.ExtraData = e.ExtraData.Clone()
	return e
}

// Validate 校验 define.md 规定的 Event 字段和边界。
func (e Event) Validate() error {
	return e.validate(true)
}

// 只有 Normalize 完成所有动态 JSON 的校验和深拷贝后才跳过二次规范化。
// 公共 Validate 仍必须检查任意调用方传入的动态字段，不能信任对象曾经被规范化。
func (e Event) validate(validateJSON bool) error {
	if e.EventSourceVersion <= 0 {
		return fmt.Errorf("event_source_version must be positive")
	}
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
		{"title", e.Title, 256},
		{"content", e.Content, 1 << 20},
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
	if err := ValidateEvaluations(e.Evaluations); err != nil {
		return err
	}
	if len(e.RelatedAlertIDs) > 2 {
		return fmt.Errorf("event may associate at most two alerts")
	}
	for _, id := range e.RelatedAlertIDs {
		if err := validateTextLength("related_alert_id", id, 1, EntityIDMaxBytes); err != nil {
			return err
		}
	}
	if err := e.Dimensions.Validate(); err != nil {
		return fmt.Errorf("event dimensions: %w", err)
	}
	if err := e.Values.Validate(); err != nil {
		return fmt.Errorf("event values: %w", err)
	}
	if err := e.Labels.Validate(); err != nil {
		return fmt.Errorf("event labels: %w", err)
	}
	if validateJSON {
		if _, err := e.SourceRawData.Normalize(); err != nil {
			return fmt.Errorf("event source_raw_data: %w", err)
		}
		if _, err := e.ExtraData.Normalize(); err != nil {
			return fmt.Errorf("event extra_data: %w", err)
		}
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
	return validateNewEventState(event)
}

// ValidateNormalizedNewEvent 校验刚由 Event.Normalize 返回的 Event 是否可首次创建。
// 调用方必须在其间不暴露或修改动态字段；任意外部输入仍使用 ValidateNewEvent。
func ValidateNormalizedNewEvent(event Event) error {
	if err := event.validate(false); err != nil {
		return err
	}
	return validateNewEventState(event)
}

func validateNewEventState(event Event) error {
	if len(event.RelatedAlertIDs) != 0 {
		return fmt.Errorf("new event related_alert_ids must be empty")
	}
	return nil
}

// WithRelatedAlertIDs 返回生命周期关联结果，不允许重写已经提交的关联。
func (e Event) WithRelatedAlertIDs(alertIDs []string) (Event, error) {
	alertIDs = normalizeAlertIDs(alertIDs)
	if len(e.RelatedAlertIDs) > 0 && !slices.Equal(e.RelatedAlertIDs, alertIDs) {
		return Event{}, fmt.Errorf("event related_alert_ids are already set")
	}
	e = e.Clone()
	e.RelatedAlertIDs = alertIDs
	return e.Normalize()
}

func normalizeAlertIDs(ids []string) []string {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// ValidateEventReplacement 只允许生命周期处理器写入 RelatedAlertIDs。
func ValidateEventReplacement(current, replacement Event) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("current event: %w", err)
	}
	if err := replacement.Validate(); err != nil {
		return fmt.Errorf("replacement event: %w", err)
	}
	left := current.Clone()
	right := replacement.Clone()
	left.RelatedAlertIDs = nil
	right.RelatedAlertIDs = nil
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("event replacement must preserve source facts")
	}
	if len(current.RelatedAlertIDs) > 0 && !slices.Equal(current.RelatedAlertIDs, replacement.RelatedAlertIDs) {
		return fmt.Errorf("event related_alert_ids are immutable after association")
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

// ValidateEventRedelivery 允许跨发布重投复用已保存的 Event，但不允许同一身份携带不同原始事实。
// 这只用于 create 冲突核对；生命周期 CAS 仍使用 ValidateEventReplacement。
func ValidateEventRedelivery(incoming, stored Event) error {
	if incoming.EventSourceVersion != stored.EventSourceVersion && incoming.EventID == stored.EventID && incoming.BKTenantID == stored.BKTenantID && incoming.EventSourceID == stored.EventSourceID && incoming.ReceivedAt.Equal(stored.ReceivedAt) && incoming.SourceEventID == stored.SourceEventID && incoming.SourceAlertID == stored.SourceAlertID && len(incoming.SourceRawData) > 0 && reflect.DeepEqual(incoming.SourceRawData, stored.SourceRawData) {
		return nil
	}
	return ValidateEventReplacement(incoming, stored)
}
