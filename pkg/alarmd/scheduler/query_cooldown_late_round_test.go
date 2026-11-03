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
)

// lateTimeoutExecutor answers every execution as a primary query sent late
// that timed out: it proved nothing to the worker, which says why.
type lateTimeoutExecutor struct{ readyAt time.Time }

func (executor *lateTimeoutExecutor) Execute(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	return execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnknown,
		QueryUnknownReason: execution.ReasonCode(contract.ReasonQueryTimeout),
		PrimaryQueryClock:  &execution.PhysicalQueryClock{BudgetMillis: 13_000, ElapsedMillis: 13_000, ReadyAtUnixMilli: executor.readyAt.UnixMilli()}}, nil
}

// lateRoundPool is a Query Group in the pool whose cooldown has run out, run
// through the Runner as production runs it, its every round sent late.
type lateRoundPool struct {
	t        *testing.T
	runner   *Runner
	source   *fakeSlotSource
	executor *lateTimeoutExecutor
	now      time.Time
}

func newLateRoundPool(t *testing.T) *lateRoundPool {
	t.Helper()
	pool := &lateRoundPool{t: t, now: time.Unix(100, 0)}
	store := newMemoryCooldownStore()
	pool.source = &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 60}}
	pool.source.slot.EarliestQueryDeadlineUnixMilli = 150_000
	store.records["query-group-1"] = QueryCooldownRecord{QueryGroup: "query-group-1", OwnerEpoch: 1,
		EnteredAt: pool.now.Add(-10 * time.Minute), Until: pool.now.Add(-time.Second), Failures: 3,
		QueryRevision: pool.source.slot.Contract.QueryRevision, ScheduleRevision: pool.source.slot.Contract.ScheduleRevision,
		SegmentStart: pool.source.slot.Contract.ScheduleSegmentStart}
	pool.executor = &lateTimeoutExecutor{}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), func() time.Time { return pool.now })
	if err != nil {
		t.Fatal(err)
	}
	pool.runner, err = NewRunner("query-group-1", &fakeSession{fence: pool.source.slot.Dispatch.OwnerFence}, pool.source, pool.executor, flights,
		func() time.Time { return pool.now })
	if err != nil {
		t.Fatal(err)
	}
	pool.runner.WithQueryCooldownStore(store)
	return pool
}

// round runs the Slot, its query ready a second ago, the Runner's previous
// execution having come back at busyUntil.
func (pool *lateRoundPool) round(busyUntil time.Time) {
	pool.t.Helper()
	pool.executor.readyAt = pool.now.Add(-time.Second)
	pool.runner.lastExecutionReturnedAt = busyUntil
	if _, attempted, err := pool.runner.RunOne(context.Background()); err != nil || !attempted {
		pool.t.Fatalf("RunOne = (attempted %t, %v), want the Slot run", attempted, err)
	}
}

// A deciding round sent late because it queued for its permit, with nothing
// of the Runner's own ahead of it, is decided late as it is: its timeout
// counts, the cooldown grows. Nothing excuses it.
func TestADecidingRoundLateForItsPermitIsDecided(t *testing.T) {
	pool := newLateRoundPool(t)
	pool.round(pool.now.Add(-time.Minute))
	if state := pool.runner.queryCooldown; state.failures != 4 || !state.until.After(pool.now) {
		t.Fatalf("after a round late for its permit: %+v, want the failure counted and the cooldown extended", state)
	}
}

// A deciding round sent late behind the Runner's own previous execution is
// excused once for the cooldown's end and decides nothing; the next such
// round is decided, so a Runner kept busy by its own replays is not let off
// round after round.
func TestARoundLateBehindItselfIsExcusedOnceForEachCooldownsEnd(t *testing.T) {
	pool := newLateRoundPool(t)
	until := pool.now.Add(-time.Second) // the record's, restored on the first round
	pool.round(pool.now)
	if state := pool.runner.queryCooldown; state.failures != 3 || !state.until.Equal(until) {
		t.Fatalf("the first round late behind its own replay: %+v, want it excused, the pool as it was", state)
	}
	pool.round(pool.now)
	if state := pool.runner.queryCooldown; state.failures != 4 || !state.until.After(pool.now) {
		t.Fatalf("the second round late behind itself: %+v, want it decided, the failure counted", state)
	}
}
