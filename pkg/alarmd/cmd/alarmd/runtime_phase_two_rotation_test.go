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

// A recovery queue that is full of objects all due sooner than this one.
//
// The dispatcher's own comment calls this "a decision, not a lack of room", and
// it lands in the same total as a ready queue with no places. They need opposite
// responses -- one is answered by more room and the other is not -- so a page
// reporting only the total can offer only one remedy, and where this branch
// dominates that remedy does nothing.
//
// The state has to be built rather than configured. Since the recovery queue's
// capacity became max(the configured value, the owned count), and the queue
// holds at most one entry per owned object, its occupancy can only reach its
// capacity while the owned set has just shrunk and the stale entries have not
// been dropped yet. That transient is what this constructs: three objects
// parked, one taken away, a new one arriving before the cleanup.
//
// An earlier version of this test set a small capacity and let the queue
// overflow. That state stopped existing when the capacity rule changed, and the
// test's own guard -- nothing was turned away, so the branch was not reached --
// is what said so. The right answer to a state someone else legitimately
// removed is to rebuild the state or retire the test, never to relax the
// assertion until it passes again.
func TestRotationSeparatesAQueueWithNoRoomFromOneHoldingSoonerWork(t *testing.T) {
	at := time.Now()
	// The configured recovery capacity equals the owned count, so the queue
	// reaches its bound under either sizing rule -- a fixed capacity, or the
	// larger of that and the owned set. The branch under test is about the
	// ordering once the queue is at its bound, and pinning it to one sizing rule
	// would make this fail the next time that rule is corrected rather than the
	// next time the ordering is.
	dispatcher := walkDispatcher(8, 3, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": at.Add(time.Second),
		"query-group-b": at.Add(2 * time.Second),
		"query-group-c": at.Add(3 * time.Second),
	})
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(runners, revision)
	if queued := dispatcher.bundle.rotationFacts().Queued; queued != 3 {
		t.Fatalf("queued=%d, want the three parked objects in the recovery queue before the set "+
			"changes; without them the branch under test is not reachable", queued)
	}

	// The owned set shrinks and grows in one step, and the entries the removed
	// object left behind are deliberately not dropped -- dropStaleQueued is the
	// cleanup this transient is defined as being before.
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.removeRunnerLocked("query-group-c")
	delete(dispatcher.bundle.assigned, "query-group-c")
	dispatcher.bundle.setRunnerLocked("query-group-d",
		&phaseTwoQueryGroupLifecycle{runner: walkRunner{readyAt: at.Add(9 * time.Second)}})
	dispatcher.bundle.assigned["query-group-d"] = struct{}{}
	dispatcher.bundle.mu.Unlock()

	changed, changedRevision := dispatcher.bundle.snapshotScheduledRunners()
	dispatcher.fillQueues(changed, changedRevision)

	facts := dispatcher.bundle.rotationFacts()
	if facts == nil {
		t.Fatal("the walk published nothing")
	}
	if facts.DeferredNotBetter == 0 {
		t.Fatalf("deferred_not_better=0, so this walk did not reach the branch under test: the new "+
			"object is due after everything the recovery queue already holds, and the queue is at "+
			"its capacity because the owned set shrank under it (facts=%+v)", facts)
	}
	if facts.DeferredQueueFull != 0 {
		t.Fatalf("deferred_queue_full=%d, want none: the ready queue has eight places, and "+
			"reporting these as a full queue is what sends a reader to grow a queue that is not "+
			"the constraint", facts.DeferredQueueFull)
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
