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
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// UnavailableReason is why the latest read did not yield an authoritative
// publication. Closed: a metric label.
type UnavailableReason string

const (
	UnavailableReadError UnavailableReason = "read_error"
	// UnavailableLocationUnconfirmed: the Console has not named the place
	// this process reads the sets from - its discovery failed, the place it
	// names is one this process holds no connection to, or the last
	// reconciliation found it writing elsewhere. Nothing is read meanwhile.
	UnavailableLocationUnconfirmed UnavailableReason = "location_unconfirmed"
	// UnavailableKeyingUnconfirmed: the Console has not said that the link
	// keys this deployment's alerts by the alert id this process sends - its
	// event source was not read yet, keys by other fields, or is not in
	// effect. Every lookup by our alert id would miss.
	UnavailableKeyingUnconfirmed UnavailableReason = "keying_unconfirmed"
	// UnavailableTooLarge: a strategy's set is larger than one read may hold
	// (ErrSetTooLarge). Counted apart from a read that failed: it is the
	// trigger for reading such a set one member at a time.
	UnavailableTooLarge UnavailableReason = "too_large"
)

// UnavailableReasons lists every reason, for the metric that pre-creates
// them all: a reason at zero has to be readable as "never happened".
var UnavailableReasons = []UnavailableReason{UnavailableReadError, UnavailableLocationUnconfirmed, UnavailableKeyingUnconfirmed, UnavailableTooLarge}

// Answer is how a lookup was answered. Closed: a metric label. The index
// members and absences are the consumer's word; the rest say the copy
// answered on its own and why, so that a gate working from the copy's own
// knowledge shows up as such and not as the consumer's word.
type Answer string

const (
	// AnswerRecentlySent: the publication does not carry the fingerprint but
	// this process sent its ABNORMAL within the publisher's lag. Counted apart
	// from member because it is the copy's word, not the consumer's.
	AnswerRecentlySent Answer = "recently_sent"
	// AnswerSelfMaintained: the publication is unavailable and the copy
	// answered from what it last read plus what this process sent.
	AnswerSelfMaintained Answer = "self_maintained"
	// AnswerPassedThrough: the publication is unavailable and the policy is
	// to let every recovery go, as before the gate existed.
	AnswerPassedThrough Answer = "passed_through"
	AnswerIndexMember   Answer = "index_member"
	AnswerIndexAbsent   Answer = "index_absent"
	// AnswerIndexStale: the strategy's set was read once, and no read or
	// calibration has succeeded since for longer than the bound the refresh
	// cadence sets (indexState.staleAfter). The last read no longer answers;
	// the lookup is answered as for a set never read, by the unavailable
	// policy. Apart from self_maintained so a read outage is told from a
	// cold start.
	AnswerIndexStale Answer = "index_stale"
)

// Answers lists every Answer, for the metric that pre-creates them all.
var Answers = []Answer{AnswerRecentlySent, AnswerSelfMaintained, AnswerPassedThrough, AnswerIndexMember, AnswerIndexAbsent, AnswerIndexStale}

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
	// Configured is whether there is a Console to take the copy's facts
	// from; a copy without one reads nothing and is never unavailable.
	Configured        bool
	Available         bool
	UnavailableReason UnavailableReason
	// LocationConfirmed and KeyedByAlertID are the Console's two facts
	// (ConsoleFacts); KeyedByAlertID is nil until a read has answered, and
	// KeyedByAlertIDAsOf is when the last one did.
	LocationConfirmed  bool
	KeyedByAlertID     *bool
	KeyedByAlertIDAsOf time.Time
	// LoadedAt is the oldest calibration among the tracked sets; zero if none
	// has been calibrated. A metric derived from it must not be emitted while
	// zero.
	LoadedAt    time.Time
	Tracked     int
	Loaded      int
	Members     int
	Added       int
	Evictions   uint64
	Refreshes   map[string]uint64
	Unavailable map[UnavailableReason]uint64
	Lookups     map[Answer]uint64
	IndexReadAt time.Time
	// StaleAfter is the bound in force for a set's last successful read
	// (indexState.staleAfter), and Stale how many tracked sets are past it.
	StaleAfter                      time.Duration
	Stale                           int
	PendingReads, PendingReconciles int
	OldestPendingAt                 time.Time
	SubscriptionReady               bool
	// NoticesRefused is the change notices dropped, by why, since the copy
	// was built: counted here and not on the subscriber, which a move of the
	// link's location replaces.
	NoticesRefused map[NoticeRefusal]uint64
	MemberBytes    int
	// CalibrationConfigured says a reconciler is bound: without one the
	// index knows members but never their severity, so no close is ever
	// sent, and a deployment has to be able to read that as "off" rather
	// than wonder why nothing closes.
	CalibrationConfigured bool
	Calibrated            int
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
	// SentDepartures counts why alerts left Added since the process
	// started, every path in SentDepartures present. OwnOpen is how many
	// alerts this process opened that a trusted set has not shown closed --
	// the alerts the gate treats as its own -- and OwnOpenDepartures why
	// alerts left that record, every path in OwnOpenDepartures present; a
	// full record makes room by letting the stalest go (evicted).
	SentDepartures    map[string]uint64
	OwnOpen           int
	OwnOpenDepartures map[string]uint64
}

type member struct {
	key         StrategyKey
	fingerprint string
}

// stamped is when this process last sent an alert's ABNORMAL.
type stamped struct {
	at time.Time
}

// Cache is this process's copy of the consumer's open alert set. It
// implements contract.OpenAlertSet.
type Cache struct {
	index  *indexState
	mu     sync.Mutex
	now    func() time.Time
	policy UnavailablePolicy
	// maxLocal bounds the fingerprints kept from this process's own sends.
	// Past it the oldest is evicted and counted.
	maxLocal int

	added map[member]stamped

	evictions uint64
	// sentDepartures and openDepartures count why alerts left added and
	// index.opened. See departures.go.
	sentDepartures, openDepartures map[string]uint64
	refreshes                      map[string]uint64
	noticesRefused                 map[NoticeRefusal]uint64
	unavailable                    map[UnavailableReason]uint64
	lookups                        map[Answer]uint64
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

// Track registers strategies to read on the next refresh; see TrackOwned,
// whose refusal it does not report.
func (cache *Cache) Track(keys ...StrategyKey) {
	if cache == nil {
		return
	}
	_ = cache.TrackOwned(keys...)
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
	open := cache.indexGate(m, now)
	cache.recordGate(m, now, open)
	return open
}

// Acknowledged records what this process sent once the sink has taken it:
// an ABNORMAL opens the fingerprint in the copy - among what was sent
// recently, and in the record of what this process opened - and a RECOVERY
// only starts the grace after which that record lets the alert go
// (expireRecovered); it hides nothing from the gate. The consumer closes an alert only at
// the Level it stands at and takes any other RECOVERY as an orphan, which
// it records and otherwise ignores; whether the alert is still open is the
// set's word while the set is trusted, and the record of what this process
// opened answers for its own alerts while it is not (the 2026-09-14
// ruling). Only envelopes the consumer will see count: a
// compatibility-protocol envelope goes to another consumer, and one without
// a fingerprint opens nothing. Called after the broker ACK and never
// before, or a batch the sink refused would move the copy for alerts that
// were never opened.
func (cache *Cache) Acknowledged(events []contract.TriggerEventV1) {
	if cache == nil || len(events) == 0 {
		return
	}
	now := cache.now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	tracked := func(event contract.TriggerEventV1) (member, bool) {
		if event.DedupeMD5 == "" || event.StrategyRef == nil || event.LegacyOutput != nil {
			return member{}, false
		}
		m := member{key: StrategyKey{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}, fingerprint: event.DedupeMD5}
		return m, cache.index.entries[m.key] != nil
	}
	// Room for the batch's first sends is made once, in one pass over the
	// record, not once per alert under this lock.
	incoming := map[member]struct{}{}
	for _, event := range events {
		if m, ok := tracked(event); ok && event.EventKind == contract.TriggerEventAbnormal {
			if _, opened := cache.index.opened[m]; !opened {
				incoming[m] = struct{}{}
			}
		}
	}
	cache.makeRoomForOpened(len(incoming))
	for _, event := range events {
		m, ok := tracked(event)
		if !ok {
			continue
		}
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			cache.added[m] = stamped{at: now}
			cache.noteOpened(m, now)
		case contract.TriggerEventRecovery:
			cache.noteRecovered(m, now)
		}
	}
	cache.boundLocal()
}

// boundLocal keeps added inside maxLocal by evicting the oldest. Called with
// the lock held.
func (cache *Cache) boundLocal() {
	over := len(cache.added) - cache.maxLocal
	if over <= 0 {
		return
	}
	type aged struct {
		m  member
		at time.Time
	}
	all := make([]aged, 0, len(cache.added))
	for m, s := range cache.added {
		all = append(all, aged{m: m, at: s.at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, entry := range all[:over] {
		cache.leaveSent(entry.m, DepartureEvicted)
		cache.evictions++
	}
}

// Refresh runs one round of index reads, and of calibrations when a
// reconciler is bound, for the tracked strategies; see refreshIndex.
func (cache *Cache) Refresh(ctx context.Context) {
	if cache == nil {
		return
	}
	cache.refreshIndex(ctx)
}

// Stats reads the copy's state and cumulative counts.
func (cache *Cache) Stats() Stats {
	if cache == nil {
		return Stats{}
	}
	return cache.indexStats()
}

// StaleBeyondBound reports whether a set calibrated once has gone without a
// calibration for longer than CalibrationMaxAge. That is the shape fleet
// health degrades on: the gate is working from the copy's own knowledge past
// the exposure it was designed for. A set never calibrated is not stale: the
// reconciler may not be bound, and CalibrationConfigured says so on its own.
func (cache *Cache) StaleBeyondBound() bool {
	if cache == nil {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for _, entry := range cache.index.entries {
		if !entry.calibratedAt.IsZero() && !cache.calibrated(entry, cache.now()) {
			return true
		}
	}
	return false
}

var _ contract.OpenAlertSet = (*Cache)(nil)
