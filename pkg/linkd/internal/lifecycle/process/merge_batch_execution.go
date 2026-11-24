// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

// LinkMergeMembersPrepared 由合并执行器显式提交成员块，不跨调用收集请求。
func (r *MergeOperator) LinkMergeMembersPrepared(ctx context.Context, tenant string, ids []string, relation domain.MergeRelation) []store.AlertUpdateResult {
	return r.membersBatch(ctx, tenant, ids, relation, store.StoredAlert{}, false)
}

// UnlinkMergeMembersPrepared 在同一批中复用终态父，保留逐成员真实状态与版本。
func (r *MergeOperator) UnlinkMergeMembersPrepared(ctx context.Context, tenant string, ids []string, relation domain.MergeRelation, parent store.StoredAlert) []store.AlertUpdateResult {
	return r.membersBatch(ctx, tenant, ids, relation, parent, true)
}

func (r *MergeOperator) membersBatch(ctx context.Context, tenant string, ids []string, relation domain.MergeRelation, parent store.StoredAlert, unlink bool) []store.AlertUpdateResult {
	results := make([]store.AlertUpdateResult, len(ids))
	if r.resources == nil {
		// 未装配生产仓储的测试替身保留单成员接口。
		jobs := make(chan int, len(ids))
		for i := range ids {
			jobs <- i
		}
		close(jobs)
		var wg sync.WaitGroup
		for range min(cap(r.slots), len(ids)) {
			wg.Go(func() {
				for i := range jobs {
					if unlink {
						results[i].StoredAlert, results[i].Err = r.UnlinkMergeMemberPrepared(ctx, tenant, ids[i], relation, parent)
					} else {
						results[i].StoredAlert, results[i].Err = r.LinkMergeMemberPrepared(ctx, tenant, ids[i], relation)
					}
				}
			})
		}
		wg.Wait()
		return results
	}
	for start := 0; start < len(ids); {
		if err := ctx.Err(); err != nil {
			for i := start; i < len(ids); i++ {
				results[i].Err = err
			}
			break
		}
		n := min(max(1, r.memberBatchSize), store.MaxBatchSize-1, len(ids)-start)
		call, cancel := context.WithTimeout(r.resources.context(ctx), 10*time.Second)
		r.memberWave(call, tenant, ids[start:start+n], relation.MemberContext(), parent, unlink, results[start:start+n])
		cancel()
		start += n
	}

	return results
}

type mergeBatchMember struct {
	index     int
	processor *lifecycle.Processor
}

func (r *MergeOperator) memberWave(ctx context.Context, tenant string, ids []string, relation domain.MergeMemberContext, parent store.StoredAlert, unlink bool, results []store.AlertUpdateResult) {
	ctx = store.WithBatchPhaseObserver(ctx, r.metrics.ObserveMergeBatchPhase)
	started := time.Now()
	phaseAt := started
	phases := map[string]float64{}
	phase := func(name string) {
		now := time.Now()
		store.ObserveBatchPhase(ctx, "batch."+name, now.Sub(phaseAt))
		phases[name] = now.Sub(phaseAt).Seconds()
		phaseAt = now
	}
	defer func() {
		store.ObserveBatchPhase(ctx, "batch.total", time.Since(started))
		r.resources.observePoolWait(ctx, r.metrics)
		if r.logger != nil {
			kind := "link"
			if unlink {
				kind = "unlink"
			}
			r.logger.DebugContext(ctx, "merge member batch completed", "kind", kind, "members", len(ids), "started", started.UTC(), "elapsed_seconds", time.Since(started).Seconds(), "phases", phases)
		}
	}()
	repo := r.metrics.ObserveRepository(r.resources.repository.Repository)
	reader := repo.(store.CurrentAlertBatchStore)
	writer := repo.(store.AlertSnapshotBatchStore)
	fail := func(err error) {
		for i := range results {
			results[i].Err = err
		}
	}
	// 1. 锁前批读只用于不可变来源/fingerprint 身份；锁内再批读实际业务状态。
	identities, err := reader.GetAlertsCurrent(ctx, tenant, ids)
	if err != nil {
		fail(err)
		return
	}
	phase("identity_read")
	byID := map[string]store.StoredAlert{}
	for _, a := range identities.Alerts {
		byID[a.Alert.AlertID] = a
	}
	// 一个批次只读取一次每种来源的发布记录，成员之间不重复访问配置仓储。
	if r.sources != nil {
		records := map[string]*controlSourceResult{}
		var reads sync.WaitGroup
		for _, a := range identities.Alerts {
			id := a.Alert.EventSourceID
			if _, exists := records[id]; exists {
				continue
			}
			item := &controlSourceResult{}
			records[id] = item
			reads.Go(func() {
				_, item.err = r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
					var err error
					started := time.Now()
					item.record, err = r.sources.Get(call, id)
					store.ObserveBatchPhase(ctx, "member.source_query", time.Since(started))
					return store.StoredAlert{}, err
				})
			})
		}
		reads.Wait()
		ctx = context.WithValue(ctx, controlSourceBatchKey{}, records)
	}
	phase("source_read")
	ready := make(chan mergeBatchMember, len(ids))
	finished := make(chan struct{})
	var leases sync.WaitGroup
	count := 0
	for i, id := range ids {
		identity, ok := byID[id]
		if !ok {
			results[i].Err = store.ErrNotFound
			continue
		}
		count++
		leases.Go(func() {
			// 租约准备使用全局工作名额；等待本批提交时归还名额，批次不被工作并发截短。
			slotStarted := time.Now()
			select {
			case r.slots <- struct{}{}:
			case <-ctx.Done():
				results[i].Err = ctx.Err()
				ready <- mergeBatchMember{index: i}
				return
			}
			store.ObserveBatchPhase(ctx, "member.slot_wait", time.Since(slotStarted))
			preparedAt := time.Now()
			reported := false
			err := r.withProcessor(ctx, identity, func(p *lifecycle.Processor) error {
				store.ObserveBatchPhase(ctx, "member.processor", time.Since(preparedAt))
				reported = true
				<-r.slots
				ready <- mergeBatchMember{i, p}
				<-finished
				return results[i].Err
			})
			results[i].Err = err
			if !reported {
				<-r.slots
				ready <- mergeBatchMember{index: i}
			}
		})

	}
	defer func() {
		t := time.Now()
		close(finished)
		leases.Wait()
		store.ObserveBatchPhase(ctx, "batch.release", time.Since(t))
		phases["release"] = time.Since(t).Seconds()
	}()
	members := make([]mergeBatchMember, 0, count)
	for range count {
		member := <-ready
		if member.processor != nil {
			members = append(members, member)
		}
	}
	phase("leases")
	if len(members) == 0 {
		return
	}
	// 4. 无新变更的失败重试也需补齐完成缓存；此 defer 先于租约归还执行。
	defer func() {
		t := time.Now()
		r.cacheMergeMemberSnapshots(ctx, members, results)
		duration := time.Since(t)
		store.ObserveBatchPhase(ctx, "batch.cache", duration)
		phases["cache"] = duration.Seconds()
	}()
	readIDs := make([]string, 0, len(members)+1)
	for _, m := range members {
		readIDs = append(readIDs, ids[m.index])
	}
	if !unlink {
		readIDs = append(readIDs, relation.ParentAlertID)
	}
	current, err := reader.GetAlertsCurrent(ctx, tenant, readIDs)
	if err != nil {
		for _, m := range members {
			results[m.index].Err = err
		}
		return
	}
	byID = map[string]store.StoredAlert{}
	for _, a := range current.Alerts {
		byID[a.Alert.AlertID] = a
	}
	if !unlink {
		parent = byID[relation.ParentAlertID]
	}
	phase("locked_read")
	updates := make([]store.AlertSnapshotUpdate, len(members))
	changed := make([]bool, len(members))
	var work sync.WaitGroup
	for j, m := range members {
		work.Go(func() {
			a, ok := byID[ids[m.index]]
			if !ok {
				results[m.index].Err = store.ErrNotFound
				return
			}
			var err error
			_, err = r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
				var prepareErr error
				started := time.Now()
				updates[j], changed[j], prepareErr = m.processor.PrepareMergeMemberBatchChange(call, a, parent, relation, unlink)
				store.ObserveBatchPhase(ctx, "member.prepare_exec", time.Since(started))
				return store.StoredAlert{}, prepareErr
			})
			results[m.index] = store.AlertUpdateResult{StoredAlert: updates[j].Current, Err: err}
		})
	}
	work.Wait()
	phase("prepare")
	selected := []mergeBatchMember{}
	writes := []store.AlertSnapshotUpdate{}
	for j, m := range members {
		if results[m.index].Err == nil && changed[j] {
			selected = append(selected, m)
			writes = append(writes, updates[j])
		}
	}
	if len(writes) == 0 {
		return
	}
	// 2. 显式 Bulk CAS 后，仅成功成员执行输出。失败成员的原状态/意图留待下一轮。
	saved, err := writer.CompareAndSetAlertsFromSnapshots(ctx, writes)
	if err != nil {
		for _, m := range selected {
			results[m.index].Err = err
		}
		return
	}
	phase("cas")
	logs := make([][]domain.AlertLog, len(selected))
	for j, m := range selected {
		results[m.index] = saved[j]
		if errors.Is(saved[j].Err, store.ErrVersionConflict) {
			results[m.index].Err = errors.Join(saved[j].Err, m.processor.RepairMergeAlertCache(ctx, tenant, ids[m.index]))
		}
		if saved[j].Err == nil {
			work.Go(func() {
				_, results[m.index].Err = r.withSlot(ctx, func(call context.Context) (store.StoredAlert, error) {
					var outputErr error
					started := time.Now()
					logs[j], outputErr = m.processor.MergeMemberChangeOutput(call, saved[j].StoredAlert)
					store.ObserveBatchPhase(ctx, "member.output_exec", time.Since(started))
					return store.StoredAlert{}, outputErr
				})
			})
		}
	}
	work.Wait()
	phase("output")
	allLogs := []domain.AlertLog{}
	owners := []int{}
	for j, m := range selected {
		if results[m.index].Err == nil {
			for _, log := range logs[j] {
				allLogs = append(allLogs, log)
				owners = append(owners, m.index)
			}
		}
	}
	for start := 0; start < len(allLogs); start += store.MaxBatchSize {
		end := min(start+store.MaxBatchSize, len(allLogs))
		items, err := repo.AppendAlertLogs(ctx, allLogs[start:end])
		for j := start; j < end; j++ {
			itemErr := err
			if err == nil {
				itemErr = items[j-start].Err
			}
			results[owners[j]].Err = errors.Join(results[owners[j]].Err, itemErr)
		}
	}
	phase("logs")
	// 3. 日志已确认的成员才局部完成；全部步骤结束并释放各自租约后才返回批次结果。
	confirm := []store.StoredAlert{}
	confirmed := []mergeBatchMember{}
	for _, m := range selected {
		if results[m.index].Err == nil {
			confirm = append(confirm, results[m.index].StoredAlert)
			confirmed = append(confirmed, m)
		}
	}
	if len(confirm) == 0 {
		return
	}
	done, err := writer.CompleteAlertChanges(ctx, confirm, store.MergeChangeField)
	phase("complete")
	for j, m := range confirmed {
		if err != nil {
			results[m.index].Err = err
			continue
		}
		results[m.index] = done[j]
		if errors.Is(done[j].Err, store.ErrVersionConflict) {
			results[m.index].Err = errors.Join(done[j].Err, m.processor.RepairMergeAlertCache(ctx, tenant, ids[m.index]))
		}
	}
}

// cacheMergeMemberSnapshots 在租约归还前批写成功成员的缓存；已完成业务的重试也刷新 token。
func (r *MergeOperator) cacheMergeMemberSnapshots(ctx context.Context, members []mergeBatchMember, results []store.AlertUpdateResult) {
	if r.resources.repository.Backend != config.RepositoryTypeElasticsearch {
		return
	}
	type cacheGroup struct {
		processor *lifecycle.Processor
		items     []store.StoredAlert
		positions []int
	}
	groups := map[string]*cacheGroup{}
	for _, m := range members {
		item := results[m.index]
		if item.Err != nil || item.Alert.Status != domain.AlertStatusActive {
			continue
		}
		group := groups[item.Alert.EventSourceID]
		if group == nil {
			group = &cacheGroup{processor: m.processor}
			groups[item.Alert.EventSourceID] = group
		}
		group.items = append(group.items, item.StoredAlert)
		group.positions = append(group.positions, m.index)
	}
	// 与显式存储批一样，批次预算已经限制并发；不再排队到单成员 I/O 名额。
	// 同批的来源顺序发送，Redis 连接池仍限制物理连接数。
	for _, group := range groups {
		started := time.Now()
		failures := group.processor.CacheCompletedMergeChanges(ctx, group.items)
		store.ObserveBatchPhase(ctx, "cache.pipeline", time.Since(started))
		for j, position := range group.positions {
			results[position].Err = failures[j]
		}
	}
}
