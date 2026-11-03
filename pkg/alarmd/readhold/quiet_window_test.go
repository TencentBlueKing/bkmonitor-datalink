// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// heldAtTheLimit is a group measured landing 36 minutes late under a 10
// minute limit, its hold frozen at the limit: one read-early arrival, so its
// quiet bar is SingleEventQuiet.
func heldAtTheLimit(t *testing.T, c *Controller) GroupSpec {
	t.Helper()
	spec := groupSpec("qg", 0)
	spec.HoldLimit = 10 * time.Minute
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 36*time.Minute+spec.SettlingWait, 0)
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if record, _ := c.Reading("qg"); !record.HeldAtLimit() || c.ReadHold("qg") != 10*time.Minute {
		t.Fatalf("fixture: record %+v hold %v, want held at the 10 minute limit", record, c.ReadHold("qg"))
	}
	return spec
}

// agreeingTrials runs the earlier reads a lowering needs, each at the
// candidate the controller offers then, from slot on, and the probe that
// passes the candidate they chose if they chose one, and says whether one
// was offered.
func agreeingTrials(t *testing.T, c *Controller, slot execution.EvaluationTime) bool {
	t.Helper()
	offered := false
	for index := range EarlierMatchesRequired {
		candidate, ok := c.EarlierCandidate("qg")
		if !ok {
			candidate = 5 * time.Minute
		}
		offered = offered || ok
		if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot+execution.EvaluationTime(index*60), 600_000),
			CandidateHold: candidate, Observed: true, Equal: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, kind, _, probing := c.FloorProbe("qg"); probing && kind == FloorProbeKindVerify {
		verifyEnd(t, c, "qg", slot+execution.EvaluationTime(EarlierMatchesRequired*60), true)
	}
	return offered
}

// A group held at its limit runs no earlier-read trial while its data still
// lands past it: an earlier read agreeing with the formal read says both
// came before the data. Each trial refused for it is counted as at_limit; a
// read-early arrival within the quiet bar starts the quiet over, so a source
// still landing late is never tried.
func TestAGroupHeldAtItsLimitIsNotTriedWhileItsDataIsLate(t *testing.T) {
	c, _, now := controllerFixture(t)
	spec := heldAtTheLimit(t, c)
	*now = now.Add(50 * time.Minute)
	if hold, ok := c.EarlierCandidate("qg"); ok {
		t.Fatalf("a group held at its limit within its quiet bar offers a %v trial", hold)
	}
	c.EarlierRefused("qg")
	if got := c.Stats().EarlierSkipped; got[EarlierSkipAtLimit] != 1 || got[EarlierSkipSearchDone] != 0 || len(got) != len(EarlierSkipReasons) {
		t.Fatalf("skipped trials %v, want one at_limit", got)
	}
	// Still late: read early again, 50 minutes on - the bar is now that
	// gap - and the quiet starts over: 40 minutes later it is within it.
	observeEarly(t, c, "qg", 36*time.Minute+spec.SettlingWait, 600_000)
	*now = now.Add(40 * time.Minute)
	if agreeingTrials(t, c, 180) || c.ReadHold("qg") != 10*time.Minute {
		t.Fatalf("a group still read early within its bar was tried, hold %v", c.ReadHold("qg"))
	}
	if record, _ := c.Reading("qg"); record.EarlierMatches != 0 {
		t.Fatalf("trials at the limit were taken as evidence: %+v", record)
	}
}

// A group held at its limit whose data comes back in time has no read-early
// arrival to give: once it has been quiet for its bar its trials run again,
// and three that agree lower the hold - in the process that held it, and in
// one restarted from its record, which re-derives the limit from the
// arrival age it kept. A refusal for anything but the limit is not counted.
func TestAGroupAtItsLimitWhoseDataComesBackInTimeIsLowered(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same process", true: "restarted"}[restart], func(t *testing.T) {
			c, store, now := controllerFixture(t)
			spec := heldAtTheLimit(t, c)
			*now = now.Add(SingleEventQuiet + time.Minute)
			if restart {
				restarted, err := NewController(Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute, Now: func() time.Time { return *now },
					Owner: func(qg execution.QueryGroupIdentity) (Owner, error) {
						return Owner{Fence: holdFence(qg), ContentScope: "view"}, nil
					}})
				if err != nil {
					t.Fatal(err)
				}
				c = restarted
				prepare(t, c, spec)
				if record, _ := c.Reading("qg"); !record.HeldAtLimit() {
					t.Fatalf("fixture: the restarted record is not held at its limit: %+v", record)
				}
			}
			if !agreeingTrials(t, c, 180) {
				t.Fatal("a group quiet past its bar at the limit offered no trial")
			}
			if c.ReadHold("qg") >= 10*time.Minute {
				t.Fatalf("three agreeing trials past the quiet bar left the hold at %v", c.ReadHold("qg"))
			}
			c.EarlierRefused("qg")
			if got := c.Stats().EarlierSkipped[EarlierSkipAtLimit]; got != 0 {
				t.Fatalf("%d trials counted skipped at the limit for a group no longer held there", got)
			}
		})
	}
}

// The quiet a lowering waits for is the longest gap between read-early
// arrivals within the record's TTL window, not over its whole life: a 150 h
// gap holds every lowering to 150 h while it is within the window, and once
// it has aged out a source late every 2 h is judged by its 2 h gaps.
func TestALongGapAgesOutOfTheQuietBar(t *testing.T) {
	c, _, now := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	ctx := context.Background()
	slot := execution.EvaluationTime(120)
	earlier := func() {
		t.Helper()
		for range EarlierMatchesRequired {
			slot += 60
			if err := c.EarlierRead(ctx, EarlierEvidence{Contract: holdContract("qg", slot, 150_000), CandidateHold: 75 * time.Second,
				Observed: true, Equal: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	observeEarly(t, c, "qg", 180*time.Second, 0)
	*now = now.Add(150 * time.Hour)
	observeEarly(t, c, "qg", 180*time.Second, 150_000)
	for range 3 {
		*now = now.Add(2 * time.Hour)
		observeEarly(t, c, "qg", 180*time.Second, 150_000)
	}
	*now = now.Add(3 * time.Hour)
	earlier()
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatalf("lowered to %v 3 hours after a 150 hour gap still within the window", c.ReadHold("qg"))
	}
	if reading, _ := c.Reading("qg"); quietBar("qg", reading, now.UnixMilli()) != 150*time.Hour.Milliseconds() {
		t.Fatalf("bar within the window %v, want 150 hours (record %+v)", time.Duration(quietBar("qg", reading, now.UnixMilli()))*time.Millisecond, reading)
	}
	for range 8 * 12 {
		*now = now.Add(2 * time.Hour)
		observeEarly(t, c, "qg", 180*time.Second, 150_000)
	}
	reading, _ := c.Reading("qg")
	if bar := quietBar("qg", reading, now.UnixMilli()); bar != 2*time.Hour.Milliseconds() {
		t.Fatalf("bar after the 150 hour gap left the window %v, want the recent 2 hours (record %+v)", time.Duration(bar)*time.Millisecond, reading)
	}
	if len(reading.EarlyGaps) > earlyGapDays {
		t.Fatalf("kept %d days of gaps, want at most %d", len(reading.EarlyGaps), earlyGapDays)
	}
	*now = now.Add(3 * time.Hour)
	earlier()
	if record, _ := c.Reading("qg"); record.VerifyCandidateMillis == nil || *record.VerifyCandidateMillis != 75_000 {
		t.Fatalf("3 quiet hours past a 2 hour bar chose %v to verify, want 75 s", record.VerifyCandidateMillis)
	}
	verifyEnd(t, c, "qg", 100_000, true)
	if c.ReadHold("qg") != 75*time.Second {
		t.Fatalf("3 quiet hours past a 2 hour bar left the hold at %v, want lowered to 75 s", c.ReadHold("qg"))
	}
}

// A record from a build that kept only the all-time longest gap is read as
// that gap ended on the day of its last read-early arrival: it holds the bar
// for the window from there and then ages out like any other, when the
// group's own day turns (groupDayOffset).
func TestAnAllTimeGapFromAnEarlierBuildAgesOutFromItsLastArrival(t *testing.T) {
	last := 10*dayMillis + 3_600_000
	record, err := Decode([]byte(`{"hold_ms":150000,"since_slot":60,"last_early_ms":` + strconv.FormatInt(last, 10) + `,"max_early_interval_ms":540000000}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   int64
		want int64
	}{
		{last, 540_000_000},
		{last + int64(earlyGapDays-1)*dayMillis, 540_000_000},
		{int64(10+earlyGapDays)*dayMillis + groupDayOffset("qg").Milliseconds() - 1, 540_000_000},
		{int64(10+earlyGapDays)*dayMillis + groupDayOffset("qg").Milliseconds(), SingleEventQuiet.Milliseconds()},
	} {
		if got := quietBar("qg", record, test.at); got != test.want {
			t.Errorf("bar at day %d = %d, want %d", utcDay(test.at), got, test.want)
		}
	}
	gaps := withEarlyGap(earlyGapsOf(record), last+dayMillis, 7_200_000)
	if len(gaps) != 2 || gaps[0] != (EarlyGap{Day: 10, Millis: 540_000_000}) || gaps[1] != (EarlyGap{Day: 11, Millis: 7_200_000}) {
		t.Fatalf("gaps after one more %v", gaps)
	}
}

// A group restored from a record an earlier build wrote moves its all-time
// gap into EarlyGaps on its next read-early arrival, and writes the old
// field no more: the gap is kept as one ending on its last arrival's day,
// beside the gap that arrival ends.
func TestARestoredAllTimeGapMovesIntoEarlyGaps(t *testing.T) {
	c, store, now := controllerFixture(t)
	last := now.UnixMilli()
	store.values["qg"] = []byte(`{"hold_ms":150000,"since_slot":60,"segment_start":60,"last_early_ms":` + strconv.FormatInt(last, 10) +
		`,"max_early_interval_ms":540000000}`)
	prepare(t, c, groupSpec("qg", 0))
	*now = now.Add(26 * time.Hour)
	observeEarly(t, c, "qg", 180*time.Second, 150_000)
	reading, _ := c.Reading("qg")
	want := []EarlyGap{{Day: utcDay(last), Millis: 540_000_000}, {Day: utcDay(now.UnixMilli()), Millis: 26 * time.Hour.Milliseconds()}}
	if reading.MaxEarlyIntervalMillis != 540_000_000 || len(reading.EarlyGaps) != 2 || reading.EarlyGaps[0] != want[0] || reading.EarlyGaps[1] != want[1] {
		t.Fatalf("restored record after one arrival %+v, want gaps %v and their longest kept for an older reader", reading, want)
	}
	stored, err := Decode(store.values["qg"])
	if err != nil || stored.MaxEarlyIntervalMillis != 540_000_000 || len(stored.EarlyGaps) != 2 {
		t.Fatalf("written record %s (%v)", store.values["qg"], err)
	}
}

// Gaps keep the longest of each day, and only the days within the window.
func TestEarlyGapsKeepTheLongestOfEachDayInTheWindow(t *testing.T) {
	gaps := withEarlyGap(nil, 3*dayMillis, 1_000)
	gaps = withEarlyGap(gaps, 3*dayMillis+5, 4_000)
	gaps = withEarlyGap(gaps, 3*dayMillis+9, 2_000)
	if len(gaps) != 1 || gaps[0] != (EarlyGap{Day: 3, Millis: 4_000}) {
		t.Fatalf("one day's gaps %v, want its longest", gaps)
	}
	gaps = withEarlyGap(gaps, int64(3+earlyGapDays-1)*dayMillis, 1_000)
	if len(gaps) != 2 {
		t.Fatalf("a gap within the window was dropped: %v", gaps)
	}
	gaps = withEarlyGap(gaps, int64(3+earlyGapDays)*dayMillis, 1_000)
	if len(gaps) != 2 || gaps[0].Day != int64(3+earlyGapDays-1) {
		t.Fatalf("a gap past the window was kept: %v", gaps)
	}
}

// A record's gaps are at most a window of days, each a positive gap, on
// strictly later days.
func TestDecodeRefusesMalformedEarlyGaps(t *testing.T) {
	if _, err := Decode([]byte(`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":1,"ms":5},{"day":2,"ms":7}]}`)); err != nil {
		t.Fatalf("well-formed gaps refused: %v", err)
	}
	for _, raw := range []string{
		`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":-1,"ms":5}]}`,
		`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":1,"ms":0}]}`,
		`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":2,"ms":5},{"day":2,"ms":5}]}`,
		`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":3,"ms":5},{"day":2,"ms":5}]}`,
		`{"hold_ms":1,"since_slot":60,"early_gaps":[{"day":1,"ms":1},{"day":2,"ms":1},{"day":3,"ms":1},{"day":4,"ms":1},{"day":5,"ms":1},{"day":6,"ms":1},{"day":7,"ms":1},{"day":8,"ms":1}]}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("Decode(%s) accepted", raw)
		}
	}
}

// A record holding something that carries no quiet - restored as written,
// with nothing that started one - is not pinned at its hold: preparing its
// next Slot starts its quiet, and once the group has gone its bar without
// being read early it is tried and lowered like any other, at its limit or
// under it.
func TestARecordWithNoQuietIsTriedOnceQuietForItsBar(t *testing.T) {
	for _, atLimit := range []bool{true, false} {
		t.Run(map[bool]string{true: "at the limit", false: "under it"}[atLimit], func(t *testing.T) {
			c, store, now := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.HoldLimit = 10 * time.Minute
			arrival, hold := (36*time.Minute + spec.SettlingWait).Milliseconds(), int64(600_000)
			if !atLimit {
				arrival, hold = (5*time.Minute + spec.SettlingWait).Milliseconds(), 300_000
			}
			store.values["qg"] = []byte(`{"hold_ms":` + strconv.FormatInt(hold, 10) + `,"since_slot":60,"segment_start":60,"arrival_age_ms":` +
				strconv.FormatInt(arrival, 10) + `,"limit_ms":600000}`)
			prepare(t, c, spec)
			if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
				t.Fatal(err)
			}
			if record, _ := c.Reading("qg"); record.HeldAtLimit() != atLimit || record.QuietSinceMillis != now.UnixMilli() {
				t.Fatalf("restored record %+v, want held at its limit %t with its quiet started now", record, atLimit)
			}
			*now = now.Add(SingleEventQuiet / 2)
			if atLimit {
				if candidate, ok := c.EarlierCandidate("qg"); ok {
					t.Fatalf("tried at %v within its bar", candidate)
				}
			}
			*now = now.Add(SingleEventQuiet)
			if !agreeingTrials(t, c, 180) || c.ReadHold("qg") >= time.Duration(hold)*time.Millisecond {
				t.Fatalf("a record without a quiet stayed at %v past its bar", c.ReadHold("qg"))
			}
		})
	}
}

// Two groups whose long gap ended on the same day are quiet for their bar at their own hours: each gap ages out when
// the group's day turns, groupDayOffset after the UTC day does, not at 00:00 UTC for both.
func TestTwoGroupsGapsAgeOutAtTheirOwnHour(t *testing.T) {
	early, late := execution.QueryGroupIdentity(""), execution.QueryGroupIdentity("")
	for i := 0; i < 64 && (early == "" || late == ""); i++ {
		qg := execution.QueryGroupIdentity(fmt.Sprintf("qg-%d", i))
		switch offset := groupDayOffset(qg); {
		case early == "" && offset < 6*time.Hour:
			early = qg
		case late == "" && offset > 18*time.Hour:
			late = qg
		}
	}
	if early == "" || late == "" {
		t.Fatal("fixture: no two groups six hours and eighteen into the day")
	}
	record, err := Decode([]byte(`{"hold_ms":150000,"since_slot":60,"early_gaps":[{"day":10,"ms":540000000}]}`))
	if err != nil {
		t.Fatal(err)
	}
	noon := int64(10+earlyGapDays)*dayMillis + 12*time.Hour.Milliseconds()
	if got := quietBar(early, record, noon); got != SingleEventQuiet.Milliseconds() {
		t.Fatalf("%s at noon of the day its gap ages out: bar %d, want aged out", early, got)
	}
	if got := quietBar(late, record, noon); got != 540_000_000 {
		t.Fatalf("%s at noon of the day its gap ages out: bar %d, want still the gap, its day not yet turned", late, got)
	}
}

// Groups whose identities differ only in their last characters - as Query Groups numbered in a row do - turn their
// days spread over the day: 24 such groups fall in 12 hours of it at least. Their FNV hashes alone fell in two.
func TestNearlyAlikeGroupsTurnTheirDaysSpreadOverTheDay(t *testing.T) {
	hours := map[int]bool{}
	for i := 1; i <= 24; i++ {
		offset := groupDayOffset(execution.QueryGroupIdentity(fmt.Sprintf("query-group-%02d", i)))
		if offset < 0 || offset >= 24*time.Hour {
			t.Fatalf("offset %v outside the day", offset)
		}
		hours[int(offset/time.Hour)] = true
	}
	if len(hours) < 12 {
		t.Fatalf("24 nearly alike groups fall in %d hours of the day, want 12 at least", len(hours))
	}
}
