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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// readOnlyBackend has the reads and none of the writes. Nothing in production
// is shaped like this today - RedisBackend is the only implementation and it
// has everything - which is exactly why this exists: the label for "the routed
// backend cannot do this" would otherwise be one nothing can ever produce, and
// a label with no producer reads the same as a label that stayed at zero.
type readOnlyBackend struct {
	values map[string][]byte
}

func (backend *readOnlyBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	result := make([][]byte, len(keys))
	for index, key := range keys {
		if value, ok := backend.values[key]; ok {
			result[index] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (backend *readOnlyBackend) SetMany(context.Context, []BackendWrite) error { return nil }

// capabilityStore opens a store whose router lists a capable backend and
// routes to the given one, so a test can reach the point-of-use assertion
// behind the probe at open.
func capabilityStore(t *testing.T, backend Backend) *ExecutionStore {
	t.Helper()
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: listedCapableRouter{routed: backend}, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A backend that cannot do what a write needs is a wiring defect, and it has to
// say so. It used to arrive as REDIS_UNAVAILABLE, which is wrong twice over:
// the reader is sent to look at a Redis that is fine, and the retryable class
// means the work is retried forever against a backend whose answer will not
// change.
func TestAMissingBackendCapabilityIsNotReportedAsTheStoreBeingDown(t *testing.T) {
	store := capabilityStore(t, &readOnlyBackend{values: map[string][]byte{}})
	ctx := context.Background()
	want := execution.ReasonCode(contract.ReasonBackendCapabilityMissing)

	gap, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity:     execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"},
		ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
		Scopes: []execution.GapScopeMutation{{
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode("GAP_SKIPPED"), RequiredFullSlots: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gapResult, err := store.ApplyGap(ctx, execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{gap},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := gapResult.Items[0]; got.Status != execution.GapGuardRejected || got.ReasonCode != want {
		t.Fatalf("gap apply = %+v, want a deterministic %s", got, want)
	}

	noData := noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940})
	noDataResult, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{noData},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := noDataResult.Items[0]; got.Status != execution.NoDataRejected || got.ReasonCode != want {
		t.Fatalf("no-data apply = %+v, want a deterministic %s", got, want)
	}
}

// The runtime state path answers the same way. It reaches the backend through
// its own assertion, so it is its own branch and its own chance to report a
// wiring defect as the store being down - which is what it did, and what it
// would go back to doing with one line changed.
func TestAMissingBackendCapabilityIsNotTheStoreBeingDownForRuntimeState(t *testing.T) {
	store := capabilityStore(t, &readOnlyBackend{values: map[string][]byte{}})
	result, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{
		Contract:  frozenRef(),
		Retention: ttlTestRetention(5, time.Minute, 0),
		Items:     seriesMutations(t, 1, applyVersion(), 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Items[0]
	if got.Status != execution.StateApplyDeterministicInvalid ||
		got.ReasonCode != execution.ReasonCode(contract.ReasonBackendCapabilityMissing) {
		t.Fatalf("apply = %+v, want a deterministic %s: retrying reaches the same backend",
			got, contract.ReasonBackendCapabilityMissing)
	}
}

// The same answer from the read path, where the missing capability is the
// renewal rather than the write.
func TestAMissingRenewalCapabilityReportsTheSameReason(t *testing.T) {
	backend := &nonRenewingBackend{values: map[string][]byte{}}
	store := capabilityStore(t, backend)
	want := execution.ReasonCode(contract.ReasonBackendCapabilityMissing)

	gapItem := gapLoadItem("generation", nil)
	gapKey, err := PlanGapKeyV2("alarmd", gapItem.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[gapKey] = validGapRecord(t, gapItem.Identity)
	gapResult, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{gapItem},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := gapResult.Items[0]; got.Status != execution.GapTerminal || got.ReasonCode != want {
		t.Fatalf("gap load = %+v, want a terminal %s", got, want)
	}

	noDataKey, err := PlanNoDataKeyV2("alarmd", noDataIdentityV2())
	if err != nil {
		t.Fatal(err)
	}
	backend.values[noDataKey] = []byte("{}")
	noDataResult, err := store.LoadNoData(context.Background(), execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := noDataResult.Items[0]; got.Status != execution.NoDataMemoryTerminal || got.ReasonCode != want {
		t.Fatalf("no-data load = %+v, want a terminal %s", got, want)
	}
}

// A routing failure keeps the retryable reading it always had. The two used to
// share one branch, and separating them is the point: one is worth retrying and
// the other never will be.
func TestARoutingFailureStillReadsAsTheStoreBeingUnavailable(t *testing.T) {
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: refusingRouter{}, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutation := noDataMutationV2(t, 0, execution.NoDataGroupMemory{GroupKey: "a", LastSeen: 940})
	result, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Items[0]
	if got.Status != execution.NoDataRetryable ||
		got.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("apply = %+v, want a retryable %s", got, contract.ReasonRedisUnavailable)
	}
}

type refusingRouter struct{}

func (refusingRouter) Route(string, string) (StorageTarget, error) {
	return StorageTarget{}, context.DeadlineExceeded
}

// It lists a target it can never route to: the store opens, and every route
// fails the way an unreachable Redis does.
func (refusingRouter) Targets() []StorageTarget {
	return []StorageTarget{{Name: "unreachable", Backend: &casMemoryBackend{values: map[string][]byte{}}}}
}

// listedCapableRouter lists one backend and routes to another. It is how the
// tests below reach the assertions at the points of use: the probe at open
// sees a capable target, the write or read then meets an incapable one. That
// is the defense path -- a router routing to something other than what it
// listed -- and it has to keep answering with the named, deterministic reason
// rather than with "the store is down".
type listedCapableRouter struct {
	routed Backend
}

func (router listedCapableRouter) Route(string, string) (StorageTarget, error) {
	return StorageTarget{Name: "target", Backend: router.routed}, nil
}

func (listedCapableRouter) Targets() []StorageTarget {
	return []StorageTarget{{Name: "target", Backend: &casMemoryBackend{values: map[string][]byte{}}}}
}
