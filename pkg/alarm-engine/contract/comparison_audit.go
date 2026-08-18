// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"unicode/utf8"
)

const (
	comparisonAuditBatchSchema = "comparison-audit-batch"
	comparisonAuditIDVersionV1 = "comparison-audit-id-v1"

	ComparisonAuditEventSnapshot = "ASSESSMENT_SNAPSHOT"

	ComparisonJoinPendingInput  = "PENDING_INPUT"
	ComparisonJoinPendingBoth   = "PENDING_BOTH"
	ComparisonJoinPendingGo     = "PENDING_GO"
	ComparisonJoinPendingPython = "PENDING_PYTHON"
	ComparisonJoinComplete      = "COMPLETE"
	ComparisonJoinConflict      = "CONFLICT"
	ComparisonJoinInvalid       = "INVALID"

	ComparisonEligibilityNone          = "NONE"
	ComparisonEligibilityEligible      = "ELIGIBLE"
	ComparisonEligibilityWarmup        = "WARMUP"
	ComparisonEligibilityCoverageGap   = "COVERAGE_GAP"
	ComparisonEligibilitySourceError   = "SOURCE_ERROR"
	ComparisonEligibilityUnsupported   = "UNSUPPORTED"
	ComparisonEligibilityEpochUnstable = "EPOCH_UNSTABLE"

	ComparisonVerdictNone     = "NONE"
	ComparisonVerdictMatch    = "MATCH"
	ComparisonVerdictHardDiff = "HARD_DIFF"

	ComparisonCoveragePending          = "PENDING"
	ComparisonCoverageOverdue          = "OVERDUE"
	ComparisonCoverageMissingAtBarrier = "MISSING_AT_BARRIER"
	ComparisonCoverageComplete         = "COMPLETE"

	ComparisonRoleGo     = "GO"
	ComparisonRolePython = "PYTHON"

	MaxComparisonAuditBytesV1 = 512 * 1024
	MaxComparisonAuditItemsV1 = 500
)

type ComparisonSourceEvidence struct {
	Outcome   string `json:"outcome"`
	ErrorCode string `json:"error_code,omitempty"`
	// SemanticSHA256 is the exact fingerprint retained by Comparator.
	SemanticSHA256 string `json:"semantic_sha256"`
}

type ComparisonDecisionBatchIdentity struct {
	PartitionHashVersion string      `json:"partition_hash_version"`
	TenantID             string      `json:"tenant_id"`
	Purpose              string      `json:"purpose"`
	StrategyRef          StrategyRef `json:"strategy_ref"`
	DecisionAlgorithm    string      `json:"decision_algorithm"`
}

type ComparisonDecisionEvidence struct {
	BatchIdentity ComparisonDecisionBatchIdentity `json:"batch_identity"`
	Decision      TriggerDecision                 `json:"decision"`
	// SemanticSHA256 is the exact fingerprint retained by Comparator. The
	// contract deliberately does not reimplement Comparator's canonicalizer.
	SemanticSHA256 string `json:"semantic_sha256"`
}

type ComparisonCoverageEvidence struct {
	Phase                 string   `json:"phase"`
	BarrierFrozen         bool     `json:"barrier_frozen"`
	MissingRoles          []string `json:"missing_roles"`
	MissingAtBarrierRoles []string `json:"missing_at_barrier_roles"`
	LateRoles             []string `json:"late_roles"`
}

type ComparisonAudit struct {
	AuditID        string                      `json:"audit_id"`
	EventKind      string                      `json:"event_kind"`
	InputID        string                      `json:"input_id"`
	RecordID       string                      `json:"record_id"`
	SourceTime     int64                       `json:"source_time"`
	JoinStatus     string                      `json:"join_status"`
	Eligibility    string                      `json:"eligibility"`
	Verdict        string                      `json:"verdict"`
	Source         ComparisonSourceEvidence    `json:"source"`
	GoDecision     *ComparisonDecisionEvidence `json:"go_decision,omitempty"`
	PythonDecision *ComparisonDecisionEvidence `json:"python_decision,omitempty"`
	Coverage       ComparisonCoverageEvidence  `json:"coverage"`
	SourceConflict bool                        `json:"source_conflict"`
	GoConflict     bool                        `json:"go_conflict"`
	PythonConflict bool                        `json:"python_conflict"`
	GoInvalid      bool                        `json:"go_invalid"`
	PythonInvalid  bool                        `json:"python_invalid"`
}

type ComparisonAuditBatch struct {
	Schema               Schema            `json:"schema"`
	RequiredFeatures     []string          `json:"required_features"`
	PartitionHashVersion string            `json:"partition_hash_version"`
	TenantID             string            `json:"tenant_id"`
	Purpose              string            `json:"purpose"`
	StrategyRef          StrategyRef       `json:"strategy_ref"`
	Audits               []ComparisonAudit `json:"audits"`
	partitionKey         []byte
}

func BuildComparisonAuditBatch(
	partitionHashVersion, tenantID, purpose string,
	strategyRef StrategyRef,
	audits []ComparisonAudit,
) (*ComparisonAuditBatch, error) {
	owned := cloneComparisonAudits(audits)
	for index := range owned {
		owned[index].AuditID = ""
		auditID, err := DeriveComparisonAuditID(owned[index])
		if err != nil {
			return nil, err
		}
		owned[index].AuditID = auditID
	}
	batch := &ComparisonAuditBatch{
		Schema:               Schema{Name: comparisonAuditBatchSchema, Major: schemaMajor, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: partitionHashVersion,
		TenantID:             tenantID,
		Purpose:              purpose,
		StrategyRef:          strategyRef,
		Audits:               owned,
	}
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	partitionKey, err := deriveTriggerPartitionKey(
		batch.PartitionHashVersion,
		batch.TenantID,
		batch.Purpose,
		batch.StrategyRef.StrategyID,
		batch.StrategyRef.ItemID,
	)
	if err != nil {
		return nil, err
	}
	batch.partitionKey = partitionKey
	return batch, nil
}

func DeriveComparisonAuditID(audit ComparisonAudit) (string, error) {
	audit.AuditID = ""
	if err := audit.validate(false); err != nil {
		return "", err
	}
	payload, err := json.Marshal(audit)
	if err != nil {
		return "", invalid("comparison_audit.audit_id", err.Error())
	}
	digest := sha256.New()
	for _, field := range [][]byte{[]byte(comparisonAuditIDVersionV1), payload} {
		if uint64(len(field)) > math.MaxUint32 {
			return "", invalid("comparison_audit.audit_id", "canonical field exceeds uint32 length")
		}
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field)))
		_, _ = digest.Write(prefix[:])
		_, _ = digest.Write(field)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func EncodeComparisonAuditBatch(batch *ComparisonAuditBatch) ([]byte, error) {
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		return nil, invalid("comparison_audit_batch", err.Error())
	}
	if len(payload) > MaxComparisonAuditBytesV1 {
		return nil, invalid("comparison_audit_batch", "exceeds encoded byte limit")
	}
	return payload, nil
}

func DecodeComparisonAuditBatch(payload []byte) (*ComparisonAuditBatch, error) {
	if len(payload) > MaxComparisonAuditBytesV1 {
		return nil, invalid("comparison_audit_batch", "exceeds encoded byte limit")
	}
	schema, object, err := validateContractEnvelope(
		payload,
		"comparison_audit_batch",
		comparisonAuditBatchSchema,
		[]string{"schema", "required_features", "partition_hash_version", "tenant_id", "purpose", "strategy_ref", "audits"},
		nil,
	)
	if err != nil {
		return nil, err
	}
	allowUnknown := schema.Minor > 0
	if _, err := validateJSONObjectFields(
		object["strategy_ref"],
		"comparison_audit_batch.strategy_ref",
		[]string{"strategy_id", "item_id", "generation", "content_sha256"},
		nil,
		allowUnknown,
	); err != nil {
		return nil, err
	}
	var header struct {
		RequiredFeatures     []string          `json:"required_features"`
		PartitionHashVersion string            `json:"partition_hash_version"`
		TenantID             string            `json:"tenant_id"`
		Purpose              string            `json:"purpose"`
		StrategyRef          StrategyRef       `json:"strategy_ref"`
		Audits               []json.RawMessage `json:"audits"`
	}
	if err := decodeJSONObject(payload, &header); err != nil {
		return nil, err
	}
	if len(header.Audits) == 0 || len(header.Audits) > MaxComparisonAuditItemsV1 {
		return nil, invalid("comparison_audit_batch.audits", "must contain between 1 and 500 audits")
	}
	audits := make([]ComparisonAudit, 0, len(header.Audits))
	for _, rawAudit := range header.Audits {
		audit, err := decodeComparisonAudit(rawAudit, allowUnknown)
		if err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	batch := &ComparisonAuditBatch{
		Schema: schema, RequiredFeatures: header.RequiredFeatures,
		PartitionHashVersion: header.PartitionHashVersion, TenantID: header.TenantID,
		Purpose: header.Purpose, StrategyRef: header.StrategyRef, Audits: audits,
	}
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	batch.partitionKey, err = deriveTriggerPartitionKey(
		batch.PartitionHashVersion,
		batch.TenantID,
		batch.Purpose,
		batch.StrategyRef.StrategyID,
		batch.StrategyRef.ItemID,
	)
	if err != nil {
		return nil, err
	}
	return batch, nil
}

func decodeComparisonAudit(payload []byte, allowUnknown bool) (ComparisonAudit, error) {
	required := []string{
		"audit_id", "event_kind", "input_id", "record_id", "source_time", "join_status", "eligibility", "verdict",
		"source", "coverage", "source_conflict", "go_conflict", "python_conflict", "go_invalid", "python_invalid",
	}
	object, err := validateJSONObjectFields(
		payload, "comparison_audit_batch.audits", required, []string{"go_decision", "python_decision"}, allowUnknown,
	)
	if err != nil {
		return ComparisonAudit{}, err
	}
	sourceObject, err := validateJSONObjectFields(
		object["source"], "comparison_audit.source", []string{"outcome", "semantic_sha256"}, []string{"error_code"}, allowUnknown,
	)
	if err != nil {
		return ComparisonAudit{}, err
	}
	if _, err := validateJSONObjectFields(
		object["coverage"], "comparison_audit.coverage",
		[]string{"phase", "barrier_frozen", "missing_roles", "missing_at_barrier_roles", "late_roles"}, nil, allowUnknown,
	); err != nil {
		return ComparisonAudit{}, err
	}
	for _, field := range []string{"go_decision", "python_decision"} {
		raw, ok := object[field]
		if !ok {
			continue
		}
		evidence, err := validateJSONObjectFields(
			raw, "comparison_audit."+field, []string{"batch_identity", "decision", "semantic_sha256"}, nil, allowUnknown,
		)
		if err != nil {
			return ComparisonAudit{}, err
		}
		if _, err := validateJSONObjectFields(
			evidence["batch_identity"], "comparison_audit."+field+".batch_identity",
			[]string{"partition_hash_version", "tenant_id", "purpose", "strategy_ref", "decision_algorithm"}, nil, allowUnknown,
		); err != nil {
			return ComparisonAudit{}, err
		}
		var identity struct {
			StrategyRef json.RawMessage `json:"strategy_ref"`
		}
		if err := decodeJSONObject(evidence["batch_identity"], &identity); err != nil {
			return ComparisonAudit{}, err
		}
		if _, err := validateJSONObjectFields(
			identity.StrategyRef, "comparison_audit."+field+".batch_identity.strategy_ref",
			[]string{"strategy_id", "item_id", "generation", "content_sha256"}, nil, allowUnknown,
		); err != nil {
			return ComparisonAudit{}, err
		}
		decisionObject, err := validateJSONObjectFields(
			evidence["decision"], "comparison_audit."+field+".decision",
			[]string{"decision_id", "input_id", "record_id", "outcome", "reason_code", "anomaly_timestamps"},
			[]string{"level"}, allowUnknown,
		)
		if err != nil {
			return ComparisonAudit{}, err
		}
		if level, ok := decisionObject["level"]; ok && bytes.Equal(bytes.TrimSpace(level), []byte("null")) {
			return ComparisonAudit{}, invalid("comparison_audit."+field+".decision.level", "must be omitted instead of null")
		}
	}
	var audit ComparisonAudit
	if err := decodeJSONObject(payload, &audit); err != nil {
		return ComparisonAudit{}, err
	}
	if _, present := sourceObject["error_code"]; present && audit.Source.ErrorCode == "" {
		return ComparisonAudit{}, invalid("comparison_audit.source.error_code", "must be a non-empty string when present")
	}
	return audit, nil
}

func (b *ComparisonAuditBatch) Validate() error {
	if b == nil {
		return invalid("comparison_audit_batch", "must be non-null")
	}
	if b.RequiredFeatures == nil {
		return invalid("comparison_audit_batch.required_features", "must be an array")
	}
	if err := validateHeader(b.Schema, b.RequiredFeatures, comparisonAuditBatchSchema, map[string]struct{}{}); err != nil {
		return err
	}
	if b.PartitionHashVersion != PartitionHashVersionV1 {
		return invalid("comparison_audit_batch.partition_hash_version", "unsupported version")
	}
	if b.TenantID == "" || !utf8.ValidString(b.TenantID) {
		return invalid("comparison_audit_batch.tenant_id", "must be non-empty valid UTF-8")
	}
	if err := validatePurpose(b.Purpose); err != nil {
		return err
	}
	if err := validateStrategyRef(b.StrategyRef); err != nil {
		return err
	}
	if len(b.Audits) == 0 || len(b.Audits) > MaxComparisonAuditItemsV1 {
		return invalid("comparison_audit_batch.audits", "must contain between 1 and 500 audits")
	}
	inputIDs := make(map[string]struct{}, len(b.Audits))
	auditIDs := make(map[string]struct{}, len(b.Audits))
	for index := range b.Audits {
		audit := &b.Audits[index]
		if err := audit.Validate(); err != nil {
			return err
		}
		expectedInputID, err := DeriveInputID(InputIdentity{
			TenantID: b.TenantID, Purpose: b.Purpose, StrategyID: b.StrategyRef.StrategyID,
			ItemID: b.StrategyRef.ItemID, StrategyContentSHA256: b.StrategyRef.ContentSHA256, RecordID: audit.RecordID,
		})
		if err != nil {
			return err
		}
		if audit.InputID != expectedInputID {
			return invalid("comparison_audit.input_id", "does not match batch identity and record_id")
		}
		if _, exists := inputIDs[audit.InputID]; exists {
			return invalid("comparison_audit_batch.audits", "must not contain duplicate input_id")
		}
		if _, exists := auditIDs[audit.AuditID]; exists {
			return invalid("comparison_audit_batch.audits", "must not contain duplicate audit_id")
		}
		inputIDs[audit.InputID] = struct{}{}
		auditIDs[audit.AuditID] = struct{}{}
		for _, side := range []struct {
			evidence *ComparisonDecisionEvidence
			invalid  bool
		}{{audit.GoDecision, audit.GoInvalid}, {audit.PythonDecision, audit.PythonInvalid}} {
			if side.evidence == nil || side.invalid {
				continue
			}
			if !side.evidence.BatchIdentity.matchesBatch(b) {
				return invalid("comparison_audit.decision.batch_identity", "does not match authoritative batch identity")
			}
		}
	}
	return nil
}

func (b *ComparisonAuditBatch) PartitionKey() ([]byte, error) {
	if b == nil {
		return nil, invalid("comparison_audit_batch", "must be non-null")
	}
	if len(b.partitionKey) != 0 {
		return append([]byte(nil), b.partitionKey...), nil
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return deriveTriggerPartitionKey(
		b.PartitionHashVersion, b.TenantID, b.Purpose, b.StrategyRef.StrategyID, b.StrategyRef.ItemID,
	)
}

func (a *ComparisonAudit) Validate() error {
	if a == nil {
		return invalid("comparison_audit", "must be non-null")
	}
	if err := a.validate(true); err != nil {
		return err
	}
	expected, err := DeriveComparisonAuditID(*a)
	if err != nil {
		return err
	}
	if a.AuditID != expected {
		return invalid("comparison_audit.audit_id", "does not match canonical semantics")
	}
	return nil
}

func (a *ComparisonAudit) validate(requireID bool) error {
	if requireID && !sha256Pattern.MatchString(a.AuditID) {
		return invalid("comparison_audit.audit_id", "must be 64 lowercase hexadecimal characters")
	}
	if a.EventKind != ComparisonAuditEventSnapshot {
		return invalid("comparison_audit.event_kind", "unsupported event kind")
	}
	if !sha256Pattern.MatchString(a.InputID) {
		return invalid("comparison_audit.input_id", "must be 64 lowercase hexadecimal characters")
	}
	_, sourceTime, err := parseRecordID(a.RecordID)
	if err != nil {
		return err
	}
	if a.SourceTime != sourceTime {
		return invalid("comparison_audit.source_time", "must match record_id")
	}
	if err := a.Source.validate(); err != nil {
		return err
	}
	for _, evidence := range []*ComparisonDecisionEvidence{a.GoDecision, a.PythonDecision} {
		if evidence == nil {
			continue
		}
		if err := evidence.validate(a.InputID, a.RecordID); err != nil {
			return err
		}
	}
	if err := a.Coverage.validate(); err != nil {
		return err
	}
	if (a.GoConflict || a.GoInvalid) && a.GoDecision == nil {
		return invalid("comparison_audit.go_decision", "Go conflict or invalid state requires evidence")
	}
	if (a.PythonConflict || a.PythonInvalid) && a.PythonDecision == nil {
		return invalid("comparison_audit.python_decision", "Python conflict or invalid state requires evidence")
	}
	expectedMissing := make([]string, 0, 2)
	if a.GoDecision == nil {
		expectedMissing = append(expectedMissing, ComparisonRoleGo)
	}
	if a.PythonDecision == nil {
		expectedMissing = append(expectedMissing, ComparisonRolePython)
	}
	if !equalStrings(a.Coverage.MissingRoles, expectedMissing) {
		return invalid("comparison_audit.coverage.missing_roles", "does not match decision evidence")
	}
	for _, lateRole := range a.Coverage.LateRoles {
		if (lateRole == ComparisonRoleGo && a.GoDecision == nil) ||
			(lateRole == ComparisonRolePython && a.PythonDecision == nil) {
			return invalid("comparison_audit.coverage.late_roles", "late role requires decision evidence")
		}
	}
	conflict := a.SourceConflict || a.GoConflict || a.PythonConflict
	invalidDecision := a.GoInvalid || a.PythonInvalid
	expectedJoin := ""
	switch {
	case conflict:
		expectedJoin = ComparisonJoinConflict
	case invalidDecision:
		expectedJoin = ComparisonJoinInvalid
	case a.GoDecision == nil && a.PythonDecision == nil:
		expectedJoin = ComparisonJoinPendingBoth
	case a.GoDecision == nil:
		expectedJoin = ComparisonJoinPendingGo
	case a.PythonDecision == nil:
		expectedJoin = ComparisonJoinPendingPython
	default:
		expectedJoin = ComparisonJoinComplete
	}
	if a.JoinStatus != expectedJoin || a.JoinStatus == ComparisonJoinPendingInput {
		return invalid("comparison_audit.join_status", "does not match authoritative evidence")
	}
	if a.JoinStatus != ComparisonJoinComplete {
		if a.Eligibility != ComparisonEligibilityNone || a.Verdict != ComparisonVerdictNone {
			return invalid("comparison_audit", "incomplete join must not carry eligibility or verdict")
		}
		return nil
	}
	if err := a.validateCompleteOutcome(); err != nil {
		return err
	}
	return nil
}

func (a *ComparisonAudit) validateCompleteOutcome() error {
	if a.Coverage.Phase != ComparisonCoverageComplete {
		return invalid("comparison_audit.coverage.phase", "complete join requires complete coverage")
	}
	validEligibility := map[string]struct{}{
		ComparisonEligibilityEligible: {}, ComparisonEligibilityWarmup: {}, ComparisonEligibilityCoverageGap: {},
		ComparisonEligibilitySourceError: {}, ComparisonEligibilityUnsupported: {}, ComparisonEligibilityEpochUnstable: {},
	}
	if _, ok := validEligibility[a.Eligibility]; !ok {
		return invalid("comparison_audit.eligibility", "unsupported complete eligibility")
	}
	switch a.Source.Outcome {
	case OutcomeError:
		if a.Eligibility != ComparisonEligibilitySourceError {
			return invalid("comparison_audit.eligibility", "ERROR source requires SOURCE_ERROR")
		}
	case OutcomeUnsupported:
		if a.Eligibility != ComparisonEligibilityUnsupported {
			return invalid("comparison_audit.eligibility", "UNSUPPORTED source requires UNSUPPORTED")
		}
	case OutcomeNormal:
		if a.Eligibility == ComparisonEligibilityWarmup || a.Eligibility == ComparisonEligibilitySourceError || a.Eligibility == ComparisonEligibilityUnsupported {
			return invalid("comparison_audit.eligibility", "does not match NORMAL source")
		}
	case OutcomeAnomalous:
		if a.Eligibility == ComparisonEligibilitySourceError || a.Eligibility == ComparisonEligibilityUnsupported {
			return invalid("comparison_audit.eligibility", "does not match ANOMALOUS source")
		}
	}
	if a.Eligibility == ComparisonEligibilityEligible {
		if len(a.Coverage.LateRoles) != 0 {
			return invalid("comparison_audit.eligibility", "late coverage cannot be eligible")
		}
		if a.Verdict != ComparisonVerdictMatch && a.Verdict != ComparisonVerdictHardDiff {
			return invalid("comparison_audit.verdict", "eligible assessment requires a comparison verdict")
		}
		if a.GoDecision.SemanticSHA256 == a.PythonDecision.SemanticSHA256 && a.Verdict != ComparisonVerdictMatch {
			return invalid("comparison_audit.verdict", "equal decisions require MATCH")
		}
		if a.GoDecision.SemanticSHA256 != a.PythonDecision.SemanticSHA256 && a.Verdict != ComparisonVerdictHardDiff {
			return invalid("comparison_audit.verdict", "different decisions require HARD_DIFF")
		}
		return nil
	}
	if a.Verdict != ComparisonVerdictNone {
		return invalid("comparison_audit.verdict", "excluded assessment must not carry a verdict")
	}
	if a.Eligibility == ComparisonEligibilityCoverageGap && len(a.Coverage.LateRoles) == 0 {
		return invalid("comparison_audit.coverage", "coverage gap requires sticky late evidence")
	}
	if a.Eligibility == ComparisonEligibilityWarmup && len(a.Coverage.LateRoles) != 0 {
		return invalid("comparison_audit.eligibility", "sticky late coverage takes precedence over warmup")
	}
	return nil
}

func (s ComparisonSourceEvidence) validate() error {
	if !sha256Pattern.MatchString(s.SemanticSHA256) {
		return invalid("comparison_audit.source.semantic_sha256", "must be 64 lowercase hexadecimal characters")
	}
	switch s.Outcome {
	case OutcomeNormal, OutcomeAnomalous:
		if s.ErrorCode != "" {
			return invalid("comparison_audit.source.error_code", "business outcome must not carry error code")
		}
	case OutcomeError, OutcomeUnsupported:
		if _, ok := errorCodes[s.Outcome][s.ErrorCode]; !ok {
			return invalid("comparison_audit.source.error_code", "unsupported code for outcome")
		}
	default:
		return invalid("comparison_audit.source.outcome", "unsupported source outcome")
	}
	return nil
}

func (d ComparisonDecisionEvidence) validate(inputID, recordID string) error {
	evidenceBatch := &TriggerDecisionBatch{
		Schema:               Schema{Name: triggerDecisionBatchSchema, Major: schemaMajor, Minor: 0},
		RequiredFeatures:     []string{},
		PartitionHashVersion: d.BatchIdentity.PartitionHashVersion,
		BatchID:              "comparison-audit-evidence",
		TenantID:             d.BatchIdentity.TenantID,
		Purpose:              d.BatchIdentity.Purpose,
		StrategyRef:          d.BatchIdentity.StrategyRef,
		DecisionAlgorithm:    d.BatchIdentity.DecisionAlgorithm,
		Decisions:            []TriggerDecision{d.Decision},
	}
	if err := evidenceBatch.Validate(); err != nil {
		return err
	}
	if d.Decision.InputID != inputID || d.Decision.RecordID != recordID {
		return invalid("comparison_audit.decision", "does not match audited input")
	}
	if !sha256Pattern.MatchString(d.SemanticSHA256) {
		return invalid("comparison_audit.decision.semantic_sha256", "must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func (c ComparisonCoverageEvidence) validate() error {
	switch c.Phase {
	case ComparisonCoveragePending, ComparisonCoverageOverdue, ComparisonCoverageMissingAtBarrier, ComparisonCoverageComplete:
	default:
		return invalid("comparison_audit.coverage.phase", "unsupported phase")
	}
	for field, roles := range map[string][]string{
		"missing_roles": c.MissingRoles, "missing_at_barrier_roles": c.MissingAtBarrierRoles, "late_roles": c.LateRoles,
	} {
		if roles == nil {
			return invalid("comparison_audit.coverage."+field, "must be an array")
		}
		if err := validateComparisonRoles(field, roles); err != nil {
			return err
		}
	}
	if c.Phase == ComparisonCoverageComplete && len(c.MissingRoles) != 0 {
		return invalid("comparison_audit.coverage.missing_roles", "complete coverage cannot have missing roles")
	}
	if c.Phase != ComparisonCoverageComplete && len(c.MissingRoles) == 0 {
		return invalid("comparison_audit.coverage.missing_roles", "incomplete coverage requires missing roles")
	}
	if c.Phase == ComparisonCoverageMissingAtBarrier && len(c.MissingAtBarrierRoles) == 0 {
		return invalid("comparison_audit.coverage.missing_at_barrier_roles", "missing phase requires barrier evidence")
	}
	if c.Phase != ComparisonCoverageMissingAtBarrier && len(c.MissingAtBarrierRoles) != 0 {
		return invalid("comparison_audit.coverage.phase", "barrier-missing evidence requires missing-at-barrier phase")
	}
	if c.Phase == ComparisonCoveragePending && c.BarrierFrozen {
		return invalid("comparison_audit.coverage.phase", "frozen barrier cannot remain pending")
	}
	missing := make(map[string]struct{}, len(c.MissingRoles))
	for _, role := range c.MissingRoles {
		missing[role] = struct{}{}
	}
	for _, role := range c.MissingAtBarrierRoles {
		if _, ok := missing[role]; !ok {
			return invalid("comparison_audit.coverage.missing_at_barrier_roles", "must be a subset of missing roles")
		}
	}
	if !c.BarrierFrozen && (len(c.MissingAtBarrierRoles) != 0 || len(c.LateRoles) != 0) {
		return invalid("comparison_audit.coverage", "unfrozen coverage cannot have missing-at-barrier or late roles")
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateComparisonRoles(field string, roles []string) error {
	if !sort.StringsAreSorted(roles) {
		return invalid("comparison_audit.coverage."+field, "must be sorted")
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if role != ComparisonRoleGo && role != ComparisonRolePython {
			return invalid("comparison_audit.coverage."+field, "contains unsupported role")
		}
		if _, ok := seen[role]; ok {
			return invalid("comparison_audit.coverage."+field, "contains duplicate role")
		}
		seen[role] = struct{}{}
	}
	return nil
}

func (i ComparisonDecisionBatchIdentity) matchesBatch(batch *ComparisonAuditBatch) bool {
	return batch != nil &&
		i.PartitionHashVersion == batch.PartitionHashVersion &&
		i.TenantID == batch.TenantID &&
		i.Purpose == batch.Purpose &&
		i.StrategyRef == batch.StrategyRef &&
		i.DecisionAlgorithm == DecisionAlgorithmV1
}

func cloneComparisonAudits(audits []ComparisonAudit) []ComparisonAudit {
	if audits == nil {
		return nil
	}
	cloned := make([]ComparisonAudit, len(audits))
	for index := range audits {
		cloned[index] = audits[index]
		cloned[index].Coverage.MissingRoles = cloneComparisonStrings(audits[index].Coverage.MissingRoles)
		cloned[index].Coverage.MissingAtBarrierRoles = cloneComparisonStrings(audits[index].Coverage.MissingAtBarrierRoles)
		cloned[index].Coverage.LateRoles = cloneComparisonStrings(audits[index].Coverage.LateRoles)
		cloned[index].GoDecision = cloneComparisonDecisionEvidence(audits[index].GoDecision)
		cloned[index].PythonDecision = cloneComparisonDecisionEvidence(audits[index].PythonDecision)
	}
	return cloned
}

func cloneComparisonDecisionEvidence(evidence *ComparisonDecisionEvidence) *ComparisonDecisionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	cloned.Decision = evidence.Decision
	if evidence.Decision.Level != nil {
		level := *evidence.Decision.Level
		cloned.Decision.Level = &level
	}
	if evidence.Decision.AnomalyTimestamps != nil {
		cloned.Decision.AnomalyTimestamps = make([]int64, len(evidence.Decision.AnomalyTimestamps))
		copy(cloned.Decision.AnomalyTimestamps, evidence.Decision.AnomalyTimestamps)
	}
	return &cloned
}

func cloneComparisonStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
