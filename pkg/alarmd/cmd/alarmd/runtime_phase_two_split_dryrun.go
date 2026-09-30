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
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// splitDryRunMaxObjects bounds how many objects one round works out a split
// for.
//
// The trigger should name one or two (decision-020 section 4.7.3.1). A round
// that finds fifty is a round where something else is wrong - a fleet whose
// pools were all reported as tiny, a ledger carrying stale peaks - and the
// answer to that is not to read fifty objects' Plans and censuses. What is
// left out is counted rather than dropped in silence.
const splitDryRunMaxObjects = 8

// splitCensusSource is what the dry run needs beyond the readings the round
// already has: which Plans an object carries, and the census each has.
//
// Two methods rather than one because they are two different reads with two
// different failure modes - the catalog's object, and the Plan's own census -
// and a round that cannot do the first has nothing to ask the second.
type splitCensusSource interface {
	// SplitCandidatePlans is the Plans one Query Group carries, each with the
	// state generation its census is keyed by and the cadence that census is
	// rewritten at.
	SplitCandidatePlans(context.Context, execution.QueryGroupIdentity) ([]splitCandidatePlan, error)
	// ReadCensus is one Plan's dimension census; false is a Plan nobody has
	// taken one of, which is a decision the planner makes rather than an
	// error.
	ReadCensus(context.Context, execution.PlanCensusIdentity) (execution.DimensionCensus, bool, error)
}

// dryRunSplits works out what splitting each over-share object would look
// like, and reports it. Nothing is published and nothing is written: this is
// the reading that has to be trusted before a split is acted on, taken on the
// objects a split would actually be taken on.
//
// Run after the byte moves rather than before: an object over its share on a
// Worker that just gave something away may not be over it any more, and the
// round's final owners are what the next round will judge. Working from the
// pre-move owners would plan splits for objects the round had already fixed
// by moving them.
func (runtime *productionPhaseTwoOwnership) dryRunSplits(
	ctx context.Context,
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	readings scheduler.ByteReadings,
	at time.Time,
) {
	source := runtime.dependencies.SplitCensus
	if source == nil {
		return
	}
	candidates, skipped := splitCandidates(owners, workers, readings)
	for _, candidate := range candidates {
		runtime.dryRunSplit(ctx, source, candidate, at.Unix())
	}
	// Every round, not only the rounds that skipped something: the three
	// counts are each other's denominator, and a skipped count on its own
	// cannot say whether a zero means nothing was left out or nothing was
	// looked at.
	runtime.observeSplitRound(ctx, observability.SplitRoundFacts{
		OverShare: len(candidates) + skipped, Examined: len(candidates), Skipped: skipped,
	})
}

// splitCandidatePlan is one Plan of a candidate object: what its census is
// keyed by, and how often that census is rewritten.
//
// The cadence travels with the identity because the planner's staleness
// bound is not a fixed figure: a census is written when the Plan's Slot
// runs, so an hourly Plan's census is older than any flat bound for most of
// the hour. Read against a flat bound, every Plan slower than it would be
// refused on nearly every round.
type splitCandidatePlan struct {
	Census                    execution.PlanCensusIdentity
	EvaluationIntervalSeconds int64
	// Queries is what this Plan runs, for asking whether the split the
	// planner decided on can be expressed at all. Carried only for the
	// objects a split is being worked out for - at most a handful a round,
	// read once per publication - because these are the largest fields a
	// Plan has and no other object needs them here.
	Queries map[execution.LogicalQueryRef]execution.QueryPlanFacts
}

// splitCandidate is one object the byte readings put over the share a single
// object may hold, with the two numbers that put it there.
type splitCandidate struct {
	QueryGroup execution.QueryGroupIdentity
	PeakBytes  uint64
	ShareBytes uint64
}

// splitCandidates is every object over its share, heaviest first, bounded.
//
// The share is the holder's pool halved - the same number the Worker refuses a
// Slot by, and the same one it judges a census candidate by. An object whose
// holder reported no pool is not judged: an unknown pool is not a large one.
func splitCandidates(
	owners map[execution.QueryGroupIdentity]string,
	workers []ownership.WorkerRegistration,
	readings scheduler.ByteReadings,
) ([]splitCandidate, int) {
	pools := make(map[string]uint64, len(workers))
	for _, worker := range workers {
		if worker.Load != nil && worker.Load.RetainedPoolBytes > 0 {
			pools[worker.WorkerID] = worker.Load.RetainedPoolBytes
		}
	}
	candidates := make([]splitCandidate, 0, len(owners))
	for queryGroup, owner := range owners {
		peak, known := readings.Peak[queryGroup]
		if !known || peak == 0 {
			continue
		}
		// One test of the share, not two. A holder that reported no pool and
		// a pool too small to halve are the same answer - this object is not
		// judged - and writing them as two checks leaves neither of them
		// load-bearing: deleting the first changed nothing, because a missing
		// pool reads as zero and a share of zero is already refused here.
		share := pools[owner] / 2
		if share == 0 || peak <= share {
			continue
		}
		candidates = append(candidates, splitCandidate{QueryGroup: queryGroup, PeakBytes: peak, ShareBytes: share})
	}
	// Heaviest first, then by name: the bound below cuts the tail, so which
	// objects survive it must be the worst ones and must not depend on map
	// order.
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].PeakBytes != candidates[right].PeakBytes {
			return candidates[left].PeakBytes > candidates[right].PeakBytes
		}
		return candidates[left].QueryGroup < candidates[right].QueryGroup
	})
	if len(candidates) > splitDryRunMaxObjects {
		return candidates[:splitDryRunMaxObjects], len(candidates) - splitDryRunMaxObjects
	}
	return candidates, 0
}

// dryRunSplit reads one object's Plans and censuses and reports what a split
// would be for each.
func (runtime *productionPhaseTwoOwnership) dryRunSplit(
	ctx context.Context, source splitCensusSource, candidate splitCandidate, at int64,
) {
	plans, err := source.SplitCandidatePlans(ctx, candidate.QueryGroup)
	if err != nil || len(plans) == 0 {
		// An object whose Plans this round could not read is not an object
		// with no Plans. Reported as a reading that is missing, under the
		// Query Group, because that is all this round knows about it.
		runtime.observeSplitPlan(ctx, candidate.QueryGroup, observability.SplitPlanFacts{
			Outcome: observability.SplitOutcomeNoReading, DryRun: true,
			PeakBytes: candidate.PeakBytes, ShareBytes: candidate.ShareBytes,
		}, nil, err)
		return
	}
	censuses := make([]execution.DimensionCensus, len(plans))
	read := make([]bool, len(plans))
	var counted uint64
	for index, plan := range plans {
		census, found, err := source.ReadCensus(ctx, plan.Census)
		if err != nil {
			continue
		}
		censuses[index], read[index] = census, found
		if found {
			counted += uint64(census.Series)
		}
	}
	for index, plan := range plans {
		input := controlplane.SplitInput{
			Plan: plan.Census.Plan, ShareBytes: candidate.ShareBytes, At: at,
			Census: censuses[index], CensusRead: read[index],
			EvaluationIntervalSeconds: plan.EvaluationIntervalSeconds,
			PeakBytes:                 attributedPeakBytes(candidate.PeakBytes, censuses[index], read[index], counted, len(plans)),
		}
		split, facts := controlplane.PlanSplit(input)
		facts.PlansInGroup = len(plans)
		// Whether the strategy's own query can express what was planned.
		// Asked only of a split that was planned: the other outcomes have no
		// dimension and no value lists to build from, and asking anyway
		// would report NOT_PLANNED for every object under its share - a
		// count of the ordinary case dressed as a refusal.
		var queries *observability.ShardQueryFacts
		if facts.Outcome == observability.SplitOutcomePlanned {
			_, built := controlplane.ShardQueries(plan.Census.Plan, plans[index].Queries, split)
			queries = &built
		}
		runtime.observeSplitPlan(ctx, candidate.QueryGroup, facts, queries, nil)
	}
}

// attributedPeakBytes is how much of the Query Group's bytes this Plan is
// answerable for.
//
// A Query Group carrying one Plan is the ordinary case and the whole of the
// peak is that Plan's. A Query Group carrying several has one byte figure for
// all of them, and the only reading that says how they divide is how many
// series each was counted with - so the peak is split in that proportion. It
// is an estimate; PlansInGroup on the line is what says how much of one, and a
// Plan with no census in a group of several gets nothing rather than a guess,
// which the planner then answers as NO_CENSUS.
func attributedPeakBytes(
	peak uint64, census execution.DimensionCensus, read bool, counted uint64, plans int,
) uint64 {
	if plans == 1 {
		return peak
	}
	if !read || counted == 0 || census.Series == 0 {
		return 0
	}
	// Multiplied before it is divided, like every other share in this
	// decision: taken the other way round the quotient is truncated before it
	// is scaled, and the error is multiplied by the series count. It cannot
	// overflow here - a retained peak is bounded by the pool and a census by
	// its own value bound, so the product stays far inside uint64.
	return peak * uint64(census.Series) / counted
}

func (runtime *productionPhaseTwoOwnership) observeSplitPlan(
	ctx context.Context, queryGroup execution.QueryGroupIdentity,
	facts observability.SplitPlanFacts, queries *observability.ShardQueryFacts, err error,
) {
	result, reason := observability.ResultSuccess, observability.ReasonNone
	if err != nil {
		result, reason = observability.ResultDegraded, observability.ReasonInternalUnknown
	}
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSplitPlanned,
		Result: observability.Result(result), Direction: observability.DirectionInternal,
		ReasonCode: reason, Err: err,
		Trace: observability.TraceFields{
			QueryGroupKey: string(queryGroup), StrategyID: facts.StrategyID, BusinessID: facts.BusinessID,
		},
		SplitPlan: &facts, ShardQuery: queries,
	})
}

// observeSplitRound says what this round looked at: how many objects were
// over their share, how many a split was worked out for, and how many were
// left.
//
// Its own line with its own structure, never an object's. These counts used
// to ride out on SplitPlanFacts.PlansInGroup - a field that means "how many
// Plans share this object's bytes, so how soft this estimate is" - which gave
// one field name two subjects: a reader filtering it for soft estimates
// caught this line and read the fleet's skipped count as one Plan's group
// size. Left in the object's outcome counter it also put a round hitting its
// bound in the same bucket as an object missing a number.
func (runtime *productionPhaseTwoOwnership) observeSplitRound(
	ctx context.Context, facts observability.SplitRoundFacts,
) {
	result := observability.ResultSuccess
	if facts.Skipped > 0 {
		result = observability.ResultDegraded
	}
	observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSplitPlanned,
		Result: observability.Result(result), Direction: observability.DirectionInternal,
		SplitRound: &facts,
	})
}
