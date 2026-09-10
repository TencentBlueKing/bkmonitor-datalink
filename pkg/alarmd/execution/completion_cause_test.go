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
		cause execution.UnavailableCause
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

// A completion that is not UNAVAILABLE has no cause to report. Inventing one
// would put a reason on a Slot that completed normally.
func TestDeriveCompletionLeavesTheCauseEmptyWhenNothingWasUnavailable(t *testing.T) {
	input := validInternalExecution()
	_, cause, err := execution.DeriveCompletion(input, execution.EvaluationResult{
		Plans: []execution.PlanEvaluationResult{{Disposition: execution.PlanDecided}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cause != "" {
		t.Fatalf("cause = %q, want none for a Slot that was not unavailable", cause)
	}
}

// The kind is what every persisted structure and the contract use, so deriving
// the cause must not have changed it. The two come from one traversal for the
// same reason: derived separately they could disagree about the same Slot.
func TestDeriveCompletionKindStillAgreesWithTheCombinedDerivation(t *testing.T) {
	input := validInternalExecution()
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
			t.Fatalf("the two derivations disagreed for %+v: %q/%v vs %q/%v",
				plans, kindOnly, errOnly, kind, err)
		}
	}
}
