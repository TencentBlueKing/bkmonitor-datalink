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
// Group with no reported peak counts nothing toward its Worker's sum and is
// counted as unread. An unknown is reported, never read as "no pressure" -
// on either side of a move. A Worker with an unread Query Group has a sum
// that is a lower bound: enough to say it is overloaded when its known part
// already is, and as a destination each of its unread Query Groups is
// counted at the ninetieth percentile of the peaks read this round.
//
// Unread is not a passing state. A Query Group has a peak only after it
// has run a Slot in the process that reports it, so after a restart every
// Query Group on an hourly to sixty-hour cadence stays unread until its
// first round, and the Leader that restarted with it holds no earlier
// reading. Refusing any Worker with one unread Query Group as a
// destination refused every Worker for as long as the longest cadence, and
// an overloaded Worker was never relieved - the state the constraint
// exists for. The long unread tail is drawn from the same Query Groups the
// read peaks describe, and a destination the estimate undercounts goes past
// the share by at most what that Query Group exceeds it, is judged
// overloaded on its next reading and moves its largest Query Group on;
// the retained-byte budget still refuses at the pool. With no peak read at
// all there is nothing to estimate from, and such a Worker is not a
// destination. The ledger still carries a moved Query Group's last reading
// over to its new holder as provisional, so the Worker a move just landed
// on is not the emptiest Worker in the fleet by the next round.
const byteConstraintPercent = 80

// unreadEstimatePercentile is the percentile of the round's read peaks an
// unread Query Group is counted at on a destination; see above.
const unreadEstimatePercentile = 90

// unreadSampleLimit bounds the unread Query Groups named per Worker.
const unreadSampleLimit = 5

// PeakDistribution is the read peaks of one round: how many, and the median,
// the ninetieth and ninety-ninth percentiles and the largest, by the same
// nearest-rank rule as the estimate.
type PeakDistribution struct {
	Count, P50, P90, P99, Max uint64
}

// peakDistribution is the distribution of peaks; zero when there are none.
func peakDistribution(peaks []uint64) PeakDistribution {
	if len(peaks) == 0 {
		return PeakDistribution{}
	}
	sorted := append([]uint64(nil), peaks...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	rank := func(percentile int) uint64 { return sorted[(len(sorted)*percentile+99)/100-1] }
	return PeakDistribution{Count: uint64(len(sorted)), P50: rank(50), P90: rank(90), P99: rank(99), Max: sorted[len(sorted)-1]}
}

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
	// UnreadEstimate is what each unread Query Group on a destination was
	// counted at: the ninetieth percentile of the peaks read this round.
	// Zero when no peak was read, and then a Worker holding an unread Query
	// Group is not a destination.
	UnreadEstimate uint64
	// UnreadBy is Unread per judged Worker, and UnreadSample up to
	// unreadSampleLimit of each Worker's unread Query Groups, lowest first.
	// The total alone said every Worker was unsettled and not which Query
	// Groups kept it so -- whether they were ones that had not run since a
	// restart or ones that never report -- and that was what decided the fix.
	UnreadBy     map[string]int
	UnreadSample map[string][]execution.QueryGroupIdentity
	// ReadPeaks is the distribution the estimate was taken from.
	ReadPeaks PeakDistribution
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
	unreadGroups := make(map[string][]execution.QueryGroupIdentity, len(judged))
	readPeaks := make([]uint64, 0, len(owners))
	for queryGroup, owner := range owners {
		if _, isJudged := plan.Sum[owner]; !isJudged {
			continue
		}
		peak, read := readings.peak(queryGroup)
		if !read {
			plan.Unread++
			unreadBy[owner]++
			unreadGroups[owner] = append(unreadGroups[owner], queryGroup)
			continue
		}
		plan.Sum[owner] += peak
		owned[owner] = append(owned[owner], queryGroup)
		readPeaks = append(readPeaks, peak)
	}
	plan.UnreadEstimate = unreadEstimate(readPeaks)
	plan.ReadPeaks = peakDistribution(readPeaks)
	plan.UnreadBy = unreadBy
	plan.UnreadSample = make(map[string][]execution.QueryGroupIdentity, len(unreadGroups))
	for workerID, groups := range unreadGroups {
		sort.Slice(groups, func(left, right int) bool { return groups[left] < groups[right] })
		plan.UnreadSample[workerID] = groups[:min(len(groups), unreadSampleLimit)]
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
			destination := router.byteDestination(queryGroup, workerID, judged, ready, readings, sum, unreadBy, plan.UnreadEstimate, peak, at)
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
	unreadEstimate uint64,
	peak uint64,
	at time.Time,
) string {
	destination, headroom := "", uint64(0)
	for _, workerID := range judged {
		if workerID == from {
			continue
		}
		projected := sum[workerID]
		if unread := unreadBy[workerID]; unread > 0 {
			if unreadEstimate == 0 {
				continue
			}
			projected += uint64(unread) * unreadEstimate
		}
		pool, _ := readings.pool(workerID)
		limit := byteLimit(pool)
		if projected > limit || peak > limit-projected {
			continue
		}
		if router.additionalEligibility != nil && !router.additionalEligibility.Eligible(queryGroup, ready[workerID], at) {
			continue
		}
		left := limit - projected
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

// unreadEstimate is the nearest-rank ninetieth percentile of the peaks read
// this round, or zero when none was read.
func unreadEstimate(peaks []uint64) uint64 {
	if len(peaks) == 0 {
		return 0
	}
	sorted := append([]uint64(nil), peaks...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	rank := (len(sorted)*unreadEstimatePercentile + 99) / 100
	return sorted[rank-1]
}
