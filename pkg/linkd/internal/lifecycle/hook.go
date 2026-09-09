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
	"fmt"

	"linkd/internal/domain"
)

const (
	HookReasonSucceeded             = "hook_succeeded"
	HookReasonFailed                = "hook_failed"
	AlertChangeCauseSourceEvent     = "source_event"
	AlertChangeCauseUserOperation   = "user_operation"
	AlertChangeCauseSystemOperation = "system_operation"
)

// AlertChangeCause 记录一次 Alert 快照变化的稳定推动身份。
type AlertChangeCause struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (c AlertChangeCause) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("alert change cause id is required")
	}
	switch c.Type {
	case AlertChangeCauseSourceEvent, AlertChangeCauseUserOperation, AlertChangeCauseSystemOperation:
		return nil
	default:
		return fmt.Errorf("alert change cause type is invalid: %q", c.Type)
	}
}

type FinalHookInput struct {
	Cause   AlertChangeCause
	Alert   domain.Alert
	Outcome ProcessOutcome
}

type FinalHookResult struct {
	Name        string
	Transport   string
	Destination string
	MessageID   string
	Skipped     bool
}

type FinalHook interface {
	Execute(ctx context.Context, input FinalHookInput) (FinalHookResult, error)
}

// NamedFinalHook 将发布中的实例身份绑定到单插件实现。
// Name 与列表顺序无关，供日志幂等身份和指标使用。
type NamedFinalHook struct {
	Name string
	Hook FinalHook
}

// Execute 固定正常及错误返回结果中的实例名；panic 的身份由调度方保留。
func (h NamedFinalHook) Execute(ctx context.Context, input FinalHookInput) (result FinalHookResult, err error) {
	defer func() { result.Name = h.Name }()
	return h.Hook.Execute(ctx, input)
}

type NoopFinalHook struct{}

func (NoopFinalHook) Execute(ctx context.Context, _ FinalHookInput) (FinalHookResult, error) {
	if err := ctx.Err(); err != nil {
		return FinalHookResult{}, err
	}
	return FinalHookResult{Skipped: true}, nil
}

// runFinalHooks 顺序执行来源插件；普通失败生成各自流水后继续，父上下文取消则停止。
// 每个实例拿到独立快照，避免插件修改动态字段影响后续插件和已持久化的 Alert。
func (p *Processor) runFinalHooks(ctx context.Context, cause AlertChangeCause, alert domain.Alert, outcome ProcessOutcome) ([]domain.AlertLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	logs := make([]domain.AlertLog, 0, len(p.finalHooks))
	for _, hook := range p.finalHooks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		log, err := p.runFinalHook(ctx, hook, cause, alert, outcome)
		if err != nil {
			return nil, err
		}
		if log != nil {
			logs = append(logs, *log)
		}
	}
	return logs, nil
}

func (p *Processor) runFinalHook(
	ctx context.Context,
	hook NamedFinalHook,
	cause AlertChangeCause,
	alert domain.Alert,
	outcome ProcessOutcome,
) (*domain.AlertLog, error) {
	result, hookFailure := p.callFinalHook(ctx, hook, FinalHookInput{Cause: cause, Alert: alert.Clone(), Outcome: outcome})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result.Skipped && hookFailure == "" {
		return nil, nil
	}
	if hookFailure == "" {
		if err := result.validate(); err != nil {
			hookFailure = "invalid_hook_result"
		}
	}
	if hookFailure != "" && result.validate() != nil {
		result = FinalHookResult{Name: hook.Name, Transport: "unknown", Destination: "unknown", MessageID: hookInvocationID(cause, alert, outcome)}
	}
	reasonCode := HookReasonSucceeded
	if hookFailure != "" {
		reasonCode = HookReasonFailed
		p.logger.WarnContext(ctx, "alert final hook failed", "bk_tenant_id", alert.BKTenantID,
			"cause_type", cause.Type, "cause_id", cause.ID, "alert_id", alert.AlertID,
			"hook_name", result.Name, "transport", result.Transport, "destination", result.Destination,
			"reason_code", hookFailure)
	}
	log, err := finalHookLog(cause, alert, outcome, result, reasonCode)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

func (p *Processor) callFinalHook(ctx context.Context, hook NamedFinalHook, input FinalHookInput) (result FinalHookResult, failureReason string) {
	defer func() {
		if recover() != nil {
			result = FinalHookResult{}
			failureReason = "hook_panic"
		}
	}()
	var err error
	result, err = hook.Execute(ctx, input)
	if err != nil {
		return result, "hook_error"
	}
	return result, ""
}

func (result FinalHookResult) validate() error {
	if result.Skipped {
		return fmt.Errorf("skipped hook result cannot be persisted")
	}
	for name, value := range map[string]string{"name": result.Name, "transport": result.Transport, "destination": result.Destination, "message_id": result.MessageID} {
		if value == "" {
			return fmt.Errorf("final hook result %s must not be empty", name)
		}
	}
	return nil
}
