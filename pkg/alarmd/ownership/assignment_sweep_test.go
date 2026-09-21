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
// Group the leader no longer runs once nobody holds a live lease on it,
// leaves one whose lease is still live for a later sweep, never touches a
// Query Group the leader still runs however stale its desired worker, and
// stops under a lost leader fence.
func TestASweepReclaimsRetiredAssignmentsOnlyOnceTheirLeaseHasLapsed(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
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
		if exists := store.client.Exists(ctx, store.ownershipKey(gone)).Val(); exists != 0 {
			t.Fatalf("ownership hash of %s survived the sweep", gone)
		}
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
