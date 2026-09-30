// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func censusObservation(facts *observability.DimensionCensusFacts) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageDimensionCensus,
		Result:          observability.ResultSuccess,
		Trace:           observability.TraceFields{QueryGroupKey: "qg-census", StrategyID: "4101", EvaluationTime: 600},
		DimensionCensus: facts,
	}
}

// A census is counted by where its values came from and by what the store
// did, and the values it named are counted beside the ones it could not.
//
// The two families are separate because they answer separate questions: how
// many censuses were taken is not how much of a strategy they could name, and
// a reader asking the second needs the overflow beside the named values or
// the answer is a number with no denominator.
func TestACensusIsCountedBySourceAndOutcomeWithItsOverflowBesideIt(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	const writes = "bkmonitor_alarmd_dimension_census_total"
	if before := gatherFamily(t, r, writes); len(before) !=
		len(observability.DimensionCensusSources())*len(observability.DimensionCensusStatuses()) {
		t.Fatalf("%d series before any round, want every source and outcome so a zero is a reading", len(before))
	}

	ctx := context.Background()
	r.Observe(ctx, censusObservation(&observability.DimensionCensusFacts{
		Source: observability.DimensionCensusSourceRound, Status: observability.DimensionCensusStatusWritten,
		Series: 12000, Dimensions: 2, Values: 4096, OverflowValues: 104, OverflowSeries: 300,
	}))
	r.Observe(ctx, censusObservation(&observability.DimensionCensusFacts{
		Source: observability.DimensionCensusSourceRoster, Status: observability.DimensionCensusStatusWritten,
		Series: 40, Dimensions: 1, Values: 40,
	}))
	r.Observe(ctx, censusObservation(&observability.DimensionCensusFacts{
		Source: observability.DimensionCensusSourceRound, Status: observability.DimensionCensusStatusRejected,
		Series: 90000, Dimensions: 4, Values: 16000, OverflowValues: 12, OverflowSeries: 40,
	}))
	r.Observe(ctx, censusObservation(nil))

	taken := func(source, status string) float64 {
		return testutil.ToFloat64(r.phaseTwo.dimensionCensusWrites.WithLabelValues(source, status))
	}
	if got := taken(observability.DimensionCensusSourceRound, observability.DimensionCensusStatusWritten); got != 1 {
		t.Fatalf("round/WRITTEN = %v, want 1", got)
	}
	// The fallback has to be countable on its own. It is the one whose values
	// are an upper bound rather than a current reading, and a reader that
	// cannot separate it from the round's is reading a distribution that is
	// partly a memory of groups that are gone.
	if got := taken(observability.DimensionCensusSourceRoster, observability.DimensionCensusStatusWritten); got != 1 {
		t.Fatalf("roster/WRITTEN = %v, want 1: the fallback must be countable apart from the round", got)
	}
	if got := taken(observability.DimensionCensusSourceRound, observability.DimensionCensusStatusRejected); got != 1 {
		t.Fatalf("round/REJECTED = %v, want 1", got)
	}
	if got := taken(observability.DimensionCensusSourceRoster, observability.DimensionCensusStatusRejected); got != 0 {
		t.Fatalf("roster/REJECTED = %v, want 0", got)
	}

	named := func(kind string) float64 {
		return testutil.ToFloat64(r.phaseTwo.dimensionCensusValues.WithLabelValues(kind))
	}
	if got := named("named"); got != 4096+40+16000 {
		t.Fatalf("named values = %v, want every census counted, refused ones included: a census the store "+
			"would not keep still says how wide the strategy is", got)
	}
	if got := named("overflow_values"); got != 116 {
		t.Fatalf("overflow values = %v, want 116", got)
	}
	if got := named("overflow_series"); got != 340 {
		t.Fatalf("overflow series = %v, want 340: read against the named values it is what says whether a "+
			"split can be planned at all", got)
	}
}

// A census that arrives with no source or no outcome is counted under a name,
// not under its own text. The label sets are pre-created from the closed
// vocabularies, and a value arriving at runtime is a series nobody declared.
func TestACensusWithNoSourceOrOutcomeIsCountedUnderTheNamedUnknown(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	before := gatherFamily(t, r, "bkmonitor_alarmd_dimension_census_total")

	r.Observe(context.Background(), censusObservation(&observability.DimensionCensusFacts{Series: 1, Values: 1}))

	if got := testutil.ToFloat64(r.phaseTwo.dimensionCensusWrites.WithLabelValues(
		observability.DimensionCensusSourceUnknown, observability.DimensionCensusStatusUnknown)); got != 1 {
		t.Fatalf("unknown/UNKNOWN = %v, want 1", got)
	}
	if after := gatherFamily(t, r, "bkmonitor_alarmd_dimension_census_total"); len(after) != len(before) {
		t.Fatalf("the family grew from %d series to %d: a census with no source must land on a declared "+
			"label, not create one", len(before), len(after))
	}
}

// Every split outcome and every round disposition has a series before
// anything is observed.
//
// Pinned rather than left to the pre-creation loops still being there. The
// whole reading of this family rests on them: UNDER_SHARE carrying a value
// means the round looked and found nothing to split, while the family being
// empty means no round looked at all - and with the loops gone the second
// state renders as the first. Deleting either loop left the whole library
// green before this existed.
func TestEverySplitOutcomeAndRoundDispositionIsPreCreated(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for family, want := range map[string]int{
		"bkmonitor_alarmd_split_plan_total":          len(observability.SplitOutcomes()),
		"bkmonitor_alarmd_split_round_objects_total": len(splitRoundDispositions),
		"bkmonitor_alarmd_split_rounds_total":        1,
		// Five, written out: the pre-creation and the collector both read
		// the cells from one method, so a count taken from that method here
		// would agree with it whatever it returned.
		"bkmonitor_alarmd_catalog_shardability_plans_total": 5,
	} {
		series := gatherFamily(t, r, family)
		if len(series) != want {
			t.Fatalf("%s pre-created %d series, want %d: a zero is only a reading when the series exists "+
				"before anything writes it", family, len(series), want)
		}
		for _, metric := range series {
			if got := metric.GetCounter().GetValue(); got != 0 {
				t.Fatalf("%s pre-created a series at %v, want zero", family, got)
			}
		}
	}
}

// A round's counts and an object's decision are two families, and one does
// not move the other.
//
// The round's three used to ride out on the object's structure, in a field
// meaning "how many Plans share this object's bytes". One field, two
// subjects: a reader filtering it for soft estimates caught the round's line
// and read the fleet's skipped count as one Plan's group size, and the round
// hitting its bound landed in the same bucket as an object missing a number.
func TestARoundsCountsAndAnObjectsDecisionAreCountedApart(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	ctx := context.Background()

	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSplitPlanned,
		Result:     observability.ResultDegraded,
		SplitRound: &observability.SplitRoundFacts{OverShare: 13, Examined: 8, Skipped: 5},
	})

	for disposition, want := range map[string]float64{"over_share": 13, "examined": 8, "skipped": 5} {
		got := testutil.ToFloat64(r.phaseTwo.splitRoundObjects.WithLabelValues(disposition))
		if got != want {
			t.Fatalf("round %s = %v, want %v", disposition, got, want)
		}
	}
	for _, outcome := range observability.SplitOutcomes() {
		if got := testutil.ToFloat64(r.phaseTwo.splitPlans.WithLabelValues(outcome)); got != 0 {
			t.Fatalf("a round's counts moved the object outcome %q to %v", outcome, got)
		}
	}
}

// A decision this build cannot name is counted under a word of its own, not
// folded into the one for a missing number.
//
// They have different owners and different answers: a missing number is a
// state of the deployment - a pool not reported, a census not written - and
// something an operator can go and look at, while an unrecognised outcome is
// a defect in this build that only a change of code fixes. Folded together, a
// counter that should send someone to the source reads like one more
// environmental condition.
func TestAnUnrecognisedOutcomeIsCountedApartFromAMissingNumber(t *testing.T) {
	r := NewRecorder(BuildInfo{})

	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageSplitPlanned,
		Result:    observability.ResultSuccess,
		SplitPlan: &observability.SplitPlanFacts{Outcome: "INVENTED", DryRun: true},
	})

	if got := testutil.ToFloat64(r.phaseTwo.splitPlans.WithLabelValues(
		observability.SplitOutcomeUnrecognised)); got != 1 {
		t.Fatalf("%s = %v, want 1", observability.SplitOutcomeUnrecognised, got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.splitPlans.WithLabelValues(
		observability.SplitOutcomeNoReading)); got != 0 {
		t.Fatalf("an unrecognised outcome was counted as a missing number (%v): the first is this build's "+
			"defect and the second is the deployment's state", got)
	}
}

// A round that found nothing over its share still counts as a round.
//
// The round family adds each round's counts, so such a round adds zero to
// every cell of it, and a Leader whose dry run ran every round and a Leader
// whose dry run never ran read the same there. The rounds counter is what
// tells them apart; a deployment reading all zeros concluded the second when
// nothing in the family could say which it was.
func TestARoundWithNothingOverItsShareIsStillCountedAsARound(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for range 3 {
		r.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSplitPlanned,
			Result: observability.ResultSuccess, SplitRound: &observability.SplitRoundFacts{},
		})
	}
	if got := testutil.ToFloat64(r.phaseTwo.splitRounds); got != 3 {
		t.Fatalf("split rounds = %v, want 3", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.splitRoundObjects.WithLabelValues("over_share")); got != 0 {
		t.Fatalf("an empty round moved over_share to %v", got)
	}
}

// The catalog's shardability census is a metric, counted once for each
// publication this replica wrote, and not for a write that failed.
//
// It was a log attribute only, and the reading it exists for - how much of
// the fleet a value list cannot cut, against how much of what needs cutting
// it cannot - had no series to read on a deployment. A failed write is
// retried under the same revision and counted when it lands, so counting
// the failure too would count that catalog twice.
func TestTheCatalogShardabilityCensusIsCountedPerSuccessfulPublication(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	census := observability.ShardabilityFacts{Plans: 15, Splittable: 7, Disjunctive: 4, NotStructured: 2, NoQueries: 1, Unrecognised: 1}
	publish := func(result string) {
		r.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageObjectCatalog,
			Result:        observability.Result(result),
			ObjectCatalog: &observability.ObjectCatalogFacts{Operation: "write", Result: result},
			Shardability:  &census,
		})
	}
	publish("failure")
	publish("success")
	for answer, want := range map[string]float64{
		"splittable": 7, "disjunctive": 4, "not_structured": 2, "no_queries": 1, "unrecognised": 1,
	} {
		if got := testutil.ToFloat64(r.phaseTwo.shardabilityPlans.WithLabelValues(answer)); got != want {
			t.Fatalf("shardability %s = %v, want %v", answer, got, want)
		}
	}
}

// The bytes a publication wrote: the objects it stored for the first time,
// whether or not the manifest then landed, since they stay in the store for
// the retention either way; and the manifest of every successful write. A
// renewal writes neither. Both kinds exist at zero before any write.
func TestTheCatalogCountsTheBytesAPublicationWrote(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	bytes := func(kind string) float64 {
		return testutil.ToFloat64(r.phaseTwo.objectCatalogWrittenBytes.WithLabelValues(kind))
	}
	// Read from what a scrape would see, not through WithLabelValues, which
	// would create the series it is asked about.
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	present := 0
	for _, family := range families {
		if family.GetName() == "bkmonitor_alarmd_object_catalog_written_bytes_total" {
			present = len(family.GetMetric())
		}
	}
	if present != 2 {
		t.Fatalf("%d series before any write, want object and manifest at zero", present)
	}
	observe := func(operation, result string, objectBytes, manifestBytes int) {
		r.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageObjectCatalog,
			Result:        observability.Result(result),
			ObjectCatalog: &observability.ObjectCatalogFacts{Operation: operation, Result: result, ObjectBytes: objectBytes, ManifestBytes: manifestBytes},
		})
	}
	observe("write", "success", 5700, 926248)
	observe("write", "failure", 300, 926248)
	observe("renew", "success", 0, 926248)
	if got := bytes("object"); got != 6000 {
		t.Errorf("object bytes = %v, want 6000: both writes stored their objects", got)
	}
	if got := bytes("manifest"); got != 926248 {
		t.Errorf("manifest bytes = %v, want the one successful write's manifest", got)
	}
}
