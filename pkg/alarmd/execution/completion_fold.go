// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// CompletionAttributionFold is what the completion derivation reads of a
// Slot's evaluation, kept as the results arrive rather than as the results.
//
// The derivation used to walk every Level outcome of every series the Slot
// held, which is why the Slot had to hold them all until it completed. A
// Slot that applies its series window by window and lets each window go
// cannot do that: by the time it completes, most of its outcomes are gone.
// What the derivation actually reads of them is small, and this is it - per
// Plan, the disposition and reason the Slot's results fold to, whether any
// Level was decided TERMINAL, and the few UNKNOWN outcomes that can still be
// the one it names.
//
// A few is enough, and exactly so: the derivation replaces its finding only
// with one ranked strictly higher, so among outcomes it would rank alike the
// first is the only one that can ever be reported. How it ranks an UNKNOWN
// outcome depends on nothing but the outcome's Plan, whether it is a guard's
// tail, and the Slot's inputs (SlotInputWholeness.UnknownIsGuardTail). The
// inputs are the same for every outcome of the Slot, so keeping the first
// outcome of each (Plan, guard tail) pair, in the order they arrived, and
// replaying those in that order finds what replaying every outcome would.
// Whether the Plan's inputs were whole is asked only when the Slot completes,
// because that is when its inputs are all known.
type CompletionAttributionFold struct {
	plans []completionPlanFold
}

// completionPlanFold is one Plan's part of the fold. The field names are the
// Plan result's own, so the derivation reads a Plan the way it read the
// result.
type completionPlanFold struct {
	Plan        PlanIdentity
	Disposition PlanDisposition
	ReasonCode  ReasonCode
	// terminal is a Level of the Plan decided TERMINAL.
	terminal bool
	// unknown is the first UNKNOWN outcome of each (outcome Plan, guard
	// tail) pair, in arrival order: two in practice, one per guard tail
	// value, since a Plan's outcomes name the Plan.
	unknown []LevelOutcome
}

// Add folds one batch of a Slot's results in. A Plan already in the fold
// takes the batch's disposition and reason only when the batch's outranks
// it, which is the rule the worker merges a Slot's results by, so the fold
// of a Slot's batches is the fold of the merged result.
func (fold *CompletionAttributionFold) Add(result EvaluationResult) {
	fold.add(result, true)
}

// FoldCompletionAttribution is the fold of one result taken whole, each Plan
// result in it as it stands. A result that names a Plan twice is folded as
// two Plans, which is how the derivation has always read such a result; a
// Slot's merged result never names one twice.
func FoldCompletionAttribution(result EvaluationResult) CompletionAttributionFold {
	var fold CompletionAttributionFold
	fold.add(result, false)
	return fold
}

func (fold *CompletionAttributionFold) add(result EvaluationResult, merge bool) {
	for _, next := range result.Plans {
		index := -1
		if merge {
			for current := range fold.plans {
				if fold.plans[current].Plan == next.Plan {
					index = current
					break
				}
			}
		}
		if index < 0 {
			fold.plans = append(fold.plans, completionPlanFold{Plan: next.Plan, Disposition: next.Disposition, ReasonCode: next.ReasonCode})
			index = len(fold.plans) - 1
		} else if PlanDispositionOutranks(next.Disposition, fold.plans[index].Disposition) {
			fold.plans[index].Disposition, fold.plans[index].ReasonCode = next.Disposition, next.ReasonCode
		}
		plan := &fold.plans[index]
		for _, outcome := range next.LevelOutcomes {
			switch outcome.Outcome {
			case LevelOutcomeTerminal:
				plan.terminal = true
			case LevelOutcomeUnknown:
				plan.keepUnknown(outcome)
			}
		}
	}
}

func (plan *completionPlanFold) keepUnknown(outcome LevelOutcome) {
	for _, kept := range plan.unknown {
		if kept.Plan == outcome.Plan && kept.GuardTail == outcome.GuardTail {
			return
		}
	}
	plan.unknown = append(plan.unknown, outcome)
}

// Plans is how many Plans the fold holds.
func (fold CompletionAttributionFold) Plans() int {
	return len(fold.plans)
}

// Dispositions is each Plan's folded disposition, in the order the Plans
// first arrived.
func (fold CompletionAttributionFold) Dispositions() []PlanDisposition {
	dispositions := make([]PlanDisposition, 0, len(fold.plans))
	for _, plan := range fold.plans {
		dispositions = append(dispositions, plan.Disposition)
	}
	return dispositions
}

// DeriveFoldedCompletionAttribution is DeriveCompletionAttribution over a
// fold of the Slot's results instead of the results.
func DeriveFoldedCompletionAttribution(input InternalExecution, fold CompletionAttributionFold) (CompletionKind, CompletionAttribution, error) {
	return deriveCompletionAttribution(input, fold)
}

// PlanDispositionOutranks says whether a Plan's result in one batch of a Slot
// replaces the disposition the Plan already has from another: the worse
// outcome wins, so a Plan that could not be decided in one series is not
// reported decided because another series of it was.
func PlanDispositionOutranks(next, current PlanDisposition) bool {
	return planDispositionRank(next) > planDispositionRank(current)
}

func planDispositionRank(disposition PlanDisposition) int {
	switch disposition {
	case PlanRetryPending:
		return 5
	case PlanTerminal:
		return 4
	case PlanUnavailable, PlanReadinessGap:
		return 3
	case PlanDecidedDegraded:
		return 2
	case PlanDecided:
		return 1
	default:
		return 0
	}
}
