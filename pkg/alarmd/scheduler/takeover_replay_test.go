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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A takeover is an owner epoch another owner's epoch preceded. Acquired
// again here one epoch later -- a Runner rebuilt in this process -- is not
// one, and keeps the moment; an epoch that skipped one another owner held,
// or the first epoch a process holds at all, is.
func TestTheTakeoverClockTellsATakeoverFromARebuild(t *testing.T) {
	clock := NewTakeoverClock()
	start := time.Unix(1_700_000_000, 0)
	at := func(minutes int) time.Time { return start.Add(time.Duration(minutes) * time.Minute) }
	for _, step := range []struct {
		name  string
		epoch uint64
		now   time.Time
		want  time.Time
	}{
		{"the first epoch held here", 7, at(0), at(0)},
		{"the same epoch, a round later", 7, at(1), at(0)},
		{"rebuilt here, one epoch later", 8, at(2), at(0)},
		{"back after another owner held epoch 9", 10, at(3), at(3)},
		{"the same again", 10, at(4), at(3)},
	} {
		if got := clock.Anchor("query-group-1", testFence(step.epoch), step.now); !got.Equal(step.want) {
			t.Fatalf("%s: taken over at %v, want %v", step.name, got, step.want)
		}
	}
	if got := NewTakeoverClock().Anchor("query-group-1", testFence(11), at(5)); !got.Equal(at(5)) {
		t.Fatalf("a restarted process: taken over at %v, want the first round it holds the epoch", got)
	}
	var none *TakeoverClock
	if !none.Anchor("query-group-1", testFence(7), at(0)).IsZero() || !clock.Anchor("query-group-1", execution.OwnerFence{}, at(0)).IsZero() {
		t.Fatal("no clock, or no epoch, noted a takeover")
	}
}

type takeoverObserver struct {
	facts []observability.ReplayTakeoverFacts
}

func (observer *takeoverObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ReplayTakeover != nil {
		observer.facts = append(observer.facts, *observation.ReplayTakeover)
	}
}

// A ten-second Slot reached at T+56 is five grid points behind. Held here
// the whole time it is this owner's own falling behind, and the distance
// rule gives it up to keep the Query Group current. Due before this process
// took the Query Group over -- a restart, a rebalance that moved it here --
// nobody here could have run it, and it is replayed; the rebuild of a Runner
// in this process is not a takeover and does not buy it that.
func TestASlotDueBeforeATakeoverIsReplayedPastTheDistanceRule(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	deadline := int64(slot)*1000 + 25_000
	reached := time.Unix(int64(slot)+56, 0)
	before := time.Unix(int64(slot)-60, 0)
	for name, tc := range map[string]struct {
		clock   bool
		earlier *execution.OwnerFence // taken over at `before` under this fence
		now     execution.OwnerFence
		replay  bool
	}{
		"no takeover clock":                          {false, nil, testFence(7), false},
		"taken over as the Slot is reached":          {true, nil, testFence(7), true},
		"taken over before the Slot was due":         {true, fenceOf(7), testFence(7), false},
		"a Runner rebuilt here since":                {true, fenceOf(7), testFence(8), false},
		"back here after another owner held it":      {true, fenceOf(7), testFence(9), true},
		"taken over by a new process, same epoch +1": {true, nil, testFence(8), true},
	} {
		source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
		observer := &takeoverObserver{}
		source.observer = observer
		if tc.clock {
			source.takeovers = NewTakeoverClock()
		}
		if tc.earlier != nil {
			source.takeovers.Anchor(source.queryGroup, *tc.earlier, before)
		}
		operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline, 0, reached, tc.now)
		if err != nil {
			t.Fatalf("%s: classifyRecovery() error = %v", name, err)
		}
		if tc.replay {
			if operation != execution.OperationReplay || facts.Disposition != ReplayEligible || facts.Distance <= testRecoveryLimits().MaxReplaySlots {
				t.Errorf("%s: %s %+v, want a replay past the distance rule", name, operation, facts)
			}
			if len(observer.facts) != 1 || observer.facts[0].Outcome != observability.ReplayTakeoverReplayed {
				t.Errorf("%s: takeover facts %+v, want one replayed", name, observer.facts)
			}
			continue
		}
		if operation != execution.OperationNormal || facts.Disposition != ReplayExpired || facts.Reason != ReplayExpiredByDistance {
			t.Errorf("%s: %s %+v, want given up on for distance", name, operation, facts)
		}
		if len(observer.facts) != 0 {
			t.Errorf("%s: takeover facts %+v for a Slot held here", name, observer.facts)
		}
	}
}

// The takeover is the first round under the epoch, whatever that round
// finds. A Query Group taken over with nothing overdue that then falls five
// minutes behind fell behind itself: its old Slots go to the distance rule.
// Noted only at the first Slot past its deadline, the takeover would move to
// that moment and hand this owner's own backlog the takeover's replay.
func TestATakeoverWithNothingOverdueStartsTheClockThen(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	reader := &fakeProgressReader{result: foundProgress(600, 540), catalog: catalog}
	now := time.Unix(610, 0)
	source := mustProductionSlotSource(t, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, reader, now)
	source.now = func() time.Time { return now }
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	if slot, _, _, err := source.Next(context.Background(), "query-group-1"); err != nil || slot.Dispatch.Operation == execution.OperationReplay {
		t.Fatalf("the takeover round: %+v %v, want the current Slot as it is", slot, err)
	}
	// Five minutes later, still under epoch 7, the cursor is at 660.
	now = time.Unix(960, 0)
	reader.result = foundProgress(660, 600)
	slot, _, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if len(observer.facts) != 0 || slot.Recovery.Disposition != ReplayExpired {
		t.Fatalf("this owner's own backlog: %+v, takeover facts %+v; want the distance rule, not the takeover's replay",
			slot.Recovery, observer.facts)
	}
}

// A Slot due at the very instant of the takeover was not due before it: the
// new owner was there for it, and it is the new owner's to fall behind on.
func TestASlotDueAtTheTakeoverIsNotDueBeforeIt(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	deadline := int64(slot)*1000 + 25_000
	source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	source.takeovers.Anchor(source.queryGroup, testFence(7), time.Unix(int64(slot), 0))
	operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline, 0, time.Unix(int64(slot)+56, 0), testFence(7))
	if err != nil || operation != execution.OperationNormal || facts.Reason != ReplayExpiredByDistance || len(observer.facts) != 0 {
		t.Fatalf("a Slot due at the takeover: %s %+v %v, takeover facts %+v; want the distance rule", operation, facts, err, observer.facts)
	}
}

func fenceOf(epoch uint64) *execution.OwnerFence {
	fence := testFence(epoch)
	return &fence
}

// Past the replay age a Slot is given up on whoever missed it; one due before
// a takeover says so on its own counter.
func TestASlotDueBeforeATakeoverPastTheReplayAgeIsGivenUp(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	deadline := int64(slot)*1000 + 25_000
	source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	reached := time.UnixMilli(deadline).Add(testRecoveryLimits().MaxReplayAge + time.Second)
	operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline, 0, reached, testFence(7))
	if err != nil || operation != execution.OperationNormal || facts.Reason != ReplayExpiredByAge {
		t.Fatalf("classifyRecovery() = %s %+v %v, want given up on for age", operation, facts, err)
	}
	if len(observer.facts) != 1 || observer.facts[0].Outcome != observability.ReplayTakeoverAgeExceeded {
		t.Fatalf("takeover facts %+v, want one age_exceeded", observer.facts)
	}
}

// The replays run in order, from the cursor, before anything due later: a
// minute-period Query Group taken over five minutes behind is handed its
// missed Slots oldest first, and a Slot that falls due while they run waits
// behind them. Replaying the current Slot first and the old ones after would
// write the Level histories out of order.
func TestTakeoverReplaysRunOldestFirstAndAheadOfWhatFallsDueMeanwhile(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	reader := &fakeProgressReader{result: foundProgress(600, 540), catalog: catalog}
	now := time.Unix(940, 0)
	source := mustProductionSlotSource(t, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, reader, now)
	source.now = func() time.Time { return now }
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	for _, want := range []execution.EvaluationTime{600, 660, 720, 780, 840, 900} {
		reader.result = foundProgress(want, want-60)
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due {
			t.Fatalf("Next() at %v = %+v due %v err %v", now, slot, due, err)
		}
		if got := slot.Contract.Slot.EvaluationTime; got != want || slot.Dispatch.Operation != execution.OperationReplay {
			t.Fatalf("at %v the source handed %d (%s), want %d replayed: the missed Slots go oldest first", now.Unix(), got,
				slot.Dispatch.Operation, want)
		}
		// Each replay takes twenty seconds; by the last one the Slot at 960
		// has fallen due, and still waits behind the Slots before it.
		now = now.Add(20 * time.Second)
	}
	if len(observer.facts) != 6 {
		t.Fatalf("takeover replays counted %d, want the six Slots due before the takeover", len(observer.facts))
	}
	// 960 fell due after the takeover. By now it is two grid points behind,
	// which the distance rule allows: it is this owner's own falling behind,
	// replayed by that rule, and not one more Slot due before the takeover.
	reader.result = foundProgress(960, 900)
	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.Contract.Slot.EvaluationTime != 960 {
		t.Fatalf("after the replays: %+v due %v err %v, want 960", slot, due, err)
	}
	if len(observer.facts) != 6 || slot.Recovery.Distance > testRecoveryLimits().MaxReplaySlots {
		t.Fatalf("960 counted as a takeover replay (%d facts) or past the distance rule (%d)", len(observer.facts), slot.Recovery.Distance)
	}
}
