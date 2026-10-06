// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// One index round writes the sets asked for, refreshes the ones declared
// unchanged, reports the ones it found missing, drops workers that left,
// keeps the round monotonic, and refuses a stale authority outright.
func TestRedisStoreAssignmentIndexRoundTrip(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	if _, err := store.ReadAssignmentIndex(ctx); !errors.Is(err, ErrAssignmentIndexAbsent) {
		t.Fatalf("ReadAssignmentIndex() before any round error = %v, want absent", err)
	}
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	groups := func(ids ...string) []execution.QueryGroupIdentity {
		out := make([]execution.QueryGroupIdentity, 0, len(ids))
		for _, id := range ids {
			out = append(out, execution.QueryGroupIdentity(id))
		}
		return out
	}
	first, err := store.PublishAssignmentIndex(ctx, authority, now, []AssignedSetWrite{
		{WorkerID: "worker-1", QueryGroups: groups("qg-2", "qg-1"), Rewrite: true},
		{WorkerID: "worker-2", QueryGroups: nil, Rewrite: true},
	})
	if err != nil || first.Round != 1 || len(first.Missing) != 0 {
		t.Fatalf("first round = %+v, error %v; want round 1 with nothing missing", first, err)
	}
	index, err := store.ReadAssignmentIndex(ctx)
	if err != nil || index.Round != 1 || index.ControlEpoch != authority.Fence.OwnerEpoch ||
		!reflect.DeepEqual(index.SetRounds, map[string]uint64{"worker-1": 1, "worker-2": 1}) {
		t.Fatalf("index after first round = %+v, error %v", index, err)
	}
	set, err := store.ReadAssignedSet(ctx, "worker-1")
	if err != nil || set.Round != 1 || !reflect.DeepEqual(set.QueryGroups, groups("qg-1", "qg-2")) {
		t.Fatalf("worker-1 set = %+v, error %v; want round 1 with the sorted Query Groups", set, err)
	}
	if set, err := store.ReadAssignedSet(ctx, "worker-2"); err != nil || set.Round != 1 || len(set.QueryGroups) != 0 {
		t.Fatalf("worker-2 set = %+v, error %v; want an empty round-1 set", set, err)
	}
	if _, err := store.ReadAssignedSet(ctx, "worker-3"); !errors.Is(err, ErrAssignedSetAbsent) {
		t.Fatalf("ReadAssignedSet(unknown) error = %v, want absent", err)
	}

	// An unchanged round bumps the round number, keeps the set rounds and
	// refreshes the lifetime of every set it was told about.
	second, err := store.PublishAssignmentIndex(ctx, authority, now.Add(time.Second), []AssignedSetWrite{
		{WorkerID: "worker-1", QueryGroups: groups("qg-1", "qg-2")},
		{WorkerID: "worker-2"},
	})
	if err != nil || second.Round != 2 || len(second.Missing) != 0 {
		t.Fatalf("second round = %+v, error %v; want round 2 with nothing missing", second, err)
	}
	index, err = store.ReadAssignmentIndex(ctx)
	if err != nil || index.Round != 2 || !reflect.DeepEqual(index.SetRounds, map[string]uint64{"worker-1": 1, "worker-2": 1}) {
		t.Fatalf("index after unchanged round = %+v, error %v", index, err)
	}
	if ttl := store.client.PTTL(ctx, store.assignedSetKey("worker-1")).Val(); ttl <= 0 || ttl > assignedSetTTL {
		t.Fatalf("worker-1 set lifetime = %v, want refreshed within %v", ttl, assignedSetTTL)
	}

	// A set that vanished is reported, not silently re-declared present;
	// the follow-up rewrite repairs it at the new round.
	if err := store.client.Del(ctx, store.assignedSetKey("worker-2")).Err(); err != nil {
		t.Fatal(err)
	}
	third, err := store.PublishAssignmentIndex(ctx, authority, now.Add(2*time.Second), []AssignedSetWrite{
		{WorkerID: "worker-1"}, {WorkerID: "worker-2"},
	})
	if err != nil || third.Round != 3 || !reflect.DeepEqual(third.Missing, []string{"worker-2"}) {
		t.Fatalf("round with a vanished set = %+v, error %v; want worker-2 reported missing", third, err)
	}
	fourth, err := store.PublishAssignmentIndex(ctx, authority, now.Add(3*time.Second), []AssignedSetWrite{
		{WorkerID: "worker-1"}, {WorkerID: "worker-2", QueryGroups: groups("qg-3"), Rewrite: true},
	})
	if err != nil || fourth.Round != 4 || len(fourth.Missing) != 0 {
		t.Fatalf("repair round = %+v, error %v", fourth, err)
	}
	index, err = store.ReadAssignmentIndex(ctx)
	if err != nil || !reflect.DeepEqual(index.SetRounds, map[string]uint64{"worker-1": 1, "worker-2": 4}) {
		t.Fatalf("index after repair = %+v, error %v", index, err)
	}

	// A worker that left the ready set loses its index entry.
	if _, err := store.PublishAssignmentIndex(ctx, authority, now.Add(4*time.Second), []AssignedSetWrite{{WorkerID: "worker-1"}}); err != nil {
		t.Fatal(err)
	}
	index, err = store.ReadAssignmentIndex(ctx)
	if err != nil || index.Round != 5 || !reflect.DeepEqual(index.SetRounds, map[string]uint64{"worker-1": 1}) {
		t.Fatalf("index after worker-2 left = %+v, error %v", index, err)
	}

	// A stale authority writes nothing.
	stale := authority
	stale.Fence.LeaseToken = "not-the-lease"
	if _, err := store.PublishAssignmentIndex(ctx, stale, now.Add(5*time.Second), []AssignedSetWrite{{WorkerID: "worker-1"}}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("PublishAssignmentIndex(stale) error = %v, want ErrStaleFence", err)
	}
	if index, err := store.ReadAssignmentIndex(ctx); err != nil || index.Round != 5 {
		t.Fatalf("index after a stale write = %+v, error %v; want round 5 untouched", index, err)
	}
	if _, err := store.PublishAssignmentIndex(ctx, authority, now, []AssignedSetWrite{{WorkerID: "worker-1"}, {WorkerID: "worker-1"}}); err == nil {
		t.Fatal("a round naming the same worker twice was accepted")
	}
}

func TestAssignedSetDigestIgnoresOrderAndSeparatesIdentities(t *testing.T) {
	left := AssignedSetDigest([]execution.QueryGroupIdentity{"b", "a"})
	right := AssignedSetDigest([]execution.QueryGroupIdentity{"a", "b"})
	if left != right {
		t.Fatal("digest depends on order")
	}
	if AssignedSetDigest([]execution.QueryGroupIdentity{"ab", "c"}) == AssignedSetDigest([]execution.QueryGroupIdentity{"a", "bc"}) {
		t.Fatal("digest does not separate identities")
	}
	if AssignedSetDigest(nil) == AssignedSetDigest([]execution.QueryGroupIdentity{"a"}) {
		t.Fatal("digest of an empty set equals a one-element set")
	}
}
