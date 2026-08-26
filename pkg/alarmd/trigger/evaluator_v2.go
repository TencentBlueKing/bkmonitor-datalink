// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package trigger

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const decisionWindowTypeV1 = "N_OF_M_WITH_CONTINUOUS_MISS"

func EvaluateV2(request EvaluationRequestV2) (EvaluationResultV2, error) {
	levels, err := validateRequestV2(request)
	if err != nil {
		return EvaluationResultV2{}, err
	}
	result := EvaluationResultV2{
		LevelOutcomes: make([]LevelOutcomeV2, 0, len(levels)),
		Counts:        EvaluationCountsV2{Levels: uint64(len(levels))},
	}
	levelResults := make([]contract.LevelResultV1, 0, len(levels))
	for index, level := range levels {
		eligibility, err := EvaluateStateEligibilityV2(
			request.EvaluationTime, level, request.Record.LevelFacts[index], request.EffectiveTimeFacts[index].Fact,
		)
		if err != nil {
			return EvaluationResultV2{}, err
		}
		outcome, successful, err := evaluateLevelV2(
			request, level, request.Record.LevelFacts[index], request.Histories[index].View,
			request.EffectiveTimeFacts[index].Fact, eligibility,
		)
		if err != nil {
			return EvaluationResultV2{}, err
		}
		if outcome.StateDisposition != eligibility.StateDisposition() {
			return EvaluationResultV2{}, invariantV2(
				"assert state eligibility", outcome.LevelID, errors.New("Level outcome changed pre-history disposition"),
			)
		}
		result.LevelOutcomes = append(result.LevelOutcomes, outcome)
		switch {
		case outcome.Result != "":
			result.Counts.Evaluated++
			switch outcome.Result {
			case contract.LevelResultAbnormal:
				result.Counts.Abnormal++
			case contract.LevelResultNormal:
				result.Counts.Normal++
			case contract.LevelResultRecovery:
				result.Counts.Recovery++
			}
			levelResults = append(levelResults, successful)
		case outcome.SuppressedReason != "":
			result.Counts.Suppressed++
		case outcome.UnavailableReason != "":
			result.Counts.Unavailable++
		default:
			return EvaluationResultV2{}, invariantV2("classify Level outcome", outcome.LevelID, errors.New("empty outcome"))
		}
	}

	switch {
	case len(levelResults) > 0:
		result.Completion = CompletionEvaluated
		result.RecordResult = aggregateRecordResultV2(levelResults)
	case result.Counts.Unavailable > 0:
		result.Completion = CompletionUnavailable
	default:
		result.Completion = CompletionSuppressed
	}
	if result.RecordResult == contract.LevelResultAbnormal || result.RecordResult == contract.LevelResultRecovery {
		if uint32(len(levelResults)) > request.Limits.MaxLevelResultsPerEvent {
			return EvaluationResultV2{}, invariantV2("admit event Level results", 0, errors.New("compiled result exceeds admitted limit"))
		}
		fingerprints := request.Plan.Fingerprints()
		event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
			EventKind: result.RecordResult, TenantID: request.TenantID, BusinessID: request.BusinessID,
			PlanRef: request.Plan.PlanRef(), RecordRef: request.RecordRef, Observed: request.Observed,
			LevelResults: levelResults, EvaluationTime: request.EvaluationTime,
			DetectPlanFingerprint: fingerprints.Detect, TriggerStateFingerprint: fingerprints.Trigger,
			ExecutionID: request.ExecutionID, MaxEvidenceBytes: request.Limits.MaxEvidenceBytesPerEvent,
		})
		if err != nil {
			return EvaluationResultV2{}, invariantV2("build TriggerEvent", 0, err)
		}
		result.TriggerEvent = event
		result.Counts.Events = 1
	}
	return result, nil
}

// EvaluateStateEligibilityV2 validates the current Level facts and decides if
// M4 history may advance before M6 reads any HistoryView. History completeness
// is intentionally not part of this decision.
func EvaluateStateEligibilityV2(
	evaluationTime int64,
	level strategy.CompiledLevel,
	fact DetectionFact,
	effective strategy.EffectiveTimeFact,
) (StateEligibilityV2, error) {
	definition := level.Definition()
	if fact.Definition != definition || fact.DetectFingerprint != level.Fingerprints().Detect {
		return StateEligibilityV2{}, invariantV2(
			"validate Detect fact", definition.LevelID, errors.New("definition or fingerprint mismatch"),
		)
	}
	if err := validateEffectiveTimeFactV2(
		evaluationTime, level.EffectiveTimeRequirementDigest(), effective,
	); err != nil {
		return StateEligibilityV2{}, invariantV2("validate EffectiveTime fact", definition.LevelID, err)
	}
	effectiveDisposition := ""
	switch effective.Status() {
	case strategy.EffectiveTimeActive, strategy.EffectiveTimeInactive:
		effectiveDisposition = StateAdvance
	case strategy.EffectiveTimeUnknown:
		effectiveDisposition = StateFreeze
	default:
		return StateEligibilityV2{}, invariantV2(
			"validate EffectiveTime fact", definition.LevelID, errors.New("unknown status"),
		)
	}

	switch fact.Result {
	case DetectionUnavailable, DetectionError:
		if fact.ReasonCode == "" ||
			!contract.ReasonAllowedForV2(fact.ReasonCode, contract.ReasonDomainReceipt|contract.ReasonDomainObservation) {
			return StateEligibilityV2{}, invariantV2(
				"validate unavailable Detect fact", definition.LevelID, errors.New("invalid fact result or reason"),
			)
		}
		return StateEligibilityV2{stateDisposition: StateFreeze}, nil
	case DetectionAnomalous, DetectionNormal:
	default:
		return StateEligibilityV2{}, invariantV2(
			"validate Detect fact", definition.LevelID, errors.New("invalid fact result"),
		)
	}
	return StateEligibilityV2{stateDisposition: effectiveDisposition}, nil
}

func validateRequestV2(request EvaluationRequestV2) ([]strategy.CompiledLevel, error) {
	if request.Plan == nil || !request.Limits.valid() || request.TenantID == "" || request.BusinessID == "" ||
		request.Record.RecordID == "" || request.Record.SourceTime < 0 || request.EvaluationTime < 0 || request.ExecutionID == "" ||
		request.RecordRef.RecordID != request.Record.RecordID || request.RecordRef.SourceTime != request.Record.SourceTime ||
		request.RecordRef.Dimensions == nil || request.Observed.Values == nil {
		return nil, invariantV2("validate request", 0, errors.New("missing identity, value, time, or budget"))
	}
	levels := request.Plan.Levels()
	if len(levels) == 0 || uint32(len(levels)) > request.Limits.MaxLevels || uint32(len(levels)) > request.Limits.MaxLevelResultsPerEvent ||
		len(request.Record.LevelFacts) != len(levels) || len(request.Histories) != len(levels) || len(request.EffectiveTimeFacts) != len(levels) {
		return nil, invariantV2("align Level inputs", 0, errors.New("Level input cardinality mismatch"))
	}
	var computeCost uint64
	for index, level := range levels {
		definition := level.Definition()
		if definition.LevelID == 0 || (index > 0 && definition.LevelID <= levels[index-1].Definition().LevelID) ||
			request.Record.LevelFacts[index].Definition.LevelID != definition.LevelID || request.Histories[index].LevelID != definition.LevelID ||
			request.EffectiveTimeFacts[index].LevelID != definition.LevelID || request.Histories[index].View == nil {
			return nil, invariantV2("align Level inputs", definition.LevelID, errors.New("Level inputs must be sorted, unique, and aligned"))
		}
		triggerPlan, recoveryPlan := level.Trigger(), level.Recovery()
		required, ok := requiredHistoryPointsV2(triggerPlan, recoveryPlan)
		if !ok || triggerPlan.WindowSize > request.Limits.MaxTriggerWindowSize ||
			recoveryPlan.ConsecutiveWindows > request.Limits.MaxRecoveryConsecutiveWindows ||
			required > request.Limits.MaxRequiredHistoryPoints || required != level.RequiredDetectHistoryPoints() {
			return nil, invariantV2("admit Level plan", definition.LevelID, errors.New("compiled window exceeds admitted shape"))
		}
		levelCost := uint64(1)
		if recoveryPlan.Enabled {
			levelCost += uint64(recoveryPlan.ConsecutiveWindows)
		}
		if math.MaxUint64-computeCost < levelCost {
			return nil, invariantV2("admit compute", definition.LevelID, errors.New("compute cost overflow"))
		}
		computeCost += levelCost
	}
	if computeCost > request.Limits.MaxComputeCost {
		return nil, invariantV2("admit compute", 0, errors.New("compute cost exceeds admitted limit"))
	}
	return levels, nil
}

func evaluateLevelV2(
	request EvaluationRequestV2,
	level strategy.CompiledLevel,
	fact DetectionFact,
	history HistoryView,
	effective strategy.EffectiveTimeFact,
	eligibility StateEligibilityV2,
) (LevelOutcomeV2, contract.LevelResultV1, error) {
	definition := level.Definition()
	outcome := LevelOutcomeV2{
		LevelID: definition.LevelID, LevelCode: definition.LevelCode, Priority: definition.Priority,
		TriggerFingerprint: level.Fingerprints().Trigger, StateDisposition: eligibility.StateDisposition(),
	}
	validFact := fact.Result == DetectionAnomalous || fact.Result == DetectionNormal
	if !validFact {
		outcome.UnavailableReason = fact.ReasonCode
		return outcome, contract.LevelResultV1{}, nil
	}
	detectEvidence, err := buildDetectEvidenceV2(request.Record, fact, strategy.EffectiveTimeActive)
	if err != nil {
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("build Detect evidence", definition.LevelID, err)
	}
	switch effective.Status() {
	case strategy.EffectiveTimeInactive:
		outcome.SuppressedReason = contract.ReasonEffectiveTimeInactive
		return outcome, contract.LevelResultV1{}, nil
	case strategy.EffectiveTimeUnknown:
		outcome.UnavailableReason = contract.ReasonEffectiveTimeUnknown
		return outcome, contract.LevelResultV1{}, nil
	case strategy.EffectiveTimeActive:
	default:
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("validate EffectiveTime fact", definition.LevelID, errors.New("unknown status"))
	}

	triggerPlan, recoveryPlan := level.Trigger(), level.Recovery()
	requiredPoints, _ := requiredHistoryPointsV2(triggerPlan, recoveryPlan)
	summary := history.Summarize(request.Record.SourceTime, requiredPoints)
	if err := validateHistorySummaryV2(request.Record.SourceTime, requiredPoints, triggerPlan.StepSeconds, summary); err != nil {
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("validate History summary", definition.LevelID, err)
	}
	triggerStart, ok := windowStartV2(request.Record.SourceTime, triggerPlan.WindowSize, triggerPlan.StepSeconds)
	if !ok {
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("calculate Trigger window", definition.LevelID, errors.New("window time overflow"))
	}
	observedAnomalies := history.CountAnomalies(triggerStart, request.Record.SourceTime)
	if observedAnomalies > triggerPlan.WindowSize {
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("count Trigger anomalies", definition.LevelID, errors.New("anomaly count exceeds window positions"))
	}
	result := ""
	if fact.Result == DetectionAnomalous && observedAnomalies >= triggerPlan.RequiredAnomalies {
		result = contract.LevelResultAbnormal
	} else if summary.Completeness != HistoryFull {
		outcome.UnavailableReason = historyReasonV2(summary.Completeness)
		outcome.HistoryCompleteness = summary.Completeness
		return outcome, contract.LevelResultV1{}, nil
	}
	observedMisses := uint32(0)
	oldestWindowStart := triggerStart
	if recoveryPlan.Enabled && result == "" {
		for offset := uint32(0); offset < recoveryPlan.ConsecutiveWindows; offset++ {
			shift, ok := multiplyUint32ToInt64(offset, triggerPlan.StepSeconds)
			if !ok || shift > request.Record.SourceTime {
				return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("calculate Recovery window", definition.LevelID, errors.New("window time overflow"))
			}
			windowEnd := request.Record.SourceTime - shift
			windowStart, ok := windowStartV2(windowEnd, triggerPlan.WindowSize, triggerPlan.StepSeconds)
			if !ok {
				return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("calculate Recovery window", definition.LevelID, errors.New("window time overflow"))
			}
			oldestWindowStart = windowStart
			if history.CountAnomalies(windowStart, windowEnd) >= triggerPlan.RequiredAnomalies {
				break
			}
			observedMisses++
		}
	}
	if result == "" && recoveryPlan.Enabled && observedMisses >= recoveryPlan.ConsecutiveWindows {
		result = contract.LevelResultRecovery
	} else if result == "" {
		result = contract.LevelResultNormal
	}

	decisionWindow := contract.DecisionWindowV1{
		Type: decisionWindowTypeV1, Version: 1, SourceTime: request.Record.SourceTime,
		Trigger: contract.TriggerWindowEvidenceV1{
			WindowStart: triggerStart, WindowEnd: request.Record.SourceTime, WindowSize: triggerPlan.WindowSize,
			RequiredAnomalies: triggerPlan.RequiredAnomalies, ObservedAnomalies: observedAnomalies,
		},
		Recovery: contract.RecoveryWindowEvidenceV1{
			Enabled: recoveryPlan.Enabled, RequiredConsecutiveWindows: recoveryPlan.ConsecutiveWindows,
			ObservedConsecutiveMisses: observedMisses, OldestWindowStart: oldestWindowStart,
		},
		HistoryCompleteness: summary.Completeness,
		WindowEvidence: contract.WindowEvidenceV1{
			AnomalyTimestampsDigest: hex.EncodeToString(summary.AnomalyDigest[:]), LateAccepted: request.LateAccepted,
		},
	}
	outcome.Result = result
	outcome.HistoryCompleteness = summary.Completeness
	outcome.DecisionWindow = &decisionWindow
	outcome.DetectEvidence = &detectEvidence
	levelResult := contract.LevelResultV1{
		LevelID: definition.LevelID, LevelCode: definition.LevelCode, Priority: definition.Priority, Result: result,
		DecisionWindow: decisionWindow, DetectEvidence: detectEvidence, LevelTriggerFingerprint: level.Fingerprints().Trigger,
	}
	return outcome, levelResult, nil
}

func buildDetectEvidenceV2(record DetectionRecord, fact DetectionFact, effectiveStatus string) (contract.DetectEvidenceV1, error) {
	if fact.Evidence.ProjectedValueOrdinal == nil || int(*fact.Evidence.ProjectedValueOrdinal) >= len(record.ProjectedValues) ||
		!validDigestV2(fact.Evidence.PredicateDigest) {
		return contract.DetectEvidenceV1{}, errors.New("missing projection or predicate evidence")
	}
	projected := record.ProjectedValues[*fact.Evidence.ProjectedValueOrdinal]
	if !projected.Available || !validCanonicalDecimalV2(projected.CanonicalDecimal) || projected.ReasonCode != "" {
		return contract.DetectEvidenceV1{}, errors.New("successful fact references unavailable projection")
	}
	raw := json.RawMessage(projected.CanonicalDecimal)
	return contract.DetectEvidenceV1{
		DetectionResult: fact.Result, PredicateDigest: fact.Evidence.PredicateDigest, NormalizedValue: raw,
		MatchedAlgorithmOrdinal: cloneUint32V2(fact.Evidence.MatchedAlgorithmOrdinal),
		MatchedGroupOrdinal:     cloneUint32V2(fact.Evidence.MatchedGroupOrdinal), ResultReason: fact.Evidence.ResultReason,
		EffectiveTimeStatus: effectiveStatus,
	}, nil
}

func validCanonicalDecimalV2(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' {
		start = 1
		if start == len(value) {
			return false
		}
	}
	dot := len(value) - 7
	if dot <= start || value[dot] != '.' || (dot-start > 1 && value[start] == '0') {
		return false
	}
	for index := start; index < len(value); index++ {
		if index == dot {
			continue
		}
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	if value[0] == '-' {
		allZero := true
		for index := start; index < len(value); index++ {
			if value[index] != '0' && value[index] != '.' {
				allZero = false
				break
			}
		}
		if allZero {
			return false
		}
	}
	return true
}

func validateEffectiveTimeFactV2(evaluationTime int64, requirementDigest string, fact strategy.EffectiveTimeFact) error {
	if fact.RequirementDigest() != requirementDigest || !validDigestV2(fact.FactDigest()) ||
		!validDigestV2(fact.FactRevision()) || fact.ValidFrom() > evaluationTime ||
		fact.ValidUntil() <= evaluationTime || fact.ValidUntil() <= fact.ValidFrom() {
		return errors.New("fact requirement, digest, revision, or validity interval is invalid")
	}
	return nil
}

func validateHistorySummaryV2(sourceTime int64, requiredPoints, stepSeconds uint32, summary HistorySummary) error {
	if summary.Completeness != HistoryFull && summary.Completeness != HistoryWarming && summary.Completeness != HistoryGapped {
		return errors.New("unknown history completeness")
	}
	if summary.WindowEnd != sourceTime || summary.WindowStart < 0 || summary.WindowStart > summary.WindowEnd ||
		summary.ValidPositions > requiredPoints || summary.AnomalyCount > summary.ValidPositions {
		return errors.New("history summary shape is invalid")
	}
	expectedOffset, ok := multiplyUint32ToInt64(requiredPoints-1, stepSeconds)
	if !ok || expectedOffset > sourceTime || summary.WindowStart != sourceTime-expectedOffset {
		return errors.New("history summary window is not position aligned")
	}
	if summary.Completeness == HistoryFull && summary.ValidPositions != requiredPoints {
		return errors.New("FULL history does not contain every position")
	}
	return nil
}

func requiredHistoryPointsV2(trigger strategy.TriggerPlan, recovery strategy.RecoveryPlan) (uint32, bool) {
	if trigger.WindowSize == 0 || trigger.RequiredAnomalies == 0 || trigger.RequiredAnomalies > trigger.WindowSize || trigger.StepSeconds == 0 {
		return 0, false
	}
	if !recovery.Enabled {
		if recovery.ConsecutiveWindows != 0 {
			return 0, false
		}
		return trigger.WindowSize, true
	}
	if recovery.ConsecutiveWindows == 0 || math.MaxUint32-trigger.WindowSize < recovery.ConsecutiveWindows-1 {
		return 0, false
	}
	return trigger.WindowSize + recovery.ConsecutiveWindows - 1, true
}

func windowStartV2(endTime int64, windowSize, stepSeconds uint32) (int64, bool) {
	span, ok := multiplyUint32ToInt64(windowSize, stepSeconds)
	if !ok || span == 0 {
		return 0, false
	}
	span--
	if span > endTime {
		return 0, false
	}
	return endTime - span, true
}

func multiplyUint32ToInt64(left, right uint32) (int64, bool) {
	value := uint64(left) * uint64(right)
	if value > math.MaxInt64 {
		return 0, false
	}
	return int64(value), true
}

func aggregateRecordResultV2(results []contract.LevelResultV1) string {
	result := contract.LevelResultNormal
	for _, level := range results {
		if level.Result == contract.LevelResultAbnormal {
			return contract.LevelResultAbnormal
		}
		if level.Result == contract.LevelResultRecovery {
			result = contract.LevelResultRecovery
		}
	}
	return result
}

func historyReasonV2(completeness string) string {
	if completeness == HistoryGapped {
		return contract.ReasonHistoryGapped
	}
	return contract.ReasonHistoryWarming
}

func validDigestV2(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func cloneUint32V2(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func invariantV2(operation string, levelID uint32, err error) error {
	return &InternalErrorV2{Operation: operation, LevelID: levelID, Err: err}
}
