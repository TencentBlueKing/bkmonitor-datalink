// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

// ShieldChecker 为定时解除复用生命周期租约和状态输出，最多四个并发检查，不创建输入 Event。
type ShieldChecker struct {
	slots   chan struct{}
	run     func(context.Context, string, string, int64) (lifecycle.ShieldCheckReport, error)
	journal *shieldcheck.Journal
	logger  *slog.Logger
}

// NewShieldChecker 将真实复查和独立诊断存储装配到同一并发预算。
func NewShieldChecker(cfg config.Config, sources closeSourceReader, policies *policy.Service, resources *policyruntime.Runtime, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime, journal *shieldcheck.Journal, observations ...*policyruntime.Observations) *ShieldChecker {
	loader := &policyruntime.Suppressor{Releases: policies, Catalog: policy.NewCatalog(policies), Targets: resources.Targets, Logger: logger}
	if len(observations) > 0 {
		loader.Observations = observations[0]
	}
	return &ShieldChecker{slots: make(chan struct{}, 4), journal: journal, logger: logger, run: func(ctx context.Context, tenant, id string, expected int64) (lifecycle.ShieldCheckReport, error) {
		report := lifecycle.ShieldCheckReport{Outcome: "failed"}
		err := withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, tenant, id, func(repo store.Repository) []lifecycle.ProcessorOption {
			return []lifecycle.ProcessorOption{lifecycle.WithShieldEvaluator(&policyruntime.Shielder{Loader: loader, Events: repo.GetEvent, CurrentAlert: func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				if current, ok := repo.(store.LifecycleAlertStore); ok {
					return current.GetAlertCurrent(ctx, tenant, id)
				}
				return repo.GetAlert(ctx, tenant, id)
			}, Relations: resources.Relations, Logger: logger, Observer: metrics})}
		}, func(p *lifecycle.Processor) error {
			var err error
			report, err = p.RecheckShield(ctx, tenant, id, expected)
			return err
		})
		return report, err
	}}
}

// CheckShield 使用记录内明确租户再实时读取；租约忙或依赖错误交由后续轮次重试。
func (s *ShieldChecker) CheckShield(ctx context.Context, tenant, id string) error {
	_, err := s.check(ctx, tenant, id, 0, "")
	return err
}

// CheckHint 与定时检查共用租约、四个名额和实际裁决，只单独标记诊断触发来源。
func (s *ShieldChecker) CheckHint(ctx context.Context, tenant, id string) error {
	result, err := s.checkFrom(ctx, tenant, id, 0, "", "hint")
	if err != nil && ctx.Err() == nil && s.logger != nil {
		s.logger.WarnContext(ctx, "shield hinted check incomplete", "bk_tenant_id", tenant, "alert_id", id,
			"error_code", shieldCheckCode(err), "check_error_code", result.ErrorCode, "outcome", result.Report.Outcome,
			"changed", result.Report.Changed, "observed_revision", result.Report.ObservedRevision, "result_revision", result.Report.ResultRevision)
	}
	return err
}

// CheckRequest 使用命令中的原业务版本复查，失效请求不切换到新状态；结果单独保存供审计。
func (s *ShieldChecker) CheckRequest(ctx context.Context, request shieldcheck.Request) (shieldcheck.Check, error) {
	if request.Validate() != nil || request.State != "pending" {
		return shieldcheck.Check{}, policy.ErrInvalid
	}
	c := request.Command
	return s.check(ctx, c.TenantID, c.AlertID, c.ExpectedRevision, request.ID)
}

func (s *ShieldChecker) check(ctx context.Context, tenant, id string, expected int64, requestID string) (shieldcheck.Check, error) {
	trigger := "timer"
	if requestID != "" {
		trigger = "request"
	}
	return s.checkFrom(ctx, tenant, id, expected, requestID, trigger)
}

func (s *ShieldChecker) checkFrom(ctx context.Context, tenant, id string, expected int64, requestID, trigger string) (shieldcheck.Check, error) {
	started := time.Now().UTC()
	result := shieldcheck.Check{TenantID: tenant, AlertID: id, Trigger: trigger, RequestID: requestID, StartedAt: started}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	report, err := s.run(call, tenant, id, expected)
	cancel()
	result.Report = report
	result.FinishedAt = time.Now().UTC()
	if result.FinishedAt.Before(started) {
		result.FinishedAt = started
	}
	if err != nil {
		result.ErrorCode = shieldCheckCode(err)
		result.Report.Outcome = "failed"
		if errors.Is(err, lifecycle.ErrShieldCheckStale) {
			result.Report.Outcome = "superseded"
		}
	}
	// 锁忙没有检查事实；保留上次真实诊断，显式请求继续排队而不是伪装成一次失败检查。
	if errors.Is(err, scheduler.ErrLockBusy) || ctx.Err() != nil {
		return result, err
	}
	if s.journal != nil {
		save, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		err = errors.Join(err, s.journal.SaveLatest(save, result))
	}
	return result, err
}

func shieldCheckCode(err error) string {
	switch {
	case errors.Is(err, lifecycle.ErrShieldCheckStale):
		return "revision_changed"
	case errors.Is(err, policy.ErrAccess):
		return "scope_mismatch"
	case errors.Is(err, store.ErrInvalidArgument), errors.Is(err, store.ErrInvalidTransition), errors.Is(err, policy.ErrInvalid):
		return "invalid_state"
	case errors.Is(err, store.ErrNotFound), errors.Is(err, eventsource.ErrNotFound), errors.Is(err, policy.ErrNotFound):
		return "record_missing"
	case errors.Is(err, scheduler.ErrLockBusy):
		return "alert_busy"
	case errors.Is(err, context.DeadlineExceeded):
		return "check_timeout"
	case errors.Is(err, context.Canceled):
		return "check_cancelled"
	case errors.Is(err, store.ErrVersionConflict), errors.Is(err, policy.ErrConflict):
		return "version_conflict"
	}
	var classified *CloseError
	if errors.As(err, &classified) {
		if classified.Status == 403 {
			return "scope_mismatch"
		}
		return "configuration_unavailable"
	}
	return "dependency_failed"
}
