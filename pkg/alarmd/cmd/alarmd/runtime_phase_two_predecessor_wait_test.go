// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The index says an object's next Slot waits on its predecessor while the
// bound it holds is that wait, and says nothing of it once a round writes
// another: the due time alone, five seconds on, reads as an object about to
// run.
func TestTheDueIndexSaysAnObjectWaitsOnItsPredecessor(t *testing.T) {
	index := newPhaseTwoDueIndex(metric.NewRecorder(metric.BuildInfo{}))
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: walkRunner{}}
	at := time.Unix(1_000_000, 0)
	awaiting := &readhold.AwaitingPredecessorError{QueryGroup: "query-group-b", Predecessor: "query-group-a",
		Reason: readhold.PredecessorRecordOpen, SinceUnixMilli: at.UnixMilli() - 60_000, UntilUnixMilli: at.UnixMilli() + 540_000,
		RetryAt: at.Add(5 * time.Second)}
	index.Record("query-group-b", lifecycle, 0, scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictNotDue,
		NotDueUntilUnix: at.Unix() + 5, NextSlotUnix: at.Unix() - 61, IntervalSeconds: 60, AwaitingPredecessor: awaiting}, at)
	wake := index.WakeOf("query-group-b")
	want := fleet.PredecessorWait{QueryGroup: "query-group-a", Reason: readhold.PredecessorRecordOpen,
		Since: at.Add(-time.Minute), Until: at.Add(9 * time.Minute)}
	if wake.AwaitingPredecessor == nil || *wake.AwaitingPredecessor != want || !wake.DueAt.Equal(at.Add(5*time.Second)) {
		t.Fatalf("wake %+v (awaiting %+v), want due at its next look and waiting %+v", wake, wake.AwaitingPredecessor, want)
	}

	index.Record("query-group-b", lifecycle, 0, scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictNotDue,
		NotDueUntilUnix: at.Unix() + 60, NextSlotUnix: at.Unix(), IntervalSeconds: 60}, at.Add(time.Second))
	if wake := index.WakeOf("query-group-b"); wake.AwaitingPredecessor != nil {
		t.Fatalf("wake %+v still waits once its Slot is frozen", wake.AwaitingPredecessor)
	}
}

// predecessorWaitFixture is the shape that left a new Query Group stalled
// for about ten minutes after a query edit: strategy 1001's group holds its
// reads 39.5 s for data it saw land 40 s late, the strategy's query is edited
// so that its Plan moves to a new group and the old one retires, and the new
// group's first Slot reads the old group's read hold record.
type predecessorWaitFixture struct {
	*cutoverStallFixture
	old, next execution.QueryGroupIdentity
	oldRunner phaseTwoQueryGroupRuntime
	// held is the old group's Slot frozen with the hold, base+60.
	held execution.EvaluationTime
}

func newPredecessorWaitFixture(t *testing.T) *predecessorWaitFixture {
	t.Helper()
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	holds := f.bundle.dependencies.ReadHolds
	last := f.progress(ctx).LastCompletion
	if err := holds.controller.Observe(ctx, readhold.Evidence{Contract: last.Contract, ArrivalAge: 40 * time.Second,
		Rung: lookback.RungNames[0], Buckets: []int64{int64(last.Contract.Slot.EvaluationTime)}}); err != nil {
		t.Fatal(err)
	}
	if hold := holds.controller.ReadHold(f.queryGroup); hold != 39500*time.Millisecond {
		t.Fatalf("fixture: the old group holds %s, want 39.5 s", hold)
	}
	fixture := &predecessorWaitFixture{cutoverStallFixture: f, old: f.queryGroup, oldRunner: f.runner,
		held: execution.EvaluationTime(f.base + 60)}
	// Its first round: frozen with the hold, and turned away until read.
	f.clock.Store(firstRoundAtMillis(f.cfg, fixture.old, int64(fixture.held), 60))
	if _, _, err := fixture.oldRunner.RunOne(ctx); err != nil {
		t.Fatal(err)
	}
	if unfinished := f.progress(ctx).UnfinishedSlot; unfinished == nil || unfinished.Contract.ReadHoldMillis != 39_500 {
		t.Fatalf("fixture: the old group's Slot %d is not frozen with its hold: %+v", fixture.held, unfinished)
	}
	return fixture
}

// runOld runs the old group's held Slot to its end.
func (fixture *predecessorWaitFixture) runOld() {
	fixture.t.Helper()
	ctx := context.Background()
	for attempt := 0; attempt < 20; attempt++ {
		if nextAt := fixture.oldRunner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		result, attempted, err := fixture.oldRunner.RunOne(ctx)
		if err != nil {
			fixture.t.Fatalf("the old group's Slot: %v", err)
		}
		if attempted && result.Completed {
			progress := loadPhaseTwoProgress(fixture.t, ctx, fixture.production, fixture.old)
			if progress.LastCompletion == nil || progress.LastCompletion.Slot != fixture.held || progress.LastCompletion.Contract.ReadHoldMillis != 39_500 {
				fixture.t.Fatalf("the old group completed %+v, want its held Slot %d", progress.LastCompletion, fixture.held)
			}
			return
		}
	}
	fixture.t.Fatal("the old group's held Slot did not complete")
}

// editQuery edits strategy 1001's query at the clock's second, which is the
// cutover's boundary, and opens the group its Plan moved to.
func (fixture *predecessorWaitFixture) editQuery() execution.EvaluationTime {
	fixture.t.Helper()
	ctx := context.Background()
	boundary := execution.EvaluationTime(fixture.now().Unix())
	installEditedStrategies(fixture.t, ctx, fixture.redisClient, 1725000600, func(first map[string]any) {
		first["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["agg_method"] = "max"
	})
	fixture.clock.Store(int64(boundary) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			fixture.t.Fatal(err)
		}
	}
	for _, candidate := range fixture.bundle.queryGroups {
		schedule, err := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, candidate)
		if err == nil && candidate != fixture.old && len(schedule.Plans) == 1 && schedule.Plans[0].Key() == fixture.initialSchedule.Plans[0].Key() {
			fixture.next = candidate
		}
	}
	if fixture.next == "" {
		fixture.t.Fatal("the query edit did not move the Plan to a new Query Group")
	}
	fixture.queryGroup = fixture.next
	fixture.runner = settledRunner(fixture.bundle, fixture.next)
	return boundary
}

// first is the new group's first Slot: the grid point after the boundary.
func (fixture *predecessorWaitFixture) first(boundary execution.EvaluationTime) execution.EvaluationTime {
	return execution.EvaluationTime((int64(boundary)/60 + 1) * 60)
}

// link is what the new group knows of the old group's last Slot: when it was
// and its completion offset.
func (fixture *predecessorWaitFixture) link() (execution.EvaluationTime, int64) {
	fixture.t.Helper()
	ctx := context.Background()
	schedule, err := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, fixture.next)
	if err != nil {
		fixture.t.Fatal(err)
	}
	links, _, err := fixture.repository.ReadHoldPredecessors(ctx, schedule)
	if err != nil || len(links) != 1 || links[0].QueryGroup != fixture.old || len(links[0].Plans) != 1 || links[0].Plans[0].PreviousSlot != fixture.held {
		fixture.t.Fatalf("the new group's link to the old one = %+v %v, want its last Slot %d", links, err, fixture.held)
	}
	return links[0].Plans[0].PreviousSlot, links[0].Plans[0].CompletionOffsetMillis
}

// round runs one round of the new group at the clock's instant and records
// its bound in the due index, as the dispatcher does.
func (fixture *predecessorWaitFixture) round() (execution.SlotExecutionResult, bool) {
	fixture.t.Helper()
	ctx := context.Background()
	result, attempted, err := fixture.runner.RunOne(ctx)
	if err != nil {
		fixture.t.Fatalf("the new group's round at %s: %v", fixture.now().UTC().Format(time.TimeOnly), err)
	}
	fixture.bundle.mu.RLock()
	lifecycle := fixture.bundle.runners[fixture.next]
	fixture.bundle.mu.RUnlock()
	fixture.bundle.dueIndex.Record(fixture.next, lifecycle, 0, lifecycle.runner.DueBound(), fixture.now())
	return result, attempted
}

// awaiting wants the new group's last round to have waited on the old group,
// as its bound, the due index and fleet's first-round waits say it, and as
// the read hold's gauge counts it, until the instant given.
func (fixture *predecessorWaitFixture) awaiting(untilMillis int64) {
	fixture.t.Helper()
	fixture.bundle.mu.RLock()
	lifecycle := fixture.bundle.runners[fixture.next]
	fixture.bundle.mu.RUnlock()
	bound := lifecycle.runner.DueBound()
	if awaiting := bound.AwaitingPredecessor; awaiting == nil || awaiting.Predecessor != fixture.old ||
		awaiting.Reason != readhold.PredecessorRecordOpen || awaiting.UntilUnixMilli != untilMillis {
		fixture.t.Fatalf("the new group's bound %+v (waiting %+v), want it waiting on %s until %d", bound, awaiting, fixture.old, untilMillis)
	}
	wake := fixture.bundle.dueIndex.WakeOf(string(fixture.next))
	if wake.AwaitingPredecessor == nil || wake.AwaitingPredecessor.QueryGroup != string(fixture.old) ||
		wake.AwaitingPredecessor.Until.UnixMilli() != untilMillis {
		fixture.t.Fatalf("the due index says %+v, want the wait on %s", wake.AwaitingPredecessor, fixture.old)
	}
	publisher := &fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", fixture.now), replica: "replica-1", now: fixture.now,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{fixture.next} }, schedule: fixture.bundle.dueIndex}
	snapshot := publisher.snapshot(context.Background())
	if len(snapshot.AwaitingFirstRound) != 1 || snapshot.AwaitingFirstRound[0].AwaitingPredecessor == nil ||
		snapshot.AwaitingFirstRound[0].AwaitingPredecessor.QueryGroup != string(fixture.old) {
		fixture.t.Fatalf("fleet's first-round waits %+v, want the new group waiting on %s", snapshot.AwaitingFirstRound, fixture.old)
	}
	if waiting := fixture.bundle.dependencies.ReadHolds.controller.Stats().WaitingGroups; waiting != 1 {
		fixture.t.Fatalf("read hold waiting groups %d, want 1", waiting)
	}
}

// completion is how the new group's Slot completed, and with which hold.
func (fixture *predecessorWaitFixture) completion(slot execution.EvaluationTime) (string, int64) {
	fixture.t.Helper()
	ended, ok := fixture.slotEndings()[slot]
	if !ok {
		return "", 0
	}
	if last := fixture.progress(context.Background()).LastCompletion; last != nil && last.Slot == slot {
		return ended, last.Contract.ReadHoldMillis
	}
	return ended, -1
}

// runNewUntil runs the new group's rounds, each at its next ready instant,
// until slot has ended.
func (fixture *predecessorWaitFixture) runNewUntil(slot execution.EvaluationTime) {
	fixture.t.Helper()
	for attempt := 0; attempt < 400; attempt++ {
		if _, ended := fixture.slotEndings()[slot]; ended {
			return
		}
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if _, attempted := fixture.round(); !attempted {
			fixture.clock.Store(fixture.now().Add(time.Second).UnixMilli())
		}
	}
	fixture.t.Fatalf("the new group's Slot %d did not end; endings %v", slot, fixture.slotEndings())
}

// inheritedHold is the hold the new group's first Slot is frozen with once
// it inherits the old group's: the old group's last Slot due 39.5 s past its
// offset, read after the new group's 0.5 s settling wait. With the old group
// still held here (peered), the new group, reading the same data, seeds its
// own hold from the old group's 40 s arrival age, 39.5 s, which is more.
func (fixture *predecessorWaitFixture) inheritedHold(first execution.EvaluationTime, peered bool) int64 {
	previous, offset := fixture.link()
	hold := int64(previous)*1000 + 39_500 + offset - int64(first)*1000 - 500
	if peered {
		hold = max(hold, 39_500)
	}
	return hold
}

// releaseOld runs the leader's rounds until the drained old group is let go:
// handed over, its read hold closed or not, and forgotten here.
func (fixture *predecessorWaitFixture) releaseOld() {
	fixture.t.Helper()
	released := func() bool {
		fixture.bundle.mu.RLock()
		defer fixture.bundle.mu.RUnlock()
		_, owned := fixture.bundle.runners[fixture.old]
		return !owned && !fixture.bundle.dependencies.ReadHolds.controller.Inspect(fixture.old).Loaded
	}
	// The handover runs on its own goroutine once a leader round drops the
	// group from this Worker's assignment.
	testwait.Until(fixture.t, "the drained old group is released", func() bool {
		if err := fixture.bundle.refreshAndReconcile(context.Background(), true); err != nil {
			fixture.t.Fatal(err)
		}
		return released()
	})
}

// retireCloses is the retired groups' read hold closes by site, and the
// closes that failed, as the bundle's metrics read them.
func (fixture *predecessorWaitFixture) retireCloses() (map[string]float64, float64) {
	fixture.t.Helper()
	recorder := fixture.bundle.dependencies.Recorder
	closes := map[string]float64{}
	for _, site := range readhold.RetireCloseSites {
		closes[site] = counterValue(fixture.t, recorder, "alarmd_read_hold_retire_closes_total", map[string]string{"site": site})
	}
	return closes, counterValue(fixture.t, recorder, "alarmd_read_hold_retire_close_failed_total", nil)
}

func (fixture *predecessorWaitFixture) predecessorCounts() (map[string]uint64, map[string]uint64) {
	stats := fixture.bundle.dependencies.ReadHolds.controller.Stats()
	return stats.Predecessors, stats.TransitionWaits
}

// The old group's held Slot completes before the edit, so the old group is
// drained at the cutover, and it is released without a round coming back to
// it. Released, it closes its record first: the new group's first Slot reads
// the hold the old group's last Slot was frozen with, inherited, instead of
// the ten-minute bound an open record stood for, and never waits.
func TestADrainedGroupClosesItsHoldWhenReleasedForTheGroupItsPlanMovedTo(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	fixture.runOld()
	fixture.clock.Store((int64(fixture.held) + 59) * 1000)
	boundary := fixture.editQuery()
	fixture.releaseOld()
	// As /metrics reads it: closed once, by the release, and nothing failed.
	if closes, failed := fixture.retireCloses(); closes[readhold.RetireCloseRelease] != 1 || closes[readhold.RetireCloseNext] != 0 || failed != 0 {
		t.Fatalf("retire closes %v, failed %v; want the drained group closed once, by its release", closes, failed)
	}

	first := fixture.first(boundary)
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60))
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	// Released, the old group seeds nothing.
	if want := fixture.inheritedHold(first, false); ended != fullEnding || hold != want {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL with the %d ms the old group's hold leaves", ended, hold, want)
	}
	predecessors, waits := fixture.predecessorCounts()
	if predecessors[readhold.PredecessorInherited] != 1 || predecessors[readhold.PredecessorRecordOpen] != 0 ||
		waits[readhold.TransitionWaitWaiting] != 0 {
		t.Fatalf("predecessors %v waits %v, want one inherited and no wait", predecessors, waits)
	}
}

// The edit lands while the old group's held Slot is in flight, so the new
// group's first Slot finds the old record open. It waits - not frozen, its
// wait on every reader's page - and the old Slot completes, the old group
// closes its record on its next round, and the new group's next look
// inherits the old hold: ready when the old group's last Slot was due, not
// ten minutes later on the bound.
func TestASuccessorWaitsForItsPredecessorsLastSlotNotForTheBound(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	boundary := fixture.editQuery()
	first := fixture.first(boundary)
	previous, offset := fixture.link()
	until := min(int64(previous)*1000+600_000+offset, int64(first)*1000+500+600_000)

	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60))
	if _, attempted := fixture.round(); attempted {
		t.Fatal("the new group's first Slot ran with its predecessor's record open")
	}
	fixture.awaiting(until)

	// The old Slot completes, and the old group's next round closes it.
	fixture.runOld()
	fixture.clock.Store(fixture.now().Add(time.Second).UnixMilli())
	if _, _, err := fixture.oldRunner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	if want := fixture.inheritedHold(first, true); ended != fullEnding || hold != want {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL with the %d ms the old group's hold leaves", ended, hold, want)
	}
	predecessors, waits := fixture.predecessorCounts()
	if predecessors[readhold.PredecessorInherited] != 1 || predecessors[readhold.PredecessorRecordOpen] != 0 ||
		waits[readhold.TransitionWaitWaiting] != 1 || waits[readhold.TransitionWaitInherited] != 1 || waits[readhold.TransitionWaitBounded] != 0 {
		t.Fatalf("predecessors %v waits %v, want one wait, ended inherited", predecessors, waits)
	}
}

// The old group's held Slot drags on four periods past the new group's first
// Slot before it completes and the old group closes its record. The new
// group's hold covers the time it waited: frozen the moment it reads the
// record closed, it is ready then, and every Slot it fell behind by runs -
// on the old record's deadline alone, it would have been more Slots behind
// than a replay covers, and skipped them.
func TestASuccessorWhosePredecessorClosesLateLosesNoSlot(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	boundary := fixture.editQuery()
	first := fixture.first(boundary)
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60))
	if _, attempted := fixture.round(); attempted {
		t.Fatal("the new group's first Slot ran with its predecessor's record open")
	}

	fixture.clock.Store((int64(first) + 4*60 + 5) * 1000)
	fixture.runOld()
	fixture.clock.Store(fixture.now().Add(time.Second).UnixMilli())
	if _, _, err := fixture.oldRunner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	read := fixture.now().UnixMilli()
	if _, attempted := fixture.round(); !attempted {
		t.Fatal("the new group's first Slot was not frozen once its predecessor closed")
	}
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	if ended != fullEnding || hold != read-int64(first)*1000-500 {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL with the %d ms it waited", ended, hold, read-int64(first)*1000-500)
	}
	last := first + 4*60
	fixture.runNewUntil(last)
	endings := fixture.slotEndings()
	for slot := first; slot <= last; slot += 60 {
		if endings[slot] != fullEnding {
			t.Fatalf("the new group's Slot %d ended %q, want every Slot it fell behind by FULL; endings %v", slot, endings[slot], endings)
		}
	}
}

// The old group's record never closes - its owner stopped with the held Slot
// in flight and nothing came back to it. The new group waits, on every
// reader's page and writing nothing, until its first Slot would have been
// ready on the bound, and freezes the bound then, as it would have at once:
// as late, and every Slot runs.
func TestASuccessorWhosePredecessorNeverClosesWaitsUntilTheBoundAndSaysSo(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	boundary := fixture.editQuery()
	first := fixture.first(boundary)
	previous, offset := fixture.link()
	until := min(int64(previous)*1000+600_000+offset, int64(first)*1000+500+600_000)

	// Its first round, a round half way through, and its last look before
	// the end: each one waits, and writes nothing.
	start := firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60)
	for _, at := range []int64{start, (start + until) / 2, until - 1} {
		fixture.clock.Store(at)
		if _, attempted := fixture.round(); attempted {
			t.Fatalf("the new group's first Slot ran at %d, before its wait ended at %d", at, until)
		}
		fixture.awaiting(until)
		if events := fixture.eventsAt(first); len(events) != 0 {
			t.Fatalf("the waiting group wrote events: %+v", events)
		}
	}
	fixture.clock.Store(until)
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	if ended != fullEnding || hold != until-int64(first)*1000-500 {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL on the bound's %d ms", ended, hold, until-int64(first)*1000-500)
	}
	predecessors, waits := fixture.predecessorCounts()
	if predecessors[readhold.PredecessorRecordOpen] != 1 || waits[readhold.TransitionWaitWaiting] != 1 || waits[readhold.TransitionWaitBounded] != 1 {
		t.Fatalf("predecessors %v waits %v, want one wait, ended on the bound", predecessors, waits)
	}
	last := first + 3*60
	fixture.runNewUntil(last)
	endings := fixture.slotEndings()
	for slot := first; slot <= last; slot += 60 {
		if endings[slot] != fullEnding {
			t.Fatalf("the new group's Slot %d ended %q, want FULL; endings %v", slot, endings[slot], endings)
		}
	}
}

// The old group's held Slot completes while the new group waits, and the old
// group, drained, is released before a round comes back to it: it closes its
// record and this Worker forgets it. The new group's next look reads the
// record again, from the store now, finds it closed and inherits the hold -
// a predecessor forgotten is read again, not taken for one that never closes.
func TestASuccessorReadsAgainAPredecessorReleasedWhileItWaits(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	boundary := fixture.editQuery()
	first := fixture.first(boundary)
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60))
	if _, attempted := fixture.round(); attempted {
		t.Fatal("the new group's first Slot ran with its predecessor's record open")
	}

	fixture.runOld()
	fixture.releaseOld()
	if closes, failed := fixture.retireCloses(); closes[readhold.RetireCloseRelease] != 1 || failed != 0 {
		t.Fatalf("retire closes %v, failed %v; want the old group closed by its release and forgotten", closes, failed)
	}
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	if want := fixture.inheritedHold(first, true); ended != fullEnding || hold != want {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL with the %d ms the old group's hold leaves", ended, hold, want)
	}
	predecessors, waits := fixture.predecessorCounts()
	if predecessors[readhold.PredecessorInherited] != 1 || predecessors[readhold.PredecessorUnreadable] != 0 ||
		waits[readhold.TransitionWaitWaiting] != 1 || waits[readhold.TransitionWaitInherited] != 1 {
		t.Fatalf("predecessors %v waits %v, want one wait, ended inherited, and nothing unreadable", predecessors, waits)
	}
}

// The drained old group's release cannot close its record: another writer
// changed it under the old owner, and the fenced write is refused. The
// release goes on, the failure is counted where /metrics reads it, and the
// new group waits on the record still open until its first Slot would have
// been ready on the bound, freezes the bound then, and says it ended so.
func TestADrainedGroupWhoseReleaseCannotCloseItsHoldIsCounted(t *testing.T) {
	fixture := newPredecessorWaitFixture(t)
	fixture.runOld()
	ctx := context.Background()
	spec, err := fixture.bundle.dependencies.ReadHolds.spec(ctx, fixture.initialSchedule)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := json.Marshal(readhold.Record{SinceSlot: 1, HoldMillis: 39_500, SegmentStart: fixture.initialSchedule.Segment.Start,
		Plans: []readhold.PlanRecord{{PlanRef: spec.Plans[0]}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.redisClient.Set(ctx, holdRecordKey(fixture.cutoverStallFixture, fixture.old), changed, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Store((int64(fixture.held) + 59) * 1000)
	boundary := fixture.editQuery()
	fixture.releaseOld()
	if closes, failed := fixture.retireCloses(); closes[readhold.RetireCloseRelease] != 0 || failed != 1 {
		t.Fatalf("retire closes %v, failed %v; want the release's close counted as failed", closes, failed)
	}

	first := fixture.first(boundary)
	previous, offset := fixture.link()
	until := min(int64(previous)*1000+600_000+offset, int64(first)*1000+500+600_000)
	fixture.clock.Store(firstRoundAtMillis(fixture.cfg, fixture.next, int64(first), 60))
	if _, attempted := fixture.round(); attempted {
		t.Fatal("the new group's first Slot ran with its predecessor's record open")
	}
	fixture.clock.Store(until)
	fixture.runNewUntil(first)
	ended, hold := fixture.completion(first)
	if ended != fullEnding || hold != until-int64(first)*1000-500 {
		t.Fatalf("the new group's first Slot ended %q with a %d ms hold, want FULL on the bound's %d ms", ended, hold, until-int64(first)*1000-500)
	}
	recorder := fixture.bundle.dependencies.Recorder
	if waits := counterValue(t, recorder, "alarmd_read_hold_transition_waits_total", map[string]string{"outcome": readhold.TransitionWaitBounded}); waits != 1 {
		t.Fatalf("waits ended on the bound %v, want 1", waits)
	}
}
