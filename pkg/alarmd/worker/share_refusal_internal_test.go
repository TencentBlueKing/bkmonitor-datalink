// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot refused because its own total is over the share carries the
// deterministic marker through the wrapping Execute puts on it, with the
// refusal's own facts: the scheduler reads it there to stop re-reading the
// window.
func TestAShareRefusalOfTheSlotsOwnTotalIsMarkedDeterministic(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{
		MaxSeries: 10, MaxRetainedBytes: 100, MaxStateMutations: 10, MaxEvents: 10, MaxGapMutations: 10,
	}}
	stream := &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(60)}
	err := co.acquireEffects(effectCounts{states: 1}, 20, stream, stream.reservationPhase("normal_output"))
	if err == nil {
		t.Fatal("an object over its share was admitted")
	}
	refusal, deterministic := execution.ShareRefusalOf(fmt.Errorf("alarmd worker: complete Slot: %w", err))
	if !deterministic {
		t.Fatalf("a refusal of the Slot's own total against the share is not marked deterministic: %v", err)
	}
	if want := (execution.ShareRefusal{Phase: "normal_output", OwnUsedBytes: 60, RequestedBytes: 20, LimitBytes: 75}); refusal != want {
		t.Fatalf("refusal = %+v, want %+v", refusal, want)
	}
}

// A refusal on the pool - other Query Groups hold part of it - is not
// deterministic: they release it, and a retry may fit. Neither the ordinary
// pool rejection nor a share-flagged one with shared usage behind it carries
// the marker.
func TestAPoolPressureRefusalIsNotMarkedDeterministic(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{
		MaxSeries: 10, MaxRetainedBytes: 100, MaxStateMutations: 10, MaxEvents: 10, MaxGapMutations: 10,
	}}
	co.reservations.retainedBytes = 90
	stream := &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(10)}
	err := co.acquireEffects(effectCounts{states: 1}, 20, stream, stream.reservationPhase("normal_output"))
	if err == nil {
		t.Fatal("a reservation past a pool others filled was admitted")
	}
	if _, deterministic := execution.ShareRefusalOf(fmt.Errorf("wrapped: %w", err)); deterministic {
		t.Fatalf("a pool rejection with shared usage 90 is marked deterministic: %v", err)
	}
	own := uint64(10)
	shared := &provisionalBudgetExceededError{budget: observability.CapacityBudgetRetainedBytes, share: true,
		facts: &observability.CapacityRejectionFacts{Phase: "normal_output", OwnUsed: &own, SharedUsed: 90, Requested: 20, Limit: 50}}
	if _, deterministic := execution.ShareRefusalOf(shared); deterministic {
		t.Fatal("a share refusal with shared usage behind it is marked deterministic")
	}
	slot := &provisionalBudgetExceededError{budget: observability.CapacityBudgetStateMutations, slot: true,
		facts: &observability.CapacityRejectionFacts{Phase: "slot_output", OwnUsed: &own, Limit: 5}}
	if _, deterministic := execution.ShareRefusalOf(slot); deterministic {
		t.Fatal("a per-Slot cap rejection is read as a share refusal")
	}
}
