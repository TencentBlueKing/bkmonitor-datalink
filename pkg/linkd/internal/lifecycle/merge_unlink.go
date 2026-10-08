// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"slices"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// UnlinkMergeMember 在成员 fingerprint lease 内结束已终结父的关系和等待，仅输出状态，不补发处置。
// 已终态成员保留历史摘要；活动成员保持真实 status/admission，等待下一条触发 Event 再判断。
func (p *Processor) UnlinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if p.mergeRelations == nil {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	for range maxCASAttempts {
		current, err := p.readMergeAlert(ctx, tenant, id)
		if err != nil {
			return store.StoredAlert{}, err
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return store.StoredAlert{}, err
		}
		r, err := p.mergeRelations.GetMergeRelation(ctx, tenant, relation)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if r.Validate() != nil || r.TenantID != tenant || r.ID != relation || (r.State != "ending" && r.State != "ended") || r.IndexOffset != len(r.Members)+1 || !slices.Contains(r.WaitMemberIDs, id) {
			return store.StoredAlert{}, store.ErrInvalidArgument
		}
		parent, err := p.readMergeAlert(ctx, tenant, r.ParentAlertID)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if parent.Alert.Fingerprint != r.ParentFingerprint || !parent.Alert.Status.Terminal() || parent.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" {
			return store.StoredAlert{}, store.ErrInvalidTransition
		}
		if current.Alert.Status.Terminal() {
			return current, nil
		}
		nextMerge, changed, err := current.Alert.Merge.WithoutRelation(r.WindowID, relation)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if !changed {
			return current, nil
		}
		next := current.Alert.Clone()
		next.Merge = nextMerge
		updated, err := p.commitMergeRelationChange(ctx, current, next, "member_unlink", r, false)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		return updated, err
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}
