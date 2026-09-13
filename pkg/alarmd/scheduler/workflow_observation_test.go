package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type workflowExecutor func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error)

func (f workflowExecutor) Execute(c context.Context, r execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	return f(c, r)
}

func TestWorkflowPermitWaitCountsEveryReturnOnce(t *testing.T) {
	var mu sync.Mutex
	var waits []observability.Observation
	queued := make(chan struct{}, 1)
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Stage == observability.StageQueryAdmission && o.Result == observability.ResultStarted {
			select {
			case queued <- struct{}{}:
			default:
			}
		}
		if o.Stage == observability.StageQueryPermitWait {
			mu.Lock()
			waits = append(waits, o)
			mu.Unlock()
		}
	})
	f, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), time.Now, observer)
	if err != nil {
		t.Fatal(err)
	}
	slot := execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 100}
	first, err := f.AcquireQueryPermit(context.Background(), slot, execution.OperationRetry, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := f.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "other", EvaluationTime: 100}, execution.OperationRetry, time.Now().Add(time.Minute))
		done <- e
	}()
	<-queued
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	normal, err := f.AcquireQueryPermit(context.Background(), execution.SlotIdentity{QueryGroup: "normal", EvaluationTime: 100}, execution.OperationNormal, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	normal.Release()
	first.Release()
	_, err = f.AcquireQueryPermit(context.Background(), slot, execution.OperationNormal, time.Now().Add(-time.Second))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(waits) != 4 {
		t.Fatalf("wait return observations=%d", len(waits))
	}
	for _, o := range waits {
		if o.Duration < 0 || o.PermitWait == nil {
			t.Fatalf("%+v", o)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queryInflight != 0 || f.recoveryInflight != 0 || len(f.normalWaiters)+len(f.recoveryWaiters) != 0 {
		t.Fatal("permit occupancy leaked")
	}
}

func TestWorkflowRunnerActualExits(t *testing.T) {
	for _, name := range []string{"single_flight_busy", "ownership_rejected", "source_backoff", "source_retry", "source_blocked", "source_not_due", "source_error", "operation_not_ready", "admission_denied", "execute_returned", "cancelled", "other_error"} {
		t.Run(name, func(t *testing.T) {
			now := time.Unix(100, 0)
			slot := frozenSlot("query-group-1")
			session := &fakeSession{fence: slot.Dispatch.OwnerFence}
			source := &fakeSlotSource{slot: slot}
			flights := NewFlightCoordinator()
			var observed []observability.Observation
			flights.observer = observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageRunnerReturned {
					observed = append(observed, o)
				}
			})
			runner, err := NewRunner("query-group-1", session, source, &blockingExecutor{}, flights, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			var admission ExecutionAdmission
			expectedAttempted := false
			switch name {
			case "single_flight_busy":
				release, _ := flights.tryAcquire("query-group-1")
				defer release()
			case "ownership_rejected":
				session.err = errors.New("owner changed")
			case "source_backoff":
				runner.sourceNextAt = now.Add(time.Second)
			case "source_retry":
				source.err = &SourceRetryError{Err: errors.New("retry")}
				expectedAttempted = true
			case "source_blocked":
				source.err = &SourceBlockedError{Err: errors.New("blocked")}
				expectedAttempted = true
			case "source_not_due":
				source.slot = FrozenSlot{}
			case "source_error":
				source.err = errors.New("source")
			case "operation_not_ready":
				flights.recoveryEnabled = true
				runner.attempt = &recoveryAttempt{contract: slot.Contract, nextAt: now.Add(time.Second)}
			case "admission_denied":
				admission = func(execution.Operation) (func(), bool) { return nil, false }
			case "execute_returned":
				expectedAttempted = true
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "other_error":
				source.slot.Contract.QueryRevision = ""
			}
			_, attempted, _ := runner.runOne(ctx, admission)
			if len(observed) != 1 || observed[0].RunOutcome != name || observed[0].Attempted != expectedAttempted || attempted != expectedAttempted {
				t.Fatalf("outcome=%+v attempted=%v want %s/%v", observed, attempted, name, expectedAttempted)
			}
		})
	}
}

func TestWorkflowRunnerPanicAndObserverIsolation(t *testing.T) {
	slot := frozenSlot("query-group-1")
	flights := NewFlightCoordinator()
	var outcome string
	flights.observer = observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		outcome = o.RunOutcome
		panic("observer must not replace business panic")
	})
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, &fakeSlotSource{slot: slot}, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
		panic("business")
	}), flights, func() time.Time { return time.Unix(100, 0) })
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if got := recover(); got != "business" {
				t.Fatalf("panic=%v", got)
			}
		}()
		_, _, _ = runner.RunOne(context.Background())
	}()
	if outcome != "panic" {
		t.Fatalf("outcome=%s", outcome)
	}
	release, ok := flights.tryAcquire("query-group-1")
	if !ok {
		t.Fatal("flight leaked")
	}
	release()
	runner.executor = &blockingExecutor{}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted || outcome != "execute_returned" {
		t.Fatalf("observer changed healthy return: %v %v %s", err, attempted, outcome)
	}
}
