// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A sweep reclaims the Assignment record and ownership hash of a Query
// Group the leader no longer runs once nobody holds a live lease on it --
// the ownership hash down to its owner epoch, under the retention --
// leaves one whose lease is still live for a later sweep, never touches a
// Query Group the leader still runs however stale its desired worker, and
// stops under a lost leader fence.
func TestASweepReclaimsRetiredAssignmentsOnlyOnceTheirLeaseHasLapsed(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	const retention = time.Hour
	if err := store.ConfigureEpochRetention(retention); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	place := func(queryGroup execution.QueryGroupIdentity, worker string) {
		if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: worker, PlacementReason: PlacementRendezvous, DecidedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Four Query Groups: one still run, one retired with a lapsed lease, one
	// retired with a live lease, one retired that never had a lease.
	place("qg-live", "worker-old")
	place("qg-lapsed", "worker-old")
	place("qg-held", "worker-1")
	place("qg-never-leased", "worker-old")
	if _, err := store.Acquire(ctx, "qg-lapsed", "worker-old", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	elapseOnRedis(t, store, "qg-lapsed", time.Minute+time.Second)
	if _, err := store.Acquire(ctx, "qg-held", "worker-1", now, time.Minute); err != nil {
		t.Fatal(err)
	}

	keep := map[execution.QueryGroupIdentity]struct{}{"qg-live": {}}
	sweep, err := store.SweepAssignments(ctx, authority, keep)
	if err != nil {
		t.Fatalf("SweepAssignments() error = %v", err)
	}
	if sweep.Scanned != 4 || sweep.Retired != 3 || sweep.Reclaimed != 2 || sweep.HeldByLease != 1 || sweep.Changed != 0 {
		t.Fatalf("sweep = %+v, want 4 scanned, 3 retired, 2 reclaimed (lapsed and never leased), 1 held", sweep)
	}
	for _, gone := range []execution.QueryGroupIdentity{"qg-lapsed", "qg-never-leased"} {
		if _, err := store.ReadAssignment(ctx, gone); !errors.Is(err, ErrAssignmentAbsent) {
			t.Fatalf("ReadAssignment(%s) after the sweep = %v, want absent", gone, err)
		}
	}
	// The lapsed one keeps its owner epoch and nothing else, until the
	// retention runs out; the one never leased had none to keep.
	if left := store.client.HGetAll(ctx, store.ownershipKey("qg-lapsed")).Val(); len(left) != 1 || left["owner_epoch"] != "1" {
		t.Fatalf("ownership hash of qg-lapsed after the sweep = %v, want its owner epoch alone", left)
	}
	if expiry := store.client.PTTL(ctx, store.ownershipKey("qg-lapsed")).Val(); expiry <= retention-time.Minute || expiry > retention {
		t.Fatalf("the kept owner epoch expires in %v, want the retention of %v", expiry, retention)
	}
	if exists := store.client.Exists(ctx, store.ownershipKey("qg-never-leased")).Val(); exists != 0 {
		t.Fatal("a sweep made an ownership hash for a Query Group never leased")
	}
	for _, kept := range []execution.QueryGroupIdentity{"qg-live", "qg-held"} {
		if _, err := store.ReadAssignment(ctx, kept); err != nil {
			t.Fatalf("ReadAssignment(%s) after the sweep = %v, want the record kept", kept, err)
		}
	}
	// The held one goes once its lease lapses.
	elapseOnRedis(t, store, "qg-held", time.Minute+time.Second)
	again, err := store.SweepAssignments(ctx, authority, keep)
	if err != nil || again.Scanned != 2 || again.Retired != 1 || again.Reclaimed != 1 || again.HeldByLease != 0 {
		t.Fatalf("second sweep = (%+v, %v), want the held record reclaimed now", again, err)
	}
	// A leader whose fence has lapsed reclaims nothing.
	elapseOnRedis(t, store, ControlLeaderIdentity, 11*time.Minute)
	if _, err := store.SweepAssignments(ctx, authority, map[execution.QueryGroupIdentity]struct{}{}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("SweepAssignments() under a lapsed leader = %v, want ErrStaleFence", err)
	}
	if _, err := store.ReadAssignment(ctx, "qg-live"); err != nil {
		t.Fatalf("a lapsed leader reclaimed a record: %v", err)
	}
}

// 02 section 6.3 rule 7: Release clears the owner, token and deadline and
// never the owner epoch; the next Acquire counts on from it, and that small
// fact is kept at least until the Query Group's retirement recovery period
// ends, so no later owner is handed an epoch that was already used. A Query
// Group's identity is a digest of its query facts, so a retired one comes
// back when its strategy is enabled again or an edit is reverted. The sweep
// that reclaims its Assignment record leaves the epoch behind, and the
// owner that takes the Query Group back acquires one past the last epoch
// its first life handed out.
func TestASweptQueryGroupThatComesBackAcquiresPastItsLastEpoch(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// A store not told how long to keep the epoch does not sweep.
	if _, err := store.SweepAssignments(ctx, authority, nil); err == nil {
		t.Fatal("a sweep without an owner epoch retention ran")
	}
	if err := store.ConfigureEpochRetention(time.Hour); err != nil {
		t.Fatal(err)
	}
	const queryGroup = execution.QueryGroupIdentity("qg-returning")
	place := func() {
		t.Helper()
		expected := uint64(0)
		if record, err := store.ReadAssignment(ctx, queryGroup); err == nil {
			expected = record.RecordRevision
		}
		if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", ExpectedRecordRevision: expected,
			PlacementReason: PlacementRendezvous, DecidedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	place()
	// Three owners in the first life: restarts and handovers.
	var last Lease
	for round := 0; round < 3; round++ {
		if last, err = store.Acquire(ctx, queryGroup, "worker-1", now, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := store.Release(ctx, last.Fence); err != nil {
			t.Fatal(err)
		}
	}
	sweep, err := store.SweepAssignments(ctx, authority, map[execution.QueryGroupIdentity]struct{}{})
	if err != nil || sweep.Reclaimed != 1 {
		t.Fatalf("sweep = (%+v, %v), want the retired Query Group reclaimed", sweep, err)
	}
	if _, err := store.ReadAssignment(ctx, queryGroup); !errors.Is(err, ErrAssignmentAbsent) {
		t.Fatalf("ReadAssignment() after the sweep = %v, want the record reclaimed", err)
	}

	// It comes back, placed on the worker that last held it. What the sweep
	// kept is no lease, though it carries that owner's epoch: nobody is read
	// as the owner, and the last owner's fence is refused.
	place()
	if owner, found, err := store.ReadQueryGroupOwner(ctx, queryGroup); found || err != nil {
		t.Fatalf("ReadQueryGroupOwner() after the sweep = (%+v, %t, %v), want no owner", owner, found, err)
	}
	if err := store.CheckFence(ctx, last.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(last owner) after the sweep = %v, want ErrStaleFence", err)
	}
	if _, err := store.Renew(ctx, last.Fence, now, time.Minute); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Renew(last owner) after the sweep = %v, want ErrStaleFence", err)
	}
	if err := store.Release(ctx, last.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Release(last owner) after the sweep = %v, want ErrStaleFence", err)
	}
	again, err := store.Acquire(ctx, queryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if again.Fence.OwnerEpoch != last.Fence.OwnerEpoch+1 {
		t.Fatalf("the returning owner holds epoch %d, want %d: one past the last its first life handed out",
			again.Fence.OwnerEpoch, last.Fence.OwnerEpoch+1)
	}
	// The lease cleared the expiry the sweep set: a live owner's hash does
	// not run out under it.
	if expiry := store.client.PTTL(ctx, store.ownershipKey(queryGroup)).Val(); expiry != -1 {
		t.Fatalf("the returning owner's ownership hash expires in %v, want no expiry", expiry)
	}
	if err := store.CheckFence(ctx, again.Fence); err != nil {
		t.Fatalf("CheckFence(returning owner) = %v", err)
	}
}
