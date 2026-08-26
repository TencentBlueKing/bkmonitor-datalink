// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

type RuntimeStateLoader interface {
	LoadWindows(context.Context, state.LoadWindowsRequest) (state.LoadWindowsResult, error)
}

type StateCodecAdmitter interface {
	AdmitWindow(*state.Window) (int, error)
}

type PipelineOptions struct {
	Compiler       *strategy.PlanCompiler
	Detector       DetectionEvaluator
	EffectiveTime  strategy.EffectiveTimeProvider
	State          RuntimeStateLoader
	StateCodec     StateCodecAdmitter
	StateSemantics strategy.StateSemantics
	DetectLimits   detect.ExecutionLimits
	TriggerLimits  trigger.EvaluationLimitsV2
}

type EvaluationPipeline struct {
	options PipelineOptions
}

type MessageResult struct {
	CriticalResult
	Receipt *contract.MessageReceiptV1
}

type compiledExecution struct {
	view inputv2.PlanView
	plan *strategy.CompiledPlan
}

type seriesExecution struct {
	detection detect.SeriesDetection
	compiled  compiledExecution
	loaded    state.LoadedWindow
}

func NewEvaluationPipeline(options PipelineOptions) (*EvaluationPipeline, error) {
	if options.Compiler == nil || options.Detector == nil || options.EffectiveTime == nil || options.State == nil || options.StateCodec == nil {
		return nil, errors.New("alarmd coordinator: compiler, detector, EffectiveTime, state and codec are required")
	}
	return &EvaluationPipeline{options: options}, nil
}

// Evaluate runs the phase-one FULL path through M2's immutable input, M3,
// M5, M4 overlay and M6. It returns side effects without publishing them.
func (pipeline *EvaluationPipeline) Evaluate(ctx context.Context, input *inputv2.EvaluationInput) (CriticalResult, error) {
	result, err := pipeline.EvaluateMessage(ctx, input)
	return result.CriticalResult, err
}

func (pipeline *EvaluationPipeline) EvaluateMessage(ctx context.Context, input *inputv2.EvaluationInput) (MessageResult, error) {
	if pipeline == nil || input == nil {
		return MessageResult{}, errors.New("alarmd coordinator: initialized pipeline and input are required")
	}
	if err := ctx.Err(); err != nil {
		return MessageResult{}, err
	}
	if input.ProcessingRoute() != inputv2.RouteFullPipeline {
		return MessageResult{}, fmt.Errorf("alarmd coordinator: G1 pipeline requires FULL input, got %s", input.ProcessingRoute())
	}

	compiled, executions, compileTerminals, err := pipeline.compilePlans(ctx, input)
	if err != nil {
		return MessageResult{}, err
	}
	terminals := append(input.Terminals().Items(), compileTerminals...)
	detected := isolatedDetection{Batch: detect.DetectionBatch{
		Completeness: input.Execution().Completeness, ExecutionMode: detect.ExecutionModeStandard,
		DetectionCoverage: detect.DetectionCoverageFull,
	}}
	if len(executions) > 0 {
		detected, err = evaluateDetectWithIsolation(ctx, pipeline.options.Detector, detect.EvaluateRequest{
			Completeness: input.Execution().Completeness, DatasetContractDigest: compiledDatasetDigest(compiled),
			Plans: executions, Limits: pipeline.options.DetectLimits,
		})
		if err != nil {
			return MessageResult{}, err
		}
	}
	terminals = append(terminals, detected.Terminals...)
	series, err := pipeline.loadSeries(ctx, input, detected.Batch, compiled)
	if err != nil {
		return MessageResult{}, err
	}

	critical := CriticalResult{Events: make([]contract.TriggerEventV1, 0), StateWrite: state.WriteWindowsRequest{Items: make([]state.LoadedWindow, len(series))}}
	evaluations := make(map[string][]recordEvaluation, len(compiled))
	for index := range series {
		events, results, err := pipeline.evaluateSeries(ctx, input, &series[index])
		if err != nil {
			return MessageResult{}, err
		}
		critical.Events = append(critical.Events, events...)
		planID := series[index].compiled.plan.PlanRef().StrategyID
		evaluations[planID] = append(evaluations[planID], results...)
		if _, err := pipeline.options.StateCodec.AdmitWindow(series[index].loaded.Window); err != nil {
			return MessageResult{}, fmt.Errorf("alarmd coordinator: admit runtime state: %w", err)
		}
		critical.StateWrite.Items[index] = series[index].loaded
	}
	receipt, err := buildMessageReceipt(input, evaluations, terminals)
	if err != nil {
		return MessageResult{}, err
	}
	return MessageResult{CriticalResult: critical, Receipt: receipt}, nil
}

func (pipeline *EvaluationPipeline) compilePlans(
	ctx context.Context,
	input *inputv2.EvaluationInput,
) (map[string]compiledExecution, []detect.PlanExecution, []inputv2.Terminal, error) {
	views := input.PlanViews()
	compiled := make(map[string]compiledExecution, len(views))
	executions := make([]detect.PlanExecution, 0, len(views))
	terminals := make([]inputv2.Terminal, 0)
	for _, view := range views {
		result, err := pipeline.options.Compiler.Compile(ctx, strategy.CompileRequest{
			Plan: view.Snapshot(), DatasetContract: input.DatasetContract().Snapshot(), StateSemantics: pipeline.options.StateSemantics,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		plan, ok := result.Plan()
		if terminal := result.PlanTerminal(); terminal != nil {
			terminals = append(terminals, inputv2.Terminal{
				Scope: inputv2.ScopePlan, PlanID: view.PlanID(), ReasonCode: terminal.ReasonCode, FieldPath: terminal.FieldPath,
			})
			continue
		}
		for _, terminal := range result.LevelTerminals() {
			levelID := terminal.LevelID
			terminals = append(terminals, inputv2.Terminal{
				Scope: inputv2.ScopeLevel, PlanID: view.PlanID(), LevelID: &levelID,
				ReasonCode: terminal.ReasonCode, FieldPath: terminal.FieldPath,
			})
		}
		if !ok || len(plan.Levels()) == 0 {
			continue
		}
		if _, duplicate := compiled[view.PlanID()]; duplicate {
			return nil, nil, nil, fmt.Errorf("alarmd coordinator: duplicate compiled plan %s", view.PlanID())
		}
		entry := compiledExecution{view: view, plan: plan}
		compiled[view.PlanID()] = entry
		executions = append(executions, detect.PlanExecution{View: view, Plan: plan})
	}
	return compiled, executions, terminals, nil
}

func compiledDatasetDigest(plans map[string]compiledExecution) string {
	for _, plan := range plans {
		return plan.plan.DatasetContractDigest()
	}
	return ""
}

func (pipeline *EvaluationPipeline) loadSeries(
	ctx context.Context,
	input *inputv2.EvaluationInput,
	detection detect.DetectionBatch,
	compiled map[string]compiledExecution,
) ([]seriesExecution, error) {
	specs := make([]state.LoadWindowSpec, len(detection.Series))
	series := make([]seriesExecution, len(detection.Series))
	batch := input.RecordBatch()
	for index, detected := range detection.Series {
		entry, ok := compiled[detected.PlanRef.StrategyID]
		if !ok || entry.plan.StateCompatibilityHash() != detected.PlanRef.StateCompatibilityHash || len(detected.Records) == 0 {
			return nil, errors.New("alarmd coordinator: Detection series is not aligned with a compiled Plan")
		}
		record, ok := batch.Record(int(detected.Records[0].RecordOrdinal))
		if !ok || record.DimensionIdentityDigest() != detected.DimensionIdentityDigest {
			return nil, errors.New("alarmd coordinator: Detection series is not aligned with its source record")
		}
		identity := state.RuntimeIdentity{
			TenantID: input.Execution().TenantID, BusinessID: record.BusinessID(), StrategyID: detected.PlanRef.StrategyID,
			StateCompatibilityHash: detected.PlanRef.StateCompatibilityHash, DimensionIdentityDigest: detected.DimensionIdentityDigest,
		}
		specs[index] = state.LoadWindowSpec{Identity: identity, Requirements: stateRequirements(entry.plan)}
		series[index] = seriesExecution{detection: detected, compiled: entry}
	}
	loaded, err := pipeline.options.State.LoadWindows(ctx, state.LoadWindowsRequest{Items: specs})
	if err != nil {
		return nil, err
	}
	if len(loaded.Items) != len(series) {
		return nil, errors.New("alarmd coordinator: runtime state load returned incomplete results")
	}
	for index, item := range loaded.Items {
		if item.Window == nil || (item.Status != state.LoadFound && item.Status != state.LoadMissing && item.Status != state.LoadResetCorrupt) {
			return nil, fmt.Errorf("alarmd coordinator: runtime state load %d incomplete: status=%s: %w", index, item.Status, item.Err)
		}
		series[index].loaded = item
	}
	return series, nil
}

func stateRequirements(plan *strategy.CompiledPlan) []state.LevelRequirement {
	semantics := plan.EvaluationSemantics()
	result := make([]state.LevelRequirement, 0, len(plan.Levels()))
	for _, level := range plan.Levels() {
		requirement := level.StateRequirement()
		result = append(result, state.LevelRequirement{
			LevelID: level.Definition().LevelID, DetectFingerprint: level.Fingerprints().Detect,
			RequiredPoints: requirement.RequiredDetectHistoryPoints, RetentionPoints: requirement.RetentionPoints,
			EvaluationInterval: time.Duration(semantics.EvaluationInterval) * time.Second,
			LatenessTolerance:  time.Duration(semantics.LatenessTolerance) * time.Second,
		})
	}
	return result
}

func (pipeline *EvaluationPipeline) evaluateSeries(
	ctx context.Context,
	input *inputv2.EvaluationInput,
	series *seriesExecution,
) ([]contract.TriggerEventV1, []recordEvaluation, error) {
	events := make([]contract.TriggerEventV1, 0)
	results := make([]recordEvaluation, 0, len(series.detection.Records))
	levels := series.compiled.plan.Levels()
	batch := input.RecordBatch()
	for _, detected := range series.detection.Records {
		record, ok := batch.Record(int(detected.RecordOrdinal))
		if !ok || record.RecordID() != detected.RecordID || record.SourceTime() != detected.SourceTime ||
			record.DimensionIdentityDigest() != series.detection.DimensionIdentityDigest {
			return nil, nil, errors.New("alarmd coordinator: Detection record is not aligned with its source record")
		}
		facts, err := pipeline.resolveEffectiveTime(ctx, input.Execution(), record.BusinessID(), levels)
		if err != nil {
			return nil, nil, err
		}
		detectionRecord := mapDetectionRecord(detected)
		point := state.StatePoint{RecordID: detected.RecordID, SourceTime: detected.SourceTime}
		for levelIndex, level := range levels {
			eligibility, err := trigger.EvaluateStateEligibilityV2(
				input.Execution().EvaluationTime, level, detectionRecord.LevelFacts[levelIndex], facts[levelIndex].Fact,
			)
			if err != nil {
				return nil, nil, err
			}
			if eligibility.StateDisposition() == trigger.StateAdvance {
				stateFact, err := mapStateFact(detectionRecord.LevelFacts[levelIndex])
				if err != nil {
					return nil, nil, err
				}
				point.Levels = append(point.Levels, stateFact)
			}
		}
		lateAccepted := false
		if len(point.Levels) > 0 {
			applied, err := series.loaded.Window.ApplyContext(ctx, []state.StatePoint{point})
			if err != nil {
				return nil, nil, err
			}
			if len(applied) != 1 || (applied[0].Status != state.PointApplied && applied[0].Status != state.PointNoop) {
				return nil, nil, fmt.Errorf("alarmd coordinator: runtime point is not evaluable: status=%s reason=%s", applied[0].Status, applied[0].ReasonCode)
			}
			lateAccepted = applied[0].Late
		}
		histories := make([]trigger.LevelHistory, len(levels))
		for index, level := range levels {
			history, ok := series.loaded.Window.History(level.Definition().LevelID)
			if !ok {
				return nil, nil, errors.New("alarmd coordinator: runtime history is missing a compiled Level")
			}
			histories[index] = trigger.LevelHistory{LevelID: level.Definition().LevelID, View: triggerHistoryView{view: history}}
		}
		result, err := trigger.EvaluateV2(trigger.EvaluationRequestV2{
			TenantID: input.Execution().TenantID, BusinessID: record.BusinessID(), Plan: series.compiled.plan,
			Record: detectionRecord,
			RecordRef: contract.TriggerRecordRefV1{
				RecordID: record.RecordID(), SourceTime: record.SourceTime(), DimensionIdentityDigest: record.DimensionIdentityDigest(),
				Dimensions: record.Dimensions(),
			},
			Observed:  contract.TriggerObservedV1{Values: observedValues(record, series.compiled.plan.Projection()), Unit: series.compiled.plan.Projection().DataUnit},
			Histories: histories, EffectiveTimeFacts: facts, EvaluationTime: input.Execution().EvaluationTime,
			ExecutionID: input.Execution().ExecutionID, LateAccepted: lateAccepted, Limits: pipeline.options.TriggerLimits,
		})
		if err != nil {
			return nil, nil, err
		}
		results = append(results, recordEvaluation{RecordOrdinal: detected.RecordOrdinal, Result: result})
		if result.TriggerEvent != nil {
			events = append(events, *result.TriggerEvent)
		}
	}
	return events, results, nil
}

func (pipeline *EvaluationPipeline) resolveEffectiveTime(
	ctx context.Context,
	execution inputv2.ExecutionMetadata,
	businessID string,
	levels []strategy.CompiledLevel,
) ([]trigger.LevelEffectiveTimeFact, error) {
	requests := make([]strategy.EffectiveTimeRequest, len(levels))
	for index, level := range levels {
		requests[index] = strategy.EffectiveTimeRequest{
			TenantID: execution.TenantID, BusinessID: businessID, EvaluationTime: execution.EvaluationTime,
			Requirement: level.EffectiveTimeRequirement(),
		}
	}
	facts, err := pipeline.options.EffectiveTime.Resolve(ctx, requests)
	if err != nil {
		return nil, err
	}
	if len(facts) != len(levels) {
		return nil, errors.New("alarmd coordinator: EffectiveTime Provider returned incomplete results")
	}
	result := make([]trigger.LevelEffectiveTimeFact, len(levels))
	for index, level := range levels {
		result[index] = trigger.LevelEffectiveTimeFact{LevelID: level.Definition().LevelID, Fact: facts[index]}
	}
	return result, nil
}

func mapDetectionRecord(record detect.RecordDetection) trigger.DetectionRecord {
	result := trigger.DetectionRecord{
		RecordID: record.RecordID, SourceTime: record.SourceTime,
		ProjectedValues: make([]trigger.ProjectedValue, len(record.ProjectedValues)),
		LevelFacts:      make([]trigger.DetectionFact, len(record.LevelFacts)),
	}
	for index, projected := range record.ProjectedValues {
		result.ProjectedValues[index] = trigger.ProjectedValue{
			CanonicalDecimal: projected.CanonicalDecimal, Available: projected.Available, ReasonCode: projected.ReasonCode,
		}
	}
	for index, fact := range record.LevelFacts {
		evidence := trigger.DetectionEvidence{
			PredicateDigest: fact.Evidence.PredicateDigest, ResultReason: fact.Evidence.ResultReason,
		}
		if fact.Evidence.HasProjectedValue {
			evidence.ProjectedValueOrdinal = uint32Pointer(fact.Evidence.ProjectedValueOrdinal)
		}
		if fact.Evidence.HasMatchedAlgorithm {
			evidence.MatchedAlgorithmOrdinal = uint32Pointer(fact.Evidence.MatchedAlgorithmOrdinal)
		}
		if fact.Evidence.HasMatchedGroup {
			evidence.MatchedGroupOrdinal = uint32Pointer(fact.Evidence.MatchedGroupOrdinal)
		}
		result.LevelFacts[index] = trigger.DetectionFact{
			Definition: fact.Definition, DetectFingerprint: fact.DetectFingerprint, Result: fact.Result,
			ReasonCode: fact.ReasonCode, Evidence: evidence,
		}
	}
	return result
}

func mapStateFact(fact trigger.DetectionFact) (state.PointLevelFact, error) {
	result := state.PointLevelFact{LevelID: fact.Definition.LevelID, DetectFingerprint: fact.DetectFingerprint}
	switch fact.Result {
	case trigger.DetectionAnomalous:
		result.Result = state.LevelFactAnomalous
	case trigger.DetectionNormal:
		result.Result = state.LevelFactNormal
	default:
		return state.PointLevelFact{}, errors.New("alarmd coordinator: ADVANCE requires an available Detection fact")
	}
	return result, nil
}

func observedValues(record inputv2.RecordView, projection contract.InputProjectionV2) map[string]json.RawMessage {
	values := make(map[string]json.RawMessage, len(projection.ValueFields))
	for _, name := range projection.ValueFields {
		if value, ok := record.Value(name); ok {
			values[name] = value
		}
	}
	return values
}

func uint32Pointer(value uint32) *uint32 { return &value }

type triggerHistoryView struct {
	view state.HistoryView
}

func (view triggerHistoryView) Summarize(endTime int64, requiredPositions uint32) trigger.HistorySummary {
	summary := view.view.Summarize(endTime, requiredPositions)
	return trigger.HistorySummary{
		Completeness: string(summary.Completeness), WindowStart: summary.WindowStart, WindowEnd: summary.WindowEnd,
		ValidPositions: summary.ValidPositions, AnomalyCount: summary.AnomalyCount, AnomalyDigest: summary.AnomalyDigest,
	}
}

func (view triggerHistoryView) CountAnomalies(fromTime, untilTime int64) uint32 {
	return view.view.CountAnomalies(fromTime, untilTime)
}
