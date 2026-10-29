// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"sync"
	"time"

	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
)

type closeBatchLeasesHeldKey struct{}

type closeBatchLocker interface {
	AcquireBatch(context.Context, []string) ([]scheduler.Lease, []error)
	ReleaseBatch(context.Context, []scheduler.Lease) []error
}

type closeBatchProjection interface {
	ProjectBatchNow(context.Context, string, []string) []error
}

// CloseAlerts 使用独立预算显式执行关闭批次，整个 HTTP 请求受二十秒截止约束。
// 存储和投影必须提供批量能力，不退回逐条 CloseAlert。结果按输入顺序返回。
func (s *AlertCloser) CloseAlerts(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	results := make([]lifecycle.CloseAlertOutcome, len(commands))
	fail := func(err error) []lifecycle.CloseAlertOutcome {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	if len(commands) == 0 {
		return results
	}
	if ctx == nil || len(commands) > store.MaxCloseBatchSize {
		return fail(&CloseError{Status: 400, Code: "invalid_command", Message: "invalid close batch"})
	}
	for _, c := range commands {
		if c.Validate() != nil || c.BKTenantID != commands[0].BKTenantID {
			return fail(&CloseError{Status: 400, Code: "invalid_command", Message: "invalid close batch"})
		}
	}
	if ctx.Err() != nil {
		return fail(&CloseError{Status: 504, Code: "not_attempted", Message: "request ended before this alert was attempted; retry the same operation", cause: ctx.Err()})
	}
	if s.resources == nil {
		return fail(&CloseError{Status: 503, Code: "unavailable", Message: "batch close resources are not configured"})
	}
	if s.projection != nil {
		if _, ok := s.projection.(closeBatchProjection); !ok {
			return fail(&CloseError{Status: 503, Code: "unavailable", Message: "batch close projection is not configured"})
		}
	}
	select {
	case s.batchSlots <- struct{}{}:
		defer func() { <-s.batchSlots }()
	default:
		return fail(&CloseError{Status: 429, Code: "busy", Message: "close capacity exceeded"})
	}
	ctx, cancel := context.WithTimeout(s.resources.context(ctx), 20*time.Second)
	defer cancel()
	ctx = store.WithBatchPhaseObserver(ctx, s.metrics.ObserveClosePhase)
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "batch.total", time.Since(started)) }()
	// 冷启动按此前千条实测约七秒留整估计；后续采用本请求已完成批次的平均墙钟。
	estimate, spent, completed := 8*time.Second, time.Duration(0), 0
	for start := 0; start < len(commands); start += max(1, s.batchSize) {
		end := min(start+max(1, s.batchSize), len(commands))
		if !closeBatchFitsBudget(ctx, estimate) {
			for i := start; i < len(commands); i++ {
				results[i].Err = &CloseError{Status: 504, Code: "not_attempted", Message: "remaining request budget is below estimated batch duration; retry the same operation", cause: ctx.Err()}
			}
			break
		}
		waveStarted := time.Now()
		copy(results[start:end], s.closeAlertsWave(ctx, commands[start:end]))
		spent += time.Since(waveStarted)
		completed++
		estimate = spent / time.Duration(completed)
	}
	return results
}

func (s *AlertCloser) closeAlertsWave(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	notifications := &closeBatchNotifications{}
	ctx = context.WithValue(ctx, closeBatchNotificationsKey{}, notifications)
	defer notifications.flush(ctx, s.logger)
	results := s.closeBatch(ctx, commands)
	ids, positions := []string{}, []int{}
	for i := range results {
		if results[i].Err != nil {
			results[i].Err = s.closeError(ctx, commands[i], results[i].Err)
		} else {
			ids, positions = append(ids, commands[i].AlertID), append(positions, i)
		}
	}
	if len(ids) > 0 && s.projection != nil {
		started := time.Now()
		errs := s.projection.(closeBatchProjection).ProjectBatchNow(ctx, commands[0].BKTenantID, ids)
		store.ObserveBatchPhase(ctx, "batch.projection", time.Since(started))
		for j, i := range positions {
			if errs[j] != nil {
				if s.logger != nil {
					s.logger.WarnContext(ctx, "synchronous close projection failed", "module", "lifecycle", "operation", "close_projection", "error", errs[j], "bk_tenant_id", commands[i].BKTenantID, "alert_id", commands[i].AlertID, "operation_id", commands[i].OperationID)
				}
				results[i].Err = &CloseError{Status: 502, Code: "result_uncertain", Message: "alert closed but KAC write failed; retry the same operation", cause: errs[j]}
			}
		}
	}
	return results
}

type closeLeaseGroup struct {
	identity  store.StoredAlert
	positions []int
}

func (s *AlertCloser) closeBatch(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	results := make([]lifecycle.CloseAlertOutcome, len(commands))
	repo := s.resources.repository.Repository
	reader, ok := repo.(store.CurrentAlertBatchStore)
	_, writable := repo.(store.AlertSnapshotBatchStore)
	if !ok || !writable {
		for i := range results {
			results[i].Err = &CloseError{Status: 503, Code: "unavailable", Message: "batch close storage is not configured"}
		}
		return results
	}
	ids := make([]string, len(commands))
	for i := range commands {
		ids[i] = commands[i].AlertID
	}
	started := time.Now()
	readIdentities := reader.GetAlertsCurrent
	if narrow, ok := repo.(store.AlertIdentityBatchStore); ok {
		readIdentities = narrow.GetAlertIdentities
	}
	identities, err := readIdentities(ctx, commands[0].BKTenantID, ids)
	store.ObserveBatchPhase(ctx, "batch.identity_read", time.Since(started))
	if err != nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	byID := map[string]store.StoredAlert{}
	records := map[string]*controlSourceResult{}
	for _, a := range identities.Alerts {
		byID[a.Alert.AlertID] = a
		records[a.Alert.EventSourceID] = &controlSourceResult{}
	}
	started = time.Now()
	for id, record := range records {
		record.record, record.err = s.sources.Get(ctx, id)
	}
	// 首批 Hook 尚未缓存时，也只读取一次每种发布配置，避免成员并发穿透来源存储。
	releases := map[controlHookKey]controlReleaseResult{}
	for _, a := range identities.Alerts {
		record := records[a.Alert.EventSourceID]
		if record.err != nil || record.record.Published <= 0 {
			continue
		}
		version := record.record.Published
		if record.record.Deleted {
			version = a.Alert.EventSourceVersion
		}
		key := controlHookKey{a.Alert.EventSourceID, version}
		if _, found := releases[key]; !found {
			release, err := s.resources.sourceRelease(ctx, s.sources, key.source, key.version)
			releases[key] = controlReleaseResult{release, err}
		}
	}
	ctx = context.WithValue(ctx, controlSourceBatchKey{}, records)
	ctx = context.WithValue(ctx, controlReleaseBatchKey{}, releases)
	store.ObserveBatchPhase(ctx, "batch.source_read", time.Since(started))
	// 同 fingerprint 可能有多个等级的 Alert，只取得一次 lease，避免本批互相报锁忙。
	groups := map[string]*closeLeaseGroup{}
	for i, c := range commands {
		a, found := byID[c.AlertID]
		if !found {
			results[i].Err = store.ErrNotFound
			continue
		}
		key := mailbox.CorrelationKey(a.Alert.BKTenantID, a.Alert.EventSourceID, a.Alert.Fingerprint)
		if groups[key] == nil {
			groups[key] = &closeLeaseGroup{identity: a}
		}
		groups[key].positions = append(groups[key].positions, i)
	}
	// 每个来源一个 Redis pipeline；同 fingerprint 的多个 Alert 共用本批租约。
	type heldBatch struct {
		locker   closeBatchLocker
		leases   []scheduler.Lease
		groups   []*closeLeaseGroup
		acquired []error
	}
	held := []heldBatch{}
	bySource := map[string][]*closeLeaseGroup{}
	for _, g := range groups {
		bySource[g.identity.Alert.EventSourceID] = append(bySource[g.identity.Alert.EventSourceID], g)
	}
	started = time.Now()
	defer func() {
		at := time.Now()
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		var release sync.WaitGroup
		for _, b := range held {
			release.Go(func() {
				errs := b.locker.ReleaseBatch(releaseCtx, b.leases)
				for j, g := range b.groups {
					if b.acquired[j] == nil && errs[j] != nil {
						for _, i := range g.positions {
							results[i].Err = errors.Join(results[i].Err, errs[j])
						}
					}
				}
			})
		}
		release.Wait()
		store.ObserveBatchPhase(ctx, "batch.release", time.Since(at))
	}()
	ready := map[controlHookKey]*closeLeaseGroup{}
	for source, list := range bySource {
		locker, err := s.batchLocker(source)
		if err != nil {
			for _, g := range list {
				for _, i := range g.positions {
					results[i].Err = err
				}
			}
			continue
		}
		keys := make([]string, len(list))
		for j, g := range list {
			a := g.identity.Alert
			keys[j] = mailbox.CorrelationKey(a.BKTenantID, a.EventSourceID, a.Fingerprint)
		}
		leases, errs := locker.AcquireBatch(ctx, keys)
		held = append(held, heldBatch{locker, leases, list, errs})
		for j, g := range list {
			if errs[j] != nil {
				for _, i := range g.positions {
					results[i].Err = errs[j]
				}
				continue
			}
			a := g.identity.Alert
			version := a.EventSourceVersion
			if record := records[a.EventSourceID]; !record.record.Deleted {
				version = record.record.Published
			}
			key := controlHookKey{a.EventSourceID, version}
			if ready[key] == nil {
				ready[key] = &closeLeaseGroup{identity: g.identity}
			}
			ready[key].positions = append(ready[key].positions, g.positions...)
		}
	}
	store.ObserveBatchPhase(ctx, "batch.leases", time.Since(started))
	ctx = context.WithValue(ctx, closeBatchLeasesHeldKey{}, true)
	var batches sync.WaitGroup
	slots := make(chan struct{}, max(1, s.concurrency))
	for _, source := range ready {
		slots <- struct{}{}
		batches.Go(func() {
			defer func() { <-slots }()
			err := s.withProcessor(ctx, source.identity, func(p *lifecycle.Processor) error {
				batch := make([]lifecycle.CloseAlertCommand, len(source.positions))
				for j, i := range source.positions {
					batch[j] = commands[i]
				}
				outcomes := p.CloseAlerts(ctx, batch, max(1, s.concurrency))
				for j, i := range source.positions {
					results[i] = outcomes[j]
				}
				return nil
			})
			if err != nil {
				for _, i := range source.positions {
					results[i].Err = errors.Join(results[i].Err, err)
				}
			}
		})
	}
	batches.Wait()
	return results
}

// closeBatchFitsBudget 在取锁和发送读写前检查预估预算；估计不承诺消除外部存储抖动。
func closeBatchFitsBudget(ctx context.Context, estimate time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > estimate
}
