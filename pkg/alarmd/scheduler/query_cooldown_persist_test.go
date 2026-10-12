// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The pool stays put unless there is evidence: a restart or a change of
// owner restores it from its record, a change that is not the query only
// brings a probe forward, a probe that proves nothing does not open the gate,
// and an exit on evidence followed by a return is counted as a re-entry.

// memoryCooldownStore is a QueryCooldownStore that keeps the latest record
// per Query Group and refuses a save from an owner older than the record's.
type memoryCooldownStore struct {
	records map[execution.QueryGroupIdentity]QueryCooldownRecord
	saves   int
	loadErr error
}

func newMemoryCooldownStore() *memoryCooldownStore {
	return &memoryCooldownStore{records: map[execution.QueryGroupIdentity]QueryCooldownRecord{}}
}

func (store *memoryCooldownStore) LoadQueryCooldown(_ context.Context, queryGroup execution.QueryGroupIdentity) (QueryCooldownRecord, bool, error) {
	if store.loadErr != nil {
		return QueryCooldownRecord{}, false, store.loadErr
	}
	record, found := store.records[queryGroup]
	return record, found, nil
}

func (store *memoryCooldownStore) SaveQueryCooldown(_ context.Context, fence execution.OwnerFence, record QueryCooldownRecord) error {
	if current, found := store.records[record.QueryGroup]; found && current.OwnerEpoch > fence.OwnerEpoch {
		return errors.New("superseded")
	}
	store.saves++
	store.records[record.QueryGroup] = record
	return nil
}

// poolRunner is a Runner on qg with the pool policy on, a clock the test
// moves, and the cooldown lines it emits.
func poolRunner(store QueryCooldownStore, now *time.Time, lines *[]observability.QueryCooldownFacts) *Runner {
	runner := &Runner{queryGroup: "qg", now: func() time.Time { return *now }, flights: &FlightCoordinator{
		limits: RecoveryLimits{},
		observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
			if o.QueryCooldown != nil {
				*lines = append(*lines, *o.QueryCooldown)
			}
		}),
	}}
	return runner.WithQueryCooldownStore(store)
}

func fenceAt(epoch uint64) execution.OwnerFence {
	return execution.OwnerFence{QueryGroup: "qg", OwnerID: fmt.Sprintf("worker-%d", epoch), OwnerEpoch: epoch, LeaseToken: "token"}
}

// failInto puts the runner's Query Group in the pool: three failed Slots.
func failInto(runner *Runner, slot *FrozenSlot) {
	for i := 0; i < unavailableThreshold; i++ {
		slot.Contract.Slot.EvaluationTime++
		runner.recordQueryAvailability(context.Background(), *slot, execution.OperationNormal, unavailableResult(), 60, runner.queryCooldownLetsRun())
	}
}

// poolSlot is a Slot of qg whose maintenance deadline is far enough away
// that the cooldown, not the deadline, decides whether it waits.
func poolSlot() FrozenSlot {
	slot := frozenSlot("qg")
	slot.RecoveryUntilUnixMilli = time.Unix(1_000_000, 0).UnixMilli()
	return slot
}

func eventsOf(lines []observability.QueryCooldownFacts) []string {
	events := make([]string, 0, len(lines))
	for _, line := range lines {
		events = append(events, line.Event)
	}
	return events
}

// A new process -- or a new owner -- finds the Query Group in the pool it was
// in, with the time it entered, and does not count an entry for it.
func TestAQueryGroupInThePoolIsRestoredWithTheTimeItEntered(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	var first []observability.QueryCooldownFacts
	before := poolRunner(store, &now, &first)
	before.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failInto(before, &slot)
	entered := before.queryCooldown.until
	if entered.IsZero() || store.records["qg"].Until != entered || !store.records["qg"].EnteredAt.Equal(now) {
		t.Fatalf("record = %+v, want the pool state saved on entry", store.records["qg"])
	}

	now = now.Add(10 * time.Second)
	var second []observability.QueryCooldownFacts
	after := poolRunner(store, &now, &second)
	after.restoreQueryCooldown(context.Background(), fenceAt(2))
	if after.queryCooldown.until != entered || after.queryCooldown.failures != unavailableThreshold {
		t.Fatalf("restored state = %+v, want the pool state the first Runner left", after.queryCooldown)
	}
	if len(second) != 1 || second[0].Event != QueryCooldownRestored || !second[0].EnteredAt.Equal(time.Unix(1_000, 0)) ||
		second[0].Source != QueryCooldownRestored {
		t.Fatalf("lines = %+v, want one restored line carrying the original entry time", second)
	}
	// Still in the pool: the Slot waits for the cooldown it was already in.
	if !after.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("a restored Query Group was let through before its cooldown ended")
	}
}

// The restore happens on the Runner's first round that holds the Query
// Group, from RunOne, not only when a test calls it.
func TestRunOneRestoresThePoolBeforeItDecidesAnything(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(100, 0)
	source := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
	source.slot.EarliestQueryDeadlineUnixMilli = 150_000
	store.records["query-group-1"] = QueryCooldownRecord{QueryGroup: "query-group-1", OwnerEpoch: 1,
		EnteredAt: now.Add(-time.Hour), Until: now.Add(time.Minute), Failures: 5,
		QueryRevision: source.slot.Contract.QueryRevision, ScheduleRevision: source.slot.Contract.ScheduleRevision,
		SegmentStart: source.slot.Contract.ScheduleSegmentStart}
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}}}
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: source.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner.WithQueryCooldownStore(store)
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || attempted {
		t.Fatalf("RunOne = (attempted %t, %v), want the restored cooldown to hold the Slot back", attempted, err)
	}
}

// An owner that has been replaced cannot write the pool state over its
// successor's.
func TestAReplacedOwnerCannotWriteOverItsSuccessor(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	var lines []observability.QueryCooldownFacts
	old := poolRunner(store, &now, &lines)
	old.restoreQueryCooldown(context.Background(), fenceAt(1))
	successor := poolRunner(store, &now, &lines)
	successor.restoreQueryCooldown(context.Background(), fenceAt(2))
	slot := poolSlot()
	failInto(successor, &slot)
	want := store.records["qg"]
	now = now.Add(time.Minute)
	old.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 60, old.queryCooldownLetsRun())
	failInto(old, &slot)
	if got := store.records["qg"]; got.OwnerEpoch != 2 || got.Until != want.Until {
		t.Fatalf("record = %+v, want the successor's %+v", got, want)
	}
}

// Another schedule or Segment for the same query is no evidence about the
// backend: the Query Group stays in the pool, is probed once, now, and a
// probe that fails keeps it there.
func TestAChangeThatIsNotTheQueryBringsAProbeForwardAndDoesNotExit(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failInto(runner, &slot)
	slot.Contract.ScheduleRevision = "schedule-2"
	slot.Contract.ScheduleSegmentStart = 90
	if runner.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("the probe the change asked for was not let through")
	}
	if runner.queryCooldown.until.IsZero() || store.records["qg"].Until.IsZero() {
		t.Fatalf("pool state = %+v, record %+v: the change took the Query Group out of the pool", runner.queryCooldown, store.records["qg"])
	}
	// The probe brought forward is in the record too, so a restart before
	// it runs does not wait out the old cooldown.
	if record := store.records["qg"]; !record.Until.Equal(now) || record.ScheduleRevision != "schedule-2" {
		t.Fatalf("record = %+v, want until brought forward to now under the new schedule", record)
	}
	// The probe fails: extended, still in the pool, and the next Slot waits.
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, unavailableResult(), 60, runner.queryCooldownLetsRun())
	if !runner.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("a failed probe let the next Slot through")
	}
	for _, event := range eventsOf(lines) {
		if event == QueryCooldownRecovered || event == QueryCooldownQueryRevisionChanged {
			t.Fatalf("events %v, want no exit", eventsOf(lines))
		}
	}
	// The query itself changing is evidence, and exits.
	slot.Contract.QueryRevision = "query-2"
	runner.deferUnavailableQuery(context.Background(), slot)
	if got := eventsOf(lines); got[len(got)-1] != QueryCooldownQueryRevisionChanged || store.records["qg"].ExitReason != QueryCooldownQueryRevisionChanged {
		t.Fatalf("events %v record %+v, want the exit on the query's change", got, store.records["qg"])
	}
}

// A probe that proves nothing either way does not open the gate: the next
// probe waits a period, and the Slots in between are held as before.
func TestAProbeThatProvesNothingDoesNotLetEverySlotThrough(t *testing.T) {
	now := time.Unix(1_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(newMemoryCooldownStore(), &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failInto(runner, &slot)
	now = runner.queryCooldown.until
	if runner.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("the probe at the end of the cooldown was held")
	}
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnknown}, 60, runner.queryCooldownLetsRun())
	if !runner.deferUnavailableQuery(context.Background(), slot) || !runner.queryCooldown.until.Equal(now.Add(time.Minute)) {
		t.Fatalf("after an unknown probe until=%s, want the next probe a period away and the Slot held", runner.queryCooldown.until)
	}
}

// An exit on evidence -- a query that answered -- followed by a return within
// the window is a re-entry, counted on the line and kept in the record, and
// it is still one after a restart in between.
func TestAnExitOnEvidenceThenAReturnIsAReentry(t *testing.T) {
	store := newMemoryCooldownStore()
	now := time.Unix(1_000, 0)
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(context.Background(), fenceAt(1))
	slot := poolSlot()
	failInto(runner, &slot)
	now = runner.queryCooldown.until
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 60, runner.queryCooldownLetsRun())
	if record := store.records["qg"]; !record.Until.IsZero() || record.ExitReason != QueryCooldownRecovered || !record.ExitedAt.Equal(now) {
		t.Fatalf("record after the exit = %+v, want out of the pool with the exit kept", record)
	}

	// A restart between the exit and the return.
	now = now.Add(2 * time.Minute)
	lines = nil
	restarted := poolRunner(store, &now, &lines)
	restarted.restoreQueryCooldown(context.Background(), fenceAt(2))
	if len(lines) != 0 {
		t.Fatalf("lines = %+v, want nothing restored for a Query Group outside the pool", lines)
	}
	failInto(restarted, &slot)
	if got := eventsOf(lines); len(got) != 1 || got[0] != QueryCooldownReentered || lines[0].Reentries != 1 ||
		lines[0].LastExitReason != QueryCooldownRecovered || store.records["qg"].Reentries != 1 {
		t.Fatalf("lines %+v record %+v, want one re-entry after the recovered exit", lines, store.records["qg"])
	}
	// Past the window it is a plain entry again.
	now = now.Add(QueryCooldownReentryWindow + time.Hour)
	restarted.recordQueryAvailability(context.Background(), slot, execution.OperationNormal, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 60, restarted.queryCooldownLetsRun())
	now = now.Add(QueryCooldownReentryWindow + time.Hour)
	lines = nil
	failInto(restarted, &slot)
	if got := eventsOf(lines); len(got) != 1 || got[0] != QueryCooldownEntered {
		t.Fatalf("events %v, want an entry once the window has passed", got)
	}
}

// Upgrading onto a store with no record, or a record that cannot be read,
// is no record: the Query Group starts outside the pool and nothing fails.
func TestNoRecordOrAnUnreadableOneIsOutsideThePool(t *testing.T) {
	for name, store := range map[string]*memoryCooldownStore{
		"no record": newMemoryCooldownStore(), "unreadable": {records: map[execution.QueryGroupIdentity]QueryCooldownRecord{}, loadErr: errors.New("store down")},
	} {
		now := time.Unix(1_000, 0)
		var lines []observability.QueryCooldownFacts
		runner := poolRunner(store, &now, &lines)
		runner.restoreQueryCooldown(context.Background(), fenceAt(1))
		if !runner.queryCooldown.until.IsZero() || len(lines) != 0 || runner.deferUnavailableQuery(context.Background(), poolSlot()) {
			t.Fatalf("%s: state %+v lines %+v, want outside the pool", name, runner.queryCooldown, lines)
		}
	}
}

// A pool record the store could not read is read again on the next round, and
// nothing is written before a read succeeds. The memory a failed read leaves
// is empty, and a write made from it would replace the record under the new
// owner's epoch: the Query Group would read as entered today with no
// re-entries, though it has been in the pool since its first entry, and an
// entry would be counted that was not one. The pool's identity survives
// restarts and owner changes, so the record is left as it was until it is
// read, and read back it is what the Runner continues from.
//
// Both ways a write can come before the read succeeds: the read fails on
// three rounds in a row, each with a failed Slot, and the third enters the
// pool; or the read fails once and the same round's Slots enter it.
func TestAFailedPoolReadIsReadAgainAndNothingIsWrittenBeforeIt(t *testing.T) {
	entered := time.Unix(500, 0)
	for name, sameRound := range map[string]bool{"one failed Slot per round": false, "the round the read failed": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Unix(1_000, 0)
			store := newMemoryCooldownStore()
			kept := QueryCooldownRecord{QueryGroup: "qg", OwnerEpoch: 1, EnteredAt: entered, Until: now.Add(time.Minute),
				Failures: unavailableThreshold, Reentries: 123}
			store.records["qg"] = kept
			store.loadErr = errors.New("store down")
			var lines []observability.QueryCooldownFacts
			runner := poolRunner(store, &now, &lines)
			slot := poolSlot()
			if sameRound {
				runner.restoreQueryCooldown(ctx, fenceAt(2))
				failInto(runner, &slot)
			} else {
				for round := 0; round < unavailableThreshold; round++ {
					runner.restoreQueryCooldown(ctx, fenceAt(2))
					slot.Contract.Slot.EvaluationTime++
					runner.recordQueryAvailability(ctx, slot, execution.OperationNormal, unavailableResult(), 60, runner.queryCooldownLetsRun())
				}
			}
			if runner.queryCooldown.until.IsZero() {
				t.Fatal("fixture: the failed Slots did not enter the pool in memory")
			}
			if store.saves != 0 || !reflect.DeepEqual(store.records["qg"], kept) {
				t.Fatalf("%d saves, record %+v; want nothing written before a read succeeds, the record as it was", store.saves, store.records["qg"])
			}

			store.loadErr = nil
			runner.restoreQueryCooldown(ctx, fenceAt(2))
			if !runner.cooldownMemory.enteredAt.Equal(entered) || runner.cooldownMemory.reentries != 123 {
				t.Fatalf("memory after the read = %+v, want the record's entry time and re-entries", runner.cooldownMemory)
			}
			slot.Contract.Slot.EvaluationTime++
			runner.recordQueryAvailability(ctx, slot, execution.OperationNormal, unavailableResult(), 60, runner.queryCooldownLetsRun())
			if got := store.records["qg"]; store.saves == 0 || !got.EnteredAt.Equal(entered) || got.Reentries != 123 || got.OwnerEpoch != 2 {
				t.Fatalf("%d saves, record %+v; want the next write to carry the entry time and re-entries it read", store.saves, got)
			}
		})
	}
}

// A record that does not decode is read as none, and unlike a failed read it
// is a read: reading it again gives the same bytes. The Runner starts outside
// the pool, and its next entry writes over the record.
func TestAnUndecodableRecordIsReadAsNoneAndWrittenOver(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000, 0)
	store := newMemoryCooldownStore()
	store.loadErr = ErrQueryCooldownUndecodable
	var lines []observability.QueryCooldownFacts
	runner := poolRunner(store, &now, &lines)
	runner.restoreQueryCooldown(ctx, fenceAt(2))
	store.loadErr = nil
	if !runner.queryCooldown.until.IsZero() || len(lines) != 0 {
		t.Fatalf("state %+v lines %+v, want outside the pool", runner.queryCooldown, lines)
	}
	slot := poolSlot()
	failInto(runner, &slot)
	if got := store.records["qg"]; store.saves != 1 || got.OwnerEpoch != 2 || got.Until.IsZero() {
		t.Fatalf("%d saves, record %+v; want the entry written over the undecodable record", store.saves, got)
	}
}
