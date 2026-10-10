// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// yieldRig is a dispatcher over one Query Group whose Runner says it is
// ready now -- as one turned away after a readiness deferral does -- with
// what the dispatcher observes, and that Query Group dispatched.
func yieldRig(t *testing.T) (*phaseTwoRunnerDispatcher, *capturedObservations, phaseTwoQueuedRunner) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	dispatcher := deferredRequeueDispatcher(t, now, map[execution.QueryGroupIdentity]*deferringRunner{
		"query-group-1": {deadline: now.Add(55 * time.Second), readyAt: now.Add(-time.Second), interval: 60},
	})
	observed := &capturedObservations{}
	dispatcher.bundle.dependencies.Observer = observed
	return dispatcher, observed, walkAndDispatch(t, dispatcher, "query-group-1")
}

func turnedAway(scheduled phaseTwoScheduledRunner, holder string) phaseTwoScheduledResult {
	return phaseTwoScheduledResult{scheduled: scheduled, ran: true, err: &scheduler.SlotInFlightError{HeldBy: holder}}
}

// A round a supplement turned away is a yield, named for what held the
// flight and not an unexplained failure. It is not queued again at once --
// its Runner still says it is ready, and it would be turned away for as
// long as the hold lasts -- but the moment the hold ends.
func TestARoundASupplementTurnedAwayRunsWhenTheHoldEnds(t *testing.T) {
	for holder, reason := range map[string]observability.ReasonCode{
		scheduler.FlightHeldBySupplement:  observability.ReasonHeldBySupplement,
		scheduler.FlightHeldByMaintenance: observability.ReasonHeldByMaintenance,
	} {
		dispatcher, observed, entry := yieldRig(t)
		dispatcher.handleResult(context.Background(), turnedAway(entry.scheduled, holder), true)
		last := (*observed)[len(*observed)-1]
		if last.Stage != observability.StageScheduleDue || last.Result != observability.ResultSkipped || last.ReasonCode != reason {
			t.Errorf("%s: observed %s %s %s, want a skipped round with the named reason", holder, last.Stage, last.Result, last.ReasonCode)
		}
		if len(dispatcher.delayed) != 0 || dispatcher.queued["query-group-1"] != nil {
			t.Errorf("%s: queued again at once (%d delayed), want it waiting for the hold to end", holder, len(dispatcher.delayed))
		}
		dispatcher.flightReleased("query-group-1")
		if len(dispatcher.delayed) != 1 || dispatcher.queued["query-group-1"] == nil {
			t.Errorf("%s: not queued when the hold ended (%d delayed)", holder, len(dispatcher.delayed))
		}
		dispatcher.flightReleased("query-group-1")
		if len(dispatcher.delayed) != 1 {
			t.Errorf("%s: a second end queued it twice (%d delayed)", holder, len(dispatcher.delayed))
		}
	}
}

// The end of a hold heard before the round's return is kept for it: the
// round is queued the moment its return is handled.
func TestTheEndOfAHoldHeardFirstQueuesTheRoundOnItsReturn(t *testing.T) {
	dispatcher, _, entry := yieldRig(t)
	dispatcher.flightReleased("query-group-1")
	dispatcher.handleResult(context.Background(), turnedAway(entry.scheduled, scheduler.FlightHeldBySupplement), true)
	if len(dispatcher.delayed) != 1 || len(dispatcher.released) != 0 {
		t.Fatalf("%d delayed, %d ends kept, want the round queued and the end used", len(dispatcher.delayed), len(dispatcher.released))
	}
}

// What is kept for a hold's end lasts one generation: the walk offers any
// round left.
func TestAHoldsEndIsKeptForOneGeneration(t *testing.T) {
	dispatcher, _, entry := yieldRig(t)
	dispatcher.handleResult(context.Background(), turnedAway(entry.scheduled, scheduler.FlightHeldBySupplement), true)
	dispatcher.flightReleased("query-group-2")
	dispatcher.beginGeneration()
	if len(dispatcher.turnedAway) != 0 || len(dispatcher.released) != 0 {
		t.Fatalf("turned away %v, ends %v after a generation, want both cleared", dispatcher.turnedAway, dispatcher.released)
	}
}

// A round turned away by another Slot is what it always was: an
// unexplained failure, and queued again as its Runner says.
func TestARoundTurnedAwayByAnotherSlotIsUnexplained(t *testing.T) {
	dispatcher, observed, entry := yieldRig(t)
	dispatcher.handleResult(context.Background(), turnedAway(entry.scheduled, scheduler.FlightHeldBySlot), true)
	last := (*observed)[len(*observed)-1]
	if last.Result != observability.ResultFailed || last.ReasonCode != observability.ReasonInternalUnknown || len(dispatcher.delayed) != 1 {
		t.Fatalf("observed %s %s with %d delayed, want the unexplained failure queued as before", last.Result, last.ReasonCode,
			len(dispatcher.delayed))
	}
}

// In the running dispatcher, a round a supplement turned away is not run
// again while the hold lasts, though its Runner says it is ready; told the
// hold ended, the dispatcher runs it without waiting for its next turn.
func TestTheRunningDispatcherRunsATurnedAwayRoundWhenTheHoldEnds(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	var runs atomic.Int32
	ranAgain := make(chan struct{})
	bundle := &phaseTwoWorkerBundle{
		dependencies:   phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		flightReleased: make(chan execution.QueryGroupIdentity, flightReleasedNotices),
		runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
			"query-group-1": {runner: &callbackPhaseTwoQueryGroup{
				nextReadyAt: func() time.Time { return time.Now().Add(-time.Second) },
				run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
					switch runs.Add(1) {
					case 1:
						return execution.SlotExecutionResult{}, false, &scheduler.SlotInFlightError{HeldBy: scheduler.FlightHeldBySupplement}
					case 2:
						close(ranAgain)
					}
					return execution.SlotExecutionResult{}, false, nil
				},
			}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{})
	done := make(chan error, 1)
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.start(ctx)
	go func() { done <- dispatcher.run(ctx, wake) }()
	wake <- struct{}{}
	for deadline := time.Now().Add(2 * time.Second); runs.Load() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	time.Sleep(200 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("the round ran %d times while the hold lasted, want once", got)
	}
	bundle.noticeFlightReleased("query-group-1")
	select {
	case <-ranAgain:
	case <-time.After(2 * time.Second):
		t.Fatal("the round was not run when the hold ended")
	}
	cancel()
	<-done
}
