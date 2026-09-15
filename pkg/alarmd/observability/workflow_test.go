package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestWorkflowTargetStateIsRunAggregateWithoutPerSeriesLogs(t *testing.T) {
	f, b := newTestFlow(t)
	for run := 0; run < 2; run++ {
		ctx := f.Context(context.Background(), flowQG)
		for i := 0; i < 100; i++ {
			f.Observe(ctx, Observation{Stage: StageStatePreflight, Duration: time.Millisecond, Counts: Counts{Keys: 2}})
			f.Observe(ctx, Observation{Stage: StageStateApplied, Duration: 2 * time.Millisecond, Counts: Counts{Keys: 1}})
		}
		if f.records != run {
			t.Fatal("state emitted per series log")
		}
		EmitTargetFlow(ctx, "runner_decision", TraceFields{}, TargetFlowFacts{})
	}
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatal(len(lines))
	}
	var last uint64
	for _, line := range lines {
		var r targetFlowRecord
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatal(err)
		}
		v := r.Facts
		if v.RunID <= last || v.StatePreflightCalls != 100 || v.StatePreflightKeys != 200 || v.StatePreflightNS != 100*int64(time.Millisecond) || v.StateApplyCalls != 100 || v.StateApplyKeys != 100 {
			t.Fatalf("run aggregation=%+v", v)
		}
		last = v.RunID
	}
}

func BenchmarkWorkflowDisabledState(b *testing.B) {
	f := (*TargetFlow)(nil)
	ctx := context.Background()
	o := Observation{Stage: StageStatePreflight, Duration: time.Millisecond}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f.Observe(ctx, o)
	}
}

func TestWorkflowMetricOnlyFactsDoNotConsumeLogQuota(t *testing.T) {
	var out bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	logger := NewLoggingObserver(New("alarmd", &out), policy)
	for _, stage := range []Stage{StageRunnerReturned, StageDispatcherSnapshot, StageQueryPermitWait} {
		for i := 0; i < 100; i++ {
			logger.Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: stage, Result: ResultTerminal})
		}
	}
	if out.Len() != 0 {
		t.Fatal("metric-only observations serialized into ordinary logs")
	}
}
