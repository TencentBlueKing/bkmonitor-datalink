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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The memory a Plan of real size holds is written, where the whole-memory
// representation refused it.
//
// This is the defect the representation change exists to remove, so it is
// pinned as a behaviour rather than left implied by the absence of a bound:
// roughly three thousand groups, each carrying the two clocks that appear
// exactly when absence is being detected, is what an object on the deployment
// holds, and under one encoded value it was past half a megabyte and refused
// every round - silently, because a Plan whose memory is refused goes on
// evaluating and only stops remembering.
func TestAMemoryTooLargeForOneValueIsWritten(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	groups := make([]execution.NoDataGroupMemory, 0, 3200)
	for index := 0; index < 3200; index++ {
		groups = append(groups, execution.NoDataGroupMemory{
			GroupKey:    fmt.Sprintf("component=flink,data_set_id=%d_clustered,__NO_DATA_DIMENSION__=true", index),
			LastSeen:    940,
			FirstAbsent: 980,
		})
	}
	mutation := noDataMutationV2(t, 0, groups...)
	encoded, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= store.options.MaxValueBytes {
		t.Fatalf("fixture: the memory encodes to %d bytes, inside the %d-byte bound one value had; "+
			"this test is not exercising the size that used to be refused",
			len(encoded), store.options.MaxValueBytes)
	}
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want the memory stored: one field per group has no size bound",
			applied.Items[0])
	}
	loaded, err := store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded.Items[0].Groups); got != len(groups) {
		t.Fatalf("read back %d groups, want all %d", got, len(groups))
	}
}

// The one bound left is on how many groups a memory holds, and a refusal on it
// says the count and the bound.
//
// Without the numbers the refusal is not actionable: a Plan a little over the
// guard is one whose expected set grew, and one many times over is a roster
// derivation that has run away, and the reason code alone reads the same for
// both.
func TestNoDataApplyGroupRefusalCarriesBothNumbers(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := boundedGroupStore(t, backend, 64)
	ctx := context.Background()

	groups := make([]execution.NoDataGroupMemory, 0, 65)
	for index := 0; index < 65; index++ {
		groups = append(groups, execution.NoDataGroupMemory{
			GroupKey: fmt.Sprintf("data_set_id=%d", index), LastSeen: 940, FirstAbsent: 980,
		})
	}
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, groups...)},
	})
	if err != nil {
		t.Fatal(err)
	}
	item := applied.Items[0]
	if item.Status != execution.NoDataRejected ||
		item.ReasonCode != execution.ReasonCode(contract.ReasonStateBudgetExceeded) {
		t.Fatalf("apply = %+v, want a deterministic budget refusal", item)
	}
	if item.Size == nil {
		t.Fatal("a bound refusal carried no measurement; STATE_BUDGET_EXCEEDED on its own is not actionable")
	}
	if item.Size.Record != execution.NoDataRecordGroups {
		t.Fatalf("measured record = %q, want the group count", item.Size.Record)
	}
	if item.Size.Groups != 65 || item.Size.Limit != 64 {
		t.Fatalf("measurement = %+v, want 65 groups against a bound of 64", item.Size)
	}
	// Nothing was written. The refusal is the whole outcome, and a partial
	// write would leave a record whose revision no later mutation expects.
	if len(backend.hashes[noDataHashKey(t)]) != 0 {
		t.Fatal("a refused write left a record behind")
	}
}

// boundedGroupStore is a store whose group guard is small enough for a test to
// reach. The production default is two orders of magnitude above what any Plan
// holds, which is what makes it a guard; a test that built 100,001 groups would
// be measuring the test.
func boundedGroupStore(t *testing.T, backend *casMemoryBackend, groups int) *ExecutionStore {
	t.Helper()
	router, err := NewFixedRouter("target", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
		MaxNoDataGroups: groups,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A write goes to the per-group record and leaves the whole-memory one exactly
// as it found it.
//
// That asymmetry is the coexistence rule, and it is what makes the old record
// safe to enumerate and delete later: nothing this build writes can put a Plan
// back on it. A build that wrote both would leave two records per Plan with no
// rule for which is the memory.
func TestApplyWritesTheHashAndNeverTheWholeMemoryRecord(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	blobKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	// A whole-memory record an older build left behind, large enough that the
	// write path would have refused it had it still been reading it.
	backend.values[blobKey] = make([]byte, 5000)

	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(),
		Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0,
			execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want the write to have gone through to the hash", applied.Items[0])
	}
	if len(backend.values[blobKey]) != 5000 {
		t.Fatal("the write touched the whole-memory record, which this build only reads")
	}
	if len(backend.hashes[noDataHashKey(t)]) == 0 {
		t.Fatal("the write did not reach the per-group record")
	}
}

// Every shape a deterministic refusal takes is in the list the metric label is
// bounded by.
//
// The list is published from execution and read by the metric to pre-create
// its series and to bound its cardinality. A refusal shape the store can
// produce and the list does not hold would be a series nobody pre-created and
// a bound that is not one, so the check has to be against the store's actual
// output rather than against a second copy of the list.
func TestEveryNoDataRefusalShapeIsPublished(t *testing.T) {
	published := make(map[execution.NoDataRefusal]bool, len(execution.NoDataRefusals))
	for _, refusal := range execution.NoDataRefusals {
		published[refusal] = true
	}

	oneGroup := execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940}

	for _, test := range []struct {
		name  string
		build func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation)
	}{
		{
			name: "the memory holds more groups than the guard allows",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				groups := make([]execution.NoDataGroupMemory, 0, 3)
				for index := 0; index < 3; index++ {
					groups = append(groups, execution.NoDataGroupMemory{
						GroupKey: fmt.Sprintf("data_set_id=%d", index), LastSeen: 940, FirstAbsent: 980,
					})
				}
				return boundedGroupStore(t, &casMemoryBackend{values: make(map[string][]byte)}, 2),
					noDataMutationV2(t, 0, groups...)
			},
		},
		{
			name: "the mutation does not match its own digest",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				mutation := noDataMutationV2(t, 0, oneGroup)
				mutation.MutationDigest = "not-the-digest"
				return generationStore(t, &casMemoryBackend{values: make(map[string][]byte)}), mutation
			},
		},
		{
			name: "the stored record is in a shape this build cannot read",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				backend := &casMemoryBackend{values: make(map[string][]byte), hashes: map[string]map[string][]byte{
					noDataHashKey(t): {noDataHeaderField: futureHashHeader(t)},
				}}
				return generationStore(t, backend), noDataMutationV2(t, 9, oneGroup)
			},
		},
		{
			name: "the backend cannot hold a hash",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				return capabilityStore(t, &readOnlyBackend{values: map[string][]byte{}}),
					noDataMutationV2(t, 0, oneGroup)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mutation := test.build(t)
			applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
				Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
			})
			if err != nil {
				t.Fatal(err)
			}
			item := applied.Items[0]
			if item.Status != execution.NoDataRejected {
				t.Fatalf("apply = %+v, want a deterministic refusal", item)
			}
			shape := execution.NoDataRefusal{Reason: item.ReasonCode}
			if item.Size != nil {
				shape.Record = item.Size.Record
			}
			if !published[shape] {
				t.Fatalf("the store produced refusal %+v, which execution.NoDataRefusals does not hold: "+
					"the metric pre-creates its series and bounds its cardinality from that list", shape)
			}
		})
	}
}

// unreadableBackend answers every read with a transport failure, which is the
// one apply outcome that cannot be reached by arranging the stored record.
type unreadableBackend struct{}

func (*unreadableBackend) MGet(context.Context, []string) ([][]byte, error) {
	return nil, errors.New("state: the store did not answer")
}

func (*unreadableBackend) SetMany(context.Context, []BackendWrite) error { return nil }

func (*unreadableBackend) CompareAndSet(
	context.Context, string, []byte, bool, []byte, time.Duration,
) (bool, error) {
	return false, errors.New("state: the store did not answer")
}

func (*unreadableBackend) RenewIfBelow(context.Context, string, time.Duration, time.Duration) (RenewalOutcome, error) {
	return "", errors.New("state: the store did not answer")
}

func (*unreadableBackend) RenewManyIfBelow(
	context.Context, []string, time.Duration, time.Duration,
) ([]RenewalOutcome, error) {
	return nil, errors.New("state: the store did not answer")
}

func (*unreadableBackend) ReadHash(context.Context, string) (map[string][]byte, error) {
	return nil, errors.New("state: the store did not answer")
}

func (*unreadableBackend) ReadHashField(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("state: the store did not answer")
}

func (*unreadableBackend) ApplyHashDelta(context.Context, HashDeltaWrite) (HashDeltaOutcome, error) {
	return HashDeltaOutcome{}, errors.New("state: the store did not answer")
}

// Every status the store can return is in the list the two observation
// families are built from.
//
// The lists are how a reader gets a computed zero rather than an absent
// series, and how the metric's cardinality is bounded. A status the store can
// return that no list holds is a series nobody pre-created, a bound that is
// not one, and an outcome the Slot reports without anybody having decided
// whether it means the memory was kept -- so the check has to be against what
// the store actually answers, not against a second copy of the list.
func TestEveryNoDataApplyStatusTheStoreReturnsIsPublished(t *testing.T) {
	published := make(map[execution.NoDataApplyStatus]bool, len(execution.NoDataApplyStatuses))
	for _, status := range execution.NoDataApplyStatuses {
		published[status] = true
	}

	oneGroup := execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940}

	for _, test := range []struct {
		name  string
		build func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation)
		want  execution.NoDataApplyStatus
	}{
		{
			name: "a first write",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				return generationStore(t, &casMemoryBackend{values: make(map[string][]byte)}),
					noDataMutationV2(t, 0, oneGroup)
			},
			want: execution.NoDataApplied,
		},
		{
			name: "the same write twice",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				store := generationStore(t, &casMemoryBackend{values: make(map[string][]byte)})
				mutation := noDataMutationV2(t, 0, oneGroup)
				if _, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
					Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
				}); err != nil {
					t.Fatal(err)
				}
				return store, mutation
			},
			want: execution.NoDataAlreadyApplied,
		},
		{
			name: "a write whose expected revision no longer holds",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				backend := &casMemoryBackend{values: make(map[string][]byte)}
				store := generationStore(t, backend)
				if _, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
					Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, oneGroup)},
				}); err != nil {
					t.Fatal(err)
				}
				// Same version, different content: the record the store holds
				// is not the one this mutation expects to be replacing.
				return store, noDataMutationV2(t, 7,
					execution.NoDataGroupMemory{GroupKey: "b", FirstAbsent: 941})
			},
			want: execution.NoDataConflict,
		},
		{
			name: "a write an older round is sending late",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				backend := &casMemoryBackend{values: make(map[string][]byte)}
				store := generationStore(t, backend)
				if _, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
					Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noDataMutationV2(t, 0, oneGroup)},
				}); err != nil {
					t.Fatal(err)
				}
				return store, olderNoDataMutation(t, oneGroup)
			},
			want: execution.NoDataStale,
		},
		{
			name: "the store did not answer",
			build: func(t *testing.T) (*ExecutionStore, execution.PlanNoDataMutation) {
				return capabilityStore(t, &unreadableBackend{}), noDataMutationV2(t, 0, oneGroup)
			},
			want: execution.NoDataRetryable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, mutation := test.build(t)
			applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
				Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
			})
			if err != nil {
				t.Fatal(err)
			}
			status := applied.Items[0].Status
			if !published[status] {
				t.Fatalf("the store returned %s, which execution.NoDataApplyStatuses does not hold: "+
					"the metric pre-creates its series and bounds its cardinality from that list", status)
			}
			if status != test.want {
				t.Fatalf("status = %s, want %s; this fixture is not reaching the outcome it names",
					status, test.want)
			}
		})
	}
}

// olderNoDataMutation is a write from a round before the one already stored.
func olderNoDataMutation(t *testing.T, groups ...execution.NoDataGroupMemory) execution.PlanNoDataMutation {
	t.Helper()
	older := applyVersion()
	older.EvaluationTime--
	return noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity:    noDataIdentityV2(), ExpectedMarkerRevision: 0, ApplyVersion: older,
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, Memory: groups,
	})
}
