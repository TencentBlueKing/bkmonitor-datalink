// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// GapRecoveryReach is how much of a standing marker one healthy Slot is
// evidence about.
//
// A Slot that evaluated the Plan's series is evidence about every scope: the
// query answered whole and each series carried its round forward. A Slot that
// completed FULL with no series at all is evidence about the Plan and about
// nothing below it -- the query answered whole, which is what a Plan scope
// asks, but no Level advanced a history, which is what a Level scope asks.
// Recovering a Level scope on a round where no Level ran would count a slot
// towards a requirement the round did not meet.
//
// The two reaches exist so that the difference between them is this one
// value and not two copies of the warmup arithmetic.
type GapRecoveryReach uint8

const (
	// GapRecoverEveryScope is the reach of a Slot that evaluated series.
	GapRecoverEveryScope GapRecoveryReach = iota
	// GapRecoverPlanScopeOnly is the reach of a Slot that completed FULL with
	// no series: the Plan's scopes advance, the Level scopes are left exactly
	// as they were loaded.
	GapRecoverPlanScopeOnly
)

// PlanGapRecoveryMutation is what one healthy Slot does to a Plan's standing
// gap marker: every scope within the Slot's reach moves one slot closer to
// its warmup requirement, and a scope that reaches it clears.
//
// Nil when there is nothing to say -- no marker, a marker that is not
// standing, or a marker whose every scope is out of this Slot's reach.
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
	reach GapRecoveryReach,
) (*PlanGapMutation, error) {
	identity := due.GapIdentity()
	gap, found := gaps.Find(identity)
	if !found || gap.Status != GapFound {
		return nil, nil
	}
	restarted := gap.LastScheduleRevision != due.ScheduleRevision
	scopes := make([]GapScopeMutation, 0, len(gap.Scopes))
	for _, current := range gap.Scopes {
		if reach == GapRecoverPlanScopeOnly && current.Scope.HasLevel {
			continue
		}
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
	// Only reachable under GapRecoverPlanScopeOnly: a loaded marker always
	// carries at least one scope, and a mutation carrying none is refused.
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
