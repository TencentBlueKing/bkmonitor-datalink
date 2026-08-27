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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

type DetectionEvaluator interface {
	Evaluate(context.Context, detect.EvaluateRequest) (detect.DetectionBatch, error)
}

type isolatedDetection struct {
	Batch     detect.DetectionBatch
	Terminals []inputv2.Terminal
}

func evaluateDetectWithIsolation(
	ctx context.Context,
	evaluator DetectionEvaluator,
	request detect.EvaluateRequest,
) (isolatedDetection, error) {
	if evaluator == nil {
		return isolatedDetection{}, errors.New("alarmd coordinator: Detection evaluator is required")
	}
	remaining := append([]detect.PlanExecution(nil), request.Plans...)
	terminals := make([]inputv2.Terminal, 0)
	for {
		request.Plans = remaining
		if len(remaining) == 0 && len(terminals) > 0 {
			return isolatedDetection{Batch: detect.DetectionBatch{
				Completeness: request.Completeness, ExecutionMode: detect.ExecutionModeStandard,
				DetectionCoverage: detect.DetectionCoverageFull,
			}, Terminals: terminals}, nil
		}
		batch, err := evaluator.Evaluate(ctx, request)
		if err == nil {
			return isolatedDetection{Batch: batch, Terminals: terminals}, nil
		}
		var budget *detect.BudgetError
		if !errors.As(err, &budget) {
			return isolatedDetection{}, err
		}
		if budget.ReasonCode == "" || !contract.ReasonAllowedForV2(budget.ReasonCode, contract.ReasonDomainReceipt) {
			return isolatedDetection{}, fmt.Errorf("alarmd coordinator: invalid Detection budget error: %w", err)
		}
		switch budget.Scope {
		case detect.BudgetScopeMessage:
			terminals = append(terminals, inputv2.Terminal{
				Scope: inputv2.ScopeMessage, ReasonCode: budget.ReasonCode, FieldPath: "detect.budget",
			})
			return isolatedDetection{Terminals: terminals}, nil
		case detect.BudgetScopePlan:
			position := -1
			for index := range remaining {
				if remaining[index].View.PlanID() == budget.PlanID {
					position = index
					break
				}
			}
			if position < 0 || budget.PlanID == "" {
				return isolatedDetection{}, fmt.Errorf("alarmd coordinator: Detection budget references unknown Plan: %w", err)
			}
			terminals = append(terminals, inputv2.Terminal{
				Scope: inputv2.ScopePlan, PlanID: budget.PlanID, ReasonCode: budget.ReasonCode, FieldPath: "detect.budget",
			})
			remaining = append(remaining[:position], remaining[position+1:]...)
		default:
			return isolatedDetection{}, fmt.Errorf("alarmd coordinator: Detection budget has unknown scope: %w", err)
		}
	}
}
