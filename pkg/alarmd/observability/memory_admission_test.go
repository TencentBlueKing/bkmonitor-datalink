// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// memoryAsks is an admission that records every ask and answers as told.
type memoryAsks struct {
	asks  []uint64
	admit bool
}

func (m *memoryAsks) ask(bytes uint64) bool {
	m.asks = append(m.asks, bytes)
	return m.admit
}

func costRoster(groups int) []CostGroup {
	roster := make([]CostGroup, 0, groups)
	for index := range groups {
		roster = append(roster, CostGroup{QueryGroupKey: fmt.Sprintf("group-%d", index), QueryRevision: "query", SnapshotRevision: "snapshot",
			ScheduleRevision: "schedule", Members: []CostPlanIdentity{{"tenant", "business", fmt.Sprintf("%d-a", index)}, {"tenant", "business", fmt.Sprintf("%d-b", index)}}})
	}
	return roster
}

// Under admission the summary is sized by the roster it is reconciled with:
// the first roster asks for its whole reservation, the same roster asks
// nothing again, a larger one asks for what it adds and, refused, is tracked
// only as far as the capacity held reaches - the shortfall on the coverage -
// and a smaller roster gives its reservation up, so growing back asks again.
func TestTheCostSummaryGrowsWithItsRosterAsFarAsTheLineAdmits(t *testing.T) {
	memory := &memoryAsks{admit: true}
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, TopN: 20, Now: time.Now, Admit: memory.ask})
	if !c.Snapshot().Enabled || c.Snapshot().CapacityBytesEstimated != 0 {
		t.Fatalf("snapshot = %+v, want enabled with nothing reserved before a roster", c.Snapshot())
	}
	coverage := func() CostCoverage { return c.scope.Load().coverage }

	c.Reconcile(costRoster(3), true)
	if len(memory.asks) != 1 || memory.asks[0] == 0 || int64(memory.asks[0]) != c.capacityBytes.Load() {
		t.Fatalf("asks = %v, reservation %d: want the first roster's whole reservation asked once", memory.asks, c.capacityBytes.Load())
	}
	if got := coverage(); got.TrackedGroups != 3 || got.TrackedPlans != 6 || got.Incomplete {
		t.Fatalf("coverage = %+v, want the roster tracked whole", got)
	}
	c.Reconcile(costRoster(3), true)
	if len(memory.asks) != 1 {
		t.Fatalf("asks = %v after the same roster, want no second ask", memory.asks)
	}

	memory.admit = false
	held := c.capacityBytes.Load()
	c.Reconcile(costRoster(5), true)
	grown := c.capacity
	grown.GroupCapacity, grown.PlanCapacity = 5, 10
	grown.MetadataBytes = c.capacity.MetadataBytes * 5 / 3
	if len(memory.asks) != 2 || int64(memory.asks[1]) != CostSummaryCapacityBytes(grown)-held || c.capacityBytes.Load() != held {
		t.Fatalf("asks = %v, reservation %d (held %d): want the growth asked and, refused, the reservation kept", memory.asks, c.capacityBytes.Load(), held)
	}
	if got := coverage(); got.TrackedGroups != 3 || got.TotalGroups != 5 || !got.Incomplete {
		t.Fatalf("coverage = %+v, want 3 of 5 tracked and the shortfall reported", got)
	}

	memory.admit = true
	c.Reconcile(costRoster(5), true)
	if got := coverage(); len(memory.asks) != 3 || got.TrackedGroups != 5 || got.Incomplete {
		t.Fatalf("asks = %v, coverage %+v: want the growth admitted and the roster tracked whole", memory.asks, got)
	}
	c.Reconcile(costRoster(2), true)
	if len(memory.asks) != 3 || c.capacityBytes.Load() >= held {
		t.Fatalf("asks = %v, reservation %d: want a smaller roster to ask nothing and give its reservation up", memory.asks, c.capacityBytes.Load())
	}
	c.Reconcile(costRoster(5), true)
	if len(memory.asks) != 4 {
		t.Fatalf("asks = %v, want growing back to ask again", memory.asks)
	}
}

// Under admission the sampler has one buffer per selected window, each
// window's buffer admitted when a selection adds it: none before, one for
// one window, and a window refused its buffer is selected and records
// nothing, counted as a queue drop. There is no rate of its own.
func TestTheSamplerHasABufferPerWindowAsFarAsTheLineAdmits(t *testing.T) {
	memory := &memoryAsks{admit: true}
	s := NewAdmittedSeriesSampler(memory.ask)
	now := time.Now()
	selection := func(group string) SeriesSampleSelection {
		return SeriesSampleSelection{QueryGroup: group, WindowID: "window-" + group, OpenedAt: now, ExpiresAt: now.Add(time.Minute),
			TenantID: "tenant", BusinessID: "business", StrategyID: "strategy", StateGeneration: "generation", PlanScheduleRevision: "schedule"}
	}
	candidate := func(group string, slot int64) SeriesSampleCandidate {
		return SeriesSampleCandidate{QueryGroup: group, TenantID: "tenant", BusinessID: "business", StrategyID: "strategy",
			StateGeneration: "generation", PlanScheduleRevision: "schedule", SeriesDigest: "series", Slot: slot}
	}
	if s.Limits().QueueCapacity != 0 || len(memory.asks) != 0 {
		t.Fatalf("limits %+v asks %v, want no buffer before a window", s.Limits(), memory.asks)
	}
	if err := s.Select([]SeriesSampleSelection{selection("a")}); err != nil {
		t.Fatal(err)
	}
	if len(memory.asks) != 1 || memory.asks[0] != uint64(SeriesSampleBufferBytes()) || s.Limits().QueueCapacity != 1 {
		t.Fatalf("asks %v, limits %+v: want one buffer asked for the one window", memory.asks, s.Limits())
	}
	// Many Slots of the one window, each written back: no rate refuses them.
	for slot := int64(1); slot <= 100; slot++ {
		reservation := s.TryReserve(context.Background(), candidate("a", slot))
		if reservation == nil {
			t.Fatalf("slot %d not reserved: %+v", slot, s.Health())
		}
		reservation.Release()
	}
	memory.admit = false
	if err := s.Select([]SeriesSampleSelection{selection("a"), selection("b")}); err != nil {
		t.Fatal(err)
	}
	if len(memory.asks) != 2 || s.Limits().QueueCapacity != 1 {
		t.Fatalf("asks %v, limits %+v: want the second window's buffer asked and refused", memory.asks, s.Limits())
	}
	held := s.TryReserve(context.Background(), candidate("a", 101))
	if held == nil {
		t.Fatal("the window with a buffer does not record")
	}
	if refused := s.TryReserve(context.Background(), candidate("b", 1)); refused != nil || s.Health().QueueDropped != 1 {
		t.Fatalf("the window refused its buffer recorded, or was not counted: %+v", s.Health())
	}
	held.Release()
}
