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

// noDataUpdate is a round that wrote nothing before it: every group is new, so
// the delta is the whole memory and the tests below can state memories rather
// than deltas.
func noDataUpdate(groups ...NoDataGroupMemory) PlanNoDataMemoryUpdate {
	return PlanNoDataMemoryUpdate{
		DerivedFrom: NoDataRepresentationPerGroup, LoadedApplyVersion: noDataApplyVersion(), ExpectedMarkerRevision: 1,
		Identity: PlanNoDataIdentity{
			Plan:            PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
			StateGeneration: "generation-1",
		},
		ApplyVersion:     noDataApplyVersion(),
		ScheduleRevision: "revision-1",
		RosterVersion:    "TARGET_STATIC/1",
		PresentAsOf:      90,
		Memory:           groups,
	}
}

// The digest closes the memory, so two callers that state the same memory in a
// different order produce the same mutation. A Slot builds its groups from a
// map, and a map's order is not a fact about the memory.
func TestPlanNoDataMutationIsIndependentOfStatedGroupOrder(t *testing.T) {
	first, err := BuildPlanNoDataMutation(noDataUpdate(
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlanNoDataMutation(noDataUpdate(
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
	built, err := BuildPlanNoDataMutation(noDataUpdate())
	if err != nil {
		t.Fatalf("an empty memory is a legitimate statement: %v", err)
	}
	if len(built.Set) != 0 || built.GroupCount != 0 {
		t.Fatalf("set = %+v count = %d, want an empty memory", built.Set, built.GroupCount)
	}
	if err := built.ValidateDigest(); err != nil {
		t.Fatal(err)
	}
	// It must not be the same statement as any non-empty memory.
	other, err := BuildPlanNoDataMutation(noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90}))
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
	base := noDataUpdate(NoDataGroupMemory{GroupKey: "a", FirstAbsent: 100})
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
	for name, broken := range map[string]func(*PlanNoDataMemoryUpdate){
		"no generation":        func(u *PlanNoDataMemoryUpdate) { u.Identity.StateGeneration = "" },
		"no schedule":          func(u *PlanNoDataMemoryUpdate) { u.ScheduleRevision = "" },
		"no roster version":    func(u *PlanNoDataMemoryUpdate) { u.RosterVersion = "" },
		"no apply version":     func(u *PlanNoDataMemoryUpdate) { u.ApplyVersion = ApplyVersion{} },
		"group with no key":    func(u *PlanNoDataMemoryUpdate) { u.Memory[0].GroupKey = "" },
		"negative timestamp":   func(u *PlanNoDataMemoryUpdate) { u.Memory[0].FirstAbsent = -1 },
		"group remembers not":  func(u *PlanNoDataMemoryUpdate) { u.Memory[0] = NoDataGroupMemory{GroupKey: "a"} },
		"negative present":     func(u *PlanNoDataMemoryUpdate) { u.PresentAsOf = -1 },
		"present moved back":   func(u *PlanNoDataMemoryUpdate) { u.LoadedPresentAsOf = u.PresentAsOf + 1 },
		"unstorable loaded":    func(u *PlanNoDataMemoryUpdate) { u.Loaded = []NoDataGroupMemory{{GroupKey: "x"}} },
		"duplicate in loaded":  func(u *PlanNoDataMemoryUpdate) { u.Loaded = append(u.Loaded, u.Memory[0], u.Memory[0]) },
		"two records one key":  func(u *PlanNoDataMemoryUpdate) { u.Memory = append(u.Memory, u.Memory[0]) },
		"present with no data": func(u *PlanNoDataMemoryUpdate) { u.PresentAsOf, u.Memory[0].FirstAbsent = 0, 0 },
		// The loaded version goes with the record: derived from one, it names
		// the version read; derived from none, it names nothing. Either way
		// round, the store's "did the record move since the read" comparison
		// would run against a value nobody read.
		"record read, no loaded version": func(u *PlanNoDataMemoryUpdate) { u.LoadedApplyVersion = ApplyVersion{} },
		"no record read, loaded version": func(u *PlanNoDataMemoryUpdate) {
			u.DerivedFrom, u.Loaded, u.LoadedPresentAsOf = NoDataRepresentationNone, nil, 0
		},
		// A record read carries a revision of one or more; a statement that
		// read one and expects zero would be applied as a delta to a record
		// the store believes absent, which is the lost-groups shape.
		"record read, no revision": func(u *PlanNoDataMemoryUpdate) { u.ExpectedMarkerRevision = 0 },
		"no record read, a revision": func(u *PlanNoDataMemoryUpdate) {
			u.DerivedFrom, u.LoadedApplyVersion, u.Loaded, u.LoadedPresentAsOf = NoDataRepresentationNone, ApplyVersion{}, nil, 0
			u.ExpectedMarkerRevision = 3
		},
	} {
		t.Run(name, func(t *testing.T) {
			update := noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90, FirstAbsent: 100})
			broken(&update)
			if _, err := BuildPlanNoDataMutation(update); err == nil {
				t.Fatal("BuildPlanNoDataMutation() accepted an incomplete payload")
			}
		})
	}
}

// A payload whose groups are not in canonical order is refused, and refused for
// being unsorted rather than only for failing to match its digest.
//
// The two are easy to confuse, and one of them does nothing. Swapping two
// groups of a built mutation changes what the digest would derive to, so a
// digest comparison alone rejects it and a test that only does that passes with
// the canonical check deleted - which is what a mutation run found. The state
// that needs the check is the one where the payload is unsorted and its digest
// was computed from that same order: everything matches itself, and only
// "sorted" being part of the contract rejects it.
//
// It has to be part of the contract because the digest is an idempotency key.
// Two orderings of one delta would otherwise both validate, and a retry that
// happened to sort differently would not be recognised as the same write.
func TestPlanNoDataMutationValidateRefusesAnUnsortedPayload(t *testing.T) {
	built, err := BuildPlanNoDataMutation(noDataUpdate(
		NoDataGroupMemory{GroupKey: "a", LastSeen: 90},
		NoDataGroupMemory{GroupKey: "b", FirstAbsent: 100},
	))
	if err != nil {
		t.Fatal(err)
	}
	unsorted := built
	unsorted.Set = []NoDataGroupDelta{built.Set[1], built.Set[0]}
	if err := unsorted.ValidateDigest(); err == nil {
		t.Fatal("ValidateDigest() accepted an unsorted delta")
	}
	deletes := built
	deletes.Del = []string{"b", "a"}
	if err := deletes.ValidateDigest(); err == nil {
		t.Fatal("ValidateDigest() accepted unsorted deletes")
	}
	// And the order cannot be made self-consistent: the derivation refuses an
	// unsorted statement too, so one delta has exactly one digest rather than
	// one per ordering. Without that, a retry that happened to sort differently
	// would carry a second idempotency key for the same write.
	if _, err := derivePlanNoDataStatementDigest(unsorted); err == nil {
		t.Fatal("an unsorted statement can be given a digest of its own")
	}
}

// A group cannot be both written and removed. One of the two would win inside
// the store, silently, and which one is a property of the script rather than of
// the statement.
func TestPlanNoDataMutationRefusesAGroupItBothWritesAndDeletes(t *testing.T) {
	built, err := BuildPlanNoDataMutation(noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90}))
	if err != nil {
		t.Fatal(err)
	}
	built.Del = []string{"a"}
	if err := built.ValidateDigest(); err == nil {
		t.Fatal("ValidateDigest() accepted a group written and deleted by one statement")
	}
}

// Changing any remembered timestamp has to change the digest, or two different
// memories share one idempotency key and the second one is dropped as a repeat.
func TestPlanNoDataMutationDigestCoversEveryTimestamp(t *testing.T) {
	base := NoDataGroupMemory{GroupKey: "a", LastSeen: 80, FirstAbsent: 100}
	original, err := BuildPlanNoDataMutation(noDataUpdate(base))
	if err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]NoDataGroupMemory{
		"last seen":    {GroupKey: "a", LastSeen: 81, FirstAbsent: 100},
		"first absent": {GroupKey: "a", LastSeen: 80, FirstAbsent: 101},
		"group key":    {GroupKey: "b", LastSeen: 80, FirstAbsent: 100},
	} {
		t.Run(name, func(t *testing.T) {
			other, err := BuildPlanNoDataMutation(noDataUpdate(changed))
			if err != nil {
				t.Fatal(err)
			}
			if other.MutationDigest == original.MutationDigest {
				t.Fatalf("changing the %s did not change the digest", name)
			}
		})
	}
}

// The round a Plan last had data in is part of the memory, not a label on it:
// it is the last-seen time of every group stored without an absence. Two
// memories that differ only by it are two different sets of timestamps.
func TestPlanNoDataMutationDigestCoversThePresentRound(t *testing.T) {
	update := noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
	first, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	update.PresentAsOf = 120
	update.Memory = []NoDataGroupMemory{{GroupKey: "a", LastSeen: 120}}
	second, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("two rounds of presence produced one digest; the second write would read as already applied")
	}
}

// The statement digest covers the version the statement was derived against.
// The store decides whether the record moved since the read from that value,
// and a value the digest does not cover is one the apply-time check could run
// against after the statement was altered in flight without anyone noticing.
func TestPlanNoDataMutationDigestCoversTheLoadedVersion(t *testing.T) {
	update := noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90})
	first, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	update.LoadedApplyVersion.EvaluationTime++
	second, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("two statements derived against different versions produced one digest")
	}
	// And a statement whose loaded version was altered after it was built
	// fails its own digest, which is the whole point of covering it.
	altered := first
	altered.LoadedApplyVersion.EvaluationTime++
	if err := altered.ValidateDigest(); err == nil {
		t.Fatal("a statement with its loaded version altered in flight validated against its digest")
	}
}

// A group seen in the round the header names is stored as present and nothing
// else, and that is where the whole representation change pays: a Plan with
// thousands of groups writes the round once rather than once per group.
func TestAGroupSeenThisRoundIsStoredAsPresent(t *testing.T) {
	built, err := BuildPlanNoDataMutation(noDataUpdate(
		NoDataGroupMemory{GroupKey: "seen", LastSeen: 90},
		NoDataGroupMemory{GroupKey: "gone", LastSeen: 60, FirstAbsent: 70},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Set) != 2 {
		t.Fatalf("set = %+v, want both groups", built.Set)
	}
	if built.Set[0].GroupKey != "gone" || built.Set[0].Absent == nil {
		t.Fatalf("absent group = %+v, want its two times written out", built.Set[0])
	}
	if *built.Set[0].Absent != (NoDataGroupAbsence{LastSeen: 60, FirstAbsent: 70}) {
		t.Fatalf("absent group = %+v, want the times it was decided with", *built.Set[0].Absent)
	}
	if built.Set[1].GroupKey != "seen" || built.Set[1].Absent != nil {
		t.Fatalf("present group = %+v, want it stored as present", built.Set[1])
	}
}

// A group the roster stopped expecting while it was present keeps its own
// last-seen time and never gets a first-absent. It is not present in the round
// the header names, and storing it as present would silently move its last-seen
// time forward with the Plan's - which is the number the reported absence
// duration is derived from, so the outage would read as shorter than it was,
// every round, for as long as it lasted.
func TestAGroupLastSeenBeforeThisRoundIsNotStoredAsPresent(t *testing.T) {
	update := noDataUpdate(
		NoDataGroupMemory{GroupKey: "history", LastSeen: 60},
		NoDataGroupMemory{GroupKey: "live", LastSeen: 90},
	)
	built, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	var history NoDataGroupDelta
	for _, group := range built.Set {
		if group.GroupKey == "history" {
			history = group
		}
	}
	if history.Absent == nil {
		t.Fatal("a group last seen two rounds ago was stored as present; its last-seen time now follows the Plan's")
	}
	if *history.Absent != (NoDataGroupAbsence{LastSeen: 60}) {
		t.Fatalf("history group = %+v, want its own last-seen time and no absence", *history.Absent)
	}
	// And the validator must refuse the other encoding of the same group, so
	// the two can never both be written and produce two digests for one memory.
	//
	// Through validateStatement rather than ValidateDigest, and that is the
	// whole point of this half. Rewriting a built mutation changes what its
	// statement digest derives to, so ValidateDigest rejects it either way and
	// a test using it passes with this rule deleted -- which is what a mutation
	// run found. The rule is pinned where it lives.
	longWay := built
	longWay.Set = []NoDataGroupDelta{{
		GroupKey: "live", Absent: &NoDataGroupAbsence{LastSeen: built.PresentAsOf},
	}}
	if err := longWay.validateStatement(); err == nil {
		t.Fatal("validateStatement() accepted a present group written the long way; one memory now " +
			"has two encodings and so two digests")
	}
	// The same payload written the short way is accepted, so the refusal above
	// is about the encoding and not about something else in the statement.
	shortWay := built
	shortWay.Set = []NoDataGroupDelta{{GroupKey: "live"}}
	if err := shortWay.validateStatement(); err != nil {
		t.Fatalf("validateStatement() refused the compressed encoding: %v", err)
	}
}

// The delta is what changed in the stored values, not in the timestamps. A
// group present in two consecutive rounds has a new last-seen time and the same
// stored value, and writing it would be writing one byte back per group per
// round - the cost this representation exists to remove.
func TestAGroupStillPresentIsNotWrittenAgain(t *testing.T) {
	update := PlanNoDataMemoryUpdate{
		DerivedFrom: NoDataRepresentationPerGroup, LoadedApplyVersion: noDataApplyVersion(), ExpectedMarkerRevision: 1,
		Identity: PlanNoDataIdentity{
			Plan:            PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
			StateGeneration: "generation-1",
		},
		ApplyVersion:     noDataApplyVersion(),
		ScheduleRevision: "revision-1",
		RosterVersion:    "TARGET_STATIC/1",
		PresentAsOf:      120,
		Memory: []NoDataGroupMemory{
			{GroupKey: "a", LastSeen: 120},
			{GroupKey: "b", LastSeen: 120},
			{GroupKey: "left", LastSeen: 60, FirstAbsent: 70},
		},
		Loaded: []NoDataGroupMemory{
			{GroupKey: "a", LastSeen: 90},
			{GroupKey: "b", LastSeen: 90},
			{GroupKey: "left", LastSeen: 60, FirstAbsent: 70},
			{GroupKey: "dropped", LastSeen: 60, FirstAbsent: 70},
		},
		LoadedPresentAsOf: 90,
	}
	built, err := BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Set) != 0 {
		t.Fatalf("set = %+v, want nothing: the two present groups keep their stored value and the "+
			"absent one did not move", built.Set)
	}
	if len(built.Del) != 1 || built.Del[0] != "dropped" {
		t.Fatalf("del = %v, want only the group that left the memory", built.Del)
	}
	if built.GroupCount != 3 {
		t.Fatalf("group count = %d, want the three the memory holds", built.GroupCount)
	}
}

// Reaching one memory from two different records produces one digest. It is
// what lets a retry that re-read a record the first attempt had already changed
// be recognised as the same statement instead of as a conflict.
func TestOneMemoryReachedFromTwoRecordsHasOneDigest(t *testing.T) {
	memory := []NoDataGroupMemory{{GroupKey: "a", LastSeen: 120}, {GroupKey: "b", LastSeen: 60, FirstAbsent: 70}}
	fresh := noDataUpdate(memory...)
	fresh.PresentAsOf = 120
	retried := fresh
	retried.Loaded = memory
	retried.LoadedPresentAsOf = 120
	retried.ExpectedMarkerRevision = 4

	first, err := BuildPlanNoDataMutation(fresh)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPlanNoDataMutation(retried)
	if err != nil {
		t.Fatal(err)
	}
	if first.MemoryDigest != second.MemoryDigest {
		t.Fatalf("one memory reached two ways produced two digests: %s vs %s",
			first.MemoryDigest, second.MemoryDigest)
	}
	// The statements do differ, and must: they are relative to different
	// records. It is the memory they agree on, which is what the store compares.
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("two statements against different records share one statement digest")
	}
	if len(second.Set) != 0 || len(second.Del) != 0 {
		t.Fatalf("the retry restated a memory that was already there: %+v %v", second.Set, second.Del)
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
	for _, schema := range []NoDataMemorySchema{NoDataMemorySchemaV1, NoDataMemorySchemaV2} {
		if !NoDataMemoryReadable(schema) {
			t.Fatalf("this build cannot read schema %d, which it is expected to", schema)
		}
	}
	if NoDataMemoryReadable(MaxSupportedNoDataMemorySchema + 1) {
		t.Fatal("a record from a newer build reads as readable")
	}
}

// This build reads two shapes and writes one. That asymmetry is the whole
// coexistence rule, and it is asserted rather than described: a build that
// wrote both would leave two records per Plan with no rule for which is the
// memory, and one that wrote the old shape would undo the change.
func TestThisBuildWritesOneSchemaAndReadsTwo(t *testing.T) {
	if WrittenNoDataMemorySchema != NoDataMemorySchemaV2 {
		t.Fatalf("this build writes schema %d, want the per-group one", WrittenNoDataMemorySchema)
	}
	built, err := BuildPlanNoDataMutation(noDataUpdate(NoDataGroupMemory{GroupKey: "a", LastSeen: 90}))
	if err != nil {
		t.Fatal(err)
	}
	if built.SchemaVersion != WrittenNoDataMemorySchema {
		t.Fatalf("built schema = %d, want %d", built.SchemaVersion, WrittenNoDataMemorySchema)
	}
	// The builder owns the version as well as the digest. A caller that could
	// state one could write the shape this build no longer maintains.
	for _, schema := range []NoDataMemorySchema{0, NoDataMemorySchemaV1, MaxSupportedNoDataMemorySchema + 1} {
		other := built
		other.SchemaVersion = schema
		if err := other.ValidateDigest(); err == nil {
			t.Fatalf("a mutation in schema %d validated; this build writes only %d",
				schema, WrittenNoDataMemorySchema)
		}
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
