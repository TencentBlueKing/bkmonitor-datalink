// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ProbationRounds is how many rounds after its hold is lowered a Query
// Group's every round is read again, at its first rung: the rounds that must
// read clean before its lowered hold is taken as reaching its data.
//
// A trial lowers a hold once the data of a few rounds in a row came before
// the candidate, so a hold can be lowered into the spread its data arrives
// over: most rounds come before it and some after. A sample reads one round
// a rest, an hour at most, and could see the first late round only that much
// later; every round up to it was read early and evaluated so. Probation
// reads every round instead, and the first late one raises the hold at once.
//
// The count is derived, not picked. The read-hold controller's own search -
// trials, raises undoing their lowering, a sample and a trial every ten
// rounds - was run on an arrival that moves a tenth either way round to
// round, its centre at 60, 65, 70, 75 and 80% of a 300 s hold, 200 runs of
// 3,000 rounds each: 1,253 lowerings, 453 of them into the spread. The rounds
// from such a lowering to the first round that came after the lowered hold
// were 7 at the median, 45 at p90, 120 at p99, 162 at p99.9 and 211 at most.
// 120 rounds sees the first late round of 99% of lowerings into the spread;
// 45 would see 90%. The tail is lowerings just under the top of the spread,
// which are late seldom, and each late round is one evaluated on data not
// yet whole. A round counts only when its reread was compared: one skipped
// for want of a permit, or whose read failed, is no evidence the hold holds.
//
// The trials now only choose; a shadow probe reads the candidate for as many
// rounds before the hold moves (readhold.Controller.FloorProbe, kind verify).
// Run the same way with it, 805 lowerings, 5 of them into the spread - the
// probes that passed by chance - and probation saw the first late round of
// each, within 91 rounds.
const ProbationRounds = 120

// Probation reread outcomes, closed: clean read the window as the first read
// had it as far as the hold is concerned - no whole window read early;
// window_read_early found it read early, which raises the hold when the
// whole window arrived later; skipped_for_permits was never read, no permit
// being free while its rung lasted; read_failed was read and did not come
// back whole; unclassified was read and could not be told apart, its series
// tables refused by the memory line or its data still changing at its
// deepest rung. Only clean counts towards ProbationRounds.
const (
	ProbationClean             = "clean"
	ProbationWindowReadEarly   = "window_read_early"
	ProbationSkippedForPermits = "skipped_for_permits"
	ProbationReadFailed        = "read_failed"
	ProbationUnclassified      = "unclassified"
)

// ProbationOutcomes is every probation reread outcome.
var ProbationOutcomes = []string{ProbationClean, ProbationWindowReadEarly, ProbationSkippedForPermits, ProbationReadFailed,
	ProbationUnclassified}

// probationState is a Query Group in probation: the rounds read clean since
// its hold was last lowered, and whether a shadow probe verified that
// lowering.
type probationState struct {
	clean    int
	verified bool
}

// What a probation that caught a round read early followed, closed: verified,
// a lowering a shadow probe read clean first - one that passed by chance;
// trial, a lowering on earlier-read trials alone, as a build before every
// lowering was verified made.
const (
	ProbationAfterVerified = "verified"
	ProbationAfterTrial    = "trial"
)

// ProbationAfters is every lowering a probation follows.
var ProbationAfters = []string{ProbationAfterVerified, ProbationAfterTrial}

// probationRounds is how many clean rounds end a probation.
func (engine *Engine) probationRounds() int {
	if engine.options.ProbationRounds > 0 {
		return engine.options.ProbationRounds
	}
	return ProbationRounds
}

// lastLowering asks, outside the engine's lock, where the hold of a Query
// Group this process has seen no round of was last lowered: its first round
// here resumes that lowering's probation (noteRoundHoldLocked).
func (engine *Engine) lastLowering(query Query) *execution.EvaluationTime {
	if engine.options.LastLowering == nil || query.Secondary {
		return nil
	}
	qg := query.Contract.Slot.QueryGroup
	engine.mu.Lock()
	state := engine.groups[qg]
	seen := state != nil && state.holdSeen
	engine.mu.Unlock()
	if seen {
		return nil
	}
	if slot, lowered := engine.options.LastLowering(qg); lowered {
		return &slot
	}
	return nil
}

// loweringVerified asks, outside the engine's lock, whether the lowering a
// Query Group's round is frozen under was verified by a shadow probe: only
// for a round frozen lower than the one before it here, or the first round
// here, which may resume a lowering's probation - the only rounds it decides
// anything for.
func (engine *Engine) loweringVerified(query Query) bool {
	if engine.options.LoweringVerified == nil || query.Secondary {
		return false
	}
	qg := query.Contract.Slot.QueryGroup
	engine.mu.Lock()
	state := engine.groups[qg]
	ask := state == nil || !state.holdSeen || query.Contract.ReadHoldMillis < state.lastHold
	engine.mu.Unlock()
	return ask && engine.options.LoweringVerified(qg)
}

// noteRoundHoldLocked takes the hold a Query Group's round was frozen with. A
// hold lower than the round before's starts its probation again, a lowering
// a step at a time among them, and a verified one too - read clean round by
// round by a shadow probe before it was made (floor_probe.go): a probe that
// passed by chance would be read early until a sample saw it, about sixty
// rounds, and probation sees the first. The probation keeps which it follows,
// to count what it catches apart. A higher hold ends it: the hold went back
// past the data, which is what probation watches for.
//
// The first round a process sees of a group - after a restart, or taken over
// from another process - has no round before it here, and resumes the
// probation of the lowering its record names, lowering, while that is fewer
// than ProbationRounds rounds back: a deployment or a takeover moves groups
// exactly while their reads are least settled. Its count starts again from
// none: the rounds since were not all read again - the rollout's own gap,
// rounds the old owner skipped for permits, rereads it had in flight - and a
// round not read again is no evidence. With its schedule's next Slot
// unknown, the lowering is taken as recent.
func (engine *Engine) noteRoundHoldLocked(state *group, query Query, lowering *execution.EvaluationTime, verified bool) {
	hold, slot := query.Contract.ReadHoldMillis, query.Contract.Slot.EvaluationTime
	switch {
	case !state.holdSeen:
		if lowering == nil || *lowering > slot {
			break
		}
		since := 0
		if period := int64(query.FollowingSlot - slot); period > 0 {
			since = int(int64(slot-*lowering) / period)
		}
		if since < engine.probationRounds() {
			engine.setProbationLocked(query.Contract.Slot.QueryGroup, state, &probationState{verified: verified})
		}
	case hold < state.lastHold:
		engine.setProbationLocked(query.Contract.Slot.QueryGroup, state, &probationState{verified: verified})
	case hold > state.lastHold:
		engine.setProbationLocked(query.Contract.Slot.QueryGroup, state, nil)
	}
	state.lastHold, state.holdSeen = hold, true
}

// captureProbationLocked keeps a first read of a Query Group in probation as
// a reread one rung deep, from the same series sums its sample, if it is
// one, reads; its rereads are kept apart from its samples, which go on as
// before.
func (engine *Engine) captureProbationLocked(state *group, read *Read, lookback time.Duration) {
	engine.nextID++
	state.probations = append(state.probations, &sample{id: engine.nextID, source: read.source,
		queryGroup: read.query.Contract.Slot.QueryGroup, contract: read.query.Contract,
		evaluation: read.query.Contract.Slot.EvaluationTime, spec: read.query.Spec, step: read.step,
		windowEnd: time.Unix(read.query.Spec.LogicalWindow.End, 0), readAt: read.readAt, lookback: lookback,
		readyAt: read.query.ReadyAt, last: trimSummary(read.summary.buckets, read.keptFrom), keptFrom: read.keptFrom,
		planned: 1, lastChange: -1, emptyFirstRead: len(read.summary.buckets) == 0, first: read.summary.series,
		delaySeconds: read.query.Spec.PlanFacts.QueryDelaySeconds, probation: true})
}

// finishProbationLocked takes a probation reread whose rungs were read or
// passed. A round read early raises the hold as a sample's would; a clean
// one counts towards the group's ProbationRounds, and the last ends it.
func (engine *Engine) finishProbationLocked(state *group, candidate *sample) {
	for index, pending := range state.probations {
		if pending == candidate {
			state.probations = append(state.probations[:index], state.probations[index+1:]...)
			break
		}
	}
	outcome := ProbationClean
	switch class, _ := classOf(candidate); {
	case candidate.unread && candidate.readFailed:
		outcome = ProbationReadFailed
	case candidate.unread:
		outcome = ProbationSkippedForPermits
	case class == ClassUnclassified:
		outcome = ProbationUnclassified
	case class == ClassWindowReadEarly:
		outcome = ProbationWindowReadEarly
		candidate.completion = max(candidate.lastChangeAge, 0)
		engine.recordProbationCaughtLocked(candidate)
		engine.noteReadEarlyLineLocked(state, candidate, ReadEarlyPathProbation)
	}
	// The round's reread ended: what it decided is kept no longer.
	delete(state.decisions, candidate.evaluation)
	engine.counts.probations[key2(candidate.source, outcome)]++
	if state.probation == nil || candidate.contract.ReadHoldMillis != state.lastHold {
		// Read under another hold than the group's now: no evidence for it.
		return
	}
	switch outcome {
	case ProbationClean:
		if state.probation.clean++; state.probation.clean >= engine.probationRounds() {
			engine.setProbationLocked(candidate.queryGroup, state, nil)
		}
	case ProbationWindowReadEarly:
		state.probation.clean = 0
		after := ProbationAfterTrial
		if state.probation.verified {
			after = ProbationAfterVerified
		}
		engine.counts.probationCaught[key2(candidate.source, after)]++
	}
}

// probationGroupsLocked is, by source, the Query Groups in probation now.
func (engine *Engine) probationGroupsLocked() map[string]uint64 {
	groups := make(map[string]uint64, len(Sources))
	for _, state := range engine.groups {
		if state.probation != nil {
			groups[state.source]++
		}
	}
	return groups
}

// setProbationLocked starts, restarts or ends a group's probation, and keeps
// the set read without the lock (KeepsDecisions) beside it.
func (engine *Engine) setProbationLocked(queryGroup execution.QueryGroupIdentity, state *group, probation *probationState) {
	state.probation = probation
	if probation != nil {
		engine.probationGroups.Store(queryGroup, struct{}{})
		return
	}
	engine.probationGroups.Delete(queryGroup)
}

// KeepsDecisions is whether a round's decision for the Query Group would be
// kept (NoteSlotDecision): the group is in probation. Read without the
// engine's lock, so a report for any other group - nearly every series of
// every Slot - is turned away before it is built.
func (engine *Engine) KeepsDecisions(queryGroup execution.QueryGroupIdentity) bool {
	_, kept := engine.probationGroups.Load(queryGroup)
	return kept
}

// InProbation reports whether a Query Group is in probation, and how many of
// its rounds have read clean since.
func (engine *Engine) InProbation(queryGroup execution.QueryGroupIdentity) (int, bool) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	state := engine.groups[queryGroup]
	if state == nil || state.probation == nil {
		return 0, false
	}
	return state.probation.clean, true
}

// recordProbationCaughtLocked hands a probation reread that found its round
// read early to the read-hold controller, whatever arrived. A sample's finding
// goes only when a whole window arrived to read an age from; a probation's is
// the lowering shown wrong either way - a round decided on data not yet whole,
// or on data that went after it - and the controller undoes the lowering and
// starts its quiet over (readhold.Controller.Observe). Kept, it went nowhere:
// the hold stayed lowered, the probation ran on, and trials went on lowering.
func (engine *Engine) recordProbationCaughtLocked(candidate *sample) {
	if engine.options.OnWholeWindowReadEarly == nil || candidate.holdReported {
		return
	}
	candidate.holdReported = true
	rung := ""
	if candidate.lastChange >= 0 {
		rung = RungNames[candidate.lastChange]
	}
	engine.holdEvidence = append(engine.holdEvidence, ReadHoldEvidence{Contract: candidate.contract,
		ArrivalAge: candidate.completion, Rung: rung, Buckets: append([]int64(nil), candidate.lastBuckets...), Probation: true})
}
