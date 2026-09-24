// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// One Query Group cannot take the whole pool, and being refused for that is
// not the same event as being refused because the pool was already full.
//
// Without a share, acquireEffects asks only whether the pool has room, so one
// object may legitimately hold all of it and every other Query Group on the
// replica starves for as long as it runs. The two refusals then arrive under
// one word, and they call for opposite work: a pool that somebody else filled
// frees up and the next attempt succeeds, while an object over its share is
// over it every round until the strategy is sharded. Waiting fixes one and
// never fixes the other.
func TestOneQueryGroupCannotTakeTheWholePool(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{
		MaxSeries: 10, MaxRetainedBytes: 100, MaxStateMutations: 10, MaxEvents: 10, MaxGapMutations: 10,
	}}
	// An empty pool, so nothing but the share can refuse this.
	stream := &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(40)}
	err := co.acquireEffects(effectCounts{states: 1}, 20, stream, stream.reservationPhase("normal_output"))

	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("an object asking for 60 of a 100 pool with nobody else running was admitted: %v", err)
	}
	if !exceeded.share {
		t.Fatal("refused, but not as a share: reported as a full pool it reads as somebody else's doing, " +
			"and the operator waits for capacity that was never the problem")
	}
	if exceeded.slot {
		t.Fatal("a share rejection is not the Slot exceeding its own per-Slot cap")
	}
	if exceeded.facts.SharedUsed != 0 {
		t.Fatalf("shared_used = %d, want none: no other Query Group is part of why this was refused",
			exceeded.facts.SharedUsed)
	}
	if exceeded.facts.OwnUsed == nil || *exceeded.facts.OwnUsed != 40 || exceeded.facts.Limit != 50 {
		t.Fatalf("facts = %+v, want this object's own total against half the pool", exceeded.facts)
	}
	// Nothing was taken: a refused reservation must not leave the pool charged.
	if co.reservations.retainedBytes != 0 {
		t.Fatalf("pool charged %d after a refusal", co.reservations.retainedBytes)
	}

	// Inside the share the same execution proceeds, so the case above is a
	// share and not a refusal of everything.
	fits := &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(20)}
	if err := co.acquireEffects(effectCounts{states: 1}, 20, fits, fits.reservationPhase("normal_output")); err != nil {
		t.Fatalf("an object inside its share was refused: %v", err)
	}
}

// The share is measured in bytes, and that is the point rather than an
// implementation detail: the same byte figure converts to wildly different
// counts depending on a strategy's shape, so a share stated as a count would
// mean a different amount of memory for every strategy it was applied to.
func TestTheShareIsMeasuredInBytesNotMutations(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{
		MaxSeries: 1000, MaxRetainedBytes: 1000, MaxStateMutations: 1000, MaxEvents: 1000, MaxGapMutations: 1000,
	}}
	// Far inside every count budget, far over the byte share.
	stream := &streamedExecution{coordinator: co, began: true, retainedByPhase: retainedSeed(400)}
	err := co.acquireEffects(effectCounts{states: 1}, 200, stream, stream.reservationPhase("normal_output"))
	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) || !exceeded.share {
		t.Fatalf("a Slot at 1 mutation and 600 bytes of a 1000-byte pool was not refused by share: %v", err)
	}
	if exceeded.budget != observability.CapacityBudgetRetainedBytes {
		t.Fatalf("budget = %q, want the byte budget", exceeded.budget)
	}

}
