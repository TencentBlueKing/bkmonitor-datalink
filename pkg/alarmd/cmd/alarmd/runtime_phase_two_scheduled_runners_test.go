package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
