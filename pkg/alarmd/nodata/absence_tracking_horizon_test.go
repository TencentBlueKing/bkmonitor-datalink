// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// trackingHorizon is short enough that a round two periods after an absence
// began is past it and one period after is not, so every case below can place
// itself on either side by choosing its evaluation time alone.
const trackingHorizon = int64(120)

func horizonRound(at int64, roster Roster, present map[string]Group, memory map[string]GroupMemory) AbsenceInput {
	input := fullRound(at, roster, present, memory)
	input.TrackingHorizonSeconds = trackingHorizon
	return input
}

func wantTrackingFacts(t *testing.T, result AbsenceResult, expired, suppressed uint64) {
	t.Helper()
	if result.Facts.Expired != expired || result.Facts.Suppressed != suppressed {
		t.Fatalf("expired = %d suppressed = %d, want %d and %d",
			result.Facts.Expired, result.Facts.Suppressed, expired, suppressed)
	}
}

// The round the horizon is reached produces no verdict for that group -- not an
// ANOMALY, and above all not a NORMAL.
//
// A NORMAL here is a recovery nobody observed: downstream closes the standing
// alert and files it as resolved, which is a worse answer than leaving it open.
// The obvious way to write this mechanism is to route the expired group through
// the path that already forgets groups -- closeDroppedAbsences -- and that path
// exists precisely to emit the closing NORMAL. So the two implementations differ
// only in a verdict that one of them does not produce at all, and this states
// which.
func TestAnExpiredAbsenceProducesNoVerdictAtAll(t *testing.T) {
	absent := hostGroup(t, "10.0.0.1")
	memory := map[string]GroupMemory{absent.Key(): {FirstAbsent: 800}}
	result := evaluate(t, horizonRound(800+trackingHorizon, historyRoster("v1", absent), nil, memory))

	if verdict, judged := result.Verdicts[absent.Key()]; judged {
		t.Fatalf("the round that reached the horizon gave %q; a NORMAL closes the standing alert as recovered "+
			"and an ANOMALY is the absence the horizon exists to stop", verdict)
	}
	wantTrackingFacts(t, result, 1, 0)
	if _, remembered := result.Memory[absent.Key()]; remembered {
		t.Fatal("a history roster is derived from this memory, so an expired group that stays in it stays expected")
	}
}

// A horizon raised after the fact does not revive what a lower one stopped.
//
// This is the whole reason the suppression is a stored timestamp rather than a
// comparison against the horizon in force now. Recomputing it would make every
// stopped absence resume the moment an operator raised the setting - a burst of
// anomalies for groups that have been silent for days, none of which is news.
func TestRaisingTheHorizonDoesNotReviveAStoppedAbsence(t *testing.T) {
	absent := hostGroup(t, "10.0.0.1")
	// Suppressed at 900 under some earlier horizon. Under the horizon in force
	// now this absence is 600 seconds old against a limit of 6000, so anything
	// that recomputes from FirstAbsent decides it is still being tracked.
	memory := map[string]GroupMemory{absent.Key(): {FirstAbsent: 900, SuppressedAt: 900}}
	input := fullRound(1500, staticRoster("v1", absent), nil, memory)
	input.TrackingHorizonSeconds = 6000

	result := evaluate(t, input)
	if verdict, judged := result.Verdicts[absent.Key()]; judged {
		t.Fatalf("raising the horizon revived a stopped absence as %q", verdict)
	}
	wantTrackingFacts(t, result, 0, 1)
	wantMemory(t, result, absent.Key(), GroupMemory{FirstAbsent: 900, SuppressedAt: 900})
}

// The whole-item absence expires too, and stays expired.
//
// It never enters the roster loop: it is what an empty roster produces, so the
// loop that stops group absences cannot stop this one. A build that placed the
// expiry check only in that loop passes every group case above and rebuilds the
// whole-item anomaly every round forever.
func TestTheWholeItemAbsenceStopsAtTheHorizonAndStaysStopped(t *testing.T) {
	whole := WholeItemGroup().Key()
	empty := Roster{Version: "v1", Source: RosterWhole, Groups: nil}

	reached := evaluate(t, horizonRound(800+trackingHorizon, empty, nil,
		map[string]GroupMemory{whole: {FirstAbsent: 800}}))
	if verdict, judged := reached.Verdicts[whole]; judged {
		t.Fatalf("the whole-item absence gave %q on the round it reached the horizon", verdict)
	}
	wantTrackingFacts(t, reached, 1, 0)
	// Marked, not forgotten: there is no roster holding this group, so a
	// forgotten one is rebuilt from nothing by the very next empty round.
	wantMemory(t, reached, whole, GroupMemory{FirstAbsent: 800, SuppressedAt: 800 + trackingHorizon})

	later := evaluate(t, horizonRound(800+trackingHorizon+60, empty, nil, reached.Memory))
	if verdict, judged := later.Verdicts[whole]; judged {
		t.Fatalf("the round after gave %q; the whole-item absence restarted", verdict)
	}
	wantTrackingFacts(t, later, 0, 1)
}

// A history roster the horizon emptied does not become a whole-item absence.
//
// Once the last group is forgotten the roster is empty, and an empty roster is
// the input the whole-item branch reads as "this item speaks about itself". So
// the round after the last group expires opens a brand new absence for the item
// as a whole - one anomaly in place of the several the horizon just stopped,
// which reads to an operator as a fresh outage. Only the Plan-level fact tells
// that empty roster apart from an item that never had groups.
func TestAHistoryRosterEmptiedByTheHorizonDoesNotBecomeAWholeItemAbsence(t *testing.T) {
	whole := WholeItemGroup().Key()
	first, second := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")

	exhausting := evaluate(t, horizonRound(800+trackingHorizon,
		historyRoster("v1", first, second), nil,
		map[string]GroupMemory{first.Key(): {FirstAbsent: 800}, second.Key(): {FirstAbsent: 800}}))
	wantTrackingFacts(t, exhausting, 2, 0)
	if len(exhausting.Memory) != 0 {
		t.Fatalf("memory = %+v, want a history roster the horizon emptied", exhausting.Memory)
	}
	if exhausting.TrackingExhaustedAt != 800+trackingHorizon {
		t.Fatalf("tracking-exhausted = %d, want the round that emptied the roster", exhausting.TrackingExhaustedAt)
	}

	// The round after. Roster and memory are both empty, which is exactly what
	// an item with no groups looks like.
	next := horizonRound(800+trackingHorizon+60,
		Roster{Version: "v1", Source: RosterHistory, Groups: nil}, nil, exhausting.Memory)
	next.TrackingExhaustedAt = exhausting.TrackingExhaustedAt
	after := evaluate(t, next)
	if verdict, judged := after.Verdicts[whole]; judged {
		t.Fatalf("an exhausted history roster produced a whole-item %q, replacing the absences "+
			"the horizon had just stopped with a single fresh one", verdict)
	}
	if _, rebuilt := after.Memory[whole]; rebuilt {
		t.Fatal("an exhausted history roster opened a whole-item absence in the memory")
	}
	if after.TrackingExhaustedAt != exhausting.TrackingExhaustedAt {
		t.Fatalf("tracking-exhausted = %d, want it carried at %d", after.TrackingExhaustedAt, exhausting.TrackingExhaustedAt)
	}
}

// The expired count is the sum of both branches, and the two branches do
// different things: a history group is forgotten, an expected one is marked.
//
// Counted by differencing this memory against the last, the marked half is
// invisible - the entry is still there - so the count reports half of what
// stopped. Both halves have to be counted where each is decided, which is what
// a case holding one group of each kind states and a case holding one kind
// cannot.
func TestTheExpiredCountIsTheSumOfBothBranches(t *testing.T) {
	forgotten, marked := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	memory := map[string]GroupMemory{
		forgotten.Key(): {FirstAbsent: 800},
		marked.Key():    {FirstAbsent: 800},
	}

	history := evaluate(t, horizonRound(800+trackingHorizon, historyRoster("v1", forgotten, marked), nil, copyMemory(memory)))
	wantTrackingFacts(t, history, 2, 0)
	static := evaluate(t, horizonRound(800+trackingHorizon, staticRoster("v1", forgotten, marked), nil, copyMemory(memory)))
	wantTrackingFacts(t, static, 2, 0)

	// The two branches really are different, so the sum above is a sum and not
	// the same branch counted twice.
	if len(history.Memory) != 0 {
		t.Fatalf("history memory = %+v, want the expired groups forgotten", history.Memory)
	}
	if len(static.Memory) != 2 {
		t.Fatalf("static memory = %+v, want the expired groups kept and marked", static.Memory)
	}
	for key, entry := range static.Memory {
		if entry.SuppressedAt != 800+trackingHorizon {
			t.Fatalf("Memory[%q] = %+v, want a suppression at the round that reached the horizon", key, entry)
		}
	}
}

// A round may clear the Plan-level fact only when it produced a memory to clear
// it with, and that rule holds on every path out of the evaluation.
//
// The contract layer refuses both halves by name: a memory stating the fact
// while holding groups, and one that cleared it without data arriving. A branch
// that clears the fact while leaving the memory empty satisfies neither, and it
// does not fail here - it fails on every write that Plan ever attempts again.
// These are the paths that reach an empty memory without judging anything, and
// each of them clears the fact if the rule is written per branch.
func TestClearingTheFactNeedsAMemoryToClearItWith(t *testing.T) {
	exhaustedAt := int64(900)
	empty := map[string]GroupMemory{}

	// A target plan that resolved, completely, to no member. It opens nothing
	// and remembers nothing, so it can never be the round that clears the fact.
	targetPlan := horizonRound(1000, Roster{Version: "v2", Source: RosterTargetPlan, Groups: nil}, nil, empty)
	targetPlan.TrackingExhaustedAt = exhaustedAt

	// The same, on a Slot whose query did not come back whole. A round that did
	// not look has learned nothing, least of all that the roster recovered.
	notFull := targetPlan
	notFull.Completeness = execution.CompletenessPartial

	// A round where series arrived for groups that all belong to another
	// business. They are dropped rather than remembered, so the memory stays
	// empty and the present-as-of does not advance either.
	foreign := hostGroup(t, "10.0.0.9")
	outOfBusiness := horizonRound(1000, Roster{Version: "v1", Source: RosterHistory, Groups: nil},
		groupSet(foreign), empty)
	outOfBusiness.TrackingExhaustedAt = exhaustedAt
	outOfBusiness.OutOfBusiness = map[string]struct{}{foreign.Key(): {}}

	for name, input := range map[string]AbsenceInput{
		"target plan resolved to no member": targetPlan,
		"slot was not complete":             notFull,
		"every group was out of business":   outOfBusiness,
	} {
		result := Evaluate(input)
		if len(result.Memory) != 0 {
			t.Fatalf("%s: memory = %+v, this case is only meaningful with an empty one", name, result.Memory)
		}
		if result.TrackingExhaustedAt != exhaustedAt {
			t.Fatalf("%s: tracking-exhausted = %d, want it carried at %d; cleared over an empty memory "+
				"the contract layer refuses every write this Plan makes from now on",
				name, result.TrackingExhaustedAt, exhaustedAt)
		}
	}

	// The other direction, so the rule above is not satisfied by never clearing
	// at all: a roster that holds groups again clears the fact, which is what
	// lets an exhausted Plan given an explicit target write anything ever again.
	recovered := horizonRound(1000, staticRoster("v3", hostGroup(t, "10.0.0.1")), nil, empty)
	recovered.TrackingExhaustedAt = exhaustedAt
	result := evaluate(t, recovered)
	if len(result.Memory) == 0 {
		t.Fatal("a roster with groups remembered nothing, so this case cannot say anything about clearing")
	}
	if result.TrackingExhaustedAt != 0 {
		t.Fatalf("tracking-exhausted = %d, want it cleared once the roster holds groups; "+
			"held over a non-empty memory the contract layer refuses the write", result.TrackingExhaustedAt)
	}
}

// Data arriving restarts tracking from nothing, suppression included.
//
// The present branch writes a whole new entry rather than updating the one it
// found, and that is the only thing that clears a suppression. Keeping the old
// entry and merely refreshing LastSeen reads as a smaller change and passes
// every other case here: the difference only shows a round later, when the
// group goes absent again and is met as still stopped. A group that recovered
// and failed again would never be reported again, and nothing would ever heal
// it - which is the shape of under-reporting this whole decision exists to
// bound, arrived at from the opposite direction.
func TestDataArrivingRestartsTrackingFromNothing(t *testing.T) {
	group := hostGroup(t, "10.0.0.1")
	stopped := map[string]GroupMemory{group.Key(): {FirstAbsent: 800, SuppressedAt: 920}}

	back := evaluate(t, horizonRound(1000, staticRoster("v1", group), groupSet(group), stopped))
	wantVerdicts(t, back, map[string]Verdict{group.Key(): VerdictNormal})
	wantMemory(t, back, group.Key(), GroupMemory{LastSeen: 1000})
	wantTrackingFacts(t, back, 0, 0)

	// Gone again, one round later: too soon for the horizon, so it must be
	// reported. Held over, the suppression would silence it here for good.
	again := evaluate(t, horizonRound(1060, staticRoster("v1", group), nil, back.Memory))
	wantVerdicts(t, again, map[string]Verdict{group.Key(): VerdictAnomaly})
	if again.Facts.Absent != 1 {
		t.Fatalf("absent = %d, want the recovered group reported when it failed again", again.Facts.Absent)
	}
	wantTrackingFacts(t, again, 0, 0)
	wantMemory(t, again, group.Key(), GroupMemory{LastSeen: 1000, FirstAbsent: 1060})
}

// The same, for a history roster that the horizon had emptied: the group comes
// back, is only remembered, is judged from the next round, and is reported when
// it fails again.
func TestAnExhaustedHistoryPlanTracksAGroupThatComesBack(t *testing.T) {
	group := hostGroup(t, "10.0.0.1")
	exhausted := horizonRound(1000, Roster{Version: "v1", Source: RosterHistory, Groups: nil},
		groupSet(group), map[string]GroupMemory{})
	exhausted.TrackingExhaustedAt = 920

	back := evaluate(t, exhausted)
	wantMemory(t, back, group.Key(), GroupMemory{LastSeen: 1000})
	if back.TrackingExhaustedAt != 0 {
		t.Fatalf("tracking-exhausted = %d, want it cleared once a group came back", back.TrackingExhaustedAt)
	}

	// Second round: the group is in the history roster now and reported.
	second := evaluate(t, horizonRound(1060, historyRoster("v2", group), groupSet(group), back.Memory))
	wantVerdicts(t, second, map[string]Verdict{group.Key(): VerdictNormal})

	third := evaluate(t, horizonRound(1120, historyRoster("v2", group), nil, second.Memory))
	wantVerdicts(t, third, map[string]Verdict{group.Key(): VerdictAnomaly})
	wantTrackingFacts(t, third, 0, 0)
}

// An exhausted Plan whose data comes back only under the whole-item group
// clears the mark, even though the memory it produces is still empty.
//
// The whole-item group is never remembered, so "the memory holds a group
// again" cannot be the only way out. Without this the mark is carried forever
// and the whole-item absence it describes is never reported again - reachable
// by emptying a history Plan's no-data agg_dimension, which leaves
// StateGeneration and therefore the memory key unchanged.
func TestWholeItemDataClearsTheExhaustionMark(t *testing.T) {
	whole := WholeItemGroup()
	input := horizonRound(1000, Roster{Version: "v1", Source: RosterHistory, Groups: nil},
		groupSet(whole), map[string]GroupMemory{})
	input.TrackingExhaustedAt = 900

	back := evaluate(t, input)
	wantVerdicts(t, back, map[string]Verdict{whole.Key(): VerdictNormal})
	if len(back.Memory) != 0 {
		t.Fatalf("memory = %+v, want it still empty: the whole-item group is never remembered", back.Memory)
	}
	if back.TrackingExhaustedAt != 0 {
		t.Fatalf("tracking-exhausted = %d, want it cleared by data arriving; carried over an empty memory "+
			"the whole-item absence is never reported again and nothing heals it", back.TrackingExhaustedAt)
	}

	// The round after, with nothing arriving, opens the whole-item absence
	// again - which is the behaviour the carried mark suppressed.
	next := horizonRound(1060, Roster{Version: "v1", Source: RosterHistory, Groups: nil}, nil, back.Memory)
	next.TrackingExhaustedAt = back.TrackingExhaustedAt
	after := evaluate(t, next)
	wantVerdicts(t, after, map[string]Verdict{whole.Key(): VerdictAnomaly})
	if after.Facts.Absent != 1 {
		t.Fatalf("absent = %d, want the whole-item absence reported", after.Facts.Absent)
	}
}
