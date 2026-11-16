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

// A Plan new to the activation is forced to warm up (the control plane
// cannot tell a Plan that never ran from one deleted and put back: the state
// is one key per series, with no index by Plan). The warm-up exists for the
// second kind - state left under the same identity and generation, across a
// hole the points do not show - and costs the first kind its first Slot,
// evaluated and thrown away, so a new strategy began detecting a period
// late. The Worker can tell them apart at that first Slot: it reads the
// state of every series the Slot reads, and the Plan's gap marker and
// no-data memory. When none of them holds anything from before this Slot,
// the Plan never ran under this generation: it is not warmed up, its series
// warm up on their own windows as any new series does, and a satisfied
// marker records the judgement so the next Slot - which finds this Slot's
// state - does not take the Plan for one that ran before.

type unseenPlansKey struct{}

// withUnseenPlans hands finalization the due Plans the Slot found nothing of
// from before it.
func withUnseenPlans(ctx context.Context, plans map[execution.PlanKey]struct{}) context.Context {
	return context.WithValue(ctx, unseenPlansKey{}, plans)
}

func unseenPlansFrom(ctx context.Context) map[execution.PlanKey]struct{} {
	plans, _ := ctx.Value(unseenPlansKey{}).(map[execution.PlanKey]struct{})
	return plans
}

// unseenPlans is the due Plans of which this Slot read nothing written before
// it: no state record of any series it reads other than one this same Slot
// wrote on an earlier attempt, nothing carried from another generation, and
// no no-data memory. A record the store could not read is something: the
// judgement errs toward warming up.
func (stream *streamedExecution) unseenPlans() map[execution.PlanKey]struct{} {
	unseen := make(map[execution.PlanKey]struct{}, len(stream.header.DuePlans))
	for _, due := range stream.header.DuePlans {
		version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
		if err != nil {
			continue
		}
		if !stream.seenSoFar(due, version) {
			unseen[due.Key()] = struct{}{}
		}
	}
	return unseen
}

// seenSoFar reports whether what the Slot has read of the Plan so far holds
// anything written before the Slot at version: what its landed windows' views
// and the first-seen pre-pass said (windows.seen), the views it holds, and
// its no-data memory.
func (stream *streamedExecution) seenSoFar(due execution.DuePlan, version execution.ApplyVersion) bool {
	// The views of a Slot's landed windows are gone; what they said of the
	// Plan was kept as they went (noteSeenViews).
	if _, seen := stream.windows.seen[due.Identity]; seen {
		return true
	}
	if viewsSeenBefore(due, version, stream.state.Items) {
		return true
	}
	for _, memory := range stream.noData.Items {
		if memory.Identity.Plan == due.Identity && memory.Identity.StateGeneration == due.StateGeneration &&
			memory.Status != execution.NoDataMemoryMissing &&
			execution.CompareApplyVersion(memory.PersistedApplyVersion, version) != execution.ApplyVersionEqual {
			return true
		}
	}
	return false
}

// viewsSeenBefore reports whether the views hold anything of the Plan under
// its generation written before the Slot at version: a record of another
// Slot, or history carried from another generation.
func viewsSeenBefore(due execution.DuePlan, version execution.ApplyVersion, views []execution.RuntimeStateView) bool {
	for _, view := range views {
		if view.Identity.Plan != due.Identity || view.Identity.StateGeneration != due.StateGeneration {
			continue
		}
		if view.Carried != nil || (view.Status != execution.StateMissingWarming &&
			execution.CompareApplyVersion(view.PersistedApplyVersion, version) != execution.ApplyVersionEqual) {
			return true
		}
	}
	return false
}

// firstSeenActivations splits the Plans this Slot forces to warm up into
// those that ran before under their generation - still forced - and those
// that never did: new to their activation (Arrived), no gap marker at all,
// and nothing read from before the Slot. Only a Plan without a marker can be
// new; one with a marker ran. A Plan that stayed active while its generation
// moved ran too, under the generation before: its new generation holds
// nothing because nothing was carried, and it warms up.
func firstSeenActivations(
	forced execution.PlanActivationResult,
	loadedGaps execution.GapLoadResult,
	unseen map[execution.PlanKey]struct{},
) (stillForced execution.PlanActivationResult, firstSeen map[execution.PlanKey]execution.PlanActivationFact) {
	stillForced = execution.PlanActivationResult{Contract: forced.Contract}
	firstSeen = make(map[execution.PlanKey]execution.PlanActivationFact)
	for _, fact := range forced.Facts {
		marker, found := loadedGaps.Find(fact.GapIdentity())
		_, nothingBefore := unseen[fact.Key()]
		if fact.Selected.Arrived && nothingBefore && (!found || marker.Status == execution.GapMissing) {
			firstSeen[fact.Key()] = fact
			continue
		}
		stillForced.Facts = append(stillForced.Facts, fact)
	}
	return stillForced, firstSeen
}

// firstSeenCandidates is the Plans this Slot forces to warm up that could
// still be judged first-seen: new to their activation and without a marker,
// the two facts firstSeenActivations reads that the Slot has before its State
// is read. The third - nothing of the Plan read from before the Slot - is
// known only once every series of the Plan has been read. A Slot applied
// whole has read them all by its one Sequence; a Slot applied window by
// window writes its first window long before, and the judgement decides
// whether the Plan writes at all, so it reads them first (firstSeenPrepass).
func firstSeenCandidates(forced execution.PlanActivationResult, loadedGaps execution.GapLoadResult) []execution.PlanActivationFact {
	var candidates []execution.PlanActivationFact
	for _, fact := range forced.Facts {
		marker, found := loadedGaps.Find(fact.GapIdentity())
		if fact.Selected.Arrived && (!found || marker.Status == execution.GapMissing) {
			candidates = append(candidates, fact)
		}
	}
	return candidates
}

// firstSeenUndecided reports whether any Plan this Slot forces to warm up
// could still be judged first-seen (firstSeenCandidates).
func firstSeenUndecided(forced execution.PlanActivationResult, loadedGaps execution.GapLoadResult) bool {
	return len(firstSeenCandidates(forced, loadedGaps)) != 0
}

// firstSeenPrepass reads, before the head of a Slot applied window by window
// writes anything, the State of every series of each candidate Plan the Slot
// reads, and keeps of it one fact per Plan: whether any view holds something
// written before the Slot, by the rule unseenPlans reads views by
// (viewsSeenBefore, after the carry the evaluation reads them through). A
// Plan seen is noted in windows.seen, where unseenPlans finds it; the views go
// as each batch is judged (decision-020 batch 8, 2.3).
//
// It reads in the preflight's batches and stops a Plan at the first batch
// that shows something from before the Slot: a Plan that ran stops early,
// and only a Plan that is really new reads every series. A Plan the Slot
// already holds something of from before it is not read at all. The series
// whose PRIMARY did not complete are left out, as the evaluation reads no
// State for them.
//
// Each batch's views are charged to the state phase, under the share, while
// it is judged and given back after it: the pre-pass holds one batch at a
// time. Its reads witness nothing (StatePreflightRequest.Unwitnessed): the
// Slot's own preflight of the same keys remembers them again, which for the
// same Slot and key replaces what was there (runtimeWitnessCache.remember),
// so a witness left here would not mislead an apply; it would only sit in the
// store, uncharged, for every series of the Plan until the window reading it
// came round.
func (stream *streamedExecution) firstSeenPrepass(ctx context.Context, candidates []execution.PlanActivationFact) error {
	batchLimit := stream.statePreflightBatchLimit()
	for _, fact := range candidates {
		var due execution.DuePlan
		found := false
		for _, candidate := range stream.header.DuePlans {
			if candidate.Key() == fact.Key() {
				due, found = candidate, true
				break
			}
		}
		if !found {
			continue
		}
		version, err := execution.BuildApplyVersion(stream.header.Contract, due.StateApplyEpoch)
		if err != nil {
			return err
		}
		if stream.seenSoFar(due, version) {
			continue
		}
		seen := false
		items := make([]execution.StatePreflightItem, 0, batchLimit)
		for index := 0; index < len(stream.windows.prepared) && !seen; index++ {
			prepared := stream.windows.prepared[index]
			if prepared.due == nil || prepared.due.Key() != due.Key() || len(primaryIncompleteIn(stream.preparedBindings(prepared))) != 0 {
				continue
			}
			items = append(items, execution.StatePreflightItem{Identity: execution.StateKeyIdentity{
				Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: prepared.identity,
			}, ApplyVersion: version, CarryFrom: carryFrom(due), Sharded: !due.Shard.IsZero()})
			if len(items) < batchLimit {
				continue
			}
			if seen, err = stream.prepassBatch(ctx, due, version, items); err != nil {
				return err
			}
			items = items[:0]
		}
		if !seen && len(items) != 0 {
			if seen, err = stream.prepassBatch(ctx, due, version, items); err != nil {
				return err
			}
		}
		if seen {
			if stream.windows.seen == nil {
				stream.windows.seen = make(map[execution.PlanIdentity]struct{})
			}
			stream.windows.seen[due.Identity] = struct{}{}
		}
	}
	return nil
}

// prepassBatch reads one batch of a candidate Plan's State and reports
// whether it holds anything written before the Slot.
func (stream *streamedExecution) prepassBatch(
	ctx context.Context, due execution.DuePlan, version execution.ApplyVersion, items []execution.StatePreflightItem,
) (bool, error) {
	request := execution.StatePreflightRequest{Contract: stream.request.Contract, Items: items,
		OwnerFence: stream.request.OwnerFence, Unwitnessed: true}
	leave := stream.enter(slotPhasePreflight)
	loaded, err := stream.coordinator.ports.State.LoadRuntime(ctx, request)
	if err == nil {
		loaded, err = execution.ClassifyStatePreflight(request, loaded)
	}
	leave()
	if err != nil {
		return false, fmt.Errorf("alarmd worker: first-seen State pre-pass: %w", err)
	}
	held := retainedObjectBytes(loaded)
	if err := stream.reserveRetained(ctx, retainPhaseState, "normal_output", held); err != nil {
		return false, err
	}
	defer stream.unretain(retainPhaseState, held)
	for index := range loaded.Items {
		loaded.Items[index], _ = carryHistory(due, loaded.Items[index])
	}
	return viewsSeenBefore(due, version, loaded.Items), nil
}

// satisfiedMarker is the marker that records a first-seen Plan as one that
// has run: a clear at this Slot's ApplyVersion on a key that holds nothing,
// which the store writes as a marker with no scope - a cleared tombstone at
// the activation's epoch, which the next Slot reads as satisfied warming.
func satisfiedMarker(
	contractRef execution.FrozenExecutionContractRef,
	fact execution.PlanActivationFact,
) (execution.PlanGapMutation, error) {
	version, err := execution.BuildApplyVersion(contractRef, fact.Selected.StateApplyEpoch)
	if err != nil {
		return execution.PlanGapMutation{}, fmt.Errorf("alarmd worker: build first-seen Plan ApplyVersion: %w", err)
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: fact.GapIdentity(), ApplyVersion: version, ScheduleRevision: fact.Selected.ScheduleRevision,
		Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{}, Kind: execution.GapClear}},
	})
	if err != nil {
		return execution.PlanGapMutation{}, fmt.Errorf("alarmd worker: build first-seen Plan marker: %w", err)
	}
	return mutation, nil
}
