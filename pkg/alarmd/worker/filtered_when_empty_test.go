// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// filteredRound is an OsRestart-shaped round's requirements: a primary that
// filters its values, and an uptime history that witnesses for it through
// its own query.
func filteredRound() []execution.DataRequirement {
	return []execution.DataRequirement{
		{RequirementID: "primary", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary", ValueFiltered: true},
		{RequirementID: "history", Role: execution.InputRoleAlgorithmDependency, LogicalQueryRef: "q-history", UnfilteredWitness: true},
	}
}

func witnessBinding(id execution.RequirementID, completeness execution.Completeness, state execution.DataState) execution.NamedInputBinding {
	return execution.NamedInputBinding{RequirementID: id, Role: execution.InputRoleAlgorithmDependency, Completeness: completeness, DataState: state}
}

// A filtered primary answered whole and empty is filtered empty only when its
// witness answered whole with data; any other witness word, a primary that
// does not filter, or one emptied by its target, is not. The witness is the
// requirement marked as one, matched by requirement: a ring ratio's previous
// point - the primary's own query, filter and all - is never one, even marked.
func TestAFilteredPrimaryIsFilteredEmptyOnlyOnItsWitnesssData(t *testing.T) {
	whole := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
	for _, test := range []struct {
		name         string
		evidence     queryAvailabilityEvidence
		primary      execution.PrimaryInputFact
		requirements []execution.DataRequirement
		bindings     []execution.NamedInputBinding
		witness      execution.WitnessData
		filtered     bool
	}{
		{name: "the witness has data", primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateData)},
			witness:  execution.WitnessDataFull, filtered: true},
		{name: "the witness is empty: the data may be missing", primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateEmpty)},
			witness:  execution.WitnessEmpty},
		{name: "the witness answered in part", primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessPartial, execution.DataStateData)},
			witness:  execution.WitnessNotFull},
		{name: "the witness is not bound", primary: whole, requirements: filteredRound(), witness: execution.WitnessUnbound},
		// As the production source binds a witness: its streamed series, and
		// the completion's share, which never carries records.
		{name: "a streamed series beside the completion's empty share", primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateData),
				witnessBinding("history", execution.CompletenessFull, execution.DataStateEmpty)},
			witness: execution.WitnessDataFull, filtered: true},
		{name: "a streamed series, then the query failed", primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateData),
				witnessBinding("history", execution.CompletenessUnavailable, execution.DataStateUnknown)},
			witness: execution.WitnessNotFull},
		{name: "emptied by the target", evidence: queryAvailabilityEvidence{primaryEmptiedOutside: true}, primary: whole, requirements: filteredRound(),
			bindings: []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateData)},
			witness:  execution.WitnessDataFull},
		{name: "the primary has data", primary: execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
			requirements: filteredRound(),
			bindings:     []execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, execution.DataStateData)},
			witness:      execution.WitnessDataFull},
		{name: "a ring ratio's previous point marked a witness", primary: whole,
			requirements: []execution.DataRequirement{
				{RequirementID: "primary", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary", ValueFiltered: true},
				{RequirementID: "previous", Role: execution.InputRoleAlgorithmDependency, LogicalQueryRef: "q-primary", UnfilteredWitness: true},
			},
			bindings: []execution.NamedInputBinding{witnessBinding("previous", execution.CompletenessFull, execution.DataStateData)},
			witness:  execution.WitnessNone},
		{name: "a ring ratio's previous point unmarked", primary: whole,
			requirements: []execution.DataRequirement{
				{RequirementID: "primary", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary", ValueFiltered: true},
				{RequirementID: "previous", Role: execution.InputRoleAlgorithmDependency, LogicalQueryRef: "q-primary"},
			},
			bindings: []execution.NamedInputBinding{witnessBinding("previous", execution.CompletenessFull, execution.DataStateData)},
			witness:  execution.WitnessNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary := test.primary
			primary.WitnessData = witnessData(test.requirements, test.bindings)
			if primary.WitnessData != test.witness {
				t.Fatalf("witness %q, want %q", primary.WitnessData, test.witness)
			}
			if got := test.evidence.filteredWhenEmpty(primary, test.requirements); got != test.filtered {
				t.Fatalf("filtered empty %t, want %t", got, test.filtered)
			}
		})
	}
	unfiltered := []execution.DataRequirement{{RequirementID: "primary", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary"}, filteredRound()[1]}
	whole.WitnessData = execution.WitnessDataFull
	if (queryAvailabilityEvidence{}).filteredWhenEmpty(whole, unfiltered) {
		t.Fatal("a primary that does not filter read as filtered empty")
	}
}

// From the round's derivation, as the coordinator makes it (labelFiltered,
// then primaryInputFacts on the completion's observation), to
// the tracker's row: an hour of filtered empty rounds whose witness had data
// is FILTERED_EMPTY; the same rounds with an empty witness stay
// EMPTY_EVERY_ROUND, data absent; and rounds going back and forth are
// counted on the row both ways, the latest deciding the line.
func TestAFilteredEmptyRoundReachesTheTrackerAsDetecting(t *testing.T) {
	round := func(state execution.DataState) observability.Observation {
		requirements := filteredRound()
		primary := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
		queryAvailabilityEvidence{}.labelFiltered(&primary, requirements,
			[]execution.NamedInputBinding{witnessBinding("history", execution.CompletenessFull, state)})
		return observability.Observation{ProgressCompletionKind: "FULL_EMPTY_COMPLETED",
			Trace:        observability.TraceFields{QueryGroupKey: "qg-restart", StrategyID: "128", BusinessID: "524"},
			PrimaryInput: primaryInputFacts(&primary)}
	}
	run := func(states ...execution.DataState) fleet.Anomaly {
		at := time.Unix(1_788_000_000, 0)
		tracker := fleet.NewTracker(nil, "pod-a", func() time.Time { return at })
		for elapsed := 0; elapsed <= 120; elapsed++ {
			observed := round(states[elapsed%len(states)])
			observed.Trace.EvaluationTime = at.Unix()
			tracker.Observe(context.Background(), observed)
			at = at.Add(time.Minute)
		}
		rows := tracker.NoData()
		if len(rows) != 1 {
			t.Fatalf("rows %+v, want one", rows)
		}
		list := []fleet.Anomaly{rows[0]}
		fleet.Attribute(list, at)
		return list[0]
	}
	filtered := run(execution.DataStateData)
	// FILTERED_EMPTY reads detecting with nothing to do (fleet's check words).
	if filtered.Kind != fleet.KindFilteredEmpty || filtered.Finding.Check != fleet.CheckFilteredEmpty {
		t.Fatalf("row %s check %s, want FILTERED_EMPTY", filtered.Kind, filtered.Finding.Check)
	}
	if filtered.Witness == nil || filtered.Witness.DataRounds != 121 || filtered.Witness.MissingRounds != 0 {
		t.Fatalf("witness %+v, want 121 rounds with data", filtered.Witness)
	}
	missing := run(execution.DataStateEmpty)
	if missing.Kind != fleet.KindEmptyEveryRound || missing.Finding.Check != fleet.CheckEmptyEveryRound {
		t.Fatalf("row %s check %s, want the data line kept when the witness has no data", missing.Kind, missing.Finding.Check)
	}
	if missing.Witness == nil || missing.Witness.DataRounds != 0 || missing.Witness.MissingRounds != 121 {
		t.Fatalf("witness %+v, want 121 rounds without data", missing.Witness)
	}
	// D, E, D, E ... ending on D (121 rounds, from 0): 61 with data, 60 without.
	flapping := run(execution.DataStateData, execution.DataStateEmpty)
	if flapping.Kind != fleet.KindFilteredEmpty || flapping.Witness == nil ||
		flapping.Witness.DataRounds != 61 || flapping.Witness.MissingRounds != 60 {
		t.Fatalf("row %s witness %+v, want FILTERED_EMPTY with 61 and 60 rounds", flapping.Kind, flapping.Witness)
	}
}
