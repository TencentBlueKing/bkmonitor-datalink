// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package scopeclose closes the alerts of series whose alert identity
// moved: a host whose alerts were filed under the fingerprint of the
// record's own address before its identity read CMDB's (reason
// superseded_identity). The series goes on under its new alert, so nothing
// this process evaluates will ever end the old one, and the alert link only
// moves an alert on an event.
//
// The admission step offers the old fingerprint of every admitted series
// whose host overlay moves it, on every Slot (Offer). The one fact that
// makes an offer actionable is that the old fingerprint is an open alert
// of this deployment's own, as alarmd's copy of the link's active alerts
// already knows it (activecopy): a send of its own, or a current search
// hit. An offer the copy has no word on, or judges not open, is not this
// close's, and counts nothing.
//
// What has to hold before anything is closed, each named when it does not:
//
//   - two different Slots offered the same fingerprint. One Slot is a first
//     observation, unconfirmed; the second makes it a close;
//   - the last of them is fresh when the close is run (Freshness).
//
// The close itself is the filter's (filterclose): a dry run against the
// fingerprints, then the strategy's active alerts of this source the filter
// names, each closed by its alert id.
//
// Closes are run at most Batch entries per step, starting after the last
// one the previous step decided, so a large backlog is walked rather than
// retried from its head.
//
// The close of alerts whose target left the strategy's scope is the control
// leader's, per strategy, on a change of the scope; this package used to
// decide it per series as well, and the family keeps its name.
package scopeclose

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
)

// The outcomes, closed: a metric label is made of them.
const (
	// OutcomeCloseSent counts fingerprints the link's filter closed or
	// found nothing active of.
	OutcomeCloseSent = "close_sent"
	// OutcomeUnconfirmed counts first observations: an old fingerprint of
	// an open alert offered by one Slot, waiting for a second.
	OutcomeUnconfirmed = "unconfirmed"
	// OutcomeSendFailed counts entries the link did not settle but may
	// next step - busy or failing (filterclose.NextTransient), items left
	// to be found again or a close cut short (filterclose.NextAgain) - kept
	// and tried next step.
	OutcomeSendFailed = "send_failed"
	// OutcomeRefused counts entries whose close the same command would be
	// answered the same way again (filterclose.NextHeld: items refused,
	// the request refused, the dry run past the expectation or the
	// client's bound, a filter that cannot be built): kept, and not sent
	// again until RefusedRetry after.
	OutcomeRefused = "close_refused"
	// OutcomeUnsupported counts entries not closed because filtered close
	// is unsupported: the link answered 501, 404 or 405. Nothing else
	// closes them.
	OutcomeUnsupported = "filter_close_unsupported"
	// OutcomeUnrouted counts entries confirmed on a replica with no link to
	// close through.
	OutcomeUnrouted = "unrouted"
	// OutcomeIdentityInvalid is a close whose strategy id is not a positive
	// number or whose revision is not positive. Nothing was sent, and the
	// observation is forgotten.
	OutcomeIdentityInvalid = "close_identity_invalid"
	// OutcomeMemoryFull counts first observations not recorded because the
	// observation table was at its bound.
	OutcomeMemoryFull = "memory_full"
	// OutcomeStaleDeferred counts closes held back at the step because the
	// last observation is older than the freshness bound.
	OutcomeStaleDeferred = "stale_deferred"
	// OutcomeObservationExpired counts first observations whose second did
	// not arrive inside the observation's lifetime.
	OutcomeObservationExpired = "observation_expired"
	// OutcomeSupersededCloseSent counts, among close_sent, the closes of
	// alerts a series opened under the fingerprint of its own address
	// before its alert identity read CMDB's.
	OutcomeSupersededCloseSent = "superseded_identity_close_sent"
)

// Outcomes lists every outcome, for the metric that pre-creates them.
var Outcomes = []string{OutcomeCloseSent, OutcomeUnconfirmed, OutcomeSendFailed, OutcomeRefused, OutcomeUnsupported, OutcomeUnrouted,
	OutcomeMemoryFull, OutcomeStaleDeferred, OutcomeIdentityInvalid, OutcomeObservationExpired, OutcomeSupersededCloseSent}

// RefusedRetry is how long a close the link answered held
// (OutcomeRefused) waits before it is sent again. Every step - every 30 s -
// sent it again before, a dry run and a real close each time, and the same
// command is answered the same way. A fixed interval, as the inactive
// close's: one close per group per interval is little for the link, and a
// refusal that can clear (the source published again, the record caught
// up with the link) is picked up within it.
const RefusedRetry = 5 * time.Minute

// The bounds of what the facts carry.
const (
	FactsStrategies         = 8
	FactsSamplesPerStrategy = 3
	samplePrefix            = 8
	// maxTalliedStrategies bounds the per-strategy counts kept for the facts.
	maxTalliedStrategies = 256
)

// DecisionSuperseded is the decision the closes carry (a filterclose
// decision word).
const DecisionSuperseded = "superseded_identity"

// Key names one strategy.
type Key struct {
	TenantID   string
	StrategyID string
}

// Offer is one admitted series of a strategy Offers cleared, whose alert
// identity reads CMDB's address over its own.
type Offer struct {
	TenantID, StrategyID string
	// Fingerprint is the one its alerts were filed under before: the
	// evaluator's dedupe identity of the record's own dimensions.
	Fingerprint      string
	StrategyRevision int64
	// Round is the Slot's evaluation time.
	Round int64
	// IntervalSeconds is the strategy's evaluation period, and ReadOffset
	// how long after Round its Slot was frozen to read. The second
	// observation arrives when the next Slot is read, which these two place.
	IntervalSeconds int64
	ReadOffset      time.Duration
}

// OpenSet is what the close asks alarmd's copy of the link's active alerts
// (activecopy.Copy).
type OpenSet interface {
	// KnowsOpen is asked once per strategy per query before any record.
	KnowsOpen(tenantID, strategyID string) bool
	// Known is the copy's word on one fingerprint, without refreshing it.
	Known(tenantID, strategyID, fingerprint string) (held, judged bool)
}

// Link is the link's close by filter (filterclose.Closer).
type Link interface {
	Fingerprints(ctx context.Context, scope filterclose.Scope, fingerprints []string, decision string, revision int64,
		decidedAt time.Time) string
}

// Options bound the close.
type Options struct {
	Now func() time.Time
	// MaxEntries bounds the entries observed and not yet decided.
	MaxEntries int
	// Batch bounds the entries one step closes.
	Batch int
	// ObservationTTL is how long an observation waits for its second
	// before it is forgotten, and how long a decided entry is not decided
	// again.
	ObservationTTL time.Duration
	// Freshness is how recent the last observation must be when a close is
	// run.
	Freshness time.Duration
}

type entryKey struct {
	key         Key
	fingerprint string
}

type entry struct {
	revision   int64
	firstRound int64
	confirmed  bool
	lastSeen   time.Time
	// ttl is how long the observation waits for its second (lifetime).
	ttl time.Duration
	// heldUntil is until when its close is not sent again: its last close
	// was answered held (OutcomeRefused). Zero when it was not.
	heldUntil time.Time
}

// lifetime is how long an observation of offer waits for its second: two of
// the strategy's periods plus its Slot's read offset, at least
// ObservationTTL.
func (closer *Closer) lifetime(offer Offer) time.Duration {
	return max(closer.options.ObservationTTL, 2*time.Duration(offer.IntervalSeconds)*time.Second+offer.ReadOffset)
}

type tally struct {
	counts  map[string]uint64
	decided []string
}

// Closer holds the observations and decides the closes.
type Closer struct {
	options Options
	mu      sync.Mutex
	set     OpenSet
	link    Link
	source  string
	entries map[entryKey]*entry
	// decided remembers what was closed, so the Slots that keep offering
	// the same fingerprint do not decide it again within the TTL.
	decided map[entryKey]time.Time
	after   entryKey
	counts  map[string]uint64
	tallies map[Key]*tally
}

// New builds a closer with nothing bound; Bind attaches the copy and the
// link once they exist. Until then nothing is offered.
func New(options Options) *Closer {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = 1024
	}
	if options.Batch <= 0 {
		options.Batch = 64
	}
	if options.ObservationTTL <= 0 {
		options.ObservationTTL = 30 * time.Minute
	}
	if options.Freshness <= 0 {
		options.Freshness = time.Minute
	}
	return &Closer{options: options, entries: map[entryKey]*entry{}, decided: map[entryKey]time.Time{},
		counts: map[string]uint64{}, tallies: map[Key]*tally{}}
}

// Bind attaches the copy, the link and the event source every filter names.
// A nil link is a replica with no link control plane: confirmed entries are
// counted unrouted.
func (closer *Closer) Bind(set OpenSet, link Link, source string) {
	closer.mu.Lock()
	closer.set, closer.link, closer.source = set, link, source
	closer.mu.Unlock()
}

// Offers answers once per strategy per query whether its series are worth
// a fingerprint each: only when the copy knows an open alert of the
// strategy. Nothing is counted either way.
func (closer *Closer) Offers(key Key) bool {
	closer.mu.Lock()
	set := closer.set
	closer.mu.Unlock()
	return set != nil && set.KnowsOpen(key.TenantID, key.StrategyID)
}

// Observe records one offer. It is called on the query's goroutine and does
// no I/O: one lookup in the copy, and a map update. An offer finds nothing
// to close for every series whose old fingerprint never opened an alert,
// or that the copy has no word on; such an offer counts nothing.
func (closer *Closer) Observe(offer Offer) {
	key := Key{TenantID: offer.TenantID, StrategyID: offer.StrategyID}
	if offer.Fingerprint == "" {
		return
	}
	closer.mu.Lock()
	set := closer.set
	closer.mu.Unlock()
	if set == nil {
		return
	}
	if held, judged := set.Known(offer.TenantID, offer.StrategyID, offer.Fingerprint); !judged || !held {
		return
	}
	ek := entryKey{key: key, fingerprint: offer.Fingerprint}
	now := closer.options.Now()
	closer.mu.Lock()
	defer closer.mu.Unlock()
	if until, done := closer.decided[ek]; done && now.Before(until) {
		return
	}
	existing := closer.entries[ek]
	if existing != nil && now.Sub(existing.lastSeen) >= existing.ttl {
		// Waited too long for its second: the two are not one observation.
		delete(closer.entries, ek)
		closer.countLocked(key, OutcomeObservationExpired, 1)
		existing = nil
	}
	if existing == nil {
		if len(closer.entries) >= closer.options.MaxEntries {
			closer.countLocked(key, OutcomeMemoryFull, 1)
			return
		}
		closer.entries[ek] = &entry{revision: offer.StrategyRevision, firstRound: offer.Round, lastSeen: now,
			ttl: closer.lifetime(offer)}
		closer.countLocked(key, OutcomeUnconfirmed, 1)
		return
	}
	existing.lastSeen = now
	if existing.revision != offer.StrategyRevision {
		// Another revision is another decision, closed by another command:
		// what held the last one does not hold it.
		existing.heldUntil = time.Time{}
	}
	existing.revision = offer.StrategyRevision
	existing.ttl = closer.lifetime(offer)
	if offer.Round != existing.firstRound {
		existing.confirmed = true
	}
}

// group is the confirmed entries of one strategy and revision that one
// filter call closes.
type group struct {
	key      Key
	revision int64
}

// Step decides and closes at most Batch confirmed entries.
func (closer *Closer) Step(ctx context.Context) {
	now := closer.options.Now()
	closer.mu.Lock()
	for ek, e := range closer.entries {
		if now.Sub(e.lastSeen) >= e.ttl {
			delete(closer.entries, ek)
			closer.countLocked(ek.key, OutcomeObservationExpired, 1)
		}
	}
	for ek, until := range closer.decided {
		if !now.Before(until) {
			delete(closer.decided, ek)
		}
	}
	confirmed := make([]entryKey, 0)
	for ek, e := range closer.entries {
		if e.confirmed {
			confirmed = append(confirmed, ek)
		}
	}
	link, source, after := closer.link, closer.source, closer.after
	closer.mu.Unlock()
	if len(confirmed) == 0 {
		return
	}
	sort.Slice(confirmed, func(i, j int) bool { return lessEntry(confirmed[i], confirmed[j]) })
	start := sort.Search(len(confirmed), func(i int) bool { return lessEntry(after, confirmed[i]) })
	var order []group
	members := map[group][]entryKey{}
	picked := 0
	for n := 0; n < len(confirmed) && picked < closer.options.Batch; n++ {
		ek := confirmed[(start+n)%len(confirmed)]
		after = ek
		closer.mu.Lock()
		e := closer.entries[ek]
		closer.mu.Unlock()
		if e == nil || now.Before(e.heldUntil) {
			continue
		}
		if now.Sub(e.lastSeen) > closer.options.Freshness {
			closer.count(ek.key, OutcomeStaleDeferred, 1)
			continue
		}
		if id, err := strconv.ParseInt(ek.key.StrategyID, 10, 64); err != nil || id <= 0 || e.revision <= 0 {
			closer.forget(ek, OutcomeIdentityInvalid)
			continue
		}
		if link == nil || source == "" {
			closer.count(ek.key, OutcomeUnrouted, 1)
			continue
		}
		g := group{key: ek.key, revision: e.revision}
		if _, seen := members[g]; !seen {
			order = append(order, g)
		}
		members[g] = append(members[g], ek)
		picked++
	}
	closer.mu.Lock()
	closer.after = after
	closer.mu.Unlock()
	for _, g := range order {
		if ctx.Err() != nil {
			return
		}
		closer.closeGroup(ctx, link, source, g, members[g], now)
	}
}

// closeGroup runs one group's filter close and decides its entries by the
// word (filterclose.NextAfter): done or matching nothing decides them;
// filtered close unsupported counts them; a word the same command would
// answer again holds them for RefusedRetry; anything else - transient, or
// a close that got somewhere - keeps them for the next step.
func (closer *Closer) closeGroup(ctx context.Context, link Link, source string, g group, keys []entryKey, now time.Time) {
	scope := filterclose.Scope{TenantID: g.key.TenantID, EventSourceID: source, StrategyID: g.key.StrategyID}
	closer.mu.Lock()
	// The decision was read when the group's entries were last offered; the
	// earliest of them bounds the close, so an alert opened after that is
	// not selected.
	var decidedAt time.Time
	fingerprints := make([]string, 0, len(keys))
	for _, ek := range keys {
		if e := closer.entries[ek]; e != nil && (decidedAt.IsZero() || e.lastSeen.Before(decidedAt)) {
			decidedAt = e.lastSeen
		}
		fingerprints = append(fingerprints, ek.fingerprint)
	}
	closer.mu.Unlock()
	sort.Strings(fingerprints)
	word := link.Fingerprints(ctx, scope, fingerprints, DecisionSuperseded, g.revision, decidedAt)
	switch next := filterclose.NextAfter(word); {
	case next == filterclose.NextSettled:
		closer.decide(keys, now)
	case word == filterclose.OutcomeUnsupported:
		closer.countEach(keys, OutcomeUnsupported)
	case next == filterclose.NextHeld:
		closer.hold(keys, now.Add(RefusedRetry))
	default:
		// Kept: the next step runs the filter again, from a new dry run.
		closer.countEach(keys, OutcomeSendFailed)
	}
}

// hold keeps the entries and holds their close until until, counted
// OutcomeRefused.
func (closer *Closer) hold(keys []entryKey, until time.Time) {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, ek := range keys {
		if e := closer.entries[ek]; e != nil {
			e.heldUntil = until
		}
		closer.countLocked(ek.key, OutcomeRefused, 1)
	}
}

func (closer *Closer) forget(ek entryKey, outcome string) {
	closer.mu.Lock()
	delete(closer.entries, ek)
	closer.countLocked(ek.key, outcome, 1)
	closer.mu.Unlock()
}

func (closer *Closer) decide(keys []entryKey, now time.Time) {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, ek := range keys {
		ttl := closer.options.ObservationTTL
		if e := closer.entries[ek]; e != nil {
			ttl = e.ttl
		}
		delete(closer.entries, ek)
		if len(closer.decided) < closer.options.MaxEntries {
			closer.decided[ek] = now.Add(ttl)
		}
		closer.countLocked(ek.key, OutcomeCloseSent, 1)
		closer.countLocked(ek.key, OutcomeSupersededCloseSent, 1)
		t := closer.tallyLocked(ek.key)
		if t == nil {
			continue
		}
		t.decided = append(t.decided, prefix(sampleOf(ek)))
		if len(t.decided) > FactsSamplesPerStrategy {
			t.decided = t.decided[len(t.decided)-FactsSamplesPerStrategy:]
		}
	}
}

func (closer *Closer) countEach(keys []entryKey, outcome string) {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, ek := range keys {
		closer.countLocked(ek.key, outcome, 1)
	}
}

func (closer *Closer) count(key Key, outcome string, n int) {
	closer.mu.Lock()
	closer.countLocked(key, outcome, n)
	closer.mu.Unlock()
}

func (closer *Closer) countLocked(key Key, outcome string, n int) {
	closer.counts[outcome] += uint64(n)
	if t := closer.tallyLocked(key); t != nil {
		t.counts[outcome] += uint64(n)
	}
}

// tallyLocked is the strategy's counts, created while there is room; a
// strategy past the bound is counted in the totals only.
func (closer *Closer) tallyLocked(key Key) *tally {
	t := closer.tallies[key]
	if t == nil && len(closer.tallies) < maxTalliedStrategies {
		t = &tally{counts: map[string]uint64{}}
		closer.tallies[key] = t
	}
	return t
}

// Stats is the outcome totals, every outcome present.
func (closer *Closer) Stats() map[string]uint64 {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	counts := make(map[string]uint64, len(Outcomes))
	for _, outcome := range Outcomes {
		counts[outcome] = closer.counts[outcome]
	}
	return counts
}

// Facts is what a replica publishes about the close.
type Facts struct {
	Pending, Confirmed   int
	MaxEntries           int
	Outcomes             map[string]uint64
	Strategies           []StrategyFacts
	TalliedStrategies    int
	MaxTalliedStrategies int
}

// StrategyFacts is one strategy's reading: its counts, the entries it has
// waiting, and the last ones decided, each a bounded prefix.
type StrategyFacts struct {
	TenantID, StrategyID string
	Pending, Confirmed   int
	Outcomes             map[string]uint64
	PendingSample        []string
	DecidedSample        []string
}

// Facts reads the close's state: totals, and up to FactsStrategies
// strategies, those with entries waiting first.
func (closer *Closer) Facts() Facts {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	facts := Facts{MaxEntries: closer.options.MaxEntries, Outcomes: map[string]uint64{},
		TalliedStrategies: len(closer.tallies), MaxTalliedStrategies: maxTalliedStrategies}
	for _, outcome := range Outcomes {
		facts.Outcomes[outcome] = closer.counts[outcome]
	}
	rows := map[Key]*StrategyFacts{}
	row := func(key Key) *StrategyFacts {
		r := rows[key]
		if r == nil {
			r = &StrategyFacts{TenantID: key.TenantID, StrategyID: key.StrategyID}
			rows[key] = r
		}
		return r
	}
	pending := map[Key][]entryKey{}
	for ek, e := range closer.entries {
		r := row(ek.key)
		if e.confirmed {
			r.Confirmed++
			facts.Confirmed++
		} else {
			r.Pending++
			facts.Pending++
		}
		pending[ek.key] = append(pending[ek.key], ek)
	}
	for key, t := range closer.tallies {
		r := row(key)
		r.Outcomes = make(map[string]uint64, len(t.counts))
		for outcome, count := range t.counts {
			r.Outcomes[outcome] = count
		}
		r.DecidedSample = append([]string(nil), t.decided...)
	}
	for key, keys := range pending {
		sort.Slice(keys, func(i, j int) bool {
			ci, cj := closer.entries[keys[i]].confirmed, closer.entries[keys[j]].confirmed
			if ci != cj {
				return ci
			}
			return keys[i].fingerprint < keys[j].fingerprint
		})
		for i := 0; i < len(keys) && i < FactsSamplesPerStrategy; i++ {
			rows[key].PendingSample = append(rows[key].PendingSample, prefix(sampleOf(keys[i])))
		}
	}
	ordered := make([]*StrategyFacts, 0, len(rows))
	for _, r := range rows {
		ordered = append(ordered, r)
	}
	sort.Slice(ordered, func(i, j int) bool {
		wi, wj := ordered[i].Pending+ordered[i].Confirmed, ordered[j].Pending+ordered[j].Confirmed
		if wi != wj {
			return wi > wj
		}
		if ordered[i].TenantID != ordered[j].TenantID {
			return ordered[i].TenantID < ordered[j].TenantID
		}
		return ordered[i].StrategyID < ordered[j].StrategyID
	})
	for i := 0; i < len(ordered) && i < FactsStrategies; i++ {
		facts.Strategies = append(facts.Strategies, *ordered[i])
	}
	return facts
}

func lessEntry(a, b entryKey) bool {
	if a.key.TenantID != b.key.TenantID {
		return a.key.TenantID < b.key.TenantID
	}
	if a.key.StrategyID != b.key.StrategyID {
		return a.key.StrategyID < b.key.StrategyID
	}
	return a.fingerprint < b.fingerprint
}

// sampleOf is what an entry is shown as: its fingerprint.
func sampleOf(ek entryKey) string {
	return ek.fingerprint
}

func prefix(value string) string {
	if len(value) <= samplePrefix {
		return value
	}
	return value[:samplePrefix]
}
