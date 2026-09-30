// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const splitShare = 1 << 30

func splitPlanIdentity() execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"}
}

// evenCensus is a dimension of `values` values carrying `each` series apiece,
// which is the distribution a value-list split is meant for.
func evenCensus(dimension string, values, each int) execution.DimensionCensus {
	entry := execution.DimensionCensusEntry{Dimension: dimension}
	for index := 0; index < values; index++ {
		entry.Values = append(entry.Values, execution.DimensionValueCount{
			Value: fmt.Sprintf("%s-%04d", dimension, index), Series: uint32(each)})
	}
	return execution.DimensionCensus{
		Identity:   execution.PlanCensusIdentity{Plan: splitPlanIdentity(), StateGeneration: "generation"},
		Source:     execution.DimensionCensusFromRound,
		ObservedAt: 1000, Series: uint32(values * each),
		Dimensions: []execution.DimensionCensusEntry{entry},
	}
}

func splitInput(census execution.DimensionCensus, peak uint64) controlplane.SplitInput {
	return controlplane.SplitInput{
		Plan: splitPlanIdentity(), PeakBytes: peak, ShareBytes: splitShare,
		Census: census, CensusRead: true, At: 1000,
	}
}

// An object over its share is cut into pieces that carry a share of it, plus
// one that carries no list.
//
// The last piece is not an oversight and not spare capacity: it is what every
// value no list names falls to, which is how the pieces stay a cover of the
// strategy when a value appears that the census never saw. Without it a new
// dimension value would be evaluated by nobody, which is an alert that never
// fires and no reading that says so.
func TestAnObjectOverItsShareIsCutIntoCarryingPiecesPlusOneThatCatchesTheRest(t *testing.T) {
	// Three times the share, so the arithmetic asks for six pieces at half a
	// share each.
	plan, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 600, 10), 3*splitShare))

	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q, want a plan; facts = %+v", facts.Outcome, facts)
	}
	if facts.Carrying != 6 || facts.Shards != 7 {
		t.Fatalf("carrying = %d of %d shards, want six carrying and a seventh with no list: "+
			"the piece that catches unnamed values takes no share of the load, so sizing the split as "+
			"though it did plans every piece too large", facts.Carrying, facts.Shards)
	}
	if len(plan.Lists) != facts.Carrying {
		t.Fatalf("%d value lists for %d carrying pieces", len(plan.Lists), facts.Carrying)
	}
	if len(plan.Estimates) != facts.Shards {
		t.Fatalf("%d estimates for %d pieces", len(plan.Estimates), facts.Shards)
	}
	last := plan.Estimates[len(plan.Estimates)-1]
	if !last.Fallback || last.Series != 0 {
		t.Fatalf("the last piece = %+v, want the one with no list, holding this census's tail", last)
	}
	for index, estimate := range plan.Estimates[:facts.Carrying] {
		if estimate.Fallback {
			t.Fatalf("piece %d is marked as the one with no list, and it has one", index)
		}
	}

	// Every value is assigned exactly once: a value in two lists is evaluated
	// twice, and a value in none is evaluated by the piece that catches the
	// rest, which is the piece sized to hold almost nothing.
	seen := map[string]int{}
	for _, list := range plan.Lists {
		for _, value := range list {
			seen[value]++
		}
	}
	if len(seen) != 600 {
		t.Fatalf("the lists name %d of the census's 600 values", len(seen))
	}
	for value, count := range seen {
		if count != 1 {
			t.Fatalf("value %q is in %d lists, want exactly one", value, count)
		}
	}
}

// The pieces come out even, and "even" is measured on the pieces that carry
// the load. The one with no list is nearly empty by design.
func TestThePlannedPiecesAreEvenAcrossThePiecesThatCarryTheLoad(t *testing.T) {
	plan, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 600, 10), 3*splitShare))

	if facts.SkewPercent != 100 {
		t.Fatalf("skew = %d%%, want an even split of six hundred equal values into six pieces", facts.SkewPercent)
	}
	var carried uint32
	for _, estimate := range plan.Estimates {
		if !estimate.Fallback {
			carried += estimate.Series
		}
	}
	if carried != 6000 {
		t.Fatalf("the carrying pieces hold %d series, want every one of the census's 6000", carried)
	}
	if facts.TargetSeries != 1000 {
		t.Fatalf("target = %d series per piece, want 6000 over six", facts.TargetSeries)
	}
}

// A healthy census names every value, so the piece that catches the rest
// carries nothing - and that must not read as an infinitely lopsided split.
//
// This is the shape the first version of the skew got wrong: measured over
// every piece including that one, a perfectly even split divides by zero and
// every object is refused as unsplittable, with a reading that says the
// opposite of what happened.
func TestAnEmptyCatchAllPieceIsNotALopsidedSplit(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	if census.Dimensions[0].OverflowSeries != 0 {
		t.Fatal("fixture: this census must name everything, or it does not exercise the empty catch-all")
	}
	_, facts := controlplane.PlanSplit(splitInput(census, 3*splitShare))
	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q, want a plan: a census that named every value is the healthy case, "+
			"and the piece that catches what it did not name is empty exactly because it is healthy",
			facts.Outcome)
	}
	if facts.TailSeries != 0 {
		t.Fatalf("tail = %d series, want none", facts.TailSeries)
	}
}

// One value carrying more than a piece may hold cannot be split by matching
// values, whatever the lists are - and that is a different answer from "not
// yet" or "too lopsided". It is the object that needs hashing.
func TestAValueHeavierThanAPieceIsRefusedAsIndivisible(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	census.Dimensions[0].Values[0].Series = 4000
	census.Series += 4000 - 10

	_, facts := controlplane.PlanSplit(splitInput(census, 3*splitShare))

	if facts.Outcome != observability.SplitOutcomeValueTooHeavy {
		t.Fatalf("outcome = %q, want %q; facts = %+v",
			facts.Outcome, observability.SplitOutcomeValueTooHeavy, facts)
	}
	if facts.HeaviestValueSeries <= facts.TargetSeries {
		t.Fatalf("the refusal reports a heaviest value of %d against a target of %d, which is not a refusal "+
			"at all: the two numbers are what says whether a wider cap would help or only hashing will",
			facts.HeaviestValueSeries, facts.TargetSeries)
	}
}

// The census's own bound can be what stops a split: series on values it could
// not name all land on the piece that catches the rest, and if they alone
// overfill it the split does not help.
func TestATailHeavierThanAPieceIsRefusedAsTheCensusesBound(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	census.Dimensions[0].OverflowValues = 5000
	census.Dimensions[0].OverflowSeries = 4000
	census.Series += 4000

	_, facts := controlplane.PlanSplit(splitInput(census, 3*splitShare))

	if facts.Outcome != observability.SplitOutcomeTailTooLarge {
		t.Fatalf("outcome = %q, want %q; facts = %+v",
			facts.Outcome, observability.SplitOutcomeTailTooLarge, facts)
	}
	if facts.TailSeries != 4000 {
		t.Fatalf("tail = %d, want the census's own overflow on the line: without it the refusal cannot be "+
			"told from the object being unsplittable", facts.TailSeries)
	}
}

// A split that would be undone as fast as it is made is not planned. The
// readings still say how far off it was, because "refused" and "refused by a
// hair" call for different answers.
func TestASplitTooLopsidedToHoldIsRefusedWithItsSkewOnTheLine(t *testing.T) {
	// Seven equal values into six pieces: every value is inside a piece's
	// target, so nothing here is indivisible, and yet one piece must take two
	// of them and comes out twice the size of the others.
	census := evenCensus("ip", 7, 100)

	_, facts := controlplane.PlanSplit(splitInput(census, 3*splitShare))

	if facts.HeaviestValueSeries > facts.TargetSeries {
		t.Fatalf("fixture: the heaviest value (%d) is past the target (%d), so this exercises the "+
			"indivisible-value refusal rather than the skew", facts.HeaviestValueSeries, facts.TargetSeries)
	}

	if facts.Outcome != observability.SplitOutcomeSkewUnreachable {
		t.Fatalf("outcome = %q, want %q; facts = %+v",
			facts.Outcome, observability.SplitOutcomeSkewUnreachable, facts)
	}
	if facts.SkewPercent <= controlplane.MaxShardSkewPercent {
		t.Fatalf("skew = %d%%, want it past the %d%% threshold and on the line",
			facts.SkewPercent, controlplane.MaxShardSkewPercent)
	}
	if facts.LargestShardSeries == 0 || facts.SmallestShardSeries == 0 {
		t.Fatalf("the refusal reports %d and %d as its two ends, want both: a ratio with no ends cannot "+
			"tell a lopsided split from a small one", facts.LargestShardSeries, facts.SmallestShardSeries)
	}
}

// A census naming fewer values than there are pieces to fill is its own
// answer: the strategy does not vary enough to be cut this many ways.
func TestACensusWithFewerValuesThanPiecesIsRefusedByName(t *testing.T) {
	_, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 3, 2000), 3*splitShare))

	if facts.Outcome != observability.SplitOutcomeTooFewValues {
		t.Fatalf("outcome = %q, want %q; facts = %+v",
			facts.Outcome, observability.SplitOutcomeTooFewValues, facts)
	}
	// And this is only reachable because it is asked before the heaviest
	// value. With fewer values than pieces some value always carries more
	// than a piece's target, so the other order leaves this word unreachable
	// - a refusal nothing can ever be reported under, which reads from a
	// dashboard exactly like a refusal that never happens.
	if facts.HeaviestValueSeries <= facts.TargetSeries {
		t.Fatalf("fixture: the heaviest value (%d) is inside the target (%d), so this case would be "+
			"refused the same way in either order and says nothing about which comes first",
			facts.HeaviestValueSeries, facts.TargetSeries)
	}
}

// The readings that are missing are named as missing. An object whose replica
// has not reported a pool, or that has no peak yet, is not an object under
// its share - and answered that way it would never be looked at again.
func TestAMissingReadingIsNotAnObjectUnderItsShare(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	for name, input := range map[string]controlplane.SplitInput{
		"no share": {Plan: splitPlanIdentity(), PeakBytes: 3 * splitShare, Census: census, CensusRead: true, At: 1000},
		"no peak":  {Plan: splitPlanIdentity(), ShareBytes: splitShare, Census: census, CensusRead: true, At: 1000},
	} {
		if _, facts := controlplane.PlanSplit(input); facts.Outcome != observability.SplitOutcomeNoReading {
			t.Fatalf("%s: outcome = %q, want %q", name, facts.Outcome, observability.SplitOutcomeNoReading)
		}
	}
	// And a census of no series at all is the same kind of gap, not an object
	// whose values are all in the tail.
	empty := evenCensus("ip", 600, 10)
	empty.Series = 0
	if _, facts := controlplane.PlanSplit(splitInput(empty, 3*splitShare)); facts.Outcome != observability.SplitOutcomeNoReading {
		t.Fatalf("a census of no series: outcome = %q, want %q", facts.Outcome, observability.SplitOutcomeNoReading)
	}
}

// An object inside its share is not split, and says so under its own word so
// that "nothing was planned" can be told from "nothing was looked at".
func TestAnObjectInsideItsShareIsNotSplit(t *testing.T) {
	_, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 600, 10), splitShare))
	if facts.Outcome != observability.SplitOutcomeUnderShare {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.SplitOutcomeUnderShare)
	}
	if facts.Shards != 0 {
		t.Fatalf("an object under its share was planned into %d pieces", facts.Shards)
	}
}

// A census nobody has taken is not a census of nothing. The object is over
// its share and the answer is "wait one round", which is a different answer
// from every other refusal here.
func TestAnObjectWithNoCensusIsNamedAsWaitingForOne(t *testing.T) {
	input := splitInput(execution.DimensionCensus{}, 3*splitShare)
	input.CensusRead = false
	_, facts := controlplane.PlanSplit(input)
	if facts.Outcome != observability.SplitOutcomeNoCensus {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.SplitOutcomeNoCensus)
	}
	if facts.Shards == 0 {
		t.Fatalf("the object's size was not reported: a reader cannot tell how badly it needs the census")
	}
}

// A census too old to describe the object is refused rather than planned
// from. A strategy whose targets changed would be cut along values it no
// longer has, and every piece would be wrong in a way nothing would report.
func TestACensusTooOldToDescribeTheObjectIsRefused(t *testing.T) {
	input := splitInput(evenCensus("ip", 600, 10), 3*splitShare)
	input.At = input.Census.ObservedAt + controlplane.MaxCensusAgeSeconds + 1

	_, facts := controlplane.PlanSplit(input)

	if facts.Outcome != observability.SplitOutcomeCensusStale {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.SplitOutcomeCensusStale)
	}
	if facts.CensusAgeSeconds <= controlplane.MaxCensusAgeSeconds {
		t.Fatalf("age = %ds, want the age that failed on the line", facts.CensusAgeSeconds)
	}
	// And one second inside the bound still plans: a threshold that refuses
	// its own boundary is a threshold nobody can reason about.
	input.At = input.Census.ObservedAt + controlplane.MaxCensusAgeSeconds
	if _, facts := controlplane.PlanSplit(input); facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("at exactly the age bound: outcome = %q, want a plan", facts.Outcome)
	}
}

// An object slower than the fixed floor is judged by its own cadence, not by
// the floor.
//
// This is the third point a threshold test needs, past the two either side of
// the line: the worst case real load offers. A census is written when the
// object's Slot runs, so an hourly strategy's census is older than any fixed
// figure for most of every hour - held to the floor it would be refused on
// nearly every round, and the whole class of slow objects could never be
// planned. Nothing caps an evaluation interval; the schedule contract asks
// only that it be positive.
func TestAnObjectSlowerThanTheFloorIsJudgedByItsOwnCadence(t *testing.T) {
	const hourly = int64(3600)
	input := splitInput(evenCensus("ip", 600, 10), 3*splitShare)
	input.EvaluationIntervalSeconds = hourly

	// A census written one round ago - as fresh as an hourly object's census
	// ever is - is far past the floor and must still be planned from.
	input.At = input.Census.ObservedAt + hourly
	if input.At-input.Census.ObservedAt <= controlplane.MaxCensusAgeSeconds {
		t.Fatalf("fixture: an hourly object's freshest census is inside the %ds floor, so this proves nothing",
			controlplane.MaxCensusAgeSeconds)
	}
	_, facts := controlplane.PlanSplit(input)
	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q for a census one round old on an hourly object, want a plan: judged by a "+
			"fixed floor, every object slower than the floor is refused on nearly every round and the "+
			"reading blames the census", facts.Outcome)
	}
	if facts.CensusAgeBoundSource != observability.SplitCensusBoundCadence {
		t.Fatalf("the bound came from %q, want the object's own cadence", facts.CensusAgeBoundSource)
	}

	// Far past its own cadence it is stale like anything else. Asserted at a
	// number this test writes out rather than one computed from the same
	// constant the code uses: an expectation that moves with the constant
	// tests that the bound equals the bound.
	input.At = input.Census.ObservedAt + 4*hourly
	if _, facts := controlplane.PlanSplit(input); facts.Outcome != observability.SplitOutcomeCensusStale {
		t.Fatalf("outcome = %q four hours after an hourly object's census was written, want %q",
			facts.Outcome, observability.SplitOutcomeCensusStale)
	}
}

// A census one round old plus the jitter of being read in a later round is
// still planned from. This is the property the cadence multiple exists for,
// and the only assertion that can fail if it is wrong.
//
// A census is written when a Slot runs and read by a round that comes after,
// so it is ALREADY one interval old the first time anyone looks at it - one
// interval of allowance is no allowance, and the schedule does not promise
// the reading round lands the same second. The earlier version of this test
// asserted against `interval * CensusAgeCadenceMultiple`, so the expectation
// moved with the constant and the constant could be set to one with every
// test still green; the fixture also sat exactly on the boundary, which the
// strict comparison lets through.
func TestACensusOneRoundOldPlusJitterIsStillPlannedFrom(t *testing.T) {
	const hourly = int64(3600)
	const jitter = int64(90)
	input := splitInput(evenCensus("ip", 600, 10), 3*splitShare)
	input.EvaluationIntervalSeconds = hourly
	input.At = input.Census.ObservedAt + hourly + jitter

	_, facts := controlplane.PlanSplit(input)

	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q for a census one interval and %ds old, want a plan: an hourly object's "+
			"census is one interval old the moment it is first read, so a bound of one interval refuses "+
			"it for any scheduling jitter at all", facts.Outcome, jitter)
	}
}

// A fast object is not given a bound tighter than the floor. The cadence
// raises the bound and never lowers it: a ten-second object whose census is a
// minute old is not describing a different strategy, and refusing it would
// make the fastest objects the hardest to plan.
func TestAFastObjectIsNotJudgedMoreTightlyThanTheFloor(t *testing.T) {
	input := splitInput(evenCensus("ip", 600, 10), 3*splitShare)
	input.EvaluationIntervalSeconds = 10
	input.At = input.Census.ObservedAt + controlplane.MaxCensusAgeSeconds

	_, facts := controlplane.PlanSplit(input)

	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q for a ten-second object at the floor, want a plan", facts.Outcome)
	}
	if facts.CensusAgeBoundSeconds != controlplane.MaxCensusAgeSeconds {
		t.Fatalf("bound = %ds, want the floor %ds", facts.CensusAgeBoundSeconds, controlplane.MaxCensusAgeSeconds)
	}
	if facts.CensusAgeBoundSource != observability.SplitCensusBoundFloorFast {
		t.Fatalf("the bound came from %q, want the floor named as the fast object's", facts.CensusAgeBoundSource)
	}
}

// An object whose cadence is unknown falls back to the floor rather than to
// no bound: a missing number is not permission to plan from any census at
// all.
func TestAnObjectWithNoKnownCadenceFallsBackToTheFloor(t *testing.T) {
	input := splitInput(evenCensus("ip", 600, 10), 3*splitShare)
	input.At = input.Census.ObservedAt + controlplane.MaxCensusAgeSeconds + 1

	_, facts := controlplane.PlanSplit(input)
	if facts.Outcome != observability.SplitOutcomeCensusStale {
		t.Fatalf("outcome = %q with no cadence known, want the floor still applied (%q)",
			facts.Outcome, observability.SplitOutcomeCensusStale)
	}
	// And it says the bound was a guess. The floor reached this way and the
	// floor reached by a fast object are the same number, for opposite
	// reasons: the first may be refusing a census that is perfectly fresh
	// for an object nobody told us the cadence of - the misattribution the
	// cadence bound exists to remove - and the second is a census stale by
	// many of the object's own rounds. Without the word they are identical
	// on the line.
	if facts.CensusAgeBoundSource != observability.SplitCensusBoundFloorUnknown {
		t.Fatalf("the bound came from %q, want it named as a guess", facts.CensusAgeBoundSource)
	}
}

// One census plans one split, run after run. This says the planner does not
// depend on map order; it does NOT say the tie-breaks are the ones intended,
// because a planner that broke every tie the other way round would agree with
// itself just as well. The two tests after it pin the rules themselves.
func TestTwoLeadersReadingOneCensusPlanTheSameSplit(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	// A second dimension that divides worse, so the choice between them is a
	// real one and not a formality.
	census.Dimensions = append(census.Dimensions, execution.DimensionCensusEntry{
		Dimension: "module",
		Values: []execution.DimensionValueCount{
			{Value: "a", Series: 3000}, {Value: "b", Series: 1000}, {Value: "c", Series: 800},
			{Value: "d", Series: 600}, {Value: "e", Series: 400}, {Value: "f", Series: 200},
		},
	})
	first, firstFacts := controlplane.PlanSplit(splitInput(census, 3*splitShare))
	for round := 0; round < 8; round++ {
		next, nextFacts := controlplane.PlanSplit(splitInput(census, 3*splitShare))
		if nextFacts != firstFacts {
			t.Fatalf("two readings of one census disagree:\n%+v\n%+v", firstFacts, nextFacts)
		}
		if fmt.Sprint(next.Lists) != fmt.Sprint(first.Lists) {
			t.Fatalf("two readings of one census assign values differently:\n%v\n%v", first.Lists, next.Lists)
		}
	}
	if firstFacts.Dimension != "ip" {
		t.Fatalf("split on %q, want the dimension that divides most evenly", firstFacts.Dimension)
	}
	if firstFacts.Candidates != 2 {
		t.Fatalf("the line says %d dimensions were offered, want both: one of one and one of several are "+
			"different facts about whether there was a choice", firstFacts.Candidates)
	}
}

// An object too large for the cap is planned at the cap and says so. Rounding
// it away would report a split that leaves every piece over its share as
// though it had fixed the problem.
func TestAnObjectPastTheShardCapIsPlannedAtTheCapAndSaysSo(t *testing.T) {
	_, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 6000, 10), 200*splitShare))

	if !facts.ShardsCapped {
		t.Fatalf("an object needing four hundred pieces was planned into %d without saying so", facts.Shards)
	}
	if facts.Shards != controlplane.MaxShardsPerStrategy {
		t.Fatalf("shards = %d, want the cap %d", facts.Shards, controlplane.MaxShardsPerStrategy)
	}
	if facts.Carrying != controlplane.MaxShardsPerStrategy-1 {
		t.Fatalf("carrying = %d, want the cap less the piece that carries no list", facts.Carrying)
	}
}

// Every plan is a dry run for now, and the line says so. A reader looking at
// a plan cannot otherwise tell one that was acted on from one that was not,
// and that is the whole of what this batch is.
func TestEveryPlanSaysItWasNotActedOn(t *testing.T) {
	for _, peak := range []uint64{splitShare / 2, 3 * splitShare, 200 * splitShare} {
		if _, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 600, 10), peak)); !facts.DryRun {
			t.Fatalf("peak %d: the plan does not say it was a dry run", peak)
		}
	}
}

// Ties between dimensions go to the name that sorts first.
//
// Pinned as a rule rather than left to whatever order the census arrived in:
// the tie is broken the same way by every build, which is what makes a split
// survive a leader handover mid-rollout. Two leaders of one build agree
// however the tie is broken - that is what makes the self-consistency test
// above unable to see this - but two builds do not, and a strategy cut one
// way by the leader that planned it and another way by the leader that
// carries it out has two sets of pieces alive at once, each holding series
// the other also holds.
func TestATieBetweenDimensionsGoesToTheNameThatSortsFirst(t *testing.T) {
	census := evenCensus("ip", 600, 10)
	// The same distribution under a second name, so the two divide equally
	// well and nothing but the tie rule separates them.
	twin := census.Dimensions[0]
	twin.Dimension = "zone"
	twin.Values = append([]execution.DimensionValueCount(nil), twin.Values...)
	census.Dimensions = append(census.Dimensions, twin)

	_, facts := controlplane.PlanSplit(splitInput(census, 3*splitShare))

	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q, want a plan", facts.Outcome)
	}
	if facts.Dimension != "ip" {
		t.Fatalf("the tie went to %q, want %q - the name that sorts first. Both dimensions divide this "+
			"census equally well, so whichever is chosen must be chosen by a rule every build follows",
			facts.Dimension, "ip")
	}
}

// Ties between equally heavy values go to the value that sorts first, and the
// assignment that follows is the same on every build.
//
// The same blind spot as the dimension tie: equal values assigned in reverse
// order still fill the pieces evenly, so nothing about the shape of the split
// changes and only the contents of each list do. Those contents are what a
// piece matches on, so two builds disagreeing about them is two pieces each
// claiming the same series.
func TestATieBetweenEquallyHeavyValuesGoesToTheValueThatSortsFirst(t *testing.T) {
	plan, facts := controlplane.PlanSplit(splitInput(evenCensus("ip", 600, 10), 3*splitShare))

	if facts.Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q, want a plan", facts.Outcome)
	}
	// Every value here carries the same weight, so the order they are handed
	// out in is the order they sort in, and the first piece takes the first.
	if plan.Lists[0][0] != "ip-0000" {
		t.Fatalf("the first piece's first value is %q, want %q: with every value equally heavy the "+
			"assignment order is the value order, and it has to be the same order on every build",
			plan.Lists[0][0], "ip-0000")
	}
	if plan.Lists[len(plan.Lists)-1][0] != "ip-0005" {
		t.Fatalf("the last piece's first value is %q, want %q", plan.Lists[len(plan.Lists)-1][0], "ip-0005")
	}
}
