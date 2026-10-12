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
	reader          indexLoader
	maxAge          time.Duration
	publishedMaxAge time.Duration
	now             func() time.Time

	mutex     sync.RWMutex
	index     *Index
	lastError error
	refreshes uint64
}

type StoreOptions struct {
	// RefreshInterval is how often the index is rebuilt.
	RefreshInterval time.Duration
	// MaxAge is how old the held index may get, counted from alarmd's own
	// read, before the store reports itself degraded. It is not a deletion
	// bound: a degraded store still answers with what it has, so the caller
	// can choose between stale facts and none.
	MaxAge time.Duration
	// PublishedMaxAge is how old the facts may get, counted from the writer's
	// publish time, when the index read one. Zero means MaxAge.
	PublishedMaxAge time.Duration
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
	if options.PublishedMaxAge < 0 {
		return nil, errors.New("alarmd cmdbcache: the publish staleness bound cannot be negative")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{reader: reader, maxAge: options.MaxAge, publishedMaxAge: options.PublishedMaxAge, now: now}, nil
}

// The reasons an index is not one to decide on, as Health and every caller
// of Usable name them.
const (
	IndexNeverLoaded = "never_loaded"
	IndexStale       = "index_stale"
	IndexEmpty       = "index_empty"
)

// factsAge is how old an index's facts are and the bound they are held to:
// from the writer's publish time when the index read one, else from alarmd's
// own read. The publish time is the earlier of the two - read before the
// hashes - so it also covers a run of failed reads; a writer clock ahead of
// this one reads as just published.
func (store *Store) factsAge(index *Index) (time.Duration, time.Duration) {
	now := store.now()
	if published := index.PublishedAt(); !published.IsZero() {
		age := now.Sub(published)
		if age < 0 {
			age = 0
		}
		if store.publishedMaxAge == 0 {
			return age, store.maxAge
		}
		return age, store.publishedMaxAge
	}
	return now.Sub(index.BuiltAt()), store.maxAge
}

// judge is the one staleness and emptiness judgement on a held index:
// never_loaded, index_stale or index_empty, and "" for an index callers may
// decide on. Admission, every target selector, the exclusions, no-data's
// host resolution and attribution all read it through Usable, and Health
// reports it, so no two of them can disagree about the same snapshot.
func (store *Store) judge(index *Index) string {
	if index == nil {
		return IndexNeverLoaded
	}
	if age, bound := store.factsAge(index); age > bound {
		return IndexStale
	}
	if index.Hosts() == 0 {
		// An empty host cache would put every host-scoped strategy out of
		// scope at once. That is never a real CMDB state here, so it is
		// reported as degradation rather than acted on as fact.
		return IndexEmpty
	}
	return ""
}

// Usable is the index in force and why it may not be decided on, "" when it
// may. Past the bound, or empty, or never loaded, the CMDB facts are
// unavailable: admitted by name rather than decided on (decision-013 section
// 2 #12 and section 5.1 item 4), never the basis of a close, and no-data
// does not judge a target on them. The index is returned either way, for a
// caller that reports what it holds.
func (store *Store) Usable() (*Index, string) {
	if store == nil {
		return nil, IndexNeverLoaded
	}
	store.mutex.RLock()
	index := store.index
	store.mutex.RUnlock()
	return index, store.judge(index)
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

// targetIndex pins the held snapshot, the one judgement on it, and the
// result of its latest refresh together, so every selector of one resolution
// reads the same snapshot the same way, and a snapshot served past a failed
// refresh can be marked.
func (store *Store) targetIndex() (*Index, string, error) {
	store.mutex.RLock()
	index, readErr := store.index, store.lastError
	store.mutex.RUnlock()
	return index, store.judge(index), readErr
}

// HostIndexResolved reports whether this store can answer about hosts at all:
// whether Usable says the index may be decided on. With no index every host
// is "not held", and with an empty one so is every host, which is
// indistinguishable from a target whose hosts have all gone; past the bound
// the answers are facts nobody can vouch for, and no-data judges only on
// facts that were there, as admission does on the same judgement.
func (store *Store) HostIndexResolved() bool {
	_, reason := store.Usable()
	return reason == ""
}

// Refresh rebuilds the index once. A failed refresh leaves the previous index
// in place and is recorded; it is not an error the caller has to handle to keep
// running.
func (store *Store) Refresh(ctx context.Context) error {
	index, err := store.reader.Load(ctx, store.now())
	store.mutex.Lock()
	if err != nil {
		store.lastError = err
		store.mutex.Unlock()
		return err
	}
	index.carryOptional(store.index)
	store.index = index
	store.lastError = nil
	store.refreshes++
	store.mutex.Unlock()
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
	ServiceInstances int
	// Age is how long ago alarmd read the held index. PublishedAge is how
	// long ago the writer published what it read, when the writer says
	// (Published); SourceAge is the other writer's last attempt, shown and
	// not decided on.
	Age            time.Duration
	Published      bool
	PublishedAge   time.Duration
	SourceAge      time.Duration
	Degraded       bool
	DegradedReason string
	Refreshes      uint64
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
	health := Health{Refreshes: store.refreshes}
	if store.index == nil {
		health.Degraded = true
		health.DegradedReason = IndexNeverLoaded
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
	if published := store.index.PublishedAt(); !published.IsZero() {
		// As factsAge reads it: a writer clock ahead of this one is a
		// publish just made.
		health.Published, health.PublishedAge = true, max(now.Sub(published), 0)
	}
	if source := store.index.SourceRefreshedAt(); !source.IsZero() {
		health.SourceAge = now.Sub(source)
	}
	if reason := store.judge(store.index); reason != "" {
		health.Degraded, health.DegradedReason = true, reason
	}
	return health
}
