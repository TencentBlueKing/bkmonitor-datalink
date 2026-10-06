// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// distanceExpiryObserver collects what the source reported about the ranges it
// gave up on.
type distanceExpiryObserver struct {
	seen []observability.Observation
}

func (o *distanceExpiryObserver) Observe(_ context.Context, observation observability.Observation) {
	o.seen = append(o.seen, observation)
}

func (o *distanceExpiryObserver) distanceFacts(t *testing.T) (*observability.RangeDistanceFacts, observability.Observation) {
	t.Helper()
	for _, observation := range o.seen {
		if observation.Stage == observability.StageRangeDistanceExpired {
			if observation.RangeDistance == nil {
				t.Fatal("the distance expiry stage carried no facts")
			}
			return observation.RangeDistance, observation
		}
	}
	t.Fatal("the source gave up on a range for distance and reported nothing about it")
	return nil, observability.Observation{}
}

// A range given up on for distance reports the numbers that decided it.
//
// Built to the shape production actually produced: a fifteen-second Query
// Group, whose Slots are the ones the fleet was seen skipping forty percent of.
// Three intervals of replay are allowed, so a Slot is only given up on once the
// clock is at least four intervals past it.
//
// The assertions are on the relations between the reported numbers and the
// range that was actually built, not on constants copied out of the
// implementation: steps has to be the smaller of the two candidate bounds, the
// count has to be the width the range covers, and the label has to agree with
// the two numbers beside it. A test that restated the arithmetic would pass
// against any arithmetic.
func TestARangeGivenUpOnForDistanceReportsWhatDecidedIt(t *testing.T) {
	const interval = int64(15)
	// The first unfinished Slot, and a clock eight intervals past it, which is
	// well beyond the four it takes to be given up on.
	const firstSlot = execution.EvaluationTime(600)
	at := time.Unix(int64(firstSlot)+8*interval, 0)

	schedule := schedulerSchedule(t, interval, firstSlot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(firstSlot, firstSlot-execution.EvaluationTime(interval)), at, testRecoveryLimits())
	source.expiredRangeEnabled = true
	observer := &distanceExpiryObserver{}
	source.observer = observer

	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	slot, due, _, err := source.Next(ctx, "query-group-1")
	if err != nil || !due || slot.ExpiredRange == nil {
		t.Fatalf("Next() = %+v due=%v err=%v; want a range to have been built", slot, due, err)
	}
	proof := slot.ExpiredRange
	if proof.EligibilityV2 == nil || proof.EligibilityV2.Reason != execution.RangeDistanceExpired {
		t.Fatalf("the range was not distance-expired (%+v); this case is about the distance branch", proof.EligibilityV2)
	}

	facts, observation := observer.distanceFacts(t)

	// The Query Group and the Slot the skipping starts at, so the population
	// can be divided by object rather than only counted.
	if observation.Trace.QueryGroupKey != "query-group-1" {
		t.Fatalf("the fact names Query Group %q", observation.Trace.QueryGroupKey)
	}
	if facts.FirstEvaluationTime != int64(firstSlot) {
		t.Fatalf("first evaluation time = %d, want the first unfinished Slot %d", facts.FirstEvaluationTime, firstSlot)
	}
	if facts.IntervalSeconds != interval || facts.MaxReplaySlots != testRecoveryLimits().MaxReplaySlots {
		t.Fatalf("interval = %d and replay slots = %d, want %d and %d; without both the steps cannot be read",
			facts.IntervalSeconds, facts.MaxReplaySlots, interval, testRecoveryLimits().MaxReplaySlots)
	}

	// The two candidate bounds, and the one that actually bound. Both travel:
	// which of them is smaller is the difference between a Query Group far
	// behind the head and one barely past its own deadline.
	distanceBound := facts.HeadSteps - int64(facts.MaxReplaySlots)
	smaller := distanceBound
	if facts.DeadlineSteps < smaller {
		smaller = facts.DeadlineSteps
	}
	if facts.Steps != smaller {
		t.Fatalf("steps = %d, want the smaller of the head bound %d and the deadline bound %d",
			facts.Steps, distanceBound, facts.DeadlineSteps)
	}
	if facts.Steps != int64(proof.Count)-1 || facts.SlotCount != proof.Count {
		t.Fatalf("reported steps %d / count %d do not describe the range that was built (count %d)",
			facts.Steps, facts.SlotCount, proof.Count)
	}
	// The range ends where the reported steps say it ends. This is what ties
	// the numbers to the Slots that were actually given up on.
	wantLast := execution.EvaluationTime(facts.FirstEvaluationTime + facts.Steps*facts.IntervalSeconds)
	if proof.Last.Contract.Slot.EvaluationTime != wantLast {
		t.Fatalf("range ends at %d, but the reported numbers say %d", proof.Last.Contract.Slot.EvaluationTime, wantLast)
	}

	// The label agrees with the two numbers printed beside it, so a reader can
	// check it rather than trust it.
	wantBound := observability.RangeBoundByBoth
	switch {
	case distanceBound < facts.DeadlineSteps:
		wantBound = observability.RangeBoundByDistance
	case facts.DeadlineSteps < distanceBound:
		wantBound = observability.RangeBoundByDeadline
	}
	if facts.BoundBy != wantBound {
		t.Fatalf("bound_by = %q, want %q for head bound %d against deadline bound %d",
			facts.BoundBy, wantBound, distanceBound, facts.DeadlineSteps)
	}
	// And it is "distance" here, which is not an accident of the fixture: the
	// deadline bound can only be the tighter one when a Slot's query deadline
	// is more than MaxReplaySlots intervals after its evaluation time, and at
	// three replay slots against a thirty-second completion offset that needs
	// an interval under about eight seconds. No cohort has one. The label is
	// therefore pinned to "distance" for every interval that exists today, and
	// a reading of anything else means the replay allowance or the offset
	// moved -- which is worth seeing, and is why the label is reported rather
	// than assumed.
	if facts.BoundBy != observability.RangeBoundByDistance {
		t.Fatalf("bound_by = %q at interval %d with %d replay slots, want %q; a different bound here means the "+
			"replay allowance or the completion offset has changed and the skipping has a new shape",
			facts.BoundBy, facts.IntervalSeconds, facts.MaxReplaySlots, observability.RangeBoundByDistance)
	}
}

// The label itself is pinned at the predicate, because two of its three values
// cannot be produced through the real builder at any interval in use.
//
// Leaving them untested because they are unreachable is how a value nobody can
// emit comes to read like a mechanism that is wired up. Leaving them out of the
// vocabulary instead would be worse: the day the replay allowance or the
// completion offset changes, the reading that says so is exactly the one that
// would be missing.
func TestTheBoundLabelNamesWhicheverBoundIsTighter(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		headSteps     int64
		deadlineSteps int64
		replaySlots   uint32
		want          string
	}{
		{"the head bound is tighter", 8, 7, 3, observability.RangeBoundByDistance},
		{"the Slot's own deadline is tighter", 8, 4, 3, observability.RangeBoundByDeadline},
		{"the two agree", 8, 5, 3, observability.RangeBoundByBoth},
		{"no replay allowance leaves the deadline in charge", 8, 4, 0, observability.RangeBoundByDeadline},
	} {
		report := rangeDistanceReport{
			headSteps: testCase.headSteps, deadlineSteps: testCase.deadlineSteps, maxReplaySlots: testCase.replaySlots,
		}
		if got := report.boundBy(); got != testCase.want {
			t.Fatalf("%s: boundBy() = %q, want %q (head %d - %d replay slots against deadline %d)",
				testCase.name, got, testCase.want, testCase.headSteps, testCase.replaySlots, testCase.deadlineSteps)
		}
	}
}

// An age-expired range reports nothing here, because the two candidate bounds
// it would report were never computed.
//
// Zero-filling them would put two numbers that do not exist next to two that
// do, and a reader dividing the skipped population by bound_by would find a
// third of it in a bucket that means "not applicable".
func TestAnAgeExpiredRangeReportsNoDistanceNumbers(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(120, 60), time.UnixMilli(955000), testRecoveryLimits())
	source.expiredRangeEnabled = true
	observer := &distanceExpiryObserver{}
	source.observer = observer

	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	slot, due, _, err := source.Next(ctx, "query-group-1")
	if err != nil || !due || slot.ExpiredRange == nil {
		t.Fatalf("Next() = %+v due=%v err=%v; want a range", slot, due, err)
	}
	if reason := slot.ExpiredRange.EligibilityV2; reason == nil || reason.Reason != execution.RangeAgeExpired {
		t.Fatalf("this fixture was meant to age-expire, got %+v", reason)
	}
	for _, observation := range observer.seen {
		if observation.Stage == observability.StageRangeDistanceExpired {
			t.Fatalf("an age-expired range reported distance numbers: %+v", observation.RangeDistance)
		}
	}
}
