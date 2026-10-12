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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// driftedScopes is a Plan carrying a CONFIG_DRIFT marker at both scopes: one
// on the Plan and one on a Level, each three FULL Slots from clearing, with
// the given number already observed.
func driftedScopes(observed uint32) []execution.GapScopeState {
	reason := execution.ReasonCode(contract.ReasonConfigDrift)
	return []execution.GapScopeState{
		{Scope: execution.GapScope{}, Status: execution.GapStatusWarming, ReasonCode: reason,
			RequiredFullSlots: 3, ObservedFullSlots: observed},
		{Scope: execution.GapScope{LevelID: 5, HasLevel: true}, Status: execution.GapStatusWarming, ReasonCode: reason,
			RequiredFullSlots: 3, ObservedFullSlots: observed},
	}
}

// A query group whose source has gone empty still recovers its marker: a FULL
// completion with no rows, every input whole, is a healthy Slot for the Plan,
// warming every scope - the Plan's and the Level's - one slot per round until
// it clears.
//
// Without this the marker outlives the outage that opened it for as long as
// the source stays empty, because the recovery used to ride on a state
// mutation and a Plan with no series produces none. The Level scope used to be
// left out as well, and a Plan that matches no series at all kept a marker a
// query failure opened for ever.
func TestAnEmptySourceSlotWarmsEveryScope(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		observed uint32
		want     execution.GapMutationKind
	}{
		{name: "first healthy Slot", observed: 0, want: execution.GapWarmup},
		{name: "second healthy Slot", observed: 1, want: execution.GapWarmup},
		{name: "the Slot that reaches the requirement", observed: 2, want: execution.GapClear},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plans, requirements := baseDuePlanAndRequirements()
			fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
			fixture.ports.gapScopes = driftedScopes(testCase.observed)

			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if len(fixture.ports.gapMutations) != 1 {
				t.Fatalf("gap mutations=%+v, want the one Plan's recovery", fixture.ports.gapMutations)
			}
			scopes := fixture.ports.gapMutations[0].Scopes
			if len(scopes) != 2 {
				t.Fatalf("recovered scopes=%+v, want the Plan scope and the Level scope", scopes)
			}
			for _, scope := range scopes {
				if scope.Kind != testCase.want {
					t.Fatalf("scope=%+v, want %s at %d of 3 observed", scope, testCase.want, testCase.observed)
				}
				if testCase.want == execution.GapWarmup &&
					(scope.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) || scope.RequiredFullSlots != 3) {
					t.Fatalf("warming scope=%+v, want the marker's own reason and requirement", scope)
				}
			}
		})
	}
}

// The same empty Slot on a Plan whose marker is only about a Level - the
// marker a failed query opens - warms that Level scope. This is the marker a
// Plan matching no series used to keep for ever: nothing ever warmed it.
func TestAnEmptySourceSlotWarmsALevelOnlyMarker(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	fixture.ports.gapScopes = driftedScopes(0)[1:]

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.gapMutations) != 1 {
		t.Fatalf("gap mutations=%+v, want the Level scope's warmup", fixture.ports.gapMutations)
	}
	scopes := fixture.ports.gapMutations[0].Scopes
	if len(scopes) != 1 || !scopes[0].Scope.HasLevel || scopes[0].Kind != execution.GapWarmup {
		t.Fatalf("scopes=%+v, want the Level scope warming", scopes)
	}
}
