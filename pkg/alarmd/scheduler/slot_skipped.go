// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// LookbackBusy reads what the lookback has of a Query Group in flight or
// reserved, at the moment it is called.
type LookbackBusy func(execution.QueryGroupIdentity) (observability.LookbackBusyFacts, bool)

// WithSlotSkipReport makes the Runner report every Slot its execution
// finalised as skipped, one slot_skipped line each, to observer, and carries
// busy into every round so each site that finalises a skip -- this Runner,
// the slot source moving the cursor, the Progress store dropping an
// unfinished Slot -- reads the lookback at that moment.
func (runner *Runner) WithSlotSkipReport(observer observability.Observer, busy LookbackBusy) *Runner {
	if runner != nil {
		runner.skipObserver, runner.lookbackBusy = observer, busy
	}
	return runner
}

// withLookbackBusy puts the Runner's lookback reading on the round.
func (runner *Runner) withLookbackBusy(ctx context.Context) context.Context {
	busy, queryGroup := runner.lookbackBusy, runner.queryGroup
	if busy == nil {
		return ctx
	}
	return observability.ContextWithLookbackBusy(ctx, func() (observability.LookbackBusyFacts, bool) {
		return busy(queryGroup)
	})
}

// observeSlotSkipped reports a Slot, or range, the execution just finalised
// without a query (execution.SlotExecutionResult.QueryFreeCause): GAP_SKIPPED
// or SNAPSHOT_UNAVAILABLE, for an expired replay or a Snapshot gone past its
// bound, alone or as a range. Every one opens a whole-Plan gap marker on each
// due Plan (contract.GapScopeQueryFreeReasons).
func (runner *Runner) observeSlotSkipped(ctx context.Context, slot FrozenSlot, operation execution.Operation, fence execution.OwnerFence, result execution.SlotExecutionResult) {
	if runner.skipObserver == nil {
		return
	}
	evaluation := int64(slot.Contract.Slot.EvaluationTime)
	facts := observability.SlotSkippedFacts{
		Path: observability.SlotSkippedQueryFree, FirstSlot: evaluation, LastSlot: evaluation, SlotCount: 1,
		Reason: string(result.ReasonCode), Cause: string(result.QueryFreeCause),
		Range: slot.Recovery.RangeDistance,
	}
	if r := slot.ExpiredRange; r != nil {
		facts.Path, facts.SlotCount = observability.SlotSkippedExpiredRange, r.Count
		facts.FirstSlot, facts.LastSlot = int64(r.First.Contract.Slot.EvaluationTime), int64(r.Last.Contract.Slot.EvaluationTime)
	}
	facts.GapGuard, facts.GapPlans, facts.GapStrategies = skipGapPlans(slot.DuePlanTargets)
	if recovery := slot.Recovery; recovery.Disposition == ReplayExpired {
		facts.Replay = &observability.SlotSkipReplayFacts{
			Reason: string(recovery.Reason), AgeMillis: recovery.SlotAgeMillis,
			AgePastDeadlineMillis: recovery.Age.Milliseconds(), AgeLimitMillis: recovery.AgeLimit.Milliseconds(),
			DistanceSlots: recovery.Distance, DistanceLimitSlots: recovery.DistanceLimit,
		}
	}
	if result.QueryFreeCause == execution.CauseResourceShareExceeded {
		facts.Share = runner.shareSkipFacts(slot)
	}
	observability.ObserveSlotSkipped(ctx, runner.skipObserver, frozenSkipTrace(slot.Contract, fence), observability.Operation(operation), facts)
}

// skipGapPlans is the gap marker a query-free finalization writes: a
// whole-Plan scope on each due Plan, named by its strategy id.
func skipGapPlans(targets execution.FrozenDuePlanTargets) (string, int, []string) {
	strategies := make([]string, 0, min(len(targets.Plans), 4))
	for _, plan := range targets.Plans {
		if len(strategies) == cap(strategies) {
			break
		}
		strategies = append(strategies, plan.StrategyID)
	}
	return observability.SkipGapPlanMarkers, len(targets.Plans), strategies
}

func frozenSkipTrace(contractRef execution.FrozenExecutionContractRef, fence execution.OwnerFence) observability.TraceFields {
	return observability.TraceFields{
		QueryGroupKey: string(contractRef.Slot.QueryGroup), EvaluationTime: int64(contractRef.Slot.EvaluationTime),
		SnapshotRevision: string(contractRef.SnapshotRevision), ScheduleSegmentStart: int64(contractRef.ScheduleSegmentStart),
		OwnerID: fence.OwnerID, OwnerEpoch: fence.OwnerEpoch,
	}
}
