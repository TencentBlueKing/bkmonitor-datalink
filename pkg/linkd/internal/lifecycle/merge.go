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
	"linkd/internal/store"
)

// MergeEvaluation 区分告警等待摘要与本次 Event 的逐策略诊断。
type MergeEvaluation struct {
	Merge    *domain.AlertMerge
	Decision *store.MergeDecision
}

// MergeEvaluator 只对抑制/屏蔽后候选入窗；Confirm 在真实 Alert CAS 之后运行。
// 独立控制面裁决不在该接口中执行，主 Lifecycle 不等待窗口结束。
type MergeEvaluator interface {
	Check(context.Context, domain.Event, domain.EventEvaluation, *store.PolicyContext, domain.Alert, func(string) (string, error)) (MergeEvaluation, error)
	Confirm(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error
}

// WithMergeEvaluator 注入合并入窗策略；应与独立裁决/释放运行时一同装配。
func WithMergeEvaluator(merger MergeEvaluator) ProcessorOption {
	return func(p *Processor) { p.merger = merger }
}

func (p *Processor) planMerge(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, snapshot *store.PolicyContext, plan *store.EventPlan, alert *domain.Alert) error {
	if p.merger == nil {
		return nil
	}
	var level func(string) (string, error)
	if mapper, ok := p.severity.(interface{ KACLevel(string) (string, error) }); ok {
		level = mapper.KACLevel
	}
	result, err := p.merger.Check(ctx, event, evaluation, snapshot, *alert, level)
	if err != nil {
		return err
	}
	if result.Decision == nil {
		return fmt.Errorf("missing merge decision")
	}
	if err := result.Decision.Validate(); err != nil {
		return err
	}
	alert.Merge = result.Merge.Clone()
	if plan.PolicyDecision == nil {
		plan.PolicyDecision = &store.PolicyDecision{}
	}
	plan.PolicyDecision.Merge = result.Decision
	if result.Decision != nil {
		for _, step := range result.Decision.Steps {
			if step.Outcome != "joined" {
				continue
			}
			log, err := eventAlertLog(event, *alert, domain.OperationKindMergeWait, "merge_waiting", alert.UpdateAt, map[string]string{"window_id": step.Wait.WindowID, "policy_id": step.Policy.ID})
			if err != nil {
				return err
			}
			log.LogID = digestStrings("merge-wait-log", event.BKTenantID, event.EventID, alert.AlertID, step.Wait.WindowID)
			plan.Logs = append(plan.Logs, log)
		}
	}
	return nil
}
