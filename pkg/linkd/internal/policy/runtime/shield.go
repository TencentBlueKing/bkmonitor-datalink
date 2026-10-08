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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// ShieldObserver 接收低基数结果，不携带策略、租户或告警身份。
type ShieldObserver interface {
	ObserveShield(context.Context, string, string)
}

// ShieldHintPublisher 只发送尽力而为的主结束提示，不承担解除和重试队列语义。
type ShieldHintPublisher interface {
	PublishShieldHint(context.Context, string, string) (int64, error)
}

// Shielder 区分新匹配的跳过与现有关系解除失败；后者保留关系等待后续重查。
type Shielder struct {
	// Hints 为终态检查提供可丢失的加速，失败不能阻止已提交的业务终态。
	Hints        ShieldHintPublisher
	Observer     ShieldObserver
	Loader       *Suppressor
	Events       func(context.Context, string, string) (store.StoredEvent, error)
	Logger       policyLogger
	Candidates   store.ActiveAlertReader
	CurrentAlert func(context.Context, string, string) (store.StoredAlert, error)
	Dependency   DependencyState
	Relations    policy.RelationLookup
}

// Check 先维护现有绑定，再按 KAC 的策略优先顺序取首个命中；不重写 Alert 普通展示字段。
func (s *Shielder) Check(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, snapshot *store.PolicyContext, alert domain.Alert, level func(string) (string, error)) (lifecycle.ShieldEvaluation, error) {
	return s.check(ctx, event, evaluation, snapshot, alert, level, false)
}

func (s *Shielder) check(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, snapshot *store.PolicyContext, alert domain.Alert, level func(string) (string, error), timer bool) (lifecycle.ShieldEvaluation, error) {
	result := lifecycle.ShieldEvaluation{Decision: &store.ShieldDecision{Severity: evaluation.Severity}}
	if snapshot == nil || s.Loader == nil || event.BKTenantID != alert.BKTenantID || event.EventSourceID != alert.EventSourceID || event.Fingerprint != alert.Fingerprint {
		return result, policy.ErrInvalid
	}
	if err := snapshot.Validate(); err != nil {
		return result, err
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	retained, steps, err := s.retain(call, event.BKTenantID, alert, snapshot, level)
	result.Decision.Steps = append(result.Decision.Steps, steps...)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, err
	}
	if retained.Active {
		result.Shield = retained
		return result, nil
	}
	for _, ref := range snapshot.Releases {
		if ref.Kind != "shield" {
			continue
		}
		step := store.ShieldStep{Policy: ref}
		record := func(outcome, reason string) {
			step.Outcome = outcome
			step.ReasonCode = reason
			result.Decision.Steps = append(result.Decision.Steps, step)
			s.observe(ctx, event.BKTenantID, alert.AlertID, event.EventID, step)
		}
		frozen, err := s.Loader.load(call, event.BKTenantID, ref)
		if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
			return result, err
		}
		if err != nil {
			record("skipped", "release_unavailable")
			continue
		}
		if !frozen.Compiled.Active(snapshot.EvaluatedAt) {
			record("not_matched", "inactive")
			continue
		}
		if frozen.Compiled.Shield.ShieldType != "time_shield" {
			if timer {
				record("not_matched", "timer_does_not_rebind")
				continue
			}
			binding, reserved, reason, err := s.newDependencyBinding(call, event, evaluation, alert, frozen, snapshot.EvaluatedAt, level)
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
				return result, err
			}
			if err != nil {
				record("skipped", reason)
				continue
			}
			if reserved != nil {
				step.Main = reserved
				record("main_reserved", "")
				continue
			}
			if binding == nil {
				record("not_matched", reason)
				continue
			}
			tags := append(slices.Clone(alert.PolicyTags), frozen.Compiled.Shield.AlarmTags...)
			slices.Sort(tags)
			tags = slices.Compact(tags)
			if len(tags) > 256 {
				record("skipped", "policy_tag_budget")
				continue
			}
			next := snapshot.EvaluatedAt.Add(5 * time.Second)
			result.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{*binding}, NextCheckAt: &next}
			result.Tags = append(result.Tags, frozen.Compiled.Shield.AlarmTags...)
			step.BindingID = binding.BindingID
			record("bound", "dependency_matched")
			return result, result.Shield.Validate(alert.AlertID, alert.Status)
		}
		view, err := policy.EventView(event, evaluation.Severity, frozen.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		if err != nil {
			record("skipped", "event_view_unavailable")
			continue
		}
		verdict, err := s.Loader.evaluate(call, frozen.Release, frozen.Compiled, view, s.Loader.Targets, snapshot.EvaluatedAt, false, false)
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, policy.ErrUnavailable) {
				record("skipped", "evaluation_unavailable")
				continue
			}
			return result, err
		}
		if !verdict.Evaluated {
			record("skipped", verdict.Reason)
			continue
		}
		if !verdict.Matched {
			record("not_matched", verdict.Reason)
			continue
		}
		tags := append(slices.Clone(alert.PolicyTags), frozen.Compiled.Shield.AlarmTags...)
		slices.Sort(tags)
		tags = slices.Compact(tags)
		if len(tags) > 256 {
			record("skipped", "policy_tag_budget")
			continue
		}
		binding := domain.ShieldBinding{Policy: domain.PolicyVersion{ID: ref.ID, Version: ref.Version, Digest: ref.Digest}, Type: "time_shield", SourceEventID: event.EventID, Severity: evaluation.Severity, BoundAt: snapshot.EvaluatedAt, Reason: frozen.Compiled.Shield.Reason}
		activation, err := frozen.Compiled.Schedule.Occurrence(snapshot.EvaluatedAt)
		if err != nil {
			record("skipped", "activation_unavailable")
			continue
		}
		binding.ActivationID = activation
		binding.BindingID = shieldBindingID(event.BKTenantID, alert.AlertID, binding)
		next := snapshot.EvaluatedAt.Add(5 * time.Second)
		result.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{binding}, NextCheckAt: &next}
		result.Tags = append(result.Tags, frozen.Compiled.Shield.AlarmTags...)
		step.BindingID = binding.BindingID
		record("bound", "shield_matched")
		return result, result.Shield.Validate(alert.AlertID, alert.Status)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

func shieldBindingID(tenant, alertID string, b domain.ShieldBinding) string {
	raw, _ := json.Marshal([]any{"shield-binding", tenant, alertID, b.Policy, b.Type, b.Mode, b.SourceEventID, b.Severity, b.MainAlertID, b.MainCandidate, b.ActivationID})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// retain 不因普通配置编辑改变既有绑定版本；停用/删除是明确的提前解除条件。
// 目标/配置/事件读取失败无法证明可以解除，必须继续保留，不能把失败当成条件未命中。
func (s *Shielder) retain(ctx context.Context, tenant string, alert domain.Alert, snapshot *store.PolicyContext, level func(string) (string, error)) (domain.AlertShield, []store.ShieldStep, error) {
	result := alert.Shield.Clone()
	result.Bindings = nil
	steps := []store.ShieldStep{}
	for _, binding := range alert.Shield.Bindings {
		ref := store.PolicyReleaseRef{Kind: "shield", ID: binding.Policy.ID, Version: binding.Policy.Version, Digest: binding.Policy.Digest}
		step := store.ShieldStep{Policy: ref, BindingID: binding.BindingID, FromBinding: true}
		keep, reason, err := s.keepBinding(ctx, tenant, alert, binding, snapshot, level)
		if err != nil {
			return domain.AlertShield{}, nil, err
		}
		step.ReasonCode = reason
		if keep {
			result.Bindings = append(result.Bindings, binding)
			step.Outcome = "retained"
			if reason != "" {
				step.Outcome = "skipped"
			}
		} else {
			step.Outcome = "released"
		}
		steps = append(steps, step)
		s.observe(ctx, tenant, alert.AlertID, binding.SourceEventID, step)
	}
	result.Active = len(result.Bindings) > 0
	if !result.Active {
		result.NextCheckAt = nil
	}
	return result, steps, nil
}

func (s *Shielder) keepBinding(ctx context.Context, tenant string, alert domain.Alert, b domain.ShieldBinding, snapshot *store.PolicyContext, level func(string) (string, error)) (bool, string, error) {
	if snapshot.ReasonCode != "" {
		return true, "policy_load_failed", nil
	}
	var latest *store.PolicyReleaseRef
	for _, ref := range snapshot.Releases {
		if ref.Kind == "shield" && ref.ID == b.Policy.ID {
			r := ref
			latest = &r
			break
		}
	}
	if latest == nil {
		return false, "policy_disabled", nil
	}
	current, err := s.Loader.load(ctx, tenant, *latest)
	if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
		return true, "", err
	}
	if err != nil {
		return true, "release_unavailable", nil
	}
	if !current.Compiled.Common.Enabled {
		return false, "policy_disabled", nil
	}
	frozen, err := s.Loader.load(ctx, tenant, store.PolicyReleaseRef{Kind: "shield", ID: b.Policy.ID, Version: b.Policy.Version, Digest: b.Policy.Digest})
	if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
		return true, "", err
	}
	if err != nil {
		return true, "release_unavailable", nil
	}
	if !frozen.Compiled.Active(snapshot.EvaluatedAt) {
		return false, "policy_inactive", nil
	}
	if b.Type != "time_shield" {
		return s.keepDependencyBinding(ctx, tenant, alert, b, frozen, snapshot.EvaluatedAt, level)
	}
	if s.Events == nil {
		return true, "binding_event_unavailable", nil
	}
	original, err := s.Events(ctx, tenant, b.SourceEventID)
	if err != nil {
		return true, "binding_event_unavailable", nil
	}
	if original.Event.BKTenantID != tenant || original.Event.EventID != b.SourceEventID || original.Event.EventSourceID != alert.EventSourceID || original.Event.Fingerprint != alert.Fingerprint {
		return true, "", policy.ErrAccess
	}
	view, err := policy.EventView(original.Event, b.Severity, frozen.Compiled.Common.FieldMappings, level, policy.RelationContext{})
	if err != nil {
		return true, "event_view_unavailable", nil
	}
	verdict, err := s.Loader.evaluate(ctx, frozen.Release, frozen.Compiled, view, s.Loader.Targets, snapshot.EvaluatedAt, false, false)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, policy.ErrUnavailable) {
			return true, "evaluation_unavailable", nil
		}
		return true, "", err
	}
	if !verdict.Evaluated {
		return true, verdict.Reason, nil
	}
	return verdict.Matched, "", nil
}

func (s *Shielder) observe(ctx context.Context, tenant, alertID, eventID string, step store.ShieldStep) {
	if step.Outcome == "skipped" {
		s.Loader.fault(ctx, tenant, "shield", step.Policy.ID)
	}
	if s.Observer != nil {
		s.Observer.ObserveShield(ctx, step.Outcome, step.ReasonCode)
	}
	if step.Outcome == "skipped" && s.Logger != nil {
		s.Logger.WarnContext(ctx, "shield policy skipped", "bk_tenant_id", tenant, "alert_id", alertID, "event_id", eventID, "policy_id", step.Policy.ID, "policy_version", step.Policy.Version, "binding_id", step.BindingID, "reason", step.ReasonCode)
	}
}

// Recheck 为控制面复用原 Event 视图及当前配置目录，不创建 Event、占抑制窗口或设置放行记录。
func (s *Shielder) Recheck(ctx context.Context, alert domain.Alert, at time.Time, level func(string) (string, error)) (lifecycle.ShieldEvaluation, error) {
	if alert.Shield.NextCheckAt != nil && s.Loader != nil && s.Loader.Observations != nil && s.Loader.Observations.metrics != nil {
		s.Loader.Observations.metrics.ObservePolicyDelay(ctx, "shield", at.Sub(*alert.Shield.NextCheckAt))
	}

	if len(alert.Shield.Bindings) == 0 {
		return lifecycle.ShieldEvaluation{Shield: alert.Shield.Clone()}, nil
	}
	if s.Events == nil || s.Loader == nil {
		return lifecycle.ShieldEvaluation{}, policy.ErrUnavailable
	}
	binding := alert.Shield.Bindings[0]
	source, err := s.Events(ctx, alert.BKTenantID, binding.SourceEventID)
	if err != nil {
		return lifecycle.ShieldEvaluation{}, err
	}
	if source.Event.BKTenantID != alert.BKTenantID || source.Event.EventID != binding.SourceEventID {
		return lifecycle.ShieldEvaluation{}, policy.ErrAccess
	}
	snapshot, err := (Snapshotter{Catalog: s.Loader.Catalog}).Snapshot(ctx, source.Event, at)
	if err != nil {
		return lifecycle.ShieldEvaluation{}, err
	}
	if snapshot.ReasonCode != "" {
		return lifecycle.ShieldEvaluation{}, policy.ErrUnavailable
	}
	var evaluation domain.EventEvaluation
	for _, e := range source.Event.Evaluations {
		if e.Severity == binding.Severity {
			evaluation = e
			break
		}
	}
	if evaluation.Action != domain.EventActionTriggered {
		return lifecycle.ShieldEvaluation{}, policy.ErrInvalid
	}
	return s.check(ctx, source.Event, evaluation, snapshot, alert, level, true)
}
