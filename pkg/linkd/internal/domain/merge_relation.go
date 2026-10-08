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
	"slices"
	"time"
)

// MergeRelationMember 保存固定成员的建立进度；terminal 只记录已观察到的真实终态，不修改其生命周期。
type MergeRelationMember struct {
	AlertID string `json:"alert_id"`
	State   string `json:"state"`
}

// MergeRelation 是一个裁决操作的有界父子关系组；完整成员不放在 Alert 的摘要字段中。
// preparing 允许部分写入，ready 要求所有成员已确认且父/成员的查询引用已建立。
type MergeRelation struct {
	ID                string                `json:"id"`
	TenantID          string                `json:"bk_tenant_id"`
	WindowID          string                `json:"window_id"`
	GroupKey          string                `json:"group_key"`
	Policy            PolicyVersion         `json:"policy"`
	ParentAlertID     string                `json:"parent_alert_id"`
	ParentFingerprint string                `json:"parent_fingerprint"`
	WaitMemberIDs     []string              `json:"wait_member_ids"`
	EndOffset         int                   `json:"end_offset"`
	EndReason         string                `json:"end_reason,omitempty"`
	EndStartedAt      *time.Time            `json:"end_started_at,omitempty"`
	EndedAt           *time.Time            `json:"ended_at,omitempty"`
	Members           []MergeRelationMember `json:"members"`
	State             string                `json:"state"`
	IndexOffset       int                   `json:"index_offset"`
	// CreatedAt 使用首次裁决的固定时间锚点，重试不改用当前时间。
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Clone 固定 UTC 时间并隔离成员进度。
func (r MergeRelation) Clone() MergeRelation {
	r.Members = slices.Clone(r.Members)
	r.WaitMemberIDs = slices.Clone(r.WaitMemberIDs)
	r.EndStartedAt = normalizeOptionalTime(r.EndStartedAt)
	r.EndedAt = normalizeOptionalTime(r.EndedAt)
	r.CreatedAt = normalizeTime(r.CreatedAt)
	r.UpdatedAt = normalizeTime(r.UpdatedAt)
	return r
}

// Validate 限制作用域、固定成员集合和准备阶段的完整性，不把 preparing 解释成已经合并。
func (r MergeRelation) Validate() error {
	if ValidateIdentityPart("tenant", r.TenantID, 64) != nil || r.Policy.Validate() != nil || r.ParentAlertID == "" || len(r.ParentAlertID) > EntityIDMaxBytes || r.ParentFingerprint == "" || len(r.ParentFingerprint) > 128 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("invalid merge relation scope")
	}
	for _, id := range []string{r.ID, r.WindowID, r.GroupKey} {
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("invalid merge relation identity")
		}
	}
	if len(r.Members) < 2 || len(r.Members) > 256 || r.IndexOffset < 0 || r.IndexOffset > len(r.Members)+1 {
		return fmt.Errorf("invalid merge relation member budget")
	}
	for i, m := range r.Members {
		if m.AlertID == "" || len(m.AlertID) > EntityIDMaxBytes || m.AlertID == r.ParentAlertID || (i > 0 && r.Members[i-1].AlertID >= m.AlertID) {
			return fmt.Errorf("invalid merge relation member identity")
		}
		if m.State != "pending" && m.State != "linked" && m.State != "terminal" {
			return fmt.Errorf("invalid merge relation member state")
		}
		if r.State == "ready" && m.State == "pending" {
			return fmt.Errorf("ready relation contains unconfirmed member")
		}
	}
	if len(r.WaitMemberIDs) < len(r.Members) || len(r.WaitMemberIDs) > 256 || r.EndOffset < 0 || r.EndOffset > len(r.WaitMemberIDs) {
		return fmt.Errorf("invalid relation cleanup budget")
	}
	for i, id := range r.WaitMemberIDs {
		if id == "" || len(id) > EntityIDMaxBytes || id == r.ParentAlertID || (i > 0 && r.WaitMemberIDs[i-1] >= id) {
			return fmt.Errorf("invalid relation waiting identity")
		}
	}
	for _, m := range r.Members {
		if !slices.Contains(r.WaitMemberIDs, m.AlertID) {
			return fmt.Errorf("selected member is absent from wait set")
		}
	}
	switch r.State {
	case "preparing", "ready":
		if r.EndOffset != 0 || r.EndReason != "" || r.EndStartedAt != nil || r.EndedAt != nil {
			return fmt.Errorf("live relation contains end metadata")
		}
	case "ending", "ended":
		if (r.EndReason != "parent_ended" && r.EndReason != "members_ended") || r.EndStartedAt == nil || r.EndStartedAt.Before(r.CreatedAt) || r.UpdatedAt.Before(*r.EndStartedAt) {
			return fmt.Errorf("invalid relation ending intent")
		}
		if r.State == "ending" && r.EndedAt != nil {
			return fmt.Errorf("unfinished relation has end time")
		}
		if r.State == "ended" && (r.EndOffset != len(r.WaitMemberIDs) || r.IndexOffset != len(r.Members)+1 || r.EndedAt == nil || r.EndedAt.Before(*r.EndStartedAt) || r.UpdatedAt.Before(*r.EndedAt)) {
			return fmt.Errorf("relation cleanup incomplete")
		}
	default:
		return fmt.Errorf("invalid merge relation state")
	}
	if r.State == "ready" && r.IndexOffset != len(r.Members)+1 {
		return fmt.Errorf("ready relation missing query references")
	}
	return nil
}

// Member 查找固定成员；缺失不会被解释成允许新成员加入。
func (r MergeRelation) Member(id string) (MergeRelationMember, bool) {
	i, found := slices.BinarySearchFunc(r.Members, id, func(m MergeRelationMember, id string) int {
		if m.AlertID < id {
			return -1
		}
		if m.AlertID > id {
			return 1
		}
		return 0
	})
	if !found {
		return MergeRelationMember{}, false
	}
	return r.Members[i], true
}
