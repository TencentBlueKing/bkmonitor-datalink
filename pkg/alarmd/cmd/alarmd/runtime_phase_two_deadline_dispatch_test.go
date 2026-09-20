// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The dispatcher takes the Slot that expires first, across both of its
// queues. Before this it took the recovery queue by readiness and the ready
// queue in walk order, one for one: a ten-second Query Group whose Slot
// became ready at :10 and expired at :25 queued behind the minute's thousand
// ready Slots expiring at :55 and started at :24, and the round was lost at
// the permit with 1.2 seconds left. Its readiness rule was right and its
// deadline was right; the order between the two queues read neither.
//
// A single worker (fanout 1) makes the order of dispatch the order the run
// callbacks are invoked in.
func newDeadlineDispatchBundle(t *testing.T, cfg configForDeadlineDispatch) (*phaseTwoWorkerBundle, chan execution.QueryGroupIdentity, *metric.Recorder) {
	t.Helper()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	started := make(chan execution.QueryGroupIdentity, 64)
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg.config, Now: func() time.Time { return cfg.now }, Recorder: recorder},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
	}
	for _, runner := range cfg.runners {
		queryGroup := runner.queryGroup
		readyAt, deadline, interval := runner.readyAt, runner.deadline, runner.interval
		// Once run, a Slot is done with: the Runner reports nothing due for
		// an hour, so a dispatch is observed exactly once.
		var ran atomic.Bool
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			nextReadyAt: func() time.Time {
				if ran.Load() {
					return cfg.now.Add(time.Hour)
				}
				return readyAt
			},
			nextDeadline: func() time.Time {
				if ran.Load() {
					return time.Time{}
				}
				return deadline
			},
			dueBound: func() scheduler.RunnerDueBound { return scheduler.RunnerDueBound{IntervalSeconds: interval} },
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				ran.Store(true)
				started <- queryGroup
				return execution.SlotExecutionResult{}, false, nil
			},
		}}
	}
	return bundle, started, recorder
}

type deadlineDispatchRunner struct {
	queryGroup execution.QueryGroupIdentity
	readyAt    time.Time
	deadline   time.Time
	interval   int64
}

type configForDeadlineDispatch struct {
	config  config.Config
	now     time.Time
	runners []deadlineDispatchRunner
}

func collectDispatches(t *testing.T, started <-chan execution.QueryGroupIdentity, count int) []execution.QueryGroupIdentity {
	t.Helper()
	order := make([]execution.QueryGroupIdentity, 0, count)
	for len(order) < count {
		select {
		case queryGroup := <-started:
			order = append(order, queryGroup)
		case <-time.After(2 * time.Second):
			t.Fatalf("dispatched %v, want %d dispatches", order, count)
		}
	}
	return order
}

func stopDeadlineDispatcher(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runScheduler(cancel) error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop after cancellation")
	}
}

func TestPhaseTwoDispatcherTakesTheEarliestDeadlineAcrossBothQueues(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0
	// One worker: the order the run callbacks are invoked in is the order
	// of dispatch.
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 1
	now := time.Unix(1_700_000_000, 0)
	runners := []deadlineDispatchRunner{
		// A retry with no deadline of its own, ready and first by identity:
		// the old order took it first.
		{queryGroup: "query-group-a-retry", readyAt: now.Add(-time.Second)},
		// The short-period Slot: ready, expiring at :15.
		{queryGroup: "query-group-short", readyAt: now.Add(-time.Second), deadline: now.Add(15 * time.Second), interval: 10},
	}
	for _, name := range []execution.QueryGroupIdentity{"query-group-m1", "query-group-m2", "query-group-m3", "query-group-m4", "query-group-m5", "query-group-m6"} {
		// The minute's ready Slots, expiring at :55, walked before the short
		// one by identity.
		runners = append(runners, deadlineDispatchRunner{queryGroup: name, deadline: now.Add(55 * time.Second), interval: 60})
	}
	bundle, started, _ := newDeadlineDispatchBundle(t, configForDeadlineDispatch{config: cfg, now: now, runners: runners})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()
	wake <- struct{}{}
	order := collectDispatches(t, started, len(runners))
	stopDeadlineDispatcher(t, cancel, done)

	want := []execution.QueryGroupIdentity{"query-group-short",
		"query-group-m1", "query-group-m2", "query-group-m3", "query-group-m4", "query-group-m5", "query-group-m6",
		"query-group-a-retry"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("dispatch order = %v, want %v: the Slot expiring first goes first, a known deadline before an unknown one, and among equals the walk order", order, want)
	}
}

// A full ready queue gives up the entry expiring last for an arrival that
// expires earlier, and counts both the hold-back and the eviction under the
// cohort of the Query Group they happened to. The evicted entry is offered
// again on the next rotation.
func TestPhaseTwoDispatcherFullReadyQueueGivesUpTheLatestDeadlineForAnEarlierOne(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 1
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 0
	// One worker: the order the run callbacks are invoked in is the order
	// of dispatch.
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 1
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 2
	now := time.Unix(1_700_000_000, 0)
	minute := func(name execution.QueryGroupIdentity) deadlineDispatchRunner {
		return deadlineDispatchRunner{queryGroup: name, deadline: now.Add(55 * time.Second), interval: 60}
	}
	runners := []deadlineDispatchRunner{
		minute("query-group-1"), minute("query-group-2"), minute("query-group-3"),
		{queryGroup: "query-group-4-short", deadline: now.Add(15 * time.Second), interval: 10},
	}
	bundle, started, recorder := newDeadlineDispatchBundle(t, configForDeadlineDispatch{config: cfg, now: now, runners: runners})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- bundle.runScheduler(ctx, wake, false) }()
	wake <- struct{}{}
	// Walk: 1 and 2 fill the queue; 3 arrives no earlier than either and is
	// held back, the walk stopping at it. Dispatching 1 frees a place; the
	// walk resumes with 3; the short one arrives behind it, expires earlier
	// than 3, takes its place, and is dispatched before 2.
	order := collectDispatches(t, started, 3)
	want := []execution.QueryGroupIdentity{"query-group-1", "query-group-4-short", "query-group-2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("dispatch order = %v, want %v", order, want)
	}
	for labels, want := range map[[2]string]float64{
		{"normal_queue_full", "other"}:    1,
		{"normal_queue_evicted", "other"}: 1,
		{"normal_queue_full", "10s"}:      0,
		{"normal_queue_evicted", "10s"}:   0,
	} {
		got := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_queue_turnaways_total", map[string]string{"outcome": labels[0], "cohort": labels[1]})
		if got != want {
			t.Fatalf("dispatch_queue_turnaways_total%v = %v, want %v", labels, got, want)
		}
	}
	// The evicted minute Slot is held back to the next rotation, not lost.
	wake <- struct{}{}
	if next := collectDispatches(t, started, 1); next[0] != "query-group-3" {
		t.Fatalf("next rotation dispatched %v first, want the evicted query-group-3", next)
	}
	stopDeadlineDispatcher(t, cancel, done)
}
