// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package absentalerts

import (
	"strconv"
	"testing"
	"time"
)

var testNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func testBounds() Bounds {
	return Bounds{MinReads: 3, MaxSnapshotAge: 30 * time.Minute, MaxCloseStrategies: 4}
}

func key(id string) Key { return Key{TenantID: "system", StrategyID: id} }

// filled is a snapshot of the given size that lists none of the strategies
// the tests close.
func filled(count int) map[Key]struct{} {
	snapshot := make(map[Key]struct{}, count)
	for i := 0; i < count; i++ {
		snapshot[key("live-"+strconv.Itoa(i))] = struct{}{}
	}
	return snapshot
}

func set(keys ...Key) map[Key]struct{} {
	result := make(map[Key]struct{}, len(keys))
	for _, k := range keys {
		result[k] = struct{}{}
	}
	return result
}

// ripe is the memory of a candidate this term has found missing on three
// rounds, first under a different observation: everything a close needs
// except the round's own gates.
func ripe(keys ...Key) map[Key]Absence {
	absences := make(map[Key]Absence, len(keys))
	for _, k := range keys {
		absences[k] = Absence{Since: testNow.Add(-time.Hour), Observation: "observation-before", Reads: 3, TermReads: 3, Confirmed: true}
	}
	return absences
}

// young is a candidate first found missing on this very round.
func young() Absence {
	return Absence{Since: testNow, Observation: "observation-now", Reads: 1, TermReads: 1}
}

// roundFor is a healthy round: the link read and maintained a minute ago,
// a snapshot of 200 live strategies under its writer's statement, and the
// roster listing the given strategies beside a few live ones.
func roundFor(roster map[Key]struct{}, absences map[Key]Absence) Round {
	for i := 0; i < 5; i++ {
		roster[key("live-"+strconv.Itoa(i))] = struct{}{}
	}
	members := make(map[Key]int, len(roster))
	for k := range roster {
		members[k] = 1
	}
	return Round{LinkRead: true, Roster: members, RosterComplete: true,
		SnapshotStrategies: filled(200), SnapshotUsable: true, SnapshotObservation: "observation-now",
		WriterHoldsLastGood: true, FirstAbsent: absences, Now: testNow}
}

// The whole point, stated once: a strategy the link holds an unrecovered
// alert for, and the snapshot no longer lists, gets its alerts closed - and
// it does not have to be one this process watched go.
func TestAStrategyTheLinkListsAndTheSnapshotDoesNotIsClosed(t *testing.T) {
	k := key("10")
	result := Compute(roundFor(set(k), ripe(k)), testBounds())
	if len(result.Close) != 1 || result.Close[0].Key != k || result.Counts.CloseDecided != 1 {
		t.Fatalf("the strategy this capability exists for was not closed: %+v", result)
	}
	if result.Counts.Candidates != 1 || result.Counts.Roster != 6 {
		t.Fatalf("the live strategies on the roster are not candidates: %+v", result.Counts)
	}
	if result.Close[0].Identity != (Identity{}) {
		t.Fatalf("an identity nobody remembered was invented: %+v", result.Close[0])
	}
}

// When the catalog does remember the strategy, the close carries what it
// remembered.
func TestARememberedIdentityTravelsWithTheClose(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.Identities = map[Key]Identity{k: {BusinessID: 2, Revision: 7}}
	result := Compute(round, testBounds())
	if len(result.Close) != 1 || result.Close[0].Identity != (Identity{BusinessID: 2, Revision: 7}) {
		t.Fatalf("the remembered identity was lost: %+v", result.Close)
	}
}

// A strategy the snapshot lists is live, whatever the roster says.
func TestAStrategyTheSnapshotListsIsNotACandidate(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.SnapshotStrategies[k] = struct{}{}
	result := Compute(round, testBounds())
	if len(result.Close) != 0 || result.Counts.Candidates != 0 {
		t.Fatalf("a strategy the source lists was closed: %+v", result)
	}
}

// A strategy the snapshot lists and the publication refused for a capability
// is a candidate as an absent one is, and its close names the capability; a
// listed strategy refused for nothing is not; and a refused strategy the
// snapshot no longer lists closes as gone.
func TestAStrategyRefusedForACapabilityIsClosedAsNoLongerDetected(t *testing.T) {
	refused, live, gone := key("10"), key("11"), key("12")
	round := roundFor(set(refused, live, gone), ripe(refused, live, gone))
	round.SnapshotStrategies[refused] = struct{}{}
	round.SnapshotStrategies[live] = struct{}{}
	round.Refused = map[string]string{"10": "QUERY_SOURCE_RETIRED", "12": "QUERY_SOURCE_RETIRED"}
	result := Compute(round, testBounds())
	closed := map[Key]string{}
	for _, absent := range result.Close {
		closed[absent.Key] = absent.Refused
	}
	if len(closed) != 2 || closed[refused] != "QUERY_SOURCE_RETIRED" || closed[gone] != "" {
		t.Fatalf("closes = %+v, want the refused strategy closed naming its capability and the gone one closed as gone", result.Close)
	}
	if _, closedLive := closed[live]; closedLive || result.Counts.Candidates != 2 {
		t.Fatalf("a listed strategy nothing refused was a candidate: %+v", result)
	}
}

// Not read is not empty. The count is reported so that it is a reading.
func TestAStrategyTheLinkCouldNotReadIsReportedNotClosed(t *testing.T) {
	round := roundFor(set(), nil)
	round.RosterUnreadable = 3
	result := Compute(round, testBounds())
	if len(result.Close) != 0 || result.Counts.RosterUnreadable != 3 {
		t.Fatalf("an unread set was hidden or closed: %+v", result)
	}
}

// No roster, no difference.
func TestAnUnreadRosterClosesNothing(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.LinkRead = false
	if result := Compute(round, testBounds()); result.Refusal != RefusalLinkUnavailable || len(result.Close) != 0 {
		t.Fatalf("a round without the roster decided something: %+v", result)
	}
}

// An incomplete walk is a smaller roster: it still decides on what it saw.
func TestAnIncompleteWalkStillClosesWhatItSaw(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.RosterComplete = false
	if result := Compute(round, testBounds()); result.Refusal != RefusalNone || result.Counts.CloseDecided != 1 {
		t.Fatalf("an incomplete walk refused what it saw: %+v", result)
	}
}

// An unread snapshot is not an empty one. Deciding on it would close every
// strategy's alerts on a round that learned nothing.
func TestAnUnreadSnapshotClosesNothingAndKeepsItsDenominators(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.SnapshotUsable, round.SnapshotStrategies, round.SnapshotObservation = false, nil, ""
	result := Compute(round, testBounds())
	if result.Refusal != RefusalSnapshotUnusable || len(result.Close) != 0 {
		t.Fatalf("an unread snapshot decided something: %+v", result)
	}
	if result.Counts.Roster != 6 {
		t.Fatalf("the refusal dropped its denominators: %+v", result.Counts)
	}
}

// An empty list without the writer's statement is refused: nothing says it
// is the writer's word rather than a list that lost its content.
func TestAnEmptySnapshotWithoutTheWritersStatementClosesNothing(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.SnapshotStrategies = map[Key]struct{}{}
	round.WriterHoldsLastGood = false
	if result := Compute(round, testBounds()); result.Refusal != RefusalSnapshotEmpty || len(result.Close) != 0 {
		t.Fatalf("an empty snapshot decided something: %+v", result)
	}
}

// A list that is not empty, without its writer's statement, is refused
// whole as writer_unstated: a strategy missing from it is not known to be
// gone. Nothing is closed, however ripe, and no clock starts or ages, so
// the first rounds with the statement serve the whole grace and two
// observations. Where it is also empty or stale, that refusal is the
// round's word (snapshot_empty, snapshot_stale): writer_unstated is checked
// last.
func TestAListWithoutTheWritersStatementClosesNothing(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.WriterHoldsLastGood = false
	if result := Compute(round, testBounds()); result.Refusal != RefusalWriterUnstated || len(result.Close) != 0 ||
		len(result.Decisions) != 0 {
		t.Fatalf("a list without the statement = %+v, want refused as writer_unstated", result)
	}
	stale := round
	stale.SnapshotAgeSeconds = int64(testBounds().MaxSnapshotAge/time.Second) + 1
	if result := Compute(stale, testBounds()); result.Refusal != RefusalSnapshotStale {
		t.Fatalf("a stale list without the statement = %s, want snapshot_stale", result.Refusal)
	}
	tracker := NewTracker(100)
	for i, observation := range []string{"observation-one", "observation-two"} {
		unstated := roundFor(set(k), nil)
		unstated.WriterHoldsLastGood = false
		unstated.SnapshotObservation = observation
		unstated.Now = testNow.Add(time.Duration(i) * 11 * time.Minute)
		if result := tracker.Round(unstated, testBounds()); result.Refusal != RefusalWriterUnstated || len(result.Close) != 0 {
			t.Fatalf("unstated round %d: %+v", i, result)
		}
	}
	if tracker.Tracked() != 0 {
		t.Fatal("an unstated round started a candidate's clock")
	}
	stated := roundFor(set(k), nil)
	stated.SnapshotObservation = "observation-three"
	stated.Now = testNow.Add(30 * time.Minute)
	if result := tracker.Round(stated, testBounds()); result.Refusal != RefusalNone || result.Counts.WithinGrace != 1 || len(result.Close) != 0 {
		t.Fatalf("the first stated round after unstated ones: %+v, want the candidate within its grace", result)
	}
}

// An empty list under the writer's statement, made about those very bytes,
// is a fact: every strategy is gone. The round decides, and a candidate past
// its grace under a second observation is closed like any other.
func TestAnEmptySnapshotUnderTheWritersStatementIsDecidedOn(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.SnapshotStrategies = map[Key]struct{}{}
	round.WriterHoldsLastGood = true
	result := Compute(round, testBounds())
	if result.Refusal != RefusalNone || result.Counts.Candidates != 6 {
		t.Fatalf("an empty snapshot under the statement = %+v, want it decided with every listed strategy a candidate", result)
	}
	closed := false
	for _, absent := range result.Close {
		closed = closed || absent.Key == k
	}
	if !closed {
		t.Fatalf("the ripe candidate was not closed: %+v", result.Close)
	}
}

// The snapshot age bound, both sides of it.
func TestTheSnapshotAgeBoundIsABoundaryOnBothSides(t *testing.T) {
	k := key("10")
	bounds := testBounds()
	inside := roundFor(set(k), ripe(k))
	inside.SnapshotAgeSeconds = int64((bounds.MaxSnapshotAge - time.Minute) / time.Second)
	if result := Compute(inside, bounds); result.Refusal != RefusalNone {
		t.Fatalf("a snapshot inside its bound was refused: %+v", result)
	}
	past := roundFor(set(k), ripe(k))
	past.SnapshotAgeSeconds = int64((bounds.MaxSnapshotAge + time.Minute) / time.Second)
	if result := Compute(past, bounds); result.Refusal != RefusalSnapshotStale || len(result.Close) != 0 {
		t.Fatalf("a stale snapshot decided something: %+v", result)
	}
}

// A deployment whose unrecovered alerts are mostly, or all, on deleted
// strategies is the case this capability exists for - and on its first run
// against a backlog, the difference can be a large share of everything.
// Nothing about that shape may be read as implausible: a hundred gone
// strategies beside a snapshot of two hundred is decided on, and the close
// bound works it off over rounds.
func TestABacklogLargerThanAnyRatioIsStillWorkedOff(t *testing.T) {
	roster := map[Key]struct{}{}
	gone := []Key{}
	for i := 0; i < 100; i++ {
		k := key("gone-" + strconv.Itoa(i))
		roster[k] = struct{}{}
		gone = append(gone, k)
	}
	round := roundFor(roster, ripe(gone...))
	result := Compute(round, testBounds())
	if result.Refusal != RefusalNone || result.Counts.CloseDecided != 4 || result.Counts.Deferred != 96 {
		t.Fatalf("the backlog this exists to clear was refused or not bounded: %+v", result)
	}
}

// The grace is this loop's own: a leader that has just been elected cannot
// close anything on its first round.
func TestACandidateIsNotClosedOnTheRoundItIsFirstSeen(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), map[Key]Absence{k: young()})
	result := Compute(round, testBounds())
	if len(result.Close) != 0 || result.Counts.WithinGrace != 1 {
		t.Fatalf("a candidate first seen this round was closed: %+v", result)
	}
}

// Confirmation is two observations of the source.
func TestACandidateSeenOnlyUnderOneObservationIsNotClosed(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), map[Key]Absence{k: {Since: testNow.Add(-time.Hour), Observation: "observation-now", Reads: 5, TermReads: 5}})
	result := Compute(round, testBounds())
	if len(result.Close) != 0 || result.Counts.Unconfirmed != 1 {
		t.Fatalf("one observation confirmed an absence: %+v", result)
	}
}

// The tracker, end to end over rounds: a strategy with no mark is within its
// grace until MinReads rounds found it missing, unconfirmed while they all
// read one observation, and closed on the first round past both. Rounds are
// counted, not time: the same rounds an hour apart decide the same.
func TestTheTrackerClosesOnlyAfterMinReadsAndASecondObservation(t *testing.T) {
	for _, gap := range []time.Duration{5 * time.Minute, time.Hour} {
		k := key("10")
		tracker := NewTracker(100)
		at := testNow
		round := func(observation string) Result {
			r := roundFor(set(k), nil)
			r.SnapshotObservation, r.Now = observation, at
			at = at.Add(gap)
			return tracker.Round(r, testBounds())
		}
		for i := 1; i < testBounds().MinReads; i++ {
			if result := round("observation-one"); result.Counts.WithinGrace != 1 || len(result.Close) != 0 {
				t.Fatalf("gap %v: read %d closed or left the grace: %+v", gap, i, result)
			}
		}
		if result := round("observation-one"); result.Counts.Unconfirmed != 1 || len(result.Close) != 0 {
			t.Fatalf("gap %v: confirmed against the same observation: %+v", gap, result)
		}
		result := round("observation-two")
		if len(result.Close) != 1 || result.Close[0].Path != PathImplicit || result.Counts.CloseDecided != 1 {
			t.Fatalf("gap %v: not closed on the implicit path after MinReads and a second observation: %+v", gap, result)
		}
	}
}

// marked is a round whose writer marked strategy 10 as left in state at
// revision.
func marked(round Round, state string, revision int64) Round {
	round.Marks = map[string]Mark{"10": {TenantID: "system", State: state, Revision: revision}}
	return round
}

// The fast path: a strategy missing under the writer's mark waits one round
// and is closed on the next one that reads the same mark, long before the
// implicit path's reads and with no second observation; the close names the
// path and the mark. The implicit path and the fast path are counted apart.
func TestAMarkedStrategyIsClosedOnTheRoundAfterItIsFirstReadMarked(t *testing.T) {
	for _, state := range []string{MarkDisabled, MarkDeleted} {
		k := key("10")
		tracker := NewTracker(100)
		first := tracker.Round(marked(roundFor(set(k), nil), state, 4), testBounds())
		if first.Counts.DepartedWaiting != 1 || len(first.Close) != 0 || first.Decisions[0].Path != PathDeparted {
			t.Fatalf("%s: the first marked round closed or did not wait on the fast path: %+v", state, first)
		}
		second := tracker.Round(marked(roundFor(set(k), nil), state, 4), testBounds())
		if len(second.Close) != 1 || second.Counts.DepartedCloseDecided != 1 || second.Counts.CloseDecided != 0 {
			t.Fatalf("%s: the second marked round did not decide on the fast path: %+v", state, second)
		}
		if got := second.Close[0]; got.Path != PathDeparted || got.Mark != (Mark{TenantID: "system", State: state, Revision: 4}) {
			t.Fatalf("%s: the close does not carry its path and mark: %+v", state, got)
		}
	}
}

// Disabled and enabled again within a round: enabling removes the mark and
// raises the revision, and the strategy is listed again. Nothing closes, and
// when it is disabled once more the wait starts over from that mark. A mark
// whose revision moved between two rounds waits again too.
func TestAStrategyBackBeforeTheSecondMarkedRoundIsNotClosed(t *testing.T) {
	k := key("10")
	tracker := NewTracker(100)
	tracker.Round(marked(roundFor(set(k), nil), MarkDisabled, 4), testBounds())
	listed := roundFor(set(k), nil)
	listed.SnapshotStrategies[k] = struct{}{}
	if result := tracker.Round(listed, testBounds()); len(result.Close) != 0 || result.Counts.Candidates != 0 {
		t.Fatalf("a strategy listed again was still a candidate: %+v", result)
	}
	if result := tracker.Round(marked(roundFor(set(k), nil), MarkDisabled, 6), testBounds()); len(result.Close) != 0 || result.Counts.DepartedWaiting != 1 {
		t.Fatalf("disabled again: closed on its first marked round: %+v", result)
	}
	if result := tracker.Round(marked(roundFor(set(k), nil), MarkDisabled, 8), testBounds()); len(result.Close) != 0 || result.Counts.DepartedWaiting != 1 {
		t.Fatalf("a mark whose revision moved closed without waiting: %+v", result)
	}
	if result := tracker.Round(marked(roundFor(set(k), nil), MarkDisabled, 8), testBounds()); len(result.Close) != 1 {
		t.Fatalf("the same mark on two rounds did not close: %+v", result)
	}
}

// A mark is believed only for the candidate's own tenant and in a state it
// knows; otherwise the strategy takes the implicit path.
func TestAMarkNotBelievedLeavesTheImplicitPath(t *testing.T) {
	k := key("10")
	for name, mark := range map[string]Mark{
		"other tenant":  {TenantID: "other", State: MarkDeleted, Revision: 4},
		"unknown state": {TenantID: "system", State: "archived", Revision: 4},
	} {
		round := roundFor(set(k), map[Key]Absence{k: {Since: testNow, Observation: "observation-before", Reads: 2, TermReads: 2,
			Mark: mark, MarkReads: 2}})
		round.Marks = map[string]Mark{"10": mark}
		result := Compute(round, testBounds())
		if len(result.Close) != 0 || result.Counts.WithinGrace != 1 || result.Decisions[0].Path != PathImplicit {
			t.Fatalf("%s: %+v, want the implicit path's grace", name, result)
		}
		if foreign := result.Counts.MarkForeign == 1; foreign != (name == "other tenant") {
			t.Fatalf("%s: a mark for another tenant counted %d", name, result.Counts.MarkForeign)
		}
	}
}

// What an earlier term kept is restored, and none of it counts as read in
// this term: the first round of the new term closes nothing, however ripe,
// and the second closes on the kept reads - not after reading them all
// again, which is what a release cost before the memory was kept - once this
// term has itself read the implicit candidate under two observations.
func TestARestoredMemoryClosesOnTheNewTermsSecondRound(t *testing.T) {
	implicit, departed := key("10"), key("11")
	kept := ripe(implicit)
	kept[departed] = Absence{Since: testNow.Add(-time.Hour), Observation: "observation-before", Reads: 1, TermReads: 1,
		Mark: Mark{TenantID: "system", State: MarkDisabled, Revision: 4}, MarkReads: 1}
	tracker := NewTracker(100)
	tracker.Restore(kept)
	round := func(observation string) Result {
		r := roundFor(set(implicit, departed), nil)
		r.SnapshotObservation = observation
		r.Marks = map[string]Mark{"11": {TenantID: "system", State: MarkDisabled, Revision: 4}}
		return tracker.Round(r, testBounds())
	}
	if first := round("observation-term-1"); len(first.Close) != 0 || first.Counts.WithinGrace != 1 || first.Counts.DepartedWaiting != 1 {
		t.Fatalf("the new term closed on its first round: %+v", first)
	}
	second := round("observation-term-2")
	if len(second.Close) != 2 || second.Counts.CloseDecided != 1 || second.Counts.DepartedCloseDecided != 1 {
		t.Fatalf("the new term's second round did not close on the kept reads: %+v", second)
	}
}

// A confirmation is not carried into a new term. A strategy confirmed missing
// under the last term, that came back and left again while no leader watched,
// is restored with its reads and no confirmation: the new term reading it
// under one observation leaves it unconfirmed, and only a second observation
// of this term's own closes it.
func TestARestoredCandidateIsConfirmedAgainByTheNewTerm(t *testing.T) {
	k := key("10")
	tracker := NewTracker(100)
	tracker.Restore(ripe(k))
	round := func(observation string) Result {
		r := roundFor(set(k), nil)
		r.SnapshotObservation = observation
		return tracker.Round(r, testBounds())
	}
	round("observation-after-the-gap")
	if result := round("observation-after-the-gap"); len(result.Close) != 0 || result.Counts.Unconfirmed != 1 {
		t.Fatalf("closed on the last term's confirmation: %+v", result)
	}
	if result := round("observation-later"); len(result.Close) != 1 {
		t.Fatalf("not closed once this term read a second observation: %+v", result)
	}
}

// A refused round ages nothing: a run of refused rounds cannot mature a
// candidate into a close.
func TestARefusedRoundDoesNotStartOrAgeAClock(t *testing.T) {
	k := key("10")
	tracker := NewTracker(100)
	refused := roundFor(set(k), nil)
	refused.SnapshotUsable = false
	tracker.Round(refused, testBounds())
	if tracker.Tracked() != 0 {
		t.Fatal("a refused round started a candidate's clock")
	}
}

// A strategy the link stops listing is forgotten, so it cannot come back
// later with an old clock.
func TestAStrategyTheLinkStopsListingIsForgotten(t *testing.T) {
	k := key("10")
	tracker := NewTracker(100)
	tracker.Round(roundFor(set(k), nil), testBounds())
	tracker.Round(roundFor(set(), nil), testBounds())
	if tracker.Tracked() != 0 {
		t.Fatal("a strategy the link no longer lists kept its clock")
	}
}

// Every candidate lands on exactly one outcome.
func TestEveryCandidateLandsOnExactlyOneOutcome(t *testing.T) {
	roster := map[Key]struct{}{}
	absences := map[Key]Absence{}
	for _, shape := range []string{"closed", "grace", "unconfirmed"} {
		k := key(shape)
		roster[k] = struct{}{}
		switch shape {
		case "closed":
			absences[k] = ripe(k)[k]
		case "grace":
			absences[k] = young()
		case "unconfirmed":
			absences[k] = Absence{Since: testNow.Add(-time.Hour), Observation: "observation-now", Reads: 3, TermReads: 3}
		}
	}
	round := roundFor(roster, absences)
	counts := Compute(round, testBounds()).Counts
	filed := counts.CloseDecided + counts.WithinGrace + counts.Unconfirmed + counts.Deferred
	if filed != counts.Candidates || counts.Candidates != 3 {
		t.Fatalf("a candidate was not filed under any outcome: %+v", counts)
	}
	for _, count := range []int{counts.CloseDecided, counts.WithinGrace, counts.Unconfirmed} {
		if count != 1 {
			t.Fatalf("each shape should file once: %+v", counts)
		}
	}
}

// The reviewer's case: a strategy whose source document lost its tenant is
// listed by the snapshot under an empty tenant, while the link lists it under
// the tenant its alerts carry. It exists; it must not read as absent.
func TestAStrategyTheSnapshotListsWithoutATenantIsNotACandidate(t *testing.T) {
	k := key("10")
	round := roundFor(set(k), ripe(k))
	round.SnapshotStrategies[Key{StrategyID: "10"}] = struct{}{}
	if result := Compute(round, testBounds()); result.Counts.Candidates != 0 || len(result.Close) != 0 {
		t.Fatalf("a strategy the snapshot lists without a tenant was read as absent: %+v", result)
	}
}

// A round starts past the last strategy the previous one decided, so the
// front of the order cannot hold the bound on every round.
func TestTheWalkStartsPastTheLastDecidedStrategy(t *testing.T) {
	roster := map[Key]struct{}{}
	gone := []Key{}
	for i := 1; i <= 6; i++ {
		k := key("gone-" + strconv.Itoa(i))
		roster[k] = struct{}{}
		gone = append(gone, k)
	}
	round := roundFor(roster, ripe(gone...))
	first := Compute(round, testBounds())
	if len(first.Close) != 4 || first.Close[0].Key != key("gone-1") {
		t.Fatalf("first round %+v", first.Close)
	}
	round.After = first.Close[3].Key
	second := Compute(round, testBounds())
	want := []string{"gone-5", "gone-6", "gone-1", "gone-2"}
	for i, k := range second.Close {
		if k.Key.StrategyID != want[i] {
			t.Fatalf("the second round did not start past the first one's last: %+v", second.Close)
		}
	}
}

// Every candidate gets one decision, in the candidates' key order whatever
// the walk's, and each outcome's count is how many decisions carry it: the
// page that lists them and the counters that count them cannot disagree.
func TestEveryCandidateIsDecidedOnceInKeyOrder(t *testing.T) {
	young, unconfirmed, first, second, third := key("14"), key("12"), key("10"), key("11"), key("13")
	absences := ripe(first, second, third)
	absences[young] = Absence{Since: testNow.Add(-time.Minute), Observation: "observation-before", Reads: 1, TermReads: 1}
	absences[unconfirmed] = Absence{Since: testNow.Add(-time.Hour), Observation: "observation-now", Reads: 3, TermReads: 3}
	untracked := key("15")
	round := roundFor(set(young, unconfirmed, first, second, third, untracked), absences)
	round.After = first
	bounds := testBounds()
	bounds.MaxCloseStrategies = 1
	result := Compute(round, bounds)
	if len(result.Decisions) != result.Counts.Candidates || result.Counts.Candidates != 6 {
		t.Fatalf("not one decision per candidate: %d decisions, %+v", len(result.Decisions), result.Counts)
	}
	want := map[Key]string{young: OutcomeWithinGrace, untracked: OutcomeWithinGrace, unconfirmed: OutcomeUnconfirmed,
		second: OutcomeCloseDecided, third: OutcomeDeferred, first: OutcomeDeferred}
	tally := map[string]int{}
	for i, decision := range result.Decisions {
		if i > 0 && !LessKey(result.Decisions[i-1].Key, decision.Key) {
			t.Fatalf("decisions are not in key order: %+v", result.Decisions)
		}
		if decision.Outcome != want[decision.Key] {
			t.Fatalf("%s decided %s, want %s", decision.Key.StrategyID, decision.Outcome, want[decision.Key])
		}
		tally[decision.Outcome]++
	}
	if tally[OutcomeWithinGrace] != result.Counts.WithinGrace || tally[OutcomeUnconfirmed] != result.Counts.Unconfirmed ||
		tally[OutcomeDeferred] != result.Counts.Deferred || tally[OutcomeCloseDecided] != result.Counts.CloseDecided {
		t.Fatalf("the decisions and the counts disagree: %+v %+v", tally, result.Counts)
	}
	for _, decision := range result.Decisions {
		switch decision.Key {
		case untracked:
			if !decision.AbsentSince.IsZero() {
				t.Fatalf("a candidate the memory does not hold was given a first absence: %+v", decision)
			}
		case young:
			if !decision.AbsentSince.Equal(testNow.Add(-time.Minute)) {
				t.Fatalf("a candidate's first absence is not the memory's: %+v", decision)
			}
		}
	}
	if len(result.Close) != 1 || result.Close[0].Key != second {
		t.Fatalf("the walk did not start after the last strategy closed: %+v", result.Close)
	}
}

// A refused round decides about no one.
func TestARefusedRoundHasNoDecisions(t *testing.T) {
	round := roundFor(set(key("10")), ripe(key("10")))
	round.LinkRead = false
	if result := Compute(round, testBounds()); result.Refusal != RefusalLinkUnavailable || len(result.Decisions) != 0 {
		t.Fatalf("a refused round decided: %+v", result)
	}
}
