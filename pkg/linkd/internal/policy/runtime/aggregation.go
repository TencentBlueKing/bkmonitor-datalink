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
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
)

// AggregationState 将候选占位、放行登记和首次抑制确认分开，避免不存在/屏蔽中的候选提前成为主告警。
type AggregationState interface {
	ClaimAggregation(context.Context, redisstate.AggregationRequest) (redisstate.AggregationDecision, error)
	CommitAggregationOwner(context.Context, redisstate.AggregationRequest, redisstate.AggregationDecision) (bool, error)
	ConfirmAggregationSuppression(context.Context, redisstate.AggregationRequest, redisstate.AggregationDecision) (redisstate.AggregationDecision, bool, error)
	ReleaseAggregation(context.Context, string, redisstate.AggregationDecision) (bool, error)
	ClearAggregationOwner(context.Context, string, string) ([]suppressioncleanup.Window, error)
}

func (s *Suppressor) aggregation(ctx context.Context, event domain.Event, ref store.PolicyReleaseRef, at time.Time, seconds int64, group string) (store.SuppressionStep, error) {
	step := store.SuppressionStep{Policy: ref, Scheme: "aggregation", Outcome: "skipped"}
	skip := func(reason string) (store.SuppressionStep, error) { step.ReasonCode = reason; return step, nil }
	if s.Aggregation == nil || s.CurrentAlert == nil || s.NewAlertID == nil {
		return skip("aggregation_unavailable")
	}
	id, err := s.NewAlertID(event)
	if err != nil {
		return step, errors.Join(policy.ErrInvalid, err)
	}
	request := redisstate.AggregationRequest{Identity: redisstate.Identity{TenantID: event.BKTenantID, SourceID: event.EventSourceID, Fingerprint: event.Fingerprint}, PolicyID: ref.ID, Version: ref.Version, Digest: ref.Digest, GroupKey: group, EventID: event.EventID, CandidateAlertID: id, At: at, Duration: time.Duration(seconds) * time.Second}
	// 最多 64 次、每次等待 25ms；不复制 KAC 的十万次查询比较循环。
	for range 64 {
		if err := ctx.Err(); err != nil {
			return step, err
		}
		observed, err := s.Aggregation.ClaimAggregation(ctx, request)
		if err != nil {
			return skip("redis_unavailable")
		}
		switch observed.Role {
		case "candidate":
			step.Outcome = "reserved"
			step.Window = windowSnapshot(observed, group)
			return step, nil
		case "suppressed":
			// 首次裁决已经原子保存；重试保持其关联，即使原主告警后来已终结。
			step.Outcome = "suppressed"
			step.ReasonCode = "aggregation_suppressed"
			step.Window = windowSnapshot(observed, group)
			return step, nil
		case "owner":
			main, err := s.CurrentAlert(ctx, event.BKTenantID, observed.OwnerAlertID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return skip("owner_read_failed")
			}
			if err == nil && (main.Alert.BKTenantID != event.BKTenantID || main.Alert.AlertID != observed.OwnerAlertID || main.Alert.EventSourceID != observed.OwnerSourceID || main.Alert.Fingerprint != observed.OwnerFingerprint) {
				return step, policy.ErrAccess
			}
			// Redis 的 admitted 登记先证明已放行，再以实时单文档读验证仍然 active；不拿状态列表搜索代替。
			if errors.Is(err, store.ErrNotFound) || !main.Alert.AdmittedActiveMain() {
				if _, err := s.Aggregation.ReleaseAggregation(ctx, event.BKTenantID, observed); err != nil {
					return skip("redis_unavailable")
				}
				continue
			}
			if main.Alert.Status != domain.AlertStatusActive {
				return skip("owner_state_invalid")
			}
			confirmed, ok, err := s.Aggregation.ConfirmAggregationSuppression(ctx, request, observed)
			if err != nil {
				return skip("redis_unavailable")
			}
			if !ok {
				continue
			}
			step.Outcome = "suppressed"
			step.ReasonCode = "aggregation_suppressed"
			step.Window = windowSnapshot(confirmed, group)
			return step, nil
		case "pending":
			timer := time.NewTimer(25 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return step, ctx.Err()
			case <-timer.C:
			}
		default:
			return skip("owner_state_invalid")
		}
	}
	return skip("aggregation_busy")
}

func windowSnapshot(d redisstate.AggregationDecision, group string) *store.SuppressionWindow {
	return &store.SuppressionWindow{WindowID: d.WindowID, GroupKey: group, Epoch: d.Epoch, OwnerAlertID: d.OwnerAlertID, OwnerEventID: d.OwnerEventID, OwnerSourceID: d.OwnerSourceID, OwnerFingerprint: d.OwnerFingerprint, StartedAtMillis: d.StartedAtMillis, ExpiresAtMillis: d.ExpiresAtMillis}
}

func windowDecision(w *store.SuppressionWindow, role string) redisstate.AggregationDecision {
	return redisstate.AggregationDecision{Role: role, WindowID: w.WindowID, Epoch: w.Epoch, OwnerAlertID: w.OwnerAlertID, OwnerEventID: w.OwnerEventID, OwnerSourceID: w.OwnerSourceID, OwnerFingerprint: w.OwnerFingerprint, StartedAtMillis: w.StartedAtMillis, ExpiresAtMillis: w.ExpiresAtMillis}
}

// releaseCandidates 在较后策略抑制当前等级时撤销前面的候选占位，不让被抑制的 Event 阻塞其他来源。
func (s *Suppressor) releaseCandidates(ctx context.Context, event domain.Event, result *store.SuppressionEvaluation) {
	if s.Aggregation == nil {
		return
	}
	for i := range result.Steps {
		step := &result.Steps[i]
		if step.Scheme != "aggregation" || step.Outcome != "reserved" || step.Window == nil {
			continue
		}
		if _, err := s.Aggregation.ReleaseAggregation(ctx, event.BKTenantID, windowDecision(step.Window, "candidate")); err != nil {
			s.observe(ctx, event, store.SuppressionStep{Policy: step.Policy, Scheme: "aggregation", Outcome: "skipped", ReasonCode: "candidate_release_failed"})
		}
		// 这里只撤销本地使用资格；Redis 失败时依靠有界 pending 租期回收，不会被登记为主。
		step.Outcome = "released"
	}
}

func (s *Suppressor) bindAggregation(ctx context.Context, event domain.Event, step store.SuppressionStep, alert domain.Alert) error {
	w := step.Window
	if w == nil || w.OwnerAlertID != alert.AlertID || w.OwnerEventID != event.EventID || w.OwnerSourceID != event.EventSourceID || w.OwnerFingerprint != event.Fingerprint {
		return policy.ErrAccess
	}
	request := redisstate.AggregationRequest{Identity: redisstate.Identity{TenantID: event.BKTenantID, SourceID: event.EventSourceID, Fingerprint: event.Fingerprint}, PolicyID: step.Policy.ID, Version: step.Policy.Version, Digest: step.Policy.Digest, GroupKey: w.GroupKey, EventID: event.EventID, CandidateAlertID: alert.AlertID, At: time.UnixMilli(w.StartedAtMillis), Duration: time.Duration(w.ExpiresAtMillis-w.StartedAtMillis) * time.Millisecond}
	var ok bool
	err := policy.ErrUnavailable
	if s.Aggregation != nil {
		ok, err = s.Aggregation.CommitAggregationOwner(ctx, request, windowDecision(w, "candidate"))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || !ok {
		s.observe(ctx, event, store.SuppressionStep{Policy: step.Policy, Scheme: "aggregation", Outcome: "skipped", ReasonCode: "owner_registration_lost"})
	}
	return nil
}
