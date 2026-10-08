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
	"reflect"
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
		if _, ok := r.Member(id); !ok {
			return store.StoredAlert{}, store.ErrInvalidArgument
		}
		parent, err := p.readMergeAlert(ctx, tenant, r.ParentAlertID)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if parent.Version.IsZero() || parent.Alert.Validate() != nil || parent.Alert.BKTenantID != tenant || parent.Alert.AlertID != r.ParentAlertID || parent.Alert.Status != domain.AlertStatusActive || parent.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || parent.Alert.Fingerprint != r.ParentFingerprint || parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" {
			return store.StoredAlert{}, store.ErrInvalidTransition
		}
		if current.Alert.Status.Terminal() {
			return current, nil
		}
		if current.Alert.Merge != nil {
			for _, w := range current.Alert.Merge.Pending {
				if w.WindowID == r.WindowID && (w.GroupKey != r.GroupKey || !reflect.DeepEqual(w.Policy, r.Policy)) {
					return store.StoredAlert{}, store.ErrInvalidTransition
				}
			}
		}
		nextMerge, changed, err := current.Alert.Merge.LinkRelation(r.WindowID, relation)
		if err != nil {
			return store.StoredAlert{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
		}
		if !changed {
			return current, nil
		}
		next := current.Alert.Clone()
		next.Merge = nextMerge
		updated, err := p.commitMergeRelationChange(ctx, current, next, "member_link", r, false)
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
		local := *p
		local.severity = table
		local.configDigest = digest
		return local.ReadyMergeParent(ctx, tenant, id, relation)
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

func (p *Processor) commitMergeRelationChange(ctx context.Context, current store.StoredAlert, next domain.Alert, kind string, r domain.MergeRelation, ready bool) (store.StoredAlert, error) {
	now, err := p.now()
	if err != nil {
		return store.StoredAlert{}, err
	}
	next.UpdateAt = nextAlertUpdateTime(now, current.Alert.UpdateAt)
	operation := digestStrings("merge-relation-change", kind, next.BKTenantID, next.AlertID, r.ID)
	if ready {
		at := next.UpdateAt
		next.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: next.Severity, CauseType: AlertChangeCauseSystemOperation, CauseID: operation}
	}
	next.MergeChange = &domain.AlertMergeChange{Kind: kind, RelationID: r.ID, OperationID: operation, WindowID: r.WindowID, EffectiveAt: next.UpdateAt, Before: current.Alert.Merge.Clone(), After: next.Merge.Clone(), ActionReady: ready}
	if err := freezeAction(&next, current.Alert.Revision+1, AlertChangeCause{Type: AlertChangeCauseSystemOperation, ID: operation}, ready); err != nil {
		return store.StoredAlert{}, err
	}
	updated, err := p.compareAndSetAlert(ctx, current, next)
	if err != nil {
		return store.StoredAlert{}, err
	}
	return p.finishMergeChange(ctx, updated)
}
