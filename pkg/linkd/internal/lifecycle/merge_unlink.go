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

	"linkd/internal/domain"
	"linkd/internal/store"
)

// UnlinkMergeMember 在成员 fingerprint lease 内结束已终结父的关系和等待，仅输出状态，不补发处置。
// 已终态成员保留历史摘要；活动成员保持真实 status/admission，等待下一条触发 Event 再判断。
func (p *Processor) UnlinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if p.mergeRelations == nil {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	return p.unlinkMergeMember(ctx, tenant, id, relation, func() (domain.MergeRelation, store.StoredAlert, error) {
		r, err := p.mergeRelations.GetMergeRelation(ctx, tenant, relation)
		if err != nil {
			return r, store.StoredAlert{}, err
		}
		if r.Validate() != nil {
			return r, store.StoredAlert{}, store.ErrInvalidArgument
		}
		parent, err := p.readMergeAlert(ctx, tenant, r.ParentAlertID)
		return r, parent, err
	})
}

// UnlinkMergeMemberPrepared 在同一窗口租约内复用已确认的终态父和固定关系事实。
// 父终态不可逆，关系只从 ending 前进到 ended；成员仍在自己的租约内实时读取/CAS。
func (p *Processor) UnlinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation, parent store.StoredAlert) (store.StoredAlert, error) {
	return p.unlinkMergeMember(ctx, tenant, id, relation.ID, func() (domain.MergeRelation, store.StoredAlert, error) {
		return relation, parent, nil
	})
}

func (p *Processor) unlinkMergeMember(ctx context.Context, tenant, id, relation string, facts func() (domain.MergeRelation, store.StoredAlert, error)) (store.StoredAlert, error) {
	for range maxCASAttempts {
		current, err := p.readMergeAlert(ctx, tenant, id)
		if err != nil {
			return store.StoredAlert{}, err
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return store.StoredAlert{}, err
		}
		r, parent, err := facts()
		if err != nil {
			return store.StoredAlert{}, err
		}
		update, changed, err := p.PrepareMergeMemberChange(ctx, current, parent, r, true)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if !changed {
			return update.Current, nil
		}
		updated, err := p.compareAndSetAlert(ctx, update.Current, update.Replacement)
		if err == nil {
			updated, err = p.finishMergeChange(ctx, updated)
		}

		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		return updated, err
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}
