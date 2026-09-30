// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Mode is which of the three states the copy is in. The set is closed: a
// metric label is made of it.
type Mode string

const (
	// ModeNeverLoaded: no publication has been read since the process
	// started. Distinct from self-maintained so that a publisher that has
	// not been deployed reads as absent, not as lost.
	ModeNeverLoaded Mode = "never_loaded"
	// ModeAuthoritative: the last read was fresh and the copy answers from it.
	ModeAuthoritative Mode = "authoritative"
	// ModeSelfMaintained: a publication was read once, and the latest read
	// found it missing, stale, unreadable or under another algorithm; the
	// copy answers from the last one it read plus what this process sent.
	ModeSelfMaintained Mode = "self_maintained"
)

// Modes lists every Mode, for the metric that pre-creates all of them.
var Modes = []Mode{ModeNeverLoaded, ModeAuthoritative, ModeSelfMaintained}

// UnavailableReason is why the latest read did not yield an authoritative
// publication. Closed: a metric label.
type UnavailableReason string

const (
	UnavailableReadError           UnavailableReason = "read_error"
	UnavailableHeartbeatMissing    UnavailableReason = "heartbeat_missing"
	UnavailableHeartbeatUnreadable UnavailableReason = "heartbeat_unreadable"
	UnavailableHeartbeatStale      UnavailableReason = "heartbeat_stale"
	UnavailableFingerprintVersion  UnavailableReason = "fingerprint_version"
	// UnavailableMembersDisjoint: the consumer's sets hold members, or were
	// read, but none of the alerts this process sent ABNORMAL for is in
	// them once the consumer has had time to open it. The sets are then not
	// keyed the way this process asks, and every lookup would miss; see
	// DisjointMinimum.
	UnavailableMembersDisjoint UnavailableReason = "members_disjoint"
)

// UnavailableReasons lists every reason, for the metric that pre-creates
// them all: a reason at zero has to be readable as "never happened".
var UnavailableReasons = []UnavailableReason{
	UnavailableReadError, UnavailableHeartbeatMissing, UnavailableHeartbeatUnreadable,
	UnavailableHeartbeatStale, UnavailableFingerprintVersion, UnavailableMembersDisjoint,
}

// SentConfirmAfter is how long after this process first sent an alert's
// ABNORMAL a read of the consumer's set is expected to carry it. The
// consumer opens the alert on the message and rebuilds the set on a hint
// it batches for about a second; five minutes covers that and a slow
// rebuild several times over. An alert younger than this at the read is
// not counted either way.
const SentConfirmAfter = 5 * time.Minute

// DisjointMinimum is how many alerts this process sent, each past
// SentConfirmAfter at the latest read and none of them found, before the
// sets are taken to be keyed differently from this process's lookups.
//
// One is enough. An alert the consumer closed on its own can put a quiet
// deployment into the state wrongly, and the price of that is the gate as
// it was before it existed: a RECOVERY for an alert the consumer no longer
// holds, which it records as orphaned and changes nothing for. A higher
// bar would leave a deployment with one or two alerts outside the fallback
// for good, holding exactly the recoveries it exists to release.
//
// Leaving the state takes positive evidence only: an alert of ours found
// in a set, or nothing of ours left open. The count dropping does not end
// it, because the recoveries the fallback lets through are what make it
// drop; ending on that would hold the last few again against sets that
// still carry none of ours.
const DisjointMinimum = 1

// Answer is how a lookup was answered. Closed: a metric label. The first
// three are authoritative answers; the rest say the copy answered on its
// own and why, so that a gate working from the copy's own knowledge shows
// up as such and not as the consumer's word.
type Answer string

const (
	// AnswerMember: the publication carries the fingerprint.
	AnswerMember Answer = "authoritative_member"
	// AnswerAbsent: the publication is fresh, covers the strategy, and does
	// not carry the fingerprint; nor did this process send it recently.
	AnswerAbsent Answer = "authoritative_absent"
	// AnswerRecentlySent: the publication does not carry the fingerprint but
	// this process sent its ABNORMAL within the publisher's lag. Counted apart
	// from member because it is the copy's word, not the consumer's.
	AnswerRecentlySent Answer = "recently_sent"
	// AnswerNotYetLoaded: the publication is fresh but the strategy was first
	// asked about after the last read, so nothing is known about it until the
	// next; answered by the unavailable policy for this one cycle.
	AnswerNotYetLoaded Answer = "not_yet_loaded"
	// AnswerSelfMaintained: the publication is unavailable and the copy
	// answered from what it last read plus what this process sent.
	AnswerSelfMaintained Answer = "self_maintained"
	// AnswerPassedThrough: the publication is unavailable and the policy is
	// to let every recovery go, as before the gate existed.
	AnswerPassedThrough Answer = "passed_through"
	AnswerIndexMember   Answer = "index_member"
	AnswerIndexAbsent   Answer = "index_absent"
)

// Answers lists every Answer, for the metric that pre-creates them all.
var Answers = []Answer{AnswerMember, AnswerAbsent, AnswerRecentlySent, AnswerNotYetLoaded, AnswerSelfMaintained, AnswerPassedThrough, AnswerIndexMember, AnswerIndexAbsent}

// UnavailablePolicy is what the copy answers while the publication is
// unavailable. It is one decision point on purpose, because the two answers
// fail in opposite directions and which one a deployment wants is a ruling,
// not an implementation default.
//
// Self-maintain (the ruling in force, 2026-09-14): answer from the last
// publication plus what this process sent. It keeps the gate working through
// an outage, at the price of holding recoveries for alerts this process did
// not open itself (opened before it started, or by another worker before a
// rebalance) until the publication returns. Pass-through: let every recovery
// go, which is the behaviour before the gate; its price is the orphan
// resolutions the gate exists to stop, for the length of the outage. Neither
// holds everything: that would turn one dependency's outage into a platform
// where no alert resolves.
type UnavailablePolicy string

const (
	PolicySelfMaintain UnavailablePolicy = "self_maintain"
	PolicyPassThrough  UnavailablePolicy = "pass_through"
)

// Stats is the copy's state and cumulative counts, read for metrics.
type Stats struct {
	Mode              Mode
	Available         bool
	UnavailableReason UnavailableReason
	// LoadedAt is when the last authoritative publication was read; zero if
	// never. A metric derived from it must not be emitted while zero.
	LoadedAt                        time.Time
	Heartbeat                       Heartbeat
	Tracked                         int
	Loaded                          int
	Members                         int
	Added                           int
	Removed                         int
	Evictions                       uint64
	Refreshes                       map[string]uint64
	Unavailable                     map[UnavailableReason]uint64
	Lookups                         map[Answer]uint64
	IndexReadAt                     time.Time
	PendingReads, PendingReconciles int
	OldestPendingAt                 time.Time
	SubscriptionReady               bool
	MemberBytes                     int
	IndexProtocol                   bool
	// CalibrationConfigured says a reconciler is bound: without one the
	// index knows members but never their severity, so no close is ever
	// sent, and a deployment has to be able to read that as "off" rather
	// than wonder why nothing closes.
	CalibrationConfigured bool
	Calibrated            int
	// SentInSet and SentNotInSet split the alerts this process sent
	// ABNORMAL for, and has not sent RECOVERY for, by whether the latest
	// read of their strategy's set carries them. Only alerts first sent at
	// least SentConfirmAfter before that read count. Disjoint is the state
	// DisjointMinimum describes.
	SentInSet, SentNotInSet int
	Disjoint                bool
	// OwnLookups is Lookups for the lookups of this process's own open
	// alerts; OwnHeld how many of those the gate answered "not open", which
	// holds a RECOVERY whose alert stays open. RecentLookups and
	// RecentOwnHeld are the last RecentGateLookups of each, whole.
	OwnLookups    map[Answer]uint64
	OwnHeld       uint64
	RecentLookups []GateLookup
	RecentOwnHeld []GateLookup
	// GateSince is when the own split started: what this process sent is
	// held in memory and starts empty at every start, so an alert opened
	// before it is not "own" here, and "no own lookup" says only that none
	// of the alerts sent since reached the gate.
	GateSince time.Time
}

type member struct {
	key         StrategyKey
	fingerprint string
}

type stamped struct {
	at time.Time
}

// Cache is this process's copy of the consumer's open alert set. It
// implements contract.OpenAlertSet.
type Cache struct {
	index  *indexState
	mu     sync.Mutex
	source Source
	now    func() time.Time
	policy UnavailablePolicy
	// maxLocal bounds the fingerprints kept from this process's own sends.
	// Past it the oldest is evicted and counted: in self-maintained mode an
	// eviction is an alert whose recovery waits for the publication.
	maxLocal int
	// trackingWindow is how long a strategy stays in the read set after it
	// was last asked about. It has to outlast the longest evaluation
	// interval a strategy can have, or a slow strategy would leave the set
	// between two of its own evaluations and be "not yet loaded" at every
	// one; the price of a long window is reading a lost strategy's key for
	// that long after a rebalance, which is one SMEMBERS per cycle.
	trackingWindow time.Duration

	tracked   map[StrategyKey]time.Time
	loaded    map[StrategyKey]bool
	sets      map[StrategyKey]map[string]struct{}
	heartbeat Heartbeat
	loadedAt  time.Time
	available bool
	reason    UnavailableReason
	added     map[member]stamped
	removed   map[member]stamped

	evictions   uint64
	refreshes   map[string]uint64
	unavailable map[UnavailableReason]uint64
	lookups     map[Answer]uint64
	// lastAnswer, ownLookups, ownHeld and the two samples are the gate's
	// lookups as recordGate keeps them.
	lastAnswer    Answer
	ownLookups    map[Answer]uint64
	ownHeld       uint64
	recentLookups gateRing
	recentOwnHeld gateRing
	// gateSince is when this copy was made: the own split rests on what
	// this process sent (index.opened, added), which lives in memory and
	// starts empty with the process.
	gateSince time.Time
}

// Options configure a Cache. Zero values take the defaults below.
type Options struct {
	Source Source
	Now    func() time.Time
	Policy UnavailablePolicy
	// MaxLocalEntries bounds added plus removed; default 1<<18.
	MaxLocalEntries int
	// TrackingWindow: default 2 hours; see Cache.trackingWindow.
	TrackingWindow time.Duration
}

func New(options Options) (*Cache, error) {
	if options.Source == nil {
		return nil, errors.New("alarmd openalerts: a source is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	switch options.Policy {
	case "":
		options.Policy = PolicySelfMaintain
	case PolicySelfMaintain, PolicyPassThrough:
	default:
		return nil, errors.New("alarmd openalerts: unknown unavailable policy " + string(options.Policy))
	}
	if options.MaxLocalEntries <= 0 {
		options.MaxLocalEntries = 1 << 18
	}
	if options.TrackingWindow <= 0 {
		options.TrackingWindow = 2 * time.Hour
	}
	return &Cache{
		source: options.Source, now: options.Now, policy: options.Policy,
		maxLocal: options.MaxLocalEntries, trackingWindow: options.TrackingWindow,
		tracked: map[StrategyKey]time.Time{}, loaded: map[StrategyKey]bool{}, sets: map[StrategyKey]map[string]struct{}{},
		added: map[member]stamped{}, removed: map[member]stamped{},
		refreshes: map[string]uint64{}, unavailable: map[UnavailableReason]uint64{}, lookups: map[Answer]uint64{}, ownLookups: map[Answer]uint64{},
		gateSince: options.Now(),
	}, nil
}

// Track registers strategies to read on the next refresh, so that the first
// lookup about them does not land on a cycle nothing is known about. A
// lookup tracks its strategy as well; this only moves that forward.
func (cache *Cache) Track(keys ...StrategyKey) {
	if cache == nil {
		return
	}
	if cache.index != nil {
		_ = cache.TrackOwned(keys...)
		return
	}
	now := cache.now()
	cache.mu.Lock()
	for _, key := range keys {
		cache.tracked[key] = now
	}
	cache.mu.Unlock()
}

// Contains implements contract.OpenAlertSet. See Answer for how it answers.
// Every answer is also recorded for reading (see recordGate).
func (cache *Cache) Contains(tenantID, strategyID, fingerprint string) bool {
	if cache == nil {
		return false
	}
	key := StrategyKey{TenantID: tenantID, StrategyID: strategyID}
	m := member{key: key, fingerprint: fingerprint}
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	open := cache.contains(m, now)
	cache.recordGate(m, now, open)
	return open
}

// contains is Contains with the lock held.
func (cache *Cache) contains(m member, now time.Time) bool {
	key, fingerprint := m.key, m.fingerprint
	if cache.index != nil {
		return cache.indexGate(m, now)
	}
	cache.tracked[key] = now
	if cache.available && cache.loaded[key] {
		if _, ok := cache.sets[key][fingerprint]; ok {
			cache.countLookup(AnswerMember)
			return true
		}
		if sent, ok := cache.added[m]; ok && now.Sub(sent.at) <= cache.localRetention() && !cache.removedAfter(m, sent.at) {
			cache.countLookup(AnswerRecentlySent)
			return true
		}
		cache.countLookup(AnswerAbsent)
		return false
	}
	if cache.available {
		cache.countLookup(AnswerNotYetLoaded)
	}
	return cache.answerUnavailable(m)
}

// answerUnavailable is the one decision point for an unavailable
// publication; see UnavailablePolicy. Called with the lock held.
func (cache *Cache) answerUnavailable(m member) bool {
	switch cache.policy {
	case PolicyPassThrough:
		cache.countLookup(AnswerPassedThrough)
		return true
	default:
		cache.countLookup(AnswerSelfMaintained)
		return cache.selfMaintainedOpen(m)
	}
}

// selfMaintainedOpen is the last publication plus what this process sent:
// a member of the last read set is open unless this process sent its
// RECOVERY after that read; a fingerprint this process sent ABNORMAL for is
// open unless it sent the RECOVERY after that.
func (cache *Cache) selfMaintainedOpen(m member) bool {
	if sent, ok := cache.added[m]; ok {
		return !cache.removedAfter(m, sent.at)
	}
	if _, ok := cache.sets[m.key][m.fingerprint]; ok {
		return !cache.removedAfter(m, cache.loadedAt)
	}
	return false
}

func (cache *Cache) removedAfter(m member, at time.Time) bool {
	removed, ok := cache.removed[m]
	return ok && !removed.at.Before(at)
}

// localRetention is the publisher's lag as far as this copy knows it. It
// is only consulted with an authoritative publication in hand, so the cycle
// is known; the fallback exists for the type's sake, not for a path.
func (cache *Cache) localRetention() time.Duration {
	if cache.index != nil {
		return cache.index.options.LocalRetention
	}
	if cache.heartbeat.Cycle > 0 {
		return LocalRetentionCycles * cache.heartbeat.Cycle
	}
	return LocalRetentionCycles * time.Minute
}

// Acknowledged records what this process sent once the sink has taken it:
// an ABNORMAL opens the fingerprint in the copy, a RECOVERY closes it. Only
// envelopes the consumer will see count: a compatibility-protocol envelope
// goes to another consumer, and one without a fingerprint opens nothing.
// Called after the broker ACK and never before, or a batch the sink refused
// would move the copy for alerts that were never opened.
func (cache *Cache) Acknowledged(events []contract.TriggerEventV1) {
	if cache == nil || len(events) == 0 {
		return
	}
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, event := range events {
		if event.DedupeMD5 == "" || event.StrategyRef == nil || event.LegacyOutput != nil {
			continue
		}
		m := member{key: StrategyKey{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}, fingerprint: event.DedupeMD5}
		if cache.index != nil && cache.index.entries[m.key] == nil {
			continue
		}
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			cache.added[m] = stamped{at: now}
			delete(cache.removed, m)
			cache.noteOpened(m, now)
		case contract.TriggerEventRecovery:
			cache.removed[m] = stamped{at: now}
			delete(cache.added, m)
			if cache.index != nil {
				delete(cache.index.opened, m)
			}
		}
	}
	cache.boundLocal()
}

// boundLocal keeps added plus removed inside maxLocal by evicting the
// oldest. Called with the lock held.
func (cache *Cache) boundLocal() {
	over := len(cache.added) + len(cache.removed) - cache.maxLocal
	if over <= 0 {
		return
	}
	type aged struct {
		m       member
		at      time.Time
		removed bool
	}
	all := make([]aged, 0, len(cache.added)+len(cache.removed))
	for m, s := range cache.added {
		all = append(all, aged{m: m, at: s.at})
	}
	for m, s := range cache.removed {
		all = append(all, aged{m: m, at: s.at, removed: true})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, entry := range all[:over] {
		if entry.removed {
			delete(cache.removed, entry.m)
		} else {
			delete(cache.added, entry.m)
		}
		cache.evictions++
	}
}

// Refresh reads the publication for the tracked strategies and decides the
// copy's state from it. It is meant to run once per publisher cycle. A read
// that yields no authoritative publication leaves the previous sets in
// place, which is what self-maintained mode answers from.
func (cache *Cache) Refresh(ctx context.Context) {
	if cache == nil {
		return
	}
	if cache.index != nil {
		cache.refreshIndex(ctx)
		return
	}
	keys := cache.readSet()
	publication, err := cache.source.Read(ctx, keys)
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	switch {
	case err != nil:
		cache.becomeUnavailable(UnavailableReadError)
	case publication.HeartbeatErr != nil:
		cache.becomeUnavailable(UnavailableHeartbeatUnreadable)
	case publication.Heartbeat == nil:
		cache.becomeUnavailable(UnavailableHeartbeatMissing)
	case publication.Heartbeat.FingerprintVersion != FingerprintVersion:
		cache.becomeUnavailable(UnavailableFingerprintVersion)
	case now.Sub(publication.Heartbeat.PublishedAt) > StalenessCycles*publication.Heartbeat.Cycle:
		cache.becomeUnavailable(UnavailableHeartbeatStale)
	default:
		cache.becomeAuthoritative(now, *publication.Heartbeat, keys, publication.Sets)
	}
}

// readSet is the strategies asked about within the tracking window, in a
// stable order. Called without the lock.
func (cache *Cache) readSet() []StrategyKey {
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	keys := make([]StrategyKey, 0, len(cache.tracked))
	for key, at := range cache.tracked {
		if now.Sub(at) > cache.trackingWindow {
			delete(cache.tracked, key)
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TenantID != keys[j].TenantID {
			return keys[i].TenantID < keys[j].TenantID
		}
		return keys[i].StrategyID < keys[j].StrategyID
	})
	return keys
}

// Called with the lock held.
func (cache *Cache) becomeUnavailable(reason UnavailableReason) {
	cache.available = false
	cache.reason = reason
	cache.unavailable[reason]++
	cache.refreshes["unavailable"]++
}

// becomeAuthoritative replaces the copy with the publication for the
// strategies read, and keeps of this process's own sends only those inside
// the publisher's lag: older ones are either in the publication or closed
// at the consumer, and either way the publication is the word to go by.
// Called with the lock held.
func (cache *Cache) becomeAuthoritative(now time.Time, heartbeat Heartbeat, keys []StrategyKey, sets map[StrategyKey][]string) {
	cache.available = true
	cache.reason = ""
	cache.heartbeat = heartbeat
	cache.loadedAt = now
	cache.refreshes["authoritative"]++
	cache.loaded = make(map[StrategyKey]bool, len(keys))
	cache.sets = make(map[StrategyKey]map[string]struct{}, len(sets))
	for _, key := range keys {
		cache.loaded[key] = true
	}
	for key, members := range sets {
		set := make(map[string]struct{}, len(members))
		for _, fingerprint := range members {
			set[fingerprint] = struct{}{}
		}
		cache.sets[key] = set
	}
	retention := LocalRetentionCycles * heartbeat.Cycle
	for m, s := range cache.added {
		if now.Sub(s.at) > retention {
			delete(cache.added, m)
		}
	}
	for m, s := range cache.removed {
		if now.Sub(s.at) > retention {
			delete(cache.removed, m)
		}
	}
}

// Stats reads the copy's state and cumulative counts.
func (cache *Cache) Stats() Stats {
	if cache == nil {
		return Stats{}
	}
	if cache.index != nil {
		return cache.indexStats()
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	stats := Stats{
		Mode: ModeNeverLoaded, Available: cache.available, UnavailableReason: cache.reason,
		LoadedAt: cache.loadedAt, Heartbeat: cache.heartbeat,
		Tracked: len(cache.tracked), Loaded: len(cache.loaded), Added: len(cache.added), Removed: len(cache.removed),
		Evictions: cache.evictions,
		Refreshes: make(map[string]uint64, len(cache.refreshes)), Unavailable: make(map[UnavailableReason]uint64, len(cache.unavailable)),
		Lookups: make(map[Answer]uint64, len(cache.lookups)),
	}
	switch {
	case cache.available:
		stats.Mode = ModeAuthoritative
	case !cache.loadedAt.IsZero():
		stats.Mode = ModeSelfMaintained
	}
	for _, set := range cache.sets {
		stats.Members += len(set)
	}
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

// StaleBeyondBound reports whether the copy has been without an
// authoritative publication for longer than the staleness bound after
// having had one. That is the shape fleet health degrades on: the gate is
// working from the copy's own knowledge past the exposure it was designed
// for. A copy that never loaded is not stale: the publisher may not be
// deployed, and the Mode says so on its own.
func (cache *Cache) StaleBeyondBound() bool {
	if cache == nil {
		return false
	}
	if cache.index != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		for _, entry := range cache.index.entries {
			if !entry.calibratedAt.IsZero() && !cache.calibrated(entry, cache.now()) {
				return true
			}
		}
		return false
	}
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.available || cache.loadedAt.IsZero() || cache.heartbeat.Cycle <= 0 {
		return false
	}
	return now.Sub(cache.loadedAt) > StalenessCycles*cache.heartbeat.Cycle
}

var _ contract.OpenAlertSet = (*Cache)(nil)
