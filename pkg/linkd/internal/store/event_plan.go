// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"fmt"
	"slices"

	"linkd/internal/domain"
)

// EvaluationResult 是单个级别的最终裁决；关联列表包含该裁决影响或抑制它的告警。
type EvaluationResult struct {
	Severity        string                   `json:"severity"`
	Action          domain.EventAction       `json:"action"`
	State           domain.EventProcessState `json:"state"`
	Outcome         string                   `json:"outcome"`
	ReasonCode      string                   `json:"reason_code,omitempty"`
	RelatedAlertIDs []string                 `json:"related_alert_ids,omitempty"`
}

// AlertMutation 是已冻结的单对象变更，Version 只由原 Repository 解释。
// 创建没有 ExpectedVersion；更新保留原版本，不能在重试时覆盖新的并发快照。
type AlertMutation struct {
	// ActionReady 仅对该次触发放行；状态同步不受它限制，恢复重试仍使用原判定。
	ActionReady     bool         `json:"action_ready"`
	ExpectedVersion string       `json:"expected_version,omitempty"`
	Alert           domain.Alert `json:"alert"`
	Outcome         string       `json:"outcome"`
}

// EventPlan 是当前调用内的变更集合，最多结束一个旧 Alert 并创建一个新 Alert。
// 不保存到 Event；中断后允许按最新真实状态重新裁决。
type EventPlan struct {
	PolicyDecision *PolicyDecision `json:"policy_decision,omitempty"`
	// SystemClose 保存事件裁决前的未知等级清理，独立于事件业务关联，重试使用同一时间。
	SystemClose *domain.Alert `json:"system_close,omitempty"`
	// ConfigDigest 仅用于诊断，不要求加载历史配置。
	ConfigDigest    string                   `json:"config_digest,omitempty"`
	UpgradePolicy   string                   `json:"upgrade_policy"`
	Mutations       []AlertMutation          `json:"mutations"`
	Logs            []domain.AlertLog        `json:"logs"`
	Evaluations     []EvaluationResult       `json:"evaluations"`
	State           domain.EventProcessState `json:"state"`
	Outcome         string                   `json:"outcome"`
	ReasonCode      string                   `json:"reason_code,omitempty"`
	RelatedAlertIDs []string                 `json:"related_alert_ids,omitempty"`
}

func cloneEvaluationResults(results []EvaluationResult) []EvaluationResult {
	results = slices.Clone(results)
	for i := range results {
		results[i].RelatedAlertIDs = slices.Clone(results[i].RelatedAlertIDs)
	}
	return results
}

// Clone 返回独立计划快照，包括告警、流水及各级别关联列表。
func (p *EventPlan) Clone() *EventPlan {
	if p == nil {
		return nil
	}
	result := *p
	result.PolicyDecision = p.PolicyDecision.Clone()
	if p.SystemClose != nil {
		a := p.SystemClose.Clone()
		result.SystemClose = &a
	}
	result.Mutations = slices.Clone(p.Mutations)
	for i := range result.Mutations {
		result.Mutations[i].Alert = result.Mutations[i].Alert.Clone()
	}
	result.Logs = slices.Clone(p.Logs)
	for i := range result.Logs {
		result.Logs[i] = result.Logs[i].Clone()
	}
	result.Evaluations = cloneEvaluationResults(p.Evaluations)
	result.RelatedAlertIDs = slices.Clone(p.RelatedAlertIDs)
	return &result
}

// Normalize 统一计划内快照的空集合和 UTC 时间，不重解析其业务 JSON，保证跨存储往返后仍可判断副作用已完成。
func (p *EventPlan) Normalize() (*EventPlan, error) {
	if p == nil {
		return nil, nil
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	result := p.Clone()
	if result.SystemClose != nil {
		a, err := result.SystemClose.NormalizeState()
		if err != nil {
			return nil, err
		}
		result.SystemClose = &a
	}
	for i := range result.Mutations {
		alert, err := result.Mutations[i].Alert.NormalizeState()
		if err != nil {
			return nil, err
		}
		result.Mutations[i].Alert = alert
	}
	for i := range result.Logs {
		result.Logs[i].CreatedTime = result.Logs[i].CreatedTime.Round(0).UTC()
		if result.Logs[i].Params == nil {
			result.Logs[i].Params = domain.JSONObject{}
		}
	}
	return result, nil
}

// Validate 校验计划预算与目标状态；动态业务数据由产生它的处理器或目标写入接口负责。
func (p *EventPlan) Validate() error {
	if err := p.PolicyDecision.Validate(); err != nil {
		return err
	}
	if p.SystemClose != nil {
		if err := p.SystemClose.ValidateState(); err != nil {
			return err
		}
		if p.SystemClose.Status != domain.AlertStatusClosed || p.SystemClose.EndType != domain.AlertEndTypeSystem || p.SystemClose.EndReason != "unknown_severity" {
			return fmt.Errorf("invalid system severity closure")
		}
	}
	if p.UpgradePolicy != "update_current" && p.UpgradePolicy != "close_and_create" {
		return fmt.Errorf("invalid plan upgrade policy")
	}
	if len(p.Mutations) > 2 || len(p.Logs) > domain.MaxEventEvaluations+6 {
		return fmt.Errorf("event plan exceeds operation limit")
	}
	if len(p.Evaluations) == 0 || len(p.Evaluations) > domain.MaxEventEvaluations {
		return fmt.Errorf("event plan requires bounded evaluations")
	}
	if p.State == domain.EventProcessStateUnprocessed || !p.State.Valid() || p.Outcome == "" || len(p.RelatedAlertIDs) > domain.MaxEventEvaluations {
		return fmt.Errorf("invalid plan result")
	}
	resultSeverities := map[string]bool{}
	for _, result := range p.Evaluations {
		if resultSeverities[result.Severity] {
			return fmt.Errorf("duplicate plan evaluation")
		}
		resultSeverities[result.Severity] = true
		associated := result.State == domain.EventProcessStateAccepted || result.State == domain.EventProcessStateSuppressed
		if (result.State == domain.EventProcessStateAccepted && len(result.RelatedAlertIDs) == 0) || (!associated && len(result.RelatedAlertIDs) > 0) || len(result.RelatedAlertIDs) > 2 {
			return fmt.Errorf("invalid evaluation association")
		}
		for _, id := range result.RelatedAlertIDs {
			if !slices.Contains(p.RelatedAlertIDs, id) {
				return fmt.Errorf("evaluation association missing from plan")
			}
		}
	}
	seen := map[string]bool{}
	for _, mutation := range p.Mutations {
		if err := mutation.Alert.ValidateState(); err != nil {
			return fmt.Errorf("plan alert: %w", err)
		}
		if mutation.Outcome == "" || seen[mutation.Alert.AlertID] {
			return fmt.Errorf("invalid plan mutation")
		}
		seen[mutation.Alert.AlertID] = true
	}
	for _, log := range p.Logs {
		if err := log.ValidateMetadata(); err != nil {
			return fmt.Errorf("plan log: %w", err)
		}
	}
	for _, result := range p.Evaluations {
		if result.Severity == "" || !result.Action.Valid() || result.State == domain.EventProcessStateUnprocessed || !result.State.Valid() || result.Outcome == "" {
			return fmt.Errorf("invalid evaluation result")
		}
	}
	return nil
}

// ApplyEventResult 将一次计划或终态 CAS 投影为新快照，保持来源事实不变。
// 最终结果不依赖持久化策略快照或执行计划。
func ApplyEventResult(current StoredEvent, result EventResult) (domain.Event, EventProcessing, error) {
	return applyEventResult(current, result, false)
}

// ApplyEventResultFromSnapshot 共享可信快照的不可变 Event 载荷，处理结果仍拥有独立状态。
func ApplyEventResultFromSnapshot(current StoredEvent, result EventResult) (domain.Event, EventProcessing, error) {
	return applyEventResult(current, result, true)
}

func applyEventResult(current StoredEvent, result EventResult, snapshot bool) (domain.Event, EventProcessing, error) {
	if current.Processing.State != domain.EventProcessStateUnprocessed {
		return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: event already processed", ErrInvalidTransition)
	}
	result, err := result.Normalize()
	if err != nil {
		return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if err := ValidatePolicyDecision(current.Event, nil, result.PolicyDecision); err != nil {
		return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	var updated domain.Event
	if snapshot {
		updated, err = current.Event.WithRelatedAlertIDsFromSnapshot(result.RelatedAlertIDs)
	} else {
		updated, err = current.Event.WithRelatedAlertIDs(result.RelatedAlertIDs)
	}
	if err != nil {
		return domain.Event{}, EventProcessing{}, err
	}
	processing := result.Processing()
	return updated, processing, nil
}

// ValidateEventPlan 校验本次内存变更属于当前租户、来源、fingerprint 和事件。
func ValidateEventPlan(event domain.Event, plan *EventPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if a := plan.SystemClose; a != nil {
		if a.BKTenantID != event.BKTenantID || a.EventSourceID != event.EventSourceID || a.Fingerprint != event.Fingerprint {
			return fmt.Errorf("system closure scope mismatch")
		}
	}
	if len(plan.Evaluations) != len(event.Evaluations) {
		return fmt.Errorf("%w: plan evaluations mismatch", ErrInvalidArgument)
	}
	for _, result := range plan.Evaluations {
		found := false
		for _, evaluation := range event.Evaluations {
			if evaluation.Severity == result.Severity && evaluation.Action == result.Action {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: plan evaluation mismatch", ErrInvalidArgument)
		}
	}
	for _, mutation := range plan.Mutations {
		alert := mutation.Alert
		if alert.BKTenantID != event.BKTenantID || alert.EventSourceID != event.EventSourceID || alert.Fingerprint != event.Fingerprint || alert.LatestEventID != event.EventID {
			return fmt.Errorf("%w: plan alert identity mismatch", ErrInvalidArgument)
		}
	}
	for _, log := range plan.Logs {
		if log.BKTenantID != event.BKTenantID || !slices.Contains(plan.RelatedAlertIDs, log.AlertID) {
			return fmt.Errorf("%w: plan log tenant mismatch", ErrInvalidArgument)
		}
	}
	return nil
}
