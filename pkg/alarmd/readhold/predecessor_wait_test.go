// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// waitBoundary is where a Plan moved from "old" to "new". On "old", at a
// 150 s hold, its last Slot is 1140 s with a 55 s completion offset: closed,
// the record gives a deadline of 1140 s + 150 s + 55 s = 1345 s, and open, the
// bound 1140 s + 600 s + 55 s = 1795 s. "new" settles for 30 s: its first Slot,
// 1200 s, is ready at 1230 s on no hold, at 1345 s on the closed record's.
const waitBoundary = execution.EvaluationTime(1200)

type waitFixture struct {
	t        *testing.T
	c        *Controller
	store    *memoryControl
	now      *time.Time
	schedule execution.FrozenQueryGroupSchedule
}

// newWaitFixture is "old" holding 150 s with its record not closed yet, and
// "new" configured for the Plan it left, with the hold limit given.
func newWaitFixture(t *testing.T, holdLimit time.Duration) *waitFixture {
	t.Helper()
	c, store, now := controllerFixture(t)
	*now = time.Unix(1100, 0)
	prepare(t, c, groupSpec("old", 0))
	observeEarly(t, c, "old", 180*time.Second, 0)
	spec := groupSpec("new", 0)
	spec.HoldLimit = holdLimit
	spec.Previous = []Previous{linked(spec, "old", waitBoundary, 1140)}
	prepare(t, c, spec)
	return &waitFixture{t: t, c: c, store: store, now: now, schedule: scheduleFor(t, "new", waitBoundary, nil)}
}

func (fixture *waitFixture) at(seconds int64) {
	*fixture.now = time.Unix(seconds, 0)
}

func (fixture *waitFixture) ask() (time.Duration, error) {
	return fixture.c.SlotReadHold(context.Background(), fixture.schedule, waitBoundary, holdFence("new"))
}

// awaiting asks for the first Slot's hold and wants it not frozen: waiting
// on "old" for reason, until the instant given.
func (fixture *waitFixture) awaiting(reason string, untilMillis int64) {
	fixture.t.Helper()
	hold, err := fixture.ask()
	var awaiting *AwaitingPredecessorError
	if !errors.As(err, &awaiting) || awaiting.QueryGroup != "new" || awaiting.Predecessor != "old" ||
		awaiting.Reason != reason || awaiting.UntilUnixMilli != untilMillis {
		fixture.t.Fatalf("first Slot at %s = %s %v, want it waiting on old (%s) until %d", fixture.now.Format(time.TimeOnly),
			hold, err, reason, untilMillis)
	}
	if want := min(fixture.now.UnixMilli()+PredecessorRetry.Milliseconds(), untilMillis); awaiting.RetryAt.UnixMilli() != want {
		fixture.t.Fatalf("retry at %d, want %d", awaiting.RetryAt.UnixMilli(), want)
	}
	if _, written := fixture.store.values["new"]; written {
		fixture.t.Fatal("a waiting group wrote its record")
	}
	if waiting := fixture.c.Stats().WaitingGroups; waiting != 1 {
		fixture.t.Fatalf("waiting groups %d, want 1", waiting)
	}
}

// frozen asks for the first Slot's hold and wants it frozen at want.
func (fixture *waitFixture) frozen(want time.Duration) {
	fixture.t.Helper()
	if hold, err := fixture.ask(); err != nil || hold != want {
		fixture.t.Fatalf("first Slot at %s = %s %v, want frozen at %s", fixture.now.Format(time.TimeOnly), hold, err, want)
	}
	if waiting := fixture.c.Stats().WaitingGroups; waiting != 0 {
		fixture.t.Fatalf("waiting groups %d after the Slot froze, want 0", waiting)
	}
}

// closeOld closes "old" at the boundary as its owner does.
func (fixture *waitFixture) closeOld() {
	fixture.t.Helper()
	end := waitBoundary
	if err := fixture.c.CloseSchedule(context.Background(), scheduleFor(fixture.t, "old", 60, &end), holdFence("old")); err != nil {
		fixture.t.Fatal(err)
	}
}

// counts wants the waits and the predecessors' reasons counted so far.
func (fixture *waitFixture) counts(waits map[string]uint64, predecessors map[string]uint64) {
	fixture.t.Helper()
	stats := fixture.c.Stats()
	for _, outcome := range TransitionWaitOutcomes {
		if stats.TransitionWaits[outcome] != waits[outcome] {
			fixture.t.Fatalf("waits %v, want %v", stats.TransitionWaits, waits)
		}
	}
	if !reflect.DeepEqual(stats.Predecessors, predecessors) {
		fixture.t.Fatalf("predecessors %v, want %v", stats.Predecessors, predecessors)
	}
}

// A successor whose predecessor's record is still open waits, frozen and
// counted only once, and reads it again until it closes. Closed four periods
// late, the hold covers the wait - the Slot is ready the moment it freezes,
// not long before as on the closed record's deadline alone, which would put
// it more Slots behind than a replay covers - and nothing waits any more.
func TestASuccessorWaitsOnAnOpenPredecessorAndCoversTheWaitWhenItClosesLate(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.at(1231)
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1}, map[string]uint64{})
	fixture.at(1236)
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1}, map[string]uint64{})

	fixture.at(1471)
	fixture.closeOld()
	// Ready at 1471 s: 1200 s + 30 s + 241 s.
	fixture.frozen(241 * time.Second)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitInherited: 1},
		map[string]uint64{PredecessorInherited: 1})
	// Seeded: the next Slot reads its own record, not the predecessor again.
	fixture.at(1472)
	fixture.frozen(241 * time.Second)
}

// A predecessor already closed at the first reading did not hold the
// successor up: a first Slot asked late, for a reason of its own, keeps the
// closed record's deadline, as it always did, without its lateness in it.
func TestAPredecessorClosedAtTheFirstReadIsTakenAsItWasForALateSuccessor(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.closeOld()
	fixture.at(1500)
	// 1345 s - 1230 s.
	fixture.frozen(115 * time.Second)
	fixture.counts(map[string]uint64{}, map[string]uint64{PredecessorInherited: 1})
}

// The wait ends when the Slot would have been ready without it, under the
// group's own limit: a 2 m limit makes that 1200 s + 30 s + 120 s, before the
// bound's 1795 s. Waiting to the bound, the Slot would only be cut to the
// limit then, ready long after its time.
func TestAWaitEndsWhenTheSlotWouldHaveBeenReadyUnderItsLimit(t *testing.T) {
	fixture := newWaitFixture(t, 2*time.Minute)
	fixture.at(1231)
	fixture.awaiting(PredecessorRecordOpen, 1_350_000)
	fixture.at(1349)
	fixture.awaiting(PredecessorRecordOpen, 1_350_000)
	fixture.at(1350)
	fixture.frozen(2 * time.Minute)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitBounded: 1},
		map[string]uint64{PredecessorRecordOpen: 1})
	if clamped := fixture.c.Stats().Clamped; clamped[ClampFallback] != 1 {
		t.Fatalf("clamped %v, want the bound cut to the limit once", clamped)
	}
}

// A predecessor whose record could not be read is waited on as an open one:
// read a few rounds later, it is inherited, and no unreadable record is
// counted for a read that came back.
func TestAnUnreadablePredecessorIsWaitedOnAndInheritedOnceRead(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.store.readErr["old"] = errors.New("unanswered")
	_ = fixture.c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"})
	fixture.at(1231)
	fixture.awaiting(PredecessorUnreadable, 1_795_000)
	fixture.at(1236)
	_ = fixture.c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"})
	fixture.awaiting(PredecessorUnreadable, 1_795_000)

	delete(fixture.store.readErr, "old")
	fixture.at(1241)
	if err := fixture.c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"}); err != nil {
		t.Fatal(err)
	}
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	fixture.closeOld()
	fixture.at(1250)
	// Closed at 1250 s, before the record's 1345 s: the record's deadline.
	fixture.frozen(115 * time.Second)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitInherited: 1},
		map[string]uint64{PredecessorInherited: 1})
}

// A predecessor no read answers is waited on until the Slot would have been
// ready on the bound, and then the bound is frozen, as it always was: the
// wait has an end.
func TestAPredecessorNeverReadFreezesTheBoundWhenTheWaitEnds(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.store.readErr["old"] = errors.New("unanswered")
	_ = fixture.c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"})
	fixture.at(1231)
	fixture.awaiting(PredecessorUnreadable, 1_795_000)
	fixture.at(1794)
	fixture.awaiting(PredecessorUnreadable, 1_795_000)
	fixture.at(1795)
	// 1795 s - 1230 s.
	fixture.frozen(565 * time.Second)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitBounded: 1},
		map[string]uint64{PredecessorUnreadable: 1})
}

// A corrupt record will not read better for waiting: the bound is frozen at
// once, and no wait begins. Nor does a first Slot asked after it would have
// been ready on the bound wait for an open one.
func TestACorruptOrLatePredecessorFreezesTheBoundAtOnce(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.store.values["old"] = []byte("{not a record")
	_ = fixture.c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"})
	fixture.at(1231)
	fixture.frozen(565 * time.Second)
	fixture.counts(map[string]uint64{}, map[string]uint64{PredecessorRecordCorrupt: 1})

	fixture = newWaitFixture(t, 10*time.Minute)
	fixture.at(1800)
	fixture.frozen(565 * time.Second)
	fixture.counts(map[string]uint64{}, map[string]uint64{PredecessorRecordOpen: 1})
}

// A predecessor forgotten while the successor waits - released by this
// Worker, its state dropped - is not one that failed to read: the successor
// waits on, reads it again on its next round, and counts neither a second
// wait nor an unreadable record. Never read again, the wait still ends, on the
// bound for what the predecessor last read.
func TestAForgottenPredecessorIsReadAgainNotTakenForUnreadable(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.at(1231)
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	fixture.c.Forget("old")
	fixture.at(1236)
	fixture.awaiting(AwaitingUnread, 1_795_000)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1}, map[string]uint64{})

	// Its owner, here again, closes it; the next round reads it.
	prepare(t, fixture.c, groupSpec("old", 0))
	fixture.closeOld()
	fixture.at(1241)
	fixture.frozen(115 * time.Second)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitInherited: 1},
		map[string]uint64{PredecessorInherited: 1})

	fixture = newWaitFixture(t, 10*time.Minute)
	fixture.at(1231)
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	fixture.c.Forget("old")
	fixture.at(1795)
	fixture.frozen(565 * time.Second)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1, TransitionWaitBounded: 1},
		map[string]uint64{PredecessorRecordOpen: 1})
}

// A group configured again for links it no longer has forgets their waits:
// it is not counted waiting, and its next Slot, with no predecessor left,
// freezes at once and ends no wait.
func TestAGroupConfiguredWithoutItsPredecessorForgetsTheWait(t *testing.T) {
	fixture := newWaitFixture(t, 10*time.Minute)
	fixture.at(1231)
	fixture.awaiting(PredecessorRecordOpen, 1_795_000)
	spec := groupSpec("new", 0)
	if err := fixture.c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if waiting := fixture.c.Stats().WaitingGroups; waiting != 0 {
		t.Fatalf("waiting groups %d after the predecessor left the spec, want 0", waiting)
	}
	fixture.frozen(0)
	fixture.counts(map[string]uint64{TransitionWaitWaiting: 1}, map[string]uint64{})
}
