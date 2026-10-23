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
