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
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The worker holds one session for as long as it runs: it acquires it,
// renews it on the interval, and once the session is gone -- here released
// from outside, as a session that lapsed is -- it does not renew it again but
// acquires a new one under a larger epoch. Released at shutdown, the Control
// Leader reads every session up to that epoch as lapsed. Each attempt is a
// lease_renewed line on the session's identity.
func TestAWorkerHoldsOneSessionAndReplacesOneItLost(t *testing.T) {
	store := newViewGateTestStore(t)
	var mu sync.Mutex
	var lines []observability.Observation
	runtime := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil), dependencies: productionPhaseTwoOwnershipDependencies{
		Store: store, Now: time.Now,
		Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
			if o.Stage == observability.StageLeaseRenewed {
				mu.Lock()
				lines = append(lines, o)
				mu.Unlock()
			}
		}),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.MaintainWorkerSession(ctx, "worker-1", 50*time.Millisecond, time.Second) }()

	var first ownership.WorkerSession
	testwait.Until(t, "the session acquired", func() bool {
		var held bool
		first, held = runtime.workerSession.current()
		return held
	})
	if err := store.ReleaseWorkerSession(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	var second ownership.WorkerSession
	testwait.Until(t, "a new session after the lost one", func() bool {
		session, held := runtime.workerSession.current()
		second = session
		return held && session.Epoch > first.Epoch
	})
	cancel()
	<-done
	if err := runtime.ReleaseWorkerSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	facts, err := store.ReadWorkerSessions(context.Background(), []string{"worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	if through, ok := facts["worker-1"].LapsedThrough(); !ok || through != second.Epoch {
		t.Fatalf("after shutdown the sessions lapsed through (%d, %t), want through %d", through, ok, second.Epoch)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) == 0 {
		t.Fatal("no lease_renewed line for the session")
	}
	for _, line := range lines {
		if line.Trace.QueryGroupKey != string(ownership.WorkerSessionIdentity("worker-1")) {
			t.Fatalf("a session line traced %q, want the session's identity", line.Trace.QueryGroupKey)
		}
	}
}

// decisionRecordingStore is the real ownership store, keeping each decision
// published through it.
type decisionRecordingStore struct {
	*ownership.RedisStore
	decisions []ownership.AssignmentDecision
}

func (store *decisionRecordingStore) PublishAssignmentReporting(ctx context.Context, authority ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, ownership.HolderKind, error) {
	store.decisions = append(store.decisions, decision)
	return store.RedisStore.PublishAssignmentReporting(ctx, authority, decision)
}

func (store *decisionRecordingStore) PublishAssignment(ctx context.Context, authority ownership.PublicationAuthority, decision ownership.AssignmentDecision) (ownership.AssignmentRecord, error) {
	store.decisions = append(store.decisions, decision)
	return store.RedisStore.PublishAssignment(ctx, authority, decision)
}

// A rebalance move names the worker it takes the Query Group from as the
// holder whose session the publication reads, so a move away from a holder
// that is gone does not wait out a grace.
func TestARebalanceMoveNamesTheWorkerItTakesFrom(t *testing.T) {
	ctx := context.Background()
	redisStore := newViewGateTestStore(t)
	store := &decisionRecordingStore{RedisStore: redisStore}
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := redisStore.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	record, err := redisStore.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: "qg-1", DesiredWorkerID: "worker-from", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Query Group is held under worker-from's session, so the move is
	// counted as one off a session holder.
	session, err := redisStore.AcquireWorkerSession(ctx, "worker-from", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holder := &workerSessionHolder{}
	holder.set(session, true)
	if _, err := ownership.OpenSessionUnder(ctx, redisStore, "qg-1", holder.current, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	runtime := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil), dependencies: productionPhaseTwoOwnershipDependencies{Store: store, Now: time.Now}}
	outcome, err := runtime.publishMoves(ctx, authority, []scheduler.RebalanceMove{{QueryGroup: "qg-1", From: "worker-from", To: "worker-to"}},
		map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"qg-1": record}, true, 0, now)
	if err != nil || len(outcome.applied) != 1 {
		t.Fatalf("publishMoves() = (%+v, %v), want the move applied", outcome, err)
	}
	if len(store.decisions) != 1 || store.decisions[0].HolderWorkerID != "worker-from" {
		t.Fatalf("published decisions %+v, want one naming worker-from as the holder", store.decisions)
	}
	if outcome.fromSessionHolders != 1 {
		t.Fatalf("moves off session holders = %d, want the one", outcome.fromSessionHolders)
	}
}

// restateWorkerSession renews the bundle's worker session now, on the clock
// the bundle runs by: a test that moves that clock past the session's TTL
// faster than the session's renewal ticks keeps it alive with this, as the
// renewal would on a clock that ran.
func restateWorkerSession(t *testing.T, bundle *phaseTwoWorkerBundle) {
	t.Helper()
	production, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if !ok {
		t.Fatal("the bundle holds no production ownership")
	}
	session, held := production.workerSession.current()
	store, sessions := production.dependencies.Store.(workerSessionStore)
	if !held || !sessions {
		t.Fatal("the bundle holds no worker session")
	}
	renewed, err := store.RenewWorkerSession(context.Background(), session, production.dependencies.Now(),
		bundle.dependencies.Config.PhaseTwo.Ownership.LeaseTTL.Duration())
	if err != nil {
		t.Fatal(err)
	}
	production.workerSession.set(renewed, true)
}

// A Query Group's lease held under the worker session is lost as the session
// is: its loop does not call it lost because the session's deadline passed
// on the process's clock -- a renewal may yet restate it -- and reports the
// loss once the session's own loop lets the session go.
func TestALeaseUnderTheSessionIsLostOnlyWhenTheSessionIs(t *testing.T) {
	ctx := context.Background()
	store := newViewGateTestStore(t)
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := store.AcquireWorkerSession(ctx, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	holder := &workerSessionHolder{}
	holder.set(worker, true)
	session, err := ownership.OpenSessionUnder(ctx, store, "qg-1", holder.current, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &productionPhaseTwoQueryGroup{session: session, queryGroup: "qg-1", now: time.Now}
	stale := worker
	stale.Deadline = time.Now().Add(-time.Second)
	holder.set(stale, true)
	done := make(chan error, 1)
	go func() { done <- runtime.MaintainLease(ctx, 20*time.Millisecond, time.Second) }()
	select {
	case err := <-done:
		t.Fatalf("MaintainLease() = %v for a session whose deadline only passed on this clock", err)
	case <-time.After(150 * time.Millisecond): // fixed-wait: several of the loop's ticks, none of which may report a loss
	}
	holder.set(worker, false)
	select {
	case err := <-done:
		if !errors.Is(err, ownership.ErrStaleFence) {
			t.Fatalf("MaintainLease() = %v once the session was let go, want ErrStaleFence", err)
		}
	case <-testwait.Expired(t):
		t.Fatal("the loss of the session was not reported")
	}
}

// unreachableSessionStore is the ownership store with its worker session
// calls failing, once cut, as a store that cannot be reached does: with no
// answer, which is no decision about the session.
type unreachableSessionStore struct {
	*ownership.RedisStore
	cut atomic.Bool
}

var errSessionStoreUnreachable = errors.New("dial tcp 192.0.2.1:6379: connect: connection refused")

func (store *unreachableSessionStore) AcquireWorkerSession(ctx context.Context, workerID string, at time.Time, ttl time.Duration) (ownership.WorkerSession, error) {
	if store.cut.Load() {
		return ownership.WorkerSession{}, errSessionStoreUnreachable
	}
	return store.RedisStore.AcquireWorkerSession(ctx, workerID, at, ttl)
}

func (store *unreachableSessionStore) RenewWorkerSession(ctx context.Context, session ownership.WorkerSession, at time.Time, ttl time.Duration) (ownership.WorkerSession, error) {
	if store.cut.Load() {
		return ownership.WorkerSession{}, errSessionStoreUnreachable
	}
	return store.RedisStore.RenewWorkerSession(ctx, session, at, ttl)
}

// A store that cannot be reached never says the worker session is gone, so
// the session's loop lets it go itself once its deadline has passed on this
// process's clock, and the Query Groups held under it are reported lost then:
// not before the deadline, and not only when the store answers again.
func TestAWorkerSessionTheStoreCannotRenewIsLetGoAtItsDeadline(t *testing.T) {
	ctx := context.Background()
	store := &unreachableSessionStore{RedisStore: newViewGateTestStore(t)}
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil), dependencies: productionPhaseTwoOwnershipDependencies{
		Store: store, Now: time.Now, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- runtime.MaintainWorkerSession(loopCtx, "worker-1", 50*time.Millisecond, 400*time.Millisecond)
	}()
	testwait.Until(t, "the session acquired", func() bool {
		_, held := runtime.workerSession.current()
		return held
	})
	session, err := ownership.OpenSessionUnder(ctx, store, "qg-1", runtime.workerSession.current, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := &productionPhaseTwoQueryGroup{session: session, queryGroup: "qg-1", now: time.Now}
	lost := make(chan error, 1)
	go func() { lost <- queryGroup.MaintainLease(loopCtx, 20*time.Millisecond, time.Second) }()

	store.cut.Store(true)
	last, _ := runtime.workerSession.current()
	select {
	case err := <-lost:
		lostAt := time.Now()
		if !errors.Is(err, ownership.ErrStaleFence) {
			t.Fatalf("MaintainLease() = %v once the session was let go, want ErrStaleFence", err)
		}
		if lostAt.Before(last.Deadline) {
			t.Fatalf("the Query Group was lost %s before the session's deadline", last.Deadline.Sub(lostAt))
		}
	case <-testwait.Expired(t):
		t.Fatal("the session the store could not renew was not let go, and its Query Group not reported lost")
	}
	if _, held := runtime.workerSession.current(); held {
		t.Fatal("the session is still held with the store unreachable past its deadline")
	}
	cancel()
	<-sessionDone
}

// A Query Group already open is not opened again by the next round of the
// same Assignment. Under a worker session the same process may take its own
// lease back, so a second open would be granted with a new token and the
// running Runner's next write refused; it is not the harmless BUSY it was.
func TestARoundOfTheSameAssignmentDoesNotOpenAnOpenQueryGroupAgain(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	queryGroups := []execution.QueryGroupIdentity{"query-group-1"}
	runner := newFakePhaseTwoQueryGroup()
	owner := &fakePhaseTwoOwnership{assigned: queryGroups, runner: runner}
	bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), &fakePhaseTwoControl{queryGroups: queryGroups}, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	waitSignal(t, runner.leaseStarted, "query-group lease maintenance")
	for round := 0; round < 2; round++ {
		if err := bundle.applyAssignment(context.Background(), queryGroups); err != nil {
			t.Fatalf("round %d of the same Assignment: %v", round, err)
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.opens != 1 {
		t.Fatalf("OpenQueryGroup called %d times over three rounds of one Assignment, want once", owner.opens)
	}
}

// At startup the worker takes its session before the startup Assignment opens
// a Query Group, so those leases are held under it. A session it cannot have
// yet -- this worker id's last process still holds one, as after a container
// restart -- leaves the Query Groups to wait for the loop to take one: an open
// meanwhile is refused as busy, not granted as a lease its holder renews itself.
func TestAWorkerTakesItsSessionBeforeItOpensAQueryGroupAtStartup(t *testing.T) {
	ctx := context.Background()
	store := newViewGateTestStore(t)
	now := time.Now()
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, worker := range []string{"worker-1", "worker-2"} {
		if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
			QueryGroup: execution.QueryGroupIdentity("qg-" + worker), DesiredWorkerID: worker, PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	open := func(worker string) (*productionPhaseTwoOwnership, *ownership.Session, error) {
		runtime := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil), dependencies: productionPhaseTwoOwnershipDependencies{
			Store: store, Now: time.Now, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
		}}
		runtime.OpenWorkerSessionAtStartup(ctx, worker, 50*time.Millisecond, time.Minute)
		if !runtime.workerSession.running() {
			t.Fatalf("%s: the session is not what its leases are taken under after startup", worker)
		}
		session, err := ownership.OpenSessionUnder(ctx, store, execution.QueryGroupIdentity("qg-"+worker), runtime.workerSession.current, time.Now(), time.Minute)
		return runtime, session, err
	}

	_, session, err := open("worker-1")
	if err != nil || !session.HeldUnderSession() {
		t.Fatalf("worker-1 at startup: (%v, %v), want its Query Group held under the session it took", session, err)
	}

	// worker-2's last process still holds its session.
	if _, err := store.AcquireWorkerSession(ctx, "worker-2", time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	runtime, _, err := open("worker-2")
	if _, held := runtime.workerSession.current(); held {
		t.Fatal("worker-2 holds a session while its last process's is alive")
	}
	if !errors.Is(err, ownership.ErrLeaseBusy) {
		t.Fatalf("worker-2's Query Group at startup = %v, want it refused as busy until the session is taken", err)
	}

	// A cadence the session's loop refuses is never maintained, so its
	// leases are not taken under a session that will not come.
	invalid := &productionPhaseTwoOwnership{records: newLeaderRecordCache(store, nil), dependencies: productionPhaseTwoOwnershipDependencies{
		Store: store, Now: time.Now, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	invalid.OpenWorkerSessionAtStartup(ctx, "worker-3", time.Minute, time.Minute)
	if invalid.workerSession.running() {
		t.Fatal("a session the loop will not maintain is what leases are taken under")
	}
}
