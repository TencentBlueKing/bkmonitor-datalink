// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// unclassified is what a line says when its site did not say: the three
// words a reader cannot act on. A scheduler decision that names its word in
// the facts must name it in reason_code too, or every grep on the reason
// field reads the decision as a defect at the emitter.
func unclassified(reason observability.ReasonCode) bool {
	return reason == observability.ReasonNotReported || reason == observability.ReasonInternalUnknown || reason == observability.ReasonOther
}

func TestTheRangeGateAndReplayExpiryLinesCarryTheirWordAsTheReasonCode(t *testing.T) {
	observer := &gateObserver{}
	source, ctx := fourBehindSource(t, observer, false)
	if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	var gate, expiry int
	for _, observation := range observer.seen {
		normalized := observability.NormalizeObservation(observation)
		switch observation.Stage {
		case observability.StageRangeGateDecided:
			gate++
			if observation.RangeGate == nil || normalized.ReasonCode != observability.ReasonCode(observation.RangeGate.Outcome) {
				t.Fatalf("range gate line reason=%q, want the outcome word %+v", normalized.ReasonCode, observation.RangeGate)
			}
			if normalized.ReasonCode != observability.RangeGateNoRangeFlight || normalized.Result != observability.ResultDegraded {
				t.Fatalf("range gate line=%s/%s, want degraded/%s: the fixture has no range flight, and a refusal is degraded", normalized.Result, normalized.ReasonCode, observability.RangeGateNoRangeFlight)
			}
		case observability.StageReplayExpired:
			expiry++
			if observation.ReplayExpiry == nil || normalized.ReasonCode != observability.ReasonCode(observation.ReplayExpiry.Reason) {
				t.Fatalf("replay expiry line reason=%q, want the expiry reason %+v", normalized.ReasonCode, observation.ReplayExpiry)
			}
		default:
			continue
		}
		if unclassified(normalized.ReasonCode) {
			t.Fatalf("%s normalizes to %q: the word is in the facts and not on the line", observation.Stage, normalized.ReasonCode)
		}
	}
	if gate != 1 || expiry != 1 {
		t.Fatalf("gate lines=%d expiry lines=%d, want one of each from a Slot given up on", gate, expiry)
	}
}

// A cooldown transition is a line with a result and a reason, not a bare
// event: entering and extending degrade the Query Group for QUERY_UNAVAILABLE,
// a query that answers resumes it from that reason, and a configuration
// change or the policy being switched off carry no reason at all -- none of
// them reads _other or reason_not_reported.
func TestACooldownTransitionCarriesAResultAndAReason(t *testing.T) {
	now := time.Unix(100, 0)
	var seen []observability.Observation
	runner := &Runner{queryGroup: "qg", now: func() time.Time { return now }, flights: &FlightCoordinator{
		limits:   RecoveryLimits{QueryUnavailableCooldown: true},
		observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { seen = append(seen, o) }),
	}}
	slot := frozenSlot("qg")
	for i := 0; i < 4; i++ {
		slot.Contract.Slot.EvaluationTime++
		runner.recordQueryAvailability(context.Background(), slot, unavailableResult(), 60)
	}
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), slot, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 60)
	for i := 0; i < 3; i++ {
		slot.Contract.Slot.EvaluationTime++
		runner.recordQueryAvailability(context.Background(), slot, unavailableResult(), 60)
	}
	runner.flights.limits.QueryUnavailableCooldown = false
	runner.deferUnavailableQuery(context.Background(), slot)

	want := []struct {
		event  string
		result observability.Result
		reason observability.ReasonCode
	}{
		{"entered", observability.ResultDegraded, observability.ReasonCode(contract.ReasonQueryUnavailable)},
		{"extended", observability.ResultDegraded, observability.ReasonCode(contract.ReasonQueryUnavailable)},
		{"recovered", observability.ResultResumed, observability.ReasonCode(contract.ReasonQueryUnavailable)},
		// Back within the re-entry window of the exit above.
		{"reentered", observability.ResultDegraded, observability.ReasonCode(contract.ReasonQueryUnavailable)},
		{"disabled", observability.ResultSuccess, observability.ReasonNone},
	}
	var lines []observability.Observation
	for _, observation := range seen {
		if observation.Stage == observability.StageQueryCooldown {
			lines = append(lines, observation)
		}
	}
	if len(lines) != len(want) {
		t.Fatalf("cooldown lines=%d, want %d: %+v", len(lines), len(want), lines)
	}
	for i, line := range lines {
		normalized := observability.NormalizeObservation(line)
		if line.QueryCooldown == nil || line.QueryCooldown.Event != want[i].event {
			t.Fatalf("line %d event=%+v, want %s", i, line.QueryCooldown, want[i].event)
		}
		if normalized.Result != want[i].result || normalized.ReasonCode != want[i].reason {
			t.Fatalf("line %d (%s) = %s/%s, want %s/%s", i, want[i].event, normalized.Result, normalized.ReasonCode, want[i].result, want[i].reason)
		}
		if normalized.Result == observability.ResultOther || unclassified(normalized.ReasonCode) {
			t.Fatalf("line %d (%s) reads as unclassified: %s/%s", i, want[i].event, normalized.Result, normalized.ReasonCode)
		}
	}
}

// A round that reached the builder and got its range is the denominator the
// refusals are read against, not a refusal: its line says success with the
// word, where the refusal words say degraded.
func TestARangeAppliedIsASuccessLineAndARefusalIsDegraded(t *testing.T) {
	observer := &gateObserver{}
	source, ctx := fourBehindSource(t, observer, true)
	if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	var lines int
	for _, observation := range observer.seen {
		if observation.Stage != observability.StageRangeGateDecided {
			continue
		}
		lines++
		normalized := observability.NormalizeObservation(observation)
		wantResult := observability.Result(observability.ResultDegraded)
		if observation.RangeGate.Outcome == observability.RangeGateApplied {
			wantResult = observability.ResultSuccess
		}
		if normalized.Result != wantResult || normalized.ReasonCode != observability.ReasonCode(observation.RangeGate.Outcome) {
			t.Fatalf("range gate line=%s/%s for outcome %q, want %s with the word", normalized.Result, normalized.ReasonCode, observation.RangeGate.Outcome, wantResult)
		}
	}
	if lines != 1 {
		t.Fatalf("range gate lines=%d, want one", lines)
	}
	// The classifier alone, for the branch the fixture may not take: applied
	// is success, and every other word on the list is degraded.
	for _, outcome := range observability.RangeGateOutcomes {
		result := observability.Result(observability.ResultDegraded)
		if outcome == observability.RangeGateApplied {
			result = observability.ResultSuccess
		}
		if got := rangeGateResult(outcome); got != result {
			t.Fatalf("rangeGateResult(%q)=%s, want %s", outcome, got, result)
		}
	}
}
