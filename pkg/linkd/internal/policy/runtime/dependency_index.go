// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"errors"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

// ErrDependencyIndexNotReady 要求重试当前事件；未完成回填不能被当作无主而放行。
var ErrDependencyIndexNotReady error = dependencyIndexNotReadyError{}

type dependencyIndexNotReadyError struct{}

func (dependencyIndexNotReadyError) Error() string { return "dependency candidate index is not ready" }

func (dependencyIndexNotReadyError) PolicyPreparationPending() bool { return true }

// DependencyCandidateIndex 只共享合资格主的排序和引用，不复制完整 Alert 到各 Worker。
type DependencyCandidateIndex interface {
	ApplyDependencyCandidates(context.Context, redisstate.DependencyCandidateUpdate) error
	TopDependencyCandidate(context.Context, string, redisstate.DependencyIndexRef) (redisstate.DependencyCandidate, bool, error)
	MarkDependencyIndexReady(context.Context, string, redisstate.DependencyIndexRef) error
	RemoveDependencyCandidate(context.Context, string, redisstate.DependencyIndexRef, redisstate.DependencyCandidate) error
}

func dependencyVersion(f policy.FrozenPolicy) domain.PolicyVersion {
	return domain.PolicyVersion{ID: f.Release.ID, Version: f.Release.Version, Digest: f.Compiled.Summary.Digest}
}

func (s *Shielder) dependencyIndexRef(f policy.FrozenPolicy) redisstate.DependencyIndexRef {
	return redisstate.DependencyIndexRef{PolicyVersion: dependencyVersion(f), ConfigDigest: s.ConfigDigest}
}

func dependencyPolicy(f policy.FrozenPolicy) bool {
	return f.Release.Kind == policy.Shield && f.Compiled.Shield != nil && f.Compiled.Shield.ShieldType == "rely_shield"
}

func alertCandidateReader(a domain.Alert, level policy.LevelMapper) policy.CandidateReader {
	type viewKey struct{ field, path, readPath string }
	views := map[viewKey]*policy.FactView{}
	return func(ctx context.Context, field string, mapping policy.FieldMapping) (policy.Value, error) {
		key := viewKey{}
		var mappings map[string]policy.FieldMapping
		if mapping.Path != "" {
			key = viewKey{field: field, path: mapping.Path, readPath: mapping.ReadPath}
			mappings = map[string]policy.FieldMapping{field: mapping}
		}
		view := views[key]
		if view == nil {
			var err error
			view, err = policy.AlertView(a, mappings, level, policy.RelationContext{})
			if err != nil {
				return policy.Value{}, err
			}
			views[key] = view
		}
		return view.Field(ctx, field)
	}
}

func (s *Shielder) dependencyCandidateUpdate(ctx context.Context, a domain.Alert, policies []policy.FrozenPolicy, at time.Time, level policy.LevelMapper) (redisstate.DependencyCandidateUpdate, error) {
	u := redisstate.DependencyCandidateUpdate{TenantID: a.BKTenantID, ConfigDigest: s.ConfigDigest, AlertID: a.AlertID, Revision: a.Revision, BeginAt: a.BeginAt, Clear: !a.AdmittedActiveMain()}
	if u.Clear {
		return u, nil
	}
	for _, f := range policies {
		if !dependencyPolicy(f) {
			continue
		}
		if f.Release.TenantID != a.BKTenantID {
			return u, policy.ErrAccess
		}
		m := redisstate.DependencyMembership{Policy: dependencyVersion(f)}
		view, err := policy.AlertView(a, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		if err != nil {
			return u, err
		}
		possible, err := f.Compiled.Conditions[0].PossibleMatch(ctx, view)
		if errors.Is(err, policy.ErrUnavailable) {
			u.Memberships = append(u.Memberships, m)
			continue
		}
		if err != nil {
			return u, err
		}
		if possible {
			call, read := TrackDependencyReads(ctx)
			verdict, err := s.Loader.evaluate(call, f.Release, f.Compiled, view, &evaluationTargets{reader: s.Loader.Targets}, at, false, false)
			m.ObservedAt = read.Time()
			if m.ObservedAt.IsZero() {
				m.ObservedAt = time.Now().UTC()
			}
			if err != nil {
				return u, err
			}
			if !verdict.Evaluated {
				switch verdict.Reason {
				case "business_field_unavailable", "business_field_invalid", "instance_field_unavailable", "condition_unavailable":
				default:
					return u, policy.ErrUnavailable
				}
			}
			m.Matched = verdict.Evaluated && verdict.Matched
		}
		u.Memberships = append(u.Memberships, m)
	}
	return u, nil
}

// ObserveDependencyAlert 在真实 Create/CAS 成功后更新主资格，跨来源共享 revision 顺序。
// 失去资格只清理反向索引；合资格 Alert 只求值内存候选规则，不能遍历全部策略。
func (s *Shielder) ObserveDependencyAlert(ctx context.Context, a domain.Alert, level func(string) (string, error)) error {
	if s.Index == nil {
		return nil
	}
	if !a.AdmittedActiveMain() {
		return s.Index.ApplyDependencyCandidates(ctx, redisstate.DependencyCandidateUpdate{TenantID: a.BKTenantID, ConfigDigest: s.ConfigDigest, AlertID: a.AlertID, Revision: a.Revision, Clear: true})
	}
	if s.Loader == nil {
		return nil
	}
	if s.Loader.Catalog == nil {
		return processingerror.RetryableError{Err: ErrDependencyIndexNotReady}
	}
	policies, err := s.Loader.Catalog.Candidates(ctx, a.BKTenantID, alertCandidateReader(a, level))
	if err != nil {
		return err
	}
	u, err := s.dependencyCandidateUpdate(ctx, a, policies, time.Now().UTC(), level)
	if err != nil {
		return err
	}
	return s.Index.ApplyDependencyCandidates(ctx, u)
}

// ObserveTerminalDependencyAlerts 批量清除已持久化终态的候选，复用现有 revision 排序脚本。
func (s *Shielder) ObserveTerminalDependencyAlerts(ctx context.Context, alerts []domain.Alert) error {
	if s.Index == nil {
		return nil
	}
	updates := make([]redisstate.DependencyCandidateUpdate, len(alerts))
	for i, a := range alerts {
		updates[i] = redisstate.DependencyCandidateUpdate{TenantID: a.BKTenantID, ConfigDigest: s.ConfigDigest, AlertID: a.AlertID, Revision: a.Revision, Clear: true}
	}
	if batch, ok := s.Index.(interface {
		ApplyDependencyCandidateBatch(context.Context, []redisstate.DependencyCandidateUpdate) error
	}); ok {
		return batch.ApplyDependencyCandidateBatch(ctx, updates)
	}
	for _, u := range updates {
		if err := s.Index.ApplyDependencyCandidates(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// RefreshDependencyIndex 仅后台对尚未就绪的规则回填，活动告警每租户只分页一次。
// 先实时重读再按 revision 写索引；回填期间的较旧状态不能覆盖生命周期的新状态。
func (s *Shielder) RefreshDependencyIndex(ctx context.Context, tenant string, at time.Time, level func(string) (string, error)) error {
	return s.refreshDependencyIndex(ctx, tenant, at, level, false)
}

// RebuildDependencyIndex 供目标或关系快照实际改变后调用；无变化的周期检查不重扫活动告警。
func (s *Shielder) RebuildDependencyIndex(ctx context.Context, tenant string, at time.Time, level func(string) (string, error)) error {
	return s.refreshDependencyIndex(ctx, tenant, at, level, true)
}

func (s *Shielder) refreshDependencyIndex(ctx context.Context, tenant string, at time.Time, level func(string) (string, error), rebuild bool) error {
	if s.Index == nil || s.Loader == nil || s.Loader.Catalog == nil || s.Candidates == nil || (s.CurrentAlert == nil && s.CurrentAlerts == nil) {
		return policy.ErrUnavailable
	}
	all, err := s.Loader.Catalog.Freeze(ctx, tenant)
	if err != nil {
		return err
	}
	return s.RefreshDependencyIndexSnapshot(ctx, tenant, at, level, all, rebuild)
}

// RefreshDependencyIndexSnapshot 使用后台已冻结的策略集合，避免准备窗口中途混入刚发布的版本。
func (s *Shielder) RefreshDependencyIndexSnapshot(ctx context.Context, tenant string, at time.Time, level func(string) (string, error), all []policy.FrozenPolicy, rebuild bool) error {
	if s.Index == nil || s.Loader == nil || s.Candidates == nil || (s.CurrentAlert == nil && s.CurrentAlerts == nil) {
		return policy.ErrUnavailable
	}
	var pending []policy.FrozenPolicy
	for _, f := range all {
		if !dependencyPolicy(f) {
			continue
		}
		_, ready, err := s.Index.TopDependencyCandidate(ctx, tenant, s.dependencyIndexRef(f))
		if err != nil {
			return err
		}
		if !ready || rebuild {
			pending = append(pending, f)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	after := ""
	for {
		page, err := s.Candidates.ListActiveAlerts(ctx, tenant, after, 32)
		if err != nil {
			return err
		}
		// 每页一次实时批读；列表只定位候选，资格仍由当前 Alert 业务状态判定。
		var currentBatch map[string]store.StoredAlert
		if s.CurrentAlerts != nil && len(page.Alerts) > 0 {
			ids := make([]string, 0, len(page.Alerts))
			requested := make(map[string]bool, len(page.Alerts))
			for _, row := range page.Alerts {
				if row.Alert.BKTenantID != tenant || row.Alert.AlertID <= after {
					return policy.ErrAccess
				}
				ids = append(ids, row.Alert.AlertID)
				requested[row.Alert.AlertID] = true
			}
			batch, err := s.CurrentAlerts(ctx, tenant, ids)
			if err != nil {
				return err
			}
			currentBatch = make(map[string]store.StoredAlert, len(batch.Alerts))
			for _, row := range batch.Alerts {
				if row.Alert.BKTenantID != tenant || !requested[row.Alert.AlertID] {
					return policy.ErrAccess
				}
				currentBatch[row.Alert.AlertID] = row
			}
		}
		updates := make([]redisstate.DependencyCandidateUpdate, 0, len(page.Alerts))
		for _, row := range page.Alerts {
			if row.Alert.BKTenantID != tenant || row.Alert.AlertID <= after {
				return policy.ErrAccess
			}
			var current store.StoredAlert
			if currentBatch != nil {
				var found bool
				current, found = currentBatch[row.Alert.AlertID]
				if !found {
					return store.ErrNotFound
				}
			} else {
				var err error
				current, err = s.CurrentAlert(ctx, tenant, row.Alert.AlertID)
				if err != nil {
					return err
				}
			}
			if current.Alert.BKTenantID != tenant || current.Alert.AlertID != row.Alert.AlertID {
				return policy.ErrAccess
			}
			// 回填可能领先于实时观察的新 Alert revision；必须同时补齐所有当前规则，不能清旧成员后只写新规则。
			u, err := s.dependencyCandidateUpdate(ctx, current.Alert, all, at, level)
			if err != nil {
				return err
			}
			updates = append(updates, u)
		}
		if batch, ok := s.Index.(interface {
			ApplyDependencyCandidateBatch(context.Context, []redisstate.DependencyCandidateUpdate) error
		}); ok {
			if err := batch.ApplyDependencyCandidateBatch(ctx, updates); err != nil {
				return err
			}
		} else {
			for _, u := range updates {
				if err := s.Index.ApplyDependencyCandidates(ctx, u); err != nil {
					return err
				}
			}
		}
		if page.Next == "" {
			break
		}
		if page.Next <= after {
			return policy.ErrUnavailable
		}
		after = page.Next
	}
	for _, f := range pending {
		if err := s.Index.MarkDependencyIndexReady(ctx, tenant, s.dependencyIndexRef(f)); err != nil {
			return err
		}
	}
	return nil
}
