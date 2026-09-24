// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A replay runs every query of its Slot against one recovery deadline, so a
// query that times out can spend the time a later one needed. The access
// layer then hands the later query's consumers a binding marked
// EXECUTION_BUDGET_EXHAUSTED instead of a result - by design, and that
// binding makes the Levels that read it UNAVAILABLE.
//
// The shape that fails is a Plan with two queries: the primary answered, so
// there is a record and a Detect fact to make, and the dependency is the one
// the budget ran out on. The Detect fact carries the binding's reason, and the
// trigger refused it as a reason no Level may be unavailable for: the whole
// Slot failed with TRIGGER_INVARIANT, where it should have held this Level
// UNKNOWN under a named reason and let the rest of the round go on.
//
// The operation is not on the evaluator's header and does not need to be: only
// a recovery operation has a query deadline to run out of, so replay is where
// the binding comes from, and what the evaluator sees is the binding.
func TestReplayWhoseDependencyRanOutOfBudgetHoldsTheLevelUnknown(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `80`, nil)}, nil)
	input := g4Input(t, request, map[string][]contract.CanonicalRecordV2{"primary": {g4Record(99, `80`, nil)}})
	reason := execution.ReasonCode(contract.ReasonExecutionBudgetExhausted)
	exhausted := false
	for index := range input.Inputs {
		binding := &input.Inputs[index]
		if binding.Role == execution.InputRolePrimary {
			continue
		}
		// What completeBudgetExhaustedQueries hands a consumer of a query it
		// never sent.
		binding.Dataset, binding.View = nil, nil
		binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
		binding.Disposition, binding.ReasonCode, binding.ImpactScope = execution.AccessUnavailable, reason, execution.ImpactPlan
		exhausted = true
	}
	if !exhausted {
		t.Fatal("fixture has no dependency binding to exhaust")
	}

	result, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
		[]execution.SeriesEvaluationInputRequest{input}, request.State, request.Gaps, nil)
	if err != nil {
		t.Fatalf("a dependency the replay could not afford failed the Slot: %v", err)
	}
	if len(result.LevelOutcomes) != 1 || result.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		result.LevelOutcomes[0].ReasonCode != reason {
		t.Fatalf("Level outcomes = %+v, want one UNKNOWN under %s", result.LevelOutcomes, reason)
	}
	if len(result.StateResults) != 1 || len(result.StateResults[0].Events) != 0 {
		t.Fatalf("an unaffordable dependency produced events: %+v", result.StateResults)
	}
}
