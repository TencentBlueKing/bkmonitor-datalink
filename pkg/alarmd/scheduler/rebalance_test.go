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
		{name: "a large fleet moves one percent per round", owners: rebalanceOwners(map[string]int{"a": 200, "b": 100}), workers: ready("a", "b"),
			wantTarget: 150, wantBatch: 3, wantMoves: 3, wantFrom: "a", wantTo: "b"},
		{name: "a gap of one is parity, not imbalance", owners: rebalanceOwners(map[string]int{"a": 9, "b": 8}), workers: ready("a", "b"), wantTarget: 8, wantBatch: 1},
		{name: "a gap within the stop spread plans nothing", owners: rebalanceOwners(map[string]int{"a": 102, "b": 98}), workers: ready("a", "b"), wantTarget: 100, wantBatch: 2},
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
		want := []RebalanceMove{
			{QueryGroup: "a-qg-000", From: "a", To: "b"}, {QueryGroup: "a-qg-001", From: "a", To: "b"}, {QueryGroup: "a-qg-002", From: "a", To: "b"},
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
