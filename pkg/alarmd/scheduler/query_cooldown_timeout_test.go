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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A query that times out on the backend Slot after Slot enters the pool on
// the third, like any other unavailable query, and its pool entry says how
// it got there: how many of the failures that put it there were timeouts
// and when the first of them was, so a reader sees "in the pool since T
// after N timeouts" rather than a bare failure count.

func timeoutResult() execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnavailable,
		QueryUnavailableReason: "QUERY_TIMEOUT"}
}

// failOnce records one more failed Slot of slot's Query Group, a minute
// after the previous one.
func failOnce(runner *Runner, slot *FrozenSlot, now *time.Time, result execution.SlotExecutionResult) {
	*now = now.Add(time.Minute)
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), *slot, execution.OperationNormal, result, 60, runner.queryCooldownLetsRun())
}

func TestTwoTimeoutsDoNotPoolAndTheThirdDoes(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(10_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()

	failOnce(runner, &slot, &now, timeoutResult())
	first := now
	failOnce(runner, &slot, &now, timeoutResult())
	if !runner.queryCooldown.until.IsZero() || len(lines) != 0 || store.saves != 0 {
		t.Fatalf("after 2 timeouts: until=%s lines=%+v saves=%d, want not in the pool", runner.queryCooldown.until, lines, store.saves)
	}
	failOnce(runner, &slot, &now, timeoutResult())
	if runner.queryCooldown.until.IsZero() {
		t.Fatal("after 3 timeouts the Query Group is not in the pool")
	}
	record := store.records["qg"]
	if record.Reason != "QUERY_TIMEOUT" || record.Failures != 3 || record.Timeouts != 3 ||
		!record.FirstTimeoutAt.Equal(first) || !record.EnteredAt.Equal(now) {
		t.Fatalf("record = %+v, want reason QUERY_TIMEOUT, 3 failures, 3 timeouts, the first at %s, entered at %s", record, first, now)
	}
	if len(lines) != 1 || lines[0].Event != QueryCooldownEntered || lines[0].Timeouts != 3 ||
		!lines[0].FirstTimeoutAt.Equal(first) || !lines[0].EnteredAt.Equal(now) {
		t.Fatalf("lines = %+v, want one entered line with 3 timeouts, the first at %s", lines, first)
	}
}

// The count is of timeouts, not of failures: a run that began with another
// failure enters on its third failure and says two of them were timeouts,
// the first of those at its own time.
func TestThePoolEntryCountsTheTimeoutsAmongItsFailures(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(10_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()

	failOnce(runner, &slot, &now, unavailableResult())
	failOnce(runner, &slot, &now, timeoutResult())
	firstTimeout := now
	failOnce(runner, &slot, &now, timeoutResult())
	record := store.records["qg"]
	if runner.queryCooldown.until.IsZero() || record.Failures != 3 || record.Timeouts != 2 || !record.FirstTimeoutAt.Equal(firstTimeout) {
		t.Fatalf("record = %+v, want in the pool on 3 failures, 2 of them timeouts, the first at %s", record, firstTimeout)
	}
}

// A new owner restores the timeouts with the rest of the entry, and a query
// that answers takes them away with it: the next timeout starts a new count.
func TestTheTimeoutsAreRestoredWithTheEntryAndClearedWithIt(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(10_000, 0)
	var lines []observability.QueryCooldownFacts
	before := poolRunner(store, &now, &lines)
	before.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failOnce(before, &slot, &now, timeoutResult())
	first := now
	failOnce(before, &slot, &now, timeoutResult())
	failOnce(before, &slot, &now, timeoutResult())

	var restored []observability.QueryCooldownFacts
	after := poolRunner(store, &now, &restored)
	after.restoreQueryCooldown(context.Background(), fenceAt(2))
	if after.queryCooldown.timeouts != 3 || !after.queryCooldown.firstTimeoutAt.Equal(first) {
		t.Fatalf("restored state = %+v, want 3 timeouts, the first at %s", after.queryCooldown, first)
	}
	if len(restored) != 1 || restored[0].Event != QueryCooldownRestored || restored[0].Timeouts != 3 || !restored[0].FirstTimeoutAt.Equal(first) {
		t.Fatalf("restored lines = %+v, want the restored line to carry the timeouts", restored)
	}

	now = after.queryCooldown.until
	slot.Contract.Slot.EvaluationTime++
	after.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 60, after.queryCooldownLetsRun())
	if record := store.records["qg"]; !record.Until.IsZero() || record.Timeouts != 0 || !record.FirstTimeoutAt.IsZero() {
		t.Fatalf("record after a query answered = %+v, want out of the pool with no timeouts", record)
	}
	failOnce(after, &slot, &now, timeoutResult())
	if after.queryCooldown.timeouts != 1 || !after.queryCooldown.firstTimeoutAt.Equal(now) {
		t.Fatalf("state = %+v, want a new count starting at the timeout after recovery", after.queryCooldown)
	}
}

// outcomeExecutor answers each Slot with the next scripted outcome, a result
// or an error.
type outcomeExecutor struct {
	outcomes []struct {
		result execution.SlotExecutionResult
		err    error
	}
}

func (executor *outcomeExecutor) Execute(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	if len(executor.outcomes) == 0 {
		return execution.SlotExecutionResult{}, errors.New("outcome executor exhausted")
	}
	next := executor.outcomes[0]
	executor.outcomes = executor.outcomes[1:]
	return next.result, next.err
}

// A query that never got a permit before its deadline was never sent, and
// says nothing about the backend: the Slot's execution ends in the access
// layer's permit error, and the pool does not count it either way. Two
// timeouts with a permit expiry between them are still two; the next
// timeout is the third.
func TestAPermitWaitThatRanOutNeverCountsTowardThePool(t *testing.T) {
	now := time.Unix(100, 0)
	source := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 60}}
	source.slot.EarliestQueryDeadlineUnixMilli = 150_000
	permitExpired := fmt.Errorf("alarmd access: acquire physical query permit: %w", context.DeadlineExceeded)
	executor := &outcomeExecutor{}
	executor.outcomes = append(executor.outcomes, struct {
		result execution.SlotExecutionResult
		err    error
	}{err: permitExpired})
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: source.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	slot := source.slot
	failOnce(runner, &slot, &now, timeoutResult())
	failOnce(runner, &slot, &now, timeoutResult())

	source.slot.Contract.Slot.EvaluationTime = slot.Contract.Slot.EvaluationTime + 1
	source.slot.ExpectedNextSlot = source.slot.Contract.Slot.EvaluationTime
	source.slot.Contract.DuePlanSetDigest = "permit-expired-slot"
	source.slot.DuePlanTargets.DuePlanSetDigest = source.slot.Contract.DuePlanSetDigest
	if _, attempted, runErr := runner.RunOne(context.Background()); !attempted || !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("RunOne() attempted=%t error=%v, want the permit error from the execution", attempted, runErr)
	}
	if runner.queryCooldown.failures != 2 || runner.queryCooldown.timeouts != 2 || !runner.queryCooldown.until.IsZero() {
		t.Fatalf("state after a permit expiry = %+v, want still 2 timeouts and not in the pool", runner.queryCooldown)
	}
	slot.Contract.Slot.EvaluationTime = source.slot.Contract.Slot.EvaluationTime
	failOnce(runner, &slot, &now, timeoutResult())
	if runner.queryCooldown.until.IsZero() || runner.queryCooldown.timeouts != 3 {
		t.Fatalf("state = %+v, want in the pool on the third timeout", runner.queryCooldown)
	}
}
