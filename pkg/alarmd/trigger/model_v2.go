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
	// CountObserved reports how many positions in the range the window actually
	// holds. It separates "nothing was seen here" from "something was seen and
	// it was not anomalous", which CountAnomalies alone cannot: that returns
	// zero for both.
	//
	// The recovery walk needs the count, not a yes/no on one position. A window
	// mostly made of holes counts few anomalies for the same reason an empty
	// one does, so "did this window trigger" can only be answered once the
	// holes are counted alongside the anomalies.
	//
	// Required rather than an optional interface on purpose. An implementation
	// that silently lacked it would have every position read as unobserved,
	// and the recovery walk would step over a whole window of real data
	// looking for evidence it already had.
	CountObserved(fromTime, untilTime int64) uint32
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

// The states of another Level a RECOVERY record can be decided beside. The
// set is closed: a metric label is made of it. None of them holds the
// envelope; see recoveryGateV2.
const (
	// RecoveryBesideLevelUnavailable: another Level could not be evaluated.
	RecoveryBesideLevelUnavailable = "level_unavailable"
	// RecoveryBesideLevelRecovering: another Level read NORMAL with recovery
	// enabled, so a window inside its recovery span still meets its trigger.
	RecoveryBesideLevelRecovering = "level_recovering"
	// RecoveryBesideLevelWithoutRecovery: another Level read NORMAL with its
	// recovery disabled.
	RecoveryBesideLevelWithoutRecovery = "level_without_recovery"
)

// The causes a RECOVERY envelope is held for, all from the open alert set.
// The set is closed: a metric label is made of it.
const (
	// RecoveryHeldNoOpenAlert: the consumer holds no open alert on the
	// series, so there is nothing for the envelope to resolve.
	RecoveryHeldNoOpenAlert = "no_open_alert"
	// RecoveryHeldFingerprintUnknown: the series identity the consumer keys
	// alerts by could not be built, so membership cannot be asked. Not "not a
	// member": an unknown read as absent would hold this Plan's recoveries
	// for good and leave no trace.
	RecoveryHeldFingerprintUnknown = "fingerprint_unknown"
)

// The outcomes of the recovery gate, the open alert set. The set is
// closed: a metric label is made of it. Each is reachable in production:
// the first four from a Plan on the alert consumer's protocol against a
// set, the last from a Plan on any other protocol.
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
	// OpenAlertGateProtocolNotGated: the Plan does not publish the alert
	// consumer's protocol, so the consumer's open alert set has nothing to
	// say about its envelope. The compatibility protocol carries anomalies
	// only and drops the RECOVERY envelope at the sink; alarmd's own decision
	// event has no consumer that keeps an open alert set. The set is not
	// asked.
	OpenAlertGateProtocolNotGated = "protocol_not_gated"
)

// RecoveryGateV2 is what became of a record whose evaluated Levels came to
// RECOVERY and none to ABNORMAL. Only the open alert set holds its envelope;
// another Level's state does not (recoveryGateV2).
type RecoveryGateV2 struct {
	// Held and Cause are the open alert set's: one of the RecoveryHeld*
	// values when it held the envelope.
	Held  bool
	Cause string
	// Beside and BesideLevelID name the first other Level the record was
	// decided beside, one of the RecoveryBeside* values, or empty when every
	// other Level agreed or was suppressed.
	Beside        string
	BesideLevelID uint32
	// OpenAlertGate is the open alert set's outcome, one of the
	// OpenAlertGate* values, for every RECOVERY record.
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
