// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

// Package window 按业务窗口处理元数据记录，负责路由、合并和提交状态。
package window

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// metadataStore is the processor's persistence boundary. Keeping it narrow also
// lets tests model partial commits and replay without a live database.
type metadataStore interface {
	LoadMetricRoutes(context.Context) (map[uint]storage.MetricRoute, error)
	GroupForDataID(context.Context, uint) (storage.MetricRoute, bool, error)
	Apply(context.Context, int, []core.Record, time.Time) (storage.Result, error)
	Expire(context.Context, time.Time) (int, error)
	LoadAll(context.Context) (map[core.ScopeKey]core.ScopeState, error)
	InvalidateReportValueCache(int)
}

// Processor owns routing, uncommitted records, and committed scope snapshots.
// The Kafka worker and scheduled tasks share it through mu.
type Processor struct {
	// mu serializes the Kafka worker with scheduled maintenance.
	mu             sync.Mutex
	refreshMu      sync.Mutex
	store          metadataStore
	routes         *routeCache
	opts           core.Options
	dataID         string
	pending        PendingRecords
	stateWindow    StateCache
	refreshChanges map[core.ScopeKey]struct{}
	refreshInvalid bool
	now            func() time.Time
}

// NewProcessor connects the metadata window to its MySQL store and shared route snapshot.
func NewProcessor(store *storage.MySQLStore, opts core.Options, dataID uint) *Processor {
	return newProcessorWithRouteCache(store, opts, dataID, moduleRouteCache)
}

func newProcessor(store metadataStore, opts core.Options, dataID uint) *Processor {
	return newProcessorWithRouteCache(store, opts, dataID, newRouteCache())
}

func newProcessorWithRouteCache(store metadataStore, opts core.Options, dataID uint, routes *routeCache) *Processor {
	return &Processor{
		store: store, routes: routes, opts: opts, dataID: strconv.FormatUint(uint64(dataID), 10),
		stateWindow: StateCache{}, now: time.Now,
	}
}

// Reset starts a new consumer-group session while retaining committed snapshots.
func (p *Processor) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending.Reset()
}

func (p *Processor) purgeMetricLocked(groupID uint, fields []string) {
	if p.refreshChanges != nil {
		p.refreshInvalid = true
	}
	deleted := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		deleted[field] = struct{}{}
	}
	kept := make([]core.Record, 0, p.pending.Len())
	for _, record := range p.pending.Ordered() {
		_, remove := deleted[record.FieldName]
		if record.GroupID == int(groupID) && remove {
			continue
		}
		kept = append(kept, record)
	}
	p.pending.Replace(kept)
	for _, field := range fields {
		p.stateWindow.RemoveMetric(int(groupID), field)
	}
	p.store.InvalidateReportValueCache(int(groupID))
	p.updateCacheMetrics()
}

// Add decodes and routes each record by its DataID, then aggregates accepted
// records in the active consumer window. The configured consumer DataID only selects the Kafka topic.
func (p *Processor) Add(ctx context.Context, data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	records, dropped, invalidReason, err := core.DecodeMessageWithDiagnostic(data)
	if err != nil {
		metrics.MetricMetadataMessages.WithLabelValues(p.dataID, "invalid").Inc()
		logger.Warnf("metric metadata message rejected dataid=%s bytes=%d reason=%q", p.dataID, len(data), err)
		return 0, nil
	}
	for reason, n := range dropped {
		metrics.MetricMetadataRecords.WithLabelValues(p.dataID, reason).Add(float64(n))
	}
	if len(dropped) > 0 {
		logger.Warnf("metric metadata records dropped dataid=%s decoded=%d dropped=%v first_invalid_reason=%q", p.dataID, len(records)+sum(dropped), dropped, invalidReason)
	}
	if len(records) == 0 {
		return 0, nil
	}
	accepted, err := p.routeRecords(ctx, records)
	if err != nil {
		return 0, err
	}
	if p.pending.Len()+len(accepted) > p.opts.BatchRecords {
		if err := p.flush(ctx); err != nil {
			return 0, err
		}
	}
	staleRecords := p.pending.Append(accepted)
	if staleRecords > 0 {
		metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "stale_timestamp").Add(float64(staleRecords))
	}
	return len(accepted), nil
}

// routeRecords checks cache misses against the database, including misses from a
// complete but potentially older snapshot. Negative lookups are never cached.
func (p *Processor) routeRecords(ctx context.Context, records []core.Record) ([]core.Record, error) {
	accepted := make([]core.Record, 0, len(records))
	type routeResult struct {
		route storage.MetricRoute
		found bool
	}
	lookedUp := make(map[uint]routeResult)
	for _, r := range records {
		route, cached, _, routeGeneration := p.routes.Lookup(r.DataID)
		// 缓存未命中时，先检查本批次是否已查询过该 DataID，避免重复数据库访问。
		if !cached {
			result, foundInBatch := lookedUp[r.DataID]
			if !foundInBatch {
				groupRoute, found, err := p.store.GroupForDataID(ctx, r.DataID)
				if err != nil {
					metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "routes").Inc()
					return nil, fmt.Errorf("load group for dataid %d: %w", r.DataID, err)
				}
				result = routeResult{route: groupRoute, found: found}
				lookedUp[r.DataID] = result
			}
			groupRoute, found := result.route, result.found
			if !found {
				metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "source_disabled").Inc()
				continue
			}
			route = groupRoute
			if !foundInBatch {
				p.routes.Remember(r.DataID, route, routeGeneration)
			}
		}
		if (r.TenantID != "" && route.TenantID != r.TenantID) || (r.BizID != 0 && route.BizID != r.BizID) {
			logger.Debugf("metric metadata route rejected dataid=%s metric=%q group_id=%d reason=tenant_or_biz_mismatch", p.dataID, r.FieldName, route.GroupID)
			metrics.MetricMetadataRecords.WithLabelValues(p.dataID, "source_mismatch").Inc()
			continue
		}
		r.GroupID = int(route.GroupID)
		accepted = append(accepted, r)
	}
	return accepted, nil
}

// Refresh updates the shared route snapshot and reloads this processor's state.
func (p *Processor) Refresh(ctx context.Context) error {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	started := time.Now()
	outcome := "error"
	defer func() {
		metrics.MetricMetadataRefreshDuration.WithLabelValues(p.dataID, outcome).Observe(time.Since(started).Seconds())
	}()

	p.mu.Lock()
	if err := p.flush(ctx); err != nil {
		p.mu.Unlock()
		return err
	}
	// 记录加载期间的本地写入，避免旧的数据库快照覆盖已提交状态。
	p.refreshChanges = make(map[core.ScopeKey]struct{})
	p.refreshInvalid = false
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.refreshChanges = nil
		p.mu.Unlock()
	}()

	if err := p.routes.RefreshIfDue(ctx, p.store, p.opts.StateRefreshInterval); err != nil {
		metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "routes").Inc()
		return fmt.Errorf("refresh metric routes: %w", err)
	}
	loadedAt := p.now()
	states, err := p.store.LoadAll(ctx)
	if err != nil {
		metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "refresh").Inc()
		return fmt.Errorf("refresh metric metadata state: %w", err)
	}
	snapshot := StateCache{scopes: states}
	for key, state := range states {
		state.LoadedAt = loadedAt
		states[key] = state
		snapshot.bytes += state.Size()
		snapshot.members += len(state.Metrics)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.refreshInvalid {
		outcome = "discarded"
		logger.Debugf("metric metadata cache refresh discarded after invalidation dataid=%s", p.dataID)
		return nil
	}
	for key := range p.refreshChanges {
		if state, ok := p.stateWindow.Lookup(key); ok {
			snapshot.Store(state)
		}
	}
	p.stateWindow = snapshot
	outcome = "success"
	p.updateCacheMetrics()
	logger.Debugf("metric metadata cache refreshed dataid=%s routes=%d scopes=%d bytes=%d", p.dataID, p.routes.Len(), p.stateWindow.Len(), p.stateWindow.Bytes())
	return nil
}

// Flush commits all pending groups before the notifier may mark any message.
func (p *Processor) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flush(ctx)
}

// Maintain flushes pending reports before expiring stale scopes.
func (p *Processor) Maintain(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.flush(ctx); err != nil {
		return err
	}
	n, err := p.store.Expire(ctx, p.now().Add(-p.opts.ExpireAfter))
	if err != nil {
		metrics.MetricMetadataStorageErrors.WithLabelValues(p.dataID, "expire").Inc()
		return err
	}
	if n > 0 {
		p.clearCache("expire")
		metrics.MetricMetadataScopes.WithLabelValues(p.dataID, "expired").Add(float64(n))
	}
	if n > 0 {
		logger.Debugf("metric metadata maintenance completed dataid=%s expired_scopes=%d", p.dataID, n)
	}
	p.updateCacheMetrics()
	return nil
}

// flush applies a batch by business group. MySQL commits each bounded group
// chunk independently; records stay pending until every chunk succeeds, so a
// partial commit can be replayed idempotently.
func (p *Processor) flush(ctx context.Context) error {
	return p.flushWithRouteRefresh(ctx, false)
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (p *Processor) remember(st core.ScopeState) {
	if p.refreshChanges != nil {
		p.refreshChanges[st.Key] = struct{}{}
	}
	p.stateWindow.Store(st)
}

func (p *Processor) clearCache(reason string) {
	if p.refreshChanges != nil {
		p.refreshInvalid = true
	}
	if p.stateWindow.Clear() {
		metrics.MetricMetadataEvictions.WithLabelValues(p.dataID, reason).Inc()
	}
	p.updateCacheMetrics()
}

func (p *Processor) updateCacheMetrics() {
	metrics.MetricMetadataCache.WithLabelValues(p.dataID, "scopes").Set(float64(p.stateWindow.Len()))
	metrics.MetricMetadataCache.WithLabelValues(p.dataID, "members").Set(float64(p.stateWindow.Members()))
	metrics.MetricMetadataCache.WithLabelValues(p.dataID, "bytes").Set(float64(p.stateWindow.Bytes()))
}
