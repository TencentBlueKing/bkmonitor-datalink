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

type NoopFinalHook struct{}

func (NoopFinalHook) Execute(ctx context.Context, _ FinalHookInput) (FinalHookResult, error) {
	if err := ctx.Err(); err != nil {
		return FinalHookResult{}, err
	}
	return FinalHookResult{Skipped: true}, nil
}

func (p *Processor) runFinalHook(
	ctx context.Context,
	cause AlertChangeCause,
	alert domain.Alert,
	outcome ProcessOutcome,
) (*domain.AlertLog, error) {
	result, hookFailure := p.callFinalHook(ctx, FinalHookInput{Cause: cause, Alert: alert.Clone(), Outcome: outcome})
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
		result = FinalHookResult{Name: "invalid", Transport: "unknown", Destination: "unknown", MessageID: hookInvocationID(cause, alert, outcome)}
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

func (p *Processor) callFinalHook(ctx context.Context, input FinalHookInput) (result FinalHookResult, failureReason string) {
	defer func() {
		if recover() != nil {
			result = FinalHookResult{}
			failureReason = "hook_panic"
		}
	}()
	var err error
	result, err = p.finalHook.Execute(ctx, input)
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
