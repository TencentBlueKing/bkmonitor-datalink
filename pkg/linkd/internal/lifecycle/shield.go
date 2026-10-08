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
	"encoding/json"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ShieldEvaluation 是对当前待触发 Alert 的屏蔽快照与可保存诊断；Tags 只追加显式策略标签。
type ShieldEvaluation struct {
	Shield   domain.AlertShield
	Decision *store.ShieldDecision
	Tags     []int64
}

// ShieldEvaluator 在 Event Enrich 之后评估候选，现有绑定使用自己的冻结源视图检查解除。
type ShieldEvaluator interface {
	Recheck(context.Context, domain.Alert, time.Time, func(string) (string, error)) (ShieldEvaluation, error)
	Check(context.Context, domain.Event, domain.EventEvaluation, *store.PolicyContext, domain.Alert, func(string) (string, error)) (ShieldEvaluation, error)
}

// WithShieldEvaluator 注入屏蔽匹配，并接入其可选的主候选登记/清理能力。
func WithShieldEvaluator(shielder ShieldEvaluator) ProcessorOption {
	return func(p *Processor) {
		p.shielder = shielder
		if registry, ok := shielder.(DependencyRegistry); ok {
			p.dependency = registry
		}
	}
}

// planAdmission 在生命周期快照生成后决定本次是否处置，不改变首次丰富快照。
// 已活动绕过 clip/aggregation 与是否已经放行是两条独立规则，不能相互替代。
func (p *Processor) planAdmission(ctx context.Context, event domain.Event, snapshot *store.PolicyContext, plan *store.EventPlan, highest int) error {
	if highest < 0 {
		return nil
	}
	evaluation := event.Evaluations[highest]
	if plan.Evaluations[highest].State != domain.EventProcessStateAccepted {
		return nil
	}
	for i := range plan.Mutations {
		mutation := &plan.Mutations[i]
		a := &mutation.Alert
		if a.Status != domain.AlertStatusActive || a.Severity != evaluation.Severity {
			continue
		}
		duplicate := a.Admission.AdmittedAt != nil && a.Admission.Severity == a.Severity && mutation.Outcome == string(OutcomeAlertUpdated)
		if duplicate {
			setEvaluationResult(plan, highest, domain.EventProcessStateSuppressed, string(OutcomeAlertSuppressed), "duplicate_trigger", []string{a.AlertID})
			plan.State = domain.EventProcessStateSuppressed
			plan.Outcome = string(OutcomeAlertSuppressed)
			plan.ReasonCode = "duplicate_trigger"
			log, err := eventAlertLog(event, *a, domain.OperationKindSuppress, "duplicate_trigger", a.UpdateAt, nil)
			if err != nil {
				return err
			}
			plan.Logs = append(plan.Logs, log)
			continue
		}
		before := a.Shield.Clone()
		if p.shielder != nil {
			var level func(string) (string, error)
			if mapper, ok := p.severity.(interface{ KACLevel(string) (string, error) }); ok {
				level = mapper.KACLevel
			}
			result, err := p.shielder.Check(ctx, event, evaluation, snapshot, *a, level)
			if err != nil {
				return err
			}
			a.Shield = result.Shield.Clone()
			a.PolicyTags = append(slices.Clone(a.PolicyTags), result.Tags...)
			slices.Sort(a.PolicyTags)
			a.PolicyTags = slices.Compact(a.PolicyTags)
			if plan.PolicyDecision == nil {
				plan.PolicyDecision = &store.PolicyDecision{}
			}
			plan.PolicyDecision.Shield = result.Decision
		}
		if !reflect.DeepEqual(before.Bindings, a.Shield.Bindings) {
			kind, reason := domain.OperationKindShield, "shield_matched"
			if !a.Shield.Active {
				kind, reason = domain.OperationKindUnshield, "shield_released"
			}
			extra := map[string]string{}
			if len(a.Shield.Bindings) > 0 {
				extra["binding_id"] = a.Shield.Bindings[0].BindingID
				extra["policy_id"] = a.Shield.Bindings[0].Policy.ID
			} else if len(before.Bindings) > 0 {
				extra["binding_id"] = before.Bindings[0].BindingID
			}
			log, err := eventAlertLog(event, *a, kind, reason, a.UpdateAt, extra)
			if err != nil {
				return err
			}
			// 保存整个绑定变更，不能只记第一条关系，供多策略屏蔽历史逐项回查。
			beforeBindings, err := json.Marshal(before.Bindings)
			if err != nil {
				return err
			}
			afterBindings, err := json.Marshal(a.Shield.Bindings)
			if err != nil {
				return err
			}
			log.Params["before_bindings"] = beforeBindings
			log.Params["after_bindings"] = afterBindings
			plan.Logs = append(plan.Logs, log)
		}
		if !a.Shield.Active {
			if err := p.planMerge(ctx, event, evaluation, snapshot, plan, a); err != nil {
				return err
			}
		}
		if !a.Shield.Active && !a.Merge.Blocking() {
			at := a.UpdateAt
			a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: AlertChangeCauseSourceEvent, CauseID: event.EventID}
			mutation.ActionReady = true
			if mutation.Outcome == string(OutcomeAlertUpdated) {
				log, err := eventAlertLog(event, *a, domain.OperationKindAdmit, "trigger_admitted", a.UpdateAt, nil)
				if err != nil {
					return err
				}
				plan.Logs = append(plan.Logs, log)
			}
		}
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return store.ValidatePolicyDecision(event, snapshot, plan.PolicyDecision)
}

// DependencyRegistry 的登记只证明候选 Alert 已存在，与处置放行相互独立。
type DependencyRegistry interface {
	BindDependency(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error
	ClearDependency(context.Context, string, string) error
}

// WithDependencyRegistry 为直接关闭装配主候选清理，无需加载可执行的策略目录。
func WithDependencyRegistry(registry DependencyRegistry) ProcessorOption {
	return func(p *Processor) { p.dependency = registry }
}
