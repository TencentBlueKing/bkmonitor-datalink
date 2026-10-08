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
	"errors"
	"slices"
	"time"

	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// RelationOperator 在目标 Alert 自己的 fingerprint lease 内执行成员及父状态操作。
// 每个方法成功时必须完成该步已保存的意图；不能同时持有多个 fingerprint 锁。
type RelationOperator interface {
	MemberReleaser
	RelationEndOperator
	LinkMergeMember(context.Context, string, string, string) (store.StoredAlert, error)
	ReadyMergeParent(context.Context, string, string, string) (store.StoredAlert, error)
}

// LinkStep 为一次成功裁决推进一个引用/成员步骤，或在全部成员完成后开放真实父。
// parent 必须是本轮实时仓储读取；父终结后只解除关系，全部子终结时先恢复父再解除。
func (j *Journal) LinkStep(ctx context.Context, tenant, id string, parent store.StoredAlert, ops RelationOperator, at time.Time) (StoredDecision, error) {
	if ops == nil {
		return StoredDecision{}, policy.ErrInvalid
	}
	current, err := j.Get(ctx, tenant, id)
	if err != nil {
		return StoredDecision{}, err
	}
	d := current.Decision
	if d.Progress.Phase == "completed" {
		return current, nil
	}
	if d.Progress.Phase == "ending" || (d.Progress.Phase == "linking" && parent.Alert.Status.Terminal()) {
		if _, err := j.EndRelationStep(ctx, tenant, id, parent, ops, at); err != nil {
			return StoredDecision{}, err
		}
		return j.Get(ctx, tenant, id)
	}
	if d.Progress.Phase != "linking" || at.Before(d.Progress.UpdatedAt) {
		return StoredDecision{}, policy.ErrInvalid
	}
	relation, err := j.EnsureRelation(ctx, tenant, id, parent, at)
	if err != nil {
		return StoredDecision{}, err
	}
	if relation.Relation.IndexOffset < len(relation.Relation.Members)+1 {
		_, err := j.IndexRelationStep(ctx, relation, at)
		return current, err
	}
	if d.Progress.MemberOffset < len(d.WaitMemberIDs) {
		member := d.WaitMemberIDs[d.Progress.MemberOffset]
		if slices.Contains(d.MemberIDs, member) {
			linked, err := ops.LinkMergeMember(ctx, tenant, member, id)
			if err != nil {
				return StoredDecision{}, err
			}
			if _, err := j.ConfirmRelationMember(ctx, relation, linked, at); err != nil {
				return StoredDecision{}, err
			}
		} else {
			released, err := ops.ReleaseMergeWindow(ctx, tenant, member, d.WindowID)
			if err := validateReleasedMember(tenant, member, d.WindowID, released, err); err != nil {
				return StoredDecision{}, err
			}
		}
		return j.AdvanceMembers(ctx, current, d.Progress.MemberOffset+1, at)
	}
	if _, err := j.ReadyRelation(ctx, relation, at); err != nil {
		return StoredDecision{}, err
	}
	ready, err := ops.ReadyMergeParent(ctx, tenant, d.Progress.ParentAlertID, id)
	if errors.Is(err, lifecycle.ErrMergeMembersEnded) {
		if _, err := j.ReconcileRelationStep(ctx, tenant, id, ops, at); err != nil {
			return StoredDecision{}, err
		}
		return j.Get(ctx, tenant, id)
	}
	if err != nil {
		return StoredDecision{}, err
	}
	return j.Complete(ctx, current, &ready, at)
}
