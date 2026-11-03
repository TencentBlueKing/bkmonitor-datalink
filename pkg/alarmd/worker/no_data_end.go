// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// MaxNoDataEndEntriesPerPlan bounds one Plan's no-data end index. The index
// holds only series whose alert is open, a subset of the groups the Plan's
// no-data memory may track, so it takes that memory's group bound
// (contract.DefaultMaxNoDataGroups). An entry is a few hundred bytes - the
// fingerprint, the series digest, the alert dimensions and a time - so the
// bound is tens of megabytes read per round, reached only by a Plan with a
// hundred thousand alerts open at once.
const MaxNoDataEndEntriesPerPlan = contract.DefaultMaxNoDataGroups

// noDataEndMaxEvidenceBytes bounds the evidence of one absent series' end: a
// few hundred bytes a level, for at most a Plan's levels.
const noDataEndMaxEvidenceBytes = 64 << 10

// noDataEndPlan is what one Plan's absent walk decides on: its index as the
// Slot read it, whether this round's answer for it was complete, and the
// series it evaluated.
type noDataEndPlan struct {
	loaded   execution.NoDataEndLoaded
	complete bool
	present  map[execution.SeriesIdentityDigest]struct{}
}

type noDataEndRound map[execution.PlanIdentity]noDataEndPlan

type noDataEndRoundKey struct{}

func withNoDataEnds(ctx context.Context, round noDataEndRound) context.Context {
	if len(round) == 0 {
		return ctx
	}
	return context.WithValue(ctx, noDataEndRoundKey{}, round)
}

func noDataEndsFrom(ctx context.Context) noDataEndRound {
	round, _ := ctx.Value(noDataEndRoundKey{}).(noDataEndRound)
	return round
}

// endsOnNoData is whether a due Plan has a level written to end on no data.
func planEndsOnNoData(due execution.DuePlan) bool {
	for _, level := range due.CompiledPlan.Levels().All() {
		if level.Recovery().MissingData == contract.RecoveryMissingDataEvaluateAsZero {
			return true
		}
	}
	return false
}

// loadNoDataEnds reads the index of every due Plan written to end on no data
// and naming an alert identity. Without an index store nothing is read.
func (stream *streamedExecution) loadNoDataEnds(ctx context.Context) error {
	index := stream.coordinator.ports.NoDataEnds
	if index == nil || stream.coordinator.ports.OpenAlerts == nil {
		return nil
	}
	if link, ok := stream.coordinator.ports.OpenAlerts.(execution.OpenAlertLink); ok && !link.Linked() {
		return nil
	}
	var keys []execution.NoDataEndKey
	for _, due := range stream.header.DuePlans {
		if planEndsOnNoData(due) && due.CompiledPlan.OutputIdentity() != nil {
			keys = append(keys, execution.NoDataEndKey{Plan: due.Identity, StateGeneration: due.StateGeneration})
		}
	}
	if len(keys) == 0 {
		return nil
	}
	loaded, err := index.LoadNoDataEnds(ctx, keys)
	if err != nil {
		return fmt.Errorf("alarmd worker: no-data end index preflight: %w", err)
	}
	stream.noDataEnds = make(map[execution.PlanIdentity]execution.NoDataEndLoaded, len(loaded))
	for _, item := range loaded {
		stream.noDataEnds[item.Key.Plan] = item
	}
	return nil
}

// noDataEndRound is what the commit's absent walk decides on, per Plan whose
// index was read: complete when every primary binding of the Plan answered
// whole, the answer was not cut short, and no read gap of the Plan is open -
// anything less is a round that cannot say a group is absent.
func (stream *streamedExecution) noDataEndRound() noDataEndRound {
	if len(stream.noDataEnds) == 0 {
		return nil
	}
	round := make(noDataEndRound, len(stream.noDataEnds))
	for _, due := range stream.header.DuePlans {
		loaded, read := stream.noDataEnds[due.Identity]
		if !read {
			continue
		}
		gap, found := stream.gaps.Find(execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration})
		gapOpen := found && gap.Status == execution.GapFound && len(gap.Scopes) > 0
		plan := noDataEndPlan{loaded: loaded, present: map[execution.SeriesIdentityDigest]struct{}{},
			complete: stream.noDataCompleteness(due) == execution.CompletenessFull && !stream.answerTruncated[due.Identity] && !gapOpen}
		for _, result := range stream.evaluated.Plans {
			if result.Plan != due.Identity {
				continue
			}
			for _, outcome := range result.LevelOutcomes {
				plan.present[outcome.SeriesIdentityDigest] = struct{}{}
			}
		}
		round[due.Identity] = plan
	}
	return round
}

// applyNoDataEnds keeps each Plan's index and ends its absent series: the
// updates this round's evaluation made, then, on a complete round, every
// entry whose series is absent - removed if the consumer no longer holds its
// alert, ended if its last real point is (R + T + 1) periods old. The ends go
// through the Slot's output like any event; an entry leaves the index only
// once its end was taken. After every Plan's state, so the index never runs
// ahead of the decisions it follows; a Plan whose output did not land
// (withheld) is left as it was.
func (coordinator *SlotExecutionCoordinator) applyNoDataEnds(ctx context.Context, request execution.SlotExecutionRequest,
	header execution.InternalExecutionHeader, evaluated execution.EvaluationResult, withheld map[execution.PlanIdentity]struct{}) error {
	round := noDataEndsFrom(ctx)
	index := coordinator.ports.NoDataEnds
	if len(round) == 0 || index == nil || coordinator.ports.OpenAlerts == nil {
		return nil
	}
	plans := make([]execution.PlanIdentity, 0, len(round))
	for plan := range round {
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return lessPlanIdentity(plans[i], plans[j]) })
	for _, identity := range plans {
		if _, skip := withheld[identity]; skip {
			continue
		}
		due, ok := duePlan(header.DuePlans, identity)
		if !ok {
			continue
		}
		plan := round[identity]
		if plan.loaded.Undecodable > 0 {
			coordinator.observeNoDataEnds(ctx, request, identity, observability.NoDataEndUndecodable, plan.loaded.Undecodable)
		}
		if plan.loaded.Unavailable {
			coordinator.observeNoDataEnds(ctx, request, identity, observability.NoDataEndUnavailable, 1)
			continue
		}
		judged := judgedSourceTime(header, due)
		if err := coordinator.applyPlanNoDataEnds(ctx, request, header.ExecutionID, judged, due, plan, updatesOf(evaluated, identity), index); err != nil {
			return err
		}
	}
	return nil
}

func updatesOf(evaluated execution.EvaluationResult, plan execution.PlanIdentity) []execution.NoDataEndUpdate {
	for _, result := range evaluated.Plans {
		if result.Plan == plan {
			return result.NoDataEnds
		}
	}
	return nil
}

// noDataEndDecision is what a Plan's index does this round: the entries put
// and removed, the absent series whose end is due, and the counts of what was
// decided on nothing.
type noDataEndDecision struct {
	write                       execution.NoDataEndWrite
	ends                        map[execution.SeriesIdentityDigest][]execution.NoDataEndEntry
	overflow, notOpen, unjudged int
}

// decideNoDataEnds decides a Plan's index for one round, without I/O. First
// this round's updates: an update nobody could judge only refreshes an entry
// that is there; a judged one keeps or removes it; a new open entry past the
// capacity is not indexed. Then, on a complete round only, each entry whose
// series is absent: left alone when the consumer's set cannot be judged,
// removed when it judges the alert not open, and ended when its last real
// point is (R + T + 1) periods older than the judged period.
func decideNoDataEnds(due execution.DuePlan, plan noDataEndPlan, updates []execution.NoDataEndUpdate,
	judge execution.OpenAlertJudge, judgedAt int64, capacity int) noDataEndDecision {
	held := make(map[execution.NoDataEndEntryKey]execution.NoDataEndEntry, len(plan.loaded.Entries))
	for _, entry := range plan.loaded.Entries {
		held[entry.Key()] = entry
	}
	decision := noDataEndDecision{write: execution.NoDataEndWrite{Key: plan.loaded.Key},
		ends: map[execution.SeriesIdentityDigest][]execution.NoDataEndEntry{}}
	for _, update := range updates {
		key := update.Entry.Key()
		_, found := held[key]
		switch {
		case !update.Judged:
			// Nobody could say whether the alert is open: keep what is
			// there fresh, and decide nothing else.
			if found {
				held[key] = update.Entry
				decision.write.Puts = append(decision.write.Puts, update.Entry)
			}
		case !update.Open:
			if found {
				decision.write.Deletes = append(decision.write.Deletes, key)
				delete(held, key)
			}
		case !found && len(held) >= capacity:
			decision.overflow++
		default:
			held[key] = update.Entry
			decision.write.Puts = append(decision.write.Puts, update.Entry)
		}
	}
	if !plan.complete {
		return decision
	}
	strategyID := due.CompiledPlan.StrategyRef().StrategyID
	for key, entry := range held {
		if _, present := plan.present[key.Series]; present {
			continue
		}
		open, judged := false, false
		if judge != nil {
			open, judged = judge.Holds(due.Identity.TenantID, strategyID, entry.Fingerprint)
		}
		switch {
		case !judged:
			decision.unjudged++
		case !open:
			decision.write.Deletes = append(decision.write.Deletes, key)
			decision.notOpen++
		case noDataEndDue(due, entry, judgedAt):
			decision.ends[key.Series] = append(decision.ends[key.Series], entry)
		}
	}
	return decision
}

func (coordinator *SlotExecutionCoordinator) applyPlanNoDataEnds(ctx context.Context, request execution.SlotExecutionRequest,
	executionID string, judgedAt int64, due execution.DuePlan, plan noDataEndPlan, updates []execution.NoDataEndUpdate, index execution.NoDataEndIndex) error {
	judge, _ := coordinator.ports.OpenAlerts.(execution.OpenAlertJudge)
	decision := decideNoDataEnds(due, plan, updates, judge, judgedAt, MaxNoDataEndEntriesPerPlan)
	write := decision.write
	write.TTL = noDataEndTTL(due)
	for outcome, count := range map[string]int{observability.NoDataEndOverflow: decision.overflow,
		observability.NoDataEndNotOpen: decision.notOpen, observability.NoDataEndUnjudged: decision.unjudged} {
		if count > 0 {
			coordinator.observeNoDataEnds(ctx, request, due.Identity, outcome, count)
		}
	}
	if len(decision.ends) > 0 {
		sent, failed := coordinator.sendNoDataEnds(ctx, request, executionID, judgedAt, due, decision.ends)
		write.Deletes = append(write.Deletes, sent...)
		if len(sent) > 0 {
			coordinator.observeNoDataEnds(ctx, request, due.Identity, observability.NoDataEndSent, len(sent))
		}
		if failed > 0 {
			coordinator.observeNoDataEnds(ctx, request, due.Identity, observability.NoDataEndSendFailed, failed)
		}
	}
	if len(write.Puts) == 0 && len(write.Deletes) == 0 {
		return nil
	}
	if err := execution.EnterCommit(ctx, execution.SlotStageState); err != nil {
		return err
	}
	if err := index.ApplyNoDataEnds(ctx, write); err != nil {
		// Reported, not raised: what this round decided is already sent, and
		// the next round that evaluates or walks these series writes again.
		coordinator.observeNoDataEnds(ctx, request, due.Identity, observability.NoDataEndUnavailable, 1)
	}
	return nil
}

// judgedSourceTime is the source time of the period this round judged for a
// Plan: the newest step its primary requirement accepted, which is the
// source time a record of a present series carries. An absent series' end
// is timed from it as a present one's is (trigger noDataEndDueV2), and its
// event is about that period.
func judgedSourceTime(header execution.InternalExecutionHeader, due execution.DuePlan) int64 {
	evaluation := int64(header.Contract.Slot.EvaluationTime)
	judged, found := int64(0), false
	for _, requirement := range header.Requirements {
		if requirement.Role != execution.InputRolePrimary || requirement.StepMillis <= 0 {
			continue
		}
		for _, consumer := range requirement.Consumers {
			if consumer.Consumer.Plan != due.Identity {
				continue
			}
			end := min(evaluation+requirement.RelativeWindow.EndOffsetSeconds, evaluation)
			judged, found = max(judged, end-requirement.StepMillis/1000), true
		}
	}
	if !found {
		return evaluation - int64(due.CompiledPlan.EvaluationSemantics().EvaluationInterval)
	}
	return judged
}

// noDataEndDue is whether an entry's level has had no real point through
// (R + T + 1) periods at the judged period's source time.
func noDataEndDue(due execution.DuePlan, entry execution.NoDataEndEntry, now int64) bool {
	for _, level := range due.CompiledPlan.Levels().All() {
		if level.Definition().LevelID != entry.LevelID {
			continue
		}
		recovery, triggerPlan := level.Recovery(), level.Trigger()
		if recovery.MissingData != contract.RecoveryMissingDataEvaluateAsZero {
			return false
		}
		periods := int64(recovery.ConsecutiveWindows) + int64(triggerPlan.WindowSize) + 1
		return now-entry.LastRealPoint >= periods*int64(triggerPlan.StepSeconds)
	}
	return false
}

// noDataEndTTL is how long a Plan's index lives without a write: twice the
// longest no-data span of its levels, and a day at least, so an absent
// series' entry outlives the wait for its end.
func noDataEndTTL(due execution.DuePlan) time.Duration {
	longest := int64(0)
	for _, level := range due.CompiledPlan.Levels().All() {
		recovery, triggerPlan := level.Recovery(), level.Trigger()
		if recovery.MissingData != contract.RecoveryMissingDataEvaluateAsZero {
			continue
		}
		span := (int64(recovery.ConsecutiveWindows) + int64(triggerPlan.WindowSize) + 1) * int64(triggerPlan.StepSeconds)
		longest = max(longest, span)
	}
	return max(24*time.Hour, 2*time.Duration(longest)*time.Second)
}

// sendNoDataEnds sends each absent series' end, one event per series for its
// due levels, and returns the entries whose end the sink took and how many
// series it did not.
func (coordinator *SlotExecutionCoordinator) sendNoDataEnds(ctx context.Context, request execution.SlotExecutionRequest,
	executionID string, sourceTime int64, due execution.DuePlan, ends map[execution.SeriesIdentityDigest][]execution.NoDataEndEntry) ([]execution.NoDataEndEntryKey, int) {
	series := make([]execution.SeriesIdentityDigest, 0, len(ends))
	for digest := range ends {
		series = append(series, digest)
	}
	sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
	now := int64(request.Contract.Slot.EvaluationTime)
	var sent []execution.NoDataEndEntryKey
	failed := 0
	for _, digest := range series {
		entries := ends[digest]
		levels := make([]uint32, 0, len(entries))
		for _, entry := range entries {
			levels = append(levels, entry.LevelID)
		}
		sort.Slice(levels, func(i, j int) bool { return levels[i] < levels[j] })
		event, err := trigger.BuildAbsentNoDataEndV1(trigger.AbsentNoDataEndV1{
			TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, Plan: due.CompiledPlan,
			DimensionIdentityDigest: string(digest), Dimensions: entries[0].Dimensions, DedupeMD5: entries[0].Fingerprint,
			SourceTime: sourceTime, EvaluationTime: now, ExecutionID: executionID,
			LevelIDs: levels, MaxEvidenceBytes: noDataEndMaxEvidenceBytes, AttributedBusinessID: entries[0].AttributedBusinessID,
		})
		if err != nil {
			failed++
			continue
		}
		if err := coordinator.writeEvents(ctx, request.Operation, due.Identity, []contract.TriggerEventV1{*event}, nil); err != nil {
			failed++
			continue
		}
		if coordinator.ports.OpenAlerts != nil {
			coordinator.ports.OpenAlerts.Acknowledged([]contract.TriggerEventV1{*event})
		}
		for _, entry := range entries {
			sent = append(sent, entry.Key())
		}
	}
	return sent, failed
}

// observeNoDataEnds reports one outcome of a Plan's index, counted.
func (coordinator *SlotExecutionCoordinator) observeNoDataEnds(ctx context.Context, request execution.SlotExecutionRequest,
	plan execution.PlanIdentity, outcome string, count int) {
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: plan.StrategyID, BusinessID: plan.BusinessID})
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataEnd, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone, Operation: observability.Operation(request.Operation),
		Counts: observability.Counts{Keys: int64(count)}, NoDataEnd: outcome,
		Trace: observability.TraceFields{StrategyID: plan.StrategyID, BusinessID: plan.BusinessID},
	})
}
