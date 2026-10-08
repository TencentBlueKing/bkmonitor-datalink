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

// RecoverMergeParent 在父 fingerprint lease 内检查全部固定成员，均已终态才恢复父。
// 读取错误或缺失不代表子已恢复；有活动成员时返回未改变的父。已经终态的父不改写原有结束原因。
func (p *Processor) RecoverMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
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
		if r.Validate() != nil || r.TenantID != tenant || r.ID != relation || r.ParentAlertID != id || current.Version.IsZero() || current.Alert.BKTenantID != tenant || current.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || current.Alert.Fingerprint != r.ParentFingerprint || current.Alert.Merge == nil || current.Alert.Merge.Role != "aggregate" {
			return store.StoredAlert{}, store.ErrInvalidArgument
		}
		if current.Alert.Status.Terminal() {
			return current, nil
		}
		if r.State != "preparing" && r.State != "ready" {
			return store.StoredAlert{}, store.ErrInvalidTransition
		}
		for _, member := range r.Members {
			// terminal 是 ConfirmRelationMember 对真实仓储终态的不可逆确认；其余成员必须实时读取。
			if member.State == "terminal" {
				continue
			}
			actual, err := p.readMergeAlert(ctx, tenant, member.AlertID)
			if err != nil {
				return store.StoredAlert{}, err
			}
			if actual.Version.IsZero() || actual.Alert.Validate() != nil || actual.Alert.BKTenantID != tenant || actual.Alert.AlertID != member.AlertID {
				return store.StoredAlert{}, store.ErrInvalidArgument
			}
			if !actual.Alert.Status.Terminal() {
				return current, nil
			}
		}
		now, err := p.now()
		if err != nil {
			return store.StoredAlert{}, err
		}
		next := current.Alert.Clone()
		next.Status = domain.AlertStatusRecovered
		next.Shield = domain.AlertShield{}
		next.UpdateAt = nextAlertUpdateTime(now, current.Alert.UpdateAt)
		endedAt := next.UpdateAt
		next.EndAt = &endedAt
		next.EndType = domain.AlertEndTypeSystem
		next.EndReason = "merge_members_ended"
		operation := digestStrings("merge-parent-recover", tenant, id, relation)
		next.PolicyChange = terminalShieldChange(current.Alert, next.UpdateAt, operation)
		next.MergeChange = &domain.AlertMergeChange{Kind: "parent_recover", RelationID: relation, OperationID: operation, WindowID: r.WindowID, EffectiveAt: next.UpdateAt, Before: current.Alert.Merge.Clone(), After: next.Merge.Clone(), ActionReady: next.Admission.AdmittedAt != nil}
		if err := freezeAction(&next, current.Alert.Revision+1, AlertChangeCause{Type: AlertChangeCauseSystemOperation, ID: operation}, next.Admission.AdmittedAt != nil); err != nil {
			return store.StoredAlert{}, err
		}
		updated, err := p.compareAndSetAlert(ctx, current, next)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return store.StoredAlert{}, err
		}
		return p.finishPendingChanges(ctx, updated)
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}
