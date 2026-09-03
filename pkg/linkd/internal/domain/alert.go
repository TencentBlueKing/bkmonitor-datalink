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
	"time"
)

// Alert 表示一次异常从发生到结束的当前生命周期快照。
type Alert struct {
	AlertID       string       `json:"alert_id"`
	BKTenantID    string       `json:"bk_tenant_id"`
	EventSourceID string       `json:"event_source_id"`
	Fingerprint   string       `json:"fingerprint"`
	Title         string       `json:"title"`
	Content       string       `json:"content"`
	Severity      string       `json:"severity"`
	ConditionKey  string       `json:"condition_key"`
	ConditionName string       `json:"condition_name"`
	Dimensions    DimensionMap `json:"dimensions"`
	SubjectSystem string       `json:"subject_system"`
	SubjectType   string       `json:"subject_type"`
	SubjectID     string       `json:"subject_id"`
	SubjectName   string       `json:"subject_name"`
	SourceEventID string       `json:"source_event_id"`
	SourceAlertID string       `json:"source_alert_id"`
	Labels        DimensionMap `json:"labels"`
	ExtraData     JSONObject   `json:"extra_data,omitempty"`

	Status         AlertStatus  `json:"status"`
	LatestEventID  string       `json:"latest_event_id"`
	LastOccurredAt time.Time    `json:"last_occurred_at"`
	UpdateAt       time.Time    `json:"update_at"`
	TriggerEventID string       `json:"trigger_event_id"`
	BeginAt        time.Time    `json:"begin_at"`
	CreateAt       time.Time    `json:"create_at"`
	EndAt          *time.Time   `json:"end_at,omitempty"`
	EndType        AlertEndType `json:"end_type,omitempty"`
	EndReason      string       `json:"end_reason,omitempty"`

	EnrichStatus EnrichStatus `json:"enrich_status"`
	Enrich       JSONObject   `json:"enrich,omitempty"`
}

// Normalize 深拷贝动态字段、规范时间并校验 Alert。
func (a Alert) Normalize() (Alert, error) {
	a.Dimensions = a.Dimensions.Normalize()
	a.Labels = a.Labels.Normalize()
	var err error
	a.ExtraData, err = a.ExtraData.Normalize()
	if err != nil {
		return Alert{}, fmt.Errorf("alert extra_data: %w", err)
	}
	a.Enrich, err = a.Enrich.Normalize()
	if err != nil {
		return Alert{}, fmt.Errorf("alert enrich: %w", err)
	}
	a.LastOccurredAt = normalizeTime(a.LastOccurredAt)
	a.UpdateAt = normalizeTime(a.UpdateAt)
	a.BeginAt = normalizeTime(a.BeginAt)
	a.CreateAt = normalizeTime(a.CreateAt)
	a.EndAt = normalizeOptionalTime(a.EndAt)
	if err := a.Validate(); err != nil {
		return Alert{}, err
	}
	return a, nil
}

// Clone 返回不共享动态字段的 Alert 副本。
func (a Alert) Clone() Alert {
	a.Dimensions = a.Dimensions.Clone()
	a.Labels = a.Labels.Clone()
	a.ExtraData = a.ExtraData.Clone()
	a.Enrich = a.Enrich.Clone()
	a.EndAt = normalizeOptionalTime(a.EndAt)
	return a
}

// Validate 校验 Alert 字段及活动态、终态不变量。
func (a Alert) Validate() error {
	for _, field := range []struct {
		name  string
		value string
		min   int
		max   int
	}{
		{"alert_id", a.AlertID, 1, EntityIDMaxBytes},
		{"bk_tenant_id", a.BKTenantID, 1, 64},
		{"event_source_id", a.EventSourceID, 1, 32},
		{"fingerprint", a.Fingerprint, 1, 128},
		{"severity", a.Severity, 1, 32},
		{"latest_event_id", a.LatestEventID, 1, EntityIDMaxBytes},
		{"trigger_event_id", a.TriggerEventID, 1, EntityIDMaxBytes},
	} {
		if err := validateTextLength(field.name, field.value, field.min, field.max); err != nil {
			return err
		}
	}
	if err := ValidateIdentityPart("bk_tenant_id", a.BKTenantID, 64); err != nil {
		return err
	}
	if !eventSourceIDPattern.MatchString(a.EventSourceID) {
		return fmt.Errorf("alert event_source_id has invalid format: %q", a.EventSourceID)
	}
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{"content", a.Content, 1 << 20},
		{"title", a.Title, 256},
		{"condition_key", a.ConditionKey, 256},
		{"condition_name", a.ConditionName, 256},
		{"subject_system", a.SubjectSystem, 32},
		{"subject_type", a.SubjectType, 128},
		{"subject_id", a.SubjectID, 256},
		{"subject_name", a.SubjectName, 256},
		{"source_event_id", a.SourceEventID, 256},
		{"source_alert_id", a.SourceAlertID, 256},
		{"end_reason", a.EndReason, 256},
	} {
		if err := validateOptionalTextLength(field.name, field.value, field.max); err != nil {
			return err
		}
	}
	if !a.Status.Valid() {
		return fmt.Errorf("alert status is invalid: %q", a.Status)
	}
	if !a.EnrichStatus.Valid() {
		return fmt.Errorf("alert enrich_status is invalid: %q", a.EnrichStatus)
	}
	if err := a.Dimensions.Validate(); err != nil {
		return fmt.Errorf("alert dimensions: %w", err)
	}
	if err := a.Labels.Validate(); err != nil {
		return fmt.Errorf("alert labels: %w", err)
	}
	if _, err := a.ExtraData.Normalize(); err != nil {
		return fmt.Errorf("alert extra_data: %w", err)
	}
	if _, err := a.Enrich.Normalize(); err != nil {
		return fmt.Errorf("alert enrich: %w", err)
	}
	for name, value := range map[string]time.Time{
		"last_occurred_at": a.LastOccurredAt,
		"update_at":        a.UpdateAt,
		"begin_at":         a.BeginAt,
		"create_at":        a.CreateAt,
	} {
		if value.IsZero() {
			return fmt.Errorf("alert %s must not be zero", name)
		}
	}
	if a.Status == AlertStatusActive {
		if a.EndAt != nil || a.EndType != "" || a.EndReason != "" {
			return fmt.Errorf("active alert must not contain end fields")
		}
		return nil
	}
	if a.EndAt == nil || a.EndAt.IsZero() {
		return fmt.Errorf("terminal alert requires end_at")
	}
	if !a.EndType.Valid() {
		return fmt.Errorf("terminal alert end_type is invalid: %q", a.EndType)
	}
	if a.Status == AlertStatusRecovered && a.EndType != AlertEndTypeSource {
		return fmt.Errorf("recovered alert end_type must be source")
	}
	return nil
}

// ValidateAlertReplacement 校验 CAS 替换只修改生命周期字段且不会重新打开终态 Alert。
func ValidateAlertReplacement(current, replacement Alert) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("current alert: %w", err)
	}
	if err := replacement.Validate(); err != nil {
		return fmt.Errorf("replacement alert: %w", err)
	}
	left := current.Clone()
	right := replacement.Clone()
	clearLifecycle := func(alert *Alert) {
		alert.Status = ""
		alert.LatestEventID = ""
		alert.LastOccurredAt = time.Time{}
		alert.UpdateAt = time.Time{}
		alert.EndAt = nil
		alert.EndType = ""
		alert.EndReason = ""
	}
	clearLifecycle(&left)
	clearLifecycle(&right)
	if !reflect.DeepEqual(left, right) {
		return fmt.Errorf("alert replacement must preserve inherited and anchor fields")
	}
	if current.Status.Terminal() {
		return fmt.Errorf("terminal alert %q is immutable", current.Status)
	}
	if replacement.Status != AlertStatusActive && !replacement.Status.Terminal() {
		return fmt.Errorf("alert replacement status is invalid: %q", replacement.Status)
	}
	if !replacement.UpdateAt.After(current.UpdateAt) {
		return fmt.Errorf("update_at must increase strictly")
	}
	return nil
}
