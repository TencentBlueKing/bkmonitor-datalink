// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// targetPlanSlotInput is one round of a target-plan Plan whose memory holds
// host 101 with an open absence and host 102 with an open absence; the
// series are what the Slot admitted.
func targetPlanSlotInput(t *testing.T, resolution *TargetResolution, series ...map[string]string) PlanSlotInput {
	t.Helper()
	input := planSlotInput(storedSnapshot(t, 880,
		execution.NoDataGroupMemory{GroupKey: hostIDGroup(t, "101").Key(), LastSeen: 880, FirstAbsent: 940},
		execution.NoDataGroupMemory{GroupKey: hostIDGroup(t, "102").Key(), LastSeen: 880, FirstAbsent: 940},
	), series...)
	input.NoData = &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{"bk_host_id"}}
	input.Scope = nil
	input.Plan = hostTargetPlan("101")
	input.TargetResolution = resolution
	input.KnownHosts, input.HostsResolved = nil, false
	return input
}

// A target plan that could not be resolved in full judges no absence, but a
// group that arrived arrived: the admission filter let its record in only
// because it is a member that did resolve, and its business is not asked
// about for a target plan at all, so the round says of it exactly what a
// complete round would - its NORMAL and its last-seen. The silent member
// keeps its open absence, untouched, under every shape: the one whose
// selector did not answer, the one the validation dropped, and the one
// nothing resolved.
func TestATargetPlanItCannotFullyResolveStillJudgesTheGroupsThatArrived(t *testing.T) {
	reported, silent := hostIDGroup(t, "101").Key(), hostIDGroup(t, "102").Key()
	for name, test := range map[string]struct {
		resolution *TargetResolution
		outcome    SlotOutcome
	}{
		"nothing resolved it":    {resolution: nil, outcome: OutcomeSkippedTargetSelectorUnavailable},
		"a selector unavailable": {resolution: &TargetResolution{State: TargetResolutionUnavailable, Members: []string{"101"}}, outcome: OutcomeSkippedTargetSelectorUnavailable},
		"members dropped":        {resolution: &TargetResolution{State: TargetResolutionIncomplete, Members: []string{"101", "102"}}, outcome: OutcomeSkippedTargetMembersDropped},
		// 102 is outside what resolved. Read as the roster this would close
		// its absence as a member that left; it may only be one that dropped.
		"members dropped, the silent one among them": {resolution: &TargetResolution{State: TargetResolutionIncomplete, Members: []string{"101"}}, outcome: OutcomeSkippedTargetMembersDropped},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := EvaluatePlanSlot(targetPlanSlotInput(t, test.resolution, map[string]string{"bk_host_id": "101"}))
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != test.outcome {
				t.Fatalf("outcome = %q, want %q: absence is still not judged and the round says so by name", result.Outcome, test.outcome)
			}
			values := map[string]int{}
			for _, series := range result.Series {
				values[series.Group.Key()] = series.Value
				if series.Closing {
					t.Fatalf("series %s is a closing NORMAL; a round that does not know the target closes nothing", series.Group.Key())
				}
			}
			if want := map[string]int{reported: PresentValue, WholeItemGroup().Key(): PresentValue}; !reflect.DeepEqual(values, want) {
				t.Fatalf("series %v, want 101 and the whole item present and nothing about 102", values)
			}
			if result.Mutation == nil || result.Mutation.PresentAsOf != 1000 {
				t.Fatalf("mutation %+v, want 101 written as present as of 1000", result.Mutation)
			}
			for _, key := range result.Mutation.Del {
				if key == silent {
					t.Fatalf("102 was deleted; its absence is open and nothing established it ended")
				}
			}
			for _, delta := range result.Mutation.Set {
				if delta.GroupKey == silent {
					t.Fatalf("102 was rewritten as %+v; a round that could not look must not move its clock", delta.Absent)
				}
			}
			if result.Facts.Absent != 0 || result.Facts.Present != 1 {
				t.Fatalf("facts %+v, want one present and none absent", result.Facts)
			}
		})
	}
}

// The other direction: with nothing arriving, the same rounds say nothing and
// write nothing, so a target that blinked moves no clock and closes nothing.
func TestATargetPlanItCannotFullyResolveWithNothingArrivingWritesNothing(t *testing.T) {
	for name, resolution := range map[string]*TargetResolution{
		"nothing resolved it":    nil,
		"a selector unavailable": {State: TargetResolutionUnavailable, Members: []string{"101"}},
		"members dropped":        {State: TargetResolutionIncomplete, Members: []string{"101"}},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := EvaluatePlanSlot(targetPlanSlotInput(t, resolution))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Series) != 0 || result.Mutation != nil {
				t.Fatalf("round = %q with series %+v and mutation %v, want neither", result.Outcome, result.Series, result.Mutation != nil)
			}
		})
	}
}

// A host target this process cannot resolve stays skipped even when a host
// reported. Whether that host is still this business's is the index's answer
// and there is none: the business decides whether the group is remembered or
// forgotten, and whether a stopped absence on it recovers (A6), so neither
// can be decided from what arrived.
func TestAHostTargetItCannotResolveJudgesNothingEvenWhatArrived(t *testing.T) {
	reported := hostTargetGroup(HostIdentity{IP: "192.0.2.1", CloudID: "0"}).Key()
	result, outcome, err := EvaluateSlot(SlotInput{
		Plan:           slotPlan(hostScope("192.0.2.1|0"), []string{HostIPDimension, HostCloudDimension}),
		EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		Series:        []map[string]string{{HostIPDimension: "192.0.2.1", HostCloudDimension: "0"}},
		HostsResolved: false,
		Memory:        map[string]GroupMemory{reported: {LastSeen: 880, FirstAbsent: 940, SuppressedAt: 960}},
	})
	if err != nil || outcome != OutcomeSkippedHostsUnresolved {
		t.Fatalf("EvaluateSlot() = %q, %v; want %q", outcome, err, OutcomeSkippedHostsUnresolved)
	}
	if len(result.Verdicts) != 0 || len(result.Memory) != 0 {
		t.Fatalf("verdicts %v memory %v, want nothing judged", result.Verdicts, result.Memory)
	}
}
