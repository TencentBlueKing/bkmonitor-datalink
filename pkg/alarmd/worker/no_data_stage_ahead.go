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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// A first write staged as the windows land (decision-020 0.6.x
// throughput design 6.2). A no-data Plan whose round reads no record writes
// its whole memory, and the round folded every group into its delta at the
// tail and held it there to the write: at a Plan of hundreds of thousands of
// groups, the whole delta beside the present set in the worker. The groups
// present this round are most of that memory, and each is written as seen at
// this round however the round ends, unless it is the whole item or a host of
// another business. So as a landed window adds a group to the Plan's present
// set, the group is kept to stage, and every segment's worth
// (execution.NoDataFirstWriteSegmentGroups) is staged under the attempt's
// staging key at once, and let go. The round leaves the groups it keeps as
// staged out of its delta (nodata.SegmentedRound.UseStaged); the statement
// carries the rest. A round that keeps a staged group otherwise, or none of
// them, writes its whole record with the statement as before
// (SegmentedResult.StagedDiffers), the stage started over under it; one
// with nothing to write discards it.
//
// What it holds beyond the present set is one segment's keys - the present
// set's own strings, a string header each - and one segment's script while it
// is staged.

// How a Plan's staging ended, the index of its word in
// observability.NoDataStageAheadOutcomes.
const (
	stageAheadCarried = iota
	stageAheadDiffers
	stageAheadDiscarded
	stageAheadStopped
)

// noDataStageAhead is one Plan's first write staged ahead: what the store
// staged (staged), the keys waiting for a segment, and whether staging has
// stopped - the Plan's round writes no whole record, the store cannot stage,
// or a segment failed, after which the groups go with the statement.
type noDataStageAhead struct {
	staged  *execution.NoDataStaged
	pending []string
	stopped bool
}

// stageAhead keeps a group the landed windows found present to stage, and
// stages a segment's worth.
func (stream *streamedExecution) stageAhead(ctx context.Context, due execution.DuePlan, key string) {
	ahead := stream.stageAheadFor(due)
	// Only a group the round keeps as seen now: by the round's own rule
	// and the hosts of other businesses it is given.
	if ahead.stopped || !nodata.KeptAsSeen(key, stream.noDataHosts[due.NoDataIdentity()].OutOfBusiness) {
		return
	}
	ahead.pending = append(ahead.pending, key)
	if len(ahead.pending) < execution.NoDataFirstWriteSegmentGroups {
		return
	}
	segment := ahead.pending
	ahead.pending = nil
	sort.Strings(segment)
	set := make([]execution.NoDataGroupDelta, len(segment))
	for index, group := range segment {
		set[index] = execution.NoDataGroupDelta{GroupKey: group}
	}
	stager := stream.coordinator.ports.NoData.(execution.PlanNoDataStager)
	result, err := stager.StageNoData(ctx, execution.NoDataStageRequest{
		Contract: stream.request.Contract, Identity: due.NoDataIdentity(), Attempt: stream.noDataWriteAttempt(),
		PresentAsOf: int64(stream.header.Contract.Slot.EvaluationTime), Set: set, Staged: ahead.staged,
	})
	if err != nil || result.Status != execution.NoDataApplied {
		// The rest go with the statement; what was staged stays staged, and
		// a stage lost on the server is the swap's STAGE_LOST, retried.
		ahead.stopped = true
		stream.stageAheadEnds[stageAheadStopped]++
		return
	}
	ahead.staged = result.Staged
	stream.groupsStagedAhead += uint64(len(segment))
	present := stream.windows.presentSet(due)
	for _, group := range segment {
		present.MarkStaged(group)
	}
}

// stageAheadFor is the Plan's staging, made on first use: stopped unless the
// Plan's round will write its whole record - its memory loaded and none
// stored - and the store can stage it.
func (stream *streamedExecution) stageAheadFor(due execution.DuePlan) *noDataStageAhead {
	windows := &stream.windows
	if ahead := windows.staging[due.Identity]; ahead != nil {
		return ahead
	}
	if windows.staging == nil {
		windows.staging = make(map[execution.PlanIdentity]*noDataStageAhead)
	}
	ahead := &noDataStageAhead{stopped: true}
	_, stages := stream.coordinator.ports.NoData.(execution.PlanNoDataStager)
	if snapshot, found := stream.noData.Find(due.NoDataIdentity()); found && stages &&
		snapshot.Status == execution.NoDataMemoryMissing && snapshot.Representation != execution.NoDataRepresentationPerGroup {
		ahead.stopped = false
	}
	windows.staging[due.Identity] = ahead
	return ahead
}

// stagedAhead is what the Plan's windows staged, nil for none.
func (stream *streamedExecution) stagedAhead(plan execution.PlanIdentity) *execution.NoDataStaged {
	if ahead := stream.windows.staging[plan]; ahead != nil {
		return ahead.staged
	}
	return nil
}

// endStagedAhead ends what the Plan's windows staged as its round finished:
// carried by its statement, discarded under a statement that writes the
// whole record over it (counted by why), or discarded with no statement.
func (stream *streamedExecution) endStagedAhead(ctx context.Context, due execution.DuePlan, finished nodata.SegmentedResult) {
	switch {
	case stream.stagedAhead(due.Identity) == nil:
	case finished.Mutation != nil && finished.Mutation.Staged != nil:
		stream.stageAheadEnds[stageAheadCarried]++
	case finished.StagedDiffers != "":
		stream.discardStagedAhead(ctx, due, stageAheadDiffers)
		for index, reason := range nodata.StagedDiffersReasons {
			if reason == finished.StagedDiffers {
				stream.stageAheadDiffers[index]++
			}
		}
	default:
		stream.discardStagedAhead(ctx, due, stageAheadDiscarded)
	}
}

// discardStagedAhead takes back what the Plan's windows staged when its
// round writes no statement of it. Best effort: a key left goes with its
// lifetime, the Slot's deadline and a little more.
func (stream *streamedExecution) discardStagedAhead(ctx context.Context, due execution.DuePlan, end int) {
	staged := stream.stagedAhead(due.Identity)
	if staged == nil {
		return
	}
	stream.stageAheadEnds[end]++
	if stager, ok := stream.coordinator.ports.NoData.(execution.PlanNoDataStager); ok {
		_ = stager.DiscardNoDataStage(ctx, execution.NoDataStageDiscard{
			Identity: due.NoDataIdentity(), Attempt: stream.noDataWriteAttempt(), Staged: staged,
		})
	}
	stream.windows.staging[due.Identity].staged = nil
}

// noDataWriteAttempt names this Slot attempt's no-data writes, as the tail's
// write names them (writeNoDataMemory).
func (stream *streamedExecution) noDataWriteAttempt() execution.NoDataWriteAttempt {
	return execution.NoDataWriteAttempt{OwnerEpoch: stream.request.OwnerFence.OwnerEpoch, AttemptNo: stream.request.AttemptNo}
}
