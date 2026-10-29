// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// stuckUnderFloor brings the fixture's group where the stuck groups on the test deployment were: its data measured
// landing arrival late, its trials choose no hold and a probe verifies it, lowering its hold to none, its data lands
// late again right after - the hold goes back to where it came from and none is its search's floor, one a probe
// failed at - and its trials lower it, verified, halfway to that floor, where the gap is no wider than the step: the
// search has run out. It returns the hold it is stuck at.
func stuckUnderFloor(t *testing.T, f *cutoverStallFixture, arrival time.Duration) time.Duration {
	t.Helper()
	holds := f.bundle.dependencies.ReadHolds
	if lowest, raised, outcomes, overtaken, _ := heldTrialRun(t, f, arrival, 12*time.Hour, false,
		func(_, lowest, _ time.Duration, _ map[string]uint64) bool { return lowest == 0 }); lowest != 0 {
		t.Fatalf("fixture: the hold %v never came down to none: trials %v, overtaken by %v", raised, outcomes, overtaken)
	}
	steppedAt := time.Duration(-1)
	_, raised, outcomes, overtaken, _ := heldTrialRun(t, f, arrival, 12*time.Hour, false,
		func(elapsed, lowest, raised time.Duration, _ map[string]uint64) bool {
			if steppedAt < 0 && lowest < raised {
				steppedAt = elapsed
			}
			return steppedAt >= 0 && elapsed >= steppedAt+10*time.Minute
		})
	hold := holds.controller.ReadHold(f.queryGroup)
	if candidate, tries := holds.controller.EarlierCandidate(f.queryGroup); tries || hold != raised/2 {
		t.Fatalf("fixture: from %v the hold came to %v, candidate %v %v; want stuck halfway to a floor of none: trials %v, overtaken by %v",
			raised, hold, candidate, tries, outcomes, overtaken)
	}
	return hold
}

// ageFloor moves the date the group's floor was learned at back by age, in the record the controller keeps, and has
// the controller read it again: a day of standing is a day of Slots the fixture would run one by one, and the
// standing reads nothing of them but that date.
func ageFloor(t *testing.T, f *cutoverStallFixture, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	key := productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(f.queryGroup) + "}:" +
		productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	raw, err := f.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	record := map[string]any{}
	if err := decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	if _, dated := record["earlier_floor_at_ms"]; !dated {
		t.Fatalf("fixture: the record %s has no dated floor", raw)
	}
	record["earlier_floor_at_ms"] = f.now().Add(-age).UnixMilli()
	aged, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	// The record's own expiry kept, set again from what is left of it: SET's KEEPTTL is Redis 6's.
	left, err := f.redisClient.PTTL(ctx, key).Result()
	if err != nil || left <= 0 {
		t.Fatalf("fixture: the record's expiry %v %v, want one set", left, err)
	}
	if err := f.redisClient.Set(ctx, key, aged, left).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.bundle.dependencies.ReadHolds.controller.RestoreBatch(ctx, []execution.QueryGroupIdentity{f.queryGroup}); err != nil {
		t.Fatal(err)
	}
}

// heldRounds runs the fixture's group as heldTrialRun does, without raising its hold first: each Slot when the
// dispatcher would run it, each early read when its start comes, started and answered before the clock moves, and
// the lookback's due rechecks read every lookbackTick of the fixture's clock, as its own loop reads them on
// production's. It stops
// when done says so or after span, and returns the Slots skipped unevaluated.
func heldRounds(t *testing.T, f *cutoverStallFixture, span time.Duration, done func(elapsed time.Duration) bool) (skipped int) {
	t.Helper()
	ctx := context.Background()
	engine := f.bundle.dependencies.Lookback
	immediate := 0
	started := f.now()
	lastStep := started
	for deadline := started.Add(span); f.now().Before(deadline); {
		if done(f.now().Sub(started)) {
			return skipped
		}
		if !f.runner.NextReadyAt().After(f.now()) {
			result, _, err := f.runner.RunOne(ctx)
			// Every Slot of the run is run to its end. One that ends in an
			// error - its lease run out on the fixture's clock
			// (ErrStaleFence), or anything else - leaves the rounds after it
			// to pass on what came before.
			if err != nil {
				t.Fatalf("RunOne at %s: %v", f.now().UTC().Format(time.TimeOnly), err)
			}
			if result.ReasonCode == "GAP_SKIPPED" {
				skipped++
			}
		}
		if f.now().Sub(lastStep) >= lookbackTick {
			engine.Step(ctx)
			lastStep = f.now()
		}
		engine.StepEarly(ctx)
		testwait.Until(t, "the early reads started answering", func() bool {
			busy, _ := engine.Busy(f.queryGroup)
			return busy.ReadsInFlight == 0
		})
		wake := f.runner.NextReadyAt()
		if !wake.After(f.now()) && immediate < 5 {
			immediate++
			continue
		}
		immediate = 0
		f.clock.Store(nextFixtureWake(f, engine, lastStep).UnixMilli())
	}
	t.Fatalf("the run did not end within %v", span)
	return skipped
}

// probeCounts is the probes' events and rounds on the fixture's lookback, summed over its sources, only those that
// happened.
func probeCounts(engine *lookback.Engine) (events, rounds map[string]uint64) {
	events, rounds = map[string]uint64{}, map[string]uint64{}
	for _, source := range engine.Stats().Sources {
		for event, count := range source.FloorProbes {
			if count > 0 {
				events[event] += count
			}
		}
		for outcome, count := range source.FloorProbeRounds {
			if count > 0 {
				rounds[outcome] += count
			}
		}
	}
	return events, rounds
}

// probeEventsOf is the events of the fixture's probes of one kind, summed over its sources, only those that happened.
func probeEventsOf(engine *lookback.Engine, kind string) map[string]uint64 {
	events := map[string]uint64{}
	for _, source := range engine.Stats().Sources {
		for event, count := range source.FloorProbesByKind[kind] {
			if count > 0 {
				events[event] += count
			}
		}
	}
	return events
}

func probationRereads(engine *lookback.Engine) uint64 {
	total := uint64(0)
	for _, source := range engine.Stats().Sources {
		for _, count := range source.ProbationRereads {
			total += count
		}
	}
	return total
}

// descent runs the fixture's group while its probes go on, until its hold has stayed put for a probe's worth of
// rounds after the last one ended or span runs out, and fails the moment its hold moves without a probe having passed
// just before: every lowering is a verified one, and nothing else moves the hold. It returns the holds it came down
// through, the Slots skipped, and the probation rereads there had been when the hold first came down, those of rounds
// before it still being read among them: a lowering ends the probation, not the rereads it already took.
func descent(t *testing.T, f *cutoverStallFixture, span time.Duration) (holds []time.Duration, skipped int, reread uint64) {
	t.Helper()
	engine, controller := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds.controller
	holds = []time.Duration{controller.ReadHold(f.queryGroup)}
	passed, settled := uint64(0), time.Duration(0)
	skipped = heldRounds(t, f, span, func(elapsed time.Duration) bool {
		events, _ := probeCounts(engine)
		if now := controller.ReadHold(f.queryGroup); now != holds[len(holds)-1] {
			if now > holds[len(holds)-1] || events[lookback.FloorProbePassed] == passed {
				t.Fatalf("the hold moved from %v to %v with no probe passed since", holds[len(holds)-1], now)
			}
			if len(holds) == 1 {
				busy, _ := engine.Busy(f.queryGroup)
				reread = probationRereads(engine) + uint64(busy.ProbationRereads)
			}
			holds, passed, settled = append(holds, now), events[lookback.FloorProbePassed], elapsed
		}
		_, _, _, probing := controller.FloorProbe(f.queryGroup)
		groups := uint64(0)
		for _, source := range engine.Stats().Sources {
			groups += source.FloorProbeGroups
		}
		return !probing && groups == 0 && elapsed >= settled+30*time.Minute
	})
	return holds, skipped, reread
}

// A group stuck above a floor of none, its floor stood a day, its data whole whenever it is read, is probed at halves
// between its floor and its hold, each read beside the formal read at the hold for lookback.ProbationRounds rounds
// before the hold comes down to it: 48 s comes down to 24, 12, 6 and 3 s, and then, the floor near enough, to the
// floor itself. The floor of none is one a probe failed at - the verified lowering to none was raised right after
// (stuckUnderFloor) - so the halves within readhold.EdgeProbeReach above it, 6 and 3 s, and the floor itself each
// read lookback.EdgeProbeSpan of their minute's rounds first. Until each probe passed every round was frozen at the hold before it - none decided on a read at the
// probe's hold - and the probation that follows each lowering, as it follows every one, reads none of its rounds
// early. A hold past the period less the reread bound has each Slot's round reserved at the Slot before it, and comes
// down the same way. No Slot is skipped on the way.
func TestAStuckGroupsFloorIsProbedAndItsHoldComesDownWithNoRoundReadEarly(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		arrival time.Duration
		holds   []time.Duration
	}{
		{"under the period less the reread bound", 96500 * time.Millisecond,
			[]time.Duration{48 * time.Second, 24 * time.Second, 12 * time.Second, 6 * time.Second, 3 * time.Second, 0}},
		{"past it, rounds reserved a Slot ahead", 117500 * time.Millisecond,
			[]time.Duration{58500 * time.Millisecond, 29250 * time.Millisecond, 14625 * time.Millisecond, 7312 * time.Millisecond, 3656 * time.Millisecond, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := startCutoverFixture(t, leasesOutliveTheRun)
			engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
			hold := stuckUnderFloor(t, f, c.arrival)
			if hold != c.holds[0] {
				t.Fatalf("fixture: stuck at %v, want %v", hold, c.holds[0])
			}
			ageFloor(t, f, readhold.FloorStanding)
			came, skipped, reread := descent(t, f, 40*time.Hour)
			events, rounds := probeCounts(engine)
			t.Logf("held %v: came down through %v; probe events %v, rounds %v", hold, came, events, rounds)
			if fmt.Sprint(came) != fmt.Sprint(c.holds) {
				t.Fatalf("the hold came down through %v, want %v", came, c.holds)
			}
			// The halves and the floor; the lowerings that brought the group to its hold were the trials' candidates,
			// verified.
			lowerings := uint64(len(c.holds) - 1)
			passed := probeEventsOf(engine, "midpoint")[lookback.FloorProbePassed] + probeEventsOf(engine, "floor")[lookback.FloorProbePassed]
			byKind := holds.controller.Stats().FloorProbesByKind
			lowered := byKind[readhold.FloorProbeKindMidpoint][readhold.FloorProbeLowered] + byKind[readhold.FloorProbeKindFloor][readhold.FloorProbeLowered]
			if passed != lowerings || events[lookback.FloorProbeFailed] != 0 || lowered != lowerings {
				t.Fatalf("probe events %v, controller %v; want %d midpoints and the floor passed and lowered", events, byKind, lowerings)
			}
			edge := uint64(lookback.EdgeProbeSpan / time.Minute)
			if want := 2*uint64(lookback.ProbationRounds) + 3*edge; rounds[lookback.FloorProbeClean] < want || rounds[lookback.FloorProbeDifferent] != 0 {
				t.Fatalf("probe rounds %v, want at least %d clean - 24 and 12 s on ProbationRounds, 6 and 3 s and the floor on "+
					"eight hours each - and none different", rounds, want)
			}
			early, caught := uint64(0), uint64(0)
			for _, source := range engine.Stats().Sources {
				early += source.ProbationRereads[lookback.ProbationWindowReadEarly]
				for _, count := range source.ProbationCaught {
					caught += count
				}
			}
			if probationRereads(engine) == reread || early != 0 || caught != 0 || !holds.controller.LoweringVerified(f.queryGroup) {
				t.Fatalf("%d rounds reread in probation since the hold first came down, %d read early, %d caught, verified %v; "+
					"want each lowering's probation reading none early, verified", probationRereads(engine)-reread, early, caught,
					holds.controller.LoweringVerified(f.queryGroup))
			}
			if skipped != 0 {
				t.Fatalf("%d Slots skipped unevaluated as the hold came down from %v", skipped, hold)
			}
		})
	}
}

// A group whose data lands late within its window - one of its series, every fifth round, twenty seconds after the
// window - is probed at halves from its 48 s hold over a floor of none. A probe's read at a half starts
// lookback.RecheckTimeout before the half's readiness, as every earlier read does, and the fixture's readiness is
// under a second past the window: a half under 25 s is read before the series is there and fails on that round - the
// point the formal read had and the probe's read did not (points_added) - and is the floor; a half from 25 s is read
// after it, passes, and is the hold. 24 s fails; 36, 30 and 27 s pass; 27 s is within readhold.MidpointResolution of
// 24 s. The hold comes down through 48, 36, 30 and 27 s, every lowering a verified one, over a floor of 24 s. A probe
// failed at that floor, so the halves within readhold.EdgeProbeReach above it, 30 and 27 s, each read
// lookback.EdgeProbeSpan of their minute's rounds clean before they pass, and 36 s, further up, lookback.ProbationRounds
// alone.
func TestAProbeComesDownToWhereItsDataIsWholeAndNoFurther(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, leasesOutliveTheRun)
	// Late only in windows that end after it starts being: a probe already begun - a half needs no standing - reads
	// no round across that change, its read at the floor before it and the formal read after. Every fifth window is
	// counted from the fixture's first Slot, not the wall clock's minute, so the run is the same whichever minute it
	// starts in.
	var lateFrom atomic.Int64
	lateFrom.Store(math.MaxInt64)
	f.uqHosts = func(end int64) []string {
		if end > lateFrom.Load() && (end-f.base)%300 == 0 && !f.now().Before(time.Unix(end, 0).Add(20*time.Second)) {
			return []string{"127.0.0.1", "127.0.0.2"}
		}
		return []string{"127.0.0.1"}
	}
	engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
	hold := stuckUnderFloor(t, f, 96500*time.Millisecond)
	ageFloor(t, f, readhold.FloorStanding)
	lateFrom.Store(f.now().Unix())
	came, skipped, _ := descent(t, f, 24*time.Hour)
	events, rounds := probeCounts(engine)
	changes := map[string]uint64{}
	for _, source := range engine.Stats().Sources {
		for change, count := range source.FloorProbeChanges {
			if count > 0 {
				changes[change] += count
			}
		}
	}
	record, _ := holds.controller.Reading(f.queryGroup)
	floor := int64(-1)
	if record.EarlierFloorMillis != nil {
		floor = *record.EarlierFloorMillis
	}
	t.Logf("first Slot %d (%d s into five minutes), held %v: came down through %v, floor %d ms; probe events %v, rounds %v, changes %v",
		f.base, f.base%300, hold, came, floor, events, rounds, changes)
	want := []time.Duration{48 * time.Second, 36 * time.Second, 30 * time.Second, 27 * time.Second}
	if fmt.Sprint(came) != fmt.Sprint(want) || floor != 24000 {
		t.Fatalf("came down through %v to a floor of %d ms; want %v, to a floor of 24000 ms", came, floor, want)
	}
	// The halves are midpoints; the lowerings that brought the group to 48 s were the trials' candidates, verified.
	midpoints := probeEventsOf(engine, "midpoint")
	if holds.controller.Stats().FloorProbesByKind[readhold.FloorProbeKindMidpoint][readhold.FloorProbeFloorRaised] != 1 ||
		midpoints[lookback.FloorProbeFailed] != 1 || midpoints[lookback.FloorProbePassed] != 3 || changes[lookback.ChangePointsAdded] != 1 {
		t.Fatalf("controller %v, midpoint probe events %v, changes %v; want the half at 24 s failed on the late series' point, raising the floor, and the three above it passed",
			holds.controller.Stats().FloorProbesByKind, midpoints, changes)
	}
	if near := uint64(lookback.ProbationRounds + 2*int(lookback.EdgeProbeSpan/time.Minute)); rounds[lookback.FloorProbeClean] < near {
		t.Fatalf("%d clean probe rounds, want at least %d: 36 s on ProbationRounds, 30 and 27 s on eight hours of a minute's rounds each",
			rounds[lookback.FloorProbeClean], near)
	}
	if skipped != 0 || !holds.controller.LoweringVerified(f.queryGroup) {
		t.Fatalf("%d Slots skipped, verified %v", skipped, holds.controller.LoweringVerified(f.queryGroup))
	}
}

// A probe's clean rounds are kept in its group's record every lookback.FloorProbeProgressEvery of them, and a group
// that leaves the process and comes back - a takeover, a rollout - has its probe go on from what was kept, from the
// Slot it started at, not from none. The rounds read after the last kept ones are read again: the safe direction.
func TestAProbeGoesOnFromItsKeptRoundsWhenItsGroupComesBack(t *testing.T) {
	t.Parallel()
	f := startCutoverFixture(t, leasesOutliveTheRun)
	engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
	stuckUnderFloor(t, f, 96500*time.Millisecond)
	ageFloor(t, f, readhold.FloorStanding)
	kept := func() int {
		record, _ := holds.controller.Reading(f.queryGroup)
		if record.Probe == nil {
			return 0
		}
		return record.Probe.Clean
	}
	heldRounds(t, f, 3*time.Hour, func(time.Duration) bool { return kept() >= 2*lookback.FloorProbeProgressEvery })
	before, _ := engine.GroupReading(f.queryGroup)
	rounds := kept()
	if rounds < 2*lookback.FloorProbeProgressEvery || before.FloorProbe == nil || before.FloorProbe.Kind != readhold.FloorProbeKindMidpoint {
		t.Fatalf("fixture: %d rounds kept, probe %+v; want a midpoint's 20 rounds kept", rounds, before.FloorProbe)
	}
	// The group leaves this process and comes back.
	engine.Forget(f.queryGroup)
	heldRounds(t, f, time.Hour, func(time.Duration) bool {
		reading, _ := engine.GroupReading(f.queryGroup)
		return reading.FloorProbe != nil
	})
	after, _ := engine.GroupReading(f.queryGroup)
	if after.FloorProbe == nil || after.FloorProbe.ResumedFrom != rounds || after.FloorProbe.Started != before.FloorProbe.Started ||
		after.FloorProbe.FloorMillis != before.FloorProbe.FloorMillis {
		t.Fatalf("the probe after coming back %+v, want it gone on from the %d rounds kept, started at %d", after.FloorProbe, rounds,
			before.FloorProbe.Started)
	}
	if events := probeEventsOf(engine, "midpoint"); events[lookback.FloorProbeResumed] != 1 || events[lookback.FloorProbeOwnerLost] != 1 {
		t.Fatalf("midpoint events %v, want one owner_lost and one resumed", events)
	}
	if got := holds.controller.Stats().ProbeProgress; got[readhold.ProbeProgressWritten] < 2 || got[readhold.ProbeProgressConflict] != 0 {
		t.Fatalf("progress writes %v, want the kept rounds written and none lost to a conflict", got)
	}
}
