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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// keepProgress writes how far the group's probe of kind at candidate has read, clean rounds since started, under
// the hold the group has now.
func keepProgress(t *testing.T, c *Controller, kind string, candidate time.Duration, clean int, started execution.EvaluationTime) {
	t.Helper()
	keepProgressAt(t, c, kind, candidate, clean, started, started+60)
}

// keepProgressAt is keepProgress whose latest clean round is the Slot kept, of a probe a minute's period.
func keepProgressAt(t *testing.T, c *Controller, kind string, candidate time.Duration, clean int, started, kept execution.EvaluationTime) {
	t.Helper()
	keepProgressEvery(t, c, kind, candidate, clean, started, kept, time.Minute)
}

// keepProgressEvery is keepProgressAt of a probe whose rounds are period apart.
func keepProgressEvery(t *testing.T, c *Controller, kind string, candidate time.Duration, clean int, started, kept execution.EvaluationTime,
	period time.Duration) {
	t.Helper()
	record, _ := c.Reading("qg")
	hold := current(record)
	if err := c.FloorProbeProgress(context.Background(), FloorProbeProgressEvidence{Contract: holdContract("qg", kept, hold),
		Floor: candidate, Kind: kind, Hold: time.Duration(hold) * time.Millisecond, Clean: clean, Started: started, Period: period}); err != nil {
		t.Fatal(err)
	}
}

// takenOver is a controller of another process that reads the group's record afresh, as a rollout or a takeover does.
func takenOver(t *testing.T, c *Controller) *Controller {
	t.Helper()
	next, err := NewController(c.options)
	if err != nil {
		t.Fatal(err)
	}
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, next, spec)
	return next
}

// A probe's clean rounds kept in the record are where the next process's probe of the same candidate goes on from,
// from the Slot it started at; nothing kept, it starts from none.
func TestTheNextOwnerGoesOnFromTheProbesKeptRounds(t *testing.T) {
	c, _, now := verifyingGroup(t, "")
	started := execution.EvaluationTime(now.Unix() - 3600)
	if _, _, ok := c.FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); ok {
		t.Fatal("a probe resumes from rounds no one kept")
	}
	keepProgress(t, c, FloorProbeKindVerify, 5*time.Minute, 50, started)
	if got := c.Stats().ProbeProgress[ProbeProgressWritten]; got != 1 {
		t.Fatalf("progress written %d, want 1", got)
	}
	next := takenOver(t, c)
	clean, from, ok := next.FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute)
	if !ok || clean != 50 || from != started {
		t.Fatalf("the next owner resumes %d rounds from %d %v, want 50 from %d", clean, from, ok, started)
	}
	// Only the same probe goes on from them.
	if _, _, ok := next.FloorProbeResume("qg", FloorProbeKindMidpoint, 5*time.Minute); ok {
		t.Fatal("a probe of another kind resumes the verification's rounds")
	}
	if _, _, ok := next.FloorProbeResume("qg", FloorProbeKindVerify, 4*time.Minute); ok {
		t.Fatal("a probe of another candidate resumes the verification's rounds")
	}
}

// What the probe reads moved - the hold raised, the floor learned anew - and its kept rounds are of no probe the
// record has: the write that moved it clears them, and the next probe starts from none.
func TestAMoveOfWhatTheProbeReadsClearsItsKeptRounds(t *testing.T) {
	c, _, now := verifyingGroup(t, "")
	started := execution.EvaluationTime(now.Unix() - 3600)
	keepProgress(t, c, FloorProbeKindVerify, 5*time.Minute, 50, started)
	observeEarly(t, c, "qg", 500*time.Second, 400_000)
	if record, _ := c.Reading("qg"); record.Probe != nil {
		t.Fatalf("after a raise the record keeps %+v", record.Probe)
	}
	if _, _, ok := takenOver(t, c).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); ok {
		t.Fatal("rounds read under the hold before a raise resume")
	}
	// A renewal clearing the floor keeps the candidate, not the rounds read over the floor before.
	c, _, now = verifyingGroup(t, `,"earlier_floor_ms":100000,"earlier_floor_at_ms":1`)
	started = execution.EvaluationTime(now.Unix() - 3600)
	keepProgress(t, c, FloorProbeKindVerify, 5*time.Minute, 50, started)
	*now = now.Add(RenewInterval + time.Minute)
	if err := c.RenewDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if record, _ := c.Reading("qg"); record.Probe != nil || record.VerifyCandidateMillis == nil {
		t.Fatalf("after a renewal: kept %+v, candidate %v; want the rounds cleared, the candidate kept", record.Probe, record.VerifyCandidateMillis)
	}
}

// Kept rounds before a round that read differently are no evidence: the probe's end clears them in its own write,
// so the next process starts from none. A floor relearned is the same floor - the same probe, read again - and its
// rounds go too. Here 50 rounds were kept, a later round read differently, and the next owner took over after.
func TestAProbesEndClearsItsKeptRoundsInTheSameWrite(t *testing.T) {
	t.Run("the trials' candidate failed", func(t *testing.T) {
		c, store, now := verifyingGroup(t, "")
		started := execution.EvaluationTime(now.Unix() - 3600)
		keepProgress(t, c, FloorProbeKindVerify, 5*time.Minute, 50, started)
		writes := len(store.requests)
		verifyEnd(t, c, "qg", started+55*60, false)
		if got := len(store.requests) - writes; got != 1 {
			t.Fatalf("the failed probe wrote %d times, want once", got)
		}
		if record, _ := c.Reading("qg"); record.Probe != nil {
			t.Fatalf("after the failed probe the record keeps %+v", record.Probe)
		}
	})
	t.Run("the floor relearned", func(t *testing.T) {
		c, _, now := stuckGroup(t, "")
		*now = now.Add(FloorStanding)
		floor, kind, _, probing := c.FloorProbe("qg")
		if !probing || kind != FloorProbeKindFloor {
			t.Fatalf("fixture: probe %v %q %v, want the floor's", floor, kind, probing)
		}
		started := execution.EvaluationTime(now.Unix() - 3600)
		keepProgress(t, c, FloorProbeKindFloor, floor, 50, started)
		if _, _, ok := c.FloorProbeResume("qg", FloorProbeKindFloor, floor); !ok {
			t.Fatal("fixture: the floor's probe kept no rounds")
		}
		probeEnd(t, c, started+55*60, floor, 120*time.Second, false)
		next := takenOver(t, c)
		if clean, _, ok := next.FloorProbeResume("qg", FloorProbeKindFloor, floor); ok {
			t.Fatalf("the next owner resumes %d rounds read before the round that read differently", clean)
		}
	})
}

// Kept rounds of a probe over another floor than the record has - a record another build wrote - are no probe the
// record has: the next probe starts from none. Over the same floor, it goes on.
func TestKeptRoundsOverAnotherFloorAreNotResumed(t *testing.T) {
	for _, c := range []struct {
		name, kept string
		resumes    bool
	}{
		{"no floor as kept", `,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":400000,"clean_rounds":50,"started_slot":%[1]d,"kept_slot":%[1]d,"period_ms":60000}`, true},
		{"a floor the record has not", `,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":400000,"floor_ms":100000,"clean_rounds":50,"started_slot":%[1]d,"kept_slot":%[1]d,"period_ms":60000}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl, store, now := controllerFixture(t)
			*now = now.Add(48 * time.Hour)
			started := now.Unix() - 3600
			store.values["qg"] = []byte(fmt.Sprintf(`{"hold_ms":400000,"since_slot":60,"arrival_age_ms":430000,"verify_candidate_ms":300000`+
				c.kept+`}`, started))
			spec := groupSpec("qg", 0)
			spec.Step = time.Minute
			prepare(t, ctl, spec)
			if _, _, ok := ctl.FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); ok != c.resumes {
				t.Fatalf("resumed %v, want %v", ok, c.resumes)
			}
		})
	}
}

// Kept rounds go stale a FloorStanding after the latest of them was read, not after their probe started: a probe of a
// long period runs past a day - ProbationRounds rounds at twenty minutes are forty hours - and one taken over then
// goes on from its rounds, and a progress write that late is kept. Rounds whose latest is a day old say nothing of the
// data now: the next probe starts from none.
func TestKeptRoundsADayOldAreNoEvidence(t *testing.T) {
	c, _, now := verifyingGroup(t, "")
	started := execution.EvaluationTime(now.Unix() - 3600)
	kept := started + execution.EvaluationTime(30*time.Hour/time.Second)
	*now = time.Unix(int64(kept), 0)
	keepProgressAt(t, c, FloorProbeKindVerify, 5*time.Minute, 90, started, kept)
	if record, _ := c.Reading("qg"); record.Probe == nil || record.Probe.Clean != 90 || record.Probe.KeptSlot != kept {
		t.Fatalf("a progress write 30 h into its probe kept %+v", record.Probe)
	}
	if got := c.Stats().ProbeProgress; got[ProbeProgressWritten] != 1 {
		t.Fatalf("progress writes %v, want one written", got)
	}
	*now = time.Unix(int64(kept), 0).Add(FloorStanding - time.Second)
	if clean, from, ok := takenOver(t, c).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); !ok || clean != 90 || from != started {
		t.Fatalf("rounds kept within a day resumed %d from %d (%v), want 90 from %d", clean, from, ok, started)
	}
	*now = now.Add(time.Second)
	if _, _, ok := takenOver(t, c).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); ok {
		t.Fatal("rounds whose latest is a day old are resumed")
	}
}

// A probe of a long period runs past a day: ProbationRounds rounds take a day at twelve minutes, five at an hour, twenty
// at four hours, and longer where rounds are overtaken. Its progress is written every ProbeProgressEvery clean rounds
// all the way and kept each time, and a process that takes the group over past the probe's first day goes on from the
// rounds kept, from the Slot the probe started at - at four hours even forty hours after the latest write, just
// before the next would come. Kept rounds go stale at FloorStanding after the latest, or, where ProbeProgressEvery
// rounds take longer, at that and a round more: at four hours, 44 h.
func TestAProbeOfALongPeriodGoesOnPastItsFirstDay(t *testing.T) {
	for _, c := range []struct {
		name   string
		period time.Duration
		// slotsPerWrite is the Slots between two writes: ProbeProgressEvery
		// clean rounds and those overtaken between them.
		slotsPerWrite, writes int
		// takeover is how long after the latest write another process
		// takes the group over; stale is when the kept rounds are no
		// longer gone on from.
		takeover, stale time.Duration
	}{
		{"twelve minutes, two rounds in ten overtaken", 12 * time.Minute, 12, 11, 30 * time.Minute, FloorStanding},
		{"an hour", time.Hour, ProbeProgressEvery, 3, 30 * time.Minute, FloorStanding},
		{"four hours, taken over just before the next write", 4 * time.Hour, ProbeProgressEvery, 2, 40*time.Hour - time.Minute,
			time.Duration(ProbeProgressEvery+1) * 4 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl, _, now := verifyingGroup(t, "")
			started := execution.EvaluationTime(now.Unix())
			kept := started
			for write := 1; write <= c.writes; write++ {
				kept = started + execution.EvaluationTime(int64(write*c.slotsPerWrite)*int64(c.period/time.Second))
				*now = time.Unix(int64(kept), 0)
				keepProgressEvery(t, ctl, FloorProbeKindVerify, 5*time.Minute, write*ProbeProgressEvery, started, kept, c.period)
				if record, _ := ctl.Reading("qg"); record.Probe == nil || record.Probe.Clean != write*ProbeProgressEvery {
					t.Fatalf("write %d, %v into the probe, kept %+v", write, time.Duration(kept-started)*time.Second, record.Probe)
				}
			}
			if elapsed := time.Duration(kept-started) * time.Second; elapsed <= FloorStanding {
				t.Fatalf("fixture: the latest write is %v into the probe, want past its first day", elapsed)
			}
			latest := time.Unix(int64(kept), 0)
			*now = latest.Add(c.takeover)
			clean, from, ok := takenOver(t, ctl).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute)
			if want := c.writes * ProbeProgressEvery; !ok || clean != want || from != started {
				t.Fatalf("taken over %v after the latest write resumed %d from %d (%v), want %d from %d", c.takeover, clean, from, ok,
					want, started)
			}
			*now = latest.Add(c.stale - time.Second)
			if _, _, ok := takenOver(t, ctl).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); !ok {
				t.Fatalf("kept rounds %v old are not gone on from, want them fresh until %v", c.stale-time.Second, c.stale)
			}
			*now = latest.Add(c.stale)
			if _, _, ok := takenOver(t, ctl).FloorProbeResume("qg", FloorProbeKindVerify, 5*time.Minute); ok {
				t.Fatalf("kept rounds %v old are gone on from", c.stale)
			}
		})
	}
}

// Progress whose latest round precedes its probe's start is no progress of that probe: refused, and nothing written.
// One whose latest round is the Slot it started at is kept.
func TestProgressWhoseLatestRoundPrecedesItsProbeIsRefused(t *testing.T) {
	c, store, now := verifyingGroup(t, "")
	started := execution.EvaluationTime(now.Unix() - 3600)
	record, _ := c.Reading("qg")
	hold := current(record)
	writes := len(store.requests)
	err := c.FloorProbeProgress(context.Background(), FloorProbeProgressEvidence{Contract: holdContract("qg", started-60, hold),
		Floor: 5 * time.Minute, Kind: FloorProbeKindVerify, Hold: time.Duration(hold) * time.Millisecond, Clean: 10, Started: started,
		Period: time.Minute})
	if err == nil || len(store.requests) != writes {
		t.Fatalf("progress kept before its probe started: %v, %d writes", err, len(store.requests)-writes)
	}
	keepProgressAt(t, c, FloorProbeKindVerify, 5*time.Minute, 1, started, started)
	if record, _ := c.Reading("qg"); record.Probe == nil || record.Probe.KeptSlot != started {
		t.Fatalf("progress kept at its start: %+v", record.Probe)
	}
}

// A progress write that loses its compare - another write moved the record first - is dropped and counted, and the
// record keeps the move that won. One for a probe the record no longer has writes nothing.
func TestAProgressWriteNeverOvertakesAMove(t *testing.T) {
	c, store, now := verifyingGroup(t, "")
	started := execution.EvaluationTime(now.Unix() - 3600)
	// Another process's write lands first: the hold raised, the candidate gone.
	moved := []byte(`{"hold_ms":500000,"since_slot":60,"arrival_age_ms":530000}`)
	store.values["qg"] = moved
	keepProgress(t, c, FloorProbeKindVerify, 5*time.Minute, 50, started)
	if !bytes.Equal(store.values["qg"], moved) {
		t.Fatalf("the progress write replaced the move: %s", store.values["qg"])
	}
	if got := c.Stats().ProbeProgress; got[ProbeProgressConflict] != 1 || got[ProbeProgressWritten] != 0 {
		t.Fatalf("progress writes %v, want one conflict", got)
	}
	// Read again, the record has no such probe: nothing to write.
	fresh := takenOver(t, c)
	writes := len(store.requests)
	keepProgress(t, fresh, FloorProbeKindVerify, 5*time.Minute, 60, started)
	if got := len(store.requests) - writes; got != 0 || fresh.Stats().ProbeProgress[ProbeProgressStale] != 1 {
		t.Fatalf("a stale progress wrote %d times, counts %v", got, fresh.Stats().ProbeProgress)
	}
}

// A record written before probes kept their rounds has none and decodes; one with them decodes them, a record with
// none writes none, and one with a negative count is no record.
func TestKeptRoundsDecodeBothWays(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int
	}{
		{`{"hold_ms":600000,"since_slot":60}`, -1},
		{`{"hold_ms":600000,"since_slot":60,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":600000,"clean_rounds":50,"started_slot":120,"kept_slot":720,"period_ms":60000}}`, 50},
	} {
		record, err := Decode([]byte(c.raw))
		if err != nil || (record.Probe == nil) != (c.want < 0) || (record.Probe != nil && record.Probe.Clean != c.want) {
			t.Fatalf("%s decoded %+v %v", c.raw, record.Probe, err)
		}
		encoded, err := json.Marshal(record)
		if err != nil || strings.Contains(string(encoded), `"probe"`) != (c.want >= 0) {
			t.Fatalf("%s encoded %s %v", c.raw, encoded, err)
		}
	}
	for _, raw := range []string{
		`{"hold_ms":600000,"since_slot":60,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":600000,"clean_rounds":-1,"started_slot":120,"kept_slot":720,"period_ms":60000}}`,
		`{"hold_ms":600000,"since_slot":60,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":200000,"clean_rounds":5,"started_slot":120,"kept_slot":720,"period_ms":60000}}`,
		// No period.
		`{"hold_ms":600000,"since_slot":60,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":600000,"clean_rounds":5,"started_slot":120,"kept_slot":720}}`,
		// Its latest round before it started.
		`{"hold_ms":600000,"since_slot":60,"probe":{"kind":"verify","candidate_ms":300000,"hold_ms":600000,"clean_rounds":5,"started_slot":120,"kept_slot":60,"period_ms":60000}}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("%s decoded", raw)
		}
	}
}
