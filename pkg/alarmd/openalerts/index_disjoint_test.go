// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// disjointFixture is a calibrated index whose set for keyA holds whatever
// members says. Calibration is what makes the shape real: on a calibrated
// entry an alert this process sent is no longer answered from its own
// record once the local retention has passed, so the set's word is final,
// and a set that does not carry this process's alerts holds every one of
// their recoveries.
type disjointFixture struct {
	c       *clock
	cache   *Cache
	members []string
}

func newDisjointFixture(t *testing.T, policy UnavailablePolicy, members ...string) *disjointFixture {
	t.Helper()
	f := &disjointFixture{c: &clock{at: time.Unix(1700000000, 0)}, members: members}
	options := indexOptions(f.c)
	options.Policy = policy
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return f.members, nil })
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

func (f *disjointFixture) send(fingerprints ...string) {
	events := make([]contract.TriggerEventV1, 0, len(fingerprints))
	for _, fp := range fingerprints {
		events = append(events, abnormal(keyA, fp))
	}
	f.cache.Acknowledged(events)
}

// reread moves the clock and has the next round read and calibrate keyA
// again, as a change notice and a requested calibration would.
func (f *disjointFixture) reread(after time.Duration) {
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

// The production shape: the sets were read, calibrated and hold members,
// and yet not one of the alerts this process opened is among them. Before
// the fix every recovery was held as "no open alert", the alert stayed open
// in the consumer, and nothing said why. Now the copy names the state and
// the gate answers from what this process sent: its own alerts recover, a
// series it never sent the ABNORMAL for is still held.
func TestSetsCarryingNoneOfOurAlertsLetOurRecoveriesThroughAndSaySo(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1", "theirs-2")
	ours := sentFingerprints(DisjointMinimum)
	f.send(ours...)
	f.reread(SentConfirmAfter + time.Second)

	stats := f.cache.Stats()
	if !stats.Disjoint || stats.SentInSet != 0 || stats.SentNotInSet != DisjointMinimum {
		t.Fatalf("stats = disjoint %v in %d not-in %d, want disjoint with all %d of ours missing",
			stats.Disjoint, stats.SentInSet, stats.SentNotInSet, DisjointMinimum)
	}
	if stats.Available || stats.UnavailableReason != UnavailableMembersDisjoint || stats.Unavailable[UnavailableMembersDisjoint] != 1 {
		t.Fatalf("available %v reason %q count %d, want the state named once", stats.Available, stats.UnavailableReason, stats.Unavailable[UnavailableMembersDisjoint])
	}
	before := f.cache.Stats().Lookups[AnswerSelfMaintained]
	if !f.cache.Contains(tenant, keyA.StrategyID, ours[0]) {
		t.Fatal("a recovery for an alert this process opened is held against sets that carry none of ours")
	}
	if f.cache.Contains(tenant, keyA.StrategyID, "never-sent") {
		t.Fatal("a series this process never sent the ABNORMAL for passed; the fallback is what we sent, not everything")
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, "theirs-1") {
		t.Fatal("a fingerprint the sets do carry stopped being open")
	}
	if got := f.cache.Stats().Lookups[AnswerSelfMaintained] - before; got != 3 {
		t.Fatalf("self_maintained lookups grew by %d, want 3: every gate answer in this state is the copy's own", got)
	}
	// Staying in the state is not counted again.
	f.reread(time.Minute)
	if got := f.cache.Stats().Unavailable[UnavailableMembersDisjoint]; got != 1 {
		t.Fatalf("members_disjoint counted %d times, want once per entry into the state", got)
	}
}

// One alert of ours found anywhere is a set keyed our way; the rest missing
// are alerts the consumer closed on its own, and their recoveries are held
// exactly as before the fix.
func TestOneOfOurAlertsInTheSetIsNotDisjoint(t *testing.T) {
	ours := sentFingerprints(DisjointMinimum + 2)
	f := newDisjointFixture(t, PolicySelfMaintain, ours[0], "theirs-1")
	f.send(ours...)
	f.reread(SentConfirmAfter + time.Second)
	stats := f.cache.Stats()
	if stats.Disjoint || stats.SentInSet != 1 || stats.SentNotInSet != len(ours)-1 {
		t.Fatalf("stats = disjoint %v in %d not-in %d, want not disjoint with 1 found", stats.Disjoint, stats.SentInSet, stats.SentNotInSet)
	}
	if f.cache.Contains(tenant, keyA.StrategyID, ours[1]) {
		t.Fatal("an alert the consumer closed on its own had its recovery let through")
	}
}

// The two constants are boundaries; each is tested on both sides.
func TestDisjointNeedsEnoughAlertsOldEnough(t *testing.T) {
	t.Run("nothing of ours to compare", func(t *testing.T) {
		f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
		f.reread(SentConfirmAfter + time.Second)
		if stats := f.cache.Stats(); stats.Disjoint || stats.SentNotInSet != 0 {
			t.Fatalf("disjoint %v not-in %d, want a set with nothing of ours to compare left alone", stats.Disjoint, stats.SentNotInSet)
		}
	})
	t.Run("a single alert of ours, old enough and missing", func(t *testing.T) {
		f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
		f.send("ours-only")
		f.reread(SentConfirmAfter + time.Second)
		if !f.cache.Stats().Disjoint || !f.cache.Contains(tenant, keyA.StrategyID, "ours-only") {
			t.Fatal("a deployment with one alert never reaches the fallback")
		}
	})
	t.Run("alerts a second too young for the consumer to have opened", func(t *testing.T) {
		f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
		f.send(sentFingerprints(DisjointMinimum)...)
		f.reread(SentConfirmAfter - time.Second)
		if stats := f.cache.Stats(); stats.Disjoint || stats.SentInSet+stats.SentNotInSet != 0 {
			t.Fatalf("disjoint %v counted %d, want nothing counted before SentConfirmAfter", stats.Disjoint, stats.SentInSet+stats.SentNotInSet)
		}
	})
	t.Run("exactly SentConfirmAfter old", func(t *testing.T) {
		f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
		f.send(sentFingerprints(DisjointMinimum)...)
		f.reread(SentConfirmAfter)
		if !f.cache.Stats().Disjoint {
			t.Fatal("alerts exactly SentConfirmAfter old at the read are counted")
		}
	})
}

// Age is from the first ABNORMAL, not the latest: a long-running anomaly is
// resent every round, and measuring from the latest send would never let
// it be counted.
func TestAResentAbnormalKeepsItsFirstSendTime(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	ours := sentFingerprints(DisjointMinimum)
	f.send(ours...)
	f.c.advance(SentConfirmAfter - time.Minute)
	f.send(ours...)
	f.reread(time.Minute)
	if !f.cache.Stats().Disjoint {
		t.Fatal("a resent ABNORMAL restarted its age; a long-running alert would never be counted")
	}
}

// The state ends on its own: once any alert of ours shows up, the gate
// answers from the sets again.
func TestDisjointEndsWhenOneOfOurAlertsAppears(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	ours := sentFingerprints(3)
	f.send(ours...)
	f.reread(SentConfirmAfter + time.Second)
	if !f.cache.Stats().Disjoint {
		t.Fatal("setup: not disjoint")
	}
	f.members = []string{"theirs-1", ours[0]}
	f.reread(time.Minute)
	if stats := f.cache.Stats(); stats.Disjoint || stats.SentInSet != 1 || stats.UnavailableReason == UnavailableMembersDisjoint {
		t.Fatalf("stats = %+v, want the state over", stats)
	}
	if f.cache.Contains(tenant, keyA.StrategyID, ours[1]) {
		t.Fatal("after the state ended the gate still answered from what this process sent")
	}
}

// Under the pass-through policy the fallback is the policy's: every
// recovery goes.
func TestDisjointFollowsThePassThroughPolicy(t *testing.T) {
	f := newDisjointFixture(t, PolicyPassThrough, "theirs-1")
	f.send(sentFingerprints(DisjointMinimum)...)
	f.reread(SentConfirmAfter + time.Second)
	if !f.cache.Contains(tenant, keyA.StrategyID, "never-sent") || f.cache.Stats().Lookups[AnswerPassedThrough] == 0 {
		t.Fatal("pass-through policy not applied while disjoint")
	}
}

// The deployment's pattern: an anomaly resent every round while
// calibrations run in between. A calibration drops this process's own send
// records once they are older than the local retention, so a first-send
// time kept there would restart at every resend, never reach
// SentConfirmAfter, and the fallback would have nothing to answer from.
func TestCalibrationBetweenResendsDoesNotForgetWhatWasOpened(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	ours := sentFingerprints(DisjointMinimum)
	for round := 0; round < 6; round++ {
		f.send(ours...)
		f.reread(time.Minute + time.Second)
	}
	if !f.cache.Stats().Disjoint {
		t.Fatal("alerts resent every round across calibrations were never old enough to count")
	}
	f.c.advance(3 * time.Minute)
	f.reread(time.Second)
	if _, kept := f.cache.added[member{key: keyA, fingerprint: ours[0]}]; kept {
		t.Fatal("setup: the calibration did not prune the local record, so this proves nothing")
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, ours[0]) {
		t.Fatal("after the calibration pruned the local record, the fallback forgot an alert this process opened")
	}
}

// A RECOVERY ends the record: the next ABNORMAL is a new alert, and its age
// starts again.
func TestARecoveryRestartsTheAge(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	ours := sentFingerprints(3)
	f.send(ours...)
	f.c.advance(SentConfirmAfter)
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, ours[0])})
	f.send(ours[0])
	f.reread(time.Second)
	if stats := f.cache.Stats(); stats.SentNotInSet != len(ours)-1 {
		t.Fatalf("not-in %d, want %d: the reopened alert is too young to count", stats.SentNotInSet, len(ours)-1)
	}
}

// The record is bounded by MaxLocalEntries; past it a new alert is not
// recorded and the eviction is counted.
func TestTheOpenedRecordIsBounded(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain)
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

// An alert the calibration finds active in the consumer's store but missing
// from the Redis set is still an alert the consumer holds, keyed our way:
// it counts as found, and the sets are not disjoint.
func TestAnAlertTheCalibrationFindsMissingFromTheSetCountsAsFound(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	ours := sentFingerprints(DisjointMinimum)
	options := indexOptions(c)
	options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return []string{"theirs-1"}, nil })
	options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
		return Reconciliation{Members: []string{"theirs-1", ours[0]}, Missing: []string{ours[0]}}, nil
	})
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	events := make([]contract.TriggerEventV1, 0, len(ours))
	for _, fp := range ours {
		events = append(events, abnormal(keyA, fp))
	}
	cache.Acknowledged(events)
	c.advance(SentConfirmAfter + time.Second)
	cache.RequestReconcile(keyA)
	cache.Refresh(context.Background())
	if stats := cache.Stats(); stats.Disjoint || stats.SentInSet != 1 {
		t.Fatalf("disjoint %v in %d, want the calibration's missing member found", stats.Disjoint, stats.SentInSet)
	}
}

// The fallback's own recoveries shrink the count it entered on; that must
// not end it. Three alerts enter, one recovers through the fallback, and
// the other two still go out against sets that carry none of ours.
func TestTheRecoveriesTheFallbackReleasesDoNotEndIt(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1", "theirs-2")
	ours := sentFingerprints(3)
	f.send(ours...)
	f.reread(SentConfirmAfter + time.Second)
	if !f.cache.Stats().Disjoint {
		t.Fatal("setup: not disjoint")
	}
	for i, fp := range ours[:2] {
		if !f.cache.Contains(tenant, keyA.StrategyID, fp) {
			t.Fatalf("recovery %d held", i)
		}
		f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, fp)})
		f.reread(time.Minute)
		if !f.cache.Stats().Disjoint {
			t.Fatalf("after %d recoveries the state ended with none of ours ever found", i+1)
		}
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, ours[2]) {
		t.Fatal("the last alert of ours was held again")
	}
}

// With nothing of ours left open there is nothing left for the fallback to
// answer, and the state ends: the next alert has to show the sets disjoint
// again on its own.
func TestDisjointEndsWhenNothingOfOursIsLeftOpen(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	f.send("ours-only")
	f.reread(SentConfirmAfter + time.Second)
	if !f.cache.Stats().Disjoint {
		t.Fatal("setup: not disjoint")
	}
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "ours-only")})
	f.reread(time.Minute)
	if f.cache.Stats().Disjoint {
		t.Fatal("the state outlived every alert it was answering for")
	}
}

// The count reaches zero without the sets changing when every alert of
// ours still open is too young to count: the old ones recovered through
// the fallback, a new one opened a minute ago. Ending the state then would
// hold the new alert's recovery against sets that have never carried one
// of ours.
func TestAYoungAlertKeepsTheStateAfterTheOldOnesRecover(t *testing.T) {
	f := newDisjointFixture(t, PolicySelfMaintain, "theirs-1")
	f.send("ours-old")
	f.reread(SentConfirmAfter + time.Second)
	if !f.cache.Stats().Disjoint {
		t.Fatal("setup: not disjoint")
	}
	f.send("ours-young")
	f.cache.Acknowledged([]contract.TriggerEventV1{recovery(keyA, "ours-old")})
	f.reread(time.Minute)
	if stats := f.cache.Stats(); !stats.Disjoint || stats.SentNotInSet != 0 {
		t.Fatalf("disjoint %v not-in %d, want the state kept with nothing old enough to count", stats.Disjoint, stats.SentNotInSet)
	}
	if !f.cache.Contains(tenant, keyA.StrategyID, "ours-young") {
		t.Fatal("the young alert's recovery was held")
	}
}
