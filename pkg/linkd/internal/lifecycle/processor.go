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

func (p *Processor) getLifecycleEvent(ctx context.Context, bkTenantID, eventID string) (store.StoredEvent, error) {
	if lifecycleStore, ok := p.repository.(store.LifecycleEventStore); ok {
		return lifecycleStore.GetLifecycleEvent(ctx, bkTenantID, eventID)
	}
	return p.repository.GetEvent(ctx, bkTenantID, eventID)
}

func (p *Processor) findActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	cached, found, err := p.recentAlerts.GetCurrent(ctx, key)
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("read recent active alert cache: %w", err)
	}
	if found {
		if cached.Alert.Status == domain.AlertStatusActive {
			return cached, nil
		}
		return store.StoredAlert{}, fmt.Errorf("%w: active alert", store.ErrNotFound)
	}
	active, err := p.repository.FindActiveAlert(ctx, key)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if err := p.recentAlerts.PutCurrent(ctx, active); err != nil {
		return store.StoredAlert{}, fmt.Errorf("cache active alert from repository: %w", err)
	}
	return active, nil
}

func (p *Processor) createAlertAfterActiveLookup(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	if repository, ok := p.repository.(store.LifecycleAlertStore); ok {
		return repository.CreateAlertAfterActiveLookup(ctx, alert)
	}
	return p.repository.CreateAlert(ctx, alert)
}

func (p *Processor) compareAndSetAlert(
	ctx context.Context,
	current store.StoredAlert,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	var updated store.StoredAlert
	var err error
	if repository, ok := p.repository.(store.LifecycleAlertStore); ok {
		updated, err = repository.CompareAndSetAlertAfterActiveLookup(
			ctx,
			current.Alert.BKTenantID,
			current.Alert.AlertID,
			current.Version,
			replacement,
		)
	} else {
		updated, err = p.repository.CompareAndSetAlert(
			ctx,
			current.Alert.BKTenantID,
			current.Alert.AlertID,
			current.Version,
			replacement,
		)
	}
	if !errors.Is(err, store.ErrVersionConflict) {
		return updated, err
	}
	if repairErr := p.repairRecentAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID); repairErr != nil {
		return store.StoredAlert{}, errors.Join(err, repairErr)
	}
	return store.StoredAlert{}, err
}

func (p *Processor) repairRecentAlert(ctx context.Context, bkTenantID, alertID string) error {
	current, err := p.getAlertCurrent(ctx, bkTenantID, alertID)
	if err != nil {
		return fmt.Errorf("read current alert %q after CAS conflict: %w", alertID, err)
	}
	if err := p.recentAlerts.Repair(ctx, current); err != nil {
		return fmt.Errorf("repair recent alert cache for %q: %w", alertID, err)
	}
	return nil
}

func (p *Processor) getAlertCurrent(
	ctx context.Context,
	bkTenantID, alertID string,
) (store.StoredAlert, error) {
	if repository, ok := p.repository.(store.LifecycleAlertStore); ok {
		return repository.GetAlertCurrent(ctx, bkTenantID, alertID)
	}
	return p.repository.GetAlert(ctx, bkTenantID, alertID)
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
			hookLog, err := p.runFinalHooks(
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
				logs = append(logs, hookLog...)
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
		updated, err := p.compareAndSetAlert(ctx, stored, replacement)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return CloseAlertResult{}, err
		}
		if err := p.recentAlerts.PutCurrent(ctx, updated); err != nil {
			return CloseAlertResult{}, fmt.Errorf("cache directly closed alert %q: %w", command.AlertID, err)
		}
		operationLog, err := operationCloseLog(command, updated.Alert)
		if err != nil {
			return CloseAlertResult{}, err
		}
		hookLog, err := p.runFinalHooks(
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
			logs = append(logs, hookLog...)
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

func sourceEventCause(event domain.Event) AlertChangeCause {
	return AlertChangeCause{Type: AlertChangeCauseSourceEvent, ID: event.EventID}
}
