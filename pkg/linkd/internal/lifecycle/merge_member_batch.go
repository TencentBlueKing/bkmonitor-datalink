// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// PrepareMergeMemberChange 复用单成员规则，在其租约内从批读快照准备建联/解除。
// changed=false 表示已完成或终态；本方法不提交新的成员变更，旧待完成意图仍先恢复。
func (p *Processor) PrepareMergeMemberChange(ctx context.Context, current, parent store.StoredAlert, r domain.MergeRelation, unlink bool) (store.AlertSnapshotUpdate, bool, error) {
	id := current.Alert.AlertID
	if unlink {
		_, member := slices.BinarySearch(r.WaitMemberIDs, id)
		if (r.State != "ending" && r.State != "ended") || r.IndexOffset != len(r.Members)+1 || !member {
			return store.AlertSnapshotUpdate{}, false, store.ErrInvalidArgument
		}
	} else if _, ok := r.Member(id); !ok {
		return store.AlertSnapshotUpdate{}, false, store.ErrInvalidArgument
	}
	return p.PrepareMergeMemberBatchChange(ctx, current, parent, r.MemberContext(), unlink)
}

// PrepareMergeMemberBatchChange 用于已经持久化分派到本批的成员，固定事实不随整组大小增长。
func (p *Processor) PrepareMergeMemberBatchChange(ctx context.Context, current, parent store.StoredAlert, r domain.MergeMemberContext, unlink bool) (store.AlertSnapshotUpdate, bool, error) {
	current, err := p.finishPendingChanges(ctx, current)
	update := store.AlertSnapshotUpdate{Current: current}
	if err != nil {
		return update, false, err
	}
	tenant := current.Alert.BKTenantID
	if r.TenantID != tenant || parent.Alert.BKTenantID != tenant || parent.Alert.AlertID != r.ParentAlertID || parent.Alert.Fingerprint != r.ParentFingerprint || parent.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" {
		return update, false, store.ErrInvalidTransition
	}
	if unlink {
		if !parent.Alert.Status.Terminal() {
			return update, false, store.ErrInvalidTransition
		}
	} else {
		if parent.Version.IsZero() || parent.Alert.Validate() != nil || parent.Alert.Status != domain.AlertStatusActive {
			return update, false, store.ErrInvalidTransition
		}
	}
	if current.Alert.Status.Terminal() {
		return update, false, nil
	}
	var nextMerge *domain.AlertMerge
	var changed bool
	kind := "member_unlink"
	if unlink {
		nextMerge, changed, err = current.Alert.Merge.WithoutRelation(r.WindowID, r.ID)
	} else {
		kind = "member_link"
		if current.Alert.Merge != nil {
			for _, w := range current.Alert.Merge.Pending {
				if w.WindowID == r.WindowID && (w.GroupKey != r.GroupKey || !reflect.DeepEqual(w.Policy, r.Policy)) {
					return update, false, store.ErrInvalidTransition
				}
			}
		}
		nextMerge, changed, err = current.Alert.Merge.LinkRelation(r.WindowID, r.ID)
	}
	if err != nil || !changed {
		return update, changed, err
	}
	next := current.Alert.Clone()
	next.Merge = nextMerge
	update.Replacement, err = p.prepareMergeMemberRelationChange(ctx, current, next, kind, r, false)
	return update, err == nil, err
}

// MergeMemberChangeOutput 在批量 CAS 成功后同步索引、缓存与输出，返回待批写的稳定日志。
func (p *Processor) MergeMemberChangeOutput(ctx context.Context, current store.StoredAlert) ([]domain.AlertLog, error) {
	started := time.Now()
	err := p.observeDependencyIndex(ctx, current.Alert)
	store.ObserveBatchPhase(ctx, "member.dependency_index", time.Since(started))
	if err != nil {
		return nil, err
	}
	return p.mergeChangeOutput(ctx, current)
}

// RepairMergeAlertCache 仅在批次单项版本冲突时重读，沿用单成员 CAS 的缓存修复。
func (p *Processor) RepairMergeAlertCache(ctx context.Context, tenant, id string) error {
	return p.repairRecentAlert(ctx, tenant, id)
}

// CacheCompletedMergeChanges 仅对同一来源的活动告警合批；终态仍按原规则修复新生命周期缓存。
func (p *Processor) CacheCompletedMergeChanges(ctx context.Context, current []store.StoredAlert) []error {
	results := make([]error, len(current))
	active, positions := []store.StoredAlert{}, []int{}
	for i, item := range current {
		if item.Alert.Status.Terminal() {
			results[i] = p.cacheMergeSnapshot(ctx, item)
		} else {
			active = append(active, item)
			positions = append(positions, i)
		}
	}
	if cache, ok := p.recentAlerts.(interface {
		PutCurrentSnapshots(context.Context, []store.StoredAlert) []error
	}); ok && len(active) > 0 {
		errors := cache.PutCurrentSnapshots(ctx, active)
		for i, position := range positions {
			results[position] = errors[i]
		}
	} else {
		for i, item := range active {
			results[positions[i]] = p.recentAlerts.PutCurrent(ctx, item)
		}
	}
	return results
}
