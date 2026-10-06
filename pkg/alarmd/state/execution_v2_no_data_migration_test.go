// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The round after the upgrade: read the record the old build left, write the
// one this build keeps.
//
// This is the whole point of the representation change and it was the one path
// with no test. Every existing case either started from the per-group record or
// from no record at all, and both of those happen to work; the one in between
// -- a Plan whose memory is still in the old record -- refused its write on
// every round for the entire fleet, because the revision it expected came off
// the old record and the new one had none.
//
// So the fixture is a record of the shape a build that predates this one
// writes, read back through the real loader, and the assertions are about the
// per-group record that results: its own revision line, every group carried
// over, and the round the Plan last had data in derived from what the old
// record holds rather than defaulted to zero.
func TestTheFirstWriteAfterReadingAWholeMemoryRecordMigratesIt(t *testing.T) {
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	// Revision seven, three groups, and no per-group record: a Plan that has
	// been remembering for a while and has not written since the upgrade.
	backend.values[blobKey] = wholeMemoryRecord(t, 7, applyVersion(),
		execution.NoDataGroupMemory{GroupKey: "absent", LastSeen: 820, FirstAbsent: 880},
		execution.NoDataGroupMemory{GroupKey: "history", LastSeen: 900},
		execution.NoDataGroupMemory{GroupKey: "present", LastSeen: 940},
	)
	store := generationStore(t, backend)

	snapshot := loadOneNoDataMemory(t, store)
	if snapshot.Status != execution.NoDataMemoryFound ||
		snapshot.Representation != execution.NoDataRepresentationWholeMemory {
		t.Fatalf("snapshot = %+v, want the whole-memory record found", snapshot)
	}
	if snapshot.MarkerRevision != 7 {
		t.Fatalf("marker revision = %d, want the old record's 7", snapshot.MarkerRevision)
	}
	if snapshot.PresentAsOf != 940 {
		t.Fatalf("present as of = %d, want the newest last-seen time in the record (940). Reading it as "+
			"zero contradicts every group in the record, and the memory the round derives from it is "+
			"then refused before it is ever written", snapshot.PresentAsOf)
	}

	// The round writes the memory back unchanged, which is what a round that
	// saw nothing does.
	mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
		LoadedApplyVersion:     snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(1),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: snapshot.PresentAsOf, Memory: snapshot.Groups,
		Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
	})
	if len(mutation.Set) != 3 {
		t.Fatalf("the statement carries %d groups, want all three. A delta against the old record leaves "+
			"out the groups that did not change, and they are not in the new record at all: %+v",
			len(mutation.Set), mutation.Set)
	}
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("write = %+v (%+v), want APPLIED. A revision belongs to the record that issued it, and "+
			"the old record's cannot be expected of a record that does not exist yet",
			applied.Items[0].Status, applied.Items[0].Conflict)
	}

	// What the per-group record now holds.
	record := backend.hashes[noDataHashKey(t)]
	if len(record) != 4 {
		t.Fatalf("the per-group record holds %d fields, want a header and three groups: %v",
			len(record), fieldNames(record))
	}
	var header noDataHashHeader
	if err := json.Unmarshal(record[noDataHeaderField], &header); err != nil {
		t.Fatal(err)
	}
	if header.MarkerRevision != 1 {
		t.Fatalf("marker revision = %d, want 1: this record's revision line starts here, and carrying the "+
			"old record's number over would claim a history it does not have", header.MarkerRevision)
	}
	if header.PresentAsOf != 940 {
		t.Fatalf("present as of = %d, want 940", header.PresentAsOf)
	}

	// And it reads back as the same memory, which is the only thing that
	// matters to the next round.
	reloaded := loadOneNoDataMemory(t, generationStore(t, backend))
	if reloaded.Representation != execution.NoDataRepresentationPerGroup {
		t.Fatalf("representation = %q, want the per-group record to be the memory now",
			reloaded.Representation)
	}
	want := map[string]execution.NoDataGroupMemory{
		"absent":  {GroupKey: "absent", LastSeen: 820, FirstAbsent: 880},
		"history": {GroupKey: "history", LastSeen: 900},
		"present": {GroupKey: "present", LastSeen: 940},
	}
	if len(reloaded.Groups) != len(want) {
		t.Fatalf("groups = %+v, want all three carried over", reloaded.Groups)
	}
	for _, group := range reloaded.Groups {
		if group != want[group.GroupKey] {
			t.Fatalf("group %q = %+v, want %+v", group.GroupKey, group, want[group.GroupKey])
		}
	}
}

// A write derived from the old record replaces the new one rather than being
// laid on top of it.
//
// Reachable during a rollout: a Plan writes the per-group record, moves to a
// worker on an older build that writes the whole-memory one, and comes back.
// The old record is then the memory, and it has since dropped groups the
// per-group record still holds. Applying the statement as a difference would
// leave those behind, and the result is a memory neither build ever wrote.
func TestAWriteDerivedFromTheOldRecordReplacesTheNewOne(t *testing.T) {
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.values[blobKey] = wholeMemoryRecord(t, 9, coexistenceApplyVersion(2),
		execution.NoDataGroupMemory{GroupKey: "kept", LastSeen: 940},
	)
	backend.hashes = map[string]map[string][]byte{
		noDataHashKey(t): perGroupRecord(t, 3, coexistenceApplyVersion(1), 880,
			execution.NoDataGroupMemory{GroupKey: "kept", LastSeen: 880},
			execution.NoDataGroupMemory{GroupKey: "stale", LastSeen: 700, FirstAbsent: 760},
		),
	}
	store := generationStore(t, backend)
	snapshot := loadOneNoDataMemory(t, store)
	if snapshot.Representation != execution.NoDataRepresentationWholeMemory {
		t.Fatalf("representation = %q, want the newer whole-memory record to be the memory",
			snapshot.Representation)
	}
	mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
		LoadedApplyVersion:     snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(3),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: snapshot.PresentAsOf, Memory: snapshot.Groups,
		Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
	})
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("write = %+v (%+v), want APPLIED", applied.Items[0].Status, applied.Items[0].Conflict)
	}
	record := backend.hashes[noDataHashKey(t)]
	if _, left := record[noDataGroupPrefix+"stale"]; left {
		t.Fatalf("the group the old record no longer has is still in the new one: %v. The statement "+
			"carries the whole memory, so what was there has to go", fieldNames(record))
	}
	if len(record) != 2 {
		t.Fatalf("the record holds %d fields, want a header and the one group: %v", len(record), fieldNames(record))
	}
	var header noDataHashHeader
	if err := json.Unmarshal(record[noDataHeaderField], &header); err != nil {
		t.Fatal(err)
	}
	if header.MarkerRevision != 4 {
		t.Fatalf("marker revision = %d, want the record's own 3 advanced by one", header.MarkerRevision)
	}
}

// A statement derived from the per-group record is still a delta against it,
// and still has to match its revision.
//
// The other half of the rule. Loosening the revision comparison for the
// statements that cannot use it must not loosen it for the ones that can:
// that comparison is what stops two writers of the same record from each
// applying a difference the other did not see.
func TestAPerGroupStatementStillHasToMatchTheRecordsRevision(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.hashes = map[string]map[string][]byte{
		noDataHashKey(t): perGroupRecord(t, 5, applyVersion(), 940,
			execution.NoDataGroupMemory{GroupKey: "kept", LastSeen: 940}),
	}
	store := generationStore(t, backend)
	mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: execution.NoDataRepresentationPerGroup,
		LoadedApplyVersion: applyVersion(),
		// Derived against revision four while the record is at five: another
		// writer moved it in between.
		ExpectedMarkerRevision: 4, ApplyVersion: coexistenceApplyVersion(1),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", PresentAsOf: 1000,
		Memory: []execution.NoDataGroupMemory{{GroupKey: "kept", LastSeen: 1000}},
	})
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := applied.Items[0]
	if item.Status != execution.NoDataConflict {
		t.Fatalf("write = %q, want a conflict: a delta against a revision the record has moved past "+
			"describes a difference from something nobody holds", item.Status)
	}
	if item.Conflict == nil {
		t.Fatal("the conflict names nothing")
	}
	if item.Conflict.Kind != execution.StateVersionConflictRevisionMoved {
		t.Fatalf("conflict kind = %q, want %q", item.Conflict.Kind, execution.StateVersionConflictRevisionMoved)
	}
	// The two sides of the comparison, on the refusal itself. A refusal that
	// reports neither is what a fleet-wide outage looked like from the outside:
	// every write refused, and the line said the reason was not reported.
	if item.Conflict.ExpectedRevision != 4 || item.Conflict.StoredRevision != 5 {
		t.Fatalf("conflict compared %d against %d, want the statement's 4 against the record's 5",
			item.Conflict.ExpectedRevision, item.Conflict.StoredRevision)
	}
	if item.Conflict.DerivedFrom != execution.NoDataRepresentationPerGroup {
		t.Fatalf("conflict derived-from = %q, want the record the statement was built from",
			item.Conflict.DerivedFrom)
	}
}

func fieldNames(record map[string][]byte) []string {
	names := make([]string, 0, len(record))
	for name := range record {
		names = append(names, name)
	}
	return names
}

// A Plan that has never written a memory reads no record, and what it reads has
// to say so in the one spelling the writer accepts.
//
// The writer refuses a statement that does not name the record it was derived
// from, and "no record" is one of the three names. A snapshot the store left
// unstamped would therefore stop every new Plan's first write -- the same
// outage as the one this change fixes, moved to the Plans that have nothing
// stored yet. Only the real loader can show it: every double in the tree stamps
// the field itself, which is exactly how the empty spelling survived to
// production in the first place.
func TestAPlanWithNoRecordReadsNoneAndWritesItsFirstMemory(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	snapshot := loadOneNoDataMemory(t, store)
	if snapshot.Status != execution.NoDataMemoryMissing {
		t.Fatalf("status = %q, want the record to be absent", snapshot.Status)
	}
	if snapshot.Representation != execution.NoDataRepresentationNone {
		t.Fatalf("representation = %q, want %s from the real loader for a record that is not there",
			snapshot.Representation, execution.NoDataRepresentationNone)
	}
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
		LoadedApplyVersion:     snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(1),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: 1000, Memory: []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 1000}},
		Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
	})
	if err != nil {
		t.Fatalf("the first memory of a new Plan does not derive: %v", err)
	}
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("first write = %+v (%+v), want APPLIED", applied.Items[0].Status, applied.Items[0].Conflict)
	}
}

// A replacing statement that loses a race says the record moved, not that it
// was reset.
//
// The two names mean different incidents. Moved is another writer getting there
// first, which is ordinary and self-correcting; reset says the key was deleted
// and built again from nothing, which is a thing somebody goes and looks into.
// A replacing statement expects no revision of this record, so comparing its
// expected number against the record's produces the second name for the first
// incident -- on the migration population, which is the whole fleet on the day
// this ships and exactly the line its acceptance is read from.
func TestAReplacingStatementThatLosesARaceReportsTheRecordMoved(t *testing.T) {
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.values[blobKey] = wholeMemoryRecord(t, 7, applyVersion(),
		execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940})
	store := generationStore(t, backend)
	snapshot := loadOneNoDataMemory(t, store)
	mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
		LoadedApplyVersion:     snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(1),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: snapshot.PresentAsOf, Memory: snapshot.Groups,
		Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
	})
	// Somebody wrote the per-group record between this writer's read of it and
	// its write, which is the only way a replacing statement can be refused.
	backend.conflict = true
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := applied.Items[0]
	if item.Status != execution.NoDataConflict || item.Conflict == nil {
		t.Fatalf("write = %+v, want a conflict that names itself", item)
	}
	if item.Conflict.Kind != execution.StateVersionConflictRevisionMoved {
		t.Fatalf("conflict kind = %q, want %q. The old record's revision is not this record's, so "+
			"comparing them names an incident that did not happen",
			item.Conflict.Kind, execution.StateVersionConflictRevisionMoved)
	}
}

// A whole-record statement expects the version it read where a delta expects
// the revision. Read none and met a record, or read the whole-memory record and
// met a per-group record newer than it: somebody wrote between this Slot's read
// and its write, and the statement is refused so the retry derives against
// what is there. Met a per-group record the read already outranked: that is the
// record the read decided to replace, and it is replaced.
func TestAReplacingStatementExpectsTheVersionItRead(t *testing.T) {
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	newRecord := func(version execution.ApplyVersion) map[string]map[string][]byte {
		return map[string]map[string][]byte{
			noDataHashKey(t): perGroupRecord(t, 2, version, 1000,
				execution.NoDataGroupMemory{GroupKey: "late", LastSeen: 1000}),
		}
	}

	t.Run("read none, met a record", func(t *testing.T) {
		backend := &casMemoryBackend{values: make(map[string][]byte)}
		store := generationStore(t, backend)
		snapshot := loadOneNoDataMemory(t, store)
		mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
			LoadedApplyVersion:     snapshot.PersistedApplyVersion,
			ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(3),
			ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
			PresentAsOf: 1060, Memory: []execution.NoDataGroupMemory{{GroupKey: "mine", LastSeen: 1060}},
		})
		// Written by an older Slot after this one read nothing.
		backend.hashes = newRecord(coexistenceApplyVersion(2))
		applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
			Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
		})
		if err != nil {
			t.Fatal(err)
		}
		item := applied.Items[0]
		if item.Status != execution.NoDataConflict || item.Conflict == nil ||
			item.Conflict.Kind != execution.StateVersionConflictRevisionMoved {
			t.Fatalf("write = %+v (%+v), want a revision_moved conflict: the record appeared after the read, "+
				"and replacing it would take the round that wrote it out of the memory", item.Status, item.Conflict)
		}
		if _, gone := backend.hashes[noDataHashKey(t)]["g:late"]; !gone {
			t.Fatal("the record written after the read was replaced")
		}
	})

	t.Run("read the whole-memory record, met a newer per-group record", func(t *testing.T) {
		backend := &casMemoryBackend{values: make(map[string][]byte)}
		backend.values[blobKey] = wholeMemoryRecord(t, 4, coexistenceApplyVersion(1),
			execution.NoDataGroupMemory{GroupKey: "kept", LastSeen: 940})
		store := generationStore(t, backend)
		snapshot := loadOneNoDataMemory(t, store)
		if snapshot.Representation != execution.NoDataRepresentationWholeMemory {
			t.Fatalf("representation = %q, want the whole-memory record", snapshot.Representation)
		}
		mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
			LoadedApplyVersion:     snapshot.PersistedApplyVersion,
			ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(3),
			ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
			PresentAsOf: snapshot.PresentAsOf, Memory: snapshot.Groups,
			Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
		})
		backend.hashes = newRecord(coexistenceApplyVersion(2))
		applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
			Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
		})
		if err != nil {
			t.Fatal(err)
		}
		item := applied.Items[0]
		if item.Status != execution.NoDataConflict || item.Conflict == nil ||
			item.Conflict.Kind != execution.StateVersionConflictRevisionMoved {
			t.Fatalf("write = %+v (%+v), want a revision_moved conflict against a per-group record newer "+
				"than the whole-memory record the statement was derived from", item.Status, item.Conflict)
		}
	})

	t.Run("read the whole-memory record, met the older per-group record it outranked", func(t *testing.T) {
		backend := &casMemoryBackend{values: make(map[string][]byte)}
		backend.values[blobKey] = wholeMemoryRecord(t, 4, coexistenceApplyVersion(2),
			execution.NoDataGroupMemory{GroupKey: "kept", LastSeen: 940})
		backend.hashes = newRecord(coexistenceApplyVersion(1))
		store := generationStore(t, backend)
		snapshot := loadOneNoDataMemory(t, store)
		if snapshot.Representation != execution.NoDataRepresentationWholeMemory {
			t.Fatalf("representation = %q, want the newer whole-memory record", snapshot.Representation)
		}
		mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
			LoadedApplyVersion:     snapshot.PersistedApplyVersion,
			ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(3),
			ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
			PresentAsOf: snapshot.PresentAsOf, Memory: snapshot.Groups,
			Loaded: snapshot.Groups, LoadedPresentAsOf: snapshot.PresentAsOf,
		})
		applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
			Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
		})
		if err != nil {
			t.Fatal(err)
		}
		if applied.Items[0].Status != execution.NoDataApplied {
			t.Fatalf("write = %+v (%+v), want APPLIED: the per-group record is the one the read outranked",
				applied.Items[0].Status, applied.Items[0].Conflict)
		}
		if _, kept := backend.hashes[noDataHashKey(t)]["g:late"]; kept {
			t.Fatal("the outranked record's groups survived a whole-record replacement")
		}
	})
}
