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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func shaperForTest(t *testing.T, clock *atomic.Int64) (*schedulerShaper, *scheduler.FlightCoordinator, config.Config) {
	t.Helper()
	cfg := validGoAccessRuntimeConfig()
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(cfg.PhaseTwo.Scheduler.RecoveryLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	return newSchedulerShaper(flights, cfg, now), flights, cfg
}

// The shaper sizes the coordinator from what the last minute asked of it:
// the busiest second of permit requests and of Slots made ready, the measured
// hold and execution length, the owned groups. The coordinator takes the
// derived budget, and the capacity reports it with its inputs.
func TestTheShaperSizesTheBudgetFromTheMeasuredWork(t *testing.T) {
	clock := &atomic.Int64{}
	clock.Store(time.Unix(3600*100, 0).UnixMilli())
	shaper, flights, cfg := shaperForTest(t, clock)
	if shape := shaper.shape(); shape.ProcessQueryPermits != cfg.PhaseTwo.Scheduler.ProcessQueryPermits {
		t.Fatalf("before any derivation the shape is %+v, want the startup one", shape)
	}
	// Nothing is derived on less than a minute of counting.
	if shaper.due() {
		t.Fatal("a derivation is due before the shaper has counted a minute")
	}
	// Twelve queries asked for in one second, each held 100 ms; eight Slots
	// made ready in that second, each running 2 s.
	deadline := time.UnixMilli(clock.Load()).Add(time.Hour)
	var held []*scheduler.QueryPermit
	for i := 0; i < 12; i++ {
		permit, err := flights.AcquireQueryPermit(context.Background(), execution.SlotIdentity{QueryGroup: "g", EvaluationTime: 60},
			execution.OperationNormal, deadline)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, permit)
	}
	for i := 0; i < 8; i++ {
		shaper.recordReady()
		shaper.recordExecution(2 * time.Second)
	}
	clock.Add(100)
	for _, permit := range held {
		permit.Release()
	}
	startup := shaper.shape()
	reading := shaper.derive(700)
	want := config.DeriveSchedulerShape(shaper.ceiling, config.SchedulerDemand{PeakQueriesPerSecond: 12, PeakSlotsPerSecond: 8,
		Hold: flights.HoldReference(), Execution: reading.Demand.Execution, OwnedQueryGroups: 700,
		MaxQueuedItemsPerQG: cfg.PhaseTwo.Scheduler.MaxQueuedItemsPerQG}, startup, 0)
	if !reading.Applied || reading.Shape != want || reading.Demand.PeakQueriesPerSecond != 12 || reading.Demand.PeakSlotsPerSecond != 8 {
		t.Fatalf("derived %+v from %+v, want %+v applied", reading.Shape, reading.Demand, want)
	}
	if reading.Demand.Execution < 1400*time.Millisecond || reading.Demand.Execution > 2900*time.Millisecond {
		t.Fatalf("execution length %v from 2 s executions", reading.Demand.Execution)
	}
	if occupancy := flights.QueryPermitOccupancy(); occupancy.Budget != want.ProcessQueryPermits || occupancy.RecoveryBudget != want.RecoveryQueryPermits {
		t.Fatalf("coordinator budget %d/%d, derived %d/%d", occupancy.Budget, occupancy.RecoveryBudget, want.ProcessQueryPermits, want.RecoveryQueryPermits)
	}
	if shaper.shape() != want {
		t.Fatalf("shape in force %+v, derived %+v", shaper.shape(), want)
	}
	facts := shaper.facts()
	if facts == nil || facts.ProcessQueryPermits != want.ProcessQueryPermits || facts.PeakQueriesPerSecond != 12 ||
		facts.OwnedQueryGroups != 700 || facts.HoldMillis != flights.HoldReference().Milliseconds() {
		t.Fatalf("capacity reports %+v for %+v", facts, want)
	}
	if shaper.due() {
		t.Fatal("a derivation is due again at once")
	}
	clock.Add(time.Minute.Milliseconds())
	if !shaper.due() {
		t.Fatal("no derivation due a minute later")
	}
	// A minute with no work halves the budget, no more, and never takes it
	// below the floor.
	before := shaper.shape().ProcessQueryPermits
	if idle := shaper.derive(700); idle.Shape.ProcessQueryPermits != max(before/2, 2) || idle.Demand.PeakQueriesPerSecond != 0 {
		t.Fatalf("an idle minute after %d permits derived %+v from %+v", before, idle.Shape, idle.Demand)
	}
}

// Without a coordinator the bundle keeps the startup shape and reports none.
func TestABundleWithoutACoordinatorKeepsTheStartupShape(t *testing.T) {
	var shaper *schedulerShaper
	if shaper.due() || shaper.facts() != nil || shaper.shape() != (config.DerivedScheduler{}) {
		t.Fatal("a nil shaper derived something")
	}
	shaper.recordReady()
	shaper.recordExecution(time.Second)
}

// The dispatcher offers work only while fewer than the bound in force
// execute, not up to its goroutines: with a startup shape under the ceiling,
// one more active Slot than the bound is past it though slots are free.
func TestTheDispatcherOffersWorkOnlyUnderTheBoundInForce(t *testing.T) {
	clock := &atomic.Int64{}
	clock.Store(time.Unix(3600*100, 0).UnixMilli())
	cfg := validGoAccessRuntimeConfig()
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 4
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(cfg.PhaseTwo.Scheduler.RecoveryLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Config: cfg}, shaper: newSchedulerShaper(flights, cfg, now)}
	dispatcher := &phaseTwoRunnerDispatcher{bundle: bundle, active: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{}}
	bound := bundle.schedulerShape().ActiveExecutions
	if bound != 4 || phaseTwoDispatcherFanout(bundle) <= bound {
		t.Fatalf("bound %d, fanout %d: the case needs a bound under the goroutines", bound, phaseTwoDispatcherFanout(bundle))
	}
	for i := 0; i < bound; i++ {
		if dispatcher.slotsFull() {
			t.Fatalf("%d active of %d slots read as full", i, bound)
		}
		dispatcher.active[execution.QueryGroupIdentity(fmt.Sprintf("g%d", i))] = &phaseTwoQueryGroupLifecycle{}
	}
	if !dispatcher.slotsFull() {
		t.Fatalf("%d active of %d slots read as free", len(dispatcher.active), bound)
	}
}

// The demand a budget is sized from is not gated by that budget: with the
// permits at their floor and fifty queries asking in one second, forty-eight
// of them waiting, the next derivation reads fifty, not the two the budget
// let through, and raises the budget to serve them. A controller whose input
// its own output gates reads its own throttling as low demand and stays at
// its floor; this is that case, from the floor.
func TestAThrottledBudgetStillSeesTheDemandItHolds(t *testing.T) {
	clock := &atomic.Int64{}
	clock.Store(time.Unix(3600*100, 0).UnixMilli())
	shaper, flights, cfg := shaperForTest(t, clock)
	floor := cfg.PhaseTwo.Scheduler.RecoveryLimits()
	floor.ProcessQueryPermits, floor.RecoveryQueryPermits = 2, 1
	if err := flights.ResizeQueryLimits(floor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deadline := time.UnixMilli(clock.Load()).Add(time.Hour)
	granted := make(chan *scheduler.QueryPermit, 50)
	for i := 0; i < 50; i++ {
		go func(i int) {
			permit, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("g%d", i)), EvaluationTime: 60},
				execution.OperationNormal, deadline)
			if err == nil {
				granted <- permit
			}
		}(i)
	}
	for flights.PeakNormalRequestsPerSecond() < 50 {
		time.Sleep(time.Millisecond)
	}
	// Two run, held 100 ms each, and are released.
	for i := 0; i < 2; i++ {
		permit := <-granted
		clock.Add(100)
		permit.Release()
	}
	clock.Add(59 * time.Second.Milliseconds())
	reading := shaper.derive(50)
	if reading.Demand.PeakQueriesPerSecond != 50 {
		t.Fatalf("fifty queries asked in one second read as %v", reading.Demand.PeakQueriesPerSecond)
	}
	if reading.Shape.ProcessQueryPermits <= 2 {
		t.Fatalf("the budget stayed at %d with fifty queries asking", reading.Shape.ProcessQueryPermits)
	}
}

// The shaper reads what is held when it derives: a minute without new
// demand does not lower the budget under the permits still in use.
func TestTheShaperKeepsTheBudgetAtWhatIsHeld(t *testing.T) {
	clock := &atomic.Int64{}
	clock.Store(time.Unix(3600*100, 0).UnixMilli())
	shaper, flights, _ := shaperForTest(t, clock)
	budget := shaper.shape().ProcessQueryPermits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deadline := time.UnixMilli(clock.Load()).Add(time.Hour)
	for i := 0; i < budget; i++ {
		permit, err := flights.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("g%d", i)), EvaluationTime: 60},
			execution.OperationNormal, deadline)
		if err != nil {
			t.Fatal(err)
		}
		defer permit.Release()
	}
	// The busiest second is out of the window by the time it derives.
	clock.Add(2 * time.Minute.Milliseconds())
	reading := shaper.derive(10)
	if reading.Demand.PeakQueriesPerSecond != 0 {
		t.Fatalf("the derivation read %v queries a second, want an idle minute", reading.Demand.PeakQueriesPerSecond)
	}
	if reading.Shape.ProcessQueryPermits < budget {
		t.Fatalf("with %d permits held an idle minute derived a budget of %d", budget, reading.Shape.ProcessQueryPermits)
	}
}
