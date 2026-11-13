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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every holder of a Query Group's Slot is judged against its share first and
// the pool after: the streamed input, the side effects, the frames kept for
// a write and a no-data first write. A 1 MiB pool's share is 768 KiB. With
// the others holding 900 KiB of the pool, a Slot holding nothing that asks
// for 200 KiB is within its share and refused for what others hold
// (RESOURCE_POOL_HELD_BY_OTHERS, its own, the pool's and the others' bytes);
// a Slot already holding 700 KiB asking for the same 200 KiB is past its
// share (QG_BUDGET_SHARE_EXCEEDED) whatever the pool has. What belongs to no
// Query Group - the worker's preparation - is the pool's alone,
// RESOURCE_HARD_STOP.
func TestEveryHolderOfASlotIsJudgedAgainstItsShareThenThePool(t *testing.T) {
	const pool, others, asked, near = 1 << 20, 900 << 10, 200 << 10, 700 << 10
	type holder struct {
		name string
		ask  func(*SlotExecutionCoordinator, *streamedExecution) error
	}
	holders := []holder{
		{"the streamed input", func(_ *SlotExecutionCoordinator, stream *streamedExecution) error {
			return stream.reserveProvisional(context.Background(), 0, asked)
		}},
		{"the side effects", func(coordinator *SlotExecutionCoordinator, stream *streamedExecution) error {
			return coordinator.acquireEffects(effectCounts{states: 1}, asked, stream, "normal_output")
		}},
		{"a no-data first write's segment", func(coordinator *SlotExecutionCoordinator, stream *streamedExecution) error {
			return coordinator.holdRetained(stream, asked, noDataReservationPhase)
		}},
	}
	for _, test := range holders {
		t.Run(test.name+", within its share", func(t *testing.T) {
			coordinator, stream := shareFixture(pool, others, 0)
			err := test.ask(coordinator, stream)
			var exceeded *provisionalBudgetExceededError
			if !errors.As(err, &exceeded) || !exceeded.others || exceeded.share {
				t.Fatalf("err = %v, want the pool's refusal for what others hold", err)
			}
			if facts := exceeded.facts; facts.OwnUsed == nil || *facts.OwnUsed != 0 || facts.SharedUsed != others ||
				facts.OthersUsed == nil || *facts.OthersUsed != others || facts.Requested != asked || facts.Limit != pool {
				t.Fatalf("facts = %+v, want own 0, pool %d, others %d, requested %d, limit %d", facts, others, others, asked, pool)
			}
			if RetainedReserved(coordinator) != others || stream.retainedTotal() != 0 {
				t.Fatalf("a refusal held %d of the pool and %d of its own, want nothing more", RetainedReserved(coordinator), stream.retainedTotal())
			}
		})
		t.Run(test.name+", past its share", func(t *testing.T) {
			coordinator, stream := shareFixture(pool, 0, near)
			err := test.ask(coordinator, stream)
			var exceeded *provisionalBudgetExceededError
			if !errors.As(err, &exceeded) || !exceeded.share || exceeded.others {
				t.Fatalf("err = %v, want the share's refusal", err)
			}
		})
	}

	t.Run("the frames kept for a write", func(t *testing.T) {
		coordinator, stream := shareFixture(pool, others, 0)
		frames := &heldFrames{coordinator: coordinator, owner: stream}
		chunk := []admittedState{{frame: &execution.EncodedStateFrame{Bytes: make([]byte, asked)}}}
		frames.keep(chunk)
		if chunk[0].frame != nil || frames.bytes != 0 || RetainedReserved(coordinator) != others {
			t.Fatalf("frames the pool has no room for were kept: %d held, pool %d", frames.bytes, RetainedReserved(coordinator))
		}
		coordinator, stream = shareFixture(pool, 0, near)
		frames = &heldFrames{coordinator: coordinator, owner: stream}
		chunk = []admittedState{{frame: &execution.EncodedStateFrame{Bytes: make([]byte, asked)}}}
		frames.keep(chunk)
		if chunk[0].frame != nil || frames.bytes != 0 {
			t.Fatalf("frames past the Slot's share were kept: %d held", frames.bytes)
		}
		coordinator, stream = shareFixture(pool, 0, 0)
		frames = &heldFrames{coordinator: coordinator, owner: stream}
		chunk = []admittedState{{frame: &execution.EncodedStateFrame{Bytes: make([]byte, asked)}}}
		frames.keep(chunk)
		want := frameRetainedBytes(chunk[0].frame)
		if chunk[0].frame == nil || stream.retainedTotal() != want || RetainedReserved(coordinator) != want {
			t.Fatalf("frames within the share hold %d of the Slot and %d of the pool, want %d of both",
				stream.retainedTotal(), RetainedReserved(coordinator), want)
		}
		frames.release()
		if stream.retainedTotal() != 0 || RetainedReserved(coordinator) != 0 {
			t.Fatalf("released frames left %d of the Slot and %d of the pool", stream.retainedTotal(), RetainedReserved(coordinator))
		}
	})

	t.Run("the worker's preparation", func(t *testing.T) {
		coordinator, _ := shareFixture(pool, others, 0)
		_, err := coordinator.reservePreparationBytes(context.Background(), asked)
		var exceeded *provisionalBudgetExceededError
		if !errors.As(err, &exceeded) || exceeded.others || exceeded.share {
			t.Fatalf("err = %v, want the pool's own refusal", err)
		}
	})
}

// A no-data first write whose heaviest segment would take the Slot past its
// share is refused for this Slot by name - its memory is not written, the
// round goes on - and one within the share that the pool has no room for
// refuses the Slot for what others hold.
func TestANoDataFirstWriteIsJudgedWithTheRestOfItsSlot(t *testing.T) {
	coordinator, store, observed := pooledNoDataFixture(4 << 20)
	share := coordinator.qgShareBytes()
	mutation := wholeMemory(t, 2000)
	segment := noDataFirstWriteSegmentBytes(mutation)
	stream := &streamedExecution{coordinator: coordinator, began: true}
	stream.retainBytes(retainPhaseInput, share-segment/2)
	coordinator.reservations.retainedBytes = stream.retainedTotal()
	ctx := withRetainedOwner(context.Background(), stream)
	request := execution.SlotExecutionRequest{Operation: execution.OperationNormal}
	if err := coordinator.applyNoDataMemory(ctx, request, refusedMemoryDue(t), []execution.PlanNoDataMutation{mutation}); err != nil {
		t.Fatalf("a first write past the share ended %v, want it refused by name and the round to go on", err)
	}
	refused := false
	for _, observation := range *observed {
		refused = refused || (observation.Stage == observability.StageNoDataMemoryRefused &&
			observation.ReasonCode == observability.ReasonCode(contract.ReasonStateBudgetExceeded))
	}
	if !refused || store.writes != 0 {
		t.Fatalf("a first write past the share: refused %t after %d writes, want it refused and not written", refused, store.writes)
	}
	// The refusal leaves no memory and nothing held: the next round, the
	// Slot holding less, writes the same first write whole.
	stream.unretain(retainPhaseInput, stream.retainedTotal())
	coordinator.reservations.retainedBytes = 0
	if err := coordinator.applyNoDataMemory(ctx, request, refusedMemoryDue(t), []execution.PlanNoDataMutation{mutation}); err != nil || store.writes != 1 {
		t.Fatalf("the next round's first write ended %v after %d writes, want it written once", err, store.writes)
	}

	coordinator, store, observed = pooledNoDataFixture(4 << 20)
	stream = &streamedExecution{coordinator: coordinator, began: true}
	coordinator.reservations.retainedBytes = coordinator.budget.MaxRetainedBytes - segment/2
	ctx = withRetainedOwner(context.Background(), stream)
	err := coordinator.applyNoDataMemory(ctx, request, refusedMemoryDue(t), []execution.PlanNoDataMutation{mutation})
	if !PoolHeldByOthers(err) || store.writes != 0 {
		t.Fatalf("a first write within the share the pool has no room for ended %v after %d writes, "+
			"want the pool's refusal for what others hold", err, store.writes)
	}
	named := false
	for _, observation := range *observed {
		named = named || (observation.Stage == observability.StageResourceHard &&
			observation.ReasonCode == observability.ReasonCode(contract.ReasonResourcePoolHeldByOthers))
	}
	if !named {
		t.Fatalf("no RESOURCE_POOL_HELD_BY_OTHERS line; observed %+v", *observed)
	}
}

// shareFixture is a coordinator of a pool of pool bytes whose others hold
// others of it, and a begun stream of its Query Group holding own.
func shareFixture(pool, others, own uint64) (*SlotExecutionCoordinator, *streamedExecution) {
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 1 << 20, MaxRetainedBytes: pool,
		MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10},
		ports: Ports{Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}}
	stream := &streamedExecution{coordinator: coordinator, began: true}
	coordinator.reservations.retainedBytes = others + own
	if own > 0 {
		stream.retainedByPhase[retainPhaseInput] = own
	}
	return coordinator, stream
}
