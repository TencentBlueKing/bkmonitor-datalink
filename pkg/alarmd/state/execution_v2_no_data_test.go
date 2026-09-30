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
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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

// noDataPresentAsOf is the round the fixtures' Plan last had data in. Every
// fixture group is absent or was last seen before it, so nothing here depends
// on the compressed encoding unless it says so.
const noDataPresentAsOf = int64(1000)

func noDataMutationV2(t *testing.T, expected uint64, groups ...execution.NoDataGroupMemory) execution.PlanNoDataMutation {
	t.Helper()
	// An expected revision of zero is a statement derived from no record;
	// any other is a delta against the per-group record read at applyVersion.
	derivedFrom, loaded := execution.NoDataRepresentationNone, execution.ApplyVersion{}
	if expected != 0 {
		derivedFrom, loaded = execution.NoDataRepresentationPerGroup, applyVersion()
	}
	return noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: derivedFrom, LoadedApplyVersion: loaded,
		Identity: noDataIdentityV2(), ExpectedMarkerRevision: expected, ApplyVersion: applyVersion(),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, Memory: groups,
	})
}

func noDataMutationFrom(t *testing.T, update execution.PlanNoDataMemoryUpdate) execution.PlanNoDataMutation {
	t.Helper()
	mutation, err := execution.BuildPlanNoDataMutation(update)
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

// noDataHashKey is where the memory this build writes lives.
func noDataHashKey(t *testing.T) string {
	t.Helper()
	key, err := PlanNoDataHashKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	return key
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
	key := noDataHashKey(t)
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
	if snapshot.SchemaVersion != execution.WrittenNoDataMemorySchema {
		t.Fatalf("schema = %d, want the one this build writes", snapshot.SchemaVersion)
	}
	if snapshot.PresentAsOf != noDataPresentAsOf {
		t.Fatalf("present as of = %d, want the round the write named", snapshot.PresentAsOf)
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
	key := noDataHashKey(t)
	future := futureHashHeader(t)
	backend.hashes = map[string]map[string][]byte{key: {noDataHeaderField: future}}

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
	if string(backend.hashes[key][noDataHeaderField]) != string(future) {
		t.Fatal("the record a newer build wrote was overwritten")
	}
}

// futureHashHeader is a header in a shape this build has no definition for.
func futureHashHeader(t *testing.T) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schema": executionNoDataSchema, "version": execution.MaxSupportedNoDataMemorySchema + 1,
		"identity": noDataIdentityV2(), "marker_revision": 9, "apply_version": applyVersion(),
		"memory_digest": "digest", "schedule_revision": "plan-r1", "roster_version": "HISTORY/1",
		"present_as_of": noDataPresentAsOf,
		// The field a newer schema added, which this build has no name for.
		"absent_since_reason": "something this build has never heard of",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
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
	stale := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity:    noDataIdentityV2(), ExpectedMarkerRevision: 0,
		ApplyVersion: execution.ApplyVersion{
			StateApplyEpoch: applyVersion().StateApplyEpoch + 1,
			EvaluationTime:  applyVersion().EvaluationTime + 60,
			SlotDigest:      applyVersion().SlotDigest,
		},
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf,
		Memory:      []execution.NoDataGroupMemory{{GroupKey: "b", LastSeen: 1000}},
	})
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{stale},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := applied.Items[0]
	if item.Status != execution.NoDataConflict {
		t.Fatalf("apply = %+v, want a conflict: it expected a record that is no longer there", item)
	}
	// A conflict that says only "conflict" is not actionable: a revision that
	// moved and one statement meeting another of its own version call for
	// different things, and only one of them resolves itself on the next round.
	if item.Conflict == nil || item.Conflict.Kind != execution.StateVersionConflictRevisionMoved {
		t.Fatalf("conflict = %+v, want the revision named as having moved", item.Conflict)
	}
	// The memories, not the statements: a reader comparing a conflict to what
	// is stored is asking which memory is there, and the statement digest of a
	// write that never landed names nothing anyone can look up.
	if item.Conflict.Proposed != stale.MemoryDigest || item.Conflict.Persisted == "" {
		t.Fatalf("conflict = %+v, want both memories named", item.Conflict)
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
	// The two representations coexist for a rollout, so they must be two keys -
	// and two keys the cleanup can tell apart with a glob, because that is how
	// it enumerates the records it is allowed to delete.
	hash, err := PlanNoDataHashKeyV2("alarmd", identity)
	if err != nil {
		t.Fatal(err)
	}
	if hash == noData {
		t.Fatalf("both no-data representations live at %q; one would overwrite the other", hash)
	}
	if !strings.HasPrefix(noData, "alarmd:nodata:v2:") {
		t.Fatalf("the whole-memory key is %q, which the cleanup pattern does not describe", noData)
	}
	if strings.HasPrefix(hash, "alarmd:nodata:v2:") {
		t.Fatalf("the hash key %q matches the cleanup pattern; the cleanup would delete live memory", hash)
	}
}

// A piece of a split strategy has its own gap marker and its own no-data
// memory, apart from its siblings' and apart from the unsplit record.
//
// The three Plan-level keys carry the PlanIdentity, which is the same for
// every piece of a strategy because the identity goes into the event
// identity and cannot carry the split. Without a segment of their own N
// pieces would share one no-data memory and one gap marker, and the sharing
// is not a degradation: each piece would load a roster made of the other
// pieces' groups and report every one of them absent, every round. So the
// piece's matcher digest fills the suffix slot, and the kind changes with it
// so that a glob over the unsplit kind cannot reach a piece's record.
//
// An unsplit Plan's keys do not move: the zero shard produces exactly the
// key it produced before there were shards, which is what keeps every record
// already written where its reader looks.
func TestAPieceOfASplitStrategyHasItsOwnPlanLevelKeys(t *testing.T) {
	unsplit := noDataIdentityV2()
	pieceOf := func(index int, matcher string) execution.PlanNoDataIdentity {
		piece := unsplit
		piece.Shard = execution.ShardRef{Dimension: "bk_target_ip", Index: index, Count: 2, MatcherDigest: string(seriesDigest(matcher))}
		return piece
	}
	first, second := pieceOf(0, "matcher-0"), pieceOf(1, "matcher-1")

	type keyed struct {
		name string
		key  func(execution.PlanNoDataIdentity) (string, error)
		kind string
	}
	for _, record := range []keyed{
		{name: "no-data memory", key: func(id execution.PlanNoDataIdentity) (string, error) { return PlanNoDataKeyV2("alarmd", id) }, kind: "nodata"},
		{name: "no-data hash", key: func(id execution.PlanNoDataIdentity) (string, error) { return PlanNoDataHashKeyV2("alarmd", id) }, kind: "nodata-hash"},
		{name: "gap marker", key: func(id execution.PlanNoDataIdentity) (string, error) {
			return PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: id.Plan, StateGeneration: id.StateGeneration, Shard: id.Shard})
		}, kind: "gap"},
	} {
		t.Run(record.name, func(t *testing.T) {
			whole, err := record.key(unsplit)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(whole, "alarmd:"+record.kind+":v2:") || strings.Count(whole, ":") != 6 {
				t.Fatalf("the unsplit key is %q, want the seven-segment %q key with an empty suffix slot", whole, record.kind)
			}
			one, err := record.key(first)
			if err != nil {
				t.Fatal(err)
			}
			two, err := record.key(second)
			if err != nil {
				t.Fatal(err)
			}
			if one == two {
				t.Fatalf("two pieces of one strategy share the key %q: only one of them would ever write it, "+
					"and each would read the other's groups as absent", one)
			}
			if one == whole || two == whole {
				t.Fatalf("a piece shares the unsplit key %q; the unsplit record would be read as a piece's", whole)
			}
			if !strings.HasPrefix(one, "alarmd:"+record.kind+"-shard:v2:") || strings.Count(one, ":") != 7 {
				t.Fatalf("a piece's key is %q, want the %q kind with the matcher digest in the suffix slot", one, record.kind+"-shard")
			}
			if strings.HasPrefix(one, "alarmd:"+record.kind+":v2:") {
				t.Fatalf("a piece's key %q matches the glob over unsplit records; a cleanup of those would reach it", one)
			}
			// The suffix is the matcher, not the index: a re-split that hands a
			// piece the same index with a different matcher is a different piece
			// with fresh records, and one that keeps the matcher keeps them.
			resplit, err := record.key(pieceOf(0, "matcher-0-resplit"))
			if err != nil {
				t.Fatal(err)
			}
			if resplit == one {
				t.Fatalf("a piece whose matcher changed keeps the key %q, so it would inherit records decided for other series", one)
			}
		})
	}
}

// A piece that does not know its own coordinate cannot name its records, and
// says so by name rather than by producing the unsplit key: a piece written
// under the unsplit key is the sharing the sharded kinds exist to end.
func TestAnIncompleteShardIsRefusedRatherThanKeyedAsUnsplit(t *testing.T) {
	for name, shard := range map[string]execution.ShardRef{
		"no dimension":       {Index: 0, Count: 2, MatcherDigest: string(seriesDigest("m"))},
		"one piece":          {Dimension: "d", Index: 0, Count: 1, MatcherDigest: string(seriesDigest("m"))},
		"index past count":   {Dimension: "d", Index: 2, Count: 2, MatcherDigest: string(seriesDigest("m"))},
		"negative index":     {Dimension: "d", Index: -1, Count: 2, MatcherDigest: string(seriesDigest("m"))},
		"no matcher digest":  {Dimension: "d", Index: 0, Count: 2},
		"matcher not sha256": {Dimension: "d", Index: 0, Count: 2, MatcherDigest: "piece-0"},
	} {
		t.Run(name, func(t *testing.T) {
			identity := noDataIdentityV2()
			identity.Shard = shard
			var identityErr *IdentityError
			if key, err := PlanNoDataHashKeyV2("alarmd", identity); !errors.As(err, &identityErr) {
				t.Fatalf("PlanNoDataHashKeyV2() = %q, %v; want a deterministic identity refusal", key, err)
			}
			if key, err := PlanGapKeyV2("alarmd", execution.PlanGapIdentity{Plan: identity.Plan, StateGeneration: identity.StateGeneration, Shard: shard}); !errors.As(err, &identityErr) {
				t.Fatalf("PlanGapKeyV2() = %q, %v; want a deterministic identity refusal", key, err)
			}
		})
	}
}

// Two pieces of one strategy write their no-data memory in the same round and
// each reads back its own.
//
// This is the failure the sharded kinds exist to prevent, run through the
// store rather than asserted on key strings: under one key the second write
// of a round is a conflict on the first's marker revision, and the loads
// hand each piece the other's groups.
func TestTwoPiecesOfOneStrategyKeepSeparateNoDataMemories(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	pieceOf := func(index int) execution.PlanNoDataIdentity {
		piece := noDataIdentityV2()
		piece.Shard = execution.ShardRef{Dimension: "bk_target_ip", Index: index, Count: 2, MatcherDigest: string(seriesDigest("matcher-" + strconv.Itoa(index)))}
		return piece
	}
	write := func(identity execution.PlanNoDataIdentity, group string) {
		t.Helper()
		mutation := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
			DerivedFrom: execution.NoDataRepresentationNone,
			Identity:    identity, ExpectedMarkerRevision: 0, ApplyVersion: applyVersion(),
			ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
			PresentAsOf: noDataPresentAsOf, Memory: []execution.NoDataGroupMemory{{GroupKey: group, FirstAbsent: 940}},
		})
		applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
			Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
		})
		if err != nil {
			t.Fatal(err)
		}
		if applied.Items[0].Status != execution.NoDataApplied {
			t.Fatalf("piece %d apply = %+v, want it applied: under a shared key the second piece's first write "+
				"conflicts with the first piece's", identity.Shard.Index, applied.Items[0])
		}
	}
	write(pieceOf(0), "ip=192.0.2.1")
	write(pieceOf(1), "ip=192.0.2.2")

	for index, want := range []string{"ip=192.0.2.1", "ip=192.0.2.2"} {
		loaded, err := store.LoadNoData(ctx, execution.NoDataLoadRequest{
			Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{{
				Identity: pieceOf(index), ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		snapshot := loaded.Items[0]
		if snapshot.Status != execution.NoDataMemoryFound || len(snapshot.Groups) != 1 || snapshot.Groups[0].GroupKey != want {
			t.Fatalf("piece %d loaded %+v, want only its own group %q: a piece that loads another's groups "+
				"reports every one of them absent", index, snapshot, want)
		}
	}
	// And the unsplit Plan's memory is untouched by either: nothing was written
	// where a build that does not split would look.
	loaded, err := store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Status != execution.NoDataMemoryMissing {
		t.Fatalf("the unsplit Plan loaded %+v, want nothing: a piece wrote where the unsplit record lives", loaded.Items[0])
	}
}

// Two pieces of one strategy open gap markers in the same round with
// different reasons, and each reads back its own. Under one key the second
// open would be a conflict on the first's marker revision, and a piece
// warming for its own gap would find the other's scopes.
func TestTwoPiecesOfOneStrategyKeepSeparateGapMarkers(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	pieceOf := func(index int) execution.PlanGapIdentity {
		base := noDataIdentityV2()
		return execution.PlanGapIdentity{Plan: base.Plan, StateGeneration: base.StateGeneration, Shard: execution.ShardRef{
			Dimension: "bk_target_ip", Index: index, Count: 2, MatcherDigest: string(seriesDigest("matcher-" + strconv.Itoa(index))),
		}}
	}
	reasons := []execution.ReasonCode{execution.ReasonCode(contract.ReasonHistoryGapped), execution.ReasonCode(contract.ReasonConfigDrift)}
	for index, reason := range reasons {
		opened, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
			Identity: pieceOf(index), ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
			Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{LevelID: 1, HasLevel: true}, Kind: execution.GapOpen,
				ReasonCode: reason, RequiredFullSlots: 2}},
		})
		if err != nil {
			t.Fatal(err)
		}
		applied, err := store.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: frozenRef(), Items: []execution.PlanGapMutation{opened}})
		if err != nil || applied.Items[0].Status != execution.GapGuardApplied {
			t.Fatalf("piece %d ApplyGap(open) = (%+v, %v); under a shared key the second piece's open conflicts with the first's",
				index, applied, err)
		}
	}
	for index, reason := range reasons {
		loaded, err := store.LoadGaps(ctx, execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{
			Identity: pieceOf(index), ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
		}}})
		if err != nil {
			t.Fatal(err)
		}
		marker := loaded.Items[0]
		if marker.Status != execution.GapFound || marker.MarkerRevision != 1 || len(marker.Scopes) != 1 || marker.Scopes[0].ReasonCode != reason {
			t.Fatalf("piece %d loaded %+v, want its own marker at revision 1 with reason %q", index, marker, reason)
		}
	}
	unsplit := noDataIdentityV2()
	loaded, err := store.LoadGaps(ctx, execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{
		Identity: execution.PlanGapIdentity{Plan: unsplit.Plan, StateGeneration: unsplit.StateGeneration}, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Status != execution.GapMissing {
		t.Fatalf("the unsplit Plan loaded %+v, want no marker: a piece wrote where the unsplit marker lives", loaded.Items[0])
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
