// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package readhold

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// jitterCycle runs a group a round a minute under an arrival that moves a
// tenth either way round to round: every round is read at the hold, and one
// read before its round's data was whole is read early; every sampleEvery-th
// round is the lookback's sample, whose rechecks see a window read early and
// raise the hold to that round's arrival; every sampleEvery-th round, offset,
// has a trial of the controller's candidate. It returns the rounds read early
// in the second half of the run and in all, the raises and the lowerings, and
// the hold it ends at.
func jitterCycle(t *testing.T, arrival time.Duration, rounds, sampleEvery int, seed int64) (lateEarly, early, raises, lowers int, final time.Duration) {
	t.Helper()
	c, _, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	start := 300 * time.Second
	observeEarly(t, c, "qg", start+spec.SettlingWait, 0)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < rounds; round++ {
		*now = now.Add(time.Minute)
		slot := execution.EvaluationTime(180 + round*60)
		hold := c.ReadHold("qg")
		this := arrival + time.Duration((rng.Float64()*0.2-0.1)*float64(arrival))
		if hold < this {
			early++
			if round >= rounds/2 {
				lateEarly++
			}
		}
		if round%sampleEvery == 0 && hold < this {
			if err := c.Observe(context.Background(), Evidence{Contract: holdContract("qg", slot, hold.Milliseconds()),
				ArrivalAge: this + spec.SettlingWait, Rung: "rung", Buckets: []int64{int64(slot)}}); err != nil {
				t.Fatal(err)
			}
		}
		if round%sampleEvery == sampleEvery/2 {
			if candidate, tries := c.EarlierCandidate("qg"); tries {
				if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot, hold.Milliseconds()),
					CandidateHold: candidate, Observed: true, Equal: candidate >= this}); err != nil {
					t.Fatal(err)
				}
			}
		}
		switch after := c.ReadHold("qg"); {
		case after > hold:
			raises++
		case after < hold:
			lowers++
		}
	}
	return lateEarly, early, raises, lowers, c.ReadHold("qg")
}

// Under an arrival that moves a tenth either way round to round, the hold
// comes to rest above the spread and stays there: after the first half of a
// two-day run no round is read before its data is whole, which a read early
// would be (its window evaluated incomplete, as only series arriving late are
// supplemented). A lowering that lands inside the spread is undone by the
// first raise after it, back to where it came from, with the lowered hold as
// the search's floor - so each run raises at most once, rather than climbing
// out of the spread one sampled round at a time.
func TestUnderAMovingArrivalTheHoldRestsAboveItsSpread(t *testing.T) {
	for _, percent := range []int{20, 30, 40, 50, 60, 70, 80, 90} {
		arrival := 300 * time.Second * time.Duration(percent) / 100
		earlyTotal, raiseTotal, worstRaises, highest := 0, 0, 0, time.Duration(0)
		for seed := int64(1); seed <= 20; seed++ {
			late, early, raises, _, final := jitterCycle(t, arrival, 3000, 10, seed*1000+int64(percent))
			if late != 0 {
				t.Errorf("arrival %d%%, run %d: %d rounds of the second half read early", percent, seed, late)
			}
			if raises > 1 {
				t.Errorf("arrival %d%%, run %d: %d raises, want at most the one that undoes a lowering into the spread", percent, seed, raises)
			}
			earlyTotal, raiseTotal, worstRaises, highest = earlyTotal+early, raiseTotal+raises, max(worstRaises, raises), max(highest, final)
		}
		t.Logf("arrival %3d%% (latest %v): read early %d of 60000 rounds in all, raises %d (worst run %d), highest resting hold %v",
			percent, arrival*11/10, earlyTotal, raiseTotal, worstRaises, highest)
	}
}
