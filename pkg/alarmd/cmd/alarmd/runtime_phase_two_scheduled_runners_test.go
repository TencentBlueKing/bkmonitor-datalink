package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func scheduledRunnerBundle() *phaseTwoWorkerBundle {
	return &phaseTwoWorkerBundle{
		runners: make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
	}
}

func scheduledRunnerNames(runners []phaseTwoScheduledRunner) []execution.QueryGroupIdentity {
	names := make([]execution.QueryGroupIdentity, 0, len(runners))
	for _, runner := range runners {
		names = append(names, runner.queryGroup)
	}
	return names
}

// TestSnapshotScheduledRunnersFollowsEveryOwnedSetChange pins the invariant the
// dispatcher's ordered view depends on: it is rebuilt after an addition, a
// removal and a replacement that leaves the size unchanged. The ordered view is
// kept between passes of the dispatcher loop, so a change it did not follow
// would keep dispatching a Query Group this Worker no longer owns, or a
// lifecycle that has been replaced.
func TestSnapshotScheduledRunnersFollowsEveryOwnedSetChange(t *testing.T) {
	bundle := scheduledRunnerBundle()
	first := &phaseTwoQueryGroupLifecycle{}
	second := &phaseTwoQueryGroupLifecycle{}
	replacement := &phaseTwoQueryGroupLifecycle{}

	bundle.mu.Lock()
	bundle.setRunnerLocked("query-group-b", second)
	bundle.setRunnerLocked("query-group-a", first)
	bundle.mu.Unlock()

	runners, revision := bundle.snapshotScheduledRunners()
	if got := scheduledRunnerNames(runners); len(got) != 2 || got[0] != "query-group-a" || got[1] != "query-group-b" {
		t.Fatalf("ordered view is %v, want query-group-a then query-group-b", got)
	}
	repeat, repeatRevision := bundle.snapshotScheduledRunners()
	if repeatRevision != revision {
		t.Fatalf("an unchanged owned set moved from revision %d to %d", revision, repeatRevision)
	}
	if len(repeat) != len(runners) {
		t.Fatalf("an unchanged owned set changed size from %d to %d", len(runners), len(repeat))
	}

	// A replacement leaves the size alone, which is exactly the change a size
	// comparison would miss.
	bundle.mu.Lock()
	bundle.setRunnerLocked("query-group-a", replacement)
	bundle.mu.Unlock()
	runners, replacedRevision := bundle.snapshotScheduledRunners()
	if replacedRevision == revision {
		t.Fatal("replacing a lifecycle left the owned set revision unchanged")
	}
	if len(runners) != 2 || runners[0].lifecycle != replacement {
		t.Fatalf("ordered view still holds the replaced lifecycle: %+v", runners[0])
	}

	bundle.mu.Lock()
	bundle.removeRunnerLocked("query-group-b")
	bundle.mu.Unlock()
	runners, removedRevision := bundle.snapshotScheduledRunners()
	if removedRevision == replacedRevision {
		t.Fatal("removing a Runner left the owned set revision unchanged")
	}
	if got := scheduledRunnerNames(runners); len(got) != 1 || got[0] != "query-group-a" {
		t.Fatalf("ordered view is %v after a removal, want query-group-a alone", got)
	}
}

// TestSnapshotScheduledRunnersIsSharedAndOrdered pins that the ordered view is
// handed out as one shared slice: callers take copies of the entry they use, so
// nothing writes through it.
func TestSnapshotScheduledRunnersIsSharedAndOrdered(t *testing.T) {
	bundle := scheduledRunnerBundle()
	bundle.mu.Lock()
	for _, name := range []execution.QueryGroupIdentity{"c", "a", "b"} {
		bundle.setRunnerLocked(name, &phaseTwoQueryGroupLifecycle{})
	}
	bundle.mu.Unlock()
	first, _ := bundle.snapshotScheduledRunners()
	second, _ := bundle.snapshotScheduledRunners()
	if len(first) != 3 || &first[0] != &second[0] {
		t.Fatal("the ordered view was rebuilt for an unchanged owned set")
	}
	if got := scheduledRunnerNames(first); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("ordered view is %v, want a, b, c", got)
	}
}

// walkRunner reports a fixed next-ready time and is never invoked: these tests
// drive the queue-filling walk on its own.
type walkRunner struct{ readyAt time.Time }

func (walkRunner) RunOne(context.Context) (execution.SlotExecutionResult, bool, error) {
	return execution.SlotExecutionResult{}, false, nil
}

func (walkRunner) RunOneAdmitted(
	context.Context,
	scheduler.ExecutionAdmission,
) (execution.SlotExecutionResult, bool, bool, error) {
	return execution.SlotExecutionResult{}, false, false, nil
}

func (runner walkRunner) NextReadyAt() time.Time { return runner.readyAt }

func (walkRunner) MaintainLease(context.Context, time.Duration, time.Duration) error { return nil }

func (walkRunner) Release(context.Context) error { return nil }

func walkDispatcher(
	readyCapacity, recoveryCapacity int,
	owned map[execution.QueryGroupIdentity]time.Time,
) *phaseTwoRunnerDispatcher {
	var cfg config.Config
	cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = readyCapacity
	cfg.PhaseTwo.Scheduler.RecoveryQueueCapacity = recoveryCapacity
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{Config: cfg, Now: time.Now},
		runners:      make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle),
		assigned:     make(map[execution.QueryGroupIdentity]struct{}),
	}
	bundle.mu.Lock()
	for queryGroup, readyAt := range owned {
		bundle.setRunnerLocked(queryGroup, &phaseTwoQueryGroupLifecycle{runner: walkRunner{readyAt: readyAt}})
		bundle.assigned[queryGroup] = struct{}{}
	}
	bundle.mu.Unlock()
	dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
	dispatcher.generation = 1
	return dispatcher
}

func queuedNames(queue []phaseTwoQueuedRunner) []execution.QueryGroupIdentity {
	names := make([]execution.QueryGroupIdentity, 0, len(queue))
	for _, queued := range queue {
		names = append(names, queued.scheduled.queryGroup)
	}
	return names
}

// TestFillQueuesDefersTheWalkWhileTheReadyQueueIsFull pins the trade-off the
// cross-pass walk makes, because it is the one the walk's own design forces and
// it is invisible until the ready queue actually fills.
//
// A Query Group that finds the ready queue full keeps its place instead of
// spending its turn, so the walk stops there. The cost is that everything
// behind it waits behind it, including a Query Group that would have gone to
// the recovery queue, which has room. The bound is that the wait is one
// dispatch, not one generation: freeing a single place lets the turned-away
// Query Group in and the walk carries on to the one behind it, in the same
// generation.
func TestFillQueuesDefersTheWalkWhileTheReadyQueueIsFull(t *testing.T) {
	dispatcher := walkDispatcher(1, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": time.Now().Add(time.Hour),
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()

	dispatcher.fillQueues(runners, revision)
	if got := queuedNames(dispatcher.normal); len(got) != 1 || got[0] != "query-group-a" {
		t.Fatalf("ready queue holds %v, want query-group-a alone", got)
	}
	if got := queuedNames(dispatcher.delayed); len(got) != 0 {
		t.Fatalf("recovery queue holds %v, want nothing: the walk stopped at the full ready queue", got)
	}

	// The walk stopped at the Query Group it could not place, so a further pass
	// that frees nothing places nothing and reconsiders nothing.
	dispatcher.fillQueues(runners, revision)
	if len(dispatcher.normal) != 1 || len(dispatcher.delayed) != 0 {
		t.Fatalf("a pass with no room queued ready=%v recovery=%v",
			queuedNames(dispatcher.normal), queuedNames(dispatcher.delayed))
	}

	// One dispatch frees one place. The Query Group turned away takes it in the
	// same generation - it never spent its turn - and the walk then reaches the
	// Query Group behind it, which the recovery queue takes.
	dispatcher.markDispatched(dispatcher.normal[0].scheduled, false, false)
	dispatcher.fillQueues(runners, revision)
	if got := queuedNames(dispatcher.normal); len(got) != 1 || got[0] != "query-group-b" {
		t.Fatalf("ready queue holds %v, want query-group-b alone", got)
	}
	if got := queuedNames(dispatcher.delayed); len(got) != 1 || got[0] != "query-group-c" {
		t.Fatalf("recovery queue holds %v, want query-group-c", got)
	}
	if dispatcher.generation != 1 {
		t.Fatalf("the walk needed generation %d to finish, want 1", dispatcher.generation)
	}
	if dispatcher.walked != len(runners) {
		t.Fatalf("the walk finished after %d of %d Query Groups", dispatcher.walked, len(runners))
	}
}

// TestReturningRunnerDoesNotTakeThePlaceTheWalkHasNotReached pins the rule that
// decides who gets a freed place in the ready queue while a generation's walk is
// still in progress.
//
// A Runner whose turn was consumed while it was active is owed another one, and
// it is the first to be able to ask for it: it asks the moment it returns, which
// is before the walk resumes. Granting it there hands the queue to whichever
// Query Groups finish fastest, and the ones the walk has not reached yet never
// see a free place. A rotation already owes it nothing extra - the next
// generation's walk reaches it again in turn - so the freed place belongs to the
// Query Group the walk stopped at.
func TestReturningRunnerDoesNotTakeThePlaceTheWalkHasNotReached(t *testing.T) {
	dispatcher := walkDispatcher(2, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
		"query-group-d": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()

	dispatcher.fillQueues(runners, revision)
	if got := queuedNames(dispatcher.normal); len(got) != 2 || got[0] != "query-group-a" || got[1] != "query-group-b" {
		t.Fatalf("ready queue holds %v, want query-group-a and query-group-b", got)
	}

	// The first Query Group is dispatched, so one place is free, and a scheduler
	// tick opens the next generation while it is still running.
	first := dispatcher.normal[0].scheduled
	dispatcher.markDispatched(first, false, false)
	dispatcher.beginGeneration()

	// It now returns. Its turn in the previous generation was spent, so it is
	// owed one, but the walk has not offered query-group-c a place yet.
	dispatcher.handleResult(context.Background(), phaseTwoScheduledResult{scheduled: first, attempted: true}, true)
	if got := queuedNames(dispatcher.normal); len(got) != 1 || got[0] != "query-group-b" {
		t.Fatalf("ready queue holds %v after the Runner returned, want query-group-b alone", got)
	}

	// The walk resumes and the free place goes to the Query Group it stopped at.
	dispatcher.fillQueues(runners, revision)
	if got := queuedNames(dispatcher.normal); len(got) != 2 || got[0] != "query-group-b" || got[1] != "query-group-c" {
		t.Fatalf("ready queue holds %v, want query-group-b then query-group-c", got)
	}
}
