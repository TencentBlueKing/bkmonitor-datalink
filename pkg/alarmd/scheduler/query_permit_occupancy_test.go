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

// The per-operation inflight counts are the only place the deployment can see
// how much of the query permit budget is actually occupied. Everything else
// counts events: how many permits were granted, how long a stage was busy.
// Neither answers "are we at the ceiling right now", which is the question the
// admission budget is sized against.
//
// So they are asserted here on their own, separately from the aggregate
// Inflight that the existing tests cover. The two are maintained by different
// lines and only the per-operation one is exported as a metric, so an aggregate
// that stays correct proves nothing about the numbers an operator reads.
func TestQueryPermitSnapshotCountsHeldPermitsPerOperation(t *testing.T) {
	clock := newMutableClock(time.Unix(300, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := clock.Now().Add(time.Minute)

	first, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "a", EvaluationTime: 60}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalInflight != 1 {
		t.Fatalf("one held normal permit reported as NormalInflight=%d (aggregate Inflight=%d): %+v",
			snapshot.NormalInflight, snapshot.Inflight, snapshot)
	}

	second, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "b", EvaluationTime: 60}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	// Two held at once is the whole point: a counter that only ever reaches one
	// cannot show a budget filling up, and the ceiling here is two.
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalInflight != 2 {
		t.Fatalf("two held normal permits reported as NormalInflight=%d (aggregate Inflight=%d): %+v",
			snapshot.NormalInflight, snapshot.Inflight, snapshot)
	}

	first.Release()
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalInflight != 1 {
		t.Fatalf("after one release NormalInflight=%d, want 1: %+v", snapshot.NormalInflight, snapshot)
	}
	second.Release()
	if snapshot := flights.queryPermitSnapshot(); snapshot.NormalInflight != 0 {
		t.Fatalf("after both releases NormalInflight=%d, want 0: %+v", snapshot.NormalInflight, snapshot)
	}
}

// The metric is set from the facts carried on the observation, not from the
// snapshot the tests above read, so the observation is asserted too: a
// coordinator that counts correctly but publishes a stale or empty fact leaves
// the operator reading the same zero.
func TestQueryPermitObservationCarriesHeldPermitCount(t *testing.T) {
	clock := newMutableClock(time.Unix(400, 0))
	limits := testRecoveryLimits()
	flights, err := NewFlightCoordinatorWithRecovery(limits, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	recorded := &permitFactRecorder{}
	flights.observer = recorded
	ctx := context.Background()
	deadline := clock.Now().Add(time.Minute)

	first, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "a", EvaluationTime: 60}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	second, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "b", EvaluationTime: 60}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { first.Release(); second.Release() }()

	if peak := recorded.peakNormalInflight(); peak != 2 {
		t.Fatalf("highest NormalInflight ever published = %d, want 2; the exported gauge cannot exceed this", peak)
	}
}

// Occupancy has to be measurable as an average over a window, which means the
// time a permit was held has to be accumulated somewhere durable rather than
// inferred from a level that is only published when the level changes.
func TestQueryPermitOccupancyAccumulatesHeldTime(t *testing.T) {
	clock := newMutableClock(time.Unix(500, 0))
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	permit, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "a", EvaluationTime: 60}, execution.OperationNormal, clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(4 * time.Second)

	// A permit still held counts. Otherwise a query that runs for the whole
	// window contributes nothing to the window it filled, and the reading is
	// lowest exactly when occupancy is highest.
	if seconds := flights.QueryPermitOccupancy().HeldSeconds[execution.OperationNormal]; seconds != 4 {
		t.Fatalf("held-but-not-released permit contributed %v seconds, want 4", seconds)
	}

	permit.Release()
	clock.Advance(6 * time.Second)
	occupancy := flights.QueryPermitOccupancy()
	// Releasing moves the same duration from "still held" into the accumulator.
	// It must not add a second copy, and it must not keep growing afterwards.
	if seconds := occupancy.HeldSeconds[execution.OperationNormal]; seconds != 4 {
		t.Fatalf("after release held seconds = %v, want the same 4 seconds counted once", seconds)
	}
	if occupancy.Inflight[execution.OperationNormal] != 0 {
		t.Fatalf("released permit still counted as held: %+v", occupancy)
	}
	if occupancy.Budget != testRecoveryLimits().ProcessQueryPermits {
		t.Fatalf("budget = %d, want the configured ceiling", occupancy.Budget)
	}
}

// A permit that is never released is the case worth seeing most, and the one a
// released-only accumulator would report as zero forever.
func TestQueryPermitOccupancyKeepsCountingAStuckPermit(t *testing.T) {
	clock := newMutableClock(time.Unix(600, 0))
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flights.AcquireQueryPermit(context.Background(),
		execution.SlotIdentity{QueryGroup: "stuck", EvaluationTime: 60},
		execution.OperationNormal, clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	first := flights.QueryPermitOccupancy().HeldSeconds[execution.OperationNormal]
	clock.Advance(time.Minute)
	second := flights.QueryPermitOccupancy().HeldSeconds[execution.OperationNormal]
	if first != 60 || second != 120 {
		t.Fatalf("stuck permit occupancy = %v then %v, want 60 then 120", first, second)
	}
}

type permitFactRecorder struct {
	facts []observability.QueryPermitFacts
}

func (recorder *permitFactRecorder) Observe(_ context.Context, observation observability.Observation) {
	if observation.QueryPermit == nil {
		return
	}
	recorder.facts = append(recorder.facts, *observation.QueryPermit)
}

func (recorder *permitFactRecorder) peakNormalInflight() int {
	peak := 0
	for _, fact := range recorder.facts {
		if fact.NormalInflight > peak {
			peak = fact.NormalInflight
		}
	}
	return peak
}
