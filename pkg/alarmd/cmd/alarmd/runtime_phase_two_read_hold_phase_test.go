// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A read hold is how much later than the settling floor a group's data
// lands, and the group's phase comes after it: read = window end +
// max(floor, landing) + phase. The hold is reckoned from the floor alone; from
// the floor plus the phase it would absorb the phase, and every group whose
// data lands at one moment would read at that moment together.
func TestTheReadHoldIsReckonedFromTheSettlingFloor(t *testing.T) {
	ctx := context.Background()
	f, _, _, _ := delayEdited(t)
	schedule := f.initialSchedule
	spec, err := f.bundle.dependencies.ReadHolds.spec(ctx, schedule)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.bundle.dependencies.Config.PhaseTwo.Access
	var want time.Duration
	for i, plan := range schedule.Plans {
		budget := time.Duration(plan.Spec.CompletionOffsetSeconds())*time.Second - cfg.DownstreamExecutionReserve.Duration()
		floor := min(execution.SettlingWaitWithinQueryBudget(budget, cfg.MinReadyDelay.Duration()), execution.MinimumSettlingWait)
		if i == 0 || floor < want {
			want = floor
		}
	}
	if spec.SettlingWait != want {
		t.Fatalf("hold reckoned from %v after the window, want the settling floor %v", spec.SettlingWait, want)
	}
}

// Two groups whose data lands at the same moment read at different seconds,
// each at its own phase past the landing: at cold start, where both are
// seeded the same arrival, and after each measured the same arrival itself.
// The invariant the stagger exists for; a hold that absorbed the phase broke
// it and read every group on one source in the same second.
func TestGroupsOnOneLandingReadAtTheirOwnPhases(t *testing.T) {
	holds := &productionReadHolds{cfg: validGoAccessRuntimeConfig()}
	holds.cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(30 * time.Second)
	holds.cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(5 * time.Second)
	const budget = 55 * time.Second
	control := seedControl{values: map[execution.QueryGroupIdentity][]byte{}}
	controller, err := readhold.NewController(readhold.Options{Control: control, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(qg execution.QueryGroupIdentity) (readhold.Owner, error) {
			return readhold.Owner{Fence: execution.OwnerFence{QueryGroup: qg, OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"}, ContentScope: "v"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	groups := []execution.QueryGroupIdentity{"group-on-cpu-a", "group-on-cpu-b"}
	phase := func(qg execution.QueryGroupIdentity) time.Duration {
		return execution.ReadWaitWithinQueryBudget(qg, budget, 30*time.Second) - execution.MinimumSettlingWait
	}
	if phase(groups[0]) == phase(groups[1]) {
		t.Fatal("the two groups share a phase; the case cannot tell them apart")
	}
	plan := readhold.PlanRef{Key: execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"}}}
	readAt := func(qg execution.QueryGroupIdentity) time.Duration {
		hold, err := controller.SlotReadHold(context.Background(), readHoldPhaseSchedule(t, qg), 120, execution.OwnerFence{QueryGroup: qg, OwnerID: "w", OwnerEpoch: 1, LeaseToken: "l"})
		if err != nil {
			t.Fatal(err)
		}
		return execution.ReadWaitWithinQueryBudget(qg, budget, 30*time.Second) + hold
	}
	for _, qg := range groups {
		if err := controller.Configure(readhold.GroupSpec{QueryGroup: qg, Plans: []readhold.PlanRef{plan}, HoldLimit: 10 * time.Minute,
			SettlingWait: holds.holdBase(qg, budget), SeedArrivalAge: 30 * time.Second, SeedOrigin: readhold.SeedColdStart}); err != nil {
			t.Fatal(err)
		}
		if err := controller.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{qg}); err != nil {
			t.Fatal(err)
		}
	}
	for _, qg := range groups {
		if got, want := readAt(qg), 30*time.Second+phase(qg); got != want {
			t.Fatalf("%s at cold start reads %v after the window, want the 30 s landing and its %v phase", qg, got, phase(qg))
		}
	}
	for _, qg := range groups {
		evidence := readhold.Evidence{Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 120},
			SnapshotRevision: "s", QueryRevision: "q", ScheduleRevision: "r", ScheduleSegmentStart: 60, DuePlanSetDigest: "d"},
			ArrivalAge: 45 * time.Second, Rung: "rung", Buckets: []int64{120}}
		if err := controller.Observe(context.Background(), evidence); err != nil {
			t.Fatal(err)
		}
	}
	for _, qg := range groups {
		if got, want := readAt(qg), 45*time.Second+phase(qg); got != want {
			t.Fatalf("%s after measuring a 45 s landing reads %v after the window, want the landing and its %v phase", qg, got, phase(qg))
		}
	}
}

// readHoldPhaseSchedule is a one-Plan, one-minute schedule of the group.
func readHoldPhaseSchedule(t *testing.T, qg execution.QueryGroupIdentity) execution.FrozenQueryGroupSchedule {
	t.Helper()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 60}
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plans := []execution.FrozenPlanSchedule{{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"}, ScheduleRevision: revision, Spec: spec}}
	groupRevision, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		t.Fatal(err)
	}
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{Publication: execution.SnapshotPublicationRef{PublicationEpoch: 1,
		SnapshotRevision: "s"}, QueryGroup: qg, QueryRevision: "q", ScheduleRevision: groupRevision, Start: 60}, Plans: plans}
}
