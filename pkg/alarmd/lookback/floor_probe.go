// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"container/heap"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A shadow probe of a Query Group's floor: once its search below the hold has
// run out at a floor that has stood long enough (readhold.Controller.
// FloorProbe), each of its rounds is read at the floor as well as at the
// hold. The formal read stays at the hold, and the round is decided on it;
// the read at the floor is compared with it, whole, as a trial is
// (completeEarlier). ProbationRounds rounds in a row that read the same -
// EdgeProbeSpan of them, if more, near a floor a probe has failed at - lower the
// hold to the floor, and a probation follows as it follows every lowering:
// a probe that passed by chance would otherwise be read early until a sample
// saw it. One that reads differently ends the probe, and the floor stands
// longer before the next. No round is decided on data read early to find
// out.
//
// A probe read rides no sample: each round has its own, reserved at the
// Slot's first attempt (Prepare) - for a hold past the period less the reread
// bound, at the first attempt of a Slot before it, as a trial is reserved
// ahead (aheadProbeLocked) - so a group has as many in flight as Slots a hold
// spans, and one more.

// How a probe round came out, closed: clean read the same at the floor as the
// formal read did at the hold; different did not; the rest are no evidence
// and neither advance nor end the probe - skipped_for_permits was never read,
// no permit being free; read_failed was read and did not come back whole;
// unclassified could not be compared, the formal read not whole or a series
// table refused; overtaken was not read in time, or its Slot froze otherwise
// than it was reserved for.
const (
	FloorProbeClean             = "clean"
	FloorProbeDifferent         = "different"
	FloorProbeSkippedForPermits = "skipped_for_permits"
	FloorProbeReadFailed        = "read_failed"
	FloorProbeUnclassified      = "unclassified"
	FloorProbeOvertaken         = "overtaken"
)

// FloorProbeRoundOutcomes is every probe round outcome.
var FloorProbeRoundOutcomes = []string{FloorProbeClean, FloorProbeDifferent, FloorProbeSkippedForPermits,
	FloorProbeReadFailed, FloorProbeUnclassified, FloorProbeOvertaken}

// What happened to a probe, closed: started; passed, ProbationRounds rounds
// in a row clean; failed, a round different; hold_moved, the hold, the floor
// or the search under them moved, which ends it unfinished; owner_lost, the
// group left this process. A probe in flight when its process stops ends
// with none of them: started rising while none of the ends does is probes
// that do not live long enough to finish.
// resumed is a start that goes on from the clean rounds the probe's record
// kept (FloorProbeResume), counted with started.
const (
	FloorProbeStarted   = "started"
	FloorProbeResumed   = "resumed"
	FloorProbePassed    = "passed"
	FloorProbeFailed    = "failed"
	FloorProbeHoldMoved = "hold_moved"
	FloorProbeOwnerLost = "owner_lost"
)

// FloorProbeEvents is every probe event.
var FloorProbeEvents = []string{FloorProbeStarted, FloorProbeResumed, FloorProbePassed, FloorProbeFailed, FloorProbeHoldMoved, FloorProbeOwnerLost}

// FloorProbeProgressEvery is how many clean rounds of a probe go between the
// writes that keep them (readhold.ProbeProgressEvery).
const FloorProbeProgressEvery = 10

// FloorProbeProgress is a running probe's clean rounds in a row, every
// FloorProbeProgressEvery of them, with what it reads and the Slot it started
// at; Contract is the Slot of its latest clean round, Period the distance
// between its rounds.
type FloorProbeProgress struct {
	Contract execution.FrozenExecutionContractRef
	Source   string
	Kind     string
	Floor    time.Duration
	Hold     time.Duration
	Clean    int
	Started  execution.EvaluationTime
	Period   time.Duration
}

// FloorProbeStart is a probe's start: what it reads, the clean rounds it
// needs, the Slot it started at, and the clean rounds it goes on from, kept
// by the process that ran it before (ResumedFrom; none for a fresh one).
type FloorProbeStart struct {
	Contract    execution.FrozenExecutionContractRef
	Source      string
	Kind        string
	Floor       time.Duration
	Hold        time.Duration
	Rounds      int
	Started     execution.EvaluationTime
	ResumedFrom int
}

// FloorProbeKinds is every kind of probe the controller gives
// (readhold.FloorProbeKinds): verify, the candidate a group's trials chose,
// read before its hold moves; midpoint, halfway between a floor and the
// hold; floor, the floor itself.
var FloorProbeKinds = []string{"verify", "midpoint", "floor"}

// floorProbeState is a Query Group's probe: the floor its rounds are read at,
// the hold their formal reads are frozen with, its clean rounds in a row, the
// Slot it started at and the latest whose first attempt it saw, and its
// reservations by Slot.
type floorProbeState struct {
	floor time.Duration
	kind  string
	hold  time.Duration
	// edge: the floor is near one a probe has failed at, and the probe reads
	// EdgeProbeSpan of its rounds clean, period apart, if that is more than
	// ProbationRounds.
	edge   bool
	period time.Duration
	clean  int
	// resumedFrom is the clean rounds it went on from, kept by the process
	// that ran it before; none for a fresh one.
	resumedFrom int
	started     execution.EvaluationTime
	prepared    execution.EvaluationTime
	reserved    map[execution.EvaluationTime]*earlierSample
}

// FloorProbeEnd is how a probe ended (FloorProbeEvents but started): the Slot
// of the round that ended it - only the Query Group for one that left the
// process - the hold its rounds were read at (Floor: the floor or a midpoint
// above it) and the hold of their formal reads, its clean rounds and the Slot
// it started at. A probe that failed has, in Changes, how the formal read of
// its last round differed from the read at Floor, its buckets counted by
// change class (Changes): points_added is points the formal read had that
// were not yet there for the probe - a series or a point not yet visible.
type FloorProbeEnd struct {
	Contract execution.FrozenExecutionContractRef
	Source   string
	Event    string
	Floor    time.Duration
	// Kind is what the probe read (FloorProbeKinds).
	Kind  string
	Hold  time.Duration
	Clean int
	// Rounds is the clean rounds it needed to pass: ProbationRounds, or near a
	// floor a probe failed at EdgeProbeSpan of rounds if that is more.
	Rounds      int
	Started     execution.EvaluationTime
	ResumedFrom int
	Changes     map[string]int
}

// GroupFloorProbe is a Query Group's probe now, for its group reading.
type GroupFloorProbe struct {
	FloorMillis int64                    `json:"floor_ms"`
	Kind        string                   `json:"kind"`
	HoldMillis  int64                    `json:"hold_ms"`
	Clean       int                      `json:"clean_rounds"`
	Rounds      int                      `json:"rounds_required"`
	Started     execution.EvaluationTime `json:"started_slot"`
	ResumedFrom int                      `json:"resumed_from"`
	Reserved    int                      `json:"reserved"`
}

// EdgeProbeSpan is how long a probe near a floor a probe has failed at reads
// clean before it passes (readhold's EarlierFloorProbed and EdgeProbeReach):
// its rounds over that long, ProbationRounds at least. A group lowered on a
// pass that came by chance reads some rounds early, and only its sample sees
// one - a round a rest, an hour at most - so about as many early rounds as
// the rest holds rounds before it does, whatever the rate: sixty at a
// minute's period, twelve at five minutes, three at twenty. The evidence it
// needs scales with time for that reason, not by a fixed multiple of the
// rounds. Read against one deployment's probes 5 s above such floors, ten of
// which failed only after 5 to 51 clean rounds: at a minute's period, eight
// hours is 480 rounds, and a group reading a round in a hundred early passes
// one probe in a hundred and twenty rather than three in ten; from four
// minutes on it is no more than ProbationRounds, whose early rounds after a
// chance pass are a few already.
const EdgeProbeSpan = 8 * time.Hour

// floorProbe asks the controller, outside the engine's lock, for the hold
// the group's probe reads at, its kind and whether it is near a failed floor;
// false with no controller to ask.
func (engine *Engine) floorProbe(queryGroup execution.QueryGroupIdentity) (time.Duration, string, bool, bool) {
	if engine.options.FloorProbe == nil {
		return 0, "", false, false
	}
	return engine.options.FloorProbe(queryGroup)
}

// probeResume is how far a probe of the group at floor, of kind, had read
// before, asked outside the engine's lock; nothing with no controller to ask.
func (engine *Engine) probeResume(queryGroup execution.QueryGroupIdentity, kind string, floor time.Duration) probeResume {
	if engine.options.FloorProbeResume == nil {
		return probeResume{}
	}
	clean, started, ok := engine.options.FloorProbeResume(queryGroup, kind, floor)
	if !ok || clean <= 0 || started <= 0 {
		return probeResume{}
	}
	return probeResume{clean: clean, started: started}
}

// probeResume is the clean rounds a probe goes on from and the Slot it
// started at; none for a fresh probe.
type probeResume struct {
	clean   int
	started execution.EvaluationTime
}

// probeRounds is how many clean rounds in a row pass a probe.
func (engine *Engine) probeRounds(probe *floorProbeState) int {
	rounds := engine.probationRounds()
	if probe.edge && probe.period > 0 {
		rounds = max(rounds, int((EdgeProbeSpan+probe.period-1)/probe.period))
	}
	return rounds
}

// prepareProbe is Prepare for a Slot of a group whose probe reads at floor:
// the Slot is counted floor_probe, not no_candidate, and once, at its first
// attempt, it takes the reservation made for it ahead and reserves the read
// of a round - its own while its point is still ahead, otherwise the first
// Slot on whose point is.
func (engine *Engine) prepareProbe(query Query, now time.Time, hold, base, floor time.Duration, kind string, edge bool, resume probeResume) {
	engine.mu.Lock()
	state := engine.groupLocked(query, now)
	slot := query.Contract.Slot.EvaluationTime
	probe := state.floorProbe
	if probe != nil && (probe.floor != floor || probe.kind != kind || probe.hold != base || probe.edge != edge) {
		engine.endProbeLocked(state, FloorProbeHoldMoved, query.Contract)
		probe = nil
	}
	if probe == nil {
		// Its rounds are its Slots, a period apart: the next Slot's distance, or
		// the group's step where no next Slot is known.
		period := state.step
		if query.FollowingSlot > query.Contract.Slot.EvaluationTime {
			period = time.Duration(query.FollowingSlot-query.Contract.Slot.EvaluationTime) * time.Second
		}
		probe = &floorProbeState{floor: floor, kind: kind, hold: base, edge: edge, period: period, started: slot,
			reserved: map[execution.EvaluationTime]*earlierSample{}}
		// The process that ran it before kept its clean rounds: it goes on
		// from them, from the Slot it started at there.
		if resume.clean > 0 {
			probe.clean, probe.resumedFrom, probe.started = resume.clean, resume.clean, resume.started
			engine.counts.floorProbes[key3(state.source, kind, FloorProbeResumed)]++
		}
		state.floorProbe = probe
		engine.counts.floorProbes[key3(state.source, kind, FloorProbeStarted)]++
		engine.probeStarts = append(engine.probeStarts, FloorProbeStart{Contract: query.Contract, Source: state.source, Kind: kind,
			Floor: floor, Hold: base, Rounds: engine.probeRounds(probe), Started: probe.started, ResumedFrom: probe.resumedFrom})
	}
	engine.skipLocked(state, query, EarlierSkipFloorProbe)
	if slot <= probe.prepared {
		// A readiness retry, or a Slot replayed out of order: its first
		// attempt decided it.
		engine.mu.Unlock()
		return
	}
	probe.prepared = slot
	// A Slot before this one whose round was never read formally - not
	// prepared again, its read not begun - will not be.
	for at, earlier := range probe.reserved {
		if at < slot && !earlier.formal {
			engine.dropProbeRoundLocked(probe, earlier, OvertakenNextSlot)
		}
	}
	own := probe.reserved[slot]
	if own != nil && own.ahead {
		// Reserved ahead for this Slot: read for its frozen query only when
		// the Slot's window, hold and readiness are those it was moved to.
		own.ahead = false
		if query.Spec.Digest == own.query.Spec.Digest && query.Contract.ReadHoldMillis == own.query.Contract.ReadHoldMillis &&
			query.ReadyAt.Equal(own.query.ReadyAt) {
			own.query = query
		} else {
			engine.dropProbeRoundLocked(probe, own, OvertakenHoldMoved)
			own = nil
		}
	}
	bound := state.rereadBound()
	baseline := query.ReadyAt.Add(-hold)
	readyAt := baseline.Add(floor)
	start := trialStart(readyAt, baseline, floor, bound)
	var reserved *earlierSample
	if own == nil && now.Before(start) {
		reserved = &earlierSample{query: query, source: state.source, candidateHold: floor, group: state, probe: probe,
			at: start, readyAt: readyAt, keptFrom: tailFrom(query.Spec.LogicalWindow, state.step, tailSteps)}
		probe.reserved[slot] = reserved
	} else {
		reserved = engine.aheadProbeLocked(state, probe, query, hold, base, floor, now)
	}
	if reserved != nil {
		heap.Push(&engine.earlyWork, earlyWork{at: reserved.at, trial: reserved})
	}
	engine.mu.Unlock()
	engine.publishProbeEnds()
	if reserved != nil {
		engine.poke()
	}
}

// aheadProbeLocked reserves the round of the first Slot on, k Slots ahead,
// whose read at the floor starts after now and that has none reserved yet,
// as aheadTrialLocked moves a trial; nil when there is none within the
// hold's reach. A group's reservations are no more than the Slots its hold
// and reread bound span, and one more: the Slots between a reservation and
// the first attempt that made it each made their own.
func (engine *Engine) aheadProbeLocked(state *group, probe *floorProbeState, query Query, hold, base, floor time.Duration, now time.Time) *earlierSample {
	step := int64(query.FollowingSlot - query.Contract.Slot.EvaluationTime)
	if query.FollowingSlot == 0 || step <= 0 {
		return nil
	}
	bound := state.rereadBound()
	limit := int((hold+bound)/(time.Duration(step)*time.Second)) + 2
	if len(probe.reserved) > limit {
		return nil
	}
	for slots := 1; slots <= limit; slots++ {
		shift := step * int64(slots)
		at := query.Contract.Slot.EvaluationTime + execution.EvaluationTime(shift)
		if probe.reserved[at] != nil {
			continue
		}
		baseline := query.ReadyAt.Add(-hold).Add(time.Duration(shift) * time.Second)
		readyAt := baseline.Add(floor)
		start := trialStart(readyAt, baseline, floor, bound)
		if !now.Before(start) {
			continue
		}
		spec, derived := shiftedSpec(query.Spec, shift)
		if !derived {
			return nil
		}
		next := query
		next.Spec, next.FollowingSlot = spec, 0
		next.Contract.Slot.EvaluationTime = at
		next.Contract.ReadHoldMillis = base.Milliseconds()
		next.ReadyAt = baseline.Add(base)
		reserved := &earlierSample{query: next, source: state.source, candidateHold: floor, group: state, probe: probe,
			ahead: true, aheadSlots: slots, at: start, readyAt: readyAt, keptFrom: tailFrom(next.Spec.LogicalWindow, state.step, tailSteps)}
		probe.reserved[at] = reserved
		return reserved
	}
	return nil
}

// dropProbeRoundLocked ends a reservation its Slot will not read formally, a
// round counted overtaken.
func (engine *Engine) dropProbeRoundLocked(probe *floorProbeState, round *earlierSample, path string) {
	round.overtake(path)
	if round.cancel != nil {
		round.cancel()
	}
	round.summary = nil
	delete(probe.reserved, round.query.Contract.Slot.EvaluationTime)
	engine.countProbeRoundLocked(round.group, round.source, FloorProbeOvertaken)
}

func (engine *Engine) countProbeRoundLocked(state *group, source, outcome string) {
	engine.counts.floorProbeRounds[key2(source, outcome)]++
	state.reading.probeRounds[wordIndex(FloorProbeRoundOutcomes, outcome)]++
}

// endProbeLocked ends the group's probe, if it has one, by event, at the Slot
// of contract: its reservations not yet read formally are dropped uncounted,
// the probe that made them being over.
func (engine *Engine) endProbeLocked(state *group, event string, contract execution.FrozenExecutionContractRef) {
	probe := state.floorProbe
	if probe == nil {
		return
	}
	state.floorProbe = nil
	for _, round := range probe.reserved {
		if !round.formal {
			round.overtake(OvertakenHoldMoved)
			if round.cancel != nil {
				round.cancel()
			}
			round.summary = nil
		}
	}
	engine.counts.floorProbes[key3(state.source, probe.kind, event)]++
	engine.probeEnds = append(engine.probeEnds, FloorProbeEnd{Contract: contract, Source: state.source, Event: event,
		Floor: probe.floor, Kind: probe.kind, Hold: probe.hold, Clean: probe.clean, Rounds: engine.probeRounds(probe), Started: probe.started,
		ResumedFrom: probe.resumedFrom})
}

// publishProbeEnds hands the kept probe starts, progress and ends to
// OnFloorProbeStart, OnFloorProbeProgress and OnFloorProbe, in that order,
// outside the engine's lock.
func (engine *Engine) publishProbeEnds() {
	engine.mu.Lock()
	starts, progress, ends := engine.probeStarts, engine.probeProgress, engine.probeEnds
	engine.probeStarts, engine.probeProgress, engine.probeEnds = nil, nil, nil
	engine.mu.Unlock()
	if engine.options.OnFloorProbeStart != nil {
		for _, start := range starts {
			engine.options.OnFloorProbeStart(start)
		}
	}
	if engine.options.OnFloorProbeProgress != nil {
		for _, kept := range progress {
			engine.options.OnFloorProbeProgress(kept)
		}
	}
	if engine.options.OnFloorProbe != nil {
		for _, end := range ends {
			engine.options.OnFloorProbe(end)
		}
	}
}

// attachProbeLocked has the formal first read of a Slot take the probe round
// reserved for it, which it is then compared with (completeFloorProbe): a
// round whose read at the floor has not finished by now is overtaken, the
// read at the hold having begun.
func (engine *Engine) attachProbeLocked(state *group, read *Read, query Query) {
	probe := state.floorProbe
	if probe == nil {
		return
	}
	round := probe.reserved[query.Contract.Slot.EvaluationTime]
	if round == nil || round.ahead || round.formal || round.query.Spec.Digest != query.Spec.Digest {
		return
	}
	read.floorProbe, round.formal = round, true
	if !round.done {
		round.overtake(OvertakenFormalFirst)
		if round.cancel != nil {
			round.cancel()
		}
	}
}

// completeFloorProbe compares the probe round a formal first read took with
// what that read found, and counts it. ProbationRounds clean rounds in a row
// pass the probe, one different fails it; a round of another hold, or of a
// probe that has ended, counts and moves nothing.
func (read *Read) completeFloorProbe(completion execution.ProviderCompletion, err error) {
	round := read.floorProbe
	if round == nil {
		return
	}
	engine := read.engine
	qg := round.query.Contract.Slot.QueryGroup
	owned := engine.options.Owns(qg)
	engine.mu.Lock()
	outcome := FloorProbeClean
	var changes map[string]int
	switch {
	case !owned:
		outcome = ""
	case err != nil || completion.Completeness != execution.CompletenessFull || read.summary == nil || read.summary.faulted:
		outcome = FloorProbeUnclassified
	case round.outcome == EarlierPermitRefused:
		outcome = FloorProbeSkippedForPermits
	case round.outcome == EarlierMemoryRefused:
		outcome = FloorProbeUnclassified
	case round.outcome == EarlierReadFailed:
		outcome = FloorProbeReadFailed
	case round.outcome == EarlierOvertaken:
		outcome = FloorProbeOvertaken
	default:
		if changes = compareSummaries(round.summary, trimSummary(read.summary.buckets, round.keptFrom)); len(changes) > 0 {
			outcome = FloorProbeDifferent
		}
	}
	round.summary = nil
	probe := round.probe
	slot := round.query.Contract.Slot.EvaluationTime
	if probe.reserved[slot] == round {
		delete(probe.reserved, slot)
	}
	if outcome != "" {
		engine.countProbeRoundLocked(round.group, round.source, outcome)
	}
	for class, buckets := range changes {
		engine.counts.floorProbeChanges[key2(round.source, class)] += uint64(buckets)
	}
	if state := engine.groups[qg]; state != nil && state.floorProbe == probe && round.query.Contract.ReadHoldMillis == probe.hold.Milliseconds() {
		switch outcome {
		case FloorProbeClean:
			switch probe.clean++; {
			case probe.clean >= engine.probeRounds(probe):
				engine.endProbeLocked(state, FloorProbePassed, round.query.Contract)
			case probe.clean%FloorProbeProgressEvery == 0:
				engine.probeProgress = append(engine.probeProgress, FloorProbeProgress{Contract: round.query.Contract, Source: round.source,
					Kind: probe.kind, Floor: probe.floor, Hold: probe.hold, Clean: probe.clean, Started: probe.started, Period: probe.period})
			}
		case FloorProbeDifferent:
			engine.endProbeLocked(state, FloorProbeFailed, round.query.Contract)
			engine.probeEnds[len(engine.probeEnds)-1].Changes = changes
		}
	}
	engine.mu.Unlock()
	engine.publishProbeEnds()
}

// floorProbeGroupsLocked is, by source, the Query Groups with a probe now.
func (engine *Engine) floorProbeGroupsLocked() map[string]uint64 {
	groups := make(map[string]uint64, len(Sources))
	for _, state := range engine.groups {
		if state.floorProbe != nil {
			groups[state.source]++
		}
	}
	return groups
}
