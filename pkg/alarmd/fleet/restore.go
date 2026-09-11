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
	//
	// It is only that. NextSlot is the round that has not happened yet, so on a
	// healthy cursor it is in the future -- by a whole period for a long-period
	// object. Anything that reads as "when this started" must not come from
	// here.
	NextSlot time.Time
	// LastFullSlot is the most recent round the object completed in full. It is
	// the only persisted time that bounds how long the object has been wrong:
	// it has not been fully fine since then. It is not the start of the current
	// run -- that moment is never written down -- so what it anchors is an
	// upper bound on the duration, reported as such.
	//
	// Zero means the persisted state records no full completion at all, which
	// is not the same as "it completed fully at the epoch" and must not be
	// turned into a timestamp.
	LastFullSlot time.Time
}

// Restore seeds one object from what survived the restart. It reports whether
// the object could be spoken for.
//
// An object the tracker has already determined is left alone: a round completed
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
	state := tracker.groups[queryGroup]
	if state != nil && state.determined {
		return false
	}
	if state == nil {
		if len(tracker.groups) >= tracker.maxTracked {
			return false
		}
		state = &queryGroupState{strategies: map[StrategyRef]struct{}{}}
	}
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
		// The cause is not persisted -- it is observation only -- so a restored
		// object explains itself with the completion kind alone until it
		// completes a round in this process. Leaving it blank is the honest
		// answer; guessing one would attribute a cause nobody recorded.
		state.degradedRuns = tracker.degradedRounds
		state.inAnomalyRun = true
		// The run started before this process did and the real start point did
		// not survive. The age is anchored at the last round the object is known
		// to have completed in full: it has not been fully fine since then, so
		// the duration that follows is an upper bound rather than an invention.
		// Anchoring at "now" instead would restart every object's clock on every
		// release and make a long-running failure look new each time.
		//
		// It is emphatically not anchored at NextSlot. That is the round that
		// has not run yet, so on a healthy cursor it is in the future -- which
		// produced a negative age on the page, and, because the list is ordered
		// oldest-first, sorted the object to the end where a truncated list drops
		// it first. An object whose clock is wrong in that direction is pushed
		// out of view by the very rule meant to keep the worst ones in it.
		state.runStartedAt = restored.LastFullSlot
		state.sinceFrom = SinceRestoredLastFull
		if state.runStartedAt.IsZero() || !state.runStartedAt.Before(at) {
			// Either nothing was ever completed in full, or the persisted slot
			// is not in the past -- a cursor from a clock this process cannot
			// reconcile. Both mean the same thing here: there is no usable
			// anchor, so the clock starts at the handover and says so. The
			// duration is then a lower bound, which is the honest direction to
			// be wrong in: it under-reports rather than claiming an age nobody
			// recorded.
			state.runStartedAt = at
			state.sinceFrom = SinceRestoredAtRestart
		}
		// failingSince stays zero. Progress records completions, never the
		// executions that did not finish, so nothing that survived the
		// restart can say when a failing sequence began. The object is
		// flagged stalled again once this process has watched it fail to
		// finish for the budget, which under-reports rather than invents a
		// start point.
	}
	tracker.groups[queryGroup] = state
	return true
}
