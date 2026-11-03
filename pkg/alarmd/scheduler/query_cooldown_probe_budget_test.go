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

// operationSeeingExecutor answers every execution with availability and
// keeps the operation each ran as.
type operationSeeingExecutor struct {
	availability execution.QueryAvailability
	operations   []execution.Operation
}

func (executor *operationSeeingExecutor) Execute(_ context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.operations = append(executor.operations, request.Operation)
	result := execution.SlotExecutionResult{Completed: true, QueryAvailability: executor.availability}
	if executor.availability == execution.QueryAvailabilityUnavailable {
		result.QueryUnavailableReason = execution.ReasonCode(contract.ReasonQueryTimeout)
	}
	return result, nil
}

// Only a normal execution decides the pool. A retry of the Slot in flight,
// which the pool lets run before its cooldown has run out, runs, and the
// Query Group stays in as it was whether the retry answered or timed out:
// the retry ran when its backoff said, not at the Slot's own second with the
// Query Groups its normal rounds queue behind, so it neither takes the Query
// Group out nor counts a failure or extends the cooldown. With the cooldown
// run out, the Slot runs as a normal execution and its answer takes the
// Query Group out, with the probe kept.
func TestOnlyANormalExecutionDecidesThePool(t *testing.T) {
	for _, test := range []struct {
		name         string
		operation    execution.Operation
		availability execution.QueryAvailability
		decides      bool
	}{
		{"a retry in flight that answers", execution.OperationRetry, execution.QueryAvailabilityAvailable, false},
		{"a retry in flight that times out", execution.OperationRetry, execution.QueryAvailabilityUnavailable, false},
		{"the cooldown run out", execution.OperationNormal, execution.QueryAvailabilityAvailable, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := newMemoryCooldownStore()
			source := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
			source.slot.EarliestQueryDeadlineUnixMilli = 150_000
			until := now.Add(time.Minute)
			if test.decides {
				until = now.Add(-time.Second)
			}
			store.records["query-group-1"] = QueryCooldownRecord{QueryGroup: "query-group-1", OwnerEpoch: 1,
				EnteredAt: now.Add(-time.Minute), Until: until, Failures: 3,
				QueryRevision: source.slot.Contract.QueryRevision, ScheduleRevision: source.slot.Contract.ScheduleRevision,
				SegmentStart: source.slot.Contract.ScheduleSegmentStart}
			executor := &operationSeeingExecutor{availability: test.availability}
			flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: source.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			runner.WithQueryCooldownStore(store)
			if !test.decides {
				runner.attempt = &recoveryAttempt{contract: source.slot.Contract, failures: 1, next: execution.OperationRetry, nextAt: now}
			}
			if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
				t.Fatalf("RunOne = (attempted %t, %v), want the Slot run", attempted, err)
			}
			if len(executor.operations) != 1 || executor.operations[0] != test.operation {
				t.Fatalf("operations %v, want [%s]", executor.operations, test.operation)
			}
			if !test.decides && (!runner.queryCooldown.until.Equal(until) || runner.queryCooldown.failures != 3) {
				t.Fatalf("after a retry %v: pool %+v, want it left as it was (until %s, 3 failures)", test.availability, runner.queryCooldown, until)
			}
			left := runner.queryCooldown.until.IsZero()
			probe := runner.cooldownMemory.lastProbe
			if left != test.decides || (probe != nil) != test.decides ||
				(probe != nil && (probe.Operation != string(execution.OperationNormal) || probe.Outcome != observability.QueryCooldownProbeAnswered)) {
				t.Fatalf("after %v from a %s: pool %+v, probe %+v; want left %t with a normal probe only if it decided",
					test.availability, test.operation, runner.queryCooldown, probe, test.decides)
			}
		})
	}
}

// The probe the pool kept is part of the Query Group's place in it: a new
// process or owner reads it back with the rest, and its next transition
// says it, so "what did the last probe have and use" survives a restart.
func TestTheLastProbeIsRestoredWithThePool(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	probe := &observability.QueryCooldownProbe{At: now.Add(-time.Minute), Operation: "replay",
		Outcome: observability.QueryCooldownProbeUnavailable, Measured: true, BudgetMillis: 25_000, ElapsedMillis: 25_000}
	store.records["qg"] = QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 1, EnteredAt: now.Add(-time.Hour),
		Until: now.Add(time.Minute), Failures: 4, LastProbe: probe}
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(2))
	if len(lines) != 1 || lines[0].LastProbe == nil || *lines[0].LastProbe != *probe {
		t.Fatalf("restored lines %+v, want the record's probe on the restored line", lines)
	}
}

// Rounds the pool did not let run - a Query Group outside it, failing its
// way in - leave no probe: the probe names an execution whose answer decided
// the pool, and the entry line has none yet.
func TestRoundsOutsideThePoolLeaveNoProbe(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failInto(runner, &slot)
	if len(lines) != 1 || lines[0].Event != QueryCooldownEntered || lines[0].LastProbe != nil {
		t.Fatalf("entry lines %+v, want one entered line with no probe", lines)
	}
}

// A deciding round is excused only for the Runner's own lateness, only on a
// timeout, and once for each cooldown's end. Its query was ready at 100 s; a
// Runner whose previous execution came back at 105 s started it late on its
// own account, one whose previous execution came back at 95 s did not (it
// waited for a permit or for dispatch, as every round does). An answer, or a
// failure that is not a timeout, is never excused.
func TestOnlyATimeoutLateOnTheRunnersOwnAccountIsExcusedOncePerCooldownEnd(t *testing.T) {
	timedOut := func(reason string) execution.SlotExecutionResult {
		return execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnavailable,
			QueryUnavailableReason: execution.ReasonCode(reason),
			PrimaryQueryClock:      &execution.PhysicalQueryClock{BudgetMillis: 20_000, ReadyAtUnixMilli: 100_000}}
	}
	answered := timedOut(contract.ReasonQueryTimeout)
	answered.QueryAvailability, answered.QueryUnavailableReason = execution.QueryAvailabilityAvailable, ""
	busy, idle := time.UnixMilli(105_000), time.UnixMilli(95_000)
	for _, test := range []struct {
		name    string
		result  execution.SlotExecutionResult
		busy    time.Time
		excused bool
	}{
		{"a timeout behind its own execution", timedOut(contract.ReasonQueryTimeout), busy, true},
		{"a timeout behind a permit", timedOut(contract.ReasonQueryTimeout), idle, false},
		{"an answer behind its own execution", answered, busy, false},
		{"another failure behind its own execution", timedOut(contract.ReasonQueryUnavailable), busy, false},
		{"a timeout of the Runner's first execution", timedOut(contract.ReasonQueryTimeout), time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &Runner{queryCooldown: queryCooldownState{until: time.Unix(90, 0), failures: 3}}
			if got := runner.queryCooldownExcusesItsOwnDelay(test.result, test.busy); got != test.excused {
				t.Fatalf("excused %t, want %t", got, test.excused)
			}
			if test.excused && runner.queryCooldownExcusesItsOwnDelay(test.result, test.busy) {
				t.Fatal("a second round was excused for the same cooldown's end")
			}
			if test.excused {
				runner.queryCooldown.until = time.Unix(150, 0)
				if !runner.queryCooldownExcusesItsOwnDelay(test.result, test.busy) {
					t.Fatal("the next cooldown's end excused nothing")
				}
			}
		})
	}
	outside := &Runner{}
	if outside.queryCooldownExcusesItsOwnDelay(timedOut(contract.ReasonQueryTimeout), busy) {
		t.Fatal("a round outside the pool was excused")
	}
}
