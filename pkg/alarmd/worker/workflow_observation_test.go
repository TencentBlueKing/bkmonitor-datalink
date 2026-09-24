package worker_test

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
)

func assertWorkflowProgress(t *testing.T, f fixture, want string) {
	t.Helper()
	found := 0
	for _, o := range *f.observations {
		if o.ProgressCompletionKind != "" {
			found++
			if o.Stage != observability.StageProgressCommitted || o.Err != nil || o.ProgressCompletionKind != want {
				t.Fatalf("false commit fact: %+v", o)
			}
		}
	}
	expected := 1
	if want == "" {
		expected = 0
	}
	if found != expected {
		t.Fatalf("commit facts=%d want%d", found, expected)
	}
}
func TestWorkflowProgressUsesActualCommit(t *testing.T) {
	for _, stage := range []string{"", "query_after_series", "state_load", "evaluate", "event_ack", "state_apply", "gap_after", "progress_commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t, true, stage)
			r, err := f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if stage == "" {
				if err != nil || !r.Completed {
					t.Fatalf("%+v %v", r, err)
				}
				assertWorkflowProgress(t, f, string(execution.CompletionFull))
			} else {
				if r.Completed || (err == nil && r.Result != observability.ResultRetrying) {
					t.Fatalf("injection not reached: %+v %v", r, err)
				}
				assertWorkflowProgress(t, f, "")
			}
		})
	}
	f := newFixture(t, true, "")
	f.ports.progressConflict = true
	_, _ = f.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	assertWorkflowProgress(t, f, "")
	plans, reqs := baseDuePlanAndRequirements()
	f, req := newCompletionOnlyFixture(t, plans, reqs, nil)
	r, err := f.coordinator.Execute(context.Background(), req)
	if err != nil || !r.Completed {
		t.Fatal(err)
	}
	assertWorkflowProgress(t, f, string(execution.CompletionFullEmpty))
	assertNoFullEmptyBusinessSideEffects(t, f)
}
