// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package trigger

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const (
	CompletionEvaluated   = "EVALUATED"
	CompletionSuppressed  = "SUPPRESSED"
	CompletionUnavailable = "UNAVAILABLE"

	StateAdvance = "ADVANCE"
	StateFreeze  = "FREEZE"

	DetectionAnomalous   = "ANOMALOUS"
	DetectionNormal      = "NORMAL"
	DetectionUnavailable = "UNAVAILABLE"
	DetectionError       = "ERROR"

	HistoryFull    = "FULL"
	HistoryWarming = "WARMING"
	HistoryGapped  = "GAPPED"
)

var ErrInvariantV2 = errors.New("alarmd trigger v2 invariant")

type InternalErrorV2 struct {
	Operation string
	LevelID   uint32
	Err       error
}

func (e *InternalErrorV2) Error() string {
	if e == nil {
		return ErrInvariantV2.Error()
	}
	if e.LevelID == 0 {
		return fmt.Sprintf("alarmd trigger v2: %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("alarmd trigger v2: %s level %d: %v", e.Operation, e.LevelID, e.Err)
}

func (e *InternalErrorV2) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *InternalErrorV2) Is(target error) bool {
	return target == ErrInvariantV2 || (e != nil && errors.Is(e.Err, target))
}

type EvaluationLimitsV2 struct {
	MaxLevels                     uint32
	MaxTriggerWindowSize          uint32
	MaxRecoveryConsecutiveWindows uint32
	MaxRequiredHistoryPoints      uint32
	MaxLevelResultsPerEvent       uint32
	MaxEvidenceBytesPerEvent      int
	MaxComputeCost                uint64
}

func (limits EvaluationLimitsV2) valid() bool {
	return limits.MaxLevels > 0 && limits.MaxTriggerWindowSize > 0 &&
		limits.MaxRecoveryConsecutiveWindows > 0 && limits.MaxRequiredHistoryPoints > 0 &&
		limits.MaxLevelResultsPerEvent > 0 && limits.MaxEvidenceBytesPerEvent > 0 && limits.MaxComputeCost > 0
}

// DetectionRecord and its children are the narrow M5-to-M6 boundary. M7 maps
// M5's immutable DetectionBatch into this view; M6 never re-runs a detector.
type DetectionRecord struct {
	RecordID        string
	SourceTime      int64
	ProjectedValues []ProjectedValue
	LevelFacts      []DetectionFact
}

type ProjectedValue struct {
	CanonicalDecimal string
	Available        bool
	ReasonCode       string
}

type DetectionFact struct {
	Definition        contract.LevelDefinitionV2
	DetectFingerprint string
	Result            string
	ReasonCode        string
	Evidence          DetectionEvidence
}

type DetectionEvidence struct {
	PredicateDigest         string
	ProjectedValueOrdinal   *uint32
	MatchedAlgorithmOrdinal *uint32
	MatchedGroupOrdinal     *uint32
	ResultReason            string
}

type HistorySummary struct {
	Completeness   string
	WindowStart    int64
	WindowEnd      int64
	ValidPositions uint32
	AnomalyCount   uint32
	AnomalyDigest  [32]byte
}

type HistoryView interface {
	Summarize(endTime int64, requiredPositions uint32) HistorySummary
	CountAnomalies(fromTime, untilTime int64) uint32
}

type LevelHistory struct {
	LevelID uint32
	View    HistoryView
}

type LevelEffectiveTimeFact struct {
	LevelID uint32
	Fact    strategy.EffectiveTimeFact
}

// StateEligibilityV2 is M6's pre-history decision for whether the current
// Level fact may advance runtime history. The value is immutable to callers.
type StateEligibilityV2 struct {
	stateDisposition string
}

func (e StateEligibilityV2) StateDisposition() string {
	return e.stateDisposition
}

type EvaluationRequestV2 struct {
	TenantID           string
	BusinessID         string
	Plan               *strategy.CompiledPlan
	Record             DetectionRecord
	RecordRef          contract.TriggerRecordRefV1
	Observed           contract.TriggerObservedV1
	Histories          []LevelHistory
	EffectiveTimeFacts []LevelEffectiveTimeFact
	EvaluationTime     int64
	ExecutionID        string
	LateAccepted       bool
	Limits             EvaluationLimitsV2
}

type LevelOutcomeV2 struct {
	LevelID             uint32
	LevelCode           string
	Priority            uint32
	Result              string
	SuppressedReason    string
	UnavailableReason   string
	StateDisposition    string
	HistoryCompleteness string
	DecisionWindow      *contract.DecisionWindowV1
	DetectEvidence      *contract.DetectEvidenceV1
	TriggerFingerprint  string
}

type EvaluationCountsV2 struct {
	Levels      uint64
	Evaluated   uint64
	Abnormal    uint64
	Normal      uint64
	Recovery    uint64
	Suppressed  uint64
	Unavailable uint64
	Events      uint64
}

type EvaluationResultV2 struct {
	Completion    string
	RecordResult  string
	LevelOutcomes []LevelOutcomeV2
	TriggerEvent  *contract.TriggerEventV1
	Counts        EvaluationCountsV2
}
