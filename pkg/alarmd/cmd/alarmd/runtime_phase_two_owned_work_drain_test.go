// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// supplementingPhaseTwoQueryGroup is a fake Query Group whose supplement
// holds until the test lets it go, as one would while its events are on
// their way to the broker.
type supplementingPhaseTwoQueryGroup struct {
	*fakePhaseTwoQueryGroup
	started chan struct{}
	finish  chan struct{}
	mu      sync.Mutex
	calls   int
}

func newSupplementingPhaseTwoQueryGroup() *supplementingPhaseTwoQueryGroup {
	return &supplementingPhaseTwoQueryGroup{fakePhaseTwoQueryGroup: newFakePhaseTwoQueryGroup(),
		started: make(chan struct{}, 1), finish: make(chan struct{})}
}

func (runner *supplementingPhaseTwoQueryGroup) Supplement(
	context.Context, execution.EvaluationTime, int64, execution.SupplementScope,
) (execution.SupplementFacts, error) {
	runner.mu.Lock()
	runner.calls++
	runner.mu.Unlock()
	runner.started <- struct{}{}
	<-runner.finish
	return execution.SupplementFacts{}, nil
}

func (runner *supplementingPhaseTwoQueryGroup) supplementCalls() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls
}

func supplementJobFor(queryGroup execution.QueryGroupIdentity) lookback.SupplementJob {
	return lookback.SupplementJob{QueryGroup: queryGroup, EvaluationTime: 1_799_999_940,
		Series: []execution.SeriesIdentityDigest{"a"}, Read: &lookback.KeptRead{}, Deadline: time.Now().Add(time.Minute)}
}

// supplementRefusedWithin runs a supplement that should be refused, and
// fails the test if it is still running after the signal bound - one that
// ran would hold on the test's fake until the test let it go, and the test
// would wait on itself.
func supplementRefusedWithin(t *testing.T, supplement func(context.Context, lookback.SupplementJob) lookback.SupplementOutcome,
	job lookback.SupplementJob, what string,
) lookback.SupplementOutcome {
	t.Helper()
	got := make(chan lookback.SupplementOutcome, 1)
	go func() { got <- supplement(context.Background(), job) }()
	select {
	case outcome := <-got:
		return outcome
	case <-time.After(signalWaitBound):
		t.Fatalf("%s ran instead of being refused", what)
		return lookback.SupplementOutcome{}
	}
}

func drainingNow(bundle *phaseTwoWorkerBundle) bool {
	bundle.mu.RLock()
	defer bundle.mu.RUnlock()
	return bundle.draining
}

func waitForRelease(t *testing.T, runner *fakePhaseTwoQueryGroup) int {
	t.Helper()
	deadline := time.Now().Add(signalWaitBound)
	for runner.releaseCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return runner.releaseCount()
}

// A supplement writes its events under the Query Group's lease, as a Slot
// does, so a Query Group moved away while one is mid-send is released only
// after it returns: released before, the next owner could take the lease at
// once and send the same window again. And once the move is applied, no
// supplement starts on the Query Group here.
func TestAReassignedQueryGroupIsReleasedOnlyAfterItsSupplementReturns(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	first := newSupplementingPhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{queryGroup},
		runners:  map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{queryGroup: first},
	}
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	var finishOnce sync.Once
	letSupplementFinish := func() { finishOnce.Do(func() { close(first.finish) }) }
	defer letSupplementFinish()
	waitSignal(t, first.leaseStarted, "lease maintenance")

	ownership := &lookbackOwnership{}
	ownership.bind(bundle)
	supplement := lookbackSupplement(ownership, nil, time.Now, waitWithin)
	outcome := make(chan lookback.SupplementOutcome, 1)
	go func() { outcome <- supplement(context.Background(), supplementJobFor(queryGroup)) }()
	waitSignal(t, first.started, "the supplement mid-send")

	control.queryGroups = nil
	owner.setAssigned(nil)
	if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
		t.Fatalf("refreshAndReconcile() error = %v", err)
	}
	if heldRunner(bundle, queryGroup) != nil {
		t.Fatal("the moved Query Group is still a current runner")
	}
	if refused := supplementRefusedWithin(t, supplement, supplementJobFor(queryGroup), "a supplement after the move"); refused.Ran || refused.Refused != lookback.DirectedFailed {
		t.Fatalf("a supplement after the move = %+v, want it refused without running", refused)
	}
	if calls := first.supplementCalls(); calls != 1 {
		t.Fatalf("supplements run = %d, want only the one in flight", calls)
	}
	time.Sleep(100 * time.Millisecond)
	if released := first.releaseCount(); released != 0 {
		t.Fatalf("lease released %d time(s) while its supplement was still sending", released)
	}
	letSupplementFinish()
	if got := <-outcome; !got.Ran {
		t.Fatalf("the supplement in flight = %+v, want it run", got)
	}
	if released := waitForRelease(t, first.fakePhaseTwoQueryGroup); released != 1 {
		t.Fatalf("release calls after the supplement returned = %d, want exactly 1", released)
	}
}

// A stop waits for a supplement mid-send before releasing the lease it
// writes under, inside the stop's own bound, and refuses a new one once it
// is draining.
func TestAStopWaitsForTheSupplementMidSendBeforeReleasing(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	first := newSupplementingPhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{
		assigned: []execution.QueryGroupIdentity{queryGroup},
		runners:  map[execution.QueryGroupIdentity]phaseTwoQueryGroupRuntime{queryGroup: first},
	}
	cfg := validGoAccessRuntimeConfig()
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var finishOnce sync.Once
	letSupplementFinish := func() { finishOnce.Do(func() { close(first.finish) }) }
	defer letSupplementFinish()
	waitSignal(t, first.leaseStarted, "lease maintenance")

	ownership := &lookbackOwnership{}
	ownership.bind(bundle)
	supplement := lookbackSupplement(ownership, nil, time.Now, waitWithin)
	go func() { _ = supplement(context.Background(), supplementJobFor(queryGroup)) }()
	waitSignal(t, first.started, "the supplement mid-send")

	bound := 2*cfg.ShutdownTimeout.Duration() + 3*time.Second
	stopCtx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	stopped := make(chan error, 1)
	began := time.Now()
	go func() { stopped <- bundle.Shutdown(stopCtx) }()
	deadline := time.Now().Add(signalWaitBound)
	for !drainingNow(bundle) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if refused := supplementRefusedWithin(t, supplement, supplementJobFor(queryGroup), "a supplement while draining"); refused.Ran {
		t.Fatalf("a supplement while draining = %+v, want it refused", refused)
	}
	time.Sleep(100 * time.Millisecond)
	if released := first.releaseCount(); released != 0 {
		t.Fatalf("lease released %d time(s) while its supplement was still sending", released)
	}
	letSupplementFinish()
	if err := <-stopped; err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if took := time.Since(began); took > bound {
		t.Fatalf("the stop took %s, past its bound %s", took, bound)
	}
	if released := first.releaseCount(); released != 1 {
		t.Fatalf("release calls after the supplement returned = %d, want exactly 1", released)
	}
}

// An inactive-time close is the other send on the lease outside a Slot:
// counted the same way, and refused, unsent, for a Query Group this replica
// no longer holds.
func TestAnInactiveCloseIsCountedOnItsQueryGroupAndRefusedOnceItIsNotHeld(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "critical")})
	var queryGroup execution.QueryGroupIdentity
	var lifecycle *phaseTwoQueryGroupLifecycle
	for held, current := range f.m.bundle.runners {
		queryGroup, lifecycle = held, current
	}
	var countedDuringSend int
	f.writer.onWrite = func() { countedDuringSend, _ = f.m.bundle.slotsInFlight(lifecycle) }
	f.m.step(context.Background())
	if len(f.writer.batches) != 1 || countedDuringSend != 1 {
		t.Fatalf("batches=%v counted during the send=%d, want one batch sent while counted", f.writer.batches, countedDuringSend)
	}
	if counted, _ := f.m.bundle.slotsInFlight(lifecycle); counted != 0 {
		t.Fatalf("still counted after the send: %d", counted)
	}

	f.m.bundle.mu.Lock()
	delete(f.m.bundle.runners, queryGroup)
	f.m.bundle.mu.Unlock()
	sent, err := f.m.sendClose(context.Background(), queryGroup, f.runner, f.m.catalog.(*maintenanceTestCatalog).plans[0],
		openalerts.StrategyKey{TenantID: "tenant-a", StrategyID: "123"}, nil)
	if sent != 0 || !errors.Is(err, errClosePrecheck) || !errors.Is(err, errCloseNotHeld) || len(f.writer.batches) != 1 {
		t.Fatalf("a close for a Query Group no longer held: sent=%d err=%v batches=%d, want refused before the send",
			sent, err, len(f.writer.batches))
	}
}

// Work on a lease starts only on a Query Group held here by a replica that
// is not stopping: draining, the Slots in flight are finishing and nothing
// new is to begin before the leases go; detached, the lease is on its way
// to another owner. Each start is counted on the lifecycle, and the last
// one out closes idle.
func TestOwnedWorkStartsOnlyOnAHeldQueryGroupOfAReplicaNotStopping(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: newFakePhaseTwoQueryGroup()}
	bundle := &phaseTwoWorkerBundle{runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{queryGroup: lifecycle}}

	first, ok := bundle.enterOwnedWork(queryGroup)
	if !ok || first != lifecycle {
		t.Fatal("work on a held Query Group was refused")
	}
	// A handover that began waiting with one piece of work in flight waits
	// on the channel it was handed, and a second start must not replace it.
	_, idle := bundle.slotsInFlight(lifecycle)
	second, _ := bundle.enterOwnedWork(queryGroup)
	if counted, _ := bundle.slotsInFlight(lifecycle); counted != 2 || idle == nil {
		t.Fatalf("counted %d, want 2 with idle open", counted)
	}
	bundle.exitOwnedWork(first)
	if channelClosed(idle) {
		t.Fatal("idle closed with work still counted")
	}
	bundle.exitOwnedWork(second)
	if !channelClosed(idle) {
		t.Fatal("idle still open after the last work left")
	}

	bundle.mu.Lock()
	bundle.draining = true
	bundle.mu.Unlock()
	if _, ok := bundle.enterOwnedWork(queryGroup); ok {
		t.Fatal("work started on a draining replica")
	}
	bundle.mu.Lock()
	bundle.draining = false
	delete(bundle.runners, queryGroup)
	bundle.mu.Unlock()
	if _, ok := bundle.enterOwnedWork(queryGroup); ok {
		t.Fatal("work started on a detached Query Group")
	}
	if counted, _ := bundle.slotsInFlight(lifecycle); counted != 0 {
		t.Fatalf("a refused start was counted: %d", counted)
	}
}
