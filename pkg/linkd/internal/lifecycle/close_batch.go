// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func (p *Processor) prepareAlertClose(ctx context.Context, current store.StoredAlert, command CloseAlertCommand, prepared *bool) (store.AlertSnapshotUpdate, bool, error) {
	command.EffectiveAt = command.EffectiveAt.Round(0).UTC()
	current, err := p.finishPendingChanges(ctx, current)
	update := store.AlertSnapshotUpdate{Current: current}
	if err != nil {
		return update, false, err
	}
	operation := command.endOperation()
	endType := domain.AlertEndTypeUser
	if command.OperatorKind == domain.OperatorKindSystem {
		endType = domain.AlertEndTypeSystem
	}
	if current.Alert.Status.Terminal() {
		a := current.Alert
		if a.Status != domain.AlertStatusClosed || a.EndType != endType || a.EndReason != command.Reason || a.EndAt == nil || !a.EndAt.Equal(command.EffectiveAt) || a.EndOperation == nil || *a.EndOperation != operation {
			return update, false, fmt.Errorf("%w: alert is already terminal", store.ErrInvalidTransition)
		}
		return update, true, nil
	}
	// 外部前置调用先于 CAS；同一次批次内的版本冲突重读不重复通知 alarmd。
	if !*prepared && p.closePreparation != nil {
		started := time.Now()
		err := p.closePreparation(ctx, current.Alert.Clone(), operation)
		store.ObserveBatchPhase(ctx, "member.close_preparation", time.Since(started))
		if err != nil {
			return update, false, fmt.Errorf("prepare alert close: %w", err)
		}
		*prepared = true
	}
	next := current.Alert.Clone()
	next.Status = domain.AlertStatusClosed
	next.Shield = domain.AlertShield{}
	next.Merge = next.Merge.EndWaiting()
	next.UpdateAt = nextAlertUpdateTime(command.EffectiveAt, current.Alert.UpdateAt)
	next.PolicyChange = terminalShieldChange(current.Alert, next.UpdateAt, command.OperationID)
	endAt := command.EffectiveAt
	next.EndAt, next.EndType, next.EndReason, next.EndOperation = &endAt, endType, command.Reason, &operation
	update.Replacement = next
	return update, false, nil
}

func (p *Processor) cacheAlertClose(ctx context.Context, current store.StoredAlert, already bool) error {
	if already {
		// 重试可能遇到同 fingerprint 的新生命周期，沿用既有修复规则保护新缓存。
		if err := p.observeDependencyIndex(ctx, current.Alert); err != nil {
			return err
		}
		return p.repairClosedAlertCache(ctx, current)
	}
	return p.recentAlerts.PutCurrent(ctx, current)
}

func (p *Processor) alertCloseOutput(ctx context.Context, command CloseAlertCommand, current store.StoredAlert) ([]domain.AlertLog, error) {
	started := time.Now()
	err := p.clearSuppression(ctx, current.Alert)
	store.ObserveBatchPhase(ctx, "member.cleanup", time.Since(started))
	if err != nil {
		return nil, err
	}
	log, err := operationCloseLog(command, current.Alert)
	if err != nil {
		return nil, err
	}
	causeType := AlertChangeCauseUserOperation
	if command.OperatorKind == domain.OperatorKindSystem {
		causeType = AlertChangeCauseSystemOperation
	}
	started = time.Now()
	hooks, err := p.runFinalHooks(ctx, AlertChangeCause{Type: causeType, ID: command.OperationID}, current.Alert, OutcomeAlertClosed)
	store.ObserveBatchPhase(ctx, "member.hooks", time.Since(started))
	return append([]domain.AlertLog{log}, hooks...), err
}

// CloseAlerts 在调用方已经持有全部 fingerprint lease 时显式批读、Bulk CAS、pipeline 缓存和批写流水。
// 同一批必须属于同一租户及同一来源发布配置；不创建跨告警事务，结果保持输入顺序。
// 只有需要外部前置确认、清理或 Hook 的步骤按 concurrency 有界执行，不逐条调用 CloseAlert。
func (p *Processor) CloseAlerts(ctx context.Context, commands []CloseAlertCommand, concurrency int) []CloseAlertOutcome {
	results := make([]CloseAlertOutcome, len(commands))
	reader := p.repository.(store.CurrentAlertBatchStore)
	read := reader.GetAlertsCurrent
	// 全局 KAC 插件使用轻量通知；遗留通用 kafka 声明不装配实例。
	// 只有仍显式配置旧 KAC Kafka Hook 的来源需要读取完整快照。
	if len(p.finalHooks) == 0 {
		if states, ok := p.repository.(store.AlertCloseStateBatchStore); ok {
			read = states.GetAlertCloseStates
		}
	}
	writer := p.repository.(store.AlertSnapshotBatchStore)
	write := writer.CompareAndSetAlertsFromSnapshots
	if closer, ok := p.repository.(store.AlertCloseBatchStore); ok {
		write = closer.CloseAlertsFromSnapshots
	}
	commands = append([]CloseAlertCommand(nil), commands...)
	for i := range commands {
		commands[i].EffectiveAt = commands[i].EffectiveAt.Round(0).UTC()
	}
	prepared := make([]bool, len(commands))
	pending := make([]int, len(commands))
	for i := range commands {
		pending[i] = i
	}
	for attempt := 0; len(pending) > 0 && attempt < maxCASAttempts; attempt++ {
		ids := make([]string, len(pending))
		for j, i := range pending {
			ids[j] = commands[i].AlertID
		}
		started := time.Now()
		current, err := read(ctx, commands[0].BKTenantID, ids)
		store.ObserveBatchPhase(ctx, "batch.locked_read", time.Since(started))
		if err != nil {
			for _, i := range pending {
				results[i].Err = err
			}
			break
		}
		byID := map[string]store.StoredAlert{}
		for _, a := range current.Alerts {
			byID[a.Alert.AlertID] = a
		}
		updates := make([]store.AlertSnapshotUpdate, len(pending))
		started = time.Now()
		closeBatchWork(len(pending), concurrency, func(j int) {
			i := pending[j]
			a, found := byID[commands[i].AlertID]
			if !found {
				results[i].Err = store.ErrNotFound
				return
			}
			var already bool
			updates[j], already, results[i].Err = p.prepareAlertClose(ctx, a, commands[i], &prepared[i])
			results[i].AlreadyClosed = already
			results[i].Alert = updates[j].Current.Alert
		})
		store.ObserveBatchPhase(ctx, "batch.prepare", time.Since(started))
		writes, positions := []store.AlertSnapshotUpdate{}, []int{}
		for j, i := range pending {
			if results[i].Err == nil && !results[i].AlreadyClosed {
				writes, positions = append(writes, updates[j]), append(positions, i)
			}
		}
		retry := []int{}
		if len(writes) > 0 {
			started = time.Now()
			saved, err := write(ctx, writes)
			store.ObserveBatchPhase(ctx, "batch.cas", time.Since(started))
			for j, i := range positions {
				results[i].Err = err
				if err == nil {
					results[i].Err = saved[j].Err
					if saved[j].Err == nil {
						results[i].Alert = saved[j].Alert
						byID[commands[i].AlertID] = saved[j].StoredAlert
					}
				}
				if errors.Is(results[i].Err, store.ErrVersionConflict) {
					retry = append(retry, i)
				}
			}
		}
		// CAS 成功后必须先发布近期缓存，缓存失败的项不能继续 Hook 或流水。
		fresh, cachePositions := []store.StoredAlert{}, []int{}
		started = time.Now()
		closeBatchWork(len(pending), concurrency, func(j int) {
			i := pending[j]
			if results[i].Err != nil {
				return
			}
			a := byID[commands[i].AlertID]
			if !results[i].AlreadyClosed {
				p.observeActiveAlert(ctx, a.Alert)
				results[i].Err = p.observeDependencyIndex(ctx, a.Alert)
			}
			if results[i].Err == nil {
				// 原有屏蔽/合并意图仍按其领域规则补齐，再输出最终关闭。
				a, results[i].Err = p.finishPendingChanges(ctx, a)
				updates[j].Current = a
				results[i].Alert = a.Alert
			}
			if results[i].Err == nil && results[i].AlreadyClosed {
				results[i].Err = p.cacheAlertClose(ctx, a, true)
			}
		})
		for j, i := range pending {
			if results[i].Err == nil && !results[i].AlreadyClosed {
				fresh, cachePositions = append(fresh, updates[j].Current), append(cachePositions, i)
			}
		}
		if len(fresh) > 0 {
			if cache, ok := p.recentAlerts.(interface {
				PutCurrentSnapshots(context.Context, []store.StoredAlert) []error
			}); ok {
				errs := cache.PutCurrentSnapshots(ctx, fresh)
				for j, i := range cachePositions {
					results[i].Err = errs[j]
				}
			} else {
				closeBatchWork(len(fresh), concurrency, func(j int) { results[cachePositions[j]].Err = p.recentAlerts.PutCurrent(ctx, fresh[j]) })
			}
		}
		store.ObserveBatchPhase(ctx, "batch.cache", time.Since(started))
		logs := make([][]domain.AlertLog, len(pending))
		started = time.Now()
		closeBatchWork(len(pending), concurrency, func(j int) {
			i := pending[j]
			if results[i].Err == nil {
				logs[j], results[i].Err = p.alertCloseOutput(ctx, commands[i], updates[j].Current)
			}
		})
		store.ObserveBatchPhase(ctx, "batch.output", time.Since(started))
		allLogs, owners := []domain.AlertLog{}, []int{}
		for j, i := range pending {
			if results[i].Err == nil {
				for _, log := range logs[j] {
					allLogs, owners = append(allLogs, log), append(owners, i)
				}
			}
		}
		started = time.Now()
		for start := 0; start < len(allLogs); start += store.MaxBatchSize {
			end := min(start+store.MaxBatchSize, len(allLogs))
			items, err := p.repository.AppendAlertLogs(ctx, allLogs[start:end])
			for j := start; j < end; j++ {
				itemErr := err
				if err == nil {
					itemErr = items[j-start].Err
				}
				results[owners[j]].Err = errors.Join(results[owners[j]].Err, itemErr)
			}
		}
		store.ObserveBatchPhase(ctx, "batch.logs", time.Since(started))
		pending = retry
	}
	closeBatchWork(len(pending), concurrency, func(j int) {
		i := pending[j]
		if errors.Is(results[i].Err, store.ErrVersionConflict) {
			results[i].Err = errors.Join(results[i].Err, p.repairRecentAlert(ctx, commands[i].BKTenantID, commands[i].AlertID))
		}
	})
	return results
}

// 每个阶段结束后才读写下一阶段的结果；不同 worker 只修改各自索引。
func closeBatchWork(count, concurrency int, run func(int)) {
	jobs := make(chan int, count)
	for i := range count {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(count, max(1, concurrency)) {
		workers.Go(func() {
			for i := range jobs {
				run(i)
			}
		})
	}
	workers.Wait()
}
