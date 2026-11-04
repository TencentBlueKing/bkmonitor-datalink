// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package editclose

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

// The group outcomes beside Decide's: a rule a group cannot decide for all
// its alerts at once. Such a group is not closed from its set; its alerts
// are left to the record-by-record path, which reads each one.
const (
	// OutcomeGroupDimensionsUndecided is a group whose grouping no digest
	// states on one side - a dynamic grouping, which only each alert's own
	// dimension names can be judged by.
	OutcomeGroupDimensionsUndecided = "group_dimensions_undecided"
	// OutcomeGroupLevelUndecided is a threshold group whose version
	// detected a level the current Plans do not, or the current Plans do
	// not detect every level the strategy declares: which of its alerts
	// stand at that level only each alert says.
	OutcomeGroupLevelUndecided = "group_level_undecided"
)

// GroupDecisions is every word DecideGroup returns that Decisions does not.
var GroupDecisions = []string{OutcomeGroupDimensionsUndecided, OutcomeGroupLevelUndecided, OutcomeRevisionIncomparable}

// Group is what one of the link's groups states about every alert in it:
// the version they were raised under, their evaluation family, the facts
// their opening events carried (the link groups them by those facts'
// digest), and the levels that version detected.
type Group struct {
	Revision    int64
	Family      string
	Facts       contract.StrategyFactsV2
	Levels      []string
	LevelsWhole bool
}

// DecideGroup is Decide for every alert of a group at once: the verdict
// any alert of it gets from Decide wherever the group alone decides it,
// and a group outcome where only each alert's record could
// (OutcomeGroupDimensionsUndecided, OutcomeGroupLevelUndecided).
//
// The version is ordered as Decide orders it: a group at the current
// version is never a candidate, and neither is one past it - the
// publication has not caught up with the source (revision_ahead). A group's
// version is one this process's own publications named, so it is an
// execution revision (the publisher's microsecond-time versions predate the
// labels a group is keyed by, and no group holds an alert raised under
// one). A group whose version is of the other scheme all the same is left
// to each alert's record (OutcomeRevisionIncomparable). Every older group
// is judged by its facts, which a version alone does not settle - the same
// version under another build of this process can state other facts.
func DecideGroup(plan Plan, group Group) (decision, note string) {
	switch {
	case group.Revision <= 0:
		return OutcomeRevisionUnknown, ""
	case !RevisionsComparable(group.Revision, plan.Revision):
		return OutcomeRevisionIncomparable, ""
	case group.Revision == plan.Revision:
		return OutcomeCurrentRevision, ""
	case group.Revision > plan.Revision:
		return OutcomeRevisionAhead, ""
	}
	var noData bool
	switch group.Family {
	case familyNoData:
		noData = true
	case familyMetricAlgorithm:
	default:
		return OutcomeFamilyUnknown, ""
	}
	if plan.Facts == nil || plan.Facts.Schema != contract.StrategyFactsSchemaV2 {
		return OutcomeKept, OutcomePlanFactsMissing
	}
	facts := group.Facts
	if facts.Schema != contract.StrategyFactsSchemaV2 || facts.MetricDigest == "" || facts.ConditionMD5 == "" ||
		!knownConditionBasis(facts.ConditionBasis) {
		return OutcomeKept, OutcomeFactsUnknownSchema
	}
	if facts.MetricDigest != plan.Facts.MetricDigest {
		return OutcomeMetricChanged, ""
	}
	current := planGrouping(plan.Facts, noData)
	if noData {
		if plan.NoData {
			if facts.DimensionDigest == "" || current == "" {
				return OutcomeGroupDimensionsUndecided, ""
			}
			if facts.DimensionDigest != current {
				return OutcomeDimensionsChanged, ""
			}
		}
		note := ""
		if decision, held := conditionVerdict(&facts, plan.Facts, conditionRuleReleased); decision != "" {
			return decision, ""
		} else if held != "" {
			note = held
		}
		if !plan.NoData {
			if plan.NoDataSuspended {
				return OutcomeNoDataSuspended, note
			}
			return OutcomeNoDataDisabled, note
		}
		return OutcomeKept, note
	}
	if facts.DimensionDigest == "" || current == "" {
		return OutcomeGroupDimensionsUndecided, ""
	}
	if facts.DimensionDigest != current {
		return OutcomeDimensionsChanged, ""
	}
	return groupLevelVerdict(plan, group), ""
}

// groupLevelVerdict keeps a threshold group whose version's every level
// the current Plans still detect: no alert of it can stand at a level that
// was removed. Anything else is left to each alert's level.
func groupLevelVerdict(plan Plan, group Group) string {
	if !plan.LevelsWhole || !group.LevelsWhole || len(group.Levels) == 0 {
		return OutcomeGroupLevelUndecided
	}
	current := make(map[string]bool, len(plan.Levels))
	for _, level := range plan.Levels {
		current[linkdoutput.SeverityFor(contract.LevelResultV1{LevelID: level.LevelID, LevelCode: level.LevelCode})] = true
	}
	for _, level := range group.Levels {
		if !current[level] {
			return OutcomeGroupLevelUndecided
		}
	}
	return OutcomeKept
}
