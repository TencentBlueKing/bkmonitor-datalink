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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot's completion derived from the fold it kept of its batches is the
// completion derived from its merged result: whichever way the series were
// split into batches, with the Plans interleaved across them, the same kind,
// cause, reason and scope. Each case also says what that answer is, so the
// two derivations cannot agree on a wrong one.
func TestTheCompletionFoldOfASlotsBatchesIsTheCompletionOfItsMergedResult(t *testing.T) {
	whole := validInternalExecution()
	planA := whole.Inputs[0].Consumer.Plan
	planB := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"}
	partial := validInternalExecution()
	partial.Inputs[0].Completeness = execution.CompletenessPartial
	partial.Inputs[0].ReasonCode = contract.ReasonHistoryGapped
	unavailable := validInternalExecution()
	unavailable.Inputs[0].Completeness = execution.CompletenessUnavailable
	unavailable.Inputs[0].ReasonCode = contract.ReasonQueryUnavailable

	unknown := func(plan execution.PlanIdentity, level uint32, tail bool, reason execution.ReasonCode) execution.LevelOutcome {
		return execution.LevelOutcome{Plan: plan, LevelID: level, Outcome: execution.LevelOutcomeUnknown, ReasonCode: reason, GuardTail: tail}
	}
	terminal := execution.LevelOutcome{Plan: planA, LevelID: 9, Outcome: execution.LevelOutcomeTerminal}
	result := func(plans ...execution.PlanEvaluationResult) execution.EvaluationResult {
		return execution.EvaluationResult{Plans: plans}
	}
	plan := func(identity execution.PlanIdentity, disposition execution.PlanDisposition, reason execution.ReasonCode,
		outcomes ...execution.LevelOutcome) execution.PlanEvaluationResult {
		return execution.PlanEvaluationResult{Plan: identity, Disposition: disposition, ReasonCode: reason, LevelOutcomes: outcomes}
	}
	levelScope := func(identity execution.PlanIdentity, level uint32) execution.CompletionScope {
		return execution.CompletionScope{Plan: identity, HasPlan: true, LevelID: level, HasLevel: true}
	}

	for _, test := range []struct {
		name    string
		input   execution.InternalExecution
		batches []execution.EvaluationResult
		merged  execution.EvaluationResult
		kind    execution.CompletionKind
		want    execution.CompletionAttribution
		fails   bool
	}{
		{
			// Level 2 is the first outcome of the highest rank: the guard
			// tail before it ranks lower, and the outcomes of the same rank
			// after it, in A and in B, never replace it.
			name:  "the first unknown of the highest rank",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming))),
				result(plan(planB, execution.PlanDecided, "", unknown(planB, 7, false, contract.ReasonQueryTimeout)),
					plan(planA, execution.PlanDecided, "", unknown(planA, 2, false, contract.ReasonHistoryGapped))),
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 3, false, contract.ReasonQueryTimeout),
					unknown(planA, 4, true, contract.ReasonQueryUnavailable))),
			},
			merged: result(
				plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming),
					unknown(planA, 2, false, contract.ReasonHistoryGapped), unknown(planA, 3, false, contract.ReasonQueryTimeout),
					unknown(planA, 4, true, contract.ReasonQueryUnavailable)),
				plan(planB, execution.PlanDecided, "", unknown(planB, 7, false, contract.ReasonQueryTimeout))),
			kind: execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CauseLevelOutcomeUnknown, Reason: contract.ReasonHistoryGapped,
				Scope: levelScope(planA, 2)},
		},
		{
			name:  "only guard tails: the first one",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming))),
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 2, true, contract.ReasonHistoryGapped))),
			},
			merged: result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming),
				unknown(planA, 2, true, contract.ReasonHistoryGapped))),
			kind: execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CauseGapGuardWarming, Reason: contract.ReasonHistoryWarming,
				Scope: levelScope(planA, 1)},
		},
		{
			// Beside an input that did not answer whole a guard tail is not
			// a tail, so the tail and the outcome after it rank alike and the
			// tail, first, is the one named.
			name:  "a guard tail beside a partial input",
			input: partial,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming))),
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 2, false, contract.ReasonQueryTimeout))),
			},
			merged: result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, true, contract.ReasonHistoryWarming),
				unknown(planA, 2, false, contract.ReasonQueryTimeout))),
			kind: execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CauseLevelOutcomeUnknown, Reason: contract.ReasonHistoryWarming,
				Scope: levelScope(planA, 1)},
		},
		{
			name:  "a terminal level outcome in a later batch",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, false, contract.ReasonQueryTimeout))),
				result(plan(planA, execution.PlanDecided, "", terminal)),
			},
			merged: result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, false, contract.ReasonQueryTimeout), terminal)),
			kind:   execution.CompletionTerminal,
		},
		{
			// The later batch's UNAVAILABLE outranks the earlier DECIDED, and
			// a Plan that is not decided is not read for its Level outcomes:
			// the TERMINAL outcome of the first batch decides nothing.
			name:  "a disposition upgraded by a later batch",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", terminal)),
				result(plan(planA, execution.PlanUnavailable, contract.ReasonQueryUnavailable)),
			},
			merged: result(plan(planA, execution.PlanUnavailable, contract.ReasonQueryUnavailable, terminal)),
			kind:   execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CausePlanUnavailable, Reason: contract.ReasonQueryUnavailable,
				Scope: execution.CompletionScope{Plan: planA, HasPlan: true}},
		},
		{
			// Of two dispositions of one rank the first stays, with its reason.
			name:  "an equal rank keeps the first reason",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanReadinessGap, contract.ReasonHistoryWarming)),
				result(plan(planA, execution.PlanUnavailable, contract.ReasonQueryUnavailable)),
				result(plan(planA, execution.PlanDecided, "")),
			},
			merged: result(plan(planA, execution.PlanReadinessGap, contract.ReasonHistoryWarming)),
			kind:   execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CauseDataNotReady, Reason: contract.ReasonHistoryWarming,
				Scope: execution.CompletionScope{Plan: planA, HasPlan: true}},
		},
		{
			name:  "a plan decided degraded",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "")),
				result(plan(planB, execution.PlanDecided, ""), plan(planA, execution.PlanDecidedDegraded, contract.ReasonHistoryGapped)),
			},
			merged: result(plan(planA, execution.PlanDecidedDegraded, contract.ReasonHistoryGapped), plan(planB, execution.PlanDecided, "")),
			kind:   execution.CompletionPartialGap,
		},
		{
			name:    "a partial primary",
			input:   partial,
			batches: []execution.EvaluationResult{result(plan(planA, execution.PlanDecided, "")), result(plan(planA, execution.PlanDecided, ""))},
			merged:  result(plan(planA, execution.PlanDecided, "")),
			kind:    execution.CompletionPartialGap,
			want: execution.CompletionAttribution{Cause: execution.CausePrimaryInputPartial, Reason: contract.ReasonHistoryGapped,
				Scope: execution.CompletionScope{Plan: planA, HasPlan: true, LevelID: partial.Inputs[0].Consumer.LevelID,
					HasLevel: partial.Inputs[0].Consumer.HasLevel, PhysicalQuery: "physical-query-1"}},
		},
		{
			// The primary outranks every Level outcome noted after it.
			name:  "an unavailable primary",
			input: unavailable,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, false, contract.ReasonQueryTimeout))),
			},
			merged: result(plan(planA, execution.PlanDecided, "", unknown(planA, 1, false, contract.ReasonQueryTimeout))),
			kind:   execution.CompletionUnavailable,
			want: execution.CompletionAttribution{Cause: execution.CausePrimaryInputUnavailable, Reason: contract.ReasonQueryUnavailable,
				Scope: execution.CompletionScope{Plan: planA, HasPlan: true, LevelID: unavailable.Inputs[0].Consumer.LevelID,
					HasLevel: unavailable.Inputs[0].Consumer.HasLevel, PhysicalQuery: "physical-query-1"}},
		},
		{
			name:  "a retry-pending plan in a later batch",
			input: whole,
			batches: []execution.EvaluationResult{
				result(plan(planA, execution.PlanDecided, "")),
				result(plan(planA, execution.PlanRetryPending, contract.ReasonQueryTimeout)),
			},
			merged: result(plan(planA, execution.PlanRetryPending, contract.ReasonQueryTimeout)),
			fails:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind, attribution, err := execution.DeriveCompletionAttribution(test.input, test.merged)
			if test.fails != (err != nil) {
				t.Fatalf("merged: err = %v, want failure %t", err, test.fails)
			}
			if !test.fails && (kind != test.kind || !reflect.DeepEqual(attribution, test.want)) {
				t.Fatalf("merged = %s %+v, want %s %+v", kind, attribution, test.kind, test.want)
			}
			for name, batches := range map[string][]execution.EvaluationResult{
				"as written": test.batches, "one outcome a batch": splitByOutcome(test.merged),
			} {
				var fold execution.CompletionAttributionFold
				for _, batch := range batches {
					fold.Add(batch)
				}
				foldedKind, folded, foldErr := execution.DeriveFoldedCompletionAttribution(test.input, fold)
				if (foldErr != nil) != (err != nil) || foldedKind != kind || !reflect.DeepEqual(folded, attribution) {
					t.Fatalf("%s: folded = %s %+v %v, merged = %s %+v %v", name, foldedKind, folded, foldErr, kind, attribution, err)
				}
			}
		})
	}
}

// splitByOutcome splits a merged result into batches of one Level outcome per
// Plan, the Plans interleaved in the order the merged result has them. The
// first batch carries each Plan's merged disposition and reason; the later
// ones say DECIDED, which outranks nothing, so they merge back to it.
func splitByOutcome(merged execution.EvaluationResult) []execution.EvaluationResult {
	var batches []execution.EvaluationResult
	for position := 0; ; position++ {
		var batch execution.EvaluationResult
		for _, plan := range merged.Plans {
			if position == 0 {
				first := plan
				first.LevelOutcomes = nil
				if len(plan.LevelOutcomes) != 0 {
					first.LevelOutcomes = plan.LevelOutcomes[:1]
				}
				batch.Plans = append(batch.Plans, first)
				continue
			}
			if position < len(plan.LevelOutcomes) {
				batch.Plans = append(batch.Plans, execution.PlanEvaluationResult{Plan: plan.Plan, Disposition: execution.PlanDecided,
					LevelOutcomes: plan.LevelOutcomes[position : position+1]})
			}
		}
		if len(batch.Plans) == 0 {
			return batches
		}
		batches = append(batches, batch)
	}
}
