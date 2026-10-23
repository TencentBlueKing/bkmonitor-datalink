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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// queryCooldownRunTimesKept is how many answered run times a Query Group's
// spread is read from, how many deciding probes it keeps for a reader, and
// how many answered probes in a row near its ceiling let it out. It is a
// sample size, not a budget.
//
// Eight answers with no cut between them is evidence that the backend cuts
// few of this query's rounds: at a cut rate of a quarter such a run comes up
// one time in ten (0.75^8), at a third one time in twenty-five. What that
// release lets back in is bounded. Re-entry takes three rounds cut in a row,
// and the release eight answers in a row at a probe every one to two
// minutes for a minute Query Group (queryCooldownDelay). Over every cut rate
// the expected cycle through this release is shortest at neither end - a
// backend that rarely cuts seldom puts the Query Group in, one that often
// cuts seldom answers eight times running - and is at its shortest near a
// cut rate of 0.28: about 0.46 re-entries an hour, against the two to six an
// hour per replica that an answer within the budget let through. Only a
// backend that answers exactly eight times and then cuts exactly three could
// do more, about 5.5 an hour, and no random cut rate does.
//
// The same bound covers a ceiling set by something that was not a cut - an
// overloaded backend answering one unavailable slower than a short query's
// runs: it holds the Query Group near that ceiling for at most eight
// answers, and an answer past it drops it.
const queryCooldownRunTimesKept = 8

// A backend that cuts a query at its own limit answers unavailable after
// that limit, and a query that usually comes back just under it is let out
// of the pool by a probe that came back at 29.9 s against a cut at 30.0 s,
// to be cut on one of its next rounds and come straight back. The answer was
// real; what it did not show is that the next round will not be cut. So a
// Query Group whose failures have shown such a cut leaves on an answer that
// clears the cut by the spread of its own run times, and the probe says
// which comparison held (observability.QueryCooldownExitRules).
//
// That margin is evidence about the next round, not the next hour. Three
// production Query Groups whose queries run 17 to 30 s against a 30 s cut
// left on answers that cleared it by their spread - 22.7 s with 7.0 s, 16.8 s
// with 11.9 s - answered some sixty rounds, met three cuts in a row and went
// back in, about once an hour each. So a Query Group that went back in after
// an answer let it out (reenteredAfterAnAnswer) has shown that an answer
// clear of its cut is not evidence for it, and leaves only on evidence: an
// answer past the ceiling, the ceiling gone, or queryCooldownRunTimesKept
// answers in a row. Its window is QueryCooldownReentryWindow, the one its
// Reentries already counts against, not a new one.
//
// What that bounds, from a model of the pool as it runs: a minute Query
// Group whose runs take X, the backend cutting every run with X at or past
// 30 s; three cuts in a row put it in, probes one to two minutes apart and
// the cooldown doubling to five minutes; 133 shapes of X (uniform, normal,
// and half quick and half slow) at 600 hours each. Worst re-entries an hour:
//
//	exit rule                                        worst   at
//	answer clear of the cut by the last 8 runs'      1.52    X in [29.8, 30.2) s
//	  spread (before)
//	the same, spread over the whole last time out    1.21    half ~1 s, half 25-40 s
//	a re-entry leaves only on evidence (this)        0.29    X ~ N(28 s, 3 s)
//
// The spread rules fail where X sits close around the cut - every answer
// clears it by less than any spread, and the next hour still holds three cuts
// in a row - and where the cut rate is high, when the time out is about
// fourteen rounds and its spread hardly wider than eight runs'. The bound the
// answers in a row give is worked out at queryCooldownRunTimesKept. A first
// entry still leaves on an answer clear of the cut by its spread.

// noteRunFacts keeps what a normal execution showed about how long the Query
// Group's query takes and where its backend cuts it, and reports a failure
// that was such a cut. Facts about another query revision are not this
// query's, and are dropped first.
func (runner *Runner) noteRunFacts(slot FrozenSlot, operation execution.Operation, result execution.SlotExecutionResult) (cut bool) {
	if operation != execution.OperationNormal || result.PrimaryQueryClock == nil {
		return false
	}
	memory := &runner.cooldownMemory
	if revision := slot.Contract.QueryRevision; revision != memory.runRevision {
		memory.runRevision, memory.runTimes, memory.ceiling = revision, nil, 0
	}
	elapsed := result.PrimaryQueryClock.ElapsedMillis
	switch result.QueryAvailability {
	case execution.QueryAvailabilityAvailable:
		if memory.ceiling > 0 && elapsed > memory.ceiling {
			// Answered past the cut it was held to: the cut has moved.
			memory.ceiling = 0
		}
		memory.runTimes = append(memory.runTimes, elapsed)
		if len(memory.runTimes) > queryCooldownRunTimesKept {
			memory.runTimes = append([]int64(nil), memory.runTimes[len(memory.runTimes)-queryCooldownRunTimesKept:]...)
		}
	case execution.QueryAvailabilityUnavailable:
		// The backend's own refusal, not alarmd's deadline, and slower than
		// any answer: where the backend stopped waiting. A failure that came
		// back quicker than an answer is an error, not a cut.
		// A refusal counts as the cut the same way: a storage that gives up on
		// a query too large for it answers with a client error.
		if (result.QueryUnavailableReason != execution.ReasonCode(contract.ReasonQueryUnavailable) &&
			result.QueryUnavailableReason != execution.ReasonCode(contract.ReasonQueryRejected)) ||
			len(memory.runTimes) == 0 || elapsed <= longestRun(memory.runTimes) {
			return false
		}
		if memory.ceiling == 0 || elapsed < memory.ceiling {
			memory.ceiling = elapsed
		}
		return true
	}
	return false
}

// queryCooldownLetsOut decides an answered deciding probe against the
// Query Group's ceiling, records the comparison on the probe, and reports
// whether the Query Group leaves. One that stays is probed again after the
// cooldown its failures give it, without counting this answer as a failure.
func (runner *Runner) queryCooldownLetsOut(ctx context.Context, probe *observability.QueryCooldownProbe, intervalSeconds int64) bool {
	memory := &runner.cooldownMemory
	spread := runSpread(memory.runTimes)
	measured := probe != nil && probe.Measured
	reentered := runner.reenteredAfterAnAnswer()
	rule := observability.QueryCooldownRuleNearCeiling
	switch {
	case memory.ceiling == 0:
		rule = observability.QueryCooldownRuleNoCeiling
	case measured && probe.ElapsedMillis > memory.ceiling:
		rule = observability.QueryCooldownRuleAboveCeiling
	case !measured && !reentered:
		rule = observability.QueryCooldownRuleUnmeasured
	case measured && !reentered && probe.ElapsedMillis+spread < memory.ceiling:
		rule = observability.QueryCooldownRuleUnderCeiling
	case memory.answeredInRow+1 >= queryCooldownRunTimesKept:
		rule = observability.QueryCooldownRuleAnsweredInRow
	case reentered && (!measured || probe.ElapsedMillis+spread < memory.ceiling):
		rule = observability.QueryCooldownRuleHeldAfterReentry
	}
	if probe != nil {
		probe.Rule, probe.CeilingMillis, probe.RunSpreadMillis = rule, memory.ceiling, spread
		runner.noteProbe(*probe)
	}
	held := rule == observability.QueryCooldownRuleNearCeiling || rule == observability.QueryCooldownRuleHeldAfterReentry
	// A missing or extreme interval is no basis for a cooldown to hold it
	// in; it leaves as an answer always let it.
	if !held || intervalSeconds <= 0 ||
		intervalSeconds > int64((24*time.Hour)/time.Second) {
		memory.answeredInRow = 0
		return true
	}
	memory.answeredInRow++
	state := &runner.queryCooldown
	state.until = runner.now().Add(queryCooldownDelay(runner.queryGroup, time.Duration(intervalSeconds)*time.Second, state.failures))
	runner.emitQueryCooldown(ctx, QueryCooldownExtended)
	runner.saveQueryCooldown(ctx)
	return false
}

// reenteredAfterAnAnswer reports whether the Query Group is in the pool
// again, on failures that showed its backend's cut, within
// QueryCooldownReentryWindow of an answer letting it out. A re-entry on
// failures that were no cut - the query service down for every Query Group,
// failing fast - says nothing about how close its answers run to the cut,
// and is decided as a first entry is. The entry, its cuts and the last exit
// are all on its record, so a restart or the next owner decides the same way.
func (runner *Runner) reenteredAfterAnAnswer() bool {
	memory := &runner.cooldownMemory
	return runner.queryCooldown.cuts > 0 && memory.exitReason == QueryCooldownRecovered &&
		memory.enteredAt.Sub(memory.exitedAt) < QueryCooldownReentryWindow
}

// noteProbe keeps a deciding probe among the latest ones, for a reader.
func (runner *Runner) noteProbe(probe observability.QueryCooldownProbe) {
	memory := &runner.cooldownMemory
	memory.probes = append(memory.probes, probe)
	if len(memory.probes) > queryCooldownRunTimesKept {
		memory.probes = append([]observability.QueryCooldownProbe(nil), memory.probes[len(memory.probes)-queryCooldownRunTimesKept:]...)
	}
}

func longestRun(runTimes []int64) int64 {
	var longest int64
	for _, run := range runTimes {
		longest = max(longest, run)
	}
	return longest
}

// runSpread is how far apart the Query Group's recent run times are: the
// margin under its ceiling a probe needs before its next normal round can be
// expected to come back too. Zero with fewer than two.
func runSpread(runTimes []int64) int64 {
	if len(runTimes) < 2 {
		return 0
	}
	shortest := runTimes[0]
	for _, run := range runTimes {
		shortest = min(shortest, run)
	}
	return longestRun(runTimes) - shortest
}
