// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A write that moves the marker to a new schedule revision discards the
// warmup count of every scope, including the scopes it does not name.
//
// The envelope carries one revision for all the scopes under it, so a scope
// left out of the mutation would go on counting slots it observed under a
// schedule that no longer exists -- and the next write that does name it
// would clear a guard that has seen one slot under the current schedule as
// though it had seen three. The rule held for as long as every recovery named
// every scope; a Slot with no series can speak for the Plan's scopes and not
// for a Level's, and it is the first write to name a subset.
//
// The sequence is the reachable one: a marker warming at both scopes under an
// old revision, one Slot with no series under the new revision, then a Slot
// that evaluates series.
func TestAScheduleRevisionChangeDiscardsEveryScopesWarmupCount(t *testing.T) {
	planScope := execution.GapScope{}
	levelScope := execution.GapScope{LevelID: 1, HasLevel: true}
	reason := execution.ReasonCode("CONFIG_DRIFT")
	warming := func(scope execution.GapScope, observed uint32) execution.GapScopeState {
		return execution.GapScopeState{Scope: scope, Status: execution.GapStatusWarming, ReasonCode: reason,
			RequiredFullSlots: 3, ObservedFullSlots: observed}
	}
	warmup := func(scope execution.GapScope) execution.GapScopeMutation {
		return execution.GapScopeMutation{Scope: scope, Kind: execution.GapWarmup, ReasonCode: reason, RequiredFullSlots: 3}
	}
	clear := func(scope execution.GapScope) execution.GapScopeMutation {
		return execution.GapScopeMutation{Scope: scope, Kind: execution.GapClear}
	}

	// Both scopes are two slots into a three-slot warmup under r1. The Plan's
	// schedule then changes, and the first Slot under r2 has no series: it
	// recovers the Plan scope alone.
	previous := []execution.GapScopeState{warming(planScope, 2), warming(levelScope, 2)}
	afterEmpty := applyGapScopes(previous, []execution.GapScopeMutation{warmup(planScope)}, "r1", "r2")
	observed := map[execution.GapScope]execution.GapScopeState{}
	for _, state := range afterEmpty {
		observed[state.Scope] = state
	}
	if got := observed[planScope]; got.ObservedFullSlots != 1 {
		t.Fatalf("Plan scope after the first Slot under the new revision = %+v, want its count restarted at 1", got)
	}
	if got := observed[levelScope]; got.ObservedFullSlots != 0 || got.Status != execution.GapStatusWarming {
		t.Fatalf("Level scope = %+v, want its count discarded with the revision and the scope still warming", got)
	}

	// The next Slot evaluates series and speaks for every scope. The Level
	// scope has seen one slot under r2, so this is its second: still warming.
	// Before the sweep above it carried the two it had earned under r1 and
	// cleared here, three slots early.
	afterSeries := applyGapScopes(afterEmpty,
		[]execution.GapScopeMutation{warmup(planScope), warmup(levelScope)}, "r2", "r2")
	observed = map[execution.GapScope]execution.GapScopeState{}
	for _, state := range afterSeries {
		observed[state.Scope] = state
	}
	if got, found := observed[levelScope]; !found || got.Status != execution.GapStatusWarming || got.ObservedFullSlots != 1 {
		t.Fatalf("Level scope after one evaluated Slot under the new revision = %+v (found %v), want warming at 1 of 3", got, found)
	}
	if got, found := observed[planScope]; !found || got.ObservedFullSlots != 2 {
		t.Fatalf("Plan scope = %+v (found %v), want 2 of 3", got, found)
	}

	// The scope a mutation clears is still removed, whatever the revision did.
	cleared := applyGapScopes(afterSeries, []execution.GapScopeMutation{clear(planScope)}, "r2", "r3")
	for _, state := range cleared {
		if state.Scope == planScope {
			t.Fatalf("cleared Plan scope survived: %+v", state)
		}
	}
}

// The reasons a count stops applying to its own scope, with the schedule
// revision held still so they are the only thing that could restart it.
//
// These are what is left in the per-mutation branch now that the revision is
// swept before the mutations are applied. A count is how many FULL Slots have
// been seen against one requirement under one reason; change either and the
// slots already counted were counted against a different question.
func TestAWarmupCountRestartsWhenItsRequirementOrItsReasonChanges(t *testing.T) {
	scope := execution.GapScope{LevelID: 2, HasLevel: true}
	warmed := []execution.GapScopeState{{Scope: scope, Status: execution.GapStatusWarming,
		ReasonCode: "CONFIG_DRIFT", RequiredFullSlots: 5, ObservedFullSlots: 3}}
	for name, testCase := range map[string]struct {
		mutation execution.GapScopeMutation
		want     uint32
	}{
		"the same question again": {
			execution.GapScopeMutation{Scope: scope, Kind: execution.GapWarmup, ReasonCode: "CONFIG_DRIFT", RequiredFullSlots: 5}, 4,
		},
		"a different requirement": {
			execution.GapScopeMutation{Scope: scope, Kind: execution.GapWarmup, ReasonCode: "CONFIG_DRIFT", RequiredFullSlots: 7}, 1,
		},
		"a different reason": {
			execution.GapScopeMutation{Scope: scope, Kind: execution.GapWarmup, ReasonCode: "HISTORY_WARMING", RequiredFullSlots: 5}, 1,
		},
	} {
		applied := applyGapScopes(warmed, []execution.GapScopeMutation{testCase.mutation}, "r1", "r1")
		if len(applied) != 1 || applied[0].ObservedFullSlots != testCase.want {
			t.Errorf("%s: scopes = %+v, want the count at %d", name, applied, testCase.want)
		}
	}
}
