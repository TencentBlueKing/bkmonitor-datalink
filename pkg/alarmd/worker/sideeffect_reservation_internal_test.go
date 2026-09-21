// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// This exercises the production aggregation boundary, not synthetic reservation
// counters. Both streams remain alive until the second merge has returned.
func TestProcessSideEffectBudgetCapsTwoLiveSlotAggregates(t *testing.T) {
	for _, kind := range []string{"state", "event", "gap"} {
		t.Run(kind, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget(kind)}
			first := &streamedExecution{coordinator: coordinator}
			second := &streamedExecution{coordinator: coordinator}
			defer first.releaseProvisional()
			defer second.releaseProvisional()
			firstResult := sideEffectTestResult(kind, "qg-first")
			secondResult := sideEffectTestResult(kind, "qg-second")
			if err := first.mergeProvisional(context.Background(), firstResult, 0); err != nil {
				t.Fatalf("first Slot fitting budget rejected: %v", err)
			}
			err := second.mergeProvisional(context.Background(), secondResult, 0)
			if err == nil {
				t.Fatalf("two live Slots each retain one %s under process limit 1: second merge accepted", kind)
			}
			if !reflect.DeepEqual(first.evaluated, firstResult) {
				t.Fatal("rejection changed another Slot's accepted aggregate")
			}
			if len(second.evaluated.Plans) != 0 {
				t.Fatal("rejected Slot retained effects before reservation succeeded")
			}
		})
	}
}

func TestProvisionalMeasurementDoesNotCopyLargeEventPayload(t *testing.T) {
	result := sideEffectTestResult("event", "qg")
	result.Plans[0].StateResults[0].Events[0].EventID = strings.Repeat("a", 4<<20)
	// Size accounting must not build another event-sized JSON buffer. Small
	// reflection/iteration allocations are unrelated to payload length.
	const runs = 100
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for index := 0; index < runs; index++ {
		_, _ = evaluationRetainedSize(execution.StatePreflightResult{}, result)
	}
	runtime.ReadMemStats(&after)
	if bytesPerOp := (after.TotalAlloc - before.TotalAlloc) / runs; bytesPerOp > 64<<10 {
		t.Fatalf("size accounting copied event payload: %d bytes/op", bytesPerOp)
	}
}

func TestSideEffectBudgetRejectionDoesNotMutateAcceptedAggregate(t *testing.T) {
	for _, kind := range []string{"state", "event", "gap"} {
		t.Run(kind, func(t *testing.T) {
			budget := sideEffectTestBudget(kind)
			var target execution.EvaluationResult
			first := sideEffectTestResult(kind, "qg")
			if err := mergeProvisional(&target, first, budget); err != nil {
				t.Fatal(err)
			}
			next := sideEffectTestResult(kind, "qg")
			// A distinct Plan prevents Gap deduplication from hiding growth.
			next.Plans[0].Plan.StrategyID = "second-plan"
			if err := mergeProvisional(&target, next, budget); err == nil {
				t.Fatal("second effect unexpectedly fit the limit")
			}
			if !reflect.DeepEqual(target, first) {
				t.Fatalf("rejected %s merge changed accepted aggregate: plans=%d", kind, len(target.Plans))
			}
		})
	}
}

func TestSharedEffectRejectionIsAtomicAcrossCountsAndBytes(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget("event")}
	stream := &streamedExecution{coordinator: coordinator}
	defer stream.releaseProvisional()
	result := sideEffectTestResult("event", "qg")
	if err := stream.mergeProvisional(context.Background(), result, 0); err != nil {
		t.Fatal(err)
	}
	beforeBytes := coordinator.reservations.retainedBytes
	if err := stream.mergeProvisional(context.Background(), result, 100); err == nil {
		t.Fatal("event overcommit accepted")
	}
	if coordinator.reservations.events != 1 || coordinator.reservations.states != 1 || coordinator.reservations.retainedBytes != beforeBytes {
		t.Fatal("failed multidimensional admission left a partial reservation")
	}
	stream.releaseProvisional()
	if stream.evaluated.Plans != nil || coordinator.reservations.events != 0 || coordinator.reservations.states != 0 || coordinator.reservations.retainedBytes != 0 {
		t.Fatal("released effects still retained")
	}
}

func TestDuplicateGapDoesNotAccumulateReservations(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget("gap")}
	stream := &streamedExecution{coordinator: coordinator}
	defer stream.releaseProvisional()
	result := sideEffectTestResult("gap", "qg")
	if err := stream.mergeProvisional(context.Background(), result, 0); err != nil {
		t.Fatal(err)
	}
	before := stream.retained
	for range 100 {
		if err := stream.mergeProvisional(context.Background(), result, 0); err != nil {
			t.Fatal(err)
		}
	}
	if stream.retained != before || coordinator.reservations.gaps != 1 {
		t.Fatal("shared duplicate gap was charged repeatedly")
	}
}

func sideEffectTestBudget(kind string) ProvisionalBudget {
	budget := ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 100}
	switch kind {
	case "state":
		budget.MaxStateMutations = 1
	case "event":
		budget.MaxEvents = 1
	case "gap":
		budget.MaxGapMutations = 1
	}
	return budget
}

func sideEffectTestResult(kind, qg string) execution.EvaluationResult {
	plan := execution.PlanEvaluationResult{Plan: execution.PlanIdentity{StrategyID: "first-plan"}}
	switch kind {
	case "state":
		plan.StateResults = []execution.StateEvaluation{{}}
	case "event":
		plan.StateResults = []execution.StateEvaluation{{Events: []contract.TriggerEventV1{{}}}}
	case "gap":
		plan.GuardBeforeEvents = []execution.PlanGapMutation{{}}
	}
	return execution.EvaluationResult{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg)}},
		Plans:    []execution.PlanEvaluationResult{plan},
	}
}
