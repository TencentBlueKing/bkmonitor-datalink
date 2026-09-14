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
	Completeness string
	WindowStart  int64
	WindowEnd    int64
	// ValidPositions and RequiredPositions travel together because neither
	// answers anything alone: the shortfall is the whole signal, and a reader
	// given only the verdict cannot separate a window that is one point away
	// from converging from one that has never been close.
	ValidPositions    uint32
	RequiredPositions uint32
	AnomalyCount      uint32
	AnomalyDigest     [32]byte
}

type HistoryView interface {
	Summarize(endTime int64, requiredPositions uint32) HistorySummary
	CountAnomalies(fromTime, untilTime int64) uint32
	// FirstAnomaly reports the earliest anomalous source time in the range, and
	// whether the range held one at all. Zero is a valid source time, so the
	// answer cannot be carried by the value alone.
	FirstAnomaly(fromTime, untilTime int64) (int64, bool)
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
	// OpenAlerts is the consumer's open alert set for the second recovery
	// gate. Nil means the caller has no such gate, which is a different
	// thing from a set that could not be loaded: the latter is a set that
	// answers from what this process sent, and is never nil. The two are
	// told apart in the gate outcome, not folded into one another.
	OpenAlerts contract.OpenAlertSet
}

type LevelOutcomeV2 struct {
	LevelID   uint32
	LevelCode string
	Priority  uint32
	// RecoveryEnabled is whether the Level's recovery plan is enabled. It is
	// set on every outcome, whichever way the Level was decided, so the
	// recovery gate reads it off the outcome and never has to line outcomes
	// up against the compiled Levels by position.
	RecoveryEnabled     bool
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

// The causes a RECOVERY envelope is held for. The set is closed: a metric
// label is made of it.
const (
	// RecoveryHeldLevelUnavailable: a Level could not be evaluated at all, so
	// whether it is still in alarm is unknown.
	RecoveryHeldLevelUnavailable = "level_unavailable"
	// RecoveryHeldLevelRecovering: a Level read NORMAL with recovery enabled,
	// which means a window inside its recovery span still meets its trigger.
	RecoveryHeldLevelRecovering = "level_recovering"
	// RecoveryHeldNoOpenAlert: every Level agreed, and the consumer holds no
	// open alert on the series, so there is nothing for the envelope to
	// resolve. This is the second gate's cause; it is counted apart from the
	// two above, which are about Levels.
	RecoveryHeldNoOpenAlert = "no_open_alert"
	// RecoveryHeldFingerprintUnknown: the series identity the consumer keys
	// alerts by could not be built, so membership cannot be asked. Not "not a
	// member": an unknown read as absent would hold this Plan's recoveries
	// for good and leave no trace.
	RecoveryHeldFingerprintUnknown = "fingerprint_unknown"
)

// The outcomes of the second recovery gate, the open alert set. The set is
// closed: a metric label is made of it. Each is reachable in production:
// the first four from a native Plan against a set, the last from a Plan on
// the compatibility protocol, which has no RECOVERY message at all.
const (
	// OpenAlertGatePassed: the consumer holds an open alert; the envelope goes.
	OpenAlertGatePassed = "passed"
	// OpenAlertGateHeldNoOpenAlert: see RecoveryHeldNoOpenAlert.
	OpenAlertGateHeldNoOpenAlert = "held_no_open_alert"
	// OpenAlertGateHeldFingerprintUnknown: see RecoveryHeldFingerprintUnknown.
	OpenAlertGateHeldFingerprintUnknown = "held_fingerprint_unknown"
	// OpenAlertGateNotConfigured: the caller passed no set. The envelope goes
	// as it did before the gate existed. A production worker always passes a
	// set, so this outcome counting there is the wiring having come apart.
	OpenAlertGateNotConfigured = "not_configured"
	// OpenAlertGateLegacyProtocol: the Plan publishes the compatibility
	// protocol, which carries anomalies only; its RECOVERY envelope is
	// dropped at the sink whatever the set says, so the set is not asked.
	OpenAlertGateLegacyProtocol = "legacy_protocol"
)

// RecoveryGateV2 is what became of a record whose evaluated Levels agreed on
// RECOVERY. The envelope that record would produce resolves the alert on its
// series at the consumer, whatever Level that alert stands at, so every Level
// has to have had its say: a Level whose state is unknown, or whose recovery
// span still holds a triggering window, has not agreed and holds the envelope.
// The Level results themselves are unaffected and still reach the state.
type RecoveryGateV2 struct {
	Held bool
	// Cause and LevelID name the first Level that held the envelope.
	Cause   string
	LevelID uint32
	// PassedLevelWithoutRecovery reports an envelope sent past a NORMAL Level
	// whose recovery is disabled. Such a Level can never say RECOVERY, so it is
	// not asked; the count says whether that shape exists in a deployment.
	PassedLevelWithoutRecovery bool
	// OpenAlertGate is the second gate's outcome, one of the OpenAlertGate*
	// values. It is empty when the first gate held, because the second was
	// then not asked: a record is counted by at most one of the two.
	OpenAlertGate string
}

type EvaluationResultV2 struct {
	Completion    string
	RecordResult  string
	LevelOutcomes []LevelOutcomeV2
	TriggerEvent  *contract.TriggerEventV1
	Counts        EvaluationCountsV2
	// RecoveryGate is set only when RecordResult is RECOVERY. A held gate
	// leaves TriggerEvent nil while RecordResult stays RECOVERY. Held is the
	// union of both gates; Cause says which.
	RecoveryGate RecoveryGateV2
}
