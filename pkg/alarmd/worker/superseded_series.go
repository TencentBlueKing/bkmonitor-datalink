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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot meets State or a gap marker a later Slot wrote when the store has
// gone back behind Progress: a Redis failover lost a Slot's tail -- its
// State, its Progress commit and the next Slot's begin -- while the next Slot
// was already running, read the older State it found and wrote its own. The
// Slot Progress returns to then finds the store ahead of it on every attempt,
// because the store does not go back again. Refused, it was retried until its
// replay expired and the Query Group stood still for that long; skipped, it
// decides what nobody has decided yet and goes on.
//
// What is skipped is only what is later than this Slot (execution.SupersededBy),
// and it is skipped where it is met, one series or one marker at a time. The
// rest of the Slot runs as it would have: a series whose State is behind the
// Slot is evaluated and written, and that is the decision an end of the
// whole Slot would have lost.

// skipSupersededSeries stands in for the evaluation of a series whose State
// is a later Slot's: nothing is evaluated, sent or written for it, and it is
// in no no-data end update. Its Plan still gets a result -- one that decides
// nothing, as a resumed series' does -- so a Plan whose every series was
// skipped is still a Plan this Slot completed, and the Slot commits rather
// than reading as one that produced no result.
func (stream *streamedExecution) skipSupersededSeries(ctx context.Context, entry completedSeries, view execution.RuntimeStateView) error {
	// Its key was loaded, and its witness is held as any loaded key's is.
	result := supersededSeriesResult(stream.header.Contract, entry.due)
	if err := stream.mergeMeasured(ctx, result, stream.measureMerge(result, 0, loadedKeyHeldBytes)); err != nil {
		return err
	}
	sample := execution.SupersededSample{Series: entry.series, Slot: entry.item.ApplyVersion, Stored: view.PersistedApplyVersion}
	stream.superseded.Record(sample)
	// One line per series, as ALREADY_APPLIED has: success, under a word of
	// its own, with the two versions it was decided on.
	stream.coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageMutationCompared,
		Result: observability.ResultSuccess, Operation: observability.Operation(stream.request.Operation),
		ReasonCode: observability.ReasonStateSupersededBeforeEvaluation,
		Trace: observability.TraceFields{StrategyID: entry.due.Identity.StrategyID, BusinessID: entry.due.Identity.BusinessID,
			DimensionIdentityDigest: string(entry.series)},
		StateSuperseded: &observability.StateSupersededFacts{Site: observability.StateSupersededAtState, Count: 1,
			Sample: sample.Facts()},
	})
	return nil
}

// supersededSeriesResult is the Plan result a skipped series contributes: a
// decided Plan with no Level outcome, no State and no gap statement, counted
// as superseded so the round's coverage says how much of it was not
// evaluated and why.
func supersededSeriesResult(contractRef execution.FrozenExecutionContractRef, due execution.DuePlan) execution.EvaluationResult {
	plan := execution.PlanEvaluationResult{Plan: due.Identity, Disposition: execution.PlanDecided, ReasonCode: observability.ReasonNone}
	plan.HistoryCoverage.Superseded = 1
	return execution.EvaluationResult{Contract: contractRef, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Plans: []execution.PlanEvaluationResult{plan}}
}

// noteSupersededGap counts, once per Plan, a gap marker a resumed series
// found from a later Slot. Every resumed series of the Plan reads the same
// marker, and the count is of markers left as they were, not of readers.
func (stream *streamedExecution) noteSupersededGap(ctx context.Context, identity execution.PlanGapIdentity) {
	if _, counted := stream.supersededGaps[identity]; counted {
		return
	}
	if stream.supersededGaps == nil {
		stream.supersededGaps = make(map[execution.PlanGapIdentity]struct{})
	}
	stream.supersededGaps[identity] = struct{}{}
	stream.coordinator.observeSupersededGaps(ctx, stream.request.Operation, 1)
}

// observeSupersededGaps is the line and count for gap marker writes a Slot
// left out because a later Slot had written the marker.
func (coordinator *SlotExecutionCoordinator) observeSupersededGaps(ctx context.Context, operation execution.Operation, count int) {
	if count == 0 {
		return
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageGapGuardCommitted,
		Result: observability.ResultSuccess, Operation: observability.Operation(operation),
		ReasonCode:      observability.ReasonNone,
		Counts:          observability.Counts{Keys: int64(count)},
		StateSuperseded: &observability.StateSupersededFacts{Site: observability.StateSupersededAtGap, Count: int64(count)},
	})
}
