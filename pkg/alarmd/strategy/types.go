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
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

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
	MaxCompiledPlanBytes      int
	MaxCacheEntries           int
	MaxCacheBytes             int
	NegativeCacheTTL          time.Duration
	BudgetRevision            string
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

type ResourceEstimate struct {
	FixedComputeCost     uint64
	CostPerRecord        uint64
	StatePointsPerSeries uint64
	CompiledBytes        int
	Algorithms           int
	ASTNodes             int
}

type NormalizedNumber struct {
	negative bool
	high     uint64
	low      uint64
}

func (n NormalizedNumber) CanonicalDecimal() string {
	digits := n.magnitude().String()
	if len(digits) <= 6 {
		digits = strings.Repeat("0", 7-len(digits)) + digits
	}
	result := digits[:len(digits)-6] + "." + digits[len(digits)-6:]
	if n.negative && (n.high != 0 || n.low != 0) {
		return "-" + result
	}
	return result
}

func (n NormalizedNumber) compare(other NormalizedNumber) int {
	if n.negative != other.negative {
		if n.negative {
			return -1
		}
		return 1
	}
	comparison := 0
	if n.high < other.high || (n.high == other.high && n.low < other.low) {
		comparison = -1
	} else if n.high > other.high || (n.high == other.high && n.low > other.low) {
		comparison = 1
	}
	if n.negative {
		return -comparison
	}
	return comparison
}

func (n NormalizedNumber) magnitude() *big.Int {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], n.high)
	binary.BigEndian.PutUint64(payload[8:], n.low)
	return new(big.Int).SetBytes(payload)
}

func normalizedNumberFromBigInt(value *big.Int) (NormalizedNumber, bool) {
	if value == nil {
		return NormalizedNumber{}, false
	}
	magnitude := new(big.Int).Abs(new(big.Int).Set(value))
	if magnitude.BitLen() > 127 {
		return NormalizedNumber{}, false
	}
	payload := magnitude.FillBytes(make([]byte, 16))
	return NormalizedNumber{
		negative: value.Sign() < 0,
		high:     binary.BigEndian.Uint64(payload[:8]),
		low:      binary.BigEndian.Uint64(payload[8:]),
	}, true
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
	root      predicateNode
	digest    string
	validated bool
}

type predicateNode struct {
	kind      string
	operator  string
	threshold NormalizedNumber
	children  []predicateNode
}

type PredicateEvaluation struct {
	matched         bool
	matchedGroup    int
	predicateDigest string
}

func (e PredicateEvaluation) Matched() bool { return e.matched }

func (e PredicateEvaluation) MatchedGroup() int { return e.matchedGroup }

func (e PredicateEvaluation) PredicateDigest() string { return e.predicateDigest }

func (p Predicate) Evaluate(value NormalizedNumber) (PredicateEvaluation, error) {
	if !p.validated || len(p.digest) != 64 {
		return PredicateEvaluation{}, errors.New("strategy: predicate is not a validated compiled value")
	}
	matchedGroup := -1
	if p.root.kind == PredicateAny {
		for index, child := range p.root.children {
			matched, err := child.evaluate(value)
			if err != nil {
				return PredicateEvaluation{}, err
			}
			if matched {
				matchedGroup = index
				break
			}
		}
		return PredicateEvaluation{matched: matchedGroup >= 0, matchedGroup: matchedGroup, predicateDigest: p.digest}, nil
	}
	matched, err := p.root.evaluate(value)
	if err != nil {
		return PredicateEvaluation{}, err
	}
	if matched {
		matchedGroup = 0
	}
	return PredicateEvaluation{matched: matched, matchedGroup: matchedGroup, predicateDigest: p.digest}, nil
}

func (p Predicate) Digest() string { return p.digest }

func (n predicateNode) validate() (int, error) {
	switch n.kind {
	case PredicateAny, PredicateAll:
		if len(n.children) == 0 || n.operator != "" {
			return 0, errors.New("strategy: invalid predicate branch")
		}
		nodes := 1
		for _, child := range n.children {
			childNodes, err := child.validate()
			if err != nil {
				return 0, err
			}
			nodes += childNodes
		}
		return nodes, nil
	case PredicateCompare:
		if len(n.children) != 0 || !validOperator(n.operator) {
			return 0, errors.New("strategy: invalid predicate comparison")
		}
		return 1, nil
	default:
		return 0, fmt.Errorf("strategy: unknown predicate node %q", n.kind)
	}
}

func (n predicateNode) evaluate(value NormalizedNumber) (bool, error) {
	switch n.kind {
	case PredicateAny:
		for _, child := range n.children {
			matched, err := child.evaluate(value)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	case PredicateAll:
		for _, child := range n.children {
			matched, err := child.evaluate(value)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	case PredicateCompare:
		comparison := value.compare(n.threshold)
		switch n.operator {
		case "GT":
			return comparison > 0, nil
		case "GTE":
			return comparison >= 0, nil
		case "EQ":
			return comparison == 0, nil
		case "NEQ":
			return comparison != 0, nil
		case "LT":
			return comparison < 0, nil
		case "LTE":
			return comparison <= 0, nil
		}
	}
	return false, errors.New("strategy: predicate invariant violated")
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
	return s.predicate
}

func (s DetectorSpec) PredicateDigest() string { return s.predicate.digest }

func (s DetectorSpec) DeclaredExecutorErrors() []string {
	return append([]string(nil), s.declaredExecutorErrors...)
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
	effectiveTime    EffectiveTimeRequirement
	stateRequirement StateRequirement
	fingerprints     LevelFingerprints
	resourceEstimate ResourceEstimate
}

func (l CompiledLevel) Definition() contract.LevelDefinitionV2 {
	return l.definition
}

func (l CompiledLevel) Connector() string {
	return l.connector
}

func (l CompiledLevel) Detectors() []DetectorSpec {
	return append([]DetectorSpec(nil), l.detectors...)
}

func (l CompiledLevel) Trigger() TriggerPlan {
	return l.trigger
}

func (l CompiledLevel) Recovery() RecoveryPlan {
	return l.recovery
}

func (l CompiledLevel) EffectiveTimeRequirement() EffectiveTimeRequirement {
	return l.effectiveTime.clone()
}

func (l CompiledLevel) EffectiveTimeRequirementDigest() string {
	return l.effectiveTime.digest
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

func (l CompiledLevel) ResourceEstimate() ResourceEstimate { return l.resourceEstimate }

type CompiledPlan struct {
	planRef             contract.RuntimePlanRefV1
	projection          contract.InputProjectionV2
	evaluationSemantics contract.ExecutionSemanticsV2
	levels              []CompiledLevel
	normalizers         map[string]NumericNormalizerSpec
	fingerprints        PlanFingerprints
	resourceEstimate    ResourceEstimate
	datasetDigest       string
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
	return append([]CompiledLevel(nil), p.levels...)
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

func (p *CompiledPlan) ResourceEstimate() ResourceEstimate {
	if p == nil {
		return ResourceEstimate{}
	}
	return p.resourceEstimate
}

func (p *CompiledPlan) DatasetContractDigest() string {
	if p == nil {
		return ""
	}
	return p.datasetDigest
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
	WindowSize        uint32          `json:"window_size"`
	RequiredAnomalies uint32          `json:"required_anomalies"`
	StepSeconds       uint32          `json:"step_seconds"`
	TimezoneRef       string          `json:"timezone_ref,omitempty"`
	Uptime            *uptimeConfigV1 `json:"uptime,omitempty"`
}

type uptimeConfigV1 struct {
	TimeRanges      *[]uptimeTimeRangeV1 `json:"time_ranges"`
	ActiveCalendars []int64              `json:"active_calendars,omitempty"`
	Calendars       []int64              `json:"calendars,omitempty"`
}

type uptimeTimeRangeV1 struct {
	Start string `json:"start"`
	End   string `json:"end"`
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
	Kind                string          `json:"kind"`
	Operator            string          `json:"operator,omitempty"`
	NormalizedThreshold *string         `json:"normalized_threshold,omitempty"`
	Children            []predicateWire `json:"children,omitempty"`
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
		threshold := n.threshold.CanonicalDecimal()
		wire.NormalizedThreshold = &threshold
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
