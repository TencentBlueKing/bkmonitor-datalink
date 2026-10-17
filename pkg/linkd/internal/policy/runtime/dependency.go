// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"errors"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

// DependencyState 协调跨来源待处理主；只有 Alert CAS 完成后才允许建立依赖绑定。
// 登记不代表处置放行，不能复用聚合的 admitted owner 协议。
type DependencyState interface {
	GetDependencyMain(context.Context, string, domain.PolicyVersion) (redisstate.DependencyReservation, bool, error)
	ClaimDependencyMain(context.Context, redisstate.DependencyRequest) (redisstate.DependencyReservation, error)
	CommitDependencyMain(context.Context, redisstate.DependencyRequest) (bool, error)
	ReleaseDependencyMain(context.Context, redisstate.DependencyRequest) (bool, error)
	ClearDependencyOwner(context.Context, string, string) (int, error)
}

type dependencyMain struct {
	alert     domain.Alert
	candidate *domain.DependencyMain
	view      *policy.FactView
	at        time.Time
}

// evaluationTargets 只在一次策略求值内复用完整目标/业务范围，不能跨 Event 缓存当前 CMDB 成员。
// 候选扫描是顺序执行；该对象不共享给其他生命周期 goroutine。
type evaluationTargets struct {
	reader    policy.TargetReader
	scope     *onemodel.TargetScope
	targets   *onemodel.TargetResult
	scopeAt   time.Time
	targetsAt time.Time
}

func (t *evaluationTargets) ResolvePolicyScope(ctx context.Context, tenant, space string) (onemodel.TargetScope, error) {
	if t.scope != nil {
		recordDependencyRead(ctx, t.scopeAt)
		return *t.scope, nil
	}
	if t.reader == nil {
		return onemodel.TargetScope{}, policy.ErrUnavailable
	}
	call, read := TrackDependencyReads(ctx)
	v, err := t.reader.ResolvePolicyScope(call, tenant, space)
	if err == nil {
		t.scope = &v
		t.scopeAt = read.Time()
		recordDependencyRead(ctx, t.scopeAt)
	}
	return v, err
}

func (t *evaluationTargets) Resolve(ctx context.Context, tenant, space string, d onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	if t.targets != nil {
		recordDependencyRead(ctx, t.targetsAt)
		return *t.targets, nil
	}
	if t.reader == nil {
		return onemodel.TargetResult{}, policy.ErrUnavailable
	}
	call, read := TrackDependencyReads(ctx)
	v, err := t.reader.Resolve(call, tenant, space, d)
	if err == nil {
		t.targets = &v
		t.targetsAt = read.Time()
		recordDependencyRead(ctx, read.Time())
	}
	return v, err
}

// PrepareTargets 保留底层固定目标快照，使同一次资格判断的业务范围与成员来自同一份源事实。
func (t *evaluationTargets) PrepareTargets(ctx context.Context, tenant, space string, target *onemodel.TargetDescriptor) (policy.TargetReader, error) {
	if prepared, ok := t.reader.(policy.PreparedTargetReader); ok {
		return prepared.PrepareTargets(ctx, tenant, space, target)
	}
	return t, nil
}

// ContainsTarget 保留共享依赖的 O(1) 成员公共面；普通 Reader 仍完整验证解析集合。
func (t *evaluationTargets) ContainsTarget(ctx context.Context, tenant, space string, d *onemodel.TargetDescriptor, candidate onemodel.InstanceRef) (bool, error) {
	if prepared, ok := t.reader.(policy.TargetMembershipReader); ok {
		return prepared.ContainsTarget(ctx, tenant, space, d, candidate)
	}
	result, err := t.Resolve(ctx, tenant, space, *d)
	if err != nil {
		return false, err
	}
	if result.Scope.TenantID != tenant || result.Scope.SpaceCode != space {
		return false, policy.ErrAccess
	}
	found := false
	for _, ref := range result.Instances {
		if ref.ModelID != d.ModelID || ref.EntityUID != ref.ModelID+"|"+ref.InstanceID {
			return false, policy.ErrTargetIdentity
		}
		found = found || ref == candidate
	}
	return found, nil
}

func (s *Shielder) dependencyMatch(ctx context.Context, f policy.FrozenPolicy, view *policy.FactView, targets policy.TargetReader, at time.Time, rely bool) (bool, error) {
	v, err := s.Loader.evaluate(ctx, f.Release, f.Compiled, view, targets, at, rely, false)
	if err != nil {
		return false, err
	}
	if !v.Evaluated {
		return false, policy.ErrUnavailable
	}
	return v.Matched, nil
}

// admittedMain 只读取跨 Worker 共享索引的最新合资格主，不在 Event 路径扫描活动告警。
// 子条件留在选定主之后执行；最新主不匹配子时不能退选较旧主。
func (s *Shielder) admittedMain(ctx context.Context, f policy.FrozenPolicy, targets policy.TargetReader, at time.Time, level func(string) (string, error)) (dependencyMain, bool, error) {
	if s.Index == nil || s.CurrentAlert == nil {
		return dependencyMain{}, false, processingerror.RetryableError{Err: ErrDependencyIndexNotReady}
	}
	version := dependencyVersion(f)
	indexRef := s.dependencyIndexRef(f)
	for range 64 {
		candidate, ready, err := s.Index.TopDependencyCandidate(ctx, f.Release.TenantID, indexRef)
		if err != nil {
			return dependencyMain{}, false, err
		}
		if !ready {
			return dependencyMain{}, false, processingerror.RetryableError{Err: ErrDependencyIndexNotReady}
		}
		if candidate.AlertID == "" {
			return dependencyMain{}, false, nil
		}
		current, err := s.CurrentAlert(ctx, f.Release.TenantID, candidate.AlertID)
		if errors.Is(err, store.ErrNotFound) {
			if err := s.Index.RemoveDependencyCandidate(ctx, f.Release.TenantID, indexRef, candidate); err != nil {
				return dependencyMain{}, false, err
			}
			continue
		}
		if err != nil {
			return dependencyMain{}, false, err
		}
		if current.Alert.BKTenantID != f.Release.TenantID || current.Alert.AlertID != candidate.AlertID {
			return dependencyMain{}, false, policy.ErrAccess
		}
		// CAS 已先落库而增量观察尚未完成时，补齐整个当前候选集合；不能只写本规则并清掉其它规则成员。
		if current.Alert.AdmittedActiveMain() && current.Alert.Revision > candidate.Revision {
			if err := s.ObserveDependencyAlert(ctx, current.Alert, level); err != nil {
				return dependencyMain{}, false, err
			}
			continue
		}
		update := redisstate.DependencyCandidateUpdate{TenantID: f.Release.TenantID, ConfigDigest: s.ConfigDigest, AlertID: candidate.AlertID, Revision: current.Alert.Revision, BeginAt: current.Alert.BeginAt, Clear: !current.Alert.AdmittedActiveMain()}
		if !update.Clear {
			view, err := policy.AlertView(current.Alert, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
			if err != nil {
				return dependencyMain{}, false, err
			}
			call, read := TrackDependencyReads(ctx)
			matched, err := s.dependencyMatch(call, f, view, targets, at, false)
			if err != nil {
				return dependencyMain{}, false, err
			}
			if matched {
				return dependencyMain{alert: current.Alert, view: view, at: current.Alert.BeginAt}, true, nil
			}
			observed := read.Time()
			if observed.IsZero() {
				observed = time.Now().UTC()
			}
			// 旧 Worker 快照不能撤销较新源事实已经确认的资格，也不能退选老主。
			if current.Alert.Revision == candidate.Revision && observed.Before(candidate.ObservedAt) {
				return dependencyMain{}, false, processingerror.RetryableError{Err: ErrDependencyIndexNotReady}
			}
			update.Memberships = []redisstate.DependencyMembership{{Policy: version, ObservedAt: observed}}
		}
		if err := s.Index.ApplyDependencyCandidates(ctx, update); err != nil {
			return dependencyMain{}, false, err
		}
	}
	return dependencyMain{}, false, processingerror.RetryableError{Err: ErrDependencyIndexNotReady}
}

func (s *Shielder) loadDependencyMain(ctx context.Context, tenant, id string, ref *domain.DependencyMain, f policy.FrozenPolicy, level func(string) (string, error)) (dependencyMain, error) {
	if s.CurrentAlert == nil {
		return dependencyMain{}, policy.ErrUnavailable
	}
	current, err := s.CurrentAlert(ctx, tenant, id)
	if err != nil {
		return dependencyMain{}, err
	}
	if current.Alert.BKTenantID != tenant || current.Alert.AlertID != id {
		return dependencyMain{}, policy.ErrAccess
	}
	result := dependencyMain{alert: current.Alert, at: current.Alert.BeginAt, candidate: ref}
	if current.Alert.Status != domain.AlertStatusActive {
		return result, nil
	}
	if ref == nil {
		result.view, err = policy.AlertView(current.Alert, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		return result, err
	}
	if ref.AlertID != id || current.Alert.EventSourceID != ref.EventSourceID || current.Alert.Fingerprint != ref.Fingerprint {
		return result, policy.ErrAccess
	}
	if s.Events == nil {
		return result, policy.ErrUnavailable
	}
	e, err := s.Events(ctx, tenant, ref.EventID)
	if errors.Is(err, policy.ErrAccess) {
		return result, err
	}
	if err != nil {
		return result, policy.ErrUnavailable
	}
	if e.Event.BKTenantID != tenant || e.Event.EventID != ref.EventID || e.Event.EventSourceID != ref.EventSourceID || e.Event.Fingerprint != ref.Fingerprint {
		return result, policy.ErrAccess
	}
	result.view, err = policy.EventView(e.Event, ref.Severity, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
	result.at = e.Event.OccurredAt
	return result, err
}

func (s *Shielder) selectDependencyMain(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, alert domain.Alert, f policy.FrozenPolicy, targets policy.TargetReader, at time.Time, level func(string) (string, error)) (dependencyMain, *domain.DependencyMain, bool, error) {
	main, found, err := s.admittedMain(ctx, f, targets, at, level)
	if err != nil || found {
		return main, nil, found, err
	}
	if s.Dependency == nil {
		return main, nil, false, policy.ErrUnavailable
	}
	view, err := policy.EventView(event, evaluation.Severity, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
	if err != nil {
		return main, nil, false, err
	}
	matches, err := s.dependencyMatch(ctx, f, view, targets, at, false)
	if err != nil {
		return main, nil, false, err
	}
	version := domain.PolicyVersion{ID: f.Release.ID, Version: f.Release.Version, Digest: f.Release.Compiled.Digest}
	candidate := domain.DependencyMain{AlertID: alert.AlertID, EventID: event.EventID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Severity: evaluation.Severity}
	for range 64 {
		if err := ctx.Err(); err != nil {
			return main, nil, false, err
		}
		observed, exists, err := s.Dependency.GetDependencyMain(ctx, event.BKTenantID, version)
		if err != nil {
			return main, nil, false, err
		}
		if !exists {
			if !matches {
				return main, nil, false, nil
			}
			observed, err = s.Dependency.ClaimDependencyMain(ctx, redisstate.DependencyRequest{TenantID: event.BKTenantID, Policy: version, Candidate: candidate})
			if err != nil {
				return main, nil, false, err
			}
		}
		// 相同 Event 的重放复用自身占位；已经登记的自身也不能屏蔽自身。
		if observed.Main == candidate {
			return dependencyMain{alert: alert, view: view, at: event.OccurredAt, candidate: &candidate}, &candidate, true, nil
		}
		if observed.Role == "registered" {
			main, err = s.loadDependencyMain(ctx, event.BKTenantID, observed.Main.AlertID, &observed.Main, f, level)
			if errors.Is(err, store.ErrNotFound) || (err == nil && main.alert.Status.Terminal()) {
				_, err = s.Dependency.ReleaseDependencyMain(ctx, redisstate.DependencyRequest{TenantID: event.BKTenantID, Policy: version, Candidate: observed.Main})
				if err != nil {
					return main, nil, false, err
				}
				continue
			}
			if err != nil {
				return main, nil, false, err
			}
			// 待处理主的匹配视图固定为登记 Event，升级不会偷偷替换其关系起点。
			matched, err := s.dependencyMatch(ctx, f, main.view, targets, at, false)
			if err != nil {
				return main, nil, false, err
			}
			if !matched {
				return main, nil, false, policy.ErrUnavailable
			}
			return main, nil, true, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return main, nil, false, ctx.Err()
		case <-timer.C:
		}
	}
	return main, nil, false, policy.ErrUnavailable
}

func (s *Shielder) dependencyChild(ctx context.Context, event domain.Event, severity string, main dependencyMain, f policy.FrozenPolicy, targets policy.TargetReader, at time.Time, level func(string) (string, error)) (bool, error) {
	if event.OccurredAt.Before(main.at.Add(-time.Duration(f.Compiled.Shield.Before)*time.Minute)) || event.OccurredAt.After(main.at.Add(time.Duration(f.Compiled.Shield.After)*time.Minute)) {
		return false, nil
	}
	relation := policy.RelationContext{}
	if f.Compiled.Shield.ShieldMode == "cmdb_shield" {
		origin, found, err := main.view.CanonicalInstance(ctx)
		if err != nil {
			return false, err
		}
		if !found || s.Relations == nil {
			return false, policy.ErrUnavailable
		}
		relation = policy.RelationContext{Origin: origin, Lookup: s.Relations}
	}
	view, err := policy.EventView(event, severity, f.Compiled.Common.FieldMappings, level, relation)
	if err != nil {
		return false, err
	}
	// 初次绑定和固定关系复查都从本次选定的主视图取值，不能将替换结果写回缓存 Release。
	view, err = view.WithOrigin(main.view)
	if err != nil {
		return false, err
	}
	return s.dependencyMatch(ctx, f, view, targets, at, true)
}

// BindDependency 在真实 Alert CAS 后提交冻结占位；普通 Redis 故障只记录诊断，缓存丢失允许重新累计。
func (s *Shielder) BindDependency(ctx context.Context, event domain.Event, decision *store.PolicyDecision, alert domain.Alert) error {
	if decision == nil || decision.Shield == nil || s.Dependency == nil || alert.Status != domain.AlertStatusActive {
		return nil
	}
	for _, step := range decision.Shield.Steps {
		if step.Outcome != "main_reserved" || step.Main == nil || step.Main.AlertID != alert.AlertID {
			continue
		}
		m := step.Main
		if m.EventID != event.EventID || m.EventSourceID != event.EventSourceID || m.Fingerprint != event.Fingerprint || event.BKTenantID != alert.BKTenantID {
			return policy.ErrAccess
		}
		ok, err := s.Dependency.CommitDependencyMain(ctx, redisstate.DependencyRequest{TenantID: event.BKTenantID, Policy: domain.PolicyVersion{ID: step.Policy.ID, Version: step.Policy.Version, Digest: step.Policy.Digest}, Candidate: *m})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !ok {
			step.Outcome = "skipped"
			step.ReasonCode = "main_registration_lost"
			s.observe(ctx, event.BKTenantID, alert.AlertID, event.EventID, step, err)
		}
	}
	return nil
}

// ClearDependency 清理终态主登记并提示复查；已有关系仍按真实主状态解除，提示失败由定时检查兜底。
func (s *Shielder) ClearDependency(ctx context.Context, tenant, id string) error {
	if s.Dependency == nil {
		return nil
	}
	_, err := s.Dependency.ClearDependencyOwner(ctx, tenant, id)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		s.observe(ctx, tenant, id, "", store.ShieldStep{Outcome: "skipped", ReasonCode: "main_cleanup_failed"}, err)
	}
	if s.Hints != nil {
		count, hintErr := s.Hints.PublishShieldHint(ctx, tenant, id)
		outcome := "published"
		if hintErr != nil {
			outcome = "publish_failed"
		} else if count == 0 {
			outcome = "no_subscriber"
		}
		if observer, ok := s.Observer.(interface{ ObserveShieldHint(context.Context, string) }); ok {
			observer.ObserveShieldHint(ctx, outcome)
		}
		if hintErr != nil && ctx.Err() == nil && s.Logger != nil {
			s.Logger.WarnContext(ctx, "shield terminal hint failed", "module", "policy", "operation", "shield_hint_publish", "error", hintErr, "bk_tenant_id", tenant, "main_alert_id", id, "reason_code", "hint_publish_failed")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (s *Shielder) newDependencyBinding(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, alert domain.Alert, f policy.FrozenPolicy, at time.Time, level func(string) (string, error)) (*domain.ShieldBinding, *domain.DependencyMain, string, error) {
	// 1. 无关事件先退出，避免每个 Event 都遍历租户活动告警。
	view, err := policy.EventView(event, evaluation.Severity, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
	if err != nil {
		return nil, nil, "dependency_child_unavailable", err
	}
	mainPossible, mainErr := f.Compiled.Conditions[0].PossibleMatch(ctx, view)
	if mainErr != nil && !errors.Is(mainErr, policy.ErrUnavailable) {
		return nil, nil, "dependency_main_unavailable", mainErr
	}
	childPossible, childErr := f.Compiled.Rely.PossibleMatch(ctx, view)
	if childErr != nil && !errors.Is(childErr, policy.ErrUnavailable) {
		return nil, nil, "dependency_child_unavailable", childErr
	}
	if !mainPossible && !childPossible {
		if mainErr != nil || childErr != nil {
			return nil, nil, "dependency_child_unavailable", policy.ErrUnavailable
		}
		return nil, nil, "dependency_not_matched", nil
	}
	// 2. 可能匹配的事件才完整选主，并保留现有登记和绑定规则。
	targets := &evaluationTargets{reader: s.Loader.Targets}
	main, reserved, found, err := s.selectDependencyMain(ctx, event, evaluation, alert, f, targets, at, level)
	if err != nil {
		return nil, nil, "dependency_main_unavailable", err
	}
	if reserved != nil {
		return nil, reserved, "", nil
	}
	if !found {
		return nil, nil, "dependency_main_missing", nil
	}
	// close_and_create 的旧 Alert 在整份计划提交前仍可被查询到；同一关联键的旧生命周期
	// 不能成为新生命周期的依赖主，否则合法升级会等待旧主终结后的定时解除。
	if main.alert.AlertID == alert.AlertID || (main.alert.EventSourceID == alert.EventSourceID && main.alert.Fingerprint == alert.Fingerprint) {
		return nil, nil, "dependency_self", nil
	}
	matched, err := s.dependencyChild(ctx, event, evaluation.Severity, main, f, targets, at, level)
	if err != nil {
		return nil, nil, "dependency_child_unavailable", err
	}
	if !matched {
		return nil, nil, "dependency_not_matched", nil
	}

	b := domain.ShieldBinding{Policy: domain.PolicyVersion{ID: f.Release.ID, Version: f.Release.Version, Digest: f.Release.Compiled.Digest}, Type: "rely_shield", Mode: f.Compiled.Shield.ShieldMode, SourceEventID: event.EventID, Severity: evaluation.Severity, BoundAt: at, Reason: f.Compiled.Shield.Reason, MainAlertID: main.alert.AlertID, MainCandidate: main.candidate}
	b.BindingID = shieldBindingID(event.BKTenantID, alert.AlertID, b)
	for _, existing := range alert.Shield.Bindings {
		if existing.BindingID == b.BindingID {
			b = existing
			break
		}
	}
	return &b, nil, "", nil
}

func (s *Shielder) keepDependencyBinding(ctx context.Context, tenant string, alert domain.Alert, b domain.ShieldBinding, f policy.FrozenPolicy, at time.Time, level func(string) (string, error)) (bool, string, error) {
	main, err := s.loadDependencyMain(ctx, tenant, b.MainAlertID, b.MainCandidate, f, level)
	if errors.Is(err, policy.ErrAccess) {
		return true, "", err
	}
	if errors.Is(err, store.ErrNotFound) || (err == nil && main.alert.Status.Terminal()) {
		return false, "dependency_main_ended", nil
	}
	if err != nil {
		return true, "dependency_main_unavailable", nil
	}
	if s.Events == nil {
		return true, "binding_event_unavailable", nil
	}
	original, err := s.Events(ctx, tenant, b.SourceEventID)
	if errors.Is(err, policy.ErrAccess) {
		return true, "", err
	}
	if err != nil {
		return true, "binding_event_unavailable", nil
	}
	e := original.Event
	if e.BKTenantID != tenant || e.EventID != b.SourceEventID || e.EventSourceID != alert.EventSourceID || e.Fingerprint != alert.Fingerprint {
		return true, "", policy.ErrAccess
	}
	matched, err := s.dependencyChild(ctx, e, b.Severity, main, f, &evaluationTargets{reader: s.Loader.Targets}, at, level)
	if errors.Is(err, policy.ErrAccess) {
		return true, "", err
	}
	if err != nil {
		return true, "dependency_child_unavailable", nil
	}
	if !matched {
		return false, "dependency_no_longer_matches", nil
	}
	return true, "", nil
}
