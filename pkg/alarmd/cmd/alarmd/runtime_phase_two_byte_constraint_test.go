// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The Control Leader moves for the byte constraint before it corrects the
// counts (decision-020 section 5.7). The incident shape: three Query
// Groups of one strategy on one replica, each peaking at 35% of its pool -
// each inside its own share, together 105% of the pool, refused thirteen
// times every ten minutes with the counts perfectly even for all anyone
// could see. The Workers' heartbeats report the peaks, the registrations
// the pools; the round judges the sums, moves the largest to the Worker
// with the most headroom, then evens the counts under the same readings.
// After the round no Worker holds more than the share and the three are
// no longer on one replica; the ledger forgets what moved until its new
// holder reports it.
func TestProductionLeaderMovesForTheByteConstraintBeforeItCorrectsTheCounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const pool = uint64(1 << 30)
	peak := pool / 100 * 35
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
			Load: &ownership.WorkerLoad{RetainedPoolBytes: pool},
		}
	}
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	store := &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		workers:                    []ownership.WorkerRegistration{worker("worker-1"), worker("worker-2"), worker("worker-3")},
		assignments:                map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
	}
	for _, queryGroup := range groups {
		store.assignments[queryGroup] = ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1,
			ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		}
	}
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	costs := scheduler.NewCostLedger(func() time.Time { return now })
	var observations []observability.Observation
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Hour, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
		SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
		Costs: costs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Hour); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	lastPlanned := func(t *testing.T) *observability.RebalanceFacts {
		t.Helper()
		var found *observability.RebalanceFacts
		for _, observation := range observations {
			if observation.Stage == observability.StageRebalancePlanned && observation.Result == observability.ResultSuccess {
				found = observation.Rebalance
			}
		}
		if found == nil || found.Bytes == nil {
			t.Fatalf("no rebalance observation with the byte-constraint facts: %+v", found)
		}
		return found
	}
	owners := func() map[execution.QueryGroupIdentity]string {
		result := map[execution.QueryGroupIdentity]string{}
		for queryGroup, record := range store.assignments {
			result[queryGroup] = record.DesiredWorkerID
		}
		return result
	}

	// The heartbeats arrive through the stream's sink, as they do from a
	// Worker: worker-1 reports its three.
	sink := costLedgerSink{ledger: costs}
	sink.RecordCosts("worker-1", []viewstream.QueryGroupCost{
		{QueryGroup: "query-group-1", RetainedBytesPeak: peak}, {QueryGroup: "query-group-2", RetainedBytesPeak: peak}, {QueryGroup: "query-group-3", RetainedBytesPeak: peak},
	})

	// Round one: a new Leader plans and publishes nothing for one window.
	if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
		t.Fatalf("PublishAssignments(round 1) error = %v", err)
	}
	facts := lastPlanned(t)
	if facts.Bytes.Judged != 3 || len(facts.Bytes.PoolUnknown) != 0 || facts.Bytes.Unread != 0 ||
		!reflect.DeepEqual(facts.Bytes.Overloaded, []string{"worker-1"}) || facts.Bytes.PlannedMoves != 1 || facts.Bytes.PublishedMoves != 0 || !facts.Bytes.Paused {
		t.Fatalf("round 1 byte facts = %+v, want worker-1 judged overloaded, one move planned and none published while the window runs", facts.Bytes)
	}
	if store.published != 0 {
		t.Fatalf("round 1 published %d moves", store.published)
	}

	// Round two, the window over: the byte move is published first, then
	// the count correction moves one more under the same readings.
	at := now.Add(40 * time.Second)
	if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
		t.Fatalf("PublishAssignments(round 2) error = %v", err)
	}
	facts = lastPlanned(t)
	if facts.Bytes.PlannedMoves != 1 || facts.Bytes.PublishedMoves != 1 || facts.Bytes.Conflicts != 0 || facts.Bytes.Paused ||
		len(facts.Bytes.Moves) != 1 || facts.Bytes.Moves[0].From != "worker-1" || facts.Bytes.Moves[0].Bytes != peak {
		t.Fatalf("round 2 byte facts = %+v, want the one byte move published", facts.Bytes)
	}
	if facts.PlannedMoves != 1 || facts.PublishedMoves != 1 {
		t.Fatalf("round 2 count facts = %+v, want one count move after the byte move", facts)
	}
	after := owners()
	if after["query-group-1"] == after["query-group-2"] && after["query-group-2"] == after["query-group-3"] {
		t.Fatalf("owners after round 2 = %v: the three are still on one replica", after)
	}
	sums := map[string]uint64{}
	for _, owner := range after {
		sums[owner] += peak
	}
	for workerID, sum := range sums {
		if sum > pool/100*scheduler.ByteConstraintPercent {
			t.Fatalf("after round 2 %s holds %d of a %d pool, past the share", workerID, sum, pool)
		}
	}
	// Every move went out under the word every reader accepts today.
	for _, decision := range store.decisions {
		if decision.PlacementReason != ownership.PlacementRebalance {
			t.Fatalf("decision %+v, want REBALANCE until every reader accepts BYTE_CONSTRAINT", decision)
		}
	}
	// The ledger follows the round's owners: the moved Query Groups' readings
	// are carried over to their new holders as provisional until those
	// holders report them, so no destination looks empty next round.
	entries := costs.Entries()
	if len(entries) != 3 {
		t.Fatalf("ledger after round 2 = %+v, want every Query Group under its holder", entries)
	}
	for _, entry := range entries {
		if after[entry.QueryGroup] != entry.WorkerID || entry.Provisional != (entry.WorkerID != "worker-1") {
			t.Fatalf("ledger entry %+v: want it under the round's owner, provisional where it moved", entry)
		}
	}
	// The fleet snapshot carries the same round.
	if published := production.LastRebalance(); published == nil || published.Bytes == nil || published.Bytes.PublishedMoves != 1 ||
		len(published.Bytes.Sums) != 3 || published.Bytes.Sums[0].WorkerID != "worker-1" || published.Bytes.Sums[0].PeakSumBytes != 3*peak {
		t.Fatalf("fleet rebalance facts = %+v, want the byte round with worker-1's sum", published)
	}

	// Round three: the new holders have not reported, but the carried-over
	// readings keep every sum whole - nothing unread, nothing unsettled,
	// nothing over the share, nothing moves.
	at = at.Add(5 * time.Second)
	if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
		t.Fatalf("PublishAssignments(round 3) error = %v", err)
	}
	facts = lastPlanned(t)
	if facts.Bytes.Unread != 0 || len(facts.Bytes.Unsettled) != 0 || len(facts.Bytes.Overloaded) != 0 || facts.Bytes.PlannedMoves != 0 || facts.PlannedMoves != 0 {
		t.Fatalf("round 3 facts = %+v (bytes %+v), want nothing unread, unsettled, overloaded or moved", facts, facts.Bytes)
	}
	if published := production.LastRebalance(); published == nil || published.Bytes == nil || len(published.Bytes.Sums) != 3 {
		t.Fatalf("fleet byte facts after round 3 = %+v", published)
	} else {
		for _, sum := range published.Bytes.Sums {
			if sum.PeakSumBytes != peak {
				t.Fatalf("round 3 sums = %+v, want one peak on each worker, the moved ones carried over", published.Bytes.Sums)
			}
		}
	}
}

// Without readings the round judges nothing and says so: every ready
// Worker is listed as pool unknown when its registration carries none, and
// the count correction plans as it always did.
func TestProductionLeaderSaysWhatTheByteConstraintCouldNotJudge(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
		}
	}
	store := &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		workers:                    []ownership.WorkerRegistration{worker("worker-1"), worker("worker-2")},
		assignments: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{"query-group-1": {
			QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1,
			ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		}},
	}
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(nil), store)
	if err != nil {
		t.Fatal(err)
	}
	var observations []observability.Observation
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Hour, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
		SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Hour); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if err := production.PublishAssignments(context.Background(), []execution.QueryGroupIdentity{"query-group-1"}, now); err != nil {
		t.Fatal(err)
	}
	var facts *observability.RebalanceFacts
	for _, observation := range observations {
		if observation.Stage == observability.StageRebalancePlanned {
			facts = observation.Rebalance
		}
	}
	if facts == nil || facts.Bytes == nil || facts.Bytes.Judged != 0 || !reflect.DeepEqual(facts.Bytes.PoolUnknown, []string{"worker-1", "worker-2"}) ||
		facts.Bytes.PlannedMoves != 0 || facts.Bytes.SharePercent != scheduler.ByteConstraintPercent {
		t.Fatalf("facts = %+v, want both workers listed as not judged and no byte move", facts.Bytes)
	}
	if facts.ReadyWorkers != 2 || facts.Assigned != 1 {
		t.Fatalf("count facts = %+v, want the count correction planned as before", facts)
	}
}

// The Worker's registration carries the pool the Leader judges it by: the
// same number the coordinator holds Slots under, never re-derived by the
// Leader from the memory limit. A registration without a capacity source
// carries no load and so no pool, and the Leader lists that Worker as not
// judged.
func TestTheWorkerRegistrationCarriesItsRetainedPool(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Coordinator.MaxRetainedBytes = 1 << 28
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Config: cfg}}
	if load := bundle.loadFacts(); load != nil {
		t.Fatalf("load without a capacity source = %+v, want none", load)
	}
	bundle.capacity = func() *fleet.Capacity { return &fleet.Capacity{PermitBudget: 4, MemoryLimit: 1 << 30} }
	load := bundle.loadFacts()
	if load == nil || load.RetainedPoolBytes != 1<<28 || load.MemoryLimitBytes != 1<<30 || load.PermitBudget != 4 {
		t.Fatalf("load = %+v, want the configured pool beside the capacity", load)
	}
}
