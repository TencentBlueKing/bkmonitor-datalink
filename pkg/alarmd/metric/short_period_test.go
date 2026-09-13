package metric

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
	"time"
)

func TestShortPeriodCompletionCountsCommitKindNotQueryOrUnfinishedAttempt(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	o := observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Operation: observability.OperationNormal, Result: observability.ResultSuccess, Duration: time.Second}
	r.Observe(context.Background(), o)
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "10s", CompletionKind: "FULL_COMPLETED", LagSeconds: 20}
	r.Observe(context.Background(), o)
	o.ShortPeriodCompletion = &observability.ShortPeriodCompletionFacts{Cohort: "10s", CompletionKind: "SNAPSHOT_UNAVAILABLE", LagSeconds: 21}
	o.Result = observability.ResultDegraded
	r.Observe(context.Background(), o)
	o.Component = observability.ComponentAccess
	o.Stage = observability.StageQueryCompleted
	r.Observe(context.Background(), o)
	for _, kind := range []string{"FULL_COMPLETED", "SNAPSHOT_UNAVAILABLE"} {
		if got := testutil.ToFloat64(r.phaseTwo.shortPeriod.completed.WithLabelValues("10s", "normal", kind)); got != 1 {
			t.Fatalf("%s count=%v", kind, got)
		}
	}
}
