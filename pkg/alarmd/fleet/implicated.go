// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"strings"
	"time"
)

// PlanSeriesZeroWindow is how long a Plan's zero series count is read as
// current: seen within this of the row's latest round. The count is written
// from the Plan's evaluation lines, and a Plan bound to nothing does not
// produce one every round, so on a running cluster the entry trailed the
// round by three to four minutes -- a lag, not staleness. Ten minutes covers
// that with room; a zero older than this is a Plan whose lines stopped, and
// says nothing about what it matches now.
//
// The same number as RecentSkipWindow, under its own name because it answers
// a different question: that one bounds "a loss still in progress", this one
// bounds "a series count still describing the Plan". Retuning the loss bound
// must not move attribution with it.
const PlanSeriesZeroWindow = 10 * time.Minute

// planEvidence is what a row says about one of its Plans, read from the
// row's per-Plan facts alone: whether the latest round bound the Plan to no
// series, and the gap guards held on it.
type planEvidence struct {
	Unbound bool
	Guards  []GapGuard
}

// evidenceByPlan reads the row's per-Plan facts -- the series counts and the
// guards -- into one entry per Plan the row lists that either fact names.
//
// A zero series count counts only while it is recent: within
// PlanSeriesZeroWindow of the row's latest round. A zero from a week ago is
// a Plan whose lines stopped altogether, which says nothing about what it
// matches now; naming it the data's on that would send a person after a
// fact this side no longer holds.
//
// A row is one object, and an object may run several Plans; the guards and
// the series counts on the row are per Plan. Folding such a row onto every
// strategy the object runs reads one Plan's evidence as every strategy's --
// on a verification cluster five strategies with hundreds of matched series
// each were told "data absent" because a sixth Plan on the same object had
// bound none. Each Plan is read by its own evidence and by nothing else:
// a zero series count is that Plan's own statement that it matched nothing,
// a held guard is that Plan's own statement that its verdict is being held,
// and the two are never combined into a name for a third Plan. A Plan the
// row lists but neither fact names has no evidence on this row.
func evidenceByPlan(row Anomaly) map[StrategyRef]*planEvidence {
	listed := map[StrategyRef]bool{}
	for _, ref := range row.Strategies {
		listed[ref] = true
	}
	evidence := map[StrategyRef]*planEvidence{}
	entry := func(ref StrategyRef) *planEvidence {
		if !listed[ref] {
			return nil
		}
		if evidence[ref] == nil {
			evidence[ref] = &planEvidence{}
		}
		return evidence[ref]
	}
	for _, plan := range row.PlanSeries {
		if plan.Matched != 0 || !recentZero(row, plan) {
			continue
		}
		if e := entry(plan.Plan); e != nil {
			e.Unbound = true
		}
	}
	for _, guard := range row.Guards {
		if e := entry(guard.Plan); e != nil {
			e.Guards = append(e.Guards, guard)
		}
	}
	return evidence
}

// recentZero says whether a Plan's zero series count is recent enough to
// read: seen within PlanSeriesZeroWindow of the row's latest round. Without
// both times there is no age to measure, and the count is read as-is: the
// row is still named, not silently spared, because the only reason to spare
// it is a measured age this side does not have.
func recentZero(row Anomaly, plan PlanSeriesMatched) bool {
	if row.ReasonLastAt.IsZero() || plan.LastSeenAt.IsZero() {
		return true
	}
	return row.ReasonLastAt.Sub(plan.LastSeenAt) <= PlanSeriesZeroWindow
}

// implicatedStrategies is the Plans a row's evidence names, smallest id
// first, for the rows whose check reads per-Plan evidence and whose object
// runs more than one Plan. Nil for every other row, and for a row whose
// evidence names none: such a row stays every strategy's, as before.
//
// The check is a parameter and not read from the row because the caller
// that files a row under a check computes its group before it writes the
// check onto the row (attribution.go); reading the row's own check there
// read the previous round's, and folded the first round under the smallest
// id. The standing passes the row's check; the group passes its own.
func implicatedStrategies(row Anomaly, check Check) []StrategyRef {
	if !planScopedCheck(check) || len(row.Strategies) <= 1 {
		return nil
	}
	evidence := evidenceByPlan(row)
	if len(evidence) == 0 {
		return nil
	}
	refs := make([]StrategyRef, 0, len(evidence))
	for ref := range evidence {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].StrategyID != refs[j].StrategyID {
			return refs[i].StrategyID < refs[j].StrategyID
		}
		return refs[i].BusinessID < refs[j].BusinessID
	})
	return refs
}

// planScopedCheck says whether a check's evidence is per Plan: the two that
// standingOf reads the guards and the series counts for. A check about the
// object's whole round (a dependency down, a refused query, a defect) is
// every strategy's whatever the guards say, because every Plan on the
// object lost that round.
func planScopedCheck(check Check) bool {
	return check == CheckWindowUndecided || check == CheckSeriesDataMissing
}

// scopedToPlan is the row read as one of its Plans would read it: only that
// Plan's guards and series count, and none of the object-wide window facts,
// which belong to series the row does not attribute to Plans. standingOf
// over this row is the Plan's own standing.
func scopedToPlan(row Anomaly, ref StrategyRef) Anomaly {
	scoped := row
	scoped.Strategies = []StrategyRef{ref}
	scoped.Guards = nil
	for _, guard := range row.Guards {
		if guard.Plan == ref {
			scoped.Guards = append(scoped.Guards, guard)
		}
	}
	scoped.PlanSeries = nil
	for _, plan := range row.PlanSeries {
		if plan.Plan == ref {
			scoped.PlanSeries = append(scoped.PlanSeries, plan)
		}
	}
	scoped.Coverage = nil
	scoped.WindowFill = nil
	return scoped
}

// standingForStrategy is the words a listed row gives one of its strategies.
// A row whose check is not per Plan, or whose object runs one Plan, gives
// every strategy the row's own standing. Otherwise the strategy gets a
// standing only when the row carries evidence about its Plan, read from that
// evidence alone: a Plan bound to no series for longer than the stall bound
// is the data's; a Plan whose guard is held is read by that guard.
func standingForStrategy(row Anomaly, ref StrategyRef) (Standing, bool) {
	implicated := implicatedStrategies(row, row.Finding.Check)
	if implicated == nil {
		if row.Standing == nil {
			return Standing{}, false
		}
		return *row.Standing, true
	}
	evidence := evidenceByPlan(row)[ref]
	if evidence == nil {
		return Standing{}, false
	}
	standing := standingOf(scopedToPlan(row, ref))
	// A Plan that has matched nothing for longer than the stall bound has
	// its answer in that fact alone; the guard-driven rules would otherwise
	// leave it under the check's own pair when no guard is held on it.
	if evidence.Unbound && standing.Action != ActionDataCheck && row.Consecutive > StalledRounds {
		standing.State, standing.Action, standing.Watch, standing.RefinedBy = StateDataAbsent, ActionDataCheck, "", RulePlanEvidence
	}
	standing.About = implicated
	return standing, true
}

// strategiesOf is the strategies a listed row folds onto: the Plans its
// evidence names when the check reads per-Plan evidence and the object runs
// several; every strategy the row lists otherwise.
func strategiesOf(row Anomaly) []StrategyRef {
	if implicated := implicatedStrategies(row, row.Finding.Check); implicated != nil {
		return implicated
	}
	return row.Strategies
}

// strategyGroupKey is the key a row under a check grouped by strategy falls
// in: the Plans its evidence names, joined smallest first, when it names
// any -- so two Plans empty on one object fold together under both names
// and the group's count is true of every member -- and the smallest listed
// id otherwise. The joined key is what the group parameter matches, so a
// composite group opens like any other.
func strategyGroupKey(row Anomaly, check Check) string {
	implicated := implicatedStrategies(row, check)
	if len(implicated) == 0 {
		return ""
	}
	ids := make([]string, 0, len(implicated))
	for _, ref := range implicated {
		ids = append(ids, ref.StrategyID)
	}
	return strings.Join(ids, "+")
}
