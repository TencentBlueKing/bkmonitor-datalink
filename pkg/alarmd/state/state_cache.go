// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The write-through State cache (0.6 fourth-batch design §4): the packed
// bytes this process last wrote successfully under each runtime key, served
// to LoadRuntime instead of reading them back while the rules below say the
// store still holds exactly those bytes. A write's compare-and-set compares
// against the stored bytes, never against these, so a cache that is wrong
// costs a conflict and a re-read; the rules exist so that an evaluation never
// runs on history the store does not hold, since its events go out first.

// Lookup results, the closed vocabulary of state_cache_lookups_total.
const (
	StateCacheHit            = "hit"
	StateCacheMissAbsent     = "miss_absent"
	StateCacheMissFence      = "miss_fence"
	StateCacheMissUnknown    = "miss_unknown"
	StateCacheMissExpired    = "miss_expired"
	StateCacheMissReplay     = "miss_replay"
	StateCacheMissActivation = "miss_activation"
	StateCacheMissSharded    = "miss_sharded"
	StateCacheMissOverBudget = "miss_over_budget"
	StateCacheMissYielded    = "miss_yielded"
)

// StateCacheLookupResults lists them, for the metric's cells.
var StateCacheLookupResults = []string{StateCacheHit, StateCacheMissAbsent, StateCacheMissFence, StateCacheMissUnknown,
	StateCacheMissExpired, StateCacheMissReplay, StateCacheMissActivation, StateCacheMissSharded, StateCacheMissOverBudget,
	StateCacheMissYielded}

// Departure reasons, the closed vocabulary of state_cache_departures_total.
const (
	StateCacheDepartReleased       = "released"
	StateCacheDepartUnknown        = "unknown"
	StateCacheDepartConflict       = "conflict"
	StateCacheDepartExpired        = "expired"
	StateCacheDepartRenewalMissing = "renewal_missing"
	StateCacheDepartUntouched      = "untouched"
	// StateCacheDepartOverBudget is a Query Group that did not fit the
	// cache's budget.
	StateCacheDepartOverBudget = "over_budget"
	// StateCacheDepartYielded is a Query Group that fit the budget but not
	// what the Slots left of the retained pool: given back for a Slot that
	// needed the room, or not taken in past it.
	StateCacheDepartYielded = "yielded"
)

// StateCacheDepartureReasons lists them.
var StateCacheDepartureReasons = []string{StateCacheDepartReleased, StateCacheDepartUnknown, StateCacheDepartConflict,
	StateCacheDepartExpired, StateCacheDepartRenewalMissing, StateCacheDepartUntouched, StateCacheDepartOverBudget, StateCacheDepartYielded}

// Restore causes and refusals (§4.4).
const (
	StateCacheRestoreMissing            = "missing"
	StateCacheRestoreRolledBack         = "rolled_back"
	StateCacheDeclineProgressRolledBack = "progress_rolled_back"
	StateCacheDeclineNewerVersion       = "newer_version"
)

// StateCacheRestoreCauses and StateCacheDeclineReasons list them.
var (
	StateCacheRestoreCauses  = []string{StateCacheRestoreMissing, StateCacheRestoreRolledBack}
	StateCacheDeclineReasons = []string{StateCacheDeclineProgressRolledBack, StateCacheDeclineNewerVersion}
)

// stateCacheEntryOverhead is each entry's fixed heap beyond its key and
// value: the entry struct, two digest strings and the map slots (design
// §4.5.1, from the probe; the calibration test holds the sum to the heap).
const stateCacheEntryOverhead = 400

type stateCacheEntry struct {
	key   string
	group execution.QueryGroupIdentity
	raw   []byte
	// digest is the stored bytes' SHA-1 as the write's witness names it:
	// what this process last wrote under the key.
	digest       string
	applyVersion execution.ApplyVersion
	// fence is the owner fence the write ran under (rule 1); epoch and
	// segmentStart the Plan's activation it ran under (rule 6).
	fence        execution.OwnerFence
	epoch        execution.StateApplyEpoch
	segmentStart execution.EvaluationTime
	// slot is the Slot the write was for (rule 5).
	slot execution.EvaluationTime
	// usableUntil is the write time plus its TTL less the replay age (rule
	// 3): past it the store's key may be gone, so the entry is.
	usableUntil time.Time
	// touched is when the entry was last read or written, and interval the
	// time between its last two writes: untouched for two intervals, it
	// leaves.
	touched, written time.Time
	interval         time.Duration
	cost             int64
}

type stateCacheGroup struct {
	entries map[string]*stateCacheEntry
	bytes   int64
}

// stateCacheRefusal is a Query Group kept out of the cache. bound is the
// departure word for what it did not fit, as of its latest Slot: the budget
// (over_budget) or the pool's room (yielded). size is what the group wrote
// for slot, its latest Slot: once a write for a later Slot arrives, size is
// what one whole round of it costs, and the group comes back only when that
// fits.
type stateCacheRefusal struct {
	bound string
	slot  execution.EvaluationTime
	size  int64
}

// missOf is the lookup word for a key of the group while it is kept out.
func (refusal *stateCacheRefusal) missOf() string {
	if refusal.bound == StateCacheDepartYielded {
		return StateCacheMissYielded
	}
	return StateCacheMissOverBudget
}

// StateCacheStats is what the cache's metrics read.
type StateCacheStats struct {
	Bytes, BudgetBytes int64
	QueryGroups        int
	Lookups            map[string]uint64
	Departures         map[string]uint64
	Restored           map[string]uint64
	Declined           map[string]uint64
}

// stateCache is per ExecutionStore. Nil is a disabled cache: every method is
// safe on it and every lookup misses without counting.
//
// It holds bytes of the retained pool only while no Slot has reserved them:
// it takes in an entry only within its budget and within the pool's room
// (room), and gives Query Groups back whole when a Slot's reservation needs
// the bytes (YieldRetainedBytes), so it never takes a share from detection.
type stateCache struct {
	mu     sync.Mutex
	budget int64
	// room is what no Slot has reserved of the retained pool, read before
	// mu is taken: the pool's owner calls into the cache under its own lock.
	// Unset, the cache is held to its budget alone.
	room atomic.Pointer[func() uint64]
	// held mirrors bytes for the pool's owner, which reads it on every
	// reservation without taking mu.
	held      atomic.Int64
	replayAge time.Duration
	now       func() time.Time
	byKey     map[string]*stateCacheEntry
	groups    map[execution.QueryGroupIdentity]*stateCacheGroup
	// refused names the Query Groups kept out: one whose write did not fit
	// the budget or the pool's room, or one given back for a Slot. It stays
	// out until one whole round of it fits beside the resident ones, judged
	// at the first write of each later Slot, and the resident ones stay in,
	// so a working set larger than the budget is served at budget ÷ working
	// set rather than thrashing as an LRU would on a fixed rotation, and a
	// group larger than the room is not taken in write by write and given
	// back whole each round.
	refused                                 map[execution.QueryGroupIdentity]*stateCacheRefusal
	bytes                                   int64
	lookups, departures, restored, declined map[string]*atomic.Uint64
}

func newStateCache(budget int64, replayAge time.Duration, now func() time.Time) *stateCache {
	if budget <= 0 {
		return nil
	}
	counters := func(words []string) map[string]*atomic.Uint64 {
		out := make(map[string]*atomic.Uint64, len(words))
		for _, word := range words {
			out[word] = &atomic.Uint64{}
		}
		return out
	}
	return &stateCache{budget: budget, replayAge: replayAge, now: now,
		byKey: map[string]*stateCacheEntry{}, groups: map[execution.QueryGroupIdentity]*stateCacheGroup{},
		refused: map[execution.QueryGroupIdentity]*stateCacheRefusal{}, lookups: counters(StateCacheLookupResults),
		departures: counters(StateCacheDepartureReasons), restored: counters(StateCacheRestoreCauses),
		declined: counters(StateCacheDeclineReasons)}
}

// sizeClass rounds a length up to the allocator's size class, close enough:
// powers of two below 1 KiB, eighths of the power of two above.
func sizeClass(length int) int64 {
	if length <= 0 {
		return 0
	}
	size := int64(8)
	for size < int64(length) {
		size <<= 1
	}
	if size <= 1024 {
		return size
	}
	step := size / 16
	rounded := (int64(length) + step - 1) / step * step
	return rounded
}

func entryCost(key string, raw []byte) int64 {
	return sizeClass(len(raw)) + sizeClass(len(key)) + stateCacheEntryOverhead
}

// lookup is the cached record for key, when every rule holds for this read,
// and the result word either way.
func (cache *stateCache) lookup(key string, request execution.StatePreflightRequest, item execution.StatePreflightItem) (*stateCacheEntry, string) {
	if cache == nil {
		return nil, ""
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	group := request.Contract.Slot.QueryGroup
	result := cache.judgeLocked(key, request, item, group)
	cache.lookups[result].Add(1)
	if result != StateCacheHit {
		return nil, result
	}
	entry := cache.byKey[key]
	entry.touched = cache.now()
	return entry, result
}

func (cache *stateCache) judgeLocked(key string, request execution.StatePreflightRequest, item execution.StatePreflightItem, group execution.QueryGroupIdentity) string {
	if item.Sharded {
		// The key carries no shard: two pieces of a split strategy whose
		// selections met would write it under one epoch (§4.3, rule 6).
		return StateCacheMissSharded
	}
	if refusal := cache.refused[group]; refusal != nil {
		return refusal.missOf()
	}
	entry := cache.byKey[key]
	if entry == nil {
		return StateCacheMissAbsent
	}
	now := cache.now()
	switch {
	case !now.Before(entry.usableUntil):
		cache.departLocked(entry, StateCacheDepartExpired)
		return StateCacheMissExpired
	case request.OwnerFence.IsZero() || entry.fence != request.OwnerFence:
		return StateCacheMissFence
	case request.Contract.Slot.EvaluationTime <= entry.slot:
		// A replay, retry or older Slot reads the store, as the replay
		// judgement always has (rule 5).
		return StateCacheMissReplay
	case entry.epoch != item.ApplyVersion.StateApplyEpoch || entry.segmentStart != request.Contract.ScheduleSegmentStart:
		return StateCacheMissActivation
	}
	return StateCacheHit
}

// lastWritten is the digest and version this process last wrote under key,
// whatever Query Group wrote it.
func (cache *stateCache) lastWritten(key string) (*stateCacheEntry, bool) {
	if cache == nil {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, found := cache.byKey[key]
	if !found {
		return nil, false
	}
	copied := *entry
	return &copied, true
}

// applied records a write that came back APPLIED.
func (cache *stateCache) applied(entry stateCacheEntry) {
	if cache == nil {
		return
	}
	room := cache.roomNow()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	capacity := min(cache.budget, room)
	now := cache.now()
	entry.cost = entryCost(entry.key, entry.raw)
	entry.touched, entry.written = now, now
	if previous := cache.byKey[entry.key]; previous != nil {
		if previous.group == entry.group && previous.written.Before(now) {
			entry.interval = now.Sub(previous.written)
		}
		cache.removeLocked(previous)
	}
	if refusal := cache.refused[entry.group]; refusal != nil {
		// Kept out while a round of it is measured: a write for its latest
		// Slot adds to the round, and the first write for a later Slot
		// closes it, taking the group back only when the whole round fits.
		switch {
		case entry.slot == refusal.slot:
			refusal.size += entry.cost
			return
		case entry.slot < refusal.slot:
			return
		}
		round := refusal.size
		refusal.slot, refusal.size = entry.slot, entry.cost
		if cache.bytes+round > capacity {
			refusal.bound = cache.boundOf(cache.bytes + round)
			return
		}
		delete(cache.refused, entry.group)
	}
	if cache.bytes+entry.cost > capacity {
		// A resident Query Group that grew past the room leaves whole; one
		// not resident does not come in. Either way it is read back, and
		// what it wrote for this Slot starts the round it is measured by.
		bound := cache.boundOf(cache.bytes + entry.cost)
		size := entry.cost
		if group := cache.groups[entry.group]; group != nil {
			for _, resident := range group.entries {
				if resident.slot == entry.slot {
					size += resident.cost
				}
				cache.departLocked(resident, bound)
			}
		} else {
			cache.departures[bound].Add(1)
		}
		cache.refused[entry.group] = &stateCacheRefusal{bound: bound, slot: entry.slot, size: size}
		return
	}
	stored := entry
	stored.raw = append([]byte(nil), entry.raw...)
	group := cache.groups[entry.group]
	if group == nil {
		group = &stateCacheGroup{entries: map[string]*stateCacheEntry{}}
		cache.groups[entry.group] = group
	}
	group.entries[entry.key] = &stored
	group.bytes += stored.cost
	cache.byKey[entry.key] = &stored
	cache.bytes += stored.cost
	cache.held.Store(cache.bytes)
}

// boundOf is the departure word for what bytes did not fit: past the budget
// it is over_budget, within it only the pool's room was short, yielded.
func (cache *stateCache) boundOf(bytes int64) string {
	if bytes > cache.budget {
		return StateCacheDepartOverBudget
	}
	return StateCacheDepartYielded
}

// roomNow is what the cache may hold of the retained pool now, read before
// mu is taken.
func (cache *stateCache) roomNow() int64 {
	room := cache.room.Load()
	if room == nil {
		return math.MaxInt64
	}
	return int64(min((*room)(), uint64(math.MaxInt64)))
}

// BorrowRetainedWithin has the cache take in no more than room says no Slot
// has reserved of the retained pool.
func (cache *stateCache) BorrowRetainedWithin(room func() uint64) {
	if cache != nil && room != nil {
		cache.room.Store(&room)
	}
}

// BorrowedRetainedBytes is what the cache holds of the retained pool.
func (cache *stateCache) BorrowedRetainedBytes() uint64 {
	if cache == nil {
		return 0
	}
	return uint64(max(cache.held.Load(), 0))
}

// YieldRetainedBytes gives at least need bytes back to the retained pool,
// or all it holds, a Query Group at a time and the largest first, so as few
// as can be are read back. A group given back stays out, as one over the
// budget does, until a whole round of it fits again, measured from what it
// held for its latest Slot.
func (cache *stateCache) YieldRetainedBytes(need uint64) {
	if cache == nil || need == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	groups := make([]execution.QueryGroupIdentity, 0, len(cache.groups))
	for group := range cache.groups {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(left, right int) bool {
		if cache.groups[groups[left]].bytes != cache.groups[groups[right]].bytes {
			return cache.groups[groups[left]].bytes > cache.groups[groups[right]].bytes
		}
		return groups[left] < groups[right]
	})
	freed := uint64(0)
	for _, group := range groups {
		if freed >= need {
			return
		}
		held := cache.groups[group]
		freed += uint64(held.bytes)
		refusal := &stateCacheRefusal{bound: StateCacheDepartYielded}
		for _, entry := range held.entries {
			switch {
			case entry.slot > refusal.slot:
				refusal.slot, refusal.size = entry.slot, entry.cost
			case entry.slot == refusal.slot:
				refusal.size += entry.cost
			}
			cache.departLocked(entry, StateCacheDepartYielded)
		}
		cache.refused[group] = refusal
	}
}

// drop removes key for reason, if it is held.
func (cache *stateCache) drop(key, reason string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.byKey[key]; entry != nil {
		cache.departLocked(entry, reason)
	}
}

// DropQueryGroup removes every entry of a Query Group this process stops,
// loses, declines or hands over (rule 1: the fence would refuse them anyway).
func (cache *stateCache) dropQueryGroup(group execution.QueryGroupIdentity) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if held := cache.groups[group]; held != nil {
		for _, entry := range held.entries {
			cache.departLocked(entry, StateCacheDepartReleased)
		}
	}
	delete(cache.refused, group)
}

// sweep removes a Query Group's entries past their life or untouched for
// two of their own intervals: an entry is dropped by an active sweep, not
// left until it is asked for.
func (cache *stateCache) sweep(group execution.QueryGroupIdentity) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	held := cache.groups[group]
	if held == nil {
		return
	}
	now := cache.now()
	for _, entry := range held.entries {
		switch {
		case !now.Before(entry.usableUntil):
			cache.departLocked(entry, StateCacheDepartExpired)
		case entry.interval > 0 && now.Sub(entry.touched) > 2*entry.interval:
			cache.departLocked(entry, StateCacheDepartUntouched)
		}
	}
}

func (cache *stateCache) departLocked(entry *stateCacheEntry, reason string) {
	cache.removeLocked(entry)
	cache.departures[reason].Add(1)
}

func (cache *stateCache) removeLocked(entry *stateCacheEntry) {
	if cache.byKey[entry.key] != entry {
		return
	}
	delete(cache.byKey, entry.key)
	cache.bytes -= entry.cost
	cache.held.Store(cache.bytes)
	if group := cache.groups[entry.group]; group != nil {
		delete(group.entries, entry.key)
		group.bytes -= entry.cost
		if len(group.entries) == 0 {
			delete(cache.groups, entry.group)
		}
	}
}

func (cache *stateCache) countRestored(cause string) {
	if cache != nil {
		cache.restored[cause].Add(1)
	}
}

func (cache *stateCache) countDeclined(reason string) {
	if cache != nil {
		cache.declined[reason].Add(1)
	}
}

func (cache *stateCache) stats() StateCacheStats {
	if cache == nil {
		return StateCacheStats{}
	}
	read := func(counters map[string]*atomic.Uint64) map[string]uint64 {
		out := make(map[string]uint64, len(counters))
		for word, counter := range counters {
			out[word] = counter.Load()
		}
		return out
	}
	cache.mu.Lock()
	bytes, groups := cache.bytes, len(cache.groups)
	cache.mu.Unlock()
	return StateCacheStats{Bytes: bytes, BudgetBytes: cache.budget, QueryGroups: groups,
		Lookups: read(cache.lookups), Departures: read(cache.departures), Restored: read(cache.restored), Declined: read(cache.declined)}
}

// StateCacheStats reports the cache's sizes and counts; the zero value when
// the cache is off.
func (store *ExecutionStore) StateCacheStats() StateCacheStats { return store.cache.stats() }

// StateCacheBorrower is the cache as the retained pool's borrower; nil when
// the cache is off.
func (store *ExecutionStore) StateCacheBorrower() execution.RetainedBytesBorrower {
	if store == nil || store.cache == nil {
		return nil
	}
	return store.cache
}

// DropStateCacheQueryGroup removes a Query Group's entries when this process
// stops holding it.
func (store *ExecutionStore) DropStateCacheQueryGroup(group execution.QueryGroupIdentity) {
	store.cache.dropQueryGroup(group)
}
