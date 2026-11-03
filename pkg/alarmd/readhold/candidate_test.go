// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package readhold

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// searchRun lowers a group held at hold by trials and the probes that verify
// what they choose: each round asks the controller for its candidate, the
// trial matches when the candidate is past that round's arrival (the hold its
// data needs, arrival(round)), and the controller takes the evidence, an hour
// on so the quiet holds. A candidate the trials chose is then read by a probe,
// which passes when the candidate is past the arrival of every one of its
// rounds. It returns the lowest hold the group ever had and its hold once no
// candidate is left.
func searchRun(t *testing.T, step, hold time.Duration, arrival func(round int) time.Duration) (lowest, final time.Duration, rounds int) {
	t.Helper()
	c, _, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = step
	prepare(t, c, spec)
	observeEarly(t, c, "qg", hold+spec.SettlingWait, 0)
	if c.ReadHold("qg") != hold {
		t.Fatalf("fixture: held at %v, want %v", c.ReadHold("qg"), hold)
	}
	lowest = hold
	for rounds = 0; rounds < 400; rounds++ {
		if candidate, kind, _, probing := c.FloorProbe("qg"); probing && kind == FloorProbeKindVerify {
			*now = now.Add(time.Hour)
			passed := true
			for range verifyRounds {
				passed = passed && candidate >= arrival(rounds)
			}
			verifyEnd(t, c, "qg", execution.EvaluationTime(180+rounds*60), passed)
			lowest = min(lowest, c.ReadHold("qg"))
			continue
		}
		candidate, tries := c.EarlierCandidate("qg")
		if !tries {
			break
		}
		*now = now.Add(time.Hour)
		slot := execution.EvaluationTime(180 + rounds*60)
		equal := candidate >= arrival(rounds)
		if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot, c.ReadHold("qg").Milliseconds()),
			CandidateHold: candidate, Observed: true, Equal: equal}); err != nil {
			t.Fatal(err)
		}
		lowest = min(lowest, c.ReadHold("qg"))
	}
	return lowest, c.ReadHold("qg"), rounds
}

// Whatever fraction of the hold the data truly needs, the trials find it:
// a different trial moves the search up, matches move the hold down, and the
// hold comes to rest within one step above the arrival and never below it.
// Halving alone found only arrivals under half the hold, and never anything
// between half and the whole.
func TestTheSearchConvergesWithinAStepOfTheArrival(t *testing.T) {
	const hold = 300 * time.Second
	for _, step := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute} {
		for percent := 10; percent <= 95; percent += 5 {
			arrival := hold * time.Duration(percent) / 100
			t.Run(fmt.Sprintf("step=%v arrival=%d%%", step, percent), func(t *testing.T) {
				lowest, final, rounds := searchRun(t, step, hold, func(int) time.Duration { return arrival })
				if lowest < arrival {
					t.Fatalf("the hold went to %v, below the arrival %v", lowest, arrival)
				}
				if final-arrival > step {
					t.Fatalf("the hold came to rest at %v after %d rounds, more than a step above the arrival %v", final, rounds, arrival)
				}
				// The search ends: once the gap is a step there is nothing left
				// to try, and no trial is spent on it.
				if rounds >= 400 {
					t.Fatalf("the search never ended: still trying after %d rounds at %v", rounds, final)
				}
			})
		}
	}
}

// An arrival that moves round to round - up to a tenth either way - is
// found as well. The hold is lowered only on matches in a row and the search
// moved up only on different trials in a row, so a round that lands late once
// moves neither: the hold never goes below the earliest round's arrival, and
// comes to rest within a step above the latest.
func TestTheSearchToleratesAnArrivalThatMovesRoundToRound(t *testing.T) {
	const hold = 300 * time.Second
	for _, step := range []time.Duration{15 * time.Second, time.Minute} {
		for percent := 20; percent <= 90; percent += 10 {
			arrival := hold * time.Duration(percent) / 100
			t.Run(fmt.Sprintf("step=%v arrival=%d%%", step, percent), func(t *testing.T) {
				rng := rand.New(rand.NewSource(int64(percent) * int64(step)))
				lowest, final, rounds := searchRun(t, step, hold, func(int) time.Duration {
					return arrival + time.Duration((rng.Float64()*0.2-0.1)*float64(arrival))
				})
				earliest, latest := arrival*9/10, arrival*11/10
				if lowest < earliest {
					t.Fatalf("the hold went to %v, below the earliest round's arrival %v", lowest, earliest)
				}
				if final-latest > step {
					t.Fatalf("the hold came to rest at %v after %d rounds, more than a step above the latest round's arrival %v", final, rounds, latest)
				}
			})
		}
	}
}

// One different trial alone does not move the search; two in a row do, and a
// match between them starts the count again.
func TestOneDifferentTrialDoesNotMoveTheSearch(t *testing.T) {
	c, _, now := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	observeEarly(t, c, "qg", 330*time.Second, 0)
	trial := func(slot execution.EvaluationTime, equal bool) {
		t.Helper()
		candidate, _ := c.EarlierCandidate("qg")
		*now = now.Add(time.Minute)
		if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot, 300_000), CandidateHold: candidate,
			Observed: true, Equal: equal}); err != nil {
			t.Fatal(err)
		}
	}
	candidate := func() time.Duration { got, _ := c.EarlierCandidate("qg"); return got }
	if candidate() != 150*time.Second {
		t.Fatalf("first candidate %v, want half the hold", candidate())
	}
	trial(180, false)
	trial(240, true)
	trial(300, false)
	if candidate() != 150*time.Second {
		t.Fatalf("different trials apart moved the candidate to %v", candidate())
	}
	trial(360, false)
	if candidate() != 225*time.Second {
		t.Fatalf("two different trials in a row: candidate %v, want halfway from 150 s to 300 s", candidate())
	}
}

// The search below a hold is forgotten when the hold is raised, and at the
// daily renewal: what was too early under the old hold, or a day ago, says
// nothing now.
func TestTheSearchIsForgottenOnARaiseAndAtRenewal(t *testing.T) {
	floored := func(t *testing.T) (*Controller, *time.Time) {
		t.Helper()
		c, _, now := controllerFixture(t)
		prepare(t, c, groupSpec("qg", 0))
		observeEarly(t, c, "qg", 330*time.Second, 0)
		for index := range FloorDifferentsRequired {
			*now = now.Add(time.Minute)
			if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", execution.EvaluationTime(180+index*60), 300_000),
				CandidateHold: 150 * time.Second, Observed: true, Equal: false}); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := c.EarlierCandidate("qg"); got != 225*time.Second {
			t.Fatalf("fixture: candidate %v, want 225 s", got)
		}
		// Dated when the second different trial in a row learned it.
		if reading, _ := c.Reading("qg"); reading.EarlierFloorAtMillis != now.UnixMilli() {
			t.Fatalf("fixture: floor learned at %d, want %d", reading.EarlierFloorAtMillis, now.UnixMilli())
		}
		return c, now
	}
	t.Run("raise", func(t *testing.T) {
		c, _ := floored(t)
		observeEarly(t, c, "qg", 400*time.Second, 300_000)
		reading, _ := c.Reading("qg")
		if got, _ := c.EarlierCandidate("qg"); reading.EarlierFloorMillis != nil || reading.EarlierFloorAtMillis != 0 || got != 185*time.Second {
			t.Fatalf("after a raise to %v: floor %v at %d, candidate %v, want none and half the hold", c.ReadHold("qg"),
				reading.EarlierFloorMillis, reading.EarlierFloorAtMillis, got)
		}
	})
	t.Run("renewal", func(t *testing.T) {
		c, now := floored(t)
		*now = now.Add(RenewInterval + time.Minute)
		if err := c.RenewDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		reading, _ := c.Reading("qg")
		if got, _ := c.EarlierCandidate("qg"); reading.EarlierFloorMillis != nil || reading.EarlierFloorAtMillis != 0 || got != 150*time.Second {
			t.Fatalf("after renewal: floor %v at %d, candidate %v, want none and half the hold", reading.EarlierFloorMillis,
				reading.EarlierFloorAtMillis, got)
		}
	})
}

// A record written before the search was kept has none: its group tries half
// its hold, as before. And a record decodes whatever later fields it holds:
// a build before this one reads a record that keeps a search, ignores it, and
// its next write forgets it.
func TestARecordWithoutASearchTriesHalfItsHoldAndUnknownFieldsDecode(t *testing.T) {
	old := []byte(`{"hold_ms":120000,"since_slot":60,"earlier_matches":1}`)
	record, err := Decode(old)
	if err != nil || record.EarlierFloorMillis != nil || record.EarlierDifferents != 0 {
		t.Fatalf("an old record: %+v %v", record, err)
	}
	if got, tries := nextCandidate("qg", record, time.Minute, 0); !tries || got != 60_000 {
		t.Fatalf("an old record's candidate %d %v, want half its hold", got, tries)
	}
	future := []byte(`{"hold_ms":120000,"since_slot":60,"earlier_floor_ms":60000,"earlier_differents":1,"a_later_field":{"x":1}}`)
	record, err = Decode(future)
	if err != nil || record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 60_000 {
		t.Fatalf("a record with a later field: %+v %v", record, err)
	}
	if record.EarlierFloorAtMillis != 0 {
		t.Fatalf("a floor written before floors were dated: learned at %d, want none said", record.EarlierFloorAtMillis)
	}
	for _, field := range []string{`"earlier_floor_ms":-1`, `"earlier_floor_at_ms":-1`} {
		if _, err := Decode([]byte(`{"hold_ms":120000,"since_slot":60,` + field + `}`)); err == nil {
			t.Fatalf("a record with %s decoded", field)
		}
	}
}

// A trial the controller gives no candidate for is counted by why: at_limit
// for a group held at its limit (TestAGroupHeldAtItsLimitIsNotTriedWhileItsDataIsLate),
// search_done for a held group whose search below the hold has run out - the
// gap between its floor and the hold no wider than a step - and nothing for a
// group with no hold, which is not refused a trial but has none to try.
func TestARefusedTrialIsCountedSearchDoneWhereTheSearchRanOut(t *testing.T) {
	for _, test := range []struct {
		name   string
		record string
		want   map[string]uint64
	}{
		{"floor a step below", `{"hold_ms":47998,"since_slot":60,"lowered":true,"earlier_floor_ms":0,"earlier_floor_at_ms":1800000}`,
			map[string]uint64{EarlierSkipSearchDone: 1}},
		{"no hold", `{"hold_ms":0,"since_slot":60}`, map[string]uint64{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			store.values["qg"] = []byte(test.record)
			prepare(t, c, spec)
			if got, tries := c.EarlierCandidate("qg"); tries {
				t.Fatalf("fixture: candidate %v, want none", got)
			}
			c.EarlierRefused("qg")
			got := map[string]uint64{}
			for reason, count := range c.Stats().EarlierSkipped {
				if count > 0 {
					got[reason] = count
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("refusals counted %v, want %v", got, test.want)
			}
		})
	}
}
