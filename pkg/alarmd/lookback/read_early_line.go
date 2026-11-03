// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every round found read early - a sample or a probation reread classed
// window_read_early - is one evaluated on data not yet whole, and is written
// as a line of its own, not sampled (OnWindowReadEarly): the group, the
// Slot, the hold it was read at and, after a lowering, the hold it came
// from, how its data changed, and what the round decided. A read-early round
// is rare - a few a day in a deployment of thousands of groups - and each is
// the one place a missed or false alert would be traced from.
//
// What the round decided comes from the worker that ran it, in this process
// (NoteSlotDecision): the lookback keeps it only for a group in probation -
// the rounds a lowering may have read early - by Slot, until the round's
// reread ends. A round decided before the group came to this process, or
// whose decision was not kept, says so (DecisionMissing) instead.

// Why a read-early round's line has no decision, closed: not_in_probation,
// a round of a group not in probation, whose decisions are not kept;
// not_recorded, a group in probation whose round's decision this process
// did not keep - decided before the group came here, or past the bound.
const (
	DecisionNotInProbation = "not_in_probation"
	DecisionNotRecorded    = "not_recorded"
)

// maxKeptDecisions is how many Slots' decisions a group in probation keeps:
// the rounds whose rereads are in flight at once - a hold spans a few Slots,
// a reread's rungs a few more - with room; past it the oldest goes.
const maxKeptDecisions = 16

// SlotDecision is what a round decided, as the worker that ran it reported:
// its events and state mutations, its Levels' outcomes counted by kind
// (NORMAL, ABNORMAL, RECOVERY, UNKNOWN, TERMINAL), and the strategies whose
// Plans it evaluated - the first maxDecisionStrategies of them, and how
// many in all.
type SlotDecision struct {
	Events         uint64
	StateMutations uint64
	Levels         map[string]uint64
	Strategies     []string
	StrategyCount  int
	// Completed says the round's completion was reported, not only some of
	// its Plans' evaluations.
	Completed bool
	// strategies is every strategy counted, so a Plan reported series by
	// series is one strategy.
	strategies map[string]bool
}

// maxDecisionStrategies is how many strategies a decision names.
const maxDecisionStrategies = 4

// SlotDecisionReport is one part of a round's decision as the worker reports
// it: the round's completion (Completed, with its events and state
// mutations), or one Plan's evaluation (Strategy, with its Levels' outcomes).
type SlotDecisionReport struct {
	QueryGroup     execution.QueryGroupIdentity
	Slot           execution.EvaluationTime
	Completed      bool
	Events         uint64
	StateMutations uint64
	Strategy       string
	Levels         map[string]uint64
}

// WindowReadEarly is a round found read early: Path, a sample's or a
// probation reread's; the hold it was read at; its data's
// changes by class (Changes) at the rung it settled at; and its Decision, or
// why there is none (DecisionMissing).
type WindowReadEarly struct {
	Contract        execution.FrozenExecutionContractRef
	Source          string
	Path            string
	Hold            time.Duration
	Rung            string
	CompletionAge   time.Duration
	Changes         map[string]int
	Decision        *SlotDecision
	DecisionMissing string
}

// The paths a round is found read early by.
const (
	ReadEarlyPathSample    = "sample"
	ReadEarlyPathProbation = "probation"
)

// NoteSlotDecision keeps part of what a round decided, for a group in
// probation, until the round's reread ends; any other group's is dropped.
func (engine *Engine) NoteSlotDecision(report SlotDecisionReport) {
	if !engine.KeepsDecisions(report.QueryGroup) {
		return
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	state := engine.groups[report.QueryGroup]
	if state == nil || state.probation == nil {
		return
	}
	if state.decisions == nil {
		state.decisions = map[execution.EvaluationTime]*SlotDecision{}
	}
	decision := state.decisions[report.Slot]
	if decision == nil {
		if len(state.decisions) >= maxKeptDecisions {
			oldest := execution.EvaluationTime(0)
			for slot := range state.decisions {
				if oldest == 0 || slot < oldest {
					oldest = slot
				}
			}
			delete(state.decisions, oldest)
		}
		decision = &SlotDecision{Levels: map[string]uint64{}}
		state.decisions[report.Slot] = decision
	}
	if report.Completed {
		decision.Completed, decision.Events, decision.StateMutations = true, report.Events, report.StateMutations
	}
	for kind, count := range report.Levels {
		decision.Levels[kind] += count
	}
	if report.Strategy != "" && !decision.strategies[report.Strategy] {
		if decision.strategies == nil {
			decision.strategies = map[string]bool{}
		}
		decision.strategies[report.Strategy] = true
		decision.StrategyCount++
		if len(decision.Strategies) < maxDecisionStrategies {
			decision.Strategies = append(decision.Strategies, report.Strategy)
			sort.Strings(decision.Strategies)
		}
	}
}

// noteReadEarlyLineLocked keeps the line of a round found read early, to be
// written outside the lock (publishReadHold), with the round's decision if
// its group kept one. The decision goes when the round's reread ends
// (finishProbationLocked), or past the bound.
func (engine *Engine) noteReadEarlyLineLocked(state *group, candidate *sample, path string) {
	line := WindowReadEarly{Contract: candidate.contract, Source: candidate.source, Path: path,
		Hold:          time.Duration(candidate.contract.ReadHoldMillis) * time.Millisecond,
		CompletionAge: candidate.completion, Changes: map[string]int{}}
	if candidate.lastChange >= 0 {
		line.Rung = RungNames[candidate.lastChange]
	}
	for class, count := range candidate.changes {
		line.Changes[class] = count
	}
	switch decision := state.decisions[candidate.evaluation]; {
	case decision != nil:
		kept := *decision
		kept.Levels = map[string]uint64{}
		for kind, count := range decision.Levels {
			kept.Levels[kind] = count
		}
		kept.Strategies, kept.strategies = append([]string(nil), decision.Strategies...), nil
		line.Decision = &kept
	case state.probation == nil && path == ReadEarlyPathSample:
		line.DecisionMissing = DecisionNotInProbation
	default:
		line.DecisionMissing = DecisionNotRecorded
	}
	engine.readEarlyLines = append(engine.readEarlyLines, line)
}
