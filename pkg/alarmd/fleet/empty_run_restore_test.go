// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"
	"time"
)

// restoredEmptyRun restores one object whose record says its last round was
// empty, at the given Slot, and that the run of empty rounds began at since.
// lastData is the record's last Slot known to have had records, zero for none.
func restoredEmptyRun(t *testing.T, tracker *Tracker, queryGroup string, at, lastSlot, since, lastData time.Time) {
	t.Helper()
	if !tracker.Restore(queryGroup, RestoredState{
		LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: at.Add(15 * time.Second),
		LastRound:     &RestoredRound{Slot: lastSlot, CompletedAt: lastSlot.Add(2 * time.Second), Kind: "FULL_EMPTY_COMPLETED"},
		EmptyRunSince: since, LastDataSlot: lastData,
	}, at, 0) {
		t.Fatalf("%s was not restored", queryGroup)
	}
}

// Two releases under an hour apart left the line blank for a day: every
// restart began the hour again, because the record restored only the last
// round. The record now carries the run's first empty Slot, and the hour is
// read from it -- so an object empty for seventy minutes is listed the moment
// the replica that took it over restores it, with the record's Slot as its
// start and the record named as the source. The count is the one round the
// record vouches for: a lower bound, and the row says so through the source.
func TestTheHourAnObjectHasBeenEmptySurvivesARestartThroughItsRecord(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	since := now.Add(-70 * time.Minute)
	restoredEmptyRun(t, tracker, "qg-restored-run", now, now.Add(-15*time.Second), since, time.Time{})

	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-restored-run"]
	if !listed {
		t.Fatalf("an object whose record says it has been empty for seventy minutes is not listed on restore: %+v", tracker.NoData())
	}
	if !row.Since.Equal(since) || row.SinceFrom != SinceRestoredEmptyRun || row.EmptyEveryRound == nil ||
		!row.EmptyEveryRound.Since.Equal(since) || row.EmptyEveryRound.Rounds != 1 || !row.EmptyEveryRound.NeverSawData {
		t.Errorf("row = %+v facts %+v, want since %s from %s, one round vouched for, no round known to have had records",
			row, row.EmptyEveryRound, since, SinceRestoredEmptyRun)
	}

	// Fifty minutes into the run at restore, eleven more watched here: the
	// hour is the record's fifty plus this process's eleven, not eleven.
	fifty := now.Add(-50 * time.Minute)
	restoredEmptyRun(t, tracker, "qg-mid-run", now, now.Add(-15*time.Second), fifty, time.Time{})
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-mid-run"]; listed {
		t.Fatal("listed at fifty minutes: the hour has not passed on any clock")
	}
	rounds := emptyRounds(tracker, at, "qg-mid-run", 15*time.Second, 11*time.Minute)
	row, listed = rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-mid-run"]
	if !listed {
		t.Fatalf("not listed after the record's fifty minutes and eleven watched here: %+v", tracker.NoData())
	}
	if !row.Since.Equal(fifty) || row.SinceFrom != SinceRestoredEmptyRun || row.EmptyEveryRound.Rounds != rounds+1 {
		t.Errorf("row = %+v facts %+v, want the record's start %s kept through the rounds watched here, %d rounds counted",
			row, row.EmptyEveryRound, fifty, rounds+1)
	}

	// A record that names a Slot known to have had records is the data side's
	// object whatever its last round was: never this line, and the data
	// side's once the round count is reached -- with the record's start.
	twoHours := now.Add(-2 * time.Hour)
	restoredEmptyRun(t, tracker, "qg-had-data-once", now, now.Add(-15*time.Second), twoHours, now.Add(-3*time.Hour))
	for round := 1; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), emptyAt("qg-had-data-once", "4101", at.at))
		at.at = at.at.Add(15 * time.Second)
	}
	rows := tracker.NoData()
	if _, listed := rowsOfKind(rows, KindEmptyEveryRound)["qg-had-data-once"]; listed {
		t.Error("an object whose record names a Slot with records is listed as having none known")
	}
	stopped, listed := rowsOfKind(rows, KindNoData)["qg-had-data-once"]
	if !listed {
		t.Fatalf("an object whose record names a Slot with records, empty since, is not on the data side's line after %d rounds: %+v",
			DefaultDegradedRounds, rows)
	}
	if !stopped.Since.Equal(twoHours) || stopped.SinceFrom != SinceRestoredEmptyRun {
		t.Errorf("data side's row = %+v, want the record's start %s from %s", stopped, twoHours, SinceRestoredEmptyRun)
	}

	// Records arriving end the restored run like any other, and the next
	// empty round starts a new one on this process's clock.
	tracker.Observe(context.Background(), dataAt("qg-restored-run", "4101", at.at))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-restored-run"]; listed {
		t.Error("still listed after a round with records")
	}
	tracker.Observe(context.Background(), emptyAt("qg-restored-run", "4101", at.at))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-restored-run"]; listed {
		t.Error("listed again on the first empty round after records: the restored start was kept past the records")
	}
}

// A record without the run's start restores nothing about the run: the object
// waits the hour from the first empty round watched here, as before the field
// existed. The latest empty Slot is the restored round's when the record has
// one; a record with the start but no round summary waits for the first empty
// round watched here to supply it, and is then listed on the restored start.
func TestARecordWithoutTheRunsStartRestoresNoRun(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	if !tracker.Restore("qg-no-start", RestoredState{
		LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: now.Add(15 * time.Second),
		LastRound: &RestoredRound{Slot: now.Add(-15 * time.Second), CompletedAt: now, Kind: "FULL_EMPTY_COMPLETED"},
	}, now, 0) {
		t.Fatal("qg-no-start was not restored")
	}
	if rows := tracker.NoData(); len(rows) != 0 {
		t.Fatalf("a record without the run's start put the object on a line: %+v", rows)
	}
	if !tracker.Restore("qg-start-no-summary", RestoredState{
		LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: now.Add(15 * time.Second),
		EmptyRunSince: now.Add(-90 * time.Minute),
	}, now, 0) {
		t.Fatal("qg-start-no-summary was not restored")
	}
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-start-no-summary"]; listed {
		t.Fatal("listed before any empty Slot is known to end the run: the record had no round summary")
	}
	tracker.Observe(context.Background(), emptyAt("qg-start-no-summary", "4101", now))
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-start-no-summary"]
	if !listed || !row.Since.Equal(now.Add(-90*time.Minute)) || row.SinceFrom != SinceRestoredEmptyRun {
		t.Errorf("row = %+v (listed %v), want listed on the first empty round watched here, from the record's start", row, listed)
	}

	// The run's start survives a failed round on the record, but a record
	// whose last round failed is not in a run of consecutive empty rounds:
	// the data side's count starts at zero, not at the restored one, so two
	// empty rounds watched here are two, one short of its line.
	if !tracker.Restore("qg-failed-last", RestoredState{
		LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: now.Add(15 * time.Second),
		LastRound:     &RestoredRound{Slot: now.Add(-15 * time.Second), CompletedAt: now, Kind: "COMPLETED_WITH_UNAVAILABLE"},
		EmptyRunSince: now.Add(-90 * time.Minute), LastDataSlot: now.Add(-3 * time.Hour),
	}, now, 0) {
		t.Fatal("qg-failed-last was not restored")
	}
	for round := 0; round < DefaultDegradedRounds-1; round++ {
		tracker.Observe(context.Background(), emptyAt("qg-failed-last", "4101", now.Add(time.Duration(round)*15*time.Second)))
	}
	if row, listed := rowsOfKind(tracker.NoData(), KindNoData)["qg-failed-last"]; listed {
		t.Errorf("on the data side's line after %d empty rounds following a failed one: %+v; the restored count was taken from a run the last round had already broken",
			DefaultDegradedRounds-1, row)
	}
}

// The hour is measured on the source's clock, first empty Slot to latest, and
// on that clock alone. An object two hours behind and catching up has the
// record's start two hours in the past and a watcher's clock that says the
// hour is long over -- and has completed a hundred and fifty seconds of Slots.
// It is not listed. The reverse holds too: two hours of empty Slots replayed
// while the watcher's clock does not move is two empty hours of source time,
// and the object is listed.
func TestTheHourIsMeasuredOnTheSourcesClockNotOnTheWatchers(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	twoHoursBehind := now.Add(-2 * time.Hour)
	restoredEmptyRun(t, tracker, "qg-catching-up", now, twoHoursBehind, twoHoursBehind, time.Time{})
	// Ten Slots of the backlog, in no time at all on the watcher's clock.
	for slot := twoHoursBehind.Add(15 * time.Second); !slot.After(twoHoursBehind.Add(150 * time.Second)); slot = slot.Add(15 * time.Second) {
		tracker.Observe(context.Background(), emptyAt("qg-catching-up", "4101", slot))
	}
	if row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-catching-up"]; listed {
		t.Errorf("an object that has completed 150 s of Slots is listed as empty for an hour: %+v; the watcher's clock leaked into the gate", row)
	}
	// The backlog worked through to the hour: listed, on Slots alone.
	for slot := twoHoursBehind.Add(165 * time.Second); !slot.After(twoHoursBehind.Add(time.Hour)); slot = slot.Add(15 * time.Second) {
		tracker.Observe(context.Background(), emptyAt("qg-catching-up", "4101", slot))
	}
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-catching-up"]; !listed {
		t.Error("an object whose Slots span an hour of empty rounds is not listed")
	}

	// Replay: two hours of Slots at one instant of the watcher's clock.
	for slot := now.Add(-2 * time.Hour); !slot.After(now); slot = slot.Add(15 * time.Second) {
		tracker.Observe(context.Background(), emptyAt("qg-replayed", "4102", slot))
	}
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-replayed"]
	if !listed {
		t.Fatal("two hours of empty Slots replayed in an instant is not listed: the watcher's clock leaked into the gate")
	}
	if !row.Since.Equal(now.Add(-2*time.Hour)) || row.SinceFrom != SinceSnapshotContinuity {
		t.Errorf("row = %+v, want the first replayed Slot as its start, watched here", row)
	}
}
