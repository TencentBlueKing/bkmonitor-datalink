// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// stuckGroup is a group whose search below its hold has run out, as the
// stuck groups on the test deployment were: held 120 s, its floor at 117 s
// learned at the fixture's start, the data a 30 s step - no candidate is left
// between the two.
func stuckGroup(t *testing.T, record string) (*Controller, *memoryControl, *time.Time) {
	t.Helper()
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = 30 * time.Second
	if record == "" {
		record = fmt.Sprintf(`{"hold_ms":120000,"since_slot":60,"lowered":true,"lowered_from_ms":240000,`+
			`"earlier_floor_ms":117000,"earlier_floor_at_ms":%d}`, now.UnixMilli())
	}
	store.values["qg"] = []byte(record)
	prepare(t, c, spec)
	if got, tries := c.EarlierCandidate("qg"); tries {
		t.Fatalf("fixture: candidate %v, want the search run out", got)
	}
	return c, store, now
}

func probeEnd(t *testing.T, c *Controller, slot execution.EvaluationTime, floor, hold time.Duration, passed bool) {
	t.Helper()
	// The kind of the probe the controller would run now; one that runs none
	// ends a probe of the floor, the evidence a probe gone stale gives.
	_, kind, _, probing := c.FloorProbe("qg")
	if !probing {
		kind = FloorProbeKindFloor
	}
	if err := c.FloorProbeResult(context.Background(), FloorProbeEvidence{Contract: holdContract("qg", slot, hold.Milliseconds()),
		Floor: floor, Kind: kind, Hold: hold, Passed: passed}); err != nil {
		t.Fatal(err)
	}
}

// A floor stands a day before its first probe; each probe that fails
// relearns it where it was and doubles how long it stands - two days, four,
// then the week a record lives, and no longer.
func TestAFloorStandsADayAndEachFailedProbeDoublesIt(t *testing.T) {
	c, _, now := stuckGroup(t, "")
	for index, standing := range []time.Duration{24 * time.Hour, 48 * time.Hour, 96 * time.Hour, 7 * 24 * time.Hour, 7 * 24 * time.Hour} {
		learned := *now
		*now = learned.Add(standing - time.Millisecond)
		if floor, _, _, probing := c.FloorProbe("qg"); probing {
			t.Fatalf("probe %d: probed at %v a millisecond before the floor stood %v", index, floor, standing)
		}
		*now = learned.Add(standing)
		floor, _, _, probing := c.FloorProbe("qg")
		if !probing || floor != 117*time.Second {
			t.Fatalf("probe %d: %v %v once the floor stood %v, want the floor at 117 s", index, floor, probing, standing)
		}
		probeEnd(t, c, execution.EvaluationTime(600+index*60), 117*time.Second, 120*time.Second, false)
		record, _ := c.Reading("qg")
		if record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 117_000 || record.EarlierFloorAtMillis != now.UnixMilli() ||
			c.ReadHold("qg") != 120*time.Second {
			t.Fatalf("probe %d failed: floor %v at %d, hold %v; want the floor where it was, dated now, the hold unmoved",
				index, record.EarlierFloorMillis, record.EarlierFloorAtMillis, c.ReadHold("qg"))
		}
	}
	if got := c.Stats().FloorProbes[FloorProbeRelearned]; got != 5 {
		t.Fatalf("relearned %d, want 5", got)
	}
}

// A probe that passes lowers the hold to the floor - pending, frozen a step
// a Slot as any lowering is - marked verified. A probation follows it as it
// follows every lowering, and a process that takes the group over resumes it:
// LastLowering has it. The floor is the hold now, and the search goes on
// below it from half.
func TestAProbeThatPassesLowersTheHoldToItsFloorVerified(t *testing.T) {
	c, store, now := stuckGroup(t, "")
	*now = now.Add(FloorStanding)
	if _, _, _, probing := c.FloorProbe("qg"); !probing {
		t.Fatal("fixture: no probe once the floor stood")
	}
	writes := len(store.requests)
	probeEnd(t, c, 600, 117*time.Second, 120*time.Second, true)
	if got := len(store.requests) - writes; got != 1 {
		t.Fatalf("a passed probe wrote %d times, want once", got)
	}
	record, _ := c.Reading("qg")
	if c.ReadHold("qg") != 117*time.Second || !record.Lowered || !record.LoweredVerified || record.LoweredFromMillis != 120_000 {
		t.Fatalf("after a passed probe: hold %v, lowered %v verified %v from %d; want 117 s, verified, from 120 s",
			c.ReadHold("qg"), record.Lowered, record.LoweredVerified, record.LoweredFromMillis)
	}
	if record.EarlierFloorMillis != nil || record.EarlierFloorRetests != 0 {
		t.Fatalf("the floor %v, retests %d after the hold came down to it; want none", record.EarlierFloorMillis, record.EarlierFloorRetests)
	}
	if got, tries := c.EarlierCandidate("qg"); !tries || got != 58500*time.Millisecond {
		t.Fatalf("the search below the new hold tries %v %v, want half of 117 s", got, tries)
	}
	if !c.LoweringVerified("qg") || c.Stats().FloorProbes[FloorProbeLowered] != 1 {
		t.Fatalf("verified %v, lowered counted %d", c.LoweringVerified("qg"), c.Stats().FloorProbes[FloorProbeLowered])
	}
	if _, err := c.SlotReadHold(context.Background(), scheduleEvery(t, "qg", 60, 60), 660, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if slot, lowered := c.LastLowering("qg"); !lowered || slot != 660 {
		t.Fatalf("last lowering %d %v, want the verified one at 660 for the next owner to resume probation from", slot, lowered)
	}
	// A raise undoes it as it does any lowering, and the next is not verified.
	observeEarly(t, c, "qg", 200*time.Second, 117_000)
	if record, _ := c.Reading("qg"); record.LoweredVerified || c.LoweringVerified("qg") {
		t.Fatal("a raise after the verified lowering left it verified")
	}
}

// Trials only choose: three in a row past the quiet bar leave the hold where it is and name the candidate, which a
// probe reads before the hold moves - at once, with no standing, and no trial runs meanwhile. One that passes lowers
// the hold to it, verified; one that fails makes it the floor, a floor a probe failed at, and the quiet starts over.
func TestATrialsChoiceLowersTheHoldOnlyOnceAProbeVerifiesIt(t *testing.T) {
	for _, passed := range []bool{true, false} {
		t.Run(map[bool]string{true: "passed", false: "failed"}[passed], func(t *testing.T) {
			c, _, now := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			prepare(t, c, spec)
			observeEarly(t, c, "qg", 10*time.Minute+spec.SettlingWait, 0)
			*now = now.Add(time.Hour)
			for index := range EarlierMatchesRequired {
				if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", execution.EvaluationTime(120+index), 600_000),
					CandidateHold: 5 * time.Minute, Observed: true, Equal: true}); err != nil {
					t.Fatal(err)
				}
			}
			if c.ReadHold("qg") != 10*time.Minute {
				t.Fatalf("the trials lowered the hold to %v themselves", c.ReadHold("qg"))
			}
			if candidate, kind, edge, probing := c.FloorProbe("qg"); !probing || kind != FloorProbeKindVerify || candidate != 5*time.Minute || edge {
				t.Fatalf("probe %v %q edge %v probing %v, want the trials' 5 minutes verified at once", candidate, kind, edge, probing)
			}
			if _, tries := c.EarlierCandidate("qg"); tries {
				t.Fatal("a trial runs while its candidate is verified")
			}
			verifyEnd(t, c, "qg", 300, passed)
			record, _ := c.Reading("qg")
			if record.VerifyCandidateMillis != nil {
				t.Fatalf("the candidate %d is still to verify after its probe ended", *record.VerifyCandidateMillis)
			}
			if passed {
				if c.ReadHold("qg") != 5*time.Minute || !c.LoweringVerified("qg") || c.Stats().FloorProbesByKind[FloorProbeKindVerify][FloorProbeLowered] != 1 {
					t.Fatalf("a passed probe: hold %v verified %v counts %v, want lowered to 5 minutes, verified", c.ReadHold("qg"),
						c.LoweringVerified("qg"), c.Stats().FloorProbesByKind[FloorProbeKindVerify])
				}
				return
			}
			if c.ReadHold("qg") != 10*time.Minute || record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 300_000 ||
				!record.EarlierFloorProbed || record.QuietSinceMillis != now.UnixMilli() || record.EarlierMatches != 0 {
				t.Fatalf("a failed probe: hold %v, floor %v probed %v, quiet since %d, %d matches; want the hold unmoved, the "+
					"candidate a floor a probe failed at, the quiet started over", c.ReadHold("qg"), record.EarlierFloorMillis,
					record.EarlierFloorProbed, record.QuietSinceMillis, record.EarlierMatches)
			}
			if c.Stats().FloorProbesByKind[FloorProbeKindVerify][FloorProbeFloorRaised] != 1 {
				t.Fatalf("counts %v, want one floor_raised", c.Stats().FloorProbesByKind[FloorProbeKindVerify])
			}
			// The search goes on above the new floor.
			if got, tries := c.EarlierCandidate("qg"); !tries || got <= 5*time.Minute || got >= 10*time.Minute {
				t.Fatalf("after the failed probe the trials try %v %v, want between the floor and the hold", got, tries)
			}
		})
	}
}

// A probe's end is taken only against the record it read under: a hold or
// floor not the probe's, or a search reopened by a raise, changes nothing and
// is counted stale.
func TestAProbeEndAgainstAnotherRecordIsStale(t *testing.T) {
	for _, test := range []struct {
		name  string
		move  func(c *Controller)
		floor time.Duration
		hold  time.Duration
	}{
		{"another floor", nil, 116 * time.Second, 120 * time.Second},
		{"another hold", nil, 117 * time.Second, 130 * time.Second},
		{"raised since", func(c *Controller) { observeEarly(t, c, "qg", 400*time.Second, 120_000) }, 117 * time.Second, 120 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, store, now := stuckGroup(t, "")
			*now = now.Add(FloorStanding)
			if test.move != nil {
				test.move(c)
			}
			before, writes := c.ReadHold("qg"), len(store.requests)
			probeEnd(t, c, 600, test.floor, test.hold, true)
			if c.ReadHold("qg") != before || len(store.requests) != writes || c.Stats().FloorProbes[FloorProbeStale] != 1 {
				t.Fatalf("a stale end: hold %v (was %v), %d writes, stale %d; want nothing changed and one stale",
					c.ReadHold("qg"), before, len(store.requests)-writes, c.Stats().FloorProbes[FloorProbeStale])
			}
		})
	}
}

// A floor's failed probes are forgotten whenever the floor is: a raise clears
// it, a floor taking another value starts over, and the same value keeps its
// count.
func TestAFloorsStandingStartsOverWhenItIsClearedOrChanges(t *testing.T) {
	t.Run("raise", func(t *testing.T) {
		c, _, now := stuckGroup(t, "")
		*now = now.Add(FloorStanding)
		probeEnd(t, c, 600, 117*time.Second, 120*time.Second, false)
		observeEarly(t, c, "qg", 400*time.Second, 120_000)
		if record, _ := c.Reading("qg"); record.EarlierFloorRetests != 0 {
			t.Fatalf("retests %d after a raise, want none", record.EarlierFloorRetests)
		}
	})
	t.Run("value", func(t *testing.T) {
		floor := int64(100_000)
		record := Record{EarlierFloorMillis: &floor, EarlierFloorRetests: 2}
		setFloor(&record, 100_000, 5)
		if record.EarlierFloorRetests != 2 {
			t.Fatalf("the same floor relearned: retests %d, want 2 kept", record.EarlierFloorRetests)
		}
		setFloor(&record, 110_000, 6)
		if record.EarlierFloorRetests != 0 {
			t.Fatalf("a floor of another value: retests %d, want none", record.EarlierFloorRetests)
		}
	})
}

// A floor written before floors were dated is dated at its first reading by the UTC day it is read on and its
// group: every process that reads it that day - a restart hours later, a takeover - dates it alike, so restarts
// several times a day do not keep moving its first probe on; the groups such floors hold are probed spread over that
// day, each by the end of it; and a group whose hold was frozen within the day waits its standing from there.
func TestAnUndatedFloorIsDatedByItsDayAndGroup(t *testing.T) {
	read := func(qg execution.QueryGroupIdentity, record string, at time.Duration) (int64, time.Time) {
		t.Helper()
		c, store, now := controllerFixture(t)
		spec := groupSpec(qg, 0)
		spec.Step = 30 * time.Second
		store.values[qg] = []byte(record)
		prepare(t, c, spec)
		*now = now.Add(at)
		c.FloorProbe(qg)
		reading, _ := c.Reading(qg)
		start := *now
		for step := time.Duration(0); step <= 2*FloorStanding; step += 10 * time.Minute {
			*now = start.Add(step)
			if _, _, _, probing := c.FloorProbe(qg); probing {
				return reading.EarlierFloorAtMillis, *now
			}
		}
		t.Fatalf("%s: an undated floor read %v into the day not probed within two days", qg, at)
		return 0, time.Time{}
	}
	// Read two days on from the fixture's start, its hold frozen days before.
	const day = 48 * time.Hour
	undated := `{"hold_ms":120000,"since_slot":60,"earlier_floor_ms":117000}`
	endOfDay := time.Unix(1800, 0).Add(day).Truncate(24 * time.Hour).Add(24 * time.Hour)
	hours := map[int64]bool{}
	for index := range 24 {
		qg := execution.QueryGroupIdentity(fmt.Sprintf("qg-%d", index))
		dated, probed := read(qg, undated, day)
		if again, _ := read(qg, undated, day+6*time.Hour); again != dated {
			t.Fatalf("%s dated %d by a process at the start of the day and %d by one six hours on", qg, dated, again)
		}
		if stood := time.UnixMilli(dated).Add(FloorStanding); stood.After(endOfDay) || probed.After(stood.Add(10*time.Minute)) {
			t.Fatalf("%s stands until %s, first probed at %s; want it standing by the end of the day it was first read on, probed then",
				qg, stood.UTC(), probed.UTC())
		}
		hours[dated/time.Hour.Milliseconds()] = true
	}
	if len(hours) < 8 {
		t.Fatalf("24 groups' floors dated in %d hours, want them spread over the day", len(hours))
	}
	// Frozen at its hold within the day it is read on: it stands a day from then.
	frozen := time.Unix(1800, 0).Add(day).Add(-time.Hour)
	recent := fmt.Sprintf(`{"hold_ms":120000,"since_slot":%d,"earlier_floor_ms":117000}`, frozen.Unix())
	if dated, probed := read("qg-recent", recent, day); dated != frozen.UnixMilli() || probed.Before(frozen.Add(FloorStanding)) {
		t.Fatalf("a floor under a hold frozen at %s dated %d, first probed at %s; want dated then and probed a day on",
			frozen.UTC(), dated, probed.UTC())
	}
}

// A record's failed probes and its verified lowering decode, and a negative
// count is no record.
func TestAFloorsProbeFieldsDecode(t *testing.T) {
	record, err := Decode([]byte(`{"hold_ms":100000,"since_slot":60,"earlier_floor_retests":2,"lowered":true,"lowered_verified":true}`))
	if err != nil || record.EarlierFloorRetests != 2 || !record.LoweredVerified {
		t.Fatalf("decoded %+v %v", record, err)
	}
	if _, err := Decode([]byte(`{"hold_ms":100000,"since_slot":60,"earlier_floor_retests":-1}`)); err == nil {
		t.Fatal("a negative retest count decoded")
	}
	// A record written before floors were marked probed is one whose floor no
	// probe failed at; one marked so decodes so, and a floor no probe failed at
	// writes no mark.
	for _, c := range []struct {
		raw    string
		probed bool
	}{
		{`{"hold_ms":100000,"since_slot":60,"earlier_floor_ms":90000}`, false},
		{`{"hold_ms":100000,"since_slot":60,"earlier_floor_ms":90000,"earlier_floor_probed":true}`, true},
	} {
		record, err := Decode([]byte(c.raw))
		if err != nil || record.EarlierFloorProbed != c.probed {
			t.Fatalf("%s decoded probed %v %v, want %v", c.raw, record.EarlierFloorProbed, err, c.probed)
		}
		encoded, err := json.Marshal(record)
		if err != nil || strings.Contains(string(encoded), "earlier_floor_probed") != c.probed {
			t.Fatalf("%s encoded %s %v", c.raw, encoded, err)
		}
	}
}

// A floor the search learned is no floor a probe failed at, however near the
// hold its halves come: a probe of one is on the plain rounds. Probed, the
// same half is, and a half further above it than EdgeProbeReach is not. A
// floor a probe failed at loses the mark when it takes another value or is
// cleared.
func TestOnlyAFloorAProbeFailedAtMakesItsProbesNearIt(t *testing.T) {
	for _, c := range []struct {
		hold      int64
		probed    string
		candidate time.Duration
		edge      bool
	}{
		{20000, ``, 15 * time.Second, false},
		{20000, `,"earlier_floor_probed":true`, 15 * time.Second, true},
		{40000, `,"earlier_floor_probed":true`, 25 * time.Second, false},
	} {
		ctl, store, now := controllerFixture(t)
		spec := groupSpec("qg", 0)
		spec.Step = time.Minute
		store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":%d,"since_slot":60,"lowered":true,"lowered_from_ms":%d,`+
			`"earlier_floor_ms":10000,"earlier_floor_at_ms":%d%s}`, c.hold, 2*c.hold, now.UnixMilli(), c.probed))
		prepare(t, ctl, spec)
		if candidate, _, edge, probing := ctl.FloorProbe("qg"); !probing || candidate != c.candidate || edge != c.edge {
			t.Fatalf("hold %d ms over a floor of 10 s%s: probe %v near a failed floor %v %v, want %v, near %v",
				c.hold, c.probed, candidate, edge, probing, c.candidate, c.edge)
		}
	}
	record := Record{}
	setFloor(&record, 10000, 1)
	record.EarlierFloorProbed = true
	if setFloor(&record, 10000, 2); !record.EarlierFloorProbed {
		t.Fatal("the same floor learned again lost its probe's failure")
	}
	if setFloor(&record, 12000, 3); record.EarlierFloorProbed {
		t.Fatal("a floor of another value kept the mark of a probe that failed at the old one")
	}
	record.EarlierFloorProbed = true
	if clearFloor(&record); record.EarlierFloorProbed {
		t.Fatal("a cleared floor kept the mark of a probe that failed at it")
	}
}

// A floor the search learned that fails its own probe is a floor a probe
// failed at from then on: relearned, its next probe, two days on, is near it.
func TestAFloorThatFailsItsOwnProbeIsOneAProbeFailedAt(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":3000,"since_slot":60,"lowered":true,"lowered_from_ms":6000,`+
		`"earlier_floor_ms":0,"earlier_floor_at_ms":%d}`, now.UnixMilli()))
	prepare(t, c, spec)
	*now = now.Add(FloorStanding)
	probed, outcomes, edges := bisect(t, c, 3*time.Second)
	if fmt.Sprint(probed) != fmt.Sprint([]time.Duration{0}) || fmt.Sprint(outcomes) != fmt.Sprint([]string{FloorProbeRelearned}) ||
		fmt.Sprint(edges) != fmt.Sprint([]bool{false}) {
		t.Fatalf("probed %v taken %v near a failed floor %v, want the search's floor read once, on the plain rounds, and relearned",
			probed, outcomes, edges)
	}
	*now = now.Add(2 * FloorStanding)
	if floor, _, edge, probing := c.FloorProbe("qg"); !probing || floor != 0 || !edge {
		t.Fatalf("two days on: probe %v near a failed floor %v %v, want the floor again, now one a probe failed at", floor, edge, probing)
	}
}

// bisect runs probes on qg one after another as long as the controller gives
// one at once, each passing when it reads at or past visible - the hold from
// which the group's data reads whole - and returns the holds read at, how
// each was taken and whether each was near a floor a probe failed at.
func bisect(t *testing.T, c *Controller, visible time.Duration) (probed []time.Duration, outcomes []string, edges []bool) {
	t.Helper()
	for slot := execution.EvaluationTime(600); ; slot += 60 {
		candidate, _, edge, probing := c.FloorProbe("qg")
		if !probing {
			return probed, outcomes, edges
		}
		before := c.Stats().FloorProbes
		probeEnd(t, c, slot, candidate, c.ReadHold("qg"), candidate >= visible)
		probed, edges = append(probed, candidate), append(edges, edge)
		for _, outcome := range FloorProbeOutcomes {
			if c.Stats().FloorProbes[outcome] > before[outcome] {
				outcomes = append(outcomes, outcome)
			}
		}
		if len(probed) > 20 {
			t.Fatalf("still probing after %v", probed)
		}
	}
}

// A group whose search ran out a step above its floor - held 58.5 s over a
// floor of none, its data whole from 20 s - is probed at halves, one after
// another with no standing between: 29.3 s reads clean and the hold comes
// down to it, 14.6 s does not and is the floor, 21.9 s is the hold, 18.3 s
// the floor. Under 5 s apart the probes stop; the floor itself is read again
// only once it has stood a day. Every lowering is verified. The floor of none
// is the search's, so the first two halves need the plain rounds; 21.9 and
// 18.3 s are within EdgeProbeReach of the 14.6 s a probe failed at, and so
// is that floor itself a day on, so they need more.
func TestAProbeReadsMidpointsUntilTheFloorIsNearTheHold(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":58514,"since_slot":60,"lowered":true,"lowered_from_ms":117029,`+
		`"earlier_floor_ms":0,"earlier_floor_at_ms":%d}`, now.UnixMilli()))
	prepare(t, c, spec)
	probed, outcomes, edges := bisect(t, c, 20*time.Second)
	wantProbed := []time.Duration{29257 * time.Millisecond, 14628 * time.Millisecond, 21942 * time.Millisecond, 18285 * time.Millisecond}
	wantOutcomes := []string{FloorProbeLowered, FloorProbeFloorRaised, FloorProbeLowered, FloorProbeFloorRaised}
	if fmt.Sprint(probed) != fmt.Sprint(wantProbed) || fmt.Sprint(outcomes) != fmt.Sprint(wantOutcomes) ||
		fmt.Sprint(edges) != fmt.Sprint([]bool{false, false, true, true}) {
		t.Fatalf("probed %v taken %v near a failed floor %v, want %v taken %v near one [false false true true]",
			probed, outcomes, edges, wantProbed, wantOutcomes)
	}
	record, _ := c.Reading("qg")
	if c.ReadHold("qg") != 21942*time.Millisecond || record.EarlierFloorMillis == nil || *record.EarlierFloorMillis != 18285 ||
		!c.LoweringVerified("qg") {
		t.Fatalf("after the probes: hold %v, floor %v, verified %v; want 21.942 s over 18.285 s, verified",
			c.ReadHold("qg"), record.EarlierFloorMillis, c.LoweringVerified("qg"))
	}
	*now = now.Add(FloorStanding)
	if floor, _, edge, probing := c.FloorProbe("qg"); !probing || floor != 18285*time.Millisecond || !edge {
		t.Fatalf("a day on: probe %v near a failed floor %v %v, want the floor itself, a probe having failed at it", floor, edge, probing)
	}
}

// A group already held where its data first reads whole - 20 s, as the
// groups reading VictoriaMetrics with a readiness some 11 s after the window
// are - fails each half below it, one round each: 10 s, then 15 s is the
// floor, and its hold stays. The floor itself, read a day on, fails too and
// stands two days.
func TestAProbeOfAHoldAtItsDataBoundaryRaisesTheFloorAndLeavesTheHold(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":20000,"since_slot":60,"lowered":true,"lowered_from_ms":40000,`+
		`"earlier_floor_ms":0,"earlier_floor_at_ms":%d}`, now.UnixMilli()))
	prepare(t, c, spec)
	probed, outcomes, edges := bisect(t, c, 20*time.Second)
	if fmt.Sprint(probed) != fmt.Sprint([]time.Duration{10 * time.Second, 15 * time.Second}) ||
		fmt.Sprint(outcomes) != fmt.Sprint([]string{FloorProbeFloorRaised, FloorProbeFloorRaised}) || c.ReadHold("qg") != 20*time.Second ||
		fmt.Sprint(edges) != fmt.Sprint([]bool{false, true}) {
		t.Fatalf("probed %v taken %v near a failed floor %v, hold %v; want 10 s, then 15 s near the 10 s a probe failed at, "+
			"both raising the floor, the hold left at 20 s", probed, outcomes, edges, c.ReadHold("qg"))
	}
	*now = now.Add(FloorStanding)
	probed, outcomes, edges = bisect(t, c, 20*time.Second)
	if fmt.Sprint(probed) != fmt.Sprint([]time.Duration{15 * time.Second}) || fmt.Sprint(outcomes) != fmt.Sprint([]string{FloorProbeRelearned}) ||
		fmt.Sprint(edges) != fmt.Sprint([]bool{true}) {
		t.Fatalf("a day on: probed %v taken %v near a failed floor %v, want the floor read once, as one a probe failed at, and relearned",
			probed, outcomes, edges)
	}
	if record, _ := c.Reading("qg"); !record.EarlierFloorProbed || !strings.Contains(string(store.values["qg"]), `"earlier_floor_probed":true`) {
		t.Fatalf("relearned: probed %v, stored %s; want the floor kept as one a probe failed at", record.EarlierFloorProbed, store.values["qg"])
	}
	*now = now.Add(2*FloorStanding - time.Millisecond)
	if _, _, _, probing := c.FloorProbe("qg"); probing {
		t.Fatal("the floor relearned once is read again before it stood two days")
	}
}
