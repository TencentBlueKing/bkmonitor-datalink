// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// indexLoader is what the store rebuilds from. The reader satisfies it; tests
// substitute their own so the age and failure rules can be exercised without a
// Redis.
type indexLoader interface {
	Load(ctx context.Context, now time.Time) (*Index, error)
}

// publishMarkReader is a loader that can say, without reading the hashes,
// whether the writer has published since: Reader.ReadPublishMarks.
type publishMarkReader interface {
	ReadPublishMarks(ctx context.Context) (PublishMarks, error)
}

// How a refresh read the cache, as ObserveRead is told: the whole cache read
// into a new index; the publish time found where the held index has it and
// the held index kept; or a read that failed and kept the held index.
const (
	ReadFull      = "full"
	ReadUnchanged = "unchanged"
	ReadFailed    = "failed"
)

// ReadResults is the closed set ObserveRead is called with.
var ReadResults = []string{ReadFull, ReadUnchanged, ReadFailed}

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

	observeRead func(result string)
	// publishedMaxAgeCap is the most the published bound may be derived to
	// (derivePublishedBound); zero keeps it at publishedMaxAge.
	publishedMaxAgeCap time.Duration
	// onTransition is told when the judgement on the held index changes
	// (Transition); nil tells no one. tenant names the store in it.
	onTransition func(Transition)
	tenant       string

	// publishes are the writer's distinct publish times the loads read,
	// oldest first, at most publishWindow+1 of them; derivedBound and
	// maxGap are what they give, set at each refresh and read without the
	// lock by every judgement.
	publishes    []time.Time
	derivedBound atomic.Int64
	maxGap       atomic.Int64
	lastJudged   string

	mutex sync.RWMutex
	index *Index
	// identity is the last index read with hosts in it, carried the hosts
	// it refused, and identityAgents the last agent hash read whole: what a
	// record's alert identity is read from (IdentityHost, IdentityAgent)
	// whether or not the index in force may be decided on. An alert's
	// identity is not a decision, and the last CMDB that placed a host is a
	// better answer for it than the record's raw address, which would file
	// the alert under another fingerprint. A host a load refused keeps the
	// facts the last load that placed it gave it, until a clean load - one
	// that refused nothing - no longer has it: the host is gone then. No
	// more hosts are carried than the load holds.
	identity *Index
	carried  map[string]*HostFacts
	// identityInstances are the service instances of the last load that held
	// any, with the instances a later load refused carried as hosts are
	// (carryRefusedInstances): what an instance-placed record's alert
	// identity is read from (IdentityServiceInstance). The instance hash is
	// published apart from the host hash, so its last populated load is
	// tracked on its own.
	identityInstances map[string]*ServiceInstanceFacts
	identityAgents    map[string]string
	lastError         error
	// loaded says a read of the cache has succeeded, whatever it found, and
	// firstAttempt is when the store first tried one. Until either a read
	// succeeded or identityHoldBound has passed since the first try, a
	// record whose identity no index can give is held (IdentityPending);
	// after, it goes out under its own address, as Python emits a record
	// CMDB holds no host for.
	loaded       bool
	firstAttempt time.Time
	// holdBound is identityHoldRefreshes refresh intervals.
	holdBound time.Duration
	refreshes uint64
	unchanged uint64
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
	// ObserveRead is told how each refresh read the cache (ReadResults).
	// Nil observes nothing.
	ObserveRead func(result string)
	// PublishedMaxAgeCap, when set, derives the published bound from the
	// writer's observed publish gaps (derivePublishedBound), between
	// PublishedMaxAge and this cap. Zero holds the bound at PublishedMaxAge.
	PublishedMaxAgeCap time.Duration
	// OnTransition is told, at a refresh, when the judgement on the held
	// index changed - into or out of stale, empty or never loaded. Nil tells
	// no one.
	OnTransition func(Transition)
}

// Transition is one change of the judgement on a store's held index, at a
// refresh: the tenant, the judgement before and after ("" for usable), the
// published age, the bound it was judged against and the longest publish
// gap the bound was derived from (zero before two publishes were seen).
type Transition struct {
	Tenant       string
	From, To     string
	PublishedAge time.Duration
	Bound        time.Duration
	MaxGap       time.Duration
}

// The published bound is derived from the longest gap between the writer's
// last publishWindow distinct publishes: two of them, and never below the
// configured bound nor above its cap. The window is two hours of a writer
// that publishes every five minutes, so a skipped cycle - a writer measured
// at one deployment skips one every half hour to hour, its gaps reaching 33
// minutes - is still in the window when the next one comes, and one long gap
// raises the bound
// for as long as it stays in the window. Twice the longest gap is a writer
// that missed one more cycle than it ever has, not one that stopped; past
// the cap, a writer that stopped is stale within two hours whatever its
// history.
const (
	publishWindow          = 24
	publishedBoundMultiple = 2
)

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
	if options.PublishedMaxAgeCap != 0 && options.PublishedMaxAgeCap < options.PublishedMaxAge {
		return nil, errors.New("alarmd cmdbcache: the published bound's cap is below the bound")
	}
	return &Store{reader: reader, maxAge: options.MaxAge, publishedMaxAge: options.PublishedMaxAge, now: now,
		observeRead: options.ObserveRead, publishedMaxAgeCap: options.PublishedMaxAgeCap,
		onTransition: options.OnTransition, tenant: DefaultTenant, lastJudged: IndexNeverLoaded,
		holdBound: identityHoldRefreshes * options.RefreshInterval}, nil
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
		return age, store.publishedBound()
	}
	return now.Sub(index.BuiltAt()), store.maxAge
}

// publishedBound is the bound a published age is held to: the derived one
// once two distinct publishes have been seen, else the configured one.
func (store *Store) publishedBound() time.Duration {
	if derived := store.derivedBound.Load(); derived > 0 {
		return time.Duration(derived)
	}
	if store.publishedMaxAge == 0 {
		return store.maxAge
	}
	return store.publishedMaxAge
}

// notePublish records a load's publish time when it is a new one, and
// derives the published bound from the gaps between the last publishWindow
// of them. Called with the lock held.
func (store *Store) notePublish(published time.Time) {
	if published.IsZero() || store.publishedMaxAgeCap == 0 {
		return
	}
	if n := len(store.publishes); n > 0 && !published.After(store.publishes[n-1]) {
		return
	}
	store.publishes = append(store.publishes, published)
	if len(store.publishes) > publishWindow+1 {
		store.publishes = append([]time.Time(nil), store.publishes[len(store.publishes)-publishWindow-1:]...)
	}
	store.derivedBound.Store(int64(derivePublishedBound(store.publishes, store.publishedMaxAge, store.publishedMaxAgeCap)))
	store.maxGap.Store(int64(maxPublishGap(store.publishes)))
}

// maxPublishGap is the longest gap between successive publishes.
func maxPublishGap(publishes []time.Time) time.Duration {
	var longest time.Duration
	for i := 1; i < len(publishes); i++ {
		longest = max(longest, publishes[i].Sub(publishes[i-1]))
	}
	return longest
}

// derivePublishedBound is publishedBoundMultiple times the longest gap
// between the publishes, within [floor, ceiling]; zero before two publishes,
// which leaves the configured bound in force.
func derivePublishedBound(publishes []time.Time, floor, ceiling time.Duration) time.Duration {
	if len(publishes) < 2 {
		return 0
	}
	return min(max(publishedBoundMultiple*maxPublishGap(publishes), floor), ceiling)
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

// IdentityHost is the host CMDB last placed at a key (an id, or an
// ip|cloud address), read from the last index that held hosts, whether or
// not the index in force may be decided on. For a record's alert identity
// only: no filter, scope or target decision reads it.
func (store *Store) IdentityHost(key string) (*HostFacts, bool) {
	if store == nil {
		return nil, false
	}
	store.mutex.RLock()
	index, carried := store.identity, store.carried
	store.mutex.RUnlock()
	if host, found := index.Lookup(key); found {
		return host, true
	}
	host, found := carried[key]
	return host, found
}

// IdentityServiceInstance is the service instance CMDB last placed under an
// id, read as IdentityHost is: from the last load that held instances, and
// the instances a later load refused. For a record's alert identity only.
func (store *Store) IdentityServiceInstance(id string) (*ServiceInstanceFacts, bool) {
	if store == nil || id == "" {
		return nil, false
	}
	store.mutex.RLock()
	instances := store.identityInstances
	store.mutex.RUnlock()
	instance, found := instances[id]
	return instance, found
}

// IdentityInstancesHeld is whether any load ever held service instances.
func (store *Store) IdentityInstancesHeld() bool {
	if store == nil {
		return false
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return len(store.identityInstances) > 0
}

// carryRefusedInstances is the instance identity after a load with
// instances: that load's instances, and - when it refused instance records -
// every instance the previous identity held that this load does not; after
// a clean load, that load's alone, and an instance absent from it is gone.
// Bounded as carryRefused is: what is carried is only ever the instances
// seen since the last clean load.
func carryRefusedInstances(index *Index, previous map[string]*ServiceInstanceFacts) map[string]*ServiceInstanceFacts {
	if index.Refused().ServiceInstances == 0 || len(previous) == 0 {
		return index.serviceInstances
	}
	merged := make(map[string]*ServiceInstanceFacts, len(index.serviceInstances)+len(previous))
	for id, instance := range index.serviceInstances {
		merged[id] = instance
	}
	for id, instance := range previous {
		if _, present := merged[id]; !present {
			merged[id] = instance
		}
	}
	return merged
}

// carryRefused is what the identity carries after a load with hosts: when
// the load refused host records, every host the identity it replaces placed
// - its index's and those it carried - that this load does not have, no
// more of them than the load holds; after a clean load, none.
func carryRefused(index *Index, previous *Index, carried map[string]*HostFacts) map[string]*HostFacts {
	if index.Refused().Hosts == 0 || previous == nil {
		return nil
	}
	// The cap is the identity's own size - the hosts it placed and those it
	// carried - which is CMDB's: a load that refused most of its hosts is
	// exactly when every one of them has to be kept, and the cap does not
	// grow, since what is carried never exceeds what was placed.
	limit := previous.Hosts() + len(carried)
	next := make(map[string]*HostFacts)
	carry := func(id string, host *HostFacts) bool {
		if len(next) >= limit {
			return false
		}
		if _, present := index.byHostID[id]; !present {
			next[id] = host
		}
		return true
	}
	for id, host := range previous.byHostID {
		if !carry(id, host) {
			return next
		}
	}
	for id, host := range carried {
		if !carry(id, host) {
			return next
		}
	}
	return next
}

// IdentityAgent is the host CMDB last placed an agent at, read as
// IdentityHost is, from the last agent hash read whole; held is false when
// no index with hosts was ever read, which is a host nothing can name.
func (store *Store) IdentityAgent(agent string) (host *HostFacts, found, held bool) {
	if store == nil || agent == "" {
		return nil, false, false
	}
	store.mutex.RLock()
	index, carried, agents := store.identity, store.carried, store.identityAgents
	store.mutex.RUnlock()
	if index == nil || agents == nil {
		return nil, false, false
	}
	id, named := agents[agent]
	if !named {
		return nil, false, true
	}
	host, found = index.byHostID[id]
	if !found {
		host, found = carried[id]
	}
	if !found || host.AgentID != agent {
		return nil, false, true
	}
	return host, true, true
}

// IdentityHeld is whether any index with hosts was ever read.
func (store *Store) IdentityHeld() bool {
	if store == nil {
		return false
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.identity != nil
}

// identityHoldRefreshes is how many refresh intervals a store whose reads
// keep failing holds host-placed records before it lets them go out under
// their own address: five minutes at the minute's cadence, a Redis
// restart or a failover, during which a held Slot is retried rather than
// filed under another fingerprint. Past it the cache is not coming back
// soon, and holding further would lose the detection itself; a later read
// that succeeds still gives the records their identity from then on.
const identityHoldRefreshes = 5

// IdentityPending is whether a record whose identity no index can give
// should still be held: no read of the cache has succeeded yet, and the
// store has not been trying for longer than its hold bound. A store that
// has not tried at all is pending: its first read is under way.
func (store *Store) IdentityPending() bool {
	if store == nil {
		return false
	}
	store.mutex.RLock()
	loaded, first := store.loaded, store.firstAttempt
	store.mutex.RUnlock()
	if loaded {
		return false
	}
	return first.IsZero() || store.now().Sub(first) < store.holdBound
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

// Refresh rebuilds the index once, unless the writer has not published since
// the held index was read (publishUnchanged). A failed refresh leaves the
// previous index in place and is recorded; it is not an error the caller has
// to handle to keep running.
func (store *Store) Refresh(ctx context.Context) error {
	if store.publishUnchanged(ctx) {
		store.mutex.Lock()
		store.lastError = nil
		store.unchanged++
		store.loaded = true
		store.mutex.Unlock()
		store.observe(ReadUnchanged)
		store.noteJudgement()
		return nil
	}
	store.mutex.Lock()
	if store.firstAttempt.IsZero() {
		store.firstAttempt = store.now()
	}
	store.mutex.Unlock()
	index, err := store.reader.Load(ctx, store.now())
	store.mutex.Lock()
	if err != nil {
		store.lastError = err
		store.mutex.Unlock()
		store.observe(ReadFailed)
		store.noteJudgement()
		return err
	}
	store.loaded = true
	index.carryOptional(store.index)
	store.index = index
	store.notePublish(index.PublishedAt())
	if index.Hosts() > 0 {
		store.carried = carryRefused(index, store.identity, store.carried)
		store.identity = index
	}
	if !index.agentsUnreadable && index.Hosts() > 0 {
		store.identityAgents = index.byAgent
	}
	if index.ServiceInstances() > 0 {
		store.identityInstances = carryRefusedInstances(index, store.identityInstances)
	}
	store.lastError = nil
	store.refreshes++
	store.mutex.Unlock()
	store.observe(ReadFull)
	store.noteJudgement()
	return nil
}

// noteJudgement tells OnTransition when the judgement on the held index has
// changed since the last refresh. A refresh is once a minute, so a store
// says each transition once and no more often than that.
func (store *Store) noteJudgement() {
	store.mutex.Lock()
	index := store.index
	reason := store.judge(index)
	from := store.lastJudged
	store.lastJudged = reason
	store.mutex.Unlock()
	if reason == from || store.onTransition == nil {
		return
	}
	transition := Transition{Tenant: store.tenant, From: from, To: reason, Bound: store.publishedBound(),
		MaxGap: time.Duration(store.maxGap.Load())}
	if index != nil && !index.PublishedAt().IsZero() {
		transition.PublishedAge = max(store.now().Sub(index.PublishedAt()), 0)
	}
	store.onTransition(transition)
}

// publishUnchanged is whether the held index can stand without reading the
// cache again: the writer says when it published each hash the load reads,
// says the same times it did when the held index was read, and is the only
// writer.
//
// The writer publishes in two groups on two schedules: the host, agent,
// topology and business mapping hashes together under the host and topology
// time, and the service instance hash alone under a time of its own. Each
// group is renamed into place, complete, in the same atomic step that writes
// its time, so a hash changes only when its group's time does. Both times
// have to be there and unmoved; a writer that publishes no time for the
// service instance hash is read in full every refresh, as before, since
// nothing would say when that hash moved. Reading every host each minute in
// between found the same hosts. A second writer -- the one whose
// refresh time is the other key -- may change the hashes in place without
// moving the publish time, so with it present every refresh reads in full, as
// it does for a writer that publishes no time at all.
//
// There is no periodic full read besides. Each way the time could stand
// still is covered without one: a time that is gone reads as none and the
// refresh reads in full; a writer that stopped publishing stopped changing
// the hashes too, and the facts age on the publish time it left, which is
// what staleness is judged on; two publishes cannot name the same second,
// since one writer holds the publish lease and a round takes minutes. A
// writer that changed the hashes without moving the time would be breaking
// the one thing this rests on, and a timer would neither notice that nor
// bound it.
func (store *Store) publishUnchanged(ctx context.Context) bool {
	marks, ok := store.reader.(publishMarkReader)
	if !ok {
		return false
	}
	store.mutex.RLock()
	held := store.index
	store.mutex.RUnlock()
	// A held index whose agent hash could not be read is read again: kept
	// on an unmoved publish time, it would leave every agent-named record
	// unplaced until the writer's next round.
	if held == nil || held.PublishedAt().IsZero() || !held.SourceRefreshedAt().IsZero() || held.agentsUnreadable {
		return false
	}
	read, err := marks.ReadPublishMarks(ctx)
	return err == nil && !read.OtherWriter && !held.serviceInstancePublishedAt.IsZero() &&
		read.HostTopo.Equal(held.PublishedAt()) && read.ServiceInstance.Equal(held.serviceInstancePublishedAt)
}

func (store *Store) observe(result string) {
	if store.observeRead != nil {
		store.observeRead(result)
	}
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
	Age          time.Duration
	Published    bool
	PublishedAge time.Duration
	// PublishedBound is the bound the published age is judged against now,
	// and PublishMaxGap the longest gap between the writer's recent
	// publishes it was derived from (zero before two were seen).
	PublishedBound time.Duration
	PublishMaxGap  time.Duration
	SourceAge      time.Duration
	Degraded       bool
	DegradedReason string
	Refreshes      uint64
	// Unchanged is how many refreshes found the writer's publish time where
	// the held index has it and kept the index without reading the cache.
	Unchanged uint64
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
	health := Health{Refreshes: store.refreshes, Unchanged: store.unchanged, PublishedBound: store.publishedBound(),
		PublishMaxGap: time.Duration(store.maxGap.Load())}
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
