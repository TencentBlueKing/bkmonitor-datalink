// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// What a hold on the retained budget is held for: the four parts of a running
// Slot's stream (retainPhase) and the admitted frames a Plan keeps for its
// write (heldFrames).
const (
	RetainedKindInput  = "input"
	RetainedKindGap    = "gap"
	RetainedKindOutput = "output"
	RetainedKindState  = "state"
	RetainedKindFrames = "frames"
)

var retainPhaseKinds = [retainPhaseCount]string{
	retainPhaseInput: RetainedKindInput, retainPhaseGap: RetainedKindGap,
	retainPhaseOutput: RetainedKindOutput, retainPhaseState: RetainedKindState,
}

// retainedHolding is one live hold as a reader on another goroutine sees it:
// whose Slot, since when, and how many bytes of each kind.
type retainedHolding struct {
	slot      execution.SlotIdentity
	operation execution.Operation
	since     time.Time
	bytes     map[string]uint64
}

// retainedHolderSource is one live hold on the retained budget: a running
// Slot's stream or one Plan's kept frames. Read from another goroutine, so
// what it reports comes from atomics and fields fixed at registration.
type retainedHolderSource interface {
	retainedHolding() retainedHolding
}

// retainedHolderRegistry is the coordinator's live holds. A hold registers
// when it first reserves and leaves when it gives everything back, so the
// registry is what the retained pool holds, by whom.
type retainedHolderRegistry struct {
	mu   sync.Mutex
	live map[retainedHolderSource]struct{}
}

func (registry *retainedHolderRegistry) add(source retainedHolderSource) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.live == nil {
		registry.live = map[retainedHolderSource]struct{}{}
	}
	registry.live[source] = struct{}{}
}

func (registry *retainedHolderRegistry) remove(source retainedHolderSource) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.live, source)
}

// RetainedHolder is one Slot's hold on the retained budget at the moment of
// reading: its stream and its Plans' kept frames together.
type RetainedHolder struct {
	Slot      execution.SlotIdentity
	Operation execution.Operation
	// Since is when the Slot's earliest live hold began.
	Since  time.Time
	Bytes  uint64
	ByKind map[string]uint64
}

// RetainedHolderReading is what holds a coordinator's retained budget at the
// moment of reading.
type RetainedHolderReading struct {
	// Holders is one row per Slot, the largest first, at most the limit
	// asked for.
	Holders []RetainedHolder
	// Oldest is the Slot that has held the longest, listed or not; nil when
	// none holds anything.
	Oldest *RetainedHolder
	// Total is how many Slots hold anything.
	Total int
	// Attributed is what every row adds up to, listed or not, and Held the
	// pool's own figure (RetainedReserved), read just after: a Slot that
	// reserves or gives back between the two moves one and not yet the
	// other, so a difference read once is the reading; Held above
	// Attributed reading after reading is a reservation no hold names.
	Attributed, Held uint64
}

// RetainedHolders reads what holds coordinator's retained budget now, at
// most limit Slots listed; a negative limit lists them all.
func RetainedHolders(coordinator *SlotExecutionCoordinator, limit int) RetainedHolderReading {
	coordinator.holders.mu.Lock()
	sources := make([]retainedHolderSource, 0, len(coordinator.holders.live))
	for source := range coordinator.holders.live {
		sources = append(sources, source)
	}
	coordinator.holders.mu.Unlock()

	type slotKey struct {
		slot      execution.SlotIdentity
		operation execution.Operation
	}
	var reading RetainedHolderReading
	bySlot := map[slotKey]*RetainedHolder{}
	for _, source := range sources {
		holding := source.retainedHolding()
		var bytes uint64
		for _, value := range holding.bytes {
			bytes += value
		}
		if bytes == 0 {
			continue
		}
		key := slotKey{holding.slot, holding.operation}
		row, seen := bySlot[key]
		if !seen {
			row = &RetainedHolder{Slot: holding.slot, Operation: holding.operation, Since: holding.since, ByKind: map[string]uint64{}}
			bySlot[key] = row
		}
		if holding.since.Before(row.Since) {
			row.Since = holding.since
		}
		for kind, value := range holding.bytes {
			if value > 0 {
				row.ByKind[kind] += value
			}
		}
		row.Bytes += bytes
		reading.Attributed += bytes
	}
	reading.Held = RetainedReserved(coordinator)
	holders := make([]RetainedHolder, 0, len(bySlot))
	for _, row := range bySlot {
		holders = append(holders, *row)
	}
	sort.Slice(holders, func(i, j int) bool {
		if holders[i].Bytes != holders[j].Bytes {
			return holders[i].Bytes > holders[j].Bytes
		}
		return holders[i].Since.Before(holders[j].Since)
	})
	for index := range holders {
		if reading.Oldest == nil || holders[index].Since.Before(reading.Oldest.Since) {
			oldest := holders[index]
			reading.Oldest = &oldest
		}
	}
	reading.Total = len(holders)
	if limit >= 0 && len(holders) > limit {
		holders = holders[:limit]
	}
	reading.Holders = holders
	return reading
}

// retainedHolding is the stream's own retention by phase.
func (stream *streamedExecution) retainedHolding() retainedHolding {
	holding := retainedHolding{slot: stream.request.Contract.Slot, operation: stream.request.Operation,
		since: stream.heldSince, bytes: map[string]uint64{}}
	for phase := range stream.retainedNow {
		if value := stream.retainedNow[phase].Load(); value > 0 {
			holding.bytes[retainPhaseKinds[phase]] = value
		}
	}
	return holding
}

// retainedHolding is the Plan's kept frames.
func (held *heldFrames) retainedHolding() retainedHolding {
	return retainedHolding{slot: held.slot, operation: held.operation, since: held.since,
		bytes: map[string]uint64{RetainedKindFrames: held.bytesNow.Load()}}
}
