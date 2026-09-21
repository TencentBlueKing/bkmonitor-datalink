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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// coexistenceApplyVersion is an apply version a given number of rounds after
// the fixtures' base, so two records can be given an order.
func coexistenceApplyVersion(rounds int64) execution.ApplyVersion {
	version := applyVersion()
	version.EvaluationTime += execution.EvaluationTime(rounds * 60)
	return version
}

// wholeMemoryRecord is what a build that only knows the single-value shape
// leaves behind.
func wholeMemoryRecord(
	t *testing.T, revision uint64, version execution.ApplyVersion, groups ...execution.NoDataGroupMemory,
) []byte {
	t.Helper()
	encoded, err := json.Marshal(noDataEnvelope{
		Schema: executionNoDataSchema, Version: execution.NoDataMemorySchemaV1, Identity: noDataIdentityV2(),
		MarkerRevision: revision, ApplyVersion: version, MutationDigest: "blob-digest",
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", Groups: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// perGroupRecord is what this build writes: one header field and one field per
// group.
func perGroupRecord(
	t *testing.T, revision uint64, version execution.ApplyVersion, presentAsOf int64,
	groups ...execution.NoDataGroupMemory,
) map[string][]byte {
	t.Helper()
	header, err := json.Marshal(noDataHashHeader{
		Schema: executionNoDataSchema, Version: execution.NoDataMemorySchemaV2, Identity: noDataIdentityV2(),
		MarkerRevision: revision, ApplyVersion: version, MemoryDigest: "hash-digest",
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", PresentAsOf: presentAsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string][]byte{noDataHeaderField: header}
	for _, group := range groups {
		if group.FirstAbsent == 0 && group.LastSeen == presentAsOf {
			fields[noDataGroupPrefix+group.GroupKey] = []byte(noDataPresentValue)
			continue
		}
		value, err := json.Marshal(noDataGroupAbsenceValue{
			LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent,
		})
		if err != nil {
			t.Fatal(err)
		}
		fields[noDataGroupPrefix+group.GroupKey] = value
	}
	return fields
}

func loadOneNoDataMemory(t *testing.T, store *ExecutionStore) execution.NoDataMemorySnapshot {
	t.Helper()
	loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Items[0]
}

// During a rollout a Plan can have both records, and which one is its memory is
// decided by which round wrote it - not by which shape it is in.
//
// A build that writes the per-group record never writes the other one, so it is
// tempting to read the per-group one whenever it exists. That is wrong in the
// direction that loses data: a Plan can move back to a build that only knows
// the single-value shape - a rollback, or a rebalance onto a worker not yet
// upgraded - and that build then writes a record newer than the one left in the
// hash. Reading the hash because it is the new shape would silently discard
// every round that happened while the Plan was over there.
func TestTheNewerOfTheTwoRecordsIsTheMemory(t *testing.T) {
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	older, newer := coexistenceApplyVersion(0), coexistenceApplyVersion(1)

	for _, test := range []struct {
		name       string
		blob       []byte
		hash       map[string][]byte
		wantGroups []string
	}{
		{
			name: "the per-group record is newer",
			blob: wholeMemoryRecord(t, 4, older, execution.NoDataGroupMemory{GroupKey: "from-blob", FirstAbsent: 900}),
			hash: perGroupRecord(t, 2, newer, 940,
				execution.NoDataGroupMemory{GroupKey: "from-hash", FirstAbsent: 920}),
			wantGroups: []string{"from-hash"},
		},
		{
			name: "the single-value record is newer, because the Plan went back to an older build",
			blob: wholeMemoryRecord(t, 4, newer, execution.NoDataGroupMemory{GroupKey: "from-blob", FirstAbsent: 900}),
			hash: perGroupRecord(t, 2, older, 940,
				execution.NoDataGroupMemory{GroupKey: "from-hash", FirstAbsent: 920}),
			wantGroups: []string{"from-blob"},
		},
		{
			name:       "only the single-value record exists, which is every Plan on the first round after the upgrade",
			blob:       wholeMemoryRecord(t, 4, older, execution.NoDataGroupMemory{GroupKey: "from-blob", FirstAbsent: 900}),
			hash:       nil,
			wantGroups: []string{"from-blob"},
		},
		{
			name:       "only the per-group record exists, which is every Plan once it has written once",
			blob:       nil,
			hash:       perGroupRecord(t, 2, older, 940, execution.NoDataGroupMemory{GroupKey: "from-hash", FirstAbsent: 920}),
			wantGroups: []string{"from-hash"},
		},
		{
			name: "both state the same round, which one Slot cannot do",
			blob: wholeMemoryRecord(t, 4, older, execution.NoDataGroupMemory{GroupKey: "from-blob", FirstAbsent: 900}),
			hash: perGroupRecord(t, 2, older, 940,
				execution.NoDataGroupMemory{GroupKey: "from-hash", FirstAbsent: 920}),
			// The shape the fleet is moving to wins the tie. It cannot happen
			// with one Slot per round, and a tie that had no rule would be
			// decided by whichever read came back first.
			wantGroups: []string{"from-hash"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			if test.blob != nil {
				backend.values[blobKey] = test.blob
			}
			if test.hash != nil {
				backend.hashes = map[string]map[string][]byte{noDataHashKey(t): test.hash}
			}
			snapshot := loadOneNoDataMemory(t, generationStore(t, backend))
			if snapshot.Status != execution.NoDataMemoryFound {
				t.Fatalf("status = %q, want the memory found", snapshot.Status)
			}
			if len(snapshot.Groups) != len(test.wantGroups) {
				t.Fatalf("groups = %+v, want %v", snapshot.Groups, test.wantGroups)
			}
			for index, want := range test.wantGroups {
				if snapshot.Groups[index].GroupKey != want {
					t.Fatalf("groups = %+v, want %v: the wrong record was read", snapshot.Groups, test.wantGroups)
				}
			}
		})
	}
}

// A group written as present reads back with the round the header names as its
// last-seen time, which is the whole of what the compression means.
func TestAPresentGroupReadsBackWithTheRoundTheHeaderNames(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.hashes = map[string]map[string][]byte{
		noDataHashKey(t): perGroupRecord(t, 3, applyVersion(), 940,
			execution.NoDataGroupMemory{GroupKey: "live", LastSeen: 940},
			execution.NoDataGroupMemory{GroupKey: "gone", LastSeen: 800, FirstAbsent: 880},
			execution.NoDataGroupMemory{GroupKey: "history", LastSeen: 820},
		),
	}
	snapshot := loadOneNoDataMemory(t, generationStore(t, backend))
	want := map[string]execution.NoDataGroupMemory{
		"gone":    {GroupKey: "gone", LastSeen: 800, FirstAbsent: 880},
		"history": {GroupKey: "history", LastSeen: 820},
		"live":    {GroupKey: "live", LastSeen: 940},
	}
	if len(snapshot.Groups) != len(want) {
		t.Fatalf("groups = %+v, want three", snapshot.Groups)
	}
	for _, group := range snapshot.Groups {
		if group != want[group.GroupKey] {
			t.Fatalf("group %q = %+v, want %+v", group.GroupKey, group, want[group.GroupKey])
		}
	}
	if snapshot.PresentAsOf != 940 {
		t.Fatalf("present as of = %d, want the round the header names", snapshot.PresentAsOf)
	}
}

// A record with groups and no header is unreadable, not a record with a default
// header.
//
// Every write puts a header there, so its absence means the record was written
// by something that does not hold this shape, or was partly destroyed. Reading
// the groups anyway would take each present group's last-seen time from a round
// nobody stated - zero - and a group last seen at zero reports an absence as
// long as the epoch, which is a no-data alert on every group of the Plan.
func TestAPerGroupRecordWithNoHeaderIsUnreadable(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.hashes = map[string]map[string][]byte{
		noDataHashKey(t): {noDataGroupPrefix + "live": []byte(noDataPresentValue)},
	}
	snapshot := loadOneNoDataMemory(t, generationStore(t, backend))
	if snapshot.Status != execution.NoDataMemoryUnreadable {
		t.Fatalf("status = %q, want it unreadable rather than read with a made-up header", snapshot.Status)
	}
	if snapshot.ReasonCode != execution.ReasonCode(contract.ReasonStateSchemaUnsupported) {
		t.Fatalf("reason = %q, want the schema reason the paused Plan already reports", snapshot.ReasonCode)
	}
	if len(snapshot.Groups) != 0 {
		t.Fatalf("groups = %+v, want none read out of a record with no header", snapshot.Groups)
	}
}

// A field the reader has no rule for makes the record corrupt rather than being
// skipped. Skipping it would be reading a record while pretending not to have
// seen part of it, and the part not seen is a group whose absence is then never
// reported.
func TestAPerGroupRecordWithAnUnreadableFieldIsCorrupt(t *testing.T) {
	for name, fields := range map[string]map[string][]byte{
		"a field that is neither header nor group": {"stray": []byte("1")},
		"a group value that is neither present nor an absence": {
			noDataGroupPrefix + "live": []byte("what"),
		},
		"a group that remembers nothing": {
			noDataGroupPrefix + "live": []byte(`{}`),
		},
		"a group present against a Plan that has never had data": {
			noDataGroupPrefix + "live": []byte(noDataPresentValue),
		},
	} {
		t.Run(name, func(t *testing.T) {
			presentAsOf := int64(940)
			if name == "a group present against a Plan that has never had data" {
				presentAsOf = 0
			}
			record := perGroupRecord(t, 3, applyVersion(), presentAsOf)
			for field, value := range fields {
				record[field] = value
			}
			backend := &casMemoryBackend{values: make(map[string][]byte)}
			backend.hashes = map[string]map[string][]byte{noDataHashKey(t): record}
			snapshot := loadOneNoDataMemory(t, generationStore(t, backend))
			if snapshot.Status != execution.NoDataMemoryTerminal {
				t.Fatalf("status = %q, want it terminal: the record cannot be read as it is", snapshot.Status)
			}
		})
	}
}

// A write reads the header and not the record it is about to change.
//
// Everything a write decides on -- the schema, the revision, the version, the
// digest -- is in the header, and the groups are what this representation
// exists to stop moving. Reading them here would undo that on the write side:
// the load already transferred them once, and a second HGETALL per Plan per
// round is the whole saving given back on exactly the large objects that
// motivated the change. At three thousand groups it is roughly a quarter of a
// megabyte a round, against the few kilobytes the delta saves.
func TestAWriteReadsTheHeaderAndNotTheRecord(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	backend.hashes = map[string]map[string][]byte{
		noDataHashKey(t): perGroupRecord(t, 1, coexistenceApplyVersion(0), 940,
			execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940},
			execution.NoDataGroupMemory{GroupKey: "b", LastSeen: 940},
		),
	}
	backend.commands = nil

	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(),
		Items: []execution.PlanNoDataMutation{noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: coexistenceApplyVersion(0),
			Identity: noDataIdentityV2(), ExpectedMarkerRevision: 1,
			ApplyVersion: coexistenceApplyVersion(1), ScheduleRevision: "plan-r1",
			RosterVersion: "TARGET_STATIC/1", PresentAsOf: 1000,
			Memory: []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 1000}},
			Loaded: []execution.NoDataGroupMemory{
				{GroupKey: "a", LastSeen: 940}, {GroupKey: "b", LastSeen: 940},
			},
			LoadedPresentAsOf: 940,
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want it applied", applied.Items[0])
	}
	for _, command := range backend.commands {
		if command != "HGET" {
			t.Fatalf("the write path issued %v, want only HGET: reading the whole record to find "+
				"the header doubles what every Plan transfers per round", backend.commands)
		}
	}
	if len(backend.commands) == 0 {
		t.Fatal("the write path read nothing; it cannot have proven the record was untouched")
	}
}

// A memory read once and written back unchanged is recognised rather than
// written again, and a Plan that goes quiet keeps its record alive.
//
// The second half is new with this representation and is the failure it can
// have that the old one could not. A single-value record was rewritten whenever
// anything moved, and that write carried a lifetime; a hash is only touched for
// the groups that changed, so a Plan whose groups are all steady writes nothing
// at all - and without a renewal on the read, its memory would expire under it
// and every group would start again from no history.
func TestReadingTheMemoryKeepsItAlive(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
	key := noDataHashKey(t)
	backend.hashes = map[string]map[string][]byte{
		key: perGroupRecord(t, 3, applyVersion(), 940, execution.NoDataGroupMemory{GroupKey: "live", LastSeen: 940}),
	}
	backend.remaining[key] = time.Minute

	if snapshot := loadOneNoDataMemory(t, generationStore(t, backend)); snapshot.Status != execution.NoDataMemoryFound {
		t.Fatalf("status = %q, want the record found", snapshot.Status)
	}
	renewed := false
	for _, call := range backend.renewals {
		if call.Key == key && call.Renewed {
			renewed = true
		}
	}
	if !renewed {
		t.Fatalf("renewals = %+v, want the per-group record renewed: a steady Plan writes nothing and "+
			"its memory would otherwise expire", backend.renewals)
	}
}
