package worker_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

type diagnosticQueryError struct{}

func (diagnosticQueryError) Error() string { return "https://user:secret@example.test/?token=secret" }
func (diagnosticQueryError) QueryFailure() (string, string) {
	return "series_identity", "IDENTITY_FIELD_MISSING"
}

func TestQueryFailureBoundaryAndHealthySibling(t *testing.T) {
	for _, tc := range []struct{ name, stage, category, code string }{
		{"provider", "execute", "series_identity", "IDENTITY_FIELD_MISSING"},
		{"stream", "stream_complete", "other", "OTHER"},
		{"budget", "execute", "budget", "series"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureWithBudget(t, worker.ProvisionalBudget{MaxSeries: 1, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
			switch tc.name {
			case "provider":
				f.ports.queryAfterSeriesError = fmt.Errorf("wrapped: %w", diagnosticQueryError{})
			case "stream":
				f.ports.failStage = "gap_load"
			case "budget":
				f.ports.reverseStateReceipts = true
			}
			failed := slotRequest(execution.OperationNormal)
			failed.Contract.Slot.QueryGroup = "failed-query-group"
			failed.OwnerFence.QueryGroup = failed.Contract.Slot.QueryGroup
			result, err := f.coordinator.Execute(context.Background(), failed)
			if err == nil || result.Completed {
				t.Fatalf("failed result=%+v err=%v", result, err)
			}
			if f.ports.eventCount != 0 || f.ports.stateApplyCalls != 0 || !isZeroProgressCommit(f.ports.lastProgress) {
				t.Fatal("failed input reached committed effects")
			}
			var got *observability.QueryFailureFacts
			for _, o := range *f.observations {
				if o.Stage == observability.StageQueryCompleted {
					got = o.QueryFailure
				}
			}
			if got == nil || got.Stage != tc.stage || got.Category != tc.category || got.Code != tc.code {
				t.Fatalf("diagnostics=%+v want=%+v", got, tc)
			}
			f.ports.queryAfterSeriesError = nil
			f.ports.failStage = ""
			f.ports.reverseStateReceipts = false
			healthy := slotRequest(execution.OperationNormal)
			result, err = f.coordinator.Execute(context.Background(), healthy)
			if err != nil || !result.Completed || f.ports.lastProgress.Identity.QueryGroup != healthy.Contract.Slot.QueryGroup {
				t.Fatalf("healthy sibling result=%+v err=%v", result, err)
			}
		})
	}
}
