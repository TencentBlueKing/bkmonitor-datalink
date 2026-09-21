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

func generationStore(t *testing.T, backend *casMemoryBackend) *ExecutionStore {
	t.Helper()
	router, err := NewFixedRouter("target", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func gapLoadItem(generation execution.StateGeneration, retention []execution.StateRetentionRequirement) execution.PlanGapLoadItem {
	return execution.PlanGapLoadItem{
		Identity:         execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: generation},
		ApplyVersion:     applyVersion(),
		ScheduleRevision: "plan-r1",
		Retention:        retention,
	}
}

// The lifetime is the runtime state's own when that is longer than the floor,
// and the floor otherwise. The floor is where a key whose window is minutes
// long gets its life from, which is almost every Plan; the other branch is for
// a window that reaches back further than a day, where a key expiring at the
// floor would outlive nothing and take the memory of an absence with it.
func TestGenerationScopedTTLTakesTheLongerOfRetentionAndTheFloor(t *testing.T) {
	short := []LevelRequirement{{LevelID: 1, RetentionPoints: 5, EvaluationInterval: time.Minute}}
	long := []LevelRequirement{{LevelID: 1, RetentionPoints: 10080, EvaluationInterval: time.Minute}}

	got, err := GenerationScopedTTL(short, time.Minute, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got != GenerationScopedFloor {
		t.Fatalf("a five minute window gave %s, want the floor %s", got, GenerationScopedFloor)
	}

	got, err = GenerationScopedTTL(long, time.Minute, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got <= GenerationScopedFloor {
		t.Fatalf("a seven day window gave %s, want longer than the floor %s", got, GenerationScopedFloor)
	}
	want, err := StateTTL(long, time.Minute, time.Minute, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("a seven day window gave %s, want the runtime lifetime %s", got, want)
	}

	// No retention stated is the floor, not an error and not nothing: a key with
	// no lifetime is the state this exists to end.
	got, err = GenerationScopedTTL(nil, time.Minute, time.Minute, 30*24*time.Hour)
	if err != nil || got != GenerationScopedFloor {
		t.Fatalf("GenerationScopedTTL(nil) = %s, %v; want the floor", got, err)
	}
}

// The script writes a new expiry only when the remaining life is below half.
//
// This is about what the script decides once it is reached, which is why each
// case gets its own store: the ask gate remembers keys per store, and a shared
// one would answer the second case from the first case's ask. What it costs to
// reach the script is a separate question, and the gate's own tests are where
// it is asked.
func TestGenerationKeyIsRenewedOnlyWhenItsLifeIsRunningOut(t *testing.T) {
	item := gapLoadItem("generation", nil)
	key, err := PlanGapKeyV2("alarmd", item.Identity)
	if err != nil {
		t.Fatal(err)
	}

	for name, test := range map[string]struct {
		remaining time.Duration
		renewed   bool
	}{
		"most of its life left": {remaining: GenerationScopedFloor - time.Minute, renewed: false},
		"exactly half left":     {remaining: GenerationScopedFloor / 2, renewed: false},
		"under half left":       {remaining: GenerationScopedFloor/2 - time.Minute, renewed: true},
		"nearly gone":           {remaining: time.Minute, renewed: true},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
			store := generationStore(t, backend)
			backend.values[key] = []byte("{}")
			backend.remaining[key] = test.remaining
			if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
				Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
			}); err != nil {
				t.Fatal(err)
			}
			if len(backend.renewals) != 1 {
				t.Fatalf("renewal calls = %d, want exactly one per loaded key", len(backend.renewals))
			}
			if backend.renewals[0].Renewed != test.renewed {
				t.Fatalf("with %s left the key was renewed = %t, want %t",
					test.remaining, backend.renewals[0].Renewed, test.renewed)
			}
			if backend.renewals[0].TTL != GenerationScopedFloor {
				t.Fatalf("renewed to %s, want the derived lifetime %s",
					backend.renewals[0].TTL, GenerationScopedFloor)
			}
		})
	}
}

// A key that exists with no expiry is renewed. Those are the keys written
// before lifetimes existed, and they are the reason this cannot simply trust
// that every key already has one: the ones still in use repair themselves the
// next time they are loaded, and only the ones nothing loads any more are left
// over for a one-off sweep.
func TestGenerationKeyWithNoExpiryIsRenewed(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	item := gapLoadItem("generation", nil)
	key, err := PlanGapKeyV2("alarmd", item.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = []byte("{}")

	if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
	}); err != nil {
		t.Fatal(err)
	}
	if len(backend.renewals) != 1 || !backend.renewals[0].Renewed {
		t.Fatalf("renewals = %+v, want the key with no expiry to have been given one", backend.renewals)
	}
}

// A load renews whether or not anything is written afterwards, which is the
// whole reason renewal is on this path. A Plan whose memory has not changed
// writes nothing, so a write-driven renewal would let a stable long-running
// state - the record that most needs to survive - be the one that expires.
func TestGenerationKeyIsRenewedByALoadThatWritesNothing(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
	store := generationStore(t, backend)
	item := gapLoadItem("generation", nil)
	key, err := PlanGapKeyV2("alarmd", item.Identity)
	if err != nil {
		t.Fatal(err)
	}
	backend.values[key] = []byte("{}")
	backend.remaining[key] = time.Minute

	if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
	}); err != nil {
		t.Fatal(err)
	}
	if len(backend.renewals) != 1 || !backend.renewals[0].Renewed {
		t.Fatalf("renewals = %+v, want the load alone to have renewed the key", backend.renewals)
	}
	if backend.remaining[key] != GenerationScopedFloor {
		t.Fatalf("remaining life = %s, want the full lifetime %s", backend.remaining[key], GenerationScopedFloor)
	}
}

// A key nobody loads is a key nobody renews, which is how an old generation's
// key ages out. Time cannot be made to pass here, so what is asserted is the
// reachability: the old generation's key is not among the keys a new
// generation's load touches.
func TestAnOldGenerationKeyIsNotTouchedByANewGenerationLoad(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte), remaining: make(map[string]time.Duration)}
	store := generationStore(t, backend)

	old := gapLoadItem("generation-old", nil)
	fresh := gapLoadItem("generation-new", nil)
	oldKey, err := PlanGapKeyV2("alarmd", old.Identity)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := PlanGapKeyV2("alarmd", fresh.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if oldKey == newKey {
		t.Fatal("fixture: the two generations share a key, so this proves nothing")
	}
	backend.values[oldKey] = []byte("{}")
	backend.values[newKey] = []byte("{}")
	backend.remaining[oldKey] = time.Minute
	backend.remaining[newKey] = time.Minute

	if _, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{fresh},
	}); err != nil {
		t.Fatal(err)
	}
	for _, call := range backend.renewals {
		if call.Key == oldKey {
			t.Fatal("a load of the new generation renewed the old generation's key, which would keep it alive forever")
		}
	}
	if backend.remaining[oldKey] != time.Minute {
		t.Fatalf("the old generation's remaining life moved to %s", backend.remaining[oldKey])
	}
}

// A backend that cannot renew fails the load rather than loading and skipping
// the renewal. Skipping would put every key back to never expiring, and the
// only place that shows up is a Redis instance months later.
//
// The store no longer opens on such a backend at all -- see
// TestAnExecutionStoreRefusesToOpenOnATargetLackingACapability -- so this
// reaches the load through a router that listed a capable target and routed
// to this one. That is the defense behind the probe, and it has to hold.
func TestALoadRefusesABackendThatCannotRenew(t *testing.T) {
	backend := &nonRenewingBackend{values: map[string][]byte{}}
	store, err := NewExecutionStore(ExecutionStoreOptions{
		Prefix: "alarmd", Router: listedCapableRouter{routed: backend}, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := gapLoadItem("generation", nil)
	key, err := PlanGapKeyV2("alarmd", item.Identity)
	if err != nil {
		t.Fatal(err)
	}
	// A record that decodes cleanly, so the only thing that can make this
	// terminal is the backend. An unreadable value would reach the same status
	// by being corrupt, and the test would pass with the renewal deleted - which
	// is what a mutation run found when this fixture held "{}".
	backend.values[key] = validGapRecord(t, item.Identity)

	result, err := store.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Status != execution.GapTerminal {
		t.Fatalf("result = %+v, want a terminal status: a backend that cannot renew is a wiring defect, "+
			"not a store having a bad minute", result.Items)
	}

	// The same record through a backend that can renew reads normally, which is
	// what makes the line above about the backend and not about the record.
	renewing := &casMemoryBackend{values: map[string][]byte{key: backend.values[key]}}
	readable := generationStore(t, renewing)
	control, err := readable.LoadGaps(context.Background(), execution.GapLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanGapLoadItem{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Items) != 1 || control.Items[0].Status == execution.GapTerminal {
		t.Fatalf("control = %+v, want the record to read cleanly when the backend can renew", control.Items)
	}
}

// validGapRecord is a stored gap that decodes and validates, so a test can put
// the failure somewhere other than the bytes.
func validGapRecord(t *testing.T, identity execution.PlanGapIdentity) []byte {
	t.Helper()
	encoded, err := json.Marshal(gapEnvelope{
		Schema: executionGapSchemaV2, Identity: identity, MarkerRevision: 1,
		ApplyVersion: applyVersion(), MutationDigest: "digest", ScheduleRevision: "plan-r1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A generation-scoped key is never written without a lifetime. The load renews
// it to whatever the Plan needs, but a key written in a Plan's last Slot is
// never loaded again - and with no lifetime at birth, that one is immortal.
func TestAGenerationKeyIsNeverWrittenWithoutALifetime(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	key, err := PlanGapKeyV2("alarmd", identity)
	if err != nil {
		t.Fatal(err)
	}

	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1",
		Scopes: []execution.GapScopeMutation{{
			Kind: execution.GapOpen, ReasonCode: execution.ReasonCode("GAP_SKIPPED"), RequiredFullSlots: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanGapMutation{mutation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Status != execution.GapGuardApplied {
		t.Fatalf("apply = %+v, want it applied", result.Items)
	}
	if got := backend.writeTTLs[key]; got != GenerationScopedFloor {
		t.Fatalf("the key was written with lifetime %s, want the floor %s; a key written without one "+
			"and never loaded again never expires", got, GenerationScopedFloor)
	}
}

type nonRenewingBackend struct {
	values map[string][]byte
}

func (backend *nonRenewingBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	result := make([][]byte, len(keys))
	for index, key := range keys {
		if value, ok := backend.values[key]; ok {
			result[index] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (backend *nonRenewingBackend) SetMany(context.Context, []BackendWrite) error { return nil }

func (backend *nonRenewingBackend) CompareAndSet(
	_ context.Context, key string, _ []byte, _ bool, value []byte, _ time.Duration,
) (bool, error) {
	backend.values[key] = append([]byte(nil), value...)
	return true, nil
}
