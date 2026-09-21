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

// A Level whose history is still warming records each point it evaluates
// and answers UNKNOWN until the window fills, so its point carries a
// business fact while the outcome does not. The result contract admits that
// pairing only while every input of the Level was complete; a dependency
// that came back with no data still lets detection reach a NORMAL fact,
// which the trigger then advanced and the contract refused as "State Level
// fact contradicts its Level outcome", on every replay and retry that met
// it. The evaluator now holds such a Level back: no fact, no advance, the
// guard as it was, and the same record with a complete dependency is
// history as before.
func TestWarmingLevelIsHeldBackWhenItsDependencyHasNoData(t *testing.T) {
	plan := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}, 2, 2)
	evaluator := newEvaluator(t)
	ctx := context.Background()

	first := g4Record(720, "100", nil)
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{first}, nil)
	request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request, map[string][]contract.CanonicalRecordV2{
		"primary": {first}, "previous": {g4Record(660, "100", nil)},
	})}
	result, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("warming round with complete inputs: %v", err)
	}
	warming := result.Plans[0]
	if len(warming.StateResults) != 1 || warming.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		warming.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) {
		t.Fatalf("first round = %+v, want an UNKNOWN warming outcome that recorded its point", warming)
	}
	mutation := warming.StateResults[0].Mutation

	// The next record's dependency is bound with no data behind it. Detection
	// still reaches a fact on what it has; the Level must not advance on it.
	second := g4Record(780, "100", nil)
	withoutData := func() execution.EvaluationRequest {
		next := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{second}, nil)
		next.State = execution.StatePreflightResult{Items: []execution.RuntimeStateView{stateViewFromMutation(mutation, execution.StateFoundWarming)}}
		input := g4Input(t, next, map[string][]contract.CanonicalRecordV2{"primary": {second}, "previous": {g4Record(720, "100", nil)}})
		for index := range input.Inputs {
			if input.Inputs[index].DatasetName == "previous" {
				input.Inputs[index].DataState = execution.DataStateEmpty
			}
		}
		next.Inputs = []execution.SeriesEvaluationInputRequest{input}
		return next
	}
	next := withoutData()
	result, err = evaluator.Evaluate(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	// A dependency that completed empty is an incomplete input by the one
	// definition the fold reads, so the worker proposes a Level marker for
	// it and the outcome names that marker's reason rather than the stored
	// guard's: the marker is the guard that ends up covering it.
	proposeRoundGuard(t, next, &result)
	if err := result.Validate(next); err != nil {
		t.Fatalf("a warming Level advanced on a dependency without data and the contract refused it: %v", err)
	}
	held := result.Plans[0]
	if held.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		held.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryEmpty) {
		t.Fatalf("outcome on the held round = %+v, want UNKNOWN naming the empty dependency", held.LevelOutcomes[0])
	}
	if len(held.GuardBeforeEvents) != 1 || len(held.GuardBeforeEvents[0].Scopes) != 1 ||
		held.GuardBeforeEvents[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryEmpty) ||
		!held.GuardBeforeEvents[0].Scopes[0].Scope.HasLevel {
		t.Fatalf("the held round's guard = %+v, want one Level marker naming the empty dependency", held.GuardBeforeEvents)
	}
	if len(held.StateResults) != 0 {
		t.Fatalf("the held round wrote state: %+v", held.StateResults[0].Mutation)
	}

	// The same record with the dependency complete is history as before.
	complete := withoutData()
	for index := range complete.Inputs[0].Inputs {
		complete.Inputs[0].Inputs[index].DataState = execution.DataStateData
	}
	result, err = evaluator.Evaluate(ctx, complete)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(complete); err != nil {
		t.Fatalf("complete round after the held one: %v", err)
	}
	recorded := result.Plans[0]
	if len(recorded.StateResults) != 1 || len(recorded.StateResults[0].Mutation.Points) == 0 ||
		recorded.StateResults[0].Mutation.Points[len(recorded.StateResults[0].Mutation.Points)-1].SourceTime != 780 {
		t.Fatalf("a complete round did not record its point: %+v", recorded)
	}
}
