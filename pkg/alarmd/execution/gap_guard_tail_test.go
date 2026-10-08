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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// guardTailOutcome is an UNKNOWN on the fixture's Plan that carries the
// guard's reason, marked by the evaluator as the guard's alone.
func guardTailOutcome(input execution.InternalExecution) execution.LevelOutcome {
	return execution.LevelOutcome{Plan: input.DuePlans[0].Identity, LevelID: 5, Outcome: execution.LevelOutcomeUnknown,
		ReasonCode: "QUERY_UNAVAILABLE", GuardTail: true}
}

// An UNKNOWN is the tail of an earlier gap only when the guard alone held it
// and every input of its own Plan answered whole this round. Each side of each
// condition, with an incomplete input of another Plan not counting against it.
func TestAnUnknownIsAGuardTailOnlyWhenItsPlanAnsweredWhole(t *testing.T) {
	for _, testCase := range []struct {
		name string
		edit func(*execution.InternalExecution, *execution.LevelOutcome)
		want bool
	}{
		{name: "held by the guard alone, every input whole", edit: func(*execution.InternalExecution, *execution.LevelOutcome) {}, want: true},
		{name: "held for a reason of this round's own", edit: func(_ *execution.InternalExecution, outcome *execution.LevelOutcome) {
			outcome.GuardTail = false
		}},
		{name: "not an UNKNOWN", edit: func(_ *execution.InternalExecution, outcome *execution.LevelOutcome) {
			outcome.Outcome = execution.LevelOutcomeRecovery
		}},
		{name: "an input of the Plan came back partial", edit: func(input *execution.InternalExecution, _ *execution.LevelOutcome) {
			input.Inputs[0].Completeness = execution.CompletenessPartial
		}},
		{name: "an input of the Plan answered but is not available", edit: func(input *execution.InternalExecution, _ *execution.LevelOutcome) {
			input.Inputs[0].Disposition = execution.AccessDegraded
		}},
		{name: "an input of another Plan came back partial", edit: func(input *execution.InternalExecution, _ *execution.LevelOutcome) {
			other := input.Inputs[0]
			other.Consumer.Plan.StrategyID += "-other"
			other.Completeness = execution.CompletenessPartial
			input.Inputs = append(input.Inputs, other)
		}, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := validInternalExecution()
			input.Inputs = append([]execution.NamedInputBinding(nil), input.Inputs...)
			outcome := guardTailOutcome(input)
			testCase.edit(&input, &outcome)
			if got := execution.NewSlotInputWholeness(input.Inputs).UnknownIsGuardTail(outcome); got != testCase.want {
				t.Fatalf("UnknownIsGuardTail() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// A Slot whose only UNKNOWN is a guard's tail keeps its kind - a Level was
// not decided - and says why under its own cause, with the guard's reason
// beside it. Anything the round did wrong on its own outranks it: an UNKNOWN
// of this round's, or a readiness gap.
func TestAGuardTailIsItsOwnCauseAndAnythingOfThisRoundOutranksIt(t *testing.T) {
	input := validInternalExecution()
	tail := guardTailOutcome(input)
	own := tail
	own.GuardTail, own.ReasonCode = false, "QUERY_TIMEOUT"
	plan := func(outcomes ...execution.LevelOutcome) execution.PlanEvaluationResult {
		return execution.PlanEvaluationResult{Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded,
			LevelOutcomes: outcomes}
	}
	for _, testCase := range []struct {
		name   string
		plans  []execution.PlanEvaluationResult
		cause  execution.CompletionCause
		reason execution.ReasonCode
	}{
		{name: "only a tail", plans: []execution.PlanEvaluationResult{plan(tail)},
			cause: execution.CauseGapGuardWarming, reason: "QUERY_UNAVAILABLE"},
		{name: "a tail beside an UNKNOWN of this round's", plans: []execution.PlanEvaluationResult{plan(tail, own)},
			cause: execution.CauseLevelOutcomeUnknown, reason: "QUERY_TIMEOUT"},
		{name: "a tail beside a readiness gap", plans: []execution.PlanEvaluationResult{plan(tail), {Disposition: execution.PlanReadinessGap}},
			cause: execution.CauseDataNotReady},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			kind, cause, reason, err := execution.DeriveCompletionDetail(input, execution.EvaluationResult{Plans: testCase.plans})
			if err != nil {
				t.Fatal(err)
			}
			if kind != execution.CompletionUnavailable || cause != testCase.cause || reason != testCase.reason {
				t.Fatalf("completion = %s / %s / %s, want %s / %s / %s", kind, cause, reason,
					execution.CompletionUnavailable, testCase.cause, testCase.reason)
			}
		})
	}
}

// A Slot whose guard is warming holds every Level of every series, and its
// inputs are one binding per series, Level and requirement: the completion's
// cost has to grow with the series, not with their square. Compare ns/op
// across the sizes - ten times the series, about ten times the time.
func BenchmarkACompletionUnderAWarmingGuard(b *testing.B) {
	for _, series := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("series_%d", series), func(b *testing.B) {
			input := validInternalExecution()
			binding := input.Inputs[0]
			tail := guardTailOutcome(input)
			const levels, requirements = 3, 2
			input.Inputs = make([]execution.NamedInputBinding, 0, series*levels*requirements)
			outcomes := make([]execution.LevelOutcome, 0, series*levels)
			for index := 0; index < series*levels; index++ {
				for requirement := 0; requirement < requirements; requirement++ {
					input.Inputs = append(input.Inputs, binding)
				}
				outcomes = append(outcomes, tail)
			}
			result := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{
				Plan: input.DuePlans[0].Identity, Disposition: execution.PlanDecidedDegraded, LevelOutcomes: outcomes}}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, cause, _, err := execution.DeriveCompletionDetail(input, result); err != nil || cause != execution.CauseGapGuardWarming {
					b.Fatalf("cause = %s, err = %v", cause, err)
				}
			}
		})
	}
}
