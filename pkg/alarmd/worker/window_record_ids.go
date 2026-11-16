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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// windowRecordIDs is one Dataset's record id buffer as a window holds it.
type windowRecordIDs struct {
	// last is the latest prepared position whose series read the buffer.
	last  int
	bytes uint64
}

// materializeRecordIDs derives the record ids of a series' inputs into
// their Datasets' window buffers before the series is evaluated
// (decision-020 batch 8, trade-off two). The Datasets keep no ids the
// decoder derived; every reader of the evaluation - the sort of its records,
// each point's trigger, the outcomes, State points and events, and the
// result's validation - reads them from the buffer, each derived once a
// point here and not once per read.
//
// The buffers are the window's: charged as input when they are made, in the
// window's bytes, and let go with the window (dropWindowRecordIDs). A
// window that cannot take them is applied first, as one the next merge
// would overfill is, and the series starts the next window.
func (stream *streamedExecution) materializeRecordIDs(ctx context.Context, position int, inputs []execution.SeriesEvaluationInputRequest) error {
	windows := &stream.windows
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			dataset := binding.Dataset
			if dataset == nil {
				continue
			}
			if held := windows.recordIDs[dataset]; held != nil {
				held.last = max(held.last, position)
				continue
			}
			bytes := dataset.RecordIDWindowBytes()
			if bytes == 0 {
				continue
			}
			if err := stream.windowBeforeRecordIDs(ctx, position, bytes); err != nil {
				return err
			}
			if err := stream.reserveRetainedInput(ctx, bytes); err != nil {
				return err
			}
			leave := stream.enter(slotPhaseRecordIDs)
			added, err := dataset.MaterializeRecordIDs()
			leave()
			if added < bytes {
				stream.unretainInput(bytes - added)
			}
			if added > 0 {
				if windows.recordIDs == nil {
					windows.recordIDs = make(map[*execution.Dataset]*windowRecordIDs)
				}
				windows.recordIDs[dataset] = &windowRecordIDs{last: position, bytes: added}
				windows.bytes += added
			}
			if err != nil {
				return fmt.Errorf("alarmd worker: derive record ids: %w", err)
			}
		}
	}
	return nil
}

// recordIDsWouldCut says whether the record id buffers inputs still need
// would overfill the window being filled, so that materializing them cuts it
// (windowBeforeRecordIDs): the caller first merges every series prepared
// before.
func (stream *streamedExecution) recordIDsWouldCut(inputs []execution.SeriesEvaluationInputRequest) bool {
	windows := &stream.windows
	if stream.supplement != nil || windows.single || stream.effects.states+stream.effects.events == 0 ||
		stream.windowReleasable() == 0 {
		return false
	}
	var bytes uint64
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			if dataset := binding.Dataset; dataset != nil && windows.recordIDs[dataset] == nil {
				bytes += dataset.RecordIDWindowBytes()
			}
		}
	}
	return bytes > 0 && windows.bytes+bytes > stream.windowBound()
}

// windowBeforeRecordIDs applies the window being filled when the record id
// buffers of the series at position would overfill it, as windowBeforeMerge
// does for a merge: the series before position are all merged, so the
// window holds none of this one's.
func (stream *streamedExecution) windowBeforeRecordIDs(ctx context.Context, position int, bytes uint64) error {
	windows := &stream.windows
	if stream.supplement != nil || windows.single || stream.effects.states+stream.effects.events == 0 ||
		stream.windowReleasable() == 0 || windows.bytes+bytes <= stream.windowBound() {
		return nil
	}
	windows.reached = position
	return stream.cutWindow(ctx)
}

// dropWindowRecordIDs lets go of the record id buffers of a landed window:
// every one no series from through on read, or all of them on the last
// window. What they held goes back to the input phase; the buffers a series
// of the next window read stay, and their bytes are that window's.
func (stream *streamedExecution) dropWindowRecordIDs(through int, final bool) {
	windows := &stream.windows
	var released, kept uint64
	for dataset, held := range windows.recordIDs {
		if !final && held.last >= through {
			kept += held.bytes
			continue
		}
		dataset.DropRecordIDs()
		released += held.bytes
		delete(windows.recordIDs, dataset)
	}
	if len(windows.recordIDs) == 0 {
		windows.recordIDs = nil
	}
	stream.unretainInput(released)
	windows.bytes += kept
}

// dropAllRecordIDs lets go of every record id buffer the Slot holds as it
// ends; its charge goes back with everything else it retained.
func (stream *streamedExecution) dropAllRecordIDs() {
	for dataset := range stream.windows.recordIDs {
		dataset.DropRecordIDs()
	}
	stream.windows.recordIDs = nil
}
