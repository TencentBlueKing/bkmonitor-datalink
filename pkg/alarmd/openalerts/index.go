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
	// Facts are the link Console's facts the sets are trusted on (see
	// ConsoleFacts). Nil is a deployment without the Console: the copy
	// reads nothing and is not configured.
	Facts ConsoleFacts
}

type StrategySnapshot struct {
	IndexReadAt, CalibratedAt time.Time
	Members                   int
	Loaded, Calibrated        bool
	Reason                    string
	// Stale says the set's last successful read, index or calibration, is
	// older than StaleAfter, the bound in force for this copy: it no longer
	// answers lookups (AnswerIndexStale).
	Stale      bool
	StaleAfter time.Duration
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
	// calibration named it.
	eventSourceID string
	// opened is the record of the alerts this process opened, until a
	// trusted set shows one closed or its recovery has been sent for a grace
	// period (pruneOpened, expireRecovered). Kept apart from Cache.added,
	// which a calibration prunes after the local retention: the fallback
	// would otherwise forget the alerts it exists to let recover. Bounded by
	// MaxLocalEntries; at it a new alert takes the place of the one whose
	// last ABNORMAL is oldest, counted as evicted (evictStalestOpened).
	opened map[member]ownRecord
	// lastUnconfirmed is the unconfirmed reason the last round found, so an
	// entry into the state is counted once (noteConfirmation).
	lastUnconfirmed UnavailableReason
}

// ownRecord is one alert this process opened: when it first and last sent
// its ABNORMAL, and when the first RECOVERY the broker took since that last
// ABNORMAL was; zero while none has been.
type ownRecord struct {
	first, lastSent, recoveredAt time.Time
}

// noteOpened records an ABNORMAL of an alert: its first send, and on every
// send the latest, which also clears a recovery sent before it. Called with
// the lock held.
func (cache *Cache) noteOpened(m member, now time.Time) {
	if record, ok := cache.index.opened[m]; ok {
		record.lastSent, record.recoveredAt = now, time.Time{}
		cache.index.opened[m] = record
		return
	}
	if len(cache.index.opened) >= cache.index.options.MaxLocalEntries {
		// A batch of first sends larger than the whole record: the batch
		// made room for what fits (makeRoomForOpened), the rest take the
		// place of the stalest one at a time.
		cache.evictStalestOpened()
	}
	cache.index.opened[m] = ownRecord{first: now, lastSent: now}
}

// staler is the order a full record lets alerts go in to make room for new
// ones. An alert whose series stopped being evaluated - its target gone, its
// dimension value gone, its fingerprint changed by an edit - never has a
// RECOVERY decided, so neither the grace nor an untrusted set ever takes it
// out; refusing new alerts instead would hold the recovery of every alert
// opened after the record filled. So: an alert whose RECOVERY was already
// sent goes first, then the one whose last ABNORMAL is oldest - an alert
// still firing is sent every round, one recovering within its window was
// sent at most that window ago. The envelope carries no period, so
// staleness is judged in time, not in periods: in a lasting untrusted state
// with the record full, a long-period strategy's alert still firing, sent
// once a period, can be older than records short-period strategies left
// behind, and be the one let go; its RECOVERY is then held until the sets
// are trusted again. A trusted set never reads this record.
func staler(a, b ownRecord) bool {
	if a.recoveredAt.IsZero() != b.recoveredAt.IsZero() {
		return !a.recoveredAt.IsZero()
	}
	return a.lastSent.Before(b.lastSent)
}

// makeRoomForOpened lets go, in one pass over the record, of as many of the
// stalest alerts as incoming first sends need, each counted as evicted.
// Called with the lock held, once per acknowledged batch.
func (cache *Cache) makeRoomForOpened(incoming int) {
	need := len(cache.index.opened) + incoming - cache.index.options.MaxLocalEntries
	if need <= 0 {
		return
	}
	type held struct {
		m      member
		record ownRecord
	}
	all := make([]held, 0, len(cache.index.opened))
	for m, record := range cache.index.opened {
		all = append(all, held{m: m, record: record})
	}
	sort.Slice(all, func(i, j int) bool { return staler(all[i].record, all[j].record) })
	for _, h := range all[:min(need, len(all))] {
		cache.evictions++
		cache.leaveOpen(h.m, DepartureEvicted)
	}
}

// evictStalestOpened lets the stalest alert go to make room for one more.
// Called with the lock held, only past what makeRoomForOpened made room for.
func (cache *Cache) evictStalestOpened() {
	var stalest member
	var at ownRecord
	found := false
	for m, record := range cache.index.opened {
		if !found || staler(record, at) {
			stalest, at, found = m, record, true
		}
	}
	if found {
		cache.evictions++
		cache.leaveOpen(stalest, DepartureEvicted)
	}
}

// noteRecovered records the first RECOVERY the broker took for an alert
// since its last ABNORMAL. It hides nothing from the gate; it starts the
// grace after which the record lets the alert go (expireRecovered). Called
// with the lock held.
func (cache *Cache) noteRecovered(m member, now time.Time) {
	if record, ok := cache.index.opened[m]; ok && record.recoveredAt.IsZero() {
		record.recoveredAt = now
		cache.index.opened[m] = record
	}
}

// expireRecovered lets go of the alerts whose recovery was sent a grace
// period ago with no ABNORMAL since, whether or not the sets are trusted.
// The consumer has had every round of the grace to close the alert at its
// Level, and an untrusted state can last - an event source keyed by other
// fields stays so - where a record that only grows would end by refusing
// every new alert and sending an orphan recovery for each old one every
// round. The grace is the calibration interval, the timescale the trusted
// path prunes on. Called with the lock held, once per refresh round.
func (cache *Cache) expireRecovered(now time.Time) {
	grace := cache.index.options.ReconcileInterval
	for m, record := range cache.index.opened {
		if !record.recoveredAt.IsZero() && now.Sub(record.recoveredAt) > grace {
			cache.leaveOpen(m, DepartureRecovered)
		}
	}
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
		added:     map[member]stamped{},
		refreshes: map[string]uint64{}, noticesRefused: map[NoticeRefusal]uint64{}, unavailable: map[UnavailableReason]uint64{}, lookups: map[Answer]uint64{}, ownLookups: map[Answer]uint64{},
		gateSince: options.Now(),
		index:     &indexState{options: options, entries: map[StrategyKey]*indexEntry{}, wake: make(chan struct{}, 1), opened: map[member]ownRecord{}},
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
			cache.leaveSent(m, DepartureUntracked)
		}
	}
	for m := range cache.index.opened {
		if _, exists := unique[m.key]; !exists {
			cache.leaveOpen(m, DepartureUntracked)
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
		if entry := cache.index.entries[key]; entry != nil {
			c, b := entrySize(key, entry)
			cache.index.members -= c
			cache.index.bytes -= b
		}
		delete(cache.index.entries, key)
		for m := range cache.added {
			if m.key == key {
				cache.leaveSent(m, DepartureUntracked)
			}
		}
		for m := range cache.index.opened {
			if m.key == key {
				cache.leaveOpen(m, DepartureUntracked)
			}
		}
	}
	order := cache.index.order[:0]
	for _, key := range cache.index.order {
		if cache.index.entries[key] != nil {
			order = append(order, key)
		}
	}
	cache.index.order = order
}

func (cache *Cache) RequestReconcile(key StrategyKey) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.index.entries[key]; entry != nil {
		entry.reconcileRequested = true
	}
}

// RequestCalibration asks for every tracked strategy to be calibrated at the
// next round rather than when its interval comes: after the reads moved to
// where the Console names, its confirmation is a calibration away, and
// waiting a whole interval would hold the recoveries of every alert this
// process did not open for that long.
func (cache *Cache) RequestCalibration() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	for _, entry := range cache.index.entries {
		entry.reconcileRequested = true
		entry.lastReconcileAttempt = time.Time{}
	}
	cache.mu.Unlock()
	cache.wakeIndex()
}

// LocationMoved forgets everything read where the sets were read before -
// every strategy's members, calibration and its time - and asks for a
// calibration at once, as RequestCalibration does. Called when the reads
// move to another place: what the old place held says nothing about the new
// one, so each strategy answers as never read until it is read again there,
// and a read still in flight from the old place lands on an entry that is
// no longer there and is dropped.
func (cache *Cache) LocationMoved() {
	if cache == nil {
		return
	}
	now := cache.now()
	cache.mu.Lock()
	members, bytes := 0, 0
	for key := range cache.index.entries {
		entry := &indexEntry{dirty: 1, dirtySince: now, reconcileRequested: true}
		cache.index.entries[key] = entry
		count, size := entrySize(key, entry)
		members, bytes = members+count, bytes+size
	}
	cache.index.members, cache.index.bytes = members, bytes
	cache.mu.Unlock()
	cache.wakeIndex()
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

// indexRefused counts a change notice the subscriber dropped. The strategy
// it named, if any, is read on the next periodic read.
func (cache *Cache) indexRefused(reason NoticeRefusal) {
	cache.mu.Lock()
	cache.noticesRefused[reason]++
	cache.mu.Unlock()
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
	if cache == nil {
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
			_ = cache.index.options.Subscriber.Watch(ctx, cache.indexReady, cache.indexChanged, cache.indexRefused)
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
	// One background round at a time, even if an external maintenance hook
	// also calls Refresh. Contending callers do no I/O and never build a queue.
	if !state.refreshMu.TryLock() {
		return
	}
	defer state.refreshMu.Unlock()
	// The Console's facts first, whether or not any strategy is tracked or
	// due for calibration; the facts bound their own reads.
	if facts := state.options.Facts; facts != nil {
		factsCtx, cancel := context.WithTimeout(ctx, state.options.CycleTimeout/2)
		facts.Refresh(factsCtx)
		cancel()
	}
	cache.mu.Lock()
	cache.noteConfirmation()
	// The sets are read only where the Console named them and once their
	// change notices are subscribed. Calibration does not wait on either:
	// it is how the Console confirms the location, or names another.
	reads := state.everReady && cache.configured() && cache.unconfirmed() != UnavailableLocationUnconfirmed
	cache.mu.Unlock()
	// Reserve half the cycle for each dependency; slow Redis cannot starve
	// reconciliation (or vice versa). Calls are serial, never four per worker.
	for _, calibration := range []bool{false, true} {
		if calibration && state.options.Reconciler == nil || !calibration && !reads {
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
	cache.noteConfirmation()
	cache.expireRecovered(cache.now())
	cache.mu.Unlock()
}

// indexGate answers the recovery gate. While the sets are not trusted - the
// Console has not confirmed where they are or that they are keyed by our
// alert ids, or there is no Console - what they hold says nothing about the
// series, and the gate answers by the policy in force, which by default is
// what this process sent (the 2026-09-14 ruling). Called with the lock held.
func (cache *Cache) indexGate(m member, now time.Time) bool {
	if cache.trusted() {
		return cache.indexContains(m, now, true)
	}
	if cache.policy == PolicyPassThrough {
		cache.countLookup(AnswerPassedThrough)
		return true
	}
	cache.countLookup(AnswerSelfMaintained)
	if _, opened := cache.index.opened[m]; opened {
		return true
	}
	// A recent send still counts when its record was let go to make room:
	// the recent sends bridge the consumer's lag, not an outage.
	if sent, ok := cache.added[m]; ok && now.Sub(sent.at) <= cache.index.options.LocalRetention {
		return true
	}
	return false
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
		cache.refreshes["unavailable"]++
		if errors.Is(err, ErrSetTooLarge) {
			entry.reason = "index_too_large"
			cache.unavailable[UnavailableTooLarge]++
			return
		}
		entry.reason = "index_read_failed"
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
	// The calibration resolved the target first: a move it found, and the
	// agreement a later one finds, can open and close an unconfirmed state
	// inside one round, which is still an entry.
	cache.noteConfirmation()
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
			cache.leaveSent(m, DepartureNotResent)
		}
	}
	cache.pruneOpened(job.key, entry, started)
}

// pruneOpened takes out of the record of what this process opened the
// alerts of key that a trusted set's calibration lists nowhere - not as a
// member, not as missing from the set, not as suppressed - and whose last
// ABNORMAL was sent more than the local retention before the calibration
// began: the consumer closed them, at whatever Level and for whatever
// reason. A just-sent alert may be one the consumer had not published when
// the calibration read it. An untrusted set says nothing about our alerts,
// and prunes nothing. Called with the lock held, after a calibration that
// succeeded (and so passed the Console's location check).
func (cache *Cache) pruneOpened(key StrategyKey, entry *indexEntry, started time.Time) {
	if !cache.trusted() {
		return
	}
	for m, record := range cache.index.opened {
		if m.key != key || started.Sub(record.lastSent) <= cache.index.options.LocalRetention {
			continue
		}
		// A suppressed alert is a member here: the calibration puts it in
		// the index. A missing one is not, and is kept all the same.
		_, member := entry.index[m.fingerprint]
		_, missing := entry.missing[m.fingerprint]
		if member || missing {
			continue
		}
		cache.leaveOpen(m, DepartureNotInSet)
	}
}

// staleAfter is how old a set's last successful read may be before it stops
// answering: three times the longest a key waits between two reads -
// IndexInterval, plus one pass over every tracked key at ReadBatch keys a
// cycle of RefreshInterval. It grows with the keys tracked, so a copy with
// more keys tolerates a longer gap; a few cycles of failed reads, not a
// configured age.
func (state *indexState) staleAfter() time.Duration {
	batch := max(1, state.options.ReadBatch)
	cycles := (len(state.order) + batch - 1) / batch
	return 3 * (state.options.IndexInterval + time.Duration(cycles)*state.options.RefreshInterval)
}

// stale is a set read once whose last successful read, index or
// calibration, is older than the bound. A set never read is not stale: it is
// a set not read yet.
func (cache *Cache) stale(entry *indexEntry, now time.Time) bool {
	last := entry.indexReadAt
	if entry.calibratedAt.After(last) {
		last = entry.calibratedAt
	}
	return !last.IsZero() && now.Sub(last) > cache.index.staleAfter()
}

func (cache *Cache) calibrated(entry *indexEntry, now time.Time) bool {
	return entry != nil && !entry.calibratedAt.IsZero() && now.Sub(entry.calibratedAt) <= cache.index.options.CalibrationMaxAge
}

func (cache *Cache) indexContains(m member, now time.Time, count bool) bool {
	entry := cache.index.entries[m.key]
	answer := AnswerSelfMaintained
	present := false
	if entry != nil {
		if !cache.calibrated(entry, now) {
			entry.reconcileRequested = true
		}
		switch {
		case cache.stale(entry, now):
			// Past the bound the last read says nothing about now: answered
			// below as a set never read is, by the unavailable policy.
			answer = AnswerIndexStale
			entry = nil
		case !entry.indexReadAt.IsZero() || !entry.calibratedAt.IsZero():
			present = cache.setCarries(entry, m.fingerprint, now)
			answer = AnswerIndexAbsent
			if present {
				answer = AnswerIndexMember
			}
		default:
			present = cache.setCarries(entry, m.fingerprint, now)
		}
	}
	if added, ok := cache.added[m]; ok && (entry == nil || !cache.calibrated(entry, now) || now.Sub(added.at) <= cache.index.options.LocalRetention || !added.at.Before(entry.calibratedStarted)) {
		present = true
		answer = AnswerRecentlySent
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
	if cache == nil {
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
	stale := cache.stale(entry, cache.now())
	if stale {
		reason = "index_stale"
	}
	return StrategySnapshot{IndexReadAt: entry.indexReadAt, CalibratedAt: entry.calibratedAt, Members: len(cache.indexMembers(key)), Loaded: !entry.indexReadAt.IsZero(), Calibrated: cache.calibrated(entry, cache.now()), Reason: reason,
		Stale: stale, StaleAfter: cache.index.staleAfter()}
}

func (cache *Cache) Members(key StrategyKey) []string {
	if cache == nil {
		return nil
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.indexMembers(key)
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
// subscription that is not ready, sets the Console has not confirmed
// (ConsoleFacts), or a strategy whose set has no current calibration. A caller that acts on "not a member" must not act on an
// unjudged answer.
func (cache *Cache) Holds(key StrategyKey, fingerprint string) (held, judged bool) {
	if cache == nil {
		return false, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	entry := cache.index.entries[key]
	if !cache.index.ready || !cache.trusted() || !cache.calibrated(entry, now) {
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
	if cache == nil {
		return 0, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry := cache.index.entries[key]
	if !cache.index.ready || !cache.trusted() || !cache.calibrated(entry, cache.now()) {
		return 0, false
	}
	return len(entry.index) + len(entry.missing), true
}

// Trusted is whether the sets may answer for this process: a Console is
// configured and has confirmed both where the sets are and that they are
// keyed by our alert ids (ConsoleFacts), read without the full stats.
func (cache *Cache) Trusted() bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.trusted()
}

// OwnEventSourceID is this deployment's source as the last successful
// calibration named it; empty until one has. An alert of another source is
// not this deployment's to close.
func (cache *Cache) OwnEventSourceID() string {
	if cache == nil {
		return ""
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.index.eventSourceID
}

func (cache *Cache) ActiveAlerts(key StrategyKey) []Alert {
	if cache == nil {
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
	stats := Stats{CalibrationConfigured: cache.index.options.Reconciler != nil, Tracked: len(cache.index.entries), Added: len(cache.added), Evictions: cache.evictions,
		Refreshes: map[string]uint64{}, Unavailable: map[UnavailableReason]uint64{}, Lookups: map[Answer]uint64{},
		StaleAfter: cache.index.staleAfter()}
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
		if cache.stale(entry, cache.now()) {
			stats.Stale++
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
	stats.Configured = cache.configured()
	stats.Available = all && cache.index.ready && cache.trusted()
	if reason := cache.unconfirmed(); reason != "" {
		// The Console's word outranks a read error: it says whether the
		// reads could answer at all.
		stats.UnavailableReason = reason
	}
	if facts := cache.index.options.Facts; facts != nil {
		stats.LocationConfirmed = facts.LocationConfirmed()
		if keyed, asOf, known := facts.KeyedByAlertID(); known {
			stats.KeyedByAlertID, stats.KeyedByAlertIDAsOf = &keyed, asOf
		}
	}
	stats.MemberBytes = cache.index.bytes
	stats.SubscriptionReady = cache.index.ready
	for k, v := range cache.refreshes {
		stats.Refreshes[k] = v
	}
	for k, v := range cache.noticesRefused {
		if stats.NoticesRefused == nil {
			stats.NoticesRefused = map[NoticeRefusal]uint64{}
		}
		stats.NoticesRefused[k] = v
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

// setCarries is the consumer's own word on the fingerprint as last read:
// in the set or found active by a calibration, and not found inactive by a
// current one. Called with the lock held.
func (cache *Cache) setCarries(entry *indexEntry, fingerprint string, now time.Time) bool {
	_, present := entry.index[fingerprint]
	if _, missing := entry.missing[fingerprint]; missing {
		present = true
	}
	if cache.calibrated(entry, now) {
		if _, suppressed := entry.suppressed[fingerprint]; suppressed {
			present = false
		}
	}
	return present
}
