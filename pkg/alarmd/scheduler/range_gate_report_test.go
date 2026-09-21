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

// gateObserver collects what the source said about the catch-up path.
type gateObserver struct{ seen []observability.Observation }

func (o *gateObserver) Observe(_ context.Context, observation observability.Observation) {
	o.seen = append(o.seen, observation)
}

func (o *gateObserver) gate(t *testing.T) *observability.RangeGateFacts {
	t.Helper()
	for _, observation := range o.seen {
		if observation.Stage == observability.StageRangeGateDecided {
			if observation.RangeGate == nil {
				t.Fatal("the range gate stage carried no facts")
			}
			return observation.RangeGate
		}
	}
	t.Fatal("a replay-expired Slot was produced and the gate said nothing about the catch-up path")
	return nil
}

// The shape production is actually in: a fifteen-second Query Group, a
// thirty-second completion offset, three replay slots allowed, and a first
// unfinished Slot four grid points behind the clock -- which is the state four
// Query Groups were measured sitting in for hours, shedding one Slot a round
// and never closing the gap.
func fourBehindSource(t *testing.T, observer *gateObserver, withRangeFlight bool) (*ProductionSlotSource, context.Context) {
	t.Helper()
	const interval = int64(15)
	const firstSlot = execution.EvaluationTime(600)
	at := time.Unix(int64(firstSlot)+4*interval, 0)
	schedule := schedulerSchedule(t, interval, firstSlot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(firstSlot, firstSlot-execution.EvaluationTime(interval)), at, testRecoveryLimits())
	source.expiredRangeEnabled = true
	source.observer = observer
	ctx := context.Background()
	if withRangeFlight {
		ctx = context.WithValue(ctx, rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	}
	return source, ctx
}

// Every round that gives up on a Slot says what became of the catch-up path,
// and names the condition that refused it.
//
// The word is checkable against the values printed beside it, which is the
// whole point: the previous reading for these Query Groups was a GAP_SKIPPED
// completion, and no amount of counting those could say why the Query Group
// never caught up.
func TestARoundThatGivesUpOnASlotSaysWhatTheCatchUpPathDid(t *testing.T) {
	t.Run("no range flight is named, and the gate's own values travel", func(t *testing.T) {
		observer := &gateObserver{}
		source, ctx := fourBehindSource(t, observer, false)
		if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		facts := observer.gate(t)
		if facts.Outcome != observability.RangeGateNoRangeFlight {
			t.Fatalf("outcome = %q, want %q: without the range flight in the context the gate cannot admit the "+
				"Slot, and that is the condition to name", facts.Outcome, observability.RangeGateNoRangeFlight)
		}
		// The values behind the word. A reader has to be able to rule the
		// other conditions out from the line itself.
		if !facts.ProgressPresent || !facts.RangeCreationEnabled {
			t.Fatalf("facts = %+v, want Progress present and range creation enabled, so the word cannot be "+
				"confused with those two refusals", facts)
		}
		if facts.ProgressNextSlot != facts.ExpectedNextSlot {
			t.Fatalf("progress next slot %d against expected %d: these two being equal is what rules out "+
				"next_slot_moved", facts.ProgressNextSlot, facts.ExpectedNextSlot)
		}
		if facts.UnfinishedSlotPresent {
			t.Fatalf("facts = %+v, want no unfinished Slot, which is what rules out that refusal", facts)
		}
	})

	t.Run("range creation switched off is a different word", func(t *testing.T) {
		observer := &gateObserver{}
		source, ctx := fourBehindSource(t, observer, true)
		source.expiredRangeEnabled = false
		if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		facts := observer.gate(t)
		if facts.Outcome != observability.RangeGateCreationDisabled || facts.RangeCreationEnabled {
			t.Fatalf("outcome = %q enabled=%v, want %q with the flag reported false",
				facts.Outcome, facts.RangeCreationEnabled, observability.RangeGateCreationDisabled)
		}
	})

	t.Run("a round that does reach the builder reports that too", func(t *testing.T) {
		observer := &gateObserver{}
		source, ctx := fourBehindSource(t, observer, true)
		if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		facts := observer.gate(t)
		// Applied or one of the builder's own words, but never a gate refusal:
		// every gate condition holds in this fixture. Reporting the successful
		// rounds is what gives the refusals a denominator.
		switch facts.Outcome {
		case observability.RangeGateApplied, observability.RangeGateProofTooLarge,
			observability.RangeGateRecoveryDisabled, observability.RangeGatePlansMismatch,
			observability.RangeGateDeadlineNotReached, observability.RangeGateStepsBelowOne,
			observability.RangeGateFreezeFailed:
		default:
			t.Fatalf("outcome = %q, want the gate to have been passed; every one of its conditions holds here, "+
				"so a refusal word means the description disagrees with the gate", facts.Outcome)
		}
	})
}

// The refusal namer agrees with the gate it describes, condition by condition.
//
// Pinned at the predicate because the gate is a single boolean expression:
// through Next() only the first false condition of a given fixture is
// observable, and building a production-shaped fixture for each of the five
// would be five fixtures proving one thing each. The risk this guards is the
// two drifting apart, which is exactly what a per-condition table catches.
func TestTheRefusalNamerAgreesWithTheGateConditionByCondition(t *testing.T) {
	const queryGroup = execution.QueryGroupIdentity("query-group-1")
	const nextSlot = execution.EvaluationTime(600)
	progress := func(next execution.EvaluationTime, unfinished bool) execution.ProgressLoadResult {
		load := foundProgress(next, next-15)
		if unfinished {
			load.Progress.UnfinishedSlot = &execution.UnfinishedSlotProjection{}
		}
		return load
	}
	flight := context.WithValue(context.Background(), rangeFlightContextKey{}, queryGroup)

	for _, testCase := range []struct {
		name     string
		enabled  bool
		load     execution.ProgressLoadResult
		ctx      context.Context
		nextSlot execution.EvaluationTime
		want     string
	}{
		{"creation disabled outranks everything", false, progress(nextSlot, false), flight, nextSlot,
			observability.RangeGateCreationDisabled},
		{"no Progress record", true, execution.ProgressLoadResult{}, flight, nextSlot,
			observability.RangeGateProgressMissing},
		{"the cursor moved under the round", true, progress(nextSlot+15, false), flight, nextSlot,
			observability.RangeGateNextSlotMoved},
		{"a Slot is still held", true, progress(nextSlot, true), flight, nextSlot,
			observability.RangeGateUnfinishedSlotPresent},
		{"no range flight", true, progress(nextSlot, false), context.Background(), nextSlot,
			observability.RangeGateNoRangeFlight},
		{"every condition holds, so the namer must not invent one", true, progress(nextSlot, false), flight, nextSlot,
			observability.RangeGateUnexplained},
		// Order, not just membership. The gate is one short-circuiting
		// expression, so "which condition refused it" is only meaningful as
		// "the first false one in the order the gate writes them" -- and a
		// namer that checks the same five in a different order still returns
		// a true statement about the round while blaming the wrong thing.
		// Every pair below has two conditions false at once.
		{"a moved cursor is blamed before a held Slot", true, progress(nextSlot+15, true), flight, nextSlot,
			observability.RangeGateNextSlotMoved},
		{"a held Slot is blamed before a missing flight", true, progress(nextSlot, true), context.Background(), nextSlot,
			observability.RangeGateUnfinishedSlotPresent},
		{"a missing Progress is blamed before a missing flight", true, execution.ProgressLoadResult{},
			context.Background(), nextSlot, observability.RangeGateProgressMissing},
		{"creation disabled is blamed before all of them", false, execution.ProgressLoadResult{},
			context.Background(), nextSlot, observability.RangeGateCreationDisabled},
	} {
		source := &ProductionSlotSource{expiredRangeEnabled: testCase.enabled, queryGroup: queryGroup}
		got := rangeGateRefusal(source, testCase.load, testCase.nextSlot, testCase.ctx, queryGroup)
		if got != testCase.want {
			t.Fatalf("%s: rangeGateRefusal() = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
