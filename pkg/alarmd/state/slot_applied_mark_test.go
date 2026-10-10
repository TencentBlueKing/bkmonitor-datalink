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
	"os/exec"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// recordingSlotAppliedBackend keeps what the store asked of it: the members
// and the lifetime, so a test can read the lifetime the store derived.
type recordingSlotAppliedBackend struct {
	casMemoryBackend
	members map[string][]string
	ttl     map[string]time.Duration
}

func (backend *recordingSlotAppliedBackend) AddSlotApplied(_ context.Context, key string, members []string, ttl time.Duration) error {
	if backend.members == nil {
		backend.members, backend.ttl = map[string][]string{}, map[string]time.Duration{}
	}
	backend.members[key] = append(backend.members[key], members...)
	backend.ttl[key] = ttl
	return nil
}

func (backend *recordingSlotAppliedBackend) ReadSlotApplied(_ context.Context, key string) ([]string, error) {
	return backend.members[key], nil
}

type oneTargetRouter struct{ backend Backend }

func (router oneTargetRouter) Route(string, string) (StorageTarget, error) {
	return StorageTarget{Name: "target", Backend: router.backend}, nil
}

func (router oneTargetRouter) Targets() []StorageTarget {
	return []StorageTarget{{Name: "target", Backend: router.backend}}
}

func slotAppliedPlans() (execution.SlotIdentity, []execution.PlanIdentity) {
	slot := execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_060}
	plans := []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}}
	return slot, plans
}

// The mark lives to the Slot's keep-until, which is where its reader ends,
// and not to recovery-until, which is where its reader begins.
//
// The query-free finalization reads the mark once the replay window has
// closed: at recovery-until or after it. A lifetime ending at recovery-until
// had the key expire at the moment the reader arrived, so a Slot that had
// evaluated and alerted read as NONE_FOUND and was recorded as a gap. The
// keep-until is the scheduler's own bound on how late that finalization can
// run -- recovery-until plus the post-recovery terminal delay -- and nothing
// reads the Slot after it.
func TestTheMarkLivesToTheSlotsKeepUntilNotToItsRecoveryUntil(t *testing.T) {
	backend := &recordingSlotAppliedBackend{casMemoryBackend: casMemoryBackend{values: map[string][]byte{}}}
	store, err := NewSlotAppliedMarkStore("alarmd", oneTargetRouter{backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	slot, plans := slotAppliedPlans()
	now := time.Unix(1_788_000_100, 0)
	keepUntil := now.Add(17 * time.Minute)
	if err := store.Record(context.Background(), slot, plans, plans, keepUntil, now); err != nil {
		t.Fatal(err)
	}
	key, _ := SlotAppliedKey("alarmd", slot)
	if got := backend.ttl[key]; got != 17*time.Minute {
		t.Fatalf("the mark was written with lifetime %s, want the %s to the Slot's keep-until: a shorter one "+
			"is gone before the finalization that reads it runs", got, 17*time.Minute)
	}
	// A Slot already past its keep-until writes nothing: nothing will read it.
	if err := store.Record(context.Background(), slot, plans, plans, now.Add(-time.Second), now); err != nil {
		t.Fatal(err)
	}
	if got := backend.ttl[key]; got != 17*time.Minute {
		t.Fatalf("a Slot past its keep-until rewrote the mark with lifetime %s", got)
	}
}

// On a real store, the mark written before the recovery boundary is still
// read as STATE_APPLIED after it -- the reading the finalization makes.
func TestOnRedisTheMarkIsStillReadAfterTheRecoveryBoundary(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{Address: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	store, err := NewSlotAppliedMarkStore("alarmd", oneTargetRouter{backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	slot, plans := slotAppliedPlans()
	ctx := context.Background()
	// The boundaries as the scheduler derives them: the replay window closes
	// one second from now, and the Slot is kept for a terminal delay past it.
	now := time.Now()
	recoveryUntil := now.Add(time.Second)
	keepUntil := recoveryUntil.Add(10 * time.Second)
	if err := store.Record(ctx, slot, plans, plans, keepUntil, now); err != nil {
		t.Fatal(err)
	}
	before, err := store.Read(ctx, slot, plans)
	if err != nil || before.Kind != execution.EvidenceStateApplied || before.PlansApplied != 1 {
		t.Fatalf("before the boundary: evidence = %+v, err = %v, want STATE_APPLIED 1/1", before, err)
	}
	// The finalization arrives after the boundary.
	time.Sleep(time.Until(recoveryUntil) + 500*time.Millisecond)
	after, err := store.Read(ctx, slot, plans)
	if err != nil || after.Kind != execution.EvidenceStateApplied || after.PlansApplied != 1 {
		t.Fatalf("after the boundary: evidence = %+v, err = %v, want STATE_APPLIED 1/1: this is when the "+
			"query-free finalization reads the mark, and a mark that ended at the boundary was never "+
			"there to be read", after, err)
	}
}
