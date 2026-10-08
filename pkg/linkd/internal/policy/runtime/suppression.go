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
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
)

// ReleaseReader 必须读取指定租户和精确版本，不能在已冻结 Event 上回退最新配置。
type ReleaseReader interface {
	GetRelease(context.Context, policy.Scope, string, int64) (policy.Release, error)
}

// SuppressionState 提供受限计数与 owner 清理，便于真实 Redis 和故障注入复用契约。
type SuppressionState interface {
	Clip(context.Context, redisstate.ClipRequest) (redisstate.ClipDecision, error)
	BindClipOwner(context.Context, redisstate.ClipRequest, redisstate.ClipDecision, string) error
	ClearClipIdentity(context.Context, redisstate.Identity, string) ([]suppressioncleanup.Window, error)
}

// SuppressionObserver 只接收固定分类，不允许事件、策略 ID 和完整错误进入指标标签。
type SuppressionObserver interface {
	ObserveSuppression(context.Context, string, string, string)
}

type policyLogger interface {
	WarnContext(context.Context, string, ...any)
}

// Suppressor 执行新告警候选的抑制规则；活动 Alert 的绕过边界由 Lifecycle 控制。
// 所有依赖有超时；普通策略故障记录跳过，身份/上下文错误不能降级。
type Suppressor struct {
	// Observations 聚合匹配耗时及逐策略尽力采样；模拟和只读预览不注入。
	Observations *Observations
	// CleanupRecorder 在正式运行中保存独立终态清理记录；测试或只读匹配用例可不注入。
	CleanupRecorder suppressioncleanup.Recorder
	Aggregation     AggregationState
	// CurrentAlert 必须实时读取指定租户的主 Alert，不使用具有 refresh 延迟的状态搜索。
	CurrentAlert func(context.Context, string, string) (store.StoredAlert, error)
	// NewAlertID 必须与 Lifecycle 使用同一个确定性生成器。
	NewAlertID func(domain.Event) (string, error)
	Releases   ReleaseReader
	Catalog    *policy.Catalog
	Targets    policy.TargetReader
	State      SuppressionState
	Observer   SuppressionObserver
	Logger     policyLogger
}

// Check 使用已持久化的裁决时间和 Release 列表；返回的结果必须先随计划保存再创建 Alert。
func (s *Suppressor) Check(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, snapshot *store.PolicyContext, level func(string) (string, error)) (store.SuppressionEvaluation, error) {
	result := store.SuppressionEvaluation{Severity: evaluation.Severity}
	if snapshot == nil || evaluation.Action != domain.EventActionTriggered {
		return result, fmt.Errorf("suppression requires frozen context and trigger")
	}
	if err := snapshot.Validate(); err != nil {
		return result, err
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, ref := range snapshot.Releases {
		if ref.Kind != "suppression" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		step := store.SuppressionStep{Policy: ref, Scheme: "match"}
		appendStep := func(outcome, reason string) {
			step.Outcome = outcome
			step.ReasonCode = reason
			result.Steps = append(result.Steps, step)
			s.observe(ctx, event, step)
		}
		frozen, err := s.load(call, event.BKTenantID, ref)
		if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
			return result, err
		}
		if err != nil {
			appendStep("skipped", "release_unavailable")
			continue
		}
		view, err := policy.EventView(event, evaluation.Severity, frozen.Compiled.Common.FieldMappings, level, policy.RelationContext{})
		if err != nil {
			appendStep("skipped", "event_view_unavailable")
			continue
		}
		verdict, err := s.evaluate(call, frozen.Release, frozen.Compiled, view, s.Targets, snapshot.EvaluatedAt, false, false)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, policy.ErrUnavailable) {
				appendStep("skipped", "evaluation_unavailable")
				continue
			}
			return result, err
		}
		if !verdict.Evaluated {
			appendStep("skipped", verdict.Reason)
			continue
		}
		if !verdict.Matched {
			appendStep("not_matched", verdict.Reason)
			continue
		}
		for _, scheme := range frozen.Compiled.Summary.Schemes {
			step = store.SuppressionStep{Policy: ref, Scheme: scheme.Type}
			if scheme.Type == "aggregation" {
				group, reason, err := policy.EvaluateGrouping(call, frozen.Compiled, view)
				if err != nil {
					appendStep("skipped", reason)
					break
				}
				step, err = s.aggregation(call, event, ref, snapshot.EvaluatedAt, scheme.Seconds, group)
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				if err != nil {
					if errors.Is(err, policy.ErrAccess) || errors.Is(err, policy.ErrInvalid) {
						return result, err
					}
					appendStep("skipped", "evaluation_unavailable")
					break
				}
				appendStep(step.Outcome, step.ReasonCode)
				if step.Outcome == "suppressed" {
					result.Suppressed = true
					result.ReasonCode = "aggregation_suppressed"
					result.RelatedAlertID = step.Window.OwnerAlertID
					s.releaseCandidates(call, event, &result)
					return result, nil
				}
				continue
			}
			if s.State == nil {
				appendStep("skipped", "redis_unavailable")
				break
			}
			request := clipRequest(event, ref, snapshot.EvaluatedAt, scheme.Seconds, scheme.Count)
			decision, err := s.State.Clip(call, request)
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if err != nil {
				appendStep("skipped", "redis_unavailable")
				break
			}
			step.Count = decision.Count
			step.Threshold = decision.Threshold
			step.DurationSeconds = scheme.Seconds
			step.CounterID = decision.CounterID
			step.Epoch = decision.Epoch
			step.EvaluatedAtMillis = decision.EvaluatedAtMillis
			if !decision.Allowed {
				appendStep("suppressed", "clip_below_threshold")
				result.Suppressed = true
				result.ReasonCode = "clip_below_threshold"
				s.releaseCandidates(call, event, &result)
				return result, nil
			}
			appendStep("passed", "")
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Suppressor) load(ctx context.Context, tenant string, ref store.PolicyReleaseRef) (policy.FrozenPolicy, error) {
	scope := policy.Scope{TenantID: tenant, Kind: policy.Kind(ref.Kind)}
	if s.Catalog != nil {
		if cached, ok := s.Catalog.FindFrozen(scope, ref.ID, ref.Version, ref.Digest); ok {
			return cached, nil
		}
	}
	if s.Releases == nil {
		return policy.FrozenPolicy{}, policy.ErrUnavailable
	}
	release, err := s.Releases.GetRelease(ctx, scope, ref.ID, ref.Version)
	if err != nil {
		return policy.FrozenPolicy{}, err
	}
	if release.Scope != scope || release.ID != ref.ID || release.Version != ref.Version {
		return policy.FrozenPolicy{}, policy.ErrAccess
	}
	compiled, err := policy.Compile(release.Kind, release.Spec)
	if err != nil {
		return policy.FrozenPolicy{}, fmt.Errorf("%w: invalid frozen release", policy.ErrInvalid)
	}
	if compiled.Summary.Digest != ref.Digest || release.Compiled.Digest != ref.Digest || release.Compiled.CompilerVersion != compiled.Summary.CompilerVersion || release.Deleted {
		return policy.FrozenPolicy{}, fmt.Errorf("%w: frozen release mismatch", policy.ErrInvalid)
	}
	return policy.FrozenPolicy{Release: release, Compiled: compiled}, nil
}

func clipRequest(event domain.Event, ref store.PolicyReleaseRef, at time.Time, seconds int64, threshold int) redisstate.ClipRequest {
	return redisstate.ClipRequest{Identity: redisstate.Identity{TenantID: event.BKTenantID, SourceID: event.EventSourceID, Fingerprint: event.Fingerprint}, PolicyID: ref.ID, Version: ref.Version, Digest: ref.Digest, EventID: event.EventID, At: at, Duration: time.Duration(seconds) * time.Second, Threshold: threshold}
}

// Bind 在 Alert 创建成功后绑定所有为选中等级放行的计数，旧窗口重试不能夺取新 owner。
func (s *Suppressor) Bind(ctx context.Context, event domain.Event, decision *store.PolicyDecision, alert domain.Alert) error {
	if decision == nil || decision.Suppression == nil {
		return nil
	}
	if alert.BKTenantID != event.BKTenantID || alert.EventSourceID != event.EventSourceID || alert.Fingerprint != event.Fingerprint {
		return policy.ErrAccess
	}
	if err := decision.Validate(); err != nil {
		return err
	}
	for _, evaluation := range decision.Suppression.Evaluations {
		if evaluation.Suppressed || evaluation.Severity != alert.Severity {
			continue
		}
		for _, step := range evaluation.Steps {
			if step.Scheme == "aggregation" && step.Outcome == "reserved" {
				if !alert.AdmittedActiveMain() {
					if s.Aggregation != nil && step.Window != nil {
						if _, err := s.Aggregation.ReleaseAggregation(ctx, event.BKTenantID, windowDecision(step.Window, "candidate")); err != nil {
							s.observe(ctx, event, store.SuppressionStep{Policy: step.Policy, Scheme: "aggregation", Outcome: "skipped", ReasonCode: "candidate_release_failed"})
						}
					}
					if ctx.Err() != nil {
						return ctx.Err()
					}
					continue
				}
				if err := s.bindAggregation(ctx, event, step, alert); err != nil {
					return err
				}
				continue
			}
			if step.Scheme != "clip" || step.Outcome != "passed" {
				continue
			}
			request := clipRequest(event, step.Policy, time.UnixMilli(step.EvaluatedAtMillis), step.DurationSeconds, step.Threshold)
			saved := redisstate.ClipDecision{Allowed: true, Count: step.Count, Threshold: step.Threshold, EvaluatedAtMillis: step.EvaluatedAtMillis, CounterID: step.CounterID, Epoch: step.Epoch}
			err := policy.ErrUnavailable
			if s.State != nil {
				err = s.State.BindClipOwner(ctx, request, saved, alert.AlertID)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				step.Outcome = "skipped"
				step.ReasonCode = "owner_bind_failed"
				s.observe(ctx, event, step)
			}
		}
	}
	return nil
}

// Clear 在同 fingerprint lease 内按终态原因清理缓存，先保存独立诊断意图。
// Redis 不可用记录 unavailable，仍允许终态推进；持久诊断失败和取消返回给上游重试。
func (s *Suppressor) Clear(ctx context.Context, cause suppressioncleanup.Cause) error {
	if cause.Validate() != nil {
		return policy.ErrInvalid
	}
	run := func(call context.Context) (suppressioncleanup.Result, error) {
		result := suppressioncleanup.Result{Clip: suppressioncleanup.Outcome{State: "unavailable"}, Aggregation: suppressioncleanup.Outcome{State: "not_applicable"}}
		var cleanupErr error
		if s.State != nil {
			n, err := s.State.ClearClipIdentity(call, redisstate.Identity{TenantID: cause.TenantID, SourceID: cause.SourceID, Fingerprint: cause.Fingerprint}, cause.AlertID)
			if err == nil {
				result.Clip = suppressioncleanup.Confirmed(n)
			} else {
				cleanupErr = err
			}
		} else {
			cleanupErr = policy.ErrUnavailable
		}
		if cause.AlertID != "" {
			result.Aggregation = suppressioncleanup.Outcome{State: "unavailable"}
			if s.Aggregation != nil {
				n, err := s.Aggregation.ClearAggregationOwner(call, cause.TenantID, cause.AlertID)
				if err == nil {
					result.Aggregation = suppressioncleanup.Confirmed(n)
				} else {
					cleanupErr = errors.Join(cleanupErr, err)
				}
			} else {
				cleanupErr = errors.Join(cleanupErr, policy.ErrUnavailable)
			}
		}
		if call.Err() != nil {
			return result, call.Err()
		}
		if cleanupErr != nil {
			if s.Logger != nil {
				s.Logger.WarnContext(call, "suppression cleanup skipped", "bk_tenant_id", cause.TenantID, "event_source_id", cause.SourceID, "alert_id", cause.AlertID, "reason", "redis_unavailable")
			}
			if s.Observer != nil {
				s.Observer.ObserveSuppression(call, "cleanup", "skipped", "redis_unavailable")
			}
		}
		return result, nil
	}
	if s.CleanupRecorder != nil {
		return s.CleanupRecorder.Run(ctx, cause, run)
	}
	_, err := run(ctx)
	return err
}

func (s *Suppressor) observe(ctx context.Context, event domain.Event, step store.SuppressionStep) {
	if step.Outcome == "skipped" {
		s.fault(ctx, event.BKTenantID, "suppression", step.Policy.ID)
	}
	if s.Observer != nil {
		s.Observer.ObserveSuppression(ctx, step.Scheme, step.Outcome, step.ReasonCode)
	}
	if step.Outcome == "skipped" && s.Logger != nil {
		s.Logger.WarnContext(ctx, "suppression policy skipped", "bk_tenant_id", event.BKTenantID, "event_source_id", event.EventSourceID, "event_id", event.EventID, "policy_id", step.Policy.ID, "policy_version", step.Policy.Version, "scheme", step.Scheme, "reason", step.ReasonCode)
	}
}
