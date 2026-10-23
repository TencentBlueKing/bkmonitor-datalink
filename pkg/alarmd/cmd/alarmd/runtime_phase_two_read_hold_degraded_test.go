package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// degradedCounts is the fixture's degraded Slots by reason, the zero ones
// left out.
func degradedCounts(f *cutoverStallFixture) map[string]uint64 {
	holds := f.bundle.dependencies.ReadHolds
	holds.degradedMu.Lock()
	defer holds.degradedMu.Unlock()
	counts := map[string]uint64{}
	for reason, count := range holds.degraded {
		if count > 0 {
			counts[reason] = count
		}
	}
	return counts
}

// Ownership and the group's own record unread refuse the Slot as before;
// everything else the hold fails on is a degradation of it, which the
// scheduler does not refuse the Slot for, and which still says its cause.
func TestOnlyOwnershipAndAnUnreadRecordRefuseASlot(t *testing.T) {
	cause := errors.New("catalog object unavailable")
	for _, tc := range []struct {
		err     error
		refuses bool
	}{
		{ownership.ErrStaleFence, true},
		{fmt.Errorf("persist: %w", ownership.ErrStaleFence), true},
		{ownership.ErrContentScopeMoved, true},
		{readhold.ErrNotRestored, true},
		{cause, false},
		{readhold.ErrConflict, false},
		{readhold.ErrSegmentStale, false},
		{readhold.ErrNotConfigured, false},
	} {
		err := degraded(readhold.DegradedSpecUnreadable, tc.err)
		if refuses := !errors.Is(err, scheduler.ErrReadHoldDegraded); refuses != tc.refuses || !errors.Is(err, tc.err) {
			t.Fatalf("%v: refuses=%t, want %t; cause kept=%t", tc.err, refuses, tc.refuses, errors.Is(err, tc.err))
		}
	}
	once := degraded(readhold.DegradedSpecUnreadable, cause)
	if again := degraded(readhold.DegradedHoldFailed, once); again != once {
		t.Fatalf("a degradation is degraded again under another reason: %v", again)
	}
	if degraded(readhold.DegradedSpecUnreadable, nil) != nil {
		t.Fatal("no error is not a degradation")
	}
}

// A group whose route and delay cannot be read -- its Segment names a Query
// Group object that is not there -- is not refused its Slot: the hold is
// the one its record carries, and the Slot is counted. Before, the group
// stopped until the object came back.
func TestAGroupWhoseRouteIsUnreadableIsHeldAsItsRecordSays(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	next := f.progress(ctx).NextSlot
	current, err := f.production.dependencies.Catalog.ReadFrozenSchedule(ctx, f.queryGroup, next)
	if err != nil {
		t.Fatal(err)
	}
	writeHoldRecord(t, f, f.queryGroup, readhold.Record{SinceSlot: 1, HoldMillis: 60_000, SegmentStart: current.Segment.Start})
	holds := f.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	group := holds.groups[f.queryGroup]
	group.prepared, group.queryRoute, group.queryDelay = execution.ScheduleSegmentFact{}, "", 0
	holds.mu.Unlock()
	holds.controller.Forget(f.queryGroup)
	unreadable := current
	unreadable.Segment.ObjectDigest = execution.ObjectDigest(strings.Repeat("0", 64))
	lease, _ := group.session.Current()
	if err := holds.PrepareSchedule(ctx, unreadable, lease.Fence); !errors.Is(err, scheduler.ErrReadHoldDegraded) ||
		!errors.Is(err, controlplane.ErrCatalogObjectUnavailable) {
		t.Fatalf("an unreadable route is not a degraded hold that keeps its cause: %v", err)
	}
	hold, err := holds.SlotReadHold(ctx, unreadable, next, lease.Fence)
	if err != nil || hold != time.Minute {
		t.Fatalf("the Slot was not held as the record says: %v %v", hold, err)
	}
	if counts := degradedCounts(f); counts[readhold.DegradedSpecUnreadable] != 1 || len(counts) != 1 {
		t.Fatalf("the degraded Slot was not counted once under its reason: %v", counts)
	}
	// A record past the schedule's hold limit stands in no further than it.
	writeHoldRecord(t, f, f.queryGroup, readhold.Record{SinceSlot: 1, HoldMillis: (2 * time.Hour).Milliseconds(), SegmentStart: current.Segment.Start})
	holds.controller.Forget(f.queryGroup)
	limit := holds.holdLimit(current)
	if hold, err := holds.SlotReadHold(ctx, unreadable, next, lease.Fence); err != nil || hold != limit || limit >= 2*time.Hour {
		t.Fatalf("the stand-in hold %v is not the limit %v: %v", hold, limit, err)
	}
}

// A record already past the Segment the group's cursor is in: its Slots
// cannot be written into it. They run on with the record's hold, counted;
// before, every one of them was refused.
func TestAGroupWhoseRecordIsPastItsSegmentRunsAtTheHoldItLastRead(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	current, err := f.production.dependencies.Catalog.ReadFrozenSchedule(ctx, f.queryGroup, f.progress(ctx).NextSlot)
	if err != nil {
		t.Fatal(err)
	}
	writeHoldRecord(t, f, f.queryGroup, readhold.Record{SinceSlot: 1, HoldMillis: 60_000, SegmentStart: current.Segment.Start + 86_400})
	after := restartUntilFull(t, f, f.queryGroup)
	if counts := degradedCounts(f); counts[readhold.DegradedStaleSegment] == 0 {
		t.Fatalf("the stale Segment was not counted: %v", counts)
	}
	if after.LastCompletion == nil || after.LastCompletion.Contract.ReadHoldMillis != 60_000 {
		t.Fatalf("the Slot was not frozen with the hold the group last read: %+v", after.LastCompletion)
	}
}

// A Segment naming another Query Group's object is no route for this one:
// the hold is degraded, not configured with the other group's query.
func TestAnObjectOfAnotherGroupIsNoRoute(t *testing.T) {
	ctx := context.Background()
	f, old, next, _ := delayEdited(t)
	schedule, err := f.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	oldSchedule, err := f.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, old)
	if err != nil || oldSchedule.Segment.ObjectDigest == schedule.Segment.ObjectDigest {
		t.Fatalf("the two groups do not name two objects: %v", err)
	}
	holds := f.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	group := holds.groups[next]
	holds.mu.Unlock()
	if group == nil {
		t.Fatal("the moved Plan's group is not held here; the fixture does not test it")
	}
	group.mu.Lock()
	group.prepared, group.queryRoute, group.queryDelay = execution.ScheduleSegmentFact{}, "", 0
	group.mu.Unlock()
	borrowed := schedule
	borrowed.Segment.ObjectDigest = oldSchedule.Segment.ObjectDigest
	lease, _ := group.session.Current()
	if err := holds.PrepareSchedule(ctx, borrowed, lease.Fence); !errors.Is(err, scheduler.ErrReadHoldDegraded) {
		t.Fatalf("another group's object was taken for this group's route: %v", err)
	}
}

// A group whose record Redis answers with an error of its own -- here the
// key holds a list, so every read of it is WRONGTYPE -- runs on with hold 0,
// counted, instead of being refused as unrestored for as long as the key
// answers so; and restores at the next Slot once it reads again.
func TestAGroupWhoseRecordRedisAnswersWithAnErrorRunsAtTheHoldItLastRead(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	key := holdRecordKey(f, f.queryGroup)
	if err := f.redisClient.RPush(ctx, key, "not a record").Err(); err != nil {
		t.Fatal(err)
	}
	after := restartUntilFull(t, f, f.queryGroup)
	if counts := degradedCounts(f); counts[readhold.DegradedRecordUnreadable] == 0 {
		t.Fatalf("the unreadable record was not counted: %v", counts)
	}
	if after.LastCompletion == nil || after.LastCompletion.Contract.ReadHoldMillis != 0 {
		t.Fatalf("the Slot was not frozen with the hold the group last read: %+v", after.LastCompletion)
	}
	if err := f.redisClient.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	before := degradedCounts(f)[readhold.DegradedRecordUnreadable]
	_ = runOneSlotFull(t, f)
	if got := degradedCounts(f)[readhold.DegradedRecordUnreadable]; got != before || !f.bundle.dependencies.ReadHolds.controller.Inspect(f.queryGroup).Loaded {
		t.Fatalf("the record readable again was not restored: counted %d -> %d", before, got)
	}
}

// A read of the record that gets no answer at all is the Redis the Slot
// needs anyway: it still refuses, to be retried, and is not taken for a
// record Redis answered with an error.
func TestARecordReadWithoutAnAnswerStillRefuses(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	current, err := f.production.dependencies.Catalog.ReadFrozenSchedule(ctx, f.queryGroup, f.progress(ctx).NextSlot)
	if err != nil {
		t.Fatal(err)
	}
	holds := f.bundle.dependencies.ReadHolds
	holds.mu.Lock()
	group := holds.groups[f.queryGroup]
	holds.mu.Unlock()
	group.mu.Lock()
	group.prepared = execution.ScheduleSegmentFact{}
	group.mu.Unlock()
	holds.controller.Forget(f.queryGroup)
	lease, _ := group.session.Current()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := holds.PrepareSchedule(canceled, current, lease.Fence); !errors.Is(err, readhold.ErrNotRestored) || errors.Is(err, scheduler.ErrReadHoldDegraded) {
		t.Fatalf("an unanswered record read did not refuse as unrestored: %v", err)
	}
}

// failingTimeline fails the read of one evaluation time of the group's own
// timeline and passes every other through.
type failingTimeline struct {
	readHoldTimeline
	at  execution.EvaluationTime
	err error
}

func (timeline failingTimeline) ReadFrozenSchedule(ctx context.Context, qg execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	if at == timeline.at {
		return execution.FrozenQueryGroupSchedule{}, timeline.err
	}
	return timeline.readHoldTimeline.ReadFrozenSchedule(ctx, qg, at)
}

// failingLinks fails the read of the group's predecessor links.
type failingLinks struct {
	readHoldCatalog
	err error
}

func (links failingLinks) ReadHoldPredecessors(context.Context, execution.FrozenQueryGroupSchedule) ([]controlplane.ReadHoldPredecessor, map[string]int, error) {
	return nil, nil, links.err
}

// Each place a prepare can fail for a reason of the hold's own degrades the
// hold under its own reason, and the Slot is frozen with the record's hold;
// a refusal at any one of them would stop the group as long as it lasts.
func TestEachPrepareFailureOfTheHoldsOwnDegradesUnderItsReason(t *testing.T) {
	failure := errors.New("read failed")
	for _, tc := range []struct {
		reason string
		fail   func(*productionReadHolds, *productionReadHoldGroup, execution.FrozenQueryGroupSchedule)
	}{
		{readhold.DegradedPreviousUnreadable, func(holds *productionReadHolds, _ *productionReadHoldGroup, current execution.FrozenQueryGroupSchedule) {
			holds.catalog = failingTimeline{readHoldTimeline: holds.catalog, at: current.Segment.Start - 1, err: failure}
		}},
		{readhold.DegradedPredecessorsUnreadable, func(holds *productionReadHolds, _ *productionReadHoldGroup, _ execution.FrozenQueryGroupSchedule) {
			holds.repository = failingLinks{readHoldCatalog: holds.repository, err: failure}
		}},
		// The group already prepared a later Segment than the open one asked.
		{readhold.DegradedStaleSegment, func(_ *productionReadHolds, group *productionReadHoldGroup, current execution.FrozenQueryGroupSchedule) {
			later := current.Segment
			later.Start += 3600
			group.prepared = later
		}},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			ctx := context.Background()
			f := startCutoverFixture(t, nil)
			_ = runOneSlotFull(t, f)
			next := f.progress(ctx).NextSlot
			current, err := f.production.dependencies.Catalog.ReadFrozenSchedule(ctx, f.queryGroup, next)
			if err != nil {
				t.Fatal(err)
			}
			writeHoldRecord(t, f, f.queryGroup, readhold.Record{SinceSlot: 1, HoldMillis: 60_000, SegmentStart: current.Segment.Start})
			holds := f.bundle.dependencies.ReadHolds
			holds.mu.Lock()
			group := holds.groups[f.queryGroup]
			holds.mu.Unlock()
			group.mu.Lock()
			group.prepared = execution.ScheduleSegmentFact{}
			group.mu.Unlock()
			holds.controller.Forget(f.queryGroup)
			tc.fail(holds, group, current)
			lease, _ := group.session.Current()
			var degradation *readHoldDegradation
			if err := holds.PrepareSchedule(ctx, current, lease.Fence); !errors.As(err, &degradation) || degradation.reason != tc.reason {
				t.Fatalf("the prepare was not degraded under %s: %v", tc.reason, err)
			}
			if hold, err := holds.SlotReadHold(ctx, current, next, lease.Fence); err != nil || hold != time.Minute {
				t.Fatalf("the Slot was not held as the record says: %v %v", hold, err)
			}
			if counts := degradedCounts(f); counts[tc.reason] != 1 || len(counts) != 1 {
				t.Fatalf("the degraded Slot was not counted once under %s: %v", tc.reason, counts)
			}
		})
	}
}
