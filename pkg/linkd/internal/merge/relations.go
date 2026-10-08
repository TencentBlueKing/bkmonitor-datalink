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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// StoredRelation 为关系准备进度保留不透明 CAS token。
type StoredRelation struct {
	Relation domain.MergeRelation
	Version  string
}

type relationReference struct {
	TenantID   string `json:"bk_tenant_id"`
	AlertID    string `json:"alert_id"`
	RelationID string `json:"relation_id"`
	Role       string `json:"role"`
}

// GetRelation 按明确租户实时读取关系，不依赖 Redis 查询索引。
func (j *Journal) GetRelation(ctx context.Context, tenant, id string) (StoredRelation, error) {
	key, err := decisionKey(tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	raw, version, err := j.docs.Get(ctx, "merge_relations", key)
	if err != nil {
		return StoredRelation{}, err
	}
	var r domain.MergeRelation
	if len(raw) > 256<<10 || json.Unmarshal(raw, &r) != nil || version == "" {
		return StoredRelation{}, policy.ErrInvalid
	}
	if r.TenantID != tenant || r.ID != id {
		return StoredRelation{}, policy.ErrAccess
	}
	r = r.Clone()
	if err := r.Validate(); err != nil {
		return StoredRelation{}, err
	}
	return StoredRelation{Relation: r, Version: version}, nil
}

// GetMergeRelation 适配 Lifecycle 的只读关系事实端口，返回完整有界组而非预计父 ID。
func (j *Journal) GetMergeRelation(ctx context.Context, tenant, id string) (domain.MergeRelation, error) {
	v, err := j.GetRelation(ctx, tenant, id)
	return v.Relation, err
}

// EnsureRelation 只在真实父已经记录为 linking 后创建关系意图；此时还不能开放父处置。
// ID 复用本次操作身份，父/策略/成员集合创建后不可改写；成员记录按 AlertID 排序，最多 256 项。
func (j *Journal) EnsureRelation(ctx context.Context, tenant, id string, parent store.StoredAlert, at time.Time) (StoredRelation, error) {
	return j.ensureRelation(ctx, tenant, id, parent, at, false)
}

func (j *Journal) ensureRelation(ctx context.Context, tenant, id string, parent store.StoredAlert, at time.Time, ending bool) (StoredRelation, error) {
	current, err := j.Get(ctx, tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	d, p := current.Decision, parent.Alert
	allowedPhase := d.Progress.Phase == "linking"
	if ending {
		allowedPhase = allowedPhase || d.Progress.Phase == "ending" || d.Progress.Phase == "completed"
	}
	if !allowedPhase || d.Outcome != "succeeded" || parent.Version.IsZero() || p.Validate() != nil || p.Merge == nil || p.Merge.Role != "aggregate" || at.Before(d.Progress.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if p.BKTenantID != tenant || p.AlertID != d.Progress.ParentAlertID || p.EventSourceID != domain.BuiltinMergeEventSourceID || p.Fingerprint != d.Progress.ParentEvent.Fingerprint {
		return StoredRelation{}, policy.ErrAccess
	}
	if !ending && p.Status != domain.AlertStatusActive {
		return StoredRelation{}, store.ErrInvalidTransition
	}
	r := domain.MergeRelation{ID: id, TenantID: tenant, WindowID: d.WindowID, GroupKey: d.GroupKey, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, ParentAlertID: p.AlertID, ParentFingerprint: p.Fingerprint, WaitMemberIDs: slices.Clone(d.WaitMemberIDs), State: "preparing", CreatedAt: d.FrozenAt, UpdatedAt: at}
	for _, member := range d.MemberIDs {
		r.Members = append(r.Members, domain.MergeRelationMember{AlertID: member, State: "pending"})
	}
	r = r.Clone()
	if err := r.Validate(); err != nil {
		return StoredRelation{}, err
	}
	key, err := decisionKey(tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return StoredRelation{}, err
	}
	err = j.docs.Put(ctx, "merge_relations", key, "", raw)
	if err != nil && !errors.Is(err, policy.ErrConflict) {
		return StoredRelation{}, err
	}
	actual, err := j.GetRelation(ctx, tenant, id)
	if err != nil {
		return StoredRelation{}, err
	}
	if !reflect.DeepEqual(relationFacts(actual.Relation), relationFacts(r)) {
		return StoredRelation{}, policy.ErrConflict
	}
	return actual, nil
}

func relationFacts(r domain.MergeRelation) domain.MergeRelation {
	r = r.Clone()
	r.State = "preparing"
	r.IndexOffset = 0
	r.UpdatedAt = r.CreatedAt
	r.EndOffset = 0
	r.EndReason = ""
	r.EndStartedAt = nil
	r.EndedAt = nil
	for i := range r.Members {
		r.Members[i].State = "pending"
	}
	return r
}

func (j *Journal) putRelation(ctx context.Context, current StoredRelation, next domain.MergeRelation) (StoredRelation, error) {
	next = next.Clone()
	if current.Version == "" || next.Validate() != nil || !reflect.DeepEqual(relationFacts(current.Relation), relationFacts(next)) || next.UpdatedAt.Before(current.Relation.UpdatedAt) || next.IndexOffset < current.Relation.IndexOffset {
		return StoredRelation{}, policy.ErrInvalid
	}
	beforeRelation := current.Relation
	rank := map[string]int{"preparing": 0, "ready": 1, "ending": 2, "ended": 3}
	if rank[next.State] < rank[beforeRelation.State] || next.EndOffset < beforeRelation.EndOffset || (beforeRelation.State == "ended" && !reflect.DeepEqual(beforeRelation, next)) {
		return StoredRelation{}, policy.ErrConflict
	}
	if beforeRelation.EndStartedAt != nil && (!reflect.DeepEqual(beforeRelation.EndStartedAt, next.EndStartedAt) || beforeRelation.EndReason != next.EndReason) {
		return StoredRelation{}, policy.ErrConflict
	}
	for i, before := range current.Relation.Members {
		after := next.Members[i]
		if (before.State == "terminal" && after.State != "terminal") || (before.State == "linked" && after.State == "pending") {
			return StoredRelation{}, policy.ErrConflict
		}
	}
	key, err := decisionKey(next.TenantID, next.ID)
	if err != nil {
		return StoredRelation{}, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return StoredRelation{}, err
	}
	if err := j.docs.Put(ctx, "merge_relations", key, current.Version, raw); err != nil {
		return StoredRelation{}, err
	}
	return j.GetRelation(ctx, next.TenantID, next.ID)
}

func referenceScope(tenant, alert string) (string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes {
		return "", policy.ErrInvalid
	}
	return prefix(tenant) + hash("relation-alert", alert) + ":", nil
}

// IndexRelationStep 先 create-only 写一个父/成员反查引用，再推进关系内前缀，单步中断可重复执行。
// 查询引用不是关系状态的第二份权威副本；读取必须回到 GetRelation，准备未完成也应如实展示。
func (j *Journal) IndexRelationStep(ctx context.Context, current StoredRelation, at time.Time) (StoredRelation, error) {
	if err := ctx.Err(); err != nil {
		return StoredRelation{}, err
	}
	r := current.Relation
	if r.Validate() != nil || current.Version == "" || at.Before(r.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if r.IndexOffset == len(r.Members)+1 {
		return current, nil
	}
	ref := relationReference{TenantID: r.TenantID, AlertID: r.ParentAlertID, RelationID: r.ID, Role: "parent"}
	if r.IndexOffset > 0 {
		ref.AlertID = r.Members[r.IndexOffset-1].AlertID
		ref.Role = "member"
	}
	scope, err := referenceScope(ref.TenantID, ref.AlertID)
	if err != nil {
		return StoredRelation{}, err
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return StoredRelation{}, err
	}
	err = j.docs.Put(ctx, "merge_relation_refs", scope+ref.RelationID, "", raw)
	if err != nil && !errors.Is(err, policy.ErrConflict) {
		return StoredRelation{}, err
	}
	actual, _, err := j.docs.Get(ctx, "merge_relation_refs", scope+ref.RelationID)
	if err != nil {
		return StoredRelation{}, err
	}
	var saved relationReference
	if json.Unmarshal(actual, &saved) != nil || saved != ref {
		return StoredRelation{}, policy.ErrConflict
	}
	next := r.Clone()
	next.IndexOffset++
	next.UpdatedAt = at
	return j.putRelation(ctx, current, next)
}

// ConfirmRelationMember 只接受成员已保存关系或真实终态的仓储结果；意图未输出完成时不确认。
func (j *Journal) ConfirmRelationMember(ctx context.Context, current StoredRelation, member store.StoredAlert, at time.Time) (StoredRelation, error) {
	if err := ctx.Err(); err != nil {
		return StoredRelation{}, err
	}
	r, a := current.Relation, member.Alert
	if (r.State != "preparing" && r.State != "ready") || r.Validate() != nil || current.Version == "" || member.Version.IsZero() || a.Validate() != nil || a.MergeChange != nil || a.PolicyChange != nil || r.IndexOffset != len(r.Members)+1 || at.Before(r.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if a.BKTenantID != r.TenantID {
		return StoredRelation{}, policy.ErrAccess
	}
	if _, ok := r.Member(a.AlertID); !ok {
		return StoredRelation{}, policy.ErrAccess
	}
	state := "linked"
	if a.Status.Terminal() {
		state = "terminal"
	} else {
		if a.Merge == nil || a.Merge.Role != "original" || !slices.Contains(a.Merge.RelationIDs, r.ID) {
			return StoredRelation{}, fmt.Errorf("member has not stored merge relation")
		}
		for _, wait := range a.Merge.Pending {
			if wait.WindowID == r.WindowID {
				return StoredRelation{}, fmt.Errorf("linked member still waits for same window")
			}
		}
	}
	next := r.Clone()
	next.UpdatedAt = at
	for i, m := range next.Members {
		if m.AlertID == a.AlertID {
			if m.State == state {
				return current, nil
			}
			next.Members[i].State = state
		}
	}
	return j.putRelation(ctx, current, next)
}

// ReadyRelation 仅在全部真实成员和查询引用确认后开放父端最后一步，不直接变更父 Alert。
func (j *Journal) ReadyRelation(ctx context.Context, current StoredRelation, at time.Time) (StoredRelation, error) {
	if err := ctx.Err(); err != nil {
		return StoredRelation{}, err
	}
	if current.Version == "" || current.Relation.Validate() != nil || at.Before(current.Relation.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if current.Relation.State == "ready" {
		return current, nil
	}
	next := current.Relation.Clone()
	next.State = "ready"
	next.UpdatedAt = at
	return j.putRelation(ctx, current, next)
}

// RelationPage 提供某个 Alert 参与的父/成员关系页，不在 Alert 中堆放无限历史成员。
type RelationPage struct {
	Relations []domain.MergeRelation `json:"items"`
	Next      string                 `json:"next"`
}

// ListRelations 的游标与租户、Alert 绑定；引用不可伪造为另一组成员，部分读取失败整页失败。
func (j *Journal) ListRelations(ctx context.Context, tenant, alert, after string, limit int) (RelationPage, error) {
	scope, err := referenceScope(tenant, alert)
	if err != nil {
		return RelationPage{}, err
	}
	if limit < 1 || limit > 16 || len(after) > 256 || (after != "" && !strings.HasPrefix(after, scope)) {
		return RelationPage{}, policy.ErrInvalid
	}
	rows, err := j.docs.List(ctx, "merge_relation_refs", scope, after, limit)
	if err != nil {
		return RelationPage{}, err
	}
	if len(rows) > limit {
		return RelationPage{}, policy.ErrInvalid
	}
	result := RelationPage{Relations: []domain.MergeRelation{}}
	last := after
	for _, raw := range rows {
		var ref relationReference
		if len(raw) > 4096 || json.Unmarshal(raw, &ref) != nil {
			return RelationPage{}, policy.ErrInvalid
		}
		if ref.TenantID != tenant || ref.AlertID != alert {
			return RelationPage{}, policy.ErrAccess
		}
		key := scope + ref.RelationID
		if key <= last {
			return RelationPage{}, policy.ErrInvalid
		}
		last = key
		stored, err := j.GetRelation(ctx, tenant, ref.RelationID)
		if err != nil {
			return RelationPage{}, err
		}
		switch ref.Role {
		case "parent":
			if stored.Relation.ParentAlertID != alert {
				return RelationPage{}, policy.ErrAccess
			}
		case "member":
			if _, ok := stored.Relation.Member(alert); !ok {
				return RelationPage{}, policy.ErrAccess
			}
		default:
			return RelationPage{}, policy.ErrInvalid
		}
		result.Relations = append(result.Relations, stored.Relation)
	}
	if len(rows) == limit {
		result.Next = last
	}
	return result, nil
}

// RelationMemberPage 是固定关系组的成员摘要页；详细快照通过裁决成员接口单独读取。
type RelationMemberPage struct {
	Members []domain.MergeRelationMember `json:"items"`
	Next    string                       `json:"next"`
}

// ListRelationMembers 对固定成员列表分页，游标绑定租户与关系，最大 16 项；不返回部分错误结果。
func (j *Journal) ListRelationMembers(ctx context.Context, tenant, id, after string, limit int) (RelationMemberPage, error) {
	key, err := decisionKey(tenant, id)
	if err != nil {
		return RelationMemberPage{}, err
	}
	scope := key + ":members:"
	if limit < 1 || limit > 16 || len(after) > 256 || (after != "" && !strings.HasPrefix(after, scope)) {
		return RelationMemberPage{}, policy.ErrInvalid
	}
	offset := 0
	if after != "" {
		offset, err = strconv.Atoi(strings.TrimPrefix(after, scope))
		if err != nil || offset < 1 {
			return RelationMemberPage{}, policy.ErrInvalid
		}
	}
	current, err := j.GetRelation(ctx, tenant, id)
	if err != nil {
		return RelationMemberPage{}, err
	}
	if offset > len(current.Relation.Members) {
		return RelationMemberPage{}, policy.ErrInvalid
	}
	end := min(offset+limit, len(current.Relation.Members))
	result := RelationMemberPage{Members: slices.Clone(current.Relation.Members[offset:end])}
	if end < len(current.Relation.Members) {
		result.Next = scope + strconv.Itoa(end)
	}
	return result, nil
}
