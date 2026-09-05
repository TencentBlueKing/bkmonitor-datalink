package worker

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func (coordinator *SlotExecutionCoordinator) observeQueryFailure(ctx context.Context, operation execution.Operation, started time.Time, stage string, err error) {
	facts := observability.QueryFailureFacts{Stage: stage, Category: "other", Code: "OTHER"}
	var exceeded *provisionalBudgetExceededError
	var diagnostic interface{ QueryFailure() (string, string) }
	if errors.As(err, &exceeded) {
		facts.Category = "budget"
		facts.Code = string(exceeded.budget)
	} else if errors.As(err, &diagnostic) {
		facts.Category, facts.Code = diagnostic.QueryFailure()
	}
	observation := observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted, Result: observability.ResultFailed, Operation: observability.Operation(operation), Direction: observability.DirectionInternal, ReasonCode: observability.ReasonInternalUnknown, Duration: time.Since(started), Err: err, QueryFailure: &facts}
	defer func() { _ = recover() }()
	coordinator.ports.Observer.Observe(ctx, observation)
}
