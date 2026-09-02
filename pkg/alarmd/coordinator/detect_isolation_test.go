// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

func TestEvaluateDetectIsolatesPlanBudgetAndRetriesSibling(t *testing.T) {
	t.Parallel()

	calls := 0
	evaluator := detectionEvaluatorFunc(func(_ context.Context, request detect.EvaluateRequest) (detect.DetectionBatch, error) {
		calls++
		if calls == 1 {
			return detect.DetectionBatch{}, &detect.BudgetError{
				Scope: detect.BudgetScopePlan, PlanID: "1001", ReasonCode: contract.ReasonPlanBudgetExceeded,
			}
		}
		if len(request.Plans) != 1 || request.Plans[0].View.PlanID() != "1002" {
			t.Fatalf("retry plans = %#v, want only sibling 1002", request.Plans)
		}
		return detect.DetectionBatch{Counts: detect.DetectionCounts{Plans: 1}}, nil
	})

	result, err := evaluateDetectWithIsolation(context.Background(), evaluator, detect.EvaluateRequest{
		Plans: planExecutionsWithIDs(t, "1001", "1002"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Batch.Counts.Plans != 1 || len(result.Terminals) != 1 ||
		result.Terminals[0].Scope != inputv2.ScopePlan || result.Terminals[0].PlanID != "1001" ||
		result.Terminals[0].ReasonCode != contract.ReasonPlanBudgetExceeded {
		t.Fatalf("isolation result = %#v, calls=%d", result, calls)
	}
}

func TestEvaluateDetectCompletesMessageBudgetAsTerminal(t *testing.T) {
	t.Parallel()

	evaluator := detectionEvaluatorFunc(func(context.Context, detect.EvaluateRequest) (detect.DetectionBatch, error) {
		return detect.DetectionBatch{}, &detect.BudgetError{
			Scope: detect.BudgetScopeMessage, ReasonCode: contract.ReasonMessageBudgetExceeded,
		}
	})
	result, err := evaluateDetectWithIsolation(context.Background(), evaluator, detect.EvaluateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Terminals) != 1 || result.Terminals[0].Scope != inputv2.ScopeMessage ||
		result.Terminals[0].ReasonCode != contract.ReasonMessageBudgetExceeded {
		t.Fatalf("terminals = %#v, want message budget terminal", result.Terminals)
	}
}

type detectionEvaluatorFunc func(context.Context, detect.EvaluateRequest) (detect.DetectionBatch, error)

func (function detectionEvaluatorFunc) Evaluate(ctx context.Context, request detect.EvaluateRequest) (detect.DetectionBatch, error) {
	return function(ctx, request)
}

func planExecutionsWithIDs(t testing.TB, planIDs ...string) []detect.PlanExecution {
	t.Helper()
	input := evaluationInputWithPlanIDs(t, planIDs...)
	views := input.PlanViews()
	result := make([]detect.PlanExecution, len(views))
	for index, view := range views {
		result[index] = detect.PlanExecution{View: view}
	}
	return result
}

func evaluationInputWithPlanIDs(t testing.TB, planIDs ...string) *inputv2.EvaluationInput {
	t.Helper()
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	base := envelope.PlanSet.EvaluationPlans[0]
	selector := envelope.Selectors[0].Selector
	envelope.PlanSet.EvaluationPlans = make([]contract.EvaluationPlanV2, len(planIDs))
	envelope.Selectors = make([]contract.PlanSelectorV2, len(planIDs))
	for index, planID := range planIDs {
		plan := base
		plan.PlanID = planID
		plan.StrategyRef.StrategyID = planID
		plan.StrategyIR.StrategyRef.StrategyID = planID
		envelope.PlanSet.EvaluationPlans[index] = plan
		envelope.Selectors[index] = contract.PlanSelectorV2{PlanOrdinal: uint32(index), Selector: selector}
	}
	envelope.PlanSet.PlanCount = uint32(len(planIDs))
	var err error
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := inputv2.New(g1ReaderLimits()).Decode(context.Background(), payload)
	if err != nil || decoded.Input == nil {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	return decoded.Input
}
