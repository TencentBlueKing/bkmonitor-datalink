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

	refusalsChanged func(RefusedRecords)

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
	// RefusalsChanged, when set, is called after a refresh whose load
	// refused a different number of records than the index it replaced -
	// counted from none for the first load - so what the writer got wrong
	// is said when it appears, changes or clears, and not on every refresh.
	RefusalsChanged func(RefusedRecords)
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
	return &Store{reader: reader, maxAge: options.MaxAge, interval: options.RefreshInterval, now: now,
		refusalsChanged: options.RefusalsChanged}, nil
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

// targetIndex pins the held snapshot and the result of its latest refresh
// together. Exclusions must not treat a failed read as an absent member.
func (store *Store) targetIndex() (*Index, error) {
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.index, store.lastError
}

// HostIndexResolved reports whether this store can answer about hosts at all.
//
// It is the same judgement Health makes, read for a different question. Health
// says how this process is doing; this says whether a caller may act on the
// answers it gets, and the two states where it may not are the ones Health
// already names as never_loaded and index_empty: with no index every host is
// "not held", and with an empty one so is every host, which is
// indistinguishable from a target whose hosts have all gone.
//
// A stale index resolves. It holds hosts and answers about them, and the
// answers being a refresh interval old is a lag this deployment lives with;
// refusing to act on them would stop every host-scoped decision for the length
// of a CMDB hiccup, which is the larger harm.
func (store *Store) HostIndexResolved() bool {
	if store == nil {
		return false
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.index != nil && store.index.Hosts() > 0
}

// Refresh rebuilds the index once. A failed refresh leaves the previous index
// in place and is recorded; it is not an error the caller has to handle to keep
// running.
func (store *Store) Refresh(ctx context.Context) error {
	index, err := store.reader.Load(ctx, store.now())
	store.mutex.Lock()
	if err != nil {
		store.lastError = err
		store.failures++
		store.mutex.Unlock()
		return err
	}
	changed := !index.Refused().SameCounts(store.index.Refused())
	index.carryOptional(store.index)
	store.index = index
	store.lastError = nil
	store.refreshes++
	store.mutex.Unlock()
	if changed && store.refusalsChanged != nil {
		store.refusalsChanged(index.Refused())
	}
	return nil
}

// Health describes what the filter is currently deciding on.
type Health struct {
	Loaded bool
	Hosts  int
	// ServiceInstances is how many service instances the held index knows.
	// Zero is not a degradation of the store - a fleet without service
	// instances is a real state - but it is what a series naming an instance
	// will be admitted-unavailable against, so it is published beside the
	// host count for that reading.
	ServiceInstances  int
	Age               time.Duration
	SourceAge         time.Duration
	Degraded          bool
	DegradedReason    string
	ConsecutiveErrors uint64
	Refreshes         uint64
	// ClusterBusinessMapping and NamespaceBusinessMapping describe the BCS
	// cluster and cluster + namespace -> business mappings the held index
	// read. Zero held is not a degradation of the store - a writer that does
	// not publish a mapping yet is a real state - but every global business
	// event that would have used one is then counted as unmapped. A read
	// failure is not a store failure either: the hosts refreshed, and the
	// held entries are the last read that succeeded.
	ClusterBusinessMapping   MappingStats
	NamespaceBusinessMapping MappingStats
	// Refused is what the load that built the held index read of the host
	// and service instance hashes and could not use. It is not a
	// degradation of the store: each such record is taken as absent, and an
	// index whose every host was refused is index_empty.
	Refused RefusedRecords
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
	health.ServiceInstances = store.index.ServiceInstances()
	health.ClusterBusinessMapping = store.index.ClusterBusinessStats()
	health.NamespaceBusinessMapping = store.index.NamespaceBusinessStats()
	health.Refused = store.index.Refused()
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
