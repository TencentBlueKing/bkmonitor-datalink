package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryFreeFinalizationCopiesTheHeldContractThroughGapAndProgress(t *testing.T) {
	for _, mode := range []execution.FinalizationMode{execution.FinalizationSnapshotUnavailable, execution.FinalizationGapSkipped} {
		t.Run(string(mode), func(t *testing.T) {
			request := slotRequest(execution.OperationReplay)
			request.Contract.ReadHoldMillis = 60_000
			request.EarliestQueryDeadlineUnixMilli += 60_000
			request.RecoveryUntilUnixMilli += 60_000
			request.KeepUntilUnixMilli += 60_000
			activation := activePlanResult("state-v2", 2)
			activation.Contract = request.Contract
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
			fixture.ports.finalization.Contract, fixture.ports.finalization.Mode = request.Contract, mode
			if mode == execution.FinalizationGapSkipped {
				fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
			}
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed || fixture.ports.lastProgress.Completion.Contract != request.Contract {
				t.Fatalf("result=%+v progress=%+v err=%v", result, fixture.ports.lastProgress, err)
			}
			if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 {
				t.Fatal("query-free finalization applied business state or events")
			}
		})
	}
}
