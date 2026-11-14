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

func firstSeenDue() execution.DuePlan {
	return execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"},
		StateGeneration: "gen-1", StateApplyEpoch: 7, ScheduleRevision: "sched-1"}
}

func ownVersion(t *testing.T, due execution.DuePlan) execution.ApplyVersion {
	t.Helper()
	version, err := execution.BuildApplyVersion(frozenRenewalContract(), due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// A due Plan is unseen when the Slot read nothing of it written before the
// Slot (design 013 section 4.6): no state record under its generation but
// one an earlier attempt of this Slot wrote, nothing carried, no no-data
// memory. A record of another generation is not its own.
func TestAPlanIsUnseenOnlyWhenTheSlotReadNothingFromBeforeIt(t *testing.T) {
	due := firstSeenDue()
	own := ownVersion(t, due)
	earlier := execution.ApplyVersion{StateApplyEpoch: 7, EvaluationTime: own.EvaluationTime - 60, SlotDigest: "earlier"}
	view := func(status execution.StateLoadStatus, version execution.ApplyVersion) execution.RuntimeStateView {
		return execution.RuntimeStateView{Identity: execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration,
			SeriesIdentityDigest: "series"}, Status: status, PersistedApplyVersion: version}
	}
	otherGeneration := view(execution.StateFoundReady, earlier)
	otherGeneration.Identity.StateGeneration = "gen-0"
	carried := view(execution.StateMissingWarming, execution.ApplyVersion{})
	carried.Carried = &execution.RuntimeStateView{Status: execution.StateFoundReady}
	for _, testCase := range []struct {
		name   string
		views  []execution.RuntimeStateView
		memory []execution.NoDataMemorySnapshot
		unseen bool
	}{
		{name: "nothing read", unseen: true},
		{name: "series missing", views: []execution.RuntimeStateView{view(execution.StateMissingWarming, execution.ApplyVersion{})}, unseen: true},
		{name: "written by an earlier attempt of this Slot", views: []execution.RuntimeStateView{view(execution.StateFoundReady, own)}, unseen: true},
		{name: "a record of another generation", views: []execution.RuntimeStateView{otherGeneration}, unseen: true},
		{name: "a record from before the Slot", views: []execution.RuntimeStateView{view(execution.StateFoundReady, earlier)}},
		{name: "carried from another generation", views: []execution.RuntimeStateView{carried}},
		{name: "no-data memory from before the Slot", memory: []execution.NoDataMemorySnapshot{{
			Identity: execution.PlanNoDataIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			Status:   execution.NoDataMemoryFound, PersistedApplyVersion: earlier}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stream := &streamedExecution{header: execution.InternalExecutionHeader{Contract: frozenRenewalContract(),
				DuePlans: []execution.DuePlan{due}}, state: execution.StatePreflightResult{Items: testCase.views},
				noData: execution.NoDataLoadResult{Items: testCase.memory}}
			if _, unseen := stream.unseenPlans()[due.Key()]; unseen != testCase.unseen {
				t.Fatalf("unseen %t, want %t", unseen, testCase.unseen)
			}
		})
	}
}

// Only a Plan new to its activation, with no gap marker at all and nothing
// read from before the Slot, is first seen; a marker of any kind - a warm-up
// marker, a satisfied one - says the Plan ran under the generation, and so
// does state read from before the Slot. A Plan whose generation moved under
// it ran under the one before, and finding nothing under the new one does
// not make it new. The satisfied marker that records the judgement is a
// clear with no scope at the Slot's own version.
func TestAPlanIsFirstSeenOnlyWithNeitherAMarkerNorEarlierState(t *testing.T) {
	due := firstSeenDue()
	fact := execution.PlanActivationFact{Plan: due.Identity, Selection: execution.ActivationCurrent,
		Selected: execution.ActivatedPlan{Identity: due.Identity, StateGeneration: due.StateGeneration, StateApplyEpoch: due.StateApplyEpoch,
			ScheduleRevision: due.ScheduleRevision, ForceWarming: true, Arrived: true}}
	moved := fact
	moved.Selected.Arrived = false
	forced := execution.PlanActivationResult{Contract: frozenRenewalContract(), Facts: []execution.PlanActivationFact{fact}}
	marker := func(status execution.GapLoadStatus) execution.GapLoadResult {
		return execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: fact.GapIdentity(), Status: status}}}
	}
	unseen := map[execution.PlanKey]struct{}{due.Key(): {}}
	for _, testCase := range []struct {
		name      string
		gaps      execution.GapLoadResult
		unseen    map[execution.PlanKey]struct{}
		moved     bool
		firstSeen bool
	}{
		{name: "no marker, nothing before", unseen: unseen, firstSeen: true},
		{name: "a missing marker, nothing before", gaps: marker(execution.GapMissing), unseen: unseen, firstSeen: true},
		{name: "a warm-up marker", gaps: marker(execution.GapFound), unseen: unseen},
		{name: "a satisfied marker", gaps: marker(execution.GapClearedTombstone), unseen: unseen},
		{name: "state from before", unseen: map[execution.PlanKey]struct{}{}},
		{name: "a generation moved, nothing before", unseen: unseen, moved: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			judged := forced
			if testCase.moved {
				judged = execution.PlanActivationResult{Contract: forced.Contract, Facts: []execution.PlanActivationFact{moved}}
			}
			stillForced, firstSeen := firstSeenActivations(judged, testCase.gaps, testCase.unseen)
			if _, judged := firstSeen[fact.Key()]; judged != testCase.firstSeen || (len(stillForced.Facts) == 1) == testCase.firstSeen {
				t.Fatalf("first seen %t, still forced %d, want first seen %t", judged, len(stillForced.Facts), testCase.firstSeen)
			}
		})
	}
	mutation, err := satisfiedMarker(frozenRenewalContract(), fact)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.ApplyVersion != ownVersion(t, due) || len(mutation.Scopes) != 1 || mutation.Scopes[0].Kind != execution.GapClear ||
		mutation.Scopes[0].Scope != (execution.GapScope{}) || mutation.Identity != fact.GapIdentity() {
		t.Fatalf("satisfied marker %+v, want a clear with no scope at the Slot's own version", mutation)
	}
}

// A marker this Slot wrote on an earlier attempt, under one of the two
// activation words, decides the Plan's change on the retry (design 013
// section 4.4 item 3): a new generation's own warm-up marker would otherwise
// read as one that ran before, and the word, and the marker's digest, would
// change on every retry. Any other marker at the version - a satisfied one,
// one a query-free completion wrote - and a marker from an earlier Slot are
// classified as before.
func TestAMarkerAtTheSlotsOwnVersionKeepsTheWordItWasWrittenUnder(t *testing.T) {
	due := firstSeenDue()
	own := ownVersion(t, due)
	earlier := execution.ApplyVersion{StateApplyEpoch: 7, EvaluationTime: own.EvaluationTime - 60, SlotDigest: "earlier"}
	rewarmed := execution.PlanActivationResult{Contract: frozenRenewalContract(), Facts: []execution.PlanActivationFact{{
		Plan: due.Identity, Selection: execution.ActivationCurrent, Selected: execution.ActivatedPlan{Identity: due.Identity,
			StateGeneration: due.StateGeneration, StateApplyEpoch: due.StateApplyEpoch, ScheduleRevision: due.ScheduleRevision, ForceWarming: true}}}}
	marker := func(status execution.GapLoadStatus, version execution.ApplyVersion, reason string) execution.GapLoadResult {
		snapshot := execution.GapGuardSnapshot{Identity: due.GapIdentity(), Status: status, PersistedApplyVersion: version}
		if reason != "" {
			snapshot.Scopes = []execution.GapScopeState{{Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(reason)}}
		}
		return execution.GapLoadResult{Items: []execution.GapGuardSnapshot{snapshot}}
	}
	for _, testCase := range []struct {
		name string
		gaps execution.GapLoadResult
		want activationChange
	}{
		{name: "this Slot's warm-up marker of a new generation", gaps: marker(execution.GapFound, own, contract.ReasonConfigDrift),
			want: activationChangeDrift},
		{name: "this Slot's marker of a Plan back", gaps: marker(execution.GapFound, own, contract.ReasonPlanReactivated),
			want: activationChangeReactivated},
		{name: "this Slot's satisfied marker", gaps: marker(execution.GapClearedTombstone, own, ""), want: activationChangeReactivated},
		{name: "this Slot's query-free marker", gaps: marker(execution.GapFound, own, contract.ReasonSnapshotUnavailable),
			want: activationChangeReactivated},
		{name: "an earlier Slot's warm-up marker", gaps: marker(execution.GapFound, earlier, contract.ReasonConfigDrift),
			want: activationChangeReactivated},
		{name: "no marker", want: activationChangeDrift},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if change := classifyActivationChange([]execution.DuePlan{due}, rewarmed, testCase.gaps, nil); change != testCase.want {
				t.Fatalf("change %v, want %v", change, testCase.want)
			}
		})
	}
}
