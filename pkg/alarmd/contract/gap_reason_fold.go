// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

// GapReasonFoldVersion names this ordering, so a deployment that changes it
// changes something that has a name.
//
// It belongs on the cross-version invariant list for the ordinary reason: the
// fold decides what a persisted gap marker says, one build writes markers the
// next build reads, and two builds folding differently disagree about a marker
// neither of them is wrong about.
const GapReasonFoldVersion = "gap-reason-fold-v1"

// GapReasonFoldOrder is the order the fold prefers, strongest first.
//
// Three derivations used to pick one reason out of the same set of incomplete
// inputs, each by its own rule -- the first algorithm's, the first input's,
// the Level outcome's -- and a scope whose two inputs failed differently made
// them disagree. Nothing was wrong with any of the three answers; they were
// three answers to a question that has one, so the contract's two comparisons
// could not both be satisfied and the Plan failed to evaluate every round.
// One order, called from all three, is what makes the question have one
// answer.
//
// The order is "the more persistent, and the more it asks protection for,
// first". A backend that did not answer at all outranks one that answered
// late, which outranks a round this deployment cut short, which outranks a
// round whose configuration moved underneath it, which outranks a round that
// did return data and not all of it.
//
// Three of these were placed here rather than by the ruling, which named
// only the query reasons. READINESS_BUDGET_INVALID is first because it is the
// most persistent of all of them: a backend outage ends on its own and an
// invalid readiness budget does not end until somebody changes the
// configuration, so a scope that has one is a scope whose input cannot be
// produced rather than one that did not arrive. CONFIG_DRIFT and
// EXECUTION_BUDGET_EXHAUSTED both mean the round cannot be trusted, and sit
// under the two that mean no data arrived at all and above the one that means
// some did.
//
// The set is not written from that reasoning, though. It is checked against
// the reason catalogue: every reason a query result may carry has to be
// ranked, and READINESS_BUDGET_INVALID is in this list because that check
// found it, not because anybody remembered it.
var GapReasonFoldOrder = []string{
	ReasonReadinessBudgetInvalid,
	ReasonQueryUnavailable,
	ReasonQueryTimeout,
	ReasonExecutionBudgetExhausted,
	ReasonConfigDrift,
	ReasonQueryPartial,
	// Last: the query succeeded and returned nothing. It asks the least
	// protection of any of these -- the Level is starved, not misinformed --
	// so a scope that also has a PARTIAL input says PARTIAL.
	ReasonQueryEmpty,
}

// FoldGapReason reduces the reasons of one gap scope's incomplete inputs to
// the one its marker carries.
//
// Deterministic in the set, not in the order it is given: the same inputs fold
// to the same reason however they are listed, which is what lets a retry write
// the marker the first attempt would have written. The result is always one of
// the reasons given -- the fold chooses, it never invents.
//
// A reason this order does not list is preferred over every reason it does.
// The alternative buries it: an unlisted reason is one nobody has ranked, and
// ranking it last would let a failure mode introduced after this table was
// written disappear behind a milder one that happens to share the scope, on
// every round, with the table looking complete. Sorted among themselves so
// two unlisted reasons still fold deterministically.
func FoldGapReason(reasons []string) string {
	folded := ""
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if folded == "" || gapReasonOutranks(reason, folded) {
			folded = reason
		}
	}
	return folded
}

// gapReasonOutranks reports whether one reason is preferred over another.
func gapReasonOutranks(candidate, incumbent string) bool {
	candidateRank, candidateListed := gapReasonRank(candidate)
	incumbentRank, incumbentListed := gapReasonRank(incumbent)
	if candidateListed != incumbentListed {
		return !candidateListed
	}
	if candidateRank != incumbentRank {
		return candidateRank < incumbentRank
	}
	// Two unlisted reasons, or the same reason twice. Lexicographic so the
	// answer does not depend on which input was read first.
	return candidate < incumbent
}

func gapReasonRank(reason string) (int, bool) {
	for rank, listed := range GapReasonFoldOrder {
		if listed == reason {
			return rank, true
		}
	}
	return len(GapReasonFoldOrder), false
}
