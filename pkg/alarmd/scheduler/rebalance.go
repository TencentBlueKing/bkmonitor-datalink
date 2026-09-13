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
// would move Assignments and how many it would move per round, and neither
// is something an operator knows better than the program.
const (
	// rebalanceBatchFraction bounds one round to max(1, assigned/100) moves,
	// so a large fleet converges over several rounds instead of handing
	// many Query Groups over at once, each of which restarts from its
	// Progress on the new owner.
	rebalanceBatchFraction = 100
	// rebalanceStopSpreadPercent stops planning when the gap between the
	// most and least loaded ready worker is at most this share of the even
	// target; below it the drift is within what rendezvous placement
	// produces on its own and moving would only churn.
	rebalanceStopSpreadPercent = 5
)

// RebalanceMove names one Assignment a rebalance round would move, from
// its current desired owner to the worker the round chose.
type RebalanceMove struct {
	QueryGroup execution.QueryGroupIdentity
	From       string
	To         string
}

// RebalancePlan describes one rebalance planning round over the desired
// owners the Control Leader just reconciled. It only describes: publishing
// the moves is a separate decision that nothing takes yet. Owned covers
// every ready worker, including those that own nothing, which is exactly
// the case rendezvous placement cannot correct by itself.
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

// PlanRebalance computes the moves one rebalance round would publish given
// the desired owner of every reconciled Query Group and the worker
// registrations at that time. Only workers that are READY and unexpired
// take part, matching Select; Query Groups whose desired owner is not one
// of them are left to rendezvous placement and are not counted. Moves go
// from the single most loaded worker to the single least loaded one, are
// bounded by the batch, never cross the midpoint of the gap, and only name
// Query Groups the destination is eligible for; ties resolve by worker
// identity and Query Group identity so the plan is deterministic.
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
	plan.Batch = plan.Assigned / rebalanceBatchFraction
	if plan.Batch < 1 {
		plan.Batch = 1
	}
	gap := plan.MostOwned - plan.LeastOwned
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
