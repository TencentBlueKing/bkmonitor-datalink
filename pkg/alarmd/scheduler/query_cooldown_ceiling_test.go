// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// timed is a completed Slot whose primary query came back in elapsed
// milliseconds: answered, or unavailable for reason.
func timed(elapsed int64, reason string) execution.SlotExecutionResult {
	result := execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable,
		PrimaryQueryClock: &execution.PhysicalQueryClock{BudgetMillis: 45_000, ElapsedMillis: elapsed}}
	if reason != "" {
		result.QueryAvailability, result.QueryUnavailableReason = execution.QueryAvailabilityUnavailable, execution.ReasonCode(reason)
	}
	return result
}

// ceilingPool is one Query Group read every minute, its normal rounds
// recorded as the Runner records them.
type ceilingPool struct {
	t      *testing.T
	runner *Runner
	slot   FrozenSlot
	now    time.Time
	lines  []observability.QueryCooldownFacts
}

func newCeilingPool(t *testing.T, store QueryCooldownStore, epoch uint64) *ceilingPool {
	pool := &ceilingPool{t: t, slot: poolSlot(), now: time.Unix(1_000, 0)}
	pool.runner = poolRunner(store, &pool.now, &pool.lines)
	pool.runner.restoreQueryCooldown(context.Background(), fenceAt(epoch))
	return pool
}

// round records the next minute's normal round, past the pool's cooldown
// when the Query Group is in it, so a round there is the deciding one.
func (pool *ceilingPool) round(result execution.SlotExecutionResult) {
	pool.now = pool.now.Add(time.Minute)
	if until := pool.runner.queryCooldown.until; !until.IsZero() && pool.now.Before(until) {
		pool.now = until.Add(time.Second)
	}
	pool.slot.Contract.Slot.EvaluationTime = execution.EvaluationTime(pool.now.Unix())
	pool.runner.recordQueryAvailability(context.Background(), pool.slot, execution.OperationNormal, result, 60,
		pool.runner.queryCooldownDecidedBy(execution.OperationNormal))
}

func (pool *ceilingPool) in() bool { return !pool.runner.queryCooldown.until.IsZero() }

// enter runs answers at runs, then three failures of reason after cut ms.
func (pool *ceilingPool) enter(runs []int64, cut int64, reason string) {
	for _, run := range runs {
		pool.round(timed(run, ""))
	}
	for i := 0; i < unavailableThreshold; i++ {
		pool.round(timed(cut, reason))
	}
	if !pool.in() {
		pool.t.Fatalf("three failures left the Query Group out of the pool: %+v", pool.runner.queryCooldown)
	}
}

func (pool *ceilingPool) lastRule() string {
	if probe := pool.runner.cooldownMemory.lastProbe; probe != nil {
		return probe.Rule
	}
	return ""
}

var slowRuns = []int64{28_100, 29_900, 28_600, 29_100, 28_400, 29_500, 28_900, 29_300}

// No Query Group is held for good: one whose answers keep coming back near
// the cut leaves after as many of them in a row as its run times keep - a
// backend answering that often answers its normal rounds - and a probe the
// backend cuts in between starts the count again. Its runs spread from 20 to
// 29.9 s, so every 29.9 s answer is within that spread of the 30 s cut.
func TestAnswersInARowNearTheCeilingLetTheQueryGroupOut(t *testing.T) {
	spreadRuns := []int64{28_100, 29_900, 28_600, 29_100, 28_400, 29_500, 28_900, 20_000}
	pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
	pool.enter(spreadRuns, 30_000, contract.ReasonQueryUnavailable)
	for i := 0; i < queryCooldownRunTimesKept-1; i++ {
		pool.round(timed(29_900, ""))
		if !pool.in() || pool.lastRule() != observability.QueryCooldownRuleNearCeiling {
			t.Fatalf("answer %d near the ceiling: in %t, rule %q; want it held near the ceiling", i+1, pool.in(), pool.lastRule())
		}
	}
	pool.round(timed(29_900, ""))
	if pool.in() || pool.lastRule() != observability.QueryCooldownRuleAnsweredInRow {
		t.Fatalf("after %d answers in a row: in %t, rule %q; want it out, answered in a row", queryCooldownRunTimesKept, pool.in(), pool.lastRule())
	}
	if probes := pool.runner.cooldownMemory.probes; len(probes) != queryCooldownRunTimesKept {
		t.Fatalf("%d probes kept, want the latest %d", len(probes), queryCooldownRunTimesKept)
	}

	broken := newCeilingPool(t, newMemoryCooldownStore(), 1)
	broken.enter(spreadRuns, 30_000, contract.ReasonQueryUnavailable)
	for i := 0; i < 3; i++ {
		broken.round(timed(29_900, ""))
	}
	broken.round(timed(30_000, contract.ReasonQueryUnavailable))
	broken.round(timed(29_900, ""))
	if !broken.in() || broken.runner.cooldownMemory.answeredInRow != 1 {
		t.Fatalf("after a cut probe and one answer: in %t, %d in a row; want held and the count started again",
			broken.in(), broken.runner.cooldownMemory.answeredInRow)
	}
}

// An answer clear of the cut by its runs' spread leaves at once: runs of
// 28.1 to 29.9 s spread 1.8 s, so 28.1 s clears a 30 s cut and 28.3 s does
// not.
func TestAnAnswerClearOfTheCutByItsSpreadLeaves(t *testing.T) {
	held := newCeilingPool(t, newMemoryCooldownStore(), 1)
	held.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	held.round(timed(28_300, ""))
	if !held.in() || held.lastRule() != observability.QueryCooldownRuleNearCeiling {
		t.Fatalf("28.3 s against 30 s less 1.8 s: in %t, rule %q; want it held", held.in(), held.lastRule())
	}
	out := newCeilingPool(t, newMemoryCooldownStore(), 1)
	out.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	out.round(timed(28_100, ""))
	if out.in() || out.lastRule() != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("28.1 s against 30 s less 1.8 s: in %t, rule %q; want it out", out.in(), out.lastRule())
	}
}

// Only a failure that ran longer than every answer shows where the backend
// cuts: a quick unavailable answer is an error, and a timeout is alarmd's own
// deadline. Neither sets a ceiling, and the first answer takes the Query
// Group out as it always did.
func TestOnlyASlowerThanEveryAnswerBackendFailureIsACut(t *testing.T) {
	for _, test := range []struct {
		name    string
		elapsed int64
		reason  string
	}{
		{"an unavailable answer quicker than the answers", 1_000, contract.ReasonQueryUnavailable},
		{"alarmd's own deadline", 30_000, contract.ReasonQueryTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
			pool.enter(slowRuns, test.elapsed, test.reason)
			if pool.runner.cooldownMemory.ceiling != 0 {
				t.Fatalf("ceiling %d after %s, want none", pool.runner.cooldownMemory.ceiling, test.name)
			}
			pool.round(timed(29_900, ""))
			if pool.in() || pool.lastRule() != observability.QueryCooldownRuleNoCeiling {
				t.Fatalf("an answer with no ceiling: in %t, rule %q; want it out", pool.in(), pool.lastRule())
			}
		})
	}
}

// An answer whose time was not measured cannot be held to the ceiling, and
// leaves as an answer did.
func TestAnUnmeasuredAnswerLeavesAsBefore(t *testing.T) {
	pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
	pool.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	pool.round(execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable})
	if pool.in() || pool.lastRule() != observability.QueryCooldownRuleUnmeasured {
		t.Fatalf("an unmeasured answer: in %t, rule %q; want it out", pool.in(), pool.lastRule())
	}
}

// The run times and the cut are the query's: another query revision starts
// them over.
func TestAnotherQueryRevisionStartsTheRunFactsOver(t *testing.T) {
	pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
	pool.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	pool.runner.clearQueryCooldown(context.Background(), QueryCooldownQueryRevisionChanged)
	pool.slot.Contract.QueryRevision += "-next"
	pool.round(timed(5_000, ""))
	if memory := pool.runner.cooldownMemory; memory.ceiling != 0 || len(memory.runTimes) != 1 || memory.runTimes[0] != 5_000 {
		t.Fatalf("after another query revision: ceiling %d, runs %v; want none and the one new run", memory.ceiling, memory.runTimes)
	}
}

// A restart or a new owner holds the Query Group to the same ceiling, with
// the same runs and probes: the record carries them.
func TestTheCeilingIsRestoredWithThePool(t *testing.T) {
	store := newMemoryCooldownStore()
	before := newCeilingPool(t, store, 1)
	before.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	before.round(timed(29_900, ""))
	if !before.in() {
		t.Fatal("an answer near the ceiling let the Query Group out")
	}
	after := newCeilingPool(t, store, 2)
	after.now = before.now
	memory := after.runner.cooldownMemory
	if memory.ceiling != 30_000 || len(memory.runTimes) != queryCooldownRunTimesKept || len(memory.probes) != 1 || memory.answeredInRow != 1 {
		t.Fatalf("restored ceiling %d, runs %v, probes %d, %d in a row; want the record's", memory.ceiling, memory.runTimes, len(memory.probes), memory.answeredInRow)
	}
	after.round(timed(29_900, ""))
	if !after.in() || after.lastRule() != observability.QueryCooldownRuleNearCeiling {
		t.Fatalf("the new owner's answer near the ceiling: in %t, rule %q; want it held", after.in(), after.lastRule())
	}
}

// reenter takes a Query Group in the pool out on an answer clear of the cut
// by its spread, waits away, then runs it outside on runs and puts it back
// with three more cuts.
func (pool *ceilingPool) reenter(away time.Duration, runs []int64) {
	pool.round(timed(20_000, ""))
	if pool.in() || pool.lastRule() != observability.QueryCooldownRuleUnderCeiling {
		pool.t.Fatalf("a first entry's 20 s answer: in %t, rule %q; want it out under the ceiling", pool.in(), pool.lastRule())
	}
	pool.now = pool.now.Add(away)
	pool.enter(runs, 30_000, contract.ReasonQueryUnavailable)
}

// A Query Group back in the pool within the re-entry window of an answer
// letting it out stays on an answer clear of its cut, timed or not, and
// leaves on answers in a row. One back after the window, or after leaving
// for another reason than an answer, is decided as a first entry is. A
// restart or the next owner holds it the same way.
func TestAReenteredQueryGroupLeavesOnlyOnEvidenceOfItsOwn(t *testing.T) {
	inRow := newCeilingPool(t, newMemoryCooldownStore(), 1)
	inRow.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	inRow.reenter(time.Hour, slowRuns)
	for i := 0; i < queryCooldownRunTimesKept-1; i++ {
		inRow.round(timed(20_000, ""))
		if !inRow.in() || inRow.lastRule() != observability.QueryCooldownRuleHeldAfterReentry {
			t.Fatalf("re-entered answer %d at 20 s: in %t, rule %q; want it held after the re-entry", i+1, inRow.in(), inRow.lastRule())
		}
	}
	inRow.round(timed(29_900, ""))
	if inRow.in() || inRow.lastRule() != observability.QueryCooldownRuleAnsweredInRow {
		t.Fatalf("after %d answers in a row: in %t, rule %q; want it out, answered in a row", queryCooldownRunTimesKept, inRow.in(), inRow.lastRule())
	}

	untimed := newCeilingPool(t, newMemoryCooldownStore(), 1)
	untimed.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	untimed.reenter(time.Hour, slowRuns)
	untimed.round(execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable})
	if !untimed.in() || untimed.lastRule() != observability.QueryCooldownRuleHeldAfterReentry {
		t.Fatalf("a re-entered untimed answer: in %t, rule %q; want it held after the re-entry", untimed.in(), untimed.lastRule())
	}

	late := newCeilingPool(t, newMemoryCooldownStore(), 1)
	late.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	late.reenter(QueryCooldownReentryWindow, slowRuns)
	late.round(timed(20_000, ""))
	if late.in() || late.lastRule() != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("back after the re-entry window: in %t, rule %q; want it out under the ceiling as a first entry", late.in(), late.lastRule())
	}

	changed := newCeilingPool(t, newMemoryCooldownStore(), 1)
	changed.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	changed.runner.clearQueryCooldown(context.Background(), QueryCooldownQueryRevisionChanged)
	changed.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	changed.round(timed(20_000, ""))
	if changed.in() || changed.lastRule() != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("back after leaving on a query change: in %t, rule %q; want it out under the ceiling", changed.in(), changed.lastRule())
	}

	// The query service down for everyone fails every query fast: the
	// re-entry shows no cut, and its first answer once the service is back
	// leaves as a first entry's would, under the ceiling the earlier stay
	// set.
	outage := newCeilingPool(t, newMemoryCooldownStore(), 1)
	outage.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	outage.round(timed(20_000, ""))
	outage.now = outage.now.Add(time.Hour)
	outage.enter(slowRuns, 1_000, contract.ReasonQueryUnavailable)
	if outage.runner.queryCooldown.cuts != 0 || outage.runner.cooldownMemory.ceiling != 30_000 {
		t.Fatalf("a re-entry on fast failures: %d cuts, ceiling %d; want no cut and the earlier ceiling kept",
			outage.runner.queryCooldown.cuts, outage.runner.cooldownMemory.ceiling)
	}
	outage.round(timed(1_000, ""))
	if outage.in() || outage.lastRule() != observability.QueryCooldownRuleUnderCeiling {
		t.Fatalf("the first 1 s answer after an outage re-entry: in %t, rule %q; want it out under the ceiling", outage.in(), outage.lastRule())
	}

	store := newMemoryCooldownStore()
	before := newCeilingPool(t, store, 1)
	before.enter(slowRuns, 30_000, contract.ReasonQueryUnavailable)
	before.reenter(time.Hour, slowRuns)
	if record, _, _ := store.LoadQueryCooldown(context.Background(), before.runner.queryGroup); record.Cuts != unavailableThreshold {
		t.Fatalf("the re-entry's record carries %d cuts, want %d", record.Cuts, unavailableThreshold)
	}
	after := newCeilingPool(t, store, 2)
	after.now = before.now
	after.round(timed(20_000, ""))
	if !after.in() || after.lastRule() != observability.QueryCooldownRuleHeldAfterReentry {
		t.Fatalf("the new owner's re-entered 20 s answer: in %t, rule %q; want it held after the re-entry", after.in(), after.lastRule())
	}
}

// The re-entry window is the one a re-entry is counted in: an entry a full
// window after an answer let the Query Group out is a first entry again.
func TestTheReentryHoldEndsWithTheReentryWindow(t *testing.T) {
	exited := time.Unix(1_000, 0)
	for _, test := range []struct {
		after time.Duration
		held  bool
	}{{QueryCooldownReentryWindow - time.Nanosecond, true}, {QueryCooldownReentryWindow, false}} {
		runner := &Runner{queryCooldown: queryCooldownState{cuts: 1}, cooldownMemory: queryCooldownMemory{exitedAt: exited,
			exitReason: QueryCooldownRecovered, enteredAt: exited.Add(test.after)}}
		if held := runner.reenteredAfterAnAnswer(); held != test.held {
			t.Fatalf("entered %s after an answer let it out: held %t, want %t", test.after, held, test.held)
		}
	}
}

// replayRound records a replay at the cooldown's end of the first Slot held
// after the last round, as the Runner records it.
func (pool *ceilingPool) replayRound(result execution.SlotExecutionResult) {
	pool.slot.Contract.Slot.EvaluationTime += 60
	if until := pool.runner.queryCooldown.until; !until.IsZero() && pool.now.Before(until) {
		pool.now = until.Add(time.Second)
	}
	pool.runner.recordQueryAvailability(context.Background(), pool.slot, execution.OperationReplay, result, 60,
		pool.runner.queryCooldownDecidedBy(execution.OperationReplay))
}

// Only a refusal no time range changes lets a replay decide: a missing
// table and a storage that cannot read sliding windows are refused for any
// window, while a backend that did not answer may answer a replay's longer
// budget and not the next round's.
func TestOnlyATimeIndependentRefusalLetsAReplayDecide(t *testing.T) {
	for _, test := range []struct {
		reason  string
		decides bool
	}{
		{contract.ReasonQueryTargetMissing, true},
		{contract.ReasonDetectIntervalStorageNotSliding, true},
		{contract.ReasonQueryUnavailable, false},
		{contract.ReasonQueryTimeout, false},
	} {
		pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
		pool.enter(slowRuns, 1_000, test.reason)
		if decides := pool.runner.queryCooldownDecidedBy(execution.OperationReplay); decides != test.decides {
			t.Errorf("a replay in the pool for %s decides %t, want %t", test.reason, decides, test.decides)
		}
		if !pool.runner.queryCooldownDecidedBy(execution.OperationNormal) {
			t.Errorf("a normal round in the pool for %s does not decide", test.reason)
		}
		pool.replayRound(timed(1_000, ""))
		if pool.in() == test.decides {
			t.Errorf("an answered replay in the pool for %s: in %t, want out %t", test.reason, pool.in(), test.decides)
		}
	}
}

// With no held Slot at the cooldown's end, a pool held for a missing table is
// decided by its normal round as before; and a Query Group back in for a
// missing table after an answer let it out leaves on the answer: those
// refusals show no cut, so the re-entry hold does not apply and the exit is
// no_ceiling. Refused again, a replay keeps it in and extends the cooldown.
func TestAMissingTablePoolIsDecidedByWhicheverRoundComesFirst(t *testing.T) {
	normal := newCeilingPool(t, newMemoryCooldownStore(), 1)
	normal.enter(slowRuns, 1_000, contract.ReasonQueryTargetMissing)
	normal.round(timed(1_000, ""))
	if normal.in() || normal.lastRule() != observability.QueryCooldownRuleNoCeiling {
		t.Fatalf("a normal round answering a missing-table pool: in %t, rule %q; want it out with no ceiling", normal.in(), normal.lastRule())
	}

	refused := newCeilingPool(t, newMemoryCooldownStore(), 1)
	refused.enter(slowRuns, 1_000, contract.ReasonQueryTargetMissing)
	until := refused.runner.queryCooldown.until
	refused.replayRound(timed(1_000, contract.ReasonQueryTargetMissing))
	if !refused.in() || !refused.runner.queryCooldown.until.After(until) || refused.runner.cooldownMemory.lastProbe.Operation != string(execution.OperationReplay) {
		t.Fatalf("a refused replay: in %t, until %s after %s; want it held, the cooldown extended and the replay its probe",
			refused.in(), refused.runner.queryCooldown.until, until)
	}

	back := newCeilingPool(t, newMemoryCooldownStore(), 1)
	back.enter(slowRuns, 1_000, contract.ReasonQueryTargetMissing)
	back.replayRound(timed(1_000, ""))
	if back.in() {
		t.Fatal("an answered replay left a missing-table pool held")
	}
	back.now = back.now.Add(time.Hour)
	back.enter(slowRuns, 1_000, contract.ReasonQueryTargetMissing)
	if back.runner.cooldownMemory.reentries != 1 || back.runner.queryCooldown.cuts != 0 {
		t.Fatalf("back for the missing table: %d re-entries, %d cuts; want one re-entry and no cut", back.runner.cooldownMemory.reentries, back.runner.queryCooldown.cuts)
	}
	back.replayRound(timed(1_000, ""))
	if back.in() || back.lastRule() != observability.QueryCooldownRuleNoCeiling {
		t.Fatalf("a re-entered missing-table pool answered by its replay: in %t, rule %q; want it out with no ceiling", back.in(), back.lastRule())
	}
}

// A held replay at a missing-table pool's cooldown end that the Runner sent
// late and that timed out decides nothing and spends nothing: the excuse for
// a timeout the Runner made late itself is still there for the normal round
// queued behind the replays, and the timeout is not read as the backend's. A
// replay the backend answered unavailable decides nothing either.
func TestAReplayTheReasonDoesNotCoverSpendsNothingOfThePool(t *testing.T) {
	pool := newCeilingPool(t, newMemoryCooldownStore(), 1)
	pool.enter(slowRuns, 1_000, contract.ReasonQueryTargetMissing)
	pool.now = pool.runner.queryCooldown.until.Add(time.Second)
	busyUntil := pool.now
	lateTimeout := execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnknown,
		QueryUnknownReason: execution.ReasonCode(contract.ReasonQueryTimeout),
		PrimaryQueryClock:  &execution.PhysicalQueryClock{ReadyAtUnixMilli: busyUntil.Add(-time.Minute).UnixMilli(), ElapsedMillis: 55_000}}
	unavailable := timed(30_000, contract.ReasonQueryUnavailable)
	for _, result := range []execution.SlotExecutionResult{lateTimeout, unavailable} {
		decides, read := pool.runner.queryCooldownDecision(execution.OperationReplay, result, busyUntil)
		if decides || read.QueryAvailability != result.QueryAvailability || !pool.runner.queryCooldown.excusedUntil.IsZero() {
			t.Fatalf("a held replay %+v: decides %t, read as %v, excused until %s; want nothing decided, read or spent",
				result.QueryAvailability, decides, read.QueryAvailability, pool.runner.queryCooldown.excusedUntil)
		}
	}
	decides, _ := pool.runner.queryCooldownDecision(execution.OperationNormal, lateTimeout, busyUntil)
	if decides || !pool.runner.queryCooldown.excusedUntil.Equal(pool.runner.queryCooldown.until) {
		t.Fatalf("the normal round that timed out late behind the replays: decides %t, excused until %s; want it excused",
			decides, pool.runner.queryCooldown.excusedUntil)
	}
	decides, read := pool.runner.queryCooldownDecision(execution.OperationReplay, timed(1_000, contract.ReasonQueryTargetMissing), busyUntil)
	if !decides || read.QueryUnavailableReason != execution.ReasonCode(contract.ReasonQueryTargetMissing) {
		t.Fatalf("a held replay refused for the table: decides %t, read %+v; want it to decide on the refusal", decides, read)
	}
}

// A storage that gives up on a query too large for it answers with a client
// error: a refusal slower than every answer is the backend's cut as an
// unavailable answer is, and sets the ceiling the same way.
func TestARefusalSlowerThanAnyAnswerIsTheCut(t *testing.T) {
	pool := newCeilingPool(t, nil, 1)
	for _, run := range slowRuns {
		pool.round(timed(run, ""))
	}
	pool.round(timed(30_000, contract.ReasonQueryRejected))
	if got := pool.runner.cooldownMemory.ceiling; got != 30_000 {
		t.Fatalf("ceiling %d after a 30 s refusal, want the refusal's time", got)
	}
}

// A query whose deadline passed while alarmd was delivering the answer is
// alarmd's own timeout: however slow, it is not where the backend cuts, and
// it sets no ceiling and holds nothing back.
func TestADeliveryTimeoutIsNoBackendCut(t *testing.T) {
	pool := newCeilingPool(t, nil, 1)
	for _, run := range slowRuns {
		pool.round(timed(run, ""))
	}
	pool.round(timed(30_000, contract.ReasonQueryDeliveryTimeout))
	if got := pool.runner.cooldownMemory.ceiling; got != 0 {
		t.Fatalf("ceiling %d after a 30 s delivery timeout, want none", got)
	}
}
