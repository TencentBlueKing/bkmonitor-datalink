// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The Slot's one consumer: its frozen query deadline is divertDeadline, five
// seconds of downstream reserve before its consumer deadline.
const liveFrozenReserveMillis = 5_000

func liveConsumers() []execution.DataRequirementConsumer {
	return []execution.DataRequirementConsumer{{
		ConsumerDeadlineUnixMilli: divertDeadline + liveFrozenReserveMillis, DownstreamExecutionReserveMilliSec: liveFrozenReserveMillis,
	}}
}

// liveSource is a held Query Group whose runtime has measured a pooled
// post-query tail of reserve; zero publishes none.
func liveSource(t *testing.T, reserve time.Duration) (*ProductionSlotSource, *takeoverObserver) {
	t.Helper()
	source, observer := heldSource(t, 5*time.Second)
	live := &execution.LiveReserve{}
	live.Set(reserve)
	if err := WithLiveReserve(live)(source); err != nil {
		t.Fatal(err)
	}
	return source, observer
}

func classifyLiveAt(t *testing.T, source *ProductionSlotSource, at time.Time) execution.Operation {
	t.Helper()
	operation, facts, err := source.classifyRecoveryOf(context.Background(), divertSlot, divertDeadline, liveConsumers(), 0, at, testFence(7))
	if err != nil {
		t.Fatalf("classifyRecoveryOf() error = %v", err)
	}
	if facts.validate(operation) != nil {
		t.Fatalf("%s with facts %+v", operation, facts)
	}
	return operation
}

// A runtime whose measured tail is longer than the frozen reserve cuts the
// read earlier than the frozen deadline. Twelve seconds before the frozen
// deadline is two before the live one: short of the five a read is given,
// so the round goes to replay, though by the frozen deadline it had time.
func TestALongerLiveReserveDivertsByTheDeadlineTheReadIsCutAt(t *testing.T) {
	source, observer := liveSource(t, 15*time.Second)
	if operation := classifyLiveAt(t, source, time.UnixMilli(divertDeadline-12_000)); operation != execution.OperationReplay {
		t.Fatalf("two seconds before the live deadline: %s, want replay", operation)
	}
	if len(observer.diversions) != 1 || observer.diversions[0].Reason != observability.SlotDiversionReadinessLate ||
		observer.diversions[0].RemainingSeconds != 2 {
		t.Fatalf("diversions %+v, want one readiness_late with the two seconds the read would have had", observer.diversions)
	}
}

// A measured tail shorter than the frozen reserve leaves the read more time:
// four seconds before the frozen deadline is seven before the live one, and
// the round runs, though the frozen deadline alone would have sent it away.
func TestAShorterLiveReserveLeavesTheRoundRunning(t *testing.T) {
	source, observer := liveSource(t, 2*time.Second)
	if operation := classifyLiveAt(t, source, time.UnixMilli(divertDeadline-4_000)); operation != execution.OperationNormal || len(observer.diversions) != 0 {
		t.Fatalf("seven seconds before the live deadline: %s, diversions %+v, want a normal round", operation, observer.diversions)
	}
}

// Before a tail is measured the frozen reserve applies, to the millisecond.
func TestWithoutAMeasuredTailTheFrozenDeadlineDecides(t *testing.T) {
	source, _ := liveSource(t, 0)
	if operation := classifyLiveAt(t, source, time.UnixMilli(divertDeadline-5_000)); operation != execution.OperationNormal {
		t.Fatalf("at frozen deadline - bound: %s, want normal", operation)
	}
	if operation := classifyLiveAt(t, source, time.UnixMilli(divertDeadline-5_000+1)); operation != execution.OperationReplay {
		t.Fatalf("a millisecond later: %s, want replay", operation)
	}
}

// A measured tail so long the live deadline would fall before the read can
// start is not used for this round - access cuts it at the frozen deadline
// then - and the round is judged by the frozen one. Twenty seconds after
// evaluation the live deadline (fifteen) has passed, the frozen one has
// thirty-five seconds to go, and the round runs.
func TestALiveDeadlineBeforeTheStartFallsBackToTheFrozenOne(t *testing.T) {
	source, observer := liveSource(t, 45*time.Second)
	if operation := classifyLiveAt(t, source, time.UnixMilli(divertDeadline-35_000)); operation != execution.OperationNormal || len(observer.diversions) != 0 {
		t.Fatalf("thirty-five seconds before the frozen deadline: %s, diversions %+v, want a normal round", operation, observer.diversions)
	}
}

// The Slot's consumers are every consumer of its requirements.
func TestASlotsConsumersAreAllItsRequirementsConsumers(t *testing.T) {
	first, second := liveConsumers()[0], liveConsumers()[0]
	second.ConsumerDeadlineUnixMilli -= 1_000
	fact := execution.FrozenSlotContractFact{Requirements: []execution.DataRequirement{
		{Consumers: []execution.DataRequirementConsumer{first}}, {Consumers: []execution.DataRequirementConsumer{second}},
	}}
	if got := slotConsumers(fact); len(got) != 2 || got[1].ConsumerDeadlineUnixMilli != second.ConsumerDeadlineUnixMilli {
		t.Fatalf("consumers %+v", got)
	}
}

// A live round let run as a normal one keeps what it was judged on: when,
// both deadlines, the bound and where it came from, and what was left by the
// deadline judged against.
func TestARoundLetRunKeepsWhatItWasJudgedOn(t *testing.T) {
	source, _ := liveSource(t, 15*time.Second)
	at := time.UnixMilli(divertDeadline - 20_000)
	operation, facts, err := source.classifyRecoveryOf(context.Background(), divertSlot, divertDeadline, liveConsumers(), 0, at, testFence(7))
	if err != nil || operation != execution.OperationNormal {
		t.Fatalf("classifyRecoveryOf() = %s, %v", operation, err)
	}
	want := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: at.UnixMilli(), FrozenDeadlineUnixMilli: divertDeadline,
		LiveDeadlineUnixMilli: divertDeadline - 10_000, BoundMillis: 5_000, BoundSource: observability.DivertBoundFallback, RemainingMillis: 10_000}
	if facts.Decision == nil || *facts.Decision != want {
		t.Fatalf("decision %+v, want %+v", facts.Decision, want)
	}
	if err := facts.validate(operation); err != nil {
		t.Fatal(err)
	}
}

// decisionExecutor keeps the divert decision its context carried.
type decisionExecutor struct {
	heard *observability.DivertDecisionFacts
}

func (executor *decisionExecutor) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.heard = observability.DivertDecisionFromContext(ctx)
	return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull, Result: observability.ResultSuccess}, nil
}

// The Runner hands the decision the source made to the executor, which names
// it on the completion of a round that times out.
func TestTheRunnerHandsTheDecisionToTheExecutor(t *testing.T) {
	decision := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: 1, FrozenDeadlineUnixMilli: 2, LiveDeadlineUnixMilli: 3,
		BoundMillis: 4, BoundSource: observability.DivertBoundQueryDuration, RemainingMillis: 5}
	slot := frozenSlot("query-group-1")
	slot.Recovery = SlotRecoveryFacts{Disposition: ReplayLive, RecheckAtUnixMilli: slot.EarliestQueryDeadlineUnixMilli, Decision: &decision}
	executor := &decisionExecutor{}
	now := time.UnixMilli(slot.EarliestQueryDeadlineUnixMilli - 30_000)
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
		&fakeSlotSource{slot: slot, facts: SlotDueFacts{IntervalSeconds: 60}}, executor, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("RunOne() = attempted %t, %v", attempted, err)
	}
	if executor.heard == nil || *executor.heard != decision {
		t.Fatalf("executor heard %+v, want %+v", executor.heard, decision)
	}
}

// A replay the divert did not judge still hands the executor when it was
// taken up and its frozen deadline, with bound source none: its lost line
// measures what was left against the recovery deadline access derives. A
// normal round the divert did not judge hands nothing.
func TestAnUnjudgedReplayHandsTheExecutorWhenItWasTakenUp(t *testing.T) {
	for _, operation := range []execution.Operation{execution.OperationReplay, execution.OperationNormal} {
		slot := frozenSlot("query-group-1")
		slot.Dispatch.Operation = operation
		executor := &decisionExecutor{}
		now := time.UnixMilli(slot.EarliestQueryDeadlineUnixMilli - 30_000)
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
			&fakeSlotSource{slot: slot, facts: SlotDueFacts{IntervalSeconds: 60}}, executor, NewFlightCoordinator(), func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("%s: RunOne() = attempted %t, %v", operation, attempted, err)
		}
		if operation == execution.OperationNormal {
			if executor.heard != nil {
				t.Fatalf("an unjudged normal round handed %+v", executor.heard)
			}
			continue
		}
		want := observability.DivertDecisionFacts{ClassifiedAtUnixMilli: now.UnixMilli(),
			FrozenDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli, BoundSource: observability.DivertBoundNone}
		if executor.heard == nil || *executor.heard != want {
			t.Fatalf("replay handed %+v, want %+v", executor.heard, want)
		}
	}
}
