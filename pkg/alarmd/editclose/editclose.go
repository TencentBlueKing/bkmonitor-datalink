// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package editclose decides which open alerts an edit of their strategy
// orphaned: alerts the strategy's current revision can no longer produce or
// recover, so nothing would ever end them. It follows the platform's own
// edit rules (bk-monitor alert manager, CloseStatusChecker
// .check_strategy_changed) as far as an alert's own record can decide them.
//
// The decision is level-triggered. An alert is a candidate when the
// revision it was raised under (its labels.strategy_version, which the link
// copies from the opening event and never changes) is older than the
// revision the strategy's current Plans raise alerts under. Nothing about
// the edit itself is observed or remembered: a process that starts after
// the edit finds the same candidates as one that watched it happen, and an
// alert at the current revision is never one.
//
// Each candidate is judged from its own record against the current Plans'
// facts, and closed only on a fact both sides state, in the platform's
// order:
//
//   - metric_changed: the strategy's metrics are not the ones the alert
//     was raised under. Read from the strategy facts the opening event
//     carried (contract.StrategyFactsV2) against the current Plans'.
//   - dimensions_changed: the grouping dimensions are not the ones the alert
//     was raised under - one added, removed or renamed. Where the alert's
//     strategy facts state its grouping (dimension_digest), that digest is
//     compared with the current Plans' (contract.GroupingDigest of the key
//     set the fingerprint is built from: the output identity fields, or the
//     no-data agg_dimension for a no-data alert). Where they do not - an
//     alert opened before events carried it, or a Plan grouping by every
//     dimension its series carries (dynamic dimensions) - only a dimension
//     removed or renamed is decided, from the alert's own dimension names:
//     the alert carries one the current grouping no longer names, minus the
//     dimensions this process adds beside the strategy's own (the no-data
//     tag and the host identity CMDB fills in). A dimension added is then
//     not decided: the record omits a grouping dimension the series did not
//     carry, so an alert lacking one of the current dimensions may be
//     either. Dynamic dimensions are not compared at all.
//   - level_removed: a threshold alert whose level is no longer among the
//     Plans' Levels. Decided only when every Level the strategy declares is
//     detected (a refused Level is not a removed one) and only for the
//     built-in levels, whose names the link stores unchanged; a level whose
//     name the link may have mapped is not decided.
//   - condition_changed: a no-data alert whose strategy's filter conditions
//     are not the ones it was raised under, by the platform's own condition
//     digest (contract.AggConditionMD5), from the strategy facts as above. A
//     threshold alert is not closed on a condition change. This rule is held
//     (conditionRuleReleased): it closes nothing, and every no-data
//     candidate it would have closed is counted condition_rule_held.
//   - no_data_disabled: a no-data alert of a strategy that no longer
//     configures no-data detection. A strategy that configures it and whose
//     Plans do not attach it is not switched off and is not decided.
//
// The strategy facts are read only when the alert's carry a schema this
// build reads (v2) and the Plans carry facts too. An alert without them -
// every alert opened before events carried them - or with another schema is
// not decided by metric_changed and condition_changed, for good, and its
// grouping only by the removed-or-renamed check; it is counted
// (facts_missing, facts_unknown_schema, plan_facts_missing) beside whatever
// the other rules decided.
//
// Every candidate that is not closed is counted under the word that kept
// it, so a still-firing alert whose group survived the edit is a reading
// (kept) and not a silence.
package editclose

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

// The per-alert outcomes, closed: a metric label is made of them.
const (
	// OutcomeCurrentRevision is an alert raised under the current revision:
	// not a candidate.
	OutcomeCurrentRevision = "current_revision"
	// OutcomeRevisionAhead is an alert raised under a revision newer than
	// the current Plans': the publication has not caught up with the source
	// yet. Not a candidate until it has.
	OutcomeRevisionAhead = "revision_ahead"
	// OutcomeRevisionUnknown is a record that names no revision.
	OutcomeRevisionUnknown = "revision_unknown"
	// OutcomeFamilyUnknown is a record whose evaluation family is missing,
	// not one this process writes, or contradicted by its no-data tag, or
	// that carries no dimensions object: which rules apply is not known.
	OutcomeFamilyUnknown = "family_unknown"
	// OutcomeKept is a candidate every decidable rule keeps: its group,
	// level and family all survive the edit.
	OutcomeKept = "kept"
	// OutcomeLevelsPartial is a threshold candidate whose level is not
	// among the Plans' Levels while some Level of the strategy is refused:
	// it may be that one. Not closed.
	OutcomeLevelsPartial = "levels_partial"
	// OutcomeSeverityUnmapped is a threshold candidate whose level is not
	// among the Plans' Levels and is, or the Plans have, a level the link
	// may have stored under a mapped name. Not closed.
	OutcomeSeverityUnmapped = "severity_unmapped"
	// OutcomeNoDataSuspended is a no-data candidate of a strategy that
	// configures no-data and whose Plans do not attach it. Not closed.
	OutcomeNoDataSuspended = "no_data_suspended"

	// The closes, by the change that decided them: the same words the close
	// names (linkdoutput.StrategyChanged*).
	OutcomeMetricChanged     = linkdoutput.StrategyChangedMetric
	OutcomeConditionChanged  = linkdoutput.StrategyChangedCondition
	OutcomeDimensionsChanged = linkdoutput.StrategyChangedDimensions
	OutcomeLevelRemoved      = linkdoutput.StrategyChangedLevel
	OutcomeNoDataDisabled    = linkdoutput.StrategyChangedNoData
)

// The notes Decide returns beside a decision: why metric_changed and
// condition_changed could not be decided for the alert.
const (
	// OutcomeFactsMissing is an alert whose opening event carried no
	// strategy facts.
	OutcomeFactsMissing = "facts_missing"
	// OutcomeFactsUnknownSchema is an alert whose strategy facts are of a
	// schema this build does not read, or not readable as one it does.
	OutcomeFactsUnknownSchema = "facts_unknown_schema"
	// OutcomePlanFactsMissing is a current Plan without strategy facts.
	OutcomePlanFactsMissing = "plan_facts_missing"
	// OutcomeConditionRuleHeld is a no-data alert condition_changed would
	// have been decided for, while the rule is held (conditionRuleReleased)
	// or the two digests are of different bases.
	OutcomeConditionRuleHeld = "condition_rule_held"
	// OutcomeSnapshotUnreadable is a no-data alert whose condition digest,
	// or the current Plans', was computed beside a metric snapshot whose
	// conditions could not be read (contract.ConditionBasisSnapshotUnreadable):
	// condition_changed cannot be decided for it.
	OutcomeSnapshotUnreadable = "snapshot_unreadable"
)

// Decisions is every word Decide returns as its decision.
var Decisions = []string{OutcomeCurrentRevision, OutcomeRevisionAhead, OutcomeRevisionUnknown, OutcomeFamilyUnknown,
	OutcomeKept, OutcomeLevelsPartial, OutcomeSeverityUnmapped, OutcomeNoDataSuspended,
	OutcomeMetricChanged, OutcomeConditionChanged, OutcomeDimensionsChanged, OutcomeLevelRemoved, OutcomeNoDataDisabled}

// Notes is every word Decide returns as its note.
var Notes = []string{OutcomeFactsMissing, OutcomeFactsUnknownSchema, OutcomePlanFactsMissing, OutcomeConditionRuleHeld,
	OutcomeSnapshotUnreadable}

// Closes reports whether a decision is a close.
func Closes(decision string) bool {
	switch decision {
	case OutcomeMetricChanged, OutcomeConditionChanged, OutcomeDimensionsChanged, OutcomeLevelRemoved, OutcomeNoDataDisabled:
		return true
	}
	return false
}

// Plan is what the strategy's current Plans state (controlplane
// .StrategyPlanFacts).
type Plan struct {
	Revision          int64
	DimensionFields   []string
	DynamicDimensions bool
	// RecordFields are the dimensions the Plans' records carry beyond the
	// grouping (controlplane.StrategyPlanFacts.RecordDimensionFields): the
	// strategy still writes them, so an alert carrying one has not lost it.
	RecordFields     []string
	Levels           []contract.LevelDefinitionV2
	LevelsWhole      bool
	NoData           bool
	NoDataDimensions []string
	NoDataSuspended  bool
	// Facts is the Plans' strategy facts, nil where they have none.
	Facts *contract.StrategyFactsV2
}

// Key is the plan's facts as one comparable word: two Plans with the same
// key decide every alert the same way, which is what lets a decision made
// against one be remembered while the key holds.
func (plan Plan) Key() string {
	var b strings.Builder
	b.WriteString(strconv.FormatInt(plan.Revision, 10))
	b.WriteString("|")
	b.WriteString(strings.Join(plan.DimensionFields, ","))
	b.WriteString("|" + strings.Join(plan.RecordFields, ","))
	b.WriteString("|" + strconv.FormatBool(plan.DynamicDimensions) + "|")
	for _, level := range plan.Levels {
		b.WriteString(strconv.FormatUint(uint64(level.LevelID), 10) + ":" + level.LevelCode + ",")
	}
	b.WriteString("|" + strconv.FormatBool(plan.LevelsWhole) + "|" + strconv.FormatBool(plan.NoData) + "|")
	b.WriteString(strings.Join(plan.NoDataDimensions, ","))
	b.WriteString("|" + strconv.FormatBool(plan.NoDataSuspended) + "|")
	if plan.Facts != nil {
		b.WriteString(plan.Facts.Schema + ":" + plan.Facts.MetricDigest + ":" + plan.Facts.ConditionMD5 + ":" +
			plan.Facts.DimensionDigest + ":" + plan.Facts.NoDataDimensionDigest + ":" + plan.Facts.ConditionBasis)
	}
	return b.String()
}

// Alert is what an alert's record states (openalerts.AlertRecord).
type Alert struct {
	Revision         int64
	DimensionKeys    []string
	DimensionsRead   bool
	EvaluationFamily string
	Severity         string
	// Facts is extra_data.strategy_facts as the opening event wrote it,
	// unparsed; nil where it wrote none.
	Facts json.RawMessage
}

// The evaluation families this process writes (linkdoutput's
// extra_data.evaluation_family).
const (
	familyMetricAlgorithm = "metric_algorithm"
	familyNoData          = "no_data"
)

// addedDimensions are the dimensions this process writes into an alert's
// dimensions beside the strategy's grouping: the no-data tag, and the host
// identity Python's fuller fills in from CMDB (admission.Facts.HostOverlay).
// An alert carrying one of them says nothing about the grouping. A grouping
// that named one and dropped it is not decided for that dimension, which
// can only cost a close.
var addedDimensions = map[string]struct{}{
	contract.NoDataDimensionTag: {}, "__additional_dimensions": {},
	"bk_target_ip": {}, "bk_target_cloud_id": {}, "bk_host_id": {},
}

// builtInLevels are the levels whose names the link stores as this process
// writes them (linkdoutput.SeverityFor): its default severities, by name.
var builtInLevels = map[string]uint32{"critical": 1, "warning": 2, "info": 3}

// Decide is one alert's verdict against the current Plans, and a note when
// the strategy facts could not be compared (one of Notes, or empty).
func Decide(plan Plan, alert Alert) (decision, note string) {
	switch {
	case alert.Revision <= 0:
		return OutcomeRevisionUnknown, ""
	case alert.Revision == plan.Revision:
		return OutcomeCurrentRevision, ""
	case alert.Revision > plan.Revision:
		return OutcomeRevisionAhead, ""
	}
	noData, known := alertFamily(alert)
	if !known {
		return OutcomeFamilyUnknown, ""
	}
	facts, note := comparableFacts(plan, alert)
	if facts != nil && facts.MetricDigest != plan.Facts.MetricDigest {
		return OutcomeMetricChanged, note
	}
	if noData {
		if plan.NoData && groupingChanged(facts, planGrouping(plan.Facts, true), alert.DimensionKeys, plan.NoDataDimensions, nil, false) {
			return OutcomeDimensionsChanged, note
		}
		if facts != nil {
			if decision, held := conditionVerdict(facts, plan.Facts, conditionRuleReleased); decision != "" {
				return decision, note
			} else if held != "" {
				note = held
			}
		}
		if !plan.NoData {
			if plan.NoDataSuspended {
				return OutcomeNoDataSuspended, note
			}
			return OutcomeNoDataDisabled, note
		}
		return OutcomeKept, note
	}
	if groupingChanged(facts, planGrouping(plan.Facts, false), alert.DimensionKeys, plan.DimensionFields, plan.RecordFields, plan.DynamicDimensions) {
		return OutcomeDimensionsChanged, note
	}
	return levelVerdict(plan, alert.Severity), note
}

// comparableFacts is the alert's strategy facts when they and the Plans'
// can be compared, or the note that says why not.
func comparableFacts(plan Plan, alert Alert) (*contract.StrategyFactsV2, string) {
	if plan.Facts == nil || plan.Facts.Schema != contract.StrategyFactsSchemaV2 {
		return nil, OutcomePlanFactsMissing
	}
	if len(alert.Facts) == 0 {
		return nil, OutcomeFactsMissing
	}
	var facts contract.StrategyFactsV2
	if json.Unmarshal(alert.Facts, &facts) != nil || facts.Schema != contract.StrategyFactsSchemaV2 ||
		facts.MetricDigest == "" || facts.ConditionMD5 == "" || !knownConditionBasis(facts.ConditionBasis) {
		return nil, OutcomeFactsUnknownSchema
	}
	return &facts, ""
}

// alertFamily is whether the alert is a no-data alert, and whether that is
// known: the family written, agreeing with the tag the no-data series
// carries in its dimensions.
func alertFamily(alert Alert) (noData, known bool) {
	if !alert.DimensionsRead {
		return false, false
	}
	tagged := false
	for _, key := range alert.DimensionKeys {
		if key == contract.NoDataDimensionTag {
			tagged = true
		}
	}
	switch alert.EvaluationFamily {
	case familyNoData:
		return true, tagged
	case familyMetricAlgorithm:
		return false, !tagged
	}
	return false, false
}

// groupingChanged decides the grouping rule: by the digests when the
// alert's facts and the Plans' both state one (current is the Plans'), and
// otherwise by the removed-or-renamed check over the alert's dimension
// names, which a dynamic grouping skips.
func groupingChanged(facts *contract.StrategyFactsV2, current string, keys, grouping, carried []string, dynamic bool) bool {
	if facts != nil && facts.DimensionDigest != "" && current != "" {
		return facts.DimensionDigest != current
	}
	return !dynamic && carriesDroppedDimension(keys, grouping, carried)
}

// carriesDroppedDimension reports whether the alert has a dimension the
// grouping no longer names. Only grouping is compared: a dimension the
// records still carry beyond the grouping (carried) is not one the edit
// dropped, whatever the grouping says. A "tags." prefix is read the way the
// fingerprint reads it (contract.ProjectMonitorTarget), on both sides.
func carriesDroppedDimension(keys, grouping, carried []string) bool {
	grouped := make(map[string]struct{}, len(grouping)+len(carried))
	for _, field := range append(append([]string(nil), grouping...), carried...) {
		grouped[strings.TrimPrefix(field, "tags.")] = struct{}{}
	}
	for _, key := range keys {
		if _, added := addedDimensions[key]; added {
			continue
		}
		if _, ok := grouped[strings.TrimPrefix(key, "tags.")]; !ok {
			return true
		}
	}
	return false
}

// levelVerdict decides a threshold alert's level against the Plans' Levels.
func levelVerdict(plan Plan, severity string) string {
	builtInOnly := true
	for _, level := range plan.Levels {
		if linkdoutput.SeverityFor(contract.LevelResultV1{LevelID: level.LevelID, LevelCode: level.LevelCode}) == severity {
			return OutcomeKept
		}
		if !linkdoutput.SeverityIsBuiltIn(contract.LevelResultV1{LevelID: level.LevelID}) {
			builtInOnly = false
		}
	}
	if !plan.LevelsWhole {
		return OutcomeLevelsPartial
	}
	if _, builtIn := builtInLevels[severity]; !builtIn || !builtInOnly || len(plan.Levels) == 0 {
		return OutcomeSeverityUnmapped
	}
	return OutcomeLevelRemoved
}

// planGrouping is the Plans' grouping digest for a threshold or a no-data
// alert, empty where the Plans state none.
func planGrouping(facts *contract.StrategyFactsV2, noData bool) string {
	if facts == nil {
		return ""
	}
	if noData {
		return facts.NoDataDimensionDigest
	}
	return facts.DimensionDigest
}

// conditionRuleReleased gates condition_changed. It is held: the
// publisher's statement that a catalog metric's snapshot conditions are the
// user's original input is a code comment, not a published contract, and a
// change of that on the publisher's side would make this rule close alerts
// nobody's edit orphaned. Release it when the publisher documents
// snapshot.agg_condition as the user's input in its publication spec, or
// publishes a user-condition digest or marker; released, it compares only
// digests of the same basis.
const conditionRuleReleased = false

// conditionVerdict is condition_changed for a no-data alert whose facts and
// the current Plans' are comparable: the close, or the note that says why
// it was not decided (snapshot_unreadable, condition_rule_held), or neither
// when the conditions did not change.
func conditionVerdict(alert, current *contract.StrategyFactsV2, released bool) (decision, note string) {
	if alert.ConditionBasis == contract.ConditionBasisSnapshotUnreadable ||
		current.ConditionBasis == contract.ConditionBasisSnapshotUnreadable {
		return "", OutcomeSnapshotUnreadable
	}
	if alert.ConditionMD5 == current.ConditionMD5 {
		return "", ""
	}
	if !released || alert.ConditionBasis != current.ConditionBasis {
		return "", OutcomeConditionRuleHeld
	}
	return OutcomeConditionChanged, ""
}

func knownConditionBasis(basis string) bool {
	switch basis {
	case contract.ConditionBasisSnapshotUser, contract.ConditionBasisQuery, contract.ConditionBasisQueryInjected,
		contract.ConditionBasisSnapshotUnreadable:
		return true
	}
	return false
}
