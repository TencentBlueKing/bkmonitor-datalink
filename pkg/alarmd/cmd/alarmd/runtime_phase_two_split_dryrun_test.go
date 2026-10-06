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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

const splitTestPool = uint64(2) << 30

func splitTestWorkers(pools map[string]uint64) []ownership.WorkerRegistration {
	workers := make([]ownership.WorkerRegistration, 0, len(pools))
	for id, pool := range pools {
		registration := ownership.WorkerRegistration{WorkerID: id}
		if pool > 0 {
			registration.Load = &ownership.WorkerLoad{RetainedPoolBytes: pool}
		}
		workers = append(workers, registration)
	}
	return workers
}

// An object is a split candidate when it is past the share ONE object may
// hold, which is half its holder's pool - the same number the Worker refuses
// a Slot by and judges a census candidate by. Anything else would have the
// Leader planning splits for objects the Worker is happily running, or none
// for the object it is refusing.
func TestOnlyAnObjectPastItsHoldersShareIsASplitCandidate(t *testing.T) {
	share := splitTestPool / 2
	owners := map[execution.QueryGroupIdentity]string{
		"qg-over":   "worker-a",
		"qg-at":     "worker-a",
		"qg-under":  "worker-a",
		"qg-unread": "worker-a",
	}
	readings := scheduler.ByteReadings{Peak: map[execution.QueryGroupIdentity]uint64{
		"qg-over":  share + 1,
		"qg-at":    share,
		"qg-under": share - 1,
	}}
	candidates, overflow := splitCandidates(owners, splitTestWorkers(map[string]uint64{"worker-a": splitTestPool}), readings)

	if overflow != 0 {
		t.Fatalf("overflow = %d, want none", overflow)
	}
	if len(candidates) != 1 || candidates[0].QueryGroup != "qg-over" {
		t.Fatalf("candidates = %+v, want only the object past its share", candidates)
	}
	if candidates[0].ShareBytes != share {
		t.Fatalf("share = %d, want half the pool (%d)", candidates[0].ShareBytes, share)
	}
}

// An object whose holder reported no pool is not judged. An unknown pool is
// not a large one, and read as one every object on a replica that has not
// reported would be planned for a split it does not need.
func TestAnObjectWhoseHolderReportedNoPoolIsNotJudged(t *testing.T) {
	owners := map[execution.QueryGroupIdentity]string{"qg": "worker-a"}
	readings := scheduler.ByteReadings{Peak: map[execution.QueryGroupIdentity]uint64{"qg": 1 << 40}}

	candidates, _ := splitCandidates(owners, splitTestWorkers(map[string]uint64{"worker-a": 0}), readings)

	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want none: a replica that has not said how large its pool is has not "+
			"said its objects are too large for it", candidates)
	}
}

// Far more objects over their share than a split trigger should ever name is
// not a split problem - it is a fleet whose pools or peaks are being misread.
// The round works out the worst few and says how many it left, rather than
// reading every object's Plans and censuses to report the same thing many
// times.
func TestARoundWithTooManyOverShareObjectsWorksOutTheWorstAndCountsTheRest(t *testing.T) {
	owners := map[execution.QueryGroupIdentity]string{}
	peaks := map[execution.QueryGroupIdentity]uint64{}
	for index := 0; index < splitDryRunMaxObjects+5; index++ {
		identity := execution.QueryGroupIdentity(fmt.Sprintf("qg-%02d", index))
		owners[identity] = "worker-a"
		peaks[identity] = splitTestPool/2 + uint64(index) + 1
	}
	candidates, overflow := splitCandidates(owners,
		splitTestWorkers(map[string]uint64{"worker-a": splitTestPool}), scheduler.ByteReadings{Peak: peaks})

	if len(candidates) != splitDryRunMaxObjects || overflow != 5 {
		t.Fatalf("%d candidates and %d left over, want %d and 5", len(candidates), overflow, splitDryRunMaxObjects)
	}
	// The heaviest survive the bound, and which ones must not depend on map
	// order: the bound exists to keep the worst objects, and a round that
	// kept a different eight each time would report a different fleet each
	// time.
	for index := 1; index < len(candidates); index++ {
		if candidates[index-1].PeakBytes < candidates[index].PeakBytes {
			t.Fatalf("candidates are not heaviest first: %+v", candidates)
		}
	}
	if candidates[0].QueryGroup != "qg-12" {
		t.Fatalf("the heaviest candidate is %q, want the largest peak", candidates[0].QueryGroup)
	}
}

// A Query Group carrying several Plans says so on every line it produces,
// and divides its bytes among them.
//
// The number is what says how soft the byte estimate is: an object whose
// peak is one Plan's is a reading, and one shared among four is an
// apportionment. A line that always claimed one would present the second as
// the first.
func TestAGroupOfSeveralPlansSaysSoAndDividesItsBytes(t *testing.T) {
	first := execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
	second := execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4102"},
		StateGeneration: "generation",
	}
	entry := func(values int) execution.DimensionCensusEntry {
		e := execution.DimensionCensusEntry{Dimension: "ip"}
		for index := 0; index < values; index++ {
			e.Values = append(e.Values, execution.DimensionValueCount{
				Value: fmt.Sprintf("ip-%04d", index), Series: 10})
		}
		return e
	}
	source := &fakeSplitSource{
		plans: map[execution.QueryGroupIdentity][]splitCandidatePlan{"qg": {{Census: first}, {Census: second}}},
		censuses: map[execution.PlanCensusIdentity]execution.DimensionCensus{
			first: {Identity: first, Source: execution.DimensionCensusFromRound,
				ObservedAt: splitDryRunTestClock().Unix(), Series: 6000,
				Dimensions: []execution.DimensionCensusEntry{entry(600)}},
			second: {Identity: second, Source: execution.DimensionCensusFromRound,
				ObservedAt: splitDryRunTestClock().Unix(), Series: 2000,
				Dimensions: []execution.DimensionCensusEntry{entry(200)}},
		},
	}

	facts := splitDryRunObservations(t, source, 4*splitTestPool)

	if len(facts) != 2 {
		t.Fatalf("%d observations, want one per Plan", len(facts))
	}
	for _, fact := range facts {
		if fact.PlansInGroup != 2 {
			t.Fatalf("plans in group = %d for strategy %s, want 2: an apportioned peak presented as a "+
				"Plan's own reading is a split sized from somebody else's bytes",
				fact.PlansInGroup, fact.StrategyID)
		}
	}
	// Three quarters of the series are the first Plan's, so three quarters of
	// the bytes are what it is answerable for - and the pieces it is planned
	// into follow from that number, not from the group's.
	byStrategy := map[string]observability.SplitPlanFacts{}
	for _, fact := range facts {
		byStrategy[fact.StrategyID] = fact
	}
	if byStrategy["4101"].PeakBytes <= byStrategy["4102"].PeakBytes {
		t.Fatalf("the Plan with three quarters of the series was given %d bytes against the other's %d",
			byStrategy["4101"].PeakBytes, byStrategy["4102"].PeakBytes)
	}
	if byStrategy["4101"].Shards <= byStrategy["4102"].Shards {
		t.Fatalf("the heavier Plan was planned into %d pieces and the lighter into %d",
			byStrategy["4101"].Shards, byStrategy["4102"].Shards)
	}
}

// A Query Group carrying one Plan hands it the whole peak. A Query Group
// carrying several divides the peak by the only reading that says how they
// share it, and a Plan with no census in such a group gets nothing rather
// than a guess - which the planner then answers as "no census" instead of
// planning a split from a number nobody measured.
func TestThePeakIsAttributedToThePlanThatIsAnswerableForIt(t *testing.T) {
	census := func(series uint32) execution.DimensionCensus {
		return execution.DimensionCensus{Series: series}
	}
	if got := attributedPeakBytes(1000, census(10), true, 10, 1); got != 1000 {
		t.Fatalf("one Plan got %d of 1000, want all of it", got)
	}
	if got := attributedPeakBytes(1000, census(30), true, 100, 3); got != 300 {
		t.Fatalf("a Plan with 30 of 100 series got %d of 1000, want 300", got)
	}
	if got := attributedPeakBytes(1000, execution.DimensionCensus{}, false, 100, 3); got != 0 {
		t.Fatalf("a Plan with no census got %d of the peak, want nothing: a share of the bytes handed to a "+
			"Plan nobody counted is a split planned from a number that was never measured", got)
	}
	if got := attributedPeakBytes(1000, census(10), true, 0, 3); got != 0 {
		t.Fatalf("a group where nothing was counted gave a Plan %d, want nothing", got)
	}
}

type fakeSplitSource struct {
	plans    map[execution.QueryGroupIdentity][]splitCandidatePlan
	censuses map[execution.PlanCensusIdentity]execution.DimensionCensus
	planErr  error
	reads    int
}

func (source *fakeSplitSource) SplitCandidatePlans(
	_ context.Context, queryGroup execution.QueryGroupIdentity,
) ([]splitCandidatePlan, error) {
	if source.planErr != nil {
		return nil, source.planErr
	}
	return source.plans[queryGroup], nil
}

func (source *fakeSplitSource) ReadCensus(
	_ context.Context, identity execution.PlanCensusIdentity,
) (execution.DimensionCensus, bool, error) {
	source.reads++
	census, found := source.censuses[identity]
	return census, found, nil
}

func splitDryRunObservations(t *testing.T, source splitCensusSource, peak uint64) []observability.SplitPlanFacts {
	t.Helper()
	facts, _ := splitDryRunFacts(t, source, peak)
	return facts
}

func splitDryRunFacts(
	t *testing.T, source splitCensusSource, peak uint64,
) ([]observability.SplitPlanFacts, []observability.SplitRoundFacts) {
	facts, rounds, _ := splitDryRunAllFacts(t, source, peak)
	return facts, rounds
}

func splitDryRunAllFacts(
	t *testing.T, source splitCensusSource, peak uint64,
) ([]observability.SplitPlanFacts, []observability.SplitRoundFacts, []observability.ShardQueryFacts) {
	t.Helper()
	var facts []observability.SplitPlanFacts
	var rounds []observability.SplitRoundFacts
	var queries []observability.ShardQueryFacts
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
		SplitCensus: source,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.SplitPlan != nil {
				facts = append(facts, *observation.SplitPlan)
			}
			if observation.SplitRound != nil {
				rounds = append(rounds, *observation.SplitRound)
			}
			if observation.ShardQuery != nil {
				queries = append(queries, *observation.ShardQuery)
			}
		}),
	}}
	runtime.dryRunSplits(context.Background(),
		map[execution.QueryGroupIdentity]string{"qg": "worker-a"},
		splitTestWorkers(map[string]uint64{"worker-a": splitTestPool}),
		scheduler.ByteReadings{Peak: map[execution.QueryGroupIdentity]uint64{"qg": peak}},
		splitDryRunTestClock())
	return facts, rounds, queries
}

// An object whose Plans this round could not read is reported as a reading
// that is missing, not passed over. Passed over it would look exactly like an
// object that is not over its share, which is the opposite fact.
func TestAnObjectWhosePlansCannotBeReadIsReportedRatherThanSkipped(t *testing.T) {
	facts := splitDryRunObservations(t, &fakeSplitSource{planErr: errors.New("catalog unavailable")}, splitTestPool)

	if len(facts) != 1 {
		t.Fatalf("%d observations, want one for the object that could not be read", len(facts))
	}
	if facts[0].Outcome != observability.SplitOutcomeNoReading {
		t.Fatalf("outcome = %q, want %q", facts[0].Outcome, observability.SplitOutcomeNoReading)
	}
	if facts[0].PeakBytes == 0 || facts[0].ShareBytes == 0 {
		t.Fatalf("the two numbers that made it a candidate are not on the line: %+v", facts[0])
	}
}

// Every plan the dry run reports says it was a dry run, and nothing it does
// reaches the store: the whole of this batch is a reading.
func TestTheDryRunReportsEveryPlanAsADryRun(t *testing.T) {
	plan := execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
	entry := execution.DimensionCensusEntry{Dimension: "ip"}
	for index := 0; index < 600; index++ {
		entry.Values = append(entry.Values, execution.DimensionValueCount{
			Value: fmt.Sprintf("ip-%04d", index), Series: 10})
	}
	source := &fakeSplitSource{
		plans: map[execution.QueryGroupIdentity][]splitCandidatePlan{"qg": {{Census: plan}}},
		censuses: map[execution.PlanCensusIdentity]execution.DimensionCensus{plan: {
			Identity: plan, Source: execution.DimensionCensusFromRound,
			ObservedAt: splitDryRunTestClock().Unix(), Series: 6000,
			Dimensions: []execution.DimensionCensusEntry{entry},
		}},
	}

	facts := splitDryRunObservations(t, source, 3*splitTestPool)

	if len(facts) != 1 {
		t.Fatalf("%d observations, want one per Plan", len(facts))
	}
	if !facts[0].DryRun {
		t.Fatalf("the plan does not say it was a dry run: %+v", facts[0])
	}
	if facts[0].Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("outcome = %q, want a plan; facts = %+v", facts[0].Outcome, facts[0])
	}
	if facts[0].PlansInGroup != 1 {
		t.Fatalf("plans in group = %d, want one: it is what says how soft the byte estimate is",
			facts[0].PlansInGroup)
	}
	if source.reads != 1 {
		t.Fatalf("%d census reads for one Plan", source.reads)
	}
}

// splitDryRunTestClock is the round's clock, fixed so a census taken "now" is
// not aged out by the time the assertion runs.
func splitDryRunTestClock() time.Time { return time.Unix(1_700_000_000, 0) }

// The round reports what it looked at every round, not only when it skipped
// something.
//
// The three counts are each other's denominator: a skipped count on its own
// cannot say whether a zero means nothing was left out or nothing was looked
// at, and those are the two states a reader most needs to separate before
// trusting a dry run.
func TestTheRoundReportsWhatItLookedAtEvenWhenItSkippedNothing(t *testing.T) {
	plan := execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
	source := &fakeSplitSource{plans: map[execution.QueryGroupIdentity][]splitCandidatePlan{"qg": {{Census: plan}}}}

	_, rounds := splitDryRunFacts(t, source, 3*splitTestPool)

	if len(rounds) != 1 {
		t.Fatalf("%d round observations, want exactly one per round", len(rounds))
	}
	if rounds[0].OverShare != 1 || rounds[0].Examined != 1 || rounds[0].Skipped != 0 {
		t.Fatalf("round = %+v, want one object over its share, examined, none skipped", rounds[0])
	}

	// And an object under its share leaves the counts at zero rather than
	// leaving the line out: no line at all is how "the dry run did not run"
	// looks, which is the opposite reading.
	_, quiet := splitDryRunFacts(t, source, splitTestPool/4)
	if len(quiet) != 1 {
		t.Fatalf("%d round observations for a quiet round, want one", len(quiet))
	}
	if quiet[0].OverShare != 0 || quiet[0].Examined != 0 {
		t.Fatalf("quiet round = %+v, want zeros", quiet[0])
	}
}

// The round's counts never travel on an object's structure. They did once,
// in the field that says how many Plans share an object's bytes, which gave
// one field two subjects.
func TestTheRoundsCountsNeverRideOnAnObjectsFacts(t *testing.T) {
	owners := map[execution.QueryGroupIdentity]string{}
	peaks := map[execution.QueryGroupIdentity]uint64{}
	for index := 0; index < splitDryRunMaxObjects+3; index++ {
		identity := execution.QueryGroupIdentity(fmt.Sprintf("qg-%02d", index))
		owners[identity] = "worker-a"
		peaks[identity] = splitTestPool/2 + uint64(index) + 1
	}
	var facts []observability.SplitPlanFacts
	var rounds []observability.SplitRoundFacts
	runtime := &productionPhaseTwoOwnership{dependencies: productionPhaseTwoOwnershipDependencies{
		SplitCensus: &fakeSplitSource{},
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.SplitPlan != nil {
				facts = append(facts, *observation.SplitPlan)
			}
			if observation.SplitRound != nil {
				rounds = append(rounds, *observation.SplitRound)
			}
		}),
	}}
	runtime.dryRunSplits(context.Background(), owners,
		splitTestWorkers(map[string]uint64{"worker-a": splitTestPool}),
		scheduler.ByteReadings{Peak: peaks}, splitDryRunTestClock())

	if len(rounds) != 1 || rounds[0].Skipped != 3 {
		t.Fatalf("rounds = %+v, want one saying three were skipped", rounds)
	}
	for _, fact := range facts {
		if fact.PlansInGroup > 1 {
			t.Fatalf("an object's line claims %d Plans share its bytes; the round skipped %d objects and "+
				"that number must not appear here - a reader filtering this field for soft estimates "+
				"would read the fleet's skipped count as one Plan's group size",
				fact.PlansInGroup, rounds[0].Skipped)
		}
	}
}

// splitDryRunQueries is one logical query that groups by the split dimension,
// with whatever conditions the strategy came with.
func splitDryRunQueries(t *testing.T, conditions execution.QueryConditions) map[execution.LogicalQueryRef]execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary", TenantID: "system",
		BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{
			DataSource: "bk_monitor", Driver: "influxdb", TableID: "system.cpu", FieldName: "usage",
			TimeField: "time", ReferenceName: "a", Dimensions: []string{"ip"},
			Conditions:      conditions,
			Functions:       []execution.QueryFunction{{Method: "default", Position: 0}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0},
		}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000,
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{SchemaDigest: "schema", NormalizationDigest: "normalization",
				IdentityFields: []string{"ip"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return map[execution.LogicalQueryRef]execution.QueryPlanFacts{"q-a": facts}
}

func splitDryRunPlannedSource(
	t *testing.T, queries map[execution.LogicalQueryRef]execution.QueryPlanFacts,
) *fakeSplitSource {
	t.Helper()
	plan := execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
	entry := execution.DimensionCensusEntry{Dimension: "ip"}
	for index := 0; index < 600; index++ {
		entry.Values = append(entry.Values, execution.DimensionValueCount{
			Value: fmt.Sprintf("ip-%04d", index), Series: 10})
	}
	return &fakeSplitSource{
		plans: map[execution.QueryGroupIdentity][]splitCandidatePlan{
			"qg": {{Census: plan, EvaluationIntervalSeconds: 60, Queries: queries}}},
		censuses: map[execution.PlanCensusIdentity]execution.DimensionCensus{plan: {
			Identity: plan, Source: execution.DimensionCensusFromRound,
			ObservedAt: splitDryRunTestClock().Unix(), Series: 6000,
			Dimensions: []execution.DimensionCensusEntry{entry},
		}},
	}
}

// An object a split was planned for is also asked whether its own query can
// express that split, on the same line.
//
// The two answers are separate questions with separate vocabularies, and a
// reader needs both: a strategy can be perfectly worth splitting and
// impossible to cut. Read against the catalog's own census of every Plan,
// this is what says whether value lists miss precisely the strategies that
// need splitting - which is the number that decides whether hashing is the
// main road.
func TestAPlannedSplitIsAlsoAskedWhetherTheQueryCanExpressIt(t *testing.T) {
	source := splitDryRunPlannedSource(t, splitDryRunQueries(t, execution.QueryConditions{}))

	facts, _, queries := splitDryRunAllFacts(t, source, 3*splitTestPool)

	if len(facts) != 1 || facts[0].Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("split facts = %+v, want one planned split", facts)
	}
	if len(queries) != 1 {
		t.Fatalf("%d shard-query answers for one planned split, want one beside it", len(queries))
	}
	if queries[0].Outcome != observability.ShardQueriesBuilt {
		t.Fatalf("outcome = %q, want the queries built; facts = %+v", queries[0].Outcome, queries[0])
	}
	if queries[0].Shards != facts[0].Shards || queries[0].Built != facts[0].Shards {
		t.Fatalf("the transform built %d of %d pieces for a split planned into %d",
			queries[0].Built, queries[0].Shards, facts[0].Shards)
	}
}

// A strategy whose own conditions are disjunctive is planned and then found
// impossible to cut, and both facts reach the line.
//
// This is the pair the design question rests on. The planner says the split
// would be even; the transform says a flat condition list cannot express it.
// Reported as one it "could not split" alone, a reader would go looking for
// a census problem.
func TestAnObjectWorthSplittingAndImpossibleToCutReportsBoth(t *testing.T) {
	disjunctive := execution.QueryConditions{
		Fields: []execution.QueryConditionField{
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}},
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "staging"}}},
		},
		Connectors: []string{"or"},
	}
	source := splitDryRunPlannedSource(t, splitDryRunQueries(t, disjunctive))

	facts, _, queries := splitDryRunAllFacts(t, source, 3*splitTestPool)

	if facts[0].Outcome != observability.SplitOutcomePlanned {
		t.Fatalf("split outcome = %q, want the planner to still say it is worth splitting: whether the "+
			"query can express it is a different question and must not change this one", facts[0].Outcome)
	}
	if len(queries) != 1 || queries[0].Outcome != observability.ShardQueriesDisjunctive {
		t.Fatalf("shard-query answers = %+v, want one saying the conditions are disjunctive", queries)
	}
}

// An object no split was planned for is not asked the question at all -
// including an object that IS over its share and was looked at.
//
// Asked anyway it would answer NOT_PLANNED, which is a count of "there was
// nothing to build from" wearing the clothes of a refusal. The object under
// its share cannot show this on its own: it never becomes a candidate, so
// the guard is never reached. The one that shows it is over its share and
// has no census.
func TestAnObjectWithNoPlannedSplitIsNotAskedAboutItsQueries(t *testing.T) {
	source := splitDryRunPlannedSource(t, splitDryRunQueries(t, execution.QueryConditions{}))

	_, _, queries := splitDryRunAllFacts(t, source, splitTestPool/4)
	if len(queries) != 0 {
		t.Fatalf("%d shard-query answers for an object under its share, want none: every object that does "+
			"not need splitting would otherwise be counted as one that could not be split", len(queries))
	}

	// Over its share, examined, and no census to plan from. This one reaches
	// the guard.
	source.censuses = nil
	facts, _, queries := splitDryRunAllFacts(t, source, 3*splitTestPool)
	if len(facts) != 1 || facts[0].Outcome != observability.SplitOutcomeNoCensus {
		t.Fatalf("split facts = %+v, want one object waiting for a census", facts)
	}
	if len(queries) != 0 {
		t.Fatalf("%d shard-query answers for an object with nothing planned, want none: there is no "+
			"dimension and no value list to build from, and the answer would be a refusal for a case that "+
			"is simply not ready", len(queries))
	}
}
