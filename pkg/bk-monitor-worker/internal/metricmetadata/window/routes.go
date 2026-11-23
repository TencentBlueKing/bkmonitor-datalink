// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
)

type metricRouteLoader interface {
	LoadMetricRoutes(context.Context) (map[uint]storage.MetricRoute, error)
}

type routeCache struct {
	refreshMu sync.Mutex
	snapshot  atomic.Pointer[routeSnapshot]
}

// routeSnapshot is immutable after publication.
type routeSnapshot struct {
	routes     map[uint]storage.MetricRoute
	loadedAt   time.Time
	loaded     bool
	generation uint64
}

var moduleRouteCache = newRouteCache()

// RefreshRoutesIfDue refreshes the process-wide route snapshot when it has expired.
func RefreshRoutesIfDue(ctx context.Context, store *storage.MySQLStore, interval time.Duration) error {
	return moduleRouteCache.RefreshIfDue(ctx, store, interval)
}

func newRouteCache() *routeCache {
	return &routeCache{}
}

// RefreshIfDue serializes full loads and publishes only complete route snapshots.
func (c *routeCache) RefreshIfDue(ctx context.Context, store metricRouteLoader, interval time.Duration) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	current := c.snapshot.Load()
	if current != nil && current.loaded && time.Since(current.loadedAt) < interval {
		return nil
	}
	return c.refreshLocked(ctx, store)
}

// Refresh always reloads and atomically publishes the complete route snapshot.
func (c *routeCache) Refresh(ctx context.Context, store metricRouteLoader) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.refreshLocked(ctx, store)
}

func (c *routeCache) refreshLocked(ctx context.Context, store metricRouteLoader) error {
	routes, err := store.LoadMetricRoutes(ctx)
	if err != nil {
		return err
	}
	var generation uint64 = 1
	if current := c.snapshot.Load(); current != nil {
		generation = current.generation + 1
	}
	c.snapshot.Store(&routeSnapshot{routes: cloneRoutes(routes), loadedAt: time.Now(), loaded: true, generation: generation})
	return nil
}

func (c *routeCache) Lookup(dataID uint) (storage.MetricRoute, bool, bool, uint64) {
	snapshot := c.snapshot.Load()
	if snapshot == nil {
		return storage.MetricRoute{}, false, false, 0
	}
	route, cached := snapshot.routes[dataID]
	loaded := snapshot.loaded
	return route, cached, loaded, snapshot.generation
}

// Remember copy-publishes a point lookup into the current snapshot.
func (c *routeCache) Remember(dataID uint, route storage.MetricRoute, generation uint64) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	current := c.snapshot.Load()
	if current != nil && current.generation != generation {
		return
	}
	routes := make(map[uint]storage.MetricRoute)
	loaded := false
	var loadedAt time.Time
	var currentGeneration uint64
	if current != nil {
		routes = cloneRoutes(current.routes)
		loaded = current.loaded
		loadedAt = current.loadedAt
		currentGeneration = current.generation
	}
	routes[dataID] = route
	c.snapshot.Store(&routeSnapshot{routes: routes, loadedAt: loadedAt, loaded: loaded, generation: currentGeneration})
}

func cloneRoutes(routes map[uint]storage.MetricRoute) map[uint]storage.MetricRoute {
	clone := make(map[uint]storage.MetricRoute, len(routes))
	for key, route := range routes {
		clone[key] = route
	}
	return clone
}

func (c *routeCache) Len() int {
	snapshot := c.snapshot.Load()
	if snapshot == nil {
		return 0
	}
	return len(snapshot.routes)
}
