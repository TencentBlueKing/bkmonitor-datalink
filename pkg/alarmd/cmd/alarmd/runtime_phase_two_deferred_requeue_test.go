// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// deferringRunner is a Query Group whose Slot is frozen with a deadline and
// which, when it returns, asks to be run again later: the readiness or backoff
// case in which handleResult requeues the same unfinished Slot.
type deferringRunner struct {
	deadline time.Time
	readyAt  time.Time
	interval int64
}

func (runner *deferringRunner) lifecycle() *phaseTwoQueryGroupLifecycle {
	return &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
		nextReadyAt:  func() time.Time { return runner.readyAt },
		nextDeadline: func() time.Time { return runner.deadline },
		dueBound:     func() scheduler.RunnerDueBound { return scheduler.RunnerDueBound{IntervalSeconds: runner.interval} },
		run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
			return execution.SlotExecutionResult{}, false, nil
		},
	}}
}

func deferredRequeueDispatcher(t *testing.T, now time.Time, owned map[execution.QueryGroupIdentity]*deferringRunner) *phaseTwoRunnerDispatcher {
	t.Helper()
	cfg := validGoAccessRuntimeConfig()
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: func() time.Time { return now }, Recorder: metric.NewRecorder(metric.BuildInfo{})},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		assigned:     make(map[execution.QueryGroupIdentity]struct{}),
	}
	bundle.mu.Lock()
	for queryGroup, runner := range owned {
		bundle.setRunnerLocked(queryGroup, runner.lifecycle())
		bundle.assigned[queryGroup] = struct{}{}
	}
	bundle.mu.Unlock()
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.generation = 1
	return dispatcher
}

// walkAndDispatch runs one walk so every owned Query Group is given its first
// place, then dispatches the named one from whichever queue holds it and
// returns the entry it was queued as.
func walkAndDispatch(t *testing.T, dispatcher *phaseTwoRunnerDispatcher, queryGroup execution.QueryGroupIdentity) phaseTwoQueuedRunner {
	t.Helper()
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	for index, entry := range dispatcher.normal {
		if entry.scheduled.queryGroup == queryGroup {
			dispatcher.normal = append(dispatcher.normal[:index], dispatcher.normal[index+1:]...)
			delete(dispatcher.queued, queryGroup)
			dispatcher.active[queryGroup] = entry.scheduled.lifecycle
			return entry
		}
	}
	for index, entry := range dispatcher.delayed {
		if entry.scheduled.queryGroup == queryGroup {
			dispatcher.markDispatched(entry.scheduled, true, index, true)
			return entry
		}
	}
	t.Fatalf("%s was not queued by the walk: normal=%d delayed=%d", queryGroup, len(dispatcher.normal), len(dispatcher.delayed))
	return phaseTwoQueuedRunner{}
}

func delayedEntry(t *testing.T, dispatcher *phaseTwoRunnerDispatcher, queryGroup execution.QueryGroupIdentity) phaseTwoQueuedRunner {
	t.Helper()
	for _, entry := range dispatcher.delayed {
		if entry.scheduled.queryGroup == queryGroup {
			return entry
		}
	}
	t.Fatalf("%s is not in the recovery queue after its deferred return: delayed=%d", queryGroup, len(dispatcher.delayed))
	return phaseTwoQueuedRunner{}
}

// A Slot that returns deferred is the same unfinished work. It goes back into
// the recovery queue with the deadline the Runner still holds frozen and the
// sequence and cohort it was first given -- not as a bare entry with none of
// the three, which is what handleResult used to write. With no deadline it
// ranked behind every dated Slot: a ten-second Slot with five seconds left
// waited behind the minute's Slots with fifty-five.
func TestADeferredSlotKeepsItsDeadlineAndPlaceWhenRequeued(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	short := &deferringRunner{deadline: now.Add(5 * time.Second), readyAt: now.Add(time.Second), interval: 10}
	dispatcher := deferredRequeueDispatcher(t, now, map[execution.QueryGroupIdentity]*deferringRunner{
		"query-group-short": short,
		// Walked after the short one by identity, so it is queued after it
		// and takes the later sequence.
		"query-group-z-minute": {deadline: now.Add(55 * time.Second), interval: 60},
	})
	first := walkAndDispatch(t, dispatcher, "query-group-short")
	if first.sequence == 0 || first.cohort != scheduler.ShortPeriodCohortForInterval(10) || !first.deadline.Equal(short.deadline) {
		t.Fatalf("the walk queued the short Slot as %+v; the test needs a real first place to compare against", first)
	}
	minute := delayedOrNormal(t, dispatcher, "query-group-z-minute")
	if minute.sequence <= first.sequence {
		t.Fatalf("minute sequence %d is not after the short one's %d; the fixture cannot show a lost place", minute.sequence, first.sequence)
	}

	dispatcher.handleResult(context.Background(), phaseTwoScheduledResult{scheduled: first.scheduled, ran: true}, true)

	requeued := delayedEntry(t, dispatcher, "query-group-short")
	if !requeued.deadline.Equal(short.deadline) {
		t.Errorf("deferred Slot deadline = %v, want the frozen %v: the recovery queue would rank it behind every dated Slot", requeued.deadline, short.deadline)
	}
	if requeued.sequence != first.sequence {
		t.Errorf("deferred Slot sequence = %d, want its first %d: a new sequence moves the same Slot behind everything queued while it was out", requeued.sequence, first.sequence)
	}
	if requeued.cohort != first.cohort {
		t.Errorf("deferred Slot cohort = %q, want %q: its turn-aways would be counted under the wrong cohort", requeued.cohort, first.cohort)
	}
	if !requeued.readyAt.Equal(short.readyAt) {
		t.Errorf("deferred Slot readyAt = %v, want the Runner's %v", requeued.readyAt, short.readyAt)
	}
	if !deadlineBefore(requeued, minute) {
		t.Errorf("deferred short Slot (deadline %v) orders after the minute Slot (deadline %v)", requeued.deadline, minute.deadline)
	}
	// The fair position: against a Slot with the same deadline and readiness
	// queued after it, the deferred one is still first among equals.
	later := phaseTwoQueuedRunner{
		scheduled: phaseTwoScheduledRunner{queryGroup: "query-group-a-later"},
		readyAt:   requeued.readyAt, deadline: requeued.deadline, sequence: minute.sequence + 1,
	}
	if !deadlineBefore(requeued, later) {
		t.Errorf("deferred Slot (sequence %d) lost its place among equals to a Slot queued later (sequence %d)", requeued.sequence, later.sequence)
	}
}

// A Runner that comes back due for a different Slot -- its frozen deadline
// moved because the previous Slot completed and the next one is waiting on a
// source backoff -- is being queued for new work, and new work is given the
// next sequence like any first queueing. Keeping the old place would let one
// Query Group hold its position across Slots for as long as it kept deferring.
func TestARunnerDueForANewSlotIsGivenANewPlace(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	runner := &deferringRunner{deadline: now.Add(5 * time.Second), readyAt: now.Add(time.Second), interval: 10}
	dispatcher := deferredRequeueDispatcher(t, now, map[execution.QueryGroupIdentity]*deferringRunner{
		"query-group-short":    runner,
		"query-group-z-minute": {deadline: now.Add(55 * time.Second), interval: 60},
	})
	first := walkAndDispatch(t, dispatcher, "query-group-short")
	minute := delayedOrNormal(t, dispatcher, "query-group-z-minute")

	// The Slot completed while it was out; the Runner is now due for the next
	// one, ten seconds on, and is backing off a source failure before it.
	runner.deadline = runner.deadline.Add(10 * time.Second)
	runner.readyAt = now.Add(3 * time.Second)
	dispatcher.handleResult(context.Background(), phaseTwoScheduledResult{scheduled: first.scheduled, ran: true}, true)

	requeued := delayedEntry(t, dispatcher, "query-group-short")
	if !requeued.deadline.Equal(runner.deadline) {
		t.Errorf("new Slot deadline = %v, want the Runner's current %v", requeued.deadline, runner.deadline)
	}
	if requeued.sequence <= minute.sequence {
		t.Errorf("new Slot sequence = %d, want one after every place given so far (minute has %d): a new Slot is a first queueing", requeued.sequence, minute.sequence)
	}
	if requeued.sequence == first.sequence {
		t.Errorf("new Slot kept the previous Slot's sequence %d", first.sequence)
	}
}

func delayedOrNormal(t *testing.T, dispatcher *phaseTwoRunnerDispatcher, queryGroup execution.QueryGroupIdentity) phaseTwoQueuedRunner {
	t.Helper()
	for _, queue := range [][]phaseTwoQueuedRunner{dispatcher.normal, dispatcher.delayed} {
		for _, entry := range queue {
			if entry.scheduled.queryGroup == queryGroup {
				return entry
			}
		}
	}
	t.Fatalf("%s is in neither queue", queryGroup)
	return phaseTwoQueuedRunner{}
}
