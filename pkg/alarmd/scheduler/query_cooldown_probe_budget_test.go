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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// budgetSeeingExecutor answers every execution available and keeps whether
// each ran marked to a normal round's budget.
type budgetSeeingExecutor struct {
	marked []bool
}

func (executor *budgetSeeingExecutor) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.marked = append(executor.marked, execution.NormalQueryBudget(ctx))
	return execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, nil
}

// An execution the pool lets run before its cooldown has run out - here a
// retry of the Slot already in flight, which deferUnavailableQuery does not
// hold back - decides the pool as much as the first one after the cooldown:
// its answer takes the Query Group out. It runs marked to a normal round's
// budget too. The same retry outside the pool is not marked.
func TestEveryExecutionThePoolLetsRunHasANormalRoundsBudget(t *testing.T) {
	for _, pooled := range []bool{true, false} {
		now := time.Unix(100, 0)
		store := newMemoryCooldownStore()
		source := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
		source.slot.EarliestQueryDeadlineUnixMilli = 150_000
		if pooled {
			store.records["query-group-1"] = QueryCooldownRecord{QueryGroup: "query-group-1", OwnerEpoch: 1,
				EnteredAt: now.Add(-time.Minute), Until: now.Add(time.Minute), Failures: 3,
				QueryRevision: source.slot.Contract.QueryRevision, ScheduleRevision: source.slot.Contract.ScheduleRevision,
				SegmentStart: source.slot.Contract.ScheduleSegmentStart}
		}
		executor := &budgetSeeingExecutor{}
		flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: source.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		runner.WithQueryCooldownStore(store)
		// A retry of this Slot is already in flight and due.
		runner.attempt = &recoveryAttempt{contract: source.slot.Contract, failures: 1, next: execution.OperationRetry, nextAt: now}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("pooled %t: RunOne = (attempted %t, %v), want the Slot run", pooled, attempted, err)
		}
		if len(executor.marked) != 1 || executor.marked[0] != pooled {
			t.Fatalf("pooled %t: executions marked %v, want [%t]", pooled, executor.marked, pooled)
		}
		if pooled && (!runner.queryCooldown.until.IsZero() || runner.cooldownMemory.lastProbe == nil) {
			t.Fatalf("the marked execution answered: pool %+v, probe %+v, want it left with the probe kept", runner.queryCooldown, runner.cooldownMemory.lastProbe)
		}
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
