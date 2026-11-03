// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const hungGrace = time.Minute

// duringExecutor runs during an execution, with the execution's context,
// and completes it.
type duringExecutor struct {
	during func(context.Context, execution.SlotExecutionRequest)
}

func (executor *duringExecutor) Execute(ctx context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	if executor.during != nil {
		executor.during(ctx, request)
	}
	if request.Supplement != nil {
		return execution.SlotExecutionResult{Supplement: &execution.SupplementFacts{}}, nil
	}
	return execution.SlotExecutionResult{Completed: true}, nil
}

// A replay, retry or probe does not run under its frozen Slot's query
// deadline: that is the first attempt's, and the takeover replay that lands
// minutes after it runs under the deadline access derives from its own
// arrival. Until access gives it that one it has no deadline and is not
// judged; from then on it is judged by that one alone. The frozen deadline
// here is twenty-eight years before the clock.
func TestARecoveryExecutionIsJudgedOnlyByTheDeadlineAccessGivesIt(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	for _, operation := range []execution.Operation{execution.OperationReplay, execution.OperationRetry, execution.OperationProbe} {
		t.Run(string(operation), func(t *testing.T) {
			slot := frozenSlot("query-group-1")
			slot.Dispatch.Operation = operation
			own := now.Add(55 * time.Second)
			var before, atOwn, pastOwn bool
			var ran bool
			executor := &duringExecutor{}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot},
				executor, NewFlightCoordinator(), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			executor.during = func(ctx context.Context, request execution.SlotExecutionRequest) {
				ran = request.Operation == operation
				_, _, before = runner.DeclineHung(now.Add(time.Hour), hungGrace)
				// What access does once it has derived the recovery deadline.
				execution.ExtendDeadline(ctx, own, now)
				_, _, atOwn = runner.DeclineHung(own.Add(hungGrace), hungGrace)
				_, _, pastOwn = runner.DeclineHung(own.Add(hungGrace+time.Millisecond), hungGrace)
			}
			if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted || !ran {
				t.Fatalf("RunOne = (%t, %v), ran as %s: %t", attempted, err, operation, ran)
			}
			if before {
				t.Fatal("declined before access gave it a deadline, by the frozen Slot's first-attempt deadline")
			}
			if atOwn {
				t.Fatal("declined at its own deadline plus exactly the grace")
			}
			if !pastOwn {
				t.Fatal("not declined past its own deadline plus the grace")
			}
		})
	}
}

// A normal execution runs under its frozen Slot's query deadline from the
// start, before any query. One that starts after that deadline - a Slot
// given up on, which finalizes without a query - counts from its start: it
// is not hung for having been handed a deadline that went before it began.
func TestANormalExecutionRunsUnderItsQueryDeadlineCountedFromNoEarlierThanItsStart(t *testing.T) {
	for _, test := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "live", now: time.UnixMilli(100_500), want: time.UnixMilli(101_000)},
		{name: "started past it", now: time.UnixMilli(400_000), want: time.UnixMilli(400_000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			slot := frozenSlot("query-group-1")
			var deadline time.Time
			var inside, past bool
			executor := &duringExecutor{}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot},
				executor, NewFlightCoordinator(), func() time.Time { return test.now })
			if err != nil {
				t.Fatal(err)
			}
			executor.during = func(context.Context, execution.SlotExecutionRequest) {
				deadline = runner.stage.Deadline()
				_, _, inside = runner.DeclineHung(test.want.Add(hungGrace), hungGrace)
				_, _, past = runner.DeclineHung(test.want.Add(hungGrace+time.Millisecond), hungGrace)
			}
			if _, _, err := runner.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !deadline.Equal(test.want) || inside || !past {
				t.Fatalf("deadline %v, declined inside the grace %t, past it %t; want %v, false, true", deadline, inside, past, test.want)
			}
		})
	}
}

// The marker is the execution's and goes with it. Once the execution has
// returned, the next round - still waiting for the flight, or turned away
// from it - carries nothing of it for the watchdog to judge.
func TestAnExecutionLeavesNothingToJudgeOnceItReturns(t *testing.T) {
	now := time.UnixMilli(400_000)
	slot := frozenSlot("query-group-1")
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot},
		&duringExecutor{}, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stage, deadline, declined := runner.DeclineHung(now.Add(24*time.Hour), hungGrace); declined || stage != "" || !deadline.IsZero() {
		t.Fatalf("after the execution returned: (%q, %v, %t), want nothing in flight", stage, deadline, declined)
	}
}

// A supplement is not a scheduled execution and does not run under its
// frozen Slot's deadline, which is long past - it supplements a Slot already
// completed. While it holds the flight there is nothing for the watchdog to
// judge.
func TestASupplementHoldingTheFlightIsNotJudgedByItsSlotsDeadline(t *testing.T) {
	slot := supplementSlot(t)
	now := time.Unix(260, 0)
	var declined bool
	executor := &duringExecutor{}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: testFence(7)}, &supplementSource{slot: slot},
		executor, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	executor.during = func(context.Context, execution.SlotExecutionRequest) {
		_, _, declined = runner.DeclineHung(time.UnixMilli(slot.EarliestQueryDeadlineUnixMilli).Add(24*time.Hour), hungGrace)
	}
	if _, err := runner.Supplement(context.Background(), 120, 0, execution.SupplementScope{Series: []execution.SeriesIdentityDigest{"a"}}); err != nil {
		t.Fatal(err)
	}
	if declined {
		t.Fatal("a supplement was declined by its completed Slot's deadline")
	}
}
