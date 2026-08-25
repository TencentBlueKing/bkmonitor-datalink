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
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
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
}

type boundPlan struct {
	execution       PlanExecution
	levels          []boundLevel
	detectorCount   uint64
	projectionCount uint64
}

type boundLevel struct {
	compiled  strategy.CompiledLevel
	detectors []boundDetector
}

type boundDetector struct {
	spec       strategy.DetectorSpec
	detector   Detector
	normalizer strategy.NumericNormalizerSpec
}

func NewEvaluator(registry *Registry) (*Evaluator, error) {
	if registry == nil || len(registry.detectors) == 0 {
		return nil, errors.New("alarmd detect: detector registry is required")
	}
	return &Evaluator{registry: registry}, nil
}

func (evaluator *Evaluator) Evaluate(ctx context.Context, request EvaluateRequest) (DetectionBatch, error) {
	if err := ctx.Err(); err != nil {
		return DetectionBatch{}, err
	}
	if evaluator == nil || evaluator.registry == nil {
		return DetectionBatch{}, &InternalError{Operation: "evaluate", Err: errors.New("detector registry is unavailable")}
	}
	if !request.Limits.valid() {
		return DetectionBatch{}, &InternalError{Operation: "evaluate", Err: errors.New("execution limits are invalid")}
	}
	if request.DatasetContractDigest == "" {
		return DetectionBatch{}, &InternalError{Operation: "evaluate", Err: errors.New("dataset contract digest is empty")}
	}
	if request.Completeness != contract.QueryCompletenessFull && request.Completeness != contract.QueryCompletenessPartial &&
		request.Completeness != contract.QueryCompletenessUnavailable {
		return DetectionBatch{}, &InternalError{Operation: "evaluate", Err: errors.New("query completeness is invalid")}
	}
	if uint64(len(request.Plans)) > request.Limits.MaxPlans {
		return DetectionBatch{}, budgetExceeded("plans", request.Limits.MaxPlans, uint64(len(request.Plans)))
	}

	batch := DetectionBatch{
		Completeness: request.Completeness, ExecutionMode: ExecutionModeStandard,
		DetectionCoverage: DetectionCoverageFull,
	}
	if request.Completeness == contract.QueryCompletenessUnavailable {
		batch.Counts.SkippedPlans = uint64(len(request.Plans))
		for _, execution := range request.Plans {
			selected := uint64(execution.View.SelectedCount())
			if next, ok := checkedAdd(batch.Counts.SkippedSelectedRecords, selected); ok {
				batch.Counts.SkippedSelectedRecords = next
			} else {
				return DetectionBatch{}, &InternalError{Operation: "count unavailable records", Err: errors.New("count overflow")}
			}
		}
		return batch, nil
	}

	plans := append([]PlanExecution(nil), request.Plans...)
	sort.Slice(plans, func(left, right int) bool { return plans[left].View.PlanID() < plans[right].View.PlanID() })
	boundPlans := make([]boundPlan, len(plans))
	for index := range plans {
		if index > 0 && plans[index-1].View.PlanID() == plans[index].View.PlanID() {
			return DetectionBatch{}, &InternalError{Operation: "bind plans", PlanID: plans[index].View.PlanID(), Err: errors.New("duplicate plan ID")}
		}
		bound, err := evaluator.bindPlan(plans[index], request.DatasetContractDigest)
		if err != nil {
			return DetectionBatch{}, err
		}
		boundPlans[index] = bound
	}

	for _, execution := range boundPlans {
		series, counts, err := evaluator.evaluatePlan(ctx, execution, request.Limits, batch.Counts)
		if err != nil {
			return DetectionBatch{}, err
		}
		batch.Series = append(batch.Series, series...)
		batch.Counts = counts
	}
	return batch, nil
}

func (evaluator *Evaluator) bindPlan(execution PlanExecution, datasetDigest string) (boundPlan, error) {
	planID := execution.View.PlanID()
	if planID == "" || execution.Plan == nil {
		return boundPlan{}, &InternalError{Operation: "bind plan", PlanID: planID, Err: errors.New("plan view or compiled plan is empty")}
	}
	if execution.Plan.PlanRef().StrategyID != planID {
		return boundPlan{}, &InternalError{Operation: "bind plan", PlanID: planID, Err: errors.New("plan ID mismatch")}
	}
	if execution.Plan.DatasetContractDigest() != datasetDigest {
		return boundPlan{}, &InternalError{Operation: "bind plan", PlanID: planID, Err: errors.New("dataset contract digest mismatch")}
	}
	if execution.Plan.EvaluationSemantics().EvaluationScope != contract.EvaluationScopeSeries {
		return boundPlan{}, &InternalError{Operation: "bind plan", PlanID: planID, Err: errors.New("evaluation scope is not SERIES")}
	}
	levels := execution.Plan.Levels()
	result := boundPlan{execution: execution, levels: make([]boundLevel, len(levels))}
	projectionKeys := make(map[projectionKey]struct{})
	for levelIndex, level := range levels {
		if levelIndex > 0 && levels[levelIndex-1].Definition().LevelID >= level.Definition().LevelID {
			return boundPlan{}, &InternalError{Operation: "bind plan", PlanID: planID, Err: errors.New("compiled levels are not ordered and unique")}
		}
		detectorSpecs := level.Detectors()
		result.levels[levelIndex] = boundLevel{compiled: level, detectors: make([]boundDetector, len(detectorSpecs))}
		for detectorIndex, spec := range detectorSpecs {
			detector, ok := evaluator.registry.resolve(DetectorKey{Kind: spec.Kind(), Version: spec.Version()})
			if !ok {
				return boundPlan{}, &InternalError{Operation: "bind detector", PlanID: planID, Err: fmt.Errorf("detector %s@%d is unavailable", spec.Kind(), spec.Version())}
			}
			normalizer, ok := execution.Plan.Normalizer(spec.NormalizerRef())
			if !ok {
				return boundPlan{}, &InternalError{Operation: "bind detector", PlanID: planID, Err: errors.New("compiled normalizer is unavailable")}
			}
			result.levels[levelIndex].detectors[detectorIndex] = boundDetector{spec: spec, detector: detector, normalizer: normalizer}
			result.detectorCount++
			projectionKeys[projectionKey{valueRef: spec.ValueRef(), normalizerRef: spec.NormalizerRef()}] = struct{}{}
		}
	}
	result.projectionCount = uint64(len(projectionKeys))
	return result, nil
}

func (evaluator *Evaluator) evaluatePlan(
	ctx context.Context,
	execution boundPlan,
	limits ExecutionLimits,
	counts DetectionCounts,
) ([]SeriesDetection, DetectionCounts, error) {
	planID := execution.execution.View.PlanID()
	selected := uint64(execution.execution.View.SelectedCount())
	if selected > limits.MaxSelectedRecordsPerPlan {
		return nil, counts, budgetExceeded("selected_records_per_plan", limits.MaxSelectedRecordsPerPlan, selected)
	}
	if err := preflightTotals(selected, uint64(len(execution.levels)), execution.detectorCount, counts, limits); err != nil {
		return nil, counts, err
	}

	records, err := collectSelectedRecords(ctx, execution.execution.View)
	if err != nil {
		return nil, counts, err
	}
	groups := groupSelectedRecords(records)
	if uint64(len(groups)) > limits.MaxSeriesPerPlan {
		return nil, counts, budgetExceeded("series_per_plan", limits.MaxSeriesPerPlan, uint64(len(groups)))
	}
	for _, group := range groups {
		if uint64(len(group.records)) > limits.MaxRecordsPerSeries {
			return nil, counts, budgetExceeded("records_per_series", limits.MaxRecordsPerSeries, uint64(len(group.records)))
		}
	}

	estimatedBytes, ok := estimatePlanResultBytes(uint64(len(groups)), uint64(len(records)), uint64(len(execution.levels)), execution.projectionCount)
	if !ok {
		return nil, counts, &InternalError{Operation: "estimate result", PlanID: planID, Err: errors.New("result byte estimate overflow")}
	}
	totalEstimated, ok := checkedAdd(counts.EstimatedResultBytes, estimatedBytes)
	if !ok {
		return nil, counts, &InternalError{Operation: "estimate result", PlanID: planID, Err: errors.New("result byte estimate overflow")}
	}
	if totalEstimated > limits.MaxResultBytes {
		return nil, counts, budgetExceeded("result_bytes", limits.MaxResultBytes, totalEstimated)
	}

	result := make([]SeriesDetection, 0, len(groups))
	for _, group := range groups {
		series := SeriesDetection{
			PlanRef: execution.execution.Plan.PlanRef(), DimensionIdentityDigest: group.dimensionIdentityDigest,
			Records: make([]RecordDetection, 0, len(group.records)),
		}
		for _, record := range group.records {
			if err := ctx.Err(); err != nil {
				return nil, counts, err
			}
			detection, factCounts, predicateCount, err := evaluator.evaluateRecord(ctx, execution, record)
			if err != nil {
				return nil, counts, err
			}
			series.Records = append(series.Records, detection)
			counts.AnomalousFacts += factCounts.AnomalousFacts
			counts.NormalFacts += factCounts.NormalFacts
			counts.UnavailableFacts += factCounts.UnavailableFacts
			counts.ErrorFacts += factCounts.ErrorFacts
			counts.PredicateEvaluations += predicateCount
		}
		result = append(result, series)
	}
	counts.Plans++
	counts.CompiledLevels += uint64(len(execution.levels))
	counts.SelectedRecords += selected
	counts.EvaluatedRecords += uint64(len(records))
	counts.Series += uint64(len(groups))
	facts, _ := checkedMul(uint64(len(records)), uint64(len(execution.levels)))
	counts.LevelFacts += facts
	counts.EstimatedResultBytes = totalEstimated
	return result, counts, nil
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

func (evaluator *Evaluator) evaluateRecord(
	ctx context.Context,
	plan boundPlan,
	record selectedRecord,
) (RecordDetection, DetectionCounts, uint64, error) {
	result := RecordDetection{
		RecordOrdinal: record.ordinal, RecordID: record.recordID, SourceTime: record.sourceTime,
		LevelFacts: make([]LevelFact, 0, len(plan.levels)),
	}
	projectionEntries := make([]projectionEntry, 0, plan.projectionCount)
	counts := DetectionCounts{}
	predicateEvaluations := uint64(0)
	for _, level := range plan.levels {
		fact, evaluations, err := evaluator.evaluateLevel(ctx, plan.execution.Plan, record.view, level, &projectionEntries)
		if err != nil {
			return RecordDetection{}, DetectionCounts{}, 0, err
		}
		predicateEvaluations += evaluations
		result.LevelFacts = append(result.LevelFacts, fact)
		switch fact.Result {
		case FactResultAnomalous:
			counts.AnomalousFacts++
		case FactResultNormal:
			counts.NormalFacts++
		case FactResultUnavailable:
			counts.UnavailableFacts++
		case FactResultError:
			counts.ErrorFacts++
		default:
			return RecordDetection{}, DetectionCounts{}, 0, &InternalError{Operation: "evaluate level", PlanID: plan.execution.Plan.PlanRef().StrategyID, Err: errors.New("unknown fact result")}
		}
	}
	result.ProjectedValues = make([]ProjectedValue, len(projectionEntries))
	for index := range projectionEntries {
		result.ProjectedValues[index] = projectionEntries[index].view
	}
	return result, counts, predicateEvaluations, nil
}

func (evaluator *Evaluator) evaluateLevel(
	ctx context.Context,
	plan *strategy.CompiledPlan,
	record inputv2.RecordView,
	level boundLevel,
	projections *[]projectionEntry,
) (LevelFact, uint64, error) {
	fact := LevelFact{
		Definition: level.compiled.Definition(), DetectFingerprint: level.compiled.Fingerprints().Detect,
	}
	matched := level.compiled.Connector() == contract.LevelConnectorAND
	evidenceSet := false
	var evidenceFact AlgorithmFact
	var evidenceAlgorithm uint32
	var evidenceProjection uint32
	predicateEvaluations := uint64(0)
	for algorithmIndex, bound := range level.detectors {
		projectionOrdinal, projection := projectRecordValue(record, bound, projections)
		if !projection.view.Available {
			fact.Result = FactResultUnavailable
			fact.ReasonCode = projection.view.ReasonCode
			fact.Evidence = ThresholdEvidence{
				ProjectedValueOrdinal: projectionOrdinal, HasProjectedValue: true, ResultReason: projection.view.ReasonCode,
			}
			return fact, predicateEvaluations, nil
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
			fact.Result = FactResultError
			fact.ReasonCode = controlled.ReasonCode
			fact.Evidence = ThresholdEvidence{
				ProjectedValueOrdinal: projectionOrdinal, HasProjectedValue: true, ResultReason: controlled.ReasonCode,
			}
			return fact, predicateEvaluations, nil
		}
		if len(algorithmFact.PredicateDigest) != 64 || algorithmFact.MatchedGroup < -1 ||
			(!algorithmFact.Matched && algorithmFact.MatchedGroup != -1) ||
			(bound.spec.Kind() == strategy.DetectorKindThreshold && algorithmFact.Matched && algorithmFact.MatchedGroup < 0) {
			return LevelFact{}, predicateEvaluations, &InternalError{
				Operation: "execute detector", PlanID: plan.PlanRef().StrategyID, Err: errors.New("detector returned an invalid fact"),
			}
		}
		predicateEvaluations++
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
	if matched {
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

func projectRecordValue(
	record inputv2.RecordView,
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

func preflightTotals(selected, levels, detectors uint64, counts DetectionCounts, limits ExecutionLimits) error {
	facts, ok := checkedMul(selected, levels)
	if !ok {
		return &InternalError{Operation: "estimate facts", Err: errors.New("fact count overflow")}
	}
	totalFacts, ok := checkedAdd(counts.LevelFacts, facts)
	if !ok {
		return &InternalError{Operation: "estimate facts", Err: errors.New("fact count overflow")}
	}
	if totalFacts > limits.MaxLevelFacts {
		return budgetExceeded("level_facts", limits.MaxLevelFacts, totalFacts)
	}
	evaluations, ok := checkedMul(selected, detectors)
	if !ok {
		return &InternalError{Operation: "estimate predicate evaluations", Err: errors.New("predicate count overflow")}
	}
	totalEvaluations, ok := checkedAdd(counts.PredicateEvaluations, evaluations)
	if !ok {
		return &InternalError{Operation: "estimate predicate evaluations", Err: errors.New("predicate count overflow")}
	}
	if totalEvaluations > limits.MaxPredicateEvaluations {
		return budgetExceeded("predicate_evaluations", limits.MaxPredicateEvaluations, totalEvaluations)
	}
	return nil
}

func estimatePlanResultBytes(series, records, levels, projections uint64) (uint64, bool) {
	seriesBytes, ok := checkedMul(series, estimatedSeriesBytes)
	if !ok {
		return 0, false
	}
	recordBytes, ok := checkedMul(records, estimatedRecordBytes)
	if !ok {
		return 0, false
	}
	projectedPerRecord, ok := checkedMul(projections, estimatedProjectedBytes)
	if !ok {
		return 0, false
	}
	projectionBytes, ok := checkedMul(records, projectedPerRecord)
	if !ok {
		return 0, false
	}
	factsPerRecord, ok := checkedMul(levels, estimatedLevelFactBytes)
	if !ok {
		return 0, false
	}
	factBytes, ok := checkedMul(records, factsPerRecord)
	if !ok {
		return 0, false
	}
	result, ok := checkedAdd(seriesBytes, recordBytes)
	if !ok {
		return 0, false
	}
	result, ok = checkedAdd(result, projectionBytes)
	if !ok {
		return 0, false
	}
	return checkedAdd(result, factBytes)
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

func budgetExceeded(name string, limit, actual uint64) error {
	return &BudgetError{Budget: name, Limit: limit, Actual: actual}
}
