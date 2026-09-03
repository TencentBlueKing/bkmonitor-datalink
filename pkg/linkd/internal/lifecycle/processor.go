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
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

var errRetryDecision = errors.New("lifecycle decision changed concurrently")

// ProcessEvent 按唯一 fingerprint 关联通道处理一个未完成 Event。
func (p *Processor) ProcessEvent(ctx context.Context, bkTenantID, eventID string) (ProcessResult, error) {
	if ctx == nil {
		return ProcessResult{}, fmt.Errorf("process lifecycle event: context must not be nil")
	}
	if bkTenantID == "" || eventID == "" {
		return ProcessResult{}, fmt.Errorf("process lifecycle event: tenant and event id are required")
	}
	var lastConflict error
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		stored, err := p.repository.GetEvent(ctx, bkTenantID, eventID)
		if err != nil {
			return ProcessResult{}, fmt.Errorf("read lifecycle event %q: %w", eventID, err)
		}
		if stored.Processing.State != domain.EventProcessStateUnprocessed {
			return ProcessResult{EventID: stored.Event.EventID, AlertID: stored.Event.RelatedAlertID,
				EventState: stored.Processing.State, Outcome: ProcessOutcome(stored.Processing.Outcome),
				ReasonCode: stored.Processing.ReasonCode}, nil
		}
		result, err := p.processUnprocessed(ctx, stored)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, store.ErrVersionConflict) || errors.Is(err, errRetryDecision) {
			lastConflict = err
			continue
		}
		return ProcessResult{}, err
	}
	return ProcessResult{}, fmt.Errorf("process lifecycle event %q after %d CAS attempts: %w", eventID, maxCASAttempts, lastConflict)
}

func (p *Processor) processUnprocessed(ctx context.Context, stored store.StoredEvent) (ProcessResult, error) {
	event := stored.Event
	active, err := p.repository.FindActiveAlert(ctx, store.ActiveAlertKey{
		BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint,
	})
	if errors.Is(err, store.ErrNotFound) {
		ended, endedErr := p.repository.FindAlertEndedByEvent(ctx, event.BKTenantID, event.EventID)
		if endedErr == nil {
			return p.resumeEndedEvent(ctx, stored, ended)
		}
		if !errors.Is(endedErr, store.ErrNotFound) {
			return ProcessResult{}, fmt.Errorf("find alert ended by event %q: %w", event.EventID, endedErr)
		}
		switch event.Action {
		case domain.EventActionTriggered:
			return p.createAlert(ctx, stored)
		case domain.EventActionResolved, domain.EventActionClosed:
			return p.finishEvent(ctx, stored, domain.EventProcessStateOrphaned, "", OutcomeEventOrphaned, ReasonActiveAlertNotFound)
		default:
			return p.finishEvent(ctx, stored, domain.EventProcessStateRejected, "", OutcomeRejected, ReasonInvalidTransition)
		}
	}
	if err != nil {
		return ProcessResult{}, fmt.Errorf("find active alert for event %q: %w", event.EventID, err)
	}
	if active.Alert.LatestEventID == event.EventID {
		return p.resumeActiveEvent(ctx, stored, active)
	}

	switch event.Action {
	case domain.EventActionTriggered:
		comparison, compareErr := p.compareSeverity(event.Severity, active.Alert.Severity)
		if compareErr != nil {
			return ProcessResult{}, compareErr
		}
		switch comparison {
		case 0:
			return p.updateAlert(ctx, stored, active)
		case -1:
			return p.rotateAlert(ctx, stored, active)
		case 1:
			return p.suppressEvent(ctx, stored, active)
		}
	case domain.EventActionResolved:
		return p.terminateAlert(ctx, stored, active, domain.AlertStatusRecovered)
	case domain.EventActionClosed:
		return p.terminateAlert(ctx, stored, active, domain.AlertStatusClosed)
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateRejected, "", OutcomeRejected, ReasonInvalidTransition)
}

// compareSeverity 返回 -1 表示 incoming 更严重，0 表示相同，1 表示更低。
func (p *Processor) compareSeverity(incoming, current string) (int, error) {
	incomingPriority, ok := p.severity.Priority(incoming)
	if !ok {
		return 0, fmt.Errorf("incoming severity is not configured: %q", incoming)
	}
	currentPriority, ok := p.severity.Priority(current)
	if !ok {
		return 0, fmt.Errorf("current alert severity is not configured: %q", current)
	}
	switch {
	case incomingPriority < currentPriority:
		return -1, nil
	case incomingPriority > currentPriority:
		return 1, nil
	default:
		return 0, nil
	}
}

func (p *Processor) createAlert(ctx context.Context, stored store.StoredEvent) (ProcessResult, error) {
	alert, err := p.newAlert(ctx, stored.Event)
	if err != nil {
		return ProcessResult{}, err
	}
	created, err := p.repository.CreateAlert(ctx, alert)
	if errors.Is(err, store.ErrIdentityConflict) {
		existing, readErr := p.repository.GetAlert(ctx, alert.BKTenantID, alert.AlertID)
		if readErr == nil && existing.Alert.TriggerEventID == stored.Event.EventID {
			return p.resumeActiveEvent(ctx, stored, existing)
		}
		return ProcessResult{}, fmt.Errorf("%w: active alert appeared while creating", errRetryDecision)
	}
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create alert for event %q: %w", stored.Event.EventID, err)
	}
	operationLog, err := eventAlertLog(stored.Event, created.Alert, domain.OperationKindTrigger,
		string(OutcomeAlertCreated), created.Alert.UpdateAt, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	cause := sourceEventCause(stored.Event)
	hookLog, err := p.runFinalHook(ctx, cause, created.Alert, OutcomeAlertCreated)
	if err != nil {
		return ProcessResult{}, err
	}
	logs := []domain.AlertLog{operationLog}
	if hookLog != nil {
		logs = append(logs, *hookLog)
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, created.Alert.AlertID, OutcomeAlertCreated, "")
}

func (p *Processor) updateAlert(ctx context.Context, stored store.StoredEvent, active store.StoredAlert) (ProcessResult, error) {
	now, err := p.now()
	if err != nil {
		return ProcessResult{}, err
	}
	replacement := active.Alert.Clone()
	replacement.LatestEventID = stored.Event.EventID
	replacement.LastOccurredAt = stored.Event.OccurredAt
	replacement.UpdateAt = nextAlertUpdateTime(now, active.Alert.UpdateAt)
	updated, err := p.repository.CompareAndSetAlert(ctx, active.Alert.BKTenantID, active.Alert.AlertID, active.Version, replacement)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("update alert %q: %w", active.Alert.AlertID, err)
	}
	hookLog, err := p.runFinalHook(ctx, sourceEventCause(stored.Event), updated.Alert, OutcomeAlertUpdated)
	if err != nil {
		return ProcessResult{}, err
	}
	if hookLog != nil {
		if err := p.appendAlertLogs(ctx, []domain.AlertLog{*hookLog}); err != nil {
			return ProcessResult{}, err
		}
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, updated.Alert.AlertID, OutcomeAlertUpdated, "")
}

func (p *Processor) terminateAlert(ctx context.Context, stored store.StoredEvent, active store.StoredAlert, status domain.AlertStatus) (ProcessResult, error) {
	now, err := p.now()
	if err != nil {
		return ProcessResult{}, err
	}
	replacement := sourceTerminalAlert(active.Alert, stored.Event, status, now)
	updated, err := p.repository.CompareAndSetAlert(ctx, active.Alert.BKTenantID, active.Alert.AlertID, active.Version, replacement)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("terminate alert %q as %s: %w", active.Alert.AlertID, status, err)
	}
	operation, outcome := domain.OperationKindRecover, OutcomeAlertRecovered
	if status == domain.AlertStatusClosed {
		operation, outcome = domain.OperationKindClose, OutcomeAlertClosed
	}
	operationLog, err := eventAlertLog(stored.Event, updated.Alert, operation, string(outcome), updated.Alert.UpdateAt, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	hookLog, err := p.runFinalHook(ctx, sourceEventCause(stored.Event), updated.Alert, outcome)
	if err != nil {
		return ProcessResult{}, err
	}
	logs := []domain.AlertLog{operationLog}
	if hookLog != nil {
		logs = append(logs, *hookLog)
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, updated.Alert.AlertID, outcome, "")
}

func (p *Processor) suppressEvent(ctx context.Context, stored store.StoredEvent, active store.StoredAlert) (ProcessResult, error) {
	log, err := eventAlertLog(stored.Event, active.Alert, domain.OperationKindSuppress,
		ReasonSeveritySuppressed, stored.Event.CreateAt, map[string]string{"severity": stored.Event.Severity})
	if err != nil {
		return ProcessResult{}, err
	}
	if err := p.appendAlertLogs(ctx, []domain.AlertLog{log}); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateSuppressed, active.Alert.AlertID, OutcomeAlertSuppressed, ReasonSeveritySuppressed)
}

func (p *Processor) rotateAlert(ctx context.Context, stored store.StoredEvent, active store.StoredAlert) (ProcessResult, error) {
	newAlert, err := p.newAlert(ctx, stored.Event)
	if err != nil {
		return ProcessResult{}, err
	}
	now, err := p.now()
	if err != nil {
		return ProcessResult{}, err
	}
	closed := severityUpgradeAlert(active.Alert, stored.Event, now)
	closedStored, err := p.repository.CompareAndSetAlert(ctx, active.Alert.BKTenantID, active.Alert.AlertID, active.Version, closed)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("close alert %q for severity upgrade: %w", active.Alert.AlertID, err)
	}
	return p.completeRotation(ctx, stored, closedStored, newAlert)
}

func (p *Processor) completeRotation(ctx context.Context, stored store.StoredEvent, closed store.StoredAlert, newAlert domain.Alert) (ProcessResult, error) {
	created, err := p.repository.CreateAlert(ctx, newAlert)
	if errors.Is(err, store.ErrIdentityConflict) {
		existing, readErr := p.repository.GetAlert(ctx, newAlert.BKTenantID, newAlert.AlertID)
		if readErr != nil {
			return ProcessResult{}, fmt.Errorf("read upgraded alert %q: %w", newAlert.AlertID, readErr)
		}
		if existing.Alert.Status != domain.AlertStatusActive || existing.Alert.TriggerEventID != stored.Event.EventID {
			return ProcessResult{}, fmt.Errorf("%w: upgraded alert has conflicting content", errRetryDecision)
		}
		created = store.CreateAlertResult{StoredAlert: existing, Created: false}
	} else if err != nil {
		return ProcessResult{}, fmt.Errorf("create upgraded alert %q: %w", newAlert.AlertID, err)
	}
	return p.finishRotation(ctx, stored, closed, created.StoredAlert)
}

func (p *Processor) finishRotation(ctx context.Context, stored store.StoredEvent, closed, created store.StoredAlert) (ProcessResult, error) {
	event := stored.Event
	if closed.Alert.Status != domain.AlertStatusClosed || closed.Alert.EndType != domain.AlertEndTypeSeverityUpgrade ||
		closed.Alert.LatestEventID != event.EventID || created.Alert.Status != domain.AlertStatusActive ||
		created.Alert.TriggerEventID != event.EventID || created.Alert.LatestEventID != event.EventID {
		return ProcessResult{}, fmt.Errorf("finish upgraded event %q: persisted alerts do not match recovery plan", event.EventID)
	}
	closedLog, err := eventAlertLog(event, closed.Alert, domain.OperationKindClose, ReasonSeverityUpgrade, closed.Alert.UpdateAt,
		map[string]string{"severity": event.Severity})
	if err != nil {
		return ProcessResult{}, err
	}
	createdLog, err := eventAlertLog(event, created.Alert, domain.OperationKindTrigger, ReasonSeverityUpgrade, created.Alert.UpdateAt, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	closedHookLog, err := p.runFinalHook(ctx, sourceEventCause(event), closed.Alert, OutcomeAlertClosed)
	if err != nil {
		return ProcessResult{}, err
	}
	createdHookLog, err := p.runFinalHook(ctx, sourceEventCause(event), created.Alert, OutcomeAlertCreated)
	if err != nil {
		return ProcessResult{}, err
	}
	logs := []domain.AlertLog{closedLog}
	if closedHookLog != nil {
		logs = append(logs, *closedHookLog)
	}
	logs = append(logs, createdLog)
	if createdHookLog != nil {
		logs = append(logs, *createdHookLog)
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, created.Alert.AlertID, OutcomeAlertRotated, "")
}

func (p *Processor) resumeActiveEvent(ctx context.Context, stored store.StoredEvent, active store.StoredAlert) (ProcessResult, error) {
	if active.Alert.LatestEventID != stored.Event.EventID {
		return ProcessResult{}, fmt.Errorf("resume active alert latest event mismatch")
	}
	if active.Alert.TriggerEventID != stored.Event.EventID {
		hookLog, err := p.runFinalHook(ctx, sourceEventCause(stored.Event), active.Alert, OutcomeAlertUpdated)
		if err != nil {
			return ProcessResult{}, err
		}
		if hookLog != nil {
			if err := p.appendAlertLogs(ctx, []domain.AlertLog{*hookLog}); err != nil {
				return ProcessResult{}, err
			}
		}
		return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, active.Alert.AlertID, OutcomeAlertUpdated, "")
	}
	ended, err := p.repository.FindAlertEndedByEvent(ctx, stored.Event.BKTenantID, stored.Event.EventID)
	if err == nil {
		return p.finishRotation(ctx, stored, ended, active)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return ProcessResult{}, err
	}
	operationLog, err := eventAlertLog(stored.Event, active.Alert, domain.OperationKindTrigger, string(OutcomeAlertCreated), active.Alert.UpdateAt, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	hookLog, err := p.runFinalHook(ctx, sourceEventCause(stored.Event), active.Alert, OutcomeAlertCreated)
	if err != nil {
		return ProcessResult{}, err
	}
	logs := []domain.AlertLog{operationLog}
	if hookLog != nil {
		logs = append(logs, *hookLog)
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, active.Alert.AlertID, OutcomeAlertCreated, "")
}

func (p *Processor) resumeEndedEvent(ctx context.Context, stored store.StoredEvent, ended store.StoredAlert) (ProcessResult, error) {
	event := stored.Event
	if ended.Alert.LatestEventID != event.EventID {
		return ProcessResult{}, fmt.Errorf("ended alert event mismatch")
	}
	if ended.Alert.EndType == domain.AlertEndTypeSeverityUpgrade && event.Action == domain.EventActionTriggered {
		newAlert, err := p.newAlert(ctx, event)
		if err != nil {
			return ProcessResult{}, err
		}
		return p.completeRotation(ctx, stored, ended, newAlert)
	}
	operation, outcome := domain.OperationKindRecover, OutcomeAlertRecovered
	if event.Action == domain.EventActionClosed {
		operation, outcome = domain.OperationKindClose, OutcomeAlertClosed
	}
	if event.Action != domain.EventActionResolved && event.Action != domain.EventActionClosed {
		return ProcessResult{}, fmt.Errorf("ended alert action mismatch")
	}
	operationLog, err := eventAlertLog(event, ended.Alert, operation, string(outcome), ended.Alert.UpdateAt, nil)
	if err != nil {
		return ProcessResult{}, err
	}
	hookLog, err := p.runFinalHook(ctx, sourceEventCause(event), ended.Alert, outcome)
	if err != nil {
		return ProcessResult{}, err
	}
	logs := []domain.AlertLog{operationLog}
	if hookLog != nil {
		logs = append(logs, *hookLog)
	}
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return ProcessResult{}, err
	}
	return p.finishEvent(ctx, stored, domain.EventProcessStateAccepted, ended.Alert.AlertID, outcome, "")
}

func (p *Processor) newAlert(ctx context.Context, event domain.Event) (domain.Alert, error) {
	alertID, err := p.idGenerator.Generate(event)
	if err != nil {
		return domain.Alert{}, fmt.Errorf("generate alert id: %w", err)
	}
	now, err := p.now()
	if err != nil {
		return domain.Alert{}, err
	}
	alert := domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Title: event.Title, Content: event.Content, Severity: event.Severity,
		ConditionKey: event.ConditionKey, ConditionName: event.ConditionName, Dimensions: event.Dimensions.Clone(),
		SubjectSystem: event.SubjectSystem, SubjectType: event.SubjectType, SubjectID: event.SubjectID, SubjectName: event.SubjectName,
		SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID, Labels: event.Labels.Clone(), ExtraData: event.ExtraData.Clone(),
		Status: domain.AlertStatusActive, LatestEventID: event.EventID, LastOccurredAt: event.OccurredAt,
		UpdateAt: now, TriggerEventID: event.EventID, BeginAt: event.OccurredAt, CreateAt: event.CreateAt,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
	return p.enrichNewAlert(ctx, event, alert)
}

func (p *Processor) finishEvent(ctx context.Context, stored store.StoredEvent, state domain.EventProcessState,
	alertID string, outcome ProcessOutcome, reasonCode string) (ProcessResult, error) {
	now, err := p.now()
	if err != nil {
		return ProcessResult{}, err
	}
	updated, err := p.repository.CompareAndSetEventResult(ctx, stored.Event.BKTenantID, stored.Event.EventID, stored.Version,
		store.EventResult{State: state, RelatedAlertID: alertID, Outcome: string(outcome), ReasonCode: reasonCode, ProcessedAt: now})
	if err != nil {
		return ProcessResult{}, fmt.Errorf("finish event %q: %w", stored.Event.EventID, err)
	}
	return ProcessResult{EventID: updated.Event.EventID, AlertID: updated.Event.RelatedAlertID,
		EventState: updated.Processing.State, Outcome: outcome, ReasonCode: reasonCode}, nil
}

// CloseAlert 直接关闭已有 active Alert，不创建 Event。
func (p *Processor) CloseAlert(ctx context.Context, command CloseAlertCommand) (CloseAlertResult, error) {
	if ctx == nil {
		return CloseAlertResult{}, fmt.Errorf("close alert: context must not be nil")
	}
	if err := command.Validate(); err != nil {
		return CloseAlertResult{}, err
	}
	command.EffectiveAt = command.EffectiveAt.Round(0).UTC()
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		stored, err := p.repository.GetAlert(ctx, command.BKTenantID, command.AlertID)
		if err != nil {
			return CloseAlertResult{}, err
		}
		endType := domain.AlertEndTypeUser
		causeType := AlertChangeCauseUserOperation
		if command.OperatorKind == domain.OperatorKindSystem {
			endType = domain.AlertEndTypeSystem
			causeType = AlertChangeCauseSystemOperation
		}
		if stored.Alert.Status.Terminal() {
			if stored.Alert.Status != domain.AlertStatusClosed || stored.Alert.EndType != endType || stored.Alert.EndReason != command.Reason ||
				stored.Alert.EndAt == nil || !stored.Alert.EndAt.Equal(command.EffectiveAt) {
				return CloseAlertResult{}, fmt.Errorf("%w: alert is already terminal", store.ErrInvalidTransition)
			}
			operationLog, err := operationCloseLog(command, stored.Alert)
			if err != nil {
				return CloseAlertResult{}, err
			}
			hookLog, err := p.runFinalHook(
				ctx,
				AlertChangeCause{Type: causeType, ID: command.OperationID},
				stored.Alert,
				OutcomeAlertClosed,
			)
			if err != nil {
				return CloseAlertResult{}, err
			}
			logs := []domain.AlertLog{operationLog}
			if hookLog != nil {
				logs = append(logs, *hookLog)
			}
			if err := p.appendAlertLogs(ctx, logs); err != nil {
				return CloseAlertResult{}, err
			}
			return CloseAlertResult{Alert: stored.Alert.Clone(), AlreadyClosed: true}, nil
		}
		replacement := stored.Alert.Clone()
		replacement.Status = domain.AlertStatusClosed
		replacement.UpdateAt = nextAlertUpdateTime(command.EffectiveAt, stored.Alert.UpdateAt)
		endAt := command.EffectiveAt
		replacement.EndAt = &endAt
		replacement.EndType = endType
		replacement.EndReason = command.Reason
		updated, err := p.repository.CompareAndSetAlert(ctx, command.BKTenantID, command.AlertID, stored.Version, replacement)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return CloseAlertResult{}, err
		}
		operationLog, err := operationCloseLog(command, updated.Alert)
		if err != nil {
			return CloseAlertResult{}, err
		}
		hookLog, err := p.runFinalHook(
			ctx,
			AlertChangeCause{Type: causeType, ID: command.OperationID},
			updated.Alert,
			OutcomeAlertClosed,
		)
		if err != nil {
			return CloseAlertResult{}, err
		}
		logs := []domain.AlertLog{operationLog}
		if hookLog != nil {
			logs = append(logs, *hookLog)
		}
		if err := p.appendAlertLogs(ctx, logs); err != nil {
			return CloseAlertResult{}, err
		}
		return CloseAlertResult{Alert: updated.Alert.Clone()}, nil
	}
	return CloseAlertResult{}, fmt.Errorf("close alert after %d CAS attempts: %w", maxCASAttempts, store.ErrVersionConflict)
}

func (p *Processor) now() (time.Time, error) {
	now := p.clock.Now().Round(0).UTC()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("lifecycle clock returned zero time")
	}
	return now, nil
}

func nextAlertUpdateTime(now, current time.Time) time.Time {
	if !now.After(current) {
		return current.Add(time.Nanosecond)
	}
	return now
}

func sourceTerminalAlert(current domain.Alert, event domain.Event, status domain.AlertStatus, now time.Time) domain.Alert {
	replacement := current.Clone()
	replacement.Status = status
	replacement.LatestEventID = event.EventID
	replacement.LastOccurredAt = event.OccurredAt
	replacement.UpdateAt = nextAlertUpdateTime(now, current.UpdateAt)
	endAt := event.OccurredAt
	replacement.EndAt = &endAt
	replacement.EndType = domain.AlertEndTypeSource
	replacement.EndReason = event.ActionReason
	return replacement
}

func severityUpgradeAlert(current domain.Alert, event domain.Event, now time.Time) domain.Alert {
	replacement := current.Clone()
	replacement.Status = domain.AlertStatusClosed
	replacement.LatestEventID = event.EventID
	replacement.LastOccurredAt = event.OccurredAt
	replacement.UpdateAt = nextAlertUpdateTime(now, current.UpdateAt)
	endAt := event.OccurredAt
	replacement.EndAt = &endAt
	replacement.EndType = domain.AlertEndTypeSeverityUpgrade
	replacement.EndReason = event.Severity
	return replacement
}

func sourceEventCause(event domain.Event) AlertChangeCause {
	return AlertChangeCause{Type: AlertChangeCauseSourceEvent, ID: event.EventID}
}
