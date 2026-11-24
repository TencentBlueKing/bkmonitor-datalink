// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Query Group a 4 GiB replica refused for its share once the State cache
// took its budget out of the pool: about 7,358 retained bytes a series (the
// refusal's own 704,641,308 bytes over its 95,760 series). Each of its Slots
// held under 729.6 MiB before - 95% of the 768 MiB share, where nothing
// reported it approaching.
const (
	largeGroupSeriesBytes = 7_358
	fourGiB               = 4 << 30
)

// coordinatorOfReplica is a coordinator with the retained pool configuration
// derives for a replica of memoryLimit, the State cache on.
func coordinatorOfReplica(memoryLimit uint64) *SlotExecutionCoordinator {
	derived := config.DeriveCoordinator(config.CapacityInputs{CPUBudget: 4, MemoryLimitBytes: memoryLimit, MemorySource: "pod_limit"}, 8192, 8192)
	return &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: derived.MaxSeries, MaxRetainedBytes: derived.MaxRetainedBytes,
		MaxStateMutations: derived.MaxStateMutations, MaxEvents: derived.MaxEvents, MaxGapMutations: derived.MaxGapMutations}}
}

// holdSeries reserves series of the large group's size one at a time, as a
// Slot's series arrive, and says how many were held before a refusal.
func holdSeries(stream *streamedExecution, series int) (int, error) {
	for held := 0; held < series; held++ {
		if err := stream.ReserveRetainedBytes(context.Background(), largeGroupSeriesBytes); err != nil {
			return held, err
		}
	}
	return series, nil
}

// On a 4 GiB replica with the State cache on, a Query Group of 100,000 such
// series - about 702 MiB, which ran on the release before the cache - holds
// every series: the cache takes nothing out of the share one Query Group may
// hold. The same group grown past 768 MiB is refused, by the share, at the
// series that crosses it.
func TestALargeQueryGroupHoldsItsWholeShareWithTheStateCacheOn(t *testing.T) {
	co := coordinatorOfReplica(fourGiB)
	stream := &streamedExecution{coordinator: co, began: true}
	if held, err := holdSeries(stream, 100_000); err != nil {
		t.Fatalf("held %d of 100000 series (%d bytes) before %v; want the whole group within its share", held, stream.retainedTotal(), err)
	}

	co = coordinatorOfReplica(fourGiB)
	stream = &streamedExecution{coordinator: co, began: true}
	share := uint64(768 << 20)
	held, err := holdSeries(stream, 110_000)
	if _, byShare := execution.ShareRefusalOf(err); err == nil || !byShare {
		t.Fatalf("a group past 768 MiB held %d series, refusal %v; want a share refusal", held, err)
	}
	if want := int(share / largeGroupSeriesBytes); held != want {
		t.Fatalf("refused after %d series, want after %d: the share is 768 MiB", held, want)
	}
}

// fullCache is a State cache that borrowed all its budget: it gives back
// what it is asked for, or all it holds.
type fullCache struct {
	held   uint64
	yields int
}

func (*fullCache) BorrowRetainedWithin(func() uint64) {}

func (cache *fullCache) BorrowedRetainedBytes() uint64 { return cache.held }

func (cache *fullCache) YieldRetainedBytes(need uint64) {
	cache.yields++
	cache.held -= min(need, cache.held)
}

// With the State cache holding all of its 128 MiB and another Slot holding
// 250 MiB, the large group still holds every series: the cache gives back
// what the reservations need, no more, and the Slots and the cache together
// never pass the pool.
func TestAFullStateCacheGivesWayToALargeQueryGroup(t *testing.T) {
	co := coordinatorOfReplica(fourGiB)
	cache := &fullCache{held: 128 << 20}
	co.ports.RetainedBorrower = cache
	beside := &streamedExecution{coordinator: co, began: true}
	if err := beside.ReserveRetainedBytes(context.Background(), 250<<20); err != nil {
		t.Fatal(err)
	}
	stream := &streamedExecution{coordinator: co, began: true}
	if held, err := holdSeries(stream, 100_000); err != nil {
		t.Fatalf("held %d of 100000 series beside a full State cache before %v", held, err)
	}
	reserved := RetainedReserved(co)
	if reserved+cache.held != co.budget.MaxRetainedBytes || cache.yields == 0 {
		t.Fatalf("reserved %d, the cache holds %d after %d yields; want the cache to have given back exactly what the pool %d lacked",
			reserved, cache.held, cache.yields, co.budget.MaxRetainedBytes)
	}
}

// A reservation the pool cannot hold with the cache empty is refused, by the
// pool, and the cache is not asked to give anything back for it.
func TestTheStateCacheGivesNothingBackForAReservationThatCannotFit(t *testing.T) {
	co := coordinatorOfReplica(fourGiB)
	cache := &fullCache{held: 128 << 20}
	co.ports.RetainedBorrower = cache
	for _, bytes := range []uint64{700 << 20, 300 << 20} {
		if err := (&streamedExecution{coordinator: co, began: true}).ReserveRetainedBytes(context.Background(), bytes); err != nil {
			t.Fatal(err)
		}
	}
	err := (&streamedExecution{coordinator: co, began: true}).ReserveRetainedBytes(context.Background(), 30<<20)
	if _, byShare := execution.ShareRefusalOf(err); err == nil || byShare {
		t.Fatalf("30 MiB past a pool with 24 MiB unreserved: %v (by the share: %v), want the pool's refusal", err, byShare)
	}
	if cache.held != 24<<20 {
		t.Fatalf("the cache holds %d after the refusal, want the 24 MiB the Slots left it", cache.held)
	}
}

// oneGapFact is a gap store that loads one fact.
type oneGapFact struct{}

func (oneGapFact) LoadGaps(context.Context, execution.GapLoadRequest) (execution.GapLoadResult, error) {
	return execution.GapLoadResult{}, nil
}

func (oneGapFact) LoadGapsInto(_ context.Context, _ execution.GapLoadRequest, accept func(execution.GapGuardSnapshot) error) error {
	return accept(execution.GapGuardSnapshot{})
}

func (oneGapFact) ApplyGap(context.Context, execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	return execution.GapGuardApplyResult{}, nil
}

// Every way into the retained pool has the State cache give way: an optional
// hold, a side effect's bytes and a gap fact, each with the pool's only room
// what the cache holds, are reserved, the cache giving back what they need.
func TestEveryWayIntoThePoolHasTheStateCacheGiveWay(t *testing.T) {
	for name, reserve := range map[string]func(*SlotExecutionCoordinator, *streamedExecution) error{
		"an optional hold": func(co *SlotExecutionCoordinator, _ *streamedExecution) error {
			if !co.tryAcquireRetained(4096) {
				return errors.New("declined")
			}
			return nil
		},
		"a side effect": func(co *SlotExecutionCoordinator, stream *streamedExecution) error {
			return co.acquireEffects(effectCounts{states: 1}, 4096, stream, "normal_apply")
		},
		"a gap fact": func(co *SlotExecutionCoordinator, stream *streamedExecution) error {
			co.ports.GapGuard = oneGapFact{}
			_, err := stream.loadGapFacts(context.Background(), execution.GapLoadRequest{})
			var exceeded *provisionalBudgetExceededError
			if errors.As(err, &exceeded) {
				return err
			}
			if stream.gapFacts != 1 {
				return errors.New("the fact was not reserved")
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			co := coordinatorOfReplica(fourGiB)
			cache := &fullCache{held: 128 << 20}
			co.ports.RetainedBorrower = cache
			co.reservations.retainedBytes = co.budget.MaxRetainedBytes - cache.held
			if err := reserve(co, &streamedExecution{coordinator: co, began: true}); err != nil {
				t.Fatalf("refused with the cache holding the pool's room: %v", err)
			}
			if cache.yields == 0 || RetainedReserved(co)+cache.held > co.budget.MaxRetainedBytes {
				t.Fatalf("%d yields, reserved %d beside the cache's %d; want the cache to give way within the pool %d",
					cache.yields, RetainedReserved(co), cache.held, co.budget.MaxRetainedBytes)
			}
		})
	}
}
