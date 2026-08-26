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
	MessageReceiptSchemaV1    = "message-receipt"
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

	ReasonMalformedJSON                    = "MALFORMED_JSON"
	ReasonSchemaMajorUnsupported           = "SCHEMA_MAJOR_UNSUPPORTED"
	ReasonRequiredFeatureUnsupported       = "REQUIRED_FEATURE_UNSUPPORTED"
	ReasonTenantInvalid                    = "TENANT_INVALID"
	ReasonPayloadDigestMismatch            = "PAYLOAD_DIGEST_MISMATCH"
	ReasonPlanSetConflict                  = "PLAN_SET_CONFLICT"
	ReasonSelectorOrdinalInvalid           = "SELECTOR_ORDINAL_INVALID"
	ReasonMessageBudgetExceeded            = "MESSAGE_BUDGET_EXCEEDED"
	ReasonPlanInvalid                      = "PLAN_INVALID"
	ReasonPlanDuplicateLevelID             = "PLAN_DUPLICATE_LEVEL_ID"
	ReasonPlanBudgetExceeded               = "PLAN_BUDGET_EXCEEDED"
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
	ReasonQueryTimeout                     = "QUERY_TIMEOUT"
	ReasonQueryUnavailable                 = "QUERY_UNAVAILABLE"
	ReasonEffectiveTimeInactive            = "EFFECTIVE_TIME_INACTIVE"
	ReasonEffectiveTimeUnknown             = "EFFECTIVE_TIME_UNKNOWN"
	ReasonHistoryWarming                   = "HISTORY_WARMING"
	ReasonHistoryGapped                    = "HISTORY_GAPPED"
	ReasonKafkaUnavailable                 = "KAFKA_UNAVAILABLE"
	ReasonRedisUnavailable                 = "REDIS_UNAVAILABLE"
	ReasonResourceHardStop                 = "RESOURCE_HARD_STOP"
	ReasonOutputACKUnknown                 = "OUTPUT_ACK_UNKNOWN"
	ReasonStateWriteRetryable              = "STATE_WRITE_RETRYABLE"
	ReasonAuditDrop                        = "AUDIT_DROP"

	CompatibilityModeLegacyGroupOfOne = "LEGACY_GROUP_OF_ONE"

	DetectPlanFingerprintDomainV1   = "detect-plan-fingerprint-v1"
	TriggerStateFingerprintDomainV1 = "trigger-state-fingerprint-v1"

	TriggerEventAbnormal = "ABNORMAL"
	TriggerEventRecovery = "RECOVERY"

	LevelResultAbnormal    = "ABNORMAL"
	LevelResultNormal      = "NORMAL"
	LevelResultRecovery    = "RECOVERY"
	LevelResultUnavailable = "UNAVAILABLE"

	ReceiptStatusCompleted             = "COMPLETED"
	ReceiptStatusCompletedWithTerminal = "COMPLETED_WITH_TERMINAL"
	ReceiptStatusRejected              = "REJECTED"
)

// StrategyRefV2 is the producer-side strategy identity. Runtime state identity
// is compiled later and therefore is deliberately absent here.
type StrategyRefV2 struct {
	TenantID   string `json:"tenant_id"`
	StrategyID string `json:"strategy_id"`
	Revision   string `json:"revision"`
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
	SchemaDigest        string   `json:"schema_digest"`
	NormalizationDigest string   `json:"normalization_digest"`
	IdentityFields      []string `json:"identity_fields"`
	SourceTimeField     string   `json:"source_time_field"`
	CollectionTimeField string   `json:"collection_time_field,omitempty"`
	ReceivedTimeField   string   `json:"received_time_field"`
}

type InputProjectionV2 struct {
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
	PlanID              string                 `json:"plan_id"`
	StrategyRef         StrategyRefV2          `json:"strategy_ref"`
	InputProjection     InputProjectionV2      `json:"input_projection"`
	SourceCompatibility *SourceCompatibilityV2 `json:"source_compatibility,omitempty"`
	StrategyIR          StrategyIRV2           `json:"strategy_ir"`
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
	Scope          ValidationScope             `json:"scope"`
	ReasonCode     string                      `json:"reason_code"`
	FieldPath      string                      `json:"field_path"`
	PlanOrdinal    *uint32                     `json:"plan_ordinal,omitempty"`
	PlanID         string                      `json:"plan_id,omitempty"`
	LevelID        *uint32                     `json:"level_id,omitempty"`
	RecordOrdinal  *uint32                     `json:"record_ordinal,omitempty"`
	RecordID       string                      `json:"record_id,omitempty"`
	UnverifiedTail *ValidationUnverifiedTailV2 `json:"unverified_tail,omitempty"`
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
}

type RecoveryWindowEvidenceV1 struct {
	Enabled                    bool   `json:"enabled"`
	RequiredConsecutiveWindows uint32 `json:"required_consecutive_windows"`
	ObservedConsecutiveMisses  uint32 `json:"observed_consecutive_misses"`
	OldestWindowStart          int64  `json:"oldest_window_start"`
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

type TriggerEventV1 struct {
	Schema                  Schema              `json:"schema"`
	RequiredFeatures        []string            `json:"required_features"`
	EventID                 string              `json:"event_id"`
	EventSemanticDigest     string              `json:"event_semantic_digest"`
	EventKind               string              `json:"event_kind"`
	PrimaryLevelID          uint32              `json:"primary_level_id"`
	TenantID                string              `json:"tenant_id"`
	BusinessID              string              `json:"business_id"`
	PlanRef                 RuntimePlanRefV1    `json:"plan_ref"`
	RecordRef               TriggerRecordRefV1  `json:"record_ref"`
	Observed                TriggerObservedV1   `json:"observed"`
	LevelResults            []LevelResultV1     `json:"level_results"`
	EvaluationTime          int64               `json:"evaluation_time"`
	DetectPlanFingerprint   string              `json:"detect_plan_fingerprint"`
	TriggerStateFingerprint string              `json:"trigger_state_fingerprint"`
	Trace                   TriggerEventTraceV1 `json:"trace"`
}

type TriggerEventBuildInputV1 struct {
	EventKind               string
	TenantID                string
	BusinessID              string
	PlanRef                 RuntimePlanRefV1
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

type PlanReceiptV1 struct {
	PlanID                string `json:"plan_id"`
	Selected              uint64 `json:"selected"`
	Abnormal              uint64 `json:"abnormal"`
	Normal                uint64 `json:"normal"`
	Recovery              uint64 `json:"recovery"`
	Unavailable           uint64 `json:"unavailable"`
	Terminal              uint64 `json:"terminal"`
	LevelTerminalAffected uint64 `json:"level_terminal_affected"`
	ResultIdentityDigest  string `json:"result_identity_digest"`
}

type ReceiptCountsV1 struct {
	Received  uint64 `json:"received"`
	Selected  uint64 `json:"selected"`
	Processed uint64 `json:"processed"`
	// Unavailable counts selected Plan x Record evaluations that produced no
	// valid three-state decision for a controlled runtime reason. ReasonCounts
	// distinguishes suppression, missing facts, warming and gapped history.
	Unavailable uint64 `json:"unavailable"`
	Terminal    uint64 `json:"terminal"`
	// LevelTerminalAffected counts Plan x Record evaluations whose sibling
	// Level terminalized. It can overlap Processed and is not part of the
	// Selected decomposition.
	LevelTerminalAffected uint64 `json:"level_terminal_affected"`
	Events                uint64 `json:"events"`
}

type MessageReceiptV1 struct {
	Schema           Schema          `json:"schema"`
	RequiredFeatures []string        `json:"required_features"`
	ReceiptID        string          `json:"receipt_id"`
	ExecutionID      string          `json:"execution_id"`
	MessageID        string          `json:"message_id"`
	PayloadDigest    string          `json:"payload_digest"`
	PlanSetDigest    string          `json:"plan_set_digest"`
	SourceWindow     SourceWindowV2  `json:"source_window"`
	Status           string          `json:"status"`
	Counts           ReceiptCountsV1 `json:"counts"`
	PerPlan          []PlanReceiptV1 `json:"per_plan"`
	ReasonCounts     []ReasonCountV1 `json:"reason_counts"`
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
