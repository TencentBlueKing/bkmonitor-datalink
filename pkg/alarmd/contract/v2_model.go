// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "encoding/json"

const (
	ExecutionEnvelopeSchemaV2 = "execution-envelope"
	StrategyIRSchemaV2        = "alarmd-strategy-ir"
	TriggerEventSchemaV1      = "trigger-event"
	ExecutionSummarySchemaV1  = "execution-summary"

	QueryCompletenessFull        = "FULL"
	QueryCompletenessPartial     = "PARTIAL"
	QueryCompletenessUnavailable = "UNAVAILABLE"

	EvaluationScopeSeries      = "SERIES"
	EvaluationScopeCrossSeries = "CROSS_SERIES"

	MissingValuePolicyRequired = "REQUIRED_VALUE"

	LevelConnectorAND = "AND"
	LevelConnectorOR  = "OR"

	SelectorKindRanges = "RANGES"
	SelectorKindBitmap = "BITMAP"

	ValidationScopePlan   ValidationScope = "PLAN"
	ValidationScopeLevel  ValidationScope = "LEVEL"
	ValidationScopeRecord ValidationScope = "RECORD"

	ReasonMalformedJSON              = "MALFORMED_JSON"
	ReasonSchemaMajorUnsupported     = "SCHEMA_MAJOR_UNSUPPORTED"
	ReasonRequiredFeatureUnsupported = "REQUIRED_FEATURE_UNSUPPORTED"
	ReasonTenantInvalid              = "TENANT_INVALID"
	ReasonPayloadDigestMismatch      = "PAYLOAD_DIGEST_MISMATCH"
	ReasonPlanSetConflict            = "PLAN_SET_CONFLICT"
	ReasonSelectorOrdinalInvalid     = "SELECTOR_ORDINAL_INVALID"
	ReasonMessageBudgetExceeded      = "MESSAGE_BUDGET_EXCEEDED"
	ReasonPlanInvalid                = "PLAN_INVALID"
	ReasonPlanDuplicateLevelID       = "PLAN_DUPLICATE_LEVEL_ID"
	ReasonPlanBudgetExceeded         = "PLAN_BUDGET_EXCEEDED"
	ReasonNoDataConfigInvalid        = "NO_DATA_CONFIG_INVALID"
	// ReasonNoDataPlanUncompilable names the shape the config layer cannot
	// see: a no-data setting that passes validation and then runs its trigger
	// window past a compile limit. It refuses the whole definition, where
	// NO_DATA_CONFIG_INVALID leaves the strategy detecting its thresholds and
	// suspends only its absence detection - which is why the two cannot share
	// a code. A reader meeting one has a strategy that detects nothing; a
	// reader meeting the other has a strategy that detects.
	ReasonNoDataPlanUncompilable = "NO_DATA_PLAN_UNCOMPILABLE"
	// The reasons a Plan's effective time refuses to compile. They are
	// declared here, with every other code a reader can meet, because a code
	// that exists only as a literal inside the compiler is one nothing
	// downstream can be written against: the catalog classifies a terminal by
	// its code, and the first of these to reach a deployment took every config
	// refresh with it.
	//
	// EFFECTIVE_TIME_INVALID is the definition's own window. The SNAPSHOT_ and
	// CALENDAR_ ones are about the snapshot the source hands over: whether it
	// arrived, whether this build can read it, and whether it carries the
	// calendars the definition names.
	ReasonEffectiveTimeInvalid               = "EFFECTIVE_TIME_INVALID"
	ReasonEffectiveTimeSnapshotInvalid       = "EFFECTIVE_TIME_SNAPSHOT_INVALID"
	ReasonEffectiveTimeSnapshotStatusInvalid = "EFFECTIVE_TIME_SNAPSHOT_STATUS_INVALID"
	ReasonEffectiveTimeSnapshotUnavailable   = "EFFECTIVE_TIME_SNAPSHOT_UNAVAILABLE"
	ReasonEffectiveTimeSchemaUnsupported     = "EFFECTIVE_TIME_SCHEMA_UNSUPPORTED"
	ReasonEffectiveTimeCalendarsMissing      = "EFFECTIVE_TIME_CALENDARS_MISSING"
	ReasonEffectiveTimeCalendarMissing       = "EFFECTIVE_TIME_CALENDAR_MISSING"
	ReasonEffectiveTimeCalendarNotPresent    = "EFFECTIVE_TIME_CALENDAR_NOT_PRESENT"
	ReasonEffectiveTimeCalendarIdentity      = "EFFECTIVE_TIME_CALENDAR_IDENTITY_INVALID"
	ReasonEffectiveTimeCalendarDuplicate     = "EFFECTIVE_TIME_CALENDAR_DUPLICATE"
	ReasonEffectiveTimeCalendarItemsMissing  = "EFFECTIVE_TIME_CALENDAR_ITEMS_MISSING"
	// ReasonCompilerTerminalUnclassified files a compiler terminal this build
	// has no classification for. It is declared here so the tables that walk
	// the catalogue can see it; the compiler's own code travels beside it.
	ReasonCompilerTerminalUnclassified = "COMPILER_TERMINAL_UNCLASSIFIED"
	// ReasonNoDataRosterUnsupported names an item whose target shape this
	// build cannot turn into an expected set. Like the one above it, it
	// suspends that Plan's no-data detection and nothing else: the strategy's
	// thresholds are compiled and detected either way.
	ReasonNoDataRosterUnsupported          = "NO_DATA_ROSTER_UNSUPPORTED"
	ReasonBackendCapabilityMissing         = "BACKEND_CAPABILITY_MISSING"
	ReasonProjectionInvalid                = "PROJECTION_INVALID"
	ReasonSelectorInvalid                  = "SELECTOR_INVALID"
	ReasonLevelInvalid                     = "LEVEL_INVALID"
	ReasonAlgorithmUnsupported             = "ALGORITHM_UNSUPPORTED"
	ReasonLevelBudgetExceeded              = "LEVEL_BUDGET_EXCEEDED"
	ReasonRecordInvalid                    = "RECORD_INVALID"
	ReasonRecordIdentityConflict           = "RECORD_IDENTITY_CONFLICT"
	ReasonRecordTooLarge                   = "RECORD_TOO_LARGE"
	ReasonTimeInvalid                      = "TIME_INVALID"
	ReasonLateOutOfWindow                  = "LATE_OUT_OF_WINDOW"
	ReasonValidationBudgetExceeded         = "VALIDATION_BUDGET_EXCEEDED"
	ReasonRequiredValueMissing             = "REQUIRED_VALUE_MISSING"
	ReasonRequiredValueTypeMismatch        = "REQUIRED_VALUE_TYPE_MISMATCH"
	ReasonRequiredValueNormalizationFailed = "REQUIRED_VALUE_NORMALIZATION_FAILED"
	ReasonConfigDrift                      = "CONFIG_DRIFT"
	ReasonQueryPartial                     = "QUERY_PARTIAL"
	// ReasonQueryEmpty names a dependency query that completed and returned
	// no rows at all. The query succeeded, so the binding carries no reason of
	// its own; this is the one the guard for the Level it starves carries.
	ReasonQueryEmpty             = "QUERY_EMPTY"
	ReasonQueryTimeout           = "QUERY_TIMEOUT"
	ReasonQueryUnavailable       = "QUERY_UNAVAILABLE"
	ReasonReadinessBudgetInvalid = "READINESS_BUDGET_INVALID"
	// ReasonQueryNotReady names a Slot deferred because the window it would
	// query is not in yet. It is the normal pacing of every Slot, and the
	// highest-volume observation alarmd makes, so it needs its own name:
	// without one it normalizes to internal_unknown and reads as a fault.
	ReasonQueryNotReady            = "QUERY_NOT_READY"
	ReasonExecutionBudgetExhausted = "EXECUTION_BUDGET_EXHAUSTED"
	ReasonSnapshotUnavailable      = "SNAPSHOT_UNAVAILABLE"
	ReasonGapSkipped               = "GAP_SKIPPED"
	// ReasonSchedulePruned names a Progress cursor moved past a part of the
	// Schedule timeline that was pruned before the cursor could be evaluated.
	// The skipped Slots were never observed, which is a coverage fact.
	ReasonSchedulePruned = "SCHEDULE_PRUNED"
	// ReasonPlanNotActive names Slots the cursor moved past because no Plan was
	// due at them: the schedule held those times and the timeline still does,
	// but the Plan had left the activation and came back, so for that stretch
	// there was nothing to run.
	//
	// Separate from SCHEDULE_PRUNED, which it used to arrive as, because the
	// two send a reader to opposite places. Pruned means the times are gone
	// from the timeline and no read will ever find them - a retention answer.
	// This means the times are there and the Plan was not, which is a question
	// about the active set, and it reads as data loss when it is not: the
	// Slots are not replayed on purpose, because replaying them would produce
	// alerts for a strategy that did not exist while they passed.
	//
	// The Slot the stretch ends at - the one that ran with the Plan back -
	// carries PLAN_REACTIVATED below: the other half of this skip. The two
	// words tell one story at its two Slots, and neither is the whole of it.
	ReasonPlanNotActive = "PLAN_NOT_ACTIVE"
	// ReasonPlanReactivated names a Slot that ran while its Plan's activation
	// changed under it with the Plan itself unchanged: the same identity,
	// schedule revision and state generation, only the activation epoch
	// moved, which is a Plan that left the active set and came back - the
	// Slot after a PLAN_NOT_ACTIVE stretch. The Slot completes as a partial
	// gap the way CONFIG_DRIFT does, and is told apart from it because the
	// two send a reader to different places: drift is an edit someone made
	// and the next Slot runs under the new selection; this is the same
	// selection returning, and the stretch before it is the PLAN_NOT_ACTIVE
	// skip, not something to look for in the strategy.
	ReasonPlanReactivated       = "PLAN_REACTIVATED"
	ReasonEffectiveTimeInactive = "EFFECTIVE_TIME_INACTIVE"
	ReasonEffectiveTimeUnknown  = "EFFECTIVE_TIME_UNKNOWN"
	ReasonHistoryWarming        = "HISTORY_WARMING"
	ReasonHistoryGapped         = "HISTORY_GAPPED"
	ReasonKafkaUnavailable      = "KAFKA_UNAVAILABLE"
	ReasonRedisUnavailable      = "REDIS_UNAVAILABLE"
	// ReasonStateReadTimeout names a Runtime State read this process issued
	// that did not come back inside its own timeout. It is not
	// REDIS_UNAVAILABLE, and the difference is the whole point: the dependency
	// answered every other caller on the same connection that second. What
	// happened is that one read of ours was too big to finish in the time we
	// gave it, which is our shape to fix and not the dependency's health.
	//
	// Named because the refusal it replaced sent every reader to the wrong
	// place. The state read that produced it was 86 MB for a single Query
	// Group, it timed out identically on every attempt, and it arrived in the
	// fleet view as a Redis outage - so the investigation began at a
	// dependency that was fine, while the row carried nothing about how much
	// had been asked for.
	ReasonStateReadTimeout = "STATE_READ_TIMEOUT"
	// ReasonStateReadDeadline names a Runtime State read that was still in
	// flight when a deadline on the call expired, rather than one the
	// connection gave up on.
	//
	// Split from STATE_READ_TIMEOUT because the two have different fixes and
	// one word could not tell them apart. The connection's own read timeout
	// fires when a reply is too large to arrive in the time the client allows
	// a single command; a deadline on the context fires when the work above
	// this read has already spent the time the Slot had. The first is fixed by
	// reading less per call, the second by what the Slot spent before it got
	// here -- and a build that called both STATE_READ_TIMEOUT sent every
	// reader to the first.
	//
	// The reading that forced the split: one Query Group timed out the same
	// way at 85.9 MB per round and again at 1.75 MB, after the stored
	// representation changed from 344,206 to 7,049 bytes a record. At the
	// first size the connection timeout is a sufficient explanation -- it
	// needs 28.6 MB/s to land inside three seconds. At the second it is not:
	// 0.58 MB/s, against a store answering every other caller that second.
	// Something other than the byte volume ends these reads, and one word
	// could not say so.
	//
	// A cancelled call is not this. Cancellation is the work above being
	// stopped -- a replica shutting down, a sibling batch's failure bringing
	// the parent context with it -- and not the time running out, so it keeps
	// the dependency's word rather than taking a third meaning into this one.
	// The word lands on a defect row, and a deployment that ships several
	// times a day would file one per replica per release for doing exactly
	// what it was told.
	ReasonStateReadDeadline = "STATE_READ_DEADLINE"
	// ReasonQGBudgetShareExceeded names one Query Group's Slot asking for more
	// of the process pool than any single object may hold.
	//
	// Separate from RESOURCE_HARD_STOP because the two call for different work
	// and one word made them indistinguishable. A hard stop is somebody else
	// having filled the pool: this Slot unwinds and the next attempt succeeds
	// once capacity frees. This is the object being too large for one replica
	// whoever else is running - waiting changes nothing, and what has to change
	// is the strategy's shape.
	//
	// Without a share at all, one object may legitimately take the whole pool
	// and starve every other Query Group on the replica. Placement spreads
	// large objects across replicas; nothing stops one from filling the replica
	// it lands on.
	ReasonQGBudgetShareExceeded = "QG_BUDGET_SHARE_EXCEEDED"
	ReasonProviderUnavailable   = "PROVIDER_UNAVAILABLE"
	ReasonProgressBeginRejected = "PROGRESS_BEGIN_REJECTED"
	ReasonProgressBeginFailed   = "PROGRESS_BEGIN_FAILED"
	ReasonActivationReadFailed  = "ACTIVATION_READ_FAILED"
	// ReasonActivationMissing names a control round that found no activation
	// record at all. It is separate from ACTIVATION_READ_FAILED because the
	// store answered: there is no record, rather than no answer, and the two
	// call for different work. A failed read is retried; a missing record is
	// rebuilt from the published Catalog by whichever replica holds the
	// Control Leader, and until one does, no replica can learn which Query
	// Groups exist.
	ReasonActivationMissing          = "ACTIVATION_MISSING"
	ReasonSnapshotRetryPending       = "SNAPSHOT_RETRY_PENDING"
	ReasonSlotSourceRetry            = "SLOT_SOURCE_RETRY"
	ReasonBlockedExactSetUnavailable = "BLOCKED_EXACT_SET_UNAVAILABLE"
	// ReasonViewNotExecutable names a round the Worker did not run because
	// its installed executable view does not yet agree with the Assignment
	// record on the Query Group's content or timeline, or does not carry it
	// (decision-016 batch 4b). The record's word arrives by renewal and the
	// view's by delta, so the next round asks again; a Worker held here past
	// the view's propagation delay is one the stream is not reaching.
	ReasonViewNotExecutable = "VIEW_NOT_EXECUTABLE"
	// ReasonGapGuardConflict names a Slot refused because the Plan gap marker
	// already persisted for its ApplyVersion neither matches what this Slot
	// proposes nor already protects it. Without a name of its own the refusal
	// left the attempt reading as an unclassified internal error, on every
	// round, for a Query Group that would never get past it.
	ReasonGapGuardConflict = "GAP_GUARD_CONFLICT"
	// The gap marker store's three refusals at apply time, named apart from
	// GAP_GUARD_CONFLICT above. That one is this Slot comparing the persisted
	// marker against what it proposes and refusing before it writes; these are
	// the store refusing the write itself, which means the marker moved
	// between this Slot's read and its write - a different question with a
	// different answer, because it names a second writer rather than a
	// disagreement this Slot could see on its own.
	//
	// Until these existed all three returned a bare error, so every one of
	// them was observed as internal_unknown: an unclassified defect that a
	// same-Slot retry then "recovered" from, which is how a Query Group
	// conflicting on every other Slot for half an hour read as healthy.
	// Observation-only; the store's statuses and the retry decision are
	// unchanged.
	// ReasonGapGuardDuplicatedAcrossBatches names one Plan carrying more than
	// one gap marker statement in a single Slot, which the evaluation contract
	// forbids and the accumulation across series batches can nonetheless
	// assemble: EvaluationResult.Validate runs on each batch's result, and
	// appendProvisional then appends to the two guard lists independently, so
	// two batches contributing one statement each produce a shape no single
	// batch could. Recorded rather than refused, so the shape can be counted
	// before anything is changed on its account.
	ReasonGapGuardDuplicatedAcrossBatches = "GAP_GUARD_DUPLICATED_ACROSS_BATCHES"
	// ReasonGapGuardDisagree names two series batches of one Slot saying
	// different things about one Plan's gap marker: clearing it with different
	// content, or expecting it at different revisions.
	//
	// It is the one refusal the Slot-wide merge cannot resolve. A clear is
	// derived from the marker the Slot loaded rather than from the batch, so
	// every batch that proposes one proposes the same one; two that differ
	// mean two batches read different markers for one Plan in one Slot, and
	// there is no winner to pick - whichever were kept, the other batch's
	// series were evaluated against a marker the Slot then denies.
	//
	// Named because it reaches the completion line, and a refusal with no word
	// arrives there as an error nobody can group, count, or tell apart from
	// the next unnamed one.
	ReasonGapGuardDisagree     = "GAP_GUARD_DISAGREE"
	ReasonGapApplyConflict     = "GAP_APPLY_CONFLICT"
	ReasonGapApplyStaleVersion = "GAP_APPLY_STALE_VERSION"
	ReasonGapWriteRetryable    = "GAP_WRITE_RETRYABLE"
	// State version refusals are observation-only names; they do not change
	// the state store's status contract or the scheduler's retry decision.
	ReasonStateVersionConflict = "STATE_VERSION_CONFLICT"
	ReasonStateStaleVersion    = "STATE_STALE_VERSION"
	// ReasonSnapshotRetentionInsufficient names a Plan whose recovery
	// contract needs a Snapshot kept longer than this deployment retains one.
	// The retention is the deployment's capacity and does not follow a Plan, so
	// the Plan is what gives way -- but only that Plan.
	ReasonSnapshotRetentionInsufficient = "SNAPSHOT_RETENTION_INSUFFICIENT"
	// ReasonCompletionOffsetBelowReserve names a Plan whose completion deadline
	// does not clear the downstream execution reserve, leaving its queries no
	// time to run in.
	ReasonCompletionOffsetBelowReserve = "COMPLETION_OFFSET_BELOW_RESERVE"
	ReasonResourceHardStop             = "RESOURCE_HARD_STOP"
	ReasonSlotBudgetExceeded           = "SLOT_BUDGET_EXCEEDED"
	ReasonOutputACKUnknown             = "OUTPUT_ACK_UNKNOWN"
	// ReasonOutputConversionRejected: the output converter would not write a
	// decision (no frozen strategy revision, no series identity, no primary
	// level, a business identity that is not a number, ...). Decided in this
	// process from the decision's own content, so the same decision meets the
	// same refusal on every round: the Plan completes terminally by this name,
	// its sibling Plans run, and nothing waits on a broker.
	ReasonOutputConversionRejected = "OUTPUT_CONVERSION_REJECTED"
	// ReasonOutputClientRejected: the Kafka client refused a message before any
	// broker saw it -- a protocol version too old for the record's headers, a
	// message over the client's own size cap. This deployment's wiring, not
	// the broker's weather: a retry sends the same message to the same client
	// and gets the same answer.
	ReasonOutputClientRejected = "OUTPUT_CLIENT_REJECTED"
	// ReasonOutputLeaseExpiring: an output batch was not started because the
	// Slot's lease has less life left than one batch needs to land
	// (decision-016 per-batch admission). Nothing was sent; the Plan waits,
	// and the Slot retries after the next renewal or ends with the lease.
	// Named apart from OUTPUT_ACK_UNKNOWN because no broker was asked.
	ReasonOutputLeaseExpiring    = "OUTPUT_LEASE_EXPIRING"
	ReasonStateWriteRetryable    = "STATE_WRITE_RETRYABLE"
	ReasonStateCorrupt           = "STATE_CORRUPT"
	ReasonStateSchemaUnsupported = "STATE_SCHEMA_UNSUPPORTED"
	ReasonStateBudgetExceeded    = "STATE_BUDGET_EXCEEDED"
	// Two evaluation failures that had no observation word, so the line they
	// reach classified them as internal_unknown -- the word for a site that
	// looked at a failure and could not name it. Both already named themselves
	// one level down and the classification simply did not ask.
	//
	// STATE_LEVEL_CONTRACT_MISMATCH is loaded Runtime State whose Level
	// contract is not the compiled Plan's. It is the word the query failure
	// facts already carry for it, reused rather than a second one invented:
	// the same failure counted under two names on two lines is a failure a
	// reader cannot add up.
	ReasonStateLevelContractMismatch = "STATE_LEVEL_CONTRACT_MISMATCH"
	// TRIGGER_INVARIANT is the trigger evaluator refusing its own state: an
	// invariant it checks before deciding did not hold. Which operation found
	// it travels as a field rather than in the word, because the word is what
	// a reader groups by and one invariant per word would make a vocabulary
	// nobody can hold.
	ReasonTriggerInvariant = "TRIGGER_INVARIANT"
	ReasonAuditDrop        = "AUDIT_DROP"
	// Ownership refusals, observation-only. The ownership store answers a
	// fence check, a lease acquire or renew, or a fenced write with one of
	// four typed errors; until these names existed every one of them was
	// observed as internal_unknown, and which of the four a deployment was
	// seeing -- a fence gone stale, a Query Group assigned elsewhere, a lease
	// held by another worker, or a content scope that moved under a write --
	// could only be told apart by reading the error sentence off a rate-limited
	// log line. They are names for the observation; the store's error values
	// and the callers' retry decisions do not change.
	ReasonOwnershipStaleFence = "OWNERSHIP_STALE_FENCE"
	ReasonOwnershipNotDesired = "OWNERSHIP_NOT_DESIRED"
	ReasonOwnershipLeaseBusy  = "OWNERSHIP_LEASE_BUSY"
	ReasonContentScopeMoved   = "CONTENT_SCOPE_MOVED"

	CompatibilityModeLegacyGroupOfOne = "LEGACY_GROUP_OF_ONE"

	DetectPlanFingerprintDomainV1   = "detect-plan-fingerprint-v1"
	TriggerStateFingerprintDomainV1 = "trigger-state-fingerprint-v1"

	TriggerEventAbnormal = "ABNORMAL"
	TriggerEventRecovery = "RECOVERY"

	LevelResultAbnormal    = "ABNORMAL"
	LevelResultNormal      = "NORMAL"
	LevelResultRecovery    = "RECOVERY"
	LevelResultUnavailable = "UNAVAILABLE"
)

const ReasonMultipleEvaluationUnitsUnsupported = "MULTIPLE_EVALUATION_UNITS_UNSUPPORTED"

// StrategyRefV2 is the producer-side strategy identity. Runtime state identity
// is compiled later and therefore is deliberately absent here.
type StrategyRefV2 struct {
	TenantID   string `json:"tenant_id"`
	StrategyID string `json:"strategy_id"`
	Revision   string `json:"revision"`
	// SnapshotRevision is the authoritative immutable snapshot version. Zero
	// means the legacy source did not publish one; Revision is execution-only.
	SnapshotRevision int64 `json:"snapshot_revision,omitempty"`
}

type QueryGroupV2 struct {
	Key            string `json:"key"`
	QueryMD5       string `json:"query_md5"`
	QueryRevision  string `json:"query_revision"`
	EvaluationTime int64  `json:"evaluation_time"`
}

type SourceWindowV2 struct {
	FromTime  int64 `json:"from_time"`
	UntilTime int64 `json:"until_time"`
}

type QueryResultV2 struct {
	Completeness string `json:"completeness"`
	ReasonCode   string `json:"reason_code,omitempty"`
}

type DatasetContractV2 struct {
	DynamicDimensions   bool     `json:"dynamic_dimensions,omitempty"`
	SchemaDigest        string   `json:"schema_digest"`
	NormalizationDigest string   `json:"normalization_digest"`
	IdentityFields      []string `json:"identity_fields"`
	SourceTimeField     string   `json:"source_time_field"`
	CollectionTimeField string   `json:"collection_time_field,omitempty"`
	ReceivedTimeField   string   `json:"received_time_field"`
}

type InputProjectionV2 struct {
	DynamicDimensions     bool     `json:"dynamic_dimensions,omitempty"`
	ValueFields           []string `json:"value_fields"`
	DimensionFields       []string `json:"dimension_fields"`
	BusinessIdentityField string   `json:"business_identity_field"`
	MultiValueAlignment   string   `json:"multi_value_alignment"`
	DataUnit              string   `json:"data_unit"`
	MissingValuePolicy    string   `json:"missing_value_policy"`
}

type ExecutionSemanticsV2 struct {
	EvaluationScope     string `json:"evaluation_scope"`
	QueryWindow         uint32 `json:"query_window"`
	AggregationInterval uint32 `json:"aggregation_interval"`
	EvaluationInterval  uint32 `json:"evaluation_interval"`
	LatenessTolerance   uint32 `json:"lateness_tolerance"`
}

// TypedPlanV1 preserves a versioned structured plan without making the Reader
// interpret module-owned config fields.
type TypedPlanV1 struct {
	Type    string          `json:"type"`
	Version uint32          `json:"version"`
	Config  json.RawMessage `json:"config"`
}

type AlgorithmIRV2 struct {
	Type    string          `json:"type"`
	Version uint32          `json:"version"`
	Config  json.RawMessage `json:"config"`
}

type DetectPlanV2 struct {
	Algorithms []AlgorithmIRV2 `json:"algorithms"`
}

type LevelDefinitionV2 struct {
	LevelID   uint32 `json:"level_id"`
	LevelCode string `json:"level_code,omitempty"`
	Priority  uint32 `json:"priority"`
}

type LevelIRV2 struct {
	Definition   LevelDefinitionV2 `json:"definition"`
	Connector    string            `json:"connector"`
	DetectPlan   DetectPlanV2      `json:"detect_plan"`
	TriggerPlan  TypedPlanV1       `json:"trigger_plan"`
	RecoveryPlan TypedPlanV1       `json:"recovery_plan"`
}

type StrategyIRV2 struct {
	Schema             Schema               `json:"schema"`
	RequiredFeatures   []string             `json:"required_features"`
	StrategyRef        StrategyRefV2        `json:"strategy_ref"`
	ExecutionSemantics ExecutionSemanticsV2 `json:"execution_semantics"`
	InputProjection    InputProjectionV2    `json:"input_projection"`
	Levels             []LevelIRV2          `json:"levels"`
}

type SourceCompatibilityV2 struct {
	ItemID string `json:"item_id"`
}

type EvaluationPlanV2 struct {
	// EffectiveTimeSnapshot freezes the publisher's complete calendar rules.
	EffectiveTimeSnapshot json.RawMessage        `json:"effective_time_snapshot,omitempty"`
	PlanID                string                 `json:"plan_id"`
	StrategyRef           StrategyRefV2          `json:"strategy_ref"`
	InputProjection       InputProjectionV2      `json:"input_projection"`
	SourceCompatibility   *SourceCompatibilityV2 `json:"source_compatibility,omitempty"`
	OutputIdentity        *MonitorOutputIdentity `json:"output_identity,omitempty"`
	// SubjectFacts are the strategy facts the subject projection reads when a
	// record's own dimensions do not name its object. Absent means the
	// projection answers from the dimensions alone.
	SubjectFacts *MonitorSubjectFacts `json:"subject_facts,omitempty"`
	LegacyOutput *LegacyOutputContext `json:"legacy_output,omitempty"`
	// TargetScope is the strategy's monitoring target, frozen. Absent means
	// the strategy names no target and every series is in scope; it never
	// means "a scope existed and was dropped" - compilation rejects the Plan
	// in that case rather than publish one that alerts outside its target.
	TargetScope *TargetScopeV2 `json:"target_scope,omitempty"`
	// TargetPlan is the same target in its second frozen form, compiled from
	// the strategy cache's target_plan document. A Plan carries at most one
	// of the two; both absent means the strategy names no target.
	TargetPlan *TargetPlanV1 `json:"target_plan,omitempty"`
	// NoData is the item's no-data detection setting. Absent means the item
	// does not detect no-data; see NoDataConfigV1 for why enablement is the
	// presence of the section rather than a field inside it.
	NoData     *NoDataConfigV1 `json:"no_data,omitempty"`
	StrategyIR StrategyIRV2    `json:"strategy_ir"`
	// WireFormat is the format this Plan's events are published as, decided
	// when the Plan was built and frozen with it so a retried Slot cannot
	// change format between attempts. Empty means the pre-choice behaviour:
	// the frozen revision decides.
	WireFormat string `json:"wire_format,omitempty"`
	// SignalType is what this Plan's events are observed from -- metric, log
	// or event -- decided from the item's query configs when the Plan is built
	// and frozen with it, for the same reason the wire format is: the sink has
	// no Plan in hand and cannot work it out from a record.
	//
	// Empty means this build could not name it: a data type it has no mapping
	// for, or an item whose configs disagree. The event then omits the field
	// rather than carrying a guess.
	SignalType         string `json:"signal_type,omitempty"`
	TerminalReasonCode string `json:"terminal_reason_code,omitempty"`
}

// PublishesCompatibleProtocol reports whether this Plan's events go out as the
// Python-compatible event.
//
// It is one function because three places need the answer - the compiler, which
// requires the conversion context to be present exactly where it is used; the
// evaluator, which attaches it; and the sink, which reads it - and they were a
// repeated condition on the frozen revision until a deployment could force the
// choice. A Plan with no stated format predates the choice, where the revision
// was the whole rule.
func (plan EvaluationPlanV2) PublishesCompatibleProtocol() bool {
	if plan.WireFormat != "" {
		return plan.WireFormat == WireFormatPythonCompatible
	}
	return plan.StrategyRef.SnapshotRevision == 0
}

// The two external formats plus the historical spelling retained for reading
// frozen Plans. TriggerEvent is only an internal result, never an output format.
const (
	// WireFormatPythonCompatible is the event the Python alert builder reads.
	WireFormatPythonCompatible = "python_compatible"
	// WireFormatTriggerEvent is a historical frozen-Plan value. Readers resolve
	// it to standard raw output; new Plans never select it.
	WireFormatTriggerEvent = "trigger_event_v1"
	// WireFormatStandardRawEvent is the standard raw event the alert pipeline
	// consumes.
	WireFormatStandardRawEvent = "standard_raw_event"
)

// ResolveOutputWireFormat interprets historical frozen Plans without changing
// their serialized identity. Evaluation (including recovery gating) and the
// output sink use the same rule. Unknown formats remain unknown for rejection.
// EventHasMessage reports whether an event of this kind becomes a message
// under this resolved wire format. The Python-compatible protocol carries
// anomalies and nothing else: its consumer decides recovery from the absence
// of anomalies, so an event of any other kind has no message there. Every
// other format carries every kind.
//
// One rule with two readers: the sink, which leaves such an event without a
// message, and the evaluation, which does not keep one it knows the sink
// would drop. Two copies of it could disagree, and the way they would
// disagree is silent - an event kept that goes nowhere, or dropped that
// should have gone.
func EventHasMessage(format, eventKind string) bool {
	return format != WireFormatPythonCompatible || eventKind == TriggerEventAbnormal
}

// DroppedAtSink reports whether the sink would take this event and leave it
// without a message, raising nothing: a kind its resolved protocol has no
// message for, and nothing about the event the sink would refuse first. An
// event the sink would refuse is not dropped silently, and is not reported
// here, so that whoever acts on this answer leaves the refusal where it was.
func DroppedAtSink(event *TriggerEventV1) bool {
	if event == nil {
		return false
	}
	if EventHasMessage(OutputWireFormatOf(event), event.EventKind) {
		return false
	}
	return event.LegacyOutput != nil && event.LegacyOutput.Configuration != nil
}

// OutputWireFormatOf is the wire format the sink resolves this event to.
func OutputWireFormatOf(event *TriggerEventV1) string {
	var revision int64
	if event.StrategyRef != nil {
		revision = event.StrategyRef.Revision
	}
	return ResolveOutputWireFormat(event.WireFormat, revision)
}

func ResolveOutputWireFormat(format string, snapshotRevision int64) string {
	switch format {
	case WireFormatTriggerEvent:
		return WireFormatStandardRawEvent
	case "":
		if snapshotRevision > 0 {
			return WireFormatStandardRawEvent
		}
		return WireFormatPythonCompatible
	default:
		return format
	}
}

// MarshalJSON keeps the 2.0 wire union flat: a producer emits either the
// executable Plan body or the bounded terminal Plan identity, never both.
func (plan EvaluationPlanV2) MarshalJSON() ([]byte, error) {
	if plan.TerminalReasonCode != "" {
		return json.Marshal(struct {
			PlanID             string        `json:"plan_id"`
			StrategyRef        StrategyRefV2 `json:"strategy_ref"`
			TerminalReasonCode string        `json:"terminal_reason_code"`
		}{plan.PlanID, plan.StrategyRef, plan.TerminalReasonCode})
	}
	return json.Marshal(struct {
		EffectiveTimeSnapshot json.RawMessage        `json:"effective_time_snapshot,omitempty"`
		PlanID                string                 `json:"plan_id"`
		StrategyRef           StrategyRefV2          `json:"strategy_ref"`
		InputProjection       InputProjectionV2      `json:"input_projection"`
		SourceCompatibility   *SourceCompatibilityV2 `json:"source_compatibility,omitempty"`
		OutputIdentity        *MonitorOutputIdentity `json:"output_identity,omitempty"`
		SubjectFacts          *MonitorSubjectFacts   `json:"subject_facts,omitempty"`
		LegacyOutput          *LegacyOutputContext   `json:"legacy_output,omitempty"`
		TargetScope           *TargetScopeV2         `json:"target_scope,omitempty"`
		TargetPlan            *TargetPlanV1          `json:"target_plan,omitempty"`
		NoData                *NoDataConfigV1        `json:"no_data,omitempty"`
		StrategyIR            StrategyIRV2           `json:"strategy_ir"`
		WireFormat            string                 `json:"wire_format,omitempty"`
	}{plan.EffectiveTimeSnapshot, plan.PlanID, plan.StrategyRef, plan.InputProjection, plan.SourceCompatibility, plan.OutputIdentity, plan.SubjectFacts, plan.LegacyOutput, plan.TargetScope, plan.TargetPlan, plan.NoData, plan.StrategyIR, plan.WireFormat})
}

type PlanSetV2 struct {
	PlanSetDigest   string             `json:"plan_set_digest"`
	PlanCount       uint32             `json:"plan_count"`
	EvaluationPlans []EvaluationPlanV2 `json:"evaluation_plans"`
}

type SelectorRangeV2 struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type SelectorV2 struct {
	Kind      string             `json:"kind"`
	Ranges    *[]SelectorRangeV2 `json:"ranges,omitempty"`
	BitmapB64 string             `json:"bitmap_b64,omitempty"`
}

type PlanSelectorV2 struct {
	PlanOrdinal uint32     `json:"plan_ordinal"`
	Selector    SelectorV2 `json:"selector"`
}

type DimensionFieldV2 struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type DimensionIdentityV2 struct {
	Fields []DimensionFieldV2 `json:"fields"`
	Digest string             `json:"digest"`
}

type CanonicalRecordV2 struct {
	RecordID          string                     `json:"record_id"`
	SourceTime        int64                      `json:"source_time"`
	BusinessID        string                     `json:"business_id"`
	DimensionIdentity DimensionIdentityV2        `json:"dimension_identity"`
	Values            map[string]json.RawMessage `json:"values"`
	Dimensions        map[string]json.RawMessage `json:"dimensions"`
	CollectionTime    *int64                     `json:"collection_time,omitempty"`
	ReceivedTime      int64                      `json:"received_time"`
}

type ExecutionEnvelopeV2 struct {
	Schema           Schema              `json:"schema"`
	RequiredFeatures []string            `json:"required_features"`
	ExecutionID      string              `json:"execution_id"`
	MessageID        string              `json:"message_id"`
	TenantID         string              `json:"tenant_id"`
	QueryGroup       QueryGroupV2        `json:"query_group"`
	SourceWindow     SourceWindowV2      `json:"source_window"`
	QueryResult      QueryResultV2       `json:"query_result"`
	DatasetContract  DatasetContractV2   `json:"dataset_contract"`
	PlanSet          PlanSetV2           `json:"plan_set"`
	Selectors        []PlanSelectorV2    `json:"selectors"`
	Records          []CanonicalRecordV2 `json:"records"`
	PayloadDigest    string              `json:"payload_digest"`
}

type ReaderLimitsV2 struct {
	MaxEnvelopeBytes     int
	MaxRecordsPerMessage int
	MaxPlansPerMessage   int
	MaxLevelsPerPlan     int
	MaxSelectorBytes     int
	MaxRecordBytes       int
	MaxPlanSetBytes      int
	MaxContractDepth     int
	MaxStringBytes       int
	MaxValidationIssues  int
}

type ValidationScope string

type ValidationIssue struct {
	Scope                 ValidationScope             `json:"scope"`
	ReasonCode            string                      `json:"reason_code"`
	FieldPath             string                      `json:"field_path"`
	PlanOrdinal           *uint32                     `json:"plan_ordinal,omitempty"`
	PlanID                string                      `json:"plan_id,omitempty"`
	PlanIdentityUntrusted bool                        `json:"plan_identity_untrusted,omitempty"`
	LevelID               *uint32                     `json:"level_id,omitempty"`
	RecordOrdinal         *uint32                     `json:"record_ordinal,omitempty"`
	RecordID              string                      `json:"record_id,omitempty"`
	UnverifiedTail        *ValidationUnverifiedTailV2 `json:"unverified_tail,omitempty"`
}

// ValidationUnverifiedTailV2 is present only on VALIDATION_BUDGET_EXCEEDED.
// Every object at or after a non-nil ordinal was not fully validated and must
// be terminalized by the consumer; it must never be treated as valid.
type ValidationUnverifiedTailV2 struct {
	PlanFromOrdinal   *uint32 `json:"plan_from_ordinal,omitempty"`
	RecordFromOrdinal *uint32 `json:"record_from_ordinal,omitempty"`
}

type FramedExecutionEnvelopeV2 struct {
	Envelope   ExecutionEnvelopeV2
	RawPayload json.RawMessage
}

// StateCompatibilityInputV1 contains only whole-key interpretation semantics.
// Level projection, unit, missing and algorithm semantics belong to Level
// fingerprints and must not cause all Levels in one packed value to reset.
type StateCompatibilityInputV1 struct {
	StateSchemaVersion          string `json:"state_schema_version"`
	CodecSemanticsVersion       string `json:"codec_semantics_version"`
	IdentitySchemaDigest        string `json:"identity_schema_digest"`
	EvaluationScope             string `json:"evaluation_scope"`
	AggregationInterval         uint32 `json:"aggregation_interval"`
	EvaluationInterval          uint32 `json:"evaluation_interval"`
	SourceTimeSemanticsVersion  string `json:"source_time_semantics_version"`
	HistoryCellSemanticsVersion string `json:"history_cell_semantics_version"`
}

type LevelDetectSemanticV1 struct {
	LevelID                uint32 `json:"level_id"`
	ProjectionDigest       string `json:"projection_digest"`
	DetectorSemanticDigest string `json:"detector_semantic_digest"`
}

type RuntimePlanRefV1 struct {
	StrategyID             string `json:"strategy_id"`
	StrategyRevision       string `json:"strategy_revision"`
	StateCompatibilityHash string `json:"state_compatibility_hash"`
}

type TriggerRecordRefV1 struct {
	RecordID                string                     `json:"record_id"`
	SourceTime              int64                      `json:"source_time"`
	DimensionIdentityDigest string                     `json:"dimension_identity_digest"`
	Dimensions              map[string]json.RawMessage `json:"dimensions"`
}

type TriggerObservedV1 struct {
	Values map[string]json.RawMessage `json:"values"`
	Unit   string                     `json:"unit"`
}

type TriggerWindowEvidenceV1 struct {
	WindowStart       int64  `json:"window_start"`
	WindowEnd         int64  `json:"window_end"`
	WindowSize        uint32 `json:"window_size"`
	RequiredAnomalies uint32 `json:"required_anomalies"`
	ObservedAnomalies uint32 `json:"observed_anomalies"`
	// AnomalyBeginTime is the source time of the earliest anomalous point in
	// this window, or zero when the window holds none.
	//
	// The window otherwise reports only how many anomalies it saw, and the
	// timestamps themselves are deliberately reduced to a digest - they are
	// evidence of a decision, not a fact a consumer needs. This one is the
	// exception: a downstream that owns an alert's lifetime needs a point in
	// time to open it from, and the window's own edges are not that point. It
	// is the earliest anomaly in this window, not the first of an ongoing
	// anomalous stretch; keeping the latter would mean keeping state about
	// stretches, which is the downstream's job and not this one's.
	AnomalyBeginTime int64 `json:"anomaly_begin_time,omitempty"`
}

type RecoveryWindowEvidenceV1 struct {
	Enabled                    bool   `json:"enabled"`
	RequiredConsecutiveWindows uint32 `json:"required_consecutive_windows"`
	ObservedConsecutiveMisses  uint32 `json:"observed_consecutive_misses"`
	OldestWindowStart          int64  `json:"oldest_window_start"`
	// SkippedWindows is how many windows the walk stepped over because they
	// held too little to answer: their observed anomalies plus their holes
	// reached the trigger's threshold, so an anomaly in the holes could have
	// fired them and "did not trigger" is not something they said.
	//
	// Windows, the unit the walk moves in, not positions - one per offset the
	// walk passed over. A single missing position can put several consecutive
	// windows out of reach, so the two counts are not interchangeable and the
	// name has to say which one this is.
	//
	// A skipped window is neither a miss nor an anomaly. Counting them as
	// misses, which is what happened before decision-022, built recoveries on
	// absence; breaking on them would let one hole cost the whole run. Saying
	// how many were stepped over is what keeps the other two numbers readable:
	// without it, a run of five misses spanning an hour and one spanning a
	// week look the same on the evidence.
	SkippedWindows uint32 `json:"skipped_windows,omitempty"`
}

type WindowEvidenceV1 struct {
	AnomalyTimestampsDigest string `json:"anomaly_timestamps_digest"`
	LateAccepted            bool   `json:"late_accepted"`
}

type DecisionWindowV1 struct {
	Type                string                   `json:"type"`
	Version             uint32                   `json:"version"`
	SourceTime          int64                    `json:"source_time"`
	Trigger             TriggerWindowEvidenceV1  `json:"trigger"`
	Recovery            RecoveryWindowEvidenceV1 `json:"recovery"`
	HistoryCompleteness string                   `json:"history_completeness"`
	WindowEvidence      WindowEvidenceV1         `json:"window_evidence"`
}

type DetectEvidenceV1 struct {
	DetectionResult         string          `json:"detection_result"`
	PredicateDigest         string          `json:"predicate_digest"`
	NormalizedValue         json.RawMessage `json:"normalized_value"`
	MatchedAlgorithmOrdinal *uint32         `json:"matched_algorithm_ordinal,omitempty"`
	MatchedGroupOrdinal     *uint32         `json:"matched_group_ordinal,omitempty"`
	ResultReason            string          `json:"result_reason,omitempty"`
	EffectiveTimeStatus     string          `json:"effective_time_status"`
}

type LevelResultV1 struct {
	LevelID                 uint32           `json:"level_id"`
	LevelCode               string           `json:"level_code,omitempty"`
	Priority                uint32           `json:"priority"`
	Result                  string           `json:"result"`
	DecisionWindow          DecisionWindowV1 `json:"decision_window"`
	DetectEvidence          DetectEvidenceV1 `json:"detect_evidence"`
	LevelTriggerFingerprint string           `json:"level_trigger_fingerprint"`
}

type TriggerEventTraceV1 struct {
	ExecutionID string `json:"execution_id"`
}

// StrategySnapshotRef is the complete downstream immutable strategy identity.
type StrategySnapshotRef struct {
	TenantID   string `json:"bk_tenant_id"`
	BusinessID int64  `json:"strategy_bk_biz_id"`
	StrategyID int64  `json:"strategy_id"`
	Revision   int64  `json:"strategy_revision"`
}

type TriggerEventV1 struct {
	LegacyOutput *LegacyEventContext `json:"-"`
	// Subject is the object this event is about, projected where the Plan was
	// still in hand. The projection needs the strategy's aggregation dimensions
	// and its frozen facts, and a Kafka sink has neither, so it cannot be left
	// to the place that writes the message.
	Subject *MonitorSubjectContext `json:"-"`
	// WireFormat is the format this event is published as, taken from the Plan
	// it was evaluated for. It travels beside the event rather than inside it:
	// a consumer reads one format and never has to be told which.
	WireFormat string `json:"-"`
	// SignalType travels beside WireFormat and for the same reason: the sink
	// has no Plan in hand. Empty means this build could not name it.
	SignalType              string               `json:"-"`
	Schema                  Schema               `json:"schema"`
	RequiredFeatures        []string             `json:"required_features"`
	EventID                 string               `json:"event_id"`
	EventSemanticDigest     string               `json:"event_semantic_digest"`
	EventKind               string               `json:"event_kind"`
	PrimaryLevelID          uint32               `json:"primary_level_id"`
	TenantID                string               `json:"tenant_id"`
	BusinessID              string               `json:"business_id"`
	PlanRef                 RuntimePlanRefV1     `json:"plan_ref"`
	StrategyRef             *StrategySnapshotRef `json:"strategy_ref,omitempty"`
	DedupeMD5               string               `json:"dedupe_md5,omitempty"`
	RecordRef               TriggerRecordRefV1   `json:"record_ref"`
	Observed                TriggerObservedV1    `json:"observed"`
	LevelResults            []LevelResultV1      `json:"level_results"`
	EvaluationTime          int64                `json:"evaluation_time"`
	DetectPlanFingerprint   string               `json:"detect_plan_fingerprint"`
	TriggerStateFingerprint string               `json:"trigger_state_fingerprint"`
	Trace                   TriggerEventTraceV1  `json:"trace"`
}

type TriggerEventBuildInputV1 struct {
	EventKind               string
	TenantID                string
	BusinessID              string
	PlanRef                 RuntimePlanRefV1
	StrategyRef             *StrategySnapshotRef
	DedupeMD5               string
	RecordRef               TriggerRecordRefV1
	Observed                TriggerObservedV1
	LevelResults            []LevelResultV1
	EvaluationTime          int64
	DetectPlanFingerprint   string
	TriggerStateFingerprint string
	ExecutionID             string
	MaxEvidenceBytes        int
}

type TriggerEventReaderLimitsV1 struct {
	MaxPayloadBytes  int
	MaxEvidenceBytes int
}

type CountSetV1 struct {
	Messages uint64 `json:"messages"`
	Records  uint64 `json:"records"`
	Bytes    uint64 `json:"bytes"`
}

type ReasonCountV1 struct {
	ReasonCode string `json:"reason_code"`
	Count      uint64 `json:"count"`
}

type ExecutionSummaryV1 struct {
	Schema           Schema          `json:"schema"`
	RequiredFeatures []string        `json:"required_features"`
	SummaryID        string          `json:"summary_id"`
	ExecutionID      string          `json:"execution_id"`
	TenantID         string          `json:"tenant_id"`
	QueryGroupKey    string          `json:"query_group_key"`
	SourceWindow     SourceWindowV2  `json:"source_window"`
	PlanSetDigest    string          `json:"plan_set_digest"`
	Source           CountSetV1      `json:"source"`
	Published        CountSetV1      `json:"published"`
	Dropped          CountSetV1      `json:"dropped"`
	ReasonCounts     []ReasonCountV1 `json:"reason_counts"`
}
