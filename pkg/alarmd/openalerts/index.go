// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type IndexOptions struct {
	Source                                               IndexSource
	Reconciler                                           Reconciler
	Subscriber                                           Subscriber
	Now                                                  func() time.Time
	Policy                                               UnavailablePolicy
	MaxStrategies, MaxMembers, MaxBytes, MaxLocalEntries int
	ReadBatch, ReconcileBatch                            int
	RefreshInterval, IndexInterval, ReconcileInterval    time.Duration
	CalibrationMaxAge, LocalRetention, CycleTimeout      time.Duration
}

type StrategySnapshot struct {
	IndexReadAt, CalibratedAt time.Time
	Members                   int
	Loaded, Calibrated        bool
	Reason                    string
}

type indexEntry struct {
	dirty, readGeneration                        uint64
	dirtySince                                   time.Time
	indexReadAt, calibratedAt, calibratedStarted time.Time
	lastReadAttempt, lastReconcileAttempt        time.Time
	index, missing, suppressed                   map[string]struct{}
	alerts                                       []Alert
	reconcileRequested                           bool
	reason                                       string
}

type indexState struct {
	options                     IndexOptions
	entries                     map[StrategyKey]*indexEntry
	order                       []StrategyKey
	readCursor, reconcileCursor int
	refreshMu                   sync.Mutex
	// wake is coalesced: only ownership/ACK starts work promptly. Notices are
	// deliberately batched on the maintenance cadence, not one request each.
	wake           chan struct{}
	ready          bool
	everReady      bool
	members, bytes int
	running        bool
	// eventSourceID is this deployment's own source as the last successful
	// calibration named it, kept for the recovery comparison.
	eventSourceID string
	// opened is when this process first sent the ABNORMAL for each alert it
	// has not sent the RECOVERY for since. Kept apart from Cache.added,
	// which a calibration prunes after the local retention: an alert resent
	// every round would otherwise be forever younger than
	// SentConfirmAfter, and the fallback would forget the alerts it exists
	// to let recover. Bounded by MaxLocalEntries; past it a new alert is
	// not recorded and counted as an eviction.
	opened map[member]time.Time
	// The latest assessment of this process's own sends against the sets;
	// see assessSent.
	sentInSet, sentNotInSet int
	disjoint                bool
}

// noteOpened records the first ABNORMAL of an alert. Called with the lock
// held; a no-op outside the index protocol.
func (cache *Cache) noteOpened(m member, now time.Time) {
	if cache.index == nil {
		return
	}
	if _, ok := cache.index.opened[m]; ok {
		return
	}
	if len(cache.index.opened) >= cache.index.options.MaxLocalEntries {
		cache.evictions++
		return
	}
	cache.index.opened[m] = now
}

func NewIndex(options IndexOptions) (*Cache, error) {
	if options.Source == nil || options.Subscriber == nil ||
		options.MaxStrategies <= 0 || options.MaxMembers <= 0 || options.MaxBytes <= 0 || options.MaxLocalEntries <= 0 ||
		options.ReadBatch <= 0 || options.ReconcileBatch <= 0 || options.RefreshInterval <= 0 || options.IndexInterval <= 0 ||
		options.ReconcileInterval <= 0 || options.CalibrationMaxAge <= 0 || options.LocalRetention <= 0 || options.CycleTimeout <= 0 {
		return nil, errors.New("alarmd openalerts: index sources and positive resource budgets are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Policy == "" {
		options.Policy = PolicySelfMaintain
	}
	if options.Policy != PolicySelfMaintain && options.Policy != PolicyPassThrough {
		return nil, errors.New("alarmd openalerts: invalid index unavailable policy")
	}
	cache := &Cache{now: options.Now, policy: options.Policy, maxLocal: options.MaxLocalEntries,
		added: map[member]stamped{}, removed: map[member]stamped{},
		refreshes: map[string]uint64{}, unavailable: map[UnavailableReason]uint64{}, lookups: map[Answer]uint64{}, ownLookups: map[Answer]uint64{},
		gateSince: options.Now(),
		index:     &indexState{options: options, entries: map[StrategyKey]*indexEntry{}, wake: make(chan struct{}, 1), opened: map[member]time.Time{}},
	}
	return cache, nil
}

// SetTracked replaces the authoritative owner scope. Invalid/over-budget input
// is rejected atomically. Entry identity guards in-flight responses after loss
// and reacquisition; no external generation or second owner is introduced.
func (cache *Cache) SetTracked(keys []StrategyKey) error {
	if cache == nil {
		return nil
	}
	if cache.index == nil {
		cache.Track(keys...)
		return nil
	}
	unique := make(map[StrategyKey]struct{}, min(len(keys), cache.index.options.MaxStrategies))
	bytes := 0
	for _, key := range keys {
		if !validStrategyKey(key) {
			return errors.New("alarmd openalerts: invalid tracked strategy")
		}
		if _, exists := unique[key]; exists {
			continue
		}
		unique[key] = struct{}{}
		bytes += 256 + len(key.TenantID) + len(key.StrategyID)
		if len(unique) > cache.index.options.MaxStrategies || bytes > cache.index.options.MaxBytes {
			return ErrCapacity
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	unchanged := len(unique) == len(cache.index.entries)
	if unchanged {
		for key := range unique {
			if cache.index.entries[key] == nil {
				unchanged = false
				break
			}
		}
	}
	if unchanged {
		return nil
	}
	now := cache.now()
	members, totalBytes := 0, 0
	for key := range unique {
		entry := cache.index.entries[key]
		if entry == nil {
			entry = &indexEntry{}
		}
		c, b := entrySize(key, entry)
		members += c
		totalBytes += b
	}
	if totalBytes > cache.index.options.MaxBytes {
		return ErrCapacity
	}
	for key := range cache.index.entries {
		if _, exists := unique[key]; !exists {
			delete(cache.index.entries, key)
		}
	}
	for m := range cache.added {
		if _, exists := unique[m.key]; !exists {
			delete(cache.added, m)
		}
	}
	for m := range cache.removed {
		if _, exists := unique[m.key]; !exists {
			delete(cache.removed, m)
		}
	}
	for m := range cache.index.opened {
		if _, exists := unique[m.key]; !exists {
			delete(cache.index.opened, m)
		}
	}
	cache.index.order = cache.index.order[:0]
	for key := range unique {
		if cache.index.entries[key] == nil {
			cache.index.entries[key] = &indexEntry{dirty: 1, dirtySince: now, reconcileRequested: true}
		}
		cache.index.order = append(cache.index.order, key)
	}
	sort.Slice(cache.index.order, func(i, j int) bool {
		a, b := cache.index.order[i], cache.index.order[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		return a.StrategyID < b.StrategyID
	})
	cache.index.readCursor, cache.index.reconcileCursor = 0, 0
	cache.index.members, cache.index.bytes = members, totalBytes
	cache.wakeIndex()
	return nil
}

// TrackOwned incrementally registers accepted owner assignments. Its work is
// proportional to the new keys, so loading one QG at a time does not rebuild
// the whole worker scope. Rejected batches make no partial ownership changes.
func (cache *Cache) TrackOwned(keys ...StrategyKey) error {
	if cache == nil {
		return nil
	}
	if cache.index == nil {
		cache.Track(keys...)
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	newKeys := make(map[StrategyKey]struct{}, min(len(keys), cache.index.options.MaxStrategies))
	bytes := 0
	for _, key := range keys {
		if !validStrategyKey(key) {
			return errors.New("alarmd openalerts: invalid tracked strategy")
		}
		if cache.index.entries[key] != nil {
			continue
		}
		if _, exists := newKeys[key]; exists {
			continue
		}
		newKeys[key] = struct{}{}
		bytes += 256 + len(key.TenantID) + len(key.StrategyID)
		if len(cache.index.entries)+len(newKeys) > cache.index.options.MaxStrategies || cache.index.bytes+bytes > cache.index.options.MaxBytes {
			return ErrCapacity
		}
	}
	for key := range newKeys {
		cache.index.entries[key] = &indexEntry{dirty: 1, dirtySince: cache.now(), reconcileRequested: true}
		cache.index.order = append(cache.index.order, key)
	}
	cache.index.bytes += bytes
	if len(newKeys) > 0 {
		cache.wakeIndex()
	}
	return nil
}

func (cache *Cache) Untrack(keys ...StrategyKey) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, key := range keys {
		if cache.index != nil {
			if entry := cache.index.entries[key]; entry != nil {
				c, b := entrySize(key, entry)
				cache.index.members -= c
				cache.index.bytes -= b
			}
			delete(cache.index.entries, key)
		} else {
			delete(cache.tracked, key)
			delete(cache.loaded, key)
			delete(cache.sets, key)
		}
		for m := range cache.added {
			if m.key == key {
				delete(cache.added, m)
			}
		}
		for m := range cache.removed {
			if m.key == key {
				delete(cache.removed, m)
			}
		}
		if cache.index != nil {
			for m := range cache.index.opened {
				if m.key == key {
					delete(cache.index.opened, m)
				}
			}
		}
	}
	if cache.index != nil {
		order := cache.index.order[:0]
		for _, key := range cache.index.order {
			if cache.index.entries[key] != nil {
				order = append(order, key)
			}
		}
		cache.index.order = order
	}
}

func (cache *Cache) RequestReconcile(key StrategyKey) {
	if cache == nil || cache.index == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.index.entries[key]; entry != nil {
		entry.reconcileRequested = true
	}
}

func (cache *Cache) wakeIndex() {
	select {
	case cache.index.wake <- struct{}{}:
	default:
	}
}

func (cache *Cache) indexReady(ready bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.index.ready = ready
	if ready {
		cache.index.everReady = true
		for _, entry := range cache.index.entries {
			entry.dirty++
			if entry.dirtySince.IsZero() {
				entry.dirtySince = cache.now()
			}
		}
		cache.wakeIndex()
	}
}

func (cache *Cache) indexChanged(key StrategyKey) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.index.entries[key]; entry != nil {
		entry.dirty++
		if entry.dirtySince.IsZero() {
			entry.dirtySince = cache.now()
		}
		// A notice has no operation/member. Preserve calibration differences,
		// and ask a later bounded calibration to settle their meaning.
		if len(entry.missing)+len(entry.suppressed) > 0 {
			entry.reconcileRequested = true
		}
	}
}

func (cache *Cache) Run(ctx context.Context) error {
	if cache == nil || cache.index == nil {
		return errors.New("alarmd openalerts: index cache is required")
	}
	cache.mu.Lock()
	if cache.index.running {
		cache.mu.Unlock()
		return errors.New("alarmd openalerts: index cache already running")
	}
	cache.index.running = true
	cache.index.ready, cache.index.everReady = false, false
	cache.mu.Unlock()
	defer func() { cache.mu.Lock(); cache.index.running = false; cache.mu.Unlock() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_ = cache.index.options.Subscriber.Watch(ctx, cache.indexReady, cache.indexChanged)
			cache.indexReady(false)
			if !waitIndex(ctx, cache.index.options.RefreshInterval) {
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(cache.index.options.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cache.Refresh(ctx)
		case <-cache.index.wake:
			cache.Refresh(ctx)
		}
	}
}

type indexJob struct {
	key        StrategyKey
	entry      *indexEntry
	generation uint64
}

func (cache *Cache) jobs(calibration bool) []indexJob {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	state := cache.index
	now := cache.now()
	cursor, limit := &state.readCursor, state.options.ReadBatch
	if calibration {
		cursor, limit = &state.reconcileCursor, state.options.ReconcileBatch
	}
	result := make([]indexJob, 0, min(limit, len(state.order)))
	for seen := 0; seen < len(state.order) && len(result) < limit; seen++ {
		*cursor %= len(state.order)
		key := state.order[*cursor]
		*cursor++
		entry := state.entries[key]
		if entry == nil {
			continue
		}
		if calibration {
			if !entry.lastReconcileAttempt.IsZero() && now.Sub(entry.lastReconcileAttempt) < max(state.options.RefreshInterval, state.options.LocalRetention) {
				continue
			}
			if !entry.reconcileRequested && !entry.calibratedAt.IsZero() && now.Sub(entry.calibratedAt) < state.options.ReconcileInterval {
				continue
			}
			entry.lastReconcileAttempt = now
		} else {
			if !entry.lastReadAttempt.IsZero() && now.Sub(entry.lastReadAttempt) < state.options.RefreshInterval {
				continue
			}
			if entry.dirty == entry.readGeneration && !entry.indexReadAt.IsZero() && now.Sub(entry.indexReadAt) < state.options.IndexInterval {
				continue
			}
			entry.lastReadAttempt = now
		}
		result = append(result, indexJob{key: key, entry: entry, generation: entry.dirty})
	}
	return result
}

func (cache *Cache) refreshIndex(ctx context.Context) {
	state := cache.index
	cache.mu.Lock()
	awaitingACK := !state.everReady
	cache.mu.Unlock()
	if awaitingACK {
		return
	}
	// One background round at a time, even if an external maintenance hook
	// also calls Refresh. Contending callers do no I/O and never build a queue.
	if !state.refreshMu.TryLock() {
		return
	}
	defer state.refreshMu.Unlock()
	// Reserve half the cycle for each dependency; slow Redis cannot starve
	// reconciliation (or vice versa). Calls are serial, never four per worker.
	for _, calibration := range []bool{false, true} {
		if calibration && state.options.Reconciler == nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, state.options.CycleTimeout/2)
		for _, job := range cache.jobs(calibration) {
			if callCtx.Err() != nil {
				break
			}
			started := cache.now()
			if calibration {
				result, err := state.options.Reconciler.Reconcile(callCtx, job.key)
				cache.applyCalibration(job, started, result, err)
			} else {
				members, err := state.options.Source.ReadSet(callCtx, job.key)
				cache.applyIndex(job, members, err)
			}
		}
		cancel()
	}
	cache.mu.Lock()
	cache.assessSent()
	cache.mu.Unlock()
}

// assessSent checks the alerts this process sent ABNORMAL for against the
// latest read of their strategy's set. It is the one reading that tells a
// consumer holding no alert on a series from sets keyed another way: both
// answer every lookup "absent", and only the second also leaves out the
// alerts this process opened itself. Called with the lock held, once per
// refresh round; the work is one pass over the local entries.
func (cache *Cache) assessSent() {
	inSet, notInSet := 0, 0
	for m, first := range cache.index.opened {
		entry := cache.index.entries[m.key]
		if entry == nil {
			continue
		}
		read := entry.indexReadAt
		if entry.calibratedStarted.After(read) {
			read = entry.calibratedStarted
		}
		if read.IsZero() || first.After(read.Add(-SentConfirmAfter)) {
			continue
		}
		_, inIndex := entry.index[m.fingerprint]
		_, inMissing := entry.missing[m.fingerprint]
		if inIndex || inMissing {
			inSet++
		} else {
			notInSet++
		}
	}
	disjoint := inSet == 0 && notInSet >= DisjointMinimum
	if cache.index.disjoint {
		// See DisjointMinimum: only positive evidence ends the state.
		disjoint = inSet == 0 && len(cache.index.opened) > 0
	}
	if disjoint && !cache.index.disjoint {
		cache.unavailable[UnavailableMembersDisjoint]++
	}
	cache.index.sentInSet, cache.index.sentNotInSet, cache.index.disjoint = inSet, notInSet, disjoint
}

// indexGate answers the recovery gate. While the sets are disjoint from
// this process's sends (see DisjointMinimum) an "absent" from them says
// nothing about the series, so the gate is answered as when the
// publication is unavailable: by the policy in force, which by default is
// what this process sent. A fingerprint the sets do carry is still open.
// Called with the lock held.
func (cache *Cache) indexGate(m member, now time.Time) bool {
	if !cache.index.disjoint {
		return cache.indexContains(m, now, true)
	}
	if cache.policy == PolicyPassThrough {
		cache.countLookup(AnswerPassedThrough)
		return true
	}
	cache.countLookup(AnswerSelfMaintained)
	if cache.indexContains(m, now, false) {
		return true
	}
	_, opened := cache.index.opened[m]
	return opened
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func entrySize(key StrategyKey, entry *indexEntry) (int, int) {
	count, bytes := 0, 256+len(key.TenantID)+len(key.StrategyID)
	for _, set := range []map[string]struct{}{entry.index, entry.missing, entry.suppressed} {
		for value := range set {
			count++
			bytes += len(value) + 64
		}
	}
	for _, alert := range entry.alerts {
		count++
		bytes += len(alert.AlertID) + len(alert.EventSourceID) + len(alert.Fingerprint) + len(alert.Severity) + 64
	}
	return count, bytes
}

// admitEntry is called with the cache lock held. Capacity refusal preserves
// the previous copy; a truncated set can never become an empty baseline.
func (cache *Cache) admitEntry(key StrategyKey, candidate *indexEntry) bool {
	count, bytes := entrySize(key, candidate)
	oldCount, oldBytes := entrySize(key, cache.index.entries[key])
	count += cache.index.members - oldCount
	bytes += cache.index.bytes - oldBytes
	if count > cache.index.options.MaxMembers || bytes > cache.index.options.MaxBytes {
		return false
	}
	cache.index.members, cache.index.bytes = count, bytes
	return true
}

func (cache *Cache) applyIndex(job indexJob, members []string, err error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.index.entries[job.key] != job.entry {
		return
	}
	entry := job.entry
	if err != nil {
		entry.reason = "index_read_failed"
		cache.refreshes["unavailable"]++
		cache.unavailable[UnavailableReadError]++
		return
	}
	next := *entry
	next.index = stringSet(members)
	if !cache.admitEntry(job.key, &next) {
		entry.reason = "capacity"
		cache.evictions++
		return
	}
	next.indexReadAt = cache.now()
	next.readGeneration = job.generation
	if next.dirty == job.generation {
		next.dirtySince = time.Time{}
	}
	next.reason = ""
	*entry = next
	cache.refreshes["index"]++
}

func (cache *Cache) applyCalibration(job indexJob, started time.Time, result Reconciliation, err error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.index.entries[job.key] != job.entry {
		return
	}
	entry := job.entry
	if err != nil {
		entry.reason = "calibration_failed"
		cache.refreshes["unavailable"]++
		cache.unavailable[UnavailableReadError]++
		return
	}
	next := *entry
	next.index = stringSet(result.Members)
	next.missing = stringSet(result.Missing)
	next.suppressed = stringSet(result.Suppressed)
	for value := range next.missing {
		delete(next.index, value)
	}
	for value := range next.suppressed {
		next.index[value] = struct{}{}
	}
	next.alerts = append([]Alert(nil), result.Alerts...)
	if !cache.admitEntry(job.key, &next) {
		entry.reason = "capacity"
		cache.evictions++
		return
	}
	next.calibratedAt = cache.now()
	next.calibratedStarted = started
	next.reconcileRequested = next.dirty != job.generation
	next.reason = ""
	*entry = next
	cache.index.eventSourceID = result.EventSourceID
	cache.refreshes["authoritative"]++
	// Once a newer complete calibration exists, aged local recoveries must
	// not permanently suppress an alert the consumer still holds active.
	for m, s := range cache.added {
		if m.key == job.key && s.at.Before(started) && cache.now().Sub(s.at) > cache.index.options.LocalRetention {
			delete(cache.added, m)
		}
	}
	for m, s := range cache.removed {
		if m.key == job.key && s.at.Before(started) && cache.now().Sub(s.at) > cache.index.options.LocalRetention {
			delete(cache.removed, m)
		}
	}
}

func (cache *Cache) calibrated(entry *indexEntry, now time.Time) bool {
	return entry != nil && !entry.calibratedAt.IsZero() && now.Sub(entry.calibratedAt) <= cache.index.options.CalibrationMaxAge
}

func (cache *Cache) indexContains(m member, now time.Time, count bool) bool {
	entry := cache.index.entries[m.key]
	answer := AnswerSelfMaintained
	present := false
	if entry != nil {
		_, present = entry.index[m.fingerprint]
		if _, missing := entry.missing[m.fingerprint]; missing {
			present = true
		}
		if cache.calibrated(entry, now) {
			if _, suppressed := entry.suppressed[m.fingerprint]; suppressed {
				present = false
			}
		} else {
			entry.reconcileRequested = true
		}
		if !entry.indexReadAt.IsZero() || !entry.calibratedAt.IsZero() {
			answer = AnswerIndexAbsent
			if present {
				answer = AnswerIndexMember
			}
		}
	}
	if added, ok := cache.added[m]; ok && (entry == nil || !cache.calibrated(entry, now) || now.Sub(added.at) <= cache.index.options.LocalRetention || !added.at.Before(entry.calibratedStarted)) {
		present = true
		answer = AnswerRecentlySent
	}
	if removed, ok := cache.removed[m]; ok {
		observed := time.Time{}
		if entry != nil {
			observed = entry.calibratedStarted
			if !cache.calibrated(entry, now) {
				observed = entry.indexReadAt
			}
		}
		if now.Sub(removed.at) <= cache.index.options.LocalRetention || !removed.at.Before(observed) {
			present = false
		}
	}
	if count && (entry == nil || !cache.calibrated(entry, now)) && cache.policy == PolicyPassThrough {
		present = true
		answer = AnswerPassedThrough
	}
	if count {
		cache.countLookup(answer)
	}
	return present
}

func (cache *Cache) Snapshot(key StrategyKey) StrategySnapshot {
	if cache == nil || cache.index == nil {
		return StrategySnapshot{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.index.entries[key]
	if entry == nil {
		return StrategySnapshot{Reason: "not_tracked"}
	}
	reason := entry.reason
	if reason == "" && !cache.calibrated(entry, cache.now()) {
		reason = "calibration_stale"
		if entry.calibratedAt.IsZero() {
			reason = "not_calibrated"
		}
	}
	return StrategySnapshot{IndexReadAt: entry.indexReadAt, CalibratedAt: entry.calibratedAt, Members: len(cache.indexMembers(key)), Loaded: !entry.indexReadAt.IsZero(), Calibrated: cache.calibrated(entry, cache.now()), Reason: reason}
}

func (cache *Cache) Members(key StrategyKey) []string {
	if cache == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.index != nil {
		return cache.indexMembers(key)
	}
	result := make([]string, 0, len(cache.sets[key]))
	for value := range cache.sets[key] {
		if cache.selfMaintainedOpen(member{key: key, fingerprint: value}) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (cache *Cache) indexMembers(key StrategyKey) []string {
	entry := cache.index.entries[key]
	if entry == nil {
		return nil
	}
	candidates := make(map[string]struct{}, len(entry.index)+len(entry.missing))
	for value := range entry.index {
		candidates[value] = struct{}{}
	}
	for value := range entry.missing {
		candidates[value] = struct{}{}
	}
	for m := range cache.added {
		if m.key == key {
			candidates[m.fingerprint] = struct{}{}
		}
	}
	result := make([]string, 0, len(candidates))
	for value := range candidates {
		if cache.indexContains(member{key: key, fingerprint: value}, cache.now(), false) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

// Holds answers, from memory and without counting a lookup, whether the
// strategy's set carries the fingerprint. judged is false whenever the
// answer would not be the link's: a copy that does not read the index, a
// subscription that is not ready, sets disjoint from this process's own
// sends (see DisjointMinimum), or a strategy whose set has no current
// calibration. A caller that acts on "not a member" must not act on an
// unjudged answer.
func (cache *Cache) Holds(key StrategyKey, fingerprint string) (held, judged bool) {
	if cache == nil || cache.index == nil {
		return false, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	entry := cache.index.entries[key]
	if !cache.index.ready || cache.index.disjoint || !cache.calibrated(entry, now) {
		return false, false
	}
	return cache.indexContains(member{key: key, fingerprint: fingerprint}, now, false), true
}

// MemberCount is how many fingerprints the strategy's set carries, judged
// as Holds is: judged is false wherever Holds would not answer. It is the
// question a caller asks once before asking Holds per fingerprint, so that
// a strategy with nothing open costs one lookup rather than one per record.
// A fingerprint this process sent and the set has not read yet is not
// counted; the next read of the set carries it.
func (cache *Cache) MemberCount(key StrategyKey) (count int, judged bool) {
	if cache == nil || cache.index == nil {
		return 0, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.index.entries[key]
	if !cache.index.ready || cache.index.disjoint || !cache.calibrated(entry, cache.now()) {
		return 0, false
	}
	return len(entry.index) + len(entry.missing), true
}

// Disjoint is whether the sets were last found to carry none of this
// process's own alerts (see DisjointMinimum), read without the full stats.
func (cache *Cache) Disjoint() bool {
	if cache == nil || cache.index == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.index.disjoint
}

// OwnEventSourceID is this deployment's source as the last successful
// calibration named it; empty until one has. An alert of another source is
// not this deployment's to close.
func (cache *Cache) OwnEventSourceID() string {
	if cache == nil || cache.index == nil {
		return ""
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.index.eventSourceID
}

func (cache *Cache) ActiveAlerts(key StrategyKey) []Alert {
	if cache == nil || cache.index == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.index.entries[key]
	if !cache.calibrated(entry, cache.now()) {
		return nil
	}
	result := make([]Alert, 0, len(entry.alerts))
	for _, alert := range entry.alerts {
		if cache.indexContains(member{key: key, fingerprint: alert.Fingerprint}, cache.now(), false) {
			result = append(result, alert)
		}
	}
	return result
}

func (cache *Cache) indexStats() Stats {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	stats := Stats{Mode: ModeSelfMaintained, IndexProtocol: true, CalibrationConfigured: cache.index.options.Reconciler != nil, Tracked: len(cache.index.entries), Added: len(cache.added), Removed: len(cache.removed), Evictions: cache.evictions,
		Refreshes: map[string]uint64{}, Unavailable: map[UnavailableReason]uint64{}, Lookups: map[Answer]uint64{}}
	all := len(cache.index.entries) > 0
	for _, entry := range cache.index.entries {
		if !entry.indexReadAt.IsZero() {
			stats.Loaded++
		}
		if entry.indexReadAt.IsZero() || cache.now().Sub(entry.indexReadAt) > cache.index.options.IndexInterval+cache.index.options.RefreshInterval {
			all = false
		}
		if cache.calibrated(entry, cache.now()) {
			stats.Calibrated++
		}
		if stats.LoadedAt.IsZero() || (!entry.calibratedAt.IsZero() && entry.calibratedAt.Before(stats.LoadedAt)) {
			stats.LoadedAt = entry.calibratedAt
		}
		stats.Members += len(entry.index)
		for value := range entry.missing {
			if _, exists := entry.index[value]; !exists {
				stats.Members++
			}
		}
		if cache.calibrated(entry, cache.now()) {
			for value := range entry.suppressed {
				if _, exists := entry.index[value]; exists {
					stats.Members--
				}
			}
		}
		if !entry.indexReadAt.IsZero() && (stats.IndexReadAt.IsZero() || entry.indexReadAt.Before(stats.IndexReadAt)) {
			stats.IndexReadAt = entry.indexReadAt
		}
		if entry.dirty != entry.readGeneration {
			stats.PendingReads++
			if stats.OldestPendingAt.IsZero() || entry.dirtySince.Before(stats.OldestPendingAt) {
				stats.OldestPendingAt = entry.dirtySince
			}
		}
		if cache.index.options.Reconciler != nil && (entry.reconcileRequested || !cache.calibrated(entry, cache.now())) {
			stats.PendingReconciles++
		}
		if entry.reason != "" {
			stats.UnavailableReason = UnavailableReadError
		}
	}
	stats.Available = all && cache.index.ready
	stats.SentInSet, stats.SentNotInSet, stats.Disjoint = cache.index.sentInSet, cache.index.sentNotInSet, cache.index.disjoint
	if cache.index.disjoint {
		stats.Available = false
		if stats.UnavailableReason == "" {
			stats.UnavailableReason = UnavailableMembersDisjoint
		}
	}
	stats.MemberBytes = cache.index.bytes
	stats.SubscriptionReady = cache.index.ready
	for k, v := range cache.refreshes {
		stats.Refreshes[k] = v
	}
	for k, v := range cache.unavailable {
		stats.Unavailable[k] = v
	}
	for k, v := range cache.lookups {
		stats.Lookups[k] = v
	}
	cache.gateStats(&stats)
	return stats
}
