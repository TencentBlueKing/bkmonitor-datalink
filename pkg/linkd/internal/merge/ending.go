// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// RelationEndOperator 在各自 fingerprint lease 内恢复父或解除成员关系；解除不得产生成员处置。
type RelationEndOperator interface {
	RecoverMergeParent(context.Context, string, string, string) (store.StoredAlert, error)
	UnlinkMergeMember(context.Context, string, string, string) (store.StoredAlert, error)
}

func parentEndReason(a domain.Alert) string {
	if a.Status == domain.AlertStatusRecovered && a.EndType == domain.AlertEndTypeSystem && a.EndReason == "merge_members_ended" {
		return "members_ended"
	}
	return "parent_ended"
}

// BeginEndRelation 保存真实父终态对应的解除意图；完成的创建裁决不重开，关系可独立结束。
// 关系和裁决分别 CAS，任一步失败都由同一身份重试；不能把终态父转成普通失败释放。
func (j *Journal) BeginEndRelation(ctx context.Context, tenant, id string, parent store.StoredAlert, at time.Time) (StoredRelation, error) {
	if !parent.Alert.Status.Terminal() || parent.Alert.MergeChange != nil || parent.Alert.PolicyChange != nil {
		return StoredRelation{}, store.ErrInvalidTransition
	}
	r, err := j.ensureRelation(ctx, tenant, id, parent, at, true)
	if err != nil {
		return StoredRelation{}, err
	}
	if at.Before(r.Relation.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if r.Relation.State == "preparing" || r.Relation.State == "ready" {
		next := r.Relation.Clone()
		next.State, next.EndReason = "ending", parentEndReason(parent.Alert)
		next.EndStartedAt, next.UpdatedAt = &at, at
		r, err = j.putRelation(ctx, r, next)
		if err != nil {
			return StoredRelation{}, err
		}
	}
	current, err := j.Get(ctx, tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	if current.Decision.Progress.Phase == "linking" {
		next := current.Decision.Clone()
		next.Progress.Phase, next.Progress.ReasonCode = "ending", r.Relation.EndReason
		next.Progress.UpdatedAt = at
		if _, err := j.put(ctx, current, next); err != nil {
			return StoredRelation{}, err
		}
	}
	return r, nil
}

// EndRelationStep 最多建立一个反查引用或解除一个成员，成功后才推进持久化前缀。
// 调用方须按关系串行执行本方法与 LinkStep；这里只取得单个 Alert 的锁，不同时持有父子锁。
func (j *Journal) EndRelationStep(ctx context.Context, tenant, id string, parent store.StoredAlert, ops RelationEndOperator, at time.Time) (StoredRelation, error) {
	if ops == nil || !parent.Alert.Status.Terminal() {
		return StoredRelation{}, policy.ErrInvalid
	}
	// 父可能在关系建立前关闭，也可能保存终态后输出失败；先创建固定事实再排空父的待输出意图。
	existing, err := j.ensureRelation(ctx, tenant, id, parent, at, true)
	if err != nil {
		return StoredRelation{}, err
	}
	if at.Before(existing.Relation.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	parent, err = ops.RecoverMergeParent(ctx, tenant, parent.Alert.AlertID, id)
	if err != nil {
		return StoredRelation{}, err
	}
	r, err := j.BeginEndRelation(ctx, tenant, id, parent, at)
	if err != nil {
		return StoredRelation{}, err
	}
	if r.Relation.IndexOffset < len(r.Relation.Members)+1 {
		return j.IndexRelationStep(ctx, r, at)
	}
	if r.Relation.EndOffset < len(r.Relation.WaitMemberIDs) {
		member := r.Relation.WaitMemberIDs[r.Relation.EndOffset]
		actual, err := ops.UnlinkMergeMember(ctx, tenant, member, id)
		if err := validateUnlinkedMember(r.Relation, member, actual, err); err != nil {
			return StoredRelation{}, err
		}
		next := r.Relation.Clone()
		next.EndOffset++
		next.UpdatedAt = at
		return j.putRelation(ctx, r, next)
	}
	if r.Relation.State != "ended" {
		next := r.Relation.Clone()
		next.State, next.EndedAt, next.UpdatedAt = "ended", &at, at
		r, err = j.putRelation(ctx, r, next)
		if err != nil {
			return StoredRelation{}, err
		}
	}
	// 关系 ended 的 CAS 可能先于裁决完成；即使重读到 ended，也必须补齐这个独立步骤。
	return r, j.completeEndedDecision(ctx, tenant, id, at)
}

func validateUnlinkedMember(r domain.MergeRelation, id string, actual store.StoredAlert, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		if _, selected := r.Member(id); !selected {
			return nil // 未选中的 reserved 候选可能从未创建 Alert。
		}
	}
	if err != nil {
		return err
	}
	a := actual.Alert
	if actual.Version.IsZero() || a.Validate() != nil || a.MergeChange != nil || a.PolicyChange != nil {
		return fmt.Errorf("incomplete merge member unlink")
	}
	if a.BKTenantID != r.TenantID || a.AlertID != id {
		return policy.ErrAccess
	}
	if !a.Status.Terminal() && a.Merge != nil {
		if slices.Contains(a.Merge.RelationIDs, r.ID) {
			return fmt.Errorf("active member still contains ended relation")
		}
		for _, wait := range a.Merge.Pending {
			if wait.WindowID == r.WindowID {
				return fmt.Errorf("active member still waits for ended relation")
			}
		}
	}
	return nil
}

func (j *Journal) completeEndedDecision(ctx context.Context, tenant, id string, at time.Time) error {
	current, err := j.Get(ctx, tenant, id)
	if err != nil || current.Decision.Progress.Phase == "completed" {
		return err
	}
	if current.Decision.Progress.Phase != "ending" {
		return policy.ErrConflict
	}
	next := current.Decision.Clone()
	next.Progress.Phase = "completed"
	next.Progress.MemberOffset = len(next.WaitMemberIDs)
	next.Progress.UpdatedAt = at
	_, err = j.put(ctx, current, next)
	return err
}

// ReconcileRelationStep 实时复核父子终态并推进一步解除；活动子存在时不修改父。
func (j *Journal) ReconcileRelationStep(ctx context.Context, tenant, id string, ops RelationEndOperator, at time.Time) (StoredRelation, error) {
	if ops == nil {
		return StoredRelation{}, policy.ErrInvalid
	}
	r, err := j.GetRelation(ctx, tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	if at.Before(r.Relation.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if r.Relation.State == "ended" {
		return r, j.completeEndedDecision(ctx, tenant, id, at)
	}
	parent, err := ops.RecoverMergeParent(ctx, tenant, r.Relation.ParentAlertID, id)
	if err != nil {
		return StoredRelation{}, err
	}
	if parent.Alert.Status == domain.AlertStatusActive {
		return r, nil
	}
	return j.EndRelationStep(ctx, tenant, id, parent, ops, at)
}

// RelationWorkPage 为关系终态复核提供有界的全局游标；单条变更仍须核对显式租户。
type RelationWorkPage struct {
	Relations []domain.MergeRelation
	Next      string
}

// ListRelationWork 不截断历史时间，已结束组不返回但推进游标，避免旧的活动关系被遗漏。
func (j *Journal) ListRelationWork(ctx context.Context, after string, limit int) (RelationWorkPage, error) {
	if len(after) > 256 || limit < 1 || limit > 16 {
		return RelationWorkPage{}, policy.ErrInvalid
	}
	rows, err := j.docs.List(ctx, "merge_relations", "", after, limit)
	if err != nil {
		return RelationWorkPage{}, err
	}
	if len(rows) > limit {
		return RelationWorkPage{}, policy.ErrInvalid
	}
	page, last := RelationWorkPage{}, after
	for _, raw := range rows {
		var r domain.MergeRelation
		if len(raw) > 256<<10 || json.Unmarshal(raw, &r) != nil || r.Validate() != nil {
			return RelationWorkPage{}, policy.ErrInvalid
		}
		key, err := decisionKey(r.TenantID, r.ID)
		if err != nil || key <= last {
			return RelationWorkPage{}, policy.ErrInvalid
		}
		last = key
		if r.State != "ended" {
			page.Relations = append(page.Relations, r)
		}
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}
