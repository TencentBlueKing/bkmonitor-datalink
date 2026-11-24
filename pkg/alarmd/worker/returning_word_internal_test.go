// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan forced to warm up is back - PLAN_REACTIVATED - when its generation
// has run before, on any one of three pieces of evidence: a marker the store
// holds for the generation, state of it this Slot read from before, or
// no-data memory of it from before. A Plan of a deployment's first
// activation was never forced and has no marker; its state says it ran.
// With none of them it is an edit (CONFIG_DRIFT). A Slot that read nothing
// - completed without its query - has only the marker to go on. A word an
// earlier attempt of the Slot wrote on its marker stands, whatever the state
// now says.
func TestAReturnIsToldByItsMarkerItsStateOrItsMemory(t *testing.T) {
	due := firstSeenDue()
	own := ownVersion(t, due)
	earlier := execution.ApplyVersion{StateApplyEpoch: 7, EvaluationTime: own.EvaluationTime - 60, SlotDigest: "earlier"}
	rewarmed := execution.PlanActivationResult{Contract: frozenRenewalContract(), Facts: []execution.PlanActivationFact{{
		Plan: due.Identity, Selection: execution.ActivationCurrent, Selected: execution.ActivatedPlan{Identity: due.Identity,
			StateGeneration: due.StateGeneration, StateApplyEpoch: due.StateApplyEpoch, ScheduleRevision: due.ScheduleRevision,
			ForceWarming: true, Arrived: true}}}}
	marker := func(status execution.GapLoadStatus, version execution.ApplyVersion, reason string) execution.GapLoadResult {
		snapshot := execution.GapGuardSnapshot{Identity: due.GapIdentity(), Status: status, PersistedApplyVersion: version}
		if reason != "" {
			snapshot.Scopes = []execution.GapScopeState{{Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(reason)}}
		}
		return execution.GapLoadResult{Items: []execution.GapGuardSnapshot{snapshot}}
	}
	stateFromBefore := []execution.RuntimeStateView{{Identity: execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
		SeriesIdentityDigest: "series"}, Status: execution.StateFoundReady, PersistedApplyVersion: earlier}}
	memoryFromBefore := []execution.NoDataMemorySnapshot{{Identity: execution.PlanNoDataIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		Status: execution.NoDataMemoryFound, PersistedApplyVersion: earlier}}
	for _, testCase := range []struct {
		name     string
		gaps     execution.GapLoadResult
		views    []execution.RuntimeStateView
		memory   []execution.NoDataMemorySnapshot
		readNone bool
		want     activationChange
	}{
		{name: "a marker of the generation", gaps: marker(execution.GapClearedTombstone, earlier, ""), want: activationChangeReactivated},
		{name: "state from before", views: stateFromBefore, want: activationChangeReactivated},
		{name: "no-data memory from before", memory: memoryFromBefore, want: activationChangeReactivated},
		{name: "nothing of it from before", want: activationChangeDrift},
		{name: "read nothing, no marker", readNone: true, views: stateFromBefore, want: activationChangeDrift},
		{name: "read nothing, a marker", readNone: true, gaps: marker(execution.GapClearedTombstone, earlier, ""), want: activationChangeReactivated},
		{name: "this Slot's word is the edit's, state from before", views: stateFromBefore,
			gaps: marker(execution.GapFound, own, contract.ReasonConfigDrift), want: activationChangeDrift},
		{name: "this Slot's word is the return's, nothing from before",
			gaps: marker(execution.GapFound, own, contract.ReasonPlanReactivated), want: activationChangeReactivated},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stream := &streamedExecution{header: execution.InternalExecutionHeader{Contract: frozenRenewalContract(),
				DuePlans: []execution.DuePlan{due}}, state: execution.StatePreflightResult{Items: testCase.views},
				noData: execution.NoDataLoadResult{Items: testCase.memory}}
			unseen := stream.unseenPlans()
			if testCase.readNone {
				unseen = nil
			}
			if change := classifyActivationChange([]execution.DuePlan{due}, rewarmed, testCase.gaps, unseen); change != testCase.want {
				t.Fatalf("change %v, want %v", change, testCase.want)
			}
			if reason := guardReasonFor([]execution.DuePlan{due}, rewarmed, testCase.gaps, unseen); reason != testCase.want.reason() {
				t.Fatalf("guard reason %s, want %s", reason, testCase.want.reason())
			}
		})
	}
}
