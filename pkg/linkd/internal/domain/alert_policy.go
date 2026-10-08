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
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// PolicyVersion 固定一个策略的不可变版本；类型和租户由所属业务记录确定。
type PolicyVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

func (p PolicyVersion) Validate() error {
	if err := ValidateIdentityPart("policy id", p.ID, 80); err != nil {
		return err
	}
	raw, err := hex.DecodeString(p.Digest)
	if err != nil || len(raw) != 32 || p.Version < 1 || p.Version >= 1<<53 {
		return fmt.Errorf("invalid policy version")
	}
	return nil
}

// AlertAdmission 保存最近一次真实放行，不能从 active 或解除屏蔽推导出放行。
// Severity 区分已放行的旧等级与尚被屏蔽的升级；Cause 不用当前时间生成业务身份。
type AlertAdmission struct {
	AdmittedAt *time.Time `json:"admitted_at,omitempty"`
	Severity   string     `json:"severity,omitempty"`
	CauseType  string     `json:"cause_type,omitempty"`
	CauseID    string     `json:"cause_id,omitempty"`
}

// ShieldBinding 是 Alert 内有界的活动关系摘要；历史关系通过业务流水保留。
// SourceEventID/Severity 指向建立关系时已经冻结的事件视图，不使用后续 Event 刷新。
type ShieldBinding struct {
	// Origin 为空表示 Event 匹配；manual 表示指定告警快捷绑定，不按普通条件解除。
	Origin      string `json:"origin,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	OperatorID  string `json:"operator_id,omitempty"`
	// MainCandidate 保存待处理主的冻结 Event 引用；nil 表示使用主 Alert 的 opening 快照。
	MainCandidate *DependencyMain `json:"main_candidate,omitempty"`
	ActivationID  string          `json:"activation_id,omitempty"`
	BindingID     string          `json:"binding_id"`
	Policy        PolicyVersion   `json:"policy"`
	Type          string          `json:"type"`
	Mode          string          `json:"mode,omitempty"`
	SourceEventID string          `json:"source_event_id"`
	Severity      string          `json:"severity"`
	MainAlertID   string          `json:"main_alert_id,omitempty"`
	BoundAt       time.Time       `json:"bound_at"`
	Reason        string          `json:"reason,omitempty"`
}

// AlertShield 与生命周期及放行记录独立。NextCheckAt 是持久化的首次/最近安排时间，后台扫描无需新 Event。
type AlertShield struct {
	Active      bool            `json:"active"`
	Bindings    []ShieldBinding `json:"bindings,omitempty"`
	NextCheckAt *time.Time      `json:"next_check_at,omitempty"`
}

func (s AlertShield) Clone() AlertShield {
	s.Bindings = slices.Clone(s.Bindings)
	if len(s.Bindings) == 0 {
		s.Bindings = nil
	}
	for i := range s.Bindings {
		s.Bindings[i].BoundAt = normalizeTime(s.Bindings[i].BoundAt)
		if s.Bindings[i].MainCandidate != nil {
			v := *s.Bindings[i].MainCandidate
			s.Bindings[i].MainCandidate = &v
		}
	}
	s.NextCheckAt = normalizeOptionalTime(s.NextCheckAt)
	return s
}

func (a AlertAdmission) Clone() AlertAdmission {
	a.AdmittedAt = normalizeOptionalTime(a.AdmittedAt)
	return a
}

func (a AlertAdmission) Validate() error {
	if a.AdmittedAt == nil {
		if a.Severity != "" || a.CauseType != "" || a.CauseID != "" {
			return fmt.Errorf("admission cause requires admitted_at")
		}
		return nil
	}
	if a.AdmittedAt.IsZero() || a.Severity == "" || len(a.Severity) > 32 || a.CauseID == "" || len(a.CauseID) > 256 {
		return fmt.Errorf("invalid admission")
	}
	switch a.CauseType {
	case "source_event", "system_operation", "user_operation":
	default:
		return fmt.Errorf("invalid admission cause")
	}
	return nil
}

func (s AlertShield) Validate(alertID string, status AlertStatus) error {
	if s.Active != (len(s.Bindings) > 0) || len(s.Bindings) > 16 || (s.NextCheckAt != nil) != s.Active {
		return fmt.Errorf("inconsistent shield state")
	}
	if status.Terminal() && s.Active {
		return fmt.Errorf("terminal alert cannot remain shielded")
	}
	if s.NextCheckAt != nil && s.NextCheckAt.IsZero() {
		return fmt.Errorf("invalid shield check time")
	}
	seen := map[string]bool{}
	for _, b := range s.Bindings {
		raw, err := hex.DecodeString(b.BindingID)
		if err != nil || len(raw) != 32 || seen[b.BindingID] || b.BoundAt.IsZero() || b.SourceEventID == "" || len(b.SourceEventID) > EntityIDMaxBytes || b.Severity == "" || len(b.Severity) > 32 || len(b.Reason) > 4096 {
			return fmt.Errorf("invalid shield binding")
		}
		switch b.Origin {
		case "":
			if b.OperationID != "" || b.OperatorID != "" {
				return fmt.Errorf("matched binding cannot carry manual operation")
			}
		case "manual":
			if b.Type != "time_shield" || ValidateIdentityPart("operation", b.OperationID, 128) != nil || b.OperatorID == "" || len(b.OperatorID) > 256 {
				return fmt.Errorf("invalid manual shield operation")
			}
		default:
			return fmt.Errorf("invalid shield origin")
		}
		seen[b.BindingID] = true
		if err := b.Policy.Validate(); err != nil {
			return err
		}
		switch b.Type {
		case "time_shield":
			if raw, err := hex.DecodeString(b.ActivationID); err != nil || len(raw) != 32 {
				return fmt.Errorf("time shield requires activation identity")
			}
			if b.Mode != "" || b.MainAlertID != "" || b.MainCandidate != nil {
				return fmt.Errorf("time shield cannot reference main alert")
			}
		case "rely_shield":
			if b.MainCandidate != nil {
				if err := b.MainCandidate.Validate(); err != nil {
					return err
				}
				if b.MainCandidate.AlertID != b.MainAlertID {
					return fmt.Errorf("dependency main reference mismatch")
				}
			}
			if (b.Mode != "custom_shield" && b.Mode != "cmdb_shield") || b.MainAlertID == "" || len(b.MainAlertID) > EntityIDMaxBytes || b.MainAlertID == alertID {
				return fmt.Errorf("invalid dependency shield")
			}
		default:
			return fmt.Errorf("invalid shield type")
		}
	}
	return nil
}

// ValidateAlertPolicyReplacement 不允许忘记已经发生的放行；绑定不能以相同身份更换主告警或版本。
func ValidateAlertPolicyReplacement(current, replacement Alert) error {
	if err := validateMergeReplacement(current.Merge, replacement.Merge); err != nil {
		return err
	}
	if replacement.MergeChange != nil {
		if !reflect.DeepEqual(replacement.MergeChange.Before, current.Merge) {
			return fmt.Errorf("merge change before differs from current state")
		}
		before, after := current.Clone(), replacement.Clone()
		before.Merge = after.Merge.Clone()
		before.MergeChange = after.MergeChange.Clone()
		before.UpdateAt = after.UpdateAt
		before.Revision = after.Revision
		before.Projection = after.Projection.Clone()
		before.ActionPending = after.ActionPending.Clone()
		if after.MergeChange.Kind == "parent_recover" {
			if current.Status != AlertStatusActive {
				return fmt.Errorf("merge recovery requires active parent")
			}
			before.Status = after.Status
			before.EndAt = normalizeOptionalTime(after.EndAt)
			before.EndType = after.EndType
			before.EndReason = after.EndReason
			before.Shield = after.Shield.Clone()
			// 合并父终态可同时冻结屏蔽清理意图；其 Before/After 仍由下方完整绑定校验约束。
			before.PolicyChange = after.PolicyChange.Clone()
		} else if after.MergeChange.ActionReady {
			if current.Admission.AdmittedAt != nil && current.Admission.Severity == current.Severity {
				return fmt.Errorf("merge change cannot readmit current severity")
			}
			before.Admission = after.Admission.Clone()
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("merge change cannot change unrelated alert facts")
		}
	}
	if replacement.PolicyChange != nil && (!reflect.DeepEqual(replacement.PolicyChange.Before, current.Shield.Bindings) || !replacement.PolicyChange.EffectiveAt.Equal(replacement.UpdateAt)) {
		return fmt.Errorf("policy change intent differs from transition")
	}
	if current.Admission.AdmittedAt != nil {
		if replacement.Admission.AdmittedAt == nil || replacement.Admission.AdmittedAt.Before(*current.Admission.AdmittedAt) {
			return fmt.Errorf("admission cannot be cleared or moved backwards")
		}
		if current.Admission.CauseType == replacement.Admission.CauseType && current.Admission.CauseID == replacement.Admission.CauseID && !reflect.DeepEqual(current.Admission, replacement.Admission) {
			return fmt.Errorf("same admission cause cannot change result")
		}
	}
	for _, before := range current.Shield.Bindings {
		for _, after := range replacement.Shield.Bindings {
			if before.BindingID == after.BindingID && !reflect.DeepEqual(before, after) {
				return fmt.Errorf("shield binding is immutable")
			}
		}
	}
	return nil
}

// AlertPolicyChange 是控制面 CAS 后仍需补齐的状态输出和流水意图。它不触发处置。
// 保留在 Alert 上，直到同一 fingerprint lease 内完成日志和输出尝试；后续业务变更须先完成它。
type AlertPolicyChange struct {
	OperationID string          `json:"operation_id"`
	EffectiveAt time.Time       `json:"effective_at"`
	Before      []ShieldBinding `json:"before,omitempty"`
	After       []ShieldBinding `json:"after,omitempty"`
}

func (c *AlertPolicyChange) Clone() *AlertPolicyChange {
	if c == nil {
		return nil
	}
	v := *c
	v.EffectiveAt = normalizeTime(v.EffectiveAt)
	v.Before = (AlertShield{Bindings: c.Before}).Clone().Bindings
	v.After = (AlertShield{Bindings: c.After}).Clone().Bindings
	return &v
}

func (c *AlertPolicyChange) Validate(a Alert) error {
	if c == nil {
		return nil
	}
	err := ValidateIdentityPart("policy operation", c.OperationID, 128)
	if err != nil || c.EffectiveAt.IsZero() || (len(c.Before) == 0 && len(c.After) == 0) || len(c.Before) > 16 || len(c.After) > 16 || !reflect.DeepEqual(c.After, a.Shield.Bindings) {
		return fmt.Errorf("invalid pending policy change")
	}
	next := c.EffectiveAt
	var checkAt *time.Time
	if len(c.Before) > 0 {
		checkAt = &next
	}
	if err := (AlertShield{Active: len(c.Before) > 0, Bindings: c.Before, NextCheckAt: checkAt}).Validate(a.AlertID, AlertStatusActive); err != nil {
		return err
	}
	return nil
}

// alertPolicyBookkeeping 只允许完成已记录的输出意图或向后安排复查，不改变业务快照和 UpdateAt。
// 后续投影 ACK 也必须走独立白名单，不能借普通终态替换放开业务字段。
func alertPolicyBookkeeping(current, replacement Alert) bool {
	before, after := current.Clone(), replacement.Clone()
	changed := false
	if before.ActionPending != nil && after.ActionPending == nil {
		before.ActionPending = nil
		changed = true
	}
	if projectionACKOnly(before.Projection, after.Projection) && !reflect.DeepEqual(before.Projection, after.Projection) {
		before.Projection = after.Projection.Clone()
		changed = true
	}
	if before.MergeChange != nil && after.MergeChange == nil {
		before.MergeChange = nil
		changed = true
	}
	if before.PolicyChange != nil && after.PolicyChange == nil {
		before.PolicyChange = nil
		changed = true
	}
	if before.Shield.Active && after.Shield.Active && before.Shield.NextCheckAt != nil && after.Shield.NextCheckAt != nil && after.Shield.NextCheckAt.After(*before.Shield.NextCheckAt) {
		before.Shield.NextCheckAt = after.Shield.NextCheckAt
		changed = true
	}
	return changed && reflect.DeepEqual(before, after)
}

// AdmittedActiveMain 只允许当前等级已经放行的活动告警进入优先主候选集；屏蔽中的升级不能借用旧等级资格。
func (a Alert) AdmittedActiveMain() bool {
	return a.Status == AlertStatusActive && !a.Shield.Active && !a.Merge.Blocking() && a.Admission.AdmittedAt != nil && a.Admission.Severity == a.Severity
}

// DependencyMain 引用已完成丰富、尚在生命周期流程中的确定性主候选；租户继承所在记录。
// 待处理主可随后进入屏蔽/合并等待，不能把它等同于已放行主；绑定只根据其真实生命周期解除。
type DependencyMain struct {
	AlertID       string `json:"alert_id"`
	EventID       string `json:"event_id"`
	EventSourceID string `json:"event_source_id"`
	Fingerprint   string `json:"fingerprint"`
	Severity      string `json:"severity"`
}

// Validate 检查冻结主引用的完整身份；租户由外层策略/绑定记录约束。
func (m DependencyMain) Validate() error {
	if err := ValidateIdentityPart("event source", m.EventSourceID, 32); err != nil {
		return err
	}
	if m.AlertID == "" || len(m.AlertID) > EntityIDMaxBytes || m.EventID == "" || len(m.EventID) > EntityIDMaxBytes || m.Fingerprint == "" || len(m.Fingerprint) > 128 || m.Severity == "" || len(m.Severity) > 32 {
		return fmt.Errorf("invalid dependency main")
	}
	return nil
}

// AlertShieldOperation 保留最近一次显式命令结果；更早命令由原 expected_revision 阻止再次应用。
type AlertShieldOperation struct {
	ID          string `json:"id"`
	RequestHash string `json:"request_hash"`
}

func (o *AlertShieldOperation) clone() *AlertShieldOperation {
	if o == nil {
		return nil
	}
	v := *o
	return &v
}

func (o AlertShieldOperation) validate() error {
	raw, err := hex.DecodeString(o.RequestHash)
	if ValidateIdentityPart("shield operation", o.ID, 128) != nil || err != nil || len(raw) != 32 || hex.EncodeToString(raw) != o.RequestHash {
		return fmt.Errorf("invalid shield operation")
	}
	return nil
}
