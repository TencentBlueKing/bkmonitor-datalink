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
)

// firstRoundRead is when access reads this fixture's Slot, by the call access
// itself makes, in milliseconds: the fixture's offset is its interval and its
// reserve the five seconds the source is built with.
func firstRoundRead(interval int64, slot execution.EvaluationTime) int64 {
	budget := time.Duration(interval)*time.Second - 5*time.Second
	return int64(slot)*1000 + execution.ReadWaitWithinQueryBudget("query-group-1", budget, 30*time.Second).Milliseconds()
}

func firstRoundNext(t *testing.T, interval int64, load execution.ProgressLoadResult, at time.Time) (bool, SlotDueFacts, *fakeSlotCatalog) {
	t.Helper()
	schedule := schedulerSchedule(t, interval, execution.EvaluationTime(interval*1000), nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, load, at)
	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil {
		t.Fatalf("Next() at %s error = %v", at, err)
	}
	return due, facts, catalog
}

// A Slot of a minute or more has its first round at its read: before it, nothing
// is frozen and nothing is asked of the store beyond the cursor. The bound is the
// second the source first answers due, so the due index and the source never
// disagree about it.
func TestASlotOfAMinuteOrMoreHasItsFirstRoundAtItsRead(t *testing.T) {
	for _, interval := range []int64{60, 300, 3600} {
		slot := execution.EvaluationTime(interval * 1000)
		read := firstRoundRead(interval, slot)
		bound := (read + 999) / 1000
		if bound <= int64(slot) {
			t.Fatalf("interval %d: read at %d is no later than the Slot, so this cannot tell the two apart", interval, read)
		}

		// At the grid point, and in the last instant of the second before.
		for _, at := range []time.Time{time.Unix(int64(slot), 0), time.UnixMilli(bound*1000 - 1)} {
			due, facts, catalog := firstRoundNext(t, interval, missingProgress(), at)
			if due {
				t.Fatalf("interval %d at %s: due", interval, at)
			}
			if facts.NotDueUntilUnix != bound || facts.NextSlotUnix != int64(slot) || facts.IntervalSeconds != interval {
				t.Fatalf("interval %d at %s: facts = %+v, want the bound %d and the Slot %d", interval, at, facts, bound, slot)
			}
			if len(catalog.requests) != 0 {
				t.Fatalf("interval %d at %s: froze %d Slots before the read", interval, at, len(catalog.requests))
			}
		}

		due, _, catalog := firstRoundNext(t, interval, missingProgress(), time.Unix(bound, 0))
		if !due || len(catalog.requests) != 1 {
			t.Fatalf("interval %d at the bound: due=%t froze %d, want the Slot frozen once", interval, due, len(catalog.requests))
		}
	}
}

// The source judges by the second, as the due index does. Judging by the
// millisecond, a dispatch between the read and the next whole second would be due
// to the source and not to the index, and read as an overshoot of the index.
func TestTheFirstRoundIsJudgedByTheSecondTheIndexIs(t *testing.T) {
	const interval = 60
	slot := execution.EvaluationTime(interval * 1000)
	read := firstRoundRead(interval, slot)
	if read%1000 == 0 {
		t.Fatalf("the read at %d falls on a second, so this cannot tell the two apart", read)
	}
	due, facts, _ := firstRoundNext(t, interval, missingProgress(), time.UnixMilli(read))
	if due || facts.NotDueUntilUnix != (read+999)/1000 {
		t.Fatalf("at the read itself: due=%t facts=%+v, want not due until the next second", due, facts)
	}
}

// A period under a minute reads close to its deadline, or past it, and cannot
// spare the seconds a round waiting for the tick loses. It keeps its grid point.
// Named by the interval and not the short-period cohorts, which only list some.
func TestAPeriodUnderAMinuteHasItsFirstRoundAtItsGridPoint(t *testing.T) {
	for _, interval := range []int64{59, 30, 20, 15, 12, 10} {
		slot := execution.EvaluationTime(interval * 1000)
		due, facts, _ := firstRoundNext(t, interval, missingProgress(), time.Unix(int64(slot)-1, 0))
		if due || facts.NotDueUntilUnix != int64(slot) || facts.NextSlotUnix != int64(slot) {
			t.Fatalf("interval %d before its grid point: due=%t facts=%+v", interval, due, facts)
		}
		due, _, catalog := firstRoundNext(t, interval, missingProgress(), time.Unix(int64(slot), 0))
		if !due || len(catalog.requests) != 1 {
			t.Fatalf("interval %d at its grid point: due=%t froze %d", interval, due, len(catalog.requests))
		}
	}
}

// A Slot already begun is a recovery: its round does not wait for a read that the
// projection already has a contract for.
func TestASlotAlreadyBegunIsNotHeldForItsRead(t *testing.T) {
	const interval = 60
	slot := execution.EvaluationTime(interval * 1000)
	schedule := schedulerSchedule(t, interval, slot, nil, "snapshot-1", 1)
	frozenCatalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	frozen, due, _, err := newProductionSlotSourceForTest(t, frozenCatalog, missingProgress(), time.Unix(int64(slot)+40, 0)).
		Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("freezing the Slot: due=%t error=%v", due, err)
	}
	load := foundProgress(slot, 0)
	load.Progress.UnfinishedSlot = &execution.UnfinishedSlotProjection{
		Contract: frozen.Contract, DuePlanTargets: frozen.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: frozen.EarliestQueryDeadlineUnixMilli, KeepUntilUnixMilli: frozen.KeepUntilUnixMilli,
	}
	// Past the grid point and before the read: a Slot not begun waits here.
	due, facts, _ := firstRoundNext(t, interval, foundProgress(slot, 0), time.Unix(int64(slot)+1, 0))
	if due || facts.NotDueUntilUnix <= int64(slot)+1 {
		t.Fatalf("a Slot not begun: due=%t facts=%+v, want it held for its read", due, facts)
	}
	due, _, _ = firstRoundNext(t, interval, load, time.Unix(int64(slot)+1, 0))
	if !due {
		t.Fatal("a Slot already begun was held for its read")
	}
	// Begun means this Slot: a projection of any other says nothing of it.
	other := foundProgress(slot, 0)
	other.Progress.UnfinishedSlot = &execution.UnfinishedSlotProjection{
		Contract: frozen.Contract, DuePlanTargets: frozen.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: frozen.EarliestQueryDeadlineUnixMilli, KeepUntilUnixMilli: frozen.KeepUntilUnixMilli,
	}
	other.Progress.UnfinishedSlot.Contract.Slot.EvaluationTime = slot + interval
	source := newProductionSlotSourceForTest(t, frozenCatalog, other, time.Unix(int64(slot)+1, 0))
	if bound := source.firstRoundSecond(schedule, slot, interval, other.Progress); bound <= int64(slot)+1 {
		t.Fatalf("a projection of another Slot left the bound at %d, want the read", bound)
	}
}

// A source that was not told the settling wait has no read to wait for.
func TestASourceWithoutASettlingWaitKeepsTheGridPoint(t *testing.T) {
	const interval = 60
	slot := execution.EvaluationTime(interval * 1000)
	schedule := schedulerSchedule(t, interval, slot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(int64(slot)-1, 0))
	source.settlingWait = 0
	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due || facts.NotDueUntilUnix != int64(slot) {
		t.Fatalf("Next() due=%t facts=%+v error=%v, want not due until the grid point", due, facts, err)
	}
}

// A schedule whose deadline cannot be derived keeps the grid point, and the round
// there says what is wrong with it, as it did: holding the Slot for a read nobody
// can work out would put that off to a time made up from the broken deadline.
func TestAScheduleWithoutADeadlineKeepsTheGridPoint(t *testing.T) {
	const interval = 60
	slot := execution.EvaluationTime(interval * 1000)
	schedule := schedulerSchedule(t, interval, slot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(int64(slot)-1, 0))
	// A reserve past the completion leaves no query deadline.
	source.queryReserve = time.Hour
	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due || facts.NotDueUntilUnix != int64(slot) {
		t.Fatalf("before the grid point: due=%t facts=%+v error=%v, want not due until the grid point", due, facts, err)
	}
}

// A Slot's deadline is reckoned from its grid point, not from when its first round
// is: reckoned from the round, a Slot waiting for its read would rank behind Slots
// that expire before it. A bound that names no Slot falls back on itself.
func TestTheDeadlineOfASlotWaitingForItsReadIsFromItsGridPoint(t *testing.T) {
	for name, test := range map[string]struct {
		facts SlotDueFacts
		want  int64
	}{
		"waiting for its read":  {SlotDueFacts{NotDueUntilUnix: 76, NextSlotUnix: 60, IntervalSeconds: 60}, 120},
		"waiting for its point": {SlotDueFacts{NotDueUntilUnix: 60, NextSlotUnix: 60, IntervalSeconds: 60}, 120},
		"naming no Slot":        {SlotDueFacts{NotDueUntilUnix: 76, IntervalSeconds: 60}, 136},
	} {
		t.Run(name, func(t *testing.T) {
			source := &fakeSlotSource{facts: test.facts}
			fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
			at := time.Unix(50, 0)
			runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, NewFlightCoordinator(),
				func() time.Time { return at })
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := runner.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if bound := runner.DueBound(); bound.Verdict != DueVerdictNotDue || bound.Deferred {
				t.Fatalf("bound = %+v, want a not-due schedule bound", bound)
			}
			if got := runner.NextDeadline().Unix(); got != test.want {
				t.Fatalf("NextDeadline() = %d, want %d", got, test.want)
			}
		})
	}
}

// A hold is not in the first round's second: a held Slot still comes round at its
// read without the hold, is frozen with the hold there, and access turns it away
// by name. That round is where the lookback reserves its earlier read.
func TestAHeldSlotComesRoundAtItsReadWithoutTheHold(t *testing.T) {
	const interval = 60
	slot := execution.EvaluationTime(interval * 1000)
	bound := (firstRoundRead(interval, slot) + 999) / 1000
	schedule := schedulerSchedule(t, interval, slot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(bound, 0))
	source.readHolds = &testReadHolds{hold: 30 * time.Second}
	frozen, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || frozen.Contract.ReadHoldMillis != 30_000 {
		t.Fatalf("Next() at the read without the hold = %+v due=%t error=%v, want the Slot frozen with its hold", frozen.Contract, due, err)
	}
}
