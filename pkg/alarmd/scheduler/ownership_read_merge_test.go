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

// There was a ReadAssignment method here, and an assignmentReads counter the
// tests below asserted was zero. Nothing can call it any more - the SlotSource
// no longer holds an Assignment reader at all - so the assertion had become
// one no change could make fail, which reads as coverage and is not. What the
// tests count instead is total store commands, which stays falsifiable: any
// path that goes back to two readings makes it rise.

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
	source := mustProductionSlotSource(t, session, catalog,
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
		t.Fatalf("ownership commands = %d (fence checks %d, merged fence checks %d), want 1",
			store.commands, store.fenceChecks, store.mergedFenceChecks)
	}
}

// TestDueSlotDecisionSpendsOneRoundTripPerOwnershipReading covers the other
// half of the mix, and the half that carries the load: a Slot that is due takes
// several fresh ownership readings, one after each piece of real elapsed work,
// and none of them may be answered from a cache. What they may not do is cost
// two round trips each.
//
// The assertion is that every store command this decision sent was the merged
// script run. It stays falsifiable in the direction that matters: a path that
// goes back to reading the Assignment separately raises commands above
// mergedFenceChecks, and one that drops the Assignment check entirely leaves
// fenceChecks non-zero.
func TestDueSlotDecisionSpendsOneRoundTripPerOwnershipReading(t *testing.T) {
	at := time.Unix(200, 0)
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	store := &countingOwnershipStore{
		fence: testFence(7), deadline: at.Add(time.Minute), assignment: testAssignment("worker-1", 3),
	}
	session, err := ownership.OpenSession(context.Background(), store, "query-group-1", "worker-1", at, time.Minute)
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	source := mustProductionSlotSource(t, session, catalog,
		&fakeProgressReader{result: missingProgress(), catalog: catalog}, at)
	// Opening the Query Group is not what one decision costs.
	store.commands = 0

	_, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("Next() due=%v error=%v, want one due Slot", due, err)
	}
	if store.mergedFenceChecks == 0 {
		t.Fatal("a due Slot decision took no ownership reading at all")
	}
	if store.commands != store.mergedFenceChecks {
		t.Fatalf("ownership commands = %d against %d merged fence checks (split fence checks %d): "+
			"every ownership reading on this path has to be one round trip",
			store.commands, store.mergedFenceChecks, store.fenceChecks)
	}
	if store.fenceChecks != 0 {
		t.Fatalf("split fence checks = %d, want 0: a fence checked without its Assignment is the weaker answer", store.fenceChecks)
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
