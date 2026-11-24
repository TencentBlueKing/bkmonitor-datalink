// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A window guard is raised by a gap and held until the window is whole
// again. Some gaps are this deployment's own doing and nobody else's: the
// Plan's activation moved under a Slot -- an edit, or a change alarmd made
// to what the Plan's trigger is keyed on -- or this process's restart or
// takeover gave up Slots while it caught up. The window then starts again
// from that point and refills by itself, one round at a time. Read through
// the guard, such a row went under WINDOW_UNDECIDED, the line that sends
// the alarmd maintainer to find whose the missing minutes are: a rollout
// that put the trigger fingerprint on two log strategies read four rounds
// out of five that way, and a restart listed two objects there until their
// windows filled half an hour later. The skip records split the restart's
// catch-up out the same way (AFTER_RESTART); this is the window it leaves.

// Rewarm is a guarded window this process's own state change opened:
// Trigger is the reason the guard carries, Since when the rewarm began --
// the guard's first round here for an activation change, the first
// catch-up skip for a restart or takeover.
type Rewarm struct {
	Trigger string    `json:"trigger"`
	Since   time.Time `json:"since"`
}

// planMovedReasons are the guard triggers that say the Plan's activation
// moved under a Slot: an edit or a re-keyed trigger (CONFIG_DRIFT), a Plan
// that left the active set (PLAN_NOT_ACTIVE) and came back
// (PLAN_REACTIVATED).
var planMovedReasons = []string{contract.ReasonConfigDrift, contract.ReasonPlanNotActive, contract.ReasonPlanReactivated}

// catchUpSkipReasons are the guard triggers a skipped Slot leaves: a Slot
// past the replay's reach, and one that came after its recovery bound.
var catchUpSkipReasons = []string{contract.ReasonGapSkipped, contract.ReasonSnapshotUnavailable}

// rewarmOf reads the object's guarded window as a rewarm when the reason
// its guard carries is one of this process's own: an activation change,
// or a skip this process's catch-up made (catchUpSkipAt). Nil otherwise.
// Caller holds the lock.
func rewarmOf(state *queryGroupState) *Rewarm {
	coverage, reason := state.coverage, state.causeReason
	if coverage == nil || coverage.Guarded == 0 || reason == "" {
		return nil
	}
	switch {
	case inVocabulary(reason, planMovedReasons):
		since := state.reasonSince
		for _, held := range state.guards {
			if held.guard.Reason == reason && !held.guard.FirstAt.IsZero() && (since.IsZero() || held.guard.FirstAt.Before(since)) {
				since = held.guard.FirstAt
			}
		}
		if since.IsZero() {
			return nil
		}
		return &Rewarm{Trigger: reason, Since: since.UTC()}
	case inVocabulary(reason, catchUpSkipReasons) && !state.catchUpSkipAt.IsZero():
		return &Rewarm{Trigger: reason, Since: state.catchUpSkipAt.UTC()}
	}
	return nil
}

// selfRewarming says a guard-held row is a rewarm still within the rounds
// filling a window takes: the row names the trigger it is held under, the
// window is short, and neither the short run nor the reason clock has run
// longer than the window's own requirement (the bound Persistent reads).
// The reason clock is read beside the short run because it does not restart
// when the window fills: a guard held for dozens of rounds whose window
// filled once and went short again is not a rewarm. Past the bound the
// window is not refilling by itself and the row is the undecided window it
// was.
//
// Data that is also missing inside the catch-up grace is not told apart
// here: the data-side readings that run first (SERIES_DATA_MISSING,
// SERIES_SPARSE and the rest, read off the window's holes) still win where
// they can tell, and where they cannot the row reads as the rewarm for at
// most WorstRequired rounds, then goes back to WINDOW_UNDECIDED.
func selfRewarming(anomaly Anomaly) bool {
	coverage := anomaly.Coverage
	return anomaly.Rewarm != nil && anomaly.Rewarm.Trigger == anomaly.CauseReason && coverage != nil &&
		coverage.Short > 0 && coverage.WorstRequired > 0 && coverage.ShortRounds <= coverage.WorstRequired &&
		uint32(max(anomaly.Consecutive, 0)) <= coverage.WorstRequired
}

// rewarmCause is the fold of a WINDOW_REWARMING row: what opened it and
// the minute it began, so a rollout's rewarms fold on the rollout.
func rewarmCause(anomaly Anomaly) string {
	if anomaly.Rewarm == nil {
		return causeNoCounts
	}
	opened := "计划变更"
	if inVocabulary(anomaly.Rewarm.Trigger, catchUpSkipReasons) {
		opened = "本副本重启或接手时跳过的轮次"
	}
	return opened + "后重攒（" + anomaly.Rewarm.Trigger + "，起点 " + anomaly.Rewarm.Since.UTC().Format("2006-01-02T15:04Z") + "）"
}
