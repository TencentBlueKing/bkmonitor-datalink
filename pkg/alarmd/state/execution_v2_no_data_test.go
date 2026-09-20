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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func noDataIdentityV2() execution.PlanNoDataIdentity {
	return execution.PlanNoDataIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
}

func noDataLoadItemV2() execution.PlanNoDataLoadItem {
	return execution.PlanNoDataLoadItem{
		Identity: noDataIdentityV2(), ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
	}
}

func noDataMutationV2(t *testing.T, expected uint64, groups ...execution.NoDataGroupMemory) execution.PlanNoDataMutation {
	t.Helper()
	mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMutation{
		Identity: noDataIdentityV2(), SchemaVersion: execution.NoDataMemorySchemaV1,
		ExpectedMarkerRevision: expected, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
		RosterVersion: "TARGET_STATIC/1", Groups: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// What was written comes back, including the roster version the round decided
// against and the marker revision the next write has to expect.
func TestNoDataMemoryRoundTripsThroughTheStore(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	loaded, err := store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Status != execution.NoDataMemoryMissing {
		t.Fatalf("a Plan that has written nothing loaded as %q", loaded.Items[0].Status)
	}

	mutation := noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940})
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want it applied", applied.Items[0])
	}
	// Written with a lifetime. The load renews it to what this Plan needs, but a
	// key created in a Plan's last Slot is never loaded again, and one written
	// without a lifetime would then be immortal.
	key, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	if got := backend.writeTTLs[key]; got != GenerationScopedFloor {
		t.Fatalf("the memory was written with lifetime %s, want the floor %s", got, GenerationScopedFloor)
	}

	loaded, err = store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loaded.Items[0]
	if snapshot.Status != execution.NoDataMemoryFound || snapshot.MarkerRevision != 1 {
		t.Fatalf("snapshot = %+v, want it found at revision 1", snapshot)
	}
	if snapshot.RosterVersion != "TARGET_STATIC/1" {
		t.Fatalf("roster version = %q, want the one that was written", snapshot.RosterVersion)
	}
	if len(snapshot.Groups) != 1 || snapshot.Groups[0].FirstAbsent != 940 {
		t.Fatalf("groups = %+v, want the one that was written", snapshot.Groups)
	}
	if snapshot.SchemaVersion != execution.NoDataMemorySchemaV1 {
		t.Fatalf("schema = %d, want the one this build writes", snapshot.SchemaVersion)
	}

	// Replaying the same mutation is recognised rather than written twice.
	repeat, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Items[0].Status != execution.NoDataAlreadyApplied {
		t.Fatalf("replay = %+v, want it recognised as already applied", repeat.Items[0])
	}
}

// A record written by a newer build comes back with its version and nothing
// else, and the fields that build added do not turn it into a corrupt record.
//
// Both halves matter. Read in one pass, a strict decoder would fail on the
// unknown field and call the record corrupt - which is a different thing and
// leads somewhere different - and a lenient one would hand back the fields it
// did recognise, which is the guess the load contract exists to refuse.
func TestNoDataMemoryFromANewerBuildIsUnreadableRatherThanCorrupt(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	key, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	future := map[string]any{
		"schema":            executionNoDataSchema,
		"version":           execution.MaxSupportedNoDataMemorySchema + 1,
		"identity":          noDataIdentityV2(),
		"marker_revision":   9,
		"apply_version":     applyVersion(),
		"mutation_digest":   "digest",
		"schedule_revision": "plan-r1",
		"roster_version":    "HISTORY/1",
		"groups":            []execution.NoDataGroupMemory{{GroupKey: "a", FirstAbsent: 940}},
		// The field a newer schema added, which this build has no name for.
		"absent_since_reason": "something this build has never heard of",
	}
	encoded, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = encoded

	loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loaded.Items[0]
	if snapshot.Status != execution.NoDataMemoryUnreadable {
		t.Fatalf("status = %q, want %q: a record from a newer build is not a corrupt one",
			snapshot.Status, execution.NoDataMemoryUnreadable)
	}
	if snapshot.SchemaVersion != execution.MaxSupportedNoDataMemorySchema+1 {
		t.Fatalf("schema = %d, want the version that wrote it kept", snapshot.SchemaVersion)
	}
	if len(snapshot.Groups) != 0 || snapshot.MarkerRevision != 0 || snapshot.RosterVersion != "" {
		t.Fatalf("snapshot = %+v, want nothing but the schema read out of it", snapshot)
	}
}

// A record this build cannot read is not overwritten either. Writing over it
// would destroy state a newer build is still keeping, and the newer build is
// the one that can read it.
func TestNoDataApplyRefusesToOverwriteARecordItCannotRead(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	key, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	future, err := json.Marshal(noDataEnvelope{
		Schema: executionNoDataSchema, Version: execution.MaxSupportedNoDataMemorySchema + 1,
		Identity: noDataIdentityV2(), MarkerRevision: 9, ApplyVersion: applyVersion(),
		MutationDigest: "digest", ScheduleRevision: "plan-r1", RosterVersion: "HISTORY/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = future

	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(),
		Items:    []execution.PlanNoDataMutation{noDataMutationV2(t, 9, execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataRejected {
		t.Fatalf("apply = %+v, want it refused", applied.Items[0])
	}
	if string(backend.values[key]) != string(future) {
		t.Fatal("the record a newer build wrote was overwritten")
	}
}

// Two writers racing on one Plan: the second one's expectation no longer holds
// and it is told so rather than winning.
func TestNoDataApplyConflictsOnAStaleMarkerRevision(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	first := noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940})
	if _, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{first},
	}); err != nil {
		t.Fatal(err)
	}

	// A second writer that still believes the record does not exist. Its apply
	// version differs, so this is the marker check rather than the version one.
	stale, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMutation{
		Identity: noDataIdentityV2(), SchemaVersion: execution.NoDataMemorySchemaV1,
		ExpectedMarkerRevision: 0,
		ApplyVersion: execution.ApplyVersion{
			StateApplyEpoch: applyVersion().StateApplyEpoch + 1,
			EvaluationTime:  applyVersion().EvaluationTime + 60,
			SlotDigest:      applyVersion().SlotDigest,
		},
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		Groups: []execution.NoDataGroupMemory{{GroupKey: "b", LastSeen: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{stale},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataConflict {
		t.Fatalf("apply = %+v, want a conflict: it expected a record that is no longer there",
			applied.Items[0])
	}
}

// The memory has its own key, at the same level as a gap marker and not the
// same one. Sharing would make two different records overwrite each other.
func TestNoDataKeyIsItsOwnKey(t *testing.T) {
	identity := noDataIdentityV2()
	noData, err := PlanNoDataKeyV2("alarmd", identity)
	if err != nil {
		t.Fatal(err)
	}
	gap, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{
		Plan: identity.Plan, StateGeneration: identity.StateGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noData == gap {
		t.Fatalf("the no-data memory and the gap marker share the key %q", noData)
	}
	other, err := PlanNoDataKeyV2("alarmd", execution.PlanNoDataIdentity{
		Plan: identity.Plan, StateGeneration: "generation-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if other == noData {
		t.Fatal("two state generations share one no-data key, so a content change would inherit the old memory")
	}
}

// Loading the memory renews its key the same way loading a gap marker does,
// including when the record is one this build cannot read: the renewal reads
// the header, which is enough, and a paused Plan's memory must not quietly
// expire while the rollback it is waiting out is still going on.
func TestNoDataLoadRenewsTheKeyEvenWhenItCannotReadTheRecord(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
	store := generationStore(t, backend)
	key, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	future, err := json.Marshal(noDataEnvelope{
		Schema: executionNoDataSchema, Version: execution.MaxSupportedNoDataMemorySchema + 1,
		Identity: noDataIdentityV2(), MarkerRevision: 9, ApplyVersion: applyVersion(),
		MutationDigest: "digest", ScheduleRevision: "plan-r1", RosterVersion: "HISTORY/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = future
	backend.remaining[key] = time.Minute

	loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Status != execution.NoDataMemoryUnreadable {
		t.Fatalf("status = %q, want the record to have been recognised as unreadable", loaded.Items[0].Status)
	}
	if len(backend.renewals) != 1 || !backend.renewals[0].Renewed {
		t.Fatalf("renewals = %+v, want the key renewed even though its contents were not read", backend.renewals)
	}
	if backend.remaining[key] != GenerationScopedFloor {
		t.Fatalf("remaining life = %s, want the full lifetime: a paused Plan's memory must outlive the rollback",
			backend.remaining[key])
	}
}

// A record whose bytes are not a record at all is corrupt, which is a different
// answer from unreadable and leads somewhere different: nothing is coming to
// make these bytes readable.
func TestNoDataLoadCallsUnreadableBytesCorrupt(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	key, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	for name, stored := range map[string][]byte{
		"not json":       []byte("{"),
		"another record": []byte(`{"schema":"alarmd-plan-gap-v2","version":1}`),
		"no version":     []byte(`{"schema":"` + executionNoDataSchema + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			backend.values[key] = stored
			loaded, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
				Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
			})
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Items[0].Status != execution.NoDataMemoryTerminal {
				t.Fatalf("status = %q, want it terminal", loaded.Items[0].Status)
			}
		})
	}
}
