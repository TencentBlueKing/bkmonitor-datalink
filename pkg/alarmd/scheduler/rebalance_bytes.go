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

// Retained bytes are a capacity constraint on placement, not a balance
// target (decision-020 section 5.7). Each Worker holds one retained-byte
// pool, and each Query Group it runs peaks somewhere inside it once a
// round; the budget keeps every Query Group inside its own share, and
// nobody kept the sum. Three Query Groups of one strategy landed on one
// replica at 175-404 MB each, every one inside its share, and together
// filled a 1 GiB pool for thirteen refusals every ten minutes. Placement
// by count is blind to it and never corrects it: counts do not change when
// a replica's bytes fill.
//
// The rule: a ready Worker whose Query Groups' peaks sum past this share of
// its pool is overloaded, and the round moves its largest Query Group to
// the ready Worker with the most headroom, one move per overloaded Worker
// per round, over the same handover a count rebalance uses. This is a
// feasibility move, not a balancing one: it is not subject to the count
// rebalance's stop spread, and the count rebalance is kept from undoing it
// by the same reading (PlanRebalanceWithBytes).
//
// The constraint is judged only where the numbers are known: a Worker that
// registered no pool is not judged and is listed as such, and a Query
// Group with no reported peak counts nothing and is counted as unread. An
// unknown is reported, never read as "no pressure" - on either side of a
// move. A Worker with an unread Query Group has a sum that is a lower
// bound: enough to say it is overloaded when its known part already is,
// not enough to say it has room, so it is not a destination this round.
// Unread is a passing state - a Worker reports a Query Group's first
// reading on its next heartbeat, unconditionally - and the ledger carries
// a moved Query Group's last reading over to its new holder as provisional
// in the meantime, so the Worker a move just landed on is not the emptiest
// Worker in the fleet by the next round.
const byteConstraintPercent = 80

// ByteConstraintPercent is the share above, for a reader that reports a
// plan.
const ByteConstraintPercent = byteConstraintPercent

// ByteReadings is what the byte constraint is judged from: each ready
// Worker's retained-byte pool as it registered it, and each Query Group's
// largest per-Slot retained bytes as the Worker that runs it last reported
// on its heartbeat. A Worker absent from Pool is not judged; a Query Group
// absent from Peak has no reading.
type ByteReadings struct {
	Pool map[string]uint64
	Peak map[execution.QueryGroupIdentity]uint64
}

func (readings ByteReadings) pool(workerID string) (uint64, bool) {
	if readings.Pool == nil {
		return 0, false
	}
	pool, known := readings.Pool[workerID]
	return pool, known && pool > 0
}

func (readings ByteReadings) peak(queryGroup execution.QueryGroupIdentity) (uint64, bool) {
	if readings.Peak == nil {
		return 0, false
	}
	peak, known := readings.Peak[queryGroup]
	return peak, known
}

// byteLimit is the share of a pool the constraint allows.
func byteLimit(pool uint64) uint64 {
	return pool / 100 * byteConstraintPercent
}

// fits reports whether a Query Group of the given peak may land on a
// Worker whose sum is already what it is: true when the Worker is not
// judged, since an unknown pool refuses nothing, and true for a Query Group
// with no reading, which adds nothing to the sum it is judged by.
func (readings ByteReadings) fits(workerID string, sum, peak uint64) bool {
	pool, judged := readings.pool(workerID)
	if !judged {
		return true
	}
	return sum+peak <= byteLimit(pool)
}

// ByteMove names one Query Group a round moves for the byte constraint,
// with the peak it was judged by.
type ByteMove struct {
	QueryGroup execution.QueryGroupIdentity
	From       string
	To         string
	Bytes      uint64
}

// BytePlan describes one round's byte-constraint planning over the desired
// owners the Control Leader just reconciled. Sum is each judged Worker's
// sum of peaks before the moves.
type BytePlan struct {
	// Judged is how many ready Workers registered a pool; PoolUnknown names
	// the ready Workers that did not, and are not judged.
	Judged      int
	PoolUnknown []string
	// Unread is how many Query Groups on judged Workers have no reported
	// peak. They count nothing toward their Worker's sum, and Unsettled
	// names the judged Workers holding one: their sums are lower bounds,
	// and they are not destinations this round.
	Unread    int
	Unsettled []string
	Sum       map[string]uint64
	// Overloaded names the judged Workers over the constraint before the
	// moves; Unplaceable the ones among them the round found no move for -
	// no Query Group of theirs with a reading fits any other judged Worker.
	Overloaded  []string
	Unplaceable []string
	Moves       []ByteMove
}

// PlanByteMoves computes the byte-constraint moves of one round. Only
// ready, unexpired Workers take part, as in PlanRebalance; the Workers are
// judged in identity order, and for each overloaded one the round takes
// its Query Groups largest first and lands the first that fits the judged
// Worker with the most headroom left - largest first so one move relieves
// the most, first that fits so a Query Group too large for anywhere does
// not leave the Worker stuck behind it. Headroom is kept as the round
// moves, so two overloaded Workers do not both fill the same destination.
// Ties resolve by identity so the plan is deterministic.
func (router *Router) PlanByteMoves(
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	readings ByteReadings,
	at time.Time,
) BytePlan {
	plan := BytePlan{Sum: map[string]uint64{}}
	if router == nil || at.IsZero() {
		return plan
	}
	ready := make(map[string]ownership.WorkerRegistration, len(workers))
	judged := make([]string, 0, len(workers))
	for _, worker := range workers {
		if worker.Validate() != nil || worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			continue
		}
		if _, duplicate := ready[worker.WorkerID]; duplicate {
			continue
		}
		ready[worker.WorkerID] = worker
		if _, known := readings.pool(worker.WorkerID); !known {
			plan.PoolUnknown = append(plan.PoolUnknown, worker.WorkerID)
			continue
		}
		judged = append(judged, worker.WorkerID)
		plan.Sum[worker.WorkerID] = 0
	}
	sort.Strings(plan.PoolUnknown)
	sort.Strings(judged)
	plan.Judged = len(judged)
	owned := make(map[string][]execution.QueryGroupIdentity, len(judged))
	unreadBy := make(map[string]int, len(judged))
	for queryGroup, owner := range owners {
		if _, isJudged := plan.Sum[owner]; !isJudged {
			continue
		}
		peak, read := readings.peak(queryGroup)
		if !read {
			plan.Unread++
			unreadBy[owner]++
			continue
		}
		plan.Sum[owner] += peak
		owned[owner] = append(owned[owner], queryGroup)
	}
	for _, workerID := range judged {
		if unreadBy[workerID] > 0 {
			plan.Unsettled = append(plan.Unsettled, workerID)
		}
	}
	if len(judged) < 2 {
		for _, workerID := range judged {
			if pool, _ := readings.pool(workerID); plan.Sum[workerID] > byteLimit(pool) {
				plan.Overloaded = append(plan.Overloaded, workerID)
				plan.Unplaceable = append(plan.Unplaceable, workerID)
			}
		}
		return plan
	}
	sum := make(map[string]uint64, len(plan.Sum))
	for workerID, total := range plan.Sum {
		sum[workerID] = total
	}
	for _, workerID := range judged {
		pool, _ := readings.pool(workerID)
		if sum[workerID] <= byteLimit(pool) {
			continue
		}
		plan.Overloaded = append(plan.Overloaded, workerID)
		candidates := owned[workerID]
		sort.Slice(candidates, func(left, right int) bool {
			leftPeak, rightPeak := readings.Peak[candidates[left]], readings.Peak[candidates[right]]
			if leftPeak != rightPeak {
				return leftPeak > rightPeak
			}
			return candidates[left] < candidates[right]
		})
		moved := false
		for _, queryGroup := range candidates {
			peak := readings.Peak[queryGroup]
			if peak == 0 {
				break
			}
			destination := router.byteDestination(queryGroup, workerID, judged, ready, readings, sum, unreadBy, peak, at)
			if destination == "" {
				continue
			}
			plan.Moves = append(plan.Moves, ByteMove{QueryGroup: queryGroup, From: workerID, To: destination, Bytes: peak})
			sum[workerID] -= peak
			sum[destination] += peak
			moved = true
			break
		}
		if !moved {
			plan.Unplaceable = append(plan.Unplaceable, workerID)
		}
	}
	return plan
}

// byteDestination is the judged Worker with the most headroom that the
// Query Group fits and is eligible for, or "" when there is none. A Worker
// with an unread Query Group is not one: its sum is a lower bound, and the
// room it appears to have may be exactly what it does not.
func (router *Router) byteDestination(
	queryGroup execution.QueryGroupIdentity,
	from string,
	judged []string,
	ready map[string]ownership.WorkerRegistration,
	readings ByteReadings,
	sum map[string]uint64,
	unreadBy map[string]int,
	peak uint64,
	at time.Time,
) string {
	destination, headroom := "", uint64(0)
	for _, workerID := range judged {
		if workerID == from || unreadBy[workerID] > 0 {
			continue
		}
		pool, _ := readings.pool(workerID)
		limit := byteLimit(pool)
		if sum[workerID]+peak > limit {
			continue
		}
		if router.additionalEligibility != nil && !router.additionalEligibility.Eligible(queryGroup, ready[workerID], at) {
			continue
		}
		left := limit - sum[workerID]
		if destination == "" || left > headroom || (left == headroom && workerID < destination) {
			destination, headroom = workerID, left
		}
	}
	return destination
}

// PlanByteMoves exposes the router's byte-constraint planning to the owner
// of the reconcile loop.
func (reconciler *Reconciler) PlanByteMoves(
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	readings ByteReadings,
	at time.Time,
) BytePlan {
	if reconciler == nil {
		return BytePlan{Sum: map[string]uint64{}}
	}
	return reconciler.router.PlanByteMoves(owners, workers, readings, at)
}
