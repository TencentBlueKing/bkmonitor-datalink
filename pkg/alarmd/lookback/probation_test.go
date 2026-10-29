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
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// probationFixture is a fixture whose group "qg" takes no sample - its rest
// set past the test - so every reread it reads is a probation one, ending a
// probation after rounds clean ones; raises is the whole-window evidence its
// rereads gave.
type probationFixture struct {
	*fixture
	raisesMu sync.Mutex
	raises   []ReadHoldEvidence
	slot     int64
}

func newProbationFixture(t *testing.T, rounds int) *probationFixture {
	return newResumingFixture(t, rounds, nil)
}

// newResumingFixture is a probationFixture of a process that has seen no
// round of qg, whose record says its hold was last lowered at lowering.
func newResumingFixture(t *testing.T, rounds int, lowering *int64) *probationFixture {
	t.Helper()
	f := &probationFixture{fixture: newFixture(t)}
	f.engine.options.ProbationRounds = rounds
	f.engine.options.LastLowering = func(execution.QueryGroupIdentity) (execution.EvaluationTime, bool) {
		if lowering == nil {
			return 0, false
		}
		return execution.EvaluationTime(*lowering), true
	}
	f.engine.options.OnWholeWindowReadEarly = func(evidence ReadHoldEvidence) {
		f.raisesMu.Lock()
		f.raises = append(f.raises, evidence)
		f.raisesMu.Unlock()
	}
	f.slot = f.clock.now().Unix()
	f.engine.mu.Lock()
	f.engine.groupLocked(query("qg", f.slot, minute, sourceTimeSeries), f.clock.now()).nextAt = f.clock.now().Add(24 * time.Hour)
	f.engine.mu.Unlock()
	return f
}

// round reads qg's next Slot, two minutes after the last, under hold with
// first in its window, and returns when it was read.
func (f *probationFixture) round(hold time.Duration, first string) (int64, time.Time) {
	f.t.Helper()
	f.slot += 120
	f.clock.set(time.Unix(f.slot, 0))
	q := query("qg", f.slot, minute, sourceTimeSeries)
	q.Contract.ReadHoldMillis = hold.Milliseconds()
	q.FollowingSlot = execution.EvaluationTime(f.slot + 120)
	read := f.engine.Begin(q)
	read.Series(point(f.slot, first), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	return f.slot, f.clock.now()
}

func (f *probationFixture) rereads() map[string]uint64 {
	return f.engine.Stats().Sources[sourceTimeSeries].ProbationRereads
}

func (f *probationFixture) pending() int {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	return len(f.engine.groups["qg"].probations)
}

// reread reads the round's probation reread at rung with the value given,
// and returns once the rung is counted.
func (f *probationFixture) reread(slot int64, readAt time.Time, rung int, value string) {
	f.t.Helper()
	f.recheck(sourceTimeSeries, readAt, rung, minute, full(point(slot, value)), RecheckCompared)
}

// cleanRound is a round under hold whose reread reads it as it was.
func (f *probationFixture) cleanRound(hold time.Duration) {
	f.t.Helper()
	slot, readAt := f.round(hold, "3")
	before := f.rereads()[ProbationClean]
	f.reread(slot, readAt, 0, "3")
	f.waitFor(func(stats Stats) bool {
		return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationClean] > before
	})
}

// A lowered hold puts every round of its group on probation: each is read
// again at its first rung, and once ProbationRounds of them read clean the
// probation ends and the next round is not read again. A round under a hold
// that was not lowered is not read again; a lowering on the way - a hold
// coming down a step per Slot - starts the count again; and the rereads
// are counted apart from the samples' classes.
func TestALoweredHoldPutsEveryRoundOnProbation(t *testing.T) {
	f := newProbationFixture(t, 3)
	f.round(2*time.Minute, "3")
	f.round(2*time.Minute, "3")
	if f.pending() != 0 {
		t.Fatal("a round under a hold never lowered was read again")
	}
	f.cleanRound(time.Minute + 30*time.Second)
	f.cleanRound(time.Minute + 30*time.Second)
	// Lowered a step again: the count starts again.
	f.cleanRound(time.Minute)
	if clean, in := f.engine.InProbation("qg"); !in || clean != 1 {
		t.Fatalf("after a further lowering: in probation %v with %d clean, want 1", in, clean)
	}
	if got := f.engine.Stats().Sources[sourceTimeSeries].ProbationGroups; got != 1 {
		t.Fatalf("groups in probation %d, want 1", got)
	}
	f.cleanRound(time.Minute)
	f.cleanRound(time.Minute)
	if _, in := f.engine.InProbation("qg"); in {
		t.Fatal("still in probation after its rounds read clean")
	}
	f.round(time.Minute, "3")
	if f.pending() != 0 {
		t.Fatal("a round after the probation ended was read again")
	}
	stats := f.engine.Stats().Sources[sourceTimeSeries]
	if stats.ProbationRereads[ProbationClean] != 5 || stats.ProbationGroups != 0 {
		t.Fatalf("rereads %v, groups %d: want 5 clean and none in probation", stats.ProbationRereads, stats.ProbationGroups)
	}
	for class, n := range stats.Classes {
		if n != 0 {
			t.Fatalf("a probation reread counted as a sample class %s", class)
		}
	}
}

// Whether a group's decisions are kept, read without the engine's lock,
// follows its probation: not before a lowering, from the lowering on, not
// once its rounds read clean, again on the next lowering, and not once the
// group leaves this process.
func TestTheGroupsWhoseDecisionsAreKeptFollowTheirProbation(t *testing.T) {
	f := newProbationFixture(t, 2)
	kept := func(want bool, when string) {
		t.Helper()
		_, in := f.engine.InProbation("qg")
		if got := f.engine.KeepsDecisions("qg"); got != want || in != want {
			t.Fatalf("%s: decisions kept %v, in probation %v, want %v", when, got, in, want)
		}
	}
	f.round(2*time.Minute, "3")
	kept(false, "never lowered")
	f.cleanRound(time.Minute + 30*time.Second)
	kept(true, "lowered")
	f.cleanRound(time.Minute + 30*time.Second)
	kept(false, "its rounds read clean")
	f.cleanRound(time.Minute)
	kept(true, "lowered again")
	f.engine.Forget("qg")
	kept(false, "forgotten")
}

// A round of a probation whose reread finds its whole window read early
// raises the hold as a sample would, at once, and its probation counts
// again from none. What it caught is counted by the lowering it followed: a
// verified one is a probe that passed by chance.
func TestAProbationRereadThatFindsTheWindowReadEarlyRaisesTheHold(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "after a trial's lowering", true: "after a verified lowering"}[verified], func(t *testing.T) {
			probationCatchesARoundReadEarly(t, verified)
		})
	}
}

func probationCatchesARoundReadEarly(t *testing.T, verified bool) {
	f := newProbationFixture(t, 3)
	f.engine.options.LoweringVerified = func(execution.QueryGroupIdentity) bool { return verified }
	f.round(2*time.Minute, "3")
	f.cleanRound(time.Minute)
	slot, readAt := f.round(time.Minute, "3")
	f.reread(slot, readAt, 0, "5")
	// Still changing at its rung: followed one further, where it stood.
	f.reread(slot, readAt, 1, "5")
	f.waitFor(func(stats Stats) bool {
		return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationWindowReadEarly] == 1
	})
	f.raisesMu.Lock()
	raises := append([]ReadHoldEvidence(nil), f.raises...)
	f.raisesMu.Unlock()
	if len(raises) != 1 || raises[0].Contract.Slot.EvaluationTime != execution.EvaluationTime(slot) || raises[0].ArrivalAge <= 0 ||
		!raises[0].Probation {
		t.Fatalf("raises %+v, want the round's once with its arrival, a probation's", raises)
	}
	if clean, in := f.engine.InProbation("qg"); !in || clean != 0 {
		t.Fatalf("after a round read early: in probation %v with %d clean, want none", in, clean)
	}
	want := map[string]uint64{ProbationAfterTrial: 1, ProbationAfterVerified: 0}
	if verified {
		want = map[string]uint64{ProbationAfterTrial: 0, ProbationAfterVerified: 1}
	}
	if got := f.engine.Stats().Sources[sourceTimeSeries].ProbationCaught; !equalCounts(got, want) {
		t.Fatalf("caught %v, want %v", got, want)
	}
	// The raise reaches the next round's hold, and ends the probation.
	f.round(2*time.Minute, "3")
	if _, in := f.engine.InProbation("qg"); in || f.pending() != 0 {
		t.Fatal("a raised hold is still on probation")
	}
}

// A probation reread whose round lost a series it had - data that went after
// the first read, no whole window arriving to read an age from - is a round
// read early all the same: the controller hears of it, as a probation's
// finding, and undoes the lowering. A sample's finding of the same shape is
// noise; a probation's went nowhere, and the hold stayed lowered.
func TestAProbationRereadWhoseSeriesWentStillReachesTheController(t *testing.T) {
	f := newProbationFixture(t, 3)
	f.round(2*time.Minute, "3")
	slot, readAt := f.round(time.Minute, "3")
	f.recheck(sourceTimeSeries, readAt, 0, minute, full(), RecheckCompared)
	f.recheck(sourceTimeSeries, readAt, 1, minute, full(), RecheckCompared)
	f.waitFor(func(stats Stats) bool {
		return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationWindowReadEarly] == 1
	})
	f.raisesMu.Lock()
	raises := append([]ReadHoldEvidence(nil), f.raises...)
	f.raisesMu.Unlock()
	if len(raises) != 1 || raises[0].Contract.Slot.EvaluationTime != execution.EvaluationTime(slot) || !raises[0].Probation {
		t.Fatalf("evidence %+v, want the round's once, a probation's finding", raises)
	}
}

// A reread never read for want of a permit is no round read clean: the
// probation lasts until ProbationRounds rounds were read.
func TestASkippedProbationRereadDoesNotCount(t *testing.T) {
	f := newProbationFixture(t, 2)
	f.round(2*time.Minute, "3")
	_, readAt := f.round(time.Minute, "3")
	f.set(func() { f.refuse = "waiters" })
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.engine.Step(context.Background())
	f.clock.set(readAt.Add(rungDelay(0, minute) + rungWindow(0, minute) + time.Second))
	f.engine.Step(context.Background())
	if got := f.rereads()[ProbationSkippedForPermits]; got != 1 {
		t.Fatalf("rereads %v, want one skipped for permits", f.rereads())
	}
	if clean, in := f.engine.InProbation("qg"); !in || clean != 0 {
		t.Fatalf("a skipped reread counted: in probation %v with %d clean", in, clean)
	}
	f.set(func() { f.refuse = "" })
	f.cleanRound(time.Minute)
	if _, in := f.engine.InProbation("qg"); !in {
		t.Fatal("the probation ended on a reread that never happened")
	}
	f.cleanRound(time.Minute)
	if _, in := f.engine.InProbation("qg"); in {
		t.Fatal("the probation did not end after its rounds read clean")
	}
}

// A reread that came back without its window whole is no round read clean
// either, and is told apart from one never read.
func TestAFailedProbationRereadDoesNotCount(t *testing.T) {
	f := newProbationFixture(t, 2)
	f.round(2*time.Minute, "3")
	_, readAt := f.round(time.Minute, "3")
	f.recheck(sourceTimeSeries, readAt, 0, minute, func(execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		return execution.ProviderCompletion{Completeness: execution.CompletenessPartial}, nil
	}, RecheckPartial)
	f.waitFor(func(stats Stats) bool {
		return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationReadFailed] == 1
	})
	if clean, in := f.engine.InProbation("qg"); !in || clean != 0 {
		t.Fatalf("a failed reread counted: in probation %v with %d clean", in, clean)
	}
}

// A process that takes over a group lowered a few rounds ago - a restart, a
// takeover - resumes its probation, its count from none: the rounds since
// were not all read again, and only a round read again counts. Here the
// lowering was two of three rounds back, and the probation ends only after
// three clean rounds on the new owner. A lowering as old as a probation, or
// none, resumes nothing.
func TestAProbationContinuesOnTheNextOwner(t *testing.T) {
	start := time.Unix(1_700_000_100, 0).Unix()
	lowering := start + 120 - 2*120
	f := newResumingFixture(t, 3, &lowering)
	for round := 1; round <= 3; round++ {
		if clean, in := f.engine.InProbation("qg"); round > 1 && (!in || clean != round-1) {
			t.Fatalf("before round %d on the new owner: in probation %v with %d clean", round, in, clean)
		}
		f.cleanRound(time.Minute)
	}
	if _, in := f.engine.InProbation("qg"); in {
		t.Fatal("the probation did not end after its rounds read clean on the new owner")
	}
	if got := f.rereads()[ProbationClean]; got != 3 {
		t.Fatalf("rereads %v, want every round of the probation read again on the new owner", f.rereads())
	}
	f.round(time.Minute, "3")
	if f.pending() != 0 {
		t.Fatal("a round after the resumed probation ended was read again")
	}

	old := start + 120 - 3*120
	f = newResumingFixture(t, 3, &old)
	f.round(time.Minute, "3")
	if _, in := f.engine.InProbation("qg"); in || f.pending() != 0 {
		t.Fatal("a lowering as old as a probation resumed one")
	}
	f = newResumingFixture(t, 3, nil)
	f.round(time.Minute, "3")
	if _, in := f.engine.InProbation("qg"); in {
		t.Fatal("a group never lowered resumed a probation")
	}
}

// A probation resumed on the next owner keeps which lowering it follows: the first round there asks, and a round it
// catches read early after a verified lowering is counted as one.
func TestAResumedProbationCountsWhatItCatchesByTheLoweringItFollows(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "after a trial's lowering", true: "after a verified lowering"}[verified], func(t *testing.T) {
			lowering := time.Unix(1_700_000_100, 0).Unix() + 120 - 2*120
			f := newResumingFixture(t, 3, &lowering)
			f.engine.options.LoweringVerified = func(execution.QueryGroupIdentity) bool { return verified }
			slot, readAt := f.round(time.Minute, "3")
			if _, in := f.engine.InProbation("qg"); !in {
				t.Fatal("fixture: the probation did not resume")
			}
			f.reread(slot, readAt, 0, "5")
			f.reread(slot, readAt, 1, "5")
			f.waitFor(func(stats Stats) bool {
				return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationWindowReadEarly] == 1
			})
			want := map[string]uint64{ProbationAfterTrial: 1, ProbationAfterVerified: 0}
			if verified {
				want = map[string]uint64{ProbationAfterTrial: 0, ProbationAfterVerified: 1}
			}
			if got := f.engine.Stats().Sources[sourceTimeSeries].ProbationCaught; !equalCounts(got, want) {
				t.Fatalf("caught %v, want %v", got, want)
			}
		})
	}
}

// Every round probation finds read early is a line of its own: the hold it was read at and the one the lowering came
// from, how its data came, and what it decided as the worker reported it - kept by Slot while the group is in
// probation, spent with the line. A round whose decision this process did not keep says so; a group not in probation
// keeps none.
func TestARoundReadEarlyIsALineWithWhatItDecided(t *testing.T) {
	for _, reported := range []bool{true, false} {
		t.Run(map[bool]string{true: "decision kept", false: "decision not kept"}[reported], func(t *testing.T) {
			f := newProbationFixture(t, 3)
			var lines []WindowReadEarly
			f.engine.options.OnWindowReadEarly = func(line WindowReadEarly) {
				f.raisesMu.Lock()
				lines = append(lines, line)
				f.raisesMu.Unlock()
			}
			// Not in probation: a decision reported is not kept.
			f.round(2*time.Minute, "3")
			f.engine.NoteSlotDecision(SlotDecisionReport{QueryGroup: "qg", Slot: execution.EvaluationTime(f.slot), Completed: true, Events: 9})
			f.engine.mu.Lock()
			kept := len(f.engine.groups["qg"].decisions)
			f.engine.mu.Unlock()
			if kept != 0 {
				t.Fatalf("a group not in probation kept %d decisions", kept)
			}
			slot, readAt := f.round(time.Minute, "3")
			if reported {
				// Reported series by series: a strategy twice is one.
				for _, strategy := range []string{"15", "11", "14", "12", "13", "11"} {
					f.engine.NoteSlotDecision(SlotDecisionReport{QueryGroup: "qg", Slot: execution.EvaluationTime(slot), Strategy: strategy,
						Levels: map[string]uint64{"ABNORMAL": 1, "NORMAL": 2}})
				}
				f.engine.NoteSlotDecision(SlotDecisionReport{QueryGroup: "qg", Slot: execution.EvaluationTime(slot), Completed: true,
					Events: 2, StateMutations: 5})
			}
			f.reread(slot, readAt, 0, "5")
			f.reread(slot, readAt, 1, "5")
			f.waitFor(func(stats Stats) bool {
				return stats.Sources[sourceTimeSeries].ProbationRereads[ProbationWindowReadEarly] == 1
			})
			f.raisesMu.Lock()
			got := append([]WindowReadEarly(nil), lines...)
			f.raisesMu.Unlock()
			if len(got) != 1 {
				t.Fatalf("lines %+v, want one", got)
			}
			line := got[0]
			if line.Path != ReadEarlyPathProbation || line.Contract.Slot.EvaluationTime != execution.EvaluationTime(slot) ||
				line.Hold != time.Minute || line.Changes[ChangeValuesChanged] == 0 {
				t.Fatalf("line %+v, want the probation's round read at 1 minute, its values changed", line)
			}
			if !reported {
				if line.Decision != nil || line.DecisionMissing != DecisionNotRecorded {
					t.Fatalf("decision %+v, missing %q, want not_recorded", line.Decision, line.DecisionMissing)
				}
				return
			}
			want := SlotDecision{Events: 2, StateMutations: 5, Levels: map[string]uint64{"ABNORMAL": 6, "NORMAL": 12},
				Strategies: []string{"11", "12", "14", "15"}, StrategyCount: 5, Completed: true}
			if line.Decision == nil || fmt.Sprint(*line.Decision) != fmt.Sprint(want) {
				t.Fatalf("decision %+v, want %+v", line.Decision, want)
			}
			f.engine.mu.Lock()
			left := len(f.engine.groups["qg"].decisions)
			f.engine.mu.Unlock()
			if left != 0 {
				t.Fatalf("%d decisions kept after the round's line, want it spent", left)
			}
		})
	}
}

// A group in probation keeps the decisions of maxKeptDecisions Slots at most, the oldest going first.
func TestTheDecisionsAGroupKeepsAreBounded(t *testing.T) {
	f := newProbationFixture(t, 3)
	f.round(2*time.Minute, "3")
	f.round(time.Minute, "3")
	for slot := range maxKeptDecisions + 3 {
		f.engine.NoteSlotDecision(SlotDecisionReport{QueryGroup: "qg", Slot: execution.EvaluationTime(1_000 + slot), Completed: true})
	}
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	decisions := f.engine.groups["qg"].decisions
	if len(decisions) != maxKeptDecisions || decisions[1_000] != nil || decisions[1_000+maxKeptDecisions+2] == nil {
		t.Fatalf("%d decisions kept, want the latest %d", len(decisions), maxKeptDecisions)
	}
}
