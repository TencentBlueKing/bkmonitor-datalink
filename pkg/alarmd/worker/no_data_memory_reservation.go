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
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// noDataReservationPhase is the phase a pool refusal of a no-data memory
// reports.
const noDataReservationPhase = "normal_no_data"

// What a whole memory's write builds for each group, beside the group's key
// and its stored value: the store's field - a name, the key behind a
// two-byte prefix, and a value - and the pair twice more as the script's
// arguments, in the store's list and in the client's command, each one boxed.
// A group with no absence is stored as one byte; an absence as its three
// timestamps in JSON, ten digits each.
const (
	noDataFieldBytes        = 16 + 24
	noDataFieldPrefixBytes  = 2
	noDataScriptArgBytes    = 2*2*16 + 16 + 24
	noDataPresentValueBytes = 1
	noDataAbsentValueBytes  = 80
	// noDataGroupBuildBytes is what a first write holds a group beyond the
	// parts above, measured rather than named: the statement's build and the
	// client's command. TestMeasureTheFirstNoDataWriteAgainstTheHeap read a
	// live peak of 495-498 bytes a group at 64-byte keys against 302 counted
	// by the parts, at 100,000 and 500,000 groups (11-10), when the write was
	// one script and built every group at once. A staged write builds one
	// segment at a time, and the cost a group is counted at is kept as the
	// bound of what a segment's group adds.
	noDataGroupBuildBytes = 196
	// A map of string to int64: a header and groups of eight 24-byte slots
	// and their control bytes, filled to seven eighths.
	noDataCloseMapHeaderBytes = 48
	noDataCloseMapGroupBytes  = 8 + 8*(16+8)
	noDataCloseMapGroupFill   = 7
)

var (
	noDataGroupDeltaBytes   = uint64(unsafe.Sizeof(execution.NoDataGroupDelta{}))
	noDataGroupAbsenceBytes = uint64(unsafe.Sizeof(execution.NoDataGroupAbsence{}))
	noDataSnapshotBytes     = uint64(unsafe.Sizeof(execution.NoDataMemorySnapshot{}))
	noDataGroupMemoryBytes  = uint64(unsafe.Sizeof(execution.NoDataGroupMemory{}))
)

// noDataGroupWriteBytes is what writing one group of a whole memory holds
// in the worker: its key in the statement, the store's field - the key again
// behind its prefix, and the value - and the pair as the script's arguments.
func noDataGroupWriteBytes(group execution.NoDataGroupDelta) uint64 {
	total := noDataGroupDeltaBytes + 2*uint64(len(group.GroupKey)) + noDataFieldBytes + noDataFieldPrefixBytes +
		noDataScriptArgBytes + noDataGroupBuildBytes
	if group.Absent != nil {
		return total + noDataGroupAbsenceBytes + noDataAbsentValueBytes
	}
	return total + noDataPresentValueBytes
}

// noDataFirstWriteSegmentBytes is what a write of a whole memory holds in the
// worker at once beyond the statement: its heaviest segment's groups.
//
// The store writes a whole memory into a staging key a segment of
// execution.NoDataFirstWriteSegmentGroups groups at a time and builds the
// fields and arguments of one segment before it sends them, so what the write
// holds at its peak is one segment's, whatever the memory's size. A segment
// is counted at the whole per-group cost, the statement's own share of each
// group with it: the statement is the round's, built before the write is
// asked for, and counting a segment of it here keeps the hold an upper bound
// of what the write adds rather than an estimate of it.
func noDataFirstWriteSegmentBytes(mutation execution.PlanNoDataMutation) uint64 {
	var heaviest uint64
	for start := 0; start < len(mutation.Set); start += execution.NoDataFirstWriteSegmentGroups {
		var segment uint64
		for _, group := range mutation.Set[start:min(start+execution.NoDataFirstWriteSegmentGroups, len(mutation.Set))] {
			segment += noDataGroupWriteBytes(group)
		}
		heaviest = max(heaviest, segment)
	}
	return heaviest
}

// noDataMemoryBytes is what one Plan's loaded memory holds for the Slot: the
// snapshot, its groups and their keys, and its manual closes.
func noDataMemoryBytes(snapshot execution.NoDataMemorySnapshot) uint64 {
	total := noDataSnapshotBytes + uint64(cap(snapshot.Groups))*noDataGroupMemoryBytes
	for _, group := range snapshot.Groups {
		total += uint64(len(group.GroupKey))
	}
	if snapshot.ManualCloses != nil {
		groups := max((uint64(len(snapshot.ManualCloses))+noDataCloseMapGroupFill-1)/noDataCloseMapGroupFill, 1)
		total += noDataCloseMapHeaderBytes + groups*noDataCloseMapGroupBytes
		for key := range snapshot.ManualCloses {
			total += uint64(len(key))
		}
	}
	return total
}

// noDataBytesRefusal is a Plan's memory refused for what it holds against the
// share, as the store's refusals are reported.
func noDataBytesRefusal(identity execution.PlanNoDataIdentity, groups int, bytes, share uint64) execution.NoDataApplyItemResult {
	return execution.NoDataApplyItemResult{Identity: identity, Status: execution.NoDataRejected,
		ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded),
		Size: &execution.NoDataRecordSize{Record: execution.NoDataRecordBytes, Groups: groups, Limit: int(min(share, uint64(^uint(0)>>1))),
			LimitSource: execution.NoDataLimitShare, Bytes: bytes}}
}

// holdNoDataFirstWrites reserves what the writes of whole memories hold, for
// the length of the write, against the Slot's share and the pool with the
// rest of what the Slot holds. The store writes the Slot's memories one after
// another and each a segment at a time, so the most any of them holds at once
// is its heaviest segment, and that is what is held. A first write whose
// heaviest segment would take the Slot past its share is refused for this
// Slot as a memory too large to hold is at its load (noDataBytesRefusal): its
// memory is not written this round and its threshold detection goes on. A
// segment within the share that the pool has no room for refuses the Slot
// (RESOURCE_POOL_HELD_BY_OTHERS, or RESOURCE_HARD_STOP with no Slot to own
// it), and the Slot is retried. It returns what it held, for the caller to
// give back, the writes it kept and the ones it refused. A coordinator with
// no retained budget - only a test builds one - holds nothing.
func (coordinator *SlotExecutionCoordinator) holdNoDataFirstWrites(
	ctx context.Context, request execution.SlotExecutionRequest, mutations []execution.PlanNoDataMutation,
) (uint64, []execution.PlanNoDataMutation, []execution.NoDataApplyItemResult, error) {
	if coordinator.budget.MaxRetainedBytes == 0 {
		return 0, mutations, nil, nil
	}
	owner := retainedOwnerFrom(ctx)
	share := coordinator.qgShareBytes()
	var held uint64
	var refused []execution.NoDataApplyItemResult
	kept := mutations[:0:0]
	for _, mutation := range mutations {
		if !mutation.ReplacesWholeRecord() {
			kept = append(kept, mutation)
			continue
		}
		segment := noDataFirstWriteSegmentBytes(mutation)
		if owner != nil && share > 0 && owner.retainedTotal()+segment > share {
			refused = append(refused, noDataBytesRefusal(mutation.Identity, len(mutation.Set), segment, share))
			continue
		}
		kept = append(kept, mutation)
		held = max(held, segment)
	}
	if err := coordinator.holdRetained(owner, held, noDataReservationPhase); err != nil {
		var exceeded *provisionalBudgetExceededError
		if errors.As(err, &exceeded) {
			coordinator.observeCapacityRejection(ctx, request.Operation, exceeded.budget, err)
		}
		return 0, nil, nil, err
	}
	return held, kept, refused, nil
}

// reserveNoDataMemory reserves what the Slot's loaded no-data memories hold,
// for as long as the Slot holds them. A memory that would take this Slot past
// its share is refused for this Slot - its Plan's no-data detection skips the
// round, SKIPPED_SLOT_BUDGET, and its memory is neither judged nor written -
// while its threshold detection goes on; what is within the share and finds
// no room in the pool refuses the Slot, RESOURCE_HARD_STOP. A coordinator
// with no retained budget - only a test builds one - holds nothing.
func (stream *streamedExecution) reserveNoDataMemory(ctx context.Context) error {
	if stream.coordinator.budget.MaxRetainedBytes == 0 {
		return nil
	}
	share := stream.coordinator.qgShareBytes()
	var held uint64
	for _, snapshot := range stream.noData.Items {
		bytes := noDataMemoryBytes(snapshot)
		if share > 0 && stream.retainedTotal()+held+bytes > share {
			if stream.noDataRefused == nil {
				stream.noDataRefused = make(map[execution.PlanNoDataIdentity]execution.NoDataApplyItemResult)
			}
			stream.noDataRefused[snapshot.Identity] = noDataBytesRefusal(snapshot.Identity, len(snapshot.Groups), bytes, share)
			continue
		}
		held += bytes
	}
	if held == 0 {
		return nil
	}
	if err := stream.reserveProvisionalAt(ctx, 0, held, stream.reservationPhase(noDataReservationPhase)); err != nil {
		return err
	}
	stream.retainBytes(retainPhaseState, held)
	return nil
}
