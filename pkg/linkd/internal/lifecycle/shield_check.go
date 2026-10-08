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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// CheckShield 必须在该 Alert 的 fingerprint lease 内执行，只推进屏蔽状态，不触发处置或合并。
// 状态 CAS 同时保存输出意图；输出/流水之后失败仍能按原操作身份补齐。
func (p *Processor) CheckShield(ctx context.Context, tenant, id string) (bool, error) {
	report, err := p.RecheckShield(ctx, tenant, id, 0)
	return report.Changed, err
}

// ShieldCheckReport 记录控制面实际复查的阶段和证据，不写入 Alert 的业务快照或投影版本。
// Changed 只在状态 CAS 成功后为 true；后续输出失败仍须保留这一事实。
type ShieldCheckReport struct {
	ObservedRevision  int64                 `json:"observed_revision"`
	ResultRevision    int64                 `json:"result_revision"`
	CheckedAt         time.Time             `json:"checked_at"`
	Outcome           string                `json:"outcome"`
	Changed           bool                  `json:"changed"`
	RemainingBindings int                   `json:"remaining_bindings"`
	Decision          *store.ShieldDecision `json:"decision,omitempty"`
}

// ErrShieldCheckStale 表示显式请求依据的业务版本已经变化；调用方不能据此强行检查另一个状态。
var ErrShieldCheckStale = errors.New("shield check revision changed")

// RecheckShield 执行与定时任务相同的复查，并返回可持久化诊断。expectedRevision 为零时不设置版本门槛。
// 非零门槛在同一 fingerprint lease 内检查；错误可能发生在状态已保存之后，报告不能解释为事务回滚。
func (p *Processor) RecheckShield(ctx context.Context, tenant, id string, expectedRevision int64) (ShieldCheckReport, error) {
	report := ShieldCheckReport{Outcome: "failed"}
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes || expectedRevision < 0 || expectedRevision >= 1<<53 {
		return report, store.ErrInvalidArgument
	}
	if provider, ok := p.severity.(interface {
		FreezeSeverity() (SeverityTable, string)
	}); ok {
		table, digest := provider.FreezeSeverity()
		local := *p
		local.severity = table
		local.configDigest = digest
		return local.RecheckShield(ctx, tenant, id, expectedRevision)
	}
	if p.shielder == nil {
		return report, fmt.Errorf("shield checker is not configured")
	}
	for range maxCASAttempts {
		report = ShieldCheckReport{Outcome: "failed"}
		current, err := p.getAlertCurrent(ctx, tenant, id)
		if err != nil {
			return report, err
		}
		if current.Version.IsZero() || current.Alert.Validate() != nil || current.Alert.BKTenantID != tenant || current.Alert.AlertID != id {
			return report, store.ErrInvalidArgument
		}
		now, err := p.now()
		if err != nil {
			return report, err
		}
		report.ObservedRevision = current.Alert.Revision
		report.ResultRevision = current.Alert.Revision
		report.RemainingBindings = len(current.Alert.Shield.Bindings)
		report.CheckedAt = now
		if expectedRevision != 0 && current.Alert.Revision != expectedRevision {
			report.Outcome = "superseded"
			return report, ErrShieldCheckStale
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return report, err
		}
		if current.Alert.Status.Terminal() || !current.Alert.Shield.Active {
			report.Outcome = "inactive"
			return report, nil
		}
		var level func(string) (string, error)
		if mapper, ok := p.severity.(interface{ KACLevel(string) (string, error) }); ok {
			level = mapper.KACLevel
		}
		evaluated, err := p.shielder.Recheck(ctx, current.Alert, now, level)
		if err != nil {
			return report, err
		}
		if evaluated.Decision != nil {
			if err := evaluated.Decision.Validate(); err != nil {
				return report, fmt.Errorf("%w: invalid shield check decision", store.ErrInvalidArgument)
			}
			report.Decision = (&store.PolicyDecision{Shield: evaluated.Decision}).Clone().Shield
		}
		replacement := current.Alert.Clone()
		replacement.Shield = evaluated.Shield.Clone()
		replacement.PolicyTags = append(replacement.PolicyTags, evaluated.Tags...)
		slices.Sort(replacement.PolicyTags)
		replacement.PolicyTags = slices.Compact(replacement.PolicyTags)
		changed := !reflect.DeepEqual(current.Alert.Shield.Bindings, replacement.Shield.Bindings) || !slices.Equal(current.Alert.PolicyTags, replacement.PolicyTags)
		if !changed {
			next := now.Add(5 * time.Second)
			if !next.After(*current.Alert.Shield.NextCheckAt) {
				report.Outcome = shieldCheckOutcome(report.Decision, false)
				return report, nil
			}
			replacement.Shield.NextCheckAt = &next
		} else {
			replacement.UpdateAt = nextAlertUpdateTime(now, current.Alert.UpdateAt)
			beforeIDs, afterIDs := []string{}, []string{}
			for _, b := range current.Alert.Shield.Bindings {
				beforeIDs = append(beforeIDs, b.BindingID)
			}
			for _, b := range replacement.Shield.Bindings {
				afterIDs = append(afterIDs, b.BindingID)
			}
			slices.Sort(beforeIDs)
			slices.Sort(afterIDs)
			// 业务版本固定本次变更身份；投影 ACK 和安排复查不推进 revision，重试不会另造操作。
			raw, _ := json.Marshal([]any{tenant, id, current.Alert.Revision, beforeIDs, afterIDs, replacement.PolicyTags})
			replacement.PolicyChange = &domain.AlertPolicyChange{OperationID: digestStrings("shield-change", string(raw)), EffectiveAt: replacement.UpdateAt, Before: current.Alert.Shield.Clone().Bindings, After: replacement.Shield.Clone().Bindings}
		}
		updated, err := p.compareAndSetAlert(ctx, current, replacement)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return report, err
		}
		report.Changed = changed
		report.ResultRevision = updated.Alert.Revision
		report.RemainingBindings = len(updated.Alert.Shield.Bindings)
		if err := p.recentAlerts.PutCurrent(ctx, updated); err != nil {
			return report, err
		}
		if changed {
			_, err = p.finishPendingChanges(ctx, updated)
			if err == nil {
				report.Outcome = shieldCheckOutcome(report.Decision, true)
			}
			return report, err
		}
		report.Outcome = shieldCheckOutcome(report.Decision, false)
		return report, nil
	}
	return report, store.ErrVersionConflict
}

func shieldCheckOutcome(decision *store.ShieldDecision, changed bool) string {
	if decision != nil {
		for _, step := range decision.Steps {
			if step.Outcome == "skipped" {
				return "partial"
			}
		}
	}
	if changed {
		return "changed"
	}
	return "retained"
}

// finishPendingChanges 在推进新业务快照之前完成既有状态输出；清理意图不递增 UpdateAt。
// FinalHook 保持现有普通失败记日志语义，必需 KAC 投影的持久化重试由 P6 的投影任务承接。
func (p *Processor) finishPendingChanges(ctx context.Context, current store.StoredAlert) (store.StoredAlert, error) {
	var err error
	current, err = p.finishActionIntent(ctx, current)
	if err != nil {
		return store.StoredAlert{}, err
	}
	current, err = p.finishMergeChange(ctx, current)
	if err != nil {
		return store.StoredAlert{}, err
	}
	intent := current.Alert.PolicyChange
	if intent == nil {
		return current, nil
	}
	logs := []domain.AlertLog{}
	for _, item := range []struct {
		kind     domain.OperationKind
		bindings []domain.ShieldBinding
	}{{domain.OperationKindUnshield, shieldBindingDifference(intent.Before, intent.After)}, {domain.OperationKindShield, shieldBindingDifference(intent.After, intent.Before)}} {
		if len(item.bindings) == 0 {
			continue
		}
		operation, _ := json.Marshal(intent.OperationID)
		bindings, err := json.Marshal(item.bindings)
		if err != nil {
			return store.StoredAlert{}, err
		}
		before, err := json.Marshal(intent.Before)
		if err != nil {
			return store.StoredAlert{}, err
		}
		after, err := json.Marshal(intent.After)
		if err != nil {
			return store.StoredAlert{}, err
		}
		actor := domain.OperatorKindSystem
		if item.kind == domain.OperationKindShield && len(item.bindings) == 1 && item.bindings[0].Origin == "manual" {
			actor = domain.OperatorKindUser
		}
		logs = append(logs, domain.AlertLog{LogID: digestStrings("shield-change-log", intent.OperationID, string(item.kind)), BKTenantID: current.Alert.BKTenantID, AlertID: current.Alert.AlertID, OperatorKind: actor, OperationKind: item.kind, CreatedTime: intent.EffectiveAt, Params: domain.JSONObject{"operation_id": operation, "bindings": bindings, "before_bindings": before, "after_bindings": after}})
	}
	hookLogs, err := p.runHooks(ctx, AlertChangeCause{Type: AlertChangeCauseSystemOperation, ID: intent.OperationID}, current.Alert, OutcomeAlertShieldChanged, false)
	if err != nil {
		return store.StoredAlert{}, err
	}
	logs = append(logs, hookLogs...)
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return store.StoredAlert{}, err
	}
	replacement := current.Alert.Clone()
	replacement.PolicyChange = nil
	updated, err := p.compareAndSetAlert(ctx, current, replacement)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if err := p.recentAlerts.PutCurrent(ctx, updated); err != nil {
		return store.StoredAlert{}, err
	}
	return updated, nil
}

// 同一 BindingID 的事实不可变，只记录真正新增/解除的绑定，保留的关系不能伪装成重新屏蔽。
func shieldBindingDifference(before, after []domain.ShieldBinding) []domain.ShieldBinding {
	result := []domain.ShieldBinding{}
	for _, binding := range before {
		if !slices.ContainsFunc(after, func(other domain.ShieldBinding) bool { return other.BindingID == binding.BindingID }) {
			result = append(result, binding)
		}
	}
	return result
}

// terminalShieldChange 把非 EventPlan 终态清理的原绑定与业务状态一起冻结，后续元数据重试只补状态输出。
func terminalShieldChange(before domain.Alert, at time.Time, cause string) *domain.AlertPolicyChange {
	if !before.Shield.Active {
		return nil
	}
	return &domain.AlertPolicyChange{OperationID: digestStrings("terminal-shield-change", before.BKTenantID, before.AlertID, cause), EffectiveAt: at, Before: before.Shield.Clone().Bindings}
}
