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

// A Slot that is both too far behind and held past its own window is reported
// as the contradiction, not as the distance.
//
// Both are true of it, and they are not two ways of saying one thing. Distance
// means the Query Group fell behind and the gap is accepted. The contradiction
// means the readiness rule and the replay window were derived from settings
// that disagree, and somebody can fix it. Deciding distance first buries the
// second inside the first exactly where it matters most: a Query Group held
// past its window on every Slot is also too far by the time anyone reads it,
// so it would report the accepted gap forever and the fixable defect would
// never be named once.
//
// The two rows are the same contradiction at two distances. The first is
// inside the limit and was already reported before this ordering; the second
// is past it, and is the one that was previously invisible.
func TestAReplayHeldPastItsWindowIsNamedEvenWhenItIsAlsoTooFar(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	for _, test := range []struct {
		name         string
		reachedAfter int64
		wantDistance uint32
	}{
		{name: "inside the distance limit", reachedAfter: 3, wantDistance: 2},
		{name: "already past the distance limit", reachedAfter: 15, wantDistance: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := testRecoveryLimits()
			limits.MaxReplaySlots = 3
			source, _ := replayClassificationSource(t, 3, slot, 30*time.Second, limits)
			observer := &recordingReplayObserver{}
			source.observer = observer
			deadline := (int64(slot) + 2) * 1000

			operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline,
				time.Unix(int64(slot)+test.reachedAfter, 0))
			if err != nil {
				t.Fatalf("classifyRecovery() error = %v", err)
			}
			if facts.Distance != test.wantDistance {
				t.Fatalf("distance = %d, want %d: the row is built to be at that distance, and the point of "+
					"the second one is that it is past the limit", facts.Distance, test.wantDistance)
			}
			if operation != execution.OperationNormal || facts.Disposition != ReplayExpired ||
				facts.Reason != ReplayExpiredByWait {
				t.Fatalf("classifyRecovery() = %s %+v, want %s. At distance %d both conditions hold, and the "+
					"one that names a fixable disagreement has to win, or it is never seen on a Query Group "+
					"that is permanently in it", operation, facts, ReplayExpiredByWait, facts.Distance)
			}
			// The two instants still travel, so the word can be checked
			// rather than trusted.
			if facts.ReadyAtUnixMilli == 0 || facts.DistanceBoundaryUnixMilli == 0 ||
				facts.ReadyAtUnixMilli < facts.DistanceBoundaryUnixMilli {
				t.Fatalf("compared instants = ready %d boundary %d, want both present with ready at or past "+
					"the boundary", facts.ReadyAtUnixMilli, facts.DistanceBoundaryUnixMilli)
			}
			if len(observer.facts) != 1 || observer.facts[0].Reason != string(ReplayExpiredByWait) {
				t.Fatalf("observed %+v, want the one report to carry the contradiction", observer.facts)
			}
		})
	}
}

// A Query Group that is too far behind but has nothing to catch up says so by
// name, and carries the two bounds that decided it.
//
// This is the shape four Query Groups sat in for hours: fifteen-second grid,
// three replay slots, first unfinished Slot four grid points back. The head
// bound is headSteps minus the replay allowance, which is zero, so the builder
// refuses -- correctly, there is no run of Slots to finalize. Before the six
// words that refusal and "the schedule's Plans disagree" and "this round is
// simply early" all arrived as one bucket, and the reading that was supposed
// to explain a stuck Query Group could not tell them apart.
func TestABuilderRefusalNamesItsOwnConditionAndCarriesTheBounds(t *testing.T) {
	const interval = int64(15)
	const firstSlot = execution.EvaluationTime(600)
	// Three grid points past the first unfinished Slot, which is where the
	// four Query Groups were measured sitting: the walked distance is already
	// four -- past the replay allowance, so the Slot is given up on and the
	// gate is entered -- while the head is only three grid points along, so
	// the head bound is three minus three.
	at := time.Unix(int64(firstSlot)+3*interval, 0)
	schedule := schedulerSchedule(t, interval, firstSlot, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(
		t, catalog, foundProgress(firstSlot, firstSlot-execution.EvaluationTime(interval)), at, testRecoveryLimits())
	source.expiredRangeEnabled = true
	observer := &gateObserver{}
	source.observer = observer
	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	facts := observer.gate(t)
	if facts.Outcome != observability.RangeGateStepsBelowOne {
		t.Fatalf("outcome = %q, want %q: the gate is passed here and the builder's own refusal is that the "+
			"range would be shorter than two Slots", facts.Outcome, observability.RangeGateStepsBelowOne)
	}
	// The bounds are what separate "barely past the window" from "far behind
	// with a deadline that has only just passed". A word without them cannot
	// be checked, and the branches that never compute them must not report
	// zeroes as if they had.
	if !facts.BoundsKnown {
		t.Fatalf("facts = %+v, want the two candidate bounds to travel with a refusal from the branch that "+
			"computes them", facts)
	}
	if facts.DistanceBound > 0 && facts.DeadlineBound > 0 {
		t.Fatalf("bounds = distance %d deadline %d, want at least one of them to be what held the range "+
			"below a single step", facts.DistanceBound, facts.DeadlineBound)
	}
}

// The refusals that never compute the bounds do not report them.
//
// Zero-filling would put two numbers that do not exist beside a word, and a
// reader dividing the refused population by "which bound held" would find part
// of it in a bucket that means "not applicable".
func TestARefusalBeforeTheBoundsAreComputedReportsNoBounds(t *testing.T) {
	observer := &gateObserver{}
	source, ctx := fourBehindSource(t, observer, true)
	source.expiredRangeEnabled = false
	if _, _, _, err := source.Next(ctx, "query-group-1"); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	facts := observer.gate(t)
	if facts.Outcome != observability.RangeGateCreationDisabled {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.RangeGateCreationDisabled)
	}
	if facts.BoundsKnown || facts.DistanceBound != 0 || facts.DeadlineBound != 0 {
		t.Fatalf("facts = %+v, want no bounds: this refusal is decided before either is computed", facts)
	}
}
