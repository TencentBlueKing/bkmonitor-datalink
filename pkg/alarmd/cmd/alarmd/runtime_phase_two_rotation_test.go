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
