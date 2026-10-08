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
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

// MergeState 将候选登记与真实成员确认分开，窗口冻结由控制面独立执行。
type MergeState interface {
	JoinMergeWindow(context.Context, redisstate.MergeRequest) (domain.MergeWait, error)
	CommitMergeMember(context.Context, string, string, domain.MergeWait) (bool, error)
	ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error)
}

// MergeObserver 接收低基数结果，不包含租户、策略或窗口身份标签。
type MergeObserver interface {
	ObserveMerge(context.Context, string, string)
}

// Merger 只处理通过前置策略的触发候选；既有窗口引用和截止时间不会被新 Event 刷新。
type Merger struct {
	Loader   *Suppressor
	State    MergeState
	Logger   policyLogger
	Observer MergeObserver
}

// Check 保存本次匹配的全部策略窗口，最多 32 个并行等待；先前已合并和内置主跳过再次入窗。
func (m *Merger) Check(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, snapshot *store.PolicyContext, alert domain.Alert, level func(string) (string, error)) (lifecycle.MergeEvaluation, error) {
	result := lifecycle.MergeEvaluation{Merge: alert.Merge.Clone(), Decision: &store.MergeDecision{Severity: evaluation.Severity}}
	if m.Loader == nil || snapshot == nil || event.BKTenantID != alert.BKTenantID || event.EventSourceID != alert.EventSourceID || event.Fingerprint != alert.Fingerprint {
		return result, policy.ErrInvalid
	}
	if err := snapshot.Validate(); err != nil {
		return result, err
	}
	if alert.Merge != nil {
		if alert.Merge.Role == "aggregate" {
			result.Decision.BypassReason = "aggregate_alert"
			return result, nil
		}
		if len(alert.Merge.RelationIDs) > 0 {
			result.Decision.BypassReason = "merged_member"
			return result, nil
		}
	}
	pending := map[string]bool{}
	if alert.Merge != nil {
		for _, wait := range alert.Merge.Pending {
			w := wait.Clone()
			pending[w.Policy.ID] = true
			result.Decision.Steps = append(result.Decision.Steps, store.MergeStep{Policy: store.PolicyReleaseRef{Kind: "merge", ID: w.Policy.ID, Version: w.Policy.Version, Digest: w.Policy.Digest}, Outcome: "retained", Wait: &w, FromWaiting: true})
		}
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, ref := range snapshot.Releases {
		if ref.Kind != "merge" || pending[ref.ID] {
			continue
		}
		step := store.MergeStep{Policy: ref}
		record := func(outcome, reason string) {
			step.Outcome = outcome
			step.ReasonCode = reason
			result.Decision.Steps = append(result.Decision.Steps, step)
			m.observe(ctx, event, step)
		}
		f, err := m.Loader.load(call, event.BKTenantID, ref)
		if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
			return result, err
		}
		if err != nil {
			record("skipped", "release_unavailable")
			continue
		}
		if !f.Compiled.Active(snapshot.EvaluatedAt) {
			record("not_matched", "inactive")
			continue
		}
		if result.Merge != nil && len(result.Merge.Pending) >= 32 {
			record("skipped", "merge_wait_budget")
			continue
		}
		view, err := policy.EventView(event, evaluation.Severity, f.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		if err != nil {
			record("skipped", "event_view_unavailable")
			continue
		}
		verdict, err := m.Loader.evaluate(call, f.Release, f.Compiled, view, m.Loader.Targets, snapshot.EvaluatedAt, false, true)
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, policy.ErrAccess) {
			return result, err
		}
		if err != nil {
			record("skipped", "evaluation_unavailable")
			continue
		}
		if !verdict.Evaluated {
			record("skipped", verdict.Reason)
			continue
		}
		if !verdict.Matched {
			record("not_matched", verdict.Reason)
			continue
		}
		if m.State == nil {
			record("skipped", "merge_state_unavailable")
			continue
		}
		groups := []int{}
		for _, g := range verdict.Groups {
			if g.Matched {
				groups = append(groups, g.Index)
			}
		}
		wait, err := m.State.JoinMergeWindow(call, redisstate.MergeRequest{TenantID: event.BKTenantID, Policy: domain.PolicyVersion{ID: ref.ID, Version: ref.Version, Digest: ref.Digest}, GroupKey: verdict.GroupKey, Member: domain.DependencyMain{AlertID: alert.AlertID, EventID: event.EventID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Severity: evaluation.Severity}, Groups: groups, GroupCount: len(f.Compiled.Conditions), At: snapshot.EvaluatedAt, Duration: time.Duration(f.Compiled.Merge.Cycle) * time.Second, Cyclic: f.Compiled.Merge.Cyclic})
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err != nil {
			record("skipped", "merge_join_unavailable")
			continue
		}
		window, found, err := m.State.ReadMergeWindow(call, event.BKTenantID, wait.WindowID)
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err != nil || !found || window.Policy != wait.Policy || window.GroupKey != wait.GroupKey {
			record("skipped", "merge_window_unavailable")
			continue
		}
		// 冻结发生在候选 CAS 之前时不把已裁决窗口重新挂回告警。CAS 之后的竞态由原截止时间及控制面补偿。
		if window.Frozen != nil {
			record("skipped", "merge_window_decided")
			continue
		}
		if result.Merge == nil {
			result.Merge = &domain.AlertMerge{Role: "original", State: "pending"}
		}
		result.Merge.Pending = append(result.Merge.Pending, wait.Clone())
		result.Merge.State = "pending"
		step.Wait = &wait
		record("joined", "merge_waiting")
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, result.Merge.Validate(alert.Status)
}

// Confirm 不重新入窗或延长窗口；缓存丢失只记录，持久化引用仍按原截止时间结束等待。
func (m *Merger) Confirm(ctx context.Context, event domain.Event, decision *store.PolicyDecision, alert domain.Alert) error {
	if decision == nil || decision.Merge == nil || alert.Status.Terminal() {
		return nil
	}
	if alert.BKTenantID != event.BKTenantID || alert.EventSourceID != event.EventSourceID || alert.Fingerprint != event.Fingerprint {
		return policy.ErrAccess
	}
	for _, step := range decision.Merge.Steps {
		if step.Outcome != "joined" || step.Wait == nil {
			continue
		}
		ok := false
		err := policy.ErrUnavailable
		if m.State != nil {
			ok, err = m.State.CommitMergeMember(ctx, alert.BKTenantID, alert.AlertID, *step.Wait)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !ok {
			step.Outcome = "skipped"
			step.ReasonCode = "merge_confirmation_lost"
			m.observe(ctx, event, step)
		}
	}
	return nil
}

func (m *Merger) observe(ctx context.Context, event domain.Event, step store.MergeStep) {
	if step.Outcome == "skipped" {
		m.Loader.fault(ctx, event.BKTenantID, "merge", step.Policy.ID)
	}
	if m.Observer != nil {
		m.Observer.ObserveMerge(ctx, step.Outcome, step.ReasonCode)
	}
	if step.Outcome == "skipped" && m.Logger != nil {
		m.Logger.WarnContext(ctx, "merge policy skipped", "bk_tenant_id", event.BKTenantID, "event_id", event.EventID, "policy_id", step.Policy.ID, "policy_version", step.Policy.Version, "reason", step.ReasonCode)
	}
}
