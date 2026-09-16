// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Rebalance policy. Both values are program constants: they describe how
// far the fleet may drift from an even split before the Control Leader
// moves Assignments and how much one round may hand over, and neither is
// something an operator knows better than the program.
//
// One round moves
//
//	min(gap/2, ceil(assigned/20))
//
// Assignments from the most to the least loaded ready worker: as many as
// the handover cap allows, never past the midpoint. The cap is the cost
// bound -- a move costs the Query Group at most one Slot, since the old
// holder's in-flight Slot is refused by the fence and the new holder
// resumes from Progress -- and the convergence bound follows from it: the
// widest gap two replicas can have is the whole fleet, half of it has to
// move, a twentieth moves a round, so any skew between two replicas closes
// within ten rounds, fifty seconds at the default cadence. With two
// replicas and 2370 Query Groups all on one of them that is 118 moves a
// round; the one-percent-a-round floor this replaced took fifty-two rounds.
//
// The per-round amount was first written as max(assigned/100, gap/(2K))
// with K=10, meant to close any gap in K rounds. It does not: recomputed
// on the shrinking gap each round it is a geometric series that leaves a
// third of the gap after K rounds. The bound has to come from a fraction of
// the fleet, not of the remaining gap, and the table test below is what
// caught it.
const (
	// rebalanceHandoverFraction is the cap: one round hands over at most
	// ceil(assigned/20), five percent of the fleet's Query Groups.
	rebalanceHandoverFraction = 20
	// rebalanceStopSpreadPercent stops planning when the gap between the
	// most and least loaded ready worker is at most this share of the even
	// target; below it the drift is within what rendezvous placement
	// produces on its own and moving would only churn.
	rebalanceStopSpreadPercent = 5
)

// RebalanceStopSpreadPercent is the tolerance above, for a reader that
// reports a plan: "no moves" means the spread is within this share of the
// even target, and the reader should say so with this number rather than
// one of its own.
const RebalanceStopSpreadPercent = rebalanceStopSpreadPercent

// RebalanceConvergenceRounds is the bound the cap implies for two ready
// workers: the rounds within which any skew between them closes to within
// the stop spread. With more workers a round still moves along one pair, so
// the bound holds per pair. Exported for the reader that reports a plan.
const RebalanceConvergenceRounds = rebalanceHandoverFraction / 2

// RebalanceMove names one Assignment a rebalance round would move, from
// its current desired owner to the worker the round chose.
type RebalanceMove struct {
	QueryGroup execution.QueryGroupIdentity
	From       string
	To         string
}

// RebalancePlan describes one rebalance planning round over the desired
// owners the Control Leader just reconciled. It only describes: whether the
// moves are published is the Leader's decision, taken on the ready set's
// stability. Owned covers every ready worker, including those that own
// nothing, which is exactly the case rendezvous placement cannot correct by
// itself. Batch is the bound this round's moves were cut at, before the
// destination's eligibility is applied.
type RebalancePlan struct {
	ReadyWorkers int
	Assigned     int
	Target       int
	MostOwned    int
	LeastOwned   int
	Batch        int
	Owned        map[string]int
	Moves        []RebalanceMove
}

// PlanRebalance computes the moves one rebalance round publishes given the
// desired owner of every reconciled Query Group and the worker
// registrations at that time. Only workers that are READY and unexpired
// take part, matching Select; Query Groups whose desired owner is not one
// of them are left to rendezvous placement and are not counted. Moves go
// from the single most loaded worker to the single least loaded one, are
// bounded by the batch above, never cross the midpoint of the gap, and only
// name Query Groups the destination is eligible for; ties resolve by worker
// identity and Query Group identity so the plan is deterministic. With
// three or more ready workers a round still moves along one pair, so the
// convergence bound holds per pair rather than for the fleet.
func (router *Router) PlanRebalance(
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	at time.Time,
) RebalancePlan {
	plan := RebalancePlan{Owned: map[string]int{}}
	if router == nil || at.IsZero() {
		return plan
	}
	ready := make(map[string]ownership.WorkerRegistration, len(workers))
	for _, worker := range workers {
		if worker.Validate() != nil || worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			continue
		}
		if _, duplicate := ready[worker.WorkerID]; duplicate {
			continue
		}
		ready[worker.WorkerID] = worker
		plan.Owned[worker.WorkerID] = 0
	}
	plan.ReadyWorkers = len(ready)
	for _, owner := range owners {
		if _, ok := ready[owner]; ok {
			plan.Owned[owner]++
			plan.Assigned++
		}
	}
	if plan.ReadyWorkers < 2 || plan.Assigned == 0 {
		return plan
	}
	plan.Target = plan.Assigned / plan.ReadyWorkers
	most, least := "", ""
	for workerID, owned := range plan.Owned {
		if most == "" || owned > plan.Owned[most] || (owned == plan.Owned[most] && workerID < most) {
			most = workerID
		}
		if least == "" || owned < plan.Owned[least] || (owned == plan.Owned[least] && workerID < least) {
			least = workerID
		}
	}
	plan.MostOwned, plan.LeastOwned = plan.Owned[most], plan.Owned[least]
	gap := plan.MostOwned - plan.LeastOwned
	plan.Batch = rebalanceBatch(plan.Assigned)
	if plan.Target == 0 || gap*100 <= plan.Target*rebalanceStopSpreadPercent {
		return plan
	}
	limit := gap / 2
	if limit > plan.Batch {
		limit = plan.Batch
	}
	if limit == 0 {
		return plan
	}
	candidates := make([]execution.QueryGroupIdentity, 0, plan.MostOwned)
	for queryGroup, owner := range owners {
		if owner == most {
			candidates = append(candidates, queryGroup)
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left] < candidates[right] })
	destination := ready[least]
	for _, queryGroup := range candidates {
		if len(plan.Moves) == limit {
			break
		}
		if router.additionalEligibility != nil && !router.additionalEligibility.Eligible(queryGroup, destination, at) {
			continue
		}
		plan.Moves = append(plan.Moves, RebalanceMove{QueryGroup: queryGroup, From: most, To: least})
	}
	return plan
}

// rebalanceBatch is the per-round bound for a fleet of assigned Query
// Groups: a twentieth of the fleet, rounded up so a fleet of fewer than
// twenty still moves one.
func rebalanceBatch(assigned int) int {
	batch := (assigned + rebalanceHandoverFraction - 1) / rebalanceHandoverFraction
	if batch < 1 {
		batch = 1
	}
	return batch
}

// PlanRebalance exposes the router's rebalance planning to the owner of the
// reconcile loop, which holds the desired owners it just published.
func (reconciler *Reconciler) PlanRebalance(
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	at time.Time,
) RebalancePlan {
	if reconciler == nil {
		return RebalancePlan{Owned: map[string]int{}}
	}
	return reconciler.router.PlanRebalance(owners, workers, at)
}
