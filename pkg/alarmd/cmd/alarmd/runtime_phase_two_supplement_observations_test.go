// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A supplement's start and completion both carry its operation, so every
// reader that leaves supplements out of its rounds can leave out both ends:
// the start once went out with no operation at all.
func TestASupplementsStartAndCompletionBothSayItIsASupplement(t *testing.T) {
	var observations []observability.Observation
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{Result: observability.ResultSuccess}, nil
		}),
		observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationSupplement}); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("observations %+v, want a start and a completion", observations)
	}
	for _, observation := range observations {
		if observation.Operation != observability.OperationSupplement {
			t.Errorf("%s says operation %q, want %q", observation.Stage, observation.Operation, observability.OperationSupplement)
		}
	}
}
