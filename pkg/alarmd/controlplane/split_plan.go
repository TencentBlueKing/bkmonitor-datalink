// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Splitting a strategy means cutting its series into pieces that are
// evaluated apart, by matching one dimension's values (decision-020 section
// 4.7.2): each piece but one carries a list of values, and the last catches
// everything no list matches. The census says which values there are and how
// many series each carries, and this is what turns that into pieces.
//
// The planner decides and reports; nothing here publishes anything. A dry run
// is not a smaller version of the real thing - it is the same decision with
// the acting removed, so that what a split would do can be read for as long
// as it takes to trust it, on the objects that would actually be split.
const (
	// MaxShardsPerStrategy caps how many pieces one strategy is cut into.
	// An object needing more than this is one no amount of cutting places
	// well, and the cap is reported rather than silently applied.
	MaxShardsPerStrategy = 16
	// ShardTargetSharePercent is how much of the per-object share one piece
	// should hold. Half, so a piece has room to grow before it is over the
	// share itself and the whole thing has to be cut again.
	ShardTargetSharePercent = 50
	// MaxShardSkewPercent is how lopsided a planned split may be: the
	// largest piece over the smallest. Past it the split would be undone by
	// the resplit trigger about as fast as it was made, so planning one is
	// worse than planning none.
	MaxShardSkewPercent = 130
	// MaxCensusAgeSeconds is the FLOOR on how old a census may be and still
	// describe the object. Past its own bound the distribution is not this
	// object's any more - a strategy whose targets changed an hour ago would
	// be cut along values it no longer has.
	//
	// A floor rather than the bound itself, because a census is only ever as
	// fresh as the object's own cadence: it is written when a Slot runs, so
	// an object evaluated every ten seconds has one seconds old, and an
	// object evaluated hourly has one that spends most of the hour older
	// than any fixed figure. Held to a flat bound, every object slower than
	// this one is refused on nearly every round - a whole class that can
	// never be planned, reported under a word that points at the census when
	// it is the bound that does not fit. Nothing in the schedule contract
	// caps an evaluation interval; it is only required to be positive.
	MaxCensusAgeSeconds = 900
	// CensusAgeCadenceMultiple is how many of the object's own evaluation
	// intervals its census may span. Two, so one missed round does not make
	// a census stale: a census is read in a later round than it was written,
	// so it is already one interval old when it is first looked at, and one
	// interval would be no allowance at all.
	CensusAgeCadenceMultiple = 2
)

// SplitInput is one object's readings, already attributed to one Plan.
//
// PeakBytes is this Plan's share of what its Query Group held, not the Query
// Group's own total: the caller knows how many Plans a Query Group carries
// and this does not, and a planner handed a Query Group's bytes for one of
// its four Plans would cut a strategy into four pieces to fix somebody
// else's size.
type SplitInput struct {
	Plan       execution.PlanIdentity
	PeakBytes  uint64
	ShareBytes uint64
	Census     execution.DimensionCensus
	CensusRead bool
	At         int64
	// EvaluationIntervalSeconds is how often this Plan runs, and with it how
	// often its census is rewritten. Zero is an unknown cadence, which falls
	// back to the floor - the bound a fixed figure gives - rather than to no
	// bound at all.
	EvaluationIntervalSeconds int64
}

// censusAgeBound is how old this object's census may be - its own cadence,
// never below the floor - and which of the three it came from.
//
// The source travels with the number because two of the three produce the
// same number. A bound of fifteen minutes on a fast object means the census
// is stale by many of that object's own rounds; the same fifteen minutes on
// an object whose cadence was not supplied means the bound is a guess, and
// refusing under it may be the very misattribution this bound exists to
// remove. Without the word they are byte-identical on the line.
func censusAgeBound(intervalSeconds int64) (int64, string) {
	if intervalSeconds <= 0 {
		return MaxCensusAgeSeconds, observability.SplitCensusBoundFloorUnknown
	}
	bound := intervalSeconds * CensusAgeCadenceMultiple
	if bound < MaxCensusAgeSeconds {
		return MaxCensusAgeSeconds, observability.SplitCensusBoundFloorFast
	}
	return bound, observability.SplitCensusBoundCadence
}

// SplitPlan is what a split would be: the dimension, the value lists, and
// what each piece is expected to carry. The last piece carries no list - it
// is the one that catches every value no list names, which is what makes the
// set of pieces cover every series including ones whose values appear after
// the census was taken.
type SplitPlan struct {
	Plan      execution.PlanIdentity
	Dimension string
	Lists     [][]string
	Estimates []ShardEstimate
}

// ShardEstimate is one piece's expected weight. Estimated, and named so:
// series are counted, bytes are the object's bytes divided among them, and a
// strategy whose series differ in size will not divide evenly.
type ShardEstimate struct {
	Series         uint32
	EstimatedBytes uint64
	Fallback       bool
}

// PlanSplit decides what splitting this object would look like, or why it
// cannot be split, and returns the readings either answer is judged from.
//
// Deterministic: two leaders reading the same census reach the same plan.
// Values are assigned heaviest first to the lightest piece so far, and ties
// everywhere - between dimensions, between equal values - are broken by
// name, because "whichever the map handed back first" would make two leaders
// disagree about a strategy neither of them is wrong about.
func PlanSplit(input SplitInput) (SplitPlan, observability.SplitPlanFacts) {
	facts := observability.SplitPlanFacts{
		StrategyID: input.Plan.StrategyID, BusinessID: input.Plan.BusinessID,
		PeakBytes: input.PeakBytes, ShareBytes: input.ShareBytes, DryRun: true,
	}
	if input.ShareBytes == 0 || input.PeakBytes == 0 {
		// No share and no peak are not "no pressure". A planner that read
		// them that way would answer UNDER_SHARE for every object on a
		// replica that has not reported yet.
		facts.Outcome = observability.SplitOutcomeNoReading
		return SplitPlan{}, facts
	}
	target := shardTargetBytes(input.ShareBytes)
	if target == 0 || input.PeakBytes <= input.ShareBytes {
		facts.Outcome = observability.SplitOutcomeUnderShare
		return SplitPlan{}, facts
	}
	// How many pieces have to CARRY the object, plus the one that carries no
	// list. The carrying count is what the share arithmetic is about: the
	// last piece exists to catch values that appear after the census, so
	// sizing the split as though it took a share of the load would plan every
	// piece too large by one piece's worth.
	carrying := int((input.PeakBytes + target - 1) / target)
	if carrying+1 > MaxShardsPerStrategy {
		carrying, facts.ShardsCapped = MaxShardsPerStrategy-1, true
	}
	facts.Carrying, facts.Shards = carrying, carrying+1
	if !input.CensusRead {
		facts.Outcome = observability.SplitOutcomeNoCensus
		return SplitPlan{}, facts
	}
	facts.Series, facts.CensusSource = input.Census.Series, string(input.Census.Source)
	facts.Candidates = len(input.Census.Dimensions)
	facts.CensusAgeSeconds = input.At - input.Census.ObservedAt
	if facts.CensusAgeSeconds < 0 {
		facts.CensusAgeSeconds = 0
	}
	facts.CensusAgeBoundSeconds, facts.CensusAgeBoundSource = censusAgeBound(input.EvaluationIntervalSeconds)
	if facts.CensusAgeSeconds > facts.CensusAgeBoundSeconds {
		facts.Outcome = observability.SplitOutcomeCensusStale
		return SplitPlan{}, facts
	}
	if input.Census.Series == 0 || facts.Candidates == 0 {
		facts.Outcome = observability.SplitOutcomeNoReading
		return SplitPlan{}, facts
	}
	facts.TargetSeries = uint32((uint64(input.Census.Series) + uint64(carrying) - 1) / uint64(carrying))

	best, bestFacts, found := bestSplitDimension(input.Census, carrying, facts)
	facts = bestFacts
	if !found {
		return SplitPlan{}, facts
	}
	if facts.SkewPercent > MaxShardSkewPercent {
		facts.Outcome = observability.SplitOutcomeSkewUnreachable
		return SplitPlan{}, facts
	}
	facts.Outcome = observability.SplitOutcomePlanned
	best.Plan = input.Plan
	for index := range best.Estimates {
		best.Estimates[index].EstimatedBytes = estimatedShardBytes(
			best.Estimates[index].Series, input.Census.Series, input.PeakBytes)
	}
	return best, facts
}

// bestSplitDimension is the dimension whose values divide most evenly, with
// the readings of whichever dimension got furthest when none can.
//
// "Got furthest" is by the same order the refusals are reported in: a
// dimension refused for its heaviest value says more than one refused for
// having too few values, because the first names the object's shape and the
// second names the census's.
func bestSplitDimension(
	census execution.DimensionCensus, carrying int, facts observability.SplitPlanFacts,
) (SplitPlan, observability.SplitPlanFacts, bool) {
	entries := append([]execution.DimensionCensusEntry(nil), census.Dimensions...)
	sort.Slice(entries, func(left, right int) bool { return entries[left].Dimension < entries[right].Dimension })

	var best SplitPlan
	bestSkew, found := 0, false
	refusal := observability.SplitOutcomeNoReading
	var refusalFacts observability.SplitPlanFacts
	for _, entry := range entries {
		attempt, attemptFacts, ok := splitOnDimension(entry, carrying, facts)
		if !ok {
			// The refusal a reader can act on wins: an indivisible value is
			// the object's own shape, a tail is the census's bound, and too
			// few values is neither.
			if refusalRank(attemptFacts.Outcome) > refusalRank(refusal) {
				refusal, refusalFacts = attemptFacts.Outcome, attemptFacts
			}
			continue
		}
		if !found || attemptFacts.SkewPercent < bestSkew {
			best, bestSkew, found = attempt, attemptFacts.SkewPercent, true
			facts = attemptFacts
		}
	}
	if !found {
		if refusalFacts.Outcome == "" {
			refusalFacts = facts
			refusalFacts.Outcome = refusal
		}
		return SplitPlan{}, refusalFacts, false
	}
	return best, facts, true
}

func refusalRank(outcome string) int {
	switch outcome {
	case observability.SplitOutcomeValueTooHeavy:
		return 3
	case observability.SplitOutcomeTailTooLarge:
		return 2
	case observability.SplitOutcomeTooFewValues:
		return 1
	default:
		return 0
	}
}

// splitOnDimension assigns one dimension's values to the carrying pieces,
// heaviest first onto the lightest piece so far.
//
// The piece with no list is not one of them. It catches every value no list
// matches, which today is the census's overflow and tomorrow is whatever
// value appears next, so it is expected to be nearly empty and is held to
// the same per-piece target separately (TAIL_TOO_LARGE). Counting it among
// the pieces the skew is measured over would make every healthy split - one
// whose census named everything - read as infinitely lopsided, because that
// piece would carry nothing at all.
func splitOnDimension(
	entry execution.DimensionCensusEntry, carrying int, facts observability.SplitPlanFacts,
) (SplitPlan, observability.SplitPlanFacts, bool) {
	facts.Dimension = entry.Dimension
	facts.TailSeries = entry.OverflowSeries
	if len(entry.Values) > 0 {
		facts.HeaviestValueSeries = entry.Values[0].Series
		for _, value := range entry.Values {
			if value.Series > facts.HeaviestValueSeries {
				facts.HeaviestValueSeries = value.Series
			}
		}
	}
	if len(entry.Values) < carrying {
		// Fewer values than pieces to fill: some piece would carry nothing,
		// which is not a split of this object, it is the same object plus
		// empty Query Groups.
		//
		// Asked before the heaviest value, and the order is not cosmetic.
		// With fewer values than pieces some value must already carry more
		// than a piece's target - the target is the total over the pieces,
		// and the values sum to the total - so the heaviness test answers
		// first for every one of these and TOO_FEW_VALUES would be a word
		// nothing could ever be reported under. Asked first, the two split
		// the population: too few values is a dimension too coarse to cut on
		// however its values shrink, and a heavy value is one value
		// dominating a dimension that has enough of them.
		facts.Outcome = observability.SplitOutcomeTooFewValues
		return SplitPlan{}, facts, false
	}
	if facts.HeaviestValueSeries > facts.TargetSeries {
		// One value carries more than a piece may hold. A value-list matcher
		// cannot divide a value, so no assignment of these values is even -
		// this is the object that needs hashing, not a different list.
		facts.Outcome = observability.SplitOutcomeValueTooHeavy
		return SplitPlan{}, facts, false
	}
	if entry.OverflowSeries > facts.TargetSeries {
		facts.Outcome = observability.SplitOutcomeTailTooLarge
		return SplitPlan{}, facts, false
	}

	values := append([]execution.DimensionValueCount(nil), entry.Values...)
	sort.Slice(values, func(left, right int) bool {
		if values[left].Series != values[right].Series {
			return values[left].Series > values[right].Series
		}
		return values[left].Value < values[right].Value
	})
	lists := make([][]string, carrying)
	loads := make([]uint32, carrying)
	for _, value := range values {
		lightest := 0
		for index := 1; index < carrying; index++ {
			if loads[index] < loads[lightest] {
				lightest = index
			}
		}
		lists[lightest] = append(lists[lightest], value.Value)
		loads[lightest] += value.Series
	}
	for index := range lists {
		sort.Strings(lists[index])
	}

	estimates := make([]ShardEstimate, 0, carrying+1)
	largest, smallest := loads[0], loads[0]
	for _, load := range loads {
		estimates = append(estimates, ShardEstimate{Series: load})
		if load > largest {
			largest = load
		}
		if load < smallest {
			smallest = load
		}
	}
	// The piece with no list, last and named as such, carrying the tail.
	estimates = append(estimates, ShardEstimate{Series: entry.OverflowSeries, Fallback: true})
	facts.LargestShardSeries, facts.SmallestShardSeries = largest, smallest
	facts.SkewPercent = shardSkewPercent(largest, smallest)
	return SplitPlan{Dimension: entry.Dimension, Lists: lists, Estimates: estimates}, facts, true
}

// shardTargetBytes is what one carrying piece should hold: the share's own
// percentage, multiplied before it is divided.
//
// The order matters at exactly the boundary this is used at. Taken the usual
// way round - share/100*percent - a share of one gibibyte yields 536,870,900
// rather than 536,870,912, and an object of exactly three shares then asks
// for seven pieces instead of six. The truncation is invisible in the middle
// of the range and wrong at every round number, which is where the fixtures
// and the readings both live.
func shardTargetBytes(shareBytes uint64) uint64 {
	return shareBytes * ShardTargetSharePercent / 100
}

// shardSkewPercent is the largest piece over the smallest, in percent. A
// smallest of zero is not a ratio: it is a piece carrying nothing, which is
// as lopsided as a split gets, so it reports the cap rather than dividing.
func shardSkewPercent(largest, smallest uint32) int {
	if smallest == 0 {
		if largest == 0 {
			return 100
		}
		return MaxShardSkewPercent + 1
	}
	return int(uint64(largest) * 100 / uint64(smallest))
}

// estimatedShardBytes divides the object's bytes among its series. An
// estimate and reported as one: the object's bytes are what it held, the
// series count is what the census saw, and a strategy whose series differ in
// size will not divide in proportion to their number.
func estimatedShardBytes(shardSeries, totalSeries uint32, peakBytes uint64) uint64 {
	if totalSeries == 0 {
		return 0
	}
	return peakBytes / uint64(totalSeries) * uint64(shardSeries)
}
