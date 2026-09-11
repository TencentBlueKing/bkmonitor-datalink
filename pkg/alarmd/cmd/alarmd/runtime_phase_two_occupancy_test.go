package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// occupancyRunner returns from RunOneAdmitted as soon as released is closed.
type occupancyRunner struct {
	entered  chan struct{}
	released chan struct{}
}

func (r occupancyRunner) RunOne(context.Context) (execution.SlotExecutionResult, bool, error) {
	return execution.SlotExecutionResult{}, false, nil
}

func (r occupancyRunner) RunOneAdmitted(
	context.Context,
	scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	close(r.entered)
	<-r.released
	return execution.SlotExecutionResult{}, false, false, nil
}

func (r occupancyRunner) NextReadyAt() time.Time { return time.Time{} }

func (r occupancyRunner) DueBound() scheduler.RunnerDueBound { return scheduler.RunnerDueBound{} }
func (r occupancyRunner) MaintainLease(context.Context, time.Duration, time.Duration) error {
	return nil
}
func (r occupancyRunner) Release(context.Context) error { return nil }

func occupancyDispatcher(observer observability.Observer) (*phaseTwoRunnerDispatcher, phaseTwoScheduledRunner) {
	cfg := config.Config{}
	// One owned Query Group and one slot: occupancy is what is under test here,
	// not fanout.
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 1
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 8
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = 8
	cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Second)
	runner := occupancyRunner{entered: make(chan struct{}), released: make(chan struct{})}
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: runner}
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Observer: observer, Now: time.Now},
		runners:      map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{"qg": lifecycle},
		assigned:     map[execution.QueryGroupIdentity]struct{}{"qg": {}},
	}
	return newPhaseTwoRunnerDispatcher(bundle, false),
		phaseTwoScheduledRunner{queryGroup: "qg", lifecycle: lifecycle}
}

func awaitExecuting(t *testing.T, dispatcher *phaseTwoRunnerDispatcher, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if dispatcher.executing.Load() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("occupancy stayed at %d, want %d", dispatcher.executing.Load(), want)
}

// A Runner return must not wait for the dispatcher occupancy snapshot to reach
// the observer. Publishing that snapshot on the return path made every Runner
// queue behind one observer call and reported that queue as occupancy.
func TestPhaseTwoDispatcherRunnerReturnDoesNotWaitForOccupancySnapshot(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	var snapshots atomic.Int64
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Stage != observability.StageDispatcherSnapshot {
			return
		}
		snapshots.Add(1)
		<-blocked
	})
	dispatcher, scheduled := occupancyDispatcher(observer)
	runner := scheduled.lifecycle.runner.(occupancyRunner)

	go dispatcher.executeScheduled(context.Background(), scheduled)
	<-runner.entered
	awaitExecuting(t, dispatcher, 1)
	close(runner.released)
	awaitExecuting(t, dispatcher, 0)
	if snapshots.Load() != 0 {
		t.Fatalf("Runner return published %d dispatcher snapshots", snapshots.Load())
	}
}

// The dispatcher loop stays the publisher, so the reported occupancy still
// carries the live Runner count together with both queue depths.
func TestPhaseTwoDispatcherLoopPublishesLiveOccupancy(t *testing.T) {
	var facts []observability.DispatcherFacts
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Dispatcher != nil && o.Dispatcher.QueuesKnown {
			facts = append(facts, *o.Dispatcher)
		}
	})
	dispatcher, scheduled := occupancyDispatcher(observer)
	dispatcher.normal = append(dispatcher.normal, phaseTwoQueuedRunner{scheduled: scheduled})
	dispatcher.observeOccupancy(context.Background())
	dispatcher.markDispatched(scheduled, false, false)
	dispatcher.changeExecuting(1)
	dispatcher.observeOccupancy(context.Background())
	dispatcher.changeExecuting(-1)
	dispatcher.handleResult(context.Background(), phaseTwoScheduledResult{scheduled: scheduled}, false)
	dispatcher.observeOccupancy(context.Background())
	want := []observability.DispatcherFacts{
		{Active: 0, Ready: 1, QueuesKnown: true},
		{Active: 1, QueuesKnown: true},
		{Active: 0, QueuesKnown: true},
	}
	if len(facts) != len(want) {
		t.Fatalf("published %+v", facts)
	}
	for i := range want {
		if facts[i] != want[i] {
			t.Fatalf("published %+v want %+v", facts, want)
		}
	}
}
