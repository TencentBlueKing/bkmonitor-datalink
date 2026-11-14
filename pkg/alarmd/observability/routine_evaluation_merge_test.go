package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// routineLines is five routine evaluation lines of Plan costA's series in
// the minute group's Slot, and the one line they merge into.
func routineLines() ([]Observation, Observation) {
	trace := TraceFields{QueryGroupKey: "minute", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}
	var lines []Observation
	var merged Observation
	for index := range 5 {
		trace.DimensionIdentityDigest = string(rune('a' + index))
		line := Observation{Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
			Operation: OperationNormal, Duration: time.Millisecond, DurationKnown: true, Counts: Counts{Records: 30},
			EvaluationRecordsKnown: true, EvaluationOwner: costA, Trace: trace,
			LevelOutcomes: []LevelOutcomeFact{{Outcome: "NORMAL", Count: 1}}}
		if !Routine(line) {
			panic("fixture: a routine line is not routine")
		}
		lines = append(lines, line)
		MergeRoutineEvaluation(&merged, line)
	}
	return lines, merged
}

// The cost summary, the target flow and the line counts read the same over
// a Plan's routine evaluations merged into one line as over the lines.
func TestRoutineEvaluationsMergedAreCountedAsTheLinesTheyWereMergedFrom(t *testing.T) {
	lines, merged := routineLines()
	if merged.Counts.Evaluations != int64(len(lines)) || merged.Trace.DimensionIdentityDigest != "" {
		t.Fatalf("merged %d evaluations of series %q, want %d of none", merged.Counts.Evaluations,
			merged.Trace.DimensionIdentityDigest, len(lines))
	}
	summary := func(observations ...Observation) []byte {
		c, now := costDueFixture(t, costMinute)
		for _, observation := range observations {
			c.Observe(context.Background(), observation)
		}
		c.Publish(*now)
		encoded, err := json.Marshal(c.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	if got, want := summary(merged), summary(lines...); !bytes.Equal(got, want) {
		t.Fatalf("cost summary over the merged line:\n%s\nover the lines:\n%s", got, want)
	}
	flow := func(observations ...Observation) (uint64, int64) {
		f, err := NewTargetFlow(New("alarmd", io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Select([]string{flowQG}); err != nil {
			t.Fatal(err)
		}
		ctx := f.Context(context.Background(), flowQG)
		for _, observation := range observations {
			// Of the selected group, as the flow's context says.
			observation.Trace.QueryGroupKey = flowQG
			f.Observe(ctx, observation)
		}
		state := ctx.Value(targetFlowContextKey{}).(*targetFlowContext)
		return state.evaluations.Load(), state.evaluationNS.Load()
	}
	gotN, gotNS := flow(merged)
	wantN, wantNS := flow(lines...)
	if wantN != uint64(len(lines)) || gotN != wantN || gotNS != wantNS {
		t.Fatalf("target flow over the merged line read %d evaluations in %d ns, over the lines %d in %d; want %d",
			gotN, gotNS, wantN, wantNS, len(lines))
	}
	counts := func(observations ...Observation) LogLineCounts {
		observer := NewLoggingObserver(New("alarmd", io.Discard), logPolicyForMergeTest(t))
		for _, observation := range observations {
			observer.Observe(context.Background(), observation)
		}
		return observer.LineCounts()
	}
	got, want := counts(merged), counts(lines...)
	if got.Unwritten[StageEvaluationCompleted] != want.Unwritten[StageEvaluationCompleted] ||
		got.Written[StageEvaluationCompleted] != want.Written[StageEvaluationCompleted] {
		t.Fatalf("line counts over the merged line %+v, over the lines %+v", got, want)
	}
}

func logPolicyForMergeTest(t *testing.T) *BoundedLogPolicy {
	t.Helper()
	limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// The retained peak census reads the same compute wall over a Plan's routine
// evaluations merged into one line as over the lines, their time known or
// not.
func TestTheCensusReadsRoutineEvaluationsMergedAsTheLines(t *testing.T) {
	for _, known := range []bool{true, false} {
		lines, _ := routineLines()
		var merged Observation
		for index := range lines {
			if !known {
				lines[index].Duration, lines[index].DurationKnown = 0, false
			}
			MergeRoutineEvaluation(&merged, lines[index])
		}
		read := func(observations ...Observation) []CostRetainedPeak {
			now := time.Unix(1440, 0)
			census := NewRetainedPeakCensus(5*time.Minute, func() time.Time { return now })
			// A timed state write of the group, so it is a reading whatever
			// the evaluations' time.
			census.Observe(context.Background(), Observation{Stage: StageStateApplied, Duration: time.Millisecond,
				DurationKnown: true, Trace: TraceFields{QueryGroupKey: "minute"}})
			for _, observation := range observations {
				census.Observe(context.Background(), observation)
			}
			return census.RetainedPeaks()
		}
		got, want := read(merged), read(lines...)
		if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
			t.Fatalf("known %t: census over the merged line %+v, over the lines %+v", known, got, want)
		}
	}
}
