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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Four different conditions all complete a Slot as UNAVAILABLE, and they call
// for opposite responses: a readiness gap clears itself, a Plan that could not
// be decided does not. Hundreds of objects listed as degraded with no way to
// tell those apart is a list nobody can act on.
func TestDeriveCompletionSeparatesTheCausesThatShareOneKind(t *testing.T) {
	input := validInternalExecution()
	for _, testCase := range []struct {
		name  string
		plan  execution.PlanDisposition
		cause execution.CompletionCause
	}{
		{"readiness gap clears itself", execution.PlanReadinessGap, execution.CauseDataNotReady},
		{"plan could not be decided", execution.PlanUnavailable, execution.CausePlanUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			kind, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
				Plans: []execution.PlanEvaluationResult{{Disposition: testCase.plan}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if kind != execution.CompletionUnavailable {
				t.Fatalf("kind = %q, want the completion kind unchanged", kind)
			}
			if cause != testCase.cause {
				t.Fatalf("cause = %q, want %q", cause, testCase.cause)
			}
		})
	}
}

// A Slot can hit several at once. The one reported is the one a human can do
// something about: a readiness gap beside a Plan that could not be decided is a
// Slot someone should look at, and reporting the gap would say the opposite.
func TestDeriveCompletionReportsTheMostActionableCause(t *testing.T) {
	input := validInternalExecution()
	kind, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{
			{Disposition: execution.PlanReadinessGap},
			{Disposition: execution.PlanUnavailable},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if kind != execution.CompletionUnavailable {
		t.Fatalf("kind = %q", kind)
	}
	if cause != execution.CausePlanUnavailable {
		t.Fatalf("cause = %q, want the undecidable Plan to outrank the readiness gap", cause)
	}
	// Order of the Plans must not decide the answer, or the same Slot would
	// explain itself differently depending on how the results were collected.
	_, reversed, err := execution.DeriveCompletion(input, execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{
			{Disposition: execution.PlanUnavailable},
			{Disposition: execution.PlanReadinessGap},
		},
	})
	if err != nil || reversed != execution.CausePlanUnavailable {
		t.Fatalf("reversed cause = %q (err %v), want the same answer regardless of order", reversed, err)
	}
}

// COMPLETED_WITH_PARTIAL_GAP folds two things into one word the way UNAVAILABLE
// folds four, and it used to reach the page with no cause at all. The one this
// derivation can see is a primary input the provider answered with a stretch
// missing; the other, an edited strategy, is decided by the Worker's drift
// constructor from the same fact and is covered there.
func TestDeriveCompletionSaysWhichConditionMadeThePartialGap(t *testing.T) {
	input := validInternalExecution()
	input.Inputs[0].Completeness = execution.CompletenessPartial
	for _, disposition := range []execution.PlanDisposition{execution.PlanDecided, execution.PlanDecidedDegraded} {
		kind, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
			Plans: []execution.PlanEvaluationResult{{Disposition: disposition}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if kind != execution.CompletionPartialGap || cause != execution.CausePrimaryInputPartial {
			t.Fatalf("%s beside a partial primary: kind=%q cause=%q, want a partial gap caused by the primary input", disposition, kind, cause)
		}
	}
}

// A Plan degraded beside a FULL primary has no producer today, so no cause is
// minted for it. If a producer appears, its Slots show up as the shortfall from
// a full cause rate, which is where unidentified paths are meant to land;
// naming it in advance would hide that a path nobody knows about exists.
func TestDeriveCompletionLeavesAnUnproducedPartialGapWithoutACause(t *testing.T) {
	kind, cause, err := execution.DeriveCompletion(validInternalExecution(), execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{{Disposition: execution.PlanDecidedDegraded}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if kind != execution.CompletionPartialGap || cause != "" {
		t.Fatalf("degraded Plan beside a FULL primary: kind=%q cause=%q, want a partial gap with no cause", kind, cause)
	}
}

// The causes of the two kinds never compete: a Slot that hits a condition of
// each completes as UNAVAILABLE and reports an UNAVAILABLE cause, even the
// least actionable one. A partial cause on an UNAVAILABLE Slot would explain a
// completion that did not happen.
func TestDeriveCompletionKeepsPartialCausesOffAnUnavailableSlot(t *testing.T) {
	input := validInternalExecution()
	input.Inputs[0].Completeness = execution.CompletenessPartial
	kind, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{{Disposition: execution.PlanReadinessGap}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if kind != execution.CompletionUnavailable || cause != execution.CauseDataNotReady {
		t.Fatalf("readiness gap beside a partial primary: kind=%q cause=%q, want UNAVAILABLE with DATA_NOT_READY", kind, cause)
	}
}

// A FULL completion has no cause to report. Inventing one would put a reason
// on a Slot that completed normally.
func TestDeriveCompletionLeavesTheCauseEmptyWhenTheSlotCompletedFull(t *testing.T) {
	input := validInternalExecution()
	_, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{{Disposition: execution.PlanDecided}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cause != "" {
		t.Fatalf("cause = %q, want none for a Slot that completed FULL", cause)
	}
}

// The kind is what every persisted structure and the contract use, so deriving
// the cause must not have changed it. The two come from one traversal for the
// same reason: derived separately they could disagree about the same Slot.
func TestDeriveCompletionKindStillAgreesWithTheCombinedDerivation(t *testing.T) {
	for _, completeness := range []execution.Completeness{execution.CompletenessFull, execution.CompletenessPartial} {
		input := validInternalExecution()
		input.Inputs[0].Completeness = completeness
		for _, plans := range [][]execution.PlanEvaluationResult{
			{{Disposition: execution.PlanReadinessGap}},
			{{Disposition: execution.PlanUnavailable}},
			{{Disposition: execution.PlanDecided}},
			{{Disposition: execution.PlanTerminal}},
			{{Disposition: execution.PlanDecidedDegraded}},
		} {
			result := execution.EvaluationResult{Plans: plans}
			kindOnly, errOnly := execution.DeriveCompletionKind(input, result)
			kind, _, err := execution.DeriveCompletion(input, result)
			if kindOnly != kind || (errOnly == nil) != (err == nil) {
				t.Fatalf("the two derivations disagreed for %s primary and %+v: %q/%v vs %q/%v",
					completeness, plans, kindOnly, errOnly, kind, err)
			}
		}
	}
}

// The streaming path marks a Plan unavailable precisely when its primary input
// was, so on every such Slot both causes are noted. The input is the one
// reported: it is the cause an operator can act on, and the undecided Plan is
// its consequence. Before this ordering every Slot whose query came back with
// nothing usable was listed as a Plan that could not be decided. A Plan
// unavailable on its own still reports itself, being the only cause noted.
func TestDeriveCompletionNamesTheInputBeforeThePlanItLeftUndecided(t *testing.T) {
	starved := validInternalExecution()
	starved.Inputs[0].Completeness = execution.CompletenessUnavailable
	starved.Inputs[0].DataState = execution.DataStateUnknown
	undecided := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{Disposition: execution.PlanUnavailable}}}
	kind, cause, err := execution.DeriveCompletion(starved, undecided)
	if err != nil {
		t.Fatal(err)
	}
	if kind != execution.CompletionUnavailable || cause != execution.CausePrimaryInputUnavailable {
		t.Fatalf("undecided Plan beside an unavailable primary: kind=%q cause=%q, want the input named", kind, cause)
	}
	_, alone, err := execution.DeriveCompletion(validInternalExecution(), undecided)
	if err != nil || alone != execution.CausePlanUnavailable {
		t.Fatalf("undecided Plan beside a FULL primary: cause=%q err=%v, want the Plan named", alone, err)
	}
}
