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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// stageObservation is the round's last observation of stage.
func stageObservation(observed []observability.Observation, stage observability.Stage) (observability.Observation, bool) {
	var found observability.Observation
	ok := false
	for _, observation := range observed {
		if observation.Stage == stage {
			found, ok = observation, true
		}
	}
	return found, ok
}

// markerOrigin is the persisted Level scope's origin, if one stands.
func (fixture *cutoverStallFixture) markerOrigin(ctx context.Context) (slot float64, cause string, standing bool) {
	fixture.t.Helper()
	envelope, found := fixture.readPlanGapMarker(ctx)
	if !found {
		return 0, "", false
	}
	scopes, _ := envelope["scopes"].([]any)
	for _, entry := range scopes {
		scope, _ := entry.(map[string]any)
		inner, _ := scope["Scope"].(map[string]any)
		if hasLevel, _ := inner["HasLevel"].(bool); hasLevel {
			slot, _ = scope["ReasonOriginSlot"].(float64)
			cause, _ = scope["ReasonOriginCause"].(string)
			return slot, cause, true
		}
	}
	return 0, "", false
}

// The production reading, end to end: a Level guard a QUERY_TIMEOUT opened
// at an earlier Slot, and series whose queries answer whole. While the guard
// warms, each round's query line says the query succeeded and that the
// evaluation's QUERY_TIMEOUT was carried from the Slot that opened the guard;
// the Slot's completion line names its cause (GAP_GUARD_WARMING) and the same
// origin; and the marker keeps that origin through every warmup write.
func TestAWholeQueryUnderACarriedTimeoutGuardSaysSoOnEveryLine(t *testing.T) {
	fixture := newZeroSeriesGapFixture(t)
	ctx := context.Background()
	required := int(fixture.requiredFullSlots(ctx))
	if required < 3 {
		t.Fatalf("RequiredFullSlots = %d, want at least 3: one empty round, one returning round, one to clear", required)
	}
	opened := int64(fixture.firstNewSlot) - 600
	returning := false
	fixture.uqHosts = func(int64) []string {
		if returning {
			return []string{"192.0.2.10"}
		}
		return []string{}
	}
	held := 0
	fixture.runCurrentDataRoundsSeeded(ctx, func() bool {
		return fixture.seedLevelGuardFrom(ctx, contract.ReasonQueryTimeout, opened)
	}, required, func(round int, outcome execution.SlotExecutionResult, seen []observability.Observation) {
		defer func() { returning = true }()
		if slot, cause, standing := fixture.markerOrigin(ctx); standing && (int64(slot) != opened || cause != contract.ReasonQueryTimeout) {
			t.Fatalf("round %d: marker origin %v/%q, want the opening Slot %d/%s kept", round, slot, cause, opened, contract.ReasonQueryTimeout)
		}
		if !returning || round >= required {
			return
		}
		held++
		query, ok := stageObservation(seen, observability.StageQueryCompleted)
		if !ok {
			t.Fatalf("round %d: no query_completed", round)
		}
		if query.Result != observability.ResultSuccess || query.QueryFailure != nil {
			t.Fatalf("round %d: query line %s/%s failure %+v, want success: the query answered whole",
				round, query.Result, query.ReasonCode, query.QueryFailure)
		}
		if query.EvaluationReason != contract.ReasonQueryTimeout || query.CarriedReason == nil ||
			query.CarriedReason.OriginSlot != opened || query.CarriedReason.OriginCause != contract.ReasonQueryTimeout {
			t.Fatalf("round %d: evaluation %s/%s carried %+v, want QUERY_TIMEOUT carried from %d",
				round, query.EvaluationResult, query.EvaluationReason, query.CarriedReason, opened)
		}
		slot, ok := stageObservation(seen, observability.StageSlotCompleted)
		if !ok {
			t.Fatalf("round %d: no slot_completed", round)
		}
		if slot.SlotCompletionCause != string(execution.CauseGapGuardWarming) || slot.SlotCompletionReason != contract.ReasonQueryTimeout ||
			slot.CarriedReason == nil || slot.CarriedReason.OriginSlot != opened {
			t.Fatalf("round %d: slot_completed cause %q/%q carried %+v, want GAP_GUARD_WARMING/QUERY_TIMEOUT from %d (result cause %+v)",
				round, slot.SlotCompletionCause, slot.SlotCompletionReason, slot.CarriedReason, opened, outcome.CompletionCause)
		}
		progress, ok := stageObservation(seen, observability.StageProgressCommitted)
		if !ok || progress.CarriedReason == nil || progress.CarriedReason.OriginSlot != opened {
			t.Fatalf("round %d: progress_committed carried %+v, want the origin %d", round, progress.CarriedReason, opened)
		}
		evaluation, ok := stageObservation(seen, observability.StageEvaluationCompleted)
		if !ok || evaluation.CarriedReason == nil || evaluation.CarriedReason.OriginSlot != opened {
			t.Fatalf("round %d: evaluation_completed carried %+v, want the origin %d", round, evaluation.CarriedReason, opened)
		}
		// The algorithm reads "unavailable QUERY_TIMEOUT" because the guard
		// held its Level, not because it met a timeout: the fact says so.
		carriedAlgorithm := false
		for _, fact := range evaluation.AlgorithmEvaluations {
			if fact.ReasonCode == contract.ReasonQueryTimeout {
				if !fact.ReasonCarried {
					t.Fatalf("round %d: algorithm fact %+v reads as this round's timeout", round, fact)
				}
				carriedAlgorithm = true
			}
		}
		if !carriedAlgorithm {
			t.Fatalf("round %d: no algorithm fact under the guard's reason: %+v", round, evaluation.AlgorithmEvaluations)
		}
	})
	if held == 0 {
		t.Fatal("no round evaluated a series under the guard, so the carried reason was never exercised")
	}
}
