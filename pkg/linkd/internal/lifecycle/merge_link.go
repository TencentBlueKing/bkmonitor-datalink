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
	"fmt"
	"slices"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ErrMergeMembersEnded 表示关系成员均已终结；控制任务应恢复父告警，不能先发出触发处置。
var ErrMergeMembersEnded = errors.New("merge members are all terminal")

// MergeRelationReader 返回已持久化关系组；调用方不能只提交预计父 ID 作为建立关系的凭据。
type MergeRelationReader interface {
	GetMergeRelation(context.Context, string, string) (domain.MergeRelation, error)
}

// WithMergeRelations 注入控制面关系操作的实时只读端口；不在普通事件中创建关系。
func WithMergeRelations(reader MergeRelationReader) ProcessorOption {
	return func(p *Processor) { p.mergeRelations = reader }
}

// readMergeAlert 在处理待输出意图或据此放行父告警前，核对实时读取的完整身份和业务快照。
// 存储返回的关联 ID 不能单独充当证据：错误租户/Alert、缺失 CAS token 或损坏字段均须阻止后续副作用。
func (p *Processor) readMergeAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	current, err := p.getAlertCurrent(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if current.Version.IsZero() || current.Alert.BKTenantID != tenant || current.Alert.AlertID != id || current.Alert.Validate() != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: merge alert scope or snapshot invalid", store.ErrInvalidArgument)
	}
	return current, nil
}

func (p *Processor) readMergeRelation(ctx context.Context, tenant, id string) (domain.MergeRelation, error) {
	if p.mergeRelations == nil {
		return domain.MergeRelation{}, fmt.Errorf("merge relation reader is not configured")
	}
	r, err := p.mergeRelations.GetMergeRelation(ctx, tenant, id)
	if err != nil {
		return r, err
	}
	if r.Validate() != nil || r.TenantID != tenant || r.ID != id {
		return domain.MergeRelation{}, fmt.Errorf("merge relation scope or facts mismatch")
	}
	if (r.State != "preparing" && r.State != "ready") || r.IndexOffset != len(r.Members)+1 {
		return domain.MergeRelation{}, fmt.Errorf("merge relation references are not complete")
	}
	return r, nil
}

// LinkMergeMember 必须在成员 fingerprint lease 内执行；先核对持久化关系和真实父，再更新成员摘要。
// 不取得父锁，不改变成员真实生命周期/丰富/屏蔽状态；已终态成员只返回事实供关系记录确认。
func (p *Processor) LinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return p.linkMergeMember(ctx, tenant, id, relation, func() (domain.MergeRelation, error) {
		return p.readMergeRelation(ctx, tenant, relation)
	})
}

// LinkMergeMemberPrepared 复用同一窗口租约内已读取的固定关系，活动父和子仍逐项实时读取。
// 关系在批边界完整校验，成员不能重复读取/校验整个大型关系对象。
func (p *Processor) LinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation) (store.StoredAlert, error) {
	if relation.TenantID != tenant || (relation.State != "preparing" && relation.State != "ready") || relation.IndexOffset != len(relation.Members)+1 {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	return p.linkMergeMember(ctx, tenant, id, relation.ID, func() (domain.MergeRelation, error) { return relation, nil })
}

func (p *Processor) linkMergeMember(ctx context.Context, tenant, id, relation string, readRelation func() (domain.MergeRelation, error)) (store.StoredAlert, error) {
	for range maxCASAttempts {
		current, err := p.readMergeAlert(ctx, tenant, id)
		if err != nil {
			return store.StoredAlert{}, err
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return store.StoredAlert{}, err
		}
		r, err := readRelation()
		if err != nil {
			return store.StoredAlert{}, err
		}
		parent, err := p.readMergeAlert(ctx, tenant, r.ParentAlertID)
		if err != nil {
			return store.StoredAlert{}, err
		}
		update, changed, err := p.PrepareMergeMemberChange(ctx, current, parent, r, false)
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

// ReadyMergeParent 在父 fingerprint lease 内执行，只为已经完整建立的关系开放资格。
// 至少一个真实成员仍活动且保留关系；全部终态返回专用结果供恢复任务处理，不产生短暂的触发通知。
func (p *Processor) ReadyMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if provider, ok := p.severity.(interface {
		FreezeSeverity() (SeverityTable, string)
	}); ok {
		table, digest := provider.FreezeSeverity()
		return p.withFrozenSeverity(table, digest).ReadyMergeParent(ctx, tenant, id, relation)
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
		r, err := p.readMergeRelation(ctx, tenant, relation)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if r.State != "ready" || r.ParentAlertID != id || current.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || current.Alert.Fingerprint != r.ParentFingerprint {
			return store.StoredAlert{}, store.ErrInvalidTransition
		}
		if current.Alert.Status != domain.AlertStatusActive {
			return store.StoredAlert{}, store.ErrInvalidTransition
		}
		nextMerge, changed, err := current.Alert.Merge.WithReadyRelation(relation)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if !changed {
			return current, nil
		}
		active := false
		for _, member := range r.Members {
			if member.State == "terminal" {
				continue
			}
			actual, err := p.readMergeAlert(ctx, tenant, member.AlertID)
			if err != nil {
				return store.StoredAlert{}, err
			}
			if actual.Alert.Status.Terminal() {
				continue
			}
			if actual.Alert.Merge == nil || !slices.Contains(actual.Alert.Merge.RelationIDs, relation) || actual.Alert.MergeChange != nil {
				return store.StoredAlert{}, store.ErrInvalidTransition
			}
			active = true
			break
		}
		if !active {
			return store.StoredAlert{}, ErrMergeMembersEnded
		}
		next := current.Alert.Clone()
		next.Merge = nextMerge
		_, known := p.severity.Priority(next.Severity)
		ready := known && !next.Shield.Active && (next.Admission.AdmittedAt == nil || next.Admission.Severity != next.Severity)
		updated, err := p.commitMergeRelationChange(ctx, current, next, "parent_ready", r, ready)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		return updated, err
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}

func (p *Processor) prepareMergeRelationChange(ctx context.Context, current store.StoredAlert, next domain.Alert, kind string, r domain.MergeRelation, ready bool) (domain.Alert, error) {
	return p.prepareMergeMemberRelationChange(ctx, current, next, kind, r.MemberContext(), ready)
}

func (p *Processor) prepareMergeMemberRelationChange(ctx context.Context, current store.StoredAlert, next domain.Alert, kind string, r domain.MergeMemberContext, ready bool) (domain.Alert, error) {
	now, err := p.now()
	if err != nil {
		return domain.Alert{}, err
	}
	next.UpdateAt = nextAlertUpdateTime(now, current.Alert.UpdateAt)
	operation := digestStrings("merge-relation-change", kind, next.BKTenantID, next.AlertID, r.ID)
	if ready {
		at := next.UpdateAt
		next.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: next.Severity, CauseType: AlertChangeCauseSystemOperation, CauseID: operation}
	}
	next.MergeChange = &domain.AlertMergeChange{Kind: kind, RelationID: r.ID, OperationID: operation, WindowID: r.WindowID, EffectiveAt: next.UpdateAt, Before: current.Alert.Merge.Clone(), After: next.Merge.Clone(), ActionReady: ready}
	if err := freezeMergeMemberFlow(next.MergeChange, &r); err != nil {
		return domain.Alert{}, err
	}
	return next, nil
}

func (p *Processor) commitMergeRelationChange(ctx context.Context, current store.StoredAlert, next domain.Alert, kind string, r domain.MergeRelation, ready bool) (store.StoredAlert, error) {
	next, err := p.prepareMergeRelationChange(ctx, current, next, kind, r, ready)
	if err != nil {
		return store.StoredAlert{}, err
	}
	updated, err := p.compareAndSetAlert(ctx, current, next)
	if err != nil {
		return store.StoredAlert{}, err
	}
	return p.finishMergeChange(ctx, updated)
}
