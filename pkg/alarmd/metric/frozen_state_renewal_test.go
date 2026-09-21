// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const frozenStateRenewalMetric = "bkmonitor_alarmd_worker_frozen_state_renewals_total"

func gatherFrozenStateRenewals(t *testing.T, observations ...observability.Observation) map[string]float64 {
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
		if family.GetName() != frozenStateRenewalMetric {
			continue
		}
		for _, series := range family.Metric {
			gathered[series.Label[0].GetValue()] = series.GetCounter().GetValue()
		}
	}
	return gathered
}

// Two of the four readings are zeros somebody acts on.
//
// missing staying at zero is what says the silent loss has been closed, and
// renewed staying at zero on a deployment where series freeze is what says the
// mechanism never ran. Neither reading exists if the series only appears on its
// first increment.
func TestEveryFrozenRenewalOutcomeIsPublishedBeforeAnyObservation(t *testing.T) {
	gathered := gatherFrozenStateRenewals(t)
	if len(gathered) != len(execution.FrozenRenewalOutcomes) {
		t.Fatalf("published %d outcome series before any observation, want all %d (%v)",
			len(gathered), len(execution.FrozenRenewalOutcomes), gathered)
	}
	for _, outcome := range execution.FrozenRenewalOutcomes {
		label := frozenRenewalLabel(outcome)
		value, found := gathered[label]
		if !found {
			t.Fatalf("%s is absent before any observation; absent and zero read alike", label)
		}
		if value != 0 {
			t.Fatalf("%s starts at %v, want 0", label, value)
		}
	}
}

// One observation carries a whole Slot's outcomes, so the counter has to add
// them rather than count the observation.
//
// A Slot that renewed two hundred keys and a Slot that renewed one are not the
// same event, and the difference is the entire quantity anyone reads this
// family for: whether the renewal rate accounts for the keys that are read and
// not written.
func TestASlotsFrozenRenewalOutcomesReachTheScrapeAsCounts(t *testing.T) {
	var facts observability.FrozenStateRenewalFacts
	facts.Record(3, 40, 1, 0)
	facts.Record(2, 0, 0, 5)
	gathered := gatherFrozenStateRenewals(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageFrozenStateRenewed,
		Operation: observability.Operation(execution.OperationNormal), Direction: observability.DirectionInternal,
		Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		FrozenStateRenewal: &facts,
	})
	for label, want := range map[string]float64{"renewed": 5, "fresh": 40, "missing": 1, "failed": 5} {
		if gathered[label] != want {
			t.Fatalf("%s = %v, want %v; the Slot's own counts are the quantity, not the number of Slots",
				label, gathered[label], want)
		}
	}
	// The population the four are counted out of, so a reader can check the
	// family against worker_work_total rather than assume it is complete.
	if facts.Frozen != 51 {
		t.Fatalf("population = %d, want the 51 series the two Plans reported", facts.Frozen)
	}
}

// The census reaches the scrape, including the zeros.
//
// A Slot that read every series it meant to evaluate and froze none of them
// reports three numbers and four zeros. That reading is the one this family
// exists for: before it, a replica with nothing frozen and a replica whose
// candidate set was computed wrongly published exactly the same thing --
// nothing -- and telling them apart cost a deployment.
func TestTheSeriesCensusReachesTheScrapeBesideTheOutcomes(t *testing.T) {
	var facts observability.FrozenStateRenewalFacts
	facts.RecordCensus(900, 150, 148)
	gathered := gatherFrozenStateCensus(t, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageFrozenStateRenewed,
		Operation: observability.Operation(execution.OperationNormal), Direction: observability.DirectionInternal,
		Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		FrozenStateRenewal: &facts,
	})
	for stage, want := range map[string]float64{"due": 900, "read": 150, "written": 148} {
		if gathered[stage] != want {
			t.Fatalf("%s = %v, want %v", stage, gathered[stage], want)
		}
	}
	// The two differences a reader takes from it, neither derivable from the
	// other: 750 series were never read at all, and 2 were read and not
	// written.
	if gathered["due"]-gathered["read"] != 750 || gathered["read"]-gathered["written"] != 2 {
		t.Fatalf("census = %v, want the two populations separable", gathered)
	}
}

func gatherFrozenStateCensus(t *testing.T, observations ...observability.Observation) map[string]float64 {
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
		if family.GetName() != "bkmonitor_alarmd_worker_frozen_state_census_total" {
			continue
		}
		for _, series := range family.Metric {
			gathered[series.Label[0].GetValue()] = series.GetCounter().GetValue()
		}
	}
	return gathered
}
