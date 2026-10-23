// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A supplement whose kept read does not hold the Slot's query ran nothing:
// its completion line says skipped with the name, not a failed Slot, so it
// is written at WARN and not ERROR.
func TestASupplementWithoutItsKeptQueryCompletesSkippedByName(t *testing.T) {
	var observations []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	absent := fmt.Errorf("alarmd worker: supplement query: %w", execution.ErrSupplementKeptReadAbsent)
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{}, absent
		}),
		observer: observer,
	}
	request := execution.SlotExecutionRequest{Operation: execution.OperationSupplement}
	if _, err := executor.Execute(context.Background(), request); !errors.Is(err, execution.ErrSupplementKeptReadAbsent) {
		t.Fatalf("Execute() error = %v, want the absence passed through", err)
	}
	completed := observationAt(t, observations, observability.StageSlotCompleted)
	if completed.Result != observability.ResultSkipped ||
		completed.ReasonCode != observability.ReasonCode(contract.ReasonSupplementKeptReadAbsent) {
		t.Fatalf("slot_completed line = %s/%s, want skipped %s", completed.Result, completed.ReasonCode,
			contract.ReasonSupplementKeptReadAbsent)
	}
	if level := observability.ReasonLogLevel(completed.ReasonCode); level.String() != "WARN" {
		t.Fatalf("the reason logs at %s, want WARN", level)
	}
}
