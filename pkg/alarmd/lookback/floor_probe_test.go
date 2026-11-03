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
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// probeRun drives one Query Group whose search below its hold has run out, Slot by Slot, as production does: a
// Slot's first attempt comes as the Slot before it is read at its hold (the group's Slots run one after another), a
// minute's source delay and the hold after each Slot's evaluation time; its probe round, if any, is read when due
// and answered at once, unless the case holds it back; its formal read comes at its readiness. Every second of the
// run the early reads due are started.
type probeRun struct {
	t     *testing.T
	f     *fixture
	hold  time.Duration
	floor time.Duration

	mu sync.Mutex
	// probing is what FloorProbe answers; candidate whether CurrentCandidate gives one.
	probing   bool
	edge      bool
	kind      string
	candidate bool
	ends      []FloorProbeEnd
	refused   int
	// What the case does to a Slot's round: the value its read at the floor finds ("1" is the formal read's),
	// its permit refused, its read failing, its read held back past the formal read, its formal read not whole.
	value            map[int64]string
	refuse           map[int64]bool
	readFails        map[int64]bool
	late             map[int64]bool
	formalIncomplete map[int64]bool
	// reservedAhead is how many Slots ahead each round was reserved, by Slot; most the reservations alive at once.
	reservedAhead map[int64]int
	most          int
	// What the case does to a Slot's frozen query: frozen at another hold than the group's now, its query revised
	// at its first attempt and read, or revised between the two.
	frozenHold map[int64]time.Duration
	revised    map[int64]bool
	revisedAt  map[int64]bool
}

const probeDelay = 60 // seconds of source delay before the hold

func newProbeRun(t *testing.T, hold, floor time.Duration, rounds int) *probeRun {
	t.Helper()
	r := &probeRun{t: t, f: newFixture(t), hold: hold, floor: floor, probing: true, value: map[int64]string{}, refuse: map[int64]bool{},
		readFails: map[int64]bool{}, late: map[int64]bool{}, formalIncomplete: map[int64]bool{}, reservedAhead: map[int64]int{},
		frozenHold: map[int64]time.Duration{}, revised: map[int64]bool{}, revisedAt: map[int64]bool{}}
	options := &r.f.engine.options
	options.ProbationRounds = rounds
	options.OnEarlierRead = func(EarlierReadEvidence) {}
	options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return hold }
	options.CurrentCandidate = func(execution.QueryGroupIdentity) (time.Duration, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return hold / 2, r.candidate
	}
	options.FloorProbe = func(execution.QueryGroupIdentity) (time.Duration, string, bool, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		kind := r.kind
		if kind == "" {
			kind = "floor"
		}
		return r.floor, kind, r.edge, r.probing
	}
	options.EarlierRefused = func(execution.QueryGroupIdentity) {
		r.mu.Lock()
		r.refused++
		r.mu.Unlock()
	}
	options.OnFloorProbe = func(end FloorProbeEnd) {
		_ = r.f.engine.Stats() // callbacks cannot hold Engine.mu
		r.mu.Lock()
		r.ends = append(r.ends, end)
		r.mu.Unlock()
	}
	return r
}

// slotQuery is the Slot at slot frozen under the run's hold, or as the case froze it.
func (r *probeRun) slotQuery(slot int64) Query {
	q := scheduledQuery(r.t, slot)
	hold := r.hold
	if frozen, moved := r.frozenHold[slot]; moved {
		hold = frozen
	}
	q.Contract.ReadHoldMillis = hold.Milliseconds()
	q.ReadyAt = time.Unix(slot+probeDelay, 0).Add(hold)
	if r.revised[slot] {
		q.Spec.Digest = "revised"
	}
	return q
}

func (r *probeRun) set(change func()) {
	r.mu.Lock()
	change()
	r.mu.Unlock()
}

// run runs the Slots from first, slots of them a minute apart, through the last one's formal read.
func (r *probeRun) run(first int64, slots int) {
	r.t.Helper()
	hold := int64(r.hold / time.Second)
	last := first + int64(slots-1)*60
	prepared, read := first, first
	for now := first + hold; now <= last+probeDelay+hold; now++ {
		r.f.clock.set(time.Unix(now, 0))
		if read < prepared && read+probeDelay+hold == now {
			r.formal(read)
			read += 60
		}
		if prepared <= last && prepared+hold == now {
			r.f.engine.Prepare(r.slotQuery(prepared))
			r.note()
			prepared += 60
		}
		r.step()
	}
}

// note keeps how far ahead each new reservation was made, and the most alive at once.
func (r *probeRun) note() {
	r.f.engine.mu.Lock()
	defer r.f.engine.mu.Unlock()
	state := r.f.engine.groups["qg"]
	if state == nil || state.floorProbe == nil {
		return
	}
	r.most = max(r.most, len(state.floorProbe.reserved))
	for at, round := range state.floorProbe.reserved {
		if _, seen := r.reservedAhead[int64(at)]; !seen {
			r.reservedAhead[int64(at)] = round.aheadSlots
		}
	}
}

// due is the Slots whose rounds are due to be read now and are not read yet.
func (r *probeRun) due() []int64 {
	r.f.engine.mu.Lock()
	defer r.f.engine.mu.Unlock()
	var slots []int64
	if state := r.f.engine.groups["qg"]; state != nil && state.floorProbe != nil {
		for at, round := range state.floorProbe.reserved {
			if !round.done && !round.formal && !round.running && !r.f.clock.now().Before(round.at) {
				slots = append(slots, int64(at))
			}
		}
	}
	return slots
}

// step starts the early reads due, answers each but those held back, and waits for the answered ones to end.
func (r *probeRun) step() {
	r.t.Helper()
	due := r.due()
	refuse := false
	for _, slot := range due {
		refuse = refuse || r.refuse[slot]
	}
	if refuse {
		r.f.set(func() { r.f.refuse = "full" })
	}
	r.f.mu.Lock()
	asked := len(r.f.specs)
	r.f.mu.Unlock()
	r.f.engine.StepEarly(context.Background())
	if refuse {
		r.f.set(func() { r.f.refuse = "" })
		return
	}
	for index := range len(due) {
		var spec execution.PhysicalQuerySpec
		testwait.Until(r.t, "a probe read asking for its window", func() bool {
			r.f.mu.Lock()
			defer r.f.mu.Unlock()
			if len(r.f.specs) <= asked+index {
				return false
			}
			spec = r.f.specs[asked+index]
			return true
		})
		slot := spec.LogicalWindow.End
		switch {
		case r.late[slot]:
		case r.readFails[slot]:
			r.f.answers <- func(execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
				return execution.ProviderCompletion{}, errors.New("backend unavailable")
			}
		case r.value[slot] == "none":
			r.f.answers <- full()
		default:
			value := r.value[slot]
			if value == "" {
				value = "1"
			}
			r.f.answers <- full(point(slot, value))
		}
	}
	testwait.Until(r.t, "the answered probe reads ending", func() bool {
		busy, _ := r.f.engine.Busy("qg")
		return busy.ReadsInFlight <= r.heldBack()
	})
}

// heldBack is the rounds held back past their formal reads that are being read now.
func (r *probeRun) heldBack() int {
	r.f.engine.mu.Lock()
	defer r.f.engine.mu.Unlock()
	held := 0
	if state := r.f.engine.groups["qg"]; state != nil && state.floorProbe != nil {
		for at, round := range state.floorProbe.reserved {
			if round.running && r.late[int64(at)] {
				held++
			}
		}
	}
	return held
}

// formal is the Slot's formal first read at its hold, which finds the data whole: "1".
func (r *probeRun) formal(slot int64) {
	r.t.Helper()
	q := r.slotQuery(slot)
	if r.revisedAt[slot] {
		q.Spec.Digest = "revised"
	}
	read := r.f.engine.Begin(q)
	read.Series(point(slot, "1"), 100)
	completeness := execution.CompletenessFull
	if r.formalIncomplete[slot] {
		completeness = execution.CompletenessPartial
	}
	read.Complete(execution.ProviderCompletion{Completeness: completeness}, nil)
	if r.late[slot] {
		// Its round, overtaken, ends as the formal read cancels it.
		testwait.Until(r.t, "the held-back probe read ending", func() bool {
			busy, _ := r.f.engine.Busy("qg")
			return busy.ReadsInFlight == 0
		})
	}
}

func (r *probeRun) events() map[string]uint64 {
	got := map[string]uint64{}
	for event, count := range r.f.engine.Stats().Sources[sourceTimeSeries].FloorProbes {
		if count > 0 {
			got[event] = count
		}
	}
	return got
}

func (r *probeRun) rounds() map[string]uint64 {
	got := map[string]uint64{}
	for outcome, count := range r.f.engine.Stats().Sources[sourceTimeSeries].FloorProbeRounds {
		if count > 0 {
			got[outcome] = count
		}
	}
	return got
}

func (r *probeRun) endsSeen() []FloorProbeEnd {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FloorProbeEnd(nil), r.ends...)
}

func equalCounts(got, want map[string]uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for key, count := range want {
		if got[key] != count {
			return false
		}
	}
	return true
}

// Every round of a probe is read at the floor and compared with its formal read: rounds' worth of them clean pass
// the probe, ended at the Slot of the last. A hold within the period less the reread bound has each Slot reserve
// its own round; one past it has each reserve the round of the first Slot on whose point is still ahead - one Slot
// ahead for a hold under two periods less the bound, two past it - so a group has that many and one more in flight,
// and from the Slots that reach, every Slot has a round. Each Slot of the probe is counted floor_probe, none
// no_candidate, and the controller hears of no refused trial.
func TestEachRoundOfAProbeIsReadAtTheFloorAndPassesTheProbe(t *testing.T) {
	for _, c := range []struct {
		name  string
		hold  time.Duration
		ahead int
	}{
		{"own Slot", 30 * time.Second, 0},
		{"one Slot ahead", 56 * time.Second, 1},
		{"two Slots ahead", 2 * time.Minute, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			const rounds, slots = 5, 9
			r := newProbeRun(t, c.hold, 0, rounds)
			first := r.f.clock.now().Unix() / 60 * 60
			r.run(first, slots)
			ends := r.endsSeen()
			// The first c.ahead Slots are past their own rounds' points with none reserved before them.
			passedAt := first + int64(c.ahead+rounds-1)*60
			if len(ends) != 1 || ends[0].Event != FloorProbePassed || int64(ends[0].Contract.Slot.EvaluationTime) != passedAt ||
				ends[0].Clean != rounds || ends[0].Rounds != rounds || ends[0].Floor != 0 || ends[0].Hold != c.hold {
				t.Fatalf("ends %+v, want one passed at Slot %d with %d clean rounds at floor 0, hold %v", ends, passedAt, rounds, c.hold)
			}
			for slot, ahead := range r.reservedAhead {
				if ahead != c.ahead {
					t.Fatalf("Slot %d's round reserved %d Slots ahead, want %d", slot, ahead, c.ahead)
				}
			}
			if r.most != c.ahead+1 {
				t.Fatalf("at most %d rounds reserved at once, want %d", r.most, c.ahead+1)
			}
			if got := r.rounds(); got[FloorProbeClean] < rounds || got[FloorProbeDifferent] != 0 {
				t.Fatalf("rounds %v, want at least %d clean and none different", got, rounds)
			}
			sources, group := skippedBy(r.f)
			if sources[EarlierSkipNoCandidate] != 0 || sources[EarlierSkipFloorProbe] != slots || group[EarlierSkipFloorProbe] != slots || r.refused != 0 {
				t.Fatalf("skipped %v, on its group %v, refusals heard %d; want every Slot floor_probe and none refused", sources, group, r.refused)
			}
		})
	}
}

// A probe near a floor a probe failed at passes on eight hours of its rounds clean, 480 a minute apart, and not one
// fewer, and its reading and its end say how many it needs.
func TestAProbeNearAFailedFloorPassesOnMoreRounds(t *testing.T) {
	const rounds, need = 3, 480
	r := newProbeRun(t, 30*time.Second, 0, rounds)
	r.edge = true
	first := r.f.clock.now().Unix() / 60 * 60
	r.run(first, need-1)
	if ends := r.endsSeen(); len(ends) != 0 {
		t.Fatalf("ends %+v after %d clean rounds, want none before %d", ends, need-1, need)
	}
	if reading, _ := r.f.engine.GroupReading("qg"); reading.FloorProbe == nil || reading.FloorProbe.Rounds != need {
		t.Fatalf("the group's probe %+v, want one needing %d rounds", reading.FloorProbe, need)
	}
	r.run(first+int64(need-1)*60, 1)
	ends := r.endsSeen()
	if len(ends) != 1 || ends[0].Event != FloorProbePassed || ends[0].Clean != need || ends[0].Rounds != need {
		t.Fatalf("ends %+v, want one passed on %d clean rounds", ends, need)
	}
}

// Near a floor a probe failed at, a probe needs eight hours of its rounds, the last one begun counting, and never fewer
// than ProbationRounds: 960 half a minute apart, 480 a minute apart, 412 seventy seconds apart, 160 three minutes
// apart, ProbationRounds from four minutes on. Elsewhere it needs ProbationRounds at any period.
func TestAProbeNearAFailedFloorNeedsEightHoursOfItsRounds(t *testing.T) {
	engine := newFixture(t).engine
	for _, c := range []struct {
		period time.Duration
		edge   int
	}{
		{30 * time.Second, 960}, {time.Minute, 480}, {70 * time.Second, 412}, {3 * time.Minute, 160},
		{4 * time.Minute, ProbationRounds}, {5 * time.Minute, ProbationRounds}, {20 * time.Minute, ProbationRounds},
	} {
		if got := engine.probeRounds(&floorProbeState{edge: true, period: c.period}); got != c.edge {
			t.Fatalf("near a failed floor, %v apart: %d rounds, want %d", c.period, got, c.edge)
		}
		if got := engine.probeRounds(&floorProbeState{period: c.period}); got != ProbationRounds {
			t.Fatalf("elsewhere, %v apart: %d rounds, want %d", c.period, got, ProbationRounds)
		}
	}
}

// A probe's rounds are its Slots: one whose Slots are five minutes apart, over data a minute apart, is near a failed
// floor on ProbationRounds, eight hours of its Slots being fewer, and not on eight hours of its data's steps.
func TestAProbesRoundsAreItsSlotsNotItsDataSteps(t *testing.T) {
	r := newProbeRun(t, 30*time.Second, 0, ProbationRounds)
	r.edge = true
	first := r.f.clock.now().Unix() / 60 * 60
	q := r.slotQuery(first)
	q.FollowingSlot = execution.EvaluationTime(first + 300)
	r.f.clock.set(time.Unix(first+int64(r.hold/time.Second), 0))
	r.f.engine.Prepare(q)
	if reading, _ := r.f.engine.GroupReading("qg"); reading.FloorProbe == nil || reading.FloorProbe.Rounds != ProbationRounds {
		t.Fatalf("the group's probe %+v, want one needing %d rounds, its Slots five minutes apart", reading.FloorProbe, ProbationRounds)
	}
}

// One round read differently at the floor fails the probe there, and every reservation it had is dropped. The next
// Slot, its floor still the one to probe, starts another from none.
func TestOneDifferentRoundFailsTheProbe(t *testing.T) {
	r := newProbeRun(t, 2*time.Minute, 0, 5)
	first := r.f.clock.now().Unix() / 60 * 60
	r.value[first+3*60] = "0"
	r.run(first, 5)
	ends := r.endsSeen()
	if len(ends) != 1 || ends[0].Event != FloorProbeFailed || int64(ends[0].Contract.Slot.EvaluationTime) != first+3*60 || ends[0].Clean != 1 {
		t.Fatalf("ends %+v, want one failed at the different Slot after one clean round", ends)
	}
	if got := r.events(); !equalCounts(got, map[string]uint64{FloorProbeStarted: 2, FloorProbeFailed: 1}) {
		t.Fatalf("probe events %v, want the failed one and the one started after it", got)
	}
	if reading, _ := r.f.engine.GroupReading("qg"); reading.FloorProbe == nil || reading.FloorProbe.Clean != 0 {
		t.Fatalf("the group's probe after the failure: %+v, want a new one with no clean round", reading.FloorProbe)
	}
}

// A round that is no evidence - its permit refused, its read failed, the formal read not whole, its read overtaken
// by the formal one - neither advances the probe nor ends it, nor starts its count over: the probe passes on its
// clean rounds alone, the one before them and those after, later.
func TestARoundThatIsNoEvidenceNeitherAdvancesNorEndsTheProbe(t *testing.T) {
	const rounds = 3
	r := newProbeRun(t, 30*time.Second, 0, rounds)
	first := r.f.clock.now().Unix() / 60 * 60
	r.refuse[first+60] = true
	r.readFails[first+2*60] = true
	r.formalIncomplete[first+3*60] = true
	r.late[first+4*60] = true
	r.run(first, 4+rounds)
	ends := r.endsSeen()
	if len(ends) != 1 || ends[0].Event != FloorProbePassed || int64(ends[0].Contract.Slot.EvaluationTime) != first+int64(4+rounds-1)*60 ||
		ends[0].Clean != rounds {
		t.Fatalf("ends %+v, want one passed at the last Slot, on its %d clean rounds", ends, rounds)
	}
	want := map[string]uint64{FloorProbeClean: rounds, FloorProbeSkippedForPermits: 1, FloorProbeReadFailed: 1,
		FloorProbeUnclassified: 1, FloorProbeOvertaken: 1}
	if got := r.rounds(); !equalCounts(got, want) {
		t.Fatalf("rounds %v, want %v", got, want)
	}
}

// A probe ends hold_moved when its floor is no longer the one to probe, when the search below the hold opens again,
// or when the floor it reads at or its kind moves - the trials chose a candidate to verify; the group leaving the
// process ends it owner_lost. Each end and start is counted under the kind of its probe. Its reservations are dropped
// with it, read by no one. A floor still to probe - moved, or the group back - starts another from none.
func TestAProbeEndsWhenWhatItReadsMoves(t *testing.T) {
	for _, c := range []struct {
		name  string
		move  func(r *probeRun)
		event string
		again bool
	}{
		{"floor not to probe", func(r *probeRun) { r.set(func() { r.probing = false }) }, FloorProbeHoldMoved, false},
		{"search open", func(r *probeRun) { r.set(func() { r.candidate = true }) }, FloorProbeHoldMoved, false},
		{"floor moved", func(r *probeRun) { r.set(func() { r.floor = 10 * time.Second }) }, FloorProbeHoldMoved, true},
		{"near a failed floor now", func(r *probeRun) { r.set(func() { r.edge = true }) }, FloorProbeHoldMoved, true},
		{"another kind now", func(r *probeRun) { r.set(func() { r.kind = "verify" }) }, FloorProbeHoldMoved, true},
		{"owner lost", func(r *probeRun) { r.f.engine.Forget("qg") }, FloorProbeOwnerLost, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newProbeRun(t, 2*time.Minute, 0, 50)
			first := r.f.clock.now().Unix() / 60 * 60
			r.run(first, 4)
			c.move(r)
			r.run(first+4*60, 1)
			ends := r.endsSeen()
			if len(ends) != 1 || ends[0].Event != c.event || ends[0].Kind != "floor" {
				t.Fatalf("ends %+v, want one %s of the floor", ends, c.event)
			}
			started := map[bool]uint64{false: 1, true: 2}[c.again]
			if got := r.events()[FloorProbeStarted]; got != started {
				t.Fatalf("%d probes started, want %d", got, started)
			}
			byKind := r.f.engine.Stats().Sources[sourceTimeSeries].FloorProbesByKind
			again := map[bool]uint64{false: 0, true: 1}[c.again && r.kind == "verify"]
			if byKind["floor"][c.event] != 1 || byKind["verify"][FloorProbeStarted] != again ||
				byKind["floor"][FloorProbeStarted] != started-again {
				t.Fatalf("by kind %v: want the floor's start and %s, and the probe after under its own kind", byKind, c.event)
			}
			reading, _ := r.f.engine.GroupReading("qg")
			switch busy, _ := r.f.engine.Busy("qg"); {
			case !c.again && (busy.FloorProbeRounds != 0 || reading.FloorProbe != nil):
				t.Fatalf("%d rounds still reserved, probe %+v, after the probe ended", busy.FloorProbeRounds, reading.FloorProbe)
			case c.again && (reading.FloorProbe == nil || reading.FloorProbe.Clean != 0 || reading.FloorProbe.FloorMillis != r.floor.Milliseconds() ||
				reading.FloorProbe.Kind != map[bool]string{false: "floor", true: "verify"}[r.kind == "verify"]):
				t.Fatalf("the probe after: %+v, want one at the floor %v with no round yet, of its kind", reading.FloorProbe, r.floor)
			}
		})
	}
}

// A round frozen lower than the one before starts probation, the lowering verified by a probe or not: a probe that
// passed by chance is read early until a sample sees it, and probation sees the first such round. A probation of the
// hold before it is replaced by the lowering's own.
func TestEveryLoweringStartsProbation(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "a trial's lowering", true: "a verified lowering"}[verified], func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.LoweringVerified = func(execution.QueryGroupIdentity) bool { return false }
			slot := f.clock.now().Unix() / 60 * 60
			read := func(at int64, hold time.Duration) {
				q := scheduledQuery(t, at)
				q.Contract.ReadHoldMillis = hold.Milliseconds()
				f.engine.Begin(q).Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
			}
			read(slot, 3*time.Minute)
			read(slot+60, 2*time.Minute)
			if _, probation := f.engine.InProbation("qg"); !probation {
				t.Fatal("fixture: a trial's lowering started no probation")
			}
			f.engine.options.LoweringVerified = func(execution.QueryGroupIdentity) bool { return verified }
			read(slot+120, time.Minute)
			if clean, probation := f.engine.InProbation("qg"); !probation || clean != 0 {
				t.Fatalf("after a lowering verified %v: in probation %v with %d clean, want a new one", verified, probation, clean)
			}
		})
	}
}

// A round the probe cannot trust is not counted towards it, and the probe passes a Slot later: a round reserved ahead
// for a Slot its first attempt finds frozen otherwise is dropped, counted overtaken; a formal read of another query
// than the one its round was reserved at does not take the round, which the next Slot drops; and a round frozen at
// another hold than the probe reads at is compared - counted clean - but is no evidence for that hold.
func TestARoundTheProbeCannotTrustIsNotCounted(t *testing.T) {
	const rounds = 3
	for _, c := range []struct {
		name  string
		hold  time.Duration
		ahead int
		set   func(r *probeRun, slot int64)
		want  map[string]uint64
	}{
		{"its Slot frozen otherwise", 2 * time.Minute, 2, func(r *probeRun, slot int64) { r.revised[slot] = true },
			map[string]uint64{FloorProbeClean: rounds, FloorProbeOvertaken: 1}},
		{"its formal read another query", 30 * time.Second, 0, func(r *probeRun, slot int64) { r.revisedAt[slot] = true },
			map[string]uint64{FloorProbeClean: rounds, FloorProbeOvertaken: 1}},
		{"its Slot frozen at another hold", 30 * time.Second, 0, func(r *probeRun, slot int64) { r.frozenHold[slot] = 40 * time.Second },
			map[string]uint64{FloorProbeClean: rounds + 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newProbeRun(t, c.hold, 0, rounds)
			first := r.f.clock.now().Unix() / 60 * 60
			// The Slot after the first with a round of its own.
			c.set(r, first+int64(c.ahead+1)*60)
			r.run(first, c.ahead+rounds+1)
			ends := r.endsSeen()
			passedAt := first + int64(c.ahead+rounds)*60
			if len(ends) != 1 || ends[0].Event != FloorProbePassed || int64(ends[0].Contract.Slot.EvaluationTime) != passedAt || ends[0].Clean != rounds {
				t.Fatalf("ends %+v, want one passed at Slot %d, a Slot later, on %d clean rounds", ends, passedAt, rounds)
			}
			if got := r.rounds(); !equalCounts(got, c.want) {
				t.Fatalf("rounds %v, want %v", got, c.want)
			}
		})
	}
}

// A probe that fails says how its last round's formal read differed from the read at the probe's hold, in buckets by
// change class, on its end and in its source's counts: a value the formal read found otherwise is values_changed,
// a point the probe's read did not have yet points_added.
func TestAFailedProbeSaysHowItsRoundDiffered(t *testing.T) {
	for _, c := range []struct {
		name   string
		value  string
		change string
	}{
		{"a value", "0", ChangeValuesChanged},
		{"a point not there yet", "none", ChangePointsAdded},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newProbeRun(t, 30*time.Second, 0, 5)
			first := r.f.clock.now().Unix() / 60 * 60
			r.value[first+60] = c.value
			r.run(first, 2)
			ends := r.endsSeen()
			if len(ends) != 1 || ends[0].Event != FloorProbeFailed || ends[0].Changes[c.change] != 1 || len(ends[0].Changes) != 1 {
				t.Fatalf("ends %+v, want one failed whose round differed in one bucket, %s", ends, c.change)
			}
			got := map[string]uint64{}
			for change, count := range r.f.engine.Stats().Sources[sourceTimeSeries].FloorProbeChanges {
				if count > 0 {
					got[change] = count
				}
			}
			if !equalCounts(got, map[string]uint64{c.change: 1}) {
				t.Fatalf("source changes %v, want %s once", got, c.change)
			}
		})
	}
}

// A round reserved Slots ahead is read before its Slot's first attempt, which takes the round for the Slot's frozen
// query while the read may still be running: the read reads the query it started with, copied under the lock, never
// the round as it is moved. The test waits until the read has passed its one locked step - only what came before it
// is ordered before the move - and holds the read on a channel of its own, so under -race this fails if the read
// reads the round itself after that step.
func TestARoundsReadReadsTheQueryItStartedWith(t *testing.T) {
	r := newProbeRun(t, 2*time.Minute, 0, 3)
	first := r.f.clock.now().Unix() / 60 * 60
	held := first + 2*60
	release := make(chan struct{})
	r.f.engine.options.Recheck = func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		if spec.LogicalWindow.End == held {
			select {
			case <-release:
			case <-ctx.Done():
				return execution.ProviderCompletion{}, ctx.Err()
			}
		}
		return full(point(spec.LogicalWindow.End, "1"))(sink)
	}
	at := func(second int64) { r.f.clock.set(time.Unix(second, 0)) }
	// first's first attempt reserves the round of held, two Slots ahead.
	at(first + 120)
	r.f.engine.Prepare(r.slotQuery(first))
	if r.note(); r.reservedAhead[held] != 2 {
		t.Fatalf("fixture: held's round reserved %d Slots ahead, want 2", r.reservedAhead[held])
	}
	// Its read starts, its point ahead of held's first attempt, and is held there.
	at(held + probeDelay - 5)
	r.f.engine.StepEarly(context.Background())
	testwait.Until(t, "held's round read past its locked step", func() bool {
		r.f.engine.mu.Lock()
		defer r.f.engine.mu.Unlock()
		round := r.f.engine.groups["qg"].floorProbe.reserved[execution.EvaluationTime(held)]
		return round != nil && round.running && round.cancel != nil
	})
	// The Slots before it and held's own first attempt, which takes the round while it is read.
	at(first + 180)
	r.f.engine.Prepare(r.slotQuery(first + 60))
	at(held + 120)
	r.f.engine.Prepare(r.slotQuery(held))
	close(release)
	testwait.Until(t, "held's round read", func() bool {
		busy, _ := r.f.engine.Busy("qg")
		return busy.ReadsInFlight == 0
	})
}

// A probe whose group's record kept its clean rounds - a rollout, a takeover - goes on from them, from the Slot it
// started at, counted resumed beside started, and passes once the rounds it still needs read clean. Every
// FloorProbeProgressEvery clean rounds of it are handed on to be kept, and its start is seen with the rounds it went on
// from. A probe with none kept starts from none.
func TestAProbeGoesOnFromTheRoundsItsRecordKept(t *testing.T) {
	for _, kept := range []int{0, 15} {
		t.Run(fmt.Sprint(kept), func(t *testing.T) {
			r := newProbeRun(t, 2*time.Minute, 0, 25)
			first := r.f.clock.now().Unix() / 60 * 60
			startedAt := execution.EvaluationTime(first - 3600)
			var starts []FloorProbeStart
			var progress []FloorProbeProgress
			options := &r.f.engine.options
			options.FloorProbeResume = func(_ execution.QueryGroupIdentity, kind string, floor time.Duration) (int, execution.EvaluationTime, bool) {
				if kept == 0 || kind != "floor" || floor != r.floor {
					return 0, 0, false
				}
				return kept, startedAt, true
			}
			options.OnFloorProbeStart = func(start FloorProbeStart) {
				r.mu.Lock()
				starts = append(starts, start)
				r.mu.Unlock()
			}
			options.OnFloorProbeProgress = func(round FloorProbeProgress) {
				r.mu.Lock()
				progress = append(progress, round)
				r.mu.Unlock()
			}
			left := 25 - kept
			r.run(first, left+3)
			wantStarted := map[bool]execution.EvaluationTime{true: startedAt, false: execution.EvaluationTime(first)}[kept > 0]
			ends := r.endsSeen()
			if len(ends) != 1 || ends[0].Event != FloorProbePassed || ends[0].Clean != 25 || ends[0].ResumedFrom != kept ||
				ends[0].Started != wantStarted {
				t.Fatalf("ends %+v, want it passed at 25 clean rounds, resumed from %d, started at %d", ends, kept, wantStarted)
			}
			// Here it read only the rounds it still needed.
			if got := r.rounds()[FloorProbeClean]; got != uint64(left) {
				t.Fatalf("%d clean rounds read here, want the %d left", got, left)
			}
			r.mu.Lock()
			gotStarts, gotProgress := append([]FloorProbeStart(nil), starts...), append([]FloorProbeProgress(nil), progress...)
			r.mu.Unlock()
			// The first start; the fixture's floor stays to probe, and another starts after it passed.
			if len(gotStarts) == 0 || gotStarts[0].ResumedFrom != kept || gotStarts[0].Started != wantStarted || gotStarts[0].Kind != "floor" ||
				gotStarts[0].Rounds != 25 {
				t.Fatalf("starts %+v, want the first of the floor resumed from %d", gotStarts, kept)
			}
			var cleans []int
			for _, round := range gotProgress {
				if round.Started != wantStarted || round.Kind != "floor" || round.Floor != r.floor || round.Hold != r.hold {
					t.Fatalf("progress %+v, want the probe's", round)
				}
				cleans = append(cleans, round.Clean)
			}
			want := map[int]string{0: "[10 20]", 15: "[20]"}[kept]
			if fmt.Sprint(cleans) != want {
				t.Fatalf("progress handed on at %v clean rounds, want %s", cleans, want)
			}
			events := r.f.engine.Stats().Sources[sourceTimeSeries].FloorProbesByKind["floor"]
			if events[FloorProbeStarted] != uint64(len(gotStarts)) || events[FloorProbeResumed] != map[bool]uint64{true: uint64(len(gotStarts)), false: 0}[kept > 0] {
				t.Fatalf("events %v, %d starts seen, want each counted started, and resumed when it went on from kept rounds", events, len(gotStarts))
			}
		})
	}
}
