// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// recordedEmptyRun is the run of empty rounds an object's record carries,
// on the source's clock: the first empty Slot of the run, and the Slot of
// the record's last round, zero when it names none.
//
// The record's writer ends the run only on records: a gap, an unavailable
// round or a skip between two empty ones leaves its start standing
// (progress noteDataAndEmptyRun). So a record read after this process has
// committed rounds of its own still carries the start from before the
// restart.
type recordedEmptyRun struct {
	since     int64
	lastRound int64
}

// noteRecordedRun keeps what the record of an object this process has
// already determined says about records: that it had them, which this
// process takes the way restoring the record first would have; or the run
// of empty rounds it is in, held until this process's own run can take its
// start -- and dropped there if this process has seen records by then.
// Caller holds tracker.mu.
func (tracker *Tracker) noteRecordedRun(state *queryGroupState, restored RestoredState) {
	lastRoundHadData := restored.LastRound != nil && restored.LastRound.Kind == "FULL_COMPLETED"
	if !restored.LastDataSlot.IsZero() || lastRoundHadData {
		state.sawData = true
		if restored.LastRound != nil && lastRoundHadData && !restored.LastRound.Slot.IsZero() &&
			restored.LastRound.Slot.Unix() > state.lastDataSlot {
			state.lastDataSlot = restored.LastRound.Slot.Unix()
		}
		if slot := restored.LastDataSlot.Unix(); !restored.LastDataSlot.IsZero() && slot > state.lastDataSlot {
			state.lastDataSlot = slot
		}
		return
	}
	if restored.EmptyRunSince.IsZero() {
		return
	}
	run := &recordedEmptyRun{since: restored.EmptyRunSince.Unix()}
	if restored.LastRound != nil && !restored.LastRound.Slot.IsZero() {
		run.lastRound = restored.LastRound.Slot.Unix()
	}
	state.recordRun = run
	tracker.joinRecordedRun(state)
}

// joinRecordedRun gives this process's run of empty rounds the start the
// record carries, the start restoring the record before the first round
// would have given it.
//
// Only onto a run this process has kept unbroken from its first empty
// round, on an object it has never seen records for, and only when the
// record's start is the earlier one. The stretch before that first round is
// read against the hour the way a restored run's first round is -- with no
// cadence yet, as inherited (emptyRunHole) -- from the first Slot of a
// skipped run that ended before it, or else from the record's last round
// when the record is older than that first round. A record this process has
// already written over names no round before its own; the stretch is then
// not known, and the record's start is taken, which is what its writer's
// rule says about a gap.
//
// Held while no empty round has come: a record read after a skip or a
// failed round is joined at the first empty one. Caller holds tracker.mu.
func (tracker *Tracker) joinRecordedRun(state *queryGroupState) {
	run := state.recordRun
	if run == nil {
		return
	}
	if state.sawData {
		state.recordRun = nil
		return
	}
	if state.firstEmptySlot == 0 {
		return
	}
	state.recordRun = nil
	if state.emptySlotFrom != SinceSnapshotContinuity || state.emptySinceSlot != state.firstEmptySlot ||
		run.since >= state.emptySinceSlot {
		return
	}
	prior := int64(0)
	switch {
	case state.gapSkip != nil && state.gapSkip.LastSlot < state.firstEmptySlot:
		prior = state.gapSkip.FirstSlot
	case run.lastRound != 0 && run.lastRound < state.firstEmptySlot:
		prior = run.lastRound
	}
	if prior != 0 {
		gap := state.firstEmptySlot - prior
		if emptyRunHole(gap, 0, true, tracker.emptyEveryRoundAfter) {
			return
		}
		if gap > state.emptyStride {
			state.emptyStride = gap
		}
	}
	state.emptySinceSlot, state.emptySlotFrom = run.since, SinceRestoredEmptyRun
	if state.emptyRuns > 0 {
		// The round the record vouches for, as the restored run counts it.
		state.emptyRuns++
	}
}

// WantsRestore reports whether reading the object's record could still tell
// this process something: an object it has not determined, or one it has
// determined without ever seeing records and whose record it has not read,
// which may date the object's run of empty rounds from before this process
// started (joinRecordedRun).
func (tracker *Tracker) WantsRestore(queryGroup string) bool {
	if tracker == nil {
		return false
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	state := tracker.groups[queryGroup]
	return state == nil || !state.determined || (!state.sawData && !state.recordRead)
}
