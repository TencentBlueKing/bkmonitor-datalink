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
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// Alert 表示一次异常从发生到结束的当前生命周期快照。
type Alert struct {
	// Revision 是业务快照的单调版本，首次为 1；与仓储 CAS token 及输出确认元数据无关。
	Revision int64 `json:"revision"`
	// Projection 保存可靠投影的目标要求及确认水位；ACK 不推进业务 Revision。
	Projection AlertProjection `json:"projection"`
	// ActionPending 保留同次业务写入的获准动作；入队确认前不得推进下一业务版本。
	ActionPending *AlertActionIntent `json:"action_pending,omitempty"`
	// EventSourceVersion 是创建时实际使用的不可变来源发布版本。
	EventSourceVersion int64        `json:"event_source_version"`
	AlertID            string       `json:"alert_id"`
	BKTenantID         string       `json:"bk_tenant_id"`
	EventSourceID      string       `json:"event_source_id"`
	Fingerprint        string       `json:"fingerprint"`
	Title              string       `json:"title"`
	Content            string       `json:"content"`
	Severity           string       `json:"severity"`
	Dimensions         DimensionMap `json:"dimensions"`
	SubjectSystem      string       `json:"subject_system"`
	SubjectType        string       `json:"subject_type"`
	SubjectID          string       `json:"subject_id"`
	SubjectName        string       `json:"subject_name"`
	SourceEventID      string       `json:"source_event_id"`
	SourceAlertID      string       `json:"source_alert_id"`
	Labels             DimensionMap `json:"labels"`
	ExtraData          JSONObject   `json:"extra_data,omitempty"`

	// Shield 只表示当前屏蔽关系，不替代 active/recovered/closed。
	Shield AlertShield `json:"shield"`
	// Merge 保存独立的合并角色、窗口等待和关系摘要；nil 表示原始告警未参与合并。
	Merge *AlertMerge `json:"merge,omitempty"`
	// PolicyChange 保存控制面状态变更尚待补齐的输出意图，完成时仅清除此元数据。
	PolicyChange *AlertPolicyChange `json:"policy_change,omitempty"`
	// MergeChange 保存合并状态变更后的待完成流水与输出意图。
	MergeChange *AlertMergeChange `json:"merge_change,omitempty"`
	// Admission 记录已发生的处置放行，解除屏蔽不会自动设置它。
	Admission AlertAdmission `json:"admission"`
	// PolicyTags 是策略明确追加的 KAC 标签 ID，与 opening Event 的普通标签分开。
	PolicyTags     []int64      `json:"policy_tags,omitempty"`
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
	a.ActionPending = a.ActionPending.Clone()
	a.Projection = a.Projection.Clone()
	a.PolicyChange = a.PolicyChange.Clone()
	a.MergeChange = a.MergeChange.Clone()
	a.Shield = a.Shield.Clone()
	a.Merge = a.Merge.Clone()
	a.Admission = a.Admission.Clone()
	a.PolicyTags = slices.Clone(a.PolicyTags)
	slices.Sort(a.PolicyTags)
	a.PolicyTags = slices.Compact(a.PolicyTags)
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
	if err := a.validate(false); err != nil {
		return Alert{}, err
	}
	return a, nil
}

// Clone 返回不共享动态字段的 Alert 副本。
func (a Alert) Clone() Alert {
	a.ActionPending = a.ActionPending.Clone()
	a.Projection = a.Projection.Clone()
	a.PolicyChange = a.PolicyChange.Clone()
	a.MergeChange = a.MergeChange.Clone()
	a.Shield = a.Shield.Clone()
	a.Merge = a.Merge.Clone()
	a.Admission = a.Admission.Clone()
	a.PolicyTags = slices.Clone(a.PolicyTags)
	a.Dimensions = a.Dimensions.Clone()
	a.Labels = a.Labels.Clone()
	a.ExtraData = a.ExtraData.Clone()
	a.Enrich = a.Enrich.Clone()
	a.EndAt = normalizeOptionalTime(a.EndAt)
	return a
}

// Validate 校验 Alert 字段及活动态、终态不变量。
func (a Alert) Validate() error {
	return a.validate(true)
}

// Normalize 已校验并深拷贝动态 JSON；公共 Validate 不得走此跳过路径。
func (a Alert) validate(validateJSON bool) error {
	if err := a.Projection.Validate(a.Revision); err != nil {
		return err
	}
	if err := a.ActionPending.Validate(a); err != nil {
		return err
	}
	if a.Revision < 1 || a.Revision >= 1<<53 {
		return fmt.Errorf("alert revision must be within 1..2^53-1")
	}
	if err := a.MergeChange.Validate(a); err != nil {
		return err
	}
	if err := a.PolicyChange.Validate(a); err != nil {
		return err
	}
	if err := a.Shield.Validate(a.AlertID, a.Status); err != nil {
		return err
	}
	if err := a.Merge.Validate(a.Status); err != nil {
		return err
	}
	if err := a.Admission.Validate(); err != nil {
		return err
	}
	if len(a.PolicyTags) > 256 {
		return fmt.Errorf("policy tags exceed budget")
	}
	for i, id := range a.PolicyTags {
		if id < 1 || id >= 1<<53 || (i > 0 && a.PolicyTags[i-1] >= id) {
			return fmt.Errorf("invalid or unordered policy tags")
		}
	}
	if a.EventSourceVersion <= 0 {
		return fmt.Errorf("event_source_version must be positive")
	}
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
	if validateJSON {
		if _, err := a.ExtraData.Normalize(); err != nil {
			return fmt.Errorf("alert extra_data: %w", err)
		}
		if _, err := a.Enrich.Normalize(); err != nil {
			return fmt.Errorf("alert enrich: %w", err)
		}
	}
	if err := ValidateEnrichPayload(a.EnrichStatus, a.Enrich); err != nil {
		return err
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
	// 内部合并父没有外部恢复 Event；全部成员终结由控制面恢复，必须保留明确的系统原因。
	mergeRecovery := a.EventSourceID == BuiltinMergeEventSourceID && a.Merge != nil && a.Merge.Role == "aggregate" && a.EndType == AlertEndTypeSystem && a.EndReason == "merge_members_ended"
	if a.Status == AlertStatusRecovered && a.EndType != AlertEndTypeSource && !mergeRecovery {
		return fmt.Errorf("recovered alert requires source end or system merge recovery")
	}
	return nil
}

// ValidateEnrichPayload 校验 Alert.enrich 固定结构及 Processor 聚合状态与 enrich_status 的一致性。
func ValidateEnrichPayload(status EnrichStatus, object JSONObject) error {
	if status == EnrichStatusPending {
		if len(object) != 0 {
			return fmt.Errorf("pending alert enrich must be empty")
		}
		return nil
	}
	if len(object) != 1 {
		return fmt.Errorf("alert enrich must contain only processors")
	}
	processorsRaw, processorsExists := object["processors"]
	if !processorsExists {
		return fmt.Errorf("alert enrich must contain processors")
	}
	var processors []map[string]struct {
		Status      EnrichStatus  `json:"status"`
		Value       JSONObject    `json:"value"`
		Patches     []EnrichPatch `json:"patches"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics,omitempty"`
	}
	if err := json.Unmarshal(processorsRaw, &processors); err != nil {
		return fmt.Errorf("alert enrich processors: %w", err)
	}
	succeeded, failed, skipped := 0, 0, 0
	for index, entry := range processors {
		if len(entry) != 1 {
			return fmt.Errorf("alert enrich processors[%d] must contain exactly one entry", index)
		}
		for name, envelope := range entry {
			if name == "" || (envelope.Status != EnrichStatusSucceeded && envelope.Status != EnrichStatusPartial && envelope.Status != EnrichStatusFailed && envelope.Status != EnrichStatusSkipped) || (envelope.Value == nil && envelope.Patches == nil) {
				return fmt.Errorf("alert enrich processors[%d] is invalid", index)
			}
			for _, patch := range envelope.Patches {
				if err := patch.Validate(); err != nil {
					return fmt.Errorf("alert enrich patch: %w", err)
				}
			}
			switch envelope.Status {
			case EnrichStatusSucceeded:
				succeeded++
			case EnrichStatusFailed:
				failed++
			case EnrichStatusSkipped:
				skipped++
			}
			for _, diagnostic := range envelope.Diagnostics {
				switch diagnostic.Code {
				case "missing_field", "invalid_field", "dependency_invalid", "classification_failed":
				default:
					return fmt.Errorf("alert enrich processors[%d] diagnostic code is invalid: %q", index, diagnostic.Code)
				}
			}
		}
	}
	aggregate := EnrichStatusPartial
	applicable := len(processors) - skipped
	switch {
	case len(processors) == 0:
		aggregate = EnrichStatusSucceeded
	case skipped == len(processors):
		aggregate = EnrichStatusSkipped
	case succeeded == applicable:
		aggregate = EnrichStatusSucceeded
	case failed == applicable:
		aggregate = EnrichStatusFailed
	}
	if status != aggregate {
		return fmt.Errorf("alert enrich_status %q does not match processor aggregate %q", status, aggregate)
	}
	return nil
}

// ValidateAlertReplacement 校验 CAS 仅修改当前级别和生命周期字段，终态不可重开。
// 级别优先级由 Lifecycle 的冻结配置校验，Repository 不自行解释级别排序。
func ValidateAlertReplacement(current, replacement Alert) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("current alert: %w", err)
	}
	if err := replacement.Validate(); err != nil {
		return fmt.Errorf("replacement alert: %w", err)
	}
	if alertPolicyBookkeeping(current, replacement) {
		return nil
	}
	if current.Revision >= 1<<53-1 || replacement.Revision != current.Revision+1 {
		return fmt.Errorf("business alert replacement must increment revision exactly once")
	}
	if err := validateProjectionBusinessReplacement(current, replacement); err != nil {
		return err
	}
	if current.PolicyChange != nil || current.MergeChange != nil || current.ActionPending != nil {
		return fmt.Errorf("pending policy change must be completed before business mutation")
	}
	if err := validateActionTransition(&current, replacement); err != nil {
		return err
	}
	if err := ValidateAlertPolicyReplacement(current, replacement); err != nil {
		return err
	}
	left := current.Clone()
	right := replacement.Clone()
	clearLifecycle := func(alert *Alert) {
		alert.Revision = 0
		alert.Projection = AlertProjection{}
		alert.ActionPending = nil
		alert.PolicyChange = nil
		alert.MergeChange = nil
		alert.Shield = AlertShield{}
		alert.Merge = nil
		alert.Admission = AlertAdmission{}
		alert.PolicyTags = nil
		alert.Severity = ""
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
