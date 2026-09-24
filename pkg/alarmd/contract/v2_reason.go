// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "sort"

type ReasonClassV2 string

const (
	ReasonClassDeterministic ReasonClassV2 = "DETERMINISTIC"
	ReasonClassRetryable     ReasonClassV2 = "RETRYABLE"
	ReasonClassCoverage      ReasonClassV2 = "COVERAGE"
)

type ReasonDomainsV2 uint32

const (
	ReasonDomainQueryResult ReasonDomainsV2 = 1 << iota
	ReasonDomainValidationIssue
	ReasonDomainReceipt
	ReasonDomainSummary
	ReasonDomainObservation
)

func (domains ReasonDomainsV2) Has(domain ReasonDomainsV2) bool {
	return domain != 0 && domains&domain == domain
}

type ReasonDefinitionV2 struct {
	Code    string
	Class   ReasonClassV2
	Domains ReasonDomainsV2
}

const (
	reasonOutcomeDomainsV2 = ReasonDomainValidationIssue | ReasonDomainReceipt | ReasonDomainObservation
	reasonMessageDomainsV2 = ReasonDomainReceipt | ReasonDomainObservation
	reasonQueryDomainsV2   = ReasonDomainQueryResult | ReasonDomainReceipt | ReasonDomainSummary | ReasonDomainObservation
)

var reasonCatalogV2 = map[string]ReasonDefinitionV2{
	ReasonMalformedJSON:              {ReasonMalformedJSON, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonSchemaMajorUnsupported:     {ReasonSchemaMajorUnsupported, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonRequiredFeatureUnsupported: {ReasonRequiredFeatureUnsupported, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonTenantInvalid:              {ReasonTenantInvalid, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonPayloadDigestMismatch:      {ReasonPayloadDigestMismatch, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonPlanSetConflict:            {ReasonPlanSetConflict, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonSelectorOrdinalInvalid:     {ReasonSelectorOrdinalInvalid, ReasonClassDeterministic, reasonMessageDomainsV2},
	ReasonMessageBudgetExceeded:      {ReasonMessageBudgetExceeded, ReasonClassDeterministic, reasonMessageDomainsV2},

	ReasonPlanInvalid: {ReasonPlanInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonMultipleEvaluationUnitsUnsupported: {
		ReasonMultipleEvaluationUnitsUnsupported, ReasonClassDeterministic, reasonOutcomeDomainsV2,
	},
	ReasonPlanDuplicateLevelID:   {ReasonPlanDuplicateLevelID, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonPlanBudgetExceeded:     {ReasonPlanBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonNoDataConfigInvalid:    {ReasonNoDataConfigInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonNoDataPlanUncompilable: {ReasonNoDataPlanUncompilable, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	// Deterministic, every one of them: the definition and the snapshot it is
	// compiled against are both frozen for the round, so the next attempt on
	// the same pair reaches the same answer. A snapshot that arrives later
	// makes a different pair, not a different verdict on this one.
	ReasonEffectiveTimeInvalid:               {ReasonEffectiveTimeInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeSnapshotInvalid:       {ReasonEffectiveTimeSnapshotInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeSnapshotStatusInvalid: {ReasonEffectiveTimeSnapshotStatusInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeSnapshotUnavailable:   {ReasonEffectiveTimeSnapshotUnavailable, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeSchemaUnsupported:     {ReasonEffectiveTimeSchemaUnsupported, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarsMissing:      {ReasonEffectiveTimeCalendarsMissing, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarMissing:       {ReasonEffectiveTimeCalendarMissing, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarNotPresent:    {ReasonEffectiveTimeCalendarNotPresent, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarIdentity:      {ReasonEffectiveTimeCalendarIdentity, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarDuplicate:     {ReasonEffectiveTimeCalendarDuplicate, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonEffectiveTimeCalendarItemsMissing:  {ReasonEffectiveTimeCalendarItemsMissing, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonCompilerTerminalUnclassified:       {ReasonCompilerTerminalUnclassified, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	// Deterministic for the same reason: the target's shape and the no-data
	// dimensions are both frozen, so every round would reach this answer again.
	ReasonNoDataRosterUnsupported:  {ReasonNoDataRosterUnsupported, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonBackendCapabilityMissing: {ReasonBackendCapabilityMissing, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonProjectionInvalid:        {ReasonProjectionInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonSelectorInvalid:          {ReasonSelectorInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLevelInvalid:             {ReasonLevelInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonAlgorithmUnsupported:     {ReasonAlgorithmUnsupported, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLevelBudgetExceeded:      {ReasonLevelBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonRecordInvalid:            {ReasonRecordInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonRecordIdentityConflict:   {ReasonRecordIdentityConflict, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonTimeInvalid:              {ReasonTimeInvalid, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonLateOutOfWindow:          {ReasonLateOutOfWindow, ReasonClassDeterministic, reasonOutcomeDomainsV2},
	ReasonValidationBudgetExceeded: {ReasonValidationBudgetExceeded, ReasonClassDeterministic, reasonOutcomeDomainsV2},

	ReasonRequiredValueMissing:             {ReasonRequiredValueMissing, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonRequiredValueTypeMismatch:        {ReasonRequiredValueTypeMismatch, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonRequiredValueNormalizationFailed: {ReasonRequiredValueNormalizationFailed, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},

	ReasonConfigDrift:      {ReasonConfigDrift, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonPlanReactivated:  {ReasonPlanReactivated, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryPartial:     {ReasonQueryPartial, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryEmpty:       {ReasonQueryEmpty, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryTimeout:     {ReasonQueryTimeout, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonQueryUnavailable: {ReasonQueryUnavailable, ReasonClassCoverage, reasonQueryDomainsV2},
	ReasonReadinessBudgetInvalid: {
		ReasonReadinessBudgetInvalid, ReasonClassCoverage, reasonQueryDomainsV2,
	},
	// Observation only: a deferral never reaches a receipt or a query result,
	// it just says the Slot will come back when its window is in.
	ReasonQueryNotReady: {ReasonQueryNotReady, ReasonClassRetryable, ReasonDomainObservation},
	// A Level may be unavailable for it, so it is a Receipt reason too. The
	// access layer hands it to every consumer of a query a recovery ran out of
	// time to send, and a consumer of a dependency query is a Level whose
	// primary query may well have answered: that Level has a record, makes a
	// Detect fact, and the fact carries this reason. Without the Receipt
	// domain the trigger refused that fact and failed the whole Slot with
	// TRIGGER_INVARIANT, on replays only, because only a recovery has a
	// deadline to run out of.
	ReasonExecutionBudgetExhausted: {
		ReasonExecutionBudgetExhausted, ReasonClassCoverage,
		ReasonDomainQueryResult | ReasonDomainReceipt | ReasonDomainObservation,
	},
	ReasonSnapshotUnavailable: {ReasonSnapshotUnavailable, ReasonClassCoverage, ReasonDomainObservation},
	ReasonGapSkipped:          {ReasonGapSkipped, ReasonClassCoverage, ReasonDomainObservation},
	ReasonSchedulePruned:      {ReasonSchedulePruned, ReasonClassCoverage, ReasonDomainObservation},
	// Coverage, like the pruned skip beside it: Slots passed without being
	// evaluated. Not deterministic, because nothing was refused - the active
	// set simply did not hold the Plan while they went by.
	ReasonPlanNotActive:         {ReasonPlanNotActive, ReasonClassCoverage, ReasonDomainObservation},
	ReasonEffectiveTimeInactive: {ReasonEffectiveTimeInactive, ReasonClassCoverage, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonEffectiveTimeUnknown:  {ReasonEffectiveTimeUnknown, ReasonClassCoverage, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonHistoryWarming:        {ReasonHistoryWarming, ReasonClassCoverage, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonHistoryGapped:         {ReasonHistoryGapped, ReasonClassCoverage, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonRecordTooLarge:        {ReasonRecordTooLarge, ReasonClassCoverage, ReasonDomainSummary | ReasonDomainObservation},
	ReasonAuditDrop:             {ReasonAuditDrop, ReasonClassCoverage, ReasonDomainObservation},

	ReasonKafkaUnavailable: {ReasonKafkaUnavailable, ReasonClassRetryable, ReasonDomainSummary | ReasonDomainObservation},
	ReasonRedisUnavailable: {ReasonRedisUnavailable, ReasonClassRetryable, ReasonDomainObservation},
	// Retryable: the Slot is retried by the scheduler, and a smaller read or a
	// quieter link can succeed. Retryable does not make it the dependency's
	// fault, which is why it has its own word.
	ReasonStateReadTimeout:  {ReasonStateReadTimeout, ReasonClassRetryable, ReasonDomainObservation},
	ReasonStateReadDeadline: {ReasonStateReadDeadline, ReasonClassRetryable, ReasonDomainObservation},
	// Deterministic: retrying reproduces it exactly. The Slot is over the share
	// every time until the strategy's shape changes, so calling it retryable
	// would have the scheduler back off and re-run it forever.
	ReasonQGBudgetShareExceeded: {ReasonQGBudgetShareExceeded, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonProviderUnavailable:   {ReasonProviderUnavailable, ReasonClassRetryable, ReasonDomainObservation},
	ReasonProgressBeginRejected: {ReasonProgressBeginRejected, ReasonClassRetryable, ReasonDomainObservation},
	ReasonProgressBeginFailed:   {ReasonProgressBeginFailed, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonActivationReadFailed:  {ReasonActivationReadFailed, ReasonClassRetryable, ReasonDomainObservation},
	// Retryable rather than deterministic: the record is absent now, and the
	// Control Leader's next round writes it back from the published Catalog.
	// Repeating the read is what finds it there.
	ReasonActivationMissing:          {ReasonActivationMissing, ReasonClassRetryable, ReasonDomainObservation},
	ReasonSnapshotRetryPending:       {ReasonSnapshotRetryPending, ReasonClassRetryable, ReasonDomainObservation},
	ReasonSlotSourceRetry:            {ReasonSlotSourceRetry, ReasonClassRetryable, ReasonDomainObservation},
	ReasonViewNotExecutable:          {ReasonViewNotExecutable, ReasonClassRetryable, ReasonDomainObservation},
	ReasonBlockedExactSetUnavailable: {ReasonBlockedExactSetUnavailable, ReasonClassDeterministic, ReasonDomainObservation},
	// Deterministic: the persisted marker and the proposed one are both facts,
	// and repeating the attempt compares the same two facts again.
	ReasonGapGuardConflict: {ReasonGapGuardConflict, ReasonClassDeterministic, ReasonDomainObservation},
	// Conflict and stale version are retryable rather than deterministic: both
	// say the marker moved, and re-reading it is what resolves them - which is
	// what the same-Slot retry was already doing before the refusals had
	// names. GAP_GUARD_CONFLICT above stays deterministic because it is this
	// Slot's own comparison, and re-running it reaches the same answer.
	// Deterministic: the same batches produce the same shape again. It is a
	// reading rather than a refusal, so nothing retries on its account.
	ReasonGapGuardDuplicatedAcrossBatches: {ReasonGapGuardDuplicatedAcrossBatches, ReasonClassDeterministic, ReasonDomainObservation},
	// Deterministic rather than retryable: the disagreement is between two
	// batches of this Slot's own evaluation, so the same Slot run again from
	// the same markers reaches it again. A retry would spend a round to be
	// refused identically.
	ReasonGapGuardDisagree:     {ReasonGapGuardDisagree, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonGapApplyConflict:     {ReasonGapApplyConflict, ReasonClassRetryable, ReasonDomainObservation},
	ReasonGapApplyStaleVersion: {ReasonGapApplyStaleVersion, ReasonClassRetryable, ReasonDomainObservation},
	ReasonGapWriteRetryable:    {ReasonGapWriteRetryable, ReasonClassRetryable, ReasonDomainObservation},
	ReasonStateVersionConflict: {ReasonStateVersionConflict, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonStateStaleVersion:    {ReasonStateStaleVersion, ReasonClassDeterministic, ReasonDomainObservation},
	// Deterministic: the Plan asks for more than this deployment has, and it
	// will ask for the same on every round until one of the two changes.
	ReasonSnapshotRetentionInsufficient: {
		ReasonSnapshotRetentionInsufficient, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonCompletionOffsetBelowReserve: {
		ReasonCompletionOffsetBelowReserve, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonResourceHardStop: {ReasonResourceHardStop, ReasonClassRetryable, ReasonDomainObservation},
	// One Slot's own State, Event or Gap output exceeds the per-Slot cap the
	// process can ever apply; the Slot completes deterministically. The code
	// is observation-only: Progress records the coverage completion reason.
	ReasonSlotBudgetExceeded: {ReasonSlotBudgetExceeded, ReasonClassCoverage, ReasonDomainObservation},
	ReasonOutputACKUnknown:   {ReasonOutputACKUnknown, ReasonClassRetryable, ReasonDomainObservation},
	// Deterministic output refusals decided in this process (see the codes):
	// they name a Slot's terminal completion, so they are receipt as well as
	// observation reasons, like the deterministic State refusals below.
	ReasonOutputConversionRejected: {ReasonOutputConversionRejected, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonOutputClientRejected:     {ReasonOutputClientRejected, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonOutputLeaseExpiring:      {ReasonOutputLeaseExpiring, ReasonClassRetryable, ReasonDomainObservation},
	ReasonStateWriteRetryable:      {ReasonStateWriteRetryable, ReasonClassRetryable, ReasonDomainObservation},
	ReasonStateCorrupt:             {ReasonStateCorrupt, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonStateSchemaUnsupported:   {ReasonStateSchemaUnsupported, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	ReasonStateBudgetExceeded:      {ReasonStateBudgetExceeded, ReasonClassDeterministic, ReasonDomainReceipt | ReasonDomainObservation},
	// Observation only, and deterministic: the same Plan meeting the same
	// stored record decides the same way, so a retry of the Slot is not what
	// resolves either of them. Not receipt reasons - no write was refused, the
	// evaluation was.
	ReasonStateLevelContractMismatch: {ReasonStateLevelContractMismatch, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonTriggerInvariant:           {ReasonTriggerInvariant, ReasonClassDeterministic, ReasonDomainObservation},
	// Ownership refusals. A stale fence, an assignment naming another worker
	// and a moved content scope are facts about the store the same attempt
	// would meet again; a lease held by another owner is the one that a later
	// attempt can find released.
	ReasonOwnershipStaleFence: {ReasonOwnershipStaleFence, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonOwnershipNotDesired: {ReasonOwnershipNotDesired, ReasonClassDeterministic, ReasonDomainObservation},
	ReasonOwnershipLeaseBusy:  {ReasonOwnershipLeaseBusy, ReasonClassRetryable, ReasonDomainObservation},
	ReasonContentScopeMoved:   {ReasonContentScopeMoved, ReasonClassDeterministic, ReasonDomainObservation},
}

func ReasonCatalogV2() []ReasonDefinitionV2 {
	definitions := make([]ReasonDefinitionV2, 0, len(reasonCatalogV2))
	for _, definition := range reasonCatalogV2 {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].Code < definitions[right].Code })
	return definitions
}

func LookupReasonV2(code string) (ReasonDefinitionV2, bool) {
	definition, ok := reasonCatalogV2[code]
	return definition, ok
}

func IsKnownReasonV2(code string) bool {
	_, ok := LookupReasonV2(code)
	return ok
}

func ReasonAllowedForV2(code string, domain ReasonDomainsV2) bool {
	definition, ok := LookupReasonV2(code)
	return ok && definition.Domains.Has(domain)
}

// LevelUnavailableReasonV2 says whether a Level may be UNAVAILABLE for this
// reason: whether a Detect fact may carry it into the trigger, and so into
// the Level's outcome and the gap guard it proposes.
//
// A Detect fact's reason has two ways in. A detector declares the reasons it
// may fail with, and the compiler refuses a declaration outside this set; an
// input binding carries the reason the access layer gave it, and nothing
// checked those until the trigger did, at run time, by failing the Slot. One
// predicate for both, so the compiler, the trigger and a test of what the
// access layer stamps all ask the same question.
func LevelUnavailableReasonV2(code string) bool {
	return ReasonAllowedForV2(code, ReasonDomainReceipt|ReasonDomainObservation)
}
