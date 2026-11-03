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
)

// verifyingGroup is a group held at 400 s - its data seen 430 s after its
// Slot, the settling wait 30 s - whose trials chose 300 s, the probe of it
// not yet ended; extra is more of its record.
func verifyingGroup(t *testing.T, extra string) (*Controller, *memoryControl, *time.Time) {
	t.Helper()
	c, store, now := controllerFixture(t)
	// Two days on, so the Slots and times of the hours before it are real ones.
	*now = now.Add(48 * time.Hour)
	store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":400000,"since_slot":60,"arrival_age_ms":430000,"quiet_since_ms":%d,`+
		`"verify_candidate_ms":300000%s}`, now.Add(-3*time.Hour).UnixMilli(), extra))
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	if candidate, kind, _, probing := c.FloorProbe("qg"); !probing || kind != FloorProbeKindVerify || candidate != 5*time.Minute {
		t.Fatalf("fixture: probe %v %q %v, want the 300 s candidate verified", candidate, kind, probing)
	}
	return c, store, now
}

// Data that comes later than the hold - a raise - and a hold seeded or limited anew clear the candidate the trials
// chose: it was chosen against data the group no longer has. A renewal clearing the floor keeps it: the candidate is
// the trials', no floor's, and the probe reading it is reading the data now.
func TestTheTrialsCandidateGoesWithTheHoldItWasChosenUnder(t *testing.T) {
	t.Run("a raise", func(t *testing.T) {
		c, _, _ := verifyingGroup(t, "")
		observeEarly(t, c, "qg", 500*time.Second, 400_000)
		if record, _ := c.Reading("qg"); record.VerifyCandidateMillis != nil || c.ReadHold("qg") <= 400*time.Second {
			t.Fatalf("after a raise to %v the candidate is %v, want none", c.ReadHold("qg"), record.VerifyCandidateMillis)
		}
	})
	t.Run("a limit that moves the hold", func(t *testing.T) {
		// The next owner freezes a Slot under a limit below the hold.
		c, _, _ := verifyingGroup(t, "")
		next, err := NewController(c.options)
		if err != nil {
			t.Fatal(err)
		}
		spec := groupSpec("qg", 0)
		spec.Step, spec.HoldLimit = time.Minute, 350*time.Second
		prepare(t, next, spec)
		if _, err := next.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 600, holdFence("qg")); err != nil {
			t.Fatal(err)
		}
		c = next
		if record, _ := c.Reading("qg"); c.ReadHold("qg") != 350*time.Second || record.VerifyCandidateMillis != nil {
			t.Fatalf("after the limit came to 350 s: hold %v, candidate %v, want none", c.ReadHold("qg"), record.VerifyCandidateMillis)
		}
	})
	t.Run("a renewal", func(t *testing.T) {
		c, _, now := verifyingGroup(t, `,"earlier_floor_ms":100000,"earlier_floor_at_ms":1`)
		*now = now.Add(RenewInterval + time.Minute)
		if err := c.RenewDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		record, _ := c.Reading("qg")
		if record.EarlierFloorMillis != nil || record.VerifyCandidateMillis == nil || *record.VerifyCandidateMillis != 300_000 {
			t.Fatalf("after a renewal: floor %v, candidate %v; want the floor cleared and 300 s still to verify",
				record.EarlierFloorMillis, record.VerifyCandidateMillis)
		}
		if _, kind, _, probing := c.FloorProbe("qg"); !probing || kind != FloorProbeKindVerify {
			t.Fatalf("after a renewal the probe is %q %v, want the candidate's", kind, probing)
		}
	})
}

// A candidate that is no longer below the hold and above the floor is moot: no probe reads it, the trials go on as
// if there were none, and the next write clears it, counted moot. Nothing lowers the hold to it.
func TestAMootCandidateIsNotProbedAndTheNextWriteClearsIt(t *testing.T) {
	for _, c := range []struct {
		name, record string
	}{
		{"at the hold", `{"hold_ms":300000,"since_slot":60,"verify_candidate_ms":300000}`},
		{"above the hold", `{"hold_ms":300000,"since_slot":60,"verify_candidate_ms":400000}`},
		{"at the floor", `{"hold_ms":300000,"since_slot":60,"verify_candidate_ms":100000,"earlier_floor_ms":100000,"earlier_floor_at_ms":1}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl, store, _ := controllerFixture(t)
			store.values["qg"] = []byte(c.record)
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			prepare(t, ctl, spec)
			if _, kind, _, probing := ctl.FloorProbe("qg"); probing && kind == FloorProbeKindVerify {
				t.Fatal("a moot candidate is probed")
			}
			candidate, tries := ctl.EarlierCandidate("qg")
			if !tries {
				t.Fatal("the trials stopped for a moot candidate")
			}
			if err := ctl.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", 180, 300_000), CandidateHold: candidate,
				Observed: true, Equal: false}); err != nil {
				t.Fatal(err)
			}
			if record, _ := ctl.Reading("qg"); record.VerifyCandidateMillis != nil || ctl.ReadHold("qg") != 5*time.Minute {
				t.Fatalf("after the next write: candidate %v, hold %v; want it cleared, the hold unmoved", record.VerifyCandidateMillis,
					ctl.ReadHold("qg"))
			}
			if got := ctl.Stats().FloorProbesByKind[FloorProbeKindVerify][FloorProbeMoot]; got != 1 {
				t.Fatalf("moot counted %d, want 1", got)
			}
		})
	}
}

// A candidate gone moot while its probe ran - the hold limited below it - is cleared by that probe's end, counted
// moot, and lowers nothing, whether the probe passed or not.
func TestAProbeOfACandidateGoneMootClearsIt(t *testing.T) {
	for _, passed := range []bool{true, false} {
		t.Run(fmt.Sprint(passed), func(t *testing.T) {
			c, store, _ := verifyingGroup(t, "")
			// The record another owner wrote under a limit below the candidate.
			store.values["qg"] = []byte(`{"hold_ms":200000,"since_slot":60,"verify_candidate_ms":300000}`)
			fresh, err := NewController(c.options)
			if err != nil {
				t.Fatal(err)
			}
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			prepare(t, fresh, spec)
			if err := fresh.FloorProbeResult(context.Background(), FloorProbeEvidence{Contract: holdContract("qg", 300, 600_000),
				Floor: 5 * time.Minute, Kind: FloorProbeKindVerify, Hold: 10 * time.Minute, Passed: passed}); err != nil {
				t.Fatal(err)
			}
			record, _ := fresh.Reading("qg")
			if record.VerifyCandidateMillis != nil || fresh.ReadHold("qg") != 200*time.Second || record.Lowered {
				t.Fatalf("candidate %v, hold %v, lowered %v; want the candidate cleared, nothing lowered", record.VerifyCandidateMillis,
					fresh.ReadHold("qg"), record.Lowered)
			}
			stats := fresh.Stats().FloorProbesByKind[FloorProbeKindVerify]
			if stats[FloorProbeMoot] != 1 || stats[FloorProbeStale] != 0 || stats[FloorProbeLowered] != 0 {
				t.Fatalf("verify counts %v, want one moot", stats)
			}
		})
	}
}

// A probe's end of another kind than the probe the record has now is stale, counted under its own kind, and changes
// nothing; one of no known kind is refused.
func TestAProbeEndOfAnotherKindIsStale(t *testing.T) {
	c, _, _ := verifyingGroup(t, "")
	if err := c.FloorProbeResult(context.Background(), FloorProbeEvidence{Contract: holdContract("qg", 300, 400_000),
		Floor: 5 * time.Minute, Kind: FloorProbeKindMidpoint, Hold: 400 * time.Second, Passed: true}); err != nil {
		t.Fatal(err)
	}
	if record, _ := c.Reading("qg"); c.ReadHold("qg") != 400*time.Second || record.VerifyCandidateMillis == nil {
		t.Fatalf("a midpoint's end moved the hold to %v, candidate %v", c.ReadHold("qg"), record.VerifyCandidateMillis)
	}
	if got := c.Stats().FloorProbesByKind[FloorProbeKindMidpoint][FloorProbeStale]; got != 1 {
		t.Fatalf("stale midpoints %d, want 1", got)
	}
	if err := c.FloorProbeResult(context.Background(), FloorProbeEvidence{Contract: holdContract("qg", 300, 400_000),
		Floor: 5 * time.Minute, Kind: "", Hold: 400 * time.Second, Passed: true}); err == nil {
		t.Fatal("a probe end of no kind was taken")
	}
}

// The trials' candidate near a floor a probe failed at - within EdgeProbeReach above it - is read for the longer
// span; further above, or over a floor the search learned, it is not.
func TestTheTrialsCandidateNearAFailedFloorIsOnTheLongerRounds(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra string
		edge  bool
	}{
		{"within reach of a probed floor", `,"earlier_floor_ms":295000,"earlier_floor_at_ms":1,"earlier_floor_probed":true`, true},
		{"past reach of a probed floor", `,"earlier_floor_ms":280000,"earlier_floor_at_ms":1,"earlier_floor_probed":true`, false},
		{"over a learned floor", `,"earlier_floor_ms":295000,"earlier_floor_at_ms":1`, false},
		{"over no floor", ``, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl, _, _ := verifyingGroup(t, c.extra)
			if _, _, edge, _ := ctl.FloorProbe("qg"); edge != c.edge {
				t.Fatalf("edge %v, want %v", edge, c.edge)
			}
		})
	}
}

// A record written before the candidate was kept has none; one with it decodes it, a record with none writes none,
// and a negative one is no record.
func TestTheTrialsCandidateDecodesBothWays(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want *int64
	}{
		{`{"hold_ms":600000,"since_slot":60}`, nil},
		{`{"hold_ms":600000,"since_slot":60,"verify_candidate_ms":300000}`, ptr(int64(300_000))},
		{`{"hold_ms":600000,"since_slot":60,"verify_candidate_ms":0}`, ptr(int64(0))},
	} {
		record, err := Decode([]byte(c.raw))
		if err != nil || (record.VerifyCandidateMillis == nil) != (c.want == nil) ||
			(c.want != nil && *record.VerifyCandidateMillis != *c.want) {
			t.Fatalf("%s decoded %v %v, want %v", c.raw, record.VerifyCandidateMillis, err, c.want)
		}
		encoded, err := json.Marshal(record)
		if err != nil || strings.Contains(string(encoded), "verify_candidate_ms") != (c.want != nil) {
			t.Fatalf("%s encoded %s %v", c.raw, encoded, err)
		}
	}
	if _, err := Decode([]byte(`{"hold_ms":600000,"since_slot":60,"verify_candidate_ms":-1}`)); err == nil {
		t.Fatal("a negative candidate decoded")
	}
}
