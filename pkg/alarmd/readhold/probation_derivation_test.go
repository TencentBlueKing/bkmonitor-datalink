// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package readhold

import (
	"context"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

// The rounds a probation lasts are the rounds it takes, after a lowering into
// the spread an arrival moves over, for the first round to land past the
// lowered hold: under an arrival that moves a tenth either way round to
// round, at 60 to 80% of the hold, lookback.ProbationRounds sees the first
// late round of 99% of such lowerings (the derivation its comment gives).
//
// Every lowering is verified first: the probe reads each round at the
// candidate the trials chose and fails on the first that lands past it. Of
// the trials' choices that used to lower the hold straight into the spread -
// 453 of 1253 lowerings here - nearly all fail their probe; the few that pass
// by chance (5 of 805) are the lowerings probation is left to watch, and it
// still sees their first late round.
func TestProbationRoundsSeeTheFirstLateRoundOfNearlyEveryLowering(t *testing.T) {
	var waits []int
	lowerings, intoSpread := 0, 0
	for _, percent := range []int{60, 65, 70, 75, 80} {
		arrival := 300 * time.Second * time.Duration(percent) / 100
		for seed := int64(1); seed <= 200; seed++ {
			c, _, now := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			prepare(t, c, spec)
			observeEarly(t, c, "qg", 300*time.Second+spec.SettlingWait, 0)
			rng := rand.New(rand.NewSource(seed*7919 + int64(percent)))
			loweredAt, lowered := -1, time.Duration(0)
			verifyClean := 0 // rounds the probe of the trials' candidate read clean
			for round := 0; round < 3000; round++ {
				*now = now.Add(time.Minute)
				slot := execution.EvaluationTime(180 + round*60)
				hold := c.ReadHold("qg")
				this := arrival + time.Duration((rng.Float64()*0.2-0.1)*float64(arrival))
				if loweredAt >= 0 && this > lowered {
					waits = append(waits, round-loweredAt)
					loweredAt = -1
				}
				// Nothing here injects a failure: a reading the controller
				// refused leaves the hold where it was, and the rounds after it
				// would measure a search that stopped moving.
				if round%10 == 0 && hold < this {
					if err := c.Observe(context.Background(), Evidence{Contract: holdContract("qg", slot, hold.Milliseconds()),
						ArrivalAge: this + spec.SettlingWait, Rung: "rung", Buckets: []int64{int64(slot)}}); err != nil {
						t.Fatalf("arrival %d%%, seed %d, round %d: Observe: %v", percent, seed, round, err)
					}
				}
				// A candidate the trials chose is read every round by a probe,
				// which fails on the first round landing past it.
				if candidate, kind, _, probing := c.FloorProbe("qg"); probing && kind == FloorProbeKindVerify {
					switch {
					case this > candidate:
						verifyEnd(t, c, "qg", slot, false)
						verifyClean = 0
					case verifyClean+1 >= lookback.ProbationRounds:
						verifyEnd(t, c, "qg", slot, true)
						verifyClean = 0
					default:
						verifyClean++
					}
				}
				if round%10 == 5 {
					if candidate, tries := c.EarlierCandidate("qg"); tries {
						if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot, hold.Milliseconds()),
							CandidateHold: candidate, Observed: true, Equal: candidate >= this}); err != nil {
							t.Fatalf("arrival %d%%, seed %d, round %d: EarlierRead: %v", percent, seed, round, err)
						}
					}
				}
				if after := c.ReadHold("qg"); after < hold {
					lowerings++
					if after < arrival*11/10 {
						intoSpread++
						loweredAt, lowered = round, after
					}
				}
			}
		}
	}
	sort.Ints(waits)
	if len(waits) == 0 {
		t.Fatalf("no lowering into the spread (lowerings %d)", lowerings)
	}
	at := func(q float64) int { return waits[int(q*float64(len(waits)-1))] }
	t.Logf("lowerings %d, into the spread %d, seen %d; rounds to the first late round: p50 %d p90 %d p99 %d p99.9 %d max %d",
		lowerings, intoSpread, len(waits), at(0.5), at(0.9), at(0.99), at(0.999), waits[len(waits)-1])
	// Verified, the lowerings into the spread are a few in a hundred at most;
	// lowered on the trials alone, they were over a third.
	if intoSpread*50 > lowerings {
		t.Fatalf("%d of %d lowerings into the spread, want verification to stop all but a few in a hundred", intoSpread, lowerings)
	}
	if at(0.99) > lookback.ProbationRounds {
		t.Fatalf("the first late round of 99%% of lowerings into the spread came within %d rounds, past the %d a probation lasts",
			at(0.99), lookback.ProbationRounds)
	}
}
