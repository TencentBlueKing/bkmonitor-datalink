// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// One generation is one rotation, so a walk that reaches every owned Query
// Group is the only thing that counts as a full turn. Nothing else in the
// deployment measures this: the object list can say an object went badly, and
// says nothing at all about an object the dispatcher never reached.
func TestRotationCountsOneFullPassOverTheOwnedSet(t *testing.T) {
	dispatcher := walkDispatcher(4, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)

	facts := dispatcher.bundle.rotationFacts()
	if facts == nil {
		t.Fatal("a completed rotation published nothing")
	}
	if facts.Completed != 1 || facts.Truncated != 0 {
		t.Fatalf("completed=%d truncated=%d, want one completed rotation", facts.Completed, facts.Truncated)
	}
	if facts.Offered != 3 || facts.Queued != 3 || facts.Deferred != 0 {
		t.Fatalf("offered=%d queued=%d deferred=%d, want all three owned Query Groups offered and queued",
			facts.Offered, facts.Queued, facts.Deferred)
	}
	// The duration is what an alert on coverage compares against a period, so a
	// finished rotation that reports no duration is the same as not reporting.
	if facts.LastSeconds < 0 {
		t.Fatalf("last_seconds = %v, want the duration of the rotation that just finished", facts.LastSeconds)
	}
}

// A deployment stuck against a full ready queue is exactly the condition this
// measurement exists to surface, and it is the one where a rotation never
// finishes. If the counts only travel when a rotation completes, the page keeps
// showing the last healthy-looking numbers for as long as the deployment is
// stuck -- the signal disappears precisely when it is true.
func TestRotationIsVisibleWhileTheWalkIsStuckAndNeverCompletes(t *testing.T) {
	dispatcher := walkDispatcher(1, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()

	// The first pass places one Query Group and then finds the ready queue full.
	dispatcher.fillQueues(runners, revision)
	// Further passes free nothing, so the walk stays where it stopped and the
	// rotation never reaches its end.
	dispatcher.fillQueues(runners, revision)
	dispatcher.fillQueues(runners, revision)

	facts := dispatcher.bundle.rotationFacts()
	if facts == nil {
		t.Fatal("a walk that cannot finish published nothing at all, so a stuck deployment looks like a silent one")
	}
	if facts.Completed != 0 {
		t.Fatalf("completed=%d, want none: the walk never reached the whole owned set", facts.Completed)
	}
	if facts.Deferred == 0 {
		t.Fatalf("deferred=%d, want the Query Group the full queue turned away to be counted", facts.Deferred)
	}
	if facts.Queued != 1 {
		t.Fatalf("queued=%d, want the one Query Group that got a place", facts.Queued)
	}
	// Which of the two branches produced those turn-aways, because they have
	// opposite answers and the page had one sentence for both. Here the ready
	// queue genuinely has no room, so more room would change it -- and a page
	// that cannot say so sends a reader to grow a queue on the days the number
	// is the other branch.
	if facts.DeferredQueueFull != facts.Deferred {
		t.Fatalf("deferred_queue_full=%d of deferred=%d, want every turn-away attributed to the "+
			"full ready queue, which is the only branch this walk can reach",
			facts.DeferredQueueFull, facts.Deferred)
	}
	if facts.DeferredNotBetter != 0 {
		t.Fatalf("deferred_not_better=%d, want none: nothing here was turned away for being due "+
			"later than what the recovery queue already holds", facts.DeferredNotBetter)
	}
}

// The recovery queue cannot turn an object away, and the counter that would
// say so is live.
//
// Two things have to be true together and neither is worth much alone. The
// queue's capacity is at least the number of objects this Worker owns and it
// holds at most one entry per object, and the single caller that fills it drops
// the stale entries immediately beforehand -- so its occupancy cannot reach its
// capacity, and deferred_not_better cannot move. That makes it an invariant
// guard rather than a measure of queue pressure: zero says the chain holds,
// non-zero says somebody changed the drop or the capacity floor.
//
// An earlier version of this test drove the branch by calling fillQueues
// without the drop, and read the result as a transient that production reaches.
// It does not: there is one call site and the drop is the line above it. That
// test was constructing a state the deployment cannot be in -- the same defect
// as a check that feeds a collector an object name the wiring never emits,
// which is a thing this codebase has already had to delete once.
//
// So the production order is what is asserted, and the bypass is kept only to
// prove the counter is not dead. A guard whose zero could also mean "nothing
// would ever increment this" says nothing at all.
func TestTheRecoveryQueueCannotTurnAnythingAwayWhileTheStaleEntriesAreDropped(t *testing.T) {
	at := time.Now()
	dispatcher := walkDispatcher(8, 3, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": at.Add(time.Second),
		"query-group-b": at.Add(2 * time.Second),
		"query-group-c": at.Add(3 * time.Second),
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.dropStaleQueued(revision)
	dispatcher.fillQueues(runners, revision)

	// The owned set shrinks and grows in one step -- the moment that looks like
	// it should overflow the queue, and the one the drop exists to clear.
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.removeRunnerLocked("query-group-c")
	delete(dispatcher.bundle.assigned, "query-group-c")
	dispatcher.bundle.setRunnerLocked("query-group-d",
		&phaseTwoQueryGroupLifecycle{runner: walkRunner{readyAt: at.Add(9 * time.Second)}})
	dispatcher.bundle.assigned["query-group-d"] = struct{}{}
	dispatcher.bundle.mu.Unlock()

	changed, changedRevision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.dropStaleQueued(changedRevision)
	dispatcher.fillQueues(changed, changedRevision)

	facts := dispatcher.bundle.rotationFacts()
	if facts == nil {
		t.Fatal("the walk published nothing")
	}
	if facts.DeferredNotBetter != 0 {
		t.Fatalf("deferred_not_better=%d in the order the deployment actually runs in; the stale "+
			"entries are dropped immediately before the queue is filled, so its occupancy cannot "+
			"reach its capacity -- a non-zero here means that drop or the capacity floor changed, "+
			"not that load rose (facts=%+v)", facts.DeferredNotBetter, facts)
	}

	// And the counter is live. Without this, the zero above would also be what a
	// counter nothing can ever increment looks like, and the guard would be
	// indistinguishable from a dead label -- which is the thing this page has
	// been clearing out all along.
	bypass := walkDispatcher(8, 3, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": at.Add(time.Second),
		"query-group-b": at.Add(2 * time.Second),
		"query-group-c": at.Add(3 * time.Second),
	})
	first, firstRevision := bypass.bundle.snapshotScheduledRunners()
	bypass.fillQueues(first, firstRevision)
	bypass.bundle.mu.Lock()
	bypass.bundle.removeRunnerLocked("query-group-c")
	delete(bypass.bundle.assigned, "query-group-c")
	bypass.bundle.setRunnerLocked("query-group-d",
		&phaseTwoQueryGroupLifecycle{runner: walkRunner{readyAt: at.Add(9 * time.Second)}})
	bypass.bundle.assigned["query-group-d"] = struct{}{}
	bypass.bundle.mu.Unlock()
	second, secondRevision := bypass.bundle.snapshotScheduledRunners()
	// Deliberately without dropStaleQueued: this is the broken order the guard
	// exists to catch, not a state the deployment reaches.
	bypass.fillQueues(second, secondRevision)

	if bypassed := bypass.bundle.rotationFacts(); bypassed.DeferredNotBetter == 0 {
		t.Fatalf("skipping the drop turned nothing away, so the guard above is asserting zero on a "+
			"counter that may simply never move (facts=%+v)", bypassed)
	}
}

// A generation replaced before its walk finished did not cover the owned set.
// Counting it as completed would report full coverage for a deployment that
// never achieves it, which is the reading this whole measurement is for.
func TestRotationCountsAReplacedWalkAsTruncatedNotCompleted(t *testing.T) {
	dispatcher := walkDispatcher(1, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)

	// A scheduler tick opens the next generation while the walk is part way
	// through, which is what happens whenever the queue cannot keep up.
	dispatcher.beginGeneration()
	dispatcher.fillQueues(runners, revision)

	facts := dispatcher.bundle.rotationFacts()
	if facts == nil {
		t.Fatal("a truncated rotation published nothing")
	}
	if facts.Truncated != 1 {
		t.Fatalf("truncated=%d, want the replaced walk counted once", facts.Truncated)
	}
	if facts.Completed != 0 {
		t.Fatalf("completed=%d, want none: no walk ever reached the whole owned set", facts.Completed)
	}
}

// The owned set changes for ordinary reasons -- a strategy is added, a
// rebalance moves objects here -- and each change restarts the walk. Whether the
// walk being replaced had finished is a question about the set it was walking,
// so asking it against the replacement gets the answer wrong every time the set
// grows, and "truncated climbing while completed stays flat" is exactly the
// reading that says a deployment has stopped covering its objects.
func TestRotationJudgesTheReplacedWalkAgainstItsOwnOwnedSet(t *testing.T) {
	dispatcher := walkDispatcher(8, 8, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	if facts := dispatcher.bundle.rotationFacts(); facts.Completed != 1 {
		t.Fatalf("completed=%d, want the first rotation to finish before the set changes", facts.Completed)
	}

	// A fourth object arrives. The rotation that just finished reached all three
	// objects it owned, which is a finished rotation whatever happens next.
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.setRunnerLocked("query-group-d", &phaseTwoQueryGroupLifecycle{runner: walkRunner{}})
	dispatcher.bundle.assigned["query-group-d"] = struct{}{}
	dispatcher.bundle.mu.Unlock()
	grown, grownRevision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(grown, grownRevision)

	facts := dispatcher.bundle.rotationFacts()
	if facts.Truncated != 0 {
		t.Fatalf("truncated=%d, want none: the finished rotation covered all three objects it owned",
			facts.Truncated)
	}
}

// The same comparison fails the other way round, and that direction is worse: a
// walk that stopped part way through twenty objects is reported as fine as soon
// as the set shrinks below where it stopped. A deployment that is not covering
// its objects then reads as one that is.
func TestRotationStillCountsATruncatedWalkWhenTheOwnedSetShrinks(t *testing.T) {
	dispatcher := walkDispatcher(1, 0, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
		"query-group-b": {},
		"query-group-c": {},
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	// One place in the ready queue, so the walk places one object and stops.
	dispatcher.fillQueues(runners, revision)
	if facts := dispatcher.bundle.rotationFacts(); facts.Completed != 0 {
		t.Fatalf("completed=%d, want the walk to have stopped part way", facts.Completed)
	}

	dispatcher.bundle.mu.Lock()
	for _, queryGroup := range []execution.QueryGroupIdentity{"query-group-b", "query-group-c"} {
		dispatcher.bundle.removeRunnerLocked(queryGroup)
		delete(dispatcher.bundle.assigned, queryGroup)
	}
	dispatcher.bundle.mu.Unlock()
	shrunk, shrunkRevision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(shrunk, shrunkRevision)

	facts := dispatcher.bundle.rotationFacts()
	if facts.Truncated != 1 {
		t.Fatalf("truncated=%d, want the unfinished walk counted once", facts.Truncated)
	}
}

// Nothing has rotated yet on a replica that has just started, and that is a
// real answer rather than a zero. Publishing zeroes would let a reader take
// "completed 0" from a starting replica and from a stuck one to mean the same
// thing.
func TestRotationReportsNothingBeforeAWalkHasRun(t *testing.T) {
	dispatcher := walkDispatcher(4, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {},
	})
	if facts := dispatcher.bundle.rotationFacts(); facts != nil {
		t.Fatalf("a replica that has not walked yet reported %+v, want no rotation at all", facts)
	}
}

// The stale entries are dropped immediately before the queues are filled, in
// the one place that fills them.
//
// This is the first half of the invariant the recovery queue's guard rests on,
// and it is a fact about the order of two statements -- which no test that
// calls those statements itself can check, because it supplies the order. So it
// is read off the source.
//
// A source-level check is the weaker kind and is used here because the stronger
// kind is not available: driving run() would need the whole bundle, and a test
// that calls dropStaleQueued and fillQueues in sequence proves only that the
// test can call them in sequence. Removing the drop from run() is a mutation
// nothing else here would catch; in production the counter would catch it,
// which is exactly what the counter is for.
func TestTheStaleEntriesAreDroppedImmediatelyBeforeTheQueuesAreFilled(t *testing.T) {
	source, err := os.ReadFile("runtime_phase_two.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(source), "\n")
	fills := 0
	for index, line := range lines {
		if !strings.Contains(line, "dispatcher.fillQueues(") {
			continue
		}
		fills++
		if index == 0 || !strings.Contains(lines[index-1], "dispatcher.dropStaleQueued(") {
			previous := ""
			if index > 0 {
				previous = strings.TrimSpace(lines[index-1])
			}
			t.Errorf("line %d fills the queues after %q; the recovery queue's occupancy can only stay "+
				"below its capacity while the stale entries are dropped first, and deferred_not_better "+
				"is published as an invariant guard on exactly that", index+1, previous)
		}
	}
	if fills != 1 {
		t.Errorf("fillQueues is called from %d places; the invariant is stated about one caller, and "+
			"a second one would need its own ordering checked", fills)
	}
}
