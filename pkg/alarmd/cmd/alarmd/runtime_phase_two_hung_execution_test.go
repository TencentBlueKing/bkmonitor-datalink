// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// hungQueryGroup runs a Slot that ignores its cancellation until the test
// lets it go. Its execution carries a real stage marker, which the case moves
// as the execution would: the deadline it runs under, the stage it is in,
// and whether it has begun to commit.
type hungQueryGroup struct {
	*callbackPhaseTwoQueryGroup
	marker   execution.StageMarker
	released atomic.Int32
}

func (runner *hungQueryGroup) DeclineHung(now time.Time, grace time.Duration) (string, time.Time, bool) {
	return runner.marker.Decline(now, grace)
}

func (runner *hungQueryGroup) Release(context.Context) error {
	runner.released.Add(1)
	return nil
}

func newHungQueryGroup(started chan<- struct{}, letGo <-chan struct{}) *hungQueryGroup {
	var once atomic.Bool
	runner := &hungQueryGroup{callbackPhaseTwoQueryGroup: &callbackPhaseTwoQueryGroup{}}
	runner.run = func(context.Context) (execution.SlotExecutionResult, bool, error) {
		if once.CompareAndSwap(false, true) {
			runner.marker.Begin(execution.SlotStageExecute)
			close(started)
			<-letGo // the context is not watched
			runner.marker.Clear()
		}
		return execution.SlotExecutionResult{Completed: true}, true, nil
	}
	return runner
}

func lastDeclined(owner *fakePhaseTwoOwnership) []ownership.DeclinedQueryGroup {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.registrations) == 0 {
		return nil
	}
	return owner.registrations[len(owner.registrations)-1].Declined
}

// hungCase is a bundle running one Query Group whose first Slot hangs until
// the case lets it go.
type hungCase struct {
	queryGroup execution.QueryGroupIdentity
	runner     *hungQueryGroup
	owner      *fakePhaseTwoOwnership
	health     *phaseTwoApplicationHealth
	bundle     *phaseTwoWorkerBundle
	letGo      func()
	ran        chan error
}

func startHungCase(t *testing.T, configure func(*hungQueryGroup)) *hungCase {
	t.Helper()
	queryGroup := execution.QueryGroupIdentity("query-group-hung")
	started, letGo := make(chan struct{}), make(chan struct{})
	runner := newHungQueryGroup(started, letGo)
	if configure != nil {
		configure(runner)
	}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	health := newPhaseTwoApplicationHealth()
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), health,
		&fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	var once atomic.Bool
	hung := &hungCase{queryGroup: queryGroup, runner: runner, owner: owner, health: health, bundle: bundle,
		ran: make(chan error, 1), letGo: func() {
			if once.CompareAndSwap(false, true) {
				close(letGo)
			}
		}}
	t.Cleanup(hung.letGo)
	go func() { hung.ran <- runScheduledOnceSettled(context.Background(), bundle) }()
	waitSignal(t, started, "the Slot that will hang")
	return hung
}

// stillHeld fails the case when the Query Group was let go.
func (hung *hungCase) stillHeld(t *testing.T, why string) {
	t.Helper()
	if hung.runner.released.Load() != 0 || heldRunner(hung.bundle, hung.queryGroup) == nil || hung.bundle.declinedRegistration() != nil {
		t.Fatalf("%s: released %d, held %t, declined %+v; want the Query Group kept", why,
			hung.runner.released.Load(), heldRunner(hung.bundle, hung.queryGroup) != nil, hung.bundle.declinedRegistration())
	}
}

// An execution past its own deadline by more than the grace, before it has
// begun to commit, has ignored its cancellation (design 02 section 6.5). Its
// Query Group is let go at once - detached, its lease released without
// waiting on the hung Slot - and named on the replica's registration with
// the stage it hangs in; the replica does not open it again while the
// execution hangs, and stays ready for the rest. When the hung execution
// returns at last the decline is lifted. A replica started afresh declines
// nothing.
func TestAnExecutionHungPastItsOwnDeadlineIsDeclinedUntilItReturns(t *testing.T) {
	hung := startHungCase(t, nil)
	deadline := time.UnixMilli(time.Now().Add(-time.Hour).UnixMilli())
	hung.runner.marker.Enter(execution.SlotStageQuery)
	hung.runner.marker.Extend(deadline, deadline)

	// Inside the grace: nothing happens.
	hung.bundle.declineHungExecutions(deadline.Add(executionPastDeadlineGrace / 2))
	hung.stillHeld(t, "inside the grace")
	// Past it: declined.
	hung.bundle.declineHungExecutions(deadline.Add(executionPastDeadlineGrace + time.Second))
	bound := time.Now().Add(signalWaitBound)
	for hung.runner.released.Load() == 0 && time.Now().Before(bound) {
		time.Sleep(time.Millisecond)
	}
	if released := hung.runner.released.Load(); released != 1 {
		t.Fatalf("release calls = %d, want the lease released once without waiting on the hung Slot", released)
	}
	if heldRunner(hung.bundle, hung.queryGroup) != nil {
		t.Fatal("the declined Query Group is still a current runner")
	}
	for time.Now().Before(bound) && len(lastDeclined(hung.owner)) == 0 {
		time.Sleep(time.Millisecond)
	}
	if declined := lastDeclined(hung.owner); len(declined) != 1 || declined[0] != (ownership.DeclinedQueryGroup{
		QueryGroup: string(hung.queryGroup), Stage: execution.SlotStageQuery,
	}) {
		t.Fatalf("registration declines %+v, want the Query Group with the stage it hangs in", declined)
	}
	// Still desired here until a leader moves it: not opened again, and the
	// replica stays ready.
	if err := hung.bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if heldRunner(hung.bundle, hung.queryGroup) != nil {
		t.Fatal("the reconcile opened the declined Query Group again")
	}
	if snapshot := hung.health.HealthSnapshot(); snapshot.State != observability.HealthReady {
		t.Fatalf("health with a declined Query Group still assigned = %+v, want ready", snapshot)
	}
	// The hung execution returns: the decline is lifted.
	hung.letGo()
	<-hung.ran
	if declined := hung.bundle.declinedRegistration(); declined != nil {
		t.Fatalf("declines after the hung execution returned = %+v, want none", declined)
	}
	// A replica started afresh carries nothing over.
	fresh := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(),
		&fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{hung.queryGroup}},
		&fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{hung.queryGroup}, runner: newFakePhaseTwoQueryGroup()})
	if declined := fresh.declinedRegistration(); declined != nil {
		t.Fatalf("a fresh replica declines %+v, want none", declined)
	}
}

// The deadline the dispatcher queued the Query Group by is the frozen Slot's
// first attempt's. A replay of a taken-over Slot runs minutes past it by
// design, under the deadline access derives from its own arrival, and is not
// hung for that. Before access has derived it the execution has no deadline
// of its own and is not judged at all; after, it is judged by that one.
func TestTheDispatchersDeadlineIsNoReasonToDeclineAReplay(t *testing.T) {
	queuedBy := time.Now().Add(-time.Hour)
	hung := startHungCase(t, func(runner *hungQueryGroup) {
		runner.nextDeadline = func() time.Time { return queuedBy }
	})
	// On the millisecond, as the marker keeps deadlines.
	now := time.UnixMilli(time.Now().UnixMilli())
	hung.bundle.declineHungExecutions(now)
	hung.stillHeld(t, "a replay with no deadline of its own yet, an hour past the dispatcher's")

	own := now.Add(55 * time.Second)
	hung.runner.marker.Enter(execution.SlotStageQuery)
	hung.runner.marker.Extend(own, now)
	hung.bundle.declineHungExecutions(own.Add(executionPastDeadlineGrace))
	hung.stillHeld(t, "a replay inside its own deadline and grace")
	hung.letGo()
	if err := <-hung.ran; err != nil {
		t.Fatalf("the replay's round = %v", err)
	}
}

// An execution that has begun to commit is never declined, however late:
// its output is written or being written, and releasing the lease before its
// State write would leave acknowledged output unapplied, for the next holder
// to send again under the same event identities. Its commit runs to the end.
func TestAnExecutionInsideItsCommitIsNeverDeclined(t *testing.T) {
	hung := startHungCase(t, nil)
	deadline := time.Now().Add(-time.Hour)
	hung.runner.marker.Extend(deadline, deadline)
	if err := execution.EnterCommit(execution.WithStageMarker(context.Background(), &hung.runner.marker), execution.SlotStageOutput); err != nil {
		t.Fatalf("EnterCommit = %v", err)
	}
	hung.bundle.declineHungExecutions(time.Now())
	hung.stillHeld(t, "an hour past its deadline, inside its commit")
	hung.letGo()
	if err := <-hung.ran; err != nil {
		t.Fatalf("the committing Slot's round = %v", err)
	}
}
