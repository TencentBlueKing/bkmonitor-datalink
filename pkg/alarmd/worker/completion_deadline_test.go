package worker

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

func TestShortCompletionUsesFrozenAbsoluteDeadlineAndPreservesOtherContracts(t *testing.T) {
	at := execution.EvaluationTime(time.Now().Unix() - 31)
	for _, interval := range []int64{10, 15} {
		spec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
		deadline, _ := spec.CompletionDeadlineUnixMilli(at)
		header := execution.InternalExecutionHeader{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: at}}, DuePlans: []execution.DuePlan{{ScheduleSpec: spec, CompletionDeadlineUnixMilli: deadline}}}
		ctx, cancel := shortPeriodCompletionContext(context.Background(), execution.OperationNormal, header)
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatal("expired frozen deadline reset")
		}
		cancel()
		for _, op := range []execution.Operation{execution.OperationRetry, execution.OperationReplay, execution.OperationProbe} {
			ctx, cancel = shortPeriodCompletionContext(context.Background(), op, header)
			if _, ok := ctx.Deadline(); ok {
				t.Fatal("recovery completion changed")
			}
			cancel()
		}
		header.DuePlans[0].ScheduleSpec.CompletionDeadlineOffsetSeconds = 0
		ctx, cancel = shortPeriodCompletionContext(context.Background(), execution.OperationNormal, header)
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("legacy schedule changed")
		}
		cancel()
	}
}
