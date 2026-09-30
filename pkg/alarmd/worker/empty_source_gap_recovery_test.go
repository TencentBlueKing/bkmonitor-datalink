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

// A query group whose source has gone empty still recovers its Plan scope: a
// FULL completion with no rows is a healthy Slot for the Plan, warming its
// marker one slot per round until it clears. The Level scope is left exactly
// as it was loaded on every one of those rounds.
//
// Without this the marker outlives the outage that opened it for as long as
// the source stays empty, because the recovery used to ride on a state
// mutation and a Plan with no series produces none. The cost is paid on the
// round the data comes back: every Level is held at UNKNOWN with the marker's
// reason, for the whole warmup, after a source that had been answering the
// entire time.
func TestAnEmptySourceSlotWarmsThePlanScopeAndLeavesTheLevelScopeAlone(t *testing.T) {
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
			// One scope, not two: the Level scope is not in the mutation at
			// all, which is what leaves it untouched -- the store applies the
			// scopes it is given and keeps the rest. A Level scope asks for
			// that series' own history to have moved, and a round with no
			// series has none to show.
			if len(scopes) != 1 || scopes[0].Scope.HasLevel {
				t.Fatalf("recovered scopes=%+v, want the Plan scope alone", scopes)
			}
			if scopes[0].Kind != testCase.want {
				t.Fatalf("Plan scope=%+v, want %s at %d of 3 observed", scopes[0], testCase.want, testCase.observed)
			}
			if testCase.want == execution.GapWarmup &&
				(scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) || scopes[0].RequiredFullSlots != 3) {
				t.Fatalf("warming Plan scope=%+v, want the marker's own reason and requirement", scopes[0])
			}
		})
	}
}

// The same empty Slot on a Plan whose marker is only about a Level writes
// nothing at all. There is no Plan scope to warm, and a mutation carrying no
// scope is refused by the contract -- so "nothing to say" has to be said by
// not proposing a mutation, not by proposing an empty one.
func TestAnEmptySourceSlotWithOnlyALevelScopeWritesNothing(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	fixture, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	fixture.ports.gapScopes = driftedScopes(0)[1:]

	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.Result != observability.ResultSuccess {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.gapMutations) != 0 {
		t.Fatalf("gap mutations=%+v, want none: the marker has no Plan scope to recover", fixture.ports.gapMutations)
	}
}
