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
	"linkd/internal/store"
)

type closeBatchProjection interface {
	ProjectBatchNow(context.Context, string, []string) []error
}

// CloseAlerts 使用独立预算显式执行关闭批次，整个 HTTP 请求仍受十秒截止约束。
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
	ctx, cancel := context.WithTimeout(s.resources.context(ctx), 10*time.Second)
	defer cancel()
	ctx = store.WithBatchPhaseObserver(ctx, s.metrics.ObserveClosePhase)
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "batch.total", time.Since(started)) }()
	for start := 0; start < len(commands); start += max(1, s.batchSize) {
		end := min(start+max(1, s.batchSize), len(commands))
		if ctx.Err() != nil {
			for i := start; i < len(commands); i++ {
				results[i].Err = &CloseError{Status: 504, Code: "not_attempted", Message: "request ended before this alert was attempted; retry the same operation", cause: ctx.Err()}
			}
			break
		}
		copy(results[start:end], s.closeAlertsWave(ctx, commands[start:end]))
	}
	return results
}

func (s *AlertCloser) closeAlertsWave(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
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
	processor *lifecycle.Processor
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
	ready := make(chan *closeLeaseGroup, len(groups))
	finished := make(chan struct{})
	var leases sync.WaitGroup
	work := make(chan struct{}, max(1, s.concurrency))
	started = time.Now()
	for _, group := range groups {
		leases.Go(func() {
			select {
			case work <- struct{}{}:
			case <-ctx.Done():
				for _, i := range group.positions {
					results[i].Err = ctx.Err()
				}
				ready <- group
				return
			}
			reported := false
			err := s.withProcessor(ctx, group.identity, func(p *lifecycle.Processor) error {
				group.processor = p
				reported = true
				<-work
				ready <- group
				<-finished
				return nil
			})
			if err != nil {
				for _, i := range group.positions {
					results[i].Err = errors.Join(results[i].Err, err)
				}
			}
			if !reported {
				<-work
				ready <- group
			}
		})
	}
	// 后续阶段与 lease 释放通过 finished 同步；释放失败也必须反映到对应成员。
	defer func() {
		started := time.Now()
		close(finished)
		leases.Wait()
		store.ObserveBatchPhase(ctx, "batch.release", time.Since(started))
	}()
	bySource := map[controlHookKey]*closeLeaseGroup{}
	for range len(groups) {
		g := <-ready
		if g.processor == nil {
			continue
		}
		a := g.identity.Alert
		version := a.EventSourceVersion
		if record := records[a.EventSourceID]; !record.record.Deleted {
			version = record.record.Published
		}
		key := controlHookKey{a.EventSourceID, version}
		if bySource[key] == nil {
			bySource[key] = &closeLeaseGroup{processor: g.processor}
		}
		bySource[key].positions = append(bySource[key].positions, g.positions...)
	}
	store.ObserveBatchPhase(ctx, "batch.leases", time.Since(started))
	var batches sync.WaitGroup
	for _, source := range bySource {
		batches.Go(func() {
			batch := make([]lifecycle.CloseAlertCommand, len(source.positions))
			for j, i := range source.positions {
				batch[j] = commands[i]
			}
			outcomes := source.processor.CloseAlerts(ctx, batch, max(1, s.concurrency))
			for j, i := range source.positions {
				results[i] = outcomes[j]
			}
		})
	}
	batches.Wait()
	return results
}
