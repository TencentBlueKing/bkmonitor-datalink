// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// 02 section 6.3 rule 7 keeps a Query Group's owner epoch at least until its
// retirement recovery period ends, so no later owner is handed an epoch that
// was already used. The pool record outlives the Query Group by a week and
// refuses a write from an owner older than the one that wrote it. A Query
// Group's identity is a digest of its query facts, so a retired one comes
// back -- its strategy enabled again, an edit reverted -- and its owner must
// still write its own pool record: an owner epoch that started over at 1
// below the record's would have every entry and exit refused, and each
// takeover would read the first life's record back.
func TestASweptQueryGroupThatComesBackWritesItsPoolRecord(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	store, err := newProductionOwnershipStore(config.Default(), client)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "leader", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const queryGroup = execution.QueryGroupIdentity("qg-returning")
	place := func() {
		t.Helper()
		expected := uint64(0)
		if record, err := store.ReadAssignment(ctx, queryGroup); err == nil {
			expected = record.RecordRevision
		}
		if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", ExpectedRecordRevision: expected,
			PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	place()
	// Three owners in the first life; the last one enters the pool.
	var lease ownership.Lease
	for round := 0; round < 3; round++ {
		if lease, err = store.Acquire(ctx, queryGroup, "worker-1", time.Now(), time.Minute); err != nil {
			t.Fatal(err)
		}
		if round < 2 {
			if err := store.Release(ctx, lease.Fence); err != nil {
				t.Fatal(err)
			}
		}
	}
	cooldowns := newRedisQueryCooldownStore(client, "test.cooldown", nil, nil, nil)
	if err := cooldowns.SaveQueryCooldown(ctx, lease.Fence, scheduler.QueryCooldownRecord{
		QueryGroup: queryGroup, OwnerEpoch: lease.Fence.OwnerEpoch, EnteredAt: now, Until: now.Add(5 * time.Minute),
		Failures: 3, QueryRevision: "q1",
	}); err != nil {
		t.Fatal(err)
	}
	// Retired: the holder lets it go and the leader sweeps it.
	if err := store.Release(ctx, lease.Fence); err != nil {
		t.Fatal(err)
	}
	if sweep, err := store.SweepAssignments(ctx, authority, map[execution.QueryGroupIdentity]struct{}{}); err != nil || sweep.Reclaimed != 1 {
		t.Fatalf("sweep = (%+v, %v), want the retired Query Group reclaimed", sweep, err)
	}
	// The epoch the sweep keeps outlives the pool record it orders.
	epochLeft := client.PTTL(ctx, store.FenceKeys(queryGroup).OwnershipKey).Val()
	recordLeft := client.PTTL(ctx, scheduler.QueryCooldownKey("test.cooldown", queryGroup)).Val()
	if epochLeft <= 0 || recordLeft <= 0 || epochLeft < recordLeft {
		t.Fatalf("the swept epoch expires in %v and the pool record in %v, want the epoch to outlive the record", epochLeft, recordLeft)
	}

	// It comes back, and its owner leaves the pool.
	place()
	again, err := store.Acquire(ctx, queryGroup, "worker-1", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := cooldowns.SaveQueryCooldown(ctx, again.Fence, scheduler.QueryCooldownRecord{
		QueryGroup: queryGroup, OwnerEpoch: again.Fence.OwnerEpoch, ExitedAt: time.Now(), ExitReason: "recovered",
	}); err != nil {
		t.Fatalf("the returning owner (epoch %d) cannot write its pool record, written by epoch %d in the first life: %v",
			again.Fence.OwnerEpoch, lease.Fence.OwnerEpoch, err)
	}
	record, found, err := cooldowns.LoadQueryCooldown(ctx, queryGroup)
	if err != nil || !found || record.OwnerEpoch != again.Fence.OwnerEpoch || record.ExitReason != "recovered" {
		t.Fatalf("pool record = (%+v, %t, %v), want the returning owner's exit", record, found, err)
	}
}
