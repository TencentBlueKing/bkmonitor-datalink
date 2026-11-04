// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// What a Worker found when it read a Plan's no-data state from the
// generation its activation carries it from (execution.NoDataCarry).
const (
	NoDataCarryMemoryCarried          = "memory_carried"
	NoDataCarryMemoryPreviousEmpty    = "memory_previous_empty"
	NoDataCarryMemoryPreviousUnread   = "memory_previous_unreadable"
	NoDataCarryEndsCarried            = "ends_carried"
	NoDataCarryEndsPreviousEmpty      = "ends_previous_empty"
	NoDataCarryEndsPreviousUnread     = "ends_previous_unreadable"
	NoDataCarryEndsPreviousNotCleared = "ends_previous_not_cleared"
	noDataCarryLoadScope              = "no_data_load"
)

// NoDataCarryLoadOutcomes are the outcomes in the order a reader groups them.
var NoDataCarryLoadOutcomes = []string{NoDataCarryMemoryCarried, NoDataCarryMemoryPreviousEmpty,
	NoDataCarryMemoryPreviousUnread, NoDataCarryEndsCarried, NoDataCarryEndsPreviousEmpty,
	NoDataCarryEndsPreviousUnread, NoDataCarryEndsPreviousNotCleared}

// observeNoDataCarry reports one no-data carry outcome for count Plans.
func (coordinator *SlotExecutionCoordinator) observeNoDataCarry(
	ctx context.Context, operation execution.Operation, outcome string, count int,
) {
	if count == 0 {
		return
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageStateCarried,
		Operation: observability.Operation(operation), Result: observability.ResultSuccess,
		StateCarry: &observability.StateCarryFacts{Scope: noDataCarryLoadScope, Result: outcome, Count: count},
	})
}

// carryNoDataMemory reads, for each Plan whose activation carries its no-data
// memory and whose own generation holds no record yet, the record of the
// generation it moved from, and has the round start from those groups.
//
// The round reads them as a memory its own generation does not hold: the
// snapshot stays MISSING and names no record, so the first write replaces
// the whole record under the new generation, and until then every round
// reads the old one again. Once the new generation holds a record the old
// one is never read. A previous record that cannot be read leaves the Plan
// on its own state, which is what it had without the carry; it is counted.
func (stream *streamedExecution) carryNoDataMemory(ctx context.Context, request execution.NoDataLoadRequest) {
	var items []execution.PlanNoDataLoadItem
	carried := map[execution.PlanNoDataIdentity]execution.PlanNoDataIdentity{}
	for _, due := range stream.header.DuePlans {
		carry := due.NoDataCarry
		if carry == nil || !carry.Memory || due.CompiledPlan.NoData() == nil {
			continue
		}
		identity := due.NoDataIdentity()
		snapshot, found := stream.noData.Find(identity)
		if !found || snapshot.Status != execution.NoDataMemoryMissing {
			continue
		}
		for _, item := range request.Items {
			if item.Identity != identity {
				continue
			}
			previous := item
			previous.Identity.StateGeneration = carry.From
			items = append(items, previous)
			carried[previous.Identity] = identity
		}
	}
	if len(items) == 0 {
		return
	}
	operation := stream.request.Operation
	previousRequest := execution.NoDataLoadRequest{Contract: request.Contract, Items: items}
	loaded, err := stream.coordinator.ports.NoData.LoadNoData(ctx, previousRequest)
	if err == nil {
		err = execution.ValidateNoDataLoad(previousRequest, loaded)
	}
	if err != nil {
		stream.coordinator.observeNoDataCarry(ctx, operation, NoDataCarryMemoryPreviousUnread, len(items))
		return
	}
	outcomes := map[string]int{}
	for _, previous := range loaded.Items {
		switch previous.Status {
		case execution.NoDataMemoryFound:
		case execution.NoDataMemoryMissing:
			outcomes[NoDataCarryMemoryPreviousEmpty]++
			continue
		default:
			outcomes[NoDataCarryMemoryPreviousUnread]++
			continue
		}
		identity := carried[previous.Identity]
		for index := range stream.noData.Items {
			snapshot := &stream.noData.Items[index]
			if snapshot.Identity != identity {
				continue
			}
			snapshot.Groups = append([]execution.NoDataGroupMemory(nil), previous.Groups...)
			snapshot.PresentAsOf = previous.PresentAsOf
			snapshot.RosterVersion = previous.RosterVersion
			snapshot.TrackingExhaustedAt = previous.TrackingExhaustedAt
			outcomes[NoDataCarryMemoryCarried]++
		}
	}
	for _, outcome := range NoDataCarryLoadOutcomes {
		stream.coordinator.observeNoDataCarry(ctx, operation, outcome, outcomes[outcome])
	}
}

// noDataEndCarry is the entries a Plan's no-data end index took from the
// generation it moved from this round, and where from.
type noDataEndCarry struct {
	from    execution.NoDataEndKey
	entries []execution.NoDataEndEntry
}

// carryNoDataEnds reads, for each Plan whose activation carries its no-data
// end index and whose own index is empty, the index of the generation it
// moved from, and has the round decide on those entries. Only entries of a
// Level that still ends on no data are taken: the others have nothing left
// to end them. The entries move: the round writes them under the new
// generation and then removes them from the old index, so an index the new
// generation later empties by ending its alerts does not take them again.
func (stream *streamedExecution) carryNoDataEnds(ctx context.Context, index execution.NoDataEndIndex) {
	var keys []execution.NoDataEndKey
	plans := map[execution.NoDataEndKey]execution.DuePlan{}
	for _, due := range stream.header.DuePlans {
		carry := due.NoDataCarry
		loaded, read := stream.noDataEnds[due.Identity]
		if carry == nil || !carry.Ends || !read || loaded.Unavailable || len(loaded.Entries) != 0 || loaded.Undecodable != 0 {
			continue
		}
		key := execution.NoDataEndKey{Plan: due.Identity, StateGeneration: carry.From}
		keys = append(keys, key)
		plans[key] = due
	}
	if len(keys) == 0 {
		return
	}
	operation := stream.request.Operation
	previous, err := index.LoadNoDataEnds(ctx, keys)
	if err != nil {
		stream.coordinator.observeNoDataCarry(ctx, operation, NoDataCarryEndsPreviousUnread, len(keys))
		return
	}
	outcomes := map[string]int{}
	for _, item := range previous {
		due, asked := plans[item.Key]
		if !asked {
			continue
		}
		if item.Unavailable {
			outcomes[NoDataCarryEndsPreviousUnread]++
			continue
		}
		var entries []execution.NoDataEndEntry
		for _, entry := range item.Entries {
			if levelEndsOnNoData(due, entry.LevelID) {
				entries = append(entries, entry)
			}
		}
		if len(entries) == 0 {
			outcomes[NoDataCarryEndsPreviousEmpty]++
			continue
		}
		loaded := stream.noDataEnds[due.Identity]
		loaded.Entries = entries
		stream.noDataEnds[due.Identity] = loaded
		if stream.noDataEndCarries == nil {
			stream.noDataEndCarries = map[execution.PlanIdentity]noDataEndCarry{}
		}
		stream.noDataEndCarries[due.Identity] = noDataEndCarry{from: item.Key, entries: entries}
		outcomes[NoDataCarryEndsCarried]++
	}
	for _, outcome := range NoDataCarryLoadOutcomes {
		stream.coordinator.observeNoDataCarry(ctx, operation, outcome, outcomes[outcome])
	}
}

// levelEndsOnNoData is whether a due Plan's Level is written to end on no
// data.
func levelEndsOnNoData(due execution.DuePlan, levelID uint32) bool {
	for _, level := range due.CompiledPlan.Levels().All() {
		if level.Definition().LevelID == levelID {
			return level.Recovery().MissingData == contract.RecoveryMissingDataEvaluateAsZero
		}
	}
	return false
}

// withCarriedEnds adds to a Plan's index write the carried entries the round
// neither removed nor rewrote, so they are held under the new generation.
func withCarriedEnds(write execution.NoDataEndWrite, carry noDataEndCarry) execution.NoDataEndWrite {
	written := make(map[execution.NoDataEndEntryKey]struct{}, len(write.Puts)+len(write.Deletes))
	for _, entry := range write.Puts {
		written[entry.Key()] = struct{}{}
	}
	for _, key := range write.Deletes {
		written[key] = struct{}{}
	}
	for _, entry := range carry.entries {
		if _, done := written[entry.Key()]; !done {
			write.Puts = append(write.Puts, entry)
		}
	}
	return write
}

// clearCarriedEnds removes the carried entries from the old generation's
// index once the new one holds them.
func clearCarriedEnds(carry noDataEndCarry, write execution.NoDataEndWrite) execution.NoDataEndWrite {
	cleared := execution.NoDataEndWrite{Key: carry.from, TTL: write.TTL}
	for _, entry := range carry.entries {
		cleared.Deletes = append(cleared.Deletes, entry.Key())
	}
	return cleared
}
