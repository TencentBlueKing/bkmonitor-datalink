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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An answer that may have been cut at the query service's group cap judges no
// absence: the silent host may be one the cap dropped, so it gets no series
// and keeps its memory. The host that reported is in the answer and is judged
// present - a recovery series and its last-seen written - or a strategy whose
// answer is always cut would never recover any group. The same round read as
// whole is the control: there the silent one is judged absent.
func TestAnAnswerThatMayHaveBeenCutJudgesNoAbsence(t *testing.T) {
	// Two hosts of its own, in the documentation range: the target names
	// both, the index knows both, the memory saw both last round, and the
	// answer has the first alone.
	input := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: hostTargetGroup(HostIdentity{IP: "192.0.2.1", CloudID: "0"}).Key(), LastSeen: 940},
		execution.NoDataGroupMemory{GroupKey: hostTargetGroup(HostIdentity{IP: "192.0.2.2", CloudID: "0"}).Key(), LastSeen: 940},
	), map[string]string{HostIPDimension: "192.0.2.1", HostCloudDimension: "0"})
	input.Scope = hostScope("192.0.2.1|0", "192.0.2.2|0")
	input.KnownHosts = knownHosts("192.0.2.1|0", "192.0.2.2|0")

	whole, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Outcome != OutcomeEvaluated || len(whole.Series) == 0 || whole.Mutation == nil {
		t.Fatalf("read as whole the round = %q with %d series and mutation %v; the control has to judge "+
			"the silent host or the case below proves nothing", whole.Outcome, len(whole.Series), whole.Mutation != nil)
	}

	input.AnswerTruncated = true
	cut, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if cut.Outcome != OutcomeSkippedAnswerTruncated {
		t.Fatalf("outcome = %q, want %q", cut.Outcome, OutcomeSkippedAnswerTruncated)
	}
	reported := hostTargetGroup(HostIdentity{IP: "192.0.2.1", CloudID: "0"}).Key()
	silent := hostTargetGroup(HostIdentity{IP: "192.0.2.2", CloudID: "0"}).Key()
	values := map[string]int{}
	for _, series := range cut.Series {
		values[series.Group.Key()] = series.Value
	}
	if want := map[string]int{reported: PresentValue, WholeItemGroup().Key(): PresentValue}; !reflect.DeepEqual(values, want) {
		t.Fatalf("a cut round produced series %v, want the reporting host and the whole item present, nothing for %s",
			values, silent)
	}
	if cut.Mutation == nil || cut.Mutation.PresentAsOf != 1000 || cut.Mutation.GroupCount != 2 {
		t.Fatalf("a cut round wrote %+v, want both hosts kept and the reporting one present as of 1000", cut.Mutation)
	}
	for _, delta := range cut.Mutation.Set {
		if delta.GroupKey == silent && (delta.Absent == nil || delta.Absent.LastSeen != 940 || delta.Absent.FirstAbsent != 0) {
			t.Fatalf("the silent host was written as %+v, want it last seen at 940 and not absent", delta.Absent)
		}
	}
	if cut.Facts.Absent != 0 || cut.Facts.Unavailable != 1 {
		t.Fatalf("a cut round reported absence facts %+v, want one unavailable and none absent", cut.Facts)
	}
}

// A cut round where nothing reported writes nothing and says nothing.
func TestACutAnswerWithNothingPresentWritesNothing(t *testing.T) {
	input := planSlotInput(storedSnapshot(t, 940,
		execution.NoDataGroupMemory{GroupKey: hostTargetGroup(HostIdentity{IP: "192.0.2.1", CloudID: "0"}).Key(), LastSeen: 940},
	))
	input.Scope = hostScope("192.0.2.1|0")
	input.KnownHosts = knownHosts("192.0.2.1|0")
	input.AnswerTruncated = true
	cut, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if cut.Outcome != OutcomeSkippedAnswerTruncated || len(cut.Series) != 0 || cut.Mutation != nil {
		t.Fatalf("a cut round with nothing present = %q with %d series and mutation %v, want neither",
			cut.Outcome, len(cut.Series), cut.Mutation != nil)
	}
}

// An answer that is not full is skipped as not full whether or not it may
// also have been cut: that is the stronger statement, and the one that
// clears on its own next round.
func TestAnAnswerNotFullIsSkippedAsNotFullEvenIfCut(t *testing.T) {
	input := planSlotInput(storedSnapshot(t, 940), presentSeries())
	input.Completeness = execution.CompletenessPartial
	input.AnswerTruncated = true
	result, err := EvaluatePlanSlot(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeSkippedQueryNotFull {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSkippedQueryNotFull)
	}
}

// What the expected set could not be built from is named first, whatever
// the answer: an unresolved or incomplete target, or a host index this
// process does not have. The cut says nothing about which members exist,
// and a stall under the cut's name would send the owner to the query when
// the cache is what is missing.
func TestAnExpectedSetThatCouldNotBeBuiltOutranksACutAnswer(t *testing.T) {
	plan := targetPlanSlotPlan(hostTargetPlan("101"), []string{"bk_host_id"})
	for name, test := range map[string]struct {
		input SlotInput
		want  SlotOutcome
	}{
		"an unresolved target": {
			input: SlotInput{Plan: plan},
			want:  OutcomeSkippedTargetSelectorUnavailable,
		},
		"a target that dropped members": {
			input: SlotInput{Plan: plan, TargetResolution: &TargetResolution{State: TargetResolutionIncomplete, Members: []string{"101"}}},
			want:  OutcomeSkippedTargetMembersDropped,
		},
	} {
		t.Run(name, func(t *testing.T) {
			test.input.EvaluationTime, test.input.PeriodSeconds = 1000, 60
			test.input.Completeness, test.input.AnswerTruncated = execution.CompletenessFull, true
			result, outcome, err := EvaluateSlot(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if outcome != test.want {
				t.Fatalf("outcome = %q, want %q", outcome, test.want)
			}
			for key, verdict := range result.Verdicts {
				if verdict != VerdictUnavailable {
					t.Fatalf("verdict %q for %s, want no absence judged", verdict, key)
				}
			}
		})
	}
	t.Run("a host index this process does not have", func(t *testing.T) {
		input := planSlotInput(storedSnapshot(t, 940), presentSeries())
		input.HostsResolved, input.KnownHosts, input.AnswerTruncated = false, nil, true
		result, err := EvaluatePlanSlot(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != OutcomeSkippedHostsUnresolved {
			t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSkippedHostsUnresolved)
		}
	})
}

// The outcome is on the list a partition pre-creates from, under the word
// the metric, the stall line and the page read.
func TestTheTruncatedOutcomeIsOnTheList(t *testing.T) {
	if OutcomeSkippedAnswerTruncated != "SKIPPED_ANSWER_TRUNCATED" {
		t.Fatalf("the outcome is %q, want SKIPPED_ANSWER_TRUNCATED", OutcomeSkippedAnswerTruncated)
	}
	for _, outcome := range SlotOutcomes {
		if outcome == OutcomeSkippedAnswerTruncated {
			return
		}
	}
	t.Fatalf("SlotOutcomes = %v, want %q on it", SlotOutcomes, OutcomeSkippedAnswerTruncated)
}
