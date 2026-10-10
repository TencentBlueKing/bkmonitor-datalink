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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// earlyQuery is qg's first read of the Slot at slot, ready at readyAt, whose
// schedule has the Slot after it following seconds on.
func earlyQuery(slot int64, readyAt time.Time, following int64) Query {
	q := query("qg", slot, minute, sourceLog)
	q.ReadyAt = readyAt
	if following > 0 {
		q.FollowingSlot = execution.EvaluationTime(slot + following)
	}
	return q
}

// earlyFirstRead is qg's first read of the Slot at slot with h1 in it, begun
// now and ready now, taking took, the next Slot a minute on; it returns when
// it began.
func (f *fixture) earlyFirstRead(slot int64, took time.Duration) time.Time {
	f.t.Helper()
	return f.firstReadFollowedBy(slot, took, 60)
}

func (f *fixture) firstReadFollowedBy(slot int64, took time.Duration, following int64) time.Time {
	f.t.Helper()
	begun := f.clock.now()
	read := f.engine.Begin(earlyQuery(slot, begun, following))
	if read == nil || read.directed == nil {
		f.t.Fatal("a directed Query Group's first read was not kept for its directed read")
	}
	read.Series(point(slot, "1"), 100)
	f.clock.set(begun.Add(took))
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	return begun
}

// guardedSupplement is a supplement that asks its guard as a Runner does,
// once it would hold the flight, and runs when the guard lets it.
type guardedSupplement struct {
	mu      sync.Mutex
	jobs    []SupplementJob
	outcome SupplementOutcome
	refuse  string
}

func (supplement *guardedSupplement) run(_ context.Context, job SupplementJob) SupplementOutcome {
	supplement.mu.Lock()
	defer supplement.mu.Unlock()
	supplement.jobs = append(supplement.jobs, job)
	if supplement.refuse != "" {
		return SupplementOutcome{Refused: supplement.refuse}
	}
	if job.Guard != nil && !job.Guard() {
		return SupplementOutcome{Refused: SupplementOvertaken, Held: time.Millisecond}
	}
	return supplement.outcome
}

func (supplement *guardedSupplement) taken() []SupplementJob {
	supplement.mu.Lock()
	defer supplement.mu.Unlock()
	return append([]SupplementJob(nil), supplement.jobs...)
}

func earlyFixture(t *testing.T, outcome SupplementOutcome) (*fixture, *guardedSupplement) {
	t.Helper()
	f, _ := directedFixture(t, SupplementOutcome{})
	supplement := &guardedSupplement{outcome: outcome}
	f.engine.options.Supplement = supplement.run
	return f, supplement
}

func (f *fixture) early() map[string]uint64 {
	return f.engine.Stats().Sources[sourceLog].EarlyReads
}

// A directed Slot whose next Slot reads before its rung is read once more
// before that, as late as its lead allows -- its first read's time and no
// supplement hold yet -- and its late series supplemented with the next
// Slot's read as the deadline, guarded: before_next. The read at the rung
// still comes, and finds nothing the early read supplemented.
func TestAnEarlyReadSupplementsBeforeTheNextSlotReads(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Held: 50 * time.Millisecond,
		Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1, Points: 1}})
	slot := f.clock.now().Unix()
	took := 2 * time.Second
	readAt := f.earlyFirstRead(slot, took)
	next := readAt.Add(time.Minute)
	// One early read due at that second, one free permit: it starts a read's
	// time before its latest start.
	start := next.Add(-took).Add(-took)
	if wake := f.engine.nextEarlyWake(); !wake.Equal(start) {
		t.Fatalf("the loop is next needed at %v, want the early read's start %v", wake, start)
	}
	f.clock.set(start.Add(-time.Millisecond))
	f.engine.Step(context.Background())
	if len(supplement.taken()) != 0 {
		t.Fatal("an early read ran before its start")
	}
	f.clock.set(start)
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	jobs := supplement.taken()
	if len(jobs) != 1 || len(jobs[0].Series) != 1 || jobs[0].Series[0] != "digest-h2" || !jobs[0].Deadline.Equal(next) || jobs[0].Guard == nil {
		t.Fatalf("jobs %+v, want the late series supplemented by the next Slot's read, guarded", jobs)
	}

	// The read at the rung: h2 is supplemented already, nothing more is late.
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if len(supplement.taken()) != 1 {
		t.Fatalf("jobs %+v, want the read at the rung to supplement nothing again", supplement.taken())
	}
	source := stats.Sources[sourceLog]
	if source.SupplementSeries[SeriesAdmitted] != 1 || source.SupplementHold["le_100ms"] != 1 {
		t.Fatalf("series %v hold %v, want the early supplement's counted once", source.SupplementSeries, source.SupplementHold)
	}
	reading := stats.Supplements[0]
	if reading.Early[EarlyBeforeNext] != 1 || reading.TookReadings != 1 || reading.HoldReadings != 1 ||
		reading.TookMaxSeconds != took.Seconds() || reading.HoldMaxSeconds != 0.05 || reading.LeadSeconds != took.Seconds()+0.05 {
		t.Fatalf("reading %+v, want the early read counted and the lead its took and hold", reading)
	}
}

// Series arriving after the next Slot's first read are the read at the
// rung's: supplemented then, the Slot filed once on both supplements, its
// late count both reads' together.
func TestTheReadAtTheRungSupplementsWhatArrivedAfterTheEarlyRead(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - 2*time.Second))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}), dataset("h3", map[int64]string{slot - 60: "6"}))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	jobs := supplement.taken()
	if len(jobs) != 2 || len(jobs[1].Series) != 1 || jobs[1].Series[0] != "digest-h3" {
		t.Fatalf("jobs %+v, want the read at the rung to supplement h3 alone", jobs)
	}
	if got := stats.Sources[sourceLog].SupplementSeries[SeriesAdmitted]; got != 2 {
		t.Fatalf("admitted %d, want both supplements' counted", got)
	}
	if reading := stats.Supplements[0]; reading.Windows[DirectedSupplemented] != 1 || reading.Series.Admitted != 2 {
		t.Fatalf("reading %+v, want one window on both supplements", reading)
	}
	if source := stats.Sources[sourceLog]; source.EarlyReadBytes != 200 || source.DirectedReadBytes != 500 {
		t.Fatalf("early bytes %d directed bytes %d, want the early read's 200 apart and within the 500 of both reads",
			source.EarlyReadBytes, source.DirectedReadBytes)
	}
}

// A Slot whose rung comes due in the pass that settles its early read -- a
// loop woken past both -- is read at its rung by the next pass, once the
// early read has its outcome: the window is filed on both reads.
func TestTheReadAtTheRungWaitsForTheEarlyReadsOutcome(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.engine.Step(context.Background())
	f.engine.mu.Lock()
	directed := f.engine.groups["qg"].directed[execution.EvaluationTime(slot)]
	running, early := directed.running, directed.early.outcome
	f.engine.mu.Unlock()
	if running || early != EarlyAnchorPassed {
		t.Fatalf("rung read running %v early %q, want the early read settled and the rung read not begun in that pass", running, early)
	}
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if len(supplement.taken()) != 1 || stats.Supplements[0].Early[EarlyAnchorPassed] != 1 {
		t.Fatalf("jobs %+v reading %+v, want the rung's supplement after the early read filed", supplement.taken(), stats.Supplements[0])
	}
}

// An early read that found late series whose supplement did not run, then
// a read at the rung that found none, is not a Slot with nothing late: it
// is filed as a supplement that did not run, by the early read's why.
func TestARefusedEarlySupplementIsNotNothingLateWhenTheRungFindsNone(t *testing.T) {
	for _, c := range []struct{ refused, early, window string }{
		{DirectedFlightBusy, EarlyFlightBusy, DirectedFlightBusy},
		{DirectedContractExpired, EarlyContractExpired, DirectedContractExpired},
		{SupplementOvertaken, EarlyOvertaken, DirectedContractExpired},
		{"other", EarlyFailed, DirectedFailed},
	} {
		t.Run(c.refused, func(t *testing.T) {
			f, supplement := earlyFixture(t, SupplementOutcome{})
			supplement.refuse = c.refused
			slot := f.clock.now().Unix()
			readAt := f.earlyFirstRead(slot, time.Second)
			f.clock.set(readAt.Add(time.Minute - 2*time.Second))
			f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
			f.engine.Step(context.Background())
			f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[c.early] == 1 })
			f.clock.set(readAt.Add(rungDelay(0, minute)))
			f.answers <- delivered(point(slot, "1"))
			f.engine.Step(context.Background())
			stats := f.waitFor(func(stats Stats) bool {
				windows := stats.Sources[sourceLog].SupplementWindows
				return windows[c.window]+windows[DirectedNothingLate] == 1
			})
			if windows := stats.Sources[sourceLog].SupplementWindows; windows[c.window] != 1 || windows[DirectedNothingLate] != 0 {
				t.Fatalf("windows %v, want the Slot filed %s", windows, c.window)
			}
			if len(supplement.taken()) != 1 {
				t.Fatalf("jobs %+v, want the early supplement alone", supplement.taken())
			}
		})
	}
}

// A Slot whose next Slot reads after its rung -- a strategy that runs daily
// over minute data -- is not read early: its rung comes first.
func TestASlotWhoseNextReadIsAfterItsRungIsNotReadEarly(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
	slot := f.clock.now().Unix()
	readAt := f.firstReadFollowedBy(slot, time.Second, 86400)
	if early := f.early(); early[EarlyRungFirst] != 1 {
		t.Fatalf("early %v, want rung_first", early)
	}
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if jobs := supplement.taken(); len(jobs) != 1 || jobs[0].Guard != nil {
		t.Fatalf("jobs %+v, want the rung's supplement alone, unguarded", jobs)
	}
}

// With no next Slot known the early read cannot be anchored: counted
// anchor_unknown, the Slot read at its rung as before.
func TestASlotWithNoNextSlotKnownIsAnchorUnknown(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	f.firstReadFollowedBy(f.clock.now().Unix(), time.Second, 0)
	if early := f.early(); early[EarlyAnchorUnknown] != 1 {
		t.Fatalf("early %v, want anchor_unknown", early)
	}
}

// The guard is what makes before_next exact: a next Slot that began before
// the supplement held the flight turns it back, overtaken, and nothing is
// merged -- the read at the rung supplements the series again.
func TestAnEarlySupplementTheNextSlotBeatIsOvertaken(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - 2*time.Second))
	// The next Slot's first read begins while the early read reads.
	f.engine.options.Recheck = func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		f.engine.Begin(earlyQuery(slot+60, f.clock.now(), 60))
		return delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))(sink)
	}
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyOvertaken] == 1 })
	f.engine.mu.Lock()
	merged := len(f.engine.groups["qg"].directed[execution.EvaluationTime(slot)].queries[0].first.set)
	f.engine.mu.Unlock()
	if merged != 1 || len(supplement.taken()) != 1 {
		t.Fatalf("first set %d, jobs %d: want nothing merged from an overtaken supplement", merged, len(supplement.taken()))
	}
}

// An early read that cannot start by its latest start is settled by why,
// the first that holds: the next Slot began, a permit was refused, never
// tried.
func TestAnEarlyReadThatDidNotStartIsSettledByWhy(t *testing.T) {
	t.Run("overtaken", func(t *testing.T) {
		f, _ := earlyFixture(t, SupplementOutcome{})
		slot := f.clock.now().Unix()
		readAt := f.earlyFirstRead(slot, time.Second)
		f.engine.Begin(earlyQuery(slot+60, readAt, 60))
		f.clock.set(readAt.Add(time.Minute))
		f.engine.Step(context.Background())
		if early := f.early(); early[EarlyOvertaken] != 1 {
			t.Fatalf("early %v, want overtaken", early)
		}
	})
	t.Run("permit refused", func(t *testing.T) {
		f, _ := earlyFixture(t, SupplementOutcome{})
		slot := f.clock.now().Unix()
		readAt := f.earlyFirstRead(slot, time.Second)
		f.set(func() { f.refuse = "full" })
		start := readAt.Add(time.Minute - 2*time.Second)
		f.clock.set(start)
		f.engine.Step(context.Background())
		// Refused, it tries again a read's time on, not before.
		if wake := f.engine.nextEarlyWake(); !wake.Equal(start.Add(time.Second)) {
			t.Fatalf("the refused read tries again at %v, want %v", wake, start.Add(time.Second))
		}
		f.clock.set(readAt.Add(time.Minute - time.Second + time.Millisecond))
		f.engine.Step(context.Background())
		if early := f.early(); early[EarlyPermitRefused] != 1 {
			t.Fatalf("early %v, want permit_refused", early)
		}
	})
	t.Run("retried in time", func(t *testing.T) {
		f, _ := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
		slot := f.clock.now().Unix()
		readAt := f.firstReadFollowedBy(slot, 4*time.Second, 60)
		f.set(func() { f.refuse = "full" })
		start := readAt.Add(time.Minute - 8*time.Second)
		f.clock.set(start)
		f.engine.Step(context.Background())
		f.set(func() { f.refuse = "" })
		f.clock.set(start.Add(4 * time.Second))
		f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
		f.engine.Step(context.Background())
		f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	})
	t.Run("never tried", func(t *testing.T) {
		f, _ := earlyFixture(t, SupplementOutcome{})
		slot := f.clock.now().Unix()
		readAt := f.earlyFirstRead(slot, time.Second)
		f.clock.set(readAt.Add(time.Minute))
		f.engine.Step(context.Background())
		if early := f.early(); early[EarlyAnchorPassed] != 1 {
			t.Fatalf("early %v, want anchor_passed", early)
		}
	})
}

// A first read longer than its Slot's period leaves no time to read early.
func TestAFirstReadLongerThanThePeriodIsAnchorPassed(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	f.earlyFirstRead(f.clock.now().Unix(), 61*time.Second)
	if early := f.early(); early[EarlyAnchorPassed] != 1 {
		t.Fatalf("early %v, want anchor_passed", early)
	}
}

// An older Slot still to be filed goes first: the early read waits, and is
// older_slot_pending if it is still waiting at its latest start.
func TestAnEarlyReadWaitsForTheOlderSlot(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	older := f.clock.now().Unix()
	olderAt := f.earlyFirstRead(older, time.Second)
	// The older Slot's early read reads and does not come back.
	blocked := make(chan struct{})
	defer close(blocked)
	f.engine.options.Recheck = func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		select {
		case <-blocked:
		case <-ctx.Done():
		}
		return execution.ProviderCompletion{}, context.Canceled
	}
	f.clock.set(olderAt.Add(time.Minute - 2*time.Second))
	f.engine.Step(context.Background())
	f.clock.set(olderAt.Add(time.Minute))
	slot := older + 60
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - time.Second + time.Millisecond))
	f.engine.StepEarly(context.Background())
	if early := f.early(); early[EarlyOlderSlotPending] != 1 {
		t.Fatalf("early %v, want older_slot_pending", early)
	}
}

// A Slot whose early supplement ran is supplemented, whatever its read at
// the rung came to: a read at the rung not made does not lose what the early
// read recovered, and ends a run of Slots whose late series all crossed.
func TestAnEarlySupplementIsKeptWhenTheRungIsNotRead(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1}})
	f.engine.mu.Lock()
	f.engine.groups["qg"].latePastRound = &latePastRoundState{consecutive: 1}
	f.engine.mu.Unlock()
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - 2*time.Second))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	f.clock.set(readAt.Add(rungDelay(0, minute) + rungWindow(0, minute) + time.Second))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if stats.Sources[sourceLog].SupplementWindows[DirectedUnobserved] != 0 {
		t.Fatalf("windows %v, want the Slot filed supplemented only", stats.Sources[sourceLog].SupplementWindows)
	}
	f.engine.mu.Lock()
	run := f.engine.groups["qg"].latePastRound
	f.engine.mu.Unlock()
	if run != nil {
		t.Fatalf("late-past-round run %+v, want it ended by the admitted series", run)
	}
}

// An early supplement refused is not merged: the read at the rung takes the
// same series again. They are one series late, not two, and if they crossed
// they were seen at the early read, not the rung.
func TestARefusedEarlySupplementsSeriesAreTheRungsAndCountedOnce(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{})
	supplement.refuse = DirectedFlightBusy
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	earlyAt := readAt.Add(time.Minute - 2*time.Second)
	f.clock.set(earlyAt)
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyFlightBusy] == 1 })
	supplement.mu.Lock()
	supplement.refuse, supplement.outcome = "", SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, CrossedT: 1}}
	supplement.mu.Unlock()
	f.engine.mu.Lock()
	f.engine.groups["qg"].latePastRound = &latePastRoundState{consecutive: 1}
	f.engine.mu.Unlock()
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if jobs := supplement.taken(); len(jobs) != 2 || jobs[1].Series[0] != "digest-h2" {
		t.Fatalf("jobs %+v, want the rung to supplement h2 again", jobs)
	}
	readings := f.engine.LatePastRound()
	if len(readings) != 1 || len(readings[0].Samples) != 1 {
		t.Fatalf("late past round %+v, want the Slot's sample", readings)
	}
	sample := readings[0].Samples[0]
	if sample.LateSeries != 1 || sample.SeenAgeSeconds != int64(earlyAt.Sub(readAt)/time.Second) {
		t.Fatalf("sample %+v, want one late series seen at the early read (%s)", sample, earlyAt.Sub(readAt))
	}
}

// A series that had crossed the Slot was in the next Slot's read: however
// late the rung that found it, it was seen no later than that read.
func TestACrossedSeriesWasSeenNoLaterThanTheNextSlotsRead(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{})
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - 2*time.Second))
	f.answers <- delivered(point(slot, "1"))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyNothingLate] == 1 })
	supplement.mu.Lock()
	supplement.outcome = SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 1, CrossedT: 1}}
	supplement.mu.Unlock()
	f.engine.mu.Lock()
	f.engine.groups["qg"].latePastRound = &latePastRoundState{consecutive: 1}
	f.engine.mu.Unlock()
	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	readings := f.engine.LatePastRound()
	if len(readings) != 1 || readings[0].Samples[0].SeenAgeSeconds != 60 {
		t.Fatalf("late past round %+v, want the crossed series seen by the next Slot's read, 60s after the first", readings)
	}
}

// When an early read that did not start was both beaten by the next Slot
// and behind an older one, it was beaten: the first that holds.
func TestAMissedEarlyReadBeatenAndBehindIsOvertaken(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	older := f.clock.now().Unix()
	olderAt := f.earlyFirstRead(older, time.Second)
	blocked := make(chan struct{})
	defer close(blocked)
	f.engine.options.Recheck = func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		select {
		case <-blocked:
		case <-ctx.Done():
		}
		return execution.ProviderCompletion{}, context.Canceled
	}
	f.clock.set(olderAt.Add(time.Minute - 2*time.Second))
	f.engine.Step(context.Background())
	f.clock.set(olderAt.Add(time.Minute))
	slot := older + 60
	readAt := f.earlyFirstRead(slot, time.Second)
	f.engine.Begin(earlyQuery(slot+60, readAt, 60))
	f.clock.set(readAt.Add(time.Minute))
	f.engine.StepEarly(context.Background())
	if early := f.early(); early[EarlyOvertaken] != 1 || early[EarlyOlderSlotPending] != 0 {
		t.Fatalf("early %v, want overtaken", early)
	}
}

// An early supplement's undecided pairs are counted: they are not given to
// the read at the rung again.
func TestAnEarlySupplementsUndecidedPairsAreCounted(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{Ran: true, Facts: execution.SupplementFacts{Candidates: 4, Admitted: 1, Withheld: 1,
		InputIncomplete: 1, ConfigDrift: 1}})
	slot := f.clock.now().Unix()
	readAt := f.earlyFirstRead(slot, time.Second)
	f.clock.set(readAt.Add(time.Minute - 2*time.Second))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	if got := stats.Sources[sourceLog].EarlyUndecided; got != 3 {
		t.Fatalf("undecided %d, want the three pairs left undecided", got)
	}
}

// Early reads due at the same second start as many rounds of free permits
// early as they need, a read's time each.
func TestEarlyReadsDueTogetherStartByTheirRounds(t *testing.T) {
	latest := time.Unix(1_700_000_000, 0)
	slots := []*directedSlot{{early: &earlyRead{latest: latest}}, {early: &earlyRead{latest: latest.Add(time.Second)}},
		{early: &earlyRead{latest: latest}}}
	took := map[*directedSlot]time.Duration{slots[0]: time.Second, slots[1]: 3 * time.Second, slots[2]: 2 * time.Second}
	if got := earlyStart(slots, took, 1); !got.Equal(latest.Add(-9 * time.Second)) {
		t.Fatalf("start with one permit %v, want three reads of the longest before the earliest latest", got)
	}
	if got := earlyStart(slots, took, 3); !got.Equal(latest.Add(-3 * time.Second)) {
		t.Fatalf("start with three permits %v, want one round", got)
	}
}

// The lead is the longest of the last readingsKept readings of each kind:
// an older one past them no longer counts.
func TestTheLeadIsTheLongestOfTheLastReadings(t *testing.T) {
	var readings durations
	readings.add(time.Minute)
	for index := 0; index < readingsKept-1; index++ {
		readings.add(time.Second)
	}
	if readings.longest() != time.Minute || readings.count != readingsKept {
		t.Fatalf("longest %v of %d, want the minute among the last %d", readings.longest(), readings.count, readingsKept)
	}
	readings.add(time.Second)
	if readings.longest() != time.Second {
		t.Fatalf("longest %v, want the minute gone once %d more readings came", readings.longest(), readingsKept)
	}
	engine := &Engine{holdMax: 3 * time.Second}
	state := &group{}
	state.took.add(2 * time.Second)
	if lead := engine.leadLocked(state); lead != 5*time.Second {
		t.Fatalf("lead %v, want the first read and the process's longest hold while the group has none", lead)
	}
	state.holds.add(time.Second)
	if lead := engine.leadLocked(state); lead != 3*time.Second {
		t.Fatalf("lead %v, want the group's own hold once it has one", lead)
	}
}
