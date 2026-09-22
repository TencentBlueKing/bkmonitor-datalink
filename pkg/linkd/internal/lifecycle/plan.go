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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ProcessEvent 在 fingerprint lease 内先保存确定性计划，再执行有界副作用并提交最终结果。
// 同一事件的所有级别共享该计划；外部 hook 仍遵从至少一次调用边界。
func (p *Processor) ProcessEvent(ctx context.Context, initial store.StoredEvent) (ProcessResult, error) {
	// Processor 被多个 fingerprint 并发使用，复制接收者而不是原地改写共享字段。
	if provider, ok := p.severity.(interface {
		FreezeSeverity() (SeverityTable, string)
	}); ok {
		table, digest := provider.FreezeSeverity()
		local := *p
		local.severity = table
		local.configDigest = digest
		return local.ProcessEvent(ctx, initial)
	}
	if ctx == nil {
		return ProcessResult{}, fmt.Errorf("process lifecycle event: context must not be nil")
	}
	if initial.Event.BKTenantID == "" || initial.Event.EventID == "" || initial.Version.IsZero() {
		return ProcessResult{}, fmt.Errorf("process lifecycle event requires identity and version")
	}
	stored := initial
	processing, err := stored.Processing.Normalize()
	if err != nil {
		return ProcessResult{}, err
	}
	stored.Processing = processing
	var lastErr error
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ProcessResult{}, err
		}
		if attempt > 0 {
			var err error
			stored, err = p.getLifecycleEvent(ctx, initial.Event.BKTenantID, initial.Event.EventID)
			if err != nil {
				return ProcessResult{}, err
			}
		}
		if stored.Processing.State != domain.EventProcessStateUnprocessed {
			return processResult(stored), nil
		}
		if stored.Processing.Plan == nil {
			plan, err := p.preparePlan(ctx, stored.Event)
			if err != nil {
				return ProcessResult{}, err
			}
			stored, err = p.writeEventResult(ctx, stored, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
			if err != nil {
				if errors.Is(err, store.ErrVersionConflict) {
					lastErr = err
					continue
				}
				return ProcessResult{}, err
			}
		}
		completed, err := p.executePlan(ctx, stored)
		if err == nil {
			return processResult(completed), nil
		}
		if errors.Is(err, errRetryDecision) {
			// 只有第一项尚未生效且原版本已被其他操作推进时才撤销；已经生效的计划必须补齐。
			_, clearErr := p.writeEventResult(ctx, stored, store.EventResult{State: domain.EventProcessStateUnprocessed})
			if clearErr != nil && !errors.Is(clearErr, store.ErrVersionConflict) {
				return ProcessResult{}, clearErr
			}
		} else if !errors.Is(err, store.ErrVersionConflict) {
			return ProcessResult{}, err
		}
		lastErr = err
	}
	return ProcessResult{}, fmt.Errorf("process event after %d CAS attempts: %w", maxCASAttempts, lastErr)
}

func processResult(stored store.StoredEvent) ProcessResult {
	return ProcessResult{EventID: stored.Event.EventID, AlertIDs: slices.Clone(stored.Event.RelatedAlertIDs), EventState: stored.Processing.State, Outcome: ProcessOutcome(stored.Processing.Outcome), ReasonCode: stored.Processing.ReasonCode}
}

func (p *Processor) writeEventResult(ctx context.Context, stored store.StoredEvent, result store.EventResult) (store.StoredEvent, error) {
	if repo, ok := p.repository.(store.LifecycleEventStore); ok {
		return repo.CompareAndSetLifecycleEventResult(ctx, stored.Event.BKTenantID, stored.Event.EventID, stored.Version, result)
	}
	return p.repository.CompareAndSetEventResult(ctx, stored.Event.BKTenantID, stored.Event.EventID, stored.Version, result)
}

func (p *Processor) preparePlan(ctx context.Context, event domain.Event) (*store.EventPlan, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	plan := &store.EventPlan{ConfigDigest: p.configDigest, UpgradePolicy: p.upgradePolicy, State: domain.EventProcessStateOrphaned, Outcome: string(OutcomeEventOrphaned), ReasonCode: ReasonActiveAlertNotFound}
	active, err := p.findActiveAlert(ctx, store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
	hasActive := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	now, err := p.now()
	if err != nil {
		return nil, err
	}
	if hasActive {
		if _, ok := p.severity.Priority(active.Alert.Severity); !ok {
			closed := terminalAlert(active.Alert, event, domain.AlertStatusClosed, domain.AlertEndTypeSystem, "unknown_severity", now)
			closed.EndAt = &now
			plan.SystemClose = &closed
			hasActive = false
			p.logger.WarnContext(ctx, "closing active alert with unknown severity", "bk_tenant_id", event.BKTenantID, "event_source_id", event.EventSourceID, "alert_id", active.Alert.AlertID, "severity", active.Alert.Severity, "config_digest", p.configDigest)
		}
	}
	unknown := []string{}
	for _, evaluation := range event.Evaluations {
		if _, ok := p.severity.Priority(evaluation.Severity); !ok {
			unknown = append(unknown, evaluation.Severity)
		}
	}
	if len(unknown) > 0 {
		plan.State = domain.EventProcessStateRejected
		plan.Outcome = "event_rejected"
		plan.ReasonCode = "unknown_severity"
		for _, evaluation := range event.Evaluations {
			plan.Evaluations = append(plan.Evaluations, store.EvaluationResult{Severity: evaluation.Severity, Action: evaluation.Action, State: domain.EventProcessStateRejected, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode})
		}
		p.logger.WarnContext(ctx, "rejecting event with unknown severity", "bk_tenant_id", event.BKTenantID, "event_source_id", event.EventSourceID, "event_id", event.EventID, "unknown_severities", unknown, "config_digest", p.configDigest)
		return plan, plan.Validate()
	}
	highest := -1
	for i, evaluation := range event.Evaluations {
		if _, ok := p.severity.Priority(evaluation.Severity); !ok {
			return nil, fmt.Errorf("unknown evaluation severity %q", evaluation.Severity)
		}
		if evaluation.Action == domain.EventActionTriggered {
			if highest < 0 {
				highest = i
			} else {
				comparison, err := p.compareSeverity(evaluation.Severity, event.Evaluations[highest].Severity)
				if err != nil {
					return nil, err
				}
				if comparison < 0 {
					highest = i
				}
			}
		}
		plan.Evaluations = append(plan.Evaluations, store.EvaluationResult{Severity: evaluation.Severity, Action: evaluation.Action, State: domain.EventProcessStateOrphaned, Outcome: string(OutcomeEventOrphaned), ReasonCode: ReasonActiveAlertNotFound})
	}
	terminal := -1
	if hasActive {
		if _, ok := p.severity.Priority(active.Alert.Severity); !ok {
			return nil, fmt.Errorf("unknown active alert severity")
		}
		for i, evaluation := range event.Evaluations {
			if evaluation.Severity == active.Alert.Severity && evaluation.Action != domain.EventActionTriggered {
				terminal = i
			}
		}
	}
	upgrade := false
	if hasActive && highest >= 0 {
		comparison, compareErr := p.compareSeverity(event.Evaluations[highest].Severity, active.Alert.Severity)
		if compareErr != nil {
			return nil, compareErr
		}
		upgrade = comparison < 0
	}
	var current domain.Alert
	if hasActive {
		current = active.Alert
	}
	switch {
	case upgrade:
		evaluation := event.Evaluations[highest]
		if p.upgradePolicy == "update_current" {
			updated := advanceAlert(active.Alert, event, now)
			updated.Severity = evaluation.Severity
			if err := planMutation(plan, event, active.Version, updated, OutcomeAlertSeverityChanged, domain.OperationKindSeverityChange, ReasonSeverityUpgrade, map[string]string{"from_severity": active.Alert.Severity, "to_severity": updated.Severity}); err != nil {
				return nil, err
			}
			current = updated
		} else {
			ended := terminalAlert(active.Alert, event, domain.AlertStatusClosed, domain.AlertEndTypeSeverityUpgrade, evaluation.Severity, now)
			if err := planMutation(plan, event, active.Version, ended, OutcomeAlertClosed, domain.OperationKindClose, ReasonSeverityUpgrade, nil); err != nil {
				return nil, err
			}
			created, createErr := p.planNewAlert(ctx, plan, event, evaluation)
			if createErr != nil {
				return nil, createErr
			}
			current = created
			plan.Outcome = string(OutcomeAlertRotated)
		}
		setEvaluationResult(plan, highest, domain.EventProcessStateAccepted, plan.Outcome, "", plan.RelatedAlertIDs)
		if terminal >= 0 {
			setEvaluationResult(plan, terminal, domain.EventProcessStateSuppressed, "evaluation_superseded", "severity_upgrade", []string{active.Alert.AlertID})
		}
	default:
		if hasActive && terminal >= 0 {
			status, operation, outcome := domain.AlertStatusRecovered, domain.OperationKindRecover, OutcomeAlertRecovered
			if event.Evaluations[terminal].Action == domain.EventActionClosed {
				status, operation, outcome = domain.AlertStatusClosed, domain.OperationKindClose, OutcomeAlertClosed
			}
			ended := terminalAlert(active.Alert, event, status, domain.AlertEndTypeSource, event.Evaluations[terminal].ActionReason, now)
			if err := planMutation(plan, event, active.Version, ended, outcome, operation, string(outcome), nil); err != nil {
				return nil, err
			}
			setEvaluationResult(plan, terminal, domain.EventProcessStateAccepted, string(outcome), "", []string{ended.AlertID})
			hasActive = false
			current = domain.Alert{}
		}
		if highest >= 0 {
			evaluation := event.Evaluations[highest]
			switch {
			case !hasActive:
				created, createErr := p.planNewAlert(ctx, plan, event, evaluation)
				if createErr != nil {
					return nil, createErr
				}
				current = created
				setEvaluationResult(plan, highest, domain.EventProcessStateAccepted, string(OutcomeAlertCreated), "", []string{created.AlertID})
			case evaluation.Severity == active.Alert.Severity:
				updated := advanceAlert(active.Alert, event, now)
				if err := planMutation(plan, event, active.Version, updated, OutcomeAlertUpdated, "", "", nil); err != nil {
					return nil, err
				}
				current = updated
				setEvaluationResult(plan, highest, domain.EventProcessStateAccepted, string(OutcomeAlertUpdated), "", []string{updated.AlertID})
			}
		}
	}
	// 只对未被选中的 triggered 生成抑制流水；缺席级别不推导状态，恢复也不会命中其他级别。
	for i, evaluation := range event.Evaluations {
		if evaluation.Action != domain.EventActionTriggered || plan.Evaluations[i].State == domain.EventProcessStateAccepted {
			continue
		}
		if current.AlertID == "" {
			return nil, fmt.Errorf("trigger evaluation has no resulting active alert")
		}
		setEvaluationResult(plan, i, domain.EventProcessStateSuppressed, string(OutcomeAlertSuppressed), ReasonSeveritySuppressed, []string{current.AlertID})
		log, logErr := eventAlertLog(event, current, domain.OperationKindSuppress, ReasonSeveritySuppressed, event.CreateAt, map[string]string{"severity": evaluation.Severity})
		if logErr != nil {
			return nil, logErr
		}
		log.LogID = digestStrings(alertLogIDDomain, log.LogID, evaluation.Severity)
		plan.Logs = append(plan.Logs, log)
		plan.RelatedAlertIDs = append(plan.RelatedAlertIDs, current.AlertID)
		if len(plan.Mutations) == 0 {
			plan.State = domain.EventProcessStateSuppressed
			plan.Outcome = string(OutcomeAlertSuppressed)
			plan.ReasonCode = ReasonSeveritySuppressed
		}
	}
	slices.Sort(plan.RelatedAlertIDs)
	plan.RelatedAlertIDs = slices.Compact(plan.RelatedAlertIDs)
	return plan, plan.Validate()
}

func setEvaluationResult(plan *store.EventPlan, index int, state domain.EventProcessState, outcome, reason string, ids []string) {
	result := &plan.Evaluations[index]
	result.State = state
	result.Outcome = outcome
	result.ReasonCode = reason
	result.RelatedAlertIDs = slices.Clone(ids)
}

func advanceAlert(current domain.Alert, event domain.Event, now time.Time) domain.Alert {
	result := current.Clone()
	result.LatestEventID = event.EventID
	result.LastOccurredAt = event.OccurredAt
	result.UpdateAt = nextAlertUpdateTime(now, current.UpdateAt)
	return result
}

func terminalAlert(current domain.Alert, event domain.Event, status domain.AlertStatus, endType domain.AlertEndType, reason string, now time.Time) domain.Alert {
	result := advanceAlert(current, event, now)
	result.Status = status
	endAt := event.OccurredAt
	result.EndAt = &endAt
	result.EndType = endType
	result.EndReason = reason
	return result
}

func planMutation(plan *store.EventPlan, event domain.Event, expected store.VersionToken, alert domain.Alert, outcome ProcessOutcome, operation domain.OperationKind, reason string, extra map[string]string) error {
	plan.Mutations = append(plan.Mutations, store.AlertMutation{ExpectedVersion: expected.String(), Alert: alert, Outcome: string(outcome)})
	plan.State = domain.EventProcessStateAccepted
	plan.Outcome = string(outcome)
	plan.ReasonCode = ""
	plan.RelatedAlertIDs = append(plan.RelatedAlertIDs, alert.AlertID)
	if operation != "" {
		log, err := eventAlertLog(event, alert, operation, reason, alert.UpdateAt, extra)
		if err != nil {
			return err
		}
		plan.Logs = append(plan.Logs, log)
	}
	return nil
}

func (p *Processor) planNewAlert(ctx context.Context, plan *store.EventPlan, event domain.Event, evaluation domain.EventEvaluation) (domain.Alert, error) {
	id, err := p.idGenerator.Generate(event)
	if err != nil {
		return domain.Alert{}, err
	}
	now, err := p.now()
	if err != nil {
		return domain.Alert{}, err
	}
	alert := domain.Alert{AlertID: id, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, EventSourceVersion: event.EventSourceVersion, Fingerprint: event.Fingerprint, Title: event.Title, Content: event.Content, Severity: evaluation.Severity, Dimensions: event.Dimensions.Clone(), SubjectSystem: event.SubjectSystem, SubjectType: event.SubjectType, SubjectID: event.SubjectID, SubjectName: event.SubjectName, SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID, Labels: event.Labels.Clone(), ExtraData: event.ExtraData.Clone(), Status: domain.AlertStatusActive, LatestEventID: event.EventID, LastOccurredAt: event.OccurredAt, UpdateAt: now, TriggerEventID: event.EventID, BeginAt: event.OccurredAt, CreateAt: event.CreateAt, EnrichStatus: domain.EnrichStatusPending}
	alert, err = p.enrichNewAlert(ctx, alert)
	if err != nil {
		return domain.Alert{}, err
	}
	err = planMutation(plan, event, store.VersionToken{}, alert, OutcomeAlertCreated, domain.OperationKindTrigger, string(OutcomeAlertCreated), nil)
	return alert, err
}

func (p *Processor) executePlan(ctx context.Context, stored store.StoredEvent) (store.StoredEvent, error) {
	plan := stored.Processing.Plan
	if err := store.ValidateEventPlan(stored.Event, plan); err != nil {
		return store.StoredEvent{}, err
	}
	if target := plan.SystemClose; target != nil {
		current, err := p.getAlertCurrent(ctx, target.BKTenantID, target.AlertID)
		if err != nil {
			return store.StoredEvent{}, err
		}
		// 已被独立操作终结时不覆盖；自身部分成功则继续补齐同一关闭命令的日志和 hook。
		if current.Alert.Status == domain.AlertStatusActive || (current.Alert.EndType == domain.AlertEndTypeSystem && current.Alert.EndReason == "unknown_severity" && current.Alert.EndAt != nil && current.Alert.EndAt.Equal(*target.EndAt)) {
			_, err = p.CloseAlert(ctx, CloseAlertCommand{OperationID: digestStrings("unknown-severity", stored.Event.EventID, target.AlertID), BKTenantID: target.BKTenantID, AlertID: target.AlertID, OperatorKind: domain.OperatorKindSystem, OperatorID: "dynamic_config", Reason: "unknown_severity", EffectiveAt: *target.EndAt, ConfigDigest: plan.ConfigDigest})
			if err != nil {
				return store.StoredEvent{}, err
			}
		}
	}
	superseded := make([]bool, len(plan.Mutations))
	for index, mutation := range plan.Mutations {
		updated, err := p.applyMutation(ctx, stored.Event, mutation, index == 0)
		if err != nil {
			return store.StoredEvent{}, err
		}
		// 独立关闭命令可能已经推进该快照；不能在重试时向策略索引重发旧 active。
		superseded[index] = updated.Alert.UpdateAt.After(mutation.Alert.UpdateAt)
		if updated.Alert.Status == domain.AlertStatusActive {
			err = p.recentAlerts.PutCurrent(ctx, updated)
		} else {
			err = p.recentAlerts.PutTerminal(ctx, updated)
		}
		if err != nil {
			return store.StoredEvent{}, err
		}
	}
	// 保持旧告警终态输出在新告警创建输出之前；活跃索引 Hook 只合并刷新提示。
	logs := make([]domain.AlertLog, 0, len(plan.Logs)+len(plan.Mutations)*len(p.finalHooks))
	for index, mutation := range plan.Mutations {
		for _, log := range plan.Logs {
			if log.AlertID == mutation.Alert.AlertID && log.OperationKind != domain.OperationKindSuppress {
				logs = append(logs, log)
			}
		}
		if superseded[index] {
			continue
		}
		hookLogs, err := p.runFinalHooks(ctx, sourceEventCause(stored.Event), mutation.Alert, ProcessOutcome(mutation.Outcome))
		if err != nil {
			return store.StoredEvent{}, err
		}
		logs = append(logs, hookLogs...)
	}
	for _, log := range plan.Logs {
		if log.OperationKind == domain.OperationKindSuppress {
			logs = append(logs, log)
		}
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return store.StoredEvent{}, err
	}
	now, err := p.now()
	if err != nil {
		return store.StoredEvent{}, err
	}
	return p.writeEventResult(ctx, stored, store.EventResult{State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, RelatedAlertIDs: plan.RelatedAlertIDs, Evaluations: plan.Evaluations, ProcessedAt: now})
}

func (p *Processor) applyMutation(ctx context.Context, event domain.Event, mutation store.AlertMutation, first bool) (store.StoredAlert, error) {
	target := mutation.Alert
	current, err := p.getAlertCurrent(ctx, event.BKTenantID, target.AlertID)
	if err == nil {
		if reflect.DeepEqual(current.Alert, target) {
			return current, nil
		}
		// 源事件写入后可能已被独立 CloseAlert 命令关闭；该更新已经生效，不能重新打开它。
		if target.Status == domain.AlertStatusActive && current.Alert.Status.Terminal() && current.Alert.LatestEventID == event.EventID && current.Alert.Severity == target.Severity && current.Alert.UpdateAt.After(target.UpdateAt) {
			return current, nil
		}
		if mutation.ExpectedVersion == "" {
			return store.StoredAlert{}, fmt.Errorf("%w: planned alert create differs", store.ErrIdentityConflict)
		}
		if current.Version.String() != mutation.ExpectedVersion {
			if first {
				if err := p.recentAlerts.Repair(ctx, current); err != nil {
					return store.StoredAlert{}, err
				}
				return store.StoredAlert{}, errRetryDecision
			}
			return store.StoredAlert{}, fmt.Errorf("%w: planned alert changed after earlier mutation", store.ErrVersionConflict)
		}
		return p.compareAndSetAlert(ctx, current, target)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.StoredAlert{}, err
	}
	if mutation.ExpectedVersion != "" {
		return store.StoredAlert{}, fmt.Errorf("%w: planned alert disappeared", store.ErrNotFound)
	}
	created, err := p.createAlertAfterActiveLookup(ctx, target)
	return created.StoredAlert, err
}
