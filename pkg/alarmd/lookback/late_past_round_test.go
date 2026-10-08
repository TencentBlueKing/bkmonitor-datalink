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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// openWindow opens one directed window of the fixture's series_late group in
// the directed reads that stand: its Slot, the rung it is read at, and the
// time_delay its query runs under.
func openWindow(f *fixture, evaluation int64, rung int, delaySeconds int64) *directedSlot {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	state := f.engine.groups["qg"]
	return &directedSlot{queryGroup: "qg", source: state.source, evaluation: execution.EvaluationTime(evaluation),
		step: state.step, rung: rung, period: state.seriesLate, queries: []*directedQuery{{spec: execution.PhysicalQuerySpec{
			PlanFacts: execution.QueryPlanFacts{QueryDelaySeconds: delaySeconds}}}}}
}

// endWindow ends an open window with what it came to.
func endWindow(f *fixture, slot *directedSlot, outcome string, facts *execution.SupplementFacts) {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	f.engine.noteDirectedLocked(f.engine.groups["qg"], slot, outcome, "", facts)
}

// lateWindow opens one directed window and ends it.
func lateWindow(f *fixture, evaluation int64, rung int, delaySeconds int64, outcome string, facts *execution.SupplementFacts) {
	endWindow(f, openWindow(f, evaluation, rung, delaySeconds), outcome, facts)
}

func crossed(n int) *execution.SupplementFacts {
	return &execution.SupplementFacts{Candidates: n, CrossedT: n}
}

func mixed(admitted, crossedT int) *execution.SupplementFacts {
	return &execution.SupplementFacts{Candidates: admitted + crossedT, Admitted: admitted, Points: uint64(admitted), CrossedT: crossedT}
}

// Two supplemented windows in a row whose every late series had crossed its
// Slot report the Query Group, with a time_delay that reads those series:
// the one it runs under plus how long after the first read they were seen,
// aligned up to the step. One such window is not a report.
func TestAGroupWhoseLateSeriesCrossTheirSlotTwiceInARowIsReported(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, crossed(3))
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("one window reported: %+v", readings)
	}
	lateWindow(f, 660, 2, 60, DirectedSupplemented, crossed(2))
	readings := f.engine.LatePastRound()
	if len(readings) != 1 {
		t.Fatalf("readings %+v, want the group after two windows", readings)
	}
	reading := readings[0]
	// The deeper rung is the later sighting: 3.5 steps after the first read.
	want := int64(60) + int64(rungDelay(2, minute).Seconds())
	want = (want + 59) / 60 * 60
	if reading.CurrentDelaySeconds != 60 || reading.SuggestedDelaySeconds != want || reading.StepSeconds != 60 ||
		len(reading.Samples) != 2 || reading.Samples[1].CrossedSeries != 2 || reading.Samples[1].Rung != RungNames[2] {
		t.Fatalf("reading %+v, want current 60 and suggested %d from the samples", reading, want)
	}
	if residual := f.engine.ResidualMisses(); len(residual) != 0 {
		t.Fatalf("a window with nothing recovered counted as a residual miss: %+v", residual)
	}
}

// A window the supplement recovered anything in ends the report: the
// supplement is working, and a longer time_delay would slow the whole group
// for the few later still. Those few are the residual miss, counted with the
// window as evidence; a window with nothing late ends the report too.
func TestAWindowTheSupplementRecoveredPartOfIsAResidualMissAndEndsTheReport(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, crossed(3))
	lateWindow(f, 660, 1, 60, DirectedSupplemented, crossed(3))
	lateWindow(f, 720, 1, 60, DirectedSupplemented, mixed(4, 1))
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("still reported after a window the supplement recovered part of: %+v", readings)
	}
	lateWindow(f, 780, 1, 60, DirectedSupplemented, mixed(2, 3))
	residual := f.engine.ResidualMisses()
	if len(residual) != 1 || residual[0].Windows != 2 || residual[0].CrossedSeries != 4 || len(residual[0].Samples) != 2 ||
		residual[0].Samples[1].AdmittedSeries != 2 || residual[0].Samples[1].CrossedSeries != 3 {
		t.Fatalf("residual %+v, want two windows, four series not recovered, the latest window last", residual)
	}
	// Recovered everything: not a residual window.
	lateWindow(f, 840, 1, 60, DirectedSupplemented, mixed(5, 0))
	if residual := f.engine.ResidualMisses(); residual[0].Windows != 2 {
		t.Fatalf("a window recovered whole counted: %+v", residual)
	}
	lateWindow(f, 900, 1, 60, DirectedSupplemented, crossed(1))
	lateWindow(f, 960, 1, 60, DirectedNothingLate, nil)
	lateWindow(f, 1020, 1, 60, DirectedSupplemented, crossed(1))
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("a window with nothing late did not end the run: %+v", readings)
	}
}

// A window not read, or read and not supplemented, says nothing: it neither
// adds to a run nor ends one.
func TestAWindowNotSupplementedNeitherAddsToNorEndsTheRun(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 660, 1, 60, DirectedUnobserved, nil)
	lateWindow(f, 720, 1, 60, DirectedFlightBusy, nil)
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("windows not supplemented added to the run: %+v", readings)
	}
	lateWindow(f, 780, 1, 60, DirectedSupplemented, crossed(2))
	if readings := f.engine.LatePastRound(); len(readings) != 1 {
		t.Fatalf("windows not supplemented ended the run: %+v", readings)
	}
}

// Both reports stand while the group is read directed, and go when its
// series are late no more.
func TestTheLateSeriesReportsGoWhenTheGroupIsNoLongerDirected(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 660, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 720, 1, 60, DirectedSupplemented, mixed(1, 1))
	lateWindow(f, 780, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 840, 1, 60, DirectedSupplemented, crossed(2))
	if len(f.engine.LatePastRound()) != 1 || len(f.engine.ResidualMisses()) != 1 {
		t.Fatal("the fixture did not report both")
	}
	// lookback.get reads them beside read_early.
	if stats := f.engine.Stats(); len(stats.LatePastRound) != 1 || len(stats.ResidualMisses) != 1 ||
		stats.ResidualMisses[0].CrossedSeries != 1 {
		t.Fatalf("stats carry %+v and %+v", stats.LatePastRound, stats.ResidualMisses)
	}
	f.engine.mu.Lock()
	f.engine.groups["qg"].seriesLate = nil
	f.engine.mu.Unlock()
	if len(f.engine.LatePastRound()) != 0 || len(f.engine.ResidualMisses()) != 0 {
		t.Fatal("reported after the group stopped being read directed")
	}
}

// A window only some of whose series had crossed their Slots, the rest
// withheld, read incomplete, drifted in configuration, recorded absent or
// not accounted for, was not late alone: it neither adds to a run nor ends
// one, and is no residual miss either, with nothing recovered.
func TestAWindowWhoseSeriesWereNotLateAloneNeitherAddsToNorEndsTheRun(t *testing.T) {
	for name, facts := range map[string]execution.SupplementFacts{
		"withheld":         {Candidates: 10, CrossedT: 1, Withheld: 9},
		"input incomplete": {Candidates: 10, CrossedT: 1, InputIncomplete: 9},
		"config drift":     {Candidates: 10, CrossedT: 1, ConfigDrift: 9},
		"recorded absent":  {Candidates: 10, CrossedT: 1, NoDataFact: 9},
		"not accounted":    {Candidates: 10, CrossedT: 1},
	} {
		other := facts
		f, _ := directedFixture(t, SupplementOutcome{})
		lateWindow(f, 600, 1, 60, DirectedSupplemented, &other)
		lateWindow(f, 660, 1, 60, DirectedSupplemented, &other)
		if readings := f.engine.LatePastRound(); len(readings) != 0 {
			t.Errorf("%s: two such windows reported: %+v", name, readings)
		}
		lateWindow(f, 720, 1, 60, DirectedSupplemented, crossed(2))
		lateWindow(f, 780, 1, 60, DirectedSupplemented, &other)
		lateWindow(f, 840, 1, 60, DirectedSupplemented, crossed(2))
		if readings := f.engine.LatePastRound(); len(readings) != 1 || len(readings[0].Samples) != 2 ||
			readings[0].Samples[0].EvaluationTime != 720 {
			t.Errorf("%s: such a window ended the run or was counted in it: %+v", name, readings)
		}
		if residual := f.engine.ResidualMisses(); len(residual) != 0 {
			t.Errorf("%s: counted as a residual miss with nothing recovered: %+v", name, residual)
		}
	}
}

// A Query Group read directed again after its series were late no more
// starts both reports afresh: nothing from before is reported before a new
// window, and a run needs two new windows in a row.
func TestAGroupReadDirectedAgainCountsItsLateSeriesAfresh(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, mixed(1, 1))
	lateWindow(f, 660, 1, 60, DirectedSupplemented, crossed(2))
	if len(f.engine.ResidualMisses()) != 1 {
		t.Fatal("the fixture did not count a residual miss")
	}
	f.engine.mu.Lock()
	before := f.engine.groups["qg"].latePastRound.since
	f.engine.mu.Unlock()

	lateNoMore(f, recorder)
	lateAgain(f, recorder)
	if residual := f.engine.ResidualMisses(); len(residual) != 0 {
		t.Fatalf("a residual miss from before it was late no more: %+v", residual)
	}
	lateWindow(f, 900, 1, 60, DirectedSupplemented, crossed(2))
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("one window after it was read directed again reported, the run carried: %+v", readings)
	}
	lateWindow(f, 960, 1, 60, DirectedSupplemented, crossed(2))
	readings := f.engine.LatePastRound()
	if len(readings) != 1 || len(readings[0].Samples) != 2 || readings[0].Samples[0].EvaluationTime != 900 ||
		!readings[0].Since.After(before) {
		t.Fatalf("readings %+v, want a run of the two new windows since after %v", readings, before)
	}
}

// Each report keeps the latest windows only, however long it runs: the
// lookback's own bound, newest last.
func TestTheLateSeriesReportsKeepTheirLatestWindowsOnly(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	for window := int64(0); window < latePastRoundKept+2; window++ {
		lateWindow(f, 600+60*window, 1, 60, DirectedSupplemented, crossed(2))
	}
	last := execution.EvaluationTime(600 + 60*(latePastRoundKept+1))
	if readings := f.engine.LatePastRound(); len(readings) != 1 || len(readings[0].Samples) != latePastRoundKept ||
		readings[0].Samples[latePastRoundKept-1].EvaluationTime != last {
		t.Fatalf("readings %+v, want the last %d windows, newest last", readings, latePastRoundKept)
	}
	for window := int64(0); window < residualMissKept+2; window++ {
		lateWindow(f, 1200+60*window, 1, 60, DirectedSupplemented, mixed(1, 1))
	}
	last = execution.EvaluationTime(1200 + 60*(residualMissKept+1))
	if residual := f.engine.ResidualMisses(); len(residual) != 1 || residual[0].Windows != residualMissKept+2 ||
		len(residual[0].Samples) != residualMissKept || residual[0].Samples[residualMissKept-1].EvaluationTime != last {
		t.Fatalf("residual %+v, want every window counted and the last %d kept, newest last", residual, residualMissKept)
	}
}

// The suggestion reads the deepest rung any window's late series were seen
// at, not the latest window's.
func TestTheSuggestionReadsTheDeepestRungOfItsWindows(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 2, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 660, 1, 60, DirectedSupplemented, crossed(2))
	want := int64(60) + int64(rungDelay(2, minute).Seconds())
	want = (want + 59) / 60 * 60
	if readings := f.engine.LatePastRound(); len(readings) != 1 || readings[0].SuggestedDelaySeconds != want {
		t.Fatalf("readings %+v, want the suggestion %d from the deeper, earlier window", readings, want)
	}
}

// A Query Group this replica does not own is another replica's to report.
func TestAGroupAnotherReplicaOwnsIsNotReportedHere(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	lateWindow(f, 600, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 660, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 720, 1, 60, DirectedSupplemented, mixed(1, 1))
	lateWindow(f, 780, 1, 60, DirectedSupplemented, crossed(2))
	lateWindow(f, 840, 1, 60, DirectedSupplemented, crossed(2))
	if len(f.engine.LatePastRound()) != 1 || len(f.engine.ResidualMisses()) != 1 {
		t.Fatal("the fixture did not report both")
	}
	f.engine.options.Owns = func(execution.QueryGroupIdentity) bool { return false }
	if past, residual := f.engine.LatePastRound(), f.engine.ResidualMisses(); len(past) != 0 || len(residual) != 0 {
		t.Fatalf("reported a group it does not own: %+v %+v", past, residual)
	}
}

// directedSample lets the fixture's group take one sample now, its rungs
// reading later, with no supplement to hand late series to.
func directedSample(f *fixture, later func(slot int64) []*execution.Dataset) {
	f.engine.mu.Lock()
	f.engine.groups["qg"].nextAt = f.clock.now()
	f.engine.mu.Unlock()
	f.classSample(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, later)
}

// lateNoMore ends the fixture group's directed reads the way they end: its
// series late no more for seriesLateCleanToEnd samples in a row.
func lateNoMore(f *fixture, recorder *supplementRecorder) {
	f.t.Helper()
	f.engine.options.Supplement = nil
	whole := func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "1")} }
	for round := 0; round < seriesLateCleanToEnd; round++ {
		directedSample(f, whole)
	}
	f.engine.options.Supplement = recorder.run
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	if f.engine.groups["qg"].seriesLate != nil {
		f.t.Fatal("the group is still read directed")
	}
}

// lateAgain has the fixture group read directed again: a sample with a
// series its first read did not have.
func lateAgain(f *fixture, recorder *supplementRecorder) {
	f.t.Helper()
	f.engine.options.Supplement = nil
	directedSample(f, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"})}
	})
	f.engine.options.Supplement = recorder.run
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	if f.engine.groups["qg"].seriesLate == nil {
		f.t.Fatal("the group is not read directed again")
	}
}

// A window in flight when the series were late no more still ends, and is
// counted into neither report: not while the group is not read directed, and
// not into the reports of the reads that come after, whether it ends before
// the group is read directed again or after.
func TestAWindowOpenedInEarlierDirectedReadsIsNotCountedInLaterOnes(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{})
	inFlight := []*directedSlot{openWindow(f, 600, 1, 60), openWindow(f, 660, 1, 60), openWindow(f, 720, 1, 60), openWindow(f, 780, 1, 60)}
	lateNoMore(f, recorder)
	endWindow(f, inFlight[0], DirectedSupplemented, crossed(2))
	endWindow(f, inFlight[1], DirectedSupplemented, mixed(1, 1))
	endWindow(f, inFlight[2], DirectedSupplemented, crossed(2))
	lateAgain(f, recorder)
	if past, residual := f.engine.LatePastRound(), f.engine.ResidualMisses(); len(past) != 0 || len(residual) != 0 {
		t.Fatalf("windows that ended after the reads they were opened in were counted: %+v %+v", past, residual)
	}
	endWindow(f, inFlight[3], DirectedSupplemented, crossed(2))
	lateWindow(f, 900, 1, 60, DirectedSupplemented, crossed(2))
	if readings := f.engine.LatePastRound(); len(readings) != 0 {
		t.Fatalf("an earlier reads' window ending now completed a run with one new window: %+v", readings)
	}
	lateWindow(f, 960, 1, 60, DirectedSupplemented, crossed(2))
	if readings := f.engine.LatePastRound(); len(readings) != 1 || len(readings[0].Samples) != 2 ||
		readings[0].Samples[0].EvaluationTime != 900 {
		t.Fatalf("readings %+v, want a run of the two new windows", readings)
	}
}

// Through the reads themselves: two directed Slots in a row whose late
// series had all crossed their Slots by the supplement report the group.
func TestTwoDirectedSlotsWhoseLateSeriesCrossedReportTheGroup(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, CrossedT: 1}})
	for window := uint64(1); window <= 2; window++ {
		slot := f.clock.now().Unix()
		f.firstRead(slot, execution.CompletenessFull)
		f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
		f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
		f.engine.Step(context.Background())
		f.waitFor(func(stats Stats) bool {
			return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == window
		})
	}
	if readings := f.engine.LatePastRound(); len(readings) != 1 || len(readings[0].Samples) != 2 {
		t.Fatalf("readings %+v, want the group reported from its two Slots", readings)
	}
}

// A sample's on-time and late counts are series, each once, as its directed
// read found them; what the supplement admitted and found crossed are
// (Plan, series) pairs, and a query five Plans share makes five of each.
func TestASamplesLateSeriesAreCountedAsTheReadFoundThem(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 10, Admitted: 5, CrossedT: 5}})
	slot := f.clock.now().Unix()
	f.firstRead(slot, execution.CompletenessFull)
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}), dataset("h3", map[int64]string{slot - 60: "6"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	residual := f.engine.ResidualMisses()
	if len(residual) != 1 || len(residual[0].Samples) != 1 {
		t.Fatalf("residual %+v, want the one window", residual)
	}
	if sample := residual[0].Samples[0]; sample.OnTimeSeries != 1 || sample.LateSeries != 2 || sample.AdmittedSeries != 5 ||
		sample.CrossedSeries != 5 {
		t.Fatalf("sample %+v, want one series on time and two late, beside five pairs admitted and five crossed", sample)
	}
}

// Each sample says how many series its window's first read had on time,
// beside the late ones: a window whose late series outnumber its on-time
// ones is read as late almost whole, without a proportion decided here.
func TestASamplesWindowSaysHowManySeriesWereOnTime(t *testing.T) {
	f, _ := directedFixture(t, SupplementOutcome{})
	onTime := func(evaluation int64, series int) *directedSlot {
		slot := openWindow(f, evaluation, 1, 60)
		slot.queries[0].first = newSeriesSet(nil)
		for digest := 0; digest < series; digest++ {
			slot.queries[0].first.set[uint64(digest)] = struct{}{}
		}
		return slot
	}
	endWindow(f, onTime(600, 4), DirectedSupplemented, mixed(4, 31))
	endWindow(f, onTime(660, 5), DirectedSupplemented, crossed(2))
	endWindow(f, onTime(720, 6), DirectedSupplemented, crossed(2))
	residual, past := f.engine.ResidualMisses(), f.engine.LatePastRound()
	if len(residual) != 1 || residual[0].Samples[0].OnTimeSeries != 4 || residual[0].Samples[0].AdmittedSeries != 4 ||
		residual[0].Samples[0].CrossedSeries != 31 {
		t.Fatalf("residual %+v, want the window's four on time beside four recovered and 31 not", residual)
	}
	if len(past) != 1 || past[0].Samples[0].OnTimeSeries != 5 || past[0].Samples[1].OnTimeSeries != 6 {
		t.Fatalf("late past round %+v, want each window's own on-time count", past)
	}
}
