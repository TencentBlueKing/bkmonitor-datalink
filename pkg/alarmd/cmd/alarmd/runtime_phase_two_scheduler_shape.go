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
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// schedulerShapeInterval is how often the shape is derived again: the owned
// set and the readiness it is read from change at reconcile, the rates it
// reads are per minute.
const schedulerShapeInterval = time.Minute

// schedulerShapeReading is the shape in force and what it was derived from,
// as the replica's capacity reports it.
type schedulerShapeReading struct {
	Shape  config.DerivedScheduler
	Demand config.SchedulerDemand
	At     time.Time
	// Applied is whether the coordinator took the shape; a shape it refused
	// leaves the previous one in force.
	Applied bool
}

// schedulerShaper sizes this process's query permits, execution slots and
// queues from the work it owns and how fast it is served
// (config.DeriveSchedulerShape), once a minute, inside the ceiling the
// container's memory set at start. Before its first derivation, and in a
// bundle with no query coordinator, the startup shape stands.
type schedulerShaper struct {
	flights *scheduler.FlightCoordinator
	ceiling config.DerivedScheduler
	// startedAt is when the shaper began counting: the first derivation is
	// a full window of measurement later, never on the counts of a process
	// that has not yet run a minute.
	startedAt time.Time
	limits    scheduler.RecoveryLimits
	now       func() time.Time

	mu sync.Mutex
	// executions is the last day's Slot execution lengths, and ready the
	// Slots made ready by second.
	executions scheduler.HourlyDurations
	ready      scheduler.SecondPeak
	// last is the recovery request count at the previous derivation, the
	// replay rate's base.
	last struct {
		at       time.Time
		recovery uint64
	}
	reading atomic.Pointer[schedulerShapeReading]
}

func newSchedulerShaper(flights *scheduler.FlightCoordinator, cfg config.Config, now func() time.Time) *schedulerShaper {
	s := cfg.PhaseTwo.Scheduler
	shaper := &schedulerShaper{flights: flights, limits: s.RecoveryLimits(), now: now, startedAt: now(),
		ceiling: config.DerivedScheduler{ActiveExecutions: s.ActiveExecutionLimit, ProcessQueryPermits: s.ProcessQueryPermits,
			RecoveryQueryPermits: s.RecoveryQueryPermits, ReadyQueueCapacity: s.ReadyQueueCapacity,
			RecoveryQueueCapacity: s.RecoveryQueueCapacity, ActiveExecutionCeiling: max(s.ActiveExecutionCeiling, s.ActiveExecutionLimit)}}
	return shaper
}

// shape is the shape in force: the last derived, or the startup one.
func (shaper *schedulerShaper) shape() config.DerivedScheduler {
	if shaper == nil {
		return config.DerivedScheduler{}
	}
	if reading := shaper.reading.Load(); reading != nil && reading.Applied {
		return reading.Shape
	}
	return shaper.ceiling
}

// recordExecution keeps one Slot execution's length.
func (shaper *schedulerShaper) recordExecution(length time.Duration) {
	if shaper == nil || length <= 0 {
		return
	}
	shaper.mu.Lock()
	defer shaper.mu.Unlock()
	shaper.executions.Record(shaper.now(), length)
}

// executionStarted marks a Slot execution's start; the function it returns
// records its length when the execution returns.
func (shaper *schedulerShaper) executionStarted() func() {
	if shaper == nil {
		return func() {}
	}
	began := shaper.now()
	return func() { shaper.recordExecution(shaper.now().Sub(began)) }
}

// recordReady counts one Slot made ready to run.
func (shaper *schedulerShaper) recordReady() {
	if shaper == nil {
		return
	}
	shaper.mu.Lock()
	defer shaper.mu.Unlock()
	shaper.ready.Add(shaper.now())
}

// due reports whether a derivation is due.
func (shaper *schedulerShaper) due() bool {
	if shaper == nil || shaper.flights == nil {
		return false
	}
	shaper.mu.Lock()
	defer shaper.mu.Unlock()
	since := shaper.last.at
	if since.IsZero() {
		since = shaper.startedAt
	}
	return shaper.now().Sub(since) >= schedulerShapeInterval
}

// derive sizes the shape from what the last minute asked of this process
// and puts it in force.
//
// The peaks are measured where the work arrives, not predicted from the
// schedules: the most normal query permits asked for in one second, and the
// most Slots made ready in one second. That is where the reads are
// synchronized, whatever spreads or piles them - their schedules, their read
// phases, their holds - and it is counted before any bound throttles it, so a
// bound too small does not hide the demand that would raise it.
func (shaper *schedulerShaper) derive(owned int) schedulerShapeReading {
	shaper.mu.Lock()
	now := shaper.now()
	_, recovery := shaper.flights.AcquisitionCounts()
	replayRate := 0.0
	if elapsed := now.Sub(shaper.last.at).Seconds(); !shaper.last.at.IsZero() && elapsed > 0 && recovery >= shaper.last.recovery {
		replayRate = float64(recovery-shaper.last.recovery) / elapsed
	}
	shaper.last.at, shaper.last.recovery = now, recovery
	execution := shaper.executions.Reference(now)
	peakSlots := shaper.ready.Peak(now)
	shaper.mu.Unlock()

	demand := config.SchedulerDemand{PeakSlotsPerSecond: float64(peakSlots),
		PeakQueriesPerSecond: float64(shaper.flights.PeakNormalRequestsPerSecond()), ReplayQueriesPerSecond: replayRate,
		Hold: shaper.flights.HoldReference(), Execution: execution,
		OwnedQueryGroups: owned, MaxQueuedItemsPerQG: shaper.limits.MaxQueuedItemsPerQG}
	occupancy := shaper.flights.QueryPermitOccupancy()
	held := occupancy.LookbackInflight
	for _, count := range occupancy.Inflight {
		held += count
	}
	shape := config.DeriveSchedulerShape(shaper.ceiling, demand, shaper.shape(), held)
	limits := shaper.limits
	limits.ProcessQueryPermits, limits.RecoveryQueryPermits = shape.ProcessQueryPermits, shape.RecoveryQueryPermits
	limits.ReadyQueueCapacity, limits.RecoveryQueueCapacity = shape.ReadyQueueCapacity, shape.RecoveryQueueCapacity
	reading := schedulerShapeReading{Shape: shape, Demand: demand, At: now, Applied: shaper.flights.ResizeQueryLimits(limits) == nil}
	shaper.reading.Store(&reading)
	return reading
}

// facts is the shape in force and its inputs as the replica's capacity
// reports them; nil before the first derivation.
func (shaper *schedulerShaper) facts() *fleet.SchedulerShape {
	if shaper == nil {
		return nil
	}
	reading := shaper.reading.Load()
	if reading == nil {
		return nil
	}
	shape, demand := reading.Shape, reading.Demand
	return &fleet.SchedulerShape{DerivedAt: reading.At.UTC(), Applied: reading.Applied,
		ProcessQueryPermits: shape.ProcessQueryPermits, RecoveryQueryPermits: shape.RecoveryQueryPermits,
		ActiveExecutions: shape.ActiveExecutions, ReadyQueueCapacity: shape.ReadyQueueCapacity,
		RecoveryQueueCapacity: shape.RecoveryQueueCapacity, ActiveExecutionCeiling: shape.ActiveExecutionCeiling,
		PeakQueriesPerSecond: demand.PeakQueriesPerSecond, ReplayQueriesPerSecond: demand.ReplayQueriesPerSecond,
		PeakSlotsPerSecond: demand.PeakSlotsPerSecond, HoldMillis: demand.Hold.Milliseconds(),
		ExecutionMillis: demand.Execution.Milliseconds(), OwnedQueryGroups: demand.OwnedQueryGroups}
}
