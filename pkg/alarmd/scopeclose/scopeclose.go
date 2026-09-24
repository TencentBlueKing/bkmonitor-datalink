// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package scopeclose closes the alerts of targets that left their strategy's
// monitoring scope.
//
// A record the target filter turns away is never evaluated, so nothing this
// process sends will ever end the alert its series opened while it was still
// in scope; the alert link only moves an alert on an event, so the alert
// stays open. The one place that knows the target let the record go is the
// admission step, and the one fact that makes the drop actionable is that
// the record's fingerprint - the one the evaluator would have given it - is
// an open alert of this deployment's own. This package joins the two.
//
// What has to hold before anything is sent, each named when it does not:
//
//   - the rejection is the target's own verdict on facts that were all read
//     and current (admission.DefinitelyOutside); a cache that could not
//     answer is cache_unavailable, never a reason to close;
//   - the strategy's open set could be judged at all: calibrated, and not
//     disjoint from this process's own sends; otherwise set_unavailable;
//   - the fingerprint is in the set (not_member otherwise) and the alert is
//     this deployment's (producer_foreign otherwise);
//   - two different Slots turned the same fingerprint away. One Slot is a
//     first observation, unconfirmed; the second makes it a close;
//   - the last of them is fresh when the close is sent (Freshness): a target
//     that came back into scope stops being rejected, and nothing else
//     would tell this memory so.
//
// Cost is shaped around the common case, a strategy with no open alert:
// the access path asks Screen once per strategy per query and counts such
// rejections in bulk, with no fingerprint and no lock per record.
//
// Closes are sent at most Batch per step, starting after the last one the
// previous step decided, so a large backlog is walked rather than retried
// from its head; and only while the deployment has armed its own inferences
// (absent_close_send). Unarmed, every close is decided and counted as
// would_send, and nothing is sent.
package scopeclose

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The outcomes, closed: a metric label is made of them.
const (
	// OutcomeClosed counts alerts a close was sent for.
	OutcomeClosed = "closed"
	// OutcomeWouldSend counts alerts a close was decided for while the
	// close was not armed.
	OutcomeWouldSend = "would_send"
	// OutcomeUnconfirmed counts first observations: a fingerprint of an
	// open alert turned away by one Slot, waiting for a second.
	OutcomeUnconfirmed = "unconfirmed"
	// OutcomeCacheUnavailable counts rejections the target could not stand
	// behind: facts that were not read, a target plan that did not resolve
	// in full. Never closed.
	OutcomeCacheUnavailable = "cache_unavailable"
	// OutcomeNotMember counts definitive rejections whose fingerprint is
	// not an open alert of the strategy - the ordinary case.
	OutcomeNotMember = "not_member"
	// OutcomeSetUnavailable counts decisions refused because the open set
	// could not be judged: not calibrated, disjoint, not read.
	OutcomeSetUnavailable = "set_unavailable"
	// OutcomeProducerForeign counts open alerts of another source.
	OutcomeProducerForeign = "producer_foreign"
	// OutcomeSendFailed counts alerts whose close the producer refused.
	OutcomeSendFailed = "send_failed"
	// OutcomeMemoryFull counts first observations not recorded because the
	// observation table was at its bound; the close for them may be delayed
	// beyond the observation TTL, and is never made on less than two fresh
	// observations.
	OutcomeMemoryFull = "memory_full"
	// OutcomeFingerprintUnsupported counts definitive rejections of a Plan
	// fed by more than one input, whose alert fingerprint no single series
	// carries; never closed.
	OutcomeFingerprintUnsupported = "fingerprint_unsupported"
	// OutcomeStaleDeferred counts closes held back at the step because the
	// last observation is older than the freshness bound: the target may
	// have come back into scope since, and a rejection is the only thing
	// that renews an observation. The close waits for a fresh one.
	OutcomeStaleDeferred = "stale_deferred"
	// OutcomeIndefinite counts rejections that are themselves not a verdict
	// on the record's place - a key or object identity that could not be
	// built - as opposed to a verdict reached without its facts
	// (cache_unavailable). Never closed.
	OutcomeIndefinite = "indefinite"
)

// Outcomes lists every outcome, for the metric that pre-creates them.
var Outcomes = []string{OutcomeClosed, OutcomeWouldSend, OutcomeUnconfirmed, OutcomeCacheUnavailable, OutcomeNotMember,
	OutcomeSetUnavailable, OutcomeProducerForeign, OutcomeSendFailed, OutcomeMemoryFull, OutcomeFingerprintUnsupported,
	OutcomeStaleDeferred, OutcomeIndefinite}

// The bounds of what the facts carry.
const (
	FactsStrategies         = 8
	FactsSamplesPerStrategy = 3
	samplePrefix            = 8
	// maxTalliedStrategies bounds the per-strategy counts kept for the facts.
	maxTalliedStrategies = 256
)

// Drop is one definitive target rejection of one series of a strategy that
// Screen cleared.
type Drop struct {
	TenantID, BusinessID, StrategyID string
	// Fingerprint is the evaluator's dedupe identity of the record; empty
	// when there is none to compute.
	Fingerprint      string
	StrategyRevision int64
	// Round is the Slot's evaluation time.
	Round int64
}

// OpenSet is what the close asks the open alert copy.
type OpenSet interface {
	// Disjoint is the one whole-copy state that makes every set's answer
	// meaningless; everything else is judged per strategy.
	Disjoint() bool
	// MemberCount is asked once per strategy per query before any record.
	MemberCount(key openalerts.StrategyKey) (count int, judged bool)
	Holds(key openalerts.StrategyKey, fingerprint string) (held, judged bool)
	ActiveAlerts(key openalerts.StrategyKey) []openalerts.Alert
	OwnEventSourceID() string
}

// Writer sends closes; the same producer path every other close uses.
type Writer interface {
	WriteCloseBatch(context.Context, []linkdoutput.CloseRequest) error
}

// Options bound the close.
type Options struct {
	// Send arms the close; see config.LinkdConfig.AbsentCloseSend.
	Send bool
	Now  func() time.Time
	// MaxEntries bounds the fingerprints observed and not yet decided.
	MaxEntries int
	// Batch bounds the closes one step decides.
	Batch int
	// ObservationTTL is how long an observation waits for its second
	// before it is forgotten, and how long a decided fingerprint is not
	// decided again.
	ObservationTTL time.Duration
	// Freshness is how recent the last observation must be when a close is
	// sent: two of the close's steps, so a Slot's rejection is acted on by
	// the step after it, and a backlog is never sent on an old one.
	Freshness time.Duration
}

type entryKey struct {
	key         openalerts.StrategyKey
	fingerprint string
}

type entry struct {
	businessID string
	revision   int64
	firstRound int64
	confirmed  bool
	lastSeen   time.Time
}

// bulkKey and bulkRound remember, per reporter and outcome, the last round
// counted in bulk and how much of it, so a retried round is counted once.
type bulkKey struct {
	key      openalerts.StrategyKey
	reporter string
	outcome  string
}

type bulkRound struct {
	round int64
	n     int
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
	writer  Writer
	entries map[entryKey]*entry
	// decided remembers what was closed or would have been, so the Slots
	// that keep turning the same series away before the link's set catches
	// up do not decide it again.
	decided map[entryKey]time.Time
	after   entryKey
	counts  map[string]uint64
	tallies map[openalerts.StrategyKey]*tally
	bulk    map[bulkKey]bulkRound
}

// New builds a closer with nothing bound; Bind attaches the open set and
// the writer once they exist. Until then every observation that needs the
// set is set_unavailable.
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
		counts: map[string]uint64{}, tallies: map[openalerts.StrategyKey]*tally{}, bulk: map[bulkKey]bulkRound{}}
}

// Bind attaches the open set and the writer.
func (closer *Closer) Bind(set OpenSet, writer Writer) {
	closer.mu.Lock()
	closer.set, closer.writer = set, writer
	closer.mu.Unlock()
}

// Armed says whether closes are sent.
func (closer *Closer) Armed() bool { return closer.options.Send }

// Screen answers once per strategy per query whether its rejections are
// worth a fingerprint each: "" when the strategy has open alerts to judge
// against, and otherwise the outcome all of them are counted under in bulk
// - set_unavailable when the copy cannot say, not_member when the set is
// empty, which on a filtered wide table is nearly every strategy.
func (closer *Closer) Screen(key openalerts.StrategyKey) string {
	closer.mu.Lock()
	set := closer.set
	closer.mu.Unlock()
	if set == nil || set.Disjoint() || set.OwnEventSourceID() == "" {
		return OutcomeSetUnavailable
	}
	count, judged := set.MemberCount(key)
	switch {
	case !judged:
		return OutcomeSetUnavailable
	case count == 0:
		return OutcomeNotMember
	}
	return ""
}

// Count adds one reporter's rejections of a strategy under one outcome, in
// bulk. The reporter names one Plan instance - business, shard, Query
// Group - of the strategy; several of them report the same round and are
// summed. A retried Slot reports as the same reporter and round again; that
// is counted once, at the largest total any of its attempts reported, so an
// attempt that failed halfway and the retry that completed are not summed.
// Only readings depend on this; no close is decided from it.
func (closer *Closer) Count(key openalerts.StrategyKey, reporter string, round int64, outcome string, n int) {
	if n <= 0 {
		return
	}
	closer.mu.Lock()
	defer closer.mu.Unlock()
	bk := bulkKey{key: key, reporter: reporter, outcome: outcome}
	last, seen := closer.bulk[bk]
	if seen && last.round == round {
		if n <= last.n {
			return
		}
		closer.countLocked(key, outcome, n-last.n)
		closer.bulk[bk] = bulkRound{round: round, n: n}
		return
	}
	if seen || len(closer.bulk) < closer.options.MaxEntries {
		closer.bulk[bk] = bulkRound{round: round, n: n}
	}
	closer.countLocked(key, outcome, n)
}

// Observe records one rejection of a strategy Screen cleared. It is called
// on the query's goroutine and does no I/O: one membership lookup in
// memory, and a map update.
func (closer *Closer) Observe(drop Drop) {
	key := openalerts.StrategyKey{TenantID: drop.TenantID, StrategyID: drop.StrategyID}
	if drop.Fingerprint == "" {
		closer.count(key, OutcomeNotMember, 1)
		return
	}
	closer.mu.Lock()
	set := closer.set
	closer.mu.Unlock()
	if set == nil {
		closer.count(key, OutcomeSetUnavailable, 1)
		return
	}
	held, judged := set.Holds(key, drop.Fingerprint)
	ek := entryKey{key: key, fingerprint: drop.Fingerprint}
	now := closer.options.Now()
	closer.mu.Lock()
	defer closer.mu.Unlock()
	switch {
	case !judged:
		closer.countLocked(key, OutcomeSetUnavailable, 1)
		return
	case !held:
		delete(closer.entries, ek)
		closer.countLocked(key, OutcomeNotMember, 1)
		return
	}
	if at, done := closer.decided[ek]; done && now.Sub(at) < closer.options.ObservationTTL {
		return
	}
	existing := closer.entries[ek]
	if existing != nil && now.Sub(existing.lastSeen) >= closer.options.ObservationTTL {
		// Waited too long for its second: the two are not one absence.
		delete(closer.entries, ek)
		existing = nil
	}
	if existing == nil {
		if len(closer.entries) >= closer.options.MaxEntries {
			closer.countLocked(key, OutcomeMemoryFull, 1)
			return
		}
		closer.entries[ek] = &entry{businessID: drop.BusinessID, revision: drop.StrategyRevision, firstRound: drop.Round, lastSeen: now}
		closer.countLocked(key, OutcomeUnconfirmed, 1)
		return
	}
	existing.lastSeen = now
	existing.businessID, existing.revision = drop.BusinessID, drop.StrategyRevision
	if drop.Round != existing.firstRound {
		existing.confirmed = true
	}
}

// Step decides and sends at most Batch closes.
func (closer *Closer) Step(ctx context.Context) {
	now := closer.options.Now()
	closer.mu.Lock()
	for ek, e := range closer.entries {
		if now.Sub(e.lastSeen) >= closer.options.ObservationTTL {
			delete(closer.entries, ek)
		}
	}
	for ek, at := range closer.decided {
		if now.Sub(at) >= closer.options.ObservationTTL {
			delete(closer.decided, ek)
		}
	}
	confirmed := make([]entryKey, 0)
	for ek, e := range closer.entries {
		if e.confirmed {
			confirmed = append(confirmed, ek)
		}
	}
	set, writer, after := closer.set, closer.writer, closer.after
	closer.mu.Unlock()
	if len(confirmed) == 0 {
		return
	}
	sort.Slice(confirmed, func(i, j int) bool { return lessEntry(confirmed[i], confirmed[j]) })
	own := ""
	if set != nil && !set.Disjoint() {
		own = set.OwnEventSourceID()
	}
	if own == "" {
		for _, ek := range confirmed {
			closer.count(ek.key, OutcomeSetUnavailable, 1)
		}
		return
	}
	start := sort.Search(len(confirmed), func(i int) bool { return lessEntry(after, confirmed[i]) })
	alerts := map[openalerts.StrategyKey]map[string]openalerts.Alert{}
	batch := make([]linkdoutput.CloseRequest, 0, closer.options.Batch)
	decided := make([]entryKey, 0, closer.options.Batch)
	for n := 0; n < len(confirmed) && len(batch) < closer.options.Batch; n++ {
		ek := confirmed[(start+n)%len(confirmed)]
		after = ek
		if !closer.fresh(ek, now) {
			closer.count(ek.key, OutcomeStaleDeferred, 1)
			continue
		}
		byFingerprint, read := alerts[ek.key]
		if !read {
			byFingerprint = map[string]openalerts.Alert{}
			for _, alert := range set.ActiveAlerts(ek.key) {
				byFingerprint[alert.Fingerprint] = alert
			}
			alerts[ek.key] = byFingerprint
		}
		alert, listed := byFingerprint[ek.fingerprint]
		if !listed {
			held, judged := set.Holds(ek.key, ek.fingerprint)
			if judged && !held {
				closer.forget(ek, OutcomeNotMember)
			} else {
				// Held, and the calibration has not listed the alert yet:
				// the close needs the alert's id, which only it carries.
				closer.count(ek.key, OutcomeSetUnavailable, 1)
			}
			continue
		}
		if alert.EventSourceID != own {
			closer.forget(ek, OutcomeProducerForeign)
			continue
		}
		request, ok := closer.request(ek, alert, now)
		if !ok {
			closer.forget(ek, OutcomeSendFailed)
			continue
		}
		batch = append(batch, request)
		decided = append(decided, ek)
	}
	closer.mu.Lock()
	closer.after = after
	closer.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if !closer.options.Send {
		closer.decide(decided, OutcomeWouldSend, now)
		return
	}
	if writer == nil {
		closer.countEach(decided, OutcomeSendFailed)
		return
	}
	if err := writer.WriteCloseBatch(ctx, batch); err != nil {
		// Kept: the next step tries again.
		closer.countEach(decided, OutcomeSendFailed)
		return
	}
	closer.decide(decided, OutcomeClosed, now)
}

// fresh is whether the entry's last observation is recent enough to act on.
func (closer *Closer) fresh(ek entryKey, now time.Time) bool {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	e := closer.entries[ek]
	return e != nil && now.Sub(e.lastSeen) <= closer.options.Freshness
}

func (closer *Closer) request(ek entryKey, alert openalerts.Alert, now time.Time) (linkdoutput.CloseRequest, bool) {
	closer.mu.Lock()
	e := closer.entries[ek]
	closer.mu.Unlock()
	if e == nil {
		return linkdoutput.CloseRequest{}, false
	}
	strategyID, err := strconv.ParseInt(ek.key.StrategyID, 10, 64)
	if err != nil || strategyID <= 0 {
		return linkdoutput.CloseRequest{}, false
	}
	businessID, err := strconv.ParseInt(e.businessID, 10, 64)
	if err != nil || businessID == 0 || e.revision <= 0 {
		return linkdoutput.CloseRequest{}, false
	}
	return linkdoutput.CloseRequest{TenantID: ek.key.TenantID, Fingerprint: ek.fingerprint, AlertInstanceID: alert.AlertID,
		StrategyID: strategyID, StrategyRevision: e.revision, BusinessID: businessID, OccurredAt: now,
		Reason: linkdoutput.CloseReasonTargetOutOfScope}, true
}

func (closer *Closer) forget(ek entryKey, outcome string) {
	closer.mu.Lock()
	delete(closer.entries, ek)
	closer.countLocked(ek.key, outcome, 1)
	closer.mu.Unlock()
}

func (closer *Closer) decide(decided []entryKey, outcome string, now time.Time) {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, ek := range decided {
		delete(closer.entries, ek)
		if len(closer.decided) < closer.options.MaxEntries {
			closer.decided[ek] = now
		}
		closer.countLocked(ek.key, outcome, 1)
		t := closer.tallyLocked(ek.key)
		if t == nil {
			continue
		}
		t.decided = append(t.decided, prefix(ek.fingerprint))
		if len(t.decided) > FactsSamplesPerStrategy {
			t.decided = t.decided[len(t.decided)-FactsSamplesPerStrategy:]
		}
	}
}

func (closer *Closer) countEach(decided []entryKey, outcome string) {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	for _, ek := range decided {
		closer.countLocked(ek.key, outcome, 1)
	}
}

func (closer *Closer) count(key openalerts.StrategyKey, outcome string, n int) {
	closer.mu.Lock()
	closer.countLocked(key, outcome, n)
	closer.mu.Unlock()
}

func (closer *Closer) countLocked(key openalerts.StrategyKey, outcome string, n int) {
	closer.counts[outcome] += uint64(n)
	if t := closer.tallyLocked(key); t != nil {
		t.counts[outcome] += uint64(n)
	}
}

// tallyLocked is the strategy's counts, created while there is room; a
// strategy past the bound is counted in the totals only.
func (closer *Closer) tallyLocked(key openalerts.StrategyKey) *tally {
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
	Armed                bool
	Pending, Confirmed   int
	MaxEntries           int
	Outcomes             map[string]uint64
	Strategies           []StrategyFacts
	TalliedStrategies    int
	MaxTalliedStrategies int
}

// StrategyFacts is one strategy's reading: its counts, the fingerprints it
// has waiting, and the last ones decided, each a prefix.
type StrategyFacts struct {
	TenantID, StrategyID string
	Pending, Confirmed   int
	Outcomes             map[string]uint64
	PendingSample        []string
	DecidedSample        []string
}

// Facts reads the close's state: totals, and up to FactsStrategies
// strategies, those with fingerprints waiting first.
func (closer *Closer) Facts() Facts {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	facts := Facts{Armed: closer.options.Send, MaxEntries: closer.options.MaxEntries, Outcomes: map[string]uint64{},
		TalliedStrategies: len(closer.tallies), MaxTalliedStrategies: maxTalliedStrategies}
	for _, outcome := range Outcomes {
		facts.Outcomes[outcome] = closer.counts[outcome]
	}
	rows := map[openalerts.StrategyKey]*StrategyFacts{}
	row := func(key openalerts.StrategyKey) *StrategyFacts {
		r := rows[key]
		if r == nil {
			r = &StrategyFacts{TenantID: key.TenantID, StrategyID: key.StrategyID}
			rows[key] = r
		}
		return r
	}
	pending := map[openalerts.StrategyKey][]entryKey{}
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
			rows[key].PendingSample = append(rows[key].PendingSample, prefix(keys[i].fingerprint))
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

func prefix(value string) string {
	if len(value) <= samplePrefix {
		return value
	}
	return value[:samplePrefix]
}

// CacheSet is the open alert copy as the close reads it. The whole copy is
// refused only when disjoint; calibration, freshness and availability are
// judged per strategy by MemberCount and Holds.
func CacheSet(cache *openalerts.Cache) OpenSet { return cacheSet{cache: cache} }

type cacheSet struct{ cache *openalerts.Cache }

func (set cacheSet) Disjoint() bool { return set.cache.Disjoint() }

func (set cacheSet) MemberCount(key openalerts.StrategyKey) (int, bool) {
	return set.cache.MemberCount(key)
}

func (set cacheSet) Holds(key openalerts.StrategyKey, fingerprint string) (bool, bool) {
	return set.cache.Holds(key, fingerprint)
}

func (set cacheSet) ActiveAlerts(key openalerts.StrategyKey) []openalerts.Alert {
	return set.cache.ActiveAlerts(key)
}

func (set cacheSet) OwnEventSourceID() string { return set.cache.OwnEventSourceID() }
