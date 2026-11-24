// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// slotEvaluationSummary is what is read of a Slot's results once evaluation
// is over by everything that reads the Slot as a whole: the completion and
// its coverage, the query line's outcome mix, the no-data end index's present
// series and updates, and the completion row's count of events kept as
// identities.
//
// Each of those used to walk stream.evaluated, every State result and Level
// outcome of every series, which is why the Slot held them all until it
// completed. The summary is kept beside the results, batch by batch, at the
// one place a batch is merged (streamedExecution.mergeProvisional), so a Slot
// that lets its series' results go after applying them still has what these
// readers need. Writing a Plan's State and events is not one of them: that
// reads the results themselves, per Plan.
type slotEvaluationSummary struct {
	// completion is what the completion derivation reads.
	completion execution.CompletionAttributionFold
	// plans is each Plan's part, in the order the Plans first arrived, which
	// is the order of evaluated.Plans.
	plans []planEvaluationSummary
	// levelOutcomes counts the Slot's Level outcomes by kind, across Plans.
	levelOutcomes map[execution.LevelOutcomeKind]int
	// withoutMessage counts the identities the Slot's series kept in place
	// of events their protocol has no message for.
	withoutMessage uint64
}

type planEvaluationSummary struct {
	plan execution.PlanIdentity
	// coverage is the Plan's window coverage across its series, folded the
	// way the merged result folds it: the first batch's as it is, every
	// later one merged in.
	coverage execution.HistoryCoverage
	// noDataEnds is every update the Plan's series made to its no-data end
	// index.
	noDataEnds []execution.NoDataEndUpdate
	// present is the Plan's series this round decided a Level of, kept only
	// for a Plan whose no-data end index the Slot read: no other Plan's is
	// asked, and keeping one per series for every Plan would cost a large
	// Slot a set of digests nobody reads.
	present map[execution.SeriesIdentityDigest]struct{}
}

// summarizeEvaluation is the summary of a result in hand, each Plan result in
// it as it stands, for a caller that has the whole result rather than its
// batches.
func summarizeEvaluation(result execution.EvaluationResult) slotEvaluationSummary {
	summary := slotEvaluationSummary{completion: execution.FoldCompletionAttribution(result)}
	for _, plan := range result.Plans {
		summary.addPlan(plan, false, nil)
	}
	return summary
}

// add folds one merged batch in, by the rule appendProvisional merges it
// into the results. indexed is the no-data end index the Slot read, by Plan.
func (summary *slotEvaluationSummary) add(next execution.EvaluationResult, indexed map[execution.PlanIdentity]execution.NoDataEndLoaded) {
	summary.completion.Add(next)
	for _, plan := range next.Plans {
		summary.addPlan(plan, true, indexed)
	}
}

func (summary *slotEvaluationSummary) addPlan(next execution.PlanEvaluationResult, merge bool,
	indexed map[execution.PlanIdentity]execution.NoDataEndLoaded) {
	index := -1
	if merge {
		for current := range summary.plans {
			if summary.plans[current].plan == next.Plan {
				index = current
				break
			}
		}
	}
	if index < 0 {
		// Copied rather than shared: Merge rewrites the window and minute
		// lists in place, and the results hold the same lists.
		summary.plans = append(summary.plans, planEvaluationSummary{plan: next.Plan, coverage: copyHistoryCoverage(next.HistoryCoverage)})
		index = len(summary.plans) - 1
	} else {
		summary.plans[index].coverage.Merge(next.HistoryCoverage)
	}
	plan := &summary.plans[index]
	plan.noDataEnds = append(plan.noDataEnds, next.NoDataEnds...)
	_, read := indexed[next.Plan]
	for _, outcome := range next.LevelOutcomes {
		if summary.levelOutcomes == nil {
			summary.levelOutcomes = make(map[execution.LevelOutcomeKind]int)
		}
		summary.levelOutcomes[outcome.Outcome]++
		if read {
			if plan.present == nil {
				plan.present = make(map[execution.SeriesIdentityDigest]struct{})
			}
			plan.present[outcome.SeriesIdentityDigest] = struct{}{}
		}
	}
	for _, state := range next.StateResults {
		summary.withoutMessage += uint64(len(state.WithoutMessage))
	}
}

// summaryPresentEntryBytes is one series digest in a Plan's present set: the
// key's string header and the empty value as a map keeps them, filled to
// seven eighths. The digest's own bytes are counted beside it - the set keeps
// them alive once the Level outcomes that carried them are let go.
var summaryPresentEntryBytes = (uint64(unsafe.Sizeof(execution.SeriesIdentityDigest(""))) + 1) * 8 / 7

// addedBytes is what folding next in would add to what the summary holds of
// the Slot's series rather than of its Plans: a present digest for each
// series new to a Plan whose no-data end index the Slot read, and the no-data
// end updates. They grow with the series and are held across every window to
// the Slot's end, so a share that did not see them would let a Slot cut into
// windows hold them uncounted. It changes nothing; add does.
func (summary *slotEvaluationSummary) addedBytes(next execution.EvaluationResult,
	indexed map[execution.PlanIdentity]execution.NoDataEndLoaded) uint64 {
	var total uint64
	for _, plan := range next.Plans {
		if len(plan.NoDataEnds) != 0 {
			total += retainedObjectBytes(plan.NoDataEnds)
		}
		if _, read := indexed[plan.Plan]; !read || len(plan.LevelOutcomes) == 0 {
			continue
		}
		var present map[execution.SeriesIdentityDigest]struct{}
		if part, found := summary.planPart(plan.Plan); found {
			present = part.present
		}
		counted := make(map[execution.SeriesIdentityDigest]struct{}, 1)
		for _, outcome := range plan.LevelOutcomes {
			digest := outcome.SeriesIdentityDigest
			if _, held := present[digest]; held {
				continue
			}
			if _, already := counted[digest]; already {
				continue
			}
			counted[digest] = struct{}{}
			total += summaryPresentEntryBytes + uint64(len(digest))
		}
	}
	return total
}

func copyHistoryCoverage(coverage execution.HistoryCoverage) execution.HistoryCoverage {
	coverage.Windows = append([]execution.WindowCoverage(nil), coverage.Windows...)
	coverage.MissingMinutes = append([]int64(nil), coverage.MissingMinutes...)
	return coverage
}

// planCount is how many Plans the Slot has a result for.
func (summary *slotEvaluationSummary) planCount() int {
	return len(summary.plans)
}

// coverage is the Slot's window coverage, every Plan's merged in.
func (summary *slotEvaluationSummary) coverage() execution.HistoryCoverage {
	var coverage execution.HistoryCoverage
	for _, plan := range summary.plans {
		coverage.Merge(plan.coverage)
	}
	return coverage
}

// planPart is the Plan's part of the summary, the first one for a result
// that names the Plan twice.
func (summary *slotEvaluationSummary) planPart(identity execution.PlanIdentity) (*planEvaluationSummary, bool) {
	for index := range summary.plans {
		if summary.plans[index].plan == identity {
			return &summary.plans[index], true
		}
	}
	return nil, false
}
