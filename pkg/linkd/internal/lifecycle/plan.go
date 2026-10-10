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
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
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
			var err error
			stored, err = p.enrichEvent(ctx, stored)
			if err != nil {
				if errors.Is(err, store.ErrVersionConflict) {
					lastErr = err
					continue
				}
				return ProcessResult{}, err
			}
			stored, err = p.freezePolicies(ctx, stored)
			if err != nil {
				if errors.Is(err, store.ErrVersionConflict) {
					lastErr = err
					continue
				}
				return ProcessResult{}, err
			}
			plan, err := p.preparePlan(ctx, stored.Event, stored.Processing.PolicyContext)
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

func (p *Processor) preparePlan(ctx context.Context, event domain.Event, snapshot *store.PolicyContext) (*store.EventPlan, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	plan := &store.EventPlan{ConfigDigest: p.configDigest, UpgradePolicy: p.upgradePolicy, State: domain.EventProcessStateOrphaned, Outcome: string(OutcomeEventOrphaned), ReasonCode: ReasonActiveAlertNotFound}
	active, err := p.findActiveAlert(ctx, store.ActiveAlertKey{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint})
	if err == nil && (active.Alert.PolicyChange != nil || active.Alert.MergeChange != nil || active.Alert.ActionPending != nil) {
		active, err = p.finishPendingChanges(ctx, active)
	}
	hasActive := err == nil
	associatedActive := domain.Alert{}
	if hasActive {
		associatedActive = active.Alert
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	now, err := p.now()
	if err != nil {
		return nil, err
	}
	closeAll := slices.ContainsFunc(event.Evaluations, func(e domain.EventEvaluation) bool { return e.Severity == domain.SeverityAll })
	// 显式全级别关闭直接匹配当前活动告警，即使该告警的等级已从动态配置删除，
	// 也保留来源给出的关闭时间和原因，不改写成 unknown_severity 系统关闭。
	if hasActive && !closeAll {
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
		if evaluation.Severity == domain.SeverityAll {
			continue
		}
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
	highest, err = p.selectNewAlert(ctx, event, snapshot, plan, associatedActive, highest)
	if err != nil {
		return nil, err
	}
	terminal := -1
	if hasActive {
		for i, evaluation := range event.Evaluations {
			// __ALL__ 优先于同级终结判定，结果不依赖 evaluations 的数组顺序。
			if evaluation.Severity == domain.SeverityAll {
				terminal = i
				break
			}
			if evaluation.Severity == active.Alert.Severity && evaluation.Action != domain.EventActionTriggered {
				terminal = i
			}
		}
	}
	upgrade := false
	// 全级别关闭先结束旧生命周期；同一事件若仍有 triggered，再创建新告警，
	// 避免升级策略把明确的来源关闭吞掉。
	if hasActive && highest >= 0 && !closeAll {
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
	if err := p.planAdmission(ctx, event, snapshot, plan, highest); err != nil {
		return nil, err
	}
	for i := range plan.Mutations {
		m := &plan.Mutations[i]
		if err := freezeAction(&m.Alert, m.Alert.Revision, sourceEventCause(event), m.ActionReady || (m.Alert.Status.Terminal() && m.Alert.Admission.AdmittedAt != nil)); err != nil {
			return nil, err
		}
	}
	// 终态清理没有后续触发准入阶段，用同一持久化 EventPlan 保存原绑定，日志重试不丢历史。
	if associatedActive.Shield.Active {
		for _, mutation := range plan.Mutations {
			if mutation.Alert.AlertID != associatedActive.AlertID || !mutation.Alert.Status.Terminal() {
				continue
			}
			log, err := eventAlertLog(event, mutation.Alert, domain.OperationKindUnshield, "alert_ended", mutation.Alert.UpdateAt, nil)
			if err != nil {
				return nil, err
			}
			before, err := json.Marshal(associatedActive.Shield.Bindings)
			if err != nil {
				return nil, err
			}
			log.Params["before_bindings"] = before
			log.Params["bindings"] = before
			log.Params["after_bindings"] = json.RawMessage(`[]`)
			plan.Logs = append(plan.Logs, log)
		}
	}
	// 只对未被选中的 triggered 生成抑制流水；缺席级别不推导状态，恢复也不会命中其他级别。
	for i, evaluation := range event.Evaluations {
		if evaluation.Action != domain.EventActionTriggered || plan.Evaluations[i].State == domain.EventProcessStateAccepted || plan.Evaluations[i].State == domain.EventProcessStateSuppressed {
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
	result.Revision = current.Revision + 1
	// 输入已通过领域校验；越界仍由最终计划校验拒绝，不能冻结一个遗漏 required_revision 的业务快照。
	for id, target := range result.Projection.Targets {
		target.RequiredRevision = result.Revision
		result.Projection.Targets[id] = target
	}
	result.LatestEventID = event.EventID
	result.LastOccurredAt = event.OccurredAt
	result.UpdateAt = nextAlertUpdateTime(now, current.UpdateAt)
	return result
}

func terminalAlert(current domain.Alert, event domain.Event, status domain.AlertStatus, endType domain.AlertEndType, reason string, now time.Time) domain.Alert {
	result := advanceAlert(current, event, now)
	result.Status = status
	result.Shield = domain.AlertShield{}
	result.Merge = result.Merge.EndWaiting()
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
	alert := domain.Alert{Revision: 1, AlertID: id, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, EventSourceVersion: event.EventSourceVersion, Fingerprint: event.Fingerprint, Title: event.Title, Content: event.Content, Severity: evaluation.Severity, Dimensions: event.Dimensions.Clone(), SubjectSystem: event.SubjectSystem, SubjectType: event.SubjectType, SubjectID: event.SubjectID, SubjectName: event.SubjectName, SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID, Labels: event.Labels.Clone(), ExtraData: event.ExtraData.Clone(), Status: domain.AlertStatusActive, LatestEventID: event.EventID, LastOccurredAt: event.OccurredAt, UpdateAt: now, TriggerEventID: event.EventID, BeginAt: event.OccurredAt, CreateAt: event.CreateAt, EnrichStatus: domain.EnrichStatusPending}
	if event.MergeOrigin != nil {
		alert.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: event.MergeOrigin.OperationID}
		alert.PolicyTags = slices.Clone(event.MergeOrigin.AlarmTags)
	}
	alert.Projection, err = p.initialProjection(ctx, event)
	if err != nil {
		return domain.Alert{}, err
	}
	enriched, ok := event.ForSeverity(evaluation.Severity)
	if !ok {
		return domain.Alert{}, fmt.Errorf("opening event has no enrich result for selected severity")
	}
	alert.EnrichStatus, alert.Enrich = enriched.Status, enriched.Data.Clone()
	// Event 丰富已冻结；只有真正创建的候选才生成核心内容，失败不冻结业务计划。
	alert.Content, err = p.buildNewAlertContent(ctx, event, evaluation, alert)
	if err != nil {
		return domain.Alert{}, err
	}
	alert, err = alert.Normalize()
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
		updated, err = p.finishActionIntent(ctx, updated)
		if err != nil {
			return store.StoredEvent{}, err
		}
		if updated.Alert.Status.Terminal() {
			if err := p.clearSuppression(ctx, updated.Alert); err != nil {
				return store.StoredEvent{}, err
			}
		} else {
			if p.suppressor != nil && mutation.ExpectedVersion == "" {
				// 聚合登记要求处置已放行；依赖待处理主只要求真实 Alert 存在。
				if err := p.suppressor.Bind(ctx, stored.Event, plan.PolicyDecision, updated.Alert); err != nil {
					return store.StoredEvent{}, err
				}
			}
			if p.merger != nil {
				if err := p.merger.Confirm(ctx, stored.Event, plan.PolicyDecision, updated.Alert); err != nil {
					return store.StoredEvent{}, err
				}
			}
			if p.dependency != nil {
				if err := p.dependency.BindDependency(ctx, stored.Event, plan.PolicyDecision, updated.Alert); err != nil {
					return store.StoredEvent{}, err
				}
			}
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
	// 没有 Alert 的恢复/关闭仍清理未绑定计数；存在新触发时不能在其计数后撤销新窗口。
	if p.suppressor != nil && len(plan.Mutations) == 0 {
		terminalOnly := true
		for _, e := range stored.Event.Evaluations {
			if e.Action == domain.EventActionTriggered {
				terminalOnly = false
			}
		}
		if terminalOnly {
			if err := p.suppressor.Clear(ctx, suppressioncleanup.Cause{TenantID: stored.Event.BKTenantID, SourceID: stored.Event.EventSourceID, Fingerprint: stored.Event.Fingerprint, Trigger: "event_terminal", EventID: stored.Event.EventID}); err != nil {
				return store.StoredEvent{}, err
			}
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
		hookLogs, err := p.runHooks(ctx, sourceEventCause(stored.Event), mutation.Alert, ProcessOutcome(mutation.Outcome), mutation.ActionReady || (mutation.Alert.Status.Terminal() && mutation.Alert.Admission.AdmittedAt != nil))
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
	return p.writeEventResult(ctx, stored, store.EventResult{PolicyDecision: plan.PolicyDecision, State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, RelatedAlertIDs: plan.RelatedAlertIDs, Evaluations: plan.Evaluations, ProcessedAt: now})
}

func (p *Processor) applyMutation(ctx context.Context, event domain.Event, mutation store.AlertMutation, first bool) (store.StoredAlert, error) {
	target := mutation.Alert
	current, err := p.getAlertCurrent(ctx, event.BKTenantID, target.AlertID)
	if err == nil {
		if domain.SameAlertBusinessSnapshot(current.Alert, target) {
			return current, nil
		}
		// 源写入之后，控制任务可能继续屏蔽/合并/关闭该生命周期。revision 加上保留的源事件/创建锚点
		// 证明旧计划已经生效；不能把输出重试当成身份冲突，也不能用旧快照覆盖较新的业务结果。
		sameLifecycle := current.Alert.BKTenantID == target.BKTenantID && current.Alert.EventSourceID == target.EventSourceID && current.Alert.Fingerprint == target.Fingerprint && current.Alert.TriggerEventID == target.TriggerEventID
		if sameLifecycle && current.Alert.Revision > target.Revision && (current.Alert.LatestEventID == event.EventID || (mutation.ExpectedVersion == "" && current.Alert.TriggerEventID == event.EventID)) {
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
