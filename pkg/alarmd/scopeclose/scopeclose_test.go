// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scopeclose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

const (
	tenant   = "tenant-test"
	strategy = "42"
	business = "2"
	ownSrc   = "source-own"
	otherSrc = "source-other"
)

var key = openalerts.StrategyKey{TenantID: tenant, StrategyID: strategy}

func fp(n int) string { return fmt.Sprintf("%032x", n) }

// fakeSet is the open alert copy as the close sees it: which fingerprints
// the strategy's set holds, which alerts calibration listed, whether it can
// be judged at all.
type fakeSet struct {
	disjoint bool
	unjudged bool
	own      string
	held     map[string]bool
	alerts   []openalerts.Alert
}

func (set *fakeSet) Disjoint() bool { return set.disjoint }
func (set *fakeSet) MemberCount(k openalerts.StrategyKey) (int, bool) {
	if set.unjudged || set.disjoint || k != key {
		return 0, false
	}
	n := 0
	for _, held := range set.held {
		if held {
			n++
		}
	}
	return n, true
}
func (set *fakeSet) Holds(k openalerts.StrategyKey, fingerprint string) (bool, bool) {
	if set.unjudged || set.disjoint || k != key {
		return false, false
	}
	return set.held[fingerprint], true
}
func (set *fakeSet) ActiveAlerts(k openalerts.StrategyKey) []openalerts.Alert {
	if k != key {
		return nil
	}
	return set.alerts
}
func (set *fakeSet) OwnEventSourceID() string { return set.own }

// openSet holds each fingerprint as an open alert of the given source.
func openSet(source string, fingerprints ...string) *fakeSet {
	set := &fakeSet{own: ownSrc, held: map[string]bool{}}
	for i, f := range fingerprints {
		set.held[f] = true
		set.alerts = append(set.alerts, openalerts.Alert{AlertID: fmt.Sprintf("alert-%d", i), EventSourceID: source, Fingerprint: f})
	}
	return set
}

type recordingWriter struct {
	batches [][]linkdoutput.CloseRequest
	fail    error
}

func (w *recordingWriter) WriteCloseBatch(_ context.Context, batch []linkdoutput.CloseRequest) error {
	if w.fail != nil {
		return w.fail
	}
	w.batches = append(w.batches, append([]linkdoutput.CloseRequest(nil), batch...))
	return nil
}

func (w *recordingWriter) sent() []linkdoutput.CloseRequest {
	var all []linkdoutput.CloseRequest
	for _, batch := range w.batches {
		all = append(all, batch...)
	}
	return all
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func drop(fingerprint string, round int64) Drop {
	return Drop{TenantID: tenant, BusinessID: business, StrategyID: strategy, Fingerprint: fingerprint,
		StrategyRevision: 7, Round: round}
}

type fixture struct {
	clock  *clock
	set    *fakeSet
	writer *recordingWriter
	closer *Closer
}

func newFixture(set *fakeSet, send bool, mutate func(*Options)) *fixture {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := Options{Send: send, Now: c.now, MaxEntries: 16, Batch: 8, ObservationTTL: 30 * time.Minute}
	if mutate != nil {
		mutate(&options)
	}
	f := &fixture{clock: c, set: set, writer: &recordingWriter{}, closer: New(options)}
	f.closer.Bind(set, f.writer)
	return f
}

// slot is one Slot turning the drops away, as the access path hands them
// over - Screen once, then each drop one by one or in bulk - and the
// close's step after it.
func (f *fixture) slot(round int64, drops ...Drop) {
	screens := map[openalerts.StrategyKey]string{}
	bulk := map[openalerts.StrategyKey]map[string]int{}
	defer func() {
		for k, words := range bulk {
			for word, count := range words {
				f.closer.Count(k, "group", round, word, count)
			}
		}
		f.closer.Step(context.Background())
		f.clock.at = f.clock.at.Add(time.Minute)
	}()
	for _, d := range drops {
		d.Round = round
		k := openalerts.StrategyKey{TenantID: d.TenantID, StrategyID: d.StrategyID}
		screen, done := screens[k]
		if !done {
			screen = f.closer.Screen(k)
			screens[k] = screen
		}
		if screen != "" {
			if bulk[k] == nil {
				bulk[k] = map[string]int{}
			}
			bulk[k][screen]++
			continue
		}
		f.closer.Observe(d)
	}
}

func (f *fixture) want(t *testing.T, name string, want map[string]uint64) {
	t.Helper()
	got := f.closer.Stats()
	for _, outcome := range Outcomes {
		if got[outcome] != want[outcome] {
			t.Errorf("%s: %s = %d, want %d (all: %v)", name, outcome, got[outcome], want[outcome], got)
		}
	}
}

// The scenarios the close exists for and every way it must refuse, one row
// each: which drops two Slots saw, what the copy said, and what was
// decided. Every outcome is asserted in every row, zeros included, so a
// refusal counted under the wrong word fails the row.
func TestTargetScopeCloseDecisions(t *testing.T) {
	member := fp(1)
	cases := []struct {
		name  string
		set   *fakeSet
		send  bool
		drops []Drop
		want  map[string]uint64
		sent  int
	}{
		{name: "an open alert of ours turned away by two Slots is closed", set: openSet(ownSrc, member), send: true,
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeClosed: 1}, sent: 1},
		{name: "unarmed, the same close is decided and not sent", set: openSet(ownSrc, member),
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeWouldSend: 1}},
		{name: "a strategy with no open alert is counted in bulk", set: openSet(ownSrc), send: true,
			drops: []Drop{drop(member, 0), drop(fp(2), 0)}, want: map[string]uint64{OutcomeNotMember: 4}},
		{name: "a fingerprint the set does not hold is not closed", set: openSet(ownSrc, member), send: true,
			drops: []Drop{drop(fp(2), 0)}, want: map[string]uint64{OutcomeNotMember: 2}},
		{name: "a record with no fingerprint is not closed", set: openSet(ownSrc, member), send: true,
			drops: []Drop{drop("", 0)}, want: map[string]uint64{OutcomeNotMember: 2}},
		{name: "an open alert of another source is not closed", set: openSet(otherSrc, member), send: true,
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeUnconfirmed: 1, OutcomeProducerForeign: 1}},
		{name: "a strategy whose set cannot be judged (uncalibrated) is not acted on",
			set: func() *fakeSet { s := openSet(ownSrc, member); s.unjudged = true; return s }(), send: true,
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
		{name: "disjoint sets are not acted on",
			set: func() *fakeSet { s := openSet(ownSrc, member); s.disjoint = true; return s }(), send: true,
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
		{name: "a copy that has not learned its own source is not acted on",
			set: func() *fakeSet { s := openSet(ownSrc, member); s.own = ""; return s }(), send: true,
			drops: []Drop{drop(member, 0)}, want: map[string]uint64{OutcomeSetUnavailable: 2}},
	}
	for _, c := range cases {
		f := newFixture(c.set, c.send, nil)
		f.slot(1700000000, c.drops...)
		f.slot(1700000060, c.drops...)
		f.want(t, c.name, c.want)
		if got := len(f.writer.sent()); got != c.sent {
			t.Errorf("%s: sent %d closes, want %d", c.name, got, c.sent)
		}
	}
}

// One Slot is one observation however often it turns the series away: a
// retried Slot, or two queries of one Slot delivering the same series,
// share one evaluation time. The close waits for a second Slot.
func TestOneSlotIsOneObservation(t *testing.T) {
	member := fp(1)
	f := newFixture(openSet(ownSrc, member), true, nil)
	f.slot(1700000000, drop(member, 0), drop(member, 0))
	f.slot(1700000000, drop(member, 0))
	f.want(t, "the same Slot three times", map[string]uint64{OutcomeUnconfirmed: 1})
	if len(f.writer.sent()) != 0 {
		t.Fatal("closed on one Slot's observation")
	}
	f.slot(1700000060, drop(member, 0))
	f.want(t, "a second Slot", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeClosed: 1})
}

// What is sent is the same close every other close is: one evaluation at
// every level, action closed, under this close's own reason, for the alert
// the calibration listed, with the strategy's frozen revision.
func TestTheCloseIsTheSharedCloseWithItsOwnReason(t *testing.T) {
	member := fp(1)
	f := newFixture(openSet(ownSrc, member), true, nil)
	f.slot(1700000000, drop(member, 0))
	f.slot(1700000060, drop(member, 0))
	sent := f.writer.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d, want 1", len(sent))
	}
	request := sent[0]
	if request.Reason != "target_out_of_scope" || request.AlertInstanceID != "alert-0" || request.Fingerprint != member ||
		request.StrategyID != 42 || request.BusinessID != 2 || request.StrategyRevision != 7 || request.TenantID != tenant {
		t.Fatalf("request = %+v", request)
	}
	event, err := linkdoutput.ConvertClose(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var evaluations []map[string]string
	if err := json.Unmarshal(payload["evaluations"], &evaluations); err != nil {
		t.Fatal(err)
	}
	if len(evaluations) != 1 || evaluations[0]["severity"] != "__ALL__" || evaluations[0]["action"] != "closed" ||
		evaluations[0]["action_reason"] != "target_out_of_scope" {
		t.Fatalf("evaluations = %v", evaluations)
	}
}

// Unarmed, a decided fingerprint is not decided again while the Slots keep
// turning it away: would_send counts alerts, not Slots.
func TestWouldSendCountsTheAlertOnce(t *testing.T) {
	member := fp(1)
	f := newFixture(openSet(ownSrc, member), false, nil)
	for i := int64(0); i < 10; i++ {
		f.slot(1700000000+60*i, drop(member, 0))
	}
	f.want(t, "ten Slots unarmed", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeWouldSend: 1})
	if len(f.writer.batches) != 0 {
		t.Fatal("an unarmed close sent something")
	}
}

// A refused send is kept and retried; a set that could not be judged at the
// step keeps the observation for when it can.
func TestARefusedStepKeepsTheObservation(t *testing.T) {
	member := fp(1)
	f := newFixture(openSet(ownSrc, member), true, nil)
	f.writer.fail = errors.New("broker down")
	f.slot(1700000000, drop(member, 0))
	f.slot(1700000060, drop(member, 0))
	f.want(t, "send refused", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeSendFailed: 1})
	f.writer.fail = nil
	f.set.disjoint = true
	f.slot(1700000120, drop(member, 0))
	f.want(t, "copy disjoint", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeSendFailed: 1, OutcomeSetUnavailable: 2})
	f.set.disjoint = false
	f.slot(1700000180, drop(member, 0))
	f.want(t, "retried", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeSendFailed: 1, OutcomeSetUnavailable: 2, OutcomeClosed: 1})
}

// At most Batch closes per step, and the next step starts after the last
// one decided rather than at the head again.
func TestClosesAreBoundedPerStepAndRotate(t *testing.T) {
	fingerprints := []string{fp(1), fp(2), fp(3)}
	f := newFixture(openSet(ownSrc, fingerprints...), true, func(o *Options) { o.Batch = 2 })
	drops := []Drop{drop(fingerprints[0], 0), drop(fingerprints[1], 0), drop(fingerprints[2], 0)}
	f.slot(1700000000, drops...)
	f.slot(1700000060, drops...)
	if len(f.writer.batches) != 1 || len(f.writer.batches[0]) != 2 {
		t.Fatalf("first step sent %v, want one batch of two", f.writer.batches)
	}
	f.slot(1700000120, drops...)
	if len(f.writer.batches) != 2 || len(f.writer.batches[1]) != 1 || f.writer.batches[1][0].Fingerprint != fingerprints[2] {
		t.Fatalf("second step sent %v, want the one left", f.writer.batches)
	}
}

// The observation table is bounded; a fingerprint past the bound is named
// and waits for a later Slot. An observation that waited longer than its
// TTL for a second is forgotten and starts over.
func TestTheObservationsAreBoundedAndExpire(t *testing.T) {
	f := newFixture(openSet(ownSrc, fp(1), fp(2)), true, func(o *Options) { o.MaxEntries = 1 })
	f.slot(1700000000, drop(fp(1), 0), drop(fp(2), 0))
	f.want(t, "past the bound", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeMemoryFull: 1})

	g := newFixture(openSet(ownSrc, fp(1)), true, nil)
	g.slot(1700000000, drop(fp(1), 0))
	g.clock.at = g.clock.at.Add(30 * time.Minute)
	g.slot(1700003600, drop(fp(1), 0))
	g.want(t, "second observation after the TTL", map[string]uint64{OutcomeUnconfirmed: 2})
	if len(g.writer.sent()) != 0 {
		t.Fatal("closed on two observations further apart than the TTL")
	}
}

// Nothing bound yet: the screen and every observation that needs the copy
// are refused by name.
func TestAnUnboundCloserRefuses(t *testing.T) {
	closer := New(Options{Send: true})
	if screen := closer.Screen(key); screen != OutcomeSetUnavailable {
		t.Fatalf("Screen = %q, want set_unavailable", screen)
	}
	closer.Observe(drop(fp(1), 1))
	closer.Step(context.Background())
	if got := closer.Stats()[OutcomeSetUnavailable]; got != 1 {
		t.Fatalf("set_unavailable = %d, want 1", got)
	}
}

// The facts carry per-strategy counts and at most three prefixes of each
// list, never a whole fingerprint.
func TestFactsAreBoundedPrefixes(t *testing.T) {
	fingerprints := []string{fp(1), fp(2), fp(3), fp(4)}
	f := newFixture(openSet(ownSrc, fingerprints...), false, nil)
	var drops []Drop
	for _, value := range fingerprints {
		drops = append(drops, drop(value, 0))
	}
	f.slot(1700000000, drops...)
	facts := f.closer.Facts()
	if len(facts.Strategies) != 1 {
		t.Fatalf("strategies = %+v", facts.Strategies)
	}
	row := facts.Strategies[0]
	if row.Pending != 4 || len(row.PendingSample) != 3 || row.Outcomes[OutcomeUnconfirmed] != 4 || facts.Pending != 4 || facts.Armed {
		t.Fatalf("facts = %+v", facts)
	}
	for _, sample := range row.PendingSample {
		if len(sample) != 8 {
			t.Fatalf("sample %q is not an 8-character prefix", sample)
		}
	}
	f.slot(1700000060, drops...)
	facts = f.closer.Facts()
	row = facts.Strategies[0]
	if row.Outcomes[OutcomeWouldSend] != 4 || len(row.DecidedSample) != 3 || facts.Outcomes[OutcomeWouldSend] != 4 {
		t.Fatalf("facts after the decision = %+v", facts)
	}
}

// Rotation is what keeps one stuck close from starving the rest: a close
// that failed stays for the next step, and the next step starts after it
// rather than at the head again.
func TestTheNextStepStartsAfterTheLastDecided(t *testing.T) {
	first, second := fp(1), fp(2)
	f := newFixture(openSet(ownSrc, first, second), true, func(o *Options) { o.Batch = 1 })
	f.writer.fail = errors.New("broker down")
	f.slot(1700000000, drop(first, 0), drop(second, 0))
	f.slot(1700000060, drop(first, 0), drop(second, 0))
	f.writer.fail = nil
	f.slot(1700000120, drop(first, 0), drop(second, 0))
	sent := f.writer.sent()
	if len(sent) != 1 || sent[0].Fingerprint != second {
		t.Fatalf("sent %v, want the one after the failed head", sent)
	}
}

// observeAt is one Slot's rejection at a given instant, without a step.
func (f *fixture) observeAt(at time.Time, round int64, fingerprint string) {
	f.clock.at = at
	f.closer.Observe(drop(fingerprint, round))
}

// The observation TTL, both sides: a second observation 29m59s after the
// first confirms; one 30m after starts over.
func TestTheObservationTTLBoundary(t *testing.T) {
	for _, c := range []struct {
		gap  time.Duration
		want map[string]uint64
		sent int
	}{
		{30*time.Minute - time.Second, map[string]uint64{OutcomeUnconfirmed: 1, OutcomeClosed: 1}, 1},
		{30 * time.Minute, map[string]uint64{OutcomeUnconfirmed: 2}, 0},
	} {
		f := newFixture(openSet(ownSrc, fp(1)), true, nil)
		start := f.clock.at
		f.observeAt(start, 1700000000, fp(1))
		f.observeAt(start.Add(c.gap), 1700001800, fp(1))
		f.closer.Step(context.Background())
		f.want(t, c.gap.String(), c.want)
		if len(f.writer.sent()) != c.sent {
			t.Errorf("%s: sent %d, want %d", c.gap, len(f.writer.sent()), c.sent)
		}
	}
}

// The dedupe window, both sides: a decided fingerprint turned away again
// 29m59s later is not observed; 30m later it is a first observation again.
func TestTheDedupeTTLBoundary(t *testing.T) {
	for _, c := range []struct {
		gap         time.Duration
		unconfirmed uint64
	}{
		{30*time.Minute - time.Second, 1},
		{30 * time.Minute, 2},
	} {
		f := newFixture(openSet(ownSrc, fp(1)), false, nil)
		start := f.clock.at
		f.observeAt(start, 1700000000, fp(1))
		f.observeAt(start.Add(time.Minute), 1700000060, fp(1))
		f.closer.Step(context.Background())
		if f.closer.Stats()[OutcomeWouldSend] != 1 {
			t.Fatalf("%s: fixture did not decide", c.gap)
		}
		f.observeAt(start.Add(time.Minute+c.gap), 1700009999, fp(1))
		if got := f.closer.Stats()[OutcomeUnconfirmed]; got != c.unconfirmed {
			t.Errorf("%s after the decision: unconfirmed = %d, want %d", c.gap, got, c.unconfirmed)
		}
	}
}

// Freshness, both sides: a confirmed close is sent while its last
// observation is at most 60 s old, and held back one second past that -
// the target may be back in scope, and only a fresh rejection says it is
// not. A fresh observation releases it.
func TestTheFreshnessBoundary(t *testing.T) {
	for _, c := range []struct {
		age  time.Duration
		sent int
	}{
		{time.Minute, 1},
		{time.Minute + time.Second, 0},
	} {
		f := newFixture(openSet(ownSrc, fp(1)), true, nil)
		start := f.clock.at
		f.observeAt(start, 1700000000, fp(1))
		f.observeAt(start.Add(time.Minute), 1700000060, fp(1))
		f.clock.at = start.Add(time.Minute + c.age)
		f.closer.Step(context.Background())
		if len(f.writer.sent()) != c.sent {
			t.Errorf("age %s: sent %d, want %d", c.age, len(f.writer.sent()), c.sent)
		}
		if c.sent == 0 {
			f.want(t, "stale", map[string]uint64{OutcomeUnconfirmed: 1, OutcomeStaleDeferred: 1})
			f.observeAt(f.clock.at, 1700000180, fp(1))
			f.closer.Step(context.Background())
			if len(f.writer.sent()) != 1 {
				t.Errorf("a fresh observation did not release the deferred close")
			}
		}
	}
}

// Screen is per strategy: a strategy with open alerts is cleared while
// another whose set is not calibrated is not, and an empty set is
// not_member without any per-record work.
func TestScreenJudgesPerStrategy(t *testing.T) {
	f := newFixture(openSet(ownSrc, fp(1)), true, nil)
	if screen := f.closer.Screen(key); screen != "" {
		t.Fatalf("Screen(open strategy) = %q, want cleared", screen)
	}
	if screen := f.closer.Screen(openalerts.StrategyKey{TenantID: tenant, StrategyID: "other"}); screen != OutcomeSetUnavailable {
		t.Fatalf("Screen(uncalibrated strategy) = %q, want set_unavailable", screen)
	}
	empty := newFixture(openSet(ownSrc), true, nil)
	if screen := empty.closer.Screen(key); screen != OutcomeNotMember {
		t.Fatalf("Screen(empty set) = %q, want not_member", screen)
	}
}

// countingDisjointSet is a copy whose sets are disjoint and whose per
// strategy answers do not know it: the whole-copy gate is what refuses.
type countingDisjointSet struct{ fakeSet }

func (set *countingDisjointSet) MemberCount(openalerts.StrategyKey) (int, bool) { return 1, true }

// Disjoint sets refuse the screen on their own, whatever a strategy's
// count says.
func TestScreenRefusesDisjointSets(t *testing.T) {
	set := &countingDisjointSet{fakeSet: *openSet(ownSrc, fp(1))}
	set.disjoint = true
	closer := New(Options{})
	closer.Bind(set, nil)
	if screen := closer.Screen(key); screen != OutcomeSetUnavailable {
		t.Fatalf("Screen = %q, want set_unavailable on disjoint sets", screen)
	}
}

// A retried Slot reports its round again: counted once, at the largest
// total any attempt reported. A new round counts afresh.
func TestARetriedRoundIsCountedOnce(t *testing.T) {
	closer := New(Options{})
	closer.Count(key, "group", 1700000000, OutcomeNotMember, 3) // an attempt that failed halfway
	closer.Count(key, "group", 1700000000, OutcomeNotMember, 5) // the retry that completed
	closer.Count(key, "group", 1700000000, OutcomeNotMember, 5) // a retry after that
	if got := closer.Stats()[OutcomeNotMember]; got != 5 {
		t.Fatalf("not_member = %d after three attempts of one round, want 5", got)
	}
	closer.Count(key, "group", 1700000060, OutcomeNotMember, 2)
	if got := closer.Stats()[OutcomeNotMember]; got != 7 {
		t.Fatalf("not_member = %d after the next round, want 7", got)
	}
}

// Several Plans of one strategy - another business, another shard, another
// Query Group - report the same round: those are summed, while each one's
// own retries are still counted once.
func TestReportersOfOneStrategyAreSummedAndTheirRetriesAreNot(t *testing.T) {
	closer := New(Options{})
	closer.Count(key, "group-a|business-2", 1700000000, OutcomeNotMember, 3)
	closer.Count(key, "group-b|business-2", 1700000000, OutcomeNotMember, 4)
	closer.Count(key, "group-a|business-3", 1700000000, OutcomeNotMember, 5)
	closer.Count(key, "group-b|business-2", 1700000000, OutcomeNotMember, 4) // a retry
	if got := closer.Stats()[OutcomeNotMember]; got != 12 {
		t.Fatalf("not_member = %d, want 3+4+5 = 12", got)
	}
	if got := closer.Facts().Strategies[0].Outcomes[OutcomeNotMember]; got != 12 {
		t.Fatalf("the strategy's own count = %d, want 12", got)
	}
}
