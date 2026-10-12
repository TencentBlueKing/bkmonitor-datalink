// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// admitted is the admission line the worker emits for one call of a Plan, as
// admitState builds it: terminal with the reason, rules and sentence when the
// store refused, success when it admitted everything. The Plan is on the
// context, the object on the context too.
func admitted(ctx context.Context, tracker *Tracker, strategy string, slot int64, refused bool, rule, text string) {
	observation := observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageStateAdmission,
		Direction: observability.DirectionInternal, Result: observability.Result(observability.ResultSuccess),
		ReasonCode:      observability.ReasonNone,
		StateApplyChunk: &observability.StateApplyChunkFacts{Count: 1},
	}
	if refused {
		observation.Result, observation.ReasonCode = observability.ResultTerminal, observability.ReasonCode(contract.ReasonStateBudgetExceeded)
		observation.StateApplyChunk.RefusalRules, observation.StateApplyChunk.RefusalText = []string{rule}, text
	}
	tracker.Observe(observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: strategy, BusinessID: "2", EvaluationTime: slot}), observation)
}

// A Plan refused at admission rides on its object's row with the store's
// sentence, until a later round admits it. A clean chunk of the refusing
// round does not end it -- one record over the limit refuses its own chunk
// and leaves the rest admitted -- and the row carries the Plan refused most
// recently, over how many Plans are refused.
func TestAStateAdmissionRefusalRidesOnTheObjectRowUntilALaterRoundAdmits(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-refused"})
	const lifetime = "state: runtime state budget exceeded: required TTL 840h0m0s exceeds maximum 720h0m0s"
	const value = "record encodes to 600000 bytes, over the value limit of 524288 bytes"
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(1000 + 60*round)
		switch round {
		case 0:
			admitted(ctx, tracker, "s-1", slot, true, "lifetime_past_ceiling", lifetime)
			admitted(ctx, tracker, "s-1", slot, false, "", "")
		case 1:
			admitted(ctx, tracker, "s-2", slot, true, "value_over_limit", value)
		case 2:
			admitted(ctx, tracker, "s-1", slot, true, "lifetime_past_ceiling", lifetime)
		}
		degradedRound(ctx, tracker, slot)
		at.at = at.at.Add(time.Minute)
	}
	refusalOf := func() *StateAdmissionRefusal {
		t.Helper()
		rows := anyColumn(tracker)
		if len(rows) != 1 {
			t.Fatalf("rows = %+v, want the one object", rows)
		}
		return rows[0].StateAdmissionRefusal
	}
	want := &StateAdmissionRefusal{Plan: StrategyRef{StrategyID: "s-1", BusinessID: "2"}, Reason: contract.ReasonStateBudgetExceeded,
		Rules: []string{"lifetime_past_ceiling"}, Text: lifetime, EvaluationTime: 1120,
		FirstAt: now, LastAt: now.Add(2 * time.Minute), Refusals: 2, Plans: 2}
	if got := refusalOf(); !reflect.DeepEqual(got, want) {
		t.Fatalf("refusal = %+v, want s-1's -- refused most recently, the same-round admission not ending it -- over 2 Plans: %+v", got, want)
	}
	// A later round admits s-1: s-2's refusal is the row's now.
	admitted(ctx, tracker, "s-1", 1180, false, "", "")
	if got := refusalOf(); got == nil || got.Plan.StrategyID != "s-2" || got.Text != value || got.Plans != 1 {
		t.Fatalf("refusal after s-1 admitted = %+v, want s-2's alone", got)
	}
	// Its own round admitting a clean chunk is not a later round.
	admitted(ctx, tracker, "s-2", 1060, false, "", "")
	if got := refusalOf(); got == nil || got.Plan.StrategyID != "s-2" {
		t.Fatalf("refusal after s-2's own round admitted a chunk = %+v, want it kept", got)
	}
	admitted(ctx, tracker, "s-2", 1240, false, "", "")
	if got := refusalOf(); got != nil {
		t.Fatalf("refusal after every Plan admitted = %+v, want none", got)
	}
}
