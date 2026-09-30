// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import "testing"

// The two facts a limited tracking horizon stores have to reach the record,
// and the store decides what reaches the record by comparing digests. A field
// the digest does not cover is a field two otherwise identical statements
// agree on, and the store answers "already applied" to the second one.
//
// The Plan-level fact is the one at risk: it is not in the group list, so
// nothing else about the memory moves when it is set. A round that only
// exhausts the roster states exactly the same groups as the round before it.
func TestTheDigestCoversThePlanLevelTrackingFact(t *testing.T) {
	// Both memories are empty, because a memory holding groups cannot state the
	// fact at all. That is the point: with the groups equal and empty, the fact
	// is the only thing between these two rounds, so a digest that does not
	// cover it makes them one memory.
	tracked := noDataUpdate()
	exhausted := noDataUpdate()
	exhausted.TrackingExhaustedAt = 90

	first, err := BuildPlanNoDataMutation(tracked)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlanNoDataMutation(exhausted)
	if err != nil {
		t.Fatal(err)
	}
	if first.MemoryDigest == second.MemoryDigest {
		t.Fatal("a memory whose roster was exhausted carries the same digest as one still tracking; " +
			"the store answers already-applied on a digest match, so the write that set it is dropped")
	}
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("two statements differing only in the Plan-level tracking fact carry one statement digest")
	}
	if second.TrackingExhaustedAt != 90 {
		t.Fatalf("built mutation carries tracking-exhausted %d, want 90", second.TrackingExhaustedAt)
	}
}

// Suppression is per group and has to survive the delta, which is built by
// listing fields rather than copying the struct. A group whose suppression is
// dropped on the way out is written back as still tracked, and the next round
// resumes producing its absence - the one outcome the horizon exists to stop.
func TestSuppressionReachesTheDeltaAndMovesTheDigest(t *testing.T) {
	absent := NoDataGroupMemory{GroupKey: "a", LastSeen: 80, FirstAbsent: 85}
	suppressed := absent
	suppressed.SuppressedAt = 90

	update := noDataUpdate(suppressed)
	update.Loaded = []NoDataGroupMemory{absent}
	update.LoadedPresentAsOf = 90

	built, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Set) != 1 {
		t.Fatalf("a round that suppressed a group wrote %d changes, want the one group", len(built.Set))
	}
	if built.Set[0].Absent == nil {
		t.Fatal("a suppressed group was written in the compressed present form")
	}
	if built.Set[0].Absent.SuppressedAt != 90 {
		t.Fatalf("delta carries suppressed-at %d, want 90; the group would be read back as still tracked",
			built.Set[0].Absent.SuppressedAt)
	}
	// Same memory but for the suppression: the digests must differ, or a
	// same-version retry of the round before this one reads as already applied.
	tracked, err := BuildPlanNoDataMutation(noDataUpdate(absent))
	if err != nil {
		t.Fatal(err)
	}
	if tracked.MemoryDigest == built.MemoryDigest {
		t.Fatal("suppressing a group left the memory digest where it was")
	}
}

// A round that changes nothing still writes nothing, suppression included.
// Without this the pair above would pass on an implementation that wrote every
// group every round, which is the shape the per-group record replaced.
func TestASuppressedGroupThatDidNotChangeIsNotWrittenAgain(t *testing.T) {
	suppressed := NoDataGroupMemory{GroupKey: "a", LastSeen: 80, FirstAbsent: 85, SuppressedAt: 90}
	update := noDataUpdate(suppressed)
	update.Loaded = []NoDataGroupMemory{suppressed}
	update.LoadedPresentAsOf = 90

	built, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Set) != 0 || len(built.Del) != 0 {
		t.Fatalf("a round that changed nothing wrote %d sets and %d deletes", len(built.Set), len(built.Del))
	}
}

// The Plan-level fact is refused in the two shapes that would let a horizon
// change reach back into what it already decided, and in the one shape that is
// not a time. Each case states a round the builder must not turn into a write.
func TestTheBuilderRefusesAnImpossibleTrackingFact(t *testing.T) {
	group := NoDataGroupMemory{GroupKey: "a", LastSeen: 80, FirstAbsent: 85}

	exhaustedWithGroups := noDataUpdate(group)
	exhaustedWithGroups.TrackingExhaustedAt = 90

	clearedWithoutData := noDataUpdate()
	clearedWithoutData.LoadedTrackingExhaustedAt = 90
	clearedWithoutData.LoadedPresentAsOf = 90
	clearedWithoutData.PresentAsOf = 90

	negative := noDataUpdate(group)
	negative.TrackingExhaustedAt = -1

	for name, update := range map[string]PlanNoDataMemoryUpdate{
		"exhausted while still holding groups": exhaustedWithGroups,
		"cleared without data arriving":        clearedWithoutData,
		"not a time at all":                    negative,
	} {
		if _, err := BuildPlanNoDataMutation(update); err == nil {
			t.Fatalf("%s: built a mutation, want a refusal", name)
		}
	}

	// The two rounds that legitimately clear the fact. Without these the pair
	// of refusals above would be satisfied by forbidding every clearing, which
	// is a different rule that reads the same from the refusals alone.
	byData := clearedWithoutData
	byData.PresentAsOf = 91
	if _, err := BuildPlanNoDataMutation(byData); err != nil {
		t.Fatalf("a round where data arrived could not clear the fact: %v", err)
	}
	// The roster has groups again and nothing has reported - an exhausted Plan
	// that gained an explicit target. The check on the other side requires the
	// fact to be cleared here, so this one must allow it; written without this
	// case between them the two refusals closed on the same round and the Plan
	// could never write again.
	byRoster := clearedWithoutData
	byRoster.Memory = []NoDataGroupMemory{{GroupKey: "host-1", FirstAbsent: 90}}
	if _, err := BuildPlanNoDataMutation(byRoster); err != nil {
		t.Fatalf("an exhausted Plan whose roster came back could not write at all: %v", err)
	}
}

// The compressed "present" form says a group was seen in the round the header
// names and carries nothing else, so a suppressed group must never take it.
// Today a suppressed group always carries a first-absent too and either test
// would keep it out; this states the rule against a group where the two
// disagree, so the guard is what holds rather than the coincidence.
func TestASuppressedGroupIsNeverCompressedIntoPresent(t *testing.T) {
	contradictory := NoDataGroupMemory{GroupKey: "a", LastSeen: 90, SuppressedAt: 90}
	value := noDataGroupValue(contradictory, 90)
	if value.Absent == nil {
		t.Fatal("a group carrying a suppression was written as present, which stores neither fact")
	}
	if value.Absent.SuppressedAt != 90 {
		t.Fatalf("written in full but without the suppression: %+v", value.Absent)
	}
	// The coincidence itself, so removing the first-absent half of the guard is
	// not silently equivalent either.
	present := NoDataGroupMemory{GroupKey: "a", LastSeen: 90}
	if noDataGroupValue(present, 90).Absent != nil {
		t.Fatal("an ordinary present group stopped being compressed")
	}
}

// A record written before the horizon existed carries neither fact, and zero
// is the right reading for both: no build that wrote one had a horizon to stop
// anything with. This is the whole of "new process reads old".
func TestARecordWithoutTheTrackingFactsReadsAsStillTracking(t *testing.T) {
	built, err := BuildPlanNoDataMutation(noDataUpdate(
		NoDataGroupMemory{GroupKey: "a", LastSeen: 80, FirstAbsent: 85},
	))
	if err != nil {
		t.Fatal(err)
	}
	if built.TrackingExhaustedAt != 0 {
		t.Fatalf("a Plan nobody exhausted carries tracking-exhausted %d", built.TrackingExhaustedAt)
	}
	if built.Set[0].Absent == nil || built.Set[0].Absent.SuppressedAt != 0 {
		t.Fatal("a group nobody suppressed carries a suppression")
	}
	if !NoDataMemoryReadable(NoDataMemorySchemaV2) || !NoDataMemoryReadable(NoDataMemorySchemaV1) {
		t.Fatal("this build must go on reading the records written before it")
	}
}
