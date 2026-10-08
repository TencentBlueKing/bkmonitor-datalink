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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// emptyRunRecord is an object's record as its writer keeps it: its last
// round, and the first empty Slot of the run it is in.
func emptyRunRecord(at time.Time, last RestoredRound, since time.Time) RestoredState {
	last.CompletedAt = last.Slot.Add(2 * time.Second)
	return RestoredState{LastCompletion: last.Kind, NextSlot: at.Add(time.Minute), LastRound: &last, EmptyRunSince: since}
}

// skippedAt is one Slot closed without a query because it fell past the
// replay window.
func skippedAt(queryGroup string, slot time.Time) observability.Observation {
	observed := completion(queryGroup, "GAP_SKIPPED", "4101")
	observed.ProgressCompletionCause = "REPLAY_EXPIRED"
	observed.Trace.EvaluationTime = slot.Unix()
	return observed
}

func everyRoundRow(tracker *Tracker, queryGroup string) (Anomaly, bool) {
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)[queryGroup]
	return row, listed
}

// An object empty for seventy minutes is listed from the record's start
// whether the record is read before this process's first round or after
// it: the read that lost the race to the first round used to be dropped
// whole, and the object left the line for an hour after every release. Read
// after, the record is the one this process has written over -- its last
// round is this process's latest -- and still carries the start from before
// the restart. Both orders give the same row.
func TestTheRecordsStartIsKeptWhicheverComesFirstTheRestoreOrTheFirstRound(t *testing.T) {
	since := now.Add(-70 * time.Minute)
	beforeRestart := RestoredRound{Slot: now.Add(-2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"}
	rounds := func(tracker *Tracker) {
		for minute := 0; minute < 3; minute++ {
			tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
		}
	}
	restoredFirst := newTracker(t, &clock{at: now})
	restoredFirst.Restore("qg", emptyRunRecord(now, beforeRestart, since), now, 0)
	rounds(restoredFirst)

	roundsFirst := newTracker(t, &clock{at: now})
	rounds(roundsFirst)
	writtenOver := RestoredRound{Slot: now.Add(2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"}
	roundsFirst.Restore("qg", emptyRunRecord(now, writtenOver, since), now, 0)

	first, listedFirst := everyRoundRow(restoredFirst, "qg")
	second, listedSecond := everyRoundRow(roundsFirst, "qg")
	if !listedFirst || !listedSecond {
		t.Fatalf("listed restored first %v, rounds first %v; want both from the record's start", listedFirst, listedSecond)
	}
	for name, row := range map[string]Anomaly{"restored first": first, "rounds first": second} {
		if !row.Since.Equal(since) || row.SinceFrom != SinceRestoredEmptyRun {
			t.Errorf("%s: since %s from %s, want the record's %s from %s", name, row.Since, row.SinceFrom, since, SinceRestoredEmptyRun)
		}
	}
	if first.EmptyEveryRound.Rounds != second.EmptyEveryRound.Rounds {
		t.Errorf("rounds %d restored first, %d rounds first; want the same count", first.EmptyEveryRound.Rounds, second.EmptyEveryRound.Rounds)
	}
}

// A record read after a skip, before any empty round, is held and joined at
// the first empty round; the record is read once.
func TestARecordReadBeforeTheFirstEmptyRoundIsJoinedAtIt(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	since := now.Add(-70 * time.Minute)
	tracker.Observe(context.Background(), skippedAt("qg", now.Add(-time.Minute)))
	if !tracker.WantsRestore("qg") {
		t.Fatal("an object determined by a skip, never seen with records, did not want its record")
	}
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(-time.Minute), Kind: "GAP_SKIPPED"}, since), now, 0)
	if tracker.WantsRestore("qg") {
		t.Fatal("the record was wanted again after it was read")
	}
	if _, listed := everyRoundRow(tracker, "qg"); listed {
		t.Fatal("listed before any empty round")
	}
	for minute := 0; minute < 2; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	if row, listed := everyRoundRow(tracker, "qg"); !listed || !row.Since.Equal(since) || row.SinceFrom != SinceRestoredEmptyRun {
		t.Fatalf("row %+v listed %v, want the held record's start %s", row, listed, since)
	}
}

// An object this process has seen records for is not given an old run of
// empty rounds: records end the run, whatever the record says. Its record is
// not wanted, and a read that happens anyway changes nothing.
func TestAnObjectSeenWithRecordsIsNotJoinedToAnOldEmptyRun(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), dataAt("qg", "4101", now.Add(-2*time.Minute)))
	for minute := 0; minute < 3; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	if tracker.WantsRestore("qg") {
		t.Fatal("an object seen with records wanted its record")
	}
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"},
		now.Add(-70*time.Minute)), now, 0)
	if row, listed := everyRoundRow(tracker, "qg"); listed {
		t.Fatalf("an object with records in this process was listed as never having any: %+v", row)
	}
	if state := tracker.groups["qg"]; state.emptySlotFrom != SinceSnapshotContinuity || state.emptySinceSlot != now.Unix() {
		t.Fatalf("the run starts at %d from %s, want this process's first empty round %d", state.emptySinceSlot, state.emptySlotFrom, now.Unix())
	}
}

// A record that names a Slot known to have had records makes the object the
// data side's in either order, as restoring it first does.
func TestARecordThatHadRecordsIsTakenAsSeenAfterTheFirstRoundToo(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	for minute := 0; minute < 3; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	record := emptyRunRecord(now, RestoredRound{Slot: now.Add(2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"}, now.Add(-70*time.Minute))
	record.LastDataSlot = now.Add(-3 * time.Hour)
	tracker.Restore("qg", record, now, 0)
	state := tracker.groups["qg"]
	if !state.sawData || state.lastDataSlot != now.Add(-3*time.Hour).Unix() {
		t.Fatalf("seen %v, last records at %d; want the record's Slot", state.sawData, state.lastDataSlot)
	}
	if _, listed := everyRoundRow(tracker, "qg"); listed {
		t.Fatal("listed as never having had records")
	}
}

// A skipped stretch of more than the hour before this process's first empty
// round is a hole in the evidence, as it is when the record is restored
// first: the record's start is not taken across it.
func TestARunIsNotJoinedAcrossASkippedStretchLongerThanTheHour(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), skippedAt("qg", now.Add(-90*time.Minute)))
	tracker.Observe(context.Background(), skippedAt("qg", now.Add(-time.Minute)))
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(-time.Minute), Kind: "GAP_SKIPPED"}, now.Add(-3*time.Hour)), now, 0)
	for minute := 0; minute < 3; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	if state := tracker.groups["qg"]; state.emptySlotFrom != SinceSnapshotContinuity || state.emptySinceSlot != now.Unix() {
		t.Fatalf("the run starts at %d from %s, want this process's first empty round %d across the hole",
			state.emptySinceSlot, state.emptySlotFrom, now.Unix())
	}
}

// The record is wanted by an object not determined yet, and by one
// determined without records until its record is read; not by one seen
// with records.
func TestWhoseRecordIsWanted(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	if !tracker.WantsRestore("qg-unseen") {
		t.Error("an object never observed did not want its record")
	}
	tracker.Observe(context.Background(), runOutcome("qg-not-due", "source_not_due"))
	if !tracker.WantsRestore("qg-not-due") {
		t.Error("an object seen only between rounds did not want its record")
	}
	tracker.Observe(context.Background(), emptyAt("qg-empty", "4101", now))
	if !tracker.WantsRestore("qg-empty") {
		t.Error("an object determined by an empty round did not want its record")
	}
	tracker.Restore("qg-empty", emptyRunRecord(now, RestoredRound{Slot: now, Kind: "FULL_EMPTY_COMPLETED"}, now.Add(-time.Hour)), now, 0)
	if tracker.WantsRestore("qg-empty") {
		t.Error("the record was wanted after it was read")
	}
	tracker.Observe(context.Background(), dataAt("qg-data", "4101", now))
	if tracker.WantsRestore("qg-data") {
		t.Error("an object seen with records wanted its record")
	}
}

// A record held for the first empty round is dropped when records come
// first: they end the run the record carries.
func TestAHeldRecordIsDroppedWhenRecordsArrive(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), skippedAt("qg", now.Add(-2*time.Minute)))
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(-2 * time.Minute), Kind: "GAP_SKIPPED"},
		now.Add(-70*time.Minute)), now, 0)
	tracker.Observe(context.Background(), dataAt("qg", "4101", now.Add(-time.Minute)))
	for minute := 0; minute < 3; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	if state := tracker.groups["qg"]; state.emptySlotFrom != SinceSnapshotContinuity || state.recordRun != nil {
		t.Fatalf("the run is from %s with the record still held %v, want this process's own after its records", state.emptySlotFrom, state.recordRun)
	}
}

// The record only ever moves a run's start earlier: a record whose run
// began after this process's first empty round leaves the start alone.
func TestARecordStartingLaterLeavesTheRunAlone(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	for minute := 0; minute < 3; minute++ {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Duration(minute)*time.Minute)))
	}
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"},
		now.Add(time.Minute)), now, 0)
	if state := tracker.groups["qg"]; state.emptySinceSlot != now.Unix() || state.emptySlotFrom != SinceSnapshotContinuity {
		t.Fatalf("the run starts at %d from %s, want %d as watched", state.emptySinceSlot, state.emptySlotFrom, now.Unix())
	}
}

// A run this process has already restarted at a hole of its own is not
// joined: the record's start would reach back across the hole.
func TestARunRestartedAtAHoleIsNotJoined(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	// A minute's pace first, so the three hours after it read as a hole.
	for minute := 3; minute > 0; minute-- {
		tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(-3*time.Hour-time.Duration(minute)*time.Minute)))
	}
	tracker.Observe(context.Background(), emptyAt("qg", "4101", now))
	tracker.Observe(context.Background(), emptyAt("qg", "4101", now.Add(time.Minute)))
	state := tracker.groups["qg"]
	if state.emptySinceSlot != now.Unix() {
		t.Fatalf("the fixture's hole did not restart the run: it starts at %d", state.emptySinceSlot)
	}
	tracker.Restore("qg", emptyRunRecord(now, RestoredRound{Slot: now.Add(time.Minute), Kind: "FULL_EMPTY_COMPLETED"},
		now.Add(-5*time.Hour)), now, 0)
	if state.emptySinceSlot != now.Unix() || state.emptySlotFrom != SinceSnapshotContinuity {
		t.Fatalf("the run starts at %d from %s, want the restart at the hole %d", state.emptySinceSlot, state.emptySlotFrom, now.Unix())
	}
}
