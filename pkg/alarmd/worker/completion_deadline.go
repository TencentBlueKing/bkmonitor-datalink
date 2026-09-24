package worker

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"time"
)

// Normal completion cancellation is a frozen Schedule fact, never an
// observation-cohort decision. Existing and recovery contracts are unchanged.
func shortPeriodCompletionContext(ctx context.Context, operation execution.Operation, header execution.InternalExecutionHeader) (context.Context, context.CancelFunc) {
	noop := func() {}
	if operation != execution.OperationNormal || len(header.DuePlans) == 0 {
		return ctx, noop
	}
	var deadline int64
	for _, due := range header.DuePlans {
		spec := due.ScheduleSpec
		if spec.Validate() != nil || (spec.EvaluationIntervalSeconds != 10 && spec.EvaluationIntervalSeconds != 15) || spec.CompletionDeadlineOffsetSeconds != 30 {
			return ctx, noop
		}
		resolved, ok := spec.CompletionDeadlineUnixMilli(header.Contract.Slot.EvaluationTime)
		if !ok || resolved != due.CompletionDeadlineUnixMilli || (deadline != 0 && deadline != resolved) {
			return ctx, noop
		}
		deadline = resolved
	}
	return context.WithDeadline(ctx, time.UnixMilli(deadline))
}
