// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"reflect"
	"strings"
	"testing"
)

func noDataApplyVersion() ApplyVersion {
	return ApplyVersion{StateApplyEpoch: 7, EvaluationTime: 1700000000, SlotDigest: "slot-digest"}
}

func noDataMutation(groups ...NoDataGroupMemory) PlanNoDataMutation {
	return PlanNoDataMutation{
		Identity: PlanNoDataIdentity{
			Plan:            PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
			StateGeneration: "generation-1",
		},
		SchemaVersion:    NoDataMemorySchemaV1,
		ApplyVersion:     noDataApplyVersion(),
		ScheduleRevision: "revision-1",
		RosterVersion:    "TARGET_STATIC/1",
		Groups:           groups,
	}
}

// The digest closes the payload, so two callers that state the same memory in a
// different order produce the same mutation. A Slot builds its groups from a
// map, and a map's order is not a fact about the memory.
func TestPlanNoDataMutationIsIndependentOfStatedGroupOrder(t *testing.T) {
	first, err := BuildPlanNoDataMutation(noDataMutation(
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlanNoDataMutation(noDataMutation(
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	if first.MutationDigest != second.MutationDigest {
		t.Fatalf("digests differ by stated order: %s vs %s", first.MutationDigest, second.MutationDigest)
	}
	if err := first.ValidateDigest(); err != nil {
		t.Fatalf("ValidateDigest() on a built mutation: %v", err)
	}
}

// A memory that remembers nothing is a state, not an omission: it is what a
// Slot produces when the last group it knew about left the business. Sending no
// mutation at all is the different thing, and it is how a Slot that did not see
// its whole period leaves the memory alone.
func TestPlanNoDataMutationAcceptsAnEmptyMemory(t *testing.T) {
	built, err := BuildPlanNoDataMutation(noDataMutation())
	if err != nil {
		t.Fatalf("an empty memory is a legitimate replacement: %v", err)
	}
	if len(built.Groups) != 0 {
		t.Fatalf("groups = %+v, want none", built.Groups)
	}
	if err := built.ValidateDigest(); err != nil {
		t.Fatal(err)
	}
	// It must not be the same payload as any non-empty memory.
	other, err := BuildPlanNoDataMutation(noDataMutation(NoDataGroupMemory{GroupKey: "a", LastSeen: 90}))
	if err != nil {
		t.Fatal(err)
	}
	if built.MutationDigest == other.MutationDigest {
		t.Fatal("clearing the memory and remembering a group produce the same digest")
	}
}

// The roster version is in the digest because the same timestamps decided
// against a different expected set are a different memory, and nothing else in
// the record says which set that was.
func TestPlanNoDataMutationDigestSeparatesRosterVersions(t *testing.T) {
	base := noDataMutation(NoDataGroupMemory{GroupKey: "a", FirstAbsent: 100})
	first, err := BuildPlanNoDataMutation(base)
	if err != nil {
		t.Fatal(err)
	}
	base.RosterVersion = "HISTORY/1"
	second, err := BuildPlanNoDataMutation(base)
	if err != nil {
		t.Fatal(err)
	}
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("two roster versions produced one digest")
	}
}

func TestPlanNoDataMutationRefusesIncompletePayloads(t *testing.T) {
	for name, broken := range map[string]func(*PlanNoDataMutation){
		"no schema":           func(m *PlanNoDataMutation) { m.SchemaVersion = 0 },
		"future schema":       func(m *PlanNoDataMutation) { m.SchemaVersion = MaxSupportedNoDataMemorySchema + 1 },
		"no generation":       func(m *PlanNoDataMutation) { m.Identity.StateGeneration = "" },
		"no schedule":         func(m *PlanNoDataMutation) { m.ScheduleRevision = "" },
		"no roster version":   func(m *PlanNoDataMutation) { m.RosterVersion = "" },
		"no apply version":    func(m *PlanNoDataMutation) { m.ApplyVersion = ApplyVersion{} },
		"group with no key":   func(m *PlanNoDataMutation) { m.Groups[0].GroupKey = "" },
		"negative timestamp":  func(m *PlanNoDataMutation) { m.Groups[0].FirstAbsent = -1 },
		"group remembers not": func(m *PlanNoDataMutation) { m.Groups[0] = NoDataGroupMemory{GroupKey: "a"} },
	} {
		t.Run(name, func(t *testing.T) {
			mutation := noDataMutation(NoDataGroupMemory{GroupKey: "a", FirstAbsent: 100})
			broken(&mutation)
			if _, err := BuildPlanNoDataMutation(mutation); err == nil {
				t.Fatal("BuildPlanNoDataMutation() accepted an incomplete payload")
			}
		})
	}
}

func TestPlanNoDataMutationRefusesDuplicateGroups(t *testing.T) {
	_, err := BuildPlanNoDataMutation(noDataMutation(
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
		NoDataGroupMemory{GroupKey: "a", FirstAbsent: 100},
	))
	if err == nil {
		t.Fatal("two records for one group were accepted; one of them would silently win")
	}
}

// The builder owns the digest. A caller that sets one is stating a fact it
// cannot have derived, and the value it states is exactly what ValidateDigest
// would go on to trust.
func TestPlanNoDataMutationBuilderOwnsTheDigest(t *testing.T) {
	mutation := noDataMutation(NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
	mutation.MutationDigest = "borrowed"
	if _, err := BuildPlanNoDataMutation(mutation); err == nil {
		t.Fatal("BuildPlanNoDataMutation() accepted a caller-supplied digest")
	}
}

// A payload whose groups are not in canonical order is refused, and refused for
// being unsorted rather than only for failing to match its digest.
//
// The two are easy to confuse, and one of them does nothing. Swapping two
// groups of a built mutation changes what the digest derives to, so the digest
// comparison alone rejects it and a test that only does that passes with the
// canonical check deleted - which is what a mutation run found. The state that
// needs the check is the one where the payload is unsorted and its digest was
// computed from that same unsorted order: everything matches itself, and only
// "sorted" being part of the contract rejects it.
//
// It has to be part of the contract because the digest is an idempotency key.
// Two orderings of one memory would otherwise both validate, under two
// different keys, so a retry that happened to sort differently would not be
// recognised as the same write.
func TestPlanNoDataMutationValidateRefusesAnUnsortedPayload(t *testing.T) {
	unsorted := noDataMutation(
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
	)
	// Derived from the unsorted payload, so the payload and its digest agree
	// with each other and disagree only with the canonical form.
	selfConsistent, err := derivePlanNoDataMutationDigest(unsorted)
	if err != nil {
		t.Fatal(err)
	}
	unsorted.MutationDigest = selfConsistent
	if err := unsorted.ValidateDigest(); err == nil {
		t.Fatal("ValidateDigest() accepted an unsorted payload carrying its own matching digest; " +
			"one memory now has two idempotency keys")
	}

	// And the ordinary case: a canonical payload whose groups were moved
	// afterwards no longer matches the digest it carries.
	built, err := BuildPlanNoDataMutation(noDataMutation(
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	built.Groups[0], built.Groups[1] = built.Groups[1], built.Groups[0]
	if err := built.ValidateDigest(); err == nil {
		t.Fatal("ValidateDigest() accepted a payload that no longer matches its digest")
	}
}

// Changing any remembered timestamp has to change the digest, or two different
// memories share one idempotency key and the second one is dropped as a repeat.
func TestPlanNoDataMutationDigestCoversEveryTimestamp(t *testing.T) {
	base := NoDataGroupMemory{GroupKey: "a", LastSeen: 90, FirstAbsent: 100}
	original, err := BuildPlanNoDataMutation(noDataMutation(base))
	if err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]NoDataGroupMemory{
		"last seen":    {GroupKey: "a", LastSeen: 91, FirstAbsent: 100},
		"first absent": {GroupKey: "a", LastSeen: 90, FirstAbsent: 101},
		"group key":    {GroupKey: "b", LastSeen: 90, FirstAbsent: 100},
	} {
		t.Run(name, func(t *testing.T) {
			other, err := BuildPlanNoDataMutation(noDataMutation(changed))
			if err != nil {
				t.Fatal(err)
			}
			if other.MutationDigest == original.MutationDigest {
				t.Fatalf("changing the %s did not change the digest", name)
			}
		})
	}
}

// A record written by a newer build than this one is left exactly as it is.
// Clearing it would restart an absence clock that had been running the whole
// time, which under-reports an outage rather than over-reporting it, and an
// under-report is the direction nobody goes looking for.
func TestNoDataMemoryReadableRefusesAFutureSchema(t *testing.T) {
	if !NoDataMemoryReadable(0) {
		t.Fatal("schema 0 is the absence of a record, not a record this build cannot read")
	}
	if !NoDataMemoryReadable(NoDataMemorySchemaV1) {
		t.Fatal("this build cannot read the schema it writes")
	}
	if NoDataMemoryReadable(MaxSupportedNoDataMemorySchema + 1) {
		t.Fatal("a record from a newer build reads as readable")
	}
	if _, err := BuildPlanNoDataMutation(func() PlanNoDataMutation {
		mutation := noDataMutation(NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
		mutation.SchemaVersion = MaxSupportedNoDataMemorySchema + 1
		return mutation
	}()); err == nil {
		t.Fatal("a mutation in a schema this build cannot read was built anyway")
	}
}

// The schema version is in the digest, and that cannot be tested yet: there is
// exactly one version this build will write, so no two buildable mutations
// differ by it. Removing it from the digest today changes nothing any test can
// see.
//
// It matters the day a second version exists. A V1 and a V2 record holding the
// same groups would share an idempotency key, and a store that dedupes on the
// digest would drop the second write - leaving the record at the old shape
// while the writer believes it landed.
//
// So rather than a green test implying a coverage that is not there, this fails
// when the second version arrives, and whoever adds it writes the assertion
// then, with two real values to write it from.
func TestNoDataSchemaDigestSeparationIsPendingASecondVersion(t *testing.T) {
	if MaxSupportedNoDataMemorySchema != NoDataMemorySchemaV1 {
		t.Fatal("a second memory schema exists: add an assertion that two schema versions holding the " +
			"same groups produce different digests, then delete this test")
	}
}

// The absence duration a no-data event reports is derived from the timestamps
// in this struct, which is what makes a build that skipped rounds - upgraded,
// rolled back, out of budget - derive the same duration as one that ran every
// round. A field counting rounds would be wrong by exactly the rounds nobody
// ran, and wrong in the quiet direction.
//
// The test does not try to detect a counter. It states the field set, so that
// adding any field to the record fails here and the person adding it reads the
// reason before deciding. A guard that tried to recognise counters by name
// would pass anything named differently, which is the whole class of mistakes
// it is supposed to be closing.
func TestNoDataGroupMemoryRemembersTimestampsAndNothingElse(t *testing.T) {
	want := []struct {
		name string
		kind string
	}{
		{"GroupKey", "string"},
		{"LastSeen", "int64"},
		{"FirstAbsent", "int64"},
	}
	recordType := reflect.TypeOf(NoDataGroupMemory{})
	if recordType.NumField() != len(want) {
		var got []string
		for index := 0; index < recordType.NumField(); index++ {
			got = append(got, recordType.Field(index).Name)
		}
		t.Fatalf("NoDataGroupMemory has fields %s, want exactly %d. "+
			"If the new field counts rounds rather than naming a time, it breaks resuming after "+
			"a build skipped rounds; if it does not, update this list and say why in the commit.",
			strings.Join(got, ", "), len(want))
	}
	for index, field := range want {
		actual := recordType.Field(index)
		if actual.Name != field.name || actual.Type.String() != field.kind {
			t.Fatalf("field %d is %s %s, want %s %s", index, actual.Name, actual.Type, field.name, field.kind)
		}
	}
}
