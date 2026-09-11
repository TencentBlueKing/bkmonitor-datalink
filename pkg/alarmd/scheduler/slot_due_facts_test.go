package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TestNextReportsTheFutureSlotAsABound pins the one fact a caller needs to stop
// asking: the second the Slot becomes due, and the cadence it belongs to.
//
// The bound is the same number the not-due decision is made on, so it is exact
// rather than an estimate, and the interval is the one the cohort is chosen
// from, so neither costs a read.
func TestNextReportsTheFutureSlotAsABound(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(59, 0))

	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due {
		t.Fatalf("Next() due=%v error=%v", due, err)
	}
	if facts.NotDueUntilUnix != 60 {
		t.Fatalf("not-due bound = %d, want the Slot second 60", facts.NotDueUntilUnix)
	}
	if facts.IntervalSeconds != 60 {
		t.Fatalf("interval = %d, want 60; without it a late wake cannot be scaled to its cadence",
			facts.IntervalSeconds)
	}
	if facts.Retired {
		t.Fatal("a Query Group waiting for a future Slot was reported as retired")
	}
}

// TestNextReportsRetirementRatherThanABound pins that retirement comes back as
// a reason rather than as a time. How long to wait before asking again is the
// caller's decision because retirement is revocable, and a source that invented
// a bound here would be deciding it on the caller's behalf.
func TestNextReportsRetirementRatherThanABound(t *testing.T) {
	boundary := execution.EvaluationTime(90)
	schedule := schedulerSchedule(t, 60, 60, &boundary, "snapshot-retired", 9)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &boundary}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(boundary, 60), time.Unix(200, 0))

	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due {
		t.Fatalf("retired Next() due=%v error=%v", due, err)
	}
	if !facts.Retired {
		t.Fatal("a retired Query Group was not reported as retired")
	}
	if facts.NotDueUntilUnix != 0 {
		t.Fatalf("retired Next() invented a bound of %d; the recheck interval is the caller's",
			facts.NotDueUntilUnix)
	}
}

// TestNextEstablishesNoBoundForBacklog is the structural safety property the due
// index rests on.
//
// A Query Group whose cursor is in the past takes the branch that resolves a
// Slot, which returns due and no bound at all. Replay, expired ranges, gap
// closure and Snapshot-unavailable finalization all reach the caller this way,
// so an index built from these returns has nothing it could park them on. The
// property is structural rather than a rule the index has to remember to
// follow, and this is where it is pinned.
func TestNextEstablishesNoBoundForBacklog(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, missingProgress(), time.Unix(200, 0), testRecoveryLimits())

	slot, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("backlog Next() due=%v error=%v", due, err)
	}
	if slot.Recovery.Disposition == "" {
		t.Fatalf("expected a recovery disposition on a backlog Slot, got %+v", slot.Recovery)
	}
	if facts.NotDueUntilUnix != 0 {
		t.Fatalf("a backlog Slot came back with a bound of %d, so an index could park recovery work",
			facts.NotDueUntilUnix)
	}
	if facts.IntervalSeconds != 60 {
		t.Fatalf("interval = %d, want 60 even on the due path", facts.IntervalSeconds)
	}
}

// TestRunnerBoundsFollowTheReturnPath pins the four returns that never reach the
// source, because each one has to be given a bound by hand and a missing case
// would silently become "due now" - safe, but a round trip per tick forever.
func TestRunnerBoundsFollowTheReturnPath(t *testing.T) {
	now := time.Unix(1_000, 0)
	slot := frozenSlot("query-group-1")

	t.Run("not due carries the source bound", func(t *testing.T) {
		source := &fakeSlotSource{facts: SlotDueFacts{NotDueUntilUnix: 1_060, IntervalSeconds: 60}}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
			source, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true}, nil
			}), NewFlightCoordinator(), func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || attempted {
			t.Fatalf("RunOne() attempted=%v error=%v", attempted, err)
		}
		bound := runner.DueBound()
		if bound.Verdict != DueVerdictNotDue || bound.NotDueUntilUnix != 1_060 ||
			bound.IntervalSeconds != 60 || bound.Deferred || bound.Executed {
			t.Fatalf("not-due bound = %+v", bound)
		}
	})

	t.Run("single flight busy reaches no verdict", func(t *testing.T) {
		flights := NewFlightCoordinator()
		source := &fakeSlotSource{facts: SlotDueFacts{NotDueUntilUnix: 1_060, IntervalSeconds: 60}}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
			source, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true}, nil
			}), flights, func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}
		release, acquired := flights.tryAcquire("query-group-1")
		if !acquired {
			t.Fatal("could not hold the single-flight gate")
		}
		defer release()
		if _, _, err := runner.RunOne(context.Background()); err == nil {
			t.Fatal("RunOne() did not report the Query Group as already in flight")
		}
		bound := runner.DueBound()
		if bound.Verdict != DueVerdictUnknown || bound.NotDueUntilUnix != 0 {
			t.Fatalf("single-flight bound = %+v, want no verdict and due now: a round that never "+
				"looked at the schedule cannot be counted for or against the index", bound)
		}
	})

	t.Run("cancelled reaches no verdict", func(t *testing.T) {
		source := &fakeSlotSource{facts: SlotDueFacts{NotDueUntilUnix: 1_060, IntervalSeconds: 60}}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
			source, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true}, nil
			}), NewFlightCoordinator(), func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := runner.RunOne(ctx); err == nil {
			t.Fatal("RunOne() ignored a cancelled context")
		}
		if bound := runner.DueBound(); bound.Verdict != DueVerdictUnknown || bound.NotDueUntilUnix != 0 {
			t.Fatalf("cancelled bound = %+v, want no verdict and due now", bound)
		}
	})

	t.Run("ownership rejection reaches no verdict", func(t *testing.T) {
		source := &fakeSlotSource{facts: SlotDueFacts{NotDueUntilUnix: 1_060, IntervalSeconds: 60}}
		runner, err := NewRunner("query-group-1", &fakeSession{err: errors.New("ownership rejected")},
			source, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true}, nil
			}), NewFlightCoordinator(), func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}
		if _, _, err := runner.RunOne(context.Background()); err == nil {
			t.Fatal("RunOne() ignored a rejected ownership check")
		}
		if bound := runner.DueBound(); bound.Verdict != DueVerdictUnknown || bound.NotDueUntilUnix != 0 {
			t.Fatalf("ownership-rejected bound = %+v, want no verdict and due now", bound)
		}
	})

	t.Run("source backoff defers without reading the store", func(t *testing.T) {
		source := &fakeSlotSource{facts: SlotDueFacts{NotDueUntilUnix: 1_060, IntervalSeconds: 60}}
		runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence},
			source, workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true}, nil
			}), NewFlightCoordinator(), func() time.Time { return now })
		if err != nil {
			t.Fatalf("NewRunner() error = %v", err)
		}
		runner.sourceNextAt = now.Add(30 * time.Second)
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || attempted {
			t.Fatalf("RunOne() attempted=%v error=%v", attempted, err)
		}
		if source.calls != 0 {
			t.Fatalf("the local backoff gate still called the source %d times", source.calls)
		}
		bound := runner.DueBound()
		if bound.Verdict != DueVerdictNotDue || !bound.Deferred || bound.NotDueUntilUnix != 1_030 {
			t.Fatalf("source-backoff bound = %+v, want a deferred bound at 1030", bound)
		}
	})
}
