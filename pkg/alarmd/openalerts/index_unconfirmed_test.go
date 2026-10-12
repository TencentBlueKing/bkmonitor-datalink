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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// setFixture is a calibrated index whose set for keyA holds whatever
// members says, under Console facts the test can change.
type setFixture struct {
	c       *clock
	cache   *Cache
	facts   *factsStub
	members []string
	reads   int
}

func newSetFixture(t *testing.T, policy UnavailablePolicy, facts *factsStub, members ...string) *setFixture {
	t.Helper()
	f := &setFixture{c: &clock{at: time.Unix(1700000000, 0)}, facts: facts, members: members}
	options := indexOptions(f.c)
	options.Policy = policy
	options.Facts = facts
	if facts == nil {
		options.Facts = nil
	}
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { f.reads++; return f.members, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		return Reconciliation{Members: append([]string(nil), f.members...)}, nil
	})
	f.cache = mustIndex(t, options)
	if err := f.cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	f.cache.Refresh(context.Background())
	return f
}

func (f *setFixture) send(fingerprints ...string) {
	events := make([]contract.TriggerEventV1, 0, len(fingerprints))
	for _, fp := range fingerprints {
		events = append(events, abnormal(keyA, fp))
	}
	f.cache.Acknowledged(events)
}

// reread moves the clock and has the next round read and calibrate keyA
// again, as a change notice and a requested calibration would.
func (f *setFixture) reread(after time.Duration) {
	f.c.advance(after)
	f.cache.indexChanged(keyA)
	f.cache.RequestReconcile(keyA)
	f.cache.Refresh(context.Background())
}

func sentFingerprints(n int) []string {
	result := make([]string, n)
	for i := range result {
		result[i] = fmt.Sprintf("ours-%d", i)
	}
	return result
}

// The location the sets are read from is not one the Console named - its
// discovery failed, or it names a Redis this process holds no connection
// to - so nothing is read, the copy says why, and the gate answers from
// what this process sent: its own alert recovers, a series the set would
// have carried does not (the 2026-09-14 ruling). Before, the sets read
// from such a place answered as the link's word.
func TestAnUnconfirmedLocationIsNotReadAndTheGateAnswersFromWhatThisProcessSent(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	f := newSetFixture(t, PolicySelfMaintain, facts, "theirs")
	f.send("ours")
	f.reread(time.Minute)
	if f.reads != 0 {
		t.Fatalf("the sets were read %d times from a place the Console did not name", f.reads)
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
		t.Fatal("want this process's alert open and the other held: the gate answers from what this process sent")
	}
	stats := f.cache.Stats()
	if stats.Available || stats.UnavailableReason != UnavailableLocationUnconfirmed || stats.LocationConfirmed {
		t.Fatalf("available %v reason %q location %v, want unavailable for an unconfirmed location", stats.Available, stats.UnavailableReason, stats.LocationConfirmed)
	}
	if stats.Lookups[AnswerSelfMaintained] != 2 || stats.Lookups[AnswerIndexAbsent] != 0 {
		t.Fatalf("lookups %v, want both answered by the copy itself", stats.Lookups)
	}
}

// The location is confirmed but the link is not known to key our alerts by
// the alert id we send - not read yet, or keyed by other fields - so a
// lookup by our alert id would miss. The sets are read, which costs nothing
// in correctness, and the gate answers from what this process sent.
func TestUnconfirmedKeyingAnswersFromWhatThisProcessSent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		keyed, known bool
	}{{"not read yet", false, false}, {"keyed by other fields", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			facts := confirmedFacts()
			facts.set(true, tc.keyed, tc.known)
			f := newSetFixture(t, PolicySelfMaintain, facts, "theirs")
			f.send("ours")
			if !f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
				t.Fatal("want the gate answering from what this process sent")
			}
			stats := f.cache.Stats()
			if stats.Available || stats.UnavailableReason != UnavailableKeyingUnconfirmed || !stats.LocationConfirmed {
				t.Fatalf("available %v reason %q location %v, want unavailable for unconfirmed keying", stats.Available, stats.UnavailableReason, stats.LocationConfirmed)
			}
		})
	}
}

// Both facts hold: the gate answers from the sets, as the link's word.
func TestConfirmedSetsAnswerTheGate(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts(), "theirs")
	if !f.cache.Contains(tenant, keyA.StrategyID, "theirs") || f.cache.Contains(tenant, keyA.StrategyID, "never-sent") {
		t.Fatal("want the set's member open and a stranger not")
	}
	if stats := f.cache.Stats(); !stats.Available || stats.UnavailableReason != "" || stats.Lookups[AnswerIndexMember] != 1 {
		t.Fatalf("stats = available %v reason %q lookups %v", stats.Available, stats.UnavailableReason, stats.Lookups)
	}
}

// The review's case for the close (trigger review Dev 8): one alert of ours
// leaves the set - the link closed it on a timeout, by hand, or because
// this process sent its close - and nothing else changes. The sets stay
// trusted: what a set holds says nothing about how it is keyed. Before,
// one such alert flipped the copy to "disjoint", and the target-scope
// close, which reads only trusted sets, stopped for every strategy.
func TestAnAlertTheLinkClosedLeavesTheSetsTrusted(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts(), "ours")
	f.send("ours")
	f.reread(5*time.Minute + time.Second)
	f.members = nil
	f.reread(time.Minute)
	f.reread(10 * time.Minute)
	if !f.cache.Trusted() || !f.cache.Stats().Available {
		t.Fatal("an alert gone from the set made the copy stop trusting it")
	}
	if held, judged := f.cache.Holds(keyA, "ours"); !judged || held {
		t.Fatalf("Holds = %v judged %v, want a judged \"not held\" for the close to act on", held, judged)
	}
}

// An entry into an unconfirmed state is counted once, not every round it
// lasts; leaving and entering again counts again.
func TestAnUnconfirmedStateIsCountedOnEntry(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	f := newSetFixture(t, PolicySelfMaintain, facts)
	for round := 0; round < 5; round++ {
		f.reread(time.Minute)
	}
	if got := f.cache.Stats().Unavailable[UnavailableLocationUnconfirmed]; got != 1 {
		t.Fatalf("location_unconfirmed counted %d times over five rounds, want once", got)
	}
	facts.set(true, true, true)
	f.reread(time.Minute)
	facts.set(false, true, true)
	f.reread(time.Minute)
	if got := f.cache.Stats().Unavailable[UnavailableLocationUnconfirmed]; got != 2 {
		t.Fatalf("location_unconfirmed counted %d times, want twice after leaving and entering again", got)
	}
}

// A calibration that finds the link writing elsewhere withdraws the
// location, and the next calibration in the same round, after the move,
// confirms it again: the state opened and closed inside one round, and it is
// still counted as one entry.
func TestAnUnconfirmedStateInsideOneRoundIsCounted(t *testing.T) {
	facts := confirmedFacts()
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Facts, options.ReconcileBatch = facts, 2
	calls := 0
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		calls++
		if calls == 1 {
			facts.set(false, true, true)
			return Reconciliation{}, errors.New("the link writes elsewhere")
		}
		facts.set(true, true, true)
		return Reconciliation{}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA, {TenantID: tenant, StrategyID: "other"}}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	if calls != 2 {
		t.Fatalf("fixture: %d calibrations in the round, want both strategies", calls)
	}
	if got := cache.Stats().Unavailable[UnavailableLocationUnconfirmed]; got != 1 {
		t.Fatalf("location_unconfirmed counted %d, want the withdrawal inside the round counted once", got)
	}
}

// The rounds before the keying is first asked - every start - are not an
// entry into keying_unconfirmed: a rollout would count one on every
// replica. Once asked and still unknown, it is counted.
func TestAKeyingNotYetAskedIsNotCounted(t *testing.T) {
	facts := confirmedFacts()
	facts.set(true, false, false)
	facts.asked = false
	f := newSetFixture(t, PolicySelfMaintain, facts)
	for round := 0; round < 3; round++ {
		f.reread(time.Second)
	}
	if got := f.cache.Stats().Unavailable[UnavailableKeyingUnconfirmed]; got != 0 {
		t.Fatalf("keying_unconfirmed counted %d before the keying was asked, want none", got)
	}
	facts.mu.Lock()
	facts.asked = true
	facts.mu.Unlock()
	f.reread(time.Second)
	if got := f.cache.Stats().Unavailable[UnavailableKeyingUnconfirmed]; got != 1 {
		t.Fatalf("keying_unconfirmed counted %d once asked and still unknown, want once", got)
	}
}

// A copy that tracks no strategy has nothing to calibrate, and still asks
// the Console for its facts on every round: a follower with no strategies
// would otherwise never learn the keying and stay unconfirmed.
func TestACopyTrackingNoStrategyStillAsksForTheFacts(t *testing.T) {
	facts := confirmedFacts()
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Facts = facts
	cache := mustIndex(t, options)
	cache.Refresh(context.Background())
	cache.Refresh(context.Background())
	facts.mu.Lock()
	refreshes := facts.refreshes
	facts.mu.Unlock()
	if refreshes != 2 {
		t.Fatalf("the facts were asked for on %d of 2 rounds with no strategy tracked", refreshes)
	}
}

// When the reads move to another place, what was read at the old one is
// forgotten: a strategy answers as never read - from what this process
// sent - until it is read at the new place, not from the old place's
// members as the link's word.
func TestAMovedLocationForgetsWhatTheOldPlaceHeld(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts(), "theirs")
	f.reread(time.Second)
	if !f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
		t.Fatal("fixture: the old place's member was not read")
	}
	f.cache.LocationMoved()
	if f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
		t.Fatal("after the move the gate still answered from the old place's members")
	}
	if snapshot := f.cache.Stats(); snapshot.Loaded != 0 || snapshot.Members != 0 {
		t.Fatalf("loaded %d members %d after the move, want nothing held from the old place", snapshot.Loaded, snapshot.Members)
	}
	f.members = []string{"moved"}
	f.reread(time.Second)
	if !f.cache.Contains(tenant, keyA.StrategyID, "moved") || f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
		t.Fatal("the new place's read did not replace what was forgotten")
	}
}

// A deployment without the link's Console is not configured, not
// unavailable: nothing is read, nothing is counted, and its health says so.
// The gate answers from what this process sent.
func TestACopyWithoutAConsoleIsNotConfigured(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, nil, "theirs")
	f.send("ours")
	f.reread(time.Minute)
	if f.reads != 0 {
		t.Fatalf("a copy without a Console read the sets %d times", f.reads)
	}
	stats := f.cache.Stats()
	if stats.Configured || stats.Available || stats.UnavailableReason != "" || len(stats.Unavailable) != 0 && (stats.Unavailable[UnavailableLocationUnconfirmed] != 0 || stats.Unavailable[UnavailableKeyingUnconfirmed] != 0) {
		t.Fatalf("configured %v available %v reason %q unavailable %v, want not configured and nothing counted",
			stats.Configured, stats.Available, stats.UnavailableReason, stats.Unavailable)
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, "ours") || f.cache.Contains(tenant, keyA.StrategyID, "theirs") {
		t.Fatal("want the gate answering from what this process sent")
	}
}

// Under the pass-through policy the fallback is the policy's: every
// recovery goes while the sets are not trusted.
func TestUnconfirmedSetsFollowThePassThroughPolicy(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	f := newSetFixture(t, PolicyPassThrough, facts, "theirs")
	if !f.cache.Contains(tenant, keyA.StrategyID, "never-sent") || f.cache.Stats().Lookups[AnswerPassedThrough] == 0 {
		t.Fatal("pass-through policy not applied while unconfirmed")
	}
}

// The deployment's pattern: an anomaly resent every round while
// calibrations run in between. A calibration drops this process's own send
// records once they are older than the local retention; the record of what
// it opened is kept, and is what the gate answers from while unconfirmed.
func TestCalibrationBetweenResendsDoesNotForgetWhatWasOpened(t *testing.T) {
	facts := confirmedFacts()
	ours := sentFingerprints(2)
	// The consumer holds our alerts open throughout.
	f := newSetFixture(t, PolicySelfMaintain, facts, append([]string{"theirs-1"}, ours...)...)
	for round := 0; round < 6; round++ {
		f.send(ours...)
		f.reread(time.Minute + time.Second)
	}
	f.c.advance(3 * time.Minute)
	f.reread(time.Second)
	if _, kept := f.cache.added[member{key: keyA, fingerprint: ours[0]}]; kept {
		t.Fatal("setup: the calibration did not prune the local record, so this proves nothing")
	}
	facts.set(false, true, true)
	if !f.cache.Contains(tenant, keyA.StrategyID, ours[0]) {
		t.Fatal("after the calibration pruned the local record, the fallback forgot an alert this process opened")
	}
}

// The record is bounded by MaxLocalEntries; past it a new alert is not
// recorded and the eviction is counted.
func TestTheOpenedRecordIsBounded(t *testing.T) {
	f := newSetFixture(t, PolicySelfMaintain, confirmedFacts())
	limit := f.cache.index.options.MaxLocalEntries
	before := f.cache.Stats().Evictions
	f.send(sentFingerprints(limit + 2)...)
	if got := len(f.cache.index.opened); got != limit {
		t.Fatalf("opened record holds %d, want the bound %d", got, limit)
	}
	if f.cache.Stats().Evictions < before+2 {
		t.Fatal("alerts past the bound were dropped without being counted")
	}
}

// While unconfirmed, a full record of what this process opened takes a new
// alert by letting go of the one whose last ABNORMAL is oldest - an alert
// whose series stopped is never sent RECOVERY, so nothing else takes it
// out - and the new alert answers open past the recent sends' retention,
// so its recovery is not held. The alert sent first but still firing stays.
func TestAFullUnconfirmedRecordLetsTheStalestGoForANewAlert(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Facts, options.MaxLocalEntries = facts, 2
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "firing")})
	c.advance(5 * time.Minute)
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "vanished")})
	c.advance(10 * time.Minute)
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "firing")})
	c.advance(5 * time.Minute)
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "new")})
	c.advance(options.LocalRetention + time.Minute)
	for fingerprint, want := range map[string]bool{"new": true, "firing": true, "vanished": false} {
		if got := cache.Contains(tenant, keyA.StrategyID, fingerprint); got != want {
			t.Errorf("%s answered open %v, want %v", fingerprint, got, want)
		}
	}
	if stats := cache.Stats(); stats.OwnOpen != 2 || stats.OwnOpenDepartures[DepartureEvicted] != 1 {
		t.Fatalf("own open %d departures %v, want 2 held and 1 evicted", stats.OwnOpen, stats.OwnOpenDepartures)
	}
}

// One acknowledged batch of first sends against a full record makes room for
// all of them in one pass: as many as the batch needs leave, an alert whose
// RECOVERY was sent before any still open, then the oldest last send, and
// every alert of the batch is held.
func TestAFullRecordMakesRoomForABatchInOnePass(t *testing.T) {
	facts := confirmedFacts()
	facts.set(false, true, true)
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	options.Facts, options.MaxLocalEntries = facts, 4
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range []string{"oldest", "older", "recent", "recovered"} {
		cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, fingerprint)})
		c.advance(time.Minute)
	}
	cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "recovered")})
	cache.Acknowledged([]contract.TriggerEventV1{abnormal(keyA, "new-1"), abnormal(keyA, "new-2"), abnormal(keyA, "new-1")})
	c.advance(options.LocalRetention + time.Minute)
	want := map[string]bool{"new-1": true, "new-2": true, "recent": true, "older": true, "oldest": false, "recovered": false}
	for fingerprint, open := range want {
		if got := cache.Contains(tenant, keyA.StrategyID, fingerprint); got != open {
			t.Errorf("%s answered open %v, want %v", fingerprint, got, open)
		}
	}
	if stats := cache.Stats(); stats.OwnOpen != 4 || stats.OwnOpenDepartures[DepartureEvicted] != 2 {
		t.Fatalf("own open %d departures %v, want 4 held and the 2 the batch needed evicted", stats.OwnOpen, stats.OwnOpenDepartures)
	}
}
