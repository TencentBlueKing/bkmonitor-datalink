// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The completion that gives a Slot up carries what held it, as the Runner
// put it on the context: a Slot the query cooldown held until it fell past
// the replay range commits as GAP_SKIPPED with held_by query_cooldown and
// the cooldown's own two numbers, on the very line the fleet reads the round
// from. A round that ran carries no holder.
func TestTheSkippedCompletionCarriesWhatHeldTheSlot(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	fixture.ports.finalization.Mode = execution.FinalizationGapSkipped
	fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
	ctx := observability.ContextWithHeldBy(context.Background(), observability.HeldByFacts{
		Decision: "query_cooldown", AtUnixMilli: 1_700_000_000_000, QueryCooldownFailures: 16, QueryCooldownUntilMilli: 1_700_000_240_000,
	})
	result, err := fixture.coordinator.Execute(ctx, slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed || result.CompletionKind != execution.CompletionGapSkipped {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	committed := completionObservation(t, *fixture.observations)
	if committed.HeldBy == nil || committed.HeldBy.Decision != "query_cooldown" ||
		committed.HeldBy.QueryCooldownFailures != 16 || committed.HeldBy.QueryCooldownUntilMilli != 1_700_000_240_000 {
		t.Fatalf("GAP_SKIPPED commit held_by=%+v, want the cooldown with its failures and deadline", committed.HeldBy)
	}

	// The same Slot given up with no holder on the context: the word for
	// that, never an absent field, so a reader can tell "nothing held it"
	// from "this build does not say".
	fixture = newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	fixture.ports.finalization.Mode = execution.FinalizationGapSkipped
	fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay)); err != nil {
		t.Fatal(err)
	}
	committed = completionObservation(t, *fixture.observations)
	if committed.HeldBy == nil || committed.HeldBy.Decision != observability.HeldByNothing {
		t.Fatalf("GAP_SKIPPED commit with nothing on the context held_by=%+v, want %q", committed.HeldBy, observability.HeldByNothing)
	}

	// A round that ran its Slot: no holder on its completion, whatever the
	// context says about the round before.
	fixture = newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	if _, err := fixture.coordinator.Execute(ctx, slotRequest(execution.OperationReplay)); err != nil {
		t.Fatal(err)
	}
	committed = completionObservation(t, *fixture.observations)
	if committed.ProgressCompletionKind == string(execution.CompletionGapSkipped) {
		t.Fatalf("fixture meant a completion that ran, got %s", committed.ProgressCompletionKind)
	}
	if committed.HeldBy != nil {
		t.Fatalf("a completion that ran carries held_by=%+v, want none", committed.HeldBy)
	}
}

func completionObservation(t *testing.T, observations []observability.Observation) observability.Observation {
	t.Helper()
	for index := len(observations) - 1; index >= 0; index-- {
		if observations[index].Stage == observability.StageProgressCommitted && observations[index].ProgressCompletionKind != "" {
			return observations[index]
		}
	}
	t.Fatal("no committed completion observed")
	return observability.Observation{}
}
