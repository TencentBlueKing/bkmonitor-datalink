// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A whole round through the coordinator whose primary answers FULL and
// EMPTY: what its requirements say about such an answer reaches the
// committed round's primary input - a value filter (ValueFiltered, with
// WitnessData NONE: this Plan has no witness), a count its provider does not
// zero-fill (UnfilledWhenEmpty), an event count with a provider lead
// (QuietWhenEmpty) - and a requirement that says none of them labels
// nothing. Each word is the coordinator's own call, from Execute.
func TestAWholeEmptyRoundCarriesItsRequirementsWords(t *testing.T) {
	for _, test := range []struct {
		name                      string
		shape                     func(*execution.DataRequirement)
		filtered, unfilled, quiet bool
	}{
		{name: "a primary that says nothing", shape: func(*execution.DataRequirement) {}},
		{name: "a value filter", shape: func(r *execution.DataRequirement) { r.ValueFiltered = true }, filtered: true},
		{name: "an unfilled count", shape: func(r *execution.DataRequirement) { r.UnfilledCounts = true }, unfilled: true},
		{name: "an event count with a lead", shape: func(r *execution.DataRequirement) {
			r.ProviderLeadSeconds = r.StepMillis / 1000
			r.RequiredColumns = append(r.RequiredColumns, "host")
		}, quiet: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.withheld = &[2]uint64{}
			fixture.ports.shapeRequirements = func(requirements []execution.DataRequirement) []execution.DataRequirement {
				for index := range requirements {
					if requirements[index].Role == execution.InputRolePrimary {
						test.shape(&requirements[index])
					}
				}
				return requirements
			}
			// A lead is part of the requirement's identity, so the contract the
			// Slot was frozen under carries the shaped set's digest.
			request := slotRequest(execution.OperationNormal)
			plans, requirements := baseDuePlanAndRequirements()
			requirements = fixture.ports.shapeRequirements(requirements)
			digest, err := execution.DeriveDuePlanSetDigest(plans, requirements)
			if err != nil {
				t.Fatal(err)
			}
			request.Contract.DuePlanSetDigest, request.DuePlanTargets.DuePlanSetDigest = digest, digest
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			var primary *observability.PrimaryInputFacts
			for _, observation := range *fixture.observations {
				if observation.Stage == observability.StageProgressCommitted {
					primary = observation.PrimaryInput
				}
			}
			if primary == nil || primary.Completeness != "FULL" || primary.DataState != "EMPTY" {
				t.Fatalf("committed primary %+v, want FULL and EMPTY", primary)
			}
			if primary.ValueFiltered != test.filtered || primary.FilteredWhenEmpty || primary.WitnessData != "" ||
				primary.UnfilledWhenEmpty != test.unfilled || primary.QuietWhenEmpty != test.quiet {
				t.Fatalf("committed primary %+v, want filter %t (no witness, not filtered empty), unfilled %t, quiet %t",
					primary, test.filtered, test.unfilled, test.quiet)
			}
		})
	}
}

// The same round with an unfiltered witness that streams a series of its
// own, through the coordinator's own path (the stream's series bindings, then
// the round's labels): a filtered primary answered whole and empty while its
// witness had data is filtered empty, and the witness word is DATA.
func TestAWholeEmptyRoundWithAWitnessThatStreamedDataIsFilteredEmpty(t *testing.T) {
	shape := func(requirements []execution.DataRequirement) []execution.DataRequirement {
		requirements[0].ValueFiltered = true
		witness := requirements[0]
		witness.RequirementID, witness.DatasetName, witness.Role = "history", "history", execution.InputRoleAlgorithmDependency
		witness.LogicalQueryRef, witness.ValueFiltered, witness.UnfilteredWitness = "query-history", false, true
		return append(requirements, witness)
	}
	fixture := newFixture(t, true, "")
	fixture.ports.withheld = &[2]uint64{}
	fixture.ports.witness = true
	fixture.ports.shapeRequirements = shape
	request := slotRequest(execution.OperationNormal)
	plans, requirements := baseDuePlanAndRequirements()
	digest, err := execution.DeriveDuePlanSetDigest(plans, shape(requirements))
	if err != nil {
		t.Fatal(err)
	}
	request.Contract.DuePlanSetDigest, request.DuePlanTargets.DuePlanSetDigest = digest, digest
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	var primary *observability.PrimaryInputFacts
	for _, observation := range *fixture.observations {
		if observation.Stage == observability.StageProgressCommitted {
			primary = observation.PrimaryInput
		}
	}
	if primary == nil || primary.Completeness != "FULL" || primary.DataState != "EMPTY" ||
		!primary.ValueFiltered || !primary.FilteredWhenEmpty || primary.WitnessData != "DATA" {
		t.Fatalf("committed primary %+v, want FULL, EMPTY, filtered empty with the witness answering DATA", primary)
	}
}
