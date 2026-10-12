// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

func TestRedisStorePublishesAssignmentOnlyWithLiveControlLeader(t *testing.T) {
	store := newIntegrationStore(t)
	now := time.Now().Truncate(time.Millisecond)
	worker := WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: "capabilities-v1", ExpiresAt: now.Add(time.Minute),
	}
	if err := store.RegisterWorker(context.Background(), worker); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	ready, _, err := store.ListReadyWorkers(context.Background(), now)
	if err != nil || len(ready) != 1 || ready[0].WorkerID != worker.WorkerID ||
		ready[0].Compatibility() != worker.Compatibility() || ready[0].DependencyStatus != worker.DependencyStatus {
		t.Fatalf("ListReadyWorkers() = (%+v, %v)", ready, err)
	}

	authority, err := store.AcquireControlLeader(context.Background(), "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	record, err := store.PublishAssignment(
		context.Background(), authority, AssignmentDecision{
			QueryGroup: "query-group-1", DesiredWorkerID: worker.WorkerID,
			ExpectedRecordRevision: 0, PlacementReason: PlacementRendezvous, DecidedAt: now,
		},
	)
	if err != nil {
		t.Fatalf("PublishAssignment() error = %v", err)
	}
	if record.DesiredWorkerID != worker.WorkerID || record.AssignmentGeneration != 1 || record.RecordRevision != 1 ||
		record.ControlEpoch != authority.Fence.OwnerEpoch {
		t.Fatalf("assignment = %+v", record)
	}

	unchanged, err := store.PublishAssignment(
		context.Background(), authority, AssignmentDecision{
			QueryGroup: "query-group-1", DesiredWorkerID: worker.WorkerID,
			ExpectedRecordRevision: record.RecordRevision, PlacementReason: PlacementRendezvous, DecidedAt: now.Add(time.Second),
		},
	)
	if err != nil {
		t.Fatalf("PublishAssignment(unchanged) error = %v", err)
	}
	if unchanged.AssignmentGeneration != record.AssignmentGeneration || unchanged.RecordRevision != record.RecordRevision {
		t.Fatalf("unchanged assignment advanced versions: before=%+v after=%+v", record, unchanged)
	}

	// The leader's lease runs out on the server; its next decision is refused
	// whatever instant the leader itself puts on it.
	elapseOnRedis(t, store, ControlLeaderIdentity, time.Minute+time.Second)
	_, err = store.PublishAssignment(
		context.Background(), authority, AssignmentDecision{
			QueryGroup: "query-group-2", DesiredWorkerID: worker.WorkerID,
			ExpectedRecordRevision: 0, PlacementReason: PlacementRendezvous, DecidedAt: now.Add(2 * time.Second),
		},
	)
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("PublishAssignment(expired authority) error = %v, want ErrStaleFence", err)
	}
}

func TestRedisStoreRejectsLateAssignmentDecisionFromSameControlLeader(t *testing.T) {
	store := newIntegrationStore(t)
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(context.Background(), "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	initial, err := store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: 0,
		PlacementReason: PlacementRendezvous, DecidedAt: now,
	})
	if err != nil {
		t.Fatalf("PublishAssignment(initial) error = %v", err)
	}
	newer, err := store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-2", ExpectedRecordRevision: initial.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("PublishAssignment(newer) error = %v", err)
	}
	_, err = store.PublishAssignment(context.Background(), authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: initial.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now.Add(2 * time.Second),
	})
	if !errors.Is(err, ErrAssignmentConflict) {
		t.Fatalf("PublishAssignment(late) error = %v, want ErrAssignmentConflict", err)
	}
	current, err := store.ReadAssignment(context.Background(), "query-group-1")
	if err != nil {
		t.Fatalf("ReadAssignment() error = %v", err)
	}
	if current.DesiredWorkerID != newer.DesiredWorkerID || current.RecordRevision != newer.RecordRevision {
		t.Fatalf("late decision changed assignment: current=%+v newer=%+v", current, newer)
	}
}

func TestRedisStoreWorkerRegistryExpiresPhysicalEntries(t *testing.T) {
	store := newIntegrationStore(t)
	now := time.Now().Truncate(time.Millisecond)
	worker := WorkerRegistration{
		WorkerID: "worker-short-lived", AssignmentReadiness: WorkerReady, DependencyStatus: DependencyHealthy,
		DeploymentProfile: "standard", CapabilitiesDigest: "capabilities-v1", ExpiresAt: now.Add(150 * time.Millisecond),
	}
	if err := store.RegisterWorker(context.Background(), worker); err != nil {
		t.Fatalf("RegisterWorker() error = %v", err)
	}
	ready, _, err := store.ListReadyWorkers(context.Background(), now)
	if err != nil || len(ready) != 1 {
		t.Fatalf("ListReadyWorkers(immediate) = (%+v, %v)", ready, err)
	}
	time.Sleep(250 * time.Millisecond)
	ready, _, err = store.ListReadyWorkers(context.Background(), time.Now())
	if err != nil || len(ready) != 0 {
		t.Fatalf("ListReadyWorkers(expired) = (%+v, %v)", ready, err)
	}
	count, err := store.client.ZCard(context.Background(), store.workerRegistryKey()).Result()
	if err != nil || count != 0 {
		t.Fatalf("worker registry index after expiry = (%d, %v), want 0", count, err)
	}
	exists, err := store.client.Exists(context.Background(), store.workerKey(worker.WorkerID)).Result()
	if err != nil || exists != 0 {
		t.Fatalf("worker registration object after expiry = (%d, %v), want absent", exists, err)
	}
}

func TestRedisStoreLeaseFenceIsMonotonicAndAssignmentBound(t *testing.T) {
	store := newIntegrationStore(t)
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(context.Background(), "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	if _, err := store.PublishAssignment(
		context.Background(), authority, AssignmentDecision{
			QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: 0,
			PlacementReason: PlacementRendezvous, DecidedAt: now,
		},
	); err != nil {
		t.Fatalf("PublishAssignment() error = %v", err)
	}
	if _, err := store.Acquire(context.Background(), "query-group-1", "worker-2", now, time.Minute); !errors.Is(err, ErrNotDesired) {
		t.Fatalf("Acquire(non-desired) error = %v, want ErrNotDesired", err)
	}

	first, err := store.Acquire(context.Background(), "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	if first.Fence.OwnerEpoch != 1 || first.Fence.LeaseToken == "" {
		t.Fatalf("first lease = %+v", first)
	}
	if err := store.CheckFence(context.Background(), first.Fence); err != nil {
		t.Fatalf("CheckFence(first) error = %v", err)
	}
	renewed, err := store.Renew(context.Background(), first.Fence, now.Add(30*time.Second), time.Minute)
	if err != nil || !renewed.Deadline.Equal(now.Add(90*time.Second)) {
		t.Fatalf("Renew() = (%+v, %v)", renewed, err)
	}
	// The renewed minute passes on the server.
	elapseOnRedis(t, store, "query-group-1", time.Minute+time.Second)
	if err := store.CheckFence(context.Background(), renewed.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(expired) error = %v, want ErrStaleFence", err)
	}

	second, err := store.Acquire(context.Background(), "query-group-1", "worker-1", now.Add(91*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("Acquire(second) error = %v", err)
	}
	if second.Fence.OwnerEpoch != first.Fence.OwnerEpoch+1 || second.Fence.LeaseToken == first.Fence.LeaseToken {
		t.Fatalf("second lease = %+v, first = %+v", second, first)
	}
	if err := store.CheckFence(context.Background(), first.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(old fence) error = %v, want ErrStaleFence", err)
	}
	if err := store.Release(context.Background(), second.Fence); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := store.CheckFence(context.Background(), second.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(released) error = %v, want ErrStaleFence", err)
	}
}

// 02 section 6.3 rule 3: Release matches at least owner, epoch and token;
// V5: a Release cannot delete a newer owner's lease. The newer owner can be
// the same worker. A lease that lapsed -- a renewal stalled past its
// deadline, or one capped at a pending content change -- is acquired again
// by the same worker at the next epoch with a new token, while the session
// that lost it may still be on its way out and releases when it stops. That
// late release names the right worker and must still leave the newer lease
// alone.
func TestAStaleReleaseLeavesTheSameWorkersNewerLease(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	const queryGroup = execution.QueryGroupIdentity("query-group-1")
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: queryGroup, DesiredWorkerID: "worker-1", PlacementReason: PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := store.Acquire(ctx, queryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	elapseOnRedis(t, store, queryGroup, time.Minute+time.Second)
	second, err := store.Acquire(ctx, queryGroup, "worker-1", now.Add(61*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fence.OwnerEpoch != first.Fence.OwnerEpoch+1 || second.Fence.LeaseToken == first.Fence.LeaseToken {
		t.Fatalf("second lease = %+v, want the same worker at the next epoch with a new token after %+v", second.Fence, first.Fence)
	}

	if err := store.Release(ctx, first.Fence); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("the lost session's Release = %v, want ErrStaleFence", err)
	}
	if err := store.CheckFence(ctx, second.Fence); err != nil {
		t.Fatalf("the newer lease after the lost session's Release: CheckFence = %v, want it live", err)
	}
	owner, found, err := store.ReadQueryGroupOwner(ctx, queryGroup)
	if err != nil || !found || owner.OwnerID != "worker-1" || owner.OwnerEpoch != second.Fence.OwnerEpoch {
		t.Fatalf("owner after the lost session's Release = (%+v, %t, %v), want worker-1 at epoch %d",
			owner, found, err, second.Fence.OwnerEpoch)
	}
	if _, err := store.Renew(ctx, second.Fence, now.Add(70*time.Second), time.Minute); err != nil {
		t.Fatalf("Renew(newer lease) = %v", err)
	}
}

func TestRedisStoreFencedCASRejectsExpiredSnapshotPublisher(t *testing.T) {
	store := newIntegrationStore(t)
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(context.Background(), "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	result, err := store.FencedCompareAndSet(context.Background(), FencedCASRequest{
		Fence: authority.Fence, Namespace: "snapshot-active", ExpectedMissing: true, Value: []byte("snapshot-1"),
	})
	if err != nil || result != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(first) = (%s, %v)", result, err)
	}
	elapseOnRedis(t, store, ControlLeaderIdentity, time.Minute+time.Second)
	result, err = store.FencedCompareAndSet(context.Background(), FencedCASRequest{
		Fence: authority.Fence, Namespace: "snapshot-active",
		Expected: []byte("snapshot-1"), Value: []byte("snapshot-2"),
	})
	if !errors.Is(err, ErrStaleFence) || result != FencedCASStaleOwner {
		t.Fatalf("FencedCompareAndSet(expired) = (%s, %v), want stale owner", result, err)
	}
	value, missing, err := store.ReadControl(context.Background(), ControlLeaderIdentity, "snapshot-active")
	if err != nil || missing || !bytes.Equal(value, []byte("snapshot-1")) {
		t.Fatalf("ReadControl(after stale owner) = (%q, %t, %v), want unchanged snapshot-1", value, missing, err)
	}
}

func TestRedisStoreReadControlComposesWithFencedCompareAndSet(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	namespace := "progress"

	value, missing, err := store.ReadControl(ctx, queryGroup, namespace)
	if err != nil || !missing || value != nil {
		t.Fatalf("ReadControl(missing) = (%q, %t, %v), want (nil, true, nil)", value, missing, err)
	}

	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: queryGroup, DesiredWorkerID: "worker-1", ExpectedRecordRevision: 0,
		PlacementReason: PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatalf("PublishAssignment() error = %v", err)
	}
	lease, err := store.Acquire(ctx, queryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	want := []byte("progress-v1")
	status, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: lease.Fence, Namespace: namespace, ExpectedMissing: true, Value: want,
	})
	if err != nil || status != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet() = (%s, %v), want (%s, nil)", status, err, FencedCASApplied)
	}

	value, missing, err = store.ReadControl(ctx, queryGroup, namespace)
	if err != nil || missing || !bytes.Equal(value, want) {
		t.Fatalf("ReadControl(roundtrip) = (%q, %t, %v), want (%q, false, nil)", value, missing, err, want)
	}
	wantNext := []byte("progress-v2")
	status, err = store.FencedCompareAndSet(ctx, FencedCASRequest{
		Fence: lease.Fence, Namespace: namespace, Expected: value, Value: wantNext,
	})
	if err != nil || status != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(read expected) = (%s, %v), want (%s, nil)", status, err, FencedCASApplied)
	}
	value, missing, err = store.ReadControl(ctx, queryGroup, namespace)
	if err != nil || missing || !bytes.Equal(value, wantNext) {
		t.Fatalf("ReadControl(updated) = (%q, %t, %v), want (%q, false, nil)", value, missing, err, wantNext)
	}
	value[0] = 'X'
	stored, missing, err := store.ReadControl(ctx, queryGroup, namespace)
	if err != nil || missing || !bytes.Equal(stored, wantNext) {
		t.Fatalf("ReadControl(after caller mutation) = (%q, %t, %v), want (%q, false, nil)", stored, missing, err, wantNext)
	}
}

// The fence check carries the Assignment record so a caller that needs both
// spends one round trip. The record has to be indistinguishable from the one
// HGETALL would have produced, or callers would start to care which way they
// asked -- so this compares the two answers against the same stored record
// rather than against a hand-written expectation.
func TestRedisStoreCheckFenceCarriesTheAssignmentItAlreadyRead(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	// The control leader identity has no Assignment hash at all. It is the
	// branch that breaks first if the record fields are returned unguarded,
	// because a Lua reply stops at its first nil.
	if err := store.CheckFence(ctx, authority.Fence); err != nil {
		t.Fatalf("CheckFence(control leader) error = %v", err)
	}
	if _, err := store.CheckFenceWithAssignment(ctx, authority.Fence); err == nil {
		t.Fatal("CheckFenceWithAssignment(control leader) returned a record, want a refusal")
	}

	if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: 0,
		PlacementReason: PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatalf("PublishAssignment() error = %v", err)
	}
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	read, err := store.ReadAssignment(ctx, "query-group-1")
	if err != nil {
		t.Fatalf("ReadAssignment() error = %v", err)
	}
	merged, err := store.CheckFenceWithAssignment(ctx, lease.Fence)
	if err != nil {
		t.Fatalf("CheckFenceWithAssignment() error = %v", err)
	}
	if merged.QueryGroup != read.QueryGroup || merged.DesiredWorkerID != read.DesiredWorkerID ||
		merged.AssignmentGeneration != read.AssignmentGeneration || merged.RecordRevision != read.RecordRevision ||
		merged.ControlEpoch != read.ControlEpoch || merged.PlacementReason != read.PlacementReason ||
		!merged.AssignedAt.Equal(read.AssignedAt) {
		t.Fatalf("CheckFenceWithAssignment() = %+v, ReadAssignment() = %+v", merged, read)
	}

	// A rejected fence yields the rejection and nothing else: an Assignment a
	// worker cannot act on must not reach it looking like one it can.
	foreign := execution.OwnerFence{
		QueryGroup: "query-group-1", OwnerID: "worker-2", OwnerEpoch: 1, LeaseToken: "lease-token",
	}
	if record, err := store.CheckFenceWithAssignment(ctx, foreign); !errors.Is(err, ErrNotDesired) ||
		record != (AssignmentRecord{}) {
		t.Fatalf("CheckFenceWithAssignment(other worker) = (%+v, %v), want (zero, ErrNotDesired)", record, err)
	}
	if err := store.Release(ctx, lease.Fence); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if record, err := store.CheckFenceWithAssignment(ctx, lease.Fence); !errors.Is(err, ErrStaleFence) ||
		record != (AssignmentRecord{}) {
		t.Fatalf("CheckFenceWithAssignment(released) = (%+v, %v), want (zero, ErrStaleFence)", record, err)
	}
}

func newIntegrationStore(t *testing.T) *RedisStore {
	t.Helper()
	server := redistest.Start(t)
	store, err := NewRedisStore(RedisStoreOptions{
		Address: server.Addr, Prefix: "alarmd-ownership-test", DialTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// The server answered before Start returned; what is left is the store's
	// own connection, under the same bound.
	deadline := time.Now().Add(redistest.ReadyWithin)
	for time.Now().Before(deadline) {
		if err := store.Ping(context.Background()); err == nil {
			return store
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
	return nil
}

// ReadControlBatch answers exactly what ReadControl answers for each Query
// Group, in one pipelined round trip per batch: a stored value comes back as
// its bytes, an absent key as missing, and neither shadows the other.
func TestRedisStoreReadControlBatchMatchesReadControl(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	namespace := "progress"
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
	if err != nil {
		t.Fatalf("AcquireControlLeader() error = %v", err)
	}
	stored := map[execution.QueryGroupIdentity][]byte{}
	var queryGroups []execution.QueryGroupIdentity
	for index := 0; index < 600; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%03d", index))
		queryGroups = append(queryGroups, queryGroup)
		if index%3 == 2 {
			continue
		}
		if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", ExpectedRecordRevision: 0,
			PlacementReason: PlacementRendezvous, DecidedAt: now,
		}); err != nil {
			t.Fatalf("PublishAssignment(%s) error = %v", queryGroup, err)
		}
		lease, err := store.Acquire(ctx, queryGroup, "worker-1", now, time.Minute)
		if err != nil {
			t.Fatalf("Acquire(%s) error = %v", queryGroup, err)
		}
		value := []byte("progress-" + string(queryGroup))
		if status, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
			Fence: lease.Fence, Namespace: namespace, ExpectedMissing: true, Value: value,
		}); err != nil || status != FencedCASApplied {
			t.Fatalf("FencedCompareAndSet(%s) = (%s, %v)", queryGroup, status, err)
		}
		stored[queryGroup] = value
	}
	reads, err := store.ReadControlBatch(ctx, queryGroups, namespace)
	if err != nil || len(reads) != len(queryGroups) {
		t.Fatalf("ReadControlBatch() = (%d reads, %v)", len(reads), err)
	}
	for index, queryGroup := range queryGroups {
		single, missing, err := store.ReadControl(ctx, queryGroup, namespace)
		if err != nil {
			t.Fatal(err)
		}
		if reads[index].Missing != missing || !bytes.Equal(reads[index].Raw, single) {
			t.Fatalf("batch read of %s = %+v, single read = (%q, %t)", queryGroup, reads[index], single, missing)
		}
		if want, ok := stored[queryGroup]; ok != !reads[index].Missing || (ok && !bytes.Equal(want, reads[index].Raw)) {
			t.Fatalf("batch read of %s = %+v, want stored=%q present=%t", queryGroup, reads[index], want, ok)
		}
	}
	if _, err := store.ReadControlBatch(ctx, []execution.QueryGroupIdentity{"query-group-000", ""}, namespace); err == nil {
		t.Fatal("an empty Query Group identity must be refused")
	}
}

// Two workers that both believe they own a Query Group, the shape a rolling
// update produces when a new replica is assigned a Query Group an old one
// still runs: the Assignment and the lease keep the two apart. From the
// moment the Assignment names the new worker, the old worker is the
// draining holder: its lease stays the only valid one, so the commit
// boundary it is in still lands, and its renewal is capped at the grace's
// end. The new worker cannot acquire while the old lease lives (busy), and
// once it holds the lease, by release or by expiry, every write of the old
// worker is refused as a stale owner and changes nothing, and the new
// worker's writes apply. No window has two writers.
func TestRedisStoreOverlappingOwnersOldWriteIsRefused(t *testing.T) {
	for _, handover := range []struct {
		name     string
		takeover func(t *testing.T, store *RedisStore, old Lease, now time.Time) time.Time
	}{
		{name: "old worker releases on shutdown", takeover: func(t *testing.T, store *RedisStore, old Lease, now time.Time) time.Time {
			t.Helper()
			if err := store.Release(context.Background(), old.Fence); err != nil {
				t.Fatalf("Release(old) error = %v", err)
			}
			return now.Add(11 * time.Second)
		}},
		{name: "old worker vanishes and its lease expires", takeover: func(t *testing.T, store *RedisStore, old Lease, now time.Time) time.Time {
			t.Helper()
			elapseOnRedis(t, store, old.Fence.QueryGroup, 61*time.Second)
			return now.Add(61 * time.Second)
		}},
	} {
		t.Run(handover.name, func(t *testing.T) {
			store := newIntegrationStore(t)
			ctx := context.Background()
			queryGroup := execution.QueryGroupIdentity("query-group-1")
			now := time.UnixMilli(1_700_000_000_000)
			authority, err := store.AcquireControlLeader(ctx, "control-1", now, time.Minute)
			if err != nil {
				t.Fatalf("AcquireControlLeader() error = %v", err)
			}
			first, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
				QueryGroup: queryGroup, DesiredWorkerID: "worker-old", ExpectedRecordRevision: 0,
				PlacementReason: PlacementRendezvous, DecidedAt: now,
			})
			if err != nil {
				t.Fatalf("PublishAssignment(old) error = %v", err)
			}
			old, err := store.Acquire(ctx, queryGroup, "worker-old", now, time.Minute)
			if err != nil || old.Fence.OwnerEpoch != 1 {
				t.Fatalf("Acquire(old) = (%+v, %v)", old, err)
			}
			if result, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
				Fence: old.Fence, Namespace: "progress", ExpectedMissing: true, Value: []byte("cursor-1"),
			}); err != nil || result != FencedCASApplied {
				t.Fatalf("FencedCompareAndSet(old, first write) = (%s, %v)", result, err)
			}

			// The Leader moves the Assignment to the new worker while the
			// old lease still lives.
			moved := now.Add(10 * time.Second)
			if _, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
				QueryGroup: queryGroup, DesiredWorkerID: "worker-new", ExpectedRecordRevision: first.RecordRevision,
				PlacementReason: PlacementRendezvous, DecidedAt: moved,
			}); err != nil {
				t.Fatalf("PublishAssignment(new) error = %v", err)
			}
			if _, err := store.Renew(ctx, old.Fence, moved, time.Minute); err != nil {
				t.Fatalf("Renew(old after the move) error = %v, want a renewal capped at the grace's end", err)
			}
			if _, err := store.Acquire(ctx, queryGroup, "worker-new", moved, time.Minute); !errors.Is(err, ErrLeaseBusy) {
				t.Fatalf("Acquire(new while the old lease lives) error = %v, want ErrLeaseBusy", err)
			}
			// The old lease is still the only valid one: the commit boundary
			// the old worker is in lands, and nobody else can write yet.
			if result, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
				Fence: old.Fence, Namespace: "progress", Expected: []byte("cursor-1"), Value: []byte("cursor-old-2"),
			}); err != nil || result != FencedCASApplied {
				t.Fatalf("FencedCompareAndSet(old during the handover window) = (%s, %v), want applied", result, err)
			}
			if err := store.CheckFence(ctx, old.Fence); err != nil {
				t.Fatalf("CheckFence(old during the handover window) error = %v, want valid", err)
			}

			at := handover.takeover(t, store, old, now)
			replacement, err := store.Acquire(ctx, queryGroup, "worker-new", at, time.Minute)
			if err != nil || replacement.Fence.OwnerEpoch != old.Fence.OwnerEpoch+1 {
				t.Fatalf("Acquire(new) = (%+v, %v), want the next owner epoch", replacement, err)
			}
			// The old worker still believes it owns the Query Group and
			// writes under its fence: refused, and the value stands.
			result, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
				Fence: old.Fence, Namespace: "progress", Expected: []byte("cursor-old-2"), Value: []byte("cursor-old-3"),
			})
			if !errors.Is(err, ErrStaleFence) || result != FencedCASStaleOwner {
				t.Fatalf("FencedCompareAndSet(old after takeover) = (%s, %v), want stale owner", result, err)
			}
			if err := store.CheckFence(ctx, old.Fence); !errors.Is(err, ErrNotDesired) {
				t.Fatalf("CheckFence(old after takeover) error = %v, want ErrNotDesired", err)
			}
			value, missing, err := store.ReadControl(ctx, queryGroup, "progress")
			if err != nil || missing || !bytes.Equal(value, []byte("cursor-old-2")) {
				t.Fatalf("ReadControl(after the refused writes) = (%q, %t, %v), want the old worker's last landed write", value, missing, err)
			}
			if result, err := store.FencedCompareAndSet(ctx, FencedCASRequest{
				Fence: replacement.Fence, Namespace: "progress", Expected: []byte("cursor-old-2"), Value: []byte("cursor-2"),
			}); err != nil || result != FencedCASApplied {
				t.Fatalf("FencedCompareAndSet(new) = (%s, %v), want applied", result, err)
			}
		})
	}
}

// pipelineCounter records how many commands each pipeline a client sends
// carries, and which.
type pipelineCounter struct{ pipelines [][]string }

func (counter *pipelineCounter) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (counter *pipelineCounter) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (counter *pipelineCounter) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	names := make([]string, 0, len(cmds))
	for _, cmd := range cmds {
		names = append(names, cmd.Name())
	}
	counter.pipelines = append(counter.pipelines, names)
	return ctx, nil
}
func (counter *pipelineCounter) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

// A read within what is admitted takes at most one pipeline of lengths and
// one of values, of at most ControlReadBatch keys each: admit is asked each
// value's length in order, the values it accepted are read and the rest left,
// and a key Redis answers with an error is that entry's error while the
// others of the pipeline read.
func TestRedisStoreReadControlWithinReadsOnePipelineOfWhatIsAdmitted(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	namespace := "progress"
	var queryGroups []execution.QueryGroupIdentity
	lengths := map[execution.QueryGroupIdentity]int{}
	for index := 0; index < ControlReadBatch+88; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%03d", index))
		queryGroups = append(queryGroups, queryGroup)
		switch {
		case index == 3:
			// Not a string: Redis answers this key with an error.
			if err := store.client.HSet(ctx, store.controlKey(queryGroup, namespace), "field", "value").Err(); err != nil {
				t.Fatal(err)
			}
		case index%5 == 4:
			// Missing.
		default:
			value := bytes.Repeat([]byte("x"), 100+index)
			if err := store.client.Set(ctx, store.controlKey(queryGroup, namespace), value, 0).Err(); err != nil {
				t.Fatal(err)
			}
			lengths[queryGroup] = len(value)
		}
	}
	counter := &pipelineCounter{}
	store.client.AddHook(counter)
	asked, spent := []uint64{}, uint64(0)
	const budget = 20_000
	reads, read, err := store.ReadControlWithin(ctx, queryGroups, namespace, func(size uint64) bool {
		asked = append(asked, size)
		if spent+size > budget {
			return false
		}
		spent += size
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(counter.pipelines) != 2 || len(counter.pipelines[0]) != ControlReadBatch || counter.pipelines[0][0] != "strlen" ||
		len(counter.pipelines[1]) != read || counter.pipelines[1][0] != "get" {
		t.Fatalf("pipelines %d of sizes %v, want one of %d lengths and one of the %d values admitted", len(counter.pipelines),
			pipelineSizes(counter.pipelines), ControlReadBatch, read)
	}
	if read == 0 || read >= ControlReadBatch || len(reads) != read || len(asked) != read+1 {
		t.Fatalf("read %d of %d with %d lengths asked, want a prefix the budget cut, asked up to the first refused", read,
			ControlReadBatch, len(asked))
	}
	for index, size := range asked {
		if want := uint64(lengths[queryGroups[index]]); size != want {
			t.Fatalf("admit asked %d for %s, whose value is %d bytes", size, queryGroups[index], want)
		}
	}
	for index := 0; index < read; index++ {
		entry := reads[index]
		switch {
		case index == 3:
			if entry.Err == nil {
				t.Fatalf("the key Redis refused read as %+v, want its own error", entry)
			}
		case index%5 == 4:
			if !entry.Missing || entry.Err != nil {
				t.Fatalf("missing key read as %+v", entry)
			}
		default:
			if entry.Err != nil || len(entry.Raw) != lengths[queryGroups[index]] {
				t.Fatalf("%s read as %d bytes and %v, want %d", queryGroups[index], len(entry.Raw), entry.Err, lengths[queryGroups[index]])
			}
		}
	}
	// A refusal of the first reads nothing and sends no values.
	counter.pipelines = nil
	if reads, read, err := store.ReadControlWithin(ctx, queryGroups, namespace, func(uint64) bool { return false }); err != nil ||
		read != 0 || len(reads) != 0 || len(counter.pipelines) != 1 {
		t.Fatalf("refused read %d, %d pipelines, error %v; want nothing read and only the lengths asked", read, len(counter.pipelines), err)
	}
}

func pipelineSizes(pipelines [][]string) []int {
	sizes := make([]int, 0, len(pipelines))
	for _, pipeline := range pipelines {
		sizes = append(sizes, len(pipeline))
	}
	return sizes
}
