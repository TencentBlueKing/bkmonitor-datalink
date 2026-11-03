// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// PlanGapRecoveryMutation is what one healthy Slot does to a Plan's standing
// gap marker: every scope moves one slot closer to its warmup requirement,
// and a scope that reaches it clears.
//
// A healthy Slot is one whose Plan input was whole: evaluated series that
// carried their round forward, or, with no series at all, every input of the
// Plan FULL and available. Both are evidence about every scope, Level scopes
// included. What a marker guards is the round whose answer is unknown; a round
// that answered whole and held nothing for the Plan is a known absence, not an
// unknown. The warmup requirement is the window a Level's decisions reach back
// over, and windows are cut by time, so once that many whole rounds have
// passed the unknown round is outside every window - whether or not a series
// had points in them. A series that comes back sooner still meets the marker
// and is held by it.
//
// Level scopes used to be left alone on a round with no series, on the reading
// that they ask for a series' history to have moved. A Plan that matches no
// series then kept a marker opened by a query failure for as long as it
// matched none: it never warmed, never cleared, and held its Levels' reason
// on the Slot every round.
//
// Nil when there is nothing to say -- no marker, or a marker that is not
// standing.
//
// Known boundary, per series: a Level a marker held keeps WARMING in its own
// state and converges only on a window that is whole again (evaluation
// guardConvergenceAllowed), so a series that misses whole minutes stays under
// GAP_GUARD_WARMING for as long as it keeps missing them. That withholds only
// NORMAL, which needs a FULL window whether or not a guard holds it: ABNORMAL
// is decided from the anomalies in the window and RECOVERY steps over minutes
// nobody observed, so the series still alerts and recovers. A reader should
// look at the window's missing minutes (answered without the series, or a
// round this side did not see whole) rather than read the reason as "not
// evaluated".
//
// A marker written under an older Plan schedule revision still recovers, it
// only restarts its warmup: the store discards the warmup count of every
// scope whose schedule revision changed (state applyGapScopes), so this Slot
// is the first observed FULL Slot under the current revision and the count
// starts at zero here too. Before this the marker was neither warmed nor
// cleared once the schedule revision moved, and because no writer refreshes
// the revision while the data is FULL, every Level under the marker stayed
// UNKNOWN with the marker's reason for as long as the marker lived -- whatever
// that reason was.
func PlanGapRecoveryMutation(
	contractRef FrozenExecutionContractRef,
	due DuePlan,
	gaps GapLoadResult,
) (*PlanGapMutation, error) {
	identity := due.GapIdentity()
	gap, found := gaps.Find(identity)
	if !found || gap.Status != GapFound {
		return nil, nil
	}
	restarted := gap.LastScheduleRevision != due.ScheduleRevision
	scopes := make([]GapScopeMutation, 0, len(gap.Scopes))
	for _, current := range gap.Scopes {
		observed := current.ObservedFullSlots
		if restarted {
			observed = 0
		}
		scope := GapScopeMutation{Scope: current.Scope, Kind: GapClear}
		if observed+1 < current.RequiredFullSlots {
			scope.Kind = GapWarmup
			scope.ReasonCode = current.ReasonCode
			scope.RequiredFullSlots = current.RequiredFullSlots
		}
		scopes = append(scopes, scope)
	}
	// A loaded marker always carries at least one scope; a mutation carrying
	// none is refused, so an empty one is never proposed.
	if len(scopes) == 0 {
		return nil, nil
	}
	version, err := BuildApplyVersion(contractRef, due.StateApplyEpoch)
	if err != nil {
		return nil, err
	}
	mutation, err := BuildPlanGapMutation(PlanGapMutation{
		Identity:               identity,
		ExpectedMarkerRevision: gap.MarkerRevision,
		ApplyVersion:           version,
		ScheduleRevision:       due.ScheduleRevision,
		Scopes:                 scopes,
	})
	if err != nil {
		return nil, err
	}
	return &mutation, nil
}
