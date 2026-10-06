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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The counts that say whether the permit budget is the constraint reach the
// page.
//
// This handoff had no check. The pair is counted in the scheduler, translated
// here, aggregated across replicas and rendered -- and a value dropped at any
// one of those arrives as a zero, which on this particular pair reads as "no
// caller ever waited". That is the reassuring answer, and it is the one the
// panel gave for as long as it had only the two gauges.
//
// Driven through a real coordinator rather than a stub, because what is being
// checked is that the translation reads the fields the coordinator actually
// fills, and a stub would agree with whatever the translation asked for.
func TestWhetherAnyoneWaitedForAPermitReachesTheCapacityView(t *testing.T) {
	// Recovery limits, because a coordinator without them refuses every permit
	// and nothing would be counted to lose.
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(scheduler.RecoveryLimits{
		ProcessQueryPermits: 2, RecoveryQueryPermits: 1,
		ReadyQueueCapacity: 8, RecoveryQueueCapacity: 8,
		MaxQueuedItemsPerQG: 2,
		MaxReplaySlots:      3, MaxReplayAge: 10 * time.Minute,
		RetryMinDelay: time.Second, RetryMaxDelay: 8 * time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := time.Now().Add(time.Minute)
	permit, err := flights.AcquireQueryPermit(ctx,
		execution.SlotIdentity{QueryGroup: "a", EvaluationTime: 60}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	defer permit.Release()

	occupancy := flights.QueryPermitOccupancy()
	if occupancy.Acquires == 0 {
		t.Fatal("the coordinator recorded no acquire, so this check cannot tell a dropped field " +
			"from a field that was never set")
	}

	source := capacitySnapshotSource(flights, config.Config{}, fleet.NewRejectionTally(), nil, nil)
	capacity := source()
	if capacity == nil {
		t.Fatal("no capacity was published")
	}
	if capacity.PermitAcquires != occupancy.Acquires {
		t.Errorf("permit_acquires crossed as %d, want %d -- the denominator of the only share on "+
			"this panel that can see a full budget", capacity.PermitAcquires, occupancy.Acquires)
	}
	if capacity.PermitWaits != occupancy.Queued {
		t.Errorf("permit_waits crossed as %d, want %d -- dropped, it reads as nobody having waited",
			capacity.PermitWaits, occupancy.Queued)
	}
}
