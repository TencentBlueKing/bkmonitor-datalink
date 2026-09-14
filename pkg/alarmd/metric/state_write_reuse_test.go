// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
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
	facts.Add(observability.StateWriteReuseDecisionStable, observability.StateWriteReuseStoredReady)
	facts.Add(observability.StateWriteReuseDecisionStable, observability.StateWriteReuseStoredReady)
	facts.Add(observability.StateWriteReuseDecisionStable, observability.StateWriteReuseStoredWarming)
	facts.Add(observability.StateWriteReuseChanged, observability.StateWriteReuseStoredReady)
	facts.Add(observability.StateWriteReuseUnobserved, observability.StateWriteReuseStoredMissing)

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
		facts.Add(observability.StateWriteReuseDecisionStable, observability.StateWriteReuseStoredReady)
	}
	for i := 0; i < 3; i++ {
		facts.Add(observability.StateWriteReuseChanged, observability.StateWriteReuseStoredWarming)
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
	facts.Add(observability.StateWriteReuseClass("invented"), observability.StateWriteReuseStored("also-invented"))
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
