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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An answer that may have been cut at the query service's group cap judges
// nothing, produces no series and writes nothing. The same round read as
// whole is the control: one host reported and one did not, so it judges the
// silent one absent and stores both - which is exactly the reading a cut
// answer must not get, because the silent host may be one the cap dropped.
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
	if len(cut.Series) != 0 || cut.Mutation != nil {
		t.Fatalf("a round whose answer may have been cut produced %d series and mutation %+v, want neither",
			len(cut.Series), cut.Mutation)
	}
	if cut.Facts != (AbsenceFacts{}) {
		t.Fatalf("a round that judged nothing reported absence facts %+v", cut.Facts)
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
			if outcome != test.want || len(result.Verdicts) != 0 {
				t.Fatalf("outcome = %q with %d verdicts, want %q and none", outcome, len(result.Verdicts), test.want)
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
