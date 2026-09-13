// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	FinalResultEvidenceSchemaV1     = "shadow-final-result-evidence"
	ChainCoverageReceiptSchemaV1    = "shadow-chain-coverage-receipt"
	ValidationEpochManifestSchemaV1 = "shadow-validation-epoch-manifest"
	ShadowFinalResult               = "FINAL_RESULT_EVIDENCE"
	ShadowCoverage                  = "CHAIN_COVERAGE_RECEIPT"
	ShadowNativeEvent               = "NATIVE_BUSINESS_EVENT"
	ShadowPython                    = "PYTHON"
	ShadowGo                        = "GO"
)

// ShadowSubjectV1 excludes native IDs, Level, kind and execution coordinates.
type ShadowSubjectV1 struct {
	IdentityVersion         string `json:"identity_version"`
	TenantID                string `json:"tenant_id"`
	BusinessID              string `json:"business_id"`
	StrategyID              string `json:"strategy_id"`
	DimensionIdentityDigest string `json:"dimension_identity_digest"`
	SourceTime              int64  `json:"source_time"`
}

type ShadowContextV1 struct {
	ComparisonConfigDigest         string `json:"comparison_config_digest"`
	PlanScheduleRevision           string `json:"plan_schedule_revision"`
	EvaluationTime                 int64  `json:"comparison_evaluation_time"`
	SlotIdentity                   string `json:"slot_identity"`
	SnapshotRevision               string `json:"snapshot_revision,omitempty"`
	QueryRevision                  string `json:"query_revision,omitempty"`
	QueryGroupScheduleRevision     string `json:"query_group_schedule_revision,omitempty"`
	ScheduleSegmentStart           int64  `json:"schedule_segment_start,omitempty"`
	DuePlanSetDigest               string `json:"due_plan_set_digest,omitempty"`
	EffectiveTimeRequirementDigest string `json:"effective_time_requirement_digest"`
	EffectiveTimeFactDigest        string `json:"effective_time_fact_digest"`
	SourceItemRef                  string `json:"source_item_ref,omitempty"`
}

// Comparable facts are supplied by the chain adapter, never inferred from a
// chain-native fingerprint. Values are canonical decimal strings.
type PrimaryComparableProjectionV1 struct {
	Version                 string            `json:"comparison_projection_version"`
	SelectionMappingVersion string            `json:"primary_selection_mapping_version"`
	LevelID                 uint32            `json:"primary_level_id"`
	Priority                uint32            `json:"priority"`
	Result                  string            `json:"primary_result"`
	Values                  map[string]string `json:"values"`
	// Empty is a known dimensionless unit. Typed adapters must establish its
	// source; the wire reader separately rejects missing or null unit fields.
	Unit                string `json:"unit"`
	WindowStart         int64  `json:"window_start"`
	WindowEnd           int64  `json:"window_end"`
	DetectConfigDigest  string `json:"detect_config_digest"`
	TriggerConfigDigest string `json:"trigger_config_digest"`
	Digest              string `json:"comparable_projection_digest"`
}

type ShadowNativeRefV1 struct {
	EventID        string          `json:"chain_native_event_id"`
	SemanticDigest string          `json:"chain_native_semantic_digest"`
	LevelResults   []LevelResultV1 `json:"level_results"`
	EvidenceDigest string          `json:"evidence_digest"`
}

type ShadowDeliveryV1 struct {
	FinalAdmission string `json:"final_admission"`
	BusinessACK    bool   `json:"business_ack"`
	ReasonCode     string `json:"reason_code"`
}

type ShadowCompletenessV1 struct {
	Input     string `json:"input"`
	Readiness string `json:"readiness"`
	History   string `json:"history"`
}

type FinalResultEvidenceV1 struct {
	Schema           Schema                        `json:"schema"`
	RequiredFeatures []string                      `json:"required_features"`
	RecordType       string                        `json:"record_type"`
	EpochID          string                        `json:"validation_epoch_id"`
	Chain            string                        `json:"chain"`
	ResultKind       string                        `json:"result_kind"`
	Subject          ShadowSubjectV1               `json:"subject"`
	Native           ShadowNativeRefV1             `json:"native_event_ref"`
	Primary          PrimaryComparableProjectionV1 `json:"primary_comparable_projection"`
	Context          ShadowContextV1               `json:"comparison_context"`
	Completeness     ShadowCompletenessV1          `json:"completeness"`
	Delivery         ShadowDeliveryV1              `json:"delivery"`
	// Go retains the exact ACKed native envelope. Python has a different native
	// contract and does not synthesize a Go envelope.
	GoEvent *TriggerEventV1 `json:"go_event,omitempty"`
}

func shadowHeader(schema Schema, features []string, name, recordType, expected string) error {
	if schema != (Schema{Name: name, Major: 1}) || features == nil || len(features) != 0 || recordType != expected {
		return invalid("shadow.header", "unsupported schema, features or record type")
	}
	return nil
}

func validateShadowSubject(s ShadowSubjectV1) error {
	if s.IdentityVersion == "" || s.TenantID == "" || s.BusinessID == "" || s.StrategyID == "" || !sha256Pattern.MatchString(s.DimensionIdentityDigest) || s.SourceTime < 0 {
		return invalid("shadow.subject", "incomplete subject")
	}
	return nil
}

func validateShadowContext(c ShadowContextV1, chain string) error {
	if !sha256Pattern.MatchString(c.ComparisonConfigDigest) || c.PlanScheduleRevision == "" || c.SlotIdentity == "" || c.EvaluationTime < 0 || !sha256Pattern.MatchString(c.EffectiveTimeRequirementDigest) || !sha256Pattern.MatchString(c.EffectiveTimeFactDigest) {
		return invalid("shadow.context", "incomplete comparison facts")
	}
	if chain == ShadowGo && (c.SnapshotRevision == "" || c.QueryRevision == "" || c.QueryGroupScheduleRevision == "" || c.ScheduleSegmentStart < 0 || !sha256Pattern.MatchString(c.DuePlanSetDigest)) {
		return invalid("shadow.context", "Go frozen provenance is required")
	}
	return nil
}

func ShadowProjectionDigestV1(p PrimaryComparableProjectionV1) (string, error) {
	p.Digest = ""
	return DeriveCanonicalDigestV2("shadow-primary-projection-v1", p)
}

func ShadowNativeDigestV1(n ShadowNativeRefV1) (string, error) {
	n.EvidenceDigest = ""
	return DeriveCanonicalDigestV2("shadow-native-evidence-v1", n)
}

func ValidateFinalResultEvidenceV1(e *FinalResultEvidenceV1) error {
	if e == nil {
		return invalid("shadow.evidence", "nil")
	}
	if err := shadowHeader(e.Schema, e.RequiredFeatures, FinalResultEvidenceSchemaV1, e.RecordType, ShadowFinalResult); err != nil {
		return err
	}
	if e.EpochID == "" || (e.Chain != ShadowGo && e.Chain != ShadowPython) {
		return invalid("shadow.evidence", "epoch and chain required")
	}
	if err := validateShadowSubject(e.Subject); err != nil {
		return err
	}
	if err := validateShadowContext(e.Context, e.Chain); err != nil {
		return err
	}
	if e.ResultKind != TriggerEventAbnormal && e.ResultKind != TriggerEventRecovery {
		return invalid("shadow.result", "not a final result")
	}
	if e.Delivery.FinalAdmission != "PRODUCED" || !e.Delivery.BusinessACK || e.Delivery.ReasonCode == "" {
		return invalid("shadow.delivery", "business ACK required")
	}
	if (e.Completeness.Input != "FULL" && e.Completeness.Input != "PARTIAL_ACCEPTED") || e.Completeness.Readiness == "" || e.Completeness.History == "" || (e.Completeness.Input == "PARTIAL_ACCEPTED" && e.ResultKind != TriggerEventAbnormal) {
		return invalid("shadow.completeness", "invalid final input status")
	}
	p := e.Primary
	if p.Version == "" || p.SelectionMappingVersion == "" || p.LevelID == 0 || p.Priority == 0 || p.Result != e.ResultKind || p.Values == nil || p.WindowStart < 0 || p.WindowEnd < p.WindowStart || !sha256Pattern.MatchString(p.DetectConfigDigest) || !sha256Pattern.MatchString(p.TriggerConfigDigest) {
		return invalid("shadow.primary", "incomplete comparable projection")
	}
	for _, v := range p.Values {
		normalized, err := NormalizeShadowDecimalV1(v)
		if err != nil || normalized != v {
			return invalid("shadow.primary.values", "canonical decimals required")
		}
	}
	digest, err := ShadowProjectionDigestV1(p)
	if err != nil {
		return err
	}
	if digest != p.Digest {
		return invalid("shadow.primary.digest", "mismatch")
	}
	if e.Native.EventID == "" || !sha256Pattern.MatchString(e.Native.SemanticDigest) || len(e.Native.LevelResults) == 0 {
		return invalid("shadow.native", "native facts required")
	}
	seen := map[uint32]bool{}
	found := false
	for _, l := range e.Native.LevelResults {
		if l.LevelID == 0 || seen[l.LevelID] || l.Priority == 0 {
			return invalid("shadow.native.levels", "invalid or duplicate Level")
		}
		seen[l.LevelID] = true
		if l.Result != LevelResultNormal && l.Result != LevelResultAbnormal && l.Result != LevelResultRecovery {
			return invalid("shadow.native.levels", "invalid result")
		}
		if l.LevelID == p.LevelID {
			found = true
			if l.Result != p.Result || l.Priority != p.Priority {
				return invalid("shadow.primary", "native primary mismatch")
			}
		}
	}
	if !found || (e.Chain == ShadowPython && len(seen) != 1) {
		return invalid("shadow.native.levels", "primary missing or synthetic Python siblings")
	}
	digest, err = ShadowNativeDigestV1(e.Native)
	if err != nil {
		return err
	}
	if digest != e.Native.EvidenceDigest {
		return invalid("shadow.native.digest", "mismatch")
	}
	if e.Chain == ShadowGo {
		if e.GoEvent == nil {
			return invalid("shadow.go_event", "required")
		}
		if err := ValidateTriggerEventV1(e.GoEvent); err != nil {
			return err
		}
		g := e.GoEvent
		if g.EventID != e.Native.EventID || g.EventSemanticDigest != e.Native.SemanticDigest || g.TenantID != e.Subject.TenantID || g.BusinessID != e.Subject.BusinessID || g.PlanRef.StrategyID != e.Subject.StrategyID || g.RecordRef.DimensionIdentityDigest != e.Subject.DimensionIdentityDigest || g.RecordRef.SourceTime != e.Subject.SourceTime || g.PrimaryLevelID != p.LevelID || g.EventKind != e.ResultKind || g.EvaluationTime != e.Context.EvaluationTime {
			return invalid("shadow.go_event", "native envelope mismatch")
		}
		a, _ := CanonicalJSONV2(g.LevelResults)
		b, _ := CanonicalJSONV2(e.Native.LevelResults)
		if string(a) != string(b) {
			return invalid("shadow.go_event", "all native Levels must be retained")
		}
	} else if e.GoEvent != nil {
		return invalid("shadow.go_event", "Python cannot carry a Go envelope")
	}
	return nil
}

// ShadowCanonicalDigestV1 hashes an already versioned canonical object.
func ShadowCanonicalDigestV1(value any) (string, error) {
	b, err := CanonicalJSONV2(value)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
