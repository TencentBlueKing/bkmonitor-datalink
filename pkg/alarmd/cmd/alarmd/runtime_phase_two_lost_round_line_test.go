// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// A round let run as a normal one that then times out says on its own
// completion line what it was judged on and what window its read had: the
// divert decision the Runner carried and the binding query budget access
// recorded, here for a sixty-second plan. A round that succeeds writes
// neither.
func TestALostRoundExplainsItselfOnItsCompletionLine(t *testing.T) {
	decision := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: 1_700_000_023_000, FrozenDeadlineUnixMilli: 1_700_000_055_000,
		LiveDeadlineUnixMilli: 1_700_000_045_000, BoundMillis: 5_000, BoundSource: observability.DivertBoundFallback, RemainingMillis: 22_000}
	budget := observability.QueryBudgetFacts{ConsumerDeadlineUnixMilli: 1_700_000_060_000, FrozenReserveMillis: 5_000, LiveReserveMillis: 15_000,
		ReadyAtUnixMilli: 1_700_000_044_700, FrozenQueryDeadlineUnixMilli: 1_700_000_055_000, LiveQueryDeadlineUnixMilli: 1_700_000_045_000}
	request := execution.SlotExecutionRequest{AttemptNo: 1, Operation: execution.OperationNormal}
	request.Contract.Slot.EvaluationTime = execution.EvaluationTime(1_700_000_000)
	for _, tc := range []struct {
		name   string
		result execution.SlotExecutionResult
		lost   bool
	}{
		{"timed out", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout)}, true},
		{"succeeded", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull, Result: observability.ResultSuccess}, false},
		// Degraded because its Levels were outside their configured hours:
		// alarmd's own judgement, every query answered, nothing lost.
		{"out of hours", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonEffectiveTimeInactive),
			CompletionCause: execution.SlotCompletionCause{Cause: execution.CauseLevelOutcomeUnknown,
				Reason: execution.ReasonCode(contract.ReasonEffectiveTimeInactive)}}, false},
		// The same judgement beside a query that did answer short: the loss
		// is the round's, and its facts stay on the line.
		{"out of hours with a short query", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonEffectiveTimeInactive),
			CompletionCause: execution.SlotCompletionCause{Cause: execution.CauseLevelOutcomeUnknown,
				Reason: execution.ReasonCode(contract.ReasonEffectiveTimeInactive)}, PhysicalQueryLoss: true}, true},
		// The primary input timed out this round: a lost query.
		{"primary timed out", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout),
			CompletionCause: execution.SlotCompletionCause{Cause: execution.CausePrimaryInputUnavailable,
				Reason: execution.ReasonCode(contract.ReasonQueryTimeout)}}, true},
		// Degraded only because a guard an earlier timeout raised still
		// holds a Level: its queries answered, and it lost no query.
		{"carried timeout", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout),
			CompletionCause: execution.SlotCompletionCause{Cause: execution.CauseGapGuardWarming,
				Reason: execution.ReasonCode(contract.ReasonQueryTimeout), ReasonCarried: true,
				ReasonOrigin: execution.ReasonOrigin{Slot: 1_699_999_940, Cause: execution.ReasonCode(contract.ReasonQueryTimeout)}}}, false},
		// Two Plans: B's Level held by a carried guard, A's primary came
		// back PARTIAL. The carried Level outranks the partial primary in
		// the attribution, and a query was short all the same.
		{"carried cause over a partial primary", execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
			Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout),
			CompletionCause: execution.SlotCompletionCause{Cause: execution.CauseLevelOutcomeUnknown,
				Reason: execution.ReasonCode(contract.ReasonQueryTimeout), ReasonCarried: true,
				ReasonOrigin: execution.ReasonOrigin{Slot: 1_699_999_940, Cause: execution.ReasonCode(contract.ReasonQueryTimeout)}},
			PhysicalQueryLoss: true}, true},
	} {
		var completion *observability.Observation
		executor := observedProductionSlotExecutor{
			next: slotExecutorFunc(func(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				observability.BudgetCaptureFromContext(ctx).Record(budget)
				return tc.result, nil
			}),
			observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageSlotCompleted {
					completion = &o
				}
			}),
			decisionLag: func(time.Duration) {},
		}
		ctx := observability.ContextWithDivertDecision(context.Background(), decision)
		if _, err := executor.Execute(ctx, request); err != nil {
			t.Fatal(err)
		}
		if completion == nil {
			t.Fatalf("%s: no completion line", tc.name)
		}
		if completion.Result != tc.result.Result || completion.ReasonCode != observability.ReasonCode(tc.result.ReasonCode) {
			t.Fatalf("%s: line result %s/%s, want the Slot's own %s/%s", tc.name, completion.Result, completion.ReasonCode,
				tc.result.Result, tc.result.ReasonCode)
		}
		if !tc.lost {
			if completion.DivertDecision != nil || completion.QueryBudget != nil {
				t.Fatalf("%s: a round that lost no query wrote %+v %+v", tc.name, completion.DivertDecision, completion.QueryBudget)
			}
			continue
		}
		if completion.DivertDecision == nil || *completion.DivertDecision != decision || completion.QueryBudget == nil || *completion.QueryBudget != budget {
			t.Fatalf("%s: decision %+v budget %+v, want %+v %+v", tc.name, completion.DivertDecision, completion.QueryBudget, decision, budget)
		}
	}
}

// Every round that is lost or fails explains itself, not only one that timed
// out: a failed round and a failed attempt being retried carry the decision
// and the budget, with when the round was classified and when its read was
// sent. A round waiting for its data does not.
func TestEveryFailedRoundCarriesItsDecisionAndBudget(t *testing.T) {
	decision := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: 1_700_000_010_000, FrozenDeadlineUnixMilli: 1_700_000_055_000,
		LiveDeadlineUnixMilli: 1_700_000_055_000, BoundMillis: 300, BoundSource: observability.DivertBoundQueryDuration, RemainingMillis: 45_000}
	budget := observability.QueryBudgetFacts{ConsumerDeadlineUnixMilli: 1_700_000_060_000, FrozenReserveMillis: 5_000, ReadyAtUnixMilli: 1_700_000_010_000,
		FrozenQueryDeadlineUnixMilli: 1_700_000_055_000, LiveQueryDeadlineUnixMilli: 1_700_000_055_000, QueryDeadlineUnixMilli: 1_700_000_055_000}
	sentAt := time.UnixMilli(1_700_000_054_400)
	request := execution.SlotExecutionRequest{AttemptNo: 1, Operation: execution.OperationNormal}
	request.Contract.Slot.EvaluationTime = execution.EvaluationTime(1_700_000_000)
	for _, tc := range []struct {
		name    string
		retried bool
		err     error
		want    observability.Result
	}{
		{"failed", false, errors.New("alarmd access: execute physical query: context deadline exceeded"), observability.ResultFailed},
		{"retrying after a failure", true, errors.New("alarmd access: execute physical query: context deadline exceeded"), observability.ResultRetrying},
	} {
		var completion *observability.Observation
		executor := observedProductionSlotExecutor{
			next: slotExecutorFunc(func(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				capture := observability.BudgetCaptureFromContext(ctx)
				capture.Record(budget)
				capture.MarkStarted(sentAt)
				return execution.SlotExecutionResult{}, tc.err
			}),
			observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageSlotCompleted {
					completion = &o
				}
			}),
			decisionLag: func(time.Duration) {},
		}
		ctx := observability.ContextWithDivertDecision(context.Background(), decision)
		if tc.retried {
			ctx = scheduler.WithFailureRetried(ctx)
		}
		_, _ = executor.Execute(ctx, request)
		if completion == nil || completion.Result != tc.want {
			t.Fatalf("%s: completion %+v, want result %s", tc.name, completion, tc.want)
		}
		if completion.DivertDecision == nil || completion.DivertDecision.ClassifiedAtUnixMilli != decision.ClassifiedAtUnixMilli ||
			completion.QueryBudget == nil || completion.QueryBudget.QueryStartedAtUnixMilli != sentAt.UnixMilli() {
			t.Fatalf("%s: decision %+v budget %+v, want the classification and the send time on the line", tc.name, completion.DivertDecision, completion.QueryBudget)
		}
	}
}

// A replay is not judged by the divert, and when it times out its line says
// so: bound source none, when it was taken up, and the time it had against
// the recovery deadline access derived, which the budget carries with the
// recovery budget.
func TestAReplaysLostRoundCarriesItsRecoveryBudget(t *testing.T) {
	taken := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: 1_700_000_100_000, FrozenDeadlineUnixMilli: 1_700_000_055_000,
		BoundSource: observability.DivertBoundNone}
	budget := observability.QueryBudgetFacts{ConsumerDeadlineUnixMilli: 1_700_000_060_000, FrozenReserveMillis: 5_000, ReadyAtUnixMilli: 1_700_000_010_000,
		FrozenQueryDeadlineUnixMilli: 1_700_000_055_000, LiveQueryDeadlineUnixMilli: 1_700_000_055_000,
		QueryDeadlineUnixMilli: 1_700_000_155_000, RecoveryBudgetMillis: 55_000}
	request := execution.SlotExecutionRequest{AttemptNo: 1, Operation: execution.OperationReplay}
	request.Contract.Slot.EvaluationTime = execution.EvaluationTime(1_700_000_000)
	var completion *observability.Observation
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			capture := observability.BudgetCaptureFromContext(ctx)
			capture.Record(budget)
			capture.MarkStarted(time.UnixMilli(1_700_000_101_000))
			return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionUnavailable,
				Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(contract.ReasonQueryTimeout)}, nil
		}),
		observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
			if o.Stage == observability.StageSlotCompleted {
				completion = &o
			}
		}),
		decisionLag: func(time.Duration) {},
	}
	if _, err := executor.Execute(observability.ContextWithDivertDecision(context.Background(), taken), request); err != nil {
		t.Fatal(err)
	}
	if completion == nil || completion.DivertDecision == nil || completion.DivertDecision.BoundSource != observability.DivertBoundNone ||
		completion.DivertDecision.RemainingMillis != 55_000 || completion.QueryBudget == nil ||
		completion.QueryBudget.QueryDeadlineUnixMilli != 1_700_000_155_000 || completion.QueryBudget.RecoveryBudgetMillis != 55_000 ||
		completion.QueryBudget.QueryStartedAtUnixMilli != 1_700_000_101_000 {
		t.Fatalf("replay line %+v / %+v, want bound none, 55 s against the recovery deadline, and the recovery budget", completion.DivertDecision, completion.QueryBudget)
	}
}
