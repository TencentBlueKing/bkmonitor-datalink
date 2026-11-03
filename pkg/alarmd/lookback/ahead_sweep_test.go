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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// settlingWait is the readiness wait without a hold: a Slot is ready that long
// after its evaluation time, and its hold after that.
const settlingWait = 10 * time.Second

// periodQuery is the held first read of the Slot at slot of a Query Group
// detected every period, its data step the period, as access frees it.
func periodQuery(t *testing.T, slot int64, period, hold time.Duration) Query {
	t.Helper()
	q := query("qg", slot, period)
	q.Spec.PlanFacts = facts(t, period, "", execution.QueryClause{})
	q.Spec.ProviderRange, q.Spec.AcceptedRange = q.Spec.LogicalWindow, q.Spec.LogicalWindow
	digest, err := execution.DerivePhysicalQueryDigest(q.Spec)
	if err != nil {
		t.Fatal(err)
	}
	q.Spec.Digest = digest
	q.Contract.ReadHoldMillis = hold.Milliseconds()
	q.ReadyAt = time.Unix(slot, 0).Add(settlingWait + hold)
	q.FollowingSlot = execution.EvaluationTime(slot + int64(period/time.Second))
	return q
}

// lastBucket is the dataset a read of q's window answers: one point in its
// last bucket, the same at every read, so a trial that reads in time matches
// the formal read.
func lastBucket(spec execution.PhysicalQuerySpec) *execution.Dataset {
	step := spec.PlanFacts.StepMillis / 1000
	return dataset("h1", map[int64]string{spec.LogicalWindow.End - step: "1"})
}

// serialRun drives one Query Group's Slots the way its Runner does: one after
// another, each first attempted at its evaluation time or when the Slot before
// it was read, whichever is later, prepared again at its readiness and read
// there; the lookback's loop stepped every second between. It returns the
// trials' outcomes, what overtook the overtaken ones, and the reservations by
// how many Slots ahead.
func serialRun(t *testing.T, period, hold time.Duration, slots int) (outcomes, overtaken, ahead map[string]uint64) {
	t.Helper()
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	var endsMu sync.Mutex
	ends := map[string]uint64{}
	f.engine.options.OnEarlierTrialEnd = func(end EarlierTrialEnd) {
		endsMu.Lock()
		ends[end.Outcome]++
		endsMu.Unlock()
	}
	f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return hold }
	f.engine.options.Recheck = func(_ context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		return full(lastBucket(spec))(sink)
	}
	ctx := context.Background()
	start := f.clock.now().Truncate(period).Add(period)
	running := func() bool {
		f.engine.mu.Lock()
		defer f.engine.mu.Unlock()
		state := f.engine.groups["qg"]
		return state != nil && state.prepared != nil && state.prepared.running
	}
	// advance steps the clock a second at a time to until, letting every trial
	// started on the way answer before the clock moves on.
	advance := func(until time.Time) {
		for now := f.clock.now(); now.Before(until); now = now.Add(time.Second) {
			f.clock.set(now)
			f.engine.StepEarly(ctx)
			f.engine.Step(ctx)
			testwait.Until(t, "the trial started on the way answering", func() bool { return !running() })
		}
		f.clock.set(until)
	}
	// The Slot before the first was read at its held readiness: the run starts
	// as the Runner runs held Slots, each first attempted behind the last.
	readAt := start.Add(-period + settlingWait + hold)
	for index := 0; index < slots; index++ {
		evaluation := start.Add(time.Duration(index) * period)
		q := periodQuery(t, evaluation.Unix(), period, hold)
		attempt := evaluation
		if readAt.After(attempt) {
			attempt = readAt
		}
		advance(attempt)
		f.engine.Prepare(q)
		advance(q.ReadyAt)
		f.engine.Prepare(q)
		read := f.engine.Begin(q)
		read.Series(lastBucket(q.Spec), 100)
		read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
		readAt = q.ReadyAt.Add(time.Second)
	}
	advance(f.clock.now().Add(period))
	stats := f.engine.Stats().Sources[sourceTimeSeries]
	// Every trial counted ended once in the per-group reading too.
	endsMu.Lock()
	for outcome, count := range stats.EarlierReads {
		if ends[outcome] != count {
			t.Errorf("trial ends %v, want each counted outcome once: %v", ends, stats.EarlierReads)
			break
		}
	}
	endsMu.Unlock()
	return stats.EarlierReads, stats.EarlierOvertaken, stats.EarlierAhead
}

// Across the range of holds a trial cannot reach at its own Slot - from just
// past the period less the reread bound to the hold limit, at the periods
// strategies detect at - the trial reserved ahead reads in time: trials
// complete and match their formal reads, and none is prepared, started or
// finished past its point. One Slot ahead covers holds short of twice the
// period less the bound; the rest need the trial of a Slot further on.
func TestATrialReservedAheadReachesEveryHoldUpToTheLimit(t *testing.T) {
	const limit = 600 * time.Second
	bound := RecheckTimeout
	for _, period := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute} {
		one := 2*period - bound
		holds := []time.Duration{period - bound + time.Second, one - time.Second, one, one + 500*time.Millisecond,
			2 * period, 3 * period, 5*period + 500*time.Millisecond, limit / 2, limit}
		for _, hold := range holds {
			t.Run(fmt.Sprintf("P=%v h=%v", period, hold), func(t *testing.T) {
				t.Parallel()
				slots := int(hold/period) + 12
				outcomes, overtaken, ahead := serialRun(t, period, hold, slots)
				for _, path := range []string{OvertakenPreparedLate, OvertakenStartedLate, OvertakenFinishedLate} {
					if overtaken[path] != 0 {
						t.Errorf("%d trials overtaken %s: outcomes %v, overtaken %v, ahead %v", overtaken[path], path, outcomes, overtaken, ahead)
					}
				}
				if outcomes[EarlierEqual] == 0 {
					t.Errorf("no trial matched its formal read: outcomes %v, overtaken %v, ahead %v", outcomes, overtaken, ahead)
				}
				if hold-execution.LoweredReadHold(hold, period) >= one && ahead["1"] != 0 {
					t.Errorf("a hold past one Slot ahead's reach reserved one Slot ahead: %v", ahead)
				}
			})
		}
	}
}

// Past the next Slot's trial start, the trial of the Slot after it is
// reserved, counted two Slots ahead, and the Slots between keep it standing.
func TestTheFirstSlotAheadWhoseTrialStartsAfterNowIsReserved(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	slot := f.clock.now().Unix()
	q := scheduledQuery(t, slot)
	// The next Slot's trial starts the reread bound before slot+180; it is
	// past, the one after's is not.
	f.clock.set(time.Unix(slot+200, 0))
	f.engine.Prepare(q)
	trial := preparedTrial(f)
	if trial == nil || !trial.ahead || trial.aheadSlots != 2 || trial.query.Contract.Slot.EvaluationTime != execution.EvaluationTime(slot+120) ||
		!trial.query.ReadyAt.Equal(q.ReadyAt.Add(2*time.Minute)) || !trial.at.Equal(trial.readyAt.Add(-RecheckTimeout)) {
		t.Fatalf("reserved %+v, want the trial two Slots ahead", trial)
	}
	if got := f.engine.Stats().Sources[sourceTimeSeries].EarlierAhead; got["2"] != 1 || got["1"] != 0 {
		t.Fatalf("ahead counted %v, want two Slots once", got)
	}
	if reading, _ := f.engine.GroupReading("qg"); reading.EarlierAhead["2"] != 1 {
		t.Fatalf("the group's reading ahead %v", reading.EarlierAhead)
	}
	between := scheduledQuery(t, slot+60)
	f.engine.Prepare(between)
	formalRead(f, between)
	if preparedTrial(f) != trial || trial.done {
		t.Fatal("the Slot between ended the trial reserved past it")
	}
}

// A trial reserved ahead whose Slot is frozen otherwise is dropped, and that
// Slot goes on to reserve as any Slot does: a moved hold costs one trial, and
// the next is reserved at once.
func TestASlotThatDropsATrialReservedAheadReservesAgain(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) {
		if evidence.Observed {
			t.Errorf("a trial gave evidence %+v", evidence)
		}
	}
	var ends []EarlierTrialEnd
	f.engine.options.OnEarlierTrialEnd = func(end EarlierTrialEnd) { ends = append(ends, end) }
	slot := f.clock.now().Unix()
	q := scheduledQuery(t, slot)
	f.clock.set(q.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(q)
	trial := preparedTrial(f)
	if trial == nil || !trial.ahead {
		t.Fatalf("fixture: reserved %+v", trial)
	}
	reserved := time.Duration(trial.query.Contract.ReadHoldMillis) * time.Millisecond
	// Its Slot frozen a hold of 3 minutes, first attempted past where its own
	// trial had to start.
	next := scheduledQuery(t, slot+60)
	next.Contract.ReadHoldMillis = (3 * time.Minute).Milliseconds()
	next.ReadyAt = next.ReadyAt.Add(time.Minute)
	f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return 3 * time.Minute }
	f.clock.set(next.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(next)
	if got := aheadOvertaken(f); len(got) != 1 || got[OvertakenHoldMoved] != 1 {
		t.Fatalf("overtaken by %v, want hold_moved once", got)
	}
	// The drop reaches the per-group reading at the loop's next step.
	f.engine.Step(context.Background())
	if len(ends) != 1 || ends[0].Outcome != EarlierOvertaken || ends[0].OvertakenBy != OvertakenHoldMoved || ends[0].AheadSlots != 1 ||
		ends[0].Contract.Slot != next.Contract.Slot || ends[0].ReservedHold != reserved || ends[0].FrozenHold != 3*time.Minute {
		t.Fatalf("trial ends %+v, want the dropped trial once, hold_moved, one Slot ahead, reserved at %v and frozen at 3 minutes", ends, reserved)
	}
	again := preparedTrial(f)
	if again == nil || again == trial || !again.ahead || again.query.Contract.Slot.EvaluationTime <= next.Contract.Slot.EvaluationTime ||
		again.query.Contract.ReadHoldMillis != (3*time.Minute).Milliseconds() {
		t.Fatalf("after the drop reserved %+v, want a later Slot's trial under the moved hold", again)
	}
}

// With the controller's candidate given, the trial tries that, not half the
// hold: its point and its evidence are the given candidate's, a candidate that
// moves before the formal read leaves it hold_moved, and none given reserves
// none.
func TestATrialTriesTheGivenCandidate(t *testing.T) {
	for _, c := range []struct {
		name   string
		moved  bool
		none   bool
		reason string
	}{
		{"the given candidate", false, false, ""},
		{"moved before the read", true, false, OvertakenHoldMoved},
		{"none given", false, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			observed := make(chan EarlierReadEvidence, 1)
			f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) { observed <- evidence }
			given := 90 * time.Second
			f.engine.options.CurrentCandidate = func(execution.QueryGroupIdentity) (time.Duration, bool) { return given, !c.none }
			q := heldQuery(f)
			f.engine.Prepare(q)
			f.engine.mu.Lock()
			var trial *earlierSample
			if state := f.engine.groups["qg"]; state != nil {
				trial = state.prepared
			}
			f.engine.mu.Unlock()
			if c.none {
				if trial != nil {
					t.Fatalf("no candidate given, reserved %+v", trial)
				}
				return
			}
			baseline := q.ReadyAt.Add(-2 * time.Minute)
			if trial == nil || trial.candidateHold != given || !trial.readyAt.Equal(baseline.Add(given)) {
				t.Fatalf("reserved %+v, want the given 90 s candidate", trial)
			}
			f.clock.set(trial.at)
			f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
			f.engine.StepEarly(context.Background())
			f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
			if c.moved {
				given = 105 * time.Second
			}
			formalRead(f, q)
			evidence := <-observed
			if c.moved {
				if evidence.Observed || overtakenPaths(f)[OvertakenHoldMoved] != 1 {
					t.Fatalf("a moved candidate's trial: evidence %+v, overtaken %v", evidence, overtakenPaths(f))
				}
				return
			}
			if !evidence.Equal || evidence.CandidateHold != 90*time.Second {
				t.Fatalf("evidence %+v, want an equal trial of 90 s", evidence)
			}
		})
	}
}

// A Slot the controller gives no candidate for is heard as refused where a
// trial would have been reserved for it - the group's rest over, its sample
// not in flight, no trial reserved ahead for a later Slot - once however
// often the Slot is prepared, and not at all while the group rests or a
// trial is reserved ahead. The trial a candidate was given for is not heard.
func TestARefusedTrialIsHeardOnlyWhereOneWouldBeReserved(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	tries := false
	f.engine.options.CurrentCandidate = func(execution.QueryGroupIdentity) (time.Duration, bool) { return 90 * time.Second, tries }
	var heard []execution.QueryGroupIdentity
	f.engine.options.EarlierRefused = func(qg execution.QueryGroupIdentity) { heard = append(heard, qg) }
	q := heldQuery(f)
	f.engine.Prepare(q)
	f.engine.Prepare(q)
	if len(heard) != 1 || heard[0] != "qg" {
		t.Fatalf("heard %v for one Slot prepared twice, want qg once", heard)
	}
	// The group resting past the next Slot's readiness: no trial would be
	// reserved for it, and none is heard.
	next := q
	next.Contract.Slot.EvaluationTime += 60
	next.ReadyAt = q.ReadyAt.Add(time.Minute)
	f.engine.mu.Lock()
	f.engine.groups["qg"].nextAt = next.ReadyAt.Add(time.Hour)
	f.engine.mu.Unlock()
	f.engine.Prepare(next)
	if len(heard) != 1 {
		t.Fatalf("heard %v for a Slot while its group rests", heard)
	}
	// A candidate given: a trial is reserved, and nothing is heard.
	f.engine.mu.Lock()
	f.engine.groups["qg"].nextAt = time.Time{}
	f.engine.mu.Unlock()
	tries = true
	later := next
	later.Contract.Slot.EvaluationTime += 60
	later.ReadyAt = next.ReadyAt.Add(time.Minute)
	f.engine.Prepare(later)
	if len(heard) != 1 || preparedTrial(f) == nil {
		t.Fatalf("a given candidate: heard %v, reserved %+v", heard, preparedTrial(f))
	}
	// Refused at the next Slot while that trial still waits for its own
	// Slot's formal read: with a candidate, Prepare would drop it and reserve
	// the next Slot's, so it stands in nothing's way and the refusal is heard.
	tries = false
	last := later
	last.Contract.Slot.EvaluationTime += 60
	last.ReadyAt = later.ReadyAt.Add(time.Minute)
	f.engine.Prepare(last)
	if len(heard) != 2 {
		t.Fatalf("heard %v at the Slot after a trial still waiting, want it heard", heard)
	}
	// A trial reserved ahead, for a Slot after this one: this Slot's own trial
	// would not be reserved, it is counted trial_reserved and not heard.
	final := last
	final.Contract.Slot.EvaluationTime += 60
	final.ReadyAt = last.ReadyAt.Add(time.Minute)
	f.engine.mu.Lock()
	f.engine.groups["qg"].prepared = &earlierSample{query: final, ahead: true, aheadSlots: 1, group: f.engine.groups["qg"]}
	f.engine.groups["qg"].prepared.query.Contract.Slot.EvaluationTime += 60
	f.engine.mu.Unlock()
	f.engine.Prepare(final)
	if len(heard) != 2 {
		t.Fatalf("heard %v while a trial is reserved ahead", heard)
	}
	if sources, _ := skippedBy(f); sources[EarlierSkipTrialReserved] != 1 {
		t.Fatalf("skipped %v, want the Slot behind a trial reserved ahead counted trial_reserved", sources)
	}
}
