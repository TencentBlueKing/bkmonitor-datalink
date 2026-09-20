// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const (
	estimatedSeriesBytes    uint64 = 192
	estimatedRecordBytes    uint64 = 160
	estimatedProjectedBytes uint64 = 192
	estimatedLevelFactBytes uint64 = 256
)

type Evaluator struct {
	registry *Registry
	observer Observer
}

// boundPlan is a compiled Plan with its detectors and algorithms resolved.
// PreparePlan builds one per Plan and the prepared-record path evaluates
// against it.
type boundPlan struct {
	execution       PlanExecution
	levels          []boundLevel
	detectorCount   uint64
	namedInputCount uint64
	projectionCount uint64
}

type boundLevel struct {
	compiled   strategy.CompiledLevel
	detectors  []boundDetector
	algorithms []boundAlgorithm
}

type boundDetector struct {
	spec       strategy.DetectorSpec
	detector   Detector
	normalizer strategy.NumericNormalizerSpec
}

type boundAlgorithm struct {
	compiled strategy.CompiledAlgorithmPlan
	standard *boundDetector
	named    namedInputDetector
}

func NewEvaluator(registry *Registry, observer Observer) (*Evaluator, error) {
	if registry == nil || len(registry.detectors) == 0 {
		return nil, errors.New("alarmd detect: detector registry is required")
	}
	return &Evaluator{registry: registry, observer: observer}, nil
}

type projectionKey struct {
	valueRef      string
	normalizerRef string
}

type projectionEntry struct {
	key   projectionKey
	value strategy.NormalizedNumber
	view  ProjectedValue
}

func (evaluator *Evaluator) evaluateLevel(
	ctx context.Context,
	plan *strategy.CompiledPlan,
	record RecordValueView,
	level boundLevel,
	projections *[]projectionEntry,
) (LevelFact, uint64, error) {
	fact := LevelFact{
		Definition: level.compiled.Definition(), DetectFingerprint: level.compiled.Fingerprints().Detect,
	}
	matched := level.compiled.Connector() == contract.LevelConnectorAND
	evidenceSet := false
	unknown := false
	terminal := false
	unknownReason := ""
	var unknownEvidence ThresholdEvidence
	var evidenceFact AlgorithmFact
	var evidenceAlgorithm uint32
	var evidenceProjection uint32
	predicateEvaluations := uint64(0)
	truths := make([]algorithmTruth, 0, len(level.detectors))
	for algorithmIndex, bound := range level.detectors {
		projectionOrdinal, projection := projectRecordValue(record, bound, projections)
		if !projection.view.Available {
			truths = append(truths, algorithmTruthUnknown)
			unknown = true
			if unknownReason == "" {
				unknownReason = projection.view.ReasonCode
				unknownEvidence = ThresholdEvidence{
					ProjectedValueOrdinal: projectionOrdinal, HasProjectedValue: true, ResultReason: projection.view.ReasonCode,
				}
			}
			continue
		}
		algorithmFact, err := callDetector(ctx, bound.detector, bound.spec, projection.value)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return LevelFact{}, predicateEvaluations, err
			}
			var controlled *ControlledError
			if !errors.As(err, &controlled) || !declaresReason(bound.spec, controlled.ReasonCode) {
				return LevelFact{}, predicateEvaluations, &InternalError{Operation: "execute detector", PlanID: plan.PlanRef().StrategyID, Err: err}
			}
			unknown = true
			terminal = true
			truths = append(truths, algorithmTruthUnknown)
			if unknownReason == "" {
				unknownReason = controlled.ReasonCode
				unknownEvidence = ThresholdEvidence{
					ProjectedValueOrdinal: projectionOrdinal, HasProjectedValue: true, ResultReason: controlled.ReasonCode,
				}
			}
			continue
		}
		if len(algorithmFact.PredicateDigest) != 64 || algorithmFact.MatchedGroup < -1 ||
			(!algorithmFact.Matched && algorithmFact.MatchedGroup != -1) ||
			(bound.spec.Kind() == strategy.DetectorKindThreshold && algorithmFact.Matched && algorithmFact.MatchedGroup < 0) {
			return LevelFact{}, predicateEvaluations, &InternalError{
				Operation: "execute detector", PlanID: plan.PlanRef().StrategyID, Err: errors.New("detector returned an invalid fact"),
			}
		}
		predicateEvaluations++
		if algorithmFact.Matched {
			truths = append(truths, algorithmTruthTrue)
		} else {
			truths = append(truths, algorithmTruthFalse)
		}
		if algorithmIndex == 0 {
			evidenceFact = algorithmFact
			evidenceProjection = projectionOrdinal
		}
		if level.compiled.Connector() == contract.LevelConnectorAND {
			if !algorithmFact.Matched {
				matched = false
				if !evidenceSet {
					evidenceSet = true
					evidenceFact = algorithmFact
					evidenceAlgorithm = uint32(algorithmIndex)
					evidenceProjection = projectionOrdinal
				}
			}
			continue
		}
		if algorithmFact.Matched {
			matched = true
			if !evidenceSet {
				evidenceSet = true
				evidenceFact = algorithmFact
				evidenceAlgorithm = uint32(algorithmIndex)
				evidenceProjection = projectionOrdinal
			}
		}
	}
	combined, determined := combineAlgorithmTruth(level.compiled.Connector(), truths)
	if determined {
		if combined {
			fact.Result = FactResultAnomalous
		} else {
			fact.Result = FactResultNormal
		}
	} else if unknown {
		fact.Result = FactResultUnavailable
		if terminal {
			fact.Result = FactResultError
		}
		fact.ReasonCode, fact.Evidence = unknownReason, unknownEvidence
		return fact, predicateEvaluations, nil
	} else if matched {
		fact.Result = FactResultAnomalous
	} else {
		fact.Result = FactResultNormal
	}
	if len(level.detectors) > 0 {
		fact.Evidence.PredicateDigest = evidenceFact.PredicateDigest
		fact.Evidence.ProjectedValueOrdinal = evidenceProjection
		fact.Evidence.HasProjectedValue = true
		if fact.Result == FactResultAnomalous {
			fact.Evidence.MatchedAlgorithmOrdinal = evidenceAlgorithm
			fact.Evidence.HasMatchedAlgorithm = true
		}
		if fact.Result == FactResultAnomalous && evidenceFact.MatchedGroup >= 0 {
			fact.Evidence.MatchedGroupOrdinal = uint32(evidenceFact.MatchedGroup)
			fact.Evidence.HasMatchedGroup = true
		}
	}
	return fact, predicateEvaluations, nil
}

type algorithmTruth uint8

const (
	algorithmTruthFalse algorithmTruth = iota
	algorithmTruthTrue
	algorithmTruthUnknown
)

func combineAlgorithmTruth(connector string, truths []algorithmTruth) (bool, bool) {
	unknown := false
	if connector == contract.LevelConnectorAND {
		for _, truth := range truths {
			if truth == algorithmTruthFalse {
				return false, true
			}
			unknown = unknown || truth == algorithmTruthUnknown
		}
		return !unknown, !unknown
	}
	for _, truth := range truths {
		if truth == algorithmTruthTrue {
			return true, true
		}
		unknown = unknown || truth == algorithmTruthUnknown
	}
	return false, !unknown
}

func projectRecordValue(
	record RecordValueView,
	detector boundDetector,
	projections *[]projectionEntry,
) (uint32, projectionEntry) {
	spec := detector.spec
	key := projectionKey{valueRef: spec.ValueRef(), normalizerRef: spec.NormalizerRef()}
	for ordinal := range *projections {
		if (*projections)[ordinal].key == key {
			return uint32(ordinal), (*projections)[ordinal]
		}
	}
	raw, present := record.Value(spec.ValueRef())
	if !present {
		raw = nil
	}
	normalized := detector.normalizer.Normalize(raw)
	entry := projectionEntry{key: key, view: ProjectedValue{
		ValueRef: spec.ValueRef(), NormalizerRef: spec.NormalizerRef(), Available: normalized.Available(), ReasonCode: normalized.ReasonCode(),
	}}
	if normalized.Available() {
		entry.value = normalized.Value()
		entry.view.CanonicalDecimal = normalized.Value().CanonicalDecimal()
	}
	ordinal := uint32(len(*projections))
	*projections = append(*projections, entry)
	return ordinal, entry
}

func callDetector(
	ctx context.Context,
	detector Detector,
	spec strategy.DetectorSpec,
	value strategy.NormalizedNumber,
) (fact AlgorithmFact, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fact = AlgorithmFact{}
			err = fmt.Errorf("detector panic: %v", recovered)
		}
	}()
	return detector.Evaluate(ctx, spec, value)
}

func declaresReason(spec strategy.DetectorSpec, reason string) bool {
	if reason == "" {
		return false
	}
	reasons := spec.DeclaredExecutorErrors()
	index := sort.SearchStrings(reasons, reason)
	return index < len(reasons) && reasons[index] == reason
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if math.MaxUint64-left < right {
		return 0, false
	}
	return left + right, true
}

func checkedMul(left, right uint64) (uint64, bool) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, false
	}
	return left * right, true
}
