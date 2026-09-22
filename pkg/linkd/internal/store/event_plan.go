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
	"reflect"
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
	ExpectedVersion string       `json:"expected_version,omitempty"`
	Alert           domain.Alert `json:"alert"`
	Outcome         string       `json:"outcome"`
}

// EventPlan 保存副作用执行前的裁决快照。最多结束一个旧 Alert 并创建一个新 Alert。
// 来源事实、排序配置或全局升级策略变化，都不能改变已经提交的计划。
type EventPlan struct {
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

// Normalize 统一计划内快照的 JSON 空对象和 UTC 时间，保证跨存储往返后仍可判断副作用已完成。
func (p *EventPlan) Normalize() (*EventPlan, error) {
	if p == nil {
		return nil, nil
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	result := p.Clone()
	if result.SystemClose != nil {
		a, err := result.SystemClose.Normalize()
		if err != nil {
			return nil, err
		}
		result.SystemClose = &a
	}
	for i := range result.Mutations {
		alert, err := result.Mutations[i].Alert.Normalize()
		if err != nil {
			return nil, err
		}
		result.Mutations[i].Alert = alert
	}
	for i := range result.Logs {
		log, err := result.Logs[i].Normalize()
		if err != nil {
			return nil, err
		}
		result.Logs[i] = log
	}
	return result, nil
}

// Validate 校验计划大小和所有持久化目标，实际租户和事件边界由 ApplyEventResult 校验。
func (p *EventPlan) Validate() error {
	if p.SystemClose != nil {
		if err := p.SystemClose.Validate(); err != nil {
			return err
		}
		if p.SystemClose.Status != domain.AlertStatusClosed || p.SystemClose.EndType != domain.AlertEndTypeSystem || p.SystemClose.EndReason != "unknown_severity" {
			return fmt.Errorf("invalid system severity closure")
		}
	}
	if p.UpgradePolicy != "update_current" && p.UpgradePolicy != "close_and_create" {
		return fmt.Errorf("invalid plan upgrade policy")
	}
	if len(p.Mutations) > 2 || len(p.Logs) > domain.MaxEventEvaluations+2 {
		return fmt.Errorf("event plan exceeds operation limit")
	}
	if len(p.Evaluations) == 0 || len(p.Evaluations) > domain.MaxEventEvaluations {
		return fmt.Errorf("event plan requires bounded evaluations")
	}
	if p.State == domain.EventProcessStateUnprocessed || !p.State.Valid() || p.Outcome == "" || len(p.RelatedAlertIDs) > 2 {
		return fmt.Errorf("invalid plan result")
	}
	resultSeverities := map[string]bool{}
	for _, result := range p.Evaluations {
		if resultSeverities[result.Severity] {
			return fmt.Errorf("duplicate plan evaluation")
		}
		resultSeverities[result.Severity] = true
		associated := result.State == domain.EventProcessStateAccepted || result.State == domain.EventProcessStateSuppressed
		if associated != (len(result.RelatedAlertIDs) > 0) || len(result.RelatedAlertIDs) > 2 {
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
		if err := mutation.Alert.Validate(); err != nil {
			return fmt.Errorf("plan alert: %w", err)
		}
		if mutation.Outcome == "" || seen[mutation.Alert.AlertID] {
			return fmt.Errorf("invalid plan mutation")
		}
		seen[mutation.Alert.AlertID] = true
	}
	for _, log := range p.Logs {
		if err := log.Validate(); err != nil {
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
// 计划一旦存在只能原样重试、在未产生副作用时由 Lifecycle 撤销，或提交它的完整最终结果。
func ApplyEventResult(current StoredEvent, result EventResult) (domain.Event, EventProcessing, error) {
	if current.Processing.State != domain.EventProcessStateUnprocessed {
		return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: event already processed", ErrInvalidTransition)
	}
	result, err := result.Normalize()
	if err != nil {
		return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if result.Plan != nil {
		if current.Processing.Plan != nil && !reflect.DeepEqual(current.Processing.Plan, result.Plan) {
			return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: event plan is immutable", ErrInvalidTransition)
		}
		if err := ValidateEventPlan(current.Event, result.Plan); err != nil {
			return domain.Event{}, EventProcessing{}, err
		}
	}
	if current.Processing.Plan != nil && result.State != domain.EventProcessStateUnprocessed {
		plan := current.Processing.Plan
		if result.State != plan.State || result.Outcome != plan.Outcome || result.ReasonCode != plan.ReasonCode || !slices.Equal(result.RelatedAlertIDs, plan.RelatedAlertIDs) || !reflect.DeepEqual(result.Evaluations, plan.Evaluations) {
			return domain.Event{}, EventProcessing{}, fmt.Errorf("%w: final result differs from saved plan", ErrInvalidTransition)
		}
	}
	updated, err := current.Event.WithRelatedAlertIDs(result.RelatedAlertIDs)
	if err != nil {
		return domain.Event{}, EventProcessing{}, err
	}
	processing := result.Processing()
	return updated, processing, nil
}

// ValidateEventPlan 校验持久化计划属于当前租户、来源、fingerprint 和事件；读取后执行前也必须调用。
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
