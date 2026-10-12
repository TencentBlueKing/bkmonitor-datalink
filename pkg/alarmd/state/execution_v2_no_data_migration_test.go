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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

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
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
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

// A Plan that has never written a memory reads no record, and what it reads has
// to say so in the one spelling the writer accepts.
//
// The writer refuses a statement that does not name the record it was derived
// from, and "no record" is one of the two names. A snapshot the store left
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
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
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
// incident.
func TestAReplacingStatementThatLosesARaceReportsTheRecordMoved(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	snapshot := loadOneNoDataMemory(t, store)
	mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		Identity: noDataIdentityV2(), DerivedFrom: snapshot.Representation,
		LoadedApplyVersion:     snapshot.PersistedApplyVersion,
		ExpectedMarkerRevision: snapshot.MarkerRevision, ApplyVersion: coexistenceApplyVersion(1),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: 940, Memory: []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 940}},
	})
	// Somebody wrote the per-group record between this writer's read of it and
	// its write, which is the only way a replacing statement can be refused.
	backend.conflict = true
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
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
		t.Fatalf("conflict kind = %q, want %q: a replacing statement expects no revision, so "+
			"comparing one names an incident that did not happen",
			item.Conflict.Kind, execution.StateVersionConflictRevisionMoved)
	}
}

// A whole-record statement is derived from no record, so it expects to meet
// none. Read none and met a record: somebody wrote between this Slot's read
// and its write, and the statement is refused so the retry derives against
// what is there.
func TestAReplacingStatementExpectsNoRecord(t *testing.T) {
	newRecord := func(version execution.ApplyVersion) map[string]map[string][]byte {
		return map[string]map[string][]byte{
			noDataHashKey(t): perGroupRecord(t, 2, version, 1000,
				execution.NoDataGroupMemory{GroupKey: "late", LastSeen: 1000}),
		}
	}
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
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{Retention: execution.GenerationRetention{Unknown: true},
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
}
