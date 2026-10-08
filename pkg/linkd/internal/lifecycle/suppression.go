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
	"slices"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
)

// NewAlertSuppressor 仅接收没有活动 Alert 的触发候选。实现消费已冻结配置并返回可保存的诊断。
// 普通策略故障由实现记录 skipped；取消、身份和基础存储错误仍必须返回给 Lifecycle。
type NewAlertSuppressor interface {
	Check(context.Context, domain.Event, domain.EventEvaluation, *store.PolicyContext, func(string) (string, error)) (store.SuppressionEvaluation, error)
	// Bind 只在真实 Alert 获准处置后登记主告警；存储创建成功本身在接入屏蔽/合并后不再等价于放行。
	Bind(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error
	Clear(context.Context, suppressioncleanup.Cause) error
}

// WithNewAlertSuppressor 在正常等级裁决之前约束新告警候选，不拦截已有活动生命周期。
func WithNewAlertSuppressor(s NewAlertSuppressor) ProcessorOption {
	return func(p *Processor) { p.suppressor = s }
}

// selectNewAlert 只在关联查询没有活动 Alert 时执行，包括所有处置资格状态。
// 按严重程度依次尝试候选；较高等级被策略抑制不意味着抹掉未命中该策略的较低等级。
func (p *Processor) selectNewAlert(ctx context.Context, event domain.Event, snapshot *store.PolicyContext, plan *store.EventPlan, active domain.Alert, highest int) (int, error) {
	if p.suppressor == nil {
		return highest, nil
	}
	decision := &store.SuppressionDecision{}
	plan.PolicyDecision = &store.PolicyDecision{Suppression: decision}
	if active.AlertID != "" {
		decision.BypassReason = "active_alert"
		decision.ActiveAlertID = active.AlertID
		return highest, nil
	}
	if highest < 0 {
		decision.BypassReason = "no_trigger"
		return highest, nil
	}
	candidates := []int{}
	for i, e := range event.Evaluations {
		if e.Action == domain.EventActionTriggered {
			candidates = append(candidates, i)
		}
	}
	slices.SortFunc(candidates, func(a, b int) int {
		comparison, _ := p.compareSeverity(event.Evaluations[a].Severity, event.Evaluations[b].Severity)
		return comparison
	})
	var level func(string) (string, error)
	if mapper, ok := p.severity.(interface{ KACLevel(string) (string, error) }); ok {
		level = mapper.KACLevel
	}
	for _, i := range candidates {
		evaluation := event.Evaluations[i]
		result, err := p.suppressor.Check(ctx, event, evaluation, snapshot, level)
		if err != nil {
			return -1, err
		}
		if result.Severity != evaluation.Severity {
			return -1, fmt.Errorf("suppression returned different severity")
		}
		decision.Evaluations = append(decision.Evaluations, result)
		if err := store.ValidatePolicyDecision(event, snapshot, plan.PolicyDecision); err != nil {
			return -1, err
		}
		if !result.Suppressed {
			return i, nil
		}
		ids := []string{}
		if result.RelatedAlertID != "" {
			ids = append(ids, result.RelatedAlertID)
			plan.RelatedAlertIDs = append(plan.RelatedAlertIDs, result.RelatedAlertID)
		}
		setEvaluationResult(plan, i, domain.EventProcessStateSuppressed, string(OutcomeAlertSuppressed), result.ReasonCode, ids)
		plan.State = domain.EventProcessStateSuppressed
		plan.Outcome = string(OutcomeAlertSuppressed)
		if plan.ReasonCode == ReasonActiveAlertNotFound {
			plan.ReasonCode = result.ReasonCode
		}
	}
	return -1, nil
}

func (p *Processor) clearSuppression(ctx context.Context, alert domain.Alert) error {
	if p.dependency != nil {
		if err := p.dependency.ClearDependency(ctx, alert.BKTenantID, alert.AlertID); err != nil {
			return err
		}
	}
	if p.suppressor == nil {
		return nil
	}
	return p.suppressor.Clear(ctx, suppressioncleanup.Cause{TenantID: alert.BKTenantID, SourceID: alert.EventSourceID, Fingerprint: alert.Fingerprint, Trigger: "alert_terminal", AlertID: alert.AlertID, Revision: alert.Revision, Status: alert.Status})
}
