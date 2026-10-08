// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package merge 保存合并裁决、成员快照与父 Event 创建进度，编排先父后子的可靠业务步骤。
// Redis 未裁决窗口可丢失；本包已经持久化的业务裁决不能用当前配置或成员状态覆盖。
package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// Decision 是一次窗口冻结后的持久化业务结果；完整成员快照按成员单独存储。
// ID 来自冻结窗口的稳定 operation_id；Progress 是唯一可变部分。
type Decision struct {
	ID            string         `json:"id"`
	TenantID      string         `json:"bk_tenant_id"`
	WindowID      string         `json:"window_id"`
	GroupKey      string         `json:"group_key"`
	Policy        policy.Release `json:"policy"`
	FrozenAt      time.Time      `json:"frozen_at"`
	StartedAt     time.Time      `json:"started_at"`
	Deadline      time.Time      `json:"deadline"`
	Outcome       string         `json:"outcome"`
	MemberIDs     []string       `json:"member_ids"`
	WaitMemberIDs []string       `json:"wait_member_ids"`
	Progress      Progress       `json:"progress"`
}

// Progress 的各阶段只能向前推进；ParentEvent 必须先保存，才能创建 Event/提交 Mailbox。
type Progress struct {
	Phase         string        `json:"phase"`
	ParentEvent   *domain.Event `json:"parent_event,omitempty"`
	ParentAlertID string        `json:"parent_alert_id,omitempty"`
	MemberOffset  int           `json:"member_offset"`
	// CaptureOffset 是已保存首次成员快照的前缀，重启后不从头重读全部快照。
	CaptureOffset int `json:"capture_offset"`
	// WindowFinished 仅记录完成后 Redis 提示清理是否确认，失败不丢弃重试入口。
	WindowFinished bool      `json:"window_finished"`
	ReasonCode     string    `json:"reason_code,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Snapshot 固定一次操作的一个成员 Alert；创建后只读，不随原 Alert 的后续状态更新。
type Snapshot struct {
	TenantID   string       `json:"bk_tenant_id"`
	DecisionID string       `json:"decision_id"`
	Alert      domain.Alert `json:"alert"`
}

// StoredDecision 保留存储 CAS token，与告警业务 revision 无关。
type StoredDecision struct {
	Decision Decision
	Version  string
}

func hash(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validHash(s string) bool { raw, err := hex.DecodeString(s); return err == nil && len(raw) == 32 }

// MembersDigest 固定排序成员集合；调用方只能传入已经验证为有序且唯一的 AlertID。
func MembersDigest(ids []string) string { return hash(append([]string{"merge-members"}, ids...)...) }

// ParentFingerprint 只包含租户、策略 ID 和成员集合，故窗口/策略版本改变不强制创建新生命周期。
func ParentFingerprint(tenant, policyID string, ids []string) string {
	return hash("merge-parent", tenant, policyID, MembersDigest(ids))
}

// Clone 深拷贝所有可变字段，来源 Release 的 Spec 也不可共享给编辑代码。
func (d Decision) Clone() Decision {
	d.MemberIDs = slices.Clone(d.MemberIDs)
	d.WaitMemberIDs = slices.Clone(d.WaitMemberIDs)
	d.Policy.Spec = slices.Clone(d.Policy.Spec)
	d.Policy.Compiled.Schemes = slices.Clone(d.Policy.Compiled.Schemes)
	for i := range d.Policy.Compiled.Schemes {
		d.Policy.Compiled.Schemes[i].Fields = slices.Clone(d.Policy.Compiled.Schemes[i].Fields)
	}
	if d.Progress.ParentEvent != nil {
		e := d.Progress.ParentEvent.Clone()
		d.Progress.ParentEvent = &e
	}
	return d
}

// Validate 约束候选集合、冻结策略和每个阶段的必需事实；不把预计父 ID 当成真实父。
func (d Decision) Validate() error {
	identity, err := domain.MergeDecisionID(d.TenantID, d.WindowID)
	if err != nil || identity != d.ID {
		return fmt.Errorf("merge decision identity differs from tenant/window")
	}
	if domain.ValidateIdentityPart("tenant", d.TenantID, 64) != nil || !validHash(d.ID) || !validHash(d.WindowID) || !validHash(d.GroupKey) || d.FrozenAt.IsZero() || d.StartedAt.IsZero() || !d.Deadline.After(d.StartedAt) || d.Deadline.Sub(d.StartedAt) > 24*time.Hour || d.FrozenAt.Before(d.StartedAt) {
		return policy.ErrInvalid
	}
	if d.Outcome == "failed" && d.FrozenAt.Before(d.Deadline) {
		return fmt.Errorf("failed window decision cannot precede deadline")
	}
	if d.Outcome != "succeeded" && d.Outcome != "failed" {
		return policy.ErrInvalid
	}
	if d.Policy.TenantID != d.TenantID || d.Policy.Kind != policy.Merge || d.Policy.ID == "" || d.Policy.Version < 1 || d.Policy.Deleted {
		return policy.ErrInvalid
	}
	c, err := policy.Compile(policy.Merge, d.Policy.Spec)
	if err != nil || !reflect.DeepEqual(c.Summary, d.Policy.Compiled) {
		return fmt.Errorf("invalid frozen merge release")
	}
	for _, ids := range [][]string{d.MemberIDs, d.WaitMemberIDs} {
		if len(ids) > 256 {
			return policy.ErrInvalid
		}
		for i, id := range ids {
			if id == "" || len(id) > domain.EntityIDMaxBytes || (i > 0 && ids[i-1] >= id) {
				return policy.ErrInvalid
			}
		}
	}
	if d.Outcome == "succeeded" && len(d.MemberIDs) < 2 {
		return policy.ErrInvalid
	}
	if len(d.WaitMemberIDs) == 0 {
		return policy.ErrInvalid
	}
	for _, id := range d.MemberIDs {
		if !slices.Contains(d.WaitMemberIDs, id) {
			return policy.ErrInvalid
		}
	}
	p := d.Progress
	if (p.WindowFinished && p.Phase != "completed") || (p.ParentEvent != nil && p.CaptureOffset != len(d.MemberIDs)) {
		return policy.ErrInvalid
	}
	if p.UpdatedAt.IsZero() || p.UpdatedAt.Before(d.FrozenAt) || p.MemberOffset < 0 || p.MemberOffset > len(d.WaitMemberIDs) || p.CaptureOffset < 0 || p.CaptureOffset > len(d.MemberIDs) || len(p.ReasonCode) > 80 || len(p.ParentAlertID) > domain.EntityIDMaxBytes {
		return policy.ErrInvalid
	}
	switch p.Phase {
	case "capturing":
		if d.Outcome != "succeeded" || p.ParentEvent != nil || p.ParentAlertID != "" || p.MemberOffset != 0 || p.ReasonCode != "" {
			return policy.ErrInvalid
		}
	case "prepared", "waiting_parent":
		if d.Outcome != "succeeded" || p.ParentEvent == nil || p.ParentAlertID != "" || p.MemberOffset != 0 || p.ReasonCode != "" {
			return policy.ErrInvalid
		}
	case "linking":
		if d.Outcome != "succeeded" || p.ParentEvent == nil || p.ParentAlertID == "" || p.ReasonCode != "" {
			return policy.ErrInvalid
		}
	case "ending":
		if d.Outcome != "succeeded" || p.ParentEvent == nil || p.ParentAlertID == "" || (p.ReasonCode != "parent_ended" && p.ReasonCode != "members_ended") {
			return policy.ErrInvalid
		}
	case "releasing":
		if p.ReasonCode == "" || p.ParentAlertID != "" {
			return policy.ErrInvalid
		}
	case "completed":
		if p.ParentAlertID != "" && (d.Outcome != "succeeded" || p.ParentEvent == nil || (p.ReasonCode != "" && p.ReasonCode != "parent_ended" && p.ReasonCode != "members_ended")) {
			return policy.ErrInvalid
		}
		if p.MemberOffset != len(d.WaitMemberIDs) || (p.ParentAlertID == "" && p.ReasonCode == "") {
			return policy.ErrInvalid
		}
	default:
		return policy.ErrInvalid
	}
	if p.ParentEvent != nil {
		e := p.ParentEvent
		if e.Validate() != nil || e.MergeOrigin == nil || e.BKTenantID != d.TenantID || e.MergeOrigin.OperationID != d.ID || e.MergeOrigin.WindowID != d.WindowID || e.MergeOrigin.Policy.ID != d.Policy.ID || e.MergeOrigin.Policy.Version != d.Policy.Version || e.MergeOrigin.Policy.Digest != d.Policy.Compiled.Digest || e.MergeOrigin.MembersDigest != MembersDigest(d.MemberIDs) || e.MergeOrigin.MemberCount != len(d.MemberIDs) || e.Fingerprint != ParentFingerprint(d.TenantID, d.Policy.ID, d.MemberIDs) || !e.CreateAt.Equal(d.FrozenAt) {
			return fmt.Errorf("parent event differs from frozen merge decision")
		}
		tags := slices.Clone(c.Merge.AlarmTags)
		slices.Sort(tags)
		if !slices.Equal(e.MergeOrigin.AlarmTags, tags) {
			return fmt.Errorf("parent tags differ from frozen policy")
		}
		expected, err := domain.GenerateEventID(d.TenantID, domain.BuiltinMergeEventSourceID, d.ID, d.FrozenAt)
		if err != nil || e.EventID != expected || e.SourceEventID != d.ID || e.SourceAlertID != e.Fingerprint || !e.OccurredAt.Equal(d.FrozenAt) || !e.ProducedAt.Equal(d.FrozenAt) || !e.ReceivedAt.Equal(d.FrozenAt) {
			return fmt.Errorf("parent event identity/time differs from decision")
		}
		if len(e.Evaluations) != 1 || e.Evaluations[0].Action != domain.EventActionTriggered || len(e.RelatedAlertIDs) > 0 || e.EnrichStatus != domain.EnrichStatusPending {
			return fmt.Errorf("prepared parent must be an unprocessed trigger")
		}
	}
	return nil
}

func validateProgress(before, after Decision) error {
	a, b := before.Clone(), after.Clone()
	a.Progress = Progress{}
	b.Progress = Progress{}
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("merge decision facts are immutable")
	}
	old, next := before.Progress, after.Progress
	if next.UpdatedAt.Before(old.UpdatedAt) || next.MemberOffset < old.MemberOffset || next.CaptureOffset < old.CaptureOffset {
		return policy.ErrConflict
	}
	if old.ParentEvent != nil && !reflect.DeepEqual(old.ParentEvent, next.ParentEvent) {
		return fmt.Errorf("prepared parent event is immutable")
	}
	if old.ParentAlertID != "" && old.ParentAlertID != next.ParentAlertID {
		return fmt.Errorf("real parent cannot be replaced")
	}
	allowed := old.Phase == next.Phase
	switch old.Phase {
	case "capturing":
		allowed = allowed || next.Phase == "prepared" || next.Phase == "releasing"
	case "prepared":
		allowed = allowed || next.Phase == "waiting_parent"
	case "waiting_parent":
		allowed = allowed || next.Phase == "linking" || next.Phase == "releasing" || next.Phase == "ending"
	case "linking":
		allowed = allowed || next.Phase == "ending" || next.Phase == "completed"
	case "releasing", "ending":
		allowed = allowed || next.Phase == "completed"
	case "completed":
		metadata := old
		if !old.WindowFinished && next.WindowFinished {
			metadata.WindowFinished = true
			metadata.UpdatedAt = next.UpdatedAt
		}
		allowed = reflect.DeepEqual(metadata, next)
	}
	if !allowed {
		return fmt.Errorf("invalid merge progress transition")
	}
	return after.Validate()
}

// Normalize 固定 JSON/UTC 表达，避免同一裁决在序列化重读后因格式差异冲突。
func (d Decision) Normalize() (Decision, error) {
	d = d.Clone()
	d.FrozenAt = d.FrozenAt.Round(0).UTC()
	d.StartedAt = d.StartedAt.Round(0).UTC()
	d.Deadline = d.Deadline.Round(0).UTC()
	d.Progress.UpdatedAt = d.Progress.UpdatedAt.Round(0).UTC()
	d.Policy.CreatedAt = d.Policy.CreatedAt.Round(0).UTC()
	c, err := policy.Compile(policy.Merge, d.Policy.Spec)
	if err != nil {
		return Decision{}, err
	}
	d.Policy.Spec = c.Canonical
	if d.Progress.ParentEvent != nil {
		e, err := d.Progress.ParentEvent.Normalize()
		if err != nil {
			return Decision{}, err
		}
		d.Progress.ParentEvent = &e
	}
	return d, d.Validate()
}
