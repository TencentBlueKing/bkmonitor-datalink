// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const stateWriteReuseMetric = "bkmonitor_alarmd_state_write_reuse_total"

func gatherStateWriteReuse(t *testing.T, observations ...observability.Observation) map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	for _, observation := range observations {
		recorder.Observe(context.Background(), observability.NormalizeObservation(observation))
	}
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]float64{}
	for _, family := range families {
		if family.GetName() != stateWriteReuseMetric {
			continue
		}
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				if key != "" {
					key += "/"
				}
				key += pair.GetValue()
			}
			gathered[key] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// The reading this metric exists for is expected to contain real zeros, so a
// class that is absent until its first increment cannot be told from one that
// was measured and found empty. Every class must be published from the first
// scrape. This is also the check that would have caught the metric not being
// registered at all, which is the way an instrument most often fails: silently,
// by not being there.
func TestStateWriteReusePublishesEveryClassBeforeAnyObservation(t *testing.T) {
	gathered := gatherStateWriteReuse(t)
	if len(gathered) == 0 {
		t.Fatal("state_write_reuse_total is absent before any observation; a zero reading would be unreadable")
	}
	for _, class := range []string{"unobserved", "identical", "decision_stable", "changed"} {
		for _, stored := range []string{"found_ready", "found_warming", "found_gapped", "missing_warming"} {
			key := class + "/" + stored
			value, found := gathered[key]
			if !found {
				t.Fatalf("%s is absent before any observation; absent and zero read alike", key)
			}
			if value != 0 {
				t.Fatalf("%s starts at %v, want 0", key, value)
			}
		}
	}
}

// The whole point of the step is that these counts reach a scrape. A fact that
// is recorded but never published is the failure this asserts against: it costs
// a full measurement round to discover, because the code looks correct.
func TestStateWriteReuseCountsReachTheScrape(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredWarming)
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeProcessedTime, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseUnobserved, observability.StateWriteChangeNone, observability.StateWriteReuseStoredMissing)

	gathered := gatherStateWriteReuse(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})

	for key, want := range map[string]float64{
		"decision_stable/found_ready":   2,
		"decision_stable/found_warming": 1,
		"changed/found_ready":           1,
		"unobserved/missing_warming":    1,
		"identical/found_ready":         0,
	} {
		if got := gathered[key]; got != want {
			t.Fatalf("%s = %v, want %v (gathered: %v)", key, got, want, gathered)
		}
	}
}

// The two steady states must stay apart in the reading. A series that keeps
// recovering and one that never leaves history warming both pay a write every
// round; summed into one rate they would describe neither, and a change sized
// against that rate would be sized against nothing real.
func TestStateWriteReuseKeepsTheTwoSteadyStatesApart(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	for i := 0; i < 7; i++ {
		facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)
	}
	for i := 0; i < 3; i++ {
		facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeProcessedTime, observability.StateWriteReuseStoredWarming)
	}
	gathered := gatherStateWriteReuse(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})
	if got := gathered["decision_stable/found_ready"]; got != 7 {
		t.Fatalf("recovering series counted %v, want 7", got)
	}
	if got := gathered["changed/found_warming"]; got != 3 {
		t.Fatalf("warming series counted %v, want 3", got)
	}
	if got := gathered["decision_stable/found_warming"]; got != 0 {
		t.Fatalf("warming series leaked into the recovering class: %v", got)
	}
}

// An unrecognised label value must not open the label set: this counter is
// incremented per admitted write, so an unbounded label is how it would turn
// into a cardinality incident on the largest deployment first.
func TestStateWriteReuseBoundsItsLabelSet(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	facts.Record(observability.StateWriteReuseClass("invented"), observability.StateWriteChangeReason("invented"), observability.StateWriteReuseStored("also-invented"))
	gathered := gatherStateWriteReuse(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})
	if got := gathered["other/other"]; got != 1 {
		t.Fatalf("unrecognised labels counted as %v under other/other, want 1 (gathered: %v)", got, gathered)
	}
	if _, found := gathered["invented/also-invented"]; found {
		t.Fatal("an unrecognised label value reached the metric")
	}
}

// Empty facts must not be published as a result. Emitting zeros for a Plan that
// classified nothing would add rounds to the denominator that never happened.
func TestStateWriteReuseIgnoresEmptyFacts(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	gathered := gatherStateWriteReuse(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})
	var total float64
	for _, value := range gathered {
		total += value
	}
	if total != 0 {
		t.Fatalf("empty facts contributed %v to the counter", total)
	}
}

const stateWriteChangeMetric = "bkmonitor_alarmd_state_write_change_reason_total"

func gatherStateWriteChange(t *testing.T, observations ...observability.Observation) map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	for _, observation := range observations {
		recorder.Observe(context.Background(), observability.NormalizeObservation(observation))
	}
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]float64{}
	for _, family := range families {
		if family.GetName() != stateWriteChangeMetric {
			continue
		}
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				if key != "" {
					key += "/"
				}
				key += pair.GetValue()
			}
			gathered[key] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// The breakdown has to reach a scrape for the same reason the classes do, and
// it has to publish at zero for the same reason: a reason that is absent until
// its first increment cannot be told from one that was looked for and not found.
func TestStateWriteChangeReasonPublishesEveryReasonAndReachesTheScrape(t *testing.T) {
	before := gatherStateWriteChange(t)
	for _, reason := range []string{
		"none", "level_count", "level_missing", "level_compatibility", "history_completeness",
		"gap_reason", "warmup_ref", "processed_time", "series_guard", "other",
	} {
		key := reason + "/found_ready"
		if value, found := before[key]; !found || value != 0 {
			t.Fatalf("%s before any observation: found=%v value=%v, want present at 0", key, found, value)
		}
	}

	facts := observability.StateWriteReuseFacts{}
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeProcessedTime, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeProcessedTime, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeCompleteness, observability.StateWriteReuseStoredWarming)
	gathered := gatherStateWriteChange(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})
	if got := gathered["processed_time/found_ready"]; got != 2 {
		t.Fatalf("processed_time/found_ready = %v, want 2 (gathered: %v)", got, gathered)
	}
	if got := gathered["history_completeness/found_warming"]; got != 1 {
		t.Fatalf("history_completeness/found_warming = %v, want 1", got)
	}
}

// The breakdown counts only changed comparisons. If it counted the others it
// would stop being a breakdown of the changed class, and the two families would
// disagree about how many comparisons happened.
func TestStateWriteChangeReasonCountsOnlyTheChangedClass(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseIdentical, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseUnobserved, observability.StateWriteChangeNone, observability.StateWriteReuseStoredMissing)
	gathered := gatherStateWriteChange(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	})
	var total float64
	for _, value := range gathered {
		total += value
	}
	if total != 0 {
		t.Fatalf("non-changed comparisons contributed %v to the reason breakdown", total)
	}
}

// The reason breakdown must add up to the changed class, or one of the two is
// wrong and there is no way to tell which from either alone.
func TestStateWriteChangeReasonSumMatchesTheChangedClass(t *testing.T) {
	facts := observability.StateWriteReuseFacts{}
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeProcessedTime, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeSeriesGuard, observability.StateWriteReuseStoredReady)
	facts.Record(observability.StateWriteReuseChanged, observability.StateWriteChangeCompleteness, observability.StateWriteReuseStoredGapped)
	facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteChangeNone, observability.StateWriteReuseStoredReady)

	observation := observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, StateWriteReuse: &facts,
	}
	classes := gatherStateWriteReuse(t, observation)
	reasons := gatherStateWriteChange(t, observation)

	var changed, breakdown float64
	for key, value := range classes {
		if strings.HasPrefix(key, "changed/") {
			changed += value
		}
	}
	for _, value := range reasons {
		breakdown += value
	}
	if changed != breakdown || changed != 3 {
		t.Fatalf("changed class = %v, reason breakdown = %v, want both 3", changed, breakdown)
	}
}
