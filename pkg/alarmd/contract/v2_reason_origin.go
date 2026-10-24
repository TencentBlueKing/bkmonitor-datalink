// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

// ReasonSourceV2 is where a reason code comes from when a round names it as
// its own (not carried from a stored guard): what the round met, or what
// alarmd decided.
type ReasonSourceV2 string

const (
	// ReasonSourceMet is a reason the round met: an answer from outside
	// alarmd - the query provider, the State or control store, Kafka, the
	// output client, an inbound message - or its own clock or measured size
	// running past a limit.
	ReasonSourceMet ReasonSourceV2 = "met"
	// ReasonSourceJudged is alarmd's own judgement, decided from the
	// strategy's configuration, the schedule or what State already holds,
	// with nothing met this round: an effective-time window, a warming
	// history, a span given up on, a configuration refused.
	ReasonSourceJudged ReasonSourceV2 = "judged"
)

// reasonSourcesV2 classifies every reason in the catalogue. A reason added
// to the catalogue without a line here fails its test: one left out would
// read as met by default, which is how an out-of-hours round came to read
// as a physical failure with a lost round's facts beside it.
var reasonSourcesV2 = map[string]ReasonSourceV2{
	// Inbound messages: the bytes the round received.
	ReasonMalformedJSON:              ReasonSourceMet,
	ReasonSchemaMajorUnsupported:     ReasonSourceMet,
	ReasonRequiredFeatureUnsupported: ReasonSourceMet,
	ReasonTenantInvalid:              ReasonSourceMet,
	ReasonPayloadDigestMismatch:      ReasonSourceMet,
	ReasonPlanSetConflict:            ReasonSourceMet,
	ReasonSelectorOrdinalInvalid:     ReasonSourceMet,
	ReasonMessageBudgetExceeded:      ReasonSourceMet,
	// Compiling the strategy's configuration.
	ReasonPlanInvalid:                        ReasonSourceJudged,
	ReasonMultipleEvaluationUnitsUnsupported: ReasonSourceJudged,
	ReasonPlanDuplicateLevelID:               ReasonSourceJudged,
	ReasonPlanBudgetExceeded:                 ReasonSourceJudged,
	ReasonNoDataConfigInvalid:                ReasonSourceJudged,
	ReasonNoDataPlanUncompilable:             ReasonSourceJudged,
	ReasonEffectiveTimeInvalid:               ReasonSourceJudged,
	ReasonEffectiveTimeSnapshotInvalid:       ReasonSourceJudged,
	ReasonEffectiveTimeSnapshotStatusInvalid: ReasonSourceJudged,
	ReasonEffectiveTimeSnapshotUnavailable:   ReasonSourceJudged,
	ReasonEffectiveTimeSchemaUnsupported:     ReasonSourceJudged,
	ReasonEffectiveTimeCalendarsMissing:      ReasonSourceJudged,
	ReasonEffectiveTimeCalendarMissing:       ReasonSourceJudged,
	ReasonEffectiveTimeCalendarNotPresent:    ReasonSourceJudged,
	ReasonEffectiveTimeCalendarIdentity:      ReasonSourceJudged,
	ReasonEffectiveTimeCalendarDuplicate:     ReasonSourceJudged,
	ReasonEffectiveTimeCalendarItemsMissing:  ReasonSourceJudged,
	ReasonEffectiveTimeItemDuplicate:         ReasonSourceJudged,
	ReasonEffectiveTimeItemInvalid:           ReasonSourceJudged,
	ReasonEffectiveTimeItemTimeInvalid:       ReasonSourceJudged,
	ReasonEffectiveTimeTimeKindInvalid:       ReasonSourceJudged,
	ReasonEffectiveTimeTimezoneInvalid:       ReasonSourceJudged,
	ReasonEffectiveTimeRepeatInvalid:         ReasonSourceJudged,
	ReasonEffectiveTimeRepeatListInvalid:     ReasonSourceJudged,
	ReasonEffectiveTimeRepeatEveryInvalid:    ReasonSourceJudged,
	ReasonEffectiveTimeRepeatUntilInvalid:    ReasonSourceJudged,
	ReasonCompilerTerminalUnclassified:       ReasonSourceJudged,
	ReasonNoDataRosterUnsupported:            ReasonSourceJudged,
	ReasonProjectionInvalid:                  ReasonSourceJudged,
	ReasonSelectorInvalid:                    ReasonSourceJudged,
	ReasonLevelInvalid:                       ReasonSourceJudged,
	ReasonAlgorithmUnsupported:               ReasonSourceJudged,
	ReasonLevelBudgetExceeded:                ReasonSourceJudged,
	ReasonDetectIntervalStorageNotSliding:    ReasonSourceJudged,
	ReasonReadinessBudgetInvalid:             ReasonSourceJudged,
	ReasonSnapshotRetentionInsufficient:      ReasonSourceJudged,
	ReasonCompletionOffsetBelowReserve:       ReasonSourceJudged,
	// The records a query returned.
	ReasonRecordInvalid:                    ReasonSourceMet,
	ReasonRecordIdentityConflict:           ReasonSourceMet,
	ReasonTimeInvalid:                      ReasonSourceMet,
	ReasonLateOutOfWindow:                  ReasonSourceMet,
	ReasonValidationBudgetExceeded:         ReasonSourceMet,
	ReasonRequiredValueMissing:             ReasonSourceMet,
	ReasonRequiredValueTypeMismatch:        ReasonSourceMet,
	ReasonRequiredValueNormalizationFailed: ReasonSourceMet,
	// The query itself.
	ReasonQueryPartial:         ReasonSourceMet,
	ReasonQueryEmpty:           ReasonSourceMet,
	ReasonQueryTimeout:         ReasonSourceMet,
	ReasonQueryUnavailable:     ReasonSourceMet,
	ReasonQueryRejected:        ReasonSourceMet,
	ReasonQueryDeliveryTimeout: ReasonSourceMet,
	ReasonQueryTargetMissing:   ReasonSourceMet,
	// The answer of one of its named outputs, which cannot be aligned.
	ReasonQueryNamedOutputDuplicateKey: ReasonSourceMet,
	ReasonProviderUnavailable:          ReasonSourceMet,
	// The round's own clock or measured size past a limit.
	ReasonExecutionBudgetExhausted: ReasonSourceMet,
	ReasonQGBudgetShareExceeded:    ReasonSourceMet,
	ReasonResourceHardStop:         ReasonSourceMet,
	ReasonSlotBudgetExceeded:       ReasonSourceMet,
	ReasonStateBudgetExceeded:      ReasonSourceMet,
	ReasonRecordTooLarge:           ReasonSourceMet,
	ReasonOutputLeaseExpiring:      ReasonSourceMet,
	// The schedule, the configuration in force and what State holds.
	ReasonConfigDrift:              ReasonSourceJudged,
	ReasonPlanReactivated:          ReasonSourceJudged,
	ReasonQueryNotReady:            ReasonSourceJudged,
	ReasonGapSkipped:               ReasonSourceJudged,
	ReasonSchedulePruned:           ReasonSourceJudged,
	ReasonPlanNotActive:            ReasonSourceJudged,
	ReasonScheduleRepaired:         ReasonSourceJudged,
	ReasonScheduleReopened:         ReasonSourceJudged,
	ReasonEffectiveTimeInactive:    ReasonSourceJudged,
	ReasonEffectiveTimeUnknown:     ReasonSourceJudged,
	ReasonHistoryWarming:           ReasonSourceJudged,
	ReasonHistoryGapped:            ReasonSourceJudged,
	ReasonViewNotExecutable:        ReasonSourceJudged,
	ReasonSupplementKeptReadAbsent: ReasonSourceJudged,
	// alarmd's own contract checks on what it produced.
	ReasonGapGuardConflict:                ReasonSourceJudged,
	ReasonGapGuardDuplicatedAcrossBatches: ReasonSourceJudged,
	ReasonGapGuardDisagree:                ReasonSourceJudged,
	ReasonStateLevelContractMismatch:      ReasonSourceJudged,
	ReasonTriggerInvariant:                ReasonSourceJudged,
	ReasonOutputConversionRejected:        ReasonSourceJudged,
	// The stores, Kafka and the output client. A snapshot unavailable is,
	// in all but one of its producers, the control store's answer (missing,
	// corrupt or unresolvable past the bound); a backend capability missing
	// is the State store answering that Redis lacks the lifetime command.
	ReasonSnapshotUnavailable:        ReasonSourceMet,
	ReasonBackendCapabilityMissing:   ReasonSourceMet,
	ReasonAuditDrop:                  ReasonSourceMet,
	ReasonKafkaUnavailable:           ReasonSourceMet,
	ReasonRedisUnavailable:           ReasonSourceMet,
	ReasonStateReadTimeout:           ReasonSourceMet,
	ReasonStateReadDeadline:          ReasonSourceMet,
	ReasonProgressBeginRejected:      ReasonSourceMet,
	ReasonProgressBeginFailed:        ReasonSourceMet,
	ReasonActivationReadFailed:       ReasonSourceMet,
	ReasonActivationMissing:          ReasonSourceMet,
	ReasonSnapshotRetryPending:       ReasonSourceMet,
	ReasonSlotSourceRetry:            ReasonSourceMet,
	ReasonBlockedExactSetUnavailable: ReasonSourceMet,
	ReasonScheduleUnreadable:         ReasonSourceMet,
	ReasonScheduleMissing:            ReasonSourceMet,
	ReasonGapApplyConflict:           ReasonSourceMet,
	ReasonGapApplyStaleVersion:       ReasonSourceMet,
	ReasonGapWriteRetryable:          ReasonSourceMet,
	ReasonStateVersionConflict:       ReasonSourceMet,
	ReasonStateStaleVersion:          ReasonSourceMet,
	ReasonStateWriteRetryable:        ReasonSourceMet,
	ReasonStateCorrupt:               ReasonSourceMet,
	ReasonStateSchemaUnsupported:     ReasonSourceMet,
	ReasonOutputACKUnknown:           ReasonSourceMet,
	ReasonOutputClientRejected:       ReasonSourceMet,
	ReasonOwnershipStaleFence:        ReasonSourceMet,
	ReasonOwnershipNotDesired:        ReasonSourceMet,
	ReasonOwnershipLeaseBusy:         ReasonSourceMet,
	ReasonContentScopeMoved:          ReasonSourceMet,
	// The CMDB caches' answer for the Plan's target: members dropped, nodes
	// missing, a cache unreadable.
	ReasonTargetResolutionIncomplete:  ReasonSourceMet,
	ReasonTargetResolutionUnavailable: ReasonSourceMet,
}

// ReasonJudgedV2 says a reason is alarmd's own judgement, not something a
// round met. A code outside the catalogue is not judged.
func ReasonJudgedV2(code string) bool {
	return reasonSourcesV2[code] == ReasonSourceJudged
}
