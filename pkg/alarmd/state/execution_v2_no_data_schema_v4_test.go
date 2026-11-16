// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

// Schema v4 changed how the memory digest is derived and nothing a record
// stores (decision-020 batch 8, 2.7, K3). Old records therefore only have to
// decode, and the store's rules for a record at a write's own apply version
// are the ones they were: the guard compares the digest of the header it
// read, whichever build derived it.

// olderSchemaRecord is a per-group record as a build writing schema wrote it,
// with the memory digest that build derived.
func olderSchemaRecord(
	t *testing.T, schema execution.NoDataMemorySchema, revision uint64, version execution.ApplyVersion,
	digest execution.MutationDigest, groups ...execution.NoDataGroupMemory,
) map[string][]byte {
	t.Helper()
	header, err := json.Marshal(noDataHashHeader{
		Schema: executionNoDataSchema, Version: schema, Identity: noDataIdentityV2(),
		MarkerRevision: revision, ApplyVersion: version, MemoryDigest: digest,
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", PresentAsOf: noDataPresentAsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string][]byte{noDataHeaderField: header}
	for _, group := range groups {
		if group.FirstAbsent == 0 && group.SuppressedAt == 0 && group.LastSeen == noDataPresentAsOf {
			fields[noDataGroupPrefix+group.GroupKey] = []byte(noDataPresentValue)
			continue
		}
		value, err := json.Marshal(noDataGroupAbsenceValue{
			LastSeen: group.LastSeen, FirstAbsent: group.FirstAbsent, SuppressedAt: group.SuppressedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		fields[noDataGroupPrefix+group.GroupKey] = value
	}
	return fields
}

func seededNoDataStore(t *testing.T, record map[string][]byte) (*ExecutionStore, *casMemoryBackend) {
	t.Helper()
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	backend.hashes = map[string]map[string][]byte{noDataHashKey(t): record}
	return generationStore(t, backend), backend
}

func applyOneNoData(t *testing.T, store *ExecutionStore, mutation execution.PlanNoDataMutation) execution.NoDataApplyItemResult {
	t.Helper()
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(),
		Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	return applied.Items[0]
}

// A record of every schema before v4 still decodes, groups, digest and all.
func TestNoDataRecordsOfEveryOlderSchemaStillDecode(t *testing.T) {
	present := execution.NoDataGroupMemory{GroupKey: "live", LastSeen: noDataPresentAsOf}
	absent := execution.NoDataGroupMemory{GroupKey: "gone", LastSeen: 800, FirstAbsent: 880}
	stopped := execution.NoDataGroupMemory{GroupKey: "stopped", LastSeen: 700, FirstAbsent: 600, SuppressedAt: 880}
	for schema, groups := range map[execution.NoDataMemorySchema][]execution.NoDataGroupMemory{
		execution.NoDataMemorySchemaV2: {present, absent},
		execution.NoDataMemorySchemaV3: {present, absent, stopped},
		execution.NoDataMemorySchemaV4: {present, absent, stopped},
	} {
		store, _ := seededNoDataStore(t, olderSchemaRecord(t, schema, 3, applyVersion(), "older-digest", groups...))
		snapshot := loadOneNoDataMemory(t, store)
		if snapshot.Status != execution.NoDataMemoryFound || snapshot.SchemaVersion != schema {
			t.Fatalf("schema %d: snapshot = %s at schema %d, want it found as written", schema, snapshot.Status,
				snapshot.SchemaVersion)
		}
		if snapshot.PersistedMutationDigest != "older-digest" || snapshot.MarkerRevision != 3 {
			t.Fatalf("schema %d: snapshot = %+v, want the header's digest and revision", schema, snapshot)
		}
		if len(snapshot.Groups) != len(groups) {
			t.Fatalf("schema %d: groups = %+v, want %+v", schema, snapshot.Groups, groups)
		}
		byKey := map[string]execution.NoDataGroupMemory{}
		for _, group := range snapshot.Groups {
			byKey[group.GroupKey] = group
		}
		for _, group := range groups {
			if byKey[group.GroupKey] != group {
				t.Fatalf("schema %d: group %q = %+v, want %+v", schema, group.GroupKey, byKey[group.GroupKey], group)
			}
		}
	}
}

// At its own apply version a write meets an older-schema record by the rules
// it always did. The same digest is already applied. A different one is one
// statement meeting another of its own version - which is what a Slot written
// by the build before v4 and retried by this one meets, the memory unchanged
// and the digest moved only because the algorithm did: one named conflict,
// and the next round passes on its newer version.
func TestNoDataApplyMeetsAnOlderSchemaRecordAtItsOwnVersionByTheSameRules(t *testing.T) {
	group := execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 800, FirstAbsent: 880}
	mutation := noDataMutationV2(t, 3, group)

	t.Run("same digest is already applied", func(t *testing.T) {
		store, _ := seededNoDataStore(t, olderSchemaRecord(t, execution.NoDataMemorySchemaV3, 3, applyVersion(),
			mutation.MemoryDigest, group))
		item := applyOneNoData(t, store, mutation)
		if item.Status != execution.NoDataAlreadyApplied || item.Conflict != nil {
			t.Fatalf("apply = %+v, want ALREADY_APPLIED: the header names this memory", item)
		}
	})

	t.Run("an older build's digest of the same memory is one named conflict", func(t *testing.T) {
		store, _ := seededNoDataStore(t, olderSchemaRecord(t, execution.NoDataMemorySchemaV3, 3, applyVersion(),
			"v3-digest-of-the-same-memory", group))
		item := applyOneNoData(t, store, mutation)
		if item.Status != execution.NoDataConflict || item.Conflict == nil ||
			item.Conflict.Kind != execution.StateVersionConflictSameVersionOtherStatement {
			t.Fatalf("apply = %+v, want the same-version conflict", item)
		}
		if item.Conflict.Persisted != "v3-digest-of-the-same-memory" || item.Conflict.Proposed != mutation.MemoryDigest {
			t.Fatalf("conflict = %+v, want both memories named", item.Conflict)
		}
	})

	t.Run("the next round replaces it with a v4 record", func(t *testing.T) {
		store, backend := seededNoDataStore(t, olderSchemaRecord(t, execution.NoDataMemorySchemaV3, 3, applyVersion(),
			"v3-digest-of-the-same-memory", group))
		next := applyVersion()
		next.EvaluationTime += 60
		later := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: applyVersion(),
			Identity: noDataIdentityV2(), ExpectedMarkerRevision: 3, ApplyVersion: next,
			ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
			PresentAsOf: noDataPresentAsOf, LoadedPresentAsOf: noDataPresentAsOf,
			Memory: []execution.NoDataGroupMemory{group, {GroupKey: "b", LastSeen: noDataPresentAsOf}},
			Loaded: []execution.NoDataGroupMemory{group},
		})
		if item := applyOneNoData(t, store, later); item.Status != execution.NoDataApplied {
			t.Fatalf("apply = %+v, want the newer round applied", item)
		}
		var version noDataHashVersion
		if err := json.Unmarshal(backend.hashes[noDataHashKey(t)][noDataHeaderField], &version); err != nil {
			t.Fatal(err)
		}
		if version.Version != execution.NoDataMemorySchemaV4 {
			t.Fatalf("header schema = %d, want the record rewritten as v4", version.Version)
		}
		snapshot := loadOneNoDataMemory(t, store)
		if snapshot.SchemaVersion != execution.NoDataMemorySchemaV4 || snapshot.PersistedMutationDigest != later.MemoryDigest {
			t.Fatalf("snapshot = %+v, want the v4 record and its digest", snapshot)
		}
	})
}
