// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func rebalanceWorker(id string, readiness ownership.AssignmentReadiness, expires time.Time, profile string) ownership.WorkerRegistration {
	return ownership.WorkerRegistration{
		WorkerID: id, AssignmentReadiness: readiness, DependencyStatus: ownership.DependencyHealthy,
		DeploymentProfile: profile, CapabilitiesDigest: "capabilities", ExpiresAt: expires,
	}
}

func rebalanceOwners(counts map[string]int) map[execution.QueryGroupIdentity]string {
	owners := map[execution.QueryGroupIdentity]string{}
	for workerID, count := range counts {
		for index := 0; index < count; index++ {
			owners[execution.QueryGroupIdentity(fmt.Sprintf("%s-qg-%03d", workerID, index))] = workerID
		}
	}
	return owners
}

// A rebalance round moves from the single most loaded ready worker to the
// single least loaded one, at most the batch, never past the midpoint of
// the gap, and not at all while the gap is within the stop spread. Workers
// that are not ready do not take part, and Query Groups owned by them are
// left to rendezvous placement.
func TestRouterPlanRebalanceBoundsMovesByBatchGapAndSpread(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	ready := func(ids ...string) []ownership.WorkerRegistration {
		workers := make([]ownership.WorkerRegistration, 0, len(ids))
		for _, id := range ids {
			workers = append(workers, rebalanceWorker(id, ownership.WorkerReady, live, "shadow"))
		}
		return workers
	}
	for _, test := range []struct {
		name       string
		owners     map[execution.QueryGroupIdentity]string
		workers    []ownership.WorkerRegistration
		wantTarget int
		wantBatch  int
		wantMoves  int
		wantFrom   string
		wantTo     string
	}{
		{name: "an even split plans nothing", owners: rebalanceOwners(map[string]int{"a": 9, "b": 9}), workers: ready("a", "b"), wantTarget: 9, wantBatch: 1},
		{name: "a small fleet moves one per round", owners: rebalanceOwners(map[string]int{"a": 12, "b": 6}), workers: ready("a", "b"),
			wantTarget: 9, wantBatch: 1, wantMoves: 1, wantFrom: "a", wantTo: "b"},
		// gap 100, cap ceil(300/20) = 15: fifteen moves a round, the gap
		// closes in four rounds.
		{name: "a large fleet moves five percent a round", owners: rebalanceOwners(map[string]int{"a": 200, "b": 100}), workers: ready("a", "b"),
			wantTarget: 150, wantBatch: 15, wantMoves: 15, wantFrom: "a", wantTo: "b"},
		// The incident shape: everything on one of two replicas, 119 a round.
		{name: "a fleet entirely on one replica moves five percent a round", owners: rebalanceOwners(map[string]int{"a": 2370, "b": 0}), workers: ready("a", "b"),
			wantTarget: 1185, wantBatch: 119, wantMoves: 119, wantFrom: "a", wantTo: "b"},
		{name: "a gap of one is parity, not imbalance", owners: rebalanceOwners(map[string]int{"a": 9, "b": 8}), workers: ready("a", "b"), wantTarget: 8, wantBatch: 1},
		{name: "a gap within the stop spread plans nothing", owners: rebalanceOwners(map[string]int{"a": 102, "b": 98}), workers: ready("a", "b"), wantTarget: 100, wantBatch: 10},
		{name: "a newly ready worker that owns nothing is the destination", owners: rebalanceOwners(map[string]int{"a": 6, "b": 6}), workers: ready("a", "b", "c"),
			wantTarget: 4, wantBatch: 1, wantMoves: 1, wantFrom: "a", wantTo: "c"},
		{name: "a single ready worker has nowhere to move", owners: rebalanceOwners(map[string]int{"a": 12}), workers: ready("a")},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := NewRouter(nil).PlanRebalance(test.owners, test.workers, now)
			if plan.Target != test.wantTarget || plan.Batch != test.wantBatch || len(plan.Moves) != test.wantMoves {
				t.Fatalf("plan = %+v, want target %d batch %d moves %d", plan, test.wantTarget, test.wantBatch, test.wantMoves)
			}
			for _, move := range plan.Moves {
				if move.From != test.wantFrom || move.To != test.wantTo || test.owners[move.QueryGroup] != move.From {
					t.Fatalf("move %+v does not go from %q to %q over an owned Query Group", move, test.wantFrom, test.wantTo)
				}
			}
		})
	}
	t.Run("moves are deterministic and name the lowest identities first", func(t *testing.T) {
		owners := rebalanceOwners(map[string]int{"a": 200, "b": 100})
		first := NewRouter(nil).PlanRebalance(owners, ready("a", "b"), now)
		second := NewRouter(nil).PlanRebalance(owners, ready("b", "a"), now)
		want := make([]RebalanceMove, 0, 15)
		for index := 0; index < 15; index++ {
			want = append(want, RebalanceMove{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("a-qg-%03d", index)), From: "a", To: "b"})
		}
		if !reflect.DeepEqual(first.Moves, want) || !reflect.DeepEqual(second.Moves, want) {
			t.Fatalf("moves = %+v / %+v, want %+v", first.Moves, second.Moves, want)
		}
	})
	t.Run("workers that are not ready neither count nor receive", func(t *testing.T) {
		owners := rebalanceOwners(map[string]int{"a": 12, "b": 6, "gone": 30})
		workers := append(ready("a", "b"),
			rebalanceWorker("gone", ownership.WorkerDraining, live, "shadow"),
			rebalanceWorker("expired", ownership.WorkerReady, now, "shadow"))
		plan := NewRouter(nil).PlanRebalance(owners, workers, now)
		if plan.ReadyWorkers != 2 || plan.Assigned != 18 || !reflect.DeepEqual(plan.Owned, map[string]int{"a": 12, "b": 6}) || len(plan.Moves) != 1 {
			t.Fatalf("plan = %+v, want two ready workers owning 18 and one move", plan)
		}
	})
}

// The destination must be eligible for every Query Group the plan names;
// a ready worker the router would never select still shows in the
// distribution, but no move is planned towards it.
func TestRouterPlanRebalanceSkipsQueryGroupsTheDestinationIsNotEligibleFor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	eligibility, err := NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	owners := rebalanceOwners(map[string]int{"a": 12})
	workers := []ownership.WorkerRegistration{
		rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow"),
		rebalanceWorker("other-profile", ownership.WorkerReady, now.Add(time.Minute), "canary"),
	}
	plan := NewRouter(eligibility).PlanRebalance(owners, workers, now)
	if plan.ReadyWorkers != 2 || plan.MostOwned != 12 || plan.LeastOwned != 0 || len(plan.Moves) != 0 {
		t.Fatalf("plan = %+v, want the ineligible worker counted but no move towards it", plan)
	}
	workers[1] = rebalanceWorker("same-profile", ownership.WorkerReady, now.Add(time.Minute), "shadow")
	if plan := NewRouter(eligibility).PlanRebalance(owners, workers, now); len(plan.Moves) != 1 || plan.Moves[0].To != "same-profile" {
		t.Fatalf("plan = %+v, want one move towards the eligible worker", plan)
	}
}

func TestReconcilerPlanRebalanceDelegatesToItsRouter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var nilReconciler *Reconciler
	if plan := nilReconciler.PlanRebalance(nil, nil, now); plan.ReadyWorkers != 0 || plan.Owned == nil {
		t.Fatalf("nil reconciler plan = %+v", plan)
	}
	reconciler, err := NewReconciler(NewRouter(nil), fakeAssignmentStoreForPlan{})
	if err != nil {
		t.Fatal(err)
	}
	workers := []ownership.WorkerRegistration{
		rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow"),
		rebalanceWorker("b", ownership.WorkerReady, now.Add(time.Minute), "shadow"),
	}
	if plan := reconciler.PlanRebalance(rebalanceOwners(map[string]int{"a": 12, "b": 6}), workers, now); len(plan.Moves) != 1 {
		t.Fatalf("plan = %+v, want one move", plan)
	}
}

type fakeAssignmentStoreForPlan struct{}

func (fakeAssignmentStoreForPlan) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error) {
	return nil, nil
}

func (fakeAssignmentStoreForPlan) ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
}

func (fakeAssignmentStoreForPlan) PublishAssignment(context.Context, ownership.PublicationAuthority, ownership.AssignmentDecision) (ownership.AssignmentRecord, error) {
	return ownership.AssignmentRecord{}, nil
}

// Any skew between two replicas closes to within the stop spread in at most
// RebalanceConvergenceRounds when the plan's moves are applied round after
// round, and no round hands over more than a twentieth of the fleet. The
// bound is the point of the cap: the one-percent floor this replaced needed
// fifty-two rounds for two replicas with everything on one of them, which on
// a five-second cadence is over four minutes of one replica doing the work
// of two -- and the gap/(2K) term written to fix that decayed geometrically
// and left a third of the gap after K rounds. Three replicas move along one
// pair a round, so the bound there is per pair.
func TestRouterPlanRebalanceClosesAnySkewWithinTheConvergenceRounds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	for _, test := range []struct {
		name       string
		counts     map[string]int
		wantRounds int
	}{
		{name: "two replicas, everything on one", counts: map[string]int{"a": 2370, "b": 0}, wantRounds: RebalanceConvergenceRounds},
		{name: "two replicas, mild skew", counts: map[string]int{"a": 1300, "b": 1070}, wantRounds: RebalanceConvergenceRounds},
		{name: "two replicas, a small fleet", counts: map[string]int{"a": 30, "b": 0}, wantRounds: RebalanceConvergenceRounds},
		{name: "two replicas, a tiny fleet", counts: map[string]int{"a": 7, "b": 0}, wantRounds: RebalanceConvergenceRounds},
		{name: "three replicas, everything on one", counts: map[string]int{"a": 2370, "b": 0, "c": 0}, wantRounds: 2 * RebalanceConvergenceRounds},
	} {
		t.Run(test.name, func(t *testing.T) {
			owners := rebalanceOwners(test.counts)
			workers := make([]ownership.WorkerRegistration, 0, len(test.counts))
			for id := range test.counts {
				workers = append(workers, rebalanceWorker(id, ownership.WorkerReady, live, "shadow"))
			}
			router := NewRouter(nil)
			rounds := 0
			for ; rounds <= test.wantRounds; rounds++ {
				plan := router.PlanRebalance(owners, workers, now)
				if len(plan.Moves) == 0 {
					break
				}
				if cap := (plan.Assigned + rebalanceHandoverFraction - 1) / rebalanceHandoverFraction; len(plan.Moves) > cap {
					t.Fatalf("round %d moved %d of %d, more than a twentieth", rounds, len(plan.Moves), plan.Assigned)
				}
				for _, move := range plan.Moves {
					owners[move.QueryGroup] = move.To
				}
			}
			final := router.PlanRebalance(owners, workers, now)
			gap := final.MostOwned - final.LeastOwned
			// A gap of one is parity: nothing can move without crossing the
			// midpoint, and a fleet too small for the spread to admit one is
			// converged all the same.
			if len(final.Moves) != 0 || (gap > 1 && gap*100 > final.Target*rebalanceStopSpreadPercent) {
				t.Fatalf("after %d rounds owned = %v, gap %d is still outside %d%% of target %d", rounds, final.Owned, gap, rebalanceStopSpreadPercent, final.Target)
			}
			if rounds > test.wantRounds {
				t.Fatalf("converged in %d rounds, want at most %d", rounds, test.wantRounds)
			}
		})
	}
}
