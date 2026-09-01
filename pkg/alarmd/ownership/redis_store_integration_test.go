// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
	ready, err := store.ListReadyWorkers(context.Background(), now)
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

	_, err = store.PublishAssignment(
		context.Background(), authority, AssignmentDecision{
			QueryGroup: "query-group-2", DesiredWorkerID: worker.WorkerID,
			ExpectedRecordRevision: 0, PlacementReason: PlacementRendezvous, DecidedAt: now.Add(2 * time.Minute),
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
	ready, err := store.ListReadyWorkers(context.Background(), now)
	if err != nil || len(ready) != 1 {
		t.Fatalf("ListReadyWorkers(immediate) = (%+v, %v)", ready, err)
	}
	time.Sleep(250 * time.Millisecond)
	ready, err = store.ListReadyWorkers(context.Background(), time.Now())
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
	if err := store.CheckFence(context.Background(), first.Fence, now.Add(30*time.Second)); err != nil {
		t.Fatalf("CheckFence(first) error = %v", err)
	}
	renewed, err := store.Renew(context.Background(), first.Fence, now.Add(30*time.Second), time.Minute)
	if err != nil || !renewed.Deadline.Equal(now.Add(90*time.Second)) {
		t.Fatalf("Renew() = (%+v, %v)", renewed, err)
	}
	if err := store.CheckFence(context.Background(), renewed.Fence, now.Add(91*time.Second)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(expired) error = %v, want ErrStaleFence", err)
	}

	second, err := store.Acquire(context.Background(), "query-group-1", "worker-1", now.Add(91*time.Second), time.Minute)
	if err != nil {
		t.Fatalf("Acquire(second) error = %v", err)
	}
	if second.Fence.OwnerEpoch != first.Fence.OwnerEpoch+1 || second.Fence.LeaseToken == first.Fence.LeaseToken {
		t.Fatalf("second lease = %+v, first = %+v", second, first)
	}
	if err := store.CheckFence(context.Background(), first.Fence, now.Add(92*time.Second)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(old fence) error = %v, want ErrStaleFence", err)
	}
	if err := store.Release(context.Background(), second.Fence); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := store.CheckFence(context.Background(), second.Fence, now.Add(92*time.Second)); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("CheckFence(released) error = %v, want ErrStaleFence", err)
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
		Fence: authority.Fence, At: now, Namespace: "snapshot-active", ExpectedMissing: true, Value: []byte("snapshot-1"),
	})
	if err != nil || result != FencedCASApplied {
		t.Fatalf("FencedCompareAndSet(first) = (%s, %v)", result, err)
	}
	result, err = store.FencedCompareAndSet(context.Background(), FencedCASRequest{
		Fence: authority.Fence, At: now.Add(2 * time.Minute), Namespace: "snapshot-active",
		Expected: []byte("snapshot-1"), Value: []byte("snapshot-2"),
	})
	if !errors.Is(err, ErrStaleFence) || result != FencedCASStaleOwner {
		t.Fatalf("FencedCompareAndSet(expired) = (%s, %v), want stale owner", result, err)
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
		Fence: lease.Fence, At: now, Namespace: namespace, ExpectedMissing: true, Value: want,
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
		Fence: lease.Fence, At: now, Namespace: namespace, Expected: value, Value: wantNext,
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

func newIntegrationStore(t *testing.T) *RedisStore {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	address := reserveAddress(t)
	server := startRedis(t, executable, address)
	store, err := NewRedisStore(RedisStoreOptions{
		Address: address, Prefix: "alarmd-ownership-test", DialTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 2,
	})
	if err != nil {
		t.Fatalf("NewRedisStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		if server.ProcessState == nil || !server.ProcessState.Exited() {
			_ = server.Process.Kill()
			_ = server.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := store.Ping(context.Background()); err == nil {
			return store
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
	return nil
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	return address
}

func startRedis(t *testing.T, executable, address string) *exec.Cmd {
	t.Helper()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi(port) error = %v", err)
	}
	command := exec.Command(executable,
		"--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning",
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatalf("redis-server start error = %v", err)
	}
	return command
}
