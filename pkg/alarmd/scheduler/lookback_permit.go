// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import "sync"

// The lookback reads a finished Slot's window again, later, to see what
// arrived after the formal read. It spends the same query budget as
// detection and must never be the reason a formal query waits. A permit for
// it is therefore granted only from permits nobody is waiting for, and never
// queued for:
//
//   - nobody is waiting for a query permit, normal or recovery;
//   - a process permit is free.
//
// It is not held to a share of the pool. Queries are waits on the query
// service, not work of this process, and a count of them is the wrong thing
// to divide: a permit nobody is waiting for costs a formal query nothing.
// What a count would guard against - a free permit taken the moment before a
// burst takes the rest - is answered by the yield instead: the moment a
// formal query has to wait for a permit, every lookback permit held is asked
// to yield (LookbackPermit.Yield), and the reads stop and give their permits
// back, so a formal query waits for no lookback read longer than one takes
// to stop.
//
// A lookback permit is not an Operation. It has its own inflight count and
// held seconds, so the normal and recovery readings the capacity is sized on
// never include it, while the process total does.

// Why a lookback permit was not granted, closed.
const (
	LookbackRefusedDisabled = "disabled"
	LookbackRefusedWaiting  = "waiters"
	// LookbackRefusedFull: every process permit is held.
	LookbackRefusedFull = "full"
)

// LookbackRefusals is every reason a lookback permit is refused.
var LookbackRefusals = []string{LookbackRefusedDisabled, LookbackRefusedWaiting, LookbackRefusedFull}

// LookbackPermit is one lookback query's share of the query budget.
type LookbackPermit struct {
	coordinator *FlightCoordinator
	id          uint64
	once        sync.Once
	yield       chan struct{}
}

// Yield is closed when a formal query has to wait for a permit: the read
// holding this permit stops and releases it.
func (permit *LookbackPermit) Yield() <-chan struct{} {
	return permit.yield
}

// Release returns the permit; a second call does nothing.
func (permit *LookbackPermit) Release() {
	if permit == nil || permit.coordinator == nil {
		return
	}
	permit.once.Do(func() { permit.coordinator.releaseLookbackPermit(permit.id) })
}

// TryAcquireLookbackPermit grants a lookback permit now or refuses with the
// reason; it never waits and never queues.
func (coordinator *FlightCoordinator) TryAcquireLookbackPermit() (*LookbackPermit, string) {
	if coordinator == nil || !coordinator.recoveryEnabled {
		return nil, LookbackRefusedDisabled
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.expireWaitersLocked(&coordinator.normalWaiters)
	coordinator.expireWaitersLocked(&coordinator.recoveryWaiters)
	if len(coordinator.normalWaiters) > 0 || len(coordinator.recoveryWaiters) > 0 || len(coordinator.recoveryChannelWaiters) > 0 {
		return nil, LookbackRefusedWaiting
	}
	if coordinator.queryInflight >= coordinator.limits.ProcessQueryPermits {
		return nil, LookbackRefusedFull
	}
	coordinator.queryInflight++
	coordinator.lookbackInflight++
	coordinator.permitSequence++
	permit := &LookbackPermit{coordinator: coordinator, id: coordinator.permitSequence, yield: make(chan struct{})}
	if coordinator.lookbackHeld == nil {
		coordinator.lookbackHeld = make(map[uint64]heldPermit)
	}
	coordinator.lookbackHeld[permit.id] = heldPermit{since: coordinator.now(), yield: permit.yield}
	return permit, ""
}

func (coordinator *FlightCoordinator) releaseLookbackPermit(id uint64) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.queryInflight > 0 {
		coordinator.queryInflight--
	}
	if coordinator.lookbackInflight > 0 {
		coordinator.lookbackInflight--
	}
	if held, ok := coordinator.lookbackHeld[id]; ok {
		delete(coordinator.lookbackHeld, id)
		if elapsed := coordinator.now().Sub(held.since); elapsed > 0 {
			coordinator.lookbackSeconds += elapsed.Seconds()
		}
	}
	// The permit freed room in the shared pool: a formal query waiting for it
	// gets it now, not at the next release.
	coordinator.dispatchQueryPermitsLocked()
}

// yieldLookbackLocked asks every lookback permit held to yield, once each: a
// formal query is waiting for a permit.
func (coordinator *FlightCoordinator) yieldLookbackLocked() {
	for id, held := range coordinator.lookbackHeld {
		if held.yield == nil {
			continue
		}
		close(held.yield)
		held.yield = nil
		coordinator.lookbackHeld[id] = held
	}
}
