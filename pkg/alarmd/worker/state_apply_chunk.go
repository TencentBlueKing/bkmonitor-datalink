// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// slotBudget derives the caps one Slot's own output must stay within. State
// and Gap mutations are bounded by what StateApplyMaxChunks Store calls can
// carry as well as by the process budget; events have no Store call bound and
// keep the process budget. Series and retained bytes are shared reservations
// and are unchanged. A Slot above a cap can never be applied by this process,
// so exceeding it is deterministic, unlike the shared reservation which frees
// up as concurrent Slots finish.
func (coordinator *SlotExecutionCoordinator) slotBudget() ProvisionalBudget {
	budget := coordinator.budget
	budget.MaxStateMutations = execution.SlotMutationCap(budget.StoreMaxItems, budget.MaxStateMutations)
	budget.MaxGapMutations = execution.SlotMutationCap(budget.StoreMaxItems, budget.MaxGapMutations)
	return budget
}

// applyChunkItems is the number of items one Store call carries and therefore
// the chunk size of a chunked apply. Without a Store bound one chunk carries
// the whole process budget, which is the single-call behaviour.
func (coordinator *SlotExecutionCoordinator) applyChunkItems(processBudget uint64) uint64 {
	if items := coordinator.budget.StoreMaxItems; items > 0 {
		return items
	}
	return processBudget
}

// applyChunk is one Store call of a chunked apply: the half-open item range
// [start, end) and its position among the chunks of the same apply.
type applyChunk struct{ index, count, start, end int }

// forEachChunk runs apply over successive chunks of at most size items in
// slice order. It stops at the first error and does not start a further
// chunk once the context is done, so a cancelled Slot sends nothing more to
// the Store and the partial result of a failed chunk is never followed by
// another one. The first chunk is always sent: the Store call observes the
// context itself, exactly as the single-call apply did. The per-Slot cap
// admits at most StateApplyMaxChunks chunks; more items are a contract
// violation of the caller, not a retryable condition.
func forEachChunk(ctx context.Context, total int, size uint64, apply func(applyChunk) error) error {
	if total == 0 {
		return nil
	}
	items := total
	if size > 0 && size < uint64(total) {
		items = int(size)
	}
	count := (total + items - 1) / items
	if count > execution.StateApplyMaxChunks {
		return fmt.Errorf("alarmd worker: %d items need %d store calls, more than the %d allowed per apply",
			total, count, execution.StateApplyMaxChunks)
	}
	for index := 0; index < count; index++ {
		if err := ctx.Err(); index > 0 && err != nil {
			return err
		}
		start := index * items
		if err := apply(applyChunk{index: index, count: count, start: start, end: min(start+items, total)}); err != nil {
			return err
		}
	}
	return nil
}

// applyTotals accumulates what the chunks of one apply have sent so far.
type applyTotals struct{ keys, bytes int64 }

// observeChunk records one Store call of a chunked apply. Duration is the
// chunk's own time; the facts carry its position and the running totals of
// the apply, including the elapsed time since its first chunk started.
func (coordinator *SlotExecutionCoordinator) observeChunk(
	ctx context.Context,
	stage observability.Stage,
	operation execution.Operation,
	chunkStarted, applyStarted time.Time,
	result observability.Result,
	reason observability.ReasonCode,
	chunk applyChunk,
	totals applyTotals,
	counts observability.Counts,
	err error,
) {
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: stage, Result: result,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		ReasonCode: reason, Duration: time.Since(chunkStarted), Counts: counts, Err: err,
		StateApplyChunk: &observability.StateApplyChunkFacts{
			Index: chunk.index, Count: chunk.count, AppliedKeys: totals.keys, AppliedBytes: totals.bytes,
			ElapsedMillis: time.Since(applyStarted).Milliseconds(),
		},
	})
}

func stateIdentities(mutations []execution.StateMutation) []execution.StateKeyIdentity {
	identities := make([]execution.StateKeyIdentity, len(mutations))
	for index, mutation := range mutations {
		identities[index] = mutation.Identity
	}
	return identities
}
