// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func splitPlanEvent(t *testing.T, facts *SplitPlanFacts) map[string]any {
	t.Helper()
	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentControlPlane, Stage: StageSplitPlanned, Result: ResultSuccess,
		Trace:     TraceFields{StrategyID: "4101", BusinessID: "2", QueryGroupKey: "qg-split"},
		SplitPlan: facts,
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode split log: %v; log=%s", err, output.String())
	}
	return event
}

// The split line carries the decision and every reading it was judged from.
//
// Asserted against the log rather than the struct, and the stage is asserted
// by the line existing at all: a stage missing from the classification table
// renders nothing however complete its attributes are, which is a fourth way
// for a fact to be attached and unreadable.
func TestTheSplitLineCarriesTheDecisionAndTheReadingsItWasJudgedFrom(t *testing.T) {
	t.Parallel()

	event := splitPlanEvent(t, &SplitPlanFacts{
		Outcome: SplitOutcomeValueTooHeavy, StrategyID: "4101", BusinessID: "2",
		PeakBytes: 3 << 30, ShareBytes: 1 << 30, Shards: 7, Carrying: 6, ShardsCapped: true,
		Dimension: "ip", Candidates: 2, Series: 6000, CensusAgeSeconds: 42, CensusAgeBoundSeconds: 7200,
		CensusAgeBoundSource: SplitCensusBoundCadence,
		CensusSource:         "round",
		HeaviestValueSeries:  4000, TargetSeries: 1000, TailSeries: 12, SkewPercent: 180,
		LargestShardSeries: 1800, SmallestShardSeries: 1000, PlansInGroup: 1, DryRun: true,
	})

	want := map[string]any{
		"split_outcome":     "VALUE_TOO_HEAVY",
		"split_peak_bytes":  float64(3 << 30),
		"split_share_bytes": float64(1 << 30),
		"split_shards":      float64(7),
		// The two numbers that make VALUE_TOO_HEAVY what it is: a value
		// carrying more than a piece may hold. Without both, the refusal
		// cannot be told from "too lopsided" and nobody can say whether a
		// wider cap would help or only hashing will.
		"split_heaviest_value_series": float64(4000),
		"split_target_series":         float64(1000),
		"split_carrying_shards":       float64(6),
		"split_shards_capped":         true,
		"split_dimension":             "ip",
		"split_dimension_candidates":  float64(2),
		"split_series":                float64(6000),
		"split_census_age_seconds":    float64(42),
		// The bound beside the age: it is not the same for every object, so
		// an age on its own cannot say whether a census is behind.
		"split_census_age_bound_seconds": float64(7200),
		// And where that bound came from: two of the three sources produce
		// the same number for opposite reasons.
		"split_census_age_bound_source": SplitCensusBoundCadence,
		"split_census_source":           "round",
		"split_tail_series":             float64(12),
		"split_skew_percent":            float64(180),
		"split_largest_shard_series":    float64(1800),
		"split_smallest_shard_series":   float64(1000),
		"split_plans_in_group":          float64(1),
		"split_dry_run":                 true,
	}
	for field, value := range want {
		got, present := event[field]
		if !present {
			t.Fatalf("the line has no %q: a reader cannot tell 'none' from 'not reported'; event=%#v", field, event)
		}
		if got != value {
			t.Fatalf("event[%q] = %#v, want %#v", field, got, value)
		}
	}
}

// A plan that was acted on and one that was not are the same line but for one
// field, so that field is always rendered - including when it is false, which
// is the value that will matter once something acts on these.
func TestTheSplitLineAlwaysSaysWhetherThePlanWasActedOn(t *testing.T) {
	t.Parallel()

	event := splitPlanEvent(t, &SplitPlanFacts{Outcome: SplitOutcomeUnderShare, DryRun: false})
	if got, present := event["split_dry_run"]; !present || got != false {
		t.Fatalf("split_dry_run = %#v (present=%v), want a rendered false", got, present)
	}
}

// An outcome this build does not know is held to the vocabulary before it
// reaches the metric, and named rather than dropped.
func TestASplitOutcomeOutsideTheVocabularyIsNamed(t *testing.T) {
	t.Parallel()

	event := splitPlanEvent(t, &SplitPlanFacts{Outcome: "INVENTED"})
	if event["split_outcome"] != SplitOutcomeUnrecognised {
		t.Fatalf("split_outcome = %#v, want %q: the metric's labels are pre-created from the vocabulary, "+
			"and one arriving at runtime is how a bounded label set stops being bounded",
			event["split_outcome"], SplitOutcomeUnrecognised)
	}
	if event["split_outcome"] == SplitOutcomeNoReading {
		t.Fatal("an unrecognised outcome was reported as a missing number: the first is this build's " +
			"defect and only a change of code fixes it, the second is a state of the deployment " +
			"somebody can go and look at")
	}
	loose := &SplitPlanFacts{Outcome: "INVENTED"}
	if normalizeSplitPlanFacts(loose).Outcome == loose.Outcome {
		t.Fatal("normalize did not hold the outcome to its vocabulary")
	}
	if loose.Outcome != "INVENTED" {
		t.Fatalf("normalize edited the caller's facts: %+v", loose)
	}
}

// Every declared outcome survives normalize, or the guard above would hold a
// real decision down to NO_READING and nothing would be red.
func TestEveryDeclaredSplitOutcomeSurvivesNormalize(t *testing.T) {
	t.Parallel()

	for _, outcome := range SplitOutcomes() {
		if got := normalizeSplitPlanFacts(&SplitPlanFacts{Outcome: outcome}); got.Outcome != outcome {
			t.Fatalf("normalize held the declared outcome %q down to %q", outcome, got.Outcome)
		}
	}
}

// The round's own line carries its three counts, and carries them every
// round: skipped on its own cannot say whether a zero means nothing was left
// out or nothing was looked at.
func TestTheSplitRoundLineCarriesItsCountsAndTheirDenominator(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentControlPlane, Stage: StageSplitPlanned, Result: ResultSuccess,
		SplitRound: &SplitRoundFacts{OverShare: 13, Examined: 8, Skipped: 5},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode split round log: %v; log=%s", err, output.String())
	}
	for field, value := range map[string]any{
		"split_round_over_share": float64(13),
		"split_round_examined":   float64(8),
		"split_round_skipped":    float64(5),
	} {
		if got, present := event[field]; !present || got != value {
			t.Fatalf("event[%q] = %#v (present=%v), want %#v", field, got, present, value)
		}
	}
	// And it is not an object's line: a reader filtering for objects whose
	// estimate was shared among several Plans must not catch this one.
	if _, present := event["split_plans_in_group"]; present {
		t.Fatalf("the round's line carries an object's field: %#v", event)
	}
	if _, present := event["split_outcome"]; present {
		t.Fatalf("the round's line wears an object's outcome word: %#v", event)
	}
}

// An answer this build does not know lands in its own cell, and every
// answer it does know lands in its own.
//
// Reached here rather than through the census, which cannot produce an
// unknown answer: its classifier returns four words and the switch names all
// four, so the cell is unreachable by construction today. The identity over
// the cells cannot see it either - the five sum to the same total whichever
// cell an answer is folded into - so folding the unknown into "splittable"
// left the whole library green.
//
// That fold is also the optimistic direction. A Plan nothing could classify
// would be counted as one a value list can split, and a planner would go and
// cut it.
//
// The cell exists because someone will add a word, and the day it first
// matters is the day it first becomes reachable. This test is what will be
// waiting for them.
func TestAnAnswerThisBuildDoesNotKnowLandsInItsOwnCell(t *testing.T) {
	t.Parallel()

	var facts ShardabilityFacts
	facts.Count("A_WORD_ADDED_AFTER_THIS_BUILD")
	if facts.Unrecognised != 1 {
		t.Fatalf("an unknown answer filed as %+v, want it in the cell of its own: counted as splittable, a "+
			"Plan nothing could classify is one a planner would go and cut", facts)
	}
	if facts.Splittable != 0 || facts.NotStructured != 0 || facts.Disjunctive != 0 || facts.NoQueries != 0 {
		t.Fatalf("an unknown answer reached a named cell: %+v", facts)
	}

	for answer, cell := range map[string]func(ShardabilityFacts) int{
		ShardQueriesBuilt:         func(f ShardabilityFacts) int { return f.Splittable },
		ShardQueriesNotStructured: func(f ShardabilityFacts) int { return f.NotStructured },
		ShardQueriesDisjunctive:   func(f ShardabilityFacts) int { return f.Disjunctive },
		ShardQueriesNoQueries:     func(f ShardabilityFacts) int { return f.NoQueries },
	} {
		var counted ShardabilityFacts
		counted.Count(answer)
		if cell(counted) != 1 || counted.Unrecognised != 0 {
			t.Fatalf("the declared answer %q filed as %+v, want its own cell and not the unknown one",
				answer, counted)
		}
	}
}
