package metric

import (
	"context"
	"errors"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestWorkflowContractHasActualReturnFacts(t *testing.T) {
	o := observability.Observation{Component: observability.ComponentScheduler, Stage: "runner_returned", Result: observability.ResultSuccess}
	field := reflect.ValueOf(&o).Elem().FieldByName("RunOutcome")
	if !field.IsValid() {
		t.Fatal("actual RunOne return facts are missing")
	}
	field.SetString("source_retry")
	NewRecorder(BuildInfo{}).Observe(context.Background(), o)
}

func TestWorkflowContractCountsAndSeriesBound(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	ctx := context.Background()
	runs := []string{"single_flight_busy", "ownership_rejected", "source_backoff", "source_retry", "source_blocked", "source_not_due", "source_error", "operation_not_ready", "admission_denied", "execute_returned", "cancelled", "panic", "other_error"}
	for _, out := range runs {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned, RunOutcome: out, Attempted: out == "source_retry"})
	}
	for _, out := range []string{"completed", "readiness_deferred", "retrying", "cancelled", "error", "incomplete"} {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, ExecuteOutcome: out})
	}
	kinds := []string{"FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "COMPLETED_WITH_PARTIAL_GAP", "COMPLETED_WITH_UNAVAILABLE", "COMPLETED_WITH_TERMINAL", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE"}
	for _, kind := range kinds {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted, Result: observability.ResultSuccess, ProgressCompletionKind: kind})
	}
	r.Observe(ctx, observability.Observation{Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted, Result: observability.ResultSuccess, ProgressCompletionKind: kinds[0], Err: errors.New("sensitive failure")})
	r.Observe(ctx, observability.Observation{Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted, Result: observability.ResultFailed, ProgressCompletionKind: kinds[0]})
	for _, stage := range []observability.Stage{observability.StageStatePreflight, observability.StageStateApplied} {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentState, Stage: stage, Duration: time.Millisecond})
	}
	for _, recovery := range []bool{false, true} {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageQueryPermitWait, PermitWait: &observability.PermitWaitFacts{Recovery: recovery}, Duration: time.Millisecond})
	}
	r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageDispatcherSnapshot, Dispatcher: &observability.DispatcherFacts{Active: 2, Ready: 7, Delayed: 3, QueuesKnown: true}})
	if got := testutil.ToFloat64(r.phaseTwo.workflow.attempted); got != 1 {
		t.Fatalf("attempted=%v", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.workflow.progress.WithLabelValues(kinds[0])); got != 1 {
		t.Fatalf("failed commit counted: %v", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.workflow.active); got != 2 {
		t.Fatalf("F=%v", got)
	}
	for _, bad := range []string{"secret-url", "", strings.Repeat("x", 1024)} {
		r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned, RunOutcome: bad})
		r.Observe(ctx, observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, ExecuteOutcome: bad})
	}
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range families {
		name := strings.TrimPrefix(f.GetName(), "bkmonitor_alarmd_")
		switch name {
		case "run_one_return_total", "run_one_attempted_total", "execute_return_total", "progress_completed_total", "scheduler_active_executions", "scheduler_ready_runners", "scheduler_delayed_runners", "query_permit_wait_seconds", "slot_operation_duration_seconds":
		default:
			continue
		}
		for _, m := range f.Metric {
			if name == "slot_operation_duration_seconds" && m.Label[0].GetValue() == "execute" {
				continue
			} // existing stage, not a new series
			for _, l := range m.Label {
				switch l.GetName() {
				case "outcome", "kind", "stage", "queue_kind":
				default:
					t.Fatalf("unexpected label %s", l.GetName())
				}
				if strings.Contains(l.GetValue(), "secret") {
					t.Fatal("unbounded label")
				}
			}
			if m.Histogram != nil {
				count += len(m.Histogram.Bucket) + 3
			} else {
				count++
			}
		}
	}
	if count != 74 {
		t.Fatalf("new series=%d want74 (including +Inf,sum,count, no created)", count)
	}
}
