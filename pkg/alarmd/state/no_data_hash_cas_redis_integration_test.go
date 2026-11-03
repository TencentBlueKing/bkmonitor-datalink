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
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// noDataServer is one redis-server started for a test, the backend the store
// writes through, and a client of the test's own, so that what the store
// wrote is read back from the server rather than from the store.
//
// The in-memory backend the other no-data tests use restates the script's
// rules in Go, and a restatement that is wrong agrees with itself. Every test
// built on this one has Redis execute the script.
type noDataServer struct {
	instance *redistest.Instance
	backend  *RedisBackend
	client   *redis.Client
}

// startNoDataServer starts a server with the given extra arguments. It does
// not ping through the backend: the backend's ping probes the fence clock with
// a script, and some of these servers are started with scripting disabled.
// redistest.Start has already waited for the server to answer.
func startNoDataServer(t *testing.T, extra ...string) *noDataServer {
	t.Helper()
	instance := redistest.Start(t, extra...)
	backend, err := NewRedisBackend(RedisBackendOptions{
		Address: instance.Addr, DialTimeout: time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	client := redis.NewClient(&redis.Options{Addr: instance.Addr, MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	return &noDataServer{instance: instance, backend: backend, client: client}
}

// store opens an execution store on the server's backend with the bounds the
// fake-backend tests use, except for the value bound, which is the
// deployment's: decision-008 section 0 names 512 KiB as the one bound the
// single-value record had, and it is the configured default.
func (server *noDataServer) store(t *testing.T, configure ...func(*ExecutionStoreOptions)) *ExecutionStore {
	t.Helper()
	return noDataStoreOver(t, server.backend, configure...)
}

func noDataStoreOver(t *testing.T, backend Backend, configure ...func(*ExecutionStoreOptions)) *ExecutionStore {
	t.Helper()
	router, err := NewFixedRouter("redis", backend)
	if err != nil {
		t.Fatal(err)
	}
	options := ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 512 << 10, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	}
	for _, change := range configure {
		change(&options)
	}
	store, err := NewExecutionStore(options)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// record is every field of the fixture Plan's per-group record, as the server
// holds it.
func (server *noDataServer) record(t *testing.T) map[string]string {
	t.Helper()
	fields, err := server.client.HGetAll(context.Background(), noDataHashKey(t)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return fields
}

// header is the fixture Plan's header as the server holds it.
func (server *noDataServer) header(t *testing.T) noDataHashHeader {
	t.Helper()
	raw, err := server.client.HGet(context.Background(), noDataHashKey(t), noDataHeaderField).Bytes()
	if err != nil {
		t.Fatalf("HGET %s: %v", noDataHeaderField, err)
	}
	var header noDataHashHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	return header
}

// storeNoData sends one mutation and returns what the store answered for it.
func storeNoData(
	t *testing.T, store *ExecutionStore, mutation execution.PlanNoDataMutation,
) execution.NoDataApplyItemResult {
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

// firstNoDataWrite is the statement of a round that read no record.
func firstNoDataWrite(t *testing.T, round int64, memory ...execution.NoDataGroupMemory) execution.PlanNoDataMutation {
	t.Helper()
	return noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity:    noDataIdentityV2(), ApplyVersion: coexistenceApplyVersion(round),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, Memory: memory,
	})
}

// deltaNoDataWrite is the statement of a round that read the per-group record
// a round loadedRound wrote, at revision expected, holding loaded.
func deltaNoDataWrite(
	t *testing.T, round int64, expected uint64, loadedRound int64, loaded []execution.NoDataGroupMemory,
	memory ...execution.NoDataGroupMemory,
) execution.PlanNoDataMutation {
	t.Helper()
	return noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: coexistenceApplyVersion(loadedRound),
		Identity: noDataIdentityV2(), ExpectedMarkerRevision: expected, ApplyVersion: coexistenceApplyVersion(round),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, Memory: memory, Loaded: loaded, LoadedPresentAsOf: noDataPresentAsOf,
	})
}

// absentGroup is a group the fixture Plan last saw before it went quiet.
func absentGroup(key string) execution.NoDataGroupMemory {
	return execution.NoDataGroupMemory{GroupKey: key, LastSeen: 900, FirstAbsent: 940}
}

// interceptedBackend is a real backend with one step run immediately before
// the script: what another writer does between this write's read of the
// header and the script that proves the header is unchanged.
type interceptedBackend struct {
	*RedisBackend
	beforeScript func()
}

func (backend *interceptedBackend) ApplyHashDelta(
	ctx context.Context, write HashDeltaWrite,
) (HashDeltaOutcome, error) {
	if backend.beforeScript != nil {
		backend.beforeScript()
	}
	return backend.RedisBackend.ApplyHashDelta(ctx, write)
}

// A write sent again is recognised as already applied, on a real server, in
// the three shapes decision-008 section 7 item 3 names: sent again without a
// new read, sent again after a read of the record the first attempt wrote, and
// sent again by a round whose read predates its own landed write.
//
// The statement's identity is (apply version, memory digest), compared before
// the revision (decision-008 section 2 step 2 and section 6). Each replay
// leaves the record byte for byte as the first write left it: the revision
// does not move, because nothing was written.
func TestAReplayedNoDataWriteIsAlreadyAppliedOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)

	first := firstNoDataWrite(t, 0, absentGroup("a"))
	if got := storeNoData(t, store, first); got.Status != execution.NoDataApplied {
		t.Fatalf("first write = %+v, want it applied", got)
	}
	written := server.record(t)
	if header := server.header(t); header.MarkerRevision != 1 {
		t.Fatalf("first write left revision %d, want 1", header.MarkerRevision)
	}

	// Sent again, nothing re-read: the half-committed Slot's retry.
	if got := storeNoData(t, store, first); got.Status != execution.NoDataAlreadyApplied || got.Conflict != nil {
		t.Fatalf("the same statement sent again = %+v, want ALREADY_APPLIED", got)
	}
	if after := server.record(t); !reflect.DeepEqual(after, written) {
		t.Fatalf("a replay rewrote the record:\nbefore %v\nafter  %v", written, after)
	}

	// Re-read first. The retry now reads the record its own first attempt
	// wrote and derives against it: a different statement on the wire, the
	// same memory. Two paths to one memory share one digest (section 6), so
	// this is the write already applied and not a conflict.
	loaded := loadOneNoDataMemory(t, store)
	if loaded.Status != execution.NoDataMemoryFound || loaded.MarkerRevision != 1 {
		t.Fatalf("the retry's read = %+v, want the first write at revision 1", loaded)
	}
	reread := deltaNoDataWrite(t, 0, loaded.MarkerRevision, 0, loaded.Groups, absentGroup("a"))
	if reread.MemoryDigest != first.MemoryDigest {
		t.Fatalf("setup: the re-derived statement names memory %s, the first %s; one memory must have one digest",
			reread.MemoryDigest, first.MemoryDigest)
	}
	if got := storeNoData(t, store, reread); got.Status != execution.NoDataAlreadyApplied {
		t.Fatalf("the re-derived statement = %+v, want ALREADY_APPLIED", got)
	}
	if after := server.record(t); !reflect.DeepEqual(after, written) {
		t.Fatalf("a re-derived replay rewrote the record:\nbefore %v\nafter  %v", written, after)
	}

	// A later round lands, and is then sent again by a retry whose read still
	// expects the revision before it. The revision no longer matches; the
	// statement does, and it is compared first.
	second := deltaNoDataWrite(t, 1, 1, 0, loaded.Groups, absentGroup("a"), absentGroup("b"))
	if got := storeNoData(t, store, second); got.Status != execution.NoDataApplied {
		t.Fatalf("second round = %+v, want it applied", got)
	}
	landed := server.record(t)
	if header := server.header(t); header.MarkerRevision != 2 {
		t.Fatalf("second round left revision %d, want 2", header.MarkerRevision)
	}
	if got := storeNoData(t, store, second); got.Status != execution.NoDataAlreadyApplied || got.Conflict != nil {
		t.Fatalf("the second round sent again while expecting revision 1 of a record at 2 = %+v, "+
			"want ALREADY_APPLIED: the statement is compared before the revision", got)
	}
	if after := server.record(t); !reflect.DeepEqual(after, landed) {
		t.Fatalf("the stale-revision replay rewrote the record:\nbefore %v\nafter  %v", landed, after)
	}
}

// A write from a round older than the one stored is STALE_VERSION on a real
// server, whatever revision it expects, and the record is left as it was.
//
// decision-008 section 2 step 2 orders the comparisons: same statement, then a
// newer stored version, then the revision. So a late delta whose revision no
// longer matches is stale rather than a conflict -- the version is asked about
// first -- and a late first write meets a record it did not expect and is
// stale for the same reason.
func TestALateWriteFromAnOlderRoundIsStaleOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)

	if got := storeNoData(t, store, firstNoDataWrite(t, 1, absentGroup("a"))); got.Status != execution.NoDataApplied {
		t.Fatalf("round 1 = %+v, want it applied", got)
	}
	loaded := loadOneNoDataMemory(t, store)
	if got := storeNoData(t, store,
		deltaNoDataWrite(t, 2, 1, 1, loaded.Groups, absentGroup("a"), absentGroup("b"))); got.Status != execution.NoDataApplied {
		t.Fatalf("round 2 = %+v, want it applied", got)
	}
	stored := server.record(t)

	for _, late := range []struct {
		name      string
		statement execution.PlanNoDataMutation
	}{
		{name: "a first write from round 0", statement: firstNoDataWrite(t, 0, absentGroup("z"))},
		{
			name:      "a delta from round 1, expecting the revision round 1 wrote",
			statement: deltaNoDataWrite(t, 1, 1, 1, loaded.Groups, absentGroup("a"), absentGroup("c")),
		},
	} {
		t.Run(late.name, func(t *testing.T) {
			got := storeNoData(t, store, late.statement)
			if got.Status != execution.NoDataStale {
				t.Fatalf("late write = %+v, want STALE_VERSION: a newer round is stored", got)
			}
			if got.Conflict != nil {
				t.Fatalf("a stale write carried conflict facts %+v; it lost on the version, not a comparison of memories",
					got.Conflict)
			}
			if after := server.record(t); !reflect.DeepEqual(after, stored) {
				t.Fatalf("a stale write changed the record:\nbefore %v\nafter  %v", stored, after)
			}
		})
	}
}

// A write that read a record which is no longer there is CONFLICT with kind
// MISSING on a real server, and writes nothing.
//
// decision-008 section 2 step 1: no header and an expected revision other than
// zero is a conflict, "the record is gone". The record can go three ways: the
// key expired or was deleted, the header alone was removed and orphan groups
// remain, or either happened between the write's own read of the header and
// its script. The first two are decided before the script, the third by it,
// and all three have to name the same thing.
func TestAWriteExpectingARecordThatIsGoneConflictsAsMissingOnARealServer(t *testing.T) {
	for _, vanish := range []struct {
		name string
		// before runs after the Slot's load and before its write; during runs
		// inside the write, after its read of the header.
		before, during func(t *testing.T, server *noDataServer)
		// leftover is what the key holds after the refused write.
		leftover func(t *testing.T) map[string]string
	}{
		{
			name: "the key expired before the write",
			before: func(t *testing.T, server *noDataServer) {
				server.client.Del(context.Background(), noDataHashKey(t))
			},
			leftover: func(*testing.T) map[string]string { return map[string]string{} },
		},
		{
			name: "the header was removed and the groups remain",
			before: func(t *testing.T, server *noDataServer) {
				server.client.HDel(context.Background(), noDataHashKey(t), noDataHeaderField)
			},
			leftover: func(*testing.T) map[string]string {
				return map[string]string{noDataGroupPrefix + "a": `{"last_seen":900,"first_absent":940}`}
			},
		},
		{
			name: "the key went between the write's read and its script",
			during: func(t *testing.T, server *noDataServer) {
				server.client.Del(context.Background(), noDataHashKey(t))
			},
			leftover: func(*testing.T) map[string]string { return map[string]string{} },
		},
	} {
		t.Run(vanish.name, func(t *testing.T) {
			server := startNoDataServer(t)
			intercepted := &interceptedBackend{RedisBackend: server.backend}
			store := noDataStoreOver(t, intercepted)
			if got := storeNoData(t, store, firstNoDataWrite(t, 0, absentGroup("a"))); got.Status != execution.NoDataApplied {
				t.Fatalf("first write = %+v", got)
			}
			loaded := loadOneNoDataMemory(t, store)
			if loaded.Status != execution.NoDataMemoryFound || loaded.MarkerRevision != 1 {
				t.Fatalf("the Slot's read = %+v, want the record found at revision 1", loaded)
			}
			if vanish.before != nil {
				vanish.before(t, server)
			}
			if vanish.during != nil {
				intercepted.beforeScript = func() { vanish.during(t, server) }
			}
			delta := deltaNoDataWrite(t, 1, loaded.MarkerRevision, 0, loaded.Groups, absentGroup("a"), absentGroup("b"))
			got := storeNoData(t, store, delta)
			if got.Status != execution.NoDataConflict || got.Conflict == nil {
				t.Fatalf("write = %+v, want a conflict: the record it was derived from is gone", got)
			}
			want := execution.NoDataConflictFacts{
				Kind: execution.StateVersionConflictMissing, Persisted: "", Proposed: delta.MemoryDigest,
				ExpectedRevision: 1, StoredRevision: 0, DerivedFrom: execution.NoDataRepresentationPerGroup,
			}
			if *got.Conflict != want {
				t.Fatalf("conflict = %+v, want %+v", *got.Conflict, want)
			}
			// Nothing written. A delta applied to an absent record would be a
			// record holding only the groups this round changed.
			if after, wantLeft := server.record(t), vanish.leftover(t); !reflect.DeepEqual(after, wantLeft) {
				t.Fatalf("the refused write left %v, want %v", after, wantLeft)
			}
		})
	}
}

// A record deleted and recreated below the revision a write expects is
// CONFLICT with kind REVISION_RESET on a real server, on both the path that
// decides before the script and the one the script decides.
//
// decision-008 section 6: a delta is only meaningful against the revision it
// was derived from, so a mismatch is a conflict; section 9 names the kinds --
// "moved" is somebody else having written since, "reset" is the key deleted
// and created again, which is a different incident and the one a reader goes
// looking for. The record is recreated by a round older than the writer, so
// the version comparison passes and the revision is what decides.
func TestARecordRecreatedBelowTheExpectedRevisionConflictsAsAResetOnARealServer(t *testing.T) {
	for _, path := range []string{"before the script", "inside the script"} {
		t.Run(path, func(t *testing.T) {
			server := startNoDataServer(t)
			intercepted := &interceptedBackend{RedisBackend: server.backend}
			store := noDataStoreOver(t, intercepted)
			// A second writer with its own store, as another worker would be.
			other := server.store(t)

			if got := storeNoData(t, store, firstNoDataWrite(t, 0, absentGroup("a"))); got.Status != execution.NoDataApplied {
				t.Fatalf("round 0 = %+v", got)
			}
			first := loadOneNoDataMemory(t, store)
			if got := storeNoData(t, store,
				deltaNoDataWrite(t, 1, 1, 0, first.Groups, absentGroup("a"), absentGroup("b"))); got.Status != execution.NoDataApplied {
				t.Fatalf("round 1 = %+v", got)
			}
			loaded := loadOneNoDataMemory(t, store)
			if loaded.MarkerRevision != 2 {
				t.Fatalf("the Slot read revision %d, want 2", loaded.MarkerRevision)
			}
			recreated := firstNoDataWrite(t, 2, absentGroup("c"))
			recreate := func() {
				server.client.Del(context.Background(), noDataHashKey(t))
				if got := storeNoData(t, other, recreated); got.Status != execution.NoDataApplied {
					t.Fatalf("the recreating write = %+v", got)
				}
			}
			if path == "before the script" {
				recreate()
			} else {
				intercepted.beforeScript = recreate
			}

			delta := deltaNoDataWrite(t, 3, loaded.MarkerRevision, 1, loaded.Groups,
				absentGroup("a"), absentGroup("b"), absentGroup("d"))
			got := storeNoData(t, store, delta)
			if got.Status != execution.NoDataConflict || got.Conflict == nil {
				t.Fatalf("write = %+v, want a conflict against the recreated record", got)
			}
			want := execution.NoDataConflictFacts{
				Kind: execution.StateVersionConflictRevisionReset, Persisted: recreated.MemoryDigest,
				Proposed: delta.MemoryDigest, ExpectedRevision: 2, StoredRevision: 1,
				DerivedFrom: execution.NoDataRepresentationPerGroup,
			}
			if *got.Conflict != want {
				t.Fatalf("conflict = %+v, want %+v", *got.Conflict, want)
			}
			if header := server.header(t); header.MarkerRevision != 1 || header.MemoryDigest != recreated.MemoryDigest {
				t.Fatalf("the refused write moved the recreated record to %+v", header)
			}
		})
	}
}

// Two statements of one apply version that state different memories are
// CONFLICT with kind SAME_VERSION_OTHER_STATEMENT on a real server, whichever
// revision the second one expects.
//
// Not stale -- nothing is newer -- and not applied -- the memories differ.
// decision-018 section 1.1 names the case: a retry that wrote a different
// statement under the same version is "versions equal, statements different",
// which is why the Slot time and not the wall clock stamps the tracking facts.
// The version is compared before the revision (decision-008 section 2), so a
// second statement that expects the stored revision meets the same answer as
// one that expects none.
func TestTwoStatementsOfOneVersionConflictByNameOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := server.store(t)
	first := firstNoDataWrite(t, 0, absentGroup("a"))
	if got := storeNoData(t, store, first); got.Status != execution.NoDataApplied {
		t.Fatalf("first statement = %+v", got)
	}
	stored := server.record(t)
	loaded := loadOneNoDataMemory(t, store)

	for _, second := range []struct {
		name      string
		statement execution.PlanNoDataMutation
		expected  uint64
	}{
		{name: "derived from no record", statement: firstNoDataWrite(t, 0, absentGroup("b")), expected: 0},
		{
			name:      "derived from the stored record",
			statement: deltaNoDataWrite(t, 0, 1, 0, loaded.Groups, absentGroup("a"), absentGroup("b")),
			expected:  1,
		},
	} {
		t.Run(second.name, func(t *testing.T) {
			got := storeNoData(t, store, second.statement)
			if got.Status != execution.NoDataConflict || got.Conflict == nil {
				t.Fatalf("second statement = %+v, want a conflict", got)
			}
			want := execution.NoDataConflictFacts{
				Kind: execution.StateVersionConflictSameVersionOtherStatement, Persisted: first.MemoryDigest,
				Proposed: second.statement.MemoryDigest, ExpectedRevision: second.expected, StoredRevision: 1,
				DerivedFrom: second.statement.DerivedFrom,
			}
			if *got.Conflict != want {
				t.Fatalf("conflict = %+v, want %+v", *got.Conflict, want)
			}
			if after := server.record(t); !reflect.DeepEqual(after, stored) {
				t.Fatalf("the conflicting statement changed the record:\nbefore %v\nafter  %v", stored, after)
			}
		})
	}
}

// A store that does not answer is RETRYABLE with REDIS_UNAVAILABLE, for each
// call that can fail, and a load of it is UNAVAILABLE with the same reason.
//
// decision-008 section 7.5 item 5 reads RETRYABLE_IO as following the Redis
// side, so it has to be what each Redis call's failure produces. The script
// failing is the call no test reached: here it is a server with scripting
// renamed away, which answers the header read and refuses the script. Nothing
// is written in any of them.
func TestAStoreThatDoesNotAnswerIsRetryableOnARealServer(t *testing.T) {
	retryable := func(t *testing.T, got execution.NoDataApplyItemResult) {
		t.Helper()
		if got.Status != execution.NoDataRetryable ||
			got.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
			t.Fatalf("write = %+v, want RETRYABLE with %s", got, contract.ReasonRedisUnavailable)
		}
	}
	unavailable := func(t *testing.T, got execution.NoDataMemorySnapshot) {
		t.Helper()
		if got.Status != execution.NoDataMemoryUnavailable ||
			got.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) || len(got.Groups) != 0 {
			t.Fatalf("load = %+v, want UNAVAILABLE with %s and nothing read", got, contract.ReasonRedisUnavailable)
		}
	}

	t.Run("the script is refused after the header was read", func(t *testing.T) {
		server := startNoDataServer(t, "--rename-command", "EVAL", "", "--rename-command", "EVALSHA", "")
		store := server.store(t)
		retryable(t, storeNoData(t, store, firstNoDataWrite(t, 0, absentGroup("a"))))
		if exists := server.client.Exists(context.Background(), noDataHashKey(t)).Val(); exists != 0 {
			t.Fatal("a write whose script was refused left a record")
		}
	})

	t.Run("the server is gone", func(t *testing.T) {
		server := startNoDataServer(t)
		store := server.store(t)
		server.instance.Stop()
		retryable(t, storeNoData(t, store, firstNoDataWrite(t, 0, absentGroup("a"))))
		unavailable(t, loadOneNoDataMemory(t, store))
	})

	t.Run("the route to the server fails", func(t *testing.T) {
		store, err := NewExecutionStore(ExecutionStoreOptions{
			Prefix: "alarmd", Router: refusingRouter{}, MaxValueBytes: 512 << 10, MaxItemsPerCall: 4,
			MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		retryable(t, storeNoData(t, store, firstNoDataWrite(t, 0, absentGroup("a"))))
		unavailable(t, loadOneNoDataMemory(t, store))
	})
}
