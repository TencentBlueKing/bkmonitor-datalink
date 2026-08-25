// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"encoding/json"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	DetectorKindThreshold = "Threshold"

	TriggerPlanTypeNOfM                   = "N_OF_M"
	RecoveryPlanTypeContinuousTriggerMiss = "CONTINUOUS_TRIGGER_MISS"

	PredicateAny     = "ANY"
	PredicateAll     = "ALL"
	PredicateCompare = "COMPARE"
)

type Limits struct {
	MaxPlanBytes              int
	MaxLevelsPerPlan          int
	MaxAlgorithmsPerLevel     int
	MaxGroupsPerAlgorithm     int
	MaxConditionsPerAlgorithm int
	MaxASTNodesPerLevel       int
	MaxRequiredHistoryPoints  uint32
}

type StateSemantics struct {
	StateSchemaVersion          string
	CodecSemanticsVersion       string
	IdentitySchemaDigest        string
	SourceTimeSemanticsVersion  string
	HistoryCellSemanticsVersion string
}

type CompileRequest struct {
	Plan            contract.EvaluationPlanV2
	DatasetContract contract.DatasetContractV2
	StateSemantics  StateSemantics
}

type Terminal struct {
	LevelID    uint32
	ReasonCode string
	FieldPath  string
}

type CompileResult struct {
	plan           *CompiledPlan
	planTerminal   *Terminal
	levelTerminals []Terminal
}

func (r CompileResult) Plan() (*CompiledPlan, bool) {
	return r.plan, r.plan != nil
}

func (r CompileResult) PlanTerminal() *Terminal {
	if r.planTerminal == nil {
		return nil
	}
	terminal := *r.planTerminal
	return &terminal
}

func (r CompileResult) LevelTerminals() []Terminal {
	return append([]Terminal(nil), r.levelTerminals...)
}

type PlanFingerprints struct {
	Detect  string
	Trigger string
}

type LevelFingerprints struct {
	Detect  string
	Trigger string
}

type TriggerPlan struct {
	WindowSize        uint32
	RequiredAnomalies uint32
	StepSeconds       uint32
}

type RecoveryPlan struct {
	Enabled            bool
	ConsecutiveWindows uint32
}

type StateRequirement struct {
	RequiredDetectHistoryPoints uint32
}

type NormalizedNumber struct {
	micros int64
}

func (n NormalizedNumber) Micros() int64 {
	return n.micros
}

type NormalizeResult struct {
	value      NormalizedNumber
	reasonCode string
}

func (r NormalizeResult) Available() bool {
	return r.reasonCode == ""
}

func (r NormalizeResult) Value() NormalizedNumber {
	return r.value
}

func (r NormalizeResult) ReasonCode() string {
	return r.reasonCode
}

type Predicate struct {
	root predicateNode
}

type predicateNode struct {
	kind      string
	operator  string
	threshold NormalizedNumber
	children  []predicateNode
}

func (p Predicate) Evaluate(value NormalizedNumber) bool {
	return p.root.evaluate(value)
}

func (p Predicate) clone() Predicate {
	return Predicate{root: p.root.clone()}
}

func (n predicateNode) clone() predicateNode {
	cloned := n
	cloned.children = make([]predicateNode, len(n.children))
	for index := range n.children {
		cloned.children[index] = n.children[index].clone()
	}
	return cloned
}

func (n predicateNode) evaluate(value NormalizedNumber) bool {
	switch n.kind {
	case PredicateAny:
		for _, child := range n.children {
			if child.evaluate(value) {
				return true
			}
		}
		return false
	case PredicateAll:
		for _, child := range n.children {
			if !child.evaluate(value) {
				return false
			}
		}
		return true
	case PredicateCompare:
		switch n.operator {
		case "GT":
			return value.micros > n.threshold.micros
		case "GTE":
			return value.micros >= n.threshold.micros
		case "EQ":
			return value.micros == n.threshold.micros
		case "NEQ":
			return value.micros != n.threshold.micros
		case "LT":
			return value.micros < n.threshold.micros
		case "LTE":
			return value.micros <= n.threshold.micros
		}
	}
	return false
}

type DetectorSpec struct {
	kind                   string
	version                uint32
	valueRef               string
	normalizerRef          string
	predicate              Predicate
	declaredExecutorErrors []string
}

func (s DetectorSpec) Kind() string {
	return s.kind
}

func (s DetectorSpec) Version() uint32 {
	return s.version
}

func (s DetectorSpec) ValueRef() string {
	return s.valueRef
}

func (s DetectorSpec) NormalizerRef() string {
	return s.normalizerRef
}

func (s DetectorSpec) Predicate() Predicate {
	return s.predicate.clone()
}

func (s DetectorSpec) DeclaredExecutorErrors() []string {
	return append([]string(nil), s.declaredExecutorErrors...)
}

func (s DetectorSpec) clone() DetectorSpec {
	cloned := s
	cloned.predicate = s.predicate.clone()
	cloned.declaredExecutorErrors = append([]string(nil), s.declaredExecutorErrors...)
	return cloned
}

type NumericNormalizerSpec struct {
	ref              string
	sourceUnit       string
	targetUnit       string
	sourceMultiplier int64
	decimalPlaces    uint32
	rounding         string
}

func (s NumericNormalizerSpec) Ref() string {
	return s.ref
}

func (s NumericNormalizerSpec) SourceUnit() string {
	return s.sourceUnit
}

func (s NumericNormalizerSpec) TargetUnit() string {
	return s.targetUnit
}

func (s NumericNormalizerSpec) DecimalPlaces() uint32 {
	return s.decimalPlaces
}

func (s NumericNormalizerSpec) Rounding() string {
	return s.rounding
}

type CompiledLevel struct {
	definition       contract.LevelDefinitionV2
	connector        string
	detectors        []DetectorSpec
	trigger          TriggerPlan
	recovery         RecoveryPlan
	stateRequirement StateRequirement
	fingerprints     LevelFingerprints
}

func (l CompiledLevel) Definition() contract.LevelDefinitionV2 {
	return l.definition
}

func (l CompiledLevel) Connector() string {
	return l.connector
}

func (l CompiledLevel) Detectors() []DetectorSpec {
	result := make([]DetectorSpec, len(l.detectors))
	for index := range l.detectors {
		result[index] = l.detectors[index].clone()
	}
	return result
}

func (l CompiledLevel) Trigger() TriggerPlan {
	return l.trigger
}

func (l CompiledLevel) Recovery() RecoveryPlan {
	return l.recovery
}

func (l CompiledLevel) RequiredDetectHistoryPoints() uint32 {
	return l.stateRequirement.RequiredDetectHistoryPoints
}

func (l CompiledLevel) StateRequirement() StateRequirement {
	return l.stateRequirement
}

func (l CompiledLevel) Fingerprints() LevelFingerprints {
	return l.fingerprints
}

func (l CompiledLevel) clone() CompiledLevel {
	cloned := l
	cloned.detectors = l.Detectors()
	return cloned
}

type CompiledPlan struct {
	planRef             contract.RuntimePlanRefV1
	projection          contract.InputProjectionV2
	evaluationSemantics contract.ExecutionSemanticsV2
	levels              []CompiledLevel
	normalizers         map[string]NumericNormalizerSpec
	fingerprints        PlanFingerprints
}

func (p *CompiledPlan) PlanRef() contract.RuntimePlanRefV1 {
	if p == nil {
		return contract.RuntimePlanRefV1{}
	}
	return p.planRef
}

func (p *CompiledPlan) StateCompatibilityHash() string {
	if p == nil {
		return ""
	}
	return p.planRef.StateCompatibilityHash
}

func (p *CompiledPlan) Projection() contract.InputProjectionV2 {
	if p == nil {
		return contract.InputProjectionV2{}
	}
	projection := p.projection
	projection.ValueFields = append([]string(nil), p.projection.ValueFields...)
	projection.DimensionFields = append([]string(nil), p.projection.DimensionFields...)
	return projection
}

func (p *CompiledPlan) EvaluationSemantics() contract.ExecutionSemanticsV2 {
	if p == nil {
		return contract.ExecutionSemanticsV2{}
	}
	return p.evaluationSemantics
}

func (p *CompiledPlan) Levels() []CompiledLevel {
	if p == nil {
		return nil
	}
	levels := make([]CompiledLevel, len(p.levels))
	for index := range p.levels {
		levels[index] = p.levels[index].clone()
	}
	return levels
}

func (p *CompiledPlan) LevelsByPriority() []CompiledLevel {
	levels := p.Levels()
	sort.Slice(levels, func(left, right int) bool {
		if levels[left].definition.Priority == levels[right].definition.Priority {
			return levels[left].definition.LevelID < levels[right].definition.LevelID
		}
		return levels[left].definition.Priority < levels[right].definition.Priority
	})
	return levels
}

func (p *CompiledPlan) Normalizer(ref string) (NumericNormalizerSpec, bool) {
	if p == nil {
		return NumericNormalizerSpec{}, false
	}
	normalizer, ok := p.normalizers[ref]
	return normalizer, ok
}

func (p *CompiledPlan) Fingerprints() PlanFingerprints {
	if p == nil {
		return PlanFingerprints{}
	}
	return p.fingerprints
}

type thresholdConfigV1 struct {
	ValueField          string             `json:"value_field"`
	DataUnit            string             `json:"data_unit"`
	ThresholdUnitPrefix *string            `json:"threshold_unit_prefix"`
	Precision           thresholdPrecision `json:"precision"`
	Groups              []thresholdGroup   `json:"groups"`
}

type thresholdPrecision struct {
	DecimalPlaces uint32 `json:"decimal_places"`
	Rounding      string `json:"rounding"`
}

type thresholdGroup struct {
	Conditions []thresholdCondition `json:"conditions"`
}

type thresholdCondition struct {
	Operator         string `json:"operator"`
	ThresholdDecimal string `json:"threshold_decimal"`
}

type triggerPlanConfigV1 struct {
	WindowSize        uint32 `json:"window_size"`
	RequiredAnomalies uint32 `json:"required_anomalies"`
	StepSeconds       uint32 `json:"step_seconds"`
}

type recoveryPlanConfigV1 struct {
	Enabled            *bool  `json:"enabled"`
	ConsecutiveWindows uint32 `json:"consecutive_windows"`
}

type detectorSemantic struct {
	Kind                   string         `json:"kind"`
	Version                uint32         `json:"version"`
	ValueRef               string         `json:"value_ref"`
	Normalizer             normalizerWire `json:"normalizer"`
	Predicate              predicateWire  `json:"predicate"`
	DeclaredExecutorErrors []string       `json:"declared_executor_errors"`
}

type normalizerWire struct {
	Ref              string `json:"ref"`
	SourceUnit       string `json:"source_unit"`
	TargetUnit       string `json:"target_unit"`
	SourceMultiplier int64  `json:"source_multiplier"`
	DecimalPlaces    uint32 `json:"decimal_places"`
	Rounding         string `json:"rounding"`
}

type predicateWire struct {
	Kind            string          `json:"kind"`
	Operator        string          `json:"operator,omitempty"`
	ThresholdMicros *int64          `json:"threshold_micros,omitempty"`
	Children        []predicateWire `json:"children,omitempty"`
}

func (s DetectorSpec) semantic(normalizer NumericNormalizerSpec) detectorSemantic {
	return detectorSemantic{
		Kind: s.kind, Version: s.version, ValueRef: s.valueRef,
		Normalizer: normalizerWire{
			Ref: normalizer.ref, SourceUnit: normalizer.sourceUnit, TargetUnit: normalizer.targetUnit,
			SourceMultiplier: normalizer.sourceMultiplier, DecimalPlaces: normalizer.decimalPlaces, Rounding: normalizer.rounding,
		},
		Predicate:              s.predicate.root.wire(),
		DeclaredExecutorErrors: append([]string(nil), s.declaredExecutorErrors...),
	}
}

func (n predicateNode) wire() predicateWire {
	wire := predicateWire{Kind: n.kind, Operator: n.operator}
	if n.kind == PredicateCompare {
		threshold := n.threshold.micros
		wire.ThresholdMicros = &threshold
	}
	if len(n.children) > 0 {
		wire.Children = make([]predicateWire, len(n.children))
		for index := range n.children {
			wire.Children[index] = n.children[index].wire()
		}
	}
	return wire
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
