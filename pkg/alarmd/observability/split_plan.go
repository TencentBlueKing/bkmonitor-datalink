// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// The split planner's closed vocabulary, kept here because this is the
// package both ends can see: the planner in controlplane reads these, and
// the metric labels are pre-created from the same lists. Written twice they
// would be two derivations of one relation, and nothing would fail when one
// of them gained a value.
//
// Every outcome is a word rather than a boolean because "this object was not
// split" is the answer a reader arrives with, and the six reasons behind it
// call for six different actions: wait, raise a bound, shard by hash, fix the
// census, or nothing at all.
const (
	// SplitOutcomePlanned is a split this round could plan: a dimension, the
	// pieces, and what each would carry.
	SplitOutcomePlanned = "PLANNED"
	// SplitOutcomeUnderShare is an object that does not need splitting. The
	// ordinary answer, and counted so that "nothing was planned" can be told
	// from "nothing was looked at".
	SplitOutcomeUnderShare = "UNDER_SHARE"
	// SplitOutcomeNoCensus is an object over its share that nobody has taken
	// a census of yet. Expected for one round after a replica takes an object
	// over, and a standing count here means the census is not being written.
	SplitOutcomeNoCensus = "NO_CENSUS"
	// SplitOutcomeCensusStale is a census too old to plan from: the
	// distribution it describes is not the one the object has now.
	SplitOutcomeCensusStale = "CENSUS_STALE"
	// SplitOutcomeValueTooHeavy is the structural refusal: one dimension
	// value carries more series than a piece may hold, on every dimension the
	// census names. A value cannot be divided by a value-list matcher, so no
	// list assignment is even - this is the object that needs hashing rather
	// than a wider bound or a longer wait.
	SplitOutcomeValueTooHeavy = "VALUE_TOO_HEAVY"
	// SplitOutcomeTailTooLarge is the census's own bound getting in the way:
	// the series on values it could not name would themselves overfill the
	// piece that catches them. Read with the census's overflow.
	SplitOutcomeTailTooLarge = "TAIL_TOO_LARGE"
	// SplitOutcomeSkewUnreachable is a split that could be cut but would not
	// hold: the best assignment is still more lopsided than the resplit
	// threshold, so it would be undone about as fast as it was made.
	SplitOutcomeSkewUnreachable = "SKEW_UNREACHABLE"
	// SplitOutcomeTooFewValues is a census naming fewer values than there are
	// pieces to fill. Not a missing reading and not the object's shape: the
	// strategy simply does not vary enough along any dimension the census
	// names to be cut this many ways.
	SplitOutcomeTooFewValues = "TOO_FEW_VALUES"
	// SplitOutcomeNoReading is the planner with a number missing - no share,
	// no peak, or a census of no series. Never read as "no pressure": an
	// unknown is reported as an unknown.
	SplitOutcomeNoReading = "NO_READING"
	// Where a census's staleness bound came from. Three words rather than
	// the number alone, because two of them produce the SAME number - the
	// floor - for opposite reasons, and a reader met by a stale census and a
	// bound of fifteen minutes cannot otherwise tell which he has.
	//
	// CADENCE: the object's own evaluation interval. A census past this has
	// genuinely not been rewritten for two of the object's own rounds, which
	// is a fact about that object's Slots.
	// FLOOR_FAST: the object runs faster than the floor, so the floor is the
	// wider bound and the one used. Past it the census is stale by many of
	// the object's own rounds.
	// FLOOR_UNKNOWN: the cadence was not supplied, so the bound is a guess.
	// A census refused under this word may be perfectly fresh for an object
	// nobody told us the cadence of - the same misattribution the cadence
	// bound exists to remove, on a smaller population.
	SplitCensusBoundCadence      = "CADENCE"
	SplitCensusBoundFloorFast    = "FLOOR_FAST"
	SplitCensusBoundFloorUnknown = "FLOOR_UNKNOWN"

	// SplitOutcomeUnrecognised is a decision this build cannot name: the
	// planner reached an outcome that is not in this vocabulary.
	//
	// Its own word rather than folded into NO_READING, which is where it
	// started. The two have different owners and different answers: a
	// missing number is a state of the deployment - a pool not reported, a
	// census not written - and something an operator can go and look at,
	// while an unrecognised outcome is a defect in this build and something
	// only a change of code fixes. Folded together, a counter that should
	// send someone to the source reads like one more environmental
	// condition, and it would rise on a deployment where nothing is wrong
	// with the deployment at all.
	SplitOutcomeUnrecognised = "OUTCOME_UNRECOGNISED"
)

// SplitOutcomes is the label set the split metrics are pre-created with, so
// every outcome is a series from startup and a zero is a reading rather than
// an absence.
func SplitOutcomes() []string {
	return []string{
		SplitOutcomePlanned, SplitOutcomeUnderShare, SplitOutcomeNoCensus,
		SplitOutcomeCensusStale, SplitOutcomeValueTooHeavy, SplitOutcomeTailTooLarge,
		SplitOutcomeSkewUnreachable, SplitOutcomeTooFewValues, SplitOutcomeNoReading,
		SplitOutcomeUnrecognised,
	}
}

// normalizeSplitPlanFacts copies the facts and holds the outcome to its
// vocabulary. An outcome outside it becomes NO_READING rather than reaching
// the metric: a label nobody declared is a series nobody pre-created, and one
// arriving at runtime is how a bounded label set stops being bounded. It
// becomes the word for "a number is missing" because that is what an
// unrecognised decision is - the planner reached an answer this build cannot
// name, and reading it as any of the others would be a claim.
func normalizeSplitPlanFacts(facts *SplitPlanFacts) *SplitPlanFacts {
	if facts == nil {
		return nil
	}
	copied := *facts
	known := false
	for _, outcome := range SplitOutcomes() {
		if copied.Outcome == outcome {
			known = true
			break
		}
	}
	if !known {
		copied.Outcome = SplitOutcomeUnrecognised
	}
	return &copied
}

// SplitRoundFacts is what one round of the split dry run looked at, as
// opposed to what it decided about any one object.
//
// Its own structure because it has a different subject. The outcome words
// belong to an object - this strategy was not split, and here is the shape
// that stopped it - and a round-level count has no business wearing one: a
// reader filtering for objects whose byte estimate was shared among several
// Plans would otherwise catch a line whose number is how many objects the
// whole fleet skipped.
//
// Reported every round rather than only when something was skipped, so the
// counts carry their own denominator. Skipped alone cannot say whether a
// zero means nothing was left out or nothing was looked at.
type SplitRoundFacts struct {
	// OverShare is how many objects this round's readings put past the share
	// a single object may hold.
	OverShare int `json:"over_share"`
	// Examined is how many of them a split was worked out for, and Skipped
	// the rest. Skipped is not a split problem: it is a round finding far
	// more over-share objects than a split trigger should ever name, and the
	// readings to look at then are the pools and the peaks.
	Examined int `json:"examined"`
	Skipped  int `json:"skipped"`
}

// SplitPlanFacts is one object's split decision as a dry run reports it: what
// was decided, from which readings, and - when a split was planned - what the
// pieces would carry.
//
// The readings travel with the decision because the decision cannot be
// checked without them. "This object was not split" is the same line for an
// object under its share and an object whose heaviest value is indivisible,
// and those are opposite situations.
type SplitPlanFacts struct {
	Outcome    string `json:"outcome"`
	StrategyID string `json:"strategy_id,omitempty"`
	BusinessID string `json:"business_id,omitempty"`
	// PeakBytes and ShareBytes are the trigger: what this object held, and
	// what one object may hold.
	PeakBytes  uint64 `json:"peak_bytes"`
	ShareBytes uint64 `json:"share_bytes"`
	// Shards is how many pieces were planned and Carrying how many of them
	// carry a value list; the difference is the one piece that catches
	// everything no list matches. ShardsCapped says the arithmetic asked for
	// more than the cap allows - a piece that stays over its share after the
	// split, which is worth seeing rather than rounding away.
	Shards       int  `json:"shards"`
	Carrying     int  `json:"carrying_shards"`
	ShardsCapped bool `json:"shards_capped,omitempty"`
	// Dimension is the one the pieces would be cut on, and Candidates how
	// many the census offered. One of several says the choice was made; one
	// of one says there was nothing to choose from.
	Dimension  string `json:"dimension,omitempty"`
	Candidates int    `json:"dimension_candidates"`
	// Series is what the census counted, CensusAgeSeconds how old that count
	// is, and CensusAgeBoundSeconds how old it was allowed to be. The bound
	// travels with the age because it is not the same for every object: a
	// census is rewritten when the object's Slot runs, so the bound follows
	// the object's own cadence, and a reader shown only the age cannot tell
	// a census that is behind from one that is exactly as fresh as an hourly
	// strategy's census ever gets.
	Series                uint32 `json:"series"`
	CensusAgeSeconds      int64  `json:"census_age_seconds"`
	CensusAgeBoundSeconds int64  `json:"census_age_bound_seconds"`
	CensusAgeBoundSource  string `json:"census_age_bound_source,omitempty"`
	CensusSource          string `json:"census_source,omitempty"`
	// HeaviestValueSeries is the largest single value's weight and
	// TargetSeries what one piece should carry. The first above the second is
	// the whole of VALUE_TOO_HEAVY, and the two numbers say how far past it
	// is - which is what decides whether a wider cap would help or only
	// hashing will.
	HeaviestValueSeries uint32 `json:"heaviest_value_series"`
	TargetSeries        uint32 `json:"target_series"`
	// TailSeries is the series on values the census could not name; they all
	// land on the piece that catches what no list matches. That piece is
	// expected to be nearly empty - it exists for values that appear after
	// the census - so it is reported here rather than folded into the skew,
	// where it would make every healthy split look lopsided.
	TailSeries uint32 `json:"tail_series"`
	// SkewPercent is the largest carrying piece over the smallest, in
	// percent, for the assignment that was chosen or the best one that was
	// refused. Percent rather than a ratio so the line carries an integer.
	SkewPercent int `json:"skew_percent"`
	// LargestShardSeries and SmallestShardSeries are that skew's two ends, so
	// a reader can tell a lopsided split from a small one.
	LargestShardSeries  uint32 `json:"largest_shard_series"`
	SmallestShardSeries uint32 `json:"smallest_shard_series"`
	// PlansInGroup is how many Plans the Query Group whose bytes were read
	// carries. One means the peak is this Plan's; more means it was shared
	// out among them, and the estimate is that much softer.
	//
	// One object's fact, and only ever that. It carried a round-level count
	// for a while - how many over-share objects the round had skipped - and
	// a reader filtering on it for soft estimates would have caught that
	// line and read the fleet's skipped count as this Plan's group size.
	PlansInGroup int `json:"plans_in_group"`
	// DryRun is true while the planner only reports. It is on the line rather
	// than implied by the build, because the line is the only place a reader
	// can tell a plan that was acted on from one that was not.
	DryRun bool `json:"dry_run"`
}
