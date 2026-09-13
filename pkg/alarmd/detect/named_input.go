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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type namedAlgorithmResult struct {
	result           string
	reasonCode       string
	canonicalDecimal string
	predicateDigest  string
}

type simpleRingRatioDetector struct{}
type osRestartDetector struct{}
type procPortDetector struct{}

func (simpleRingRatioDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindSimpleRingRatio, Version: 1}
}

func (osRestartDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindOsRestart, Version: 1}
}

func (procPortDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindProcPort, Version: 1}
}

func (simpleRingRatioDetector) Evaluate(
	_ context.Context,
	algorithm strategy.CompiledAlgorithmPlan,
	input execution.SeriesEvaluationInputRequest,
	primary execution.RecordView,
) namedAlgorithmResult {
	config, ok := algorithm.SimpleRingRatioConfig()
	if !ok {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	current, canonical, err := numericRecordValue(primary, config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	previousBinding, ok := namedBinding(input, "previous")
	if !ok || !namedInputTrusted(previousBinding) {
		return unavailableFromBinding(previousBinding)
	}
	previousRecord, found, err := exactPoint(previousBinding, primary.SourceTime()-algorithmOffset(algorithm, "previous"))
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	if !found {
		return namedUnavailable(contract.ReasonHistoryGapped)
	}
	previous, _, err := numericRecordValue(previousRecord, config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	floor, err := configuredPercent(config.FloorEnabled, config.FloorDecimal)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	ceil, err := configuredPercent(config.CeilEnabled, config.CeilDecimal)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	status, err := evaluateSimpleRingRatio(simpleRingRatioInput{
		current: current, previous: &previous, floorPercent: floor, ceilPercent: ceil,
	})
	return namedResult(status, canonical, algorithm.AlgorithmPlanID(), err)
}

func (osRestartDetector) Evaluate(
	_ context.Context,
	algorithm strategy.CompiledAlgorithmPlan,
	input execution.SeriesEvaluationInputRequest,
	primary execution.RecordView,
) namedAlgorithmResult {
	config, ok := algorithm.OsRestartConfig()
	if !ok {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	current, canonical, err := numericRecordValue(primary, config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	history, ok := namedBinding(input, "uptime_history")
	if !ok || !namedInputTrusted(history) {
		return unavailableFromBinding(history)
	}
	points := make(map[string]execution.RecordView, 3)
	for _, name := range []string{"previous", "previous_10m", "previous_25m"} {
		record, found, findErr := exactPoint(history, primary.SourceTime()-algorithmOffset(algorithm, name))
		if findErr != nil {
			return namedTerminal(contract.ReasonRecordInvalid)
		}
		if found {
			points[name] = record
		}
	}
	var previous *float64
	if record, found := points["previous"]; found {
		value, _, valueErr := numericRecordValue(record, config.ValueField)
		if valueErr != nil {
			return namedTerminal(contract.ReasonRecordInvalid)
		}
		previous = &value
	}
	tenMinute, err := validNumericPoint(points["previous_10m"], config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	twentyFiveMinute, err := validNumericPoint(points["previous_25m"], config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	status, err := evaluateOSRestart(osRestartInput{
		current: current, previous: previous,
		hasTenMinutePoint:        tenMinute,
		hasTwentyFiveMinutePoint: twentyFiveMinute,
	})
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	return namedResult(status, canonical, algorithm.AlgorithmPlanID(), nil)
}

func (procPortDetector) Evaluate(
	_ context.Context,
	algorithm strategy.CompiledAlgorithmPlan,
	_ execution.SeriesEvaluationInputRequest,
	primary execution.RecordView,
) namedAlgorithmResult {
	config, ok := algorithm.ProcPortConfig()
	if !ok {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	raw, found := primary.Value(config.ValueField)
	if !found {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	_, canonical, err := numericRecordValue(primary, config.ValueField)
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	status, err := evaluateProcPort(procPortInput{procExists: raw, dimensions: primary.Dimensions()})
	return namedResult(status, canonical, algorithm.AlgorithmPlanID(), err)
}

// EvaluatePreparedSeriesRecord evaluates one immutable primary record against
// one exact Level-local named-input request per compiled Level.
func (evaluator *Evaluator) EvaluatePreparedSeriesRecord(
	ctx context.Context,
	prepared PreparedPlan,
	inputs []execution.SeriesEvaluationInputRequest,
	record execution.RecordView,
) ([]LevelFact, []ProjectedValue, uint64, error) {
	if evaluator == nil || prepared.bound.execution.Plan == nil || record.RecordID() == "" {
		return nil, nil, 0, errors.New("alarmd detect: prepared plan, evaluator and primary record are required")
	}
	byLevel := make(map[uint32]execution.SeriesEvaluationInputRequest, len(inputs))
	series := execution.SeriesIdentityDigest(record.DimensionIdentityDigest())
	for _, input := range inputs {
		if input.Consumer.Plan.StrategyID != prepared.bound.execution.Plan.PlanRef().StrategyID ||
			!input.Consumer.HasLevel || input.SeriesIdentity != series {
			return nil, nil, 0, errors.New("alarmd detect: named input belongs to a different Plan, Level or series")
		}
		if _, duplicate := byLevel[input.Consumer.LevelID]; duplicate {
			return nil, nil, 0, errors.New("alarmd detect: duplicate named input Level")
		}
		byLevel[input.Consumer.LevelID] = input
	}
	if len(byLevel) != len(prepared.bound.levels) {
		return nil, nil, 0, errors.New("alarmd detect: named inputs do not exactly cover compiled Levels")
	}

	facts := make([]LevelFact, 0, len(prepared.bound.levels))
	projections := make([]ProjectedValue, 0, prepared.bound.projectionCount+prepared.bound.namedInputCount)
	var evaluations uint64
	for _, level := range prepared.bound.levels {
		input, found := byLevel[level.compiled.Definition().LevelID]
		if !found {
			return nil, nil, 0, errors.New("alarmd detect: named inputs do not exactly cover compiled Levels")
		}
		if err := validateCompiledRequirements(level, input); err != nil {
			return nil, nil, 0, err
		}
		fact, count, err := evaluator.evaluateNamedInputLevel(ctx, prepared.bound.execution.Plan, record, input, level, &projections)
		if err != nil {
			return nil, nil, 0, err
		}
		facts = append(facts, fact)
		evaluations += count
	}
	return facts, projections, evaluations, nil
}

func (evaluator *Evaluator) evaluateNamedInputLevel(
	ctx context.Context,
	plan *strategy.CompiledPlan,
	record execution.RecordView,
	input execution.SeriesEvaluationInputRequest,
	level boundLevel,
	projections *[]ProjectedValue,
) (LevelFact, uint64, error) {
	fact := LevelFact{Definition: level.compiled.Definition(), DetectFingerprint: level.compiled.Fingerprints().Detect}
	truths := make([]algorithmTruth, 0, len(level.algorithms))
	var firstUnavailable namedAlgorithmResult
	var evidence ThresholdEvidence
	var fallbackEvidence ThresholdEvidence
	var fallbackEvidenceSet bool
	var evaluations uint64
	standardProjectionEntries := make([]projectionEntry, 0)
	for ordinal, algorithm := range level.algorithms {
		if err := ctx.Err(); err != nil {
			return LevelFact{}, evaluations, err
		}
		var result namedAlgorithmResult
		matchedGroup := -1
		projectionOrdinal := uint32(len(*projections))
		if algorithm.standard != nil {
			_, projected := projectRecordValue(record, *algorithm.standard, &standardProjectionEntries)
			*projections = append(*projections, projected.view)
			if !projected.view.Available {
				result = namedUnavailable(projected.view.ReasonCode)
			} else {
				algorithmFact, err := callDetector(ctx, algorithm.standard.detector, algorithm.standard.spec, projected.value)
				if err != nil {
					return LevelFact{}, evaluations, &InternalError{Operation: "execute detector", PlanID: plan.PlanRef().StrategyID, Err: err}
				}
				if len(algorithmFact.PredicateDigest) != 64 || algorithmFact.MatchedGroup < -1 ||
					(!algorithmFact.Matched && algorithmFact.MatchedGroup != -1) ||
					(algorithmFact.Matched && algorithmFact.MatchedGroup < 0) {
					return LevelFact{}, evaluations, &InternalError{
						Operation: "execute detector", PlanID: plan.PlanRef().StrategyID, Err: errors.New("detector returned an invalid fact"),
					}
				}
				result = namedAlgorithmResult{result: FactResultNormal,
					canonicalDecimal: projected.value.CanonicalDecimal(), predicateDigest: algorithmFact.PredicateDigest}
				if algorithmFact.Matched {
					result.result = FactResultAnomalous
					matchedGroup = algorithmFact.MatchedGroup
				}
			}
		} else {
			result = algorithm.named.Evaluate(ctx, algorithm.compiled, input, record)
			*projections = append(*projections, ProjectedValue{ValueRef: namedValueField(algorithm.compiled),
				NormalizerRef: algorithm.compiled.NormalizedConfigDigest(), CanonicalDecimal: result.canonicalDecimal,
				Available: result.result == FactResultNormal || result.result == FactResultAnomalous, ReasonCode: result.reasonCode})
		}
		currentEvidence := ThresholdEvidence{
			PredicateDigest: result.predicateDigest, ProjectedValueOrdinal: projectionOrdinal, HasProjectedValue: true,
		}
		if result.result == FactResultAnomalous {
			currentEvidence.MatchedAlgorithmOrdinal = uint32(ordinal)
			currentEvidence.HasMatchedAlgorithm = true
			if matchedGroup >= 0 {
				currentEvidence.MatchedGroupOrdinal = uint32(matchedGroup)
				currentEvidence.HasMatchedGroup = true
			}
		}
		switch result.result {
		case FactResultAnomalous:
			truths = append(truths, algorithmTruthTrue)
			evaluations++
			if !fallbackEvidenceSet {
				fallbackEvidence, fallbackEvidenceSet = currentEvidence, true
			}
		case FactResultNormal:
			truths = append(truths, algorithmTruthFalse)
			evaluations++
			if !fallbackEvidenceSet {
				fallbackEvidence, fallbackEvidenceSet = currentEvidence, true
			}
		case FactResultUnavailable, FactResultError:
			truths = append(truths, algorithmTruthUnknown)
			if firstUnavailable.result == "" {
				firstUnavailable = result
			}
		default:
			return LevelFact{}, evaluations, errors.New("alarmd detect: named-input detector returned an invalid result")
		}
		matched, determined := combineAlgorithmTruth(level.compiled.Connector(), truths)
		if determined && ((level.compiled.Connector() == contract.LevelConnectorAND && !matched) ||
			(level.compiled.Connector() == contract.LevelConnectorOR && matched)) {
			evidence = currentEvidence
		}
	}
	matched, determined := combineAlgorithmTruth(level.compiled.Connector(), truths)
	if !determined {
		fact.Result, fact.ReasonCode = firstUnavailable.result, firstUnavailable.reasonCode
		fact.Evidence = ThresholdEvidence{ResultReason: firstUnavailable.reasonCode}
		return fact, evaluations, nil
	}
	if matched {
		fact.Result = FactResultAnomalous
	} else {
		fact.Result = FactResultNormal
	}
	if evidence.PredicateDigest == "" && fallbackEvidenceSet {
		evidence = fallbackEvidence
	}
	fact.Evidence = evidence
	return fact, evaluations, nil
}

func validateCompiledRequirements(level boundLevel, input execution.SeriesEvaluationInputRequest) error {
	expected := make(map[execution.RequirementID]strategy.AlgorithmInputRequirement)
	for _, algorithm := range level.algorithms {
		for _, requirement := range algorithm.compiled.InputRequirements() {
			expected[execution.RequirementID(requirement.RequirementID)] = requirement
		}
	}
	if len(expected) == 0 {
		// Canonical Threshold has no algorithm-private requirements, but the
		// runtime still supplies its frozen PRIMARY DataRequirement.
		return nil
	}
	if len(input.RequirementIDs) != len(expected) || len(input.Inputs) != len(expected) {
		return errors.New("alarmd detect: named input does not cover the compiled requirement exact set")
	}
	seen := make(map[execution.RequirementID]struct{}, len(input.RequirementIDs))
	for _, id := range input.RequirementIDs {
		if _, ok := expected[id]; !ok {
			return errors.New("alarmd detect: named input includes an unknown compiled requirement")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("alarmd detect: named input repeats a compiled requirement")
		}
		seen[id] = struct{}{}
	}
	seenBindings := make(map[execution.RequirementID]struct{}, len(input.Inputs))
	for index, binding := range input.Inputs {
		if binding.Consumer != input.Consumer || binding.RequirementID != input.RequirementIDs[index] {
			return errors.New("alarmd detect: named input binding order or consumer differs from its request")
		}
		requirement, ok := expected[binding.RequirementID]
		if !ok {
			return errors.New("alarmd detect: named input binding includes an unknown compiled requirement")
		}
		if binding.DatasetName != execution.DatasetName(requirement.DatasetName) || binding.Role != execution.InputRole(requirement.Role) {
			return errors.New("alarmd detect: named input binding differs from compiled requirement")
		}
		if _, duplicate := seenBindings[binding.RequirementID]; duplicate {
			return errors.New("alarmd detect: named input repeats a compiled binding")
		}
		seenBindings[binding.RequirementID] = struct{}{}
	}
	return nil
}

func namedBinding(input execution.SeriesEvaluationInputRequest, name execution.DatasetName) (execution.NamedInputBinding, bool) {
	for _, binding := range input.Inputs {
		if binding.DatasetName == name {
			return binding, true
		}
	}
	return execution.NamedInputBinding{}, false
}

func namedInputTrusted(binding execution.NamedInputBinding) bool {
	return binding.Completeness == execution.CompletenessFull && binding.Disposition == execution.AccessAvailable &&
		(binding.DataState == execution.DataStateData || binding.DataState == execution.DataStateEmpty) &&
		binding.Dataset != nil && binding.View != nil && len(binding.QualityFacts) == 0 && len(binding.Terminals) == 0
}

func unavailableFromBinding(binding execution.NamedInputBinding) namedAlgorithmResult {
	if binding.Disposition == execution.AccessTerminal {
		reason := string(binding.ReasonCode)
		if reason == "" {
			reason = contract.ReasonRecordInvalid
		}
		return namedTerminal(reason)
	}
	reason := string(binding.ReasonCode)
	if reason == "" && len(binding.QualityFacts) > 0 {
		reason = string(binding.QualityFacts[0].ReasonCode)
	}
	if reason == "" && len(binding.Terminals) > 0 {
		return namedTerminal(string(binding.Terminals[0].ReasonCode))
	}
	if reason == "" {
		reason = contract.ReasonQueryUnavailable
	}
	return namedUnavailable(reason)
}

func exactPoint(binding execution.NamedInputBinding, sourceTime int64) (execution.RecordView, bool, error) {
	if binding.View == nil {
		return execution.RecordView{}, false, nil
	}
	var result execution.RecordView
	found := false
	for index := 0; index < binding.View.Len(); index++ {
		record, ok := binding.View.Record(index)
		if !ok {
			return execution.RecordView{}, false, errors.New("alarmd detect: named input record is missing")
		}
		if record.SourceTime() != sourceTime {
			continue
		}
		if found {
			return execution.RecordView{}, false, errors.New("alarmd detect: duplicate exact named-input point")
		}
		result, found = record, true
	}
	return result, found, nil
}

func algorithmOffset(algorithm strategy.CompiledAlgorithmPlan, name string) int64 {
	for _, requirement := range algorithm.InputRequirements() {
		for _, point := range requirement.NamedPoints {
			if point.Name == name {
				return point.OffsetSeconds
			}
		}
	}
	return 0
}

func numericRecordValue(record execution.RecordView, field string) (float64, string, error) {
	raw, found := record.Value(field)
	if !found {
		return 0, "", errors.New("value missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return 0, "", err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return 0, "", errors.New("value contains trailing JSON")
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, "", errors.New("value is not numeric")
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, "", errors.New("value is not finite")
	}
	return value, strconv.FormatFloat(value, 'f', 6, 64), nil
}

func configuredPercent(enabled bool, decimal string) (*float64, error) {
	if !enabled {
		return nil, nil
	}
	value, err := strconv.ParseFloat(decimal, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("invalid percentage")
	}
	return &value, nil
}

func validNumericPoint(record execution.RecordView, field string) (bool, error) {
	if record.RecordID() == "" {
		return false, nil
	}
	_, _, err := numericRecordValue(record, field)
	return err == nil, err
}

func namedValueField(algorithm strategy.CompiledAlgorithmPlan) string {
	if config, ok := algorithm.TraditionalComparisonConfig(); ok {
		return config.ValueField
	}
	if config, ok := algorithm.SimpleRingRatioConfig(); ok {
		return config.ValueField
	}
	if config, ok := algorithm.OsRestartConfig(); ok {
		return config.ValueField
	}
	if config, ok := algorithm.ProcPortConfig(); ok {
		return config.ValueField
	}
	return "value"
}

func namedResult(status pureDetectionStatus, canonical string, predicateDigest string, err error) namedAlgorithmResult {
	if err != nil {
		return namedTerminal(contract.ReasonRecordInvalid)
	}
	switch status {
	case pureDetectionAnomalous:
		return namedAlgorithmResult{result: FactResultAnomalous, canonicalDecimal: canonical, predicateDigest: predicateDigest}
	case pureDetectionNormal:
		return namedAlgorithmResult{result: FactResultNormal, canonicalDecimal: canonical, predicateDigest: predicateDigest}
	default:
		return namedUnavailable(contract.ReasonHistoryGapped)
	}
}

func namedUnavailable(reason string) namedAlgorithmResult {
	return namedAlgorithmResult{result: FactResultUnavailable, reasonCode: reason}
}

func namedTerminal(reason string) namedAlgorithmResult {
	return namedAlgorithmResult{result: FactResultError, reasonCode: reason}
}
