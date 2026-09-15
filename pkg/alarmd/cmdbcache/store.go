// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// indexLoader is what the store rebuilds from. The reader satisfies it; tests
// substitute their own so the age and failure rules can be exercised without a
// Redis.
type indexLoader interface {
	Load(ctx context.Context, now time.Time) (*Index, error)
}

// Store holds the index alarmd is currently filtering against and replaces it
// wholesale on refresh.
//
// Filtering now depends on this cache being readable, which is a failure mode
// the unfiltered build did not have. The answer is neither of the two extremes:
// dropping the filter on a blip would silently restore alerting outside every
// strategy's target, and refusing to evaluate would silence real alerts because
// a cache hiccuped. The store keeps serving the last good index and only
// declares itself degraded once that index is older than a stated bound, so the
// decision to act on staleness belongs to the caller and is visible.
type Store struct {
	reader   indexLoader
	maxAge   time.Duration
	interval time.Duration
	now      func() time.Time

	mutex     sync.RWMutex
	index     *Index
	lastError error
	failures  uint64
	refreshes uint64
}

type StoreOptions struct {
	// RefreshInterval is how often the index is rebuilt.
	RefreshInterval time.Duration
	// MaxAge is how old the held index may get before the store reports itself
	// degraded. It is not a deletion bound: a degraded store still answers with
	// what it has, so the caller can choose between stale facts and none.
	MaxAge time.Duration
	// Now is injectable so the age rules are testable without sleeping.
	Now func() time.Time
}

func NewStore(reader indexLoader, options StoreOptions) (*Store, error) {
	if reader == nil {
		return nil, errors.New("alarmd cmdbcache: a reader is required")
	}
	if options.RefreshInterval <= 0 {
		return nil, errors.New("alarmd cmdbcache: a positive refresh interval is required")
	}
	if options.MaxAge <= options.RefreshInterval {
		return nil, errors.New("alarmd cmdbcache: the staleness bound must exceed the refresh interval")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{reader: reader, maxAge: options.MaxAge, interval: options.RefreshInterval, now: now}, nil
}

// Current returns the index in force, or nil before the first successful load.
func (store *Store) Current() *Index {
	if store == nil {
		return nil
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.index
}

// Refresh rebuilds the index once. A failed refresh leaves the previous index
// in place and is recorded; it is not an error the caller has to handle to keep
// running.
func (store *Store) Refresh(ctx context.Context) error {
	index, err := store.reader.Load(ctx, store.now())
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if err != nil {
		store.lastError = err
		store.failures++
		return err
	}
	store.index = index
	store.lastError = nil
	store.refreshes++
	return nil
}

// Health describes what the filter is currently deciding on.
type Health struct {
	Loaded            bool
	Hosts             int
	Age               time.Duration
	SourceAge         time.Duration
	Degraded          bool
	DegradedReason    string
	ConsecutiveErrors uint64
	Refreshes         uint64
}

func (store *Store) Health() Health {
	if store == nil {
		return Health{Degraded: true, DegradedReason: "no_store"}
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	health := Health{ConsecutiveErrors: store.failures, Refreshes: store.refreshes}
	if store.index == nil {
		health.Degraded = true
		health.DegradedReason = "never_loaded"
		return health
	}
	now := store.now()
	health.Loaded = true
	health.Hosts = store.index.Hosts()
	health.Age = now.Sub(store.index.BuiltAt())
	if source := store.index.SourceRefreshedAt(); !source.IsZero() {
		health.SourceAge = now.Sub(source)
	}
	switch {
	case health.Age > store.maxAge:
		health.Degraded = true
		health.DegradedReason = "index_stale"
	case store.index.Hosts() == 0:
		// An empty host cache would put every host-scoped strategy out of
		// scope at once. That is never a real CMDB state here, so it is
		// reported as degradation rather than acted on as fact.
		health.Degraded = true
		health.DegradedReason = "index_empty"
	}
	return health
}

// Run keeps the index fresh until the context ends. The first load happens
// immediately so a starting worker does not filter against an empty index.
func (store *Store) Run(ctx context.Context) {
	if store == nil {
		return
	}
	_ = store.Refresh(ctx)
	ticker := time.NewTicker(store.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.Refresh(ctx)
		}
	}
}
