// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// RestoredState is what the control plane already recorded about an object
// before this process started.
//
// The tracker learns an object's state by watching a round complete, and it
// keeps that only in memory. So every restart -- including a configuration
// reload, which changes no code at all -- leaves the replica unable to speak
// for anything it owns until each object completes a fresh round. The whole
// deployment then reports UNKNOWN for as long as the slowest object's period,
// which is precisely the window a rolling release most needs a verdict in.
//
// None of that is missing information: the last completion survives in the
// object's Progress. This is that evidence, brought back in.
type RestoredState struct {
	// LastCompletion is the persisted completion kind. Empty means the object
	// has never completed a round, which is not evidence of anything.
	LastCompletion string
	// NextSlot is when the object's next round is due. It is the freshness
	// test: a Progress cursor left far behind says the object stopped, so its
	// last completion describes history rather than now.
	NextSlot time.Time
}

// Restore seeds one object from what survived the restart. It reports whether
// the object could be spoken for.
//
// An object the tracker has already observed is left alone: a round completed
// in this process is better evidence than a persisted cursor, and overwriting
// it would move the object backwards.
//
// staleAfter is how far behind the Progress cursor may be before the persisted
// completion stops counting as evidence about now. It is the deployment's own
// replay age rather than a constant: past it the deployment has already
// promised to terminate a Slot that cannot complete, so a cursor older than
// that describes an object that stopped, not one that is merely between rounds.
func (tracker *Tracker) Restore(queryGroup string, restored RestoredState, at time.Time, staleAfter time.Duration) bool {
	if tracker == nil || queryGroup == "" || restored.LastCompletion == "" {
		return false
	}
	if staleAfter > 0 && !restored.NextSlot.IsZero() && at.Sub(restored.NextSlot) > staleAfter {
		// The object is further behind than the deployment tolerates, so what
		// it last completed is not a statement about now. Leaving it
		// undetermined is the honest answer, and it is also the safe one: an
		// object nobody can speak for holds the verdict at UNKNOWN instead of
		// being folded into healthy.
		return false
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, seen := tracker.groups[queryGroup]; seen {
		return false
	}
	state := &queryGroupState{strategies: map[StrategyRef]struct{}{}}
	state.determined = true
	state.lastCompleted = restored.LastCompletion
	if !healthyCompletion(restored.LastCompletion) {
		// The persisted state says the last round did not go well. Seeding it
		// straight into an anomaly rather than waiting to see it fail again is
		// deliberate: the alternative reports the object as healthy until it
		// re-accumulates rounds, which is the "empty anomaly list from an empty
		// tracker" failure this whole determined/unknown split exists to stop.
		// Over-reporting one object for one round is the cheaper mistake.
		state.currentKind = KindDegradedRun
		state.reasonCode = restored.LastCompletion
		state.degradedRuns = tracker.degradedRounds
		state.inAnomalyRun = true
		// The run started before this process did and the real start point did
		// not survive, so the age is anchored where the evidence is: the round
		// that recorded the failure. Anchoring at "now" would restart every
		// object's clock on every release and make a long-running failure look
		// new each time.
		state.runStartedAt = restored.NextSlot
		if state.runStartedAt.IsZero() {
			state.runStartedAt = at
		}
	}
	tracker.groups[queryGroup] = state
	return true
}
