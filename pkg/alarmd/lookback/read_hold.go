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
	"container/heap"
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ReadHoldEvidence says when a confirmed whole window first held its final
// values, measured from the original window end, including source delay.
// Contract carries the hold frozen for the formal first read.
type ReadHoldEvidence struct {
	Contract   execution.FrozenExecutionContractRef
	ArrivalAge time.Duration
	Rung       string
	Buckets    []int64
	// Probation marks a probation reread that found its round read early: a
	// lowered hold shown wrong, whether or not a whole window arrived to read
	// an age from (ArrivalAge is when the reread saw it, at least).
	Probation bool
}

// EarlierReadEvidence compares a candidate h/2 read with the formal first
// read of the same Slot. An unobserved read supplies no decrease evidence.
type EarlierReadEvidence struct {
	Contract      execution.FrozenExecutionContractRef
	CandidateHold time.Duration
	Observed      bool
	Equal         bool
	Outcome       string
}

const (
	EarlierEqual           = "equal"
	EarlierDifferent       = "different"
	EarlierPermitRefused   = "permit_refused"
	EarlierMemoryRefused   = "memory_refused"
	EarlierReadFailed      = "read_failed"
	EarlierFirstIncomplete = "first_read_incomplete"
	EarlierOwnerLost       = "owner_lost"
	EarlierOvertaken       = "overtaken"
)

var EarlierReadOutcomes = []string{EarlierEqual, EarlierDifferent, EarlierPermitRefused, EarlierMemoryRefused,
	EarlierReadFailed, EarlierFirstIncomplete, EarlierOwnerLost, EarlierOvertaken}

// What overtook a trial counted EarlierOvertaken, the first that did: one
// outcome has six causes, and which of them ends the trials decides the fix.
const (
	// OvertakenPreparedLate: the Slot was prepared after the candidate
	// readiness point had passed, so no trial could start in time.
	OvertakenPreparedLate = "prepared_after_readiness"
	// OvertakenNextSlot: the group's next Slot was prepared while this
	// trial's Slot had not reached its formal read.
	OvertakenNextSlot = "next_slot_first"
	// OvertakenFormalFirst: the formal read began before the trial was done.
	OvertakenFormalFirst = "formal_read_first"
	// OvertakenHoldMoved: the group's hold changed under the trial, so its
	// candidate is no longer the next step down - or a trial reserved ahead
	// found its Slot frozen otherwise than it was moved to.
	OvertakenHoldMoved = "hold_moved"
	// OvertakenStartedLate and OvertakenFinishedLate: the trial's read began,
	// or ended, after the candidate readiness point.
	OvertakenStartedLate  = "started_after_readiness"
	OvertakenFinishedLate = "finished_after_readiness"
)

var EarlierOvertakenPaths = []string{OvertakenPreparedLate, OvertakenNextSlot, OvertakenFormalFirst, OvertakenHoldMoved,
	OvertakenStartedLate, OvertakenFinishedLate}

// Why a held Slot's first attempt reserves no earlier-read trial, as Prepare
// finds it, closed:
//   - not_owned: the group is not this process's. Counted by source alone,
//     at each attempt: the lookback keeps nothing of a group it does not own.
//   - no_hold: the group's hold has come down to none since the Slot was
//     frozen with one; there is nothing below it to try.
//   - backend_failing: the group's queries are failing, and a trial is one
//     more read of the same backend.
//   - no_candidate: the read-hold controller gives no candidate to try, and
//     counts why (readhold.EarlierSkipReasons).
//   - trial_reserved: a trial is already reserved ahead, for a later Slot.
//   - sample_in_flight: the group's sample is being taken or read, and a
//     trial rides the next one.
//   - resting: the group's sample rests past the Slot's readiness, and a
//     trial rides the sample its rest ends with.
//   - floor_probe: the group's search below its hold has run out and its
//     floor is being probed (floor_probe.go): the Slot has a probe round, not
//     a trial, and is not no_candidate.
const (
	EarlierSkipNotOwned       = "not_owned"
	EarlierSkipNoHold         = "no_hold"
	EarlierSkipBackendFailing = "backend_failing"
	EarlierSkipNoCandidate    = "no_candidate"
	EarlierSkipTrialReserved  = "trial_reserved"
	EarlierSkipSampleInFlight = "sample_in_flight"
	EarlierSkipResting        = "resting"
	EarlierSkipFloorProbe     = "floor_probe"
)

// EarlierSkipReasons is every reason Prepare reserves no trial. A Slot is
// counted once, by what its first Prepare found; one whose readiness retry
// then reserves a trial is counted here and among the trials too, which says
// what moved its trial past its point.
var EarlierSkipReasons = []string{EarlierSkipNotOwned, EarlierSkipNoHold, EarlierSkipBackendFailing, EarlierSkipNoCandidate,
	EarlierSkipTrialReserved, EarlierSkipSampleInFlight, EarlierSkipResting, EarlierSkipFloorProbe}

// skipLocked counts the Slot of query skipped for reason on its group's
// state, once a Slot: the first Prepare of a Slot decides it, and its
// readiness retries are not counted again - nor is any Slot before the
// latest decided, whose retry can come after the next Slot's first attempt
// under a hold past the period. A group's first attempts come in Slot
// order, so none is missed; a Slot replayed out of order is not counted,
// rather than counted twice. It says whether it counted.
func (engine *Engine) skipLocked(state *group, query Query, reason string) bool {
	slot := query.Contract.Slot.EvaluationTime
	if slot <= state.decided {
		return false
	}
	state.decided = slot
	engine.counts.earlierSkipped[key2(state.source, reason)]++
	state.reading.skipped[wordIndex(EarlierSkipReasons, reason)]++
	return true
}

// skip is skipLocked under the engine's lock.
func (engine *Engine) skip(query Query, now time.Time, reason string) {
	engine.mu.Lock()
	engine.skipLocked(engine.groupLocked(query, now), query, reason)
	engine.mu.Unlock()
}

// EarlierAheadSlots counts trials reserved ahead by how many Slots ahead
// (aheadTrialLocked): a hold far past the period needs the trial of a Slot
// further on than the next.
var EarlierAheadSlots = []string{"1", "2", "3", "4+"}

func aheadSlotsLabel(slots int) string {
	if slots >= len(EarlierAheadSlots) {
		return EarlierAheadSlots[len(EarlierAheadSlots)-1]
	}
	return EarlierAheadSlots[slots-1]
}

// overtake ends trial as overtaken by path unless something ended it first.
func (trial *earlierSample) overtake(path string) {
	if trial.done {
		return
	}
	trial.done, trial.outcome, trial.overtakenBy = true, EarlierOvertaken, path
}

// EarlierTrialEnd is how one trial ended: its Slot, its outcome and, when it
// was overtaken, what overtook it; its candidate hold, and how many Slots
// ahead it was reserved (0 at its own Slot). Overtaken by a moved hold, it
// also has the hold the trial was reserved under and the hold its Slot froze
// at: a hold lowered a step per Slot moves under every trial reserved on the
// way down, and the two tell such a step from any other move.
type EarlierTrialEnd struct {
	Contract      execution.FrozenExecutionContractRef
	Source        string
	Outcome       string
	OvertakenBy   string
	CandidateHold time.Duration
	AheadSlots    int
	ReservedHold  time.Duration
	FrozenHold    time.Duration
}

// endTrialLocked keeps how trial ended for OnEarlierTrialEnd.
func (engine *Engine) endTrialLocked(trial *earlierSample, outcome string) {
	if engine.options.OnEarlierTrialEnd == nil {
		return
	}
	end := EarlierTrialEnd{Contract: trial.query.Contract, Source: trial.source, Outcome: outcome,
		CandidateHold: trial.candidateHold, AheadSlots: trial.aheadSlots}
	if outcome == EarlierOvertaken {
		end.OvertakenBy = trial.overtakenBy
	}
	if end.OvertakenBy == OvertakenHoldMoved {
		end.ReservedHold = time.Duration(trial.query.Contract.ReadHoldMillis) * time.Millisecond
		end.FrozenHold = end.ReservedHold
		if trial.frozenHold != nil {
			end.FrozenHold = *trial.frozenHold
		}
	}
	engine.trialEnds = append(engine.trialEnds, end)
}

// publishTrialEnds hands the kept trial ends to OnEarlierTrialEnd, outside
// the engine's lock.
func (engine *Engine) publishTrialEnds() {
	if engine.options.OnEarlierTrialEnd == nil {
		return
	}
	engine.mu.Lock()
	ends := engine.trialEnds
	engine.trialEnds = nil
	engine.mu.Unlock()
	for _, end := range ends {
		engine.options.OnEarlierTrialEnd(end)
	}
}

// countOvertakenLocked counts what overtook a trial counted EarlierOvertaken.
func (engine *Engine) countOvertakenLocked(trial *earlierSample) {
	engine.counts.earlierOvertaken[key2(trial.source, trial.overtakenBy)]++
	trial.group.reading.overtaken[wordIndex(EarlierOvertakenPaths, trial.overtakenBy)]++
}

// ReadHoldIgnoredReasons count findings that cannot raise a whole-window hold.
var ReadHoldIgnoredReasons = []string{ClassPartialRevised, IgnoredNoWholeWindowArrival}

// IgnoredNoWholeWindowArrival is a sample classed window_read_early in which
// no series arrived whole after the first read -- one went, or one changed
// without arriving -- so no arrival age can be read from it. It is not two
// later reads disagreeing.
const IgnoredNoWholeWindowArrival = "no_whole_window_arrival"

type ignoredReadHold struct {
	evidence ReadHoldEvidence
	reason   string
}

func firstReadAge(candidate *sample) time.Duration {
	ready := candidate.readyAt
	if ready.IsZero() {
		ready = candidate.readAt
	}
	return ready.Sub(candidate.windowEnd)
}

func (engine *Engine) recordIgnoredLocked(candidate *sample, reason string) {
	engine.counts.holdIgnored[key2(candidate.source, reason)]++
	if engine.options.OnReadHoldIgnored != nil {
		engine.ignoredEvidence = append(engine.ignoredEvidence, ignoredReadHold{reason: reason, evidence: ReadHoldEvidence{
			Contract: candidate.contract, ArrivalAge: candidate.completion}})
	}
}

func wholeWindowArrival(candidate *sample) bool {
	return candidate.emptyFirstRead && len(candidate.last) > 0 || candidate.existingArrived > 0
}

func (engine *Engine) recordReadHoldLocked(candidate *sample) {
	class, _ := classOf(candidate)
	if engine.options.OnWholeWindowReadEarly == nil || candidate.holdReported || candidate.lastChange < 0 || class != ClassWindowReadEarly || !wholeWindowArrival(candidate) {
		return
	}
	candidate.holdReported = true
	engine.holdEvidence = append(engine.holdEvidence, ReadHoldEvidence{Contract: candidate.contract,
		ArrivalAge: candidate.completion, Rung: RungNames[candidate.lastChange], Buckets: append([]int64(nil), candidate.lastBuckets...)})
}

type earlierSample struct {
	query         Query
	source        string
	at            time.Time
	readyAt       time.Time
	candidateHold time.Duration
	keptFrom      int64
	summary       readSummary
	running       bool
	done          bool
	formal        bool
	outcome       string
	// overtakenBy is what overtook the trial when outcome is EarlierOvertaken
	// (EarlierOvertakenPaths).
	overtakenBy string
	cancel      context.CancelFunc
	group       *group
	// ahead marks a trial reserved at an earlier Slot's first attempt for a
	// Slot its schedule has later, aheadSlots Slots on. Its query is the
	// earlier one moved to that Slot until the Slot's own first attempt
	// adopts the frozen query.
	ahead      bool
	aheadSlots int
	// frozenHold is the hold the Slot of a trial reserved ahead froze at
	// when it dropped the trial for it; a trial at its own Slot was read
	// under the hold its Slot froze at.
	frozenHold *time.Duration
	// probe marks a round of a shadow probe of the group's floor, read at
	// the floor (candidateHold) and kept in the probe's reservations, never
	// in prepared (floor_probe.go).
	probe *floorProbeState
}

// dropTrialLocked ends a trial whose Slot will not compare it, counted
// overtaken by path unless something overtook it first.
func (engine *Engine) dropTrialLocked(trial *earlierSample, path string) {
	if trial.overtakenBy == "" {
		trial.overtakenBy = path
	}
	trial.done, trial.outcome = true, EarlierOvertaken
	if trial.cancel != nil {
		trial.cancel()
	}
	engine.counts.earlierReads[key2(trial.source, EarlierOvertaken)]++
	engine.countOvertakenLocked(trial)
	trial.group.reading.earlier[wordIndex(EarlierReadOutcomes, EarlierOvertaken)]++
	engine.endTrialLocked(trial, EarlierOvertaken)
}

// trialStart is when a trial of candidate starts: as long before its point
// readyAt as a read of the group's query takes (bound), so a query slower
// than RecheckTimeout can still finish before that point and prove the lower
// hold; a fixed RecheckTimeout cut every trial of such a group short and kept
// its alerts later than they need to be.
//
// A trial of a lower hold starts no earlier than the readiness without the
// hold (baseline). A trial of no hold - half the hold under a step - has that
// readiness as its own point, so held to it the trial had no time to read:
// with the step equal to the period, every hold under two periods. It starts
// the bound before instead. Only when it looks moves: its read is still
// compared whole with the formal first read, and one that is incomplete, or
// finds less, proves nothing.
func trialStart(readyAt, baseline time.Time, candidate, bound time.Duration) time.Time {
	start := readyAt.Add(-bound)
	if candidate > 0 && start.Before(baseline) {
		start = baseline
	}
	return start
}

// shiftedSpec is spec with its windows moved by shift seconds and its digest
// derived again; false when the moved spec has none.
func shiftedSpec(spec execution.PhysicalQuerySpec, shift int64) (execution.PhysicalQuerySpec, bool) {
	moved := spec
	for _, window := range []*execution.QueryWindow{&moved.LogicalWindow, &moved.ProviderRange, &moved.AcceptedRange} {
		window.Start += shift
		window.End += shift
	}
	digest, err := execution.DerivePhysicalQueryDigest(moved)
	if err != nil {
		return spec, false
	}
	moved.Digest = digest
	return moved, true
}

// aheadTrialLocked reserves, at query's first attempt, the trial of a Slot its
// schedule has later, or returns nil. Slots of a Query Group run one after
// another, so a Slot's first attempt comes no earlier than the previous Slot's
// held read. With a hold past the period less the reread bound that is past
// where the Slot's own trial had to start - under two steps, a trial of no
// hold whose point is the readiness without the hold; from two steps, a trial
// of half the hold - and the trial reserved there is overtaken before it
// reads.
//
// The next Slot's trial can be past its start as well: its start is a period
// after this Slot's, and this Slot's first attempt is itself a hold after
// the Slot before it was due. So the trial reserved is that of the first Slot
// on, k Slots ahead, whose trial starts after now and whose formal read ends
// the group's rest. k is bounded by the hold: k Slots ahead are k periods
// later, and a first attempt is no more than a hold and a reread bound late,
// so a k past that, and two Slots of slack, never needs looking at.
//
// The Slot k on is this one moved by k of the schedule's steps: its window,
// and its readiness the same offset after its evaluation time, under the
// group's hold now. Its own first attempt keeps the trial only when its frozen
// query is that one (Prepare); the Slots between keep it standing.
func (engine *Engine) aheadTrialLocked(state *group, query Query, hold, base, candidate time.Duration, now time.Time) *earlierSample {
	step := int64(query.FollowingSlot - query.Contract.Slot.EvaluationTime)
	if query.FollowingSlot == 0 || step <= 0 {
		return nil
	}
	bound := state.rereadBound()
	limit := int((hold+bound)/(time.Duration(step)*time.Second)) + 2
	for slots := 1; slots <= limit; slots++ {
		shift := step * int64(slots)
		baseline := query.ReadyAt.Add(-hold).Add(time.Duration(shift) * time.Second)
		readyAt := baseline.Add(candidate)
		start := trialStart(readyAt, baseline, candidate, bound)
		if !now.Before(start) || baseline.Add(base).Before(state.nextAt) {
			continue
		}
		spec, derived := shiftedSpec(query.Spec, shift)
		if !derived {
			return nil
		}
		next := query
		next.Spec, next.FollowingSlot = spec, 0
		next.Contract.Slot.EvaluationTime = query.Contract.Slot.EvaluationTime + execution.EvaluationTime(shift)
		next.Contract.ReadHoldMillis = base.Milliseconds()
		next.ReadyAt = baseline.Add(base)
		return &earlierSample{query: next, source: state.source, candidateHold: candidate, group: state, ahead: true,
			aheadSlots: slots, at: start, readyAt: readyAt, keptFrom: tailFrom(next.Spec.LogicalWindow, state.step, tailSteps)}
	}
	return nil
}

func (engine *Engine) groupLocked(query Query, now time.Time) *group {
	qg := query.Contract.Slot.QueryGroup
	state := engine.groups[qg]
	if state == nil {
		state = &group{depth: 1, rest: RungSteps[0], sinceProbe: probeEvery - 2}
		state.reading.since = now
		if engine.options.ProbeFirstSamples {
			state.sinceProbe = probeEvery - 1
		}
		if !engine.options.UnspreadFirstSamples {
			state.nextAt = now.Add(time.Duration(spreadFraction(qg) * float64(restCap)))
		}
		engine.groups[qg] = state
	}
	state.source = sourceOf(query.Spec.PlanFacts)
	state.step = time.Duration(query.Spec.PlanFacts.StepMillis) * time.Millisecond
	if plan, planned := engine.plans[qg]; planned && now.Before(plan.ExpiresAt) {
		engine.seedLocked(state, plan, now)
	}
	return state
}

// refuseEarlier counts a Slot whose trial the controller gave no candidate
// for: no_candidate, and EarlierRefused told of it, when Prepare would have
// reserved one for it - the group's rest over at the Slot's readiness, its
// sample not in flight and no trial reserved ahead for a later Slot -
// otherwise by what of those stood first. A trial still waiting for an
// earlier Slot's formal read stands in nothing's way: Prepare drops it as
// the next Slot arrives. Once a Slot, however often its readiness is
// retried. Nothing else of the group's state moves: a reservation standing
// for an earlier Slot stays as it was.
func (engine *Engine) refuseEarlier(query Query, now time.Time) {
	defer engine.publishProbeEnds()
	engine.mu.Lock()
	state := engine.groupLocked(query, now)
	// A probe ends when its floor is no longer the one to probe.
	engine.endProbeLocked(state, FloorProbeHoldMoved, query.Contract)
	reason := EarlierSkipNoCandidate
	switch previous := state.prepared; {
	case state.capturing || state.sample != nil:
		reason = EarlierSkipSampleInFlight
	case previous != nil && previous.ahead && previous.query.Contract.Slot.EvaluationTime > query.Contract.Slot.EvaluationTime:
		reason = EarlierSkipTrialReserved
	case query.ReadyAt.Before(state.nextAt):
		reason = EarlierSkipResting
	}
	told := engine.skipLocked(state, query, reason) && reason == EarlierSkipNoCandidate && engine.options.EarlierRefused != nil
	engine.mu.Unlock()
	if told {
		engine.options.EarlierRefused(query.Contract.Slot.QueryGroup)
	}
}

// Prepare sees a normal, non-retry query before readiness waits. It reserves
// exactly the sample Begin would take, once per Slot, so the h/2 read uses
// existing sampling rather than an independent query schedule. Begin then
// continues that reservation even when readiness has been retried.
func (engine *Engine) Prepare(query Query) {
	if engine == nil || engine.options.OnEarlierRead == nil || query.Operation != execution.OperationNormal || query.Secondary ||
		query.AttemptNo != 1 || query.Spec.PlanFacts.StepMillis <= 0 || query.Contract.ReadHoldMillis <= 0 || query.ReadyAt.IsZero() {
		return
	}
	if !engine.options.Owns(query.Contract.Slot.QueryGroup) {
		engine.mu.Lock()
		engine.counts.earlierSkipped[key2(sourceOf(query.Spec.PlanFacts), EarlierSkipNotOwned)]++
		engine.mu.Unlock()
		return
	}
	now := engine.options.Now()
	hold := time.Duration(query.Contract.ReadHoldMillis) * time.Millisecond
	base := hold
	if engine.options.CurrentReadHold != nil {
		base = engine.options.CurrentReadHold(query.Contract.Slot.QueryGroup)
	}
	if base <= 0 {
		engine.skip(query, now, EarlierSkipNoHold)
		return
	}
	if query.BackendFailing {
		engine.skip(query, now, EarlierSkipBackendFailing)
		return
	}
	// The candidate is the controller's when it has one to give, asked
	// outside the engine's lock as the hold is; none to give, no trial.
	given, hasGiven := time.Duration(0), engine.options.CurrentCandidate != nil
	if hasGiven {
		var tries bool
		if given, tries = engine.options.CurrentCandidate(query.Contract.Slot.QueryGroup); !tries {
			// None to try: the search below the hold has run out. Its floor,
			// once it has stood long enough, is probed round by round instead.
			if floor, kind, edge, probing := engine.floorProbe(query.Contract.Slot.QueryGroup); probing {
				engine.prepareProbe(query, now, hold, base, floor, kind, edge, engine.probeResume(query.Contract.Slot.QueryGroup, kind, floor))
				return
			}
			engine.refuseEarlier(query, now)
			return
		}
	}
	defer engine.publishProbeEnds()
	engine.mu.Lock()
	state := engine.groupLocked(query, now)
	// A candidate to try: the search below the hold is open again, and a
	// probe of the floor it had run out at ends.
	engine.endProbeLocked(state, FloorProbeHoldMoved, query.Contract)
	if previous := state.prepared; previous != nil {
		switch {
		case previous.query.Contract.Slot == query.Contract.Slot && previous.ahead:
			// The Slot a trial was reserved for ahead, at its own first
			// attempt: the trial was read for this frozen query only if the
			// Slot's window, hold and readiness are those it was moved to. A
			// Slot frozen otherwise - its hold moved, its schedule or query
			// revised - drops it and goes on to reserve as any Slot does, so a
			// moved hold costs one trial, not the next one too.
			previous.ahead = false
			if query.Spec.Digest == previous.query.Spec.Digest && query.Contract.ReadHoldMillis == previous.query.Contract.ReadHoldMillis &&
				query.ReadyAt.Equal(previous.query.ReadyAt) {
				previous.query = query
				state.decided = max(state.decided, query.Contract.Slot.EvaluationTime)
				engine.mu.Unlock()
				return
			}
			// Dropped below, once, as hold_moved unless something overtook
			// it first.
			if previous.overtakenBy == "" {
				previous.overtakenBy = OvertakenHoldMoved
				frozen := time.Duration(query.Contract.ReadHoldMillis) * time.Millisecond
				previous.frozenHold = &frozen
			}
		case previous.query.Contract.Slot == query.Contract.Slot:
			// The same Slot prepared again, its readiness deferred and
			// retried: the reservation stands.
			engine.mu.Unlock()
			return
		case previous.ahead && previous.query.Contract.Slot.EvaluationTime > query.Contract.Slot.EvaluationTime:
			// A Slot before the one a trial is reserved ahead for - the Slot
			// that reserved it, retried, which its first Prepare decided, or
			// one between: the reservation stands.
			engine.skipLocked(state, query, EarlierSkipTrialReserved)
			engine.mu.Unlock()
			return
		}
		// A frozen Slot that never reached a first read supplies no evidence.
		if !previous.formal {
			// A trial that finished still waits for its Slot's formal read;
			// the next Slot's arrival overtakes it whatever its own outcome.
			engine.dropTrialLocked(previous, OvertakenNextSlot)
		}
		state.prepared = nil
	}
	if state.capturing || state.sample != nil {
		engine.skipLocked(state, query, EarlierSkipSampleInFlight)
		engine.mu.Unlock()
		return
	}
	candidate := given
	if !hasGiven {
		candidate = execution.LoweredReadHold(base, state.step)
	}
	baseline := query.ReadyAt.Add(-hold)
	readyAt := baseline.Add(candidate)
	start := trialStart(readyAt, baseline, candidate, state.rereadBound())
	// This Slot's first attempt is past where its own trial had to start, as
	// every one is under a hold past the period less the reread bound: the
	// next Slot's trial is reserved now instead.
	if now.After(start) {
		if ahead := engine.aheadTrialLocked(state, query, hold, base, candidate, now); ahead != nil {
			state.prepared, state.decided = ahead, max(state.decided, query.Contract.Slot.EvaluationTime)
			engine.counts.earlierAhead[key2(state.source, aheadSlotsLabel(ahead.aheadSlots))]++
			state.reading.ahead[wordIndex(EarlierAheadSlots, aheadSlotsLabel(ahead.aheadSlots))]++
			heap.Push(&engine.earlyWork, earlyWork{at: ahead.at, trial: ahead})
			engine.mu.Unlock()
			engine.poke()
			return
		}
	}
	// The group's rest is judged at the Slot's formal read, where Begin
	// takes the sample this reserves, not now: a held Slot is prepared at
	// its first attempt, a hold before that read. Judged now, a rest ending
	// between the two left the first attempt to skip and the readiness retry
	// to reserve the trial past its own candidate point, where it is
	// overtaken before it reads - almost every trial, on a hold near the
	// period.
	if query.ReadyAt.Before(state.nextAt) {
		engine.skipLocked(state, query, EarlierSkipResting)
		engine.mu.Unlock()
		return
	}
	state.decided = max(state.decided, query.Contract.Slot.EvaluationTime)
	state.prepared = &earlierSample{query: query, source: state.source, candidateHold: candidate,
		group: state,
		// Start within the existing recheck budget before the candidate
		// readiness point. Data already whole here proves it whole at h/2;
		// starting after that point cannot prove a decrease.
		at: start, readyAt: readyAt, keptFrom: tailFrom(query.Spec.LogicalWindow, state.step, tailSteps)}
	if late := now.Sub(readyAt); now.UnixMilli() > readyAt.UnixMilli() {
		state.prepared.overtake(OvertakenPreparedLate)
		engine.counts.earlierPreparedLateMax[state.source] = max(engine.counts.earlierPreparedLateMax[state.source], late)
	}
	heap.Push(&engine.earlyWork, earlyWork{at: start, trial: state.prepared})
	engine.mu.Unlock()
	engine.poke()
}

func (read *Read) completeEarlier(completion execution.ProviderCompletion, err error) {
	trial := read.earlier
	if trial == nil {
		return
	}
	engine := read.engine
	owned := engine.options.Owns(trial.query.Contract.Slot.QueryGroup)
	candidateCurrent := true
	switch {
	case engine.options.CurrentCandidate != nil:
		// The trial's candidate is still the one to try: the hold and the
		// search below it have not moved under it.
		given, tries := engine.options.CurrentCandidate(trial.query.Contract.Slot.QueryGroup)
		candidateCurrent = tries && given == trial.candidateHold
	case engine.options.CurrentReadHold != nil:
		hold := engine.options.CurrentReadHold(trial.query.Contract.Slot.QueryGroup)
		candidateCurrent = hold > 0 && execution.LoweredReadHold(hold, trial.group.step) == trial.candidateHold
	}
	engine.mu.Lock()
	outcome := trial.outcome
	switch {
	case !owned:
		outcome = EarlierOwnerLost
	case err != nil || completion.Completeness != execution.CompletenessFull || read.summary == nil || read.summary.faulted:
		outcome = EarlierFirstIncomplete
	case outcome == "" && !candidateCurrent:
		outcome = EarlierOvertaken
		trial.overtakenBy = OvertakenHoldMoved
	case outcome == "":
		outcome = EarlierEqual
		if len(compareSummaries(trial.summary, trimSummary(read.summary.buckets, trial.keptFrom))) > 0 {
			outcome = EarlierDifferent
		}
	}
	trial.summary = nil
	if state := engine.groups[trial.query.Contract.Slot.QueryGroup]; state != nil && state.prepared == trial {
		state.prepared = nil
	}
	engine.counts.earlierReads[key2(trial.source, outcome)]++
	if outcome == EarlierOvertaken {
		engine.countOvertakenLocked(trial)
	}
	trial.group.reading.earlier[wordIndex(EarlierReadOutcomes, outcome)]++
	engine.endTrialLocked(trial, outcome)
	engine.mu.Unlock()
	defer engine.publishTrialEnds()
	engine.options.OnEarlierRead(EarlierReadEvidence{Contract: trial.query.Contract, CandidateHold: trial.candidateHold,
		Observed: outcome == EarlierEqual || outcome == EarlierDifferent, Equal: outcome == EarlierEqual, Outcome: outcome})
}

func (engine *Engine) publishReadHold() {
	engine.publishTrialEnds()
	engine.publishProbeEnds()
	engine.mu.Lock()
	lines := engine.readEarlyLines
	engine.readEarlyLines = nil
	engine.mu.Unlock()
	if engine.options.OnWindowReadEarly != nil {
		for _, line := range lines {
			if engine.options.Owns(line.Contract.Slot.QueryGroup) {
				engine.options.OnWindowReadEarly(line)
			}
		}
	}
	if engine.options.OnWholeWindowReadEarly == nil && engine.options.OnReadHoldIgnored == nil {
		return
	}
	engine.mu.Lock()
	evidence := engine.holdEvidence
	engine.holdEvidence = nil
	ignored := engine.ignoredEvidence
	engine.ignoredEvidence = nil
	engine.mu.Unlock()
	for _, item := range evidence {
		if engine.options.Owns(item.Contract.Slot.QueryGroup) {
			engine.options.OnWholeWindowReadEarly(item)
		}
	}
	for _, item := range ignored {
		if engine.options.Owns(item.evidence.Contract.Slot.QueryGroup) {
			engine.options.OnReadHoldIgnored(item.evidence, item.reason)
		}
	}
}

// earlyWork is shared by directed early reads and h/2 sample reads. Both
// use the engine loop's existing wakeup, minimum heap and lookback permits.
type earlyWork struct {
	at    time.Time
	slot  *directedSlot
	trial *earlierSample
}
type earlyHeap []earlyWork

func (queue earlyHeap) Len() int { return len(queue) }
func (queue earlyHeap) Less(i, j int) bool {
	if !queue[i].at.Equal(queue[j].at) {
		return queue[i].at.Before(queue[j].at)
	}
	deadline := func(work earlyWork) time.Time {
		if work.slot != nil {
			return work.slot.early.latest
		}
		return work.trial.readyAt
	}
	return deadline(queue[i]).Before(deadline(queue[j]))
}
func (queue earlyHeap) Swap(i, j int)   { queue[i], queue[j] = queue[j], queue[i] }
func (queue *earlyHeap) Push(value any) { *queue = append(*queue, value.(earlyWork)) }
func (queue *earlyHeap) Pop() any {
	last := len(*queue) - 1
	value := (*queue)[last]
	(*queue)[last] = earlyWork{}
	*queue = (*queue)[:last]
	return value
}

func (engine *Engine) earlyQueueLocked(now time.Time, free int) time.Time {
	directed, wake := engine.dueEarlyLocked(now, free)
	for _, slot := range directed {
		if !slot.early.queued {
			slot.early.queued = true
			heap.Push(&engine.earlyWork, earlyWork{at: now, slot: slot})
		}
	}
	for engine.earlyWork.Len() > 0 {
		work := engine.earlyWork[0]
		if work.trial != nil && (work.trial.done || work.trial.formal) || work.slot != nil && (work.slot.dropped || work.slot.early.done) {
			heap.Pop(&engine.earlyWork)
			continue
		}
		if wake.IsZero() || work.at.Before(wake) {
			wake = work.at
		}
		break
	}
	return wake
}

// earlierReadOf is what a trial's or probe round's read reads, copied under
// the engine's lock when the read starts: the read runs without the lock, and
// the round's query is the Slot's frozen one once the Slot's first attempt
// takes a round reserved ahead (prepareProbe, Prepare), written under the lock
// while the read may be running.
type earlierReadOf struct {
	queryGroup execution.QueryGroupIdentity
	spec       execution.PhysicalQuerySpec
	readyAt    time.Time
	keptFrom   int64
}

func (trial *earlierSample) readOfLocked() earlierReadOf {
	return earlierReadOf{queryGroup: trial.query.Contract.Slot.QueryGroup, spec: trial.query.Spec, readyAt: trial.readyAt,
		keptFrom: trial.keptFrom}
}

func (engine *Engine) earlierRead(ctx context.Context, trial *earlierSample, read earlierReadOf, release func(), yield <-chan struct{}) {
	// As long as a read of the group's query takes, never past the candidate
	// readiness point: the trial can never delay the formal read.
	budget := min(engine.rereadTimeout(read.queryGroup), max(read.readyAt.Sub(engine.options.Now()), 0))
	readCtx, cancel := context.WithTimeout(ctx, budget)
	engine.mu.Lock()
	trial.cancel = cancel
	if engine.options.Now().UnixMilli() > trial.readyAt.UnixMilli() && !trial.formal {
		trial.overtake(OvertakenStartedLate)
	}
	skipped := trial.formal || trial.done
	if skipped {
		cancel()
	}
	engine.mu.Unlock()
	back := make(chan struct{})
	if yield != nil {
		go func() {
			select {
			case <-yield:
				cancel()
			case <-back:
			}
		}()
	}
	sink := &earlierSink{summary: newSummarizer(read.spec.PlanFacts.Normalization.CanonicalValueField),
		admit: engine.options.Memory, keptFrom: read.keptFrom}
	// Even an empty summary must have passed the memory line.
	sink.reserve(64)
	lookback, known := queryLookback(read.spec.PlanFacts)
	if !known {
		lookback = time.Duration(read.spec.PlanFacts.StepMillis) * time.Millisecond
	}
	from := max(read.spec.LogicalWindow.Start, read.keptFrom-int64(lookback/time.Second))
	var completion execution.ProviderCompletion
	var err error
	if !sink.refused && !skipped {
		completion, err = engine.options.Recheck(readCtx, tailSpec(read.spec, from), sink)
	}
	finished := engine.options.Now()
	close(back)
	release()
	cancel()
	engine.mu.Lock()
	engine.counts.earlierBytes[trial.source] += sink.bytes
	trial.group.reading.earlierBytes += sink.bytes
	trial.running, trial.cancel = false, nil
	if !trial.done && !trial.formal {
		trial.done = true
		switch {
		case finished.After(trial.readyAt):
			trial.outcome, trial.overtakenBy = EarlierOvertaken, OvertakenFinishedLate
		case sink.refused:
			trial.outcome = EarlierMemoryRefused
		case err != nil || completion.Completeness != execution.CompletenessFull || sink.summary.faulted || closed(yield):
			trial.outcome = EarlierReadFailed
		default:
			trial.summary = sink.summary.buckets
		}
	}
	engine.mu.Unlock()
	engine.poke()
}

// earlierSink keeps only the compared tail, admitted before it grows. The
// summary remains live until the formal first read has been compared.
type earlierSink struct {
	summary  *summarizer
	admit    func(uint64) bool
	keptFrom int64
	granted  int
	refused  bool
	bytes    uint64
}

func (sink *earlierSink) reserve(want int) {
	if sink.refused || want <= sink.granted {
		return
	}
	capacity := max(sink.granted, 64)
	for capacity < want {
		capacity *= 2
	}
	if sink.admit != nil && !sink.admit(uint64(capacity-sink.granted)*summaryEntryBytes) {
		sink.refused, sink.summary.buckets = true, nil
		return
	}
	sink.granted = capacity
}

func (sink *earlierSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.bytes += batch.Delivery.Bytes
	if sink.refused || sink.summary.faulted || batch.Dataset == nil {
		return nil
	}
	// The provider may read a lookback before the compared tail. Summing
	// one view at a time avoids retaining or allocating another dataset.
	for index := 0; index < batch.Dataset.Len(); index++ {
		record, _ := batch.Dataset.Record(index)
		if record.SourceTime() < sink.keptFrom {
			continue
		}
		if _, known := sink.summary.buckets[record.SourceTime()]; !known {
			sink.reserve(len(sink.summary.buckets) + 1)
			if sink.refused {
				return nil
			}
		}
		series := hashString(record.DimensionIdentityDigest())
		sink.summary.addRecord(record, series, mix(series))
		if sink.summary.faulted {
			return nil
		}
	}
	return nil
}
