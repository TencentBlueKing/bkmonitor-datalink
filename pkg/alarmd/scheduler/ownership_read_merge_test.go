// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// countingOwnershipStore counts store round trips the way Redis counts them:
// one increment per call that would leave the process. It stands in for both
// the lease store behind the Session and the Assignment reader behind the
// SlotSource, because on the idle path those are the same Redis instance and
// the question under test is how many commands one idle attempt sends it.
type countingOwnershipStore struct {
	fence      execution.OwnerFence
	deadline   time.Time
	assignment ownership.AssignmentRecord
	fenceErr   error

	commands           int
	fenceChecks        int
	assignmentReads    int
	mergedFenceChecks  int
	acquireCalls       int
	renewCalls         int
	releaseCalls       int
	unexpectedIdentity execution.QueryGroupIdentity
}

func (store *countingOwnershipStore) Acquire(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
	_ string,
	_ time.Time,
	_ time.Duration,
) (ownership.Lease, error) {
	store.commands++
	store.acquireCalls++
	if queryGroup != store.fence.QueryGroup {
		store.unexpectedIdentity = queryGroup
	}
	return ownership.Lease{Fence: store.fence, Deadline: store.deadline}, nil
}

func (store *countingOwnershipStore) Renew(
	_ context.Context,
	fence execution.OwnerFence,
	_ time.Time,
	_ time.Duration,
) (ownership.Lease, error) {
	store.commands++
	store.renewCalls++
	return ownership.Lease{Fence: fence, Deadline: store.deadline}, nil
}

func (store *countingOwnershipStore) CheckFence(_ context.Context, _ execution.OwnerFence, _ time.Time) error {
	store.commands++
	store.fenceChecks++
	return store.fenceErr
}

func (store *countingOwnershipStore) CheckFenceWithAssignment(
	_ context.Context,
	_ execution.OwnerFence,
	_ time.Time,
) (ownership.AssignmentRecord, error) {
	store.commands++
	store.mergedFenceChecks++
	if store.fenceErr != nil {
		return ownership.AssignmentRecord{}, store.fenceErr
	}
	return store.assignment, nil
}

func (store *countingOwnershipStore) Release(_ context.Context, _ execution.OwnerFence) error {
	store.commands++
	store.releaseCalls++
	return nil
}

func (store *countingOwnershipStore) ReadAssignment(
	_ context.Context,
	queryGroup execution.QueryGroupIdentity,
) (ownership.AssignmentRecord, error) {
	store.commands++
	store.assignmentReads++
	if queryGroup != store.fence.QueryGroup {
		store.unexpectedIdentity = queryGroup
	}
	return store.assignment, nil
}

// idleRunnerFixture builds the production Runner over the production SlotSource
// for a Query Group whose first Slot is still in the future, which is the
// source_not_due return that dominates the live run_one mix.
func idleRunnerFixture(t *testing.T, at time.Time) (*Runner, *countingOwnershipStore, *[]observability.Observation) {
	t.Helper()
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	store := &countingOwnershipStore{
		fence: testFence(7), deadline: at.Add(time.Minute), assignment: testAssignment("worker-1", 3),
	}
	session, err := ownership.OpenSession(context.Background(), store, "query-group-1", "worker-1", at, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	source := mustProductionSlotSource(t, store, session, catalog,
		&fakeProgressReader{result: missingProgress(), catalog: catalog}, at)
	flights := NewFlightCoordinator()
	observed := &[]observability.Observation{}
	flights.observer = observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Stage == observability.StageRunnerReturned {
			*observed = append(*observed, o)
		}
	})
	runner, err := NewRunner("query-group-1", session, source, &blockingExecutor{}, flights, func() time.Time { return at })
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	// OpenSession already spent one Acquire. The counter is about what one
	// attempt costs, not what opening the Query Group cost.
	store.commands = 0
	return runner, store, observed
}

// TestRunnerIdleAttemptSpendsOneOwnershipRoundTrip pins the cost of the return
// that dominates the live mix. The Runner's own fence check and the two reads
// the SlotSource opens with are separated by nothing but a local time
// comparison, so they describe one instant and have to cost one round trip.
func TestRunnerIdleAttemptSpendsOneOwnershipRoundTrip(t *testing.T) {
	at := time.Unix(59, 0)
	runner, store, observed := idleRunnerFixture(t, at)

	_, attempted, err := runner.RunOne(context.Background())
	if err != nil || attempted {
		t.Fatalf("RunOne() attempted=%v error=%v", attempted, err)
	}
	if len(*observed) != 1 || (*observed)[0].RunOutcome != "source_not_due" {
		t.Fatalf("observed = %+v, want one source_not_due return", *observed)
	}
	if store.commands != 1 {
		t.Fatalf("ownership commands = %d (fence checks %d, merged fence checks %d, assignment reads %d), want 1",
			store.commands, store.fenceChecks, store.mergedFenceChecks, store.assignmentReads)
	}
	if store.assignmentReads != 0 {
		t.Fatalf("separate assignment reads = %d, want 0: the fence check already returned the record", store.assignmentReads)
	}
}

// TestRunnerLocalBackoffPrecedesAnyOwnershipRoundTrip keeps the only local
// decision on the path ahead of the store. A Query Group the Runner has already
// decided not to touch yet must not pay a round trip to find that out.
func TestRunnerLocalBackoffPrecedesAnyOwnershipRoundTrip(t *testing.T) {
	at := time.Unix(59, 0)
	runner, store, observed := idleRunnerFixture(t, at)
	runner.sourceNextAt = at.Add(time.Second)

	_, attempted, err := runner.RunOne(context.Background())
	if err != nil || attempted {
		t.Fatalf("RunOne() attempted=%v error=%v", attempted, err)
	}
	if len(*observed) != 1 || (*observed)[0].RunOutcome != "source_backoff" {
		t.Fatalf("observed = %+v, want one source_backoff return", *observed)
	}
	if store.commands != 0 {
		t.Fatalf("ownership commands = %d, want 0 while the Runner is inside its own backoff", store.commands)
	}
}

// TestRunnerOwnershipRejectionKeepsItsOwnOutcome guards the metric boundary the
// merge could have moved. An Assignment that no longer names this worker is an
// ownership loss, not a Slot source failure, and the dispatcher decides to stop
// the Query Group by unwrapping exactly these errors.
func TestRunnerOwnershipRejectionKeepsItsOwnOutcome(t *testing.T) {
	for name, rejection := range map[string]error{
		"not_desired": ownership.ErrNotDesired,
		"stale_fence": ownership.ErrStaleFence,
	} {
		t.Run(name, func(t *testing.T) {
			at := time.Unix(59, 0)
			runner, store, observed := idleRunnerFixture(t, at)
			store.fenceErr = rejection

			_, attempted, err := runner.RunOne(context.Background())
			if attempted || !errors.Is(err, rejection) {
				t.Fatalf("RunOne() attempted=%v error=%v, want %v", attempted, err, rejection)
			}
			if len(*observed) != 1 || (*observed)[0].RunOutcome != "ownership_rejected" {
				t.Fatalf("observed = %+v, want one ownership_rejected return", *observed)
			}
		})
	}
}
