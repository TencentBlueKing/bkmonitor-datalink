package main

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
	"time"
)

func TestObservedShortPeriodSlotRequiresSuccessfulProgressKind(t *testing.T) {
	for _, test := range []struct {
		name   string
		result execution.SlotExecutionResult
		err    error
		count  bool
	}{
		{name: "unfinished"},
		{name: "cancelled", err: context.DeadlineExceeded},
		{name: "missing kind", result: execution.SlotExecutionResult{Completed: true}},
		{name: "failed with stale result", result: execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull}, err: errors.New("state failed")},
		{name: "full", result: execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull}, count: true},
		{name: "query free", result: execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionSnapshotUnavailable}, count: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var facts []*observability.ShortPeriodCompletionFacts
			runner := observedProductionSlotExecutor{next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return test.result, test.err
			}), observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.ShortPeriodCompletion != nil {
					facts = append(facts, o.ShortPeriodCompletion)
				}
			})}
			// The third attempt at this Slot: a lag past the deadline on it is
			// a retry's, and the fact has to say so.
			request := execution.SlotExecutionRequest{ShortPeriodCohort: "10s", Operation: execution.OperationNormal, AttemptNo: 3, Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: execution.EvaluationTime(time.Now().Unix() - 20)}}}
			_, _ = runner.Execute(context.Background(), request)
			if (len(facts) == 1) != test.count {
				t.Fatalf("facts=%+v", facts)
			}
			if test.count && (facts[0].CompletionKind != string(test.result.CompletionKind) || facts[0].LagSeconds < 20 || facts[0].AttemptNo != 3) {
				t.Fatalf("wrong kind/lag/attempt: %+v, want the request's kind, a lag of at least 20 s and attempt 3", facts[0])
			}
		})
	}
}
