// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The ways a Plan's data unit is chosen (DataUnitFacts.Basis).
const (
	// DataUnitBasisExpressionUnit: the expression the rules detect declares
	// its own unit (expression_unit), and only that unit is used; the
	// threshold prefix is a step of it. A declared empty unit says the
	// expression has none: its values are compared raw.
	DataUnitBasisExpressionUnit = "EXPRESSION_UNIT"
	// DataUnitBasisQueryUnit: nothing is declared and the item has one query
	// config, whose unit is the data unit, as Python reads it. A threshold
	// written without a prefix is in the unit's smallest step.
	DataUnitBasisQueryUnit = "QUERY_UNIT"
	// DataUnitBasisMultiQueryRaw: nothing is declared and the item computes
	// over several query configs. A value computed over several metrics has
	// no unit any member can lend it -- the ratio of a duration to a count is
	// not a duration -- so the value and the threshold are compared raw, as
	// Python's detection compares them (DataPoint.unit is "" for more than one
	// data source, detect/core.py:37-41).
	DataUnitBasisMultiQueryRaw = "MULTI_QUERY_RAW"
)

// ReasonDataUnitUnknownComparedRaw names a Level whose data unit the unit
// table does not define: its values cannot be scaled, so they and the
// thresholds are compared raw. The Level runs; it is not withheld.
const ReasonDataUnitUnknownComparedRaw = "DATA_UNIT_UNKNOWN_COMPARED_RAW"

// ReasonUnitPrefixIgnoredNoUnit names a Level whose threshold carries a
// prefix while its values have no unit -- a declared empty expression_unit, a
// value computed over several metrics, or a unit without steps. The prefix
// scales nothing and is ignored; the values and the thresholds are compared
// raw. Named because an owner who chose the prefix expects it to mean
// something, and nothing else says it does not.
const ReasonUnitPrefixIgnoredNoUnit = "UNIT_PREFIX_IGNORED_NO_UNIT"

// ReasonUnitPrefixOutsideStepsReadAsSmallest names a Level whose threshold
// prefix is not one of its data unit's steps. The prefix is read as if it were
// not given -- the threshold in the unit's smallest step -- which is what
// Python's conversion does with a suffix it cannot find (core/unit/models.py:
// 179-192), so the Level detects exactly as the platform does. The Level runs;
// it is not withheld.
const ReasonUnitPrefixOutsideStepsReadAsSmallest = "UNIT_PREFIX_OUTSIDE_STEPS_READ_AS_SMALLEST"

// DataUnitFacts says how a Plan's values are compared: how its data unit was
// chosen, and which Levels compare raw values instead. It is what a reader is
// shown, not what a Worker reads -- the units each algorithm compiles with are
// already in its config. Nil for a Plan whose unit is its one query's and
// whose Levels all compare in it, so no such Plan's object changes bytes.
type DataUnitFacts struct {
	Basis     string             `json:"basis"`
	RawLevels []DataUnitRawLevel `json:"raw_levels,omitempty"`
}

// DataUnitRawLevel is one Level compared raw, with the unit and prefix that
// could not be read and why.
type DataUnitRawLevel struct {
	LevelID uint32 `json:"level_id"`
	Reason  string `json:"reason"`
	Unit    string `json:"unit"`
	Prefix  string `json:"prefix,omitempty"`
}

// expressionDataUnit is the unit of the expression a Level's rules detect,
// and where it came from.
type expressionDataUnit struct {
	unit  string
	basis string
}

// unitOfDetectedExpression is the data unit of expression, the expression a
// rule detects.
//
// Today an item has one expression and every rule detects it, so this reads
// the item: its declared expression_unit when there is one, otherwise the
// unit of its one query config, otherwise none. A declared empty unit is a
// declaration, not an absence: the writer says the expression has no unit, and
// the query's unit is not consulted. It is asked per expression
// rather than per item so that a rule detecting another expression of the same
// item -- several metrics detected separately -- asks for that expression's
// unit, not the item's.
func unitOfDetectedExpression(item legacyItem, expression string) expressionDataUnit {
	if item.ExpressionUnit != nil && expression == item.Expression {
		return expressionDataUnit{unit: *item.ExpressionUnit, basis: DataUnitBasisExpressionUnit}
	}
	if len(item.QueryConfigs) != 1 {
		return expressionDataUnit{basis: DataUnitBasisMultiQueryRaw}
	}
	var config struct {
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(item.QueryConfigs[0], &config); err != nil {
		return expressionDataUnit{basis: DataUnitBasisQueryUnit}
	}
	return expressionDataUnit{unit: config.Unit, basis: DataUnitBasisQueryUnit}
}

// projectionUnit is the unit the Plan's input projection carries: the data
// unit when the table defines it, otherwise none, since nothing can be scaled
// by a unit nobody defines.
func (unit expressionDataUnit) projectionUnit() string {
	if !strategy.UnitKnown(unit.unit) {
		return ""
	}
	return unit.unit
}

// algorithmUnitReading is the data unit and threshold prefix one algorithm
// compiles with, and the reason it is noted when it is: compared raw, or a
// written prefix ignored.
type algorithmUnitReading struct {
	dataUnit string
	prefix   string
	note     string
}

// readAlgorithmUnit decides the data unit and prefix an algorithm with the
// written prefix compiles with.
//
// A unit the table does not define compiles with no unit and no prefix, so
// the value and the threshold are compared as written; a prefix that is not
// one of the unit's steps is read as not given, the threshold in the unit's
// smallest step. Both are what Python's conversion does with them. Neither
// takes the Level out of service; both are named on it.
//
// An empty prefix is read as written, declared unit or not: the unit's step
// whose suffix is empty, which is its smallest (bytes' B), as Python reads it.
// A writer that supports expression_unit writes a step of the unit in every
// prefix -- the empty one only where the unit has an empty step, or where the
// expression has no unit -- so there is nothing to fill in here, and filling
// it in would change the threshold of every strategy saved again unchanged
// with an empty step.
func readAlgorithmUnit(unit expressionDataUnit, writtenPrefix string) algorithmUnitReading {
	if !strategy.UnitKnown(unit.unit) {
		return algorithmUnitReading{note: ReasonDataUnitUnknownComparedRaw}
	}
	if !strategy.UnitPrefixInSteps(unit.unit, writtenPrefix) {
		return algorithmUnitReading{dataUnit: unit.unit, note: ReasonUnitPrefixOutsideStepsReadAsSmallest}
	}
	if writtenPrefix != "" && !strategy.UnitHasSteps(unit.unit) {
		return algorithmUnitReading{dataUnit: unit.unit, note: ReasonUnitPrefixIgnoredNoUnit}
	}
	return algorithmUnitReading{dataUnit: unit.unit, prefix: writtenPrefix}
}

// algorithmReadsUnit says the algorithm kind compiles with the data unit and
// a threshold prefix; the others compare nothing in a unit, and a unit that
// cannot be read says nothing about them.
func algorithmReadsUnit(kind string) bool {
	return kind == strategy.DetectorKindThreshold || kind == SourceAlgorithmTypePingUnreachable || strategy.IsTraditionalComparison(kind)
}

// algorithmReadsPrefix says the algorithm kind reads its threshold prefix: a
// unit note is named only on these. The ratio and year-on-year kinds compile
// with the unit too but convert both sides alike and read no prefix, as
// Python's do, so a note on them would name a prefix that changes nothing.
func algorithmReadsPrefix(kind string) bool {
	switch kind {
	case strategy.DetectorKindThreshold, strategy.DetectorKindYearRoundAmplitude, strategy.DetectorKindRingRatioAmplitude, strategy.DetectorKindYearRoundRange:
		return true
	}
	return false
}

// unitNote is the note on a Level whose unit reading is named: compared raw,
// or its written prefix ignored.
func unitNote(sourceID string, levelID uint32, unit expressionDataUnit, writtenPrefix, reason string) ObjectDisposition {
	field := "items[0].query_configs[*].unit"
	switch {
	case reason == ReasonUnitPrefixOutsideStepsReadAsSmallest || reason == ReasonUnitPrefixIgnoredNoUnit:
		field = "items[0].algorithms[*].unit_prefix"
	case unit.basis == DataUnitBasisExpressionUnit:
		field = "items[0].expression_unit"
	}
	return ObjectDisposition{SourceID: sourceID, Scope: "LEVEL", LevelID: levelID, Disposition: DispositionConfigNoted, Reason: reason,
		FieldPath: field, Detail: dispositionDetail(fmt.Sprintf("unit=%q prefix=%q", unit.unit, writtenPrefix))}
}

// levelNoted says levelID already carries the note reason, so one Level with
// several algorithms reading the same unit is named once.
func levelNoted(levels []DataUnitRawLevel, levelID uint32, reason string) bool {
	for _, level := range levels {
		if level.LevelID == levelID && level.Reason == reason {
			return true
		}
	}
	return false
}

// dataUnitFactsOf is the Plan's DataUnitFacts: nil for a Plan whose unit is
// its one query's and whose Levels all read it, so such a Plan's object keeps
// its bytes.
func dataUnitFactsOf(unit expressionDataUnit, rawLevels []DataUnitRawLevel) *DataUnitFacts {
	if unit.basis == DataUnitBasisQueryUnit && len(rawLevels) == 0 {
		return nil
	}
	return &DataUnitFacts{Basis: unit.basis, RawLevels: rawLevels}
}
