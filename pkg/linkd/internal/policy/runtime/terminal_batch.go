// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
)

// ClearDependencies 批量清理终态主及复查提示；普通缓存故障与单项路径一样只记录降级。
func (s *Shielder) ClearDependencies(ctx context.Context, alerts []domain.Alert) []error {
	results := make([]error, len(alerts))
	if s.Dependency == nil {
		return results
	}
	batch, ok := s.Dependency.(interface {
		ClearDependencyOwnersBatch(context.Context, []domain.Alert) []error
	})
	if !ok {
		for i, a := range alerts {
			results[i] = s.ClearDependency(ctx, a.BKTenantID, a.AlertID)
		}
		return results
	}
	errs := batch.ClearDependencyOwnersBatch(ctx, alerts)
	for i, a := range alerts {
		if errs[i] != nil {
			s.observe(ctx, a.BKTenantID, a.AlertID, "", store.ShieldStep{Outcome: "skipped", ReasonCode: "main_cleanup_failed"}, errs[i])
		}
		results[i] = ctx.Err()
	}
	if hints, ok := s.Hints.(interface {
		PublishShieldHintsBatch(context.Context, []domain.Alert) ([]int64, []error)
	}); ok {
		counts, errs := hints.PublishShieldHintsBatch(ctx, alerts)
		for i, a := range alerts {
			outcome := "published"
			if errs[i] != nil {
				outcome = "publish_failed"
			} else if counts[i] == 0 {
				outcome = "no_subscriber"
			}
			if observer, ok := s.Observer.(interface{ ObserveShieldHint(context.Context, string) }); ok {
				observer.ObserveShieldHint(ctx, outcome)
			}
			if errs[i] != nil && ctx.Err() == nil && s.Logger != nil {
				s.Logger.WarnContext(ctx, "dependency hint publish failed", "module", "policy", "operation", "dependency_cleanup", "error", errs[i], "bk_tenant_id", a.BKTenantID, "alert_id", a.AlertID)
			}
		}
	}
	return results
}

// ClearBatch 保存批量清理意图后一次执行 Redis 清理，再批量保存实际诊断。
func (s *Suppressor) ClearBatch(ctx context.Context, causes []suppressioncleanup.Cause) []error {
	if s.BatchState == nil {
		results := make([]error, len(causes))
		for i, c := range causes {
			results[i] = s.Clear(ctx, c)
		}
		return results
	}
	run := func(call context.Context, selected []suppressioncleanup.Cause) ([]suppressioncleanup.Result, []error) {
		results, errs := s.BatchState.ClearSuppressionBatch(call, selected)
		for i, c := range selected {
			if errs[i] != nil {
				if s.Logger != nil {
					s.Logger.WarnContext(call, "suppression cleanup skipped", "module", "policy", "operation", "suppression_cleanup", "error", errs[i], "bk_tenant_id", c.TenantID, "event_source_id", c.SourceID, "alert_id", c.AlertID, "reason", "redis_unavailable")
				}
				if s.Observer != nil {
					s.Observer.ObserveSuppression(call, "cleanup", "skipped", "redis_unavailable")
				}
			}
			errs[i] = call.Err()
		}
		return results, errs
	}
	if journal, ok := s.CleanupRecorder.(interface {
		RunBatch(context.Context, []suppressioncleanup.Cause, func(context.Context, []suppressioncleanup.Cause) ([]suppressioncleanup.Result, []error)) []error
	}); ok {
		return journal.RunBatch(ctx, causes, run)
	}
	if s.CleanupRecorder != nil {
		results := make([]error, len(causes))
		for i, c := range causes {
			results[i] = s.Clear(ctx, c)
		}
		return results
	}
	_, errs := run(ctx, causes)
	return errs
}
