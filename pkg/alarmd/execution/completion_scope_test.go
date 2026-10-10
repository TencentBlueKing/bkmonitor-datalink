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
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A round that did not answer whole names where: the primary input's query
// with the reason access gave it - an UNAVAILABLE round used to carry the
// cause and an empty reason, so every such round on the page read the same
// - a Level's outcome with its Level, a Plan with its Plan.
func TestACompletionCauseNamesTheQueryLevelOrPlanItWasFoundIn(t *testing.T) {
	input := validInternalExecution()
	plan := input.Inputs[0].Consumer.Plan
	decided := execution.PlanEvaluationResult{Plan: plan, Disposition: execution.PlanDecided}

	unavailable := validInternalExecution()
	unavailable.Inputs[0].Completeness = execution.CompletenessUnavailable
	unavailable.Inputs[0].ReasonCode = contract.ReasonQueryUnavailable
	kind, attribution, err := execution.DeriveCompletionAttribution(unavailable, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{decided}})
	if err != nil {
		t.Fatal(err)
	}
	consumer := unavailable.Inputs[0].Consumer
	if kind != execution.CompletionUnavailable || attribution.Cause != execution.CausePrimaryInputUnavailable ||
		attribution.Reason != contract.ReasonQueryUnavailable || attribution.Scope != (execution.CompletionScope{Plan: plan, HasPlan: true,
		LevelID: consumer.LevelID, HasLevel: consumer.HasLevel, PhysicalQuery: "physical-query-1"}) {
		t.Fatalf("primary unavailable = %s %+v, want the query and access's reason", kind, attribution)
	}
	partial := validInternalExecution()
	partial.Inputs[0].Completeness = execution.CompletenessPartial
	partial.Inputs[0].ReasonCode = contract.ReasonHistoryGapped
	if kind, attribution, err := execution.DeriveCompletionAttribution(partial, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{decided}}); err != nil ||
		kind != execution.CompletionPartialGap || attribution.Cause != execution.CausePrimaryInputPartial ||
		attribution.Reason != contract.ReasonHistoryGapped || attribution.Scope.PhysicalQuery != "physical-query-1" {
		t.Fatalf("primary partial = %s %+v %v, want the query and its reason", kind, attribution, err)
	}

	unknown := decided
	unknown.LevelOutcomes = []execution.LevelOutcome{{Plan: plan, LevelID: 3, Outcome: execution.LevelOutcomeUnknown, ReasonCode: contract.ReasonHistoryWarming}}
	if kind, attribution, err := execution.DeriveCompletionAttribution(input, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{unknown}}); err != nil ||
		kind != execution.CompletionUnavailable || attribution.Reason != contract.ReasonHistoryWarming ||
		attribution.Scope != (execution.CompletionScope{Plan: plan, HasPlan: true, LevelID: 3, HasLevel: true}) {
		t.Fatalf("Level unknown = %s %+v %v, want Level 3 named", kind, attribution, err)
	}

	for _, disposition := range []execution.PlanDisposition{execution.PlanUnavailable, execution.PlanReadinessGap} {
		named := execution.PlanEvaluationResult{Plan: plan, Disposition: disposition, ReasonCode: contract.ReasonQueryUnavailable}
		if _, attribution, err := execution.DeriveCompletionAttribution(input, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{named}}); err != nil ||
			attribution.Scope != (execution.CompletionScope{Plan: plan, HasPlan: true}) || attribution.Reason != contract.ReasonQueryUnavailable {
			t.Fatalf("%s = %+v %v, want the Plan named and its reason", disposition, attribution, err)
		}
	}

	// A primary whose code is the fallback is named for what the fallback
	// stands in for, not by the fallback: the query was never sent, or no
	// attempt said why. Its code stays the fallback for what reads bindings.
	for attribution, want := range map[execution.UnavailableAttribution]execution.ReasonCode{
		execution.UnavailableNoAttempts:      execution.ReasonQueryNotAttempted,
		execution.UnavailableNoAttemptReason: execution.ReasonQueryReasonUnrecorded,
		execution.UnavailableFromAttempt:     contract.ReasonQueryUnavailable,
	} {
		fallback := validInternalExecution()
		fallback.Inputs[0].Completeness = execution.CompletenessUnavailable
		fallback.Inputs[0].ReasonCode = contract.ReasonQueryUnavailable
		fallback.Inputs[0].UnavailableAttribution = attribution
		if _, attributed, err := execution.DeriveCompletionAttribution(fallback, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{decided}}); err != nil ||
			attributed.Cause != execution.CausePrimaryInputUnavailable || attributed.Reason != want {
			t.Fatalf("%s: %+v %v, want reason %s", attribution, attributed, err, want)
		}
	}
	// The words are the ones the counter and the line keep.
	for _, word := range []execution.ReasonCode{execution.ReasonQueryNotAttempted, execution.ReasonQueryReasonUnrecorded} {
		if !slices.Contains(observability.CompletionAttributionReasons, observability.ReasonCode(word)) ||
			observability.NormalizeReason(observability.ReasonCode(word), observability.ResultDegraded) != observability.ReasonCode(word) {
			t.Fatalf("%s is not kept by the counter and the line", word)
		}
	}

	// The detail derivation says the same cause and reason.
	kindDetail, cause, reason, err := execution.DeriveCompletionDetail(unavailable, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{decided}})
	if err != nil || kindDetail != execution.CompletionUnavailable || cause != execution.CausePrimaryInputUnavailable || reason != contract.ReasonQueryUnavailable {
		t.Fatalf("detail = %s %s %s %v, want the attribution's", kindDetail, cause, reason, err)
	}
}

// The cause words the completion counter and line take are every cause a
// completion can carry and no other.
func TestTheCounterTakesEveryCompletionCause(t *testing.T) {
	causes := []execution.CompletionCause{execution.CauseDataNotReady, execution.CausePlanUnavailable, execution.CausePrimaryInputUnavailable,
		execution.CauseLevelOutcomeUnknown, execution.CauseGapGuardWarming, execution.CausePrimaryInputPartial, execution.CauseConfigDrift,
		execution.CausePlanReactivated, execution.CausePlanNotActive}
	if len(causes) != len(observability.ProgressCompletionCauses) {
		t.Fatalf("%d causes, the counter takes %d", len(causes), len(observability.ProgressCompletionCauses))
	}
	for _, cause := range causes {
		if !slices.Contains(observability.ProgressCompletionCauses, string(cause)) {
			t.Fatalf("the counter does not take %s", cause)
		}
	}
}
