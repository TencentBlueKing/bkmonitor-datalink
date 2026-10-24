// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// A held Query Group run through the production Worker - its Slots frozen with the hold, dispatched by its Runner,
// executed by access, the lookback stepped as its loop steps it - reserves each earlier-read trial at the Slot's
// first attempt, not a hold later at its readiness retry.
//
// Before, the lookback judged the group's rest at the moment Prepare ran: a rest that ended between a held Slot's
// first attempt and its readiness retry - most of them, on a hold near the period - left the first attempt to skip
// and the retry to reserve the trial a hold past its own candidate point, overtaken before it read. Here that was
// every trial, prepared a whole hold late.
//
// What this does not show is a trial completing. With the data step equal to the period, as a strategy's
// agg_interval makes it, a hold under two steps lowers to no hold and leaves the trial no time before its point,
// and a longer one has its Slot's first attempt behind the previous Slot's held read, past the point. Those are
// the trial's design, not when it is prepared: TestAHeldGroupsTrialOfNoHoldCompletesAndTheHoldComesDown and
// TestAHoldPastThePeriodIsTriedFromThePreviousSlotAndComesDown.
func TestAHeldSlotReservesItsEarlierReadTrialAtItsFirstAttempt(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	engine := f.bundle.dependencies.Lookback
	if engine == nil {
		t.Fatal("fixture: the bundle runs no lookback")
	}
	holds := f.bundle.dependencies.ReadHolds
	// The group measures its data landing a minute late, as the lookback reports a window read early: its hold
	// rises near the period, and its next Slots are frozen held.
	last := f.progress(ctx).LastCompletion
	if last == nil {
		t.Fatal("fixture: no Slot completed")
	}
	if err := holds.controller.Observe(ctx, readhold.Evidence{Contract: last.Contract, ArrivalAge: time.Minute,
		Rung: lookback.RungNames[0], Buckets: []int64{int64(last.Contract.Slot.EvaluationTime)}}); err != nil {
		t.Fatal(err)
	}
	const step = 5 * time.Second
	var hold time.Duration
	trials := func() (prepared uint64, lateMax float64) {
		for _, source := range engine.Stats().Sources {
			for _, count := range source.EarlierReads {
				prepared += count
			}
			lateMax = max(lateMax, source.EarlierPreparedLateMaxSeconds)
		}
		return prepared, lateMax
	}
	for deadline := f.now().Add(3 * time.Hour); f.now().Before(deadline); f.clock.Store(f.now().Add(step).UnixMilli()) {
		if prepared, _ := trials(); prepared >= 3 {
			break
		}
		if !f.runner.NextReadyAt().After(f.now()) {
			if _, _, err := f.runner.RunOne(ctx); err != nil {
				t.Logf("RunOne at %s: %v", f.now().UTC().Format(time.TimeOnly), err)
			}
		}
		engine.StepEarly(ctx)
		hold = max(hold, holds.controller.ReadHold(f.queryGroup))
	}
	prepared, lateMax := trials()
	if hold < 30*time.Second || prepared == 0 {
		t.Fatalf("fixture: hold %v, %d trials; want a held group whose trials are reserved", hold, prepared)
	}
	// Reserved at the first attempt, a trial is late at most by when the Runner got to the Slot - here within the
	// loop's step - never by the hold.
	if lateMax*float64(time.Second) >= float64(hold/2) {
		t.Fatalf("trials reserved up to %.1f s past their candidate point on a %v hold: reserved at the readiness retry", lateMax, hold)
	}
}

// leasesOutliveTheRun gives the fixture's registration, Control Leader and Query Group leases a week: a run of
// hours on the fixture's clock passes in seconds of the wall clock its renewals run on, and a lease that ran out on
// the fixture's clock leaves every Slot after it refused (heldTrialRun fails on that rather than run on).
func leasesOutliveTheRun(cfg *config.Config) {
	week := config.Duration(7 * 24 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationTTL, cfg.PhaseTwo.Ownership.ControlLeaderTTL, cfg.PhaseTwo.Ownership.LeaseTTL = week, week, week
}

// nextFixtureWake is where a run on the fixture's clock moves it next, as the process's loops wake on production's:
// the lookback's next tick after lastStep, the Runner's next Slot due, or the lookback's next early read - a trial's
// or a probe's, started when its start comes, not at the first tick after - whichever comes first.
func nextFixtureWake(f *cutoverStallFixture, engine *lookback.Engine, lastStep time.Time) time.Time {
	now := f.now()
	next := lastStep.Add(lookbackTick)
	if wake := f.runner.NextReadyAt(); wake.After(now) && wake.Before(next) {
		next = wake.Add(time.Millisecond)
	}
	if early := engine.NextEarlyWake(); early.After(now) && early.Before(next) {
		next = early
	}
	return next
}

// heldTrialRun drives a held group's Slots through the production Worker, the clock taken to each Slot's first
// attempt as the dispatcher wakes it and stepped between, the lookback stepped as its loop steps it, for up to
// span or until done says so. The group's data is measured landing arrival late, which raises its hold. It
// returns the group's lowest hold after it was raised, the trials' outcomes, what overtook the overtaken ones,
// and the raised hold.
func heldTrialRun(t *testing.T, f *cutoverStallFixture, arrival, span time.Duration, trialPartial bool,
	done func(elapsed, lowest, raised time.Duration, outcomes map[string]uint64) bool,
) (lowest, raised time.Duration, outcomes, overtaken map[string]uint64, skipped int) {
	t.Helper()
	ctx := context.Background()
	engine := f.bundle.dependencies.Lookback
	holds := f.bundle.dependencies.ReadHolds
	last := f.progress(ctx).LastCompletion
	if engine == nil || last == nil {
		t.Fatal("fixture: no lookback, or no Slot completed")
	}
	// The group measures its data landing arrival late, as the lookback reports a window read early.
	if err := holds.controller.Observe(ctx, readhold.Evidence{Contract: last.Contract, ArrivalAge: arrival,
		Rung: lookback.RungNames[0], Buckets: []int64{int64(last.Contract.Slot.EvaluationTime)}}); err != nil {
		t.Fatal(err)
	}
	raised = holds.controller.ReadHold(f.queryGroup)
	lowest = raised
	// When asked to, a read of a window before its held readiness answers partial - its data was not whole yet -
	// and one at or after it whole: a trial's read comes back incomplete, the formal read complete.
	if trialPartial {
		f.uqPartial = func(end int64) bool { return f.now().Before(time.Unix(end, 0).Add(raised)) }
	}
	immediate := 0
	outcomesNow := func() map[string]uint64 {
		counts := map[string]uint64{}
		for _, source := range engine.Stats().Sources {
			for outcome, count := range source.EarlierReads {
				counts[outcome] += count
			}
		}
		return counts
	}
	started := f.now()
	// The lookback's loop reads the due rechecks once every lookbackTick of
	// the process's clock (Engine.Run), and wakes between for its early reads
	// when the next is due. That loop runs on the wall clock and this one on
	// the fixture's, which leaps, so the run takes those steps itself, at the
	// same moments on its own clock (nextFixtureWake); left to the wall clock,
	// they came only as often as the run paused.
	lastStep := started
	for deadline := started.Add(span); f.now().Before(deadline); {
		if done(f.now().Sub(started), lowest, raised, outcomesNow()) {
			break
		}
		if next := f.runner.NextReadyAt(); !next.After(f.now()) {
			result, _, err := f.runner.RunOne(ctx)
			// Every Slot of the run is run to its end. One that ends in an
			// error - its lease run out on the fixture's clock
			// (ErrStaleFence), or anything else - leaves the rounds after it
			// to pass on what came before.
			if err != nil {
				t.Fatalf("RunOne at %s: %v", f.now().UTC().Format(time.TimeOnly), err)
			}
			// A Slot the scheduler gave up on and skipped unevaluated.
			if result.ReasonCode == "GAP_SKIPPED" {
				skipped++
			}
		}
		if f.now().Sub(lastStep) >= lookbackTick {
			engine.Step(ctx)
			lastStep = f.now()
		}
		engine.StepEarly(ctx)
		// The trial reads on its own goroutine: let it answer before the clock moves.
		testwait.Until(t, "the earlier-read trial answering", func() bool {
			busy, _ := engine.Busy(f.queryGroup)
			return busy.ReadsInFlight == 0
		})
		if hold := holds.controller.ReadHold(f.queryGroup); hold < lowest {
			lowest = hold
		}
		// A Slot due now runs now, as the dispatcher runs it: the clock moves only when none is due, or when the
		// Runner stays due without moving on.
		wake := f.runner.NextReadyAt()
		if !wake.After(f.now()) && immediate < 5 {
			immediate++
			continue
		}
		immediate = 0
		f.clock.Store(nextFixtureWake(f, engine, lastStep).UnixMilli())
	}
	outcomes, overtaken = map[string]uint64{}, map[string]uint64{}
	for _, source := range engine.Stats().Sources {
		for outcome, count := range source.EarlierReads {
			outcomes[outcome] += count
		}
		for path, count := range source.EarlierOvertaken {
			if count > 0 {
				overtaken[path] += count
			}
		}
	}
	t.Logf("hold %v -> lowest %v; trials %v, overtaken by %v; skipped %d", raised, lowest, outcomes, overtaken, skipped)
	return lowest, raised, outcomes, overtaken, skipped
}

// A held group's trial of no hold reads before the Slot's readiness without the hold - the reread bound before
// it - finds the data the formal read later reads, and after the matches and quiet the controller asks for, the
// hold comes down. Before, a trial of no hold was confined to that readiness itself and had no time to read.
func TestAHeldGroupsTrialOfNoHoldCompletesAndTheHoldComesDown(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, nil)
	// Half a minute late: the hold rises under two steps, so a trial tries no hold at all, and short of the
	// period, as the seeds are.
	lowest, raised, outcomes, _, _ := heldTrialRun(t, f, 30*time.Second, 6*time.Hour, false,
		func(_, lowest, raised time.Duration, _ map[string]uint64) bool { return lowest < raised })
	if raised < 20*time.Second {
		t.Fatalf("fixture: hold raised to %v", raised)
	}
	if outcomes[lookback.EarlierEqual] == 0 {
		t.Fatalf("no trial matched its formal read: %v", outcomes)
	}
	if lowest >= raised {
		t.Fatalf("hold stayed at %v (lowest %v) after %d matching trials", raised, lowest, outcomes[lookback.EarlierEqual])
	}
}

// Reading earlier moves only when the trial looks, never what proves a lower hold: a trial whose read comes back
// incomplete supplies no evidence, and the hold does not come down.
func TestAnIncompleteTrialOfNoHoldLeavesTheHold(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, nil)
	// Long enough that the matches and quiet the controller asks for would have lowered it had they counted.
	lowest, raised, outcomes, _, _ := heldTrialRun(t, f, 30*time.Second, 6*time.Hour, true,
		func(elapsed, _, _ time.Duration, outcomes map[string]uint64) bool {
			return elapsed >= 2*time.Hour && outcomes[lookback.EarlierReadFailed] > readhold.EarlierMatchesRequired
		})
	if outcomes[lookback.EarlierReadFailed] == 0 {
		t.Fatalf("fixture: no trial read came back incomplete: %v", outcomes)
	}
	if outcomes[lookback.EarlierEqual] != 0 || lowest < raised {
		t.Fatalf("an incomplete trial lowered the hold %v -> %v: %v", raised, lowest, outcomes)
	}
}

// A hold past the period less the reread bound leaves a Slot's first attempt behind the previous Slot's held read,
// past where its own trial had to start: under two steps, a trial of no hold whose point is the readiness without
// the hold; from two steps, a trial of half the hold. Reserved from the previous Slot's first attempt, the trial
// reads in time, matches the formal read, and the hold comes down; none is prepared after its point.
func TestAHoldPastThePeriodIsTriedFromThePreviousSlotAndComesDown(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		arrival time.Duration
	}{
		{"under two steps", 100 * time.Second},
		{"two steps and more", 160 * time.Second},
		// Past twice the period less the reread bound: the next Slot's trial is
		// past its start too, and the trial two Slots ahead is reserved.
		{"just past one Slot ahead", 118 * time.Second},
		{"five periods", 300500 * time.Millisecond},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := startCutoverFixture(t, nil)
			// Run on half an hour past the first lowering: the Slots frozen under the lowered hold run too.
			loweredAt := time.Duration(-1)
			lowest, raised, outcomes, overtaken, skipped := heldTrialRun(t, f, c.arrival, 6*time.Hour, false,
				func(elapsed, lowest, raised time.Duration, _ map[string]uint64) bool {
					if lowest < raised && loweredAt < 0 {
						loweredAt = elapsed
					}
					return loweredAt >= 0 && elapsed >= loweredAt+30*time.Minute
				})
			if raised <= time.Minute-lookback.RecheckTimeout {
				t.Fatalf("fixture: hold raised to %v, want past the period less the reread bound", raised)
			}
			if outcomes[lookback.EarlierEqual] == 0 {
				t.Fatalf("no trial matched its formal read: %v, overtaken by %v", outcomes, overtaken)
			}
			if lowest >= raised {
				t.Fatalf("hold stayed at %v after %d matching trials", raised, outcomes[lookback.EarlierEqual])
			}
			if overtaken[lookback.OvertakenPreparedLate] != 0 {
				t.Fatalf("%d trials prepared past their point", overtaken[lookback.OvertakenPreparedLate])
			}
			// Lowering the hold never costs a Slot: none is skipped unevaluated.
			if skipped != 0 {
				t.Fatalf("%d Slots skipped unevaluated as the hold came down from %v to %v", skipped, raised, lowest)
			}
		})
	}
}

// A group measured past its hold limit is held at the limit and tried no lower: its data arrives past the longest
// hold it may have, so a trial and the formal read would both come before it. Each trial not run is counted
// at_limit. Once an arrival is measured within the limit the trials resume - reserved as far ahead as a hold at
// the limit needs - and the hold comes down a step per Slot, skipping none.
func TestAHoldAtItsLimitIsTriedOnlyOnceAnArrivalIsMeasuredWithinIt(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, nil)
	engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
	lowest, raised, outcomes, _, skipped := heldTrialRun(t, f, 700*time.Second, 20*time.Minute, false,
		func(time.Duration, time.Duration, time.Duration, map[string]uint64) bool { return false })
	record, _ := holds.controller.Reading(f.queryGroup)
	if !record.HeldAtLimit() {
		t.Fatalf("fixture: a 700 s arrival left the group not held at its limit: %+v", record)
	}
	tried := uint64(0)
	for _, count := range outcomes {
		tried += count
	}
	if lowest != raised || tried != 0 || skipped != 0 {
		t.Fatalf("held at the limit: hold %v -> %v, trials %v, %d Slots skipped; want no trial and the hold kept", raised, lowest, outcomes, skipped)
	}
	if got := productionLookbackStats(engine, holds).ReadHoldEarlierSkipped[readhold.EarlierSkipAtLimit]; got == 0 {
		t.Fatal("no trial counted skipped at the limit")
	}
	// An arrival within the limit: the hold is not raised by it, and is no longer held at the limit.
	within := time.Duration(record.LimitMillis) * time.Millisecond
	loweredAt := time.Duration(-1)
	lowest, raised, outcomes, overtaken, skipped := heldTrialRun(t, f, within, 6*time.Hour, false,
		func(elapsed, lowest, raised time.Duration, _ map[string]uint64) bool {
			if lowest < raised && loweredAt < 0 {
				loweredAt = elapsed
			}
			return loweredAt >= 0 && elapsed >= loweredAt+30*time.Minute
		})
	if outcomes[lookback.EarlierEqual] == 0 || lowest >= raised {
		t.Fatalf("an arrival within the limit left the hold at %v -> %v: trials %v, overtaken by %v", raised, lowest, outcomes, overtaken)
	}
	if overtaken[lookback.OvertakenPreparedLate] != 0 || skipped != 0 {
		t.Fatalf("%d trials prepared past their point, %d Slots skipped", overtaken[lookback.OvertakenPreparedLate], skipped)
	}
	deep := uint64(0)
	for _, source := range engine.Stats().Sources {
		deep += source.EarlierAhead["4+"]
	}
	if deep == 0 {
		t.Fatal("fixture: no trial reserved four or more Slots ahead from the hold limit")
	}
}

// Reserved from the previous Slot, a trial still proves nothing when its read comes back incomplete: the hold
// does not come down.
func TestAnIncompleteTrialFromThePreviousSlotLeavesTheHold(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, nil)
	lowest, raised, outcomes, _, _ := heldTrialRun(t, f, 100*time.Second, 6*time.Hour, true,
		func(elapsed, _, _ time.Duration, outcomes map[string]uint64) bool {
			return elapsed >= 3*time.Hour && outcomes[lookback.EarlierReadFailed] > readhold.EarlierMatchesRequired
		})
	if outcomes[lookback.EarlierReadFailed] == 0 {
		t.Fatalf("fixture: no trial read came back incomplete: %v", outcomes)
	}
	if outcomes[lookback.EarlierEqual] != 0 || lowest < raised {
		t.Fatalf("an incomplete trial lowered the hold %v -> %v: %v", raised, lowest, outcomes)
	}
}

// A group lowered under its search floor - a raise right after a lowering makes the lowered hold the floor, and
// halfway between it and the hold leaves a gap no wider than a step - is probed at halves between the floor and its
// hold, none of those Slots refused, each passed half its hold, until the hold is within readhold.MidpointResolution
// of the floor: 48 s comes down to 24, 12, 6 and 3 s over a floor of none - a floor a probe failed at, the lowering
// to it verified, so 6 and 3 s, within readhold.EdgeProbeReach of it, each read eight hours. There, its floor not yet
// stood a day, it has nothing left to try, and says so: the controller counts each trial it refuses search_done, the group's
// lookback reading counts the same Slots no_candidate, and its record says when the floor was learned. In one
// deployment four groups lowered so sat at 46-58 s for 57 hours, and the only reason counted was at_limit, another
// group's.
func TestAGroupStuckUnderItsSearchFloorSaysWhy(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, leasesOutliveTheRun)
	engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
	refused := func() map[string]uint64 { return productionLookbackStats(engine, holds).ReadHoldEarlierSkipped }
	const arrival = 96500 * time.Millisecond
	// Raised, then lowered to no hold by its trials of no hold: a candidate every time, none refused.
	if lowest, raised, outcomes, overtaken, _ := heldTrialRun(t, f, arrival, 12*time.Hour, false,
		func(_, lowest, _ time.Duration, _ map[string]uint64) bool { return lowest == 0 }); lowest != 0 {
		t.Fatalf("fixture: the hold %v never came down to none: trials %v, overtaken by %v", raised, outcomes, overtaken)
	}
	if got := refused(); got[readhold.EarlierSkipSearchDone] != 0 {
		t.Fatalf("trials refused search_done while the search had candidates: %v", got)
	}
	// Its data lands late again right after that lowering: the hold goes back to where it came from, and none,
	// the lowered hold, is the floor, learned now. Halfway is the step left; there the gap is under the minute.
	learnedAt := f.now()
	steppedAt := time.Duration(-1)
	lowest, raised, outcomes, overtaken, _ := heldTrialRun(t, f, arrival, 12*time.Hour, false,
		func(elapsed, lowest, raised time.Duration, _ map[string]uint64) bool {
			if steppedAt < 0 && lowest < raised {
				steppedAt = elapsed
			}
			return steppedAt >= 0 && elapsed >= steppedAt+10*time.Minute
		})
	if steppedAt < 0 || lowest != raised/2 {
		t.Fatalf("fixture: from %v the hold came to %v, want halfway to the floor of none: trials %v, overtaken by %v",
			raised, lowest, outcomes, overtaken)
	}
	if got := refused(); got[readhold.EarlierSkipSearchDone] != 0 {
		t.Fatalf("Slots refused search_done while their group's half was probed: %v", got)
	}
	// The record says when the floor was learned: at the raise.
	if record, _ := holds.controller.Reading(f.queryGroup); record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 0 ||
		record.EarlierFloorAtMillis != learnedAt.UnixMilli() || !record.EarlierFloorProbed {
		t.Fatalf("the record's floor %v learned at %d probed %v, want none, learned at the raise %d, a floor a probe failed at",
			record.EarlierFloorMillis, record.EarlierFloorAtMillis, record.EarlierFloorProbed, learnedAt.UnixMilli())
	}
	// The probes near a floor a probe failed at read eight hours each, most of a day: the floor is dated afresh
	// here, so it stands its day from the descent's start and the group sits stuck under it for what is left.
	ageFloor(t, f, 0)
	learnedAt = f.now()
	came, _, _ := descent(t, f, 30*time.Hour)
	want := []time.Duration{48 * time.Second, 24 * time.Second, 12 * time.Second, 6 * time.Second, 3 * time.Second}
	if fmt.Sprint(came) != fmt.Sprint(want) {
		t.Fatalf("from %v the hold came down through %v, want %v", lowest, came, want)
	}
	// Stuck there until the floor has stood a day, which the probes near it took most of.
	stuck := min(3*time.Hour, readhold.FloorStanding-f.now().Sub(learnedAt)-30*time.Minute)
	if stuck < time.Hour {
		t.Fatalf("fixture: the floor stands a day %v after the descent, want an hour at least", stuck+30*time.Minute)
	}
	if skipped := heldRounds(t, f, stuck+time.Minute, func(elapsed time.Duration) bool { return elapsed >= stuck }); skipped != 0 {
		t.Fatalf("%d Slots skipped unevaluated under the floor", skipped)
	}
	if hold := holds.controller.ReadHold(f.queryGroup); hold != want[len(want)-1] || f.now().Sub(learnedAt) >= readhold.FloorStanding {
		t.Fatalf("%v on: hold %v, the floor learned %v before; want %v, the floor not yet stood %v",
			stuck, hold, f.now().Sub(learnedAt), want[len(want)-1], readhold.FloorStanding)
	}
	record, _ := holds.controller.Reading(f.queryGroup)
	if record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 0 || record.EarlierFloorAtMillis != learnedAt.UnixMilli() {
		t.Fatalf("the record's floor %v learned at %d, want none, dated at the descent's start %d", record.EarlierFloorMillis,
			record.EarlierFloorAtMillis, learnedAt.UnixMilli())
	}
	reading, _ := engine.GroupReading(f.queryGroup)
	got := refused()
	if got[readhold.EarlierSkipSearchDone] == 0 || got[readhold.EarlierSkipAtLimit] != 0 {
		t.Fatalf("%v stuck under its floor: refusals counted %v, want search_done alone", stuck, got)
	}
	// One Slot, one count, on both sides: the controller hears exactly the Slots the lookback counts.
	if noCandidate := reading.EarlierSkipped[lookback.EarlierSkipNoCandidate]; noCandidate != got[readhold.EarlierSkipSearchDone] {
		t.Fatalf("the group's reading counts %d Slots no_candidate, the controller %d search_done", noCandidate, got[readhold.EarlierSkipSearchDone])
	}
}

// A probation that finds a lowered group's round read early undoes the lowering, whatever arrived: here a series the
// first read had is gone by the probation's reread - no whole window arriving to read an age from - and the hold goes
// back where the trials lowered it from, the lowered hold the floor, and no trial lowers it again while the group is
// not quiet for its bar. Left as the data was, the same rounds leave the hold where the trials put it. In one
// deployment such a finding reached no one: the hold stayed lowered and the trials went on lowering it.
func TestAProbationFindingUndoesALoweringFromTheProductionWiring(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "the data as it was", true: "a series gone by the reread"}[gone], func(t *testing.T) {
			t.Parallel()
			var output lockedLogBuffer
			f := startCutoverFixtureWith(t, leasesOutliveTheRun, observability.New(observability.ComponentRuntime, &output), nil)
			holds := f.bundle.dependencies.ReadHolds
			if lowest, raised, outcomes, overtaken, _ := heldTrialRun(t, f, 96500*time.Millisecond, 12*time.Hour, false,
				func(_, lowest, _ time.Duration, _ map[string]uint64) bool { return lowest == 0 }); lowest != 0 {
				t.Fatalf("fixture: the hold %v never came down to none: trials %v, overtaken by %v", raised, outcomes, overtaken)
			}
			before := len(readEarlyLines(output.String()))
			lowered, _ := holds.controller.Reading(f.queryGroup)
			if !lowered.Lowered || lowered.LoweredFromMillis <= 0 {
				t.Fatalf("fixture: the record %+v is not lowered from a hold", lowered)
			}
			from := time.Duration(lowered.LoweredFromMillis) * time.Millisecond
			if gone {
				var since atomic.Int64
				since.Store(f.now().Unix())
				f.uqHosts = func(end int64) []string {
					if end > since.Load() && f.now().Before(time.Unix(end, 0).Add(time.Minute)) {
						return []string{"127.0.0.1", "127.0.0.2"}
					}
					return []string{"127.0.0.1"}
				}
			}
			heldRounds(t, f, 51*time.Minute, func(elapsed time.Duration) bool { return elapsed >= 50*time.Minute })
			record, _ := holds.controller.Reading(f.queryGroup)
			hold := holds.controller.ReadHold(f.queryGroup)
			findings := holds.controller.Stats().ProbationFindings
			lines := readEarlyLines(output.String())[before:]
			if !gone {
				if hold != 0 || findings[readhold.ProbationFindingUndone]+findings[readhold.ProbationFindingQuiet] != 0 || len(lines) != 0 {
					t.Fatalf("the data as it was: hold %v, probation findings %v, read-early lines %q; want it left at none, and none",
						hold, findings, lines)
				}
				return
			}
			if findings[readhold.ProbationFindingUndone] != 1 {
				t.Fatalf("a series gone by the reread: probation findings %v, want the lowering undone once", findings)
			}
			// Every round it caught - each of the rereads in flight found the series gone - is a line of its own, with
			// what that round decided as the worker reported it; the first was read under the lowering it undid.
			if len(lines) == 0 || !strings.Contains(lines[0], fmt.Sprintf(`"lowered_from_ms":%d`, from.Milliseconds())) {
				t.Fatalf("read-early lines %q, want the probation's, the first lowered from %v", lines, from)
			}
			for _, line := range lines {
				for _, field := range []string{`"path":"probation"`, `"read_hold_ms":0`, `"points_removed":1`, `"decision_summary":"kept"`,
					`"completed":true`, `"strategies_from":"decision"`, `"strategy_count":1`} {
					if !strings.Contains(line, field) {
						t.Errorf("the read-early line %s lacks %s", line, field)
					}
				}
			}
			if hold < from || record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 0 || record.Lowered {
				t.Fatalf("a series gone by the reread: hold %v, floor %v, lowered %v; want back to %v at least over a floor of none",
					hold, record.EarlierFloorMillis, record.Lowered, from)
			}
		})
	}
}

// readEarlyLines is the window_read_early lines of a production log.
func readEarlyLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, `"window_read_early"`) {
			lines = append(lines, line)
		}
	}
	return lines
}
