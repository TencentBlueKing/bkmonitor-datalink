// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package activecopy is alarmd's own copy of which of its series the alert
// link holds an active alert on, as the recovery gate and the no-data end
// ask it (contract.OpenAlertSet, execution.OpenAlertJudge).
//
// The copy knows a fingerprint in one of three ways: this process sent its
// ABNORMAL and the broker took it (own_sent); the link's search found an
// active alert of it (search_hit); or the search found none (search_miss).
// Anything else is unknown. Only what a caller asks about is ever refreshed:
// a fingerprint the gate holds a RECOVERY for, or the no-data end needs a
// judgement on, that the copy has no current word on is queued, and a pass
// asks the link for the queued fingerprints in batches of at most 500 per
// request (terms fingerprint), scoped by tenant, this deployment's event
// source and status active, across strategies: a fingerprint already names
// its strategy (contract.MonitorDedupeMD5 hashes the strategy id), so a
// strategy term would only split the batches.
//
// The gate never waits for the link. A RECOVERY whose fingerprint the copy
// has no current word on is held this round, and the pass that runs before
// the next round answers it: hold one round. Deciding, checking and sending
// inside the evaluation (decide/check/send) would put the link's latency and
// its 429s inside every Slot's evaluation; holding costs one evaluation
// interval of delay, only for fingerprints the copy had no word on - after
// this process started, after a RECOVERY was sent, or once a word aged out.
//
// A read that failed or was refused (429) answers nothing: the fingerprints
// stay queued and held, counted. A miss is the link's word only once a
// search made at least MissMinAge after the first miss of its run missed
// too: the link's search sees an alert only after the store's refresh
// (alert-search.md "搜索可见性受现有 ES refresh 周期影响"), so one miss right
// after the alert opened is not "not open", and the no-data end, which
// deletes its record on "not open", is only ever answered not-open on two.
package activecopy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
)

// The bounds and lives, program constants: the deployment knows no better.
const (
	// BatchSize is the fingerprints one request names: the link's page
	// bound (linkdmanage.MaxLimit), so one active alert per fingerprint
	// fits one page.
	BatchSize = linkdmanage.MaxLimit
	// MinBatchSize is how far a batch is cut after the link failed one
	// (a page past the link's response bound answers 502).
	MinBatchSize = 50
	// PassInterval is how often a pass runs: well inside the shortest
	// evaluation period, so a fingerprint held one round is answered
	// before the next.
	PassInterval = 10 * time.Second
	// RequestsPerPass bounds one pass, sent one at a time: at most 120
	// requests a minute from one process, never two at once.
	RequestsPerPass = 20
	// passTimeout bounds one pass; what it did not reach waits for the
	// next.
	passTimeout = 8 * time.Second
	// HitLife is how long a search hit answers. Past it the gate holds and
	// the no-data end does not judge until a new search.
	HitLife = 5 * time.Minute
	// MissLife is how long a judged miss answers; past it the fingerprint
	// is asked again. It bounds how long an alert opened elsewhere - by
	// another owner around a handover - is held.
	MissLife = time.Hour
	// MissMinAge is how long a run of misses must last before it is the
	// link's word: several of the store's refresh periods.
	MissMinAge = 2 * time.Minute
	// OwnLife is how long a send of this process answers without a search.
	OwnLife = 24 * time.Hour
	// untrackedGap is how long a strategy may go without being tracked
	// before what the copy knows of it is dropped: another owner may have
	// opened or closed its alerts meanwhile.
	untrackedGap = 10 * time.Minute
	// MaxEntries bounds the fingerprints the copy knows and has queued.
	MaxEntries = 1 << 20
	// UnsupportedRetryMin and UnsupportedRetryMax bound the wait before the
	// copy asks again a link that answered it cannot search: a rollback or
	// a proxy's 404 during a rollout passes, so the copy never stops for
	// good. The wait doubles from the first to the second while the link
	// keeps answering so; the second is filterclose's recheck.
	UnsupportedRetryMin = time.Minute
	UnsupportedRetryMax = 10 * time.Minute
)

// Answer is how a lookup was answered. Closed: a metric label.
type Answer string

const (
	AnswerOwnSent    Answer = "own_sent"
	AnswerSearchHit  Answer = "search_hit"
	AnswerSearchMiss Answer = "search_miss"
	// AnswerYoungMiss is a run of misses shorter than MissMinAge: held by
	// the gate, not judged by the no-data end.
	AnswerYoungMiss Answer = "young_miss"
	// AnswerUnknown is a fingerprint the copy has no current word on:
	// queued, and held or not judged until a pass answers it.
	AnswerUnknown Answer = "unknown"
	// AnswerFull is an unknown fingerprint not queued because the copy is
	// at MaxEntries.
	AnswerFull Answer = "full"
	// AnswerLinkUnsupported is an unknown fingerprint while the link
	// answers it cannot search: queued for the retry, held meanwhile.
	AnswerLinkUnsupported Answer = "link_unsupported"
)

// Answers lists every Answer, for the metric that pre-creates them all.
var Answers = []Answer{AnswerOwnSent, AnswerSearchHit, AnswerSearchMiss, AnswerYoungMiss, AnswerUnknown, AnswerFull,
	AnswerLinkUnsupported}

// Pass outcomes. Closed: a metric label.
const (
	OutcomeRequest = "request"
	// OutcomeLinkBusy is a request the link refused for its search slots
	// (429, no queueing): the pass stops, the fingerprints stay queued.
	OutcomeLinkBusy = "link_busy"
	// OutcomeLinkUnsupported is a link that cannot search (501) or does
	// not serve the route (404, 405): the pass stops and the copy asks
	// again after a bounded wait (UnsupportedRetryMin..Max); nothing is
	// judged meanwhile.
	OutcomeLinkUnsupported = "link_unsupported"
	// OutcomeFingerprintRefused is a queued fingerprint that is not a
	// native one (32 lowercase hex digits): it is taken out of its batch,
	// unasked, and the rest of the batch is sent. Counted per fingerprint.
	OutcomeFingerprintRefused = "fingerprint_refused"
	// OutcomeLinkRefused is a request the link refused for its credential
	// (401, 403): the token is missing, wrong or not allowed. The pass
	// stops and the fingerprints stay queued and held, as for read_failed;
	// the word is filterclose's for the same answer. A 401 or 403 never
	// clears by waiting, so it is told once per run of them (Refused).
	OutcomeLinkRefused = "link_refused"
	// OutcomeReadFailed is any other failed request: the fingerprints stay
	// queued and held.
	OutcomeReadFailed = "read_failed"
	OutcomeHit        = "hit"
	OutcomeMiss       = "miss"
	// OutcomeForeignItem is an item the search returned that is not an
	// active alert of a queued fingerprint, this source and its strategy.
	OutcomeForeignItem = "foreign_item"
	// OutcomeDropped is what an untracked strategy's drop let go.
	OutcomeDropped = "dropped"
)

// Outcomes lists every pass outcome.
var Outcomes = []string{OutcomeRequest, OutcomeLinkBusy, OutcomeLinkUnsupported, OutcomeReadFailed, OutcomeHit, OutcomeMiss,
	OutcomeForeignItem, OutcomeDropped, OutcomeFingerprintRefused, OutcomeLinkRefused}

// Searcher is the link's search (linkdmanage.Client).
type Searcher interface {
	Search(ctx context.Context, tenantID string, query json.RawMessage, after string, limit int) (linkdmanage.SearchPage, error)
}

// Options builds a copy.
type Options struct {
	Link Searcher
	// EventSourceID is the link's source alarmd's events arrive under;
	// every read names it.
	EventSourceID string
	Now           func() time.Time
	// Warn is told once each time the link starts answering that it cannot
	// search.
	Warn func(error)
	// Refused is told once each time the link starts refusing the
	// credential (401, 403), until a search is answered again.
	Refused func(error)
}

// StrategyKey names one strategy.
type StrategyKey struct {
	TenantID   string
	StrategyID string
}

type member struct {
	key         StrategyKey
	fingerprint string
}

type kind uint8

const (
	kindOwnSent kind = iota + 1
	kindHit
	kindMiss
)

type word struct {
	kind kind
	// at is when the send was acknowledged or the search that found it was
	// sent; firstMiss is the first search of a run of misses.
	at, firstMiss time.Time
	// levels are the Levels of an own send still open: added by the
	// ABNORMAL results this process acknowledged, removed by RECOVERY ones.
	// Nil is not known - a word from a search, or one from before a restart.
	levels []uint32
}

// Copy is the copy. Its lookups are memory only.
type Copy struct {
	options Options

	mu    sync.Mutex
	words map[member]word
	// open counts, per strategy, the words that are a send or a hit,
	// current or not: a screen, never an answer.
	open    map[StrategyKey]int
	queued  map[member]struct{}
	tracked map[StrategyKey]time.Time
	batch   int
	// unsupported is set while the link answers it cannot search: from its
	// first such answer to the next search it answers. retryAt is when the
	// copy asks again, retryWait the wait that set it.
	unsupported bool
	// refused is set from the link's first 401 or 403 to the next search it
	// answers: the run Refused is told about once.
	refused   bool
	retryAt   time.Time
	retryWait time.Duration
	answers   map[Answer]uint64
	outcomes  map[string]uint64
}

// New builds the copy.
func New(options Options) (*Copy, error) {
	if options.Link == nil || options.EventSourceID == "" {
		return nil, errors.New("alarmd activecopy: a link and the event source are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Copy{options: options, words: map[member]word{}, open: map[StrategyKey]int{}, queued: map[member]struct{}{}, tracked: map[StrategyKey]time.Time{},
		batch: BatchSize, answers: map[Answer]uint64{}, outcomes: map[string]uint64{}}, nil
}

// Linked says a link stands behind the copy (execution.OpenAlertLink).
func (c *Copy) Linked() bool { return c != nil }

// Contains is the recovery gate's question (contract.OpenAlertSet): pass a
// RECOVERY only on this process's own send or a current search hit.
func (c *Copy) Contains(tenantID, strategyID, fingerprint string) bool {
	if c == nil {
		return false
	}
	m := member{key: StrategyKey{TenantID: tenantID, StrategyID: strategyID}, fingerprint: fingerprint}
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, open := c.lookup(m, c.options.Now())
	c.answers[answer]++
	return open && (answer == AnswerOwnSent || answer == AnswerSearchHit)
}

// Holds is the no-data end's question (execution.OpenAlertJudge): judged
// only on a send, a current hit, or a miss run of at least MissMinAge.
func (c *Copy) Holds(tenantID, strategyID, fingerprint string) (held, judged bool) {
	if c == nil {
		return false, false
	}
	m := member{key: StrategyKey{TenantID: tenantID, StrategyID: strategyID}, fingerprint: fingerprint}
	c.mu.Lock()
	defer c.mu.Unlock()
	answer, open := c.lookup(m, c.options.Now())
	c.answers[answer]++
	switch answer {
	case AnswerOwnSent, AnswerSearchHit:
		return open, true
	case AnswerSearchMiss:
		return false, true
	}
	return false, false
}

// Known is what the copy knows of a fingerprint without queueing it: the
// superseded-identity close judges only on the copy's existing word (an
// offer is not a decision the copy refreshes for). held is a send or a
// current hit, judged also a judged miss.
func (c *Copy) Known(tenantID, strategyID, fingerprint string) (held, judged bool) {
	if c == nil {
		return false, false
	}
	m := member{key: StrategyKey{TenantID: tenantID, StrategyID: strategyID}, fingerprint: fingerprint}
	now := c.options.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	w, ok := c.words[m]
	switch {
	case !ok:
		return false, false
	case w.kind == kindOwnSent && now.Sub(w.at) <= OwnLife, w.kind == kindHit && now.Sub(w.at) <= HitLife:
		return true, true
	case w.kind == kindMiss && w.at.Sub(w.firstMiss) >= MissMinAge && now.Sub(w.at) <= MissLife:
		return false, true
	}
	return false, false
}

// KnowsOpen says the copy holds a send or a hit, current or not, for some
// fingerprint of the strategy: a screen before Known, never an answer.
func (c *Copy) KnowsOpen(tenantID, strategyID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open[StrategyKey{TenantID: tenantID, StrategyID: strategyID}] > 0
}

// set and forget change a word, keeping open. Called with the lock held.
func (c *Copy) set(m member, w word) {
	if old, ok := c.words[m]; ok && old.kind != kindMiss {
		c.open[m.key]--
	}
	c.words[m] = w
	if w.kind != kindMiss {
		c.open[m.key]++
	}
}

func (c *Copy) forget(m member) {
	old, ok := c.words[m]
	if !ok {
		return
	}
	delete(c.words, m)
	if old.kind != kindMiss {
		if c.open[m.key]--; c.open[m.key] <= 0 {
			delete(c.open, m.key)
		}
	}
}

// lookup answers m and queues it when the answer is not current. Called
// with the lock held.
func (c *Copy) lookup(m member, now time.Time) (Answer, bool) {
	w, ok := c.words[m]
	if ok {
		switch w.kind {
		case kindOwnSent:
			if now.Sub(w.at) <= OwnLife {
				return AnswerOwnSent, true
			}
		case kindHit:
			if now.Sub(w.at) <= HitLife {
				return AnswerSearchHit, true
			}
		case kindMiss:
			if now.Sub(w.at) <= MissLife {
				if w.at.Sub(w.firstMiss) >= MissMinAge {
					return AnswerSearchMiss, false
				}
				// Asked again only once a search can see what the first
				// could not.
				if now.Sub(w.firstMiss) >= MissMinAge {
					c.queue(m)
				}
				return AnswerYoungMiss, false
			}
		}
	}
	if !c.queue(m) {
		return AnswerFull, false
	}
	if c.unsupported {
		return AnswerLinkUnsupported, false
	}
	return AnswerUnknown, false
}

// queue adds m to the next pass, within MaxEntries.
func (c *Copy) queue(m member) bool {
	if _, ok := c.queued[m]; ok {
		return true
	}
	if len(c.queued)+len(c.words) >= MaxEntries {
		return false
	}
	c.queued[m] = struct{}{}
	return true
}

// Acknowledged takes the envelopes the broker acknowledged: an ABNORMAL is
// the copy's own word that the alert is open; a RECOVERY makes the copy
// forget the fingerprint, so the next question about it is asked of the
// link. Only envelopes the link sees count: a compatibility-protocol one
// goes elsewhere, and one without a fingerprint opens nothing.
//
// It returns the envelopes that change alarmd's record of the episodes it
// opened (package openepisodes): an ABNORMAL the copy held no own send of
// before - the episode's first, or its first since this process started
// or the strategy was tracked again - and a RECOVERY that ends it. A
// repeated ABNORMAL of an episode returns nothing, so the record is written
// once an episode, not once a round.
//
// The link keeps one alert per fingerprint across its Levels, at the most
// severe Level it was raised to, and resolves it on that Level's RECOVERY
// alone: a RECOVERY of a less severe Level leaves the alert active, and
// ending the episode then would leave the record short of the link.
// So the copy keeps the open Levels of each
// own send, and a RECOVERY ends the episode when it recovers the most
// severe of them or leaves none open; one whose open Levels the copy does
// not know (after a restart, or a series it learned of by a search) keeps
// the episode, the direction the record may err in, which a close that
// settles prunes.
func (c *Copy) Acknowledged(events []contract.TriggerEventV1) []contract.TriggerEventV1 {
	if c == nil || len(events) == 0 {
		return nil
	}
	now := c.options.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var changed []contract.TriggerEventV1
	for _, event := range events {
		if event.DedupeMD5 == "" || event.StrategyRef == nil || event.LegacyOutput != nil {
			continue
		}
		m := member{key: StrategyKey{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}, fingerprint: event.DedupeMD5}
		old, known := c.words[m]
		var levels []uint32
		if known && old.kind == kindOwnSent {
			levels = old.levels
		}
		switch event.EventKind {
		case contract.TriggerEventAbnormal:
			// An ABNORMAL that also recovers the Level the alert is at ends
			// that alert at the link, which opens a new one on the Level it
			// triggers: a new episode, as the same two in two envelopes are.
			if !known || old.kind != kindOwnSent || len(levels) > 0 && recoversAt(event, mostSevere(levels)) {
				changed = append(changed, event)
				levels = nil
			}
			if known || len(c.words)+len(c.queued) < MaxEntries {
				c.set(m, word{kind: kindOwnSent, at: now, levels: openLevels(levels, event.LevelResults)})
			}
			delete(c.queued, m)
		case contract.TriggerEventRecovery:
			if levels == nil {
				// Which Levels are open is not known: the episode stays.
				continue
			}
			if remaining := openLevels(levels, event.LevelResults); len(remaining) > 0 && !recoversAt(event, mostSevere(levels)) {
				c.set(m, word{kind: kindOwnSent, at: old.at, levels: remaining})
				continue
			}
			c.forget(m)
			changed = append(changed, event)
		}
	}
	return changed
}

// OpenLevels is the Levels of the series' own send still open
// (contract.OpenLevelSet): known only for a series whose ABNORMAL this
// process acknowledged since it was last forgotten.
func (c *Copy) OpenLevels(tenantID, strategyID, fingerprint string) ([]uint32, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w, ok := c.words[member{key: StrategyKey{TenantID: tenantID, StrategyID: strategyID}, fingerprint: fingerprint}]
	if !ok || w.kind != kindOwnSent || w.levels == nil {
		return nil, false
	}
	return slices.Clone(w.levels), true
}

// mostSevere is the open Level the link keeps the alert at: it raises the
// alert to a more severe Level and never lowers it, so while the most
// severe Level is open no other has been. Most severe is the lowest Level
// id, as the link's severity table and Python rank Levels, not the Plan's
// detect priority, which a strategy may set against the ids.
func mostSevere(open []uint32) standingLevel {
	return standingLevel{id: slices.Min(open)}
}

// openLevels is the open Levels after one envelope's results: an ABNORMAL
// result opens its Level, a RECOVERY one closes it, any other leaves it.
// Always a known set (empty, not nil, when none is open), and a new slice:
// the word's own is never changed in place.
func openLevels(open []uint32, results []contract.LevelResultV1) []uint32 {
	next := make([]uint32, 0, len(open)+len(results))
	next = append(next, open...)
	for _, result := range results {
		at := slices.Index(next, result.LevelID)
		switch {
		case result.Result == contract.LevelResultAbnormal && at < 0:
			next = append(next, result.LevelID)
		case result.Result == contract.LevelResultRecovery && at >= 0:
			next = slices.Delete(next, at, at+1)
		}
	}
	return next
}

// Track notes the strategies this process evaluates. A strategy tracked
// again after untrackedGap starts from nothing: what it knew may be another
// owner's to change since.
func (c *Copy) Track(keys ...StrategyKey) {
	if c == nil {
		return
	}
	now := c.options.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var dropped map[StrategyKey]bool
	for _, key := range keys {
		if last, ok := c.tracked[key]; ok && now.Sub(last) > untrackedGap {
			if dropped == nil {
				dropped = map[StrategyKey]bool{}
			}
			dropped[key] = true
		}
		c.tracked[key] = now
	}
	if dropped != nil {
		c.dropLocked(func(key StrategyKey) bool { return dropped[key] })
	}
}

func (c *Copy) dropLocked(drop func(StrategyKey) bool) {
	n := 0
	for m := range c.words {
		if drop(m.key) {
			c.forget(m)
			n++
		}
	}
	for m := range c.queued {
		if drop(m.key) {
			delete(c.queued, m)
			n++
		}
	}
	c.outcomes[OutcomeDropped] += uint64(n)
}

// Run runs a pass every PassInterval until ctx ends.
func (c *Copy) Run(ctx context.Context) {
	ticker := time.NewTicker(PassInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pass, cancel := context.WithTimeout(ctx, passTimeout)
			c.Pass(pass)
			cancel()
		}
	}
}

// Pass asks the link for the queued fingerprints, at most RequestsPerPass
// requests, one at a time, and prunes what aged out.
func (c *Copy) Pass(ctx context.Context) {
	if c == nil {
		return
	}
	now := c.options.Now()
	c.mu.Lock()
	c.pruneLocked(now)
	if c.unsupported && now.Before(c.retryAt) {
		c.mu.Unlock()
		return
	}
	byTenant := map[string][]member{}
	for m := range c.queued {
		byTenant[m.key.TenantID] = append(byTenant[m.key.TenantID], m)
	}
	size := c.batch
	c.mu.Unlock()
	tenants := make([]string, 0, len(byTenant))
	for tenant := range byTenant {
		tenants = append(tenants, tenant)
		sort.Slice(byTenant[tenant], func(i, j int) bool { return less(byTenant[tenant][i], byTenant[tenant][j]) })
	}
	sort.Strings(tenants)
	requests := 0
	for _, tenant := range tenants {
		members := byTenant[tenant]
		for start := 0; start < len(members); start += size {
			if requests == RequestsPerPass || ctx.Err() != nil {
				return
			}
			requests++
			if !c.ask(ctx, tenant, members[start:min(len(members), start+size)]) {
				return
			}
		}
	}
}

// ask sends one batch; false stops the pass. A fingerprint the read
// cannot name is taken out of the batch and counted, so it never holds up
// the rest.
func (c *Copy) ask(ctx context.Context, tenant string, batch []member) bool {
	asked := make([]member, 0, len(batch))
	fingerprints := make([]string, 0, len(batch))
	refused := 0
	for _, m := range batch {
		if !nativeFingerprint(m.fingerprint) {
			refused++
			continue
		}
		asked = append(asked, m)
		fingerprints = append(fingerprints, m.fingerprint)
	}
	if refused > 0 {
		c.mu.Lock()
		for _, m := range batch {
			if !nativeFingerprint(m.fingerprint) {
				delete(c.queued, m)
			}
		}
		c.outcomes[OutcomeFingerprintRefused] += uint64(refused)
		c.mu.Unlock()
	}
	if len(asked) == 0 {
		return true
	}
	batch = asked
	query, err := ReadQuery(c.options.EventSourceID, fingerprints)
	if err != nil {
		c.count(OutcomeReadFailed, 1)
		return true
	}
	sentAt := c.options.Now()
	c.count(OutcomeRequest, 1)
	var items []linkdmanage.Alert
	after := ""
	// One active alert per fingerprint: the batch fits one page. A link
	// that pages anyway is followed for as many pages as the batch could
	// fill, counted as requests.
	for page := 0; ; page++ {
		got, err := c.options.Link.Search(ctx, tenant, query, after, linkdmanage.MaxLimit)
		if err != nil {
			return c.failed(err)
		}
		items = append(items, got.Items...)
		if got.Next == "" {
			break
		}
		if page+1 >= (len(batch)+linkdmanage.MaxLimit-1)/linkdmanage.MaxLimit+1 {
			c.count(OutcomeReadFailed, 1)
			return true
		}
		after = got.Next
		c.count(OutcomeRequest, 1)
	}
	byFingerprint := make(map[string]member, len(batch))
	for _, m := range batch {
		byFingerprint[m.fingerprint] = m
	}
	hit := map[member]bool{}
	foreign := 0
	for _, item := range items {
		m, ok := byFingerprint[item.Fingerprint]
		strategy, labelled := item.Label("strategy_id")
		if !ok || item.EventSourceID != c.options.EventSourceID || item.Status != "active" || item.TenantID != tenant ||
			!labelled || strategy != m.key.StrategyID {
			foreign++
			continue
		}
		hit[m] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsupported, c.retryAt, c.retryWait = false, time.Time{}, 0
	c.refused = false
	if c.batch < BatchSize {
		c.batch = min(BatchSize, c.batch*2)
	}
	c.outcomes[OutcomeForeignItem] += uint64(foreign)
	for _, m := range batch {
		if _, still := c.queued[m]; !still {
			// Answered meanwhile by this process's own send, or dropped.
			continue
		}
		delete(c.queued, m)
		if hit[m] {
			c.set(m, word{kind: kindHit, at: sentAt})
			c.outcomes[OutcomeHit]++
			continue
		}
		w, ok := c.words[m]
		if ok && w.kind == kindMiss && sentAt.Sub(w.at) <= MissLife {
			w.at = sentAt
		} else {
			w = word{kind: kindMiss, at: sentAt, firstMiss: sentAt}
		}
		c.set(m, w)
		c.outcomes[OutcomeMiss]++
	}
	return true
}

// failed counts a failed request and says whether the pass goes on.
func (c *Copy) failed(err error) bool {
	switch {
	case linkdmanage.Status(err) == 429:
		c.count(OutcomeLinkBusy, 1)
		return false
	case linkdmanage.Unsupported(err):
		c.count(OutcomeLinkUnsupported, 1)
		c.mu.Lock()
		first := !c.unsupported
		c.unsupported = true
		c.retryWait = min(UnsupportedRetryMax, max(UnsupportedRetryMin, c.retryWait*2))
		c.retryAt = c.options.Now().Add(c.retryWait)
		c.mu.Unlock()
		if first && c.options.Warn != nil {
			c.options.Warn(err)
		}
		return false
	case linkdmanage.Status(err) == http.StatusUnauthorized, linkdmanage.Status(err) == http.StatusForbidden:
		c.count(OutcomeLinkRefused, 1)
		c.mu.Lock()
		first := !c.refused
		c.refused = true
		c.mu.Unlock()
		if first && c.options.Refused != nil {
			c.options.Refused(err)
		}
		return false
	}
	c.count(OutcomeReadFailed, 1)
	if linkdmanage.Status(err) == 502 {
		c.mu.Lock()
		c.batch = max(MinBatchSize, c.batch/2)
		c.mu.Unlock()
	}
	return false
}

// pruneLocked forgets the words past every use.
func (c *Copy) pruneLocked(now time.Time) {
	for m, w := range c.words {
		switch {
		case w.kind == kindOwnSent && now.Sub(w.at) > OwnLife,
			w.kind == kindHit && now.Sub(w.at) > MissLife,
			w.kind == kindMiss && now.Sub(w.at) > MissLife:
			c.forget(m)
		}
	}
	// A strategy untracked past the gap is let go whole: its tracked moment
	// and every word on it. Letting the moment go alone made Track see the
	// strategy as new rather than as returning, so its own sends -- kept for
	// OwnLife -- survived the absence: a returning strategy's ABNORMALs then
	// read as repeats, its new episodes were never recorded, and the
	// strategy-wide closes counted it short and were refused.
	var expired map[StrategyKey]bool
	for key, at := range c.tracked {
		if now.Sub(at) > untrackedGap {
			delete(c.tracked, key)
			if expired == nil {
				expired = map[StrategyKey]bool{}
			}
			expired[key] = true
		}
	}
	if expired != nil {
		c.dropLocked(func(key StrategyKey) bool { return expired[key] })
	}
}

func (c *Copy) count(outcome string, n int) {
	c.mu.Lock()
	c.outcomes[outcome] += uint64(n)
	c.mu.Unlock()
}

// Stats is the copy's counts and sizes.
type Stats struct {
	Answers  map[Answer]uint64
	Outcomes map[string]uint64
	Known    int
	Queued   int
	// Unsupported is a link that answers it cannot search, until it answers
	// a search again.
	Unsupported bool
}

// Stats reads the counts, every word present.
func (c *Copy) Stats() Stats {
	stats := Stats{Answers: make(map[Answer]uint64, len(Answers)), Outcomes: make(map[string]uint64, len(Outcomes))}
	for _, answer := range Answers {
		stats.Answers[answer] = 0
	}
	for _, outcome := range Outcomes {
		stats.Outcomes[outcome] = 0
	}
	if c == nil {
		return stats
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for answer, n := range c.answers {
		stats.Answers[answer] = n
	}
	for outcome, n := range c.outcomes {
		stats.Outcomes[outcome] = n
	}
	stats.Known, stats.Queued, stats.Unsupported = len(c.words), len(c.queued), c.unsupported
	return stats
}

func less(a, b member) bool {
	if a.key.StrategyID != b.key.StrategyID {
		return a.key.StrategyID < b.key.StrategyID
	}
	return a.fingerprint < b.fingerprint
}
