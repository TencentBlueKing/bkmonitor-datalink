// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

func newTestStore(t *testing.T, lease time.Duration) *Store {
	t.Helper()
	store := NewStore(redistest.Start(t).Client(), "test.sre:{env}:")
	store.lease = lease
	return store
}

func testRecord(id string) Record {
	return Record{ID: id, Namespace: "ns", Name: "app", UID: "uid-1", Container: "app", Previous: "app:1", Image: "app:2", State: StatePending}
}

// Only the holder of a live lease writes a receipt. A released lease is
// taken at once by another process, and from then on the first holder's
// writes are refused: a process that lost a receipt stops moving it.
func TestOnlyTheHolderOfALiveLeaseWritesAReceipt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, time.Minute)
	record := testRecord("r1")
	if err := store.Create(ctx, record, "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, record, "pod-b/1"); err == nil {
		t.Fatal("a receipt was created twice")
	}
	if _, taken, err := store.Take(ctx, "r1", "pod-b/1"); err != nil || taken {
		t.Fatalf("a held receipt was taken: %v %v", taken, err)
	}
	record.State = StateRollingOut
	if err := store.Put(ctx, record, "pod-a/1"); err != nil {
		t.Fatalf("the holder's write: %v", err)
	}
	if err := store.Put(ctx, record, "pod-b/1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("another process's write = %v, want refused", err)
	}
	if err := store.Release(ctx, "r1", "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	taken, ok, err := store.Take(ctx, "r1", "pod-b/1")
	if err != nil || !ok || taken.State != StateRollingOut {
		t.Fatalf("take after release = %+v %v %v, want the receipt as written", taken, ok, err)
	}
	if err := store.Renew(ctx, "r1", "pod-a/1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("the first holder's renewal after a takeover = %v, want refused", err)
	}
	entry, err := store.Get(ctx, "r1")
	if err != nil || entry.DrivenBy != "pod-b/1" || entry.LeaseExpired() {
		t.Fatalf("entry %+v %v, want held by pod-b", entry, err)
	}
}

// A lease nobody renews runs out by Redis's clock, and the receipt can then
// be taken.
func TestAnUnrenewedLeaseRunsOut(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, 200*time.Millisecond)
	if err := store.Create(ctx, testRecord("r1"), "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Take(ctx, "r1", "pod-b/1"); ok {
		t.Fatal("taken before the lease ran out")
	}
	time.Sleep(400 * time.Millisecond)
	if entry, _ := store.Get(ctx, "r1"); !entry.LeaseExpired() {
		t.Fatalf("lease %v not expired at %v", entry.LeaseUntil, entry.Now)
	}
	if _, ok, err := store.Take(ctx, "r1", "pod-b/1"); err != nil || !ok {
		t.Fatalf("take after the lease ran out = %v %v", ok, err)
	}
}

// A receipt in progress is listed as active until it reaches a final
// state; the Deployment's latest receipt is readable by its name; a receipt
// that no longer exists leaves the active list when someone tries to take
// it.
func TestAFinalReceiptLeavesTheActiveListAndTheLatestIsReadableByName(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, time.Minute)
	for _, id := range []string{"r1", "r2"} {
		if err := store.Create(ctx, testRecord(id), "pod-a/1"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if active, _ := store.Active(ctx); len(active) != 2 {
		t.Fatalf("active %v, want both", active)
	}
	final := testRecord("r1")
	final.State = StateComplete
	if err := store.Put(ctx, final, "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	if active, _ := store.Active(ctx); len(active) != 1 || active[0] != "r2" {
		t.Fatalf("active %v, want only r2", active)
	}
	if err := store.Put(ctx, final, "pod-a/1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a write after the final one = %v, want refused", err)
	}
	if latest, err := store.Latest(ctx, "ns", "app"); err != nil || latest.Record.ID != "r2" {
		t.Fatalf("latest %+v %v, want r2", latest.Record, err)
	}
	store.client.Del(ctx, store.recordKey("r2"))
	if _, ok, err := store.Take(ctx, "r2", "pod-b/1"); ok || err != nil {
		t.Fatalf("take of a missing receipt = %v %v", ok, err)
	}
	if active, _ := store.Active(ctx); len(active) != 0 {
		t.Fatalf("active %v, want the missing receipt gone", active)
	}
	if _, err := store.Get(ctx, "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get of a missing receipt = %v", err)
	}
}

// A final receipt left in the in-progress set is never taken over again: the
// take drops it from the set instead.
func TestAFinalReceiptIsNeverTakenAgain(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, time.Minute)
	final := testRecord("r1")
	if err := store.Create(ctx, final, "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	final.State = StateComplete
	if err := store.Put(ctx, final, "pod-a/1"); err != nil {
		t.Fatal(err)
	}
	store.client.SAdd(ctx, store.activeKey(), "r1")
	if _, ok, err := store.Take(ctx, "r1", "pod-b/1"); ok || err != nil {
		t.Fatalf("a final receipt was taken: %v %v", ok, err)
	}
	if active, _ := store.Active(ctx); len(active) != 0 {
		t.Fatalf("active %v, want the final receipt dropped", active)
	}
}
