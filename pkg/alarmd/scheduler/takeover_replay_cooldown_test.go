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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group the degraded pool holds is not replayed past the distance
// rule though its Slot was due before the takeover: the replay's query
// would be held like every other, and the pool gives its Slots up for
// distance as it did before takeovers were told apart. Nothing is reported
// as a takeover replay for it.
func TestAQueryGroupTheDegradedPoolHoldsIsNotReplayedPastTheDistanceRule(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	deadline := int64(slot)*1000 + 25_000
	reached := time.Unix(int64(slot)+56, 0)
	for _, held := range []bool{false, true} {
		source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
		observer := &takeoverObserver{}
		source.observer, source.takeovers = observer, NewTakeoverClock()
		operation, facts, err := source.classifyRecovery(withQueryCooldownHeld(context.Background(), held), slot, deadline, 0, reached, testFence(7))
		if err != nil {
			t.Fatalf("held %v: classifyRecovery() error = %v", held, err)
		}
		if held {
			if operation != execution.OperationNormal || facts.Disposition != ReplayExpired || facts.Reason != ReplayExpiredByDistance || len(observer.facts) != 0 {
				t.Fatalf("held: %s %+v, takeover facts %+v; want given up on for distance, nothing reported", operation, facts, observer.facts)
			}
			continue
		}
		if operation != execution.OperationReplay || len(observer.facts) != 1 {
			t.Fatalf("not held: %s %+v, takeover facts %+v; want the takeover replay", operation, facts, observer.facts)
		}
	}
}

// A Slot classified again -- its replay failed and is retried, or its
// Runner woke before it ran -- is one Slot: reported once as replayed. Given
// up on for its age later it is reported once more, under that outcome, and
// the next Slot is its own.
func TestASlotClassifiedAgainIsReportedOncePerOutcome(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	deadline := int64(slot)*1000 + 25_000
	reached := time.Unix(int64(slot)+56, 0)
	source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	outcomes := func() []string {
		var out []string
		for _, facts := range observer.facts {
			out = append(out, facts.Outcome)
		}
		return out
	}
	for i := 0; i < 3; i++ {
		if operation, _, err := source.classifyRecovery(context.Background(), slot, deadline, 0, reached.Add(time.Duration(i)*time.Second), testFence(7)); err != nil || operation != execution.OperationReplay {
			t.Fatalf("classification %d = %s %v, want a replay", i, operation, err)
		}
	}
	if got := outcomes(); fmt.Sprint(got) != "[replayed]" {
		t.Fatalf("after three classifications of one Slot: %v, want it reported once", got)
	}
	aged := time.UnixMilli(deadline).Add(testRecoveryLimits().MaxReplayAge + time.Second)
	for i := 0; i < 2; i++ {
		if _, facts, err := source.classifyRecovery(context.Background(), slot, deadline, 0, aged, testFence(7)); err != nil || facts.Reason != ReplayExpiredByAge {
			t.Fatalf("aged classification %d = %+v %v", i, facts, err)
		}
	}
	if got := outcomes(); fmt.Sprint(got) != "[replayed age_exceeded]" {
		t.Fatalf("after the Slot aged out: %v, want it reported once more, as age_exceeded", got)
	}
	next := slot + 10
	if _, _, err := source.classifyRecovery(context.Background(), next, int64(next)*1000+25_000, 0, reached.Add(10*time.Second), testFence(7)); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(); fmt.Sprint(got) != "[replayed age_exceeded replayed]" {
		t.Fatalf("after the next Slot: %v, want it reported as its own", got)
	}
}

// cooldownFlagSource records what each Next was told about the degraded pool.
type cooldownFlagSource struct {
	*fakeSlotSource
	held []bool
}

func (source *cooldownFlagSource) Next(ctx context.Context, queryGroup execution.QueryGroupIdentity) (FrozenSlot, bool, SlotDueFacts, error) {
	source.held = append(source.held, queryCooldownHeld(ctx))
	return source.fakeSlotSource.Next(ctx, queryGroup)
}

// The Runner tells its source when the pool holds its queries: not before
// the cooldown, while it runs, and not once it has run out.
func TestTheRunnerTellsItsSourceWhileThePoolHoldsItsQueries(t *testing.T) {
	now := time.Unix(100, 0)
	inner := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
	inner.slot.EarliestQueryDeadlineUnixMilli = 150_000
	source := &cooldownFlagSource{fakeSlotSource: inner}
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{
		unavailableResult(), unavailableResult(), unavailableResult(),
		{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable},
	}}
	limits := testRecoveryLimits()
	limits.QueryUnavailableCooldown = true
	flights, err := NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: inner.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		inner.slot.Contract.DuePlanSetDigest = execution.DuePlanSetDigest(fmt.Sprintf("slot-digest-%d", i))
		inner.slot.DuePlanTargets.DuePlanSetDigest = inner.slot.Contract.DuePlanSetDigest
		inner.slot.Contract.Slot.EvaluationTime = execution.EvaluationTime(100 + i)
		inner.slot.ExpectedNextSlot = inner.slot.Contract.Slot.EvaluationTime
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("round %d: %t %v", i, attempted, err)
		}
	}
	inner.slot.Contract.Slot.EvaluationTime++
	inner.slot.ExpectedNextSlot++
	inner.slot.Contract.DuePlanSetDigest = "next-slot-digest"
	inner.slot.DuePlanTargets.DuePlanSetDigest = inner.slot.Contract.DuePlanSetDigest
	if _, attempted, _, err := runner.RunOneAdmitted(context.Background(), func(execution.Operation) (func(), bool) { return func() {}, true }); err != nil || attempted {
		t.Fatalf("the pool let a query through: %t %v", attempted, err)
	}
	now = runner.queryCooldown.until
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatal(attempted, err)
	}
	if got := fmt.Sprint(source.held); got != "[false false false true false]" {
		t.Fatalf("the source was told %s, want held only while the cooldown ran", got)
	}
}

// What was reported survives a Runner rebuilt here -- the same Slot is not
// reported again -- and starts afresh on a real takeover, where every Slot
// before it is new to this owner.
func TestReportedSlotsSurviveARebuildAndResetOnATakeover(t *testing.T) {
	clock := NewTakeoverClock()
	const group = execution.QueryGroupIdentity("qg")
	at := time.Unix(1_700_124_100, 0)
	clock.Anchor(group, testFence(7), at)
	if !clock.FirstClassification(group, 1_700_124_000, "replayed") || clock.FirstClassification(group, 1_700_124_000, "replayed") {
		t.Fatal("a Slot is reported on its first classification only")
	}
	clock.Anchor(group, testFence(8), at.Add(time.Minute))
	if clock.FirstClassification(group, 1_700_124_000, "replayed") {
		t.Fatal("a Runner rebuilt here reported the same Slot again")
	}
	clock.Anchor(group, testFence(10), at.Add(2*time.Minute))
	if !clock.FirstClassification(group, 1_700_124_000, "replayed") {
		t.Fatal("a real takeover kept the previous owner's reports")
	}
	if !(*TakeoverClock)(nil).FirstClassification(group, 1, "replayed") || !clock.FirstClassification("qg-never-held", 1, "replayed") {
		t.Fatal("a nil clock, or a Query Group it holds no takeover of, reports every classification")
	}
}

// With the pool turned off a record restored from before still names a
// future end, and the Runner lets the query run (deferUnavailableQuery
// clears it); the source is not told the pool holds it, or a Slot due
// before the takeover would be given up on while its query runs.
func TestARestoredCooldownWithThePoolOffHoldsNothing(t *testing.T) {
	now := time.Unix(100, 0)
	inner := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
	inner.slot.EarliestQueryDeadlineUnixMilli = 150_000
	source := &cooldownFlagSource{fakeSlotSource: inner}
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}}}
	limits := testRecoveryLimits()
	limits.QueryUnavailableCooldown = false
	flights, err := NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: inner.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryCooldownStore()
	store.records["query-group-1"] = QueryCooldownRecord{QueryGroup: "query-group-1", Failures: 3, Until: now.Add(10 * time.Minute)}
	runner.WithQueryCooldownStore(store)
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("the query did not run with the pool off: %t %v", attempted, err)
	}
	if got := fmt.Sprint(source.held); got != "[false]" {
		t.Fatalf("the source was told %s, want not held with the pool off", got)
	}
}
