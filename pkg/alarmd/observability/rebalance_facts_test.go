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
