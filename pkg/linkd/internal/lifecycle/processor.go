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
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

var errRetryDecision = errors.New("lifecycle decision changed concurrently")

func (p *Processor) getLifecycleEvent(ctx context.Context, bkTenantID, eventID string) (store.StoredEvent, error) {
	defer p.measureStep(phaseOther, "event_reload")()
	if _, ok := p.repository.(store.LifecycleEventSnapshotStore); ok {
		return p.repository.GetEvent(ctx, bkTenantID, eventID)
	}
	if lifecycleStore, ok := p.repository.(store.LifecycleEventStore); ok {
		return lifecycleStore.GetLifecycleEvent(ctx, bkTenantID, eventID)
	}
	return p.repository.GetEvent(ctx, bkTenantID, eventID)
}

func (p *Processor) findActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	defer p.measureStep(phasePlan, "active_lookup")()
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
	defer p.measureStep(phaseApply, "alert_create")()
	var result store.CreateAlertResult
	var err error
	if repository, ok := p.repository.(store.LifecycleAlertStore); ok {
		result, err = repository.CreateAlertAfterActiveLookup(ctx, alert)
	} else {
		result, err = p.repository.CreateAlert(ctx, alert)
	}
	if err != nil && ctx.Err() == nil {
		// 创建可能已提交但响应丢失；同一 opening 身份直接采用真实状态，不再次创建。
		current, readErr := p.getAlertCurrent(ctx, alert.BKTenantID, alert.AlertID)
		if readErr == nil && current.Alert.EventSourceID == alert.EventSourceID && current.Alert.Fingerprint == alert.Fingerprint && current.Alert.TriggerEventID == alert.TriggerEventID {
			return store.CreateAlertResult{StoredAlert: current, Created: false}, nil
		}
	}
	if err == nil {
		p.observeActiveAlert(ctx, result.Alert)
		err = p.observeDependencyIndex(ctx, result.Alert)
	}
	return result, err
}

func (p *Processor) compareAndSetAlert(
	ctx context.Context,
	current store.StoredAlert,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	defer p.measureStep(phaseApply, "alert_cas")()
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "member.cas", time.Since(started)) }()
	var updated store.StoredAlert
	var err error
	if repository, ok := p.repository.(store.LifecycleAlertSnapshotStore); ok {
		updated, err = repository.CompareAndSetAlertFromSnapshot(ctx, current, replacement)
	} else {
		replacement, err = domain.PrepareAlertReplacement(current.Alert, replacement)
		if err != nil {
			return store.StoredAlert{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
		}
		updated, err = p.compareAndSetAlertWithoutSnapshot(ctx, current, replacement)
	}
	if err == nil {
		if current.Alert.Status != updated.Alert.Status {
			p.observeActiveAlert(ctx, updated.Alert)
		}
		err = p.observeDependencyIndex(ctx, updated.Alert)
	}
	if !errors.Is(err, store.ErrVersionConflict) {
		return updated, err
	}
	if repairErr := p.repairRecentAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID); repairErr != nil {
		return store.StoredAlert{}, errors.Join(err, repairErr)
	}
	return store.StoredAlert{}, err
}

// withFrozenSeverity 把等级映射及其已有摘要一起固定给策略检查和主索引观察。
func (p *Processor) withFrozenSeverity(table SeverityTable, digest string) *Processor {
	local := *p
	local.severity = table
	local.configDigest = digest
	if configured, ok := p.shielder.(interface{ WithConfigDigest(string) ShieldEvaluator }); ok {
		local.shielder = configured.WithConfigDigest(digest)
	}
	if configured, ok := p.dependency.(interface {
		WithDependencyConfigDigest(string) DependencyRegistry
	}); ok {
		local.dependency = configured.WithDependencyConfigDigest(digest)
	}
	return &local
}

func (p *Processor) observeDependencyIndex(ctx context.Context, alert domain.Alert) error {
	registry, level := p.dependencyObservation()
	observer, ok := registry.(interface {
		ObserveDependencyAlert(context.Context, domain.Alert, func(string) (string, error)) error
	})
	if !ok {
		return nil
	}
	if err := observer.ObserveDependencyAlert(ctx, alert, level); err != nil {
		return processingerror.RetryableError{Err: fmt.Errorf("update dependency candidate index: %w", err)}
	}
	return nil
}

func (p *Processor) dependencyObservation() (DependencyRegistry, func(string) (string, error)) {
	// 生命周期业务使用冻结配置；已保存 Alert 的共享资格跟随当前配置，避免晚 CAS 写回退役 namespace。
	registry := p.dependency
	severity := p.dependencySeverity
	if severity == nil {
		severity = p.severity
	}
	if provider, ok := severity.(interface {
		FreezeSeverity() (SeverityTable, string)
	}); ok {
		table, digest := provider.FreezeSeverity()
		severity = table
		if configured, ok := registry.(interface {
			WithDependencyConfigDigest(string) DependencyRegistry
		}); ok {
			registry = configured.WithDependencyConfigDigest(digest)
		}
	}
	var level func(string) (string, error)
	if mapper, ok := severity.(interface{ KACLevel(string) (string, error) }); ok {
		level = mapper.KACLevel
	}
	return registry, level
}

func (p *Processor) observeTerminalDependencyAlerts(ctx context.Context, alerts []domain.Alert) error {
	registry, _ := p.dependencyObservation()
	if batch, ok := registry.(interface {
		ObserveTerminalDependencyAlerts(context.Context, []domain.Alert) error
	}); ok {
		if err := batch.ObserveTerminalDependencyAlerts(ctx, alerts); err != nil {
			return processingerror.RetryableError{Err: fmt.Errorf("update dependency candidate index: %w", err)}
		}
		return nil
	}
	for _, a := range alerts {
		if err := p.observeDependencyIndex(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) compareAndSetAlertWithoutSnapshot(ctx context.Context, current store.StoredAlert, replacement domain.Alert) (store.StoredAlert, error) {
	if repository, ok := p.repository.(store.LifecycleAlertStore); ok {
		return repository.CompareAndSetAlertAfterActiveLookup(
			ctx,
			current.Alert.BKTenantID,
			current.Alert.AlertID,
			current.Version,
			replacement,
		)
	}
	return p.repository.CompareAndSetAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID, current.Version, replacement)
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
	current, err := p.readAlertCurrent(ctx, bkTenantID, alertID)
	if err == nil {
		err = p.observeDependencyIndex(ctx, current.Alert)
	}
	return current, err
}

// readAlertCurrent 只读业务事实；准备终结告警时不能先重算旧活动状态的依赖资格。
func (p *Processor) readAlertCurrent(ctx context.Context, bkTenantID, alertID string) (store.StoredAlert, error) {
	defer p.measureStep(phaseApply, "alert_read")()
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "member.read", time.Since(started)) }()
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
	prepared := false
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		stored, err := p.readAlertCurrent(ctx, command.BKTenantID, command.AlertID)
		if err != nil {
			return CloseAlertResult{}, err
		}
		update, already, err := p.prepareAlertClose(ctx, stored, command, &prepared)
		if err != nil {
			return CloseAlertResult{}, err
		}
		updated := update.Current
		if !already {
			updated, err = p.compareAndSetAlert(ctx, update.Current, update.Replacement)
			if errors.Is(err, store.ErrVersionConflict) {
				continue
			}
			if err != nil {
				return CloseAlertResult{}, err
			}
		}
		updated, err = p.finishPendingChanges(ctx, updated)
		if err != nil {
			return CloseAlertResult{}, err
		}
		if err := p.cacheAlertClose(ctx, updated, already); err != nil {
			return CloseAlertResult{}, err
		}
		logs, err := p.alertCloseOutput(ctx, command, updated)
		if err != nil {
			return CloseAlertResult{}, err
		}
		if err := p.appendAlertLogs(ctx, logs); err != nil {
			return CloseAlertResult{}, err
		}
		return CloseAlertResult{Alert: updated.Alert.Clone(), AlreadyClosed: already}, nil
	}
	return CloseAlertResult{}, fmt.Errorf("close alert after %d CAS attempts: %w", maxCASAttempts, store.ErrVersionConflict)
}

func (p *Processor) repairClosedAlertCache(ctx context.Context, stored store.StoredAlert) error {
	key := store.ActiveAlertKey{BKTenantID: stored.Alert.BKTenantID, EventSourceID: stored.Alert.EventSourceID, Fingerprint: stored.Alert.Fingerprint}
	cached, found, err := p.recentAlerts.GetCurrent(ctx, key)
	if err != nil {
		return err
	}
	if found && cached.Alert.AlertID != stored.Alert.AlertID && !cached.Alert.CreateAt.Before(stored.Alert.CreateAt) {
		return nil
	}
	if !found {
		active, err := p.repository.FindActiveAlert(ctx, key)
		if err == nil && active.Alert.AlertID != stored.Alert.AlertID {
			return p.recentAlerts.PutCurrent(ctx, active)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return p.recentAlerts.PutCurrent(ctx, stored)
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
