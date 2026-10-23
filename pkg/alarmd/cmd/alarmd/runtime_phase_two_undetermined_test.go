// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// undeterminedSchedule is a due index that holds one wake for every object.
type undeterminedSchedule struct{ wake fleet.WakeFacts }

func (schedule undeterminedSchedule) Census(time.Time, int) fleet.ScheduleCensus {
	return fleet.ScheduleCensus{}
}

func (schedule undeterminedSchedule) WakeOf(string) fleet.WakeFacts { return schedule.wake }

// An owned Query Group that never gets a round is, until its first Slot
// since the takeover is runnable, only waiting for it: not undetermined, and
// no UNDETERMINED gap. Once that Slot is runnable and still no round has
// come, the deployment names it under Undetermined - its Query Group, when
// this replica took it and the replica - and fleet_gaps counts UNDETERMINED.
// A group stuck before it ever prepares a Schedule reads exactly so.
func TestAGroupThatNeverGetsARoundIsNamedUndeterminedOnceItsFirstRoundIsDue(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(1_800_000_000, 0)}
	ownedAt := clock.now()
	firstSlot := ownedAt.Add(30 * time.Minute)
	publisher := fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned:    func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-stuck"} },
		schedule: undeterminedSchedule{wake: fleet.WakeFacts{Known: true, IntervalSeconds: 3600, NextSlot: firstSlot, DueAt: firstSlot}}}
	judge := func() (fleet.View, bool) {
		snapshot := publisher.snapshot(context.Background())
		view := fleet.Aggregate(fleet.Expectation{QueryGroups: 1, Known: true}, []fleet.Snapshot{snapshot}, []string{"replica-1"},
			clock.now(), time.Minute)
		counted := false
		for _, gap := range fleetVerdictOf(view, fleet.ReplicaPart{}, clock.now()).Gaps {
			counted = counted || (gap.Value == string(fleet.GapUndetermined) && gap.Count > 0)
		}
		return view, counted
	}

	clock.at = ownedAt.Add(10 * time.Minute)
	view, counted := judge()
	if view.UndeterminedTotal != 0 || len(view.Undetermined) != 0 || counted {
		t.Fatalf("before its first Slot is runnable: undetermined %+v (total %d), gap counted %t; want it only waiting",
			view.Undetermined, view.UndeterminedTotal, counted)
	}
	if view.AwaitingFirstRound == nil || view.AwaitingFirstRound.Objects != 1 {
		t.Fatalf("awaiting first round %+v, want the one group", view.AwaitingFirstRound)
	}

	clock.at = firstSlot.Add(time.Minute)
	view, counted = judge()
	if view.UndeterminedTotal != 1 || len(view.Undetermined) != 1 {
		t.Fatalf("past its first Slot: undetermined %+v (total %d), want the group named", view.Undetermined, view.UndeterminedTotal)
	}
	named := view.Undetermined[0]
	if named.QueryGroup != "qg-stuck" || !named.OwnedSince.Equal(ownedAt.Add(10*time.Minute)) || named.Replica != "replica-1" {
		t.Fatalf("named %+v, want qg-stuck, owned since the first publish that saw it, on replica-1", named)
	}
	if !counted {
		t.Fatalf("fleet_gaps does not count UNDETERMINED; gaps %+v", view.Gaps)
	}
}
