// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import (
	"fmt"
	"testing"
)

// Rebalance facts are bounded like every other logged fact: negative
// counts clamp to zero and the per-worker list is cut at its cap with the
// truncation made visible.
func TestRebalanceFactsAreNormalizedAndBounded(t *testing.T) {
	owned := make([]RebalanceOwnedSample, 0, MaxRebalanceOwnedSamples+1)
	for index := 0; index <= MaxRebalanceOwnedSamples; index++ {
		owned = append(owned, RebalanceOwnedSample{WorkerID: fmt.Sprintf("worker-%03d", index), Owned: index - 1})
	}
	got := NormalizeObservation(Observation{
		Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess, Operation: OperationLoad,
		Rebalance: &RebalanceFacts{ReadyWorkers: -1, Assigned: 5, Target: -2, PlannedMoves: 1, Owned: owned},
	})
	if got.Component != ComponentOwnership || got.Stage != StageRebalancePlanned || got.Rebalance == nil {
		t.Fatalf("observation = %+v, want the rebalance stage kept", got)
	}
	facts := got.Rebalance
	if facts.ReadyWorkers != 0 || facts.Target != 0 || facts.Assigned != 5 || facts.PlannedMoves != 1 {
		t.Fatalf("counts were not normalized: %+v", facts)
	}
	if len(facts.Owned) != MaxRebalanceOwnedSamples || !facts.Truncated || facts.Owned[0].Owned != 0 || facts.Owned[1].Owned != 0 {
		t.Fatalf("owned samples were not bounded: len=%d truncated=%v first=%+v", len(facts.Owned), facts.Truncated, facts.Owned[:2])
	}
	if len(owned) != MaxRebalanceOwnedSamples+1 || owned[0].Owned != -1 {
		t.Fatal("normalization mutated the caller's samples")
	}
}

// The moves a plan names are bounded like the owned counts, with their own
// truncation flag, and the count of planned moves is not cut with them.
func TestRebalanceMovesAreBoundedWithTheirOwnTruncation(t *testing.T) {
	moves := make([]RebalanceMoveSample, 0, MaxRebalanceMoveSamples+1)
	for index := 0; index <= MaxRebalanceMoveSamples; index++ {
		moves = append(moves, RebalanceMoveSample{QueryGroup: fmt.Sprintf("query-group-%03d", index), From: "worker-1", To: "worker-2"})
	}
	got := NormalizeObservation(Observation{
		Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess, Operation: OperationLoad,
		Rebalance: &RebalanceFacts{ReadyWorkers: 2, Assigned: 40, PlannedMoves: len(moves), Moves: moves,
			Owned: []RebalanceOwnedSample{{WorkerID: "worker-1", Owned: 40}, {WorkerID: "worker-2", Owned: 0}}},
	})
	facts := got.Rebalance
	if len(facts.Moves) != MaxRebalanceMoveSamples || !facts.MovesTruncated || facts.Truncated ||
		facts.Moves[0] != moves[0] || facts.PlannedMoves != MaxRebalanceMoveSamples+1 {
		t.Fatalf("moves were not bounded on their own: len=%d moves_truncated=%v owned_truncated=%v planned=%d", len(facts.Moves), facts.MovesTruncated, facts.Truncated, facts.PlannedMoves)
	}
	within := NormalizeObservation(Observation{Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess, Operation: OperationLoad,
		Rebalance: &RebalanceFacts{PlannedMoves: 2, Moves: moves[:2]}}).Rebalance
	if len(within.Moves) != 2 || within.MovesTruncated || within.Moves[1] != moves[1] {
		t.Fatalf("moves within the bound were changed: %+v", within)
	}
	if len(moves) != MaxRebalanceMoveSamples+1 {
		t.Fatal("normalization mutated the caller's moves")
	}
}
