// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every case here drives the real evaluator and then hands what it produced to
// the real result contract.
//
// That combination is the point. The rule these cases are about -- a Plan whose
// load failed has to say it will be retried -- had three tests around it and
// none of them could see it break: the coordinator's used a fake evaluator, so
// the disposition came from the double rather than from the fold under test;
// the evaluator's fed a failed State load and then asserted something else
// entirely. The contract refused the result on every round in production while
// both layers stayed green. So: real evaluator in, real contract after.
func evaluateAndValidate(
	t *testing.T, request execution.EvaluationRequest,
) execution.PlanEvaluationResult {
	t.Helper()
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("the contract refused what the evaluator produced: %v", err)
	}
	if len(result.Plans) != 1 {
		t.Fatalf("Evaluate() returned %d Plans, want one", len(result.Plans))
	}
	return result.Plans[0]
}

// retryableStateRequest is a round whose Runtime State could not be read.
func retryableStateRequest(t *testing.T) execution.EvaluationRequest {
	t.Helper()
	request := requestFixture(t, json.RawMessage(`10`), nil)
	request.State.Items[0].Status = execution.StateRetryableIO
	request.State.Items[0].ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
	return request
}

// gapStatusRequest is a round whose Plan gap marker came back in one status.
func gapStatusRequest(t *testing.T, status execution.GapLoadStatus, reason string) execution.EvaluationRequest {
	t.Helper()
	request := requestFixture(t, json.RawMessage(`10`), nil)
	due := request.Header.DuePlans[0]
	request.Gaps.Items[0] = execution.GapGuardSnapshot{
		Identity:   execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		Status:     status,
		ReasonCode: execution.ReasonCode(reason),
	}
	return request
}

// R1: a State load that failed makes the Plan retry-pending, naming what the
// load said.
//
// Before this it was DECIDED_DEGRADED, because the disposition was folded from
// the Level outcomes and a series whose State could not be read produces
// UNKNOWN outcomes like any other. The Plan was reported as decided, the
// contract refused it as RETRYABLE_LOAD_NOT_RETRY_PENDING every round, and the
// refusal carried no reason anybody could act on.
func TestAFailedStateLoadMakesThePlanRetryPending(t *testing.T) {
	plan := evaluateAndValidate(t, retryableStateRequest(t))
	if plan.Disposition != execution.PlanRetryPending {
		t.Fatalf("disposition = %q, want %q: the round can be run again and the contract requires it to "+
			"say so", plan.Disposition, execution.PlanRetryPending)
	}
	if plan.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("reason = %q, want the load's own %q", plan.ReasonCode, contract.ReasonRedisUnavailable)
	}
	if len(plan.StateResults) != 0 {
		t.Fatalf("a series whose State could not be read produced %d mutations", len(plan.StateResults))
	}
}

// R1: a gap marker that could not be read holds the whole Plan, and nothing is
// written.
//
// The guard state this round would be judged against is unknown. Evaluating
// anyway and writing State is a decision taken without the one input that says
// whether the Level was allowed to advance -- and unlike a refusal, a write
// cannot be taken back on the retry.
func TestAnUnreadableGapMarkerHoldsThePlanAndWritesNothing(t *testing.T) {
	plan := evaluateAndValidate(t,
		gapStatusRequest(t, execution.GapUnavailable, contract.ReasonRedisUnavailable))
	if plan.Disposition != execution.PlanRetryPending {
		t.Fatalf("disposition = %q, want %q", plan.Disposition, execution.PlanRetryPending)
	}
	if plan.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("reason = %q, want the gap load's own", plan.ReasonCode)
	}
	if len(plan.StateResults) != 0 {
		t.Fatalf("the Plan wrote %d State mutations without knowing its guard state", len(plan.StateResults))
	}
	if len(plan.LevelOutcomes) == 0 {
		t.Fatal("the Plan reported no Level outcomes at all")
	}
	for _, outcome := range plan.LevelOutcomes {
		if outcome.Outcome != execution.LevelOutcomeUnknown {
			t.Fatalf("Level %d = %q, want UNKNOWN: nothing was judged", outcome.LevelID, outcome.Outcome)
		}
	}
}

// R2: a terminal gap marker makes the Plan terminal, and nothing is written.
//
// Retrying reads the same broken marker, so this one does not say retry.
func TestATerminalGapMarkerMakesThePlanTerminal(t *testing.T) {
	plan := evaluateAndValidate(t,
		gapStatusRequest(t, execution.GapTerminal, contract.ReasonStateCorrupt))
	if plan.Disposition != execution.PlanTerminal {
		t.Fatalf("disposition = %q, want %q", plan.Disposition, execution.PlanTerminal)
	}
	if plan.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
		t.Fatalf("reason = %q, want the gap load's own", plan.ReasonCode)
	}
	if len(plan.StateResults) != 0 {
		t.Fatalf("a Plan with a broken marker wrote %d mutations", len(plan.StateResults))
	}
	for _, outcome := range plan.LevelOutcomes {
		if outcome.Outcome != execution.LevelOutcomeTerminal {
			t.Fatalf("Level %d = %q, want TERMINAL", outcome.LevelID, outcome.Outcome)
		}
	}
}

// R1 over R2: a round that can succeed on its own is given the chance.
//
// A terminal marker is still terminal next round; a failed read may not be. So
// when both are in play the Plan retries, and it names the State's reason,
// which is the one this series met first.
func TestARetryableLoadOutranksATerminalMarker(t *testing.T) {
	request := retryableStateRequest(t)
	due := request.Header.DuePlans[0]
	request.Gaps.Items[0] = execution.GapGuardSnapshot{
		Identity:   execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		Status:     execution.GapUnavailable,
		ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt),
	}
	plan := evaluateAndValidate(t, request)
	if plan.Disposition != execution.PlanRetryPending {
		t.Fatalf("disposition = %q, want %q", plan.Disposition, execution.PlanRetryPending)
	}
	if plan.ReasonCode != execution.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("reason = %q, want the State load's %q ahead of the gap's; the order has to be stable or "+
			"the same round reports a different reason each attempt",
			plan.ReasonCode, contract.ReasonRedisUnavailable)
	}
}

// R4: what the loads decided is not overwritten by what the Levels concluded.
//
// The fold reads the outcomes, which say nothing about whether the records
// behind them could be read. Running it after a load has spoken is how a Plan
// that has to be retried came to be reported as decided in the first place.
func TestTheOutcomeFoldDoesNotOverwriteWhatTheLoadsDecided(t *testing.T) {
	plan := evaluateAndValidate(t, retryableStateRequest(t))
	if plan.Disposition != execution.PlanRetryPending {
		t.Fatalf("disposition = %q, want the load's %q rather than the fold's DECIDED_DEGRADED",
			plan.Disposition, execution.PlanRetryPending)
	}
	degraded := false
	for _, outcome := range plan.LevelOutcomes {
		if outcome.Outcome == execution.LevelOutcomeUnknown {
			degraded = true
		}
	}
	if !degraded {
		t.Fatal("no UNKNOWN outcome, so the fold had nothing to overwrite with and this proves nothing")
	}
}

// R3: a series whose stored record cannot be decoded is guarded by that record.
//
// The outcome is TERMINAL and there is no marker to point at. The bad blob is
// the persistent fact -- read again identically every round, more durable than
// anything a writer could put beside it -- and the contract now accepts it as
// the guard. Before, the Level was refused on every round for as long as the
// record sat there, and nothing named what to clear.
func TestABadRecordIsTheGuardForTheTerminalOutcomeItCauses(t *testing.T) {
	request := requestFixture(t, json.RawMessage(`10`), nil)
	request.State.Items[0].Status = execution.StateDeterministicInvalid
	request.State.Items[0].ReasonCode = execution.ReasonCode(contract.ReasonStateCorrupt)
	plan := evaluateAndValidate(t, request)
	if plan.Disposition != execution.PlanDecidedDegraded {
		t.Fatalf("disposition = %q, want %q: a broken record is one series' problem and does not make the "+
			"Plan retry or terminate", plan.Disposition, execution.PlanDecidedDegraded)
	}
	for _, outcome := range plan.LevelOutcomes {
		if outcome.Outcome != execution.LevelOutcomeTerminal ||
			outcome.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
			t.Fatalf("Level %d = %q/%q, want TERMINAL naming the load", outcome.LevelID, outcome.Outcome, outcome.ReasonCode)
		}
	}
}

// R3's other half: the exemption reads the load, not the outcome.
//
// A TERMINAL outcome whose series read its record perfectly well has no guard
// and must still be refused. Without this the exemption would be "any TERMINAL
// passes", which removes the rule rather than narrowing it.
func TestATerminalOutcomeWithAReadableRecordIsStillRefused(t *testing.T) {
	request := requestFixture(t, json.RawMessage(`10`), nil)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	// A business terminal on a series whose State loaded cleanly: no marker,
	// no broken record, nothing to cover it.
	plan := result.Plans[0]
	if len(plan.LevelOutcomes) == 0 {
		t.Fatal("the fixture produced no Level outcome to make terminal")
	}
	for index := range plan.LevelOutcomes {
		plan.LevelOutcomes[index].Outcome = execution.LevelOutcomeTerminal
		plan.LevelOutcomes[index].ReasonCode = execution.ReasonCode(contract.ReasonStateCorrupt)
	}
	plan.Disposition = execution.PlanDecidedDegraded
	plan.ReasonCode = execution.ReasonCode(contract.ReasonStateCorrupt)
	result.Plans[0] = plan
	result.Result, result.ReasonCode = "DEGRADED", execution.ReasonCode(contract.ReasonStateCorrupt)
	if err := result.Validate(request); err == nil {
		t.Fatal("the contract accepted a TERMINAL Level with a readable record and no guard; the " +
			"exemption is reading the outcome rather than the load that caused it")
	}
}

// R3's third half: the marker covers the outcome it caused, not any terminal.
//
// The reason comparison is the difference between "a broken marker excuses this
// Level" and "a broken marker excuses this Plan from having guards at all". A
// Level terminal for one cause standing under a marker broken for another is a
// Level nothing durable accounts for, and it has to be refused even though the
// Plan beside it is terminal for the marker's own reason.
func TestATerminalOutcomeUnderABrokenMarkerMustNameItsReason(t *testing.T) {
	request := gapStatusRequest(t, execution.GapTerminal, contract.ReasonStateCorrupt)
	result, err := newEvaluator(t).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if err := result.Validate(request); err != nil {
		t.Fatalf("the contract refused the unmutated round, so what follows would prove nothing: %v", err)
	}
	plan := result.Plans[0]
	if len(plan.LevelOutcomes) == 0 {
		t.Fatal("the fixture produced no Level outcome to re-reason")
	}
	for index := range plan.LevelOutcomes {
		if plan.LevelOutcomes[index].Outcome != execution.LevelOutcomeTerminal {
			t.Fatalf("Level %d = %q, want the marker to have made it TERMINAL",
				plan.LevelOutcomes[index].LevelID, plan.LevelOutcomes[index].Outcome)
		}
		plan.LevelOutcomes[index].ReasonCode = execution.ReasonCode(contract.ReasonRecordInvalid)
	}
	// The Plan keeps the marker's reason. Only the Levels now name a cause the
	// marker does not, which is the one thing under test.
	result.Plans[0] = plan
	if err := result.Validate(request); err == nil {
		t.Fatal("the contract accepted a TERMINAL Level naming RECORD_INVALID under a marker broken for " +
			"STATE_CORRUPT; the exemption is reading the marker's status without its reason")
	}
}

// Not reachable through the contract, and nailed one level down instead:
// widening the exemption past TERMINAL -- dropping the outcome-kind check while
// keeping the status and reason ones -- survives every case in this file. It is
// not a hole in the cases. The shape it would admit cannot be built through the
// real contract at all: the rule behind codeOutcomeInvalidSeriesNotTerminal
// already requires a DeterministicInvalid series' outcome to be TERMINAL and to
// carry the view's reason, so a non-TERMINAL outcome beside a broken record is
// refused there before this exemption is ever consulted.
//
// The check stays, because that upstream rule is a separate rule that a later
// change could relax, and the day it does this is the line that keeps a
// business UNKNOWN from passing unguarded. Since the contract cannot
// discriminate it, the two predicates are pinned directly in
// execution/result_contract_internal_test.go -- outcome kind, load status and
// reason, one cell each.

// A series whose State could not be read produces Level outcomes and no
// window summary, and says so: the round that handled it counts it, so
// levels plus the two reasons reconciles against the series the Slot handled.
//
// Without the count, a round where most series failed their State load
// reports the few that did not as though they were the whole object -- the
// same shape on the page as a small object with nothing wrong.
func TestASeriesWhoseStateCouldNotBeReadIsCountedByTheRoundThatHandledIt(t *testing.T) {
	plan := evaluateAndValidate(t, retryableStateRequest(t))
	coverage := plan.HistoryCoverage
	if coverage.Constrained == 0 {
		t.Fatalf("coverage = %+v, want the series counted as one this round could not evaluate", coverage)
	}
	if coverage.Levels != 0 || coverage.Short != 0 {
		t.Fatalf("coverage = %+v, want no window counts: nothing was evaluated to summarise", coverage)
	}
	if len(plan.LevelOutcomes) == 0 {
		t.Fatalf("the round produced no Level outcome for a series it counted: %+v", plan)
	}
}
