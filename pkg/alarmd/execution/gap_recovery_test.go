// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// recoveryFixture is a Plan with a standing marker at both scopes, each three
// FULL Slots from clearing with the given number already observed, under the
// given schedule revision.
func recoveryFixture(observed uint32, markerSchedule execution.PlanScheduleRevision) (execution.DuePlan, execution.GapLoadResult) {
	plans, _ := baseDuePlanAndRequirements()
	due := plans[0]
	reason := execution.ReasonCode(contract.ReasonConfigDrift)
	return due, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: due.GapIdentity(), Status: execution.GapFound, MarkerRevision: 4,
		PersistedMutationDigest: "previous-gap", LastScheduleRevision: markerSchedule,
		Scopes: []execution.GapScopeState{
			{Scope: execution.GapScope{}, Status: execution.GapStatusWarming, ReasonCode: reason,
				RequiredFullSlots: 3, ObservedFullSlots: observed},
			{Scope: execution.GapScope{LevelID: 5, HasLevel: true}, Status: execution.GapStatusWarming, ReasonCode: reason,
				RequiredFullSlots: 3, ObservedFullSlots: observed},
		},
	}}}
}

// One healthy Slot moves every scope of the marker, the Plan's and each
// Level's, one slot closer to its requirement.
func TestAHealthySlotMovesEveryScope(t *testing.T) {
	due, gaps := recoveryFixture(0, due0ScheduleRevision(t))
	mutation, err := execution.PlanGapRecoveryMutation(frozenContract(), due, gaps)
	if err != nil || mutation == nil {
		t.Fatalf("PlanGapRecoveryMutation() = %+v, %v", mutation, err)
	}
	levels := 0
	for _, scope := range mutation.Scopes {
		if scope.Kind != execution.GapWarmup {
			t.Fatalf("scope=%+v, want a warmup at 0 of 3", scope)
		}
		if scope.Scope.HasLevel {
			levels++
		}
	}
	if len(mutation.Scopes) != 2 || levels != 1 {
		t.Fatalf("scopes=%+v, want the Plan scope and the Level scope", mutation.Scopes)
	}
}

// A marker written under an older schedule revision still recovers, and its
// warmup starts over: the store discards the count of every scope whose
// revision changed, so a count read from the marker is not this revision's.
// Without the restart the decision here and the count the store keeps
// disagree -- this Slot says "one short, clear it" against a count the store
// has already thrown away.
func TestAMarkerUnderAnOlderScheduleRevisionRestartsItsWarmup(t *testing.T) {
	due, gaps := recoveryFixture(2, "some-older-schedule")
	mutation, err := execution.PlanGapRecoveryMutation(frozenContract(), due, gaps)
	if err != nil || mutation == nil {
		t.Fatalf("PlanGapRecoveryMutation() = %+v, %v", mutation, err)
	}
	for _, scope := range mutation.Scopes {
		if scope.Kind != execution.GapWarmup || scope.RequiredFullSlots != 3 {
			t.Fatalf("scope=%+v, want the warmup restarted at 0 of 3, not cleared on the old count", scope)
		}
	}
	// The same count under the current revision is one short of the
	// requirement, so it clears. The two cases differ only in the revision.
	current, currentGaps := recoveryFixture(2, due0ScheduleRevision(t))
	mutation, err = execution.PlanGapRecoveryMutation(frozenContract(), current, currentGaps)
	if err != nil || mutation == nil {
		t.Fatalf("PlanGapRecoveryMutation() = %+v, %v", mutation, err)
	}
	for _, scope := range mutation.Scopes {
		if scope.Kind != execution.GapClear {
			t.Fatalf("scope=%+v, want a clear at 2 of 3 under the current revision", scope)
		}
	}
}

// Only a marker the load actually read is recovered. A marker that is missing,
// unreadable or already a tombstone says nothing about a warmup, and proposing
// a revision for one would be a write against a revision this Slot never saw.
func TestOnlyAStandingMarkerIsRecovered(t *testing.T) {
	for _, status := range []execution.GapLoadStatus{
		execution.GapMissing, execution.GapUnavailable, execution.GapTerminal, execution.GapClearedTombstone,
	} {
		due, gaps := recoveryFixture(0, due0ScheduleRevision(t))
		gaps.Items[0].Status = status
		mutation, err := execution.PlanGapRecoveryMutation(frozenContract(), due, gaps)
		if err != nil || mutation != nil {
			t.Fatalf("%s: PlanGapRecoveryMutation() = %+v, %v, want nothing proposed", status, mutation, err)
		}
	}
	due, _ := recoveryFixture(0, due0ScheduleRevision(t))
	mutation, err := execution.PlanGapRecoveryMutation(frozenContract(), due, execution.GapLoadResult{})
	if err != nil || mutation != nil {
		t.Fatalf("with no marker loaded: PlanGapRecoveryMutation() = %+v, %v, want nothing proposed", mutation, err)
	}
}

func due0ScheduleRevision(t *testing.T) execution.PlanScheduleRevision {
	t.Helper()
	plans, _ := baseDuePlanAndRequirements()
	return plans[0].ScheduleRevision
}
