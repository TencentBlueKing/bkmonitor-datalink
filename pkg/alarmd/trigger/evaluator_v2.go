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
	"strconv"
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
	if result.RecordResult == contract.LevelResultRecovery {
		result.RecoveryGate = recoveryGateV2(result.LevelOutcomes)
	}
	if result.RecordResult == contract.LevelResultAbnormal || result.RecordResult == contract.LevelResultRecovery {
		if uint32(len(levelResults)) > request.Limits.MaxLevelResultsPerEvent {
			return EvaluationResultV2{}, invariantV2("admit event Level results", 0, errors.New("compiled result exceeds admitted limit"))
		}
		fingerprints := request.Plan.Fingerprints()
		var snapshotRef *contract.StrategySnapshotRef
		var dedupeMD5 string
		ref := request.Plan.StrategyRef()
		if ref.SnapshotRevision > 0 {
			strategyID, strategyErr := strconv.ParseInt(ref.StrategyID, 10, 64)
			businessID, businessErr := strconv.ParseInt(request.BusinessID, 10, 64)
			if strategyErr != nil || businessErr != nil || ref.TenantID != request.TenantID {
				return EvaluationResultV2{}, invariantV2("build strategy snapshot reference", 0, errors.New("invalid frozen strategy identity"))
			}
			snapshotRef = &contract.StrategySnapshotRef{TenantID: ref.TenantID, BusinessID: businessID, StrategyID: strategyID, Revision: ref.SnapshotRevision}
			if identity := request.Plan.OutputIdentity(); identity != nil {
				var err error
				dedupeMD5, err = contract.MonitorDedupeMD5(ref.StrategyID, request.BusinessID, request.RecordRef.Dimensions, *identity)
				if err != nil {
					return EvaluationResultV2{}, invariantV2("build monitor dedupe identity", 0, err)
				}
			}
		}
		if result.RecordResult == contract.LevelResultRecovery {
			result.RecoveryGate = openAlertGateV2(result.RecoveryGate, request, ref.StrategyID, dedupeMD5)
			if result.RecoveryGate.Held {
				return result, nil
			}
		}
		event, err := contract.BuildTriggerEventV1(contract.TriggerEventBuildInputV1{
			StrategyRef: snapshotRef,
			DedupeMD5:   dedupeMD5,
			EventKind:   result.RecordResult, TenantID: request.TenantID, BusinessID: request.BusinessID,
			PlanRef: request.Plan.PlanRef(), RecordRef: request.RecordRef, Observed: request.Observed,
			LevelResults: levelResults, EvaluationTime: request.EvaluationTime,
			DetectPlanFingerprint: fingerprints.Detect, TriggerStateFingerprint: fingerprints.Trigger,
			ExecutionID: request.ExecutionID, MaxEvidenceBytes: request.Limits.MaxEvidenceBytesPerEvent,
		})
		if err != nil {
			return EvaluationResultV2{}, invariantV2("build TriggerEvent", 0, err)
		}
		result.TriggerEvent = event
		// The compatibility context is attached whenever the Plan publishes that
		// protocol. Before the format was stated, the only Plans that did were
		// the ones with no frozen revision, so the two conditions were the same
		// one; a forced compatibility choice makes them different, and reading
		// the revision here would leave those Plans without the context their
		// conversion needs.
		if legacy := request.Plan.LegacyOutput(); legacy != nil && request.Plan.PublishesCompatibleProtocol() {
			var timestamps []int64
			for _, outcome := range event.LevelResults {
				if outcome.LevelID != event.PrimaryLevelID {
					continue
				}
				for _, history := range request.Histories {
					if history.LevelID != event.PrimaryLevelID {
						continue
					}
					iterator, ok := history.View.(interface {
						ForEachAnomaly(int64, int64, func(int64) bool)
					})
					if !ok {
						return EvaluationResultV2{}, invariantV2("legacy anomaly history", event.PrimaryLevelID, errors.New("history cannot expose actual anomaly timestamps"))
					}
					iterator.ForEachAnomaly(outcome.DecisionWindow.Trigger.WindowStart, request.RecordRef.SourceTime, func(ts int64) bool { timestamps = append(timestamps, ts); return true })
				}
				if len(timestamps) != int(outcome.DecisionWindow.Trigger.ObservedAnomalies) {
					return EvaluationResultV2{}, invariantV2("legacy anomaly history", event.PrimaryLevelID, errors.New("actual anomaly timestamps disagree with trigger evidence"))
				}
			}
			event.LegacyOutput = &contract.LegacyEventContext{Configuration: legacy, AnomalyTimestamps: append([]int64{}, timestamps...)}
		}
		event.WireFormat = request.Plan.WireFormat()
		event.SignalType = request.Plan.SignalType()
		if identity := request.Plan.OutputIdentity(); identity != nil {
			subject, remaining, subjectErr := contract.ProjectMonitorSubject(
				request.RecordRef.Dimensions, *identity, request.Plan.SubjectFacts(),
			)
			if subjectErr != nil {
				return EvaluationResultV2{}, invariantV2("project event subject", 0, subjectErr)
			}
			event.Subject = &contract.MonitorSubjectContext{Subject: subject, Dimensions: remaining}
		}
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
		if !contract.LevelUnavailableReasonV2(fact.ReasonCode) {
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
	// RecoveryEnabled is set here, before any return, so that every outcome
	// carries it: the suppressed and unavailable paths leave before the
	// recovery plan is otherwise consulted.
	outcome := LevelOutcomeV2{
		LevelID: definition.LevelID, LevelCode: definition.LevelCode, Priority: definition.Priority,
		RecoveryEnabled:    level.Recovery().Enabled,
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
	anomalyBeginTime, _ := history.FirstAnomaly(triggerStart, request.Record.SourceTime)
	if observedAnomalies > triggerPlan.WindowSize {
		return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("count Trigger anomalies", definition.LevelID, errors.New("anomaly count exceeds window positions"))
	}
	result := ""
	if fact.Result == DetectionAnomalous && observedAnomalies >= triggerPlan.RequiredAnomalies {
		result = contract.LevelResultAbnormal
	}
	// The recovery walk runs before the completeness gate, and only recovery
	// does. A window short of positions cannot say a Level is normal, but it
	// can say the Level has been observed normal for long enough to close what
	// is open: an alert stays open until a recovery closes it, so making
	// recovery wait for a full window means a three minute hole keeps every
	// open alert of a Plan with a day-long window open for a day. Decided at
	// decision-022: recovery reads observed positions, NORMAL is unchanged and
	// still requires FULL.
	observedMisses, skippedWindows := uint32(0), uint32(0)
	oldestWindowStart := triggerStart
	if recoveryPlan.Enabled && result == "" {
		// How far back an observed position may still count: the retained
		// window and no further, because nothing older than that exists to be
		// read. A bound wider than the retention would be a bound nothing can
		// reach, and its branches would be code no round runs.
		//
		// Read off the Level's own retention rather than recomputed from the
		// window. The two were the same number until the compiler began
		// retaining a slack beyond the required window, and a walk still
		// bounded at the required size would stop exactly where the slack
		// begins - leaving the positions the slack exists to keep unread, and
		// this whole rule reachable only in the cases that never needed it.
		//
		// Not read off the summary either: the summary comes from the history,
		// and a history that leaves the field zero would silently bound the
		// walk to nothing rather than to the window.
		retained := level.StateRequirement().RetentionPoints
		if retained == 0 {
			return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2(
				"bound Recovery walk", definition.LevelID, errors.New("Level retains no positions"))
		}
		for offset := uint32(0); observedMisses < recoveryPlan.ConsecutiveWindows && offset < retained; offset++ {
			shift, ok := multiplyUint32ToInt64(offset, triggerPlan.StepSeconds)
			if !ok {
				return LevelOutcomeV2{}, contract.LevelResultV1{}, invariantV2("calculate Recovery window", definition.LevelID, errors.New("window time overflow"))
			}
			// Reaching the start of time is a stop, not a fault. Before this
			// walk could skip, the offset count kept it inside the history and
			// getting here meant the configuration was impossible, so it was
			// an invariant violation; a walk that steps over holes reaches it
			// on ordinary histories.
			if shift > request.Record.SourceTime {
				break
			}
			windowEnd := request.Record.SourceTime - shift
			windowStart, ok := windowStartV2(windowEnd, triggerPlan.WindowSize, triggerPlan.StepSeconds)
			if !ok {
				// No window can be formed this far back, so there is nothing
				// left to observe. Same stop as the two above, and for the
				// same reason they changed: a walk that can skip reaches here
				// on ordinary histories, where the fixed-length one could only
				// arrive by misconfiguration.
				break
			}
			oldestWindowStart = windowStart
			// A window nobody observed is evidence of neither recovery nor its
			// opposite. Counting it as a miss, which is what happened before,
			// built recoveries on absence; breaking on it would make a single
			// hole cost the whole run. It is stepped over, and said so on the
			// evidence.
			//
			// Which of the three it is has to be decided with the holes in
			// hand. CountAnomalies returns the same small number for a quiet
			// window and for a window that was never observed, so the anomaly
			// count alone cannot tell "this did not trigger" from "there was
			// not enough here to say". Only when every hole could have been
			// anomalous and the window still would not have reached the
			// threshold is the miss an observation rather than an absence.
			anomalies := history.CountAnomalies(windowStart, windowEnd)
			if anomalies >= triggerPlan.RequiredAnomalies {
				break
			}
			observed := history.CountObserved(windowStart, windowEnd)
			holes := uint32(0)
			if observed < triggerPlan.WindowSize {
				holes = triggerPlan.WindowSize - observed
			}
			if anomalies+holes >= triggerPlan.RequiredAnomalies {
				skippedWindows++
				continue
			}
			observedMisses++
		}
	}
	if result == "" && recoveryPlan.Enabled && observedMisses >= recoveryPlan.ConsecutiveWindows {
		result = contract.LevelResultRecovery
	} else if result == "" {
		if summary.Completeness != HistoryFull {
			outcome.UnavailableReason = historyReasonV2(summary.Completeness)
			outcome.HistoryCompleteness = summary.Completeness
			return outcome, contract.LevelResultV1{}, nil
		}
		result = contract.LevelResultNormal
	}

	decisionWindow := contract.DecisionWindowV1{
		Type: decisionWindowTypeV1, Version: 1, SourceTime: request.Record.SourceTime,
		Trigger: contract.TriggerWindowEvidenceV1{
			WindowStart: triggerStart, WindowEnd: request.Record.SourceTime, WindowSize: triggerPlan.WindowSize,
			RequiredAnomalies: triggerPlan.RequiredAnomalies, ObservedAnomalies: observedAnomalies,
			AnomalyBeginTime: anomalyBeginTime,
		},
		Recovery: contract.RecoveryWindowEvidenceV1{
			Enabled: recoveryPlan.Enabled, RequiredConsecutiveWindows: recoveryPlan.ConsecutiveWindows,
			ObservedConsecutiveMisses: observedMisses, OldestWindowStart: oldestWindowStart,
			SkippedWindows: skippedWindows,
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
	if trigger.WindowSize == 0 || trigger.RequiredAnomalies == 0 || trigger.StepSeconds == 0 {
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

// recoveryGateV2 describes a record whose evaluated Levels came to RECOVERY
// and none to ABNORMAL. It no longer holds the envelope on another Level.
//
// Each Level's RECOVERY is a fact about that Level alone: its own recovery
// span holds no triggering window. The envelope says only that. The standard
// conversion writes one evaluation per decided Level under that Level's own
// severity, RECOVERY as resolved, and leaves out every Level that is NORMAL,
// unavailable or suppressed (linkdoutput evaluations). The alert consumer
// ends an active alert only on an evaluation whose severity is the alert's
// own and whose action is not triggered; an evaluation for any other
// severity is recorded as orphaned and changes nothing. So a RECOVERY for
// one Level can close that Level's alert and no other, which is what the
// reference implementation does: it recovers an alert by the alert's own
// Level. The compatibility protocol carries no RECOVERY at all.
//
// The gate used to hold the envelope until every Level had agreed, on the
// premise that the consumer resolved an alert on any RECOVERY whatever its
// Level. That premise stopped holding when the conversion went per Level,
// and the hold stayed: an alert whose own Level had recovered waited for as
// long as a sibling Level was undecidable, which for a Level whose query
// comes back empty or whose history stays gapped is indefinitely.
//
// What the gate still reports is the shape it used to hold: the first other
// Level, in Level order, that is unavailable or reads NORMAL with recovery
// enabled, and failing that one that reads NORMAL with recovery disabled.
// That count is what the change is read by: records that now go on where
// they used to wait.
func recoveryGateV2(outcomes []LevelOutcomeV2) RecoveryGateV2 {
	gate := RecoveryGateV2{}
	withoutRecovery := uint32(0)
	for _, outcome := range outcomes {
		switch {
		case outcome.UnavailableReason != "":
			if gate.Beside == "" {
				gate.Beside, gate.BesideLevelID = RecoveryBesideLevelUnavailable, outcome.LevelID
			}
		case outcome.SuppressedReason != "":
			// Suppressed by effective time: says nothing about this record.
		case outcome.Result == contract.LevelResultNormal:
			if outcome.RecoveryEnabled {
				if gate.Beside == "" {
					gate.Beside, gate.BesideLevelID = RecoveryBesideLevelRecovering, outcome.LevelID
				}
			} else if withoutRecovery == 0 {
				withoutRecovery = outcome.LevelID
			}
		}
	}
	if gate.Beside == "" && withoutRecovery != 0 {
		gate.Beside, gate.BesideLevelID = RecoveryBesideLevelWithoutRecovery, withoutRecovery
	}
	return gate
}

// openAlertGateV2 is the gate on a RECOVERY envelope: does the consumer hold
// an open alert on this series at all? The consumer ends the alert of the
// envelope's severity and records the envelope as an orphan when it holds
// none, and a healthy series says RECOVERY every cycle, so without this gate
// the orphans outnumber the real resolutions by the ratio of healthy series
// to open alerts. The set answers membership only, not severity: an envelope
// for one Level on a series whose open alert stands at another still passes
// and is an orphan there, bounded by the open alerts. It does not say whether
// the series recovered, which the Level results have already decided.
//
// Two shapes do not ask the set. A Plan that does not publish the alert
// consumer's protocol is not gated: the set is that consumer's, and the
// The Python compatibility protocol carries no RECOVERY message (the sink
// drops it). A caller that passed no
// set has no gate; that is the state before the gate existed and is named
// as such, so a worker that stops passing the set shows up as a count
// rather than as recoveries quietly going out again.
//
// A fingerprint that could not be built is a third state, held and named.
// On the consumer's protocol it is unreachable by construction: the control
// plane sets the output identity with the frozen revision the protocol
// requires, and admission refuses the pairing that would leave it out. If
// it happened anyway, the choice here is between holding this Plan's
// recoveries and passing an envelope the sink cannot convert -- which fails
// the whole batch and with it every series in the Slot. Holding costs one
// Plan; it is counted, and it is not read as "not a member", which would
// look exactly like a consumer that holds no alerts on it.
//
// The Level the record was decided beside is kept whatever this gate says:
// it describes the record, not the envelope.
func openAlertGateV2(gate RecoveryGateV2, request EvaluationRequestV2, strategyID, dedupeMD5 string) RecoveryGateV2 {
	switch {
	case request.Plan.WireFormat() != contract.WireFormatStandardRawEvent:
		gate.OpenAlertGate = OpenAlertGateProtocolNotGated
	case request.OpenAlerts == nil:
		gate.OpenAlertGate = OpenAlertGateNotConfigured
	case dedupeMD5 == "":
		gate.Held, gate.Cause, gate.OpenAlertGate = true, RecoveryHeldFingerprintUnknown, OpenAlertGateHeldFingerprintUnknown
	case !request.OpenAlerts.Contains(request.TenantID, strategyID, dedupeMD5):
		gate.Held, gate.Cause, gate.OpenAlertGate = true, RecoveryHeldNoOpenAlert, OpenAlertGateHeldNoOpenAlert
	default:
		gate.OpenAlertGate = OpenAlertGatePassed
	}
	return gate
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
