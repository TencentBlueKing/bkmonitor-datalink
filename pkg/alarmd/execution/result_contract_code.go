// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package execution

// The result contract refuses a Plan evaluation for one of the rules below, and
// every refusal used to leave the same trace: a plain error whose text said
// which rule, wrapped at one site into a single failure code. Everything that
// this contract could refuse arrived at the page and the rate-limited log as
// EVALUATION_RESULT_INVALID, so an operator could tell that the deployment had
// rejected its own result but not which of forty-six rules it had broken -- and
// those rules are different people's work: a Level outcome that contradicts its
// own guard is the algorithm path, a history replacement that changes a loaded
// point is the state path, a missing TriggerEvent envelope is the event path.
//
// Each refusal now names itself. The code travels the route that was already
// there: worker.wrapEvaluationError prefers a code the error declares over the
// one its wrap site would have supplied, the same way StateContractMismatchError
// already did. The category stays with the stage, because where it failed is
// what the wrap site knows and what it failed is what the error knows.
//
// The codes are a closed vocabulary and resultContractCodes lists all of them,
// so a consumer that classifies them can be held to covering the list rather
// than to a copy of it. They are not a metric label: query_failure_total carries
// stage and category only, and this belongs to the log line and the page, where
// one value per rule is what a reader needs and cardinality is not a budget.
const (
	codeOutcomePrimaryRecordMissing        = "OUTCOME_PRIMARY_RECORD_MISSING"
	codeOutcomeNotASelectedLevel           = "OUTCOME_NOT_A_SELECTED_LEVEL"
	codeOutcomeDuplicate                   = "OUTCOME_DUPLICATE"
	codeOutcomeMissingForLevel             = "OUTCOME_MISSING_FOR_LEVEL"
	codeOutcomeIdentityIncomplete          = "OUTCOME_IDENTITY_INCOMPLETE"
	codeOutcomeKindInvalid                 = "OUTCOME_KIND_INVALID"
	codeOutcomeRetryableSeriesNotUnknown   = "OUTCOME_RETRYABLE_SERIES_NOT_UNKNOWN"
	codeOutcomeInvalidSeriesNotTerminal    = "OUTCOME_INVALID_SERIES_NOT_TERMINAL"
	codeOutcomeBusinessUnderActiveGuard    = "OUTCOME_BUSINESS_UNDER_ACTIVE_GUARD"
	codeOutcomeUnknownDropsGuardReason     = "OUTCOME_UNKNOWN_DROPS_GUARD_REASON"
	codeOutcomeEffectiveTimeFactMissing    = "OUTCOME_EFFECTIVE_TIME_FACT_MISSING"
	codeOutcomeEffectiveTimeUnknownMissed  = "OUTCOME_EFFECTIVE_TIME_UNKNOWN_MISSED"
	codeOutcomeTerminalDependencyMissed    = "OUTCOME_TERMINAL_DEPENDENCY_MISSED"
	codeOutcomeUnavailableDependencyBusine = "OUTCOME_UNAVAILABLE_DEPENDENCY_BUSINESS"
	codeProofOnFullOutcome                 = "PROOF_ON_FULL_OUTCOME"
	codeOutcomePartialNotBusinessClear     = "OUTCOME_PARTIAL_NORMAL_OR_RECOVERY"
	codeProofOnNonAbnormalPartial          = "PROOF_ON_NON_ABNORMAL_PARTIAL"
	codeProofCapabilityMissing             = "PROOF_CAPABILITY_MISSING"
	codeProofDuplicate                     = "PROOF_DUPLICATE"
	codeProofDoesNotCloseEvidence          = "PROOF_DOES_NOT_CLOSE_EVIDENCE"
	codeProofMissingForPartialInput        = "PROOF_MISSING_FOR_PARTIAL_INPUT"
	codeStateLoadedViewMissing             = "STATE_LOADED_VIEW_MISSING"
	codeLocalizedTerminalNotTerminal       = "LOCALIZED_TERMINAL_NOT_TERMINAL"
	codeLocalizedTerminalReasonDiffers     = "LOCALIZED_TERMINAL_REASON_DIFFERS"
	codeLocalizedQualityNotUnknown         = "LOCALIZED_QUALITY_NOT_UNKNOWN"
	codeLocalizedQualityReasonDiffers      = "LOCALIZED_QUALITY_REASON_DIFFERS"
	codeStateAnchorUnjustified             = "STATE_ANCHOR_UNJUSTIFIED"
	codeStateFactOutcomeMissing            = "STATE_FACT_OUTCOME_MISSING"
	codeStateFactContradictsOutcome        = "STATE_FACT_CONTRADICTS_OUTCOME"
	codeStateFactDuplicate                 = "STATE_FACT_DUPLICATE"
	codeStatePartialAbnormalDropsProven    = "STATE_PARTIAL_ABNORMAL_DROPS_PROVENANCE"
	codeStateFactMissingForOutcome         = "STATE_FACT_MISSING_FOR_OUTCOME"
	codeHistoryRetentionBoundMissing       = "HISTORY_RETENTION_BOUND_MISSING"
	codeHistoryLoadedPointChanged          = "HISTORY_LOADED_POINT_CHANGED"
	codeHistoryLoadedPointInvented         = "HISTORY_LOADED_POINT_CHANGED_OR_INVENTED"
	codeHistorySnapshotIncomplete          = "HISTORY_SNAPSHOT_INCOMPLETE"
	codeGuardMissingForPartialAbnormal     = "GUARD_MISSING_FOR_PARTIAL_ABNORMAL"
	codeGuardMissingForDegradedOutcome     = "GUARD_MISSING_FOR_DEGRADED_OUTCOME"
	codeGuardMissingPreEvent               = "GUARD_MISSING_PRE_EVENT"
	codeEventEffectiveTimeFactMissing      = "EVENT_EFFECTIVE_TIME_FACT_MISSING"
	codeEventDuplicate                     = "EVENT_DUPLICATE"
	codeEventKindMismatch                  = "EVENT_KIND_MISMATCH"
	codeEventLevelResultContradicts        = "EVENT_LEVEL_RESULT_CONTRADICTS_OUTCOME"
	codeEventOmitsSiblingOutcome           = "EVENT_OMITS_SIBLING_OUTCOME"
	codeEventEnvelopeCountInvalid          = "EVENT_ENVELOPE_COUNT_INVALID"
	codeEventMissingForBusinessOutcome     = "EVENT_MISSING_FOR_BUSINESS_OUTCOME"
)

// resultContractCodes is the closed vocabulary, in the order the rules appear.
var resultContractCodes = []string{
	codeOutcomePrimaryRecordMissing, codeOutcomeNotASelectedLevel, codeOutcomeDuplicate,
	codeOutcomeMissingForLevel, codeOutcomeIdentityIncomplete, codeOutcomeKindInvalid,
	codeOutcomeRetryableSeriesNotUnknown, codeOutcomeInvalidSeriesNotTerminal,
	codeOutcomeBusinessUnderActiveGuard, codeOutcomeUnknownDropsGuardReason,
	codeOutcomeEffectiveTimeFactMissing, codeOutcomeEffectiveTimeUnknownMissed,
	codeOutcomeTerminalDependencyMissed, codeOutcomeUnavailableDependencyBusine,
	codeProofOnFullOutcome, codeOutcomePartialNotBusinessClear, codeProofOnNonAbnormalPartial,
	codeProofCapabilityMissing, codeProofDuplicate, codeProofDoesNotCloseEvidence,
	codeProofMissingForPartialInput, codeStateLoadedViewMissing,
	codeLocalizedTerminalNotTerminal, codeLocalizedTerminalReasonDiffers,
	codeLocalizedQualityNotUnknown, codeLocalizedQualityReasonDiffers,
	codeStateAnchorUnjustified, codeStateFactOutcomeMissing, codeStateFactContradictsOutcome,
	codeStateFactDuplicate, codeStatePartialAbnormalDropsProven, codeStateFactMissingForOutcome,
	codeHistoryRetentionBoundMissing, codeHistoryLoadedPointChanged, codeHistoryLoadedPointInvented,
	codeHistorySnapshotIncomplete, codeGuardMissingForPartialAbnormal,
	codeGuardMissingForDegradedOutcome, codeGuardMissingPreEvent,
	codeEventEffectiveTimeFactMissing, codeEventDuplicate, codeEventKindMismatch,
	codeEventLevelResultContradicts, codeEventOmitsSiblingOutcome,
	codeEventEnvelopeCountInvalid, codeEventMissingForBusinessOutcome,
}

// ResultContractCodes returns the closed vocabulary of result contract refusal
// codes. A consumer that classifies these can be checked against this list
// rather than against its own copy of it: a copy agrees with the list on the
// day it is written and never afterwards, and nothing signals the day it stops.
func ResultContractCodes() []string {
	return append([]string(nil), resultContractCodes...)
}

// ResultContractError is a refusal by the result contract. It carries which
// rule refused, and nothing else: the identity of what was refused belongs to
// the observation, which already has it.
type ResultContractError struct {
	code string
	what string
}

// Code returns the refusing rule's code.
func (err *ResultContractError) Code() string { return err.code }

func (err *ResultContractError) Error() string { return "alarmd execution: " + err.what }

// QueryFailure names the failure for the query failure facts: the code is this
// error's own, the category is left to the stage that wraps it.
func (err *ResultContractError) QueryFailure() (string, string) { return "", err.code }

func resultContractViolation(code string, what string) error {
	return &ResultContractError{code: code, what: what}
}
