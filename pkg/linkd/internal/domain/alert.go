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
	// EventSourceVersion 是创建时实际使用的不可变来源发布版本。
	EventSourceVersion int64        `json:"event_source_version"`
	AlertID            string       `json:"alert_id"`
	BKTenantID         string       `json:"bk_tenant_id"`
	EventSourceID      string       `json:"event_source_id"`
	Fingerprint        string       `json:"fingerprint"`
	Title              string       `json:"title"`
	Content            string       `json:"content"` // Content 创建时生成，后续同级触发或原地升级时直接采用该 Event.content。
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

	// LastShieldOperation 保留快捷屏蔽命令身份，不因解除绑定而清除。
	LastShieldOperation *AlertShieldOperation `json:"last_shield_operation,omitempty"`

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

	// EndOperation 保存主动关闭身份，供重试和跨系统操作追踪。
	EndOperation *AlertEndOperation `json:"end_operation,omitempty"`

	EnrichStatus EnrichStatus `json:"enrich_status"`
	Enrich       JSONObject   `json:"enrich,omitempty"`
}

// Normalize 用于新建或外部写入，规范动态 JSON 并检查完整载荷。
func (a Alert) Normalize() (Alert, error) {
	a, err := a.NormalizeState()
	if err != nil {
		return Alert{}, err
	}
	return a.normalizePayload()
}

// normalizePayload 只在外部输入或动态载荷发生变化时扫描 JSON。
func (a Alert) normalizePayload() (Alert, error) {
	var err error
	a.ExtraData, err = a.ExtraData.Normalize()
	if err != nil {
		return Alert{}, fmt.Errorf("alert extra_data: %w", err)
	}
	a.Enrich, err = a.Enrich.Normalize()
	if err != nil {
		return Alert{}, fmt.Errorf("alert enrich: %w", err)
	}
	if err := ValidateEnrichPayload(a.EnrichStatus, a.Enrich); err != nil {
		return Alert{}, err
	}
	return a, nil
}

// NormalizeState 隔离计划中的快照并统一时间、空集合，不重解析已冻结的业务 JSON。
func (a Alert) NormalizeState() (Alert, error) {
	return a.Clone().normalizeState()
}

func (a Alert) normalizeState() (Alert, error) {
	a.Title = TruncateDisplayText(a.Title)
	a.SubjectName = TruncateDisplayText(a.SubjectName)
	slices.Sort(a.PolicyTags)
	a.PolicyTags = slices.Compact(a.PolicyTags)
	a.Dimensions = a.Dimensions.Normalize()
	a.Labels = a.Labels.Normalize()
	if a.ExtraData == nil {
		a.ExtraData = JSONObject{}
	}
	if a.Enrich == nil {
		a.Enrich = JSONObject{}
	}
	a.LastOccurredAt = normalizeTime(a.LastOccurredAt)
	a.UpdateAt = normalizeTime(a.UpdateAt)
	a.BeginAt = normalizeTime(a.BeginAt)
	a.CreateAt = normalizeTime(a.CreateAt)
	a.EndAt = normalizeOptionalTime(a.EndAt)
	return a, a.ValidateState()
}

// Clone 返回不共享动态字段的 Alert 副本。
func (a Alert) Clone() Alert {
	a = a.cloneState()
	a.ExtraData = a.ExtraData.Clone()
	a.Enrich = a.Enrich.Clone()
	return a
}

// cloneState 隔离可变状态；动态 JSON 仅由可信不可变快照路径共享。
func (a Alert) cloneState() Alert {
	a.PolicyChange = a.PolicyChange.Clone()
	a.MergeChange = a.MergeChange.Clone()
	a.Shield = a.Shield.Clone()
	a.LastShieldOperation = a.LastShieldOperation.clone()
	a.Merge = a.Merge.Clone()
	a.Admission = a.Admission.Clone()
	a.PolicyTags = slices.Clone(a.PolicyTags)
	a.Dimensions = a.Dimensions.Clone()
	a.Labels = a.Labels.Clone()
	a.EndAt = normalizeOptionalTime(a.EndAt)
	a.EndOperation = a.EndOperation.clone()
	return a
}

// Validate 校验 Alert 字段及活动态、终态不变量。
func (a Alert) Validate() error {
	if err := a.ValidateState(); err != nil {
		return err
	}
	if _, err := a.ExtraData.Normalize(); err != nil {
		return fmt.Errorf("alert extra_data: %w", err)
	}
	if _, err := a.Enrich.Normalize(); err != nil {
		return fmt.Errorf("alert enrich: %w", err)
	}
	return ValidateEnrichPayload(a.EnrichStatus, a.Enrich)
}

// ValidateState 检查身份、生命周期、投影及动作状态，不重解析已冻结的丰富和扩展数据。
func (a Alert) ValidateState() error {
	if o := a.LastShieldOperation; o != nil {
		if o.validate() != nil {
			return fmt.Errorf("invalid shield operation")
		}
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
		{"title", a.Title, DisplayTextMaxChars * 4},
		{"subject_system", a.SubjectSystem, 32},
		{"subject_type", a.SubjectType, 128},
		{"subject_id", a.SubjectID, 256},
		{"subject_name", a.SubjectName, DisplayTextMaxChars * 4},
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
		if a.EndAt != nil || a.EndType != "" || a.EndReason != "" || a.EndOperation != nil {
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
	if a.EndOperation != nil {
		if err := a.EndOperation.Validate(); err != nil {
			return err
		}
		expected := AlertEndTypeUser
		if a.EndOperation.OperatorKind == OperatorKindSystem {
			expected = AlertEndTypeSystem
		}
		if a.Status != AlertStatusClosed || a.EndType != expected {
			return fmt.Errorf("end operation does not match terminal state")
		}
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

// ValidateAlertReplacement 校验 CAS 仅修改内容、当前级别和生命周期字段，终态不可重开。
// 级别优先级由 Lifecycle 的冻结配置校验，Repository 不自行解释级别排序。
func ValidateAlertReplacement(current, replacement Alert) error {
	if err := current.Validate(); err != nil {
		return fmt.Errorf("current alert: %w", err)
	}
	if err := replacement.Validate(); err != nil {
		return fmt.Errorf("replacement alert: %w", err)
	}
	return validateAlertReplacementTransition(current, replacement)
}

// validateAlertReplacementTransition 的双方已完成字段校验，只检查本次状态转换。
func validateAlertReplacementTransition(current, replacement Alert) error {
	if alertPolicyBookkeeping(current, replacement) {
		return nil
	}
	if current.Revision >= 1<<53-1 || replacement.Revision != current.Revision+1 {
		return fmt.Errorf("business alert replacement must increment revision exactly once")
	}
	if current.PolicyChange != nil || current.MergeChange != nil {
		return fmt.Errorf("pending policy change must be completed before business mutation")
	}
	if err := ValidateAlertPolicyReplacement(current, replacement); err != nil {
		return err
	}
	// 只替换结构体字段，不修改任何底层 map/slice；比较无需复制 JSON 载荷。
	left, right := current, replacement
	clearLifecycle := func(alert *Alert) {
		alert.Revision = 0
		alert.Content = ""
		alert.PolicyChange = nil
		alert.MergeChange = nil
		alert.Shield = AlertShield{}
		alert.LastShieldOperation = nil
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
		alert.EndOperation = nil
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
