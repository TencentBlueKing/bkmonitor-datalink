package main

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
	"time"
)

func TestWorkflowExecutorReturnClassifications(t *testing.T) {
	for _, name := range []string{"completed", "readiness_deferred", "retrying", "cancelled", "error", "incomplete"} {
		t.Run(name, func(t *testing.T) {
			result := execution.SlotExecutionResult{}
			var cause error
			switch name {
			case "completed":
				result.Completed = true
			case "readiness_deferred":
				cause = phaseTwoReadinessDeferredError{readyAt: time.Now().Add(time.Second)}
			case "retrying":
				result.Result = observability.ResultRetrying
			case "cancelled":
				cause = context.Canceled
			case "error":
				cause = errors.New("sensitive error")
			}
			var observations []observability.Observation
			e := observedProductionSlotExecutor{next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return result, cause
			}), observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { observations = append(observations, o) })}
			got, err := e.Execute(context.Background(), execution.SlotExecutionRequest{})
			if !errors.Is(err, cause) || got.Completed != result.Completed || len(observations) != 2 || observations[1].ExecuteOutcome != name {
				t.Fatalf("return/observations changed: %+v %v %+v", got, err, observations)
			}
		})
	}
}

func TestWorkflowDispatcherOccupancyUsesActualQueues(t *testing.T) {
	var snapshots []observability.DispatcherFacts
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Dispatcher != nil && o.Dispatcher.QueuesKnown {
			snapshots = append(snapshots, *o.Dispatcher)
		}
	})}}
	d := newPhaseTwoRunnerDispatcher(bundle, false)
	first := phaseTwoScheduledRunner{queryGroup: "a", lifecycle: &phaseTwoQueryGroupLifecycle{}}
	second := phaseTwoScheduledRunner{queryGroup: "b", lifecycle: &phaseTwoQueryGroupLifecycle{}}
	d.normal = append(d.normal, phaseTwoQueuedRunner{scheduled: first})
	d.delayed = append(d.delayed, phaseTwoQueuedRunner{scheduled: second})
	d.observeOccupancy(context.Background())
	d.markDispatched(first, false, true)
	d.changeExecuting(1)
	d.observeOccupancy(context.Background())
	d.changeExecuting(-1)
	d.handleResult(context.Background(), phaseTwoScheduledResult{scheduled: first}, false)
	d.observeOccupancy(context.Background())
	want := []observability.DispatcherFacts{{Active: 0, Ready: 1, Delayed: 1, QueuesKnown: true}, {Active: 1, Ready: 0, Delayed: 1, QueuesKnown: true}, {Active: 0, Ready: 0, Delayed: 1, QueuesKnown: true}}
	if len(snapshots) != len(want) {
		t.Fatal(snapshots)
	}
	for i := range want {
		if snapshots[i] != want[i] {
			t.Fatalf("%+v want%+v", snapshots, want)
		}
	}
}
